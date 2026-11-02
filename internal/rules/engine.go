package rules

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strconv"
	"strings"
	"time"

	"cel.dev/cel-go/cel"
	"cel.dev/cel-go/common/types"
	"cel.dev/cel-go/common/types/ref"
	"cel.dev/cel-go/common/types/traits"
	"cel.dev/cel-go/ext"
	"cel.dev/cel-go/interpreter"

	"github.com/radiobros/emailbridge/internal/errs"
)

const (
	defaultCostLimit  uint64 = 50_000
	defaultTimeLimit         = 50 * time.Millisecond
	maxMapOutputBytes        = 256 << 10
	maxExprBytes             = 4096
)

// Program is a compiled filter + optional map expression.
type Program struct {
	Filter    cel.Program
	Map       cel.Program // nil when no map expression
	FilterSrc string
	MapSrc    string
}

// Engine compiles and evaluates route expressions (S8 §2–§3, W73 helpers).
type Engine struct {
	env       *cel.Env
	costLimit uint64
	timeLimit time.Duration
	textMax   int
	cache     *programCache
}

// EngineOptions configure an Engine.
type EngineOptions struct {
	CostLimit uint64
	TimeLimit time.Duration
	TextMax   int
	CacheSize int
}

// NewEngine builds a CEL environment per S8 §2 / W73.
func NewEngine(o EngineOptions) (*Engine, error) {
	env, err := newCELEnv()
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
	cs := o.CacheSize
	if cs <= 0 {
		cs = 256
	}
	tm := o.TextMax
	if tm <= 0 {
		tm = defaultTextMax
	}
	return &Engine{env: env, costLimit: cost, timeLimit: tl, textMax: tm, cache: newProgramCache(cs)}, nil
}

func envCostLimit() uint64 {
	if v := os.Getenv("EB_RULES_COST_LIMIT"); v != "" {
		if n, err := strconv.ParseUint(v, 10, 64); err == nil && n > 0 {
			return n
		}
	}
	return defaultCostLimit
}

func envTimeLimit() time.Duration {
	if v := os.Getenv("EB_RULES_TIME_LIMIT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
		if ms, err := strconv.Atoi(v); err == nil && ms > 0 {
			return time.Duration(ms) * time.Millisecond
		}
	}
	return defaultTimeLimit
}

func newCELEnv() (*cel.Env, error) {
	opts := []cel.EnvOption{
		cel.ParserRecursionLimit(32),
		cel.ParserExpressionSizeLimit(maxExprBytes),
		cel.Variable("event", cel.MapType(cel.StringType, cel.DynType)),
		cel.Variable("type", cel.StringType),
		cel.Variable("data", cel.MapType(cel.StringType, cel.DynType)),
		cel.Variable("account", cel.MapType(cel.StringType, cel.DynType)),
		cel.Variable("env", cel.MapType(cel.StringType, cel.StringType)),
		cel.Variable("now", cel.TimestampType),
		ext.Strings(),
		ext.Encoders(),
		ext.Math(),
		ext.Lists(),
		cel.Function("header",
			cel.Overload("header_string",
				[]*cel.Type{cel.StringType}, cel.StringType,
				cel.UnaryBinding(func(name ref.Val) ref.Val {
					n := strings.ToLower(string(name.(types.String)))
					vals := getHeaders()[n]
					if len(vals) == 0 {
						return types.String("")
					}
					return types.String(vals[0])
				}),
			),
		),
		cel.Function("headerValues",
			cel.Overload("headerValues_string",
				[]*cel.Type{cel.StringType}, cel.ListType(cel.StringType),
				cel.UnaryBinding(func(name ref.Val) ref.Val {
					n := strings.ToLower(string(name.(types.String)))
					return types.DefaultTypeAdapter.NativeToValue(getHeaders()[n])
				}),
			),
		),
		cel.Function("hasHeader",
			cel.Overload("hasHeader_string",
				[]*cel.Type{cel.StringType}, cel.BoolType,
				cel.UnaryBinding(func(name ref.Val) ref.Val {
					n := strings.ToLower(string(name.(types.String)))
					_, ok := getHeaders()[n]
					return types.Bool(ok)
				}),
			),
		),
		cel.Function("domain", cel.Overload("domain_dyn", []*cel.Type{cel.DynType}, cel.StringType, cel.UnaryBinding(domainFn))),
		cel.Function("addresses", cel.Overload("addresses_list", []*cel.Type{cel.ListType(cel.DynType)}, cel.ListType(cel.StringType), cel.UnaryBinding(addressesFn))),
		// W73 mail helpers
		cel.Function("isInbox", cel.Overload("isInbox_void", []*cel.Type{}, cel.BoolType, cel.FunctionBinding(isInboxFn))),
		cel.Function("inFolder", cel.Overload("inFolder_string", []*cel.Type{cel.StringType}, cel.BoolType, cel.UnaryBinding(inFolderFn))),
		cel.Function("hasLabel", cel.Overload("hasLabel_string", []*cel.Type{cel.StringType}, cel.BoolType, cel.UnaryBinding(hasLabelFn))),
		cel.Function("labels", cel.Overload("labels_void", []*cel.Type{}, cel.ListType(cel.StringType), cel.FunctionBinding(labelsFn))),
		cel.Function("text", cel.Overload("text_void", []*cel.Type{}, cel.StringType, cel.FunctionBinding(textFn))),
		cel.Function("textContains", cel.Overload("textContains_string", []*cel.Type{cel.StringType}, cel.BoolType, cel.UnaryBinding(textContainsFn))),
		cel.Function("textMatches", cel.Overload("textMatches_string", []*cel.Type{cel.StringType}, cel.BoolType, cel.UnaryBinding(textMatchesFn))),
		cel.Function("subject", cel.Overload("subject_void", []*cel.Type{}, cel.StringType, cel.FunctionBinding(subjectFn))),
		cel.Function("subjectContains", cel.Overload("subjectContains_string", []*cel.Type{cel.StringType}, cel.BoolType, cel.UnaryBinding(subjectContainsFn))),
		cel.Function("subjectMatches", cel.Overload("subjectMatches_string", []*cel.Type{cel.StringType}, cel.BoolType, cel.UnaryBinding(subjectMatchesFn))),
		cel.Function("hasAttachments", cel.Overload("hasAttachments_void", []*cel.Type{}, cel.BoolType, cel.FunctionBinding(hasAttachmentsFn))),
		cel.Function("attachments", cel.Overload("attachments_void", []*cel.Type{}, cel.ListType(cel.MapType(cel.StringType, cel.DynType)), cel.FunctionBinding(attachmentsFn))),
		cel.Function("hasAttachment", cel.Overload("hasAttachment_string", []*cel.Type{cel.StringType}, cel.BoolType, cel.UnaryBinding(hasAttachmentFn))),
		cel.Function("attachmentTypes", cel.Overload("attachmentTypes_void", []*cel.Type{}, cel.ListType(cel.StringType), cel.FunctionBinding(attachmentTypesFn))),
		cel.Function("from", cel.Overload("from_void", []*cel.Type{}, cel.StringType, cel.FunctionBinding(fromFn))),
		cel.Function("fromDomain", cel.Overload("fromDomain_void", []*cel.Type{}, cel.StringType, cel.FunctionBinding(fromDomainFn))),
		cel.Function("toAddresses", cel.Overload("toAddresses_void", []*cel.Type{}, cel.ListType(cel.StringType), cel.FunctionBinding(toAddressesFn))),
		cel.Function("ccAddresses", cel.Overload("ccAddresses_void", []*cel.Type{}, cel.ListType(cel.StringType), cel.FunctionBinding(ccAddressesFn))),
		cel.Function("inDomains", cel.Overload("inDomains_list", []*cel.Type{cel.ListType(cel.StringType)}, cel.BoolType, cel.UnaryBinding(inDomainsFn))),
		cel.Function("replyTo", cel.Overload("replyTo_void", []*cel.Type{}, cel.StringType, cel.FunctionBinding(replyToFn))),
		cel.Function("isReply", cel.Overload("isReply_void", []*cel.Type{}, cel.BoolType, cel.FunctionBinding(isReplyFn))),
		cel.Function("isForward", cel.Overload("isForward_void", []*cel.Type{}, cel.BoolType, cel.FunctionBinding(isForwardFn))),
		cel.Function("isAutoReply", cel.Overload("isAutoReply_void", []*cel.Type{}, cel.BoolType, cel.FunctionBinding(isAutoReplyFn))),
		cel.Function("isBounce", cel.Overload("isBounce_void", []*cel.Type{}, cel.BoolType, cel.FunctionBinding(isBounceFn))),
		cel.Function("spf", cel.Overload("spf_void", []*cel.Type{}, cel.StringType, cel.FunctionBinding(spfFn))),
		cel.Function("dkim", cel.Overload("dkim_void", []*cel.Type{}, cel.StringType, cel.FunctionBinding(dkimFn))),
		cel.Function("dmarc", cel.Overload("dmarc_void", []*cel.Type{}, cel.StringType, cel.FunctionBinding(dmarcFn))),
		cel.Function("spamScore", cel.Overload("spamScore_void", []*cel.Type{}, cel.DoubleType, cel.FunctionBinding(spamScoreFn))),
		cel.Function("pecType", cel.Overload("pecType_void", []*cel.Type{}, cel.StringType, cel.FunctionBinding(pecTypeFn))),
		cel.Function("isPecReceipt", cel.Overload("isPecReceipt_void", []*cel.Type{}, cel.BoolType, cel.FunctionBinding(isPecReceiptFn))),
		cel.Function("localTime", cel.Overload("localTime_string", []*cel.Type{cel.StringType}, cel.TimestampType, cel.UnaryBinding(localTimeFn))),
		cel.Function("hourOf", cel.Overload("hourOf_ts", []*cel.Type{cel.TimestampType}, cel.IntType, cel.UnaryBinding(hourOfFn))),
		cel.Function("weekdayOf", cel.Overload("weekdayOf_ts", []*cel.Type{cel.TimestampType}, cel.IntType, cel.UnaryBinding(weekdayOfFn))),
		cel.Function("businessHours", cel.Overload("businessHours_sss",
			[]*cel.Type{cel.StringType, cel.StringType, cel.StringType}, cel.BoolType,
			cel.FunctionBinding(businessHoursFn))),
		cel.Function("inList", cel.Overload("inList_ss",
			[]*cel.Type{cel.StringType, cel.StringType}, cel.BoolType,
			cel.BinaryBinding(inListFn))),
		cel.Function("merge", cel.Overload("merge_mm",
			[]*cel.Type{cel.MapType(cel.StringType, cel.DynType), cel.MapType(cel.StringType, cel.DynType)},
			cel.MapType(cel.StringType, cel.DynType), cel.BinaryBinding(mergeFn))),
		cel.Function("pick", cel.Overload("pick_ml",
			[]*cel.Type{cel.MapType(cel.StringType, cel.DynType), cel.ListType(cel.StringType)},
			cel.MapType(cel.StringType, cel.DynType), cel.BinaryBinding(pickFn))),
		cel.Function("omit", cel.Overload("omit_ml",
			[]*cel.Type{cel.MapType(cel.StringType, cel.DynType), cel.ListType(cel.StringType)},
			cel.MapType(cel.StringType, cel.DynType), cel.BinaryBinding(omitFn))),
	}
	return cel.NewEnv(opts...)
}

// Compile type-checks filter (must be bool) and optional mapExpr.
func (e *Engine) Compile(filter, mapExpr string) (*Program, error) {
	if strings.TrimSpace(filter) == "" {
		return nil, compileErr("filter", "filter expression is required", 0, 0)
	}
	if len(filter) > maxExprBytes {
		return nil, compileErr("filter", "expression exceeds 4 KiB", 0, 0)
	}
	filterAst, err := e.compileOne("filter", filter, cel.BoolType)
	if err != nil {
		return nil, err
	}
	filterPrg, err := e.programOf(filterAst)
	if err != nil {
		return nil, compileErr("filter", err.Error(), 0, 0)
	}
	p := &Program{Filter: filterPrg, FilterSrc: filter}
	if strings.TrimSpace(mapExpr) == "" {
		return p, nil
	}
	if len(mapExpr) > maxExprBytes {
		return nil, compileErr("map", "expression exceeds 4 KiB", 0, 0)
	}
	mapAst, err := e.compileOne("map", mapExpr, nil)
	if err != nil {
		return nil, err
	}
	mapPrg, err := e.programOf(mapAst)
	if err != nil {
		return nil, compileErr("map", err.Error(), 0, 0)
	}
	p.Map = mapPrg
	p.MapSrc = mapExpr
	return p, nil
}

func (e *Engine) compileOne(path, src string, want *cel.Type) (*cel.Ast, error) {
	ast, iss := e.env.Compile(src)
	if iss.Err() != nil {
		line, col, msg := issuePos(iss)
		return nil, compileErr(path, msg, line, col)
	}
	if want != nil && ast.OutputType().String() != want.String() {
		return nil, compileErr(path, fmt.Sprintf("expression must return %s, got %s", want, ast.OutputType()), 0, 0)
	}
	return ast, nil
}

func issuePos(iss *cel.Issues) (line, col int, msg string) {
	msg = iss.Err().Error()
	es := iss.Errors()
	if len(es) == 0 {
		return 0, 0, msg
	}
	msg = es[0].Message
	if es[0].Location != nil {
		line, col = es[0].Location.Line(), es[0].Location.Column()
	}
	return line, col, msg
}

func (e *Engine) programOf(ast *cel.Ast) (cel.Program, error) {
	return e.env.Program(ast,
		cel.CostTracking(runtimeCostEstimator{}),
		cel.CostLimit(e.costLimit),
		cel.InterruptCheckFrequency(100),
	)
}

func compileErr(path, msg string, line, col int) error {
	e := errs.New(errs.CodeRouteCompileError, msg).
		WithSafe("path", path).
		WithSafe("line", line).
		WithSafe("column", col)
	return e
}

// EvalResult is the outcome of evaluating a program against an event.
type EvalResult struct {
	Matched    bool
	Mapped     any
	Cost       uint64
	DurationMs int64
}

// Eval runs the filter (and map when matched) against the event activation.
func (e *Engine) Eval(ctx context.Context, p *Program, event map[string]any, account map[string]any, env map[string]string) (matched bool, mapped any, cost uint64, err error) {
	if p == nil || p.Filter == nil {
		return false, nil, 0, errs.New(errs.CodeRouteEvalError, "program is nil")
	}
	return e.EvalWithLists(ctx, p, event, account, env, nil)
}

func costOf(det *cel.EvalDetails) uint64 {
	if det == nil || det.ActualCost() == nil {
		return 0
	}
	return *det.ActualCost()
}

func mapEvalErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return errs.New(errs.CodeRouteTimeout, "route evaluation exceeded the time limit")
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "cost limit") || strings.Contains(msg, "costlimit") {
		return errs.New(errs.CodeRouteCostExceeded, "route evaluation exceeded the cost limit")
	}
	var cancel interpreter.EvalCancelledError
	if errors.As(err, &cancel) {
		if cancel.Cause == interpreter.CostLimitExceeded {
			return errs.New(errs.CodeRouteCostExceeded, "route evaluation exceeded the cost limit")
		}
		return errs.New(errs.CodeRouteTimeout, "route evaluation exceeded the time limit")
	}
	return errs.New(errs.CodeRouteEvalError, err.Error())
}

var anyReflectType = reflect.TypeOf((*any)(nil)).Elem()

// maxMapDepth bounds the recursion of celToNative (map output nesting).
const maxMapDepth = 64

// celToNative converts a CEL value to plain JSON-able Go values. It walks lists and maps itself so CEL
// null stays JSON null at any depth (a generic ConvertToNative turns a nested null into the protobuf
// NullValue enum, i.e. 0), timestamps become RFC 3339 strings, and bytes are refused (S8 §6).
func celToNative(v ref.Val, depth int) (any, error) {
	if depth > maxMapDepth {
		return nil, errs.New(errs.CodeRouteEvalError, "map output is nested too deeply")
	}
	if v == nil || types.IsUnknownOrError(v) {
		return nil, errs.New(errs.CodeRouteEvalError, "map returned an error or unknown value")
	}
	switch x := v.(type) {
	case types.Null:
		return nil, nil
	case types.Bool:
		return bool(x), nil
	case types.Int:
		return int64(x), nil
	case types.Uint:
		return uint64(x), nil
	case types.Double:
		return float64(x), nil
	case types.String:
		return string(x), nil
	case types.Bytes:
		return nil, errs.New(errs.CodeRouteEvalError, "map returned non-JSON bytes")
	case types.Timestamp:
		return x.Time.UTC().Format(time.RFC3339Nano), nil
	case types.Duration:
		return x.String(), nil
	case traits.Mapper:
		out := map[string]any{}
		it := x.Iterator()
		for it.HasNext() == types.True {
			k := it.Next()
			ks, ok := k.(types.String)
			if !ok {
				return nil, errs.New(errs.CodeRouteEvalError, "map output keys must be strings")
			}
			val, err := celToNative(x.Get(k), depth+1)
			if err != nil {
				return nil, err
			}
			out[string(ks)] = val
		}
		return out, nil
	case traits.Lister:
		n, ok := x.Size().(types.Int)
		if !ok {
			return nil, errs.New(errs.CodeRouteEvalError, "map returned an invalid list")
		}
		out := make([]any, 0, int(n))
		for i := types.Int(0); i < n; i++ {
			val, err := celToNative(x.Get(i), depth+1)
			if err != nil {
				return nil, err
			}
			out = append(out, val)
		}
		return out, nil
	}
	if _, ok := v.Value().([]byte); ok {
		return nil, errs.New(errs.CodeRouteEvalError, "map returned non-JSON bytes")
	}
	native, err := v.ConvertToNative(anyReflectType)
	if err != nil {
		return v.Value(), nil
	}
	return native, nil
}

func valueToJSON(v ref.Val) (any, error) {
	native, err := celToNative(v, 0)
	if err != nil {
		return nil, err
	}
	b, err := json.Marshal(native)
	if err != nil {
		return nil, errs.New(errs.CodeRouteEvalError, "map returned a non-JSON value")
	}
	if len(b) > maxMapOutputBytes {
		return nil, errs.New(errs.CodeRouteEvalError, "map output exceeds 256 KiB")
	}
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, errs.New(errs.CodeRouteEvalError, "map returned a non-JSON value")
	}
	return out, nil
}

// Cached returns a compiled program for (routeID, version), compiling on miss.
func (e *Engine) Cached(routeID string, version int, filter, mapExpr string) (*Program, error) {
	if p, ok := e.cache.get(routeID, version); ok {
		return p, nil
	}
	p, err := e.Compile(filter, mapExpr)
	if err != nil {
		return nil, err
	}
	e.cache.put(routeID, version, p)
	return p, nil
}

// InvalidateCache drops a cached program.
func (e *Engine) InvalidateCache(routeID string, version int) {
	e.cache.remove(routeID, version)
}

// InvalidateRoute drops every cached version of a route.
func (e *Engine) InvalidateRoute(routeID string) {
	e.cache.removeRoute(routeID)
}
