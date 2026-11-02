package rules

import (
	"context"
	"testing"
)

func BenchmarkTypicalExpressions(b *testing.B) {
	e, err := NewEngine(EngineOptions{})
	if err != nil {
		b.Fatal(err)
	}
	ev := sampleEvent()
	account := map[string]any{"email": "bob@example.com"}
	env := map[string]string{"webhookKey": "k"}
	exprs := []struct {
		name, filter, mapExpr string
	}{
		{"pec_filter", `type == "message.received" && hasHeader("x-ricevuta")`, ""},
		{"slim_map", `true`, `{"messageId": data.messageIdHeader, "receipt": header("x-ricevuta")}`},
		{"from_domain", `domain(data.from) == "pec.example.it"`, ""},
	}
	for _, tc := range exprs {
		p, err := e.Compile(tc.filter, tc.mapExpr)
		if err != nil {
			b.Fatal(err)
		}
		b.Run(tc.name, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				_, _, _, err := e.Eval(context.Background(), p, ev, account, env)
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
