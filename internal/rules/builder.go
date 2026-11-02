package rules

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/radiobros/emailbridge/internal/errs"
)

// BuilderMatch combines conditions with all (&&) or any (||) or not.
const (
	MatchAll        = "all"
	MatchAny        = "any"
	MatchNot        = "not"
	maxBuilderDepth = 8
)

// Builder is the no-code filter model (S8 §4 / P3.1 / W73).
type Builder struct {
	Match      string             `json:"match" doc:"How the conditions combine: all, any or not" example:"all"`
	Conditions []BuilderCondition `json:"conditions" doc:"Conditions or nested groups"`
}

// BuilderCondition is one predicate or a nested group (W73).
type BuilderCondition struct {
	Field      string             `json:"field,omitempty" doc:"Field: type, account.id, account.externalId, mailbox.path, mailbox.specialUse, from.address, from.domain, to.domain, subject, flags, label, body, hasAttachments, attachments, attachment.name, attachment.type, attachment.size, auth.spf, auth.dkim, auth.dmarc, isInbox, isReply, isForward, isAutoReply, isBounce, spamScore, pecType, size, weekday, hour, businessHours, header, inList" example:"from.domain"`
	Name       string             `json:"name,omitempty" doc:"Header name (field header) or list name (field inList)" example:"vip-customers"` // header / list name
	Op         string             `json:"op,omitempty" doc:"Operator: eq, neq, contains, startsWith, endsWith, matches, in, includes, anyOf, gt, gte, lt, lte, exists or notExists (which ones apply depends on the field)" example:"in"`
	Value      any                `json:"value,omitempty" doc:"Value to compare with: a string, number, boolean or list (for in / anyOf)"`
	Match      string             `json:"match,omitempty" doc:"Nested group: all, any or not" example:"any"` // nested group: all|any|not
	Conditions []BuilderCondition `json:"conditions,omitempty" doc:"Children of a nested group"`             // nested group children
}

// BuilderCompileResult is POST /v1/routes/builder/compile.
type BuilderCompileResult struct {
	CEL   string `json:"cel" doc:"The compiled CEL filter" example:"type == \"message.received\" && fromDomain() in [\"example.org\", \"example.net\"]"`
	OK    bool   `json:"ok" doc:"The model compiled" example:"true"`
	Error string `json:"error,omitempty" doc:"Why the model could not be compiled" example:"conditions[1]: operator gt needs a number"`
}

// BuilderExpressibleResult is POST /v1/routes/builder/expressible.
type BuilderExpressibleResult struct {
	Expressible bool     `json:"expressible" doc:"The CEL can be shown in the builder" example:"true"`
	Builder     *Builder `json:"builder,omitempty" doc:"The builder model; omitted when not expressible"`
}

var (
	reBuilderQuoted  = regexp.MustCompile(`^"((?:\\.|[^"\\])*)"$`)
	reHeaderExists   = regexp.MustCompile(`^hasHeader\("((?:\\.|[^"\\])*)"\)$`)
	reHeaderNot      = regexp.MustCompile(`^!hasHeader\("((?:\\.|[^"\\])*)"\)$`)
	reHeaderEq       = regexp.MustCompile(`^header\("((?:\\.|[^"\\])*)"\)\s*==\s*"((?:\\.|[^"\\])*)"$`)
	reHeaderNeq      = regexp.MustCompile(`^header\("((?:\\.|[^"\\])*)"\)\s*!=\s*"((?:\\.|[^"\\])*)"$`)
	reHeaderContains = regexp.MustCompile(`^header\("((?:\\.|[^"\\])*)"\)\.contains\("((?:\\.|[^"\\])*)"\)$`)
	reHeaderStarts   = regexp.MustCompile(`^header\("((?:\\.|[^"\\])*)"\)\.startsWith\("((?:\\.|[^"\\])*)"\)$`)
	reHeaderEnds     = regexp.MustCompile(`^header\("((?:\\.|[^"\\])*)"\)\.endsWith\("((?:\\.|[^"\\])*)"\)$`)
	reHeaderMatches  = regexp.MustCompile(`^header\("((?:\\.|[^"\\])*)"\)\.matches\("((?:\\.|[^"\\])*)"\)$`)
	reHeaderIn       = regexp.MustCompile(`^header\("((?:\\.|[^"\\])*)"\)\s+in\s+(\[[^\]]*\])$`)
)

// CompileBuilder turns a builder model into deterministic CEL text and type-checks it.
func (e *Engine) CompileBuilder(b Builder) (*BuilderCompileResult, error) {
	cel, err := BuilderToCEL(b)
	if err != nil {
		return nil, err
	}
	out := &BuilderCompileResult{CEL: cel, OK: true}
	if e == nil {
		return out, nil
	}
	if _, err := e.Compile(cel, ""); err != nil {
		out.OK = false
		out.Error = err.Error()
		return out, err
	}
	return out, nil
}

// BuilderToCEL emits CEL without compiling (deterministic; golden-file friendly).
func BuilderToCEL(b Builder) (string, error) {
	return groupToCEL(b.Match, b.Conditions, 0)
}

func groupToCEL(match string, conditions []BuilderCondition, depth int) (string, error) {
	if depth > maxBuilderDepth {
		return "", errs.New(errs.CodeValidationFailed, "builder nesting exceeds depth 8")
	}
	match = strings.TrimSpace(match)
	if match == "" {
		match = MatchAll
	}
	if match != MatchAll && match != MatchAny && match != MatchNot {
		return "", errs.New(errs.CodeValidationFailed, "builder.match must be all, any or not")
	}
	if len(conditions) == 0 {
		if match == MatchNot {
			return "false", nil
		}
		return "true", nil
	}
	parts := make([]string, 0, len(conditions))
	for i, c := range conditions {
		expr, err := conditionToCEL(c, depth)
		if err != nil {
			return "", errs.New(errs.CodeValidationFailed, err.Error()).WithSafe("index", i)
		}
		parts = append(parts, expr)
	}
	if match == MatchNot {
		inner := parts[0]
		if len(parts) > 1 {
			wrapped := make([]string, len(parts))
			for i, p := range parts {
				wrapped[i] = "(" + p + ")"
			}
			inner = strings.Join(wrapped, " && ")
		}
		return "!(" + inner + ")", nil
	}
	join := " && "
	if match == MatchAny {
		join = " || "
	}
	if len(parts) == 1 {
		return parts[0], nil
	}
	wrapped := make([]string, len(parts))
	for i, p := range parts {
		wrapped[i] = "(" + p + ")"
	}
	return strings.Join(wrapped, join), nil
}

func conditionToCEL(c BuilderCondition, depth int) (string, error) {
	if strings.TrimSpace(c.Match) != "" || len(c.Conditions) > 0 {
		return groupToCEL(c.Match, c.Conditions, depth+1)
	}
	field := strings.TrimSpace(c.Field)
	op := strings.TrimSpace(c.Op)
	if field == "" || op == "" {
		return "", fmt.Errorf("condition field and op are required")
	}
	switch field {
	case "header":
		return headerCondition(c.Name, op, c.Value)
	case "type":
		return scalarCondition("type", op, c.Value)
	case "account.id":
		return scalarCondition("account.id", op, c.Value)
	case "account.externalId":
		return scalarCondition("account.externalId", op, c.Value)
	case "mailbox.path", "folder":
		return scalarCondition("data.mailbox.path", op, c.Value)
	case "mailbox.specialUse":
		return scalarCondition("data.mailbox.specialUse", op, c.Value)
	case "from.address":
		return scalarCondition("data.from.address", op, c.Value)
	case "from.domain":
		return scalarCondition("domain(data.from)", op, c.Value)
	case "to.domain":
		return toDomainCondition(op, c.Value)
	case "subject":
		return scalarCondition("data.subject", op, c.Value)
	case "flags":
		return flagsCondition(op, c.Value)
	case "hasAttachments": // W73: the helper is safe when the event has no attachments field
		return boolHelperCondition("hasAttachments()", op, c.Value)
	case "attachments":
		return attachmentsCondition(op, c.Value)
	case "auth.spf": // result word via the helper: authResults.spf is an object {result, domain}
		return scalarCondition("spf()", op, c.Value)
	case "auth.dkim":
		return scalarCondition("dkim()", op, c.Value)
	case "auth.dmarc":
		return scalarCondition("dmarc()", op, c.Value)
	case "isInbox":
		return boolHelperCondition("isInbox()", op, c.Value)
	case "label":
		s, err := asString(c.Value)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf(`hasLabel(%s)`, celQuote(s)), nil
	case "body":
		return bodyCondition(op, c.Value)
	case "attachment.name":
		s, err := asString(c.Value)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf(`hasAttachment(%s)`, celQuote(s)), nil
	case "attachment.type":
		s, err := asString(c.Value)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf(`%s in attachmentTypes()`, celQuote(strings.ToLower(s))), nil
	case "attachment.size":
		return numericCondition(`attachments().exists(a, a.size`, op, c.Value, true)
	case "inList":
		listName := strings.TrimSpace(c.Name)
		if listName == "" {
			return "", fmt.Errorf("inList requires name")
		}
		s, err := asString(c.Value)
		if err != nil {
			// default value expression: fromDomain()
			return fmt.Sprintf(`inList(%s, fromDomain())`, celQuote(listName)), nil
		}
		return fmt.Sprintf(`inList(%s, %s)`, celQuote(listName), celQuote(s)), nil
	case "isReply":
		return boolHelperCondition("isReply()", op, c.Value)
	case "isAutoReply":
		return boolHelperCondition("isAutoReply()", op, c.Value)
	case "isForward":
		return boolHelperCondition("isForward()", op, c.Value)
	case "isBounce":
		return boolHelperCondition("isBounce()", op, c.Value)
	case "spamScore":
		return numericCondition("spamScore()", op, c.Value, false)
	case "pecType":
		return scalarCondition("pecType()", op, c.Value)
	case "size":
		return numericCondition("data.size", op, c.Value, false)
	case "weekday": // W73b: name = IANA zone (the organisation's), default UTC
		return numericCondition(fmt.Sprintf(`weekdayOf(localTime(%s))`, celQuote(builderTZ(c.Name))), op, c.Value, false)
	case "hour":
		return numericCondition(fmt.Sprintf(`hourOf(localTime(%s))`, celQuote(builderTZ(c.Name))), op, c.Value, false)
	case "businessHours":
		// value: {tz, window, days} or ["tz","window","days"]
		tz, window, days, err := asBusinessHours(c.Value)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf(`businessHours(%s, %s, %s)`, celQuote(tz), celQuote(window), celQuote(days)), nil
	default:
		if reBuilderPath.MatchString(field) { // W73b: any payload field picked from the event schema
			return pathCondition(field, op, c.Value)
		}
		return "", fmt.Errorf("unknown builder field %q", field)
	}
}

// reBuilderPath is a payload path the builder may reference directly: identifiers only, so a value can never
// inject CEL through the field name.
var reBuilderPath = regexp.MustCompile(`^(data|event|account)(\.[A-Za-z_][A-Za-z0-9_]{0,63}){1,8}$`)

func builderTZ(name string) string {
	if tz := strings.TrimSpace(name); tz != "" {
		return tz
	}
	return "UTC"
}

// pathCondition renders a predicate on a schema field (W73b): presence, bool, number, string and list ops.
func pathCondition(path, op string, value any) (string, error) {
	switch op {
	case "exists":
		return fmt.Sprintf(`has(%s)`, path), nil
	case "notExists":
		return fmt.Sprintf(`!has(%s)`, path), nil
	case "includes": // list field contains the value
		s, err := asString(value)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf(`%s in %s`, celQuote(s), path), nil
	case "anyOf": // list field shares an element with the values
		list, err := asStringList(value)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf(`%s.exists(x, x in %s)`, path, celStringList(list)), nil
	}
	if b, ok := value.(bool); ok && (op == "eq" || op == "neq") {
		cmp := "=="
		if op == "neq" {
			cmp = "!="
		}
		return fmt.Sprintf(`%s %s %t`, path, cmp, b), nil
	}
	if n, ok := asNumberLiteral(value); ok {
		cmp, known := map[string]string{"eq": "==", "neq": "!=", "gt": ">", "gte": ">=", "lt": "<", "lte": "<="}[op]
		if !known {
			return "", fmt.Errorf("unsupported numeric op %q", op)
		}
		return fmt.Sprintf(`%s %s %s`, path, cmp, n), nil
	}
	switch op {
	case "gt", "gte", "lt", "lte":
		return "", fmt.Errorf("value must be a number")
	}
	return scalarCondition(path, op, value)
}

// asNumberLiteral renders a JSON number as a CEL double literal (payload numbers decode as doubles).
func asNumberLiteral(v any) (string, bool) {
	var f float64
	switch x := v.(type) {
	case float64:
		f = x
	case int:
		f = float64(x)
	case int64:
		f = float64(x)
	case json.Number:
		var err error
		if f, err = x.Float64(); err != nil {
			return "", false
		}
	default:
		return "", false
	}
	s := strconv.FormatFloat(f, 'f', -1, 64)
	if !strings.ContainsAny(s, ".eE") {
		s += ".0"
	}
	return s, true
}

// boolHelperCondition renders a bool helper: exists / notExists, or eq / neq against a bool value
// (omitted value = true). {"op": "eq", "value": false} is the negation.
func boolHelperCondition(call, op string, value any) (string, error) {
	want := true
	if value != nil {
		b, ok := value.(bool)
		if !ok {
			return "", fmt.Errorf("value must be a boolean")
		}
		want = b
	}
	switch op {
	case "exists":
		return call, nil
	case "notExists":
		return "!" + call, nil
	case "eq":
		if want {
			return call, nil
		}
		return "!" + call, nil
	case "neq":
		if want {
			return "!" + call, nil
		}
		return call, nil
	default:
		return "", fmt.Errorf("unsupported op %q for a boolean field", op)
	}
}

func bodyCondition(op string, value any) (string, error) {
	switch op {
	case "contains":
		s, err := asString(value)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf(`textContains(%s)`, celQuote(s)), nil
	case "matches":
		s, err := asString(value)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf(`textMatches(%s)`, celQuote(s)), nil
	case "exists":
		return `text() != ""`, nil
	case "notExists":
		return `text() == ""`, nil
	default:
		return "", fmt.Errorf("unsupported op %q for body", op)
	}
}

func numericCondition(left, op string, value any, existsClose bool) (string, error) {
	n, err := asInt(value)
	if err != nil {
		return "", err
	}
	suffix := ""
	if existsClose {
		suffix = ")"
	}
	switch op {
	case "eq":
		return fmt.Sprintf(`%s == %d%s`, left, n, suffix), nil
	case "neq":
		return fmt.Sprintf(`%s != %d%s`, left, n, suffix), nil
	case "gt":
		return fmt.Sprintf(`%s > %d%s`, left, n, suffix), nil
	case "gte":
		return fmt.Sprintf(`%s >= %d%s`, left, n, suffix), nil
	case "lt":
		return fmt.Sprintf(`%s < %d%s`, left, n, suffix), nil
	case "lte":
		return fmt.Sprintf(`%s <= %d%s`, left, n, suffix), nil
	default:
		return "", fmt.Errorf("unsupported numeric op %q", op)
	}
}

func asBusinessHours(v any) (tz, window, days string, err error) {
	switch x := v.(type) {
	case []any:
		if len(x) != 3 {
			return "", "", "", fmt.Errorf("businessHours value needs [tz, window, days]")
		}
		tz, _ = x[0].(string)
		window, _ = x[1].(string)
		days, _ = x[2].(string)
	case map[string]any:
		tz, _ = x["tz"].(string)
		window, _ = x["window"].(string)
		days, _ = x["days"].(string)
	default:
		return "", "", "", fmt.Errorf("businessHours value must be object or list")
	}
	if tz == "" || window == "" || days == "" {
		return "", "", "", fmt.Errorf("businessHours tz, window and days are required")
	}
	return tz, window, days, nil
}

func headerCondition(name, op string, value any) (string, error) {
	name = strings.TrimSpace(strings.ToLower(name))
	if name == "" {
		return "", fmt.Errorf("header conditions require name")
	}
	hn := celQuote(name)
	switch op {
	case "exists":
		return fmt.Sprintf(`hasHeader(%s)`, hn), nil
	case "notExists":
		return fmt.Sprintf(`!hasHeader(%s)`, hn), nil
	case "eq", "neq", "contains", "startsWith", "endsWith", "matches":
		s, err := asString(value)
		if err != nil {
			return "", err
		}
		q := celQuote(s)
		left := fmt.Sprintf(`header(%s)`, hn)
		return stringOp(left, op, q), nil
	case "in":
		list, err := asStringList(value)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf(`header(%s) in %s`, hn, celStringList(list)), nil
	default:
		return "", fmt.Errorf("unsupported op %q for header", op)
	}
}

func scalarCondition(left, op string, value any) (string, error) {
	switch op {
	case "exists":
		return fmt.Sprintf(`%s != ""`, left), nil
	case "notExists":
		return fmt.Sprintf(`%s == ""`, left), nil
	case "eq", "neq", "contains", "startsWith", "endsWith", "matches":
		s, err := asString(value)
		if err != nil {
			return "", err
		}
		return stringOp(left, op, celQuote(s)), nil
	case "in":
		list, err := asStringList(value)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf(`%s in %s`, left, celStringList(list)), nil
	default:
		return "", fmt.Errorf("unsupported op %q", op)
	}
}

func toDomainCondition(op string, value any) (string, error) {
	switch op {
	case "eq":
		s, err := asString(value)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf(`data.to.exists(a, domain(a) == %s)`, celQuote(s)), nil
	case "neq":
		s, err := asString(value)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf(`!data.to.exists(a, domain(a) == %s)`, celQuote(s)), nil
	case "in":
		list, err := asStringList(value)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf(`data.to.exists(a, domain(a) in %s)`, celStringList(list)), nil
	case "contains", "startsWith", "endsWith", "matches":
		s, err := asString(value)
		if err != nil {
			return "", err
		}
		inner := stringOp("domain(a)", op, celQuote(s))
		return fmt.Sprintf(`data.to.exists(a, %s)`, inner), nil
	case "exists":
		return `size(data.to) > 0`, nil
	case "notExists":
		return `size(data.to) == 0`, nil
	default:
		return "", fmt.Errorf("unsupported op %q for to.domain", op)
	}
}

func flagsCondition(op string, value any) (string, error) {
	switch op {
	case "exists":
		return `size(data.flags) > 0`, nil
	case "notExists":
		return `size(data.flags) == 0`, nil
	case "contains", "eq":
		s, err := asString(value)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf(`%s in data.flags`, celQuote(s)), nil
	case "in":
		list, err := asStringList(value)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf(`data.flags.exists(f, f in %s)`, celStringList(list)), nil
	default:
		return "", fmt.Errorf("unsupported op %q for flags", op)
	}
}

func attachmentsCondition(op string, value any) (string, error) {
	switch op {
	case "exists":
		return `size(data.attachments) > 0`, nil
	case "notExists":
		return `size(data.attachments) == 0`, nil
	case "eq", "neq":
		n, err := asInt(value)
		if err != nil {
			return "", err
		}
		cmp := "=="
		if op == "neq" {
			cmp = "!="
		}
		return fmt.Sprintf(`size(data.attachments) %s %d`, cmp, n), nil
	default:
		return "", fmt.Errorf("unsupported op %q for attachments", op)
	}
}

func stringOp(left, op, quotedRight string) string {
	switch op {
	case "eq":
		return fmt.Sprintf(`%s == %s`, left, quotedRight)
	case "neq":
		return fmt.Sprintf(`%s != %s`, left, quotedRight)
	case "contains":
		return fmt.Sprintf(`%s.contains(%s)`, left, quotedRight)
	case "startsWith":
		return fmt.Sprintf(`%s.startsWith(%s)`, left, quotedRight)
	case "endsWith":
		return fmt.Sprintf(`%s.endsWith(%s)`, left, quotedRight)
	case "matches":
		return fmt.Sprintf(`%s.matches(%s)`, left, quotedRight)
	default:
		return ""
	}
}

func celQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func celStringList(list []string) string {
	parts := make([]string, len(list))
	for i, s := range list {
		parts[i] = celQuote(s)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

func asString(v any) (string, error) {
	switch x := v.(type) {
	case nil:
		return "", fmt.Errorf("value is required")
	case string:
		return x, nil
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64), nil
	case json.Number:
		return x.String(), nil
	case bool:
		return strconv.FormatBool(x), nil
	default:
		return "", fmt.Errorf("value must be a string")
	}
}

func asInt(v any) (int64, error) {
	switch x := v.(type) {
	case float64:
		return int64(x), nil
	case int:
		return int64(x), nil
	case int64:
		return x, nil
	case json.Number:
		return x.Int64()
	case string:
		return strconv.ParseInt(x, 10, 64)
	default:
		return 0, fmt.Errorf("value must be an integer")
	}
}

func asStringList(v any) ([]string, error) {
	switch x := v.(type) {
	case []string:
		if len(x) == 0 {
			return nil, fmt.Errorf("in requires a non-empty list")
		}
		return x, nil
	case []any:
		if len(x) == 0 {
			return nil, fmt.Errorf("in requires a non-empty list")
		}
		out := make([]string, 0, len(x))
		for _, e := range x {
			s, err := asString(e)
			if err != nil {
				return nil, err
			}
			out = append(out, s)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("in requires a list of strings")
	}
}

// Expressible reports whether cel was emitted by BuilderToCEL (best-effort reverse).
func Expressible(cel string) BuilderExpressibleResult {
	cel = strings.TrimSpace(cel)
	if cel == "" {
		return BuilderExpressibleResult{Expressible: false}
	}
	if cel == "true" {
		return BuilderExpressibleResult{
			Expressible: true,
			Builder:     &Builder{Match: MatchAll, Conditions: []BuilderCondition{}},
		}
	}
	match := MatchAll
	var parts []string
	if strings.Contains(cel, " || ") {
		match = MatchAny
		parts = splitTopLevel(cel, " || ")
	} else if strings.Contains(cel, " && ") {
		parts = splitTopLevel(cel, " && ")
	} else {
		parts = []string{cel}
	}
	conds := make([]BuilderCondition, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if len(p) >= 2 && p[0] == '(' && p[len(p)-1] == ')' {
			p = p[1 : len(p)-1]
		}
		c, ok := parseCondition(p)
		if !ok {
			return BuilderExpressibleResult{Expressible: false}
		}
		conds = append(conds, c)
	}
	b := Builder{Match: match, Conditions: conds}
	// Round-trip check: only accept if we would emit the same CEL.
	got, err := BuilderToCEL(b)
	if err != nil || got != cel {
		return BuilderExpressibleResult{Expressible: false}
	}
	return BuilderExpressibleResult{Expressible: true, Builder: &b}
}

func splitTopLevel(s, sep string) []string {
	var out []string
	depth := 0
	start := 0
	for i := 0; i+len(sep) <= len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		}
		if depth == 0 && s[i:i+len(sep)] == sep {
			out = append(out, s[start:i])
			start = i + len(sep)
			i += len(sep) - 1
		}
	}
	out = append(out, s[start:])
	return out
}

func parseCondition(expr string) (BuilderCondition, bool) {
	expr = strings.TrimSpace(expr)
	if m := reHeaderExists.FindStringSubmatch(expr); m != nil {
		return BuilderCondition{Field: "header", Name: unquoteCEL(m[1]), Op: "exists"}, true
	}
	if m := reHeaderNot.FindStringSubmatch(expr); m != nil {
		return BuilderCondition{Field: "header", Name: unquoteCEL(m[1]), Op: "notExists"}, true
	}
	if m := reHeaderEq.FindStringSubmatch(expr); m != nil {
		return BuilderCondition{Field: "header", Name: unquoteCEL(m[1]), Op: "eq", Value: unquoteCEL(m[2])}, true
	}
	if m := reHeaderNeq.FindStringSubmatch(expr); m != nil {
		return BuilderCondition{Field: "header", Name: unquoteCEL(m[1]), Op: "neq", Value: unquoteCEL(m[2])}, true
	}
	if m := reHeaderContains.FindStringSubmatch(expr); m != nil {
		return BuilderCondition{Field: "header", Name: unquoteCEL(m[1]), Op: "contains", Value: unquoteCEL(m[2])}, true
	}
	if m := reHeaderStarts.FindStringSubmatch(expr); m != nil {
		return BuilderCondition{Field: "header", Name: unquoteCEL(m[1]), Op: "startsWith", Value: unquoteCEL(m[2])}, true
	}
	if m := reHeaderEnds.FindStringSubmatch(expr); m != nil {
		return BuilderCondition{Field: "header", Name: unquoteCEL(m[1]), Op: "endsWith", Value: unquoteCEL(m[2])}, true
	}
	if m := reHeaderMatches.FindStringSubmatch(expr); m != nil {
		return BuilderCondition{Field: "header", Name: unquoteCEL(m[1]), Op: "matches", Value: unquoteCEL(m[2])}, true
	}
	if m := reHeaderIn.FindStringSubmatch(expr); m != nil {
		list, ok := parseCELStringList(m[2])
		if !ok {
			return BuilderCondition{}, false
		}
		return BuilderCondition{Field: "header", Name: unquoteCEL(m[1]), Op: "in", Value: list}, true
	}

	fieldMap := []struct {
		prefix string
		field  string
	}{
		{"type", "type"},
		{"account.id", "account.id"},
		{"account.externalId", "account.externalId"},
		{"data.mailbox.path", "mailbox.path"},
		{"data.mailbox.specialUse", "mailbox.specialUse"},
		{"data.from.address", "from.address"},
		{"domain(data.from)", "from.domain"},
		{"data.subject", "subject"},
		{"spf()", "auth.spf"},
		{"dkim()", "auth.dkim"},
		{"dmarc()", "auth.dmarc"},
		{"data.authResults.spf", "auth.spf"}, // pre-W73 generated CEL still parses back
		{"data.authResults.dkim", "auth.dkim"},
		{"data.authResults.dmarc", "auth.dmarc"},
	}
	for _, fm := range fieldMap {
		if c, ok := parseScalarLeft(expr, fm.prefix, fm.field); ok {
			return c, true
		}
	}
	if c, ok := parseToDomain(expr); ok {
		return c, true
	}
	if c, ok := parseFlags(expr); ok {
		return c, true
	}
	if c, ok := parseAttachments(expr); ok {
		return c, true
	}
	return BuilderCondition{}, false
}

func parseScalarLeft(expr, left, field string) (BuilderCondition, bool) {
	if expr == left+` != ""` {
		return BuilderCondition{Field: field, Op: "exists"}, true
	}
	if expr == left+` == ""` {
		return BuilderCondition{Field: field, Op: "notExists"}, true
	}
	ops := []struct {
		mid string
		op  string
	}{
		{" == ", "eq"},
		{" != ", "neq"},
		{".contains(", "contains"},
		{".startsWith(", "startsWith"},
		{".endsWith(", "endsWith"},
		{".matches(", "matches"},
		{" in ", "in"},
	}
	for _, o := range ops {
		if !strings.HasPrefix(expr, left) {
			continue
		}
		rest := expr[len(left):]
		switch o.op {
		case "eq", "neq":
			if !strings.HasPrefix(rest, o.mid) {
				continue
			}
			val := strings.TrimSpace(rest[len(o.mid):])
			if s, ok := parseCELString(val); ok {
				return BuilderCondition{Field: field, Op: o.op, Value: s}, true
			}
		case "contains", "startsWith", "endsWith", "matches":
			if !strings.HasPrefix(rest, o.mid) || !strings.HasSuffix(rest, ")") {
				continue
			}
			inner := rest[len(o.mid) : len(rest)-1]
			if s, ok := parseCELString(inner); ok {
				return BuilderCondition{Field: field, Op: o.op, Value: s}, true
			}
		case "in":
			if !strings.HasPrefix(rest, o.mid) {
				continue
			}
			list, ok := parseCELStringList(strings.TrimSpace(rest[len(o.mid):]))
			if ok {
				return BuilderCondition{Field: field, Op: "in", Value: list}, true
			}
		}
	}
	return BuilderCondition{}, false
}

func parseToDomain(expr string) (BuilderCondition, bool) {
	if expr == `size(data.to) > 0` {
		return BuilderCondition{Field: "to.domain", Op: "exists"}, true
	}
	if expr == `size(data.to) == 0` {
		return BuilderCondition{Field: "to.domain", Op: "notExists"}, true
	}
	const pref = `data.to.exists(a, domain(a) == `
	if strings.HasPrefix(expr, pref) && strings.HasSuffix(expr, ")") {
		inner := expr[len(pref) : len(expr)-1]
		if s, ok := parseCELString(inner); ok {
			return BuilderCondition{Field: "to.domain", Op: "eq", Value: s}, true
		}
	}
	const npref = `!data.to.exists(a, domain(a) == `
	if strings.HasPrefix(expr, npref) && strings.HasSuffix(expr, ")") {
		inner := expr[len(npref) : len(expr)-1]
		if s, ok := parseCELString(inner); ok {
			return BuilderCondition{Field: "to.domain", Op: "neq", Value: s}, true
		}
	}
	const ipref = `data.to.exists(a, domain(a) in `
	if strings.HasPrefix(expr, ipref) && strings.HasSuffix(expr, ")") {
		inner := expr[len(ipref) : len(expr)-1]
		if list, ok := parseCELStringList(inner); ok {
			return BuilderCondition{Field: "to.domain", Op: "in", Value: list}, true
		}
	}
	for _, o := range []struct{ mid, op string }{
		{`data.to.exists(a, domain(a).contains(`, "contains"},
		{`data.to.exists(a, domain(a).startsWith(`, "startsWith"},
		{`data.to.exists(a, domain(a).endsWith(`, "endsWith"},
		{`data.to.exists(a, domain(a).matches(`, "matches"},
	} {
		if strings.HasPrefix(expr, o.mid) && strings.HasSuffix(expr, "))") {
			inner := expr[len(o.mid) : len(expr)-2]
			if s, ok := parseCELString(inner); ok {
				return BuilderCondition{Field: "to.domain", Op: o.op, Value: s}, true
			}
		}
	}
	return BuilderCondition{}, false
}

func parseFlags(expr string) (BuilderCondition, bool) {
	if expr == `size(data.flags) > 0` {
		return BuilderCondition{Field: "flags", Op: "exists"}, true
	}
	if expr == `size(data.flags) == 0` {
		return BuilderCondition{Field: "flags", Op: "notExists"}, true
	}
	if strings.HasSuffix(expr, ` in data.flags`) {
		left := strings.TrimSuffix(expr, ` in data.flags`)
		if s, ok := parseCELString(strings.TrimSpace(left)); ok {
			return BuilderCondition{Field: "flags", Op: "contains", Value: s}, true
		}
	}
	const pref = `data.flags.exists(f, f in `
	if strings.HasPrefix(expr, pref) && strings.HasSuffix(expr, ")") {
		inner := expr[len(pref) : len(expr)-1]
		if list, ok := parseCELStringList(inner); ok {
			return BuilderCondition{Field: "flags", Op: "in", Value: list}, true
		}
	}
	return BuilderCondition{}, false
}

func parseAttachments(expr string) (BuilderCondition, bool) {
	if expr == `size(data.attachments) > 0` {
		return BuilderCondition{Field: "attachments", Op: "exists"}, true
	}
	if expr == `size(data.attachments) == 0` {
		return BuilderCondition{Field: "attachments", Op: "notExists"}, true
	}
	const pref = `size(data.attachments) `
	if strings.HasPrefix(expr, pref) {
		rest := expr[len(pref):]
		for _, o := range []struct{ mid, op string }{{"== ", "eq"}, {"!= ", "neq"}} {
			if strings.HasPrefix(rest, o.mid) {
				n, err := strconv.ParseInt(strings.TrimSpace(rest[len(o.mid):]), 10, 64)
				if err == nil {
					return BuilderCondition{Field: "attachments", Op: o.op, Value: float64(n)}, true
				}
			}
		}
	}
	return BuilderCondition{}, false
}

func parseCELString(s string) (string, bool) {
	s = strings.TrimSpace(s)
	m := reBuilderQuoted.FindStringSubmatch(s)
	if m == nil {
		return "", false
	}
	return unquoteCEL(m[1]), true
}

func parseCELStringList(s string) ([]string, bool) {
	s = strings.TrimSpace(s)
	if len(s) < 2 || s[0] != '[' || s[len(s)-1] != ']' {
		return nil, false
	}
	inner := strings.TrimSpace(s[1 : len(s)-1])
	if inner == "" {
		return nil, false
	}
	var out []string
	for _, part := range splitListItems(inner) {
		v, ok := parseCELString(part)
		if !ok {
			return nil, false
		}
		out = append(out, v)
	}
	return out, true
}

func splitListItems(s string) []string {
	var out []string
	var b strings.Builder
	inStr := false
	esc := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inStr {
			b.WriteByte(c)
			if esc {
				esc = false
				continue
			}
			if c == '\\' {
				esc = true
				continue
			}
			if c == '"' {
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
			b.WriteByte(c)
		case ',':
			out = append(out, strings.TrimSpace(b.String()))
			b.Reset()
		default:
			b.WriteByte(c)
		}
	}
	if b.Len() > 0 {
		out = append(out, strings.TrimSpace(b.String()))
	}
	return out
}

func unquoteCEL(s string) string {
	var b strings.Builder
	esc := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if esc {
			b.WriteByte(c)
			esc = false
			continue
		}
		if c == '\\' {
			esc = true
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

// BuilderFromMap decodes a loose map (Mongo / JSON) into Builder.
func BuilderFromMap(m map[string]any) (Builder, error) {
	if m == nil {
		return Builder{}, errs.New(errs.CodeValidationFailed, "builder is required")
	}
	b, err := json.Marshal(m)
	if err != nil {
		return Builder{}, err
	}
	var out Builder
	if err := json.Unmarshal(b, &out); err != nil {
		return Builder{}, errs.New(errs.CodeValidationFailed, "invalid builder").WithSafe("err", err.Error())
	}
	return out, nil
}

// BuilderToMap encodes Builder as map[string]any for storage.
func BuilderToMap(b Builder) map[string]any {
	raw, _ := json.Marshal(b)
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	return m
}
