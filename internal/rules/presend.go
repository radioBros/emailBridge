package rules

import (
	"context"
	"fmt"
	"strings"
	"time"

	"cel.dev/cel-go/cel"
	"cel.dev/cel-go/common/types"
	"cel.dev/cel-go/common/types/ref"
	"cel.dev/cel-go/ext"
)

// PreSendAction is the result of a pre-send CEL policy (P5.5).
type PreSendAction string

// Pre-send actions.
const (
	PreSendAllow  PreSendAction = "allow"
	PreSendHold   PreSendAction = "hold"
	PreSendReject PreSendAction = "reject"
)

// PreSendResult is the evaluated policy decision.
type PreSendResult struct {
	Action PreSendAction
	Reason string
}

// PreSendInput is the activation map for a pre-send expression.
type PreSendInput struct {
	Envelope map[string]any
	Headers  map[string]string
	Size     int64
	Account  map[string]any
	Now      time.Time
}

// PreSendEngine compiles and evaluates pre-send CEL (S0: cel-go only here).
type PreSendEngine struct {
	env       *cel.Env
	costLimit uint64
	timeLimit time.Duration
}

// NewPreSendEngine builds a CEL env for pre-send policies.
func NewPreSendEngine(o EngineOptions) (*PreSendEngine, error) {
	env, err := cel.NewEnv(
		cel.ParserRecursionLimit(32),
		cel.ParserExpressionSizeLimit(maxExprBytes),
		cel.Variable("envelope", cel.MapType(cel.StringType, cel.DynType)),
		cel.Variable("headers", cel.MapType(cel.StringType, cel.StringType)),
		cel.Variable("size", cel.IntType),
		cel.Variable("account", cel.MapType(cel.StringType, cel.DynType)),
		cel.Variable("now", cel.TimestampType),
		ext.Strings(),
		ext.Encoders(),
		ext.Math(),
		ext.Lists(),
		cel.Function("domain",
			cel.Overload("presend_domain_dyn",
				[]*cel.Type{cel.DynType},
				cel.StringType,
				cel.UnaryBinding(domainFn),
			),
		),
	)
	if err != nil {
		return nil, err
	}
	cost := o.CostLimit
	if cost == 0 {
		cost = envCostLimit()
	}
	tl := o.TimeLimit
	if tl == 0 {
		tl = envTimeLimit()
	}
	return &PreSendEngine{env: env, costLimit: cost, timeLimit: tl}, nil
}

// Eval compiles (uncached) and evaluates expr against in. Empty expr → allow.
// Compile / runtime failures return allow + error (caller may fail-open).
func (e *PreSendEngine) Eval(ctx context.Context, expr string, in PreSendInput) (PreSendResult, error) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return PreSendResult{Action: PreSendAllow}, nil
	}
	if len(expr) > maxExprBytes {
		return PreSendResult{Action: PreSendAllow}, fmt.Errorf("expression exceeds 4 KiB")
	}
	ast, iss := e.env.Compile(expr)
	if iss.Err() != nil {
		return PreSendResult{Action: PreSendAllow}, iss.Err()
	}
	prg, err := e.env.Program(ast,
		cel.CostLimit(e.costLimit),
		cel.EvalOptions(cel.OptOptimize),
	)
	if err != nil {
		return PreSendResult{Action: PreSendAllow}, err
	}
	now := in.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	headers := in.Headers
	if headers == nil {
		headers = map[string]string{}
	}
	envelope := in.Envelope
	if envelope == nil {
		envelope = map[string]any{}
	}
	account := in.Account
	if account == nil {
		account = map[string]any{}
	}
	actx := ctx
	if e.timeLimit > 0 {
		var cancel context.CancelFunc
		actx, cancel = context.WithTimeout(ctx, e.timeLimit)
		defer cancel()
	}
	out, _, err := prg.ContextEval(actx, map[string]any{
		"envelope": envelope,
		"headers":  headers,
		"size":     in.Size,
		"account":  account,
		"now":      now,
	})
	if err != nil {
		return PreSendResult{Action: PreSendAllow}, err
	}
	return parsePreSendVal(out), nil
}

func parsePreSendVal(v ref.Val) PreSendResult {
	if v == nil || types.IsUnknownOrError(v) {
		return PreSendResult{Action: PreSendAllow}
	}
	switch x := v.Value().(type) {
	case string:
		return PreSendResult{Action: normalizeAction(x)}
	case map[string]any:
		act, _ := x["action"].(string)
		reason, _ := x["reason"].(string)
		return PreSendResult{Action: normalizeAction(act), Reason: reason}
	case map[ref.Val]ref.Val:
		var act, reason string
		for k, vv := range x {
			ks, _ := k.Value().(string)
			switch ks {
			case "action":
				act, _ = vv.Value().(string)
			case "reason":
				reason, _ = vv.Value().(string)
			}
		}
		return PreSendResult{Action: normalizeAction(act), Reason: reason}
	default:
		if s, ok := v.(types.String); ok {
			return PreSendResult{Action: normalizeAction(string(s))}
		}
	}
	return PreSendResult{Action: PreSendAllow}
}

func normalizeAction(s string) PreSendAction {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "hold":
		return PreSendHold
	case "reject":
		return PreSendReject
	default:
		return PreSendAllow
	}
}
