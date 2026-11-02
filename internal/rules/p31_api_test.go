package rules

import (
	"context"
	"testing"

	"github.com/radiobros/emailbridge/internal/tenancy"
)

func TestP31APIHandlersDirect(t *testing.T) {
	eng, err := NewEngine(EngineOptions{})
	if err != nil {
		t.Fatal(err)
	}
	m := &Module{engine: eng, repo: &Repo{Engine: eng}}
	ctx := tenancy.WithCaller(context.Background(), tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: "usr_1", OrganizationID: "org_1", Role: tenancy.RoleAdmin,
	})

	out, err := m.compileBuilder(ctx, &compileBuilderIn{Body: struct {
		Builder Builder `json:"builder"`
	}{Builder: Builder{Match: MatchAll, Conditions: []BuilderCondition{
		{Field: "type", Op: "eq", Value: "message.received"},
	}}}})
	if err != nil || !out.Body.OK || out.Body.CEL == "" {
		t.Fatalf("%+v %v", out, err)
	}
	_, err = m.compileBuilder(ctx, &compileBuilderIn{Body: struct {
		Builder Builder `json:"builder"`
	}{Builder: Builder{Match: "xor"}}})
	if err == nil {
		t.Fatal("expected compile error")
	}

	exp, err := m.expressibleBuilder(ctx, &expressibleIn{Body: struct {
		CEL string `json:"cel"`
	}{CEL: out.Body.CEL}})
	if err != nil || !exp.Body.Expressible {
		t.Fatalf("%+v %v", exp, err)
	}

	deny := tenancy.WithCaller(context.Background(), tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: "usr_2", OrganizationID: "org_1", Role: tenancy.RoleViewer,
	})
	if _, err := m.compileBuilder(deny, &compileBuilderIn{}); err == nil {
		t.Fatal("viewer should not compile")
	}
	if _, err := m.expressibleBuilder(deny, &expressibleIn{}); err == nil {
		t.Fatal("viewer should not expressible")
	}
}

func TestStartDryRunValidation(t *testing.T) {
	eng, err := NewEngine(EngineOptions{})
	if err != nil {
		t.Fatal(err)
	}
	r := &Repo{Engine: eng}
	ctx := tenancy.WithCaller(context.Background(), tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: "usr_1", OrganizationID: "org_1", Role: tenancy.RoleAdmin,
	})
	if _, err := r.StartDryRun(ctx, "rte_x", DryRunRequest{}); err == nil {
		t.Fatal("expected jobs missing or not found")
	}
}
