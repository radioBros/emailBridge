package query

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/logging"
	"github.com/radiobros/emailbridge/internal/logging/store"
)

func codeOf(t *testing.T, err error) errs.Code {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error")
	}
	return errs.CodeOf(err)
}

func TestCursorRoundTripAndRejects(t *testing.T) {
	p := store.Position{Time: base.UnixNano(), ID: newID(base, 1)}
	got, err := DecodeCursor(EncodeCursor(p))
	if err != nil || *got != p {
		t.Fatalf("round trip %v %v", got, err)
	}
	for _, bad := range []string{"!!", "bm9waXBl", EncodeCursor(store.Position{Time: 1, ID: "evt_1"}),
		"eHx" + "sb2dfMDFqYmYzazh2NHc3cTJtOXg1YzZ0MHIxeXo"} {
		if _, err := DecodeCursor(bad); codeOf(t, err) != errs.CodeValidationFailed {
			t.Errorf("cursor %q accepted", bad)
		}
	}
}

func TestQueryValidation(t *testing.T) {
	from, to := dayRange()
	cases := []struct {
		q    Query
		code errs.Code
	}{
		{Query{To: to}, errs.CodeValidationFailed},
		{Query{From: to, To: from}, errs.CodeValidationFailed},
		{Query{From: from, To: from.Add(31*24*time.Hour + time.Second)}, errs.CodeLogRangeTooLarge},
		{Query{From: from, To: to, Categories: []logging.Category{"secret/../../x"}}, errs.CodeValidationFailed},
		{Query{From: from, To: to, MinLevel: "loud"}, errs.CodeValidationFailed},
		{Query{From: from, To: to, Q: strings.Repeat("x", MaxQueryText+1)}, errs.CodeValidationFailed},
		{Query{From: from, To: to, Limit: MaxLimit + 1}, errs.CodeValidationFailed},
		{Query{From: from, To: to, Limit: -1}, errs.CodeValidationFailed},
		{Query{From: from, To: to, Cursor: "%%%"}, errs.CodeValidationFailed},
	}
	for _, c := range cases {
		if _, _, err := c.q.filter(); codeOf(t, err) != c.code {
			t.Errorf("%+v: %v", c.q, err)
		}
	}
	f, limit, err := (&Query{From: from, To: from.Add(MaxSpan)}).filter()
	if err != nil || limit != DefaultLimit || f.Before != nil {
		t.Fatalf("31 days exactly must pass: %v", err)
	}
	if _, err := New(Options{}); err == nil {
		t.Fatal("service without bus and local node")
	}
}

func raw(t *testing.T, recs ...store.LogRecord) []json.RawMessage {
	t.Helper()
	var out []json.RawMessage
	for i := range recs {
		b, _ := json.Marshal(&recs[i])
		out = append(out, b)
	}
	return out
}

func lr(sec int, id int) store.LogRecord {
	return store.LogRecord{ID: newID(base, id), Time: base.Add(time.Duration(sec) * time.Second).UnixNano(), Level: "info", Category: "api", Msg: "m"}
}

func TestMergeOrdersDedupesAndRespectsTruncation(t *testing.T) {
	// node A: 10, 8, 6 (truncated after 6); node B: 9, 7, 5, 3
	a := store.Reply{Node: "a", Records: raw(t, lr(10, 1), lr(8, 2), lr(6, 3)), Truncated: true}
	b := store.Reply{Node: "b", Records: raw(t, lr(9, 4), lr(7, 5), lr(5, 6), lr(3, 7), lr(10, 1))}
	p := merge([]store.Reply{a, b}, []string{"c"}, 10)
	var secs []int64
	for _, r := range p.Records {
		secs = append(secs, (r.Time-base.UnixNano())/int64(time.Second))
	}
	if len(secs) != 5 || secs[0] != 10 || secs[4] != 6 {
		t.Fatalf("page cut at the truncated node's last record: %v", secs)
	}
	if p.NextCursor == "" || p.Partial[0] != "c" {
		t.Fatal("cursor and partial")
	}
	// limit trims and sets a cursor; no more records → no cursor
	p = merge([]store.Reply{b}, nil, 2)
	if len(p.Records) != 2 || p.NextCursor == "" {
		t.Fatal("limit")
	}
	p = merge([]store.Reply{b}, nil, 10)
	if len(p.Records) != 5 || p.NextCursor != "" {
		t.Fatalf("last page %d %q", len(p.Records), p.NextCursor)
	}
	p = merge(nil, nil, 10)
	if p.Records == nil || len(p.Records) != 0 {
		t.Fatal("empty data must be [] not null")
	}
	bad := store.Reply{Node: "x", Records: []json.RawMessage{[]byte("{")}}
	if p := merge([]store.Reply{bad}, nil, 5); len(p.Records) != 0 {
		t.Fatal("garbage record")
	}
}

func TestLocalSearchFiltersAndPaging(t *testing.T) {
	s := openStore(t, "n1")
	seedStore(t, s, 300, 0, time.Minute)
	svc, _ := localService(t, s)
	ctx := context.Background()
	from, to := dayRange()
	count := func(q Query) int {
		t.Helper()
		q.From, q.To, q.Limit = from, to, MaxLimit
		p, err := svc.Search(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		return len(p.Records)
	}
	for name, c := range map[string]struct {
		q    Query
		want int
	}{
		"all":             {Query{}, 300},
		"category":        {Query{Categories: []logging.Category{logging.CatSync}}, 150},
		"level":           {Query{MinLevel: "warn"}, 150},
		"org":             {Query{OrganizationID: orgA}, 100},
		"account":         {Query{AccountID: "acc_1"}, 75},
		"fts":             {Query{Q: "connection ref"}, 30},
		"fts hostile":     {Query{Q: `" OR 1 --`}, 0},
		"fts injection":   {Query{Q: `x" OR msg:*`}, 0},
		"fts near":        {Query{Q: `NEAR(event 1)`}, 0},
		"fts quotes only": {Query{Q: `""""`}, 300},
	} {
		if n := count(c.q); n != c.want {
			t.Errorf("%s: %d, want %d", name, n, c.want)
		}
	}
	// paging: 300 records in pages of 47, stable while new records arrive
	q := Query{From: from, To: to, Limit: 47}
	seen := map[string]bool{}
	pages := 0
	var prev *store.Position
	for {
		p, err := svc.Search(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range p.Records {
			if seen[r.ID] {
				t.Fatalf("duplicate %s", r.ID)
			}
			if prev != nil && !r.Pos().Before(*prev) {
				t.Fatal("order across pages")
			}
			pos := r.Pos()
			prev = &pos
			seen[r.ID] = true
		}
		pages++
		if p.NextCursor == "" {
			break
		}
		q.Cursor = p.NextCursor
		_ = s.Write(ctx, []logging.Record{mkRec(base.Add(time.Duration(pages)*time.Second), 90000+pages, logging.CatAPI, logging.LevelInfo, "new", orgA)})
	}
	if len(seen) != 300 || pages != 7 {
		t.Fatalf("paged %d in %d pages", len(seen), pages)
	}
	// errors from the store surface as validation problems
	if _, err := svc.Search(ctx, Query{From: from, To: to, MinLevel: "nope"}); codeOf(t, err) != errs.CodeValidationFailed {
		t.Fatal(err)
	}
}

func TestLocalGetStatsEraseExport(t *testing.T) {
	s := openStore(t, "n1")
	seedStore(t, s, 60, 0, time.Minute)
	svc, _ := localService(t, s)
	ctx := context.Background()
	id := newID(base.Add(-3*time.Minute), 3)
	r, partial, err := svc.Get(ctx, id)
	if err != nil || r == nil || r.ID != id || len(partial) != 0 {
		t.Fatalf("get %+v %v %v", r, partial, err)
	}
	if r, _, err := svc.Get(ctx, newID(base, 99999)); err != nil || r != nil {
		t.Fatal("missing record")
	}
	if _, _, err := svc.Get(ctx, "log_zzzzzzzzzzzzzzzzzzzzzzzzzz"); codeOf(t, err) != errs.CodeValidationFailed {
		t.Fatal("bad ULID")
	}
	if _, _, err := svc.Get(ctx, "nope"); codeOf(t, err) != errs.CodeValidationFailed {
		t.Fatal("bad id")
	}
	from, to := dayRange()
	st, err := svc.Stats(ctx, from, to, "")
	if err != nil || st.ByCategory["api"] != 30 || st.ByLevel["warn"] != 30 || st.Disk["api"]["n1"] <= 0 {
		t.Fatalf("stats %+v %v", st, err)
	}
	st, err = svc.Stats(ctx, from, to, orgA)
	if err != nil || st.ByCategory["api"]+st.ByCategory["sync"] != 20 {
		t.Fatalf("org stats %+v %v", st, err)
	}
	if _, err := svc.Stats(ctx, from, from.Add(40*24*time.Hour), ""); codeOf(t, err) != errs.CodeLogRangeTooLarge {
		t.Fatal("stats range")
	}
	var sb strings.Builder
	n, partial, err := svc.Export(ctx, Query{From: from, To: to, Limit: 7}, &sb)
	if err != nil || n != 60 || len(partial) != 0 || strings.Count(sb.String(), "\n") != 60 {
		t.Fatalf("export %d %v %v", n, partial, err)
	}
	if _, _, err := svc.Export(ctx, Query{From: from, To: to}, errWriter{}); err == nil {
		t.Fatal("export write error")
	}
	if _, _, err := svc.Export(ctx, Query{}, &sb); err == nil {
		t.Fatal("export validation")
	}
	n, partial, err = svc.Erase(ctx, "acc_2")
	if err != nil || n != 15 || len(partial) != 0 {
		t.Fatalf("erase %d %v %v", n, partial, err)
	}
	if _, _, err := svc.Erase(ctx, ""); err == nil {
		t.Fatal("erase without account")
	}
	// an expired context is a timeout, not a hang
	dctx, cancel := context.WithDeadline(ctx, time.Now().Add(-time.Second))
	defer cancel()
	if _, err := svc.Search(dctx, Query{From: from, To: to}); codeOf(t, err) != errs.CodeTimeout {
		t.Fatal("expired deadline")
	}
}

type errWriter struct{}

func (errWriter) Write([]byte) (int, error) { return 0, errors.New("broken pipe") }

func TestLocalTail(t *testing.T) {
	s := openStore(t, "n1")
	svc, hub := localService(t, s)
	got := make(chan store.LogRecord, 4)
	stop, err := svc.Tail(context.Background(), TailFilter{OrganizationID: orgA}, func(r store.LogRecord) { got <- r })
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	_ = hub.Write(context.Background(), []logging.Record{
		mkRec(time.Now(), 1, logging.CatAPI, logging.LevelInfo, "other tenant", orgB),
		mkRec(time.Now(), 2, logging.CatAPI, logging.LevelInfo, "instance level", ""),
		mkRec(time.Now(), 3, logging.CatAPI, logging.LevelInfo, "mine", orgA),
	})
	select {
	case r := <-got:
		if r.Msg != "mine" {
			t.Fatalf("tail leaked %q", r.Msg)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no tail record")
	}
	if _, err := svc.Tail(context.Background(), TailFilter{MinLevel: "x"}, nil); codeOf(t, err) != errs.CodeValidationFailed {
		t.Fatal("tail validation")
	}
	noHub, _ := New(Options{Local: &store.Node{Store: s}})
	if _, err := noHub.Tail(context.Background(), TailFilter{}, nil); err == nil {
		t.Fatal("tail without hub")
	}
}
