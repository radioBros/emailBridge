package integrations

import (
	"context"

	"github.com/radiobros/emailbridge/internal/integrations/connectors"
)

// Materialize turns a connectors.Def into a registry Definition.
func Materialize(d connectors.Def) *Definition {
	eps := make([]EndpointTemplate, len(d.Endpoints))
	for i, e := range d.Endpoints {
		sc := map[string]string{}
		for k, v := range e.SinkConfig {
			sc[k] = v
		}
		eps[i] = EndpointTemplate{
			Name: e.Name, URL: e.URL, Kind: e.Kind, Events: append([]string(nil), e.Events...),
			SinkConfig: sc, PayloadText: e.PayloadText, PayloadAttachments: e.PayloadAttachments,
		}
	}
	rts := make([]RouteTemplate, len(d.Routes))
	for i, r := range d.Routes {
		rts[i] = RouteTemplate{
			Name: r.Name, EndpointIndex: r.EndpointIndex,
			Filter: r.Filter, Map: r.Map,
		}
	}
	settings := map[string]string{}
	for k, v := range d.Settings {
		settings[k] = v
	}
	def := &Definition{
		ID: d.ID, Version: d.Version, Name: d.Name, Category: d.Category,
		Icon: d.Icon, Color: d.Color, Description: d.Description, DocsPath: d.DocsPath,
		ConfigSchema:      d.ConfigSchema,
		SecretFields:      append([]string(nil), d.SecretFields...),
		Capabilities:      append([]string(nil), d.Capabilities...),
		InstanceOwnerOnly: d.InstanceOwnerOnly,
		Resources: ResourceTemplates{
			WebhookEndpoints: eps,
			Routes:           rts,
			Settings:         settings,
		},
	}
	if d.TestFunc != nil {
		fn := d.TestFunc
		def.Test = func(ctx context.Context, in TestInput) (TestResult, error) {
			tr, err := fn(ctx, connectors.TestInput{Config: in.Config, Secrets: in.Secrets, Client: in.Client})
			if err != nil {
				return TestResult{}, err
			}
			return TestResult{OK: tr.OK, LatencyMs: tr.LatencyMs, Detail: tr.Detail}, nil
		}
	} else if d.Tester != "" {
		tester := d.Tester
		def.Test = func(ctx context.Context, in TestInput) (TestResult, error) {
			tr, err := connectors.RunTest(ctx, connectors.Def{Tester: tester}, connectors.TestInput{
				Config: in.Config, Secrets: in.Secrets, Client: in.Client,
			})
			if err != nil {
				return TestResult{}, err
			}
			return TestResult{OK: tr.OK, LatencyMs: tr.LatencyMs, Detail: tr.Detail}, nil
		}
	}
	return def
}

// P82Definitions materialises every P8.2 connector.
func P82Definitions() []*Definition {
	return materializeAll(connectors.All())
}

// P83Definitions materialises every P8.3 connector.
func P83Definitions() []*Definition {
	return materializeAll(connectors.P83All())
}

func materializeAll(raw []connectors.Def) []*Definition {
	out := make([]*Definition, 0, len(raw))
	for _, d := range raw {
		out = append(out, Materialize(d))
	}
	return out
}
