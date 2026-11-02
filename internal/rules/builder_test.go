package rules

import (
	"testing"
)

func TestBuilderToCEL_DeterministicRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		b    Builder
		cel  string
	}{
		{
			name: "empty",
			b:    Builder{Match: MatchAll, Conditions: nil},
			cel:  "true",
		},
		{
			name: "type in",
			b: Builder{Match: MatchAll, Conditions: []BuilderCondition{
				{Field: "type", Op: "in", Value: []string{"message.received"}},
			}},
			cel: `type in ["message.received"]`,
		},
		{
			name: "header exists + from domain",
			b: Builder{Match: MatchAll, Conditions: []BuilderCondition{
				{Field: "type", Op: "in", Value: []string{"message.received"}},
				{Field: "header", Name: "x-ricevuta", Op: "exists"},
				{Field: "from.domain", Op: "eq", Value: "pec.example.it"},
			}},
			cel: `(type in ["message.received"]) && (hasHeader("x-ricevuta")) && (domain(data.from) == "pec.example.it")`,
		},
		{
			name: "any match",
			b: Builder{Match: MatchAny, Conditions: []BuilderCondition{
				{Field: "subject", Op: "contains", Value: "invoice"},
				{Field: "subject", Op: "contains", Value: "receipt"},
			}},
			cel: `(data.subject.contains("invoice")) || (data.subject.contains("receipt"))`,
		},
		{
			name: "attachments and flags",
			b: Builder{Match: MatchAll, Conditions: []BuilderCondition{
				{Field: "attachments", Op: "exists"},
				{Field: "flags", Op: "contains", Value: "\\Flagged"},
			}},
			cel: `(size(data.attachments) > 0) && ("\\Flagged" in data.flags)`,
		},
		{
			name: "to.domain eq",
			b: Builder{Match: MatchAll, Conditions: []BuilderCondition{
				{Field: "to.domain", Op: "eq", Value: "example.com"},
			}},
			cel: `data.to.exists(a, domain(a) == "example.com")`,
		},
		{
			name: "auth spf",
			b: Builder{Match: MatchAll, Conditions: []BuilderCondition{
				{Field: "auth.spf", Op: "eq", Value: "pass"},
			}},
			cel: `spf() == "pass"`,
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
			if err != nil || !res.OK || res.CEL != tc.cel {
				t.Fatalf("compile: %+v %v", res, err)
			}
			rev := Expressible(got)
			if !rev.Expressible || rev.Builder == nil {
				t.Fatalf("not expressible: %+v", rev)
			}
			again, err := BuilderToCEL(*rev.Builder)
			if err != nil || again != got {
				t.Fatalf("round trip: %q vs %q (%v)", again, got, err)
			}
		})
	}
}

func TestExpressible_RejectsHandWritten(t *testing.T) {
	cases := []string{
		`type == "message.received" && size(data.attachments) > 2`,
		`true || false`,
		`has(data.foo)`,
		``,
	}
	for _, cel := range cases {
		if Expressible(cel).Expressible {
			t.Fatalf("expected not expressible: %q", cel)
		}
	}
}

func TestBuilderToCEL_Validation(t *testing.T) {
	if _, err := BuilderToCEL(Builder{Match: "xor"}); err == nil {
		t.Fatal("bad match")
	}
	if _, err := BuilderToCEL(Builder{Match: MatchAll, Conditions: []BuilderCondition{
		{Field: "header", Op: "exists"},
	}}); err == nil {
		t.Fatal("header needs name")
	}
	if _, err := BuilderToCEL(Builder{Match: MatchAll, Conditions: []BuilderCondition{
		{Field: "nope", Op: "eq", Value: "x"},
	}}); err == nil {
		t.Fatal("unknown field")
	}
}
