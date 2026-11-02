package rules

import (
	"context"
	"errors"
	"testing"

	"cel.dev/cel-go/common/types"
	"cel.dev/cel-go/interpreter"

	"github.com/radiobros/emailbridge/internal/errs"
)

func TestMapEvalErrBranches(t *testing.T) {
	if mapEvalErr(nil) != nil {
		t.Fatal()
	}
	if errs.CodeOf(mapEvalErr(context.DeadlineExceeded)) != errs.CodeRouteTimeout {
		t.Fatal("deadline")
	}
	if errs.CodeOf(mapEvalErr(context.Canceled)) != errs.CodeRouteTimeout {
		t.Fatal("cancel")
	}
	if errs.CodeOf(mapEvalErr(errors.New("operation cancelled: actual cost limit exceeded"))) != errs.CodeRouteCostExceeded {
		t.Fatal("cost string")
	}
	cerr := interpreter.EvalCancelledError{Cause: interpreter.CostLimitExceeded, Message: "cost"}
	if errs.CodeOf(mapEvalErr(cerr)) != errs.CodeRouteCostExceeded {
		t.Fatal("cost cancel")
	}
	cerr2 := interpreter.EvalCancelledError{Cause: interpreter.ContextCancelled, Message: "ctx"}
	if errs.CodeOf(mapEvalErr(cerr2)) != errs.CodeRouteTimeout {
		t.Fatal("ctx cancel")
	}
	if errs.CodeOf(mapEvalErr(errors.New("boom"))) != errs.CodeRouteEvalError {
		t.Fatal("generic")
	}
}

func TestEvalNilProgramAndEmptyMaps(t *testing.T) {
	e := testEngine(t, EngineOptions{})
	_, _, _, err := e.Eval(context.Background(), nil, nil, nil, nil)
	if errs.CodeOf(err) != errs.CodeRouteEvalError {
		t.Fatal(err)
	}
	p, err := e.Compile("true", "")
	if err != nil {
		t.Fatal(err)
	}
	matched, mapped, _, err := e.Eval(context.Background(), p, map[string]any{"type": "x"}, nil, nil)
	if err != nil || !matched || mapped != nil {
		t.Fatalf("%v %v %v", matched, mapped, err)
	}
}

func TestCompileTooLarge(t *testing.T) {
	e := testEngine(t, EngineOptions{})
	big := make([]byte, maxExprBytes+10)
	for i := range big {
		big[i] = 'x'
	}
	_, err := e.Compile(string(big), "")
	if err == nil {
		t.Fatal("expected size error")
	}
	_, err = e.Compile("true", string(big))
	if err == nil {
		t.Fatal("expected map size error")
	}
}

func TestNewEngineDefaults(t *testing.T) {
	e, err := NewEngine(EngineOptions{CacheSize: -1})
	if err != nil || e.cache.size != 256 {
		t.Fatal(err, e.cache.size)
	}
}

func TestGetHeadersMissing(t *testing.T) {
	evalHeaders.Delete("h")
	if len(getHeaders()) != 0 {
		t.Fatal()
	}
}

func TestAddressStringTypesString(t *testing.T) {
	if addressString(types.String("a@b.com")) != "a@b.com" {
		t.Fatal()
	}
	if addressString(123) != "" {
		t.Fatal()
	}
}

func TestCacheSizeFloor(t *testing.T) {
	c := newProgramCache(0)
	if c.size != 1 {
		t.Fatal(c.size)
	}
	p := &Program{}
	c.put("r", 1, p)
	c.put("r", 1, p) // update existing
	got, ok := c.get("r", 1)
	if !ok || got != p {
		t.Fatal()
	}
}

func TestDecodeCursorEmptyID(t *testing.T) {
	s := encodeCursor(cursorPayload{})
	if _, err := decodeCursor(s); err == nil {
		t.Fatal("expected empty id error")
	}
}

func TestCursorFilterAscending(t *testing.T) {
	kf, err := cursorFilter(cursorPayload{ID: "rte_b"}, "_id", 1)
	if err != nil || kf["_id"] == nil {
		t.Fatal(kf, err)
	}
	kf, err = cursorFilter(cursorPayload{ID: "rte_b", Key: "n"}, "name", 1)
	if err != nil {
		t.Fatal(err)
	}
	_ = kf
}

func TestAddressesFnNonList(t *testing.T) {
	got := addressesFn(types.String("x"))
	if len(got.Value().([]string)) != 0 {
		t.Fatal(got)
	}
}

func TestSecretValuesEmpty(t *testing.T) {
	if SecretValuesFromEnv(nil, nil) != nil {
		t.Fatal()
	}
	if MaskSecrets("x", nil) != "x" {
		t.Fatal()
	}
}

func TestCachedCompileError(t *testing.T) {
	e := testEngine(t, EngineOptions{})
	if _, err := e.Cached("rte_bad", 1, "!!!", ""); err == nil {
		t.Fatal("expected compile error")
	}
}
