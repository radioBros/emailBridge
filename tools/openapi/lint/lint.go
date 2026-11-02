// Package lint checks the documentation quality of the generated OpenAPI document and of the event JSON
// Schemas (W70): every operation has a summary and a description, documented parameters, request and 2xx
// response examples (a whole-body example, or an example on every field), declared problem responses with
// RFC 9457 examples, a description on every body field — and every example validates against its schema
// with huma's own validator (the one the server runs on requests), so a documented example is a payload the
// server really accepts or returns.
//
// Run: go test ./tools/openapi/lint/ (see lint_test.go for the environment knobs).
package lint

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"go.yaml.in/yaml/v3"
)

// Rules reported by the lint.
const (
	RuleSummary         = "summary"
	RuleDescription     = "description"
	RuleParam           = "param"
	RuleRequestExample  = "request-example"
	RuleResponseExample = "response-example"
	RuleProblems        = "problems"
	RuleFieldDoc        = "field-doc"
	RuleInvalidExample  = "invalid-example"
)

// Finding is one documentation defect.
type Finding struct {
	Rule    string
	Where   string // e.g. "request application/json" or "response 404"
	Message string
}

func (f Finding) String() string {
	if f.Where == "" {
		return f.Rule + ": " + f.Message
	}
	return f.Rule + " [" + f.Where + "]: " + f.Message
}

// Operation is one operation of the document with its findings.
type Operation struct {
	ID       string
	Method   string
	Path     string
	Tag      string
	Findings []Finding
}

// Doc is a loaded OpenAPI document.
type Doc struct {
	raw map[string]any
	reg *registry
}

// LoadFile reads a YAML or JSON OpenAPI document.
func LoadFile(path string) (*Doc, error) {
	b, err := os.ReadFile(path) //nolint:gosec // G304: path given by the developer / CI
	if err != nil {
		return nil, err
	}
	return Load(b)
}

// Load parses a YAML or JSON OpenAPI document.
func Load(b []byte) (*Doc, error) {
	m, err := decode(b)
	if err != nil {
		return nil, err
	}
	reg := newRegistry("#/components/schemas/")
	comps, _ := m["components"].(map[string]any)
	schemas, _ := comps["schemas"].(map[string]any)
	for name, s := range schemas {
		reg.schemas[name] = convert(s)
	}
	reg.precompute()
	return &Doc{raw: m, reg: reg}, nil
}

// decode parses YAML/JSON and normalises it through JSON so numbers are float64 like a decoded request body.
func decode(b []byte) (map[string]any, error) {
	var v any
	if err := yaml.Unmarshal(b, &v); err != nil {
		return nil, err
	}
	j, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(j, &m); err != nil {
		return nil, err
	}
	return m, nil
}

// Operations lints every operation, sorted by path and method.
func (d *Doc) Operations() []Operation {
	paths, _ := d.raw["paths"].(map[string]any)
	var out []Operation
	for p, itemRaw := range paths {
		item, _ := itemRaw.(map[string]any)
		for _, m := range []string{"get", "put", "post", "patch", "delete"} {
			op, ok := item[m].(map[string]any)
			if !ok {
				continue
			}
			o := Operation{Method: strings.ToUpper(m), Path: p}
			o.ID, _ = op["operationId"].(string)
			if tags, _ := op["tags"].([]any); len(tags) > 0 {
				o.Tag, _ = tags[0].(string)
			}
			if o.ID == "" {
				o.ID = o.Method + " " + p
			}
			o.Findings = d.lintOperation(item, op)
			out = append(out, o)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].Method < out[j].Method
	})
	return out
}

func (d *Doc) lintOperation(item, op map[string]any) []Finding {
	var fs []Finding
	add := func(rule, where, msg string, args ...any) {
		fs = append(fs, Finding{Rule: rule, Where: where, Message: fmt.Sprintf(msg, args...)})
	}
	if strings.TrimSpace(str(op["summary"])) == "" {
		add(RuleSummary, "", "operation has no summary")
	}
	if strings.TrimSpace(str(op["description"])) == "" {
		add(RuleDescription, "", "operation has no description (what it does, permission/scope, side effects, events)")
	}

	// Parameters (path-level ones apply too).
	params := append(append([]any{}, list(item["parameters"])...), list(op["parameters"])...)
	for _, pr := range params {
		p := d.resolveComponent(pr, "parameters")
		if p == nil {
			continue
		}
		where := "param " + str(p["in"]) + ":" + str(p["name"])
		if strings.TrimSpace(str(p["description"])) == "" {
			add(RuleParam, where, "parameter has no description")
		}
		s := convert(p["schema"])
		examples := mediaExamples(p)
		if len(examples) == 0 && !d.exemplified(s, map[*huma.Schema]bool{}) {
			add(RuleParam, where, "parameter has no example")
		}
		for name, ex := range examples {
			for _, e := range d.validate(s, huma.ModeWriteToServer, ex) {
				add(RuleInvalidExample, where, "example %s: %s", name, e)
			}
		}
		fs = append(fs, d.schemaExampleFindings(s, where, huma.ModeWriteToServer)...)
	}

	// Request body.
	if rb := d.resolveComponent(op["requestBody"], "requestBodies"); rb != nil {
		for ct, mtRaw := range mapOf(rb["content"]) {
			fs = append(fs, d.lintMedia("request "+ct, mapOf(mtRaw), huma.ModeWriteToServer, RuleRequestExample)...)
		}
	}

	// Responses.
	has4xx := false
	for code, rRaw := range mapOf(op["responses"]) {
		r := d.resolveComponent(rRaw, "responses")
		status, err := strconv.Atoi(code)
		if err != nil || r == nil {
			continue
		}
		where := "response " + code
		for name, hRaw := range mapOf(r["headers"]) {
			h := d.resolveComponent(hRaw, "headers")
			if h == nil {
				continue
			}
			hw := where + " header " + name
			if strings.TrimSpace(str(h["description"])) == "" {
				add(RuleParam, hw, "response header has no description")
			}
			hs := convert(h["schema"])
			for en, ex := range mediaExamples(h) {
				for _, e := range d.validate(hs, huma.ModeReadFromServer, ex) {
					add(RuleInvalidExample, hw, "example %s: %s", en, e)
				}
			}
		}
		switch {
		case status >= 200 && status < 300:
			for ct, mtRaw := range mapOf(r["content"]) {
				fs = append(fs, d.lintMedia(where+" "+ct, mapOf(mtRaw), huma.ModeReadFromServer, RuleResponseExample)...)
			}
		case status >= 400:
			if status < 500 {
				has4xx = true
			}
			for ct, mtRaw := range mapOf(r["content"]) {
				mt := mapOf(mtRaw)
				if ct != "application/problem+json" {
					fs = append(fs, d.lintMedia(where+" "+ct, mt, huma.ModeReadFromServer, RuleResponseExample)...)
					continue
				}
				exs := mediaExamples(mt)
				if len(exs) == 0 {
					add(RuleProblems, where, "problem response has no example: declare its codes with httpapi.Problems(…)")
				}
				s := convert(mt["schema"])
				for name, ex := range exs {
					for _, e := range d.validate(s, huma.ModeReadFromServer, ex) {
						add(RuleInvalidExample, where, "example %s: %s", name, e)
					}
					if pm, ok := ex.(map[string]any); ok {
						if st, _ := pm["status"].(float64); int(st) != status {
							add(RuleInvalidExample, where, "example %s has status %v", name, pm["status"])
						}
					}
				}
			}
		}
	}
	if !has4xx && !publicProbe(op, params) {
		add(RuleProblems, "", "operation declares no 4xx problem response")
	}
	sort.SliceStable(fs, func(i, j int) bool { return fs[i].String() < fs[j].String() })
	return fs
}

// publicProbe reports an unauthenticated operation without inputs (security: [], no body, only header
// parameters), such as /healthz — the only kind of operation allowed to declare no 4xx response.
func publicProbe(op map[string]any, params []any) bool {
	sec, ok := op["security"].([]any)
	if !ok || len(sec) > 0 || op["requestBody"] != nil {
		return false
	}
	for _, p := range params {
		if in := str(mapOf(p)["in"]); in != "header" && in != "" {
			return false
		}
	}
	return true
}

// lintMedia checks one media type: an example (whole body or per field), field docs, valid examples.
func (d *Doc) lintMedia(where string, mt map[string]any, mode huma.ValidateMode, rule string) []Finding {
	var fs []Finding
	s := convert(mt["schema"])
	if s == nil {
		return nil
	}
	exs := mediaExamples(mt)
	if len(exs) == 0 {
		if missing := d.missingExamples(s); len(missing) > 0 {
			fs = append(fs, Finding{Rule: rule, Where: where, Message: "no body example and these fields have no example: " + summarize(missing)})
		}
	}
	for name, ex := range exs {
		for _, e := range d.validate(s, mode, ex) {
			fs = append(fs, Finding{Rule: RuleInvalidExample, Where: where, Message: fmt.Sprintf("example %s: %s", name, e)})
		}
	}
	if missing := d.missingDocs(s); len(missing) > 0 {
		fs = append(fs, Finding{Rule: RuleFieldDoc, Where: where, Message: "fields without description: " + summarize(missing)})
	}
	return append(fs, d.schemaExampleFindings(s, where, mode)...)
}

// schemaExampleFindings validates every field-level `examples` entry reachable from s against its field.
func (d *Doc) schemaExampleFindings(s *huma.Schema, where string, mode huma.ValidateMode) []Finding {
	var fs []Finding
	d.walk(s, "", map[*huma.Schema]bool{}, func(path string, n *huma.Schema) {
		for i, ex := range n.Examples {
			for _, e := range d.validate(n, mode, ex) {
				p := path
				if p == "" {
					p = "(body)"
				}
				fs = append(fs, Finding{Rule: RuleInvalidExample, Where: where, Message: fmt.Sprintf("field %s example #%d: %s", p, i+1, e)})
			}
		}
	})
	return fs
}

func summarize(items []string) string {
	sort.Strings(items)
	if len(items) > 12 {
		return strings.Join(items[:12], ", ") + fmt.Sprintf(" … and %d more", len(items)-12)
	}
	return strings.Join(items, ", ")
}

// validate runs huma's validator.
func (d *Doc) validate(s *huma.Schema, mode huma.ValidateMode, v any) []string {
	return validateWith(d.reg, s, mode, v)
}

func validateWith(reg *registry, s *huma.Schema, mode huma.ValidateMode, v any) []string {
	if s == nil {
		return nil
	}
	precompute(s, map[*huma.Schema]bool{})
	res := &huma.ValidateResult{}
	huma.Validate(reg, s, huma.NewPathBuffer([]byte{}, 0), mode, normalise(v), res)
	out := make([]string, 0, len(res.Errors))
	for _, e := range res.Errors {
		out = append(out, e.Error())
	}
	return out
}

// normalise round-trips a value through JSON (numbers → float64, maps → map[string]any).
func normalise(v any) any {
	b, err := json.Marshal(v)
	if err != nil {
		return v
	}
	var out any
	if json.Unmarshal(b, &out) != nil {
		return v
	}
	return out
}

func (d *Doc) deref(s *huma.Schema) *huma.Schema {
	for i := 0; s != nil && s.Ref != "" && i < 32; i++ {
		s = d.reg.SchemaFromRef(s.Ref)
	}
	return s
}

// exemplified reports whether a value for s can be shown from field-level examples alone.
func (d *Doc) exemplified(s *huma.Schema, seen map[*huma.Schema]bool) bool {
	s = d.deref(s)
	if s == nil {
		return false
	}
	if len(s.Examples) > 0 || s.Default != nil || s.Const != nil || len(s.Enum) > 0 {
		return true
	}
	if seen[s] {
		return true // recursive type: the outer level is judged
	}
	seen[s] = true
	defer delete(seen, s)
	for _, alts := range [][]*huma.Schema{s.OneOf, s.AnyOf, s.AllOf} {
		if len(alts) > 0 {
			for _, a := range alts {
				if !d.exemplified(a, seen) {
					return false
				}
			}
			return true
		}
	}
	switch s.Type {
	case "object":
		if len(s.Properties) == 0 {
			return false // map or free-form object: needs its own example
		}
		for _, p := range s.Properties {
			if !d.exemplified(p, seen) {
				return false
			}
		}
		return true
	case "array":
		return d.exemplified(s.Items, seen)
	}
	return false
}

// missingExamples lists the leaf fields that keep s from being exemplified.
func (d *Doc) missingExamples(s *huma.Schema) []string {
	var out []string
	var rec func(s *huma.Schema, path string, seen map[*huma.Schema]bool)
	rec = func(s *huma.Schema, path string, seen map[*huma.Schema]bool) {
		s = d.deref(s)
		if s == nil || d.exemplified(s, map[*huma.Schema]bool{}) || seen[s] {
			return
		}
		seen[s] = true
		defer delete(seen, s)
		switch {
		case s.Type == "object" && len(s.Properties) > 0:
			for name, p := range s.Properties {
				rec(p, join(path, name), seen)
			}
		case s.Type == "array" && s.Items != nil:
			rec(s.Items, path+"[]", seen)
		default:
			if path == "" {
				path = "(body)"
			}
			out = append(out, path)
		}
	}
	rec(s, "", map[*huma.Schema]bool{})
	return out
}

// missingDocs lists body fields without a description.
func (d *Doc) missingDocs(s *huma.Schema) []string {
	var out []string
	d.walkProps(s, "", map[*huma.Schema]bool{}, func(path string, prop *huma.Schema) {
		if strings.TrimSpace(prop.Description) == "" {
			if t := d.deref(prop); t == nil || strings.TrimSpace(t.Description) == "" {
				out = append(out, path)
			}
		}
	})
	return out
}

// walkProps visits every property (raw, before $ref resolution) of the objects reachable from s.
func (d *Doc) walkProps(s *huma.Schema, path string, seen map[*huma.Schema]bool, fn func(path string, prop *huma.Schema)) {
	s = d.deref(s)
	if s == nil || seen[s] {
		return
	}
	seen[s] = true
	for name, p := range s.Properties {
		fn(join(path, name), p)
		d.walkProps(p, join(path, name), seen, fn)
	}
	if s.Items != nil {
		d.walkProps(s.Items, path+"[]", seen, fn)
	}
	if ap, ok := s.AdditionalProperties.(*huma.Schema); ok {
		d.walkProps(ap, path+"{}", seen, fn)
	}
	for _, alts := range [][]*huma.Schema{s.OneOf, s.AnyOf, s.AllOf} {
		for _, a := range alts {
			d.walkProps(a, path, seen, fn)
		}
	}
}

// walk visits every schema node reachable from s (resolved).
func (d *Doc) walk(s *huma.Schema, path string, seen map[*huma.Schema]bool, fn func(path string, n *huma.Schema)) {
	s = d.deref(s)
	if s == nil || seen[s] {
		return
	}
	seen[s] = true
	fn(path, s)
	for name, p := range s.Properties {
		d.walk(p, join(path, name), seen, fn)
	}
	if s.Items != nil {
		d.walk(s.Items, path+"[]", seen, fn)
	}
	if ap, ok := s.AdditionalProperties.(*huma.Schema); ok {
		d.walk(ap, path+"{}", seen, fn)
	}
	for _, alts := range [][]*huma.Schema{s.OneOf, s.AnyOf, s.AllOf} {
		for _, a := range alts {
			d.walk(a, path, seen, fn)
		}
	}
}

func join(path, name string) string {
	if path == "" {
		return name
	}
	return path + "." + name
}

// resolveComponent follows a $ref to #/components/<kind>/<name>.
func (d *Doc) resolveComponent(v any, kind string) map[string]any {
	m := mapOf(v)
	for i := 0; m != nil && i < 8; i++ {
		ref := str(m["$ref"])
		if ref == "" {
			return m
		}
		name := strings.TrimPrefix(ref, "#/components/"+kind+"/")
		m = mapOf(mapOf(mapOf(d.raw["components"])[kind])[name])
	}
	return m
}

// mediaExamples returns the examples of a media type / parameter / header object, keyed by name.
func mediaExamples(m map[string]any) map[string]any {
	out := map[string]any{}
	if v, ok := m["example"]; ok && v != nil {
		out["example"] = v
	}
	for name, e := range mapOf(m["examples"]) {
		if em := mapOf(e); em != nil {
			if v, ok := em["value"]; ok {
				out[name] = v
			}
		}
	}
	return out
}

// convert builds a huma.Schema from a decoded JSON Schema object (the subset huma generates).
func convert(v any) *huma.Schema {
	if b, ok := v.(bool); ok {
		if b {
			return &huma.Schema{}
		}
		return &huma.Schema{Not: &huma.Schema{}}
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	s := &huma.Schema{}
	switch t := m["type"].(type) {
	case string:
		s.Type = t
	case []any:
		for _, x := range t {
			if x == "null" {
				s.Nullable = true
			} else if xs, ok := x.(string); ok {
				s.Type = xs
			}
		}
	}
	s.Ref = str(m["$ref"])
	s.Format = str(m["format"])
	s.Description = str(m["description"])
	s.Title = str(m["title"])
	s.Pattern = str(m["pattern"])
	s.ContentEncoding = str(m["contentEncoding"])
	s.Default = m["default"]
	s.Const = m["const"]
	s.Examples = list(m["examples"])
	s.Enum = list(m["enum"])
	s.Items = convert(m["items"])
	s.Not = convert(m["not"])
	s.ReadOnly, _ = m["readOnly"].(bool)
	s.WriteOnly, _ = m["writeOnly"].(bool)
	s.UniqueItems, _ = m["uniqueItems"].(bool)
	s.Minimum, s.Maximum = num(m["minimum"]), num(m["maximum"])
	s.ExclusiveMinimum, s.ExclusiveMaximum = num(m["exclusiveMinimum"]), num(m["exclusiveMaximum"])
	s.MultipleOf = num(m["multipleOf"])
	s.MinLength, s.MaxLength = intp(m["minLength"]), intp(m["maxLength"])
	s.MinItems, s.MaxItems = intp(m["minItems"]), intp(m["maxItems"])
	s.MinProperties, s.MaxProperties = intp(m["minProperties"]), intp(m["maxProperties"])
	for _, r := range list(m["required"]) {
		if rs, ok := r.(string); ok {
			s.Required = append(s.Required, rs)
		}
	}
	if props := mapOf(m["properties"]); props != nil {
		s.Properties = map[string]*huma.Schema{}
		for k, p := range props {
			s.Properties[k] = convert(p)
		}
	}
	switch ap := m["additionalProperties"].(type) {
	case bool:
		s.AdditionalProperties = ap
	case map[string]any:
		s.AdditionalProperties = convert(ap)
	}
	for _, kw := range []struct {
		key string
		dst *[]*huma.Schema
	}{{"oneOf", &s.OneOf}, {"anyOf", &s.AnyOf}, {"allOf", &s.AllOf}} {
		for _, a := range list(m[kw.key]) {
			*kw.dst = append(*kw.dst, convert(a))
		}
	}
	if dr := mapOf(m["dependentRequired"]); dr != nil {
		s.DependentRequired = map[string][]string{}
		for k, vs := range dr {
			for _, x := range list(vs) {
				if xs, ok := x.(string); ok {
					s.DependentRequired[k] = append(s.DependentRequired[k], xs)
				}
			}
		}
	}
	return s
}

func precompute(s *huma.Schema, seen map[*huma.Schema]bool) {
	if s == nil || seen[s] {
		return
	}
	seen[s] = true
	s.PrecomputeMessages()
	for _, p := range s.Properties {
		precompute(p, seen)
	}
	precompute(s.Items, seen)
	precompute(s.Not, seen)
	if ap, ok := s.AdditionalProperties.(*huma.Schema); ok {
		precompute(ap, seen)
	}
	for _, alts := range [][]*huma.Schema{s.OneOf, s.AnyOf, s.AllOf} {
		for _, a := range alts {
			precompute(a, seen)
		}
	}
}

// registry resolves $refs for huma.Validate.
type registry struct {
	prefix  string
	schemas map[string]*huma.Schema
}

func newRegistry(prefix string) *registry {
	return &registry{prefix: prefix, schemas: map[string]*huma.Schema{}}
}

func (r *registry) precompute() {
	seen := map[*huma.Schema]bool{}
	for _, s := range r.schemas {
		precompute(s, seen)
	}
}

func (r *registry) Schema(reflect.Type, bool, string) *huma.Schema { return nil }

func (r *registry) TypeFromRef(string) reflect.Type { return nil }

func (r *registry) RegisterTypeAlias(reflect.Type, reflect.Type) {}

func (r *registry) SchemaFromRef(ref string) *huma.Schema {
	return r.schemas[strings.TrimPrefix(ref, r.prefix)]
}

func (r *registry) Map() map[string]*huma.Schema { return r.schemas }

func str(v any) string {
	s, _ := v.(string)
	return s
}

func list(v any) []any {
	l, _ := v.([]any)
	return l
}

func mapOf(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func num(v any) *float64 {
	f, ok := v.(float64)
	if !ok {
		return nil
	}
	return &f
}

func intp(v any) *int {
	f, ok := v.(float64)
	if !ok {
		return nil
	}
	i := int(f)
	return &i
}
