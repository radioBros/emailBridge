package main

import (
	"context"
	"fmt"
	"io"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/attachments"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/gateways"
	"github.com/radiobros/emailbridge/internal/integrations"
	"github.com/radiobros/emailbridge/internal/rules"
	"github.com/radiobros/emailbridge/internal/settings"
	"github.com/radiobros/emailbridge/internal/sinks"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/submit"
	"github.com/radiobros/emailbridge/internal/tenancy"
	"github.com/radiobros/emailbridge/internal/webhook"
)

// connectIntegrations wires webhook, routes and settings adapters onto the integrations module
// before it starts (P8.1). Call before the integrations.Module in the modules list. It runs on API nodes (install,
// catalogue) and on webhook nodes, where kind=integration deliveries call the connector actions (W52).
func connectIntegrations(m *integrations.Module) app.Module {
	return &hook{name: "wire.integrations", roles: []app.Role{app.RoleAPI, app.RoleWebhook}, start: func(_ context.Context, a *app.App) error {
		wh, err := app.MustGet[*webhook.Service](a)
		if err != nil {
			return err
		}
		wh.IntegrationAction = func(ctx context.Context, orgID, instanceID string, msg sinks.Message) error {
			return m.Service().RunAction(ctx, orgID, instanceID, msg)
		}
		m.Attachments = attachmentBytes{a: a}
		// Install / catalogue adapters: present on API nodes.
		if rr, ok := app.Get[*rules.Repo](a); ok && rr != nil {
			m.Routes = &rulesRouteAdapter{repo: rr}
		}
		if sts, ok := app.Get[*settings.Service](a); ok && sts != nil {
			m.Settings = &settingsAdapter{svc: sts}
		}
		m.Endpoints = &webhookEndpointAdapter{svc: wh}
		if gw, ok := app.Get[*gateways.Service](a); ok && gw != nil {
			m.Gateways = &gatewayAdapter{svc: gw}
		}
		if sub, ok := app.Get[*submit.Service](a); ok && sub != nil {
			m.Submit = &submitAdapter{svc: sub}
		}
		return nil
	}}
}

// attachmentBytes reads attachment content for storage connector uploads (W52) from the attachments service:
// the S3 copy, the on-demand fetcher or the raw archive. Infected attachments are never handed out.
type attachmentBytes struct{ a *app.App }

func (r attachmentBytes) GetAttachment(ctx context.Context, accountID, messageID, attachmentID string, maxBytes int64) ([]byte, string, error) {
	svc, ok := app.Get[*attachments.Service](r.a)
	if !ok || svc == nil {
		return nil, "", errs.New(errs.CodeAttachmentNotFound, "The attachment content is not available.")
	}
	// A delivery has no signed-in caller: read as the system on behalf of the action's organisation (set by
	// RunAction); the attachment store and the on-demand IMAP fetch stay tenant-scoped to it.
	org, ok := store.OrganizationFrom(ctx)
	if !ok {
		return nil, "", store.ErrNoTenant
	}
	ctx = tenancy.WithCaller(ctx, tenancy.Caller{Kind: tenancy.CallerSystem, OrganizationID: org})
	doc, rc, err := svc.Open(ctx, accountID, messageID, attachmentID)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = rc.Close() }()
	if doc.Infected {
		return nil, "", errs.New(errs.CodeAttachmentNotFound, "The attachment is infected and is not handed out.")
	}
	if maxBytes <= 0 {
		maxBytes = 25 << 20
	}
	b, err := io.ReadAll(io.LimitReader(rc, maxBytes+1))
	if err != nil {
		return nil, "", errs.Wrap(err, errs.CodeInternalError, "integrations.attachment")
	}
	if int64(len(b)) > maxBytes {
		return nil, "", errs.New(errs.CodeValidationFailed, "The attachment is larger than the upload limit.")
	}
	return b, doc.ContentType, nil
}

func ifMatchETag(version int64) string {
	if version <= 0 {
		return ""
	}
	return fmt.Sprintf(`"v%d"`, version)
}

type webhookEndpointAdapter struct{ svc *webhook.Service }

func (a *webhookEndpointAdapter) Create(ctx context.Context, spec integrations.EndpointSpec) (*integrations.CreatedEndpoint, error) {
	payload := webhook.PayloadOptions{Attachments: spec.PayloadAttachments}
	if spec.PayloadText {
		payload.Text = "plain"
	}
	ep, err := a.svc.CreateEndpoint(ctx, webhook.EndpointCreate{
		Name: spec.Name, URL: spec.URL, Kind: spec.Kind,
		Events: append([]string(nil), spec.Events...), SinkConfig: spec.SinkConfig, Payload: payload,
	})
	if err != nil {
		return nil, err
	}
	return &integrations.CreatedEndpoint{ID: ep.ID, Version: ep.Version}, nil
}

func (a *webhookEndpointAdapter) UpdateURL(ctx context.Context, id, url string, version int64) (int64, error) {
	ep, err := a.svc.UpdateEndpoint(ctx, id, webhook.EndpointUpdate{URL: &url}, ifMatchETag(version))
	if err != nil {
		return 0, err
	}
	return ep.Version, nil
}

func (a *webhookEndpointAdapter) SetEnabled(ctx context.Context, id string, enabled bool, version int64) (int64, error) {
	ep, err := a.svc.UpdateEndpoint(ctx, id, webhook.EndpointUpdate{Enabled: &enabled}, ifMatchETag(version))
	if err != nil {
		return 0, err
	}
	return ep.Version, nil
}

func (a *webhookEndpointAdapter) Delete(ctx context.Context, id string, version int64) error {
	return a.svc.DeleteEndpoint(ctx, id, ifMatchETag(version))
}

type rulesRouteAdapter struct{ repo *rules.Repo }

func (a *rulesRouteAdapter) Create(ctx context.Context, spec integrations.RouteSpec) (*integrations.CreatedRoute, error) {
	rt, err := a.repo.Create(ctx, rules.RouteCreate{
		Name: spec.Name, EndpointID: spec.EndpointID, Filter: spec.Filter, Map: spec.Map,
	})
	if err != nil {
		return nil, err
	}
	return &integrations.CreatedRoute{ID: rt.ID, Version: rt.Version}, nil
}

func (a *rulesRouteAdapter) SetEnabled(ctx context.Context, id string, enabled bool, version int64) (int64, error) {
	rt, err := a.repo.Update(ctx, id, rules.RouteUpdate{Enabled: &enabled}, ifMatchETag(version))
	if err != nil {
		return 0, err
	}
	return rt.Version, nil
}

func (a *rulesRouteAdapter) Delete(ctx context.Context, id string, version int64) error {
	return a.repo.Delete(ctx, id, ifMatchETag(version))
}

type settingsAdapter struct{ svc *settings.Service }

func (a *settingsAdapter) Apply(ctx context.Context, values map[string]string) error {
	vals := make(map[string]any, len(values))
	for k, v := range values {
		vals[k] = v
	}
	_, err := a.svc.Patch(ctx, "organization", map[string]any{"values": vals}, "")
	return err
}

func (a *settingsAdapter) Clear(ctx context.Context, keys []string) error {
	vals := make(map[string]any, len(keys))
	for _, k := range keys {
		vals[k] = nil
	}
	_, err := a.svc.Patch(ctx, "organization", map[string]any{"values": vals}, "")
	return err
}

func (a *settingsAdapter) SetSwitch(ctx context.Context, name string, on bool) error {
	_, err := a.svc.Patch(ctx, "organization", map[string]any{
		"switches": map[string]any{name: map[string]any{"value": on}},
	}, "")
	return err
}

type gatewayAdapter struct{ svc *gateways.Service }

func (a *gatewayAdapter) Create(ctx context.Context, spec integrations.GatewaySpec) (*integrations.CreatedGateway, error) {
	in := gateways.CreateInput{
		Name: spec.Name, Kind: spec.Kind,
		API: &gateways.APIConfig{Region: spec.Region, Domain: spec.Domain, BaseURL: spec.BaseURL, From: spec.From},
		Credentials: &gateways.CredentialsInput{
			AccessKey: spec.AccessKey, SecretKey: spec.SecretKey, APIKey: spec.APIKey,
		},
	}
	g, err := a.svc.CreateFromIntegration(ctx, in)
	if err != nil {
		return nil, err
	}
	return &integrations.CreatedGateway{ID: g.ID}, nil
}

func (a *gatewayAdapter) Delete(ctx context.Context, id string) error {
	return a.svc.DeleteFromIntegration(ctx, id)
}

func (a *gatewayAdapter) SetEnabled(ctx context.Context, id string, enabled bool) error {
	return a.svc.SetEnabledFromIntegration(ctx, id, enabled)
}

func (a *gatewayAdapter) Update(ctx context.Context, id string, spec integrations.GatewaySpec) error {
	return a.svc.UpdateFromIntegration(ctx, id, gateways.CreateInput{
		Name: spec.Name, Kind: spec.Kind,
		API: &gateways.APIConfig{Region: spec.Region, Domain: spec.Domain, BaseURL: spec.BaseURL, From: spec.From},
		Credentials: &gateways.CredentialsInput{
			AccessKey: spec.AccessKey, SecretKey: spec.SecretKey, APIKey: spec.APIKey,
		},
	})
}

// submitAdapter sends helpdesk replies (FeatherDesk callback, P8.1) through the normal submit path, so send
// policy, suppression, throttling and the outbox apply to them like to any API send.
type submitAdapter struct{ svc *submit.Service }

func (a *submitAdapter) SubmitOutbound(ctx context.Context, accountID string, to []string, subject, text string, headers map[string]string, key string) error {
	req := submit.SubmitRequest{Subject: subject, Text: text, Headers: headers}
	for _, addr := range to {
		req.To = append(req.To, submit.AddressInput{Address: addr})
	}
	_, err := a.svc.Submit(ctx, accountID, req, key, false)
	return err
}
