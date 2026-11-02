package configcode

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/radiobros/emailbridge/internal/apikeys"
	"github.com/radiobros/emailbridge/internal/audit"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/jobs"
	"github.com/radiobros/emailbridge/internal/rules"
	"github.com/radiobros/emailbridge/internal/tenancy"
	"github.com/radiobros/emailbridge/internal/webhook"
)

// Service plans, applies and exports organisation config.
type Service struct {
	Settings SettingsAPI
	Webhooks WebhooksAPI
	Routes   RoutesAPI
	APIKeys  APIKeysAPI
	Jobs     JobsAPI
	Orgs     Orgs
	Audit    Auditor
	Log      *slog.Logger
	// Env resolves ${secret:NAME} references from the organisation's own settings.env secrets.
	Env EnvSource

	// ResolveSecret overrides secret resolution (tests).
	ResolveSecret func(ref string) (string, error)
	// SkipFeatureGate disables the configAsCode.enabled check (unit tests).
	SkipFeatureGate bool
}

func (s *Service) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

func (s *Service) requireEnabled(ctx context.Context) error {
	if s.SkipFeatureGate || s.Settings == nil {
		return nil
	}
	c, _ := tenancy.CallerFrom(ctx)
	body, err := s.Settings.Effective(ctx, c.OrganizationID)
	if err != nil {
		return err
	}
	sv, ok := body.Switches["configAsCode.enabled"]
	if !ok {
		return errs.New(errs.CodeFeatureDisabled, "Config as code is disabled.")
	}
	on, _ := sv.Value.(bool)
	if !on {
		return errs.New(errs.CodeFeatureDisabled, "Config as code is disabled.")
	}
	return nil
}

// Export returns the live organisation as an OrganizationConfig document.
func (s *Service) Export(ctx context.Context) (*Document, error) {
	if err := tenancy.RequireRead(ctx, "settings"); err != nil {
		return nil, err
	}
	if err := s.requireEnabled(ctx); err != nil {
		return nil, err
	}
	return s.exportDocument(ctx)
}

// Plan diffs desired against live.
func (s *Service) Plan(ctx context.Context, doc Document, prune bool) (*Plan, error) {
	if err := tenancy.Require(ctx, tenancy.PermSettingsWrite); err != nil {
		return nil, err
	}
	if err := s.requireEnabled(ctx); err != nil {
		return nil, err
	}
	if doc.APIVersion == "" {
		doc.APIVersion = APIVersion
	}
	if doc.Kind == "" {
		doc.Kind = KindOrg
	}
	live, err := s.loadLive(ctx)
	if err != nil {
		return nil, err
	}
	plan, err := BuildPlan(&doc, live, prune)
	if err != nil {
		return nil, err
	}
	if err := s.checkSecretRefs(ctx, &doc); err != nil {
		return nil, err
	}
	return plan, nil
}

// Apply queues a config.apply job after verifying the plan hash against current live state.
func (s *Service) Apply(ctx context.Context, doc Document, prune bool, planHash string) (*jobs.Job, error) {
	if err := tenancy.Require(ctx, tenancy.PermSettingsWrite); err != nil {
		return nil, err
	}
	if err := s.requireEnabled(ctx); err != nil {
		return nil, err
	}
	if strings.TrimSpace(planHash) == "" {
		return nil, errs.New(errs.CodeValidationFailed, "planHash is required.")
	}
	plan, err := s.Plan(ctx, doc, prune)
	if err != nil {
		return nil, err
	}
	if plan.PlanHash != planHash {
		return nil, errs.New(errs.CodeConfigDrift, "Live configuration changed since this plan was computed.")
	}
	if s.Jobs == nil {
		return nil, errs.New(errs.CodeInternalError, "Job service is not configured.")
	}
	payload := ApplyRequest{Document: doc, Prune: prune, PlanHash: planHash}
	job, err := s.Jobs.Create(ctx, JobKind, payload)
	if err != nil {
		return nil, err
	}
	s.audit(ctx, "config.apply.queued", job.ID, audit.Changes{
		"planHash": {After: planHash},
		"actions":  {After: len(plan.Actions)},
	})
	return job, nil
}

// RegisterHandlers wires the config.apply job kind.
func (s *Service) RegisterHandlers() {
	if s.Jobs == nil {
		return
	}
	s.Jobs.Handle(JobKind, s.handleApply)
}

func (s *Service) handleApply(ctx context.Context, j *jobs.Job, r jobs.Reporter) (any, error) {
	var req ApplyRequest
	if len(j.Payload) > 0 {
		if err := json.Unmarshal(j.Payload, &req); err != nil {
			return nil, errs.New(errs.CodeValidationFailed, "Invalid apply payload.")
		}
	}
	plan, err := s.Plan(ctx, req.Document, req.Prune)
	if err != nil {
		return nil, err
	}
	if plan.PlanHash != req.PlanHash {
		return nil, errs.New(errs.CodeConfigDrift, "Live configuration changed since this plan was computed.")
	}
	result := &ApplyResult{PlanHash: plan.PlanHash, Applied: []ActionRef{}}
	total := int64(len(plan.Actions))
	r.Progress(ctx, 0, total)
	for i, a := range plan.Actions {
		if r.Cancelled() {
			return result, errs.New(errs.CodeConflict, "Config apply cancelled.")
		}
		token, err := s.applyAction(ctx, &req.Document, a)
		if err != nil {
			result.StoppedAt = &StoppedAction{Op: a.Op, Kind: a.Kind, Name: a.Name, Error: err.Error()}
			return result, err
		}
		result.Applied = append(result.Applied, ActionRef{Op: a.Op, Kind: a.Kind, Name: a.Name, ID: a.ID, Token: token})
		s.audit(ctx, fmt.Sprintf("config.apply.%s.%s", a.Kind, a.Op), a.ID,
			audit.Changes{"name": {After: a.Name}, "kind": {After: a.Kind}, "op": {After: a.Op}})
		r.Progress(ctx, int64(i+1), total)
	}
	return result, nil
}

func (s *Service) applyAction(ctx context.Context, doc *Document, a Action) (token string, err error) {
	switch a.Kind {
	case KindSettings:
		return "", s.applySettings(ctx, doc.Spec.Settings)
	case KindSwitch:
		return "", s.applySwitch(ctx, a.Name, a.Op, doc.Spec.Switches)
	case KindEndpoint:
		return "", s.applyEndpoint(ctx, a, doc)
	case KindRoute:
		return "", s.applyRoute(ctx, a, doc)
	case KindAPIKey:
		return s.applyAPIKey(ctx, a, doc)
	default:
		return "", errs.New(errs.CodeValidationFailed, "Unknown action kind.").WithSafe("kind", a.Kind)
	}
}

func (s *Service) applySettings(ctx context.Context, values map[string]any) error {
	if s.Settings == nil || len(values) == 0 {
		return nil
	}
	_, err := s.Settings.Patch(ctx, "organization", map[string]any{"values": values}, "")
	return err
}

func (s *Service) applySwitch(ctx context.Context, name, op string, switches map[string]any) error {
	if s.Settings == nil {
		return nil
	}
	if op == OpDelete {
		_, err := s.Settings.Patch(ctx, "organization", map[string]any{"switches": map[string]any{name: nil}}, "")
		return err
	}
	v, ok := switches[name]
	if !ok {
		return errs.New(errs.CodeValidationFailed, "Switch missing from document.").WithSafe("name", name)
	}
	_, err := s.Settings.Patch(ctx, "organization", map[string]any{"switches": map[string]any{name: v}}, "")
	return err
}

func (s *Service) applyEndpoint(ctx context.Context, a Action, doc *Document) error {
	if s.Webhooks == nil {
		return errs.New(errs.CodeInternalError, "Webhook service is not configured.")
	}
	switch a.Op {
	case OpDelete:
		return s.Webhooks.DeleteEndpoint(ctx, a.ID, etag(a.Version))
	case OpCreate:
		spec := findEndpoint(doc, a.Name)
		in, err := s.endpointCreate(ctx, spec)
		if err != nil {
			return err
		}
		_, err = s.Webhooks.CreateEndpoint(ctx, in)
		return err
	case OpUpdate:
		spec := findEndpoint(doc, a.Name)
		in, err := s.endpointUpdate(ctx, spec)
		if err != nil {
			return err
		}
		_, err = s.Webhooks.UpdateEndpoint(ctx, a.ID, in, etag(a.Version))
		return err
	}
	return nil
}

func (s *Service) applyRoute(ctx context.Context, a Action, doc *Document) error {
	if s.Routes == nil {
		return errs.New(errs.CodeInternalError, "Routes service is not configured.")
	}
	switch a.Op {
	case OpDelete:
		return s.Routes.Delete(ctx, a.ID, etag(a.Version))
	case OpCreate:
		spec := findRoute(doc, a.Name)
		in, err := s.routeCreate(ctx, spec)
		if err != nil {
			return err
		}
		_, err = s.Routes.Create(ctx, in)
		return err
	case OpUpdate:
		spec := findRoute(doc, a.Name)
		in, err := s.routeUpdate(ctx, spec)
		if err != nil {
			return err
		}
		_, err = s.Routes.Update(ctx, a.ID, in, etag(a.Version))
		return err
	}
	return nil
}

func (s *Service) applyAPIKey(ctx context.Context, a Action, doc *Document) (token string, err error) {
	if s.APIKeys == nil {
		return "", errs.New(errs.CodeInternalError, "API keys service is not configured.")
	}
	switch a.Op {
	case OpDelete:
		return "", s.APIKeys.Revoke(ctx, a.ID, etag(a.Version))
	case OpCreate:
		spec := findAPIKey(doc, a.Name)
		_, tok, err := s.APIKeys.Create(ctx, apikeys.CreateInput{
			Name: spec.Name, Scopes: spec.Scopes, AccountID: spec.AccountID,
			Restrictions: spec.Restrictions, RateLimit: spec.RateLimit, ExpiresAt: spec.ExpiresAt,
		})
		return tok, err
	case OpUpdate:
		spec := findAPIKey(doc, a.Name)
		scopes := append([]string(nil), spec.Scopes...)
		in := apikeys.PatchInput{
			Scopes: &scopes, Restrictions: spec.Restrictions, RateLimit: spec.RateLimit,
		}
		if acc := strings.TrimSpace(spec.AccountID); acc == "" {
			in.AccountID = apikeys.ClearAccountID()
		} else {
			in.AccountID = apikeys.SetAccountID(acc)
		}
		if spec.ExpiresAt == nil {
			in.ExpiresAt = apikeys.ClearExpiresAt()
		} else {
			in.ExpiresAt = apikeys.SetExpiresAt(*spec.ExpiresAt)
		}
		_, err := s.APIKeys.Patch(ctx, a.ID, in, etag(a.Version))
		return "", err
	}
	return "", nil
}

func (s *Service) resolve(ctx context.Context, ref string) (string, error) {
	if s.ResolveSecret != nil {
		return s.ResolveSecret(ref)
	}
	return ResolveOrgSecret(ctx, s.Env, ref)
}

// checkSecretRefs resolves every secret reference of doc and discards the values, so a plan fails early with
// a validation error naming an undefined secret. Values never leave this function.
func (s *Service) checkSecretRefs(ctx context.Context, doc *Document) error {
	for _, ep := range doc.Spec.WebhookEndpoints {
		if ep.Secret != "" {
			if _, err := s.resolve(ctx, ep.Secret); err != nil {
				return err
			}
		}
		for _, h := range ep.Headers {
			if h.Secret && h.Value != "" {
				if _, err := s.resolve(ctx, h.Value); err != nil {
					return err
				}
			}
		}
	}
	for _, rt := range doc.Spec.Routes {
		if rt.Secret != "" {
			if _, err := s.resolve(ctx, rt.Secret); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Service) endpointCreate(ctx context.Context, spec EndpointSpec) (webhook.EndpointCreate, error) {
	in := webhook.EndpointCreate{
		Name: spec.Name, Kind: spec.Kind, Enabled: spec.Enabled, URL: spec.URL,
		Events: append([]string(nil), spec.Events...), SinkConfig: spec.SinkConfig,
		Switches: spec.Switches, Headers: spec.Headers,
	}
	if spec.Filter != nil {
		in.Filter = *spec.Filter
	}
	if spec.Payload != nil {
		in.Payload = *spec.Payload
	}
	if spec.Retry != nil {
		in.Retry = *spec.Retry
	}
	if spec.Delivery != nil {
		in.Delivery = spec.Delivery
	}
	if spec.Security != nil {
		in.Security = *spec.Security
	}
	if spec.Secret != "" {
		v, err := s.resolve(ctx, spec.Secret)
		if err != nil {
			return in, err
		}
		in.Secret = v
	}
	for i := range in.Headers {
		if in.Headers[i].Secret && in.Headers[i].Value != "" {
			v, err := s.resolve(ctx, in.Headers[i].Value)
			if err != nil {
				return in, err
			}
			in.Headers[i].Value = v
		}
	}
	return in, nil
}

func (s *Service) endpointUpdate(ctx context.Context, spec EndpointSpec) (webhook.EndpointUpdate, error) {
	in := webhook.EndpointUpdate{
		Name: strPtr(spec.Name), Enabled: spec.Enabled, URL: strPtr(spec.URL),
		Events: &spec.Events, Filter: spec.Filter, Payload: spec.Payload,
		Retry: spec.Retry, Delivery: spec.Delivery, Switches: &spec.Switches,
	}
	if spec.Security != nil {
		in.Security = spec.Security
	}
	if spec.Secret != "" {
		v, err := s.resolve(ctx, spec.Secret)
		if err != nil {
			return in, err
		}
		in.Secret = &v
	}
	if spec.SinkConfig != nil {
		in.SinkConfig = &spec.SinkConfig
	}
	if len(spec.Headers) > 0 {
		headers := append([]webhook.WebhookHeader(nil), spec.Headers...)
		for i := range headers {
			if headers[i].Secret && headers[i].Value != "" {
				v, err := s.resolve(ctx, headers[i].Value)
				if err != nil {
					return in, err
				}
				headers[i].Value = v
			}
		}
		in.Headers = &headers
	}
	return in, nil
}

func (s *Service) routeCreate(ctx context.Context, spec RouteSpec) (rules.RouteCreate, error) {
	epID, err := s.endpointIDByName(ctx, spec.Endpoint)
	if err != nil {
		return rules.RouteCreate{}, err
	}
	in := rules.RouteCreate{
		Name: spec.Name, Enabled: spec.Enabled, EndpointID: epID,
		Filter: spec.Filter, Map: spec.Map, Builder: spec.Builder,
		Headers: spec.Headers, Payload: spec.Payload,
	}
	if spec.Order != nil {
		in.Order = *spec.Order
	}
	if spec.Secret != "" {
		v, err := s.resolve(ctx, spec.Secret)
		if err != nil {
			return in, err
		}
		in.Secret = v
	}
	return in, nil
}

func (s *Service) routeUpdate(ctx context.Context, spec RouteSpec) (rules.RouteUpdate, error) {
	epID, err := s.endpointIDByName(ctx, spec.Endpoint)
	if err != nil {
		return rules.RouteUpdate{}, err
	}
	in := rules.RouteUpdate{
		Name: strPtr(spec.Name), Enabled: spec.Enabled, EndpointID: &epID,
		Filter: strPtr(spec.Filter), Map: strPtr(spec.Map),
		Builder: &spec.Builder, Headers: &spec.Headers, Payload: &spec.Payload, Order: spec.Order,
	}
	if spec.Secret != "" {
		v, err := s.resolve(ctx, spec.Secret)
		if err != nil {
			return in, err
		}
		in.Secret = &v
	}
	return in, nil
}

func (s *Service) endpointIDByName(ctx context.Context, name string) (string, error) {
	eps, err := s.listAllEndpoints(ctx)
	if err != nil {
		return "", err
	}
	for _, ep := range eps {
		if ep.Name == name {
			return ep.ID, nil
		}
	}
	return "", errs.New(errs.CodeNotFound, "Webhook endpoint not found.").WithSafe("name", name)
}

func (s *Service) audit(ctx context.Context, action, id string, changes audit.Changes) {
	if s.Audit == nil {
		return
	}
	if err := s.Audit.Record(ctx, action, audit.Target{Type: "config", ID: id}, changes); err != nil {
		s.log().WarnContext(ctx, "config audit write failed", "action", action, "err", err)
	}
}

func findEndpoint(doc *Document, name string) EndpointSpec {
	for _, e := range doc.Spec.WebhookEndpoints {
		if e.Name == name {
			return e
		}
	}
	return EndpointSpec{Name: name}
}

func findRoute(doc *Document, name string) RouteSpec {
	for _, r := range doc.Spec.Routes {
		if r.Name == name {
			return r
		}
	}
	return RouteSpec{Name: name}
}

func findAPIKey(doc *Document, name string) APIKeySpec {
	for _, k := range doc.Spec.APIKeys {
		if k.Name == name {
			return k
		}
	}
	return APIKeySpec{Name: name}
}

func etag(v int64) string {
	if v <= 0 {
		return ""
	}
	return fmt.Sprintf(`"v%d"`, v)
}

func strPtr(s string) *string { return &s }
