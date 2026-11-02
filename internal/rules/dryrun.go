package rules

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/radiobros/emailbridge/internal/audit"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/jobs"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

// dryRunEventDoc is the events collection projection used by rules.dryRun.
type dryRunEventDoc struct {
	ID        string    `bson:"_id"`
	AccountID string    `bson:"accountId,omitempty"`
	Type      string    `bson:"type"`
	Time      time.Time `bson:"time"`
	Subject   string    `bson:"subject"`
	Data      string    `bson:"data"`
	Schema    int       `bson:"schema"`
}

const (
	JobKindDryRun       = "rules.dryRun"
	defaultDryRunWindow = 24
	maxDryRunWindow     = 24
	defaultDryRunLimit  = 1000
	maxDryRunLimit      = 1000
	maxDryRunSamples    = 20
	eventsCollection    = "events"
)

// DryRunRequest is POST …/dry-run body.
type DryRunRequest struct {
	WindowHours int            `json:"windowHours,omitempty" doc:"How far back to scan stored events, in hours (default and at most 24)" example:"24"`
	Limit       int            `json:"limit,omitempty" doc:"Maximum events to scan (default and at most 1000)" example:"500"`
	Filter      string         `json:"filter,omitempty" doc:"Draft filter to try instead of the stored one" example:"type == \"message.received\" && hasAttachments()"`
	Map         string         `json:"map,omitempty" doc:"Draft map to try instead of the stored one" example:"{\"messageId\": data.messageId}"`
	Builder     map[string]any `json:"builder,omitempty" doc:"Draft builder model to try"`
}

// DryRunSample is one matched mapped output in the job result.
type DryRunSample struct {
	EventID string `json:"eventId"`
	Mapped  any    `json:"mapped,omitempty"`
}

// DryRunCostStats summarises CEL cost across the scan.
type DryRunCostStats struct {
	Total uint64  `json:"total"`
	Max   uint64  `json:"max"`
	Avg   float64 `json:"avg"`
}

// DryRunResult is the succeeded job result.
type DryRunResult struct {
	Matched       int             `json:"matched"`
	Scanned       int             `json:"scanned"`
	Samples       []DryRunSample  `json:"samples"`
	ErrorsByPhase map[string]int  `json:"errorsByPhase"`
	Cost          DryRunCostStats `json:"cost"`
	WindowHours   int             `json:"windowHours"`
	Limit         int             `json:"limit"`
}

type dryRunPayload struct {
	RouteID     string `json:"routeId,omitempty"`
	EndpointID  string `json:"endpointId,omitempty"` // W73 endpoint rule dry-run
	WindowHours int    `json:"windowHours"`
	Limit       int    `json:"limit"`
	Filter      string `json:"filter"`
	Map         string `json:"map"`
}

// StartDryRun queues a rules.dryRun job. Never delivers.
func (r *Repo) StartDryRun(ctx context.Context, routeID string, req DryRunRequest) (*jobs.Job, error) {
	if err := tenancy.Require(ctx, tenancy.PermWebhooksWrite); err != nil {
		return nil, err
	}
	if r.Jobs == nil {
		return nil, errs.New(errs.CodeInternalError, "Job service is not configured.")
	}
	route, err := r.Get(ctx, routeID)
	if err != nil {
		return nil, err
	}
	return r.startDryRun(ctx, dryRunPayload{RouteID: routeID}, route.Filter, route.Map, req,
		"route.dryRun", audit.Target{Type: "route", ID: routeID})
}

// StartEndpointRuleDryRun queues a rules.dryRun job for an endpoint CEL rule (W73). The caller (the webhook
// service) has already loaded the endpoint in the caller's organisation. Never delivers.
func (r *Repo) StartEndpointRuleDryRun(ctx context.Context, endpointID, filter, mapExpr string, req DryRunRequest) (*jobs.Job, error) {
	if err := tenancy.Require(ctx, tenancy.PermWebhooksWrite); err != nil {
		return nil, err
	}
	if r.Jobs == nil {
		return nil, errs.New(errs.CodeInternalError, "Job service is not configured.")
	}
	return r.startDryRun(ctx, dryRunPayload{EndpointID: endpointID}, filter, mapExpr, req,
		"endpoint.rule.dryRun", audit.Target{Type: "webhookEndpoint", ID: endpointID})
}

func (r *Repo) startDryRun(ctx context.Context, payload dryRunPayload, filter, mapExpr string, req DryRunRequest,
	auditAction string, target audit.Target) (*jobs.Job, error) {
	window := req.WindowHours
	if window <= 0 {
		window = defaultDryRunWindow
	}
	if window > maxDryRunWindow {
		return nil, errs.New(errs.CodeValidationFailed, "windowHours must be between 1 and 24")
	}
	limit := req.Limit
	if limit <= 0 {
		limit = defaultDryRunLimit
	}
	if limit > maxDryRunLimit {
		return nil, errs.New(errs.CodeValidationFailed, "limit must be between 1 and 1000")
	}
	if req.Filter != "" {
		filter = req.Filter
	}
	if req.Map != "" {
		mapExpr = req.Map
	}
	if req.Builder != nil {
		b, err := BuilderFromMap(req.Builder)
		if err != nil {
			return nil, err
		}
		cel, err := BuilderToCEL(b)
		if err != nil {
			return nil, err
		}
		filter = cel
	}
	if strings.TrimSpace(filter) == "" {
		filter = "true"
	}
	if _, err := r.Engine.Compile(filter, mapExpr); err != nil {
		return nil, err
	}
	payload.WindowHours, payload.Limit, payload.Filter, payload.Map = window, limit, filter, mapExpr
	job, err := r.Jobs.Create(ctx, JobKindDryRun, payload)
	if err != nil {
		return nil, err
	}
	if r.Audit != nil {
		_ = r.Audit.Record(ctx, auditAction, target, audit.Changes{
			"jobId":       {After: job.ID},
			"windowHours": {After: window},
			"limit":       {After: limit},
		})
	}
	return job, nil
}

// RegisterHandlers wires rules.dryRun on the jobs service.
func (r *Repo) RegisterHandlers() {
	if r.Jobs == nil {
		return
	}
	r.Jobs.Handle(JobKindDryRun, r.handleDryRun)
}

func (r *Repo) handleDryRun(ctx context.Context, j *jobs.Job, rep jobs.Reporter) (any, error) {
	var p dryRunPayload
	if len(j.Payload) > 0 {
		if err := json.Unmarshal(j.Payload, &p); err != nil {
			return nil, errs.New(errs.CodeValidationFailed, "Invalid rules.dryRun payload.")
		}
	}
	org := j.OrganizationID
	if org == "" {
		return nil, errs.New(errs.CodeValidationFailed, "rules.dryRun requires an organisation.")
	}
	ctx = store.WithOrganization(ctx, org)

	prg, err := r.Engine.Compile(p.Filter, p.Map)
	if err != nil {
		return nil, err
	}

	since := time.Now().UTC().Add(-time.Duration(p.WindowHours) * time.Hour)
	filter := bson.M{
		"organizationId": org,
		"time":           bson.M{"$gte": since},
	}
	opts := options.Find().
		SetSort(bson.D{{Key: "time", Value: -1}, {Key: "_id", Value: -1}}).
		SetLimit(int64(p.Limit))
	cur, err := r.DB.Collection(eventsCollection).Find(ctx, filter, opts)
	if err != nil {
		return nil, err
	}
	defer func() { _ = cur.Close(ctx) }()

	var docs []dryRunEventDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, err
	}

	env := map[string]string{}
	var secretKeys []string
	if r.EnvLoader != nil {
		env, secretKeys, _ = r.EnvLoader(ctx, org)
	}
	secrets := SecretValuesFromEnv(env, secretKeys)
	var lists map[string]map[string]struct{}
	if r.Lists != nil {
		if lists, err = r.Lists.Snapshot(ctx, org); err != nil {
			return nil, err
		}
	}

	out := DryRunResult{
		Samples:       []DryRunSample{},
		ErrorsByPhase: map[string]int{},
		WindowHours:   p.WindowHours,
		Limit:         p.Limit,
	}
	total := int64(len(docs))
	rep.Progress(ctx, 0, total)

	for i, d := range docs {
		if rep.Cancelled() {
			return out, errs.New(errs.CodeConflict, "Dry run cancelled.")
		}
		event := eventDocToMap(d, org)
		account := map[string]any{}
		if r.AccountLoader != nil && d.AccountID != "" {
			if a, err := r.AccountLoader(ctx, org, d.AccountID); err == nil && a != nil {
				account = a
			}
		}
		matched, mapped, cost, evalErr := r.Engine.EvalWithLists(ctx, prg, event, account, env, lists)
		out.Scanned++
		out.Cost.Total += cost
		if cost > out.Cost.Max {
			out.Cost.Max = cost
		}
		if evalErr != nil {
			phase := "filter"
			code := errs.CodeOf(evalErr)
			switch code {
			case errs.CodeRouteCompileError:
				phase = "compile"
			case errs.CodeRouteEvalError:
				phase = "map"
			}
			out.ErrorsByPhase[phase]++
		} else if matched {
			out.Matched++
			if len(out.Samples) < maxDryRunSamples {
				out.Samples = append(out.Samples, DryRunSample{
					EventID: d.ID,
					Mapped:  MaskSecrets(mapped, secrets),
				})
			}
		}
		rep.Progress(ctx, int64(i+1), total)
	}
	if out.Scanned > 0 {
		out.Cost.Avg = float64(out.Cost.Total) / float64(out.Scanned)
	}
	return out, nil
}

func eventDocToMap(d dryRunEventDoc, org string) map[string]any {
	var data any = map[string]any{}
	if d.Data != "" {
		var m map[string]any
		if json.Unmarshal([]byte(d.Data), &m) == nil {
			data = m
		}
	}
	src := "/organizations/" + org
	if d.AccountID != "" {
		src += "/accounts/" + d.AccountID
	}
	return map[string]any{
		"specversion":     "1.0",
		"id":              d.ID,
		"type":            d.Type,
		"source":          src,
		"subject":         d.Subject,
		"time":            d.Time.UTC().Format(time.RFC3339Nano),
		"datacontenttype": "application/json",
		"eborg":           org,
		"ebaccount":       d.AccountID,
		"ebschema":        d.Schema,
		"data":            data,
	}
}
