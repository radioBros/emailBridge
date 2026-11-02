package configcode

import (
	"context"
	"fmt"
	"strings"

	"github.com/radiobros/emailbridge/internal/apikeys"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/rules"
	"github.com/radiobros/emailbridge/internal/tenancy"
	"github.com/radiobros/emailbridge/internal/webhook"
)

func (s *Service) loadLive(ctx context.Context) (*liveSnapshot, error) {
	c, ok := tenancy.CallerFrom(ctx)
	if !ok || c.OrganizationID == "" {
		return nil, errs.New(errs.CodeForbidden, "An organisation context is required.")
	}
	snap := &liveSnapshot{
		OrgID:     c.OrganizationID,
		Settings:  map[string]any{},
		Switches:  map[string]any{},
		Endpoints: map[string]liveEndpoint{},
		Routes:    map[string]liveRoute{},
		APIKeys:   map[string]liveAPIKey{},
	}
	if s.Orgs != nil {
		if org, err := s.Orgs.GetOrganization(ctx, c.OrganizationID); err == nil && org != nil {
			snap.OrgSlug = org.Slug
		}
	}

	if s.Settings != nil {
		body, err := s.Settings.Effective(ctx, c.OrganizationID)
		if err != nil {
			return nil, err
		}
		snap.SettingsV = body.Version
		for k, sv := range body.Values {
			if sv.Source == "organization" {
				snap.Settings[k] = sv.Value
			}
		}
		for k, sv := range body.Switches {
			if sv.Source == "organization" {
				snap.Switches[k] = sv.Value
			}
		}
	}

	eps, err := s.listAllEndpoints(ctx)
	if err != nil {
		return nil, err
	}
	epIDToName := map[string]string{}
	for _, ep := range eps {
		epIDToName[ep.ID] = ep.Name
		en := ep.Enabled
		spec := EndpointSpec{
			Name: ep.Name, Kind: ep.Kind, Enabled: &en, URL: ep.URL,
			Events: append([]string(nil), ep.Events...), Filter: ptrFilter(ep.Filter),
			Payload: ptrPayload(ep.Payload), Retry: ptrRetry(ep.Retry),
			Switches: ep.Switches, SinkConfig: ep.SinkConfig,
		}
		if ep.Security.SignatureMode != "" {
			spec.Security = &webhook.SecurityWrite{SignatureMode: ep.Security.SignatureMode}
		}
		du := deliveryFrom(ep.Delivery)
		spec.Delivery = &du
		snap.Endpoints[ep.Name] = liveEndpoint{
			ID: ep.ID, Version: ep.Version, Spec: spec, HasSecret: ep.SecretPreview != "",
		}
	}

	rts, err := s.listAllRoutes(ctx)
	if err != nil {
		return nil, err
	}
	for _, rt := range rts {
		en := rt.Enabled
		ord := rt.Order
		epName := epIDToName[rt.EndpointID]
		spec := RouteSpec{
			Name: rt.Name, Endpoint: epName, Enabled: &en, Filter: rt.Filter,
			Map: rt.Map, Order: &ord, Headers: rt.Headers, Payload: rt.Payload, Builder: rt.Builder,
		}
		snap.Routes[rt.Name] = liveRoute{
			ID: rt.ID, Version: rt.Version, EndpointName: epName, Spec: spec,
			HasSecret: !rt.Secret.IsZero(),
		}
	}

	keys, err := s.listAllAPIKeys(ctx)
	if err != nil {
		return nil, err
	}
	for _, k := range keys {
		if k.RevokedAt != nil {
			continue
		}
		snap.APIKeys[k.Name] = liveAPIKey{
			ID: k.ID, Version: k.Version,
			Spec: APIKeySpec{
				Name: k.Name, Scopes: append([]string(nil), k.Scopes...),
				AccountID: k.AccountID, Restrictions: k.Restrictions,
				RateLimit: k.RateLimit, ExpiresAt: k.ExpiresAt,
			},
		}
	}
	return snap, nil
}

func (s *Service) listAllEndpoints(ctx context.Context) ([]webhook.EndpointView, error) {
	if s.Webhooks == nil {
		return nil, nil
	}
	var all []webhook.EndpointView
	cursor := ""
	for {
		res, err := s.Webhooks.ListEndpoints(ctx, webhook.EndpointListFilter{Limit: 200, Cursor: cursor})
		if err != nil {
			return nil, err
		}
		all = append(all, res.Data...)
		if res.NextCursor == nil || *res.NextCursor == "" {
			break
		}
		cursor = *res.NextCursor
		if len(all) > maxPerKind {
			return nil, errs.New(errs.CodeValidationFailed, "Too many webhook endpoints to plan.")
		}
	}
	return all, nil
}

func (s *Service) listAllRoutes(ctx context.Context) ([]rules.Route, error) {
	if s.Routes == nil {
		return nil, nil
	}
	var all []rules.Route
	cursor := ""
	for {
		res, err := s.Routes.List(ctx, rules.ListFilter{Limit: 200, Cursor: cursor})
		if err != nil {
			return nil, err
		}
		all = append(all, res.Data...)
		if res.NextCursor == nil || *res.NextCursor == "" {
			break
		}
		cursor = *res.NextCursor
		if len(all) > maxPerKind {
			return nil, errs.New(errs.CodeValidationFailed, "Too many routes to plan.")
		}
	}
	return all, nil
}

func (s *Service) listAllAPIKeys(ctx context.Context) ([]apikeys.Key, error) {
	if s.APIKeys == nil {
		return nil, nil
	}
	var all []apikeys.Key
	cursor := ""
	for {
		res, err := s.APIKeys.List(ctx, apikeys.ListFilter{Limit: 200, Cursor: cursor})
		if err != nil {
			return nil, err
		}
		all = append(all, res.Data...)
		if res.NextCursor == nil || *res.NextCursor == "" {
			break
		}
		cursor = *res.NextCursor
		if len(all) > maxPerKind {
			return nil, errs.New(errs.CodeValidationFailed, "Too many API keys to plan.")
		}
	}
	return all, nil
}

func (s *Service) exportDocument(ctx context.Context) (*Document, error) {
	live, err := s.loadLive(ctx)
	if err != nil {
		return nil, err
	}
	doc := &Document{
		APIVersion: APIVersion,
		Kind:       KindOrg,
		Metadata:   Metadata{Organization: live.OrgSlug},
		Spec: Spec{
			Settings:         cloneMap(live.Settings),
			Switches:         cloneMap(live.Switches),
			Templates:        []map[string]any{},
			SMTPGateways:     []map[string]any{},
			SuppressionLists: []map[string]any{},
		},
	}
	for _, name := range sortedKeys(live.Endpoints) {
		ep := live.Endpoints[name]
		spec := ep.Spec
		spec.Secret = FormatSecretRef(secretEnvName("WEBHOOK", name))
		// strip live-only noise from export
		spec.Headers = nil
		doc.Spec.WebhookEndpoints = append(doc.Spec.WebhookEndpoints, spec)
	}
	for _, name := range sortedKeys(live.Routes) {
		rt := live.Routes[name]
		spec := rt.Spec
		if rt.HasSecret {
			spec.Secret = FormatSecretRef(secretEnvName("ROUTE", name))
		}
		doc.Spec.Routes = append(doc.Spec.Routes, spec)
	}
	for _, name := range sortedKeys(live.APIKeys) {
		doc.Spec.APIKeys = append(doc.Spec.APIKeys, live.APIKeys[name].Spec)
	}
	return doc, nil
}

func secretEnvName(prefix, resource string) string {
	up := strings.ToUpper(strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			return r
		}
		return '_'
	}, resource))
	return fmt.Sprintf("%s_%s_SECRET", prefix, up)
}

func cloneMap(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func ptrFilter(f webhook.EndpointFilter) *webhook.EndpointFilter {
	if len(f.AccountIDs) == 0 && len(f.MailboxPaths) == 0 {
		return nil
	}
	cp := f
	return &cp
}

func ptrPayload(p webhook.PayloadOptions) *webhook.PayloadOptions {
	if p.Text == "" && p.TextMax == 0 && len(p.Headers) == 0 && !p.Attachments &&
		p.AttachmentMax == 0 && !p.WebSafeHTML && !p.CalendarEvents {
		return nil
	}
	cp := p
	return &cp
}

func ptrRetry(r webhook.RetryOptions) *webhook.RetryOptions {
	if r.MaxAttempts == 0 && r.BackoffBase == "" && r.BackoffJitter == 0 && r.Timeout == "" {
		return nil
	}
	cp := r
	return &cp
}

func deliveryFrom(d webhook.DeliveryOptions) webhook.DeliveryUpdate {
	ord, mc, ct, r4, auto := d.Ordering, d.MaxConcurrency, d.ContentType, d.RetryOn4xx, d.AutoDisableAfter
	return webhook.DeliveryUpdate{
		Ordering: &ord, MaxConcurrency: &mc, ContentType: &ct, RetryOn4xx: &r4, AutoDisableAfter: &auto,
	}
}

// Orgs looks up organisation metadata for export.
type Orgs interface {
	GetOrganization(ctx context.Context, id string) (*tenancy.Organization, error)
}
