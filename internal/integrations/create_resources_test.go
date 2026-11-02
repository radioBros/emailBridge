package integrations

import (
	"context"
	"fmt"
	"testing"

	"github.com/radiobros/emailbridge/internal/integrations/connectors"
)

type fakeEP struct {
	specs []EndpointSpec
	ids   int
}

func (f *fakeEP) Create(_ context.Context, spec EndpointSpec) (*CreatedEndpoint, error) {
	f.specs = append(f.specs, spec)
	f.ids++
	return &CreatedEndpoint{ID: fmt.Sprintf("ep_%d", f.ids), Version: 1}, nil
}
func (f *fakeEP) UpdateURL(context.Context, string, string, int64) (int64, error) { return 1, nil }
func (f *fakeEP) SetEnabled(context.Context, string, bool, int64) (int64, error) {
	return 1, nil
}
func (f *fakeEP) Delete(context.Context, string, int64) error { return nil }

type fakeRT struct{ n int }

func (f *fakeRT) Create(context.Context, RouteSpec) (*CreatedRoute, error) {
	f.n++
	return &CreatedRoute{ID: "rt_1", Version: 1}, nil
}
func (f *fakeRT) SetEnabled(context.Context, string, bool, int64) (int64, error) { return 1, nil }
func (f *fakeRT) Delete(context.Context, string, int64) error                    { return nil }

type fakeGW struct {
	created []GatewaySpec
	deleted []string
}

func (f *fakeGW) Create(_ context.Context, spec GatewaySpec) (*CreatedGateway, error) {
	f.created = append(f.created, spec)
	return &CreatedGateway{ID: "gwy_1"}, nil
}
func (f *fakeGW) Delete(_ context.Context, id string) error {
	f.deleted = append(f.deleted, id)
	return nil
}
func (f *fakeGW) SetEnabled(context.Context, string, bool) error { return nil }
func (f *fakeGW) Update(context.Context, string, GatewaySpec) error {
	return nil
}

func TestCreateResourcesCRMAndSending(t *testing.T) {
	ep := &fakeEP{}
	rt := &fakeRT{}
	gw := &fakeGW{}
	svc := &Service{Endpoints: ep, Routes: rt, Gateways: gw}
	crm := Materialize(connectors.HubSpot())
	cr, err := svc.createResources(context.Background(), crm, "int_01hxxxxxxxxxxxxxxxxxxxxxxx", map[string]any{
		"filter": `type == "message.received"`, "portalId": "1",
	}, map[string]string{"accessToken": "t"})
	if err != nil || len(cr.Endpoints) != 1 || len(cr.Routes) != 1 {
		t.Fatalf("%+v %v", cr, err)
	}
	if len(ep.specs) != 1 || ep.specs[0].Kind != connectors.KindIntegration {
		t.Fatalf("%+v", ep.specs)
	}
	if id, _ := ep.specs[0].SinkConfig["instanceId"].(string); id != "int_01hxxxxxxxxxxxxxxxxxxxxxxx" {
		t.Fatalf("%v", ep.specs[0].SinkConfig)
	}
	ses := Materialize(connectors.SES())
	cr2, err := svc.createResources(context.Background(), ses, "int_02hxxxxxxxxxxxxxxxxxxxxxxx", map[string]any{
		"region": "eu-west-1", "from": "a@b.c", "accessKey": "AK",
	}, map[string]string{"secretKey": "SK"})
	if err != nil || len(cr2.Gateways) != 1 {
		t.Fatalf("%+v %v", cr2, err)
	}
	if len(gw.created) != 1 || gw.created[0].Kind != "ses" {
		t.Fatalf("%+v", gw.created)
	}
	_ = svc.deleteResources(context.Background(), cr2)
	if len(gw.deleted) != 1 {
		t.Fatal(gw.deleted)
	}
}

func TestResolveTemplateInstanceID(t *testing.T) {
	out := resolveTemplate("{{instance.id}}/{{config.x}}", map[string]any{
		"__instanceId": "int_1", "x": "y",
	}, nil)
	if out != "int_1/y" {
		t.Fatal(out)
	}
}
