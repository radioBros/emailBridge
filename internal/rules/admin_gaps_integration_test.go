//go:build integration

package rules

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

func TestIntegrationRouteSearchAndLastError(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	if err := LastErrorIndexMigration(1).Up(ctx, db); err != nil {
		t.Fatal(err)
	}
	engine, err := NewEngine(EngineOptions{})
	if err != nil {
		t.Fatal(err)
	}
	ep := "whe_01jb0000000000000000000001"
	repo := &Repo{
		DB: db, Engine: engine,
		EndpointExists: func(context.Context, string, string) (bool, error) { return true, nil },
	}
	org := store.NewID(store.PrefixOrganization)
	as := tenancy.WithCaller(ctx, tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: "usr_1", OrganizationID: org, Role: tenancy.RoleAdmin,
	})

	r1, err := repo.Create(as, RouteCreate{Name: "Billing Alert", EndpointID: ep, Filter: "true"})
	if err != nil {
		t.Fatal(err)
	}
	r2, err := repo.Create(as, RouteCreate{Name: "Other", EndpointID: ep, Filter: "true"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = repo.Create(as, RouteCreate{Name: "Noise", EndpointID: ep, Filter: "true"})
	if err != nil {
		t.Fatal(err)
	}

	byName, err := repo.List(as, ListFilter{Q: "billing", Page: 1, Limit: 10, IncludeTotal: true})
	if err != nil || len(byName.Data) != 1 || byName.Data[0].ID != r1.ID || byName.Total == nil || *byName.Total != 1 {
		t.Fatalf("name q: %+v %v", byName, err)
	}
	literal, err := repo.List(as, ListFilter{Q: ".*", Limit: 10})
	if err != nil || len(literal.Data) != 0 {
		t.Fatalf("regex metachar must match literally: %+v %v", literal, err)
	}
	if _, err := repo.List(as, ListFilter{Q: string(make([]byte, 201))}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("long q: %v", err)
	}

	got, err := repo.Get(as, r1.ID)
	if err != nil || got.LastError != nil {
		t.Fatalf("no errors yet: %+v %v", got, err)
	}

	older := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	newer := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	if err := repo.WriteRuleError(ctx, RuleError{
		OrganizationID: org, RouteID: r1.ID, Version: 1, Phase: "compile", Message: "old", At: older,
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.WriteRuleError(ctx, RuleError{
		OrganizationID: org, RouteID: r1.ID, Version: 1, Phase: "filter", Message: "boom", At: newer,
	}); err != nil {
		t.Fatal(err)
	}

	listed, err := repo.List(as, ListFilter{Q: "Billing", Limit: 10})
	if err != nil || len(listed.Data) != 1 || listed.Data[0].LastError == nil {
		t.Fatalf("lastError on list: %+v %v", listed, err)
	}
	le := listed.Data[0].LastError
	if le.Code != "filter" || le.Message != "boom" || !le.At.Equal(newer) {
		t.Fatalf("lastError=%+v", le)
	}
	clean, err := repo.Get(as, r2.ID)
	if err != nil || clean.LastError != nil {
		t.Fatalf("route without errors must omit lastError: %+v %v", clean, err)
	}
}

// A stored route error is shown in the admin: an evaluation error quoting a secret env value must be masked (G3).
func TestIntegrationWriteRuleErrorMasksSecrets(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	org := store.NewID(store.PrefixOrganization)
	repo := &Repo{DB: db, EnvLoader: func(context.Context, string) (map[string]string, []string, error) {
		return map[string]string{"API_TOKEN": "s3cr3t-token-value", "REGION": "eu"}, []string{"API_TOKEN"}, nil
	}}
	route := store.NewID(store.PrefixRoute)
	if err := repo.WriteRuleError(ctx, RuleError{
		OrganizationID: org, RouteID: route, Version: 1, Phase: "eval",
		Message:        `strconv.Atoi: parsing "s3cr3t-token-value": invalid syntax`,
		PayloadExcerpt: `{"subject":"s3cr3t-token-value"}`,
	}); err != nil {
		t.Fatal(err)
	}
	var got RuleError
	if err := db.Collection(RuleErrorsCollection).FindOne(ctx, map[string]any{"routeId": route}).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got.Message, "s3cr3t-token-value") || strings.Contains(got.PayloadExcerpt, "s3cr3t-token-value") {
		t.Fatalf("secret stored unmasked: %+v", got)
	}
	if !strings.Contains(got.Message, "invalid syntax") {
		t.Fatalf("message lost its context: %q", got.Message)
	}
}
