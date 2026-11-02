package configcode

import (
	"context"

	"github.com/radiobros/emailbridge/internal/apikeys"
	"github.com/radiobros/emailbridge/internal/audit"
	"github.com/radiobros/emailbridge/internal/jobs"
	"github.com/radiobros/emailbridge/internal/rules"
	"github.com/radiobros/emailbridge/internal/settings"
	"github.com/radiobros/emailbridge/internal/webhook"
)

// SettingsAPI is the settings surface used by plan/apply/export.
type SettingsAPI interface {
	Effective(ctx context.Context, orgID string) (settings.SettingsBody, error)
	Patch(ctx context.Context, scope string, patch map[string]any, ifMatch string) (settings.SettingsBody, error)
}

// WebhooksAPI is the webhook endpoint surface used by plan/apply/export.
type WebhooksAPI interface {
	ListEndpoints(ctx context.Context, filter webhook.EndpointListFilter) (*webhook.EndpointList, error)
	CreateEndpoint(ctx context.Context, in webhook.EndpointCreate) (*webhook.Endpoint, error)
	UpdateEndpoint(ctx context.Context, id string, in webhook.EndpointUpdate, ifMatch string) (*webhook.Endpoint, error)
	DeleteEndpoint(ctx context.Context, id, ifMatch string) error
}

// RoutesAPI is the routes surface used by plan/apply/export.
type RoutesAPI interface {
	List(ctx context.Context, f rules.ListFilter) (*rules.ListResult, error)
	Create(ctx context.Context, in rules.RouteCreate) (*rules.Route, error)
	Update(ctx context.Context, id string, in rules.RouteUpdate, ifMatch string) (*rules.Route, error)
	Delete(ctx context.Context, id, ifMatch string) error
}

// APIKeysAPI is the API-keys surface used by plan/apply/export.
type APIKeysAPI interface {
	List(ctx context.Context, f apikeys.ListFilter) (*apikeys.KeyList, error)
	Create(ctx context.Context, in apikeys.CreateInput) (apikeys.Key, string, error)
	Patch(ctx context.Context, id string, in apikeys.PatchInput, ifMatch string) (apikeys.Key, error)
	Revoke(ctx context.Context, id string, ifMatch string) error
}

// JobsAPI queues and handles config.apply jobs.
type JobsAPI interface {
	Create(ctx context.Context, kind string, payload any) (*jobs.Job, error)
	Handle(kind string, h jobs.Handler)
}

// Auditor writes audit records for applied actions.
type Auditor interface {
	Record(ctx context.Context, action string, target audit.Target, changes audit.Changes) error
}
