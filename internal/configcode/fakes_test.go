package configcode

import (
	"context"
	"sync"

	"github.com/radiobros/emailbridge/internal/apikeys"
	"github.com/radiobros/emailbridge/internal/audit"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/jobs"
	"github.com/radiobros/emailbridge/internal/rules"
	"github.com/radiobros/emailbridge/internal/settings"
	"github.com/radiobros/emailbridge/internal/tenancy"
	"github.com/radiobros/emailbridge/internal/webhook"
)

type fakeSettings struct {
	mu       sync.Mutex
	body     settings.SettingsBody
	patched  []map[string]any
	effErr   error
	patchErr error
}

func (f *fakeSettings) Effective(context.Context, string) (settings.SettingsBody, error) {
	if f.effErr != nil {
		return settings.SettingsBody{}, f.effErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.body, nil
}

func (f *fakeSettings) Patch(_ context.Context, _ string, patch map[string]any, _ string) (settings.SettingsBody, error) {
	if f.patchErr != nil {
		return settings.SettingsBody{}, f.patchErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.patched = append(f.patched, patch)
	return f.body, nil
}

type fakeWebhooks struct {
	mu        sync.Mutex
	endpoints []webhook.EndpointView
	created   []webhook.EndpointCreate
	updated   []webhook.EndpointUpdate
	deleted   []string
	listErr   error
	createErr error
	updateErr error
	deleteErr error
	pages     int // when >0, first List returns a cursor then empties
}

func (f *fakeWebhooks) ListEndpoints(_ context.Context, filter webhook.EndpointListFilter) (*webhook.EndpointList, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.pages > 0 && filter.Cursor == "" {
		cur := "next"
		f.pages--
		return &webhook.EndpointList{Data: f.endpoints, NextCursor: &cur}, nil
	}
	if filter.Cursor != "" {
		return &webhook.EndpointList{Data: nil}, nil
	}
	return &webhook.EndpointList{Data: append([]webhook.EndpointView(nil), f.endpoints...)}, nil
}

func (f *fakeWebhooks) CreateEndpoint(_ context.Context, in webhook.EndpointCreate) (*webhook.Endpoint, error) {
	if f.createErr != nil {
		return nil, f.createErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.created = append(f.created, in)
	id := "ep_new"
	en := true
	if in.Enabled != nil {
		en = *in.Enabled
	}
	f.endpoints = append(f.endpoints, webhook.EndpointView{
		ID: id, Name: in.Name, Kind: in.Kind, Enabled: en, URL: in.URL,
		Events: append([]string(nil), in.Events...), Version: 1,
	})
	return &webhook.Endpoint{ID: id, Name: in.Name, Version: 1}, nil
}

func (f *fakeWebhooks) UpdateEndpoint(_ context.Context, id string, in webhook.EndpointUpdate, _ string) (*webhook.Endpoint, error) {
	if f.updateErr != nil {
		return nil, f.updateErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updated = append(f.updated, in)
	return &webhook.Endpoint{ID: id, Version: 2}, nil
}

func (f *fakeWebhooks) DeleteEndpoint(_ context.Context, id, _ string) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleted = append(f.deleted, id)
	return nil
}

type fakeRoutes struct {
	mu        sync.Mutex
	routes    []rules.Route
	created   []rules.RouteCreate
	updated   []rules.RouteUpdate
	deleted   []string
	listErr   error
	createErr error
	updateErr error
	deleteErr error
}

func (f *fakeRoutes) List(context.Context, rules.ListFilter) (*rules.ListResult, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return &rules.ListResult{Data: append([]rules.Route(nil), f.routes...)}, nil
}

func (f *fakeRoutes) Create(_ context.Context, in rules.RouteCreate) (*rules.Route, error) {
	if f.createErr != nil {
		return nil, f.createErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.created = append(f.created, in)
	return &rules.Route{ID: "rt_new", Name: in.Name, Version: 1}, nil
}

func (f *fakeRoutes) Update(_ context.Context, id string, in rules.RouteUpdate, _ string) (*rules.Route, error) {
	if f.updateErr != nil {
		return nil, f.updateErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updated = append(f.updated, in)
	return &rules.Route{ID: id, Version: 2}, nil
}

func (f *fakeRoutes) Delete(_ context.Context, id, _ string) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleted = append(f.deleted, id)
	return nil
}

type fakeAPIKeys struct {
	mu        sync.Mutex
	keys      []apikeys.Key
	created   []apikeys.CreateInput
	patched   []apikeys.PatchInput
	revoked   []string
	listErr   error
	createErr error
	patchErr  error
	revokeErr error
}

func (f *fakeAPIKeys) List(context.Context, apikeys.ListFilter) (*apikeys.KeyList, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return &apikeys.KeyList{Data: append([]apikeys.Key(nil), f.keys...)}, nil
}

func (f *fakeAPIKeys) Create(_ context.Context, in apikeys.CreateInput) (apikeys.Key, string, error) {
	if f.createErr != nil {
		return apikeys.Key{}, "", f.createErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.created = append(f.created, in)
	return apikeys.Key{ID: "key_new", Name: in.Name, Version: 1}, "ebk_tok", nil
}

func (f *fakeAPIKeys) Patch(_ context.Context, id string, in apikeys.PatchInput, _ string) (apikeys.Key, error) {
	if f.patchErr != nil {
		return apikeys.Key{}, f.patchErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.patched = append(f.patched, in)
	return apikeys.Key{ID: id, Version: 2}, nil
}

func (f *fakeAPIKeys) Revoke(_ context.Context, id, _ string) error {
	if f.revokeErr != nil {
		return f.revokeErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.revoked = append(f.revoked, id)
	return nil
}

type fakeJobs struct {
	mu        sync.Mutex
	created   []*jobs.Job
	handlers  map[string]jobs.Handler
	createErr error
}

func (f *fakeJobs) Create(_ context.Context, kind string, payload any) (*jobs.Job, error) {
	if f.createErr != nil {
		return nil, f.createErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	j := &jobs.Job{ID: "job_1", Kind: kind}
	f.created = append(f.created, j)
	_ = payload
	return j, nil
}

func (f *fakeJobs) Handle(kind string, h jobs.Handler) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.handlers == nil {
		f.handlers = map[string]jobs.Handler{}
	}
	f.handlers[kind] = h
}

type fakeOrgs struct {
	slug string
	err  error
}

func (f *fakeOrgs) GetOrganization(context.Context, string) (*tenancy.Organization, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &tenancy.Organization{Slug: f.slug}, nil
}

type fakeAudit struct {
	mu      sync.Mutex
	actions []string
	err     error
}

func (f *fakeAudit) Record(_ context.Context, action string, _ audit.Target, _ audit.Changes) error {
	if f.err != nil {
		return f.err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.actions = append(f.actions, action)
	return nil
}

func sv(v any, source string) settings.SettingValue {
	return settings.SettingValue{Value: v, Source: source}
}

var _ = errs.CodeInternalError
