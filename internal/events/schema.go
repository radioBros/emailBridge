package events

import (
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/danielgtaylor/huma/v2"
)

// jsonSchemaDialect is the dialect of every generated schema (S3 §2).
const jsonSchemaDialect = "https://json-schema.org/draft/2020-12/schema"

// SchemaFiles generates every event schema plus envelope.json: file name → indented JSON. Names are
// "<type>.v<N>.json" (api/events, S3 §2). The output is deterministic so CI can diff it.
func SchemaFiles() (map[string][]byte, error) {
	out := map[string][]byte{}
	var envExamples []any
	for _, ct := range Types() {
		var examples []any
		if env, data, ok := exampleFor(ct); ok { // W70: the envelope as a webhook receives it, then the data
			examples = []any{env, data}
			if ct.Name == AccountAdded || ct.Name == EndpointTest {
				envExamples = append(envExamples, env) // an account event and an organisation-level event
			}
		}
		b, err := schemaDoc(ct.Data, SchemaURL(ct.Name, ct.Version), ct.Name+" data (v"+fmt.Sprint(ct.Version)+")", examples)
		if err != nil {
			return nil, err
		}
		out[fmt.Sprintf("%s.v%d.json", ct.Name, ct.Version)] = b
	}
	env, err := schemaDoc(reflect.TypeFor[Event](), SchemaBase+"envelope.json", "EmailBridge event envelope (CloudEvents 1.0)", envExamples)
	if err != nil {
		return nil, err
	}
	out["envelope.json"] = env
	return out, nil
}

func schemaDoc(t reflect.Type, id, title string, examples []any) ([]byte, error) {
	reg := huma.NewMapRegistry("#/$defs/", huma.DefaultSchemaNamer)
	root := huma.SchemaFromType(reg, t)
	if root.Ref != "" { // named struct: inline the root, keep the rest in $defs
		name := root.Ref[len("#/$defs/"):]
		root = reg.Map()[name]
		delete(reg.Map(), name)
	}
	raw, err := json.Marshal(root)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	openContent(m)
	m["$schema"] = jsonSchemaDialect
	m["$id"] = id
	m["title"] = title
	if len(examples) > 0 {
		m["examples"] = examples
	}
	if defs := reg.Map(); len(defs) > 0 {
		rawDefs, err := json.Marshal(defs)
		if err != nil {
			return nil, err
		}
		var dm map[string]any
		if err := json.Unmarshal(rawDefs, &dm); err != nil {
			return nil, err
		}
		openContent(dm)
		m["$defs"] = dm
	}
	b, err := json.MarshalIndent(m, "", "  ") // map keys are sorted: deterministic
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// openContent removes "additionalProperties": false everywhere: S3 §2 promises that additive changes keep
// the major version and consumers ignore unknown fields, so a v1 validator must accept newer payloads.
func openContent(v any) {
	switch x := v.(type) {
	case map[string]any:
		if ap, ok := x["additionalProperties"].(bool); ok && !ap {
			delete(x, "additionalProperties")
		}
		for _, c := range x {
			openContent(c)
		}
	case []any:
		for _, c := range x {
			openContent(c)
		}
	}
}
