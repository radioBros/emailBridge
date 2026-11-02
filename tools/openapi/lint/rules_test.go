package lint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const fixture = `
openapi: 3.1.0
info: {title: t, version: "1"}
components:
  schemas:
    Problem:
      type: object
      additionalProperties: false
      required: [type, title, status, code, kind]
      properties:
        type: {type: string}
        title: {type: string}
        status: {type: integer, format: int64}
        code: {type: string}
        kind: {type: string}
        detail: {type: string}
    Org:
      type: object
      additionalProperties: false
      required: [id, name, createdAt]
      properties:
        id: {type: string, pattern: "^org_[0-9a-z]{26}$", description: Organisation ID., examples: ["org_01m5caqpk0kast63yc13wpy2d9"]}
        name: {type: string, maxLength: 10, description: Display name., examples: ["Acme"]}
        createdAt: {type: string, format: date-time, description: Creation time., examples: ["2026-10-20T09:00:00Z"]}
    Loose:
      type: object
      properties:
        id: {type: string}
        meta: {type: object, additionalProperties: true, description: Free-form.}
paths:
  /good:
    post:
      operationId: good
      summary: Create org
      description: Creates an organisation.
      parameters:
        - {name: Idempotency-Key, in: header, description: Key., schema: {type: string}, example: "k-1"}
      requestBody:
        content:
          application/json:
            schema: {$ref: "#/components/schemas/Org"}
      responses:
        "201":
          description: Created
          content:
            application/json:
              schema: {$ref: "#/components/schemas/Org"}
        "409":
          description: Conflict
          content:
            application/problem+json:
              schema: {$ref: "#/components/schemas/Problem"}
              examples:
                conflict: {value: {type: "https://emailbridge.dev/problems/conflict", title: Conflict, status: 409, code: conflict, kind: conflict}}
  /bad:
    get:
      operationId: bad
      parameters:
        - {name: orgId, in: path, required: true, schema: {type: string}}
      responses:
        "200":
          description: OK
          content:
            application/json:
              schema: {$ref: "#/components/schemas/Loose"}
        "500":
          description: oops
          content:
            application/problem+json:
              schema: {$ref: "#/components/schemas/Problem"}
  /invalid:
    post:
      operationId: invalid
      summary: Invalid examples
      description: Every example here is wrong.
      requestBody:
        content:
          application/json:
            schema: {$ref: "#/components/schemas/Org"}
            examples:
              extra: {value: {id: "org_01m5caqpk0kast63yc13wpy2d9", name: Acme, createdAt: "2026-10-20T09:00:00Z", surplus: 1}}
              wrongType: {value: {id: "org_01m5caqpk0kast63yc13wpy2d9", name: 42, createdAt: "2026-10-20T09:00:00Z"}}
              badPattern: {value: {id: "ORG-1", name: Acme, createdAt: "2026-10-20T09:00:00Z"}}
              badFormat: {value: {id: "org_01m5caqpk0kast63yc13wpy2d9", name: Acme, createdAt: "yesterday"}}
              missing: {value: {id: "org_01m5caqpk0kast63yc13wpy2d9"}}
      responses:
        "200":
          description: OK
          content:
            application/json:
              schema:
                type: object
                properties:
                  n: {type: integer, description: Count., examples: ["seven"]}
        "404":
          description: Not found
          content:
            application/problem+json:
              schema: {$ref: "#/components/schemas/Problem"}
              example: {type: "https://emailbridge.dev/problems/not_found", title: Not found, status: 410, code: not_found, kind: not_found}
`

func opByID(t *testing.T, id string) Operation {
	t.Helper()
	doc, err := Load([]byte(fixture))
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range doc.Operations() {
		if op.ID == id {
			return op
		}
	}
	t.Fatalf("operation %s not found", id)
	return Operation{}
}

func rules(op Operation) map[string]int {
	out := map[string]int{}
	for _, f := range op.Findings {
		out[f.Rule]++
	}
	return out
}

func TestGoodOperationIsClean(t *testing.T) {
	op := opByID(t, "good")
	if len(op.Findings) != 0 {
		t.Fatalf("findings: %v", op.Findings)
	}
}

func TestBadOperationFindings(t *testing.T) {
	op := opByID(t, "bad")
	r := rules(op)
	for _, want := range []string{RuleSummary, RuleDescription, RuleParam, RuleResponseExample, RuleProblems, RuleFieldDoc} {
		if r[want] == 0 {
			t.Errorf("missing %s finding; got %v", want, op.Findings)
		}
	}
}

func TestInvalidExamplesAreCaught(t *testing.T) {
	op := opByID(t, "invalid")
	msgs := map[string]bool{}
	for _, f := range op.Findings {
		if f.Rule != RuleInvalidExample {
			continue
		}
		for _, name := range []string{"extra", "wrongType", "badPattern", "badFormat", "missing", "field n", "has status"} {
			if strings.Contains(f.Message, name) {
				msgs[name] = true
			}
		}
	}
	for _, name := range []string{"extra", "wrongType", "badPattern", "badFormat", "missing", "field n", "has status"} {
		if !msgs[name] {
			t.Errorf("invalid example %q not reported; findings: %v", name, op.Findings)
		}
	}
}

func TestEventsLint(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("envelope.json", `{"type":"object","required":["specversion","type","data"],"properties":{"specversion":{"type":"string"},"type":{"type":"string"},"data":{}},
	  "examples":[{"specversion":"1.0","type":"account.added","data":{}}]}`)
	write("account.added.v1.json", `{"type":"object","required":["accountId"],"properties":{"accountId":{"type":"string","description":"Account ID."}},
	  "examples":[{"accountId":"acc_01m5c8kym00mr02c50c1r5a3d4"},{"specversion":"1.0","type":"account.added","data":{"accountId":"acc_01m5c8kym00mr02c50c1r5a3d4"}},
	              {"specversion":"1.0","type":"account.deleted","data":{"accountId":7}}]}`)
	write("account.deleted.v1.json", `{"type":"object","properties":{"accountId":{"type":"string"}}}`)
	schemas, err := LoadEvents(dir)
	if err != nil {
		t.Fatal(err)
	}
	LintEvents(schemas)
	by := map[string]*EventSchema{}
	for _, s := range schemas {
		by[s.File] = s
	}
	if f := by["envelope.json"].Findings; len(f) != 0 {
		t.Fatalf("envelope: %v", f)
	}
	added := by["account.added.v1.json"]
	if added.EventType != "account.added" || len(added.Findings) != 2 { // wrong type + invalid data in example #3
		t.Fatalf("account.added findings: %v", added.Findings)
	}
	del := rules(Operation{Findings: by["account.deleted.v1.json"].Findings})
	if del[RuleResponseExample] != 1 || del[RuleFieldDoc] != 1 {
		t.Fatalf("account.deleted findings: %v", by["account.deleted.v1.json"].Findings)
	}
}
