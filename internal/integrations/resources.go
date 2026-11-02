package integrations

import (
	"context"
	"fmt"
	"strings"
)

// EndpointSpec is passed to EndpointResources.
type EndpointSpec struct {
	Name       string
	URL        string
	Kind       string
	Events     []string
	SinkConfig map[string]any
	// PayloadText / PayloadAttachments set the endpoint's payload options (text "plain" / attachments).
	PayloadText, PayloadAttachments bool
}

// GatewaySpec creates an API sending gateway from a sending-provider connector.
type GatewaySpec struct {
	Name      string
	Kind      string
	Region    string
	Domain    string
	BaseURL   string
	From      string
	AccessKey string
	SecretKey string
	APIKey    string
}

// CreatedGateway is returned after gateway create.
type CreatedGateway struct {
	ID string
}

// GatewayResources creates/deletes gateways owned by sending connectors (W52).
type GatewayResources interface {
	Create(ctx context.Context, spec GatewaySpec) (*CreatedGateway, error)
	Delete(ctx context.Context, id string) error
	SetEnabled(ctx context.Context, id string, enabled bool) error
	Update(ctx context.Context, id string, spec GatewaySpec) error
}

// CreatedEndpoint is returned after create.
type CreatedEndpoint struct {
	ID      string
	Version int64
}

// RouteSpec is passed to RouteResources.
type RouteSpec struct {
	Name       string
	EndpointID string
	Filter     string
	Map        string
}

// CreatedRoute is returned after create.
type CreatedRoute struct {
	ID      string
	Version int64
}

// EndpointResources creates and manages webhook endpoints owned by integrations.
type EndpointResources interface {
	Create(ctx context.Context, spec EndpointSpec) (*CreatedEndpoint, error)
	UpdateURL(ctx context.Context, id, url string, version int64) (newVersion int64, err error)
	SetEnabled(ctx context.Context, id string, enabled bool, version int64) (newVersion int64, err error)
	Delete(ctx context.Context, id string, version int64) error
}

// RouteResources creates and manages routes owned by integrations.
type RouteResources interface {
	Create(ctx context.Context, spec RouteSpec) (*CreatedRoute, error)
	SetEnabled(ctx context.Context, id string, enabled bool, version int64) (newVersion int64, err error)
	Delete(ctx context.Context, id string, version int64) error
}

// SettingsResources applies organisation settings and feature switches.
type SettingsResources interface {
	Apply(ctx context.Context, values map[string]string) error
	Clear(ctx context.Context, keys []string) error
	SetSwitch(ctx context.Context, name string, on bool) error
}

// resolveTemplate replaces {{config.x}}, {{secrets.x}} and {{instance.id}} placeholders.
func resolveTemplate(s string, config map[string]any, secrets map[string]string) string {
	out := s
	for k, v := range config {
		ph := "{{config." + k + "}}"
		if strings.Contains(out, ph) {
			out = strings.ReplaceAll(out, ph, fmt.Sprint(v))
		}
	}
	for k, v := range secrets {
		ph := "{{secrets." + k + "}}"
		if strings.Contains(out, ph) {
			out = strings.ReplaceAll(out, ph, v)
		}
	}
	if id, ok := config["__instanceId"].(string); ok && id != "" {
		out = strings.ReplaceAll(out, "{{instance.id}}", id)
	}
	return out
}
