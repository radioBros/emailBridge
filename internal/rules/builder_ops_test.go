package rules

import (
	"encoding/json"
	"testing"
)

func TestBuilderToCEL_AllOpsAndFields(t *testing.T) {
	cases := []struct {
		name string
		b    Builder
		cel  string
	}{
		{
			name: "header ops",
			b: Builder{Match: MatchAll, Conditions: []BuilderCondition{
				{Field: "header", Name: "X-Foo", Op: "notExists"},
				{Field: "header", Name: "x-bar", Op: "eq", Value: "1"},
				{Field: "header", Name: "x-bar", Op: "neq", Value: "2"},
				{Field: "header", Name: "x-bar", Op: "contains", Value: "a"},
				{Field: "header", Name: "x-bar", Op: "startsWith", Value: "a"},
				{Field: "header", Name: "x-bar", Op: "endsWith", Value: "z"},
				{Field: "header", Name: "x-bar", Op: "matches", Value: "^a"},
				{Field: "header", Name: "x-bar", Op: "in", Value: []string{"a", "b"}},
			}},
			cel: `(!hasHeader("x-foo")) && (header("x-bar") == "1") && (header("x-bar") != "2") && (header("x-bar").contains("a")) && (header("x-bar").startsWith("a")) && (header("x-bar").endsWith("z")) && (header("x-bar").matches("^a")) && (header("x-bar") in ["a", "b"])`,
		},
		{
			name: "scalar mailbox account subject",
			b: Builder{Match: MatchAll, Conditions: []BuilderCondition{
				{Field: "account.id", Op: "eq", Value: "acc_1"},
				{Field: "account.externalId", Op: "neq", Value: "x"},
				{Field: "mailbox.path", Op: "startsWith", Value: "IN"},
				{Field: "mailbox.specialUse", Op: "exists"},
				{Field: "from.address", Op: "endsWith", Value: "@ex.com"},
				{Field: "subject", Op: "notExists"},
				{Field: "auth.dkim", Op: "in", Value: []any{"pass", "fail"}},
				{Field: "auth.dmarc", Op: "contains", Value: "p"},
			}},
			cel: `(account.id == "acc_1") && (account.externalId != "x") && (data.mailbox.path.startsWith("IN")) && (data.mailbox.specialUse != "") && (data.from.address.endsWith("@ex.com")) && (data.subject == "") && (dkim() in ["pass", "fail"]) && (dmarc().contains("p"))`,
		},
		{
			name: "to.domain variants",
			b: Builder{Match: MatchAny, Conditions: []BuilderCondition{
				{Field: "to.domain", Op: "neq", Value: "bad.com"},
				{Field: "to.domain", Op: "in", Value: []string{"a.com", "b.com"}},
				{Field: "to.domain", Op: "contains", Value: "mail"},
				{Field: "to.domain", Op: "startsWith", Value: "m"},
				{Field: "to.domain", Op: "endsWith", Value: ".it"},
				{Field: "to.domain", Op: "matches", Value: "^a"},
				{Field: "to.domain", Op: "exists"},
				{Field: "to.domain", Op: "notExists"},
			}},
			cel: `(!data.to.exists(a, domain(a) == "bad.com")) || (data.to.exists(a, domain(a) in ["a.com", "b.com"])) || (data.to.exists(a, domain(a).contains("mail"))) || (data.to.exists(a, domain(a).startsWith("m"))) || (data.to.exists(a, domain(a).endsWith(".it"))) || (data.to.exists(a, domain(a).matches("^a"))) || (size(data.to) > 0) || (size(data.to) == 0)`,
		},
		{
			name: "flags and attachments",
			b: Builder{Match: MatchAll, Conditions: []BuilderCondition{
				{Field: "flags", Op: "notExists"},
				{Field: "flags", Op: "eq", Value: "\\Seen"},
				{Field: "flags", Op: "in", Value: []string{"\\Seen", "\\Flagged"}},
				{Field: "attachments", Op: "notExists"},
				{Field: "attachments", Op: "eq", Value: float64(2)},
				{Field: "attachments", Op: "neq", Value: int64(0)},
			}},
			cel: `(size(data.flags) == 0) && ("\\Seen" in data.flags) && (data.flags.exists(f, f in ["\\Seen", "\\Flagged"])) && (size(data.attachments) == 0) && (size(data.attachments) == 2) && (size(data.attachments) != 0)`,
		},
	}
	eng, err := NewEngine(EngineOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := BuilderToCEL(tc.b)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.cel {
				t.Fatalf("cel:\n got %q\nwant %q", got, tc.cel)
			}
			res, err := eng.CompileBuilder(tc.b)
			if err != nil || !res.OK {
				t.Fatalf("compile: %+v %v", res, err)
			}
			rev := Expressible(got)
			if !rev.Expressible {
				t.Fatalf("not expressible: %q", got)
			}
			again, err := BuilderToCEL(*rev.Builder)
			if err != nil || again != got {
				t.Fatalf("round trip: %q vs %q (%v)", again, got, err)
			}
		})
	}
}

func TestBuilderToCEL_ErrorPaths(t *testing.T) {
	bad := []Builder{
		{Match: MatchAll, Conditions: []BuilderCondition{{Field: "header", Name: "h", Op: "bogus"}}},
		{Match: MatchAll, Conditions: []BuilderCondition{{Field: "subject", Op: "bogus"}}},
		{Match: MatchAll, Conditions: []BuilderCondition{{Field: "to.domain", Op: "bogus"}}},
		{Match: MatchAll, Conditions: []BuilderCondition{{Field: "flags", Op: "bogus"}}},
		{Match: MatchAll, Conditions: []BuilderCondition{{Field: "attachments", Op: "contains", Value: "x"}}},
		{Match: MatchAll, Conditions: []BuilderCondition{{Field: "subject", Op: "eq"}}},
		{Match: MatchAll, Conditions: []BuilderCondition{{Field: "attachments", Op: "eq", Value: map[string]any{}}}},
		{Match: MatchAll, Conditions: []BuilderCondition{{Field: "header", Name: "h", Op: "in", Value: 3}}},
		{Match: MatchAll, Conditions: []BuilderCondition{{Field: "flags", Op: "in", Value: true}}},
	}
	for i, b := range bad {
		if _, err := BuilderToCEL(b); err == nil {
			t.Fatalf("case %d expected error", i)
		}
	}
}

func TestAsStringAsIntLists(t *testing.T) {
	if _, err := asString(nil); err == nil {
		t.Fatal("nil")
	}
	if s, err := asString(float64(1.5)); err != nil || s != "1.5" {
		t.Fatal(s, err)
	}
	if s, err := asString(json.Number("9")); err != nil || s != "9" {
		t.Fatal(s, err)
	}
	if s, err := asString(true); err != nil || s != "true" {
		t.Fatal(s, err)
	}
	if _, err := asString([]string{"x"}); err == nil {
		t.Fatal("slice")
	}
	if n, err := asInt(float64(3)); err != nil || n != 3 {
		t.Fatal(n, err)
	}
	if n, err := asInt(4); err != nil || n != 4 {
		t.Fatal(n, err)
	}
	if n, err := asInt(int64(5)); err != nil || n != 5 {
		t.Fatal(n, err)
	}
	if n, err := asInt(json.Number("6")); err != nil || n != 6 {
		t.Fatal(n, err)
	}
	if n, err := asInt("7"); err != nil || n != 7 {
		t.Fatal(n, err)
	}
	if _, err := asInt(true); err == nil {
		t.Fatal("bool")
	}
	list, err := asStringList([]any{"a", float64(1), json.Number("2"), true})
	if err != nil || len(list) != 4 || list[1] != "1" || list[3] != "true" {
		t.Fatal(list, err)
	}
	list2, err := asStringList([]string{"a", "b"})
	if err != nil || len(list2) != 2 {
		t.Fatal(list2, err)
	}
	if _, err := asStringList("solo"); err == nil {
		// single string is not a list
		t.Fatal("string alone")
	}
	if _, err := asStringList(3); err == nil {
		t.Fatal("int")
	}
}

func TestBuilderFromToMap(t *testing.T) {
	if _, err := BuilderFromMap(nil); err == nil {
		t.Fatal("nil")
	}
	b := Builder{Match: MatchAll, Conditions: []BuilderCondition{
		{Field: "type", Op: "eq", Value: "message.received"},
	}}
	m := BuilderToMap(b)
	got, err := BuilderFromMap(m)
	if err != nil || got.Match != MatchAll || len(got.Conditions) != 1 {
		t.Fatalf("%+v %v", got, err)
	}
	if _, err := BuilderFromMap(map[string]any{"match": []int{1}}); err == nil {
		t.Fatal("invalid")
	}
}

func TestExpressible_HeaderAndAttachmentVariants(t *testing.T) {
	cel := `(hasHeader("x-a")) && (!hasHeader("x-b")) && (header("x-c") == "1") && (header("x-c") != "2") && (header("x-c").contains("a")) && (header("x-c").startsWith("a")) && (header("x-c").endsWith("z")) && (header("x-c").matches("^a")) && (header("x-c") in ["a"]) && (size(data.attachments) == 1) && (size(data.attachments) != 2) && (size(data.flags) > 0) && (size(data.flags) == 0) && ("\\Seen" in data.flags)`
	rev := Expressible(cel)
	if !rev.Expressible {
		t.Fatalf("%+v", rev)
	}
	again, err := BuilderToCEL(*rev.Builder)
	if err != nil || again != cel {
		t.Fatalf("%q vs %q %v", again, cel, err)
	}
}

func TestCompileBuilder_NilEngine(t *testing.T) {
	var eng *Engine
	res, err := eng.CompileBuilder(Builder{Match: MatchAll})
	if err != nil || !res.OK || res.CEL != "true" {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestStringOpDefault(t *testing.T) {
	if stringOp("a", "noop", `"b"`) != "" {
		t.Fatal("expected empty")
	}
}
