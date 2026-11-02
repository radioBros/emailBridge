package connectors

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/radiobros/emailbridge/internal/errs"
)

const (
	TypeSentry  = "sentry"
	TypeGrafana = "grafana"
	TypeLoki    = "loki"
	TypeOTel    = "otel"
)

// GuardInstall refuses connectors that require the instance owner when the caller is not one.
func GuardInstall(d Def, instanceOwner bool) error {
	if !d.InstanceOwnerOnly || instanceOwner {
		return nil
	}
	return errs.New(errs.CodeForbidden, d.Name+" may only be installed in the instance owner's organisation.").
		WithSafe("type", d.ID).WithSafe("reason", "instance_owner_required")
}

// SentryRuntime holds decrypted connector values for the observability reporter (W37).
type SentryRuntime struct {
	DSN         string
	Environment string
	SampleRate  float64
	Enabled     bool
}

// SentryRuntimeFrom builds runtime options from instance config + secrets.
func SentryRuntimeFrom(cfg map[string]any, secrets map[string]string, enabled bool) SentryRuntime {
	rate := 1.0
	if s := strCfg(cfg, "sampleRate"); s != "" {
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			rate = f
		}
	}
	env := strCfg(cfg, "environment")
	if env == "" {
		env = "production"
	}
	return SentryRuntime{
		DSN:         strings.TrimSpace(secrets["dsn"]),
		Environment: env,
		SampleRate:  rate,
		Enabled:     enabled && strings.TrimSpace(secrets["dsn"]) != "",
	}
}

// Sentry returns the Sentry/GlitchTip DSN settings connector (instance error reporting when env DSN is empty).
func Sentry() Def {
	return Def{
		ID: TypeSentry, Version: "1.0.0", Name: "Sentry / GlitchTip", Category: CategoryObservability,
		Icon: "mdi-bug", Color: "#362D59",
		Description: "Instance error reporting for any Sentry-compatible DSN (Sentry or GlitchTip). " +
			"Installable only in the instance owner's organisation. EB_SENTRY_DSN always wins when set.",
		DocsPath:          "/docs/integrations/sentry",
		InstanceOwnerOnly: true,
		ConfigSchema: map[string]any{
			"type":     "object",
			"required": []any{"dsn"},
			"properties": map[string]any{
				"dsn":         map[string]any{"type": "string", "writeOnly": true, "title": "DSN"},
				"sampleRate":  map[string]any{"type": "string", "title": "Sample rate", "default": "1.0"},
				"environment": map[string]any{"type": "string", "title": "Environment", "default": "production"},
			},
		},
		SecretFields: []string{"dsn"},
		Capabilities: []string{CapTest},
		Settings: map[string]string{
			"integrations.sentry.sampleRate":  "{{config.sampleRate}}",
			"integrations.sentry.environment": "{{config.environment}}",
		},
		TestFunc: func(ctx context.Context, in TestInput) (TestResult, error) {
			_ = ctx
			dsn := strings.TrimSpace(in.Secrets["dsn"])
			if dsn == "" {
				return TestResult{OK: false, Detail: "dsn required"}, nil
			}
			if !strings.HasPrefix(dsn, "http://") && !strings.HasPrefix(dsn, "https://") {
				return TestResult{OK: false, Detail: "dsn must be an http(s) URL"}, nil
			}
			return TestResult{OK: true, Detail: "DSN shape OK"}, nil
		},
	}
}

// Grafana returns the Grafana dashboard import preset.
func Grafana() Def {
	return Def{
		ID: TypeGrafana, Version: "1.0.0", Name: "Grafana dashboard", Category: CategoryObservability,
		Icon: "mdi-chart-areaspline", Color: "#F46800",
		Description: "Import the bundled EmailBridge Grafana dashboard for mail, sync and delivery metrics.",
		DocsPath:    "/docs/integrations/grafana",
		ConfigSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"dashboardPath": map[string]any{
					"type": "string", "title": "Dashboard path",
					"default": "deploy/observability/presets/grafana/emailbridge.json",
				},
			},
		},
		Capabilities: []string{CapTest},
		Settings: map[string]string{
			"integrations.grafana.dashboardPath": "{{config.dashboardPath}}",
		},
		TestFunc: func(ctx context.Context, in TestInput) (TestResult, error) {
			_ = ctx
			path := strCfg(in.Config, "dashboardPath")
			if path == "" {
				path = "deploy/observability/presets/grafana/emailbridge.json"
			}
			if _, err := os.Stat(path); err != nil {
				if _, err2 := os.Stat(filepath.Clean(path)); err2 != nil {
					return TestResult{OK: false, Detail: fmt.Sprintf("dashboard not found: %s", path)}, nil
				}
			}
			return TestResult{OK: true, Detail: "dashboard file present"}, nil
		},
	}
}

// Loki returns the Loki label preset.
func Loki() Def {
	return Def{
		ID: TypeLoki, Version: "1.0.0", Name: "Loki labels", Category: CategoryObservability,
		Icon: "mdi-text-box-search", Color: "#F97316",
		Description: "Preset Loki label set for the EmailBridge stdout log mirror (service, role, node).",
		DocsPath:    "/docs/integrations/loki",
		ConfigSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"serviceLabel": map[string]any{"type": "string", "default": "emailbridge", "title": "service label"},
				"includeRole":  map[string]any{"type": "boolean", "default": true, "title": "Include role label"},
				"includeNode":  map[string]any{"type": "boolean", "default": true, "title": "Include node label"},
			},
		},
		Capabilities: []string{CapTest},
		Settings: map[string]string{
			"integrations.loki.serviceLabel": "{{config.serviceLabel}}",
		},
		TestFunc: func(ctx context.Context, in TestInput) (TestResult, error) {
			_ = ctx
			_ = in
			return TestResult{OK: true, Detail: "labels documented in presets/loki/labels.md"}, nil
		},
	}
}

// OTel returns the OpenTelemetry collector preset.
func OTel() Def {
	return Def{
		ID: TypeOTel, Version: "1.0.0", Name: "OpenTelemetry collector", Category: CategoryObservability,
		Icon: "mdi-telescope", Color: "#425CC7",
		Description: "Bundled OpenTelemetry collector configuration for EmailBridge traces and metrics.",
		DocsPath:    "/docs/integrations/otel",
		ConfigSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"collectorPath": map[string]any{
					"type": "string", "title": "Collector config path",
					"default": "deploy/observability/presets/otel/collector.yaml",
				},
			},
		},
		Capabilities: []string{CapTest},
		Settings: map[string]string{
			"integrations.otel.collectorPath": "{{config.collectorPath}}",
		},
		TestFunc: func(ctx context.Context, in TestInput) (TestResult, error) {
			_ = ctx
			path := strCfg(in.Config, "collectorPath")
			if path == "" {
				path = "deploy/observability/presets/otel/collector.yaml"
			}
			if _, err := os.Stat(path); err != nil {
				return TestResult{OK: false, Detail: fmt.Sprintf("collector config not found: %s", path)}, nil
			}
			return TestResult{OK: true, Detail: "collector config present"}, nil
		},
	}
}
