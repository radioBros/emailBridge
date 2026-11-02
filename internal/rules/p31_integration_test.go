//go:build integration

package rules

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/jobs"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

func TestIntegrationVersionsRestoreAndDryRun(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	if err := P31Migration(1).Up(ctx, db); err != nil {
		t.Fatal(err)
	}
	engine, err := NewEngine(EngineOptions{})
	if err != nil {
		t.Fatal(err)
	}
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	js := &jobs.Service{DB: db, NodeID: "n1", Log: quiet}
	ep := "whe_01jb0000000000000000000001"
	repo := &Repo{
		DB: db, Engine: engine, Jobs: js,
		EndpointExists: func(context.Context, string, string) (bool, error) { return true, nil },
		EnvLoader: func(context.Context, string) (map[string]string, []string, error) {
			return map[string]string{"k": "sekrit"}, []string{"k"}, nil
		},
	}
	repo.RegisterHandlers()
	go js.Run(ctx, 2)
	org := store.NewID(store.PrefixOrganization)
	as := tenancy.WithCaller(ctx, tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: "usr_1", OrganizationID: org, Role: tenancy.RoleAdmin,
	})
	as = store.WithOrganization(as, org)

	r, err := repo.Create(as, RouteCreate{
		Name: "v1", EndpointID: ep, Filter: `type == "message.received"`,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = repo.Update(as, r.ID, RouteUpdate{
		Filter: ptr(`type == "message.sent"`),
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	vers, err := repo.ListVersions(as, r.ID, VersionListFilter{Page: 1, Limit: 10, IncludeTotal: true})
	if err != nil || len(vers.Data) != 2 || vers.Total == nil || *vers.Total != 2 {
		t.Fatalf("versions: %+v %v", vers, err)
	}
	if vers.Data[0].Version != 2 || vers.Data[1].Version != 1 {
		t.Fatalf("order: %+v", vers.Data)
	}
	restored, err := repo.RestoreVersion(as, r.ID, 1, "")
	if err != nil {
		t.Fatal(err)
	}
	if restored.CurrentVersion != 3 || restored.Filter != `type == "message.received"` {
		t.Fatalf("restore: %+v", restored)
	}
	vers2, err := repo.ListVersions(as, r.ID, VersionListFilter{IncludeTotal: true, Page: 1, Limit: 10})
	if err != nil || vers2.Total == nil || *vers2.Total != 3 {
		t.Fatalf("after restore: %+v %v", vers2, err)
	}

	// Seed recent events for dry run.
	now := time.Now().UTC()
	for i, typ := range []string{"message.received", "message.sent", "message.received"} {
		data, _ := json.Marshal(map[string]any{"subject": "s", "headers": map[string]any{}})
		_, err := db.Collection(eventsCollection).InsertOne(ctx, map[string]any{
			"_id": store.NewID(store.PrefixEvent), "organizationId": org,
			"type": typ, "time": now.Add(-time.Duration(i) * time.Minute),
			"subject": "msg_1", "data": string(data), "schema": 1, "partition": 0, "published": true,
			"expireAt": now.Add(24 * time.Hour),
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	job, err := repo.StartDryRun(as, r.ID, DryRunRequest{WindowHours: 24, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if job.OrganizationID != org || job.Kind != JobKindDryRun {
		t.Fatalf("job meta: %+v", job)
	}
	got := waitJob(t, js, as, job.ID, jobs.Succeeded)
	var result DryRunResult
	if err := json.Unmarshal(got.Result, &result); err != nil {
		t.Fatal(err)
	}
	if result.Scanned != 3 || result.Matched != 2 {
		t.Fatalf("dry run result: %+v", result)
	}
	if len(result.Samples) == 0 {
		t.Fatal("expected samples")
	}

	// Draft builder override — run handler inline for determinism.
	raw, err := js.RunNow(as, JobKindDryRun, org, dryRunPayload{
		RouteID: r.ID, WindowHours: 24, Limit: 100,
		Filter: `type in ["message.sent"]`, Map: "",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	result2, ok := raw.(DryRunResult)
	if !ok {
		b, _ := json.Marshal(raw)
		_ = json.Unmarshal(b, &result2)
	}
	if result2.Matched != 1 {
		t.Fatalf("builder dry run: %+v", result2)
	}

	// Caps + builder override + draft filter.
	if _, err := repo.StartDryRun(as, r.ID, DryRunRequest{WindowHours: 99}); err == nil {
		t.Fatal("window cap")
	}
	if _, err := repo.StartDryRun(as, r.ID, DryRunRequest{Limit: 5000}); err == nil {
		t.Fatal("limit cap")
	}
	job2, err := repo.StartDryRun(as, r.ID, DryRunRequest{
		Builder: map[string]any{
			"match": "all",
			"conditions": []any{
				map[string]any{"field": "type", "op": "eq", "value": "message.received"},
			},
		},
		Map: `{"ok": true}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = waitJob(t, js, as, job2.ID, jobs.Succeeded)

	m := &Module{engine: engine, repo: repo}
	comp, err := m.compileBuilder(as, &compileBuilderIn{Body: struct {
		Builder Builder `json:"builder"`
	}{Builder: Builder{Match: MatchAll}}})
	if err != nil || !comp.Body.OK {
		t.Fatalf("%+v %v", comp, err)
	}
	exp, err := m.expressibleBuilder(as, &expressibleIn{Body: struct {
		CEL string `json:"cel"`
	}{CEL: "true"}})
	if err != nil || !exp.Body.Expressible {
		t.Fatalf("%+v %v", exp, err)
	}
	vout, err := m.listVersions(as, &versionsIn{RouteID: r.ID, Page: 1, Limit: 10, IncludeTotal: true})
	if err != nil || len(vout.Body.Data) < 1 {
		t.Fatalf("%+v %v", vout, err)
	}
	rest, err := m.restoreVersion(as, &restoreIn{RouteID: r.ID, Version: 1})
	if err != nil || rest.Body.CurrentVersion < 1 {
		t.Fatalf("%+v %v", rest, err)
	}
	dout, err := m.dryRunRoute(as, &dryRunIn{RouteID: r.ID, Body: DryRunRequest{Limit: 10}})
	if err != nil || dout.Status != 202 || dout.Location == "" {
		t.Fatalf("%+v %v", dout, err)
	}
}

func ptr[T any](v T) *T { return &v }

func waitJob(t *testing.T, js *jobs.Service, ctx context.Context, id, state string) *jobs.Job {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	var last string
	for time.Now().Before(deadline) {
		j, err := js.Get(ctx, id)
		if err != nil {
			last = err.Error()
			time.Sleep(50 * time.Millisecond)
			continue
		}
		last = j.State
		if j.State == state {
			return j
		}
		if j.State == jobs.Failed || j.State == jobs.Cancelled {
			t.Fatalf("job %s ended %s err=%v result=%s", id, j.State, j.Error, j.ErrorText)
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for job %s → %s (last=%s)", id, state, last)
	return nil
}
