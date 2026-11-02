package sync

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/content"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/index"
	"github.com/radiobros/emailbridge/internal/mailproto/imapc"
	"github.com/radiobros/emailbridge/internal/mailproto/mime"
)

func TestReportable(t *testing.T) {
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	notify := now.Add(-time.Hour)
	week := 7 * 24 * time.Hour
	cases := []struct {
		name     string
		baseline bool
		next     uint32
		uid      uint32
		date     time.Time
		want     bool
	}{
		{"new mail", true, 10, 10, now, true},
		{"above next", true, 10, 15, now, true},
		{"below next (already indexed range)", true, 10, 9, now, false},
		{"no baseline: silent", false, 10, 20, now, false},
		{"before notifyFrom", true, 10, 12, notify.Add(-time.Minute), false},
		{"at notifyFrom", true, 10, 12, notify, true},
		{"older than catch-up", true, 10, 12, now.Add(-week - time.Hour), false},
	}
	for _, c := range cases {
		nf := notify
		if c.name == "older than catch-up" {
			nf = time.Time{}
		}
		if got := reportable(c.baseline, c.next, c.uid, c.date, nf, now, week); got != c.want {
			t.Errorf("%s: %v", c.name, got)
		}
	}
	if !reportable(true, 1, 1, now.Add(-30*24*time.Hour), time.Time{}, now, 0) {
		t.Fatal("no catch-up limit configured")
	}
}

func TestWatched(t *testing.T) {
	list := []imapc.MailboxInfo{
		{Name: "INBOX"}, {Name: "Sent", SpecialUse: `\Sent`}, {Name: "[Gmail]/All Mail", SpecialUse: `\All`},
		{Name: "Starred", SpecialUse: `\Flagged`}, {Name: "Parent", Attrs: []string{`\Noselect`}}, {Name: "Projects/A"}, {Name: "Projects/B"},
	}
	names := func(ms []imapc.MailboxInfo) []string {
		out := []string{}
		for _, m := range ms {
			out = append(out, m.Name)
		}
		return out
	}
	if got := names(watched(list, []string{"*"})); !slices.Equal(got, []string{"INBOX", "Sent", "Projects/A", "Projects/B"}) {
		t.Fatalf("all: %v", got)
	}
	if got := names(watched(list, nil)); len(got) != 4 {
		t.Fatalf("default: %v", got)
	}
	if got := names(watched(list, []string{"inbox", "Projects/*"})); !slices.Equal(got, []string{"INBOX", "Projects/A", "Projects/B"}) {
		t.Fatalf("patterns: %v", got)
	}
	w := watched(list, []string{"*"})
	if got := idleFolders(w, nil, 1); !slices.Equal(got, []string{"INBOX"}) {
		t.Fatalf("idle default: %v", got)
	}
	if got := idleFolders(w, []string{"Sent", "INBOX", "Nope"}, 3); !slices.Equal(got, []string{"Sent", "INBOX"}) {
		t.Fatalf("idle list: %v", got)
	}
	if got := idleFolders(w, []string{"Sent", "INBOX"}, 1); len(got) != 1 {
		t.Fatalf("idle cap: %v", got)
	}
}

func TestBackoff(t *testing.T) {
	minD, maxD := 5*time.Second, 10*time.Minute
	hi := func(n int64) int64 { return n - 1 }
	lo := func(int64) int64 { return 0 }
	if d := backoff(1, minD, maxD, errs.KindTemporary, lo); d != minD {
		t.Fatalf("first, low jitter: %v", d)
	}
	if d := backoff(3, minD, maxD, errs.KindTemporary, hi); d != 20*time.Second {
		t.Fatalf("third, high jitter: %v", d)
	}
	if d := backoff(40, minD, maxD, errs.KindTemporary, hi); d != maxD {
		t.Fatalf("capped: %v", d)
	}
	if d := backoff(1, minD, maxD, errs.KindAuth, lo); d != 5*time.Minute {
		t.Fatalf("auth floor: %v", d)
	}
	if d := backoff(1, minD, maxD, errs.KindRateLimited, lo); d != 5*time.Minute {
		t.Fatalf("rate floor: %v", d)
	}
	if d := backoff(1, minD, time.Hour, errs.KindConfig, lo); d != 15*time.Minute {
		t.Fatalf("config floor: %v", d)
	}
	if d := backoff(0, minD, maxD, errs.KindTemporary, lo); d != minD {
		t.Fatalf("attempt 0: %v", d)
	}
	for i := 0; i < 100; i++ {
		if d := backoff(4, minD, maxD, errs.KindTemporary, randInt64); d < minD || d > 40*time.Second {
			t.Fatalf("jitter out of range: %v", d)
		}
	}
	if randInt64(0) != 0 {
		t.Fatal("rand 0")
	}
}

func TestFailureStateAndFlags(t *testing.T) {
	if failureState(errs.New(errs.CodeMailAuthFailed, "x")) != StateAuthError || failureState(errors.New("eof")) != StateConnectError {
		t.Fatal("failure state")
	}
	if !flagsEqual([]string{`\Seen`, `\Recent`, `\Flagged`}, []string{`\Flagged`, `\Seen`}) || flagsEqual([]string{`\Seen`}, nil) {
		t.Fatal("flagsEqual")
	}
	added, removed := flagDiff([]string{`\Seen`, `\Draft`}, []string{`\Seen`, `\Flagged`, `\Recent`})
	if !slices.Equal(added, []string{`\Flagged`}) || !slices.Equal(removed, []string{`\Draft`}) {
		t.Fatalf("%v %v", added, removed)
	}
	a, r := flagDiff(nil, nil)
	if a == nil || r == nil {
		t.Fatal("empty slices, not null")
	}
	if c := chunks([]uint32{1, 2, 3, 4, 5}, 2); len(c) != 3 || len(c[2]) != 1 {
		t.Fatalf("chunks %v", c)
	}
	if c := chunks(nil, 0); len(c) != 0 {
		t.Fatal("empty chunks")
	}
}

func TestDueFullAndReasonOf(t *testing.T) {
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	r := &runner{lastFull: map[string]time.Time{}, now: func() time.Time { return now }}
	m := &index.Mailbox{ID: "mbx_1"}
	if r.dueFull(m) {
		t.Fatal("full passes are off when FullResync is 0")
	}
	r.cfg.FullResync = time.Hour
	if !r.dueFull(m) {
		t.Fatal("a folder never fully diffed is due")
	}
	r.lastFull[m.ID] = now.Add(-30 * time.Minute)
	if r.dueFull(m) {
		t.Fatal("not due within the interval")
	}
	r.lastFull[m.ID] = now.Add(-time.Hour)
	if !r.dueFull(m) {
		t.Fatal("due once the interval has passed")
	}

	if reasonOf(nil) != nil {
		t.Fatal("no reason without an error")
	}
	long := strings.Repeat("x", 600) + "\r\nsecond line"
	got := reasonOf(errs.New(errs.CodeMailAuthFailed, long))
	if got.Code != string(errs.CodeMailAuthFailed) || got.Kind == "" || len(got.Message) > 512 || strings.ContainsAny(got.Message, "\r\n") {
		t.Fatalf("reason %+v", got)
	}
}

func TestSelectHeadersAndThreadID(t *testing.T) {
	h := mime.Header{"subject": {"Hi"}, "message-id": {"<a@x>"}, "x-custom": {"1", "2"}}
	if got := selectHeaders(h, []string{"Subject", "X-Missing"}); len(got) != 1 || got["subject"][0] != "Hi" {
		t.Fatalf("named: %v", got)
	}
	if got := selectHeaders(h, []string{"*"}); len(got) != 3 || len(got["x-custom"]) != 2 {
		t.Fatalf("all: %v", got)
	}
	// Thread IDs come from internal/content (P4.2); the grouping rules of P1 still hold.
	own := content.ThreadID("<own@x>", "", nil, "Hi")
	if own == "" || content.ThreadID("own@x", "", nil, "Hi") != own {
		t.Fatal("a message without references is its own thread (brackets do not matter)")
	}
	reply := content.ThreadID("<reply@x>", "<root@x>", nil, "Re: Hi")
	deep := content.ThreadID("<deep@x>", "<mid@x>", []string{"<root@x>", "<mid@x>"}, "Re: Re: Hi")
	if reply != deep || reply != content.ThreadID("<root@x>", "", nil, "Hi") {
		t.Fatal("replies group under the first reference, else In-Reply-To")
	}
}
