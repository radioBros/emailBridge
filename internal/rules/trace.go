package rules

import (
	"context"
	"strings"
	"time"

	"cel.dev/cel-go/common/types"
	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/radiobros/emailbridge/internal/errs"
)

// TraceTerm is one top-level && / || term (or builder condition) with its result.
type TraceTerm struct {
	Expr   string `json:"expr" doc:"The term" example:"hasAttachments()"`
	Result bool   `json:"result" doc:"Its value" example:"true"`
}

// BuildTrace evaluates each top-level conjunction/disjunction term of filterSrc.
func (e *Engine) BuildTrace(ctx context.Context, filterSrc string, event map[string]any, account map[string]any, env map[string]string, lists map[string]map[string]struct{}) []TraceTerm {
	filterSrc = strings.TrimSpace(filterSrc)
	if filterSrc == "" {
		return nil
	}
	parts, sep := splitFilterTerms(filterSrc)
	if len(parts) <= 1 && sep == "" {
		// Single expression: one term = whole filter.
		ok, _ := e.evalBool(ctx, filterSrc, event, account, env, lists)
		return []TraceTerm{{Expr: filterSrc, Result: ok}}
	}
	out := make([]TraceTerm, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		ok, _ := e.evalBool(ctx, p, event, account, env, lists)
		out = append(out, TraceTerm{Expr: p, Result: ok})
	}
	return out
}

func splitFilterTerms(src string) (parts []string, sep string) {
	if strings.Contains(src, " || ") {
		return splitTopLevel(src, " || "), "||"
	}
	if strings.Contains(src, " && ") {
		return splitTopLevel(src, " && "), "&&"
	}
	return []string{src}, ""
}

func (e *Engine) evalBool(ctx context.Context, src string, event, account map[string]any, env map[string]string, lists map[string]map[string]struct{}) (bool, error) {
	prg, err := e.Compile(src, "")
	if err != nil {
		return false, err
	}
	matched, _, _, err := e.EvalWithLists(ctx, prg, event, account, env, lists)
	return matched, err
}

// EvalWithLists is Eval with an optional named-list snapshot.
func (e *Engine) EvalWithLists(ctx context.Context, p *Program, event map[string]any, account map[string]any, env map[string]string, lists map[string]map[string]struct{}) (matched bool, mapped any, cost uint64, err error) {
	if p == nil || p.Filter == nil {
		return false, nil, 0, nil
	}
	if event == nil {
		event = map[string]any{}
	}
	if env == nil {
		env = map[string]string{}
	}
	data, _ := event["data"].(map[string]any)
	if data == nil {
		data = map[string]any{}
	}
	typ, _ := event["type"].(string)
	account = normalizeAccount(account, event, data)
	eventTime := parseEventTime(event, data, time.Now().UTC())
	evalMu.Lock()
	defer evalMu.Unlock()
	setEvalBagTyped(typ, data, lists, eventTime, e.textMax)
	act := map[string]any{
		"event":   event,
		"type":    typ,
		"data":    data,
		"account": account,
		"env":     env,
		"now":     eventTime,
	}

	evalCtx := ctx
	var cancel context.CancelFunc
	if e.timeLimit > 0 {
		evalCtx, cancel = context.WithTimeout(ctx, e.timeLimit)
		defer cancel()
	}

	out, det, err := p.Filter.ContextEval(evalCtx, act)
	cost = costOf(det)
	if err != nil {
		return false, nil, cost, mapEvalErr(err)
	}
	b, ok := out.(types.Bool)
	if !ok {
		return false, nil, cost, errs.New(errs.CodeRouteEvalError, "filter did not return a bool")
	}
	if !bool(b) {
		return false, nil, cost, nil
	}
	if p.Map == nil {
		return true, nil, cost, nil
	}
	setEvalBagTyped(typ, data, lists, eventTime, e.textMax)
	mout, mdet, err := p.Map.ContextEval(evalCtx, act)
	cost += costOf(mdet)
	if err != nil {
		return true, nil, cost, mapEvalErr(err)
	}
	native, err := valueToJSON(mout)
	if err != nil {
		return true, nil, cost, err
	}
	return true, native, cost, nil
}

// normalizeAccount guarantees the `account` variable always carries id, externalId, email and name as
// strings ("" when unknown — never null or missing), so expressions such as
// `account.externalId != "" ? account.externalId : account.id` never fail with "no such key". The id falls
// back to the event's account (ebaccount / data.accountId) when no account document was loaded, and
// externalId to data.externalId. `metadata` (G4) is always a map — empty when the account has none — so
// `has(account.metadata.crm)` and `"crm" in account.metadata` work on every event.
func normalizeAccount(in map[string]any, event, data map[string]any) map[string]any {
	out := make(map[string]any, len(in)+5)
	for k, v := range in {
		out[k] = v
	}
	for _, k := range []string{"id", "externalId", "email", "name"} {
		switch v := out[k].(type) {
		case string: // already the right shape
		case *string:
			if v != nil {
				out[k] = *v
			} else {
				out[k] = ""
			}
		default:
			out[k] = ""
		}
	}
	if out["id"] == "" {
		for _, v := range []any{event["ebaccount"], event["ebAccount"], data["accountId"]} {
			if s, ok := v.(string); ok && s != "" {
				out["id"] = s
				break
			}
		}
	}
	if out["externalId"] == "" {
		if s, ok := data["externalId"].(string); ok {
			out["externalId"] = s
		}
	}
	out["metadata"] = accountMetadata(out["metadata"])
	return out
}

// accountMetadata returns the account metadata as a map[string]any (a copy; empty when absent or not a map).
func accountMetadata(v any) map[string]any {
	var src map[string]any
	switch m := v.(type) {
	case map[string]any:
		src = m
	case bson.M:
		src = m
	}
	out := make(map[string]any, len(src))
	for k, val := range src {
		out[k] = val
	}
	return out
}
