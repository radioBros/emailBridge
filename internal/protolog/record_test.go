package protolog

import (
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/logging/store"
)

func TestFromLogRecordAndFilters(t *testing.T) {
	acc := "acc_1"
	r := store.LogRecord{
		ID: "log_01habcdefghijklmnopqrstuvw", Time: 42, AccountID: &acc,
		Attrs: map[string]any{"dir": "C", "proto": "imap", "connId": "c1", "line": "a1 NOOP", "bytes": int64(7), "latencyMs": float64(3)},
	}
	got, ok := fromLogRecord(r)
	if !ok || got.Dir != "C" || got.Proto != "imap" || got.ConnID != "c1" || got.Line != "a1 NOOP" || got.Bytes != 7 || got.LatencyMs != 3 || got.AccountID != acc {
		t.Fatalf("%+v ok=%v", got, ok)
	}
	if _, ok := fromLogRecord(store.LogRecord{Attrs: map[string]any{"dir": "C"}}); ok {
		t.Fatal("incomplete")
	}
	if matchProtoConn(r, "smtp", "") || !matchProtoConn(r, "imap", "c1") || matchProtoConn(r, "", "other") {
		t.Fatal("filter")
	}
}

func TestResolveRange(t *testing.T) {
	from, to, err := resolveRange(timeZero(), timeZero())
	if err != nil || to.Before(from) || to.Sub(from) < defaultSpan {
		t.Fatalf("%v %v %v", from, to, err)
	}
	a := from.Add(time.Hour)
	b := from.Add(2 * time.Hour)
	gotA, gotB, err := resolveRange(a, b)
	if err != nil || !gotA.Equal(a.UTC()) || !gotB.Equal(b.UTC()) {
		t.Fatal(err)
	}
	if _, _, err := resolveRange(a, timeZero()); err == nil {
		t.Fatal("partial range")
	}
}

func timeZero() time.Time { return time.Time{} }
