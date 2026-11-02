package rules

import (
	"context"
	"errors"
	"testing"

	"github.com/radiobros/emailbridge/internal/errs"
)

func strp(s string) *string { return &s }

func draftRepo(t *testing.T) *Repo {
	t.Helper()
	e, err := NewEngine(EngineOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return &Repo{Engine: e}
}

func TestDraftExprs(t *testing.T) {
	f, m, err := DraftExprs(RouteTestRequest{Filter: strp("  isInbox() "), Map: strp(` {"a": 1} `)})
	if err != nil || f != "isInbox()" || m != `{"a": 1}` {
		t.Fatalf("got %q %q %v", f, m, err)
	}
	b := &Builder{Match: MatchAll, Conditions: []BuilderCondition{{Field: "isInbox", Op: "eq", Value: true}}}
	f, _, err = DraftExprs(RouteTestRequest{Filter: strp(""), Builder: b})
	if err != nil || f != "isInbox()" {
		t.Fatalf("builder fallback: %q %v", f, err)
	}
	f, m, err = DraftExprs(RouteTestRequest{Map: strp("event")})
	if err != nil || f != "true" || m != "event" {
		t.Fatalf("map only: %q %q %v", f, m, err)
	}
	bad := &Builder{Match: "nope"}
	if _, _, err := DraftExprs(RouteTestRequest{Builder: bad}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("bad builder: %v", err)
	}
	if (RouteTestRequest{Sample: "message.received"}).IsDraft() {
		t.Fatal("no draft fields must not be a draft")
	}
}

func TestDraftEvaluationIsUncachedAndTraced(t *testing.T) {
	r := draftRepo(t)
	ctx := context.Background()
	// The stored program for this key says "false"; the draft must win and must not replace it in the cache.
	if _, err := r.Engine.Cached("endpoint:whe_x", 1, "false", ""); err != nil {
		t.Fatal(err)
	}
	res, err := r.TestProgram(ctx, "org_1", "endpoint:whe_x", 1, "false", "", RouteTestRequest{
		Sample: "message.received",
		Filter: strp(`type == "message.received" && isInbox()`),
		Map:    strp(`{"event": "messageNew", "subject": data.subject}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Matched || res.Error != "" || len(res.Trace) != 2 {
		t.Fatalf("draft result: %+v", res)
	}
	mapped, _ := res.Mapped.(map[string]any)
	if mapped["event"] != "messageNew" {
		t.Fatalf("mapped: %#v", res.Mapped)
	}
	stored, err := r.TestProgram(ctx, "org_1", "endpoint:whe_x", 1, "false", "", RouteTestRequest{Sample: "message.received"})
	if err != nil || stored.Matched {
		t.Fatalf("stored program changed by the draft: %+v %v", stored, err)
	}
}

func TestDraftCompileErrorHasPosition(t *testing.T) {
	r := draftRepo(t)
	res, err := r.TestProgram(context.Background(), "org_1", "route:rte_x", 1, "true", "", RouteTestRequest{
		Sample: "message.received",
		Filter: strp("type == \"message.received\" &&\n  nope()"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Error == "" || res.ErrorAt == nil || res.ErrorAt.Path != "filter" || res.ErrorAt.Line != 2 {
		t.Fatalf("want a filter error on line 2, got %+v (at %+v)", res, res.ErrorAt)
	}
	res, err = r.TestProgram(context.Background(), "org_1", "route:rte_x", 1, "true", "", RouteTestRequest{
		Sample: "message.received",
		Map:    strp(`{"a": data.subject +}`),
	})
	if err != nil || res.ErrorAt == nil || res.ErrorAt.Path != "map" {
		t.Fatalf("want a map error, got %+v %v", res, err)
	}
}

func TestErrorPositionOf(t *testing.T) {
	if ErrorPositionOf(errors.New("plain")) != nil {
		t.Fatal("plain error has no position")
	}
	if ErrorPositionOf(errs.New(errs.CodeValidationFailed, "x")) != nil {
		t.Fatal("non-compile error has no position")
	}
	p := ErrorPositionOf(compileErr("map", "boom", 3, 7))
	if p == nil || p.Path != "map" || p.Line != 3 || p.Column != 7 {
		t.Fatalf("got %+v", p)
	}
	if ErrorPositionOf(errs.New(errs.CodeRouteCompileError, "no path")) != nil {
		t.Fatal("compile error without a path has no position")
	}
}

func TestBuilderSchemaPathLeaves(t *testing.T) {
	cases := []struct {
		c    BuilderCondition
		want string
	}{
		{BuilderCondition{Field: "data.pec.type", Op: "eq", Value: "accettazione"}, `data.pec.type == "accettazione"`},
		{BuilderCondition{Field: "data.size", Op: "gt", Value: float64(1024)}, `data.size > 1024.0`},
		{BuilderCondition{Field: "data.spam.score", Op: "lte", Value: 4.5}, `data.spam.score <= 4.5`},
		{BuilderCondition{Field: "data.seen", Op: "eq", Value: false}, `data.seen == false`},
		{BuilderCondition{Field: "data.bounce", Op: "exists"}, `has(data.bounce)`},
		{BuilderCondition{Field: "data.bounce", Op: "notExists"}, `!has(data.bounce)`},
		{BuilderCondition{Field: "data.labels", Op: "includes", Value: `\Important`}, `"\\Important" in data.labels`},
		{BuilderCondition{Field: "data.flags", Op: "anyOf", Value: []any{`\Seen`, `\Flagged`}}, `data.flags.exists(x, x in ["\\Seen", "\\Flagged"])`},
		{BuilderCondition{Field: "account.email", Op: "endsWith", Value: "@studio.it"}, `account.email.endsWith("@studio.it")`},
		{BuilderCondition{Field: "weekday", Name: "Europe/Rome", Op: "lt", Value: float64(6)}, `weekdayOf(localTime("Europe/Rome")) < 6`},
		{BuilderCondition{Field: "hour", Op: "gte", Value: float64(9)}, `hourOf(localTime("UTC")) >= 9`},
	}
	for _, tc := range cases {
		got, err := BuilderToCEL(Builder{Match: MatchAll, Conditions: []BuilderCondition{tc.c}})
		if err != nil || got != tc.want {
			t.Errorf("%+v: got %q %v, want %q", tc.c, got, err, tc.want)
		}
	}
	for _, bad := range []BuilderCondition{
		{Field: "data.x) || true || (data", Op: "eq", Value: "a"},
		{Field: "data", Op: "eq", Value: "a"},
		{Field: "data.size", Op: "gt", Value: "big"},
		{Field: "data.size", Op: "between", Value: float64(1)},
	} {
		if _, err := BuilderToCEL(Builder{Conditions: []BuilderCondition{bad}}); err == nil {
			t.Errorf("%+v: want an error", bad)
		}
	}
}

func TestBuilderSchemaPathLeavesEvaluate(t *testing.T) {
	r := draftRepo(t)
	b := &Builder{Match: MatchAll, Conditions: []BuilderCondition{
		{Field: "data.size", Op: "gt", Value: float64(1000)},
		{Field: "data.labels", Op: "includes", Value: "work"},
		{Field: "data.bounce", Op: "notExists"},
	}}
	for _, size := range []any{float64(2048), int64(2048), 2048} {
		ev := map[string]any{"type": "message.received", "data": map[string]any{"size": size, "labels": []any{"work"}}}
		res, err := r.TestProgram(context.Background(), "org_1", "x", 1, "", "", RouteTestRequest{Event: ev, Builder: b})
		if err != nil || res.Error != "" || !res.Matched {
			t.Fatalf("size %T: %+v %v", size, res, err)
		}
	}
}
