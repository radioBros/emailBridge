package query

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/radiobros/emailbridge/internal/logging"
	"github.com/radiobros/emailbridge/internal/logging/store"
)

var base = time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)

const (
	orgA = "org_aaaaaaaaaaaaaaaaaaaaaaaaaa"
	orgB = "org_bbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func newID(t time.Time, seq int) string {
	var e [10]byte
	e[7], e[8], e[9] = byte(seq>>16), byte(seq>>8), byte(seq)
	return "log_" + strings.ToLower(ulid.MustNew(ulid.Timestamp(t), bytes.NewReader(e[:])).String())
}

func mkRec(t time.Time, seq int, cat logging.Category, level slog.Level, msg, org string) logging.Record {
	return logging.Record{ID: newID(t, seq), Time: t, Level: level, LevelName: logging.LevelName(level), Category: cat,
		Component: "test", Node: "n1", Msg: msg, IDs: logging.IDs{OrganizationID: org}}
}

func openStore(t *testing.T, node string) *store.Store {
	t.Helper()
	s, err := store.Open(context.Background(), store.Options{Dir: t.TempDir(), Node: node, SweepEvery: -1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// seedStore writes n records, one per minute back from base, alternating orgA / orgB / instance level
// and api / sync categories; every 10th mentions "connection refused". seqOffset keeps IDs unique across
// stores.
func seedStore(t *testing.T, s *store.Store, n, seqOffset int, step time.Duration) {
	t.Helper()
	var recs []logging.Record
	for i := 0; i < n; i++ {
		org := []string{orgA, orgB, ""}[i%3]
		cat := []logging.Category{logging.CatAPI, logging.CatSync}[i%2]
		msg := fmt.Sprintf("event %d", i)
		if i%10 == 0 {
			msg = "connection refused by peer"
		}
		r := mkRec(base.Add(-time.Duration(i)*step-time.Duration(seqOffset)), seqOffset+i, cat,
			[]slog.Level{logging.LevelInfo, logging.LevelWarn}[i%2], msg, org)
		r.IDs.AccountID = fmt.Sprintf("acc_%d", i%4)
		r.Node = s.Node()
		recs = append(recs, r)
	}
	if err := s.Write(context.Background(), recs); err != nil {
		t.Fatal(err)
	}
}

func localService(t *testing.T, s *store.Store) (*Service, *store.Hub) {
	t.Helper()
	h := store.NewHub(s.Node(), 0)
	t.Cleanup(h.Close)
	svc, err := New(Options{Local: &store.Node{Store: s, Hub: h}})
	if err != nil {
		t.Fatal(err)
	}
	return svc, h
}

func dayRange() (time.Time, time.Time) { return base.Add(-5 * 24 * time.Hour), base.Add(time.Hour) }
