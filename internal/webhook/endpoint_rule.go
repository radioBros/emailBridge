package webhook

import (
	"context"
	"errors"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/radiobros/emailbridge/internal/audit"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/jobs"
	"github.com/radiobros/emailbridge/internal/rules"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

const (
	defaultRuleVersionLimit = 50
	maxRuleVersionLimit     = 200
)

// EndpointRuleVersion is one row of endpointRuleVersions (W73 §1): the rule as it was at that version.
type EndpointRuleVersion struct {
	ID             string         `bson:"_id" json:"id" doc:"Endpoint rule version ID (erv_…)" example:"erv_01m5dxhnv019njbyyapkhp5rev"`
	OrganizationID string         `bson:"organizationId" json:"organizationId" doc:"Organisation ID (org_…)" example:"org_01m5caqpk0kast63yc13wpy2d9"`
	EndpointID     string         `bson:"endpointId" json:"endpointId" doc:"Endpoint ID (whe_…)" example:"whe_01m5cshyc0d23xkhrbz7e490pm"`
	Version        int            `bson:"version" json:"version" doc:"Version number, from 1" example:"2"`
	Filter         string         `bson:"filter" json:"filter" doc:"Filter of this version" example:"type == \"message.received\" && fromDomain() == \"example.org\""`
	Map            string         `bson:"map,omitempty" json:"map,omitempty" doc:"Map of this version" example:"{\"messageId\": data.messageId}"`
	Builder        map[string]any `bson:"builder,omitempty" json:"builder,omitempty" doc:"Builder model of this version"`
	CreatedBy      string         `bson:"createdBy,omitempty" json:"createdBy,omitempty" doc:"User or API key that saved it" example:"usr_01m5ccvej0dqefkn0xhkcq2q6z"`
	CreatedAt      time.Time      `bson:"createdAt" json:"createdAt" doc:"When it was saved" example:"2026-10-26T14:02:51Z"`
}

// EndpointRuleVersionList is GET …/rule/versions.
type EndpointRuleVersionList struct {
	Data  []EndpointRuleVersion `json:"data" doc:"Versions, newest first."`
	Total *int64                `json:"total,omitempty" doc:"Total number of versions (only with includeTotal)." example:"2"`
}

// ruleProgram returns the effective filter/map/version of an endpoint rule ("true" when only a map is set).
func ruleProgram(r *EndpointRule) (filter, mapExpr string, version int) {
	filter = r.Filter
	if strings.TrimSpace(filter) == "" {
		filter = "true"
	}
	version = r.CurrentVersion
	if version <= 0 {
		version = 1
	}
	return filter, r.Map, version
}

// DryRunEndpointRule queues a rules.dryRun job for the endpoint rule (or for the filter/map/builder in the
// request, to try a draft before saving). Same limits and result as the route dry-run; never delivers.
func (s *Service) DryRunEndpointRule(ctx context.Context, id string, req rules.DryRunRequest) (*jobs.Job, error) {
	if err := tenancy.RequireOn(ctx, tenancy.PermWebhooksWrite, id); err != nil {
		return nil, err
	}
	ep, err := s.Repo.endpoint(ctx, id)
	if err != nil {
		return nil, err
	}
	if s.Rules == nil {
		return nil, errs.New(errs.CodeInternalError, "Rules engine is not configured.")
	}
	draft := req.Filter != "" || req.Map != "" || req.Builder != nil
	if !ep.Rule.HasCEL() && !draft {
		return nil, errs.New(errs.CodeValidationFailed, "The endpoint has no filter or map rule; send one in the request to try a draft.")
	}
	filter, mapExpr := "", ""
	if ep.Rule.HasCEL() {
		filter, mapExpr, _ = ruleProgram(ep.Rule)
	}
	return s.Rules.StartEndpointRuleDryRun(ctx, ep.ID, filter, mapExpr, req)
}

// ListEndpointRuleVersions returns the endpoint's rule versions, newest first.
func (s *Service) ListEndpointRuleVersions(ctx context.Context, id string, limit, page int, includeTotal bool) (*EndpointRuleVersionList, error) {
	if err := tenancy.RequireReadOn(ctx, "webhooks", id); err != nil {
		return nil, err
	}
	if _, err := s.Repo.endpoint(ctx, id); err != nil {
		return nil, err
	}
	base, err := store.Scoped(ctx, bson.M{"endpointId": id})
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = defaultRuleVersionLimit
	}
	limit = min(limit, maxRuleVersionLimit)
	page = max(page, 1)
	cur, err := s.Repo.DB.Collection(endpointRuleVersionsCollection).Find(ctx, base, options.Find().
		SetSort(bson.D{{Key: "version", Value: -1}}).
		SetSkip(int64(page-1)*int64(limit)).SetLimit(int64(limit)))
	if err != nil {
		return nil, err
	}
	rows := []EndpointRuleVersion{}
	if err := cur.All(ctx, &rows); err != nil {
		return nil, err
	}
	out := &EndpointRuleVersionList{Data: rows}
	if includeTotal {
		n, err := s.Repo.DB.Collection(endpointRuleVersionsCollection).CountDocuments(ctx, base)
		if err != nil {
			return nil, err
		}
		out.Total = &n
	}
	return out, nil
}

// RestoreEndpointRuleVersion copies an old rule version onto the endpoint as a new current version
// (If-Match on the endpoint, compiled before saving, audited as endpoint.rule.restore). Restoring a version
// whose content equals the current rule changes nothing.
func (s *Service) RestoreEndpointRuleVersion(ctx context.Context, id string, version int, ifMatch string) (*Endpoint, error) {
	if err := tenancy.RequireOn(ctx, tenancy.PermWebhooksWrite, id); err != nil {
		return nil, err
	}
	if version < 1 {
		return nil, errs.New(errs.CodeValidationFailed, "version must be >= 1")
	}
	ep, err := s.Repo.endpoint(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := checkETag(ifMatch, ep.Version); err != nil {
		return nil, err
	}
	filter, err := store.Scoped(ctx, bson.M{"endpointId": id, "version": version})
	if err != nil {
		return nil, err
	}
	var row EndpointRuleVersion
	err = s.Repo.DB.Collection(endpointRuleVersionsCollection).FindOne(ctx, filter).Decode(&row)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, errs.New(errs.CodeNotFound, "Endpoint rule version not found.")
	}
	if err != nil {
		return nil, err
	}
	if ifMatch == "" {
		ifMatch = formatETag(ep.Version)
	}
	before := 0
	if ep.Rule != nil {
		before = ep.Rule.CurrentVersion
	}
	updated, err := s.UpdateEndpoint(ctx, id, EndpointUpdate{
		Rule: &EndpointRule{Filter: row.Filter, Map: row.Map, Builder: row.Builder},
	}, ifMatch)
	if err != nil {
		return nil, err
	}
	after := 0
	if updated.Rule != nil {
		after = updated.Rule.CurrentVersion
	}
	s.audit(ctx, "endpoint.rule.restore", id, audit.Changes{
		"restoredFrom": {After: version}, "version": {Before: before, After: after},
	})
	return updated, nil
}
