package lint

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/danielgtaylor/huma/v2"
)

// EventSchema is one api/events/*.json file with its findings.
type EventSchema struct {
	File        string
	Examples    int
	Findings    []Finding
	IsEnvelope  bool
	EventType   string // e.g. account.added (from the file name)
	schema      *huma.Schema
	reg         *registry
	rawExamples []any
}

// LoadEvents reads every JSON Schema in dir (api/events).
func LoadEvents(dir string) ([]*EventSchema, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	var out []*EventSchema
	for _, f := range files {
		b, err := os.ReadFile(f) //nolint:gosec // G304: files of the given directory
		if err != nil {
			return nil, err
		}
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			return nil, fmt.Errorf("%s: %w", filepath.Base(f), err)
		}
		reg := newRegistry("#/$defs/")
		for name, s := range mapOf(m["$defs"]) {
			reg.schemas[name] = convert(s)
		}
		reg.precompute()
		base := filepath.Base(f)
		es := &EventSchema{File: base, IsEnvelope: base == "envelope.json", schema: convert(m), reg: reg,
			rawExamples: list(m["examples"])}
		es.Examples = len(es.rawExamples)
		if !es.IsEnvelope {
			// account.added.v1.json → account.added
			es.EventType = strings.TrimSuffix(strings.TrimSuffix(base, ".json"), ".v"+lastVersion(base))
		}
		out = append(out, es)
	}
	return out, nil
}

func lastVersion(base string) string {
	name := strings.TrimSuffix(base, ".json")
	i := strings.LastIndex(name, ".v")
	if i < 0 {
		return ""
	}
	return name[i+2:]
}

// LintEvents fills Findings: every schema has at least one example, every example validates (an example
// shaped like a CloudEvents envelope is validated against envelope.json and its `data` against the event's
// schema), and every data field has a description.
func LintEvents(schemas []*EventSchema) {
	var env *EventSchema
	for _, s := range schemas {
		if s.IsEnvelope {
			env = s
		}
	}
	for _, s := range schemas {
		add := func(rule, where, msg string, args ...any) {
			s.Findings = append(s.Findings, Finding{Rule: rule, Where: where, Message: fmt.Sprintf(msg, args...)})
		}
		if s.Examples == 0 {
			add(RuleResponseExample, "", "schema has no examples")
		}
		for i, ex := range s.rawExamples {
			where := fmt.Sprintf("example #%d", i+1)
			em, isEnvelope := ex.(map[string]any)
			isEnvelope = isEnvelope && !s.IsEnvelope && em["specversion"] != nil && em["data"] != nil
			if isEnvelope {
				if env != nil {
					for _, e := range validateWith(env.reg, env.schema, huma.ModeReadFromServer, ex) {
						add(RuleInvalidExample, where+" envelope", "%s", e)
					}
				}
				if t := str(em["type"]); t != s.EventType {
					add(RuleInvalidExample, where+" envelope", "type %q, want %q", t, s.EventType)
				}
				ex = em["data"]
			}
			for _, e := range validateWith(s.reg, s.schema, huma.ModeReadFromServer, ex) {
				add(RuleInvalidExample, where, "%s", e)
			}
		}
		d := &Doc{reg: s.reg}
		if missing := d.missingDocs(s.schema); len(missing) > 0 && !s.IsEnvelope {
			add(RuleFieldDoc, "", "fields without description: %s", summarize(missing))
		}
	}
}
