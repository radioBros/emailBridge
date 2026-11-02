package fulltext

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/radiobros/emailbridge/internal/bus"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/events"
	"github.com/radiobros/emailbridge/internal/features"
	"github.com/radiobros/emailbridge/internal/index"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

func TestParseQuery(t *testing.T) {
	for _, tc := range []struct {
		in, search string
		terms      []string
	}{
		{"invoice", "invoice", []string{"invoice"}},
		{"  invoice   2026 ", "invoice 2026", []string{"invoice", "2026"}},
		{`"payment  due" invoice`, `"payment due" invoice`, []string{"payment due", "invoice"}},
		{`invoice -reminder`, `invoice -reminder`, []string{"invoice"}},
		{`--x invoice-`, `-x invoice`, []string{"invoice"}},
		{`"unterminated phrase`, `"unterminated phrase"`, []string{"unterminated phrase"}},
		{`a"b`, `a "b"`, []string{"a", "b"}},
		{"tab\tseparated\x00ctl", "tab separated ctl", []string{"tab", "separated", "ctl"}},
		{`"" fattura`, `fattura`, []string{"fattura"}},
	} {
		q, err := parseQuery(tc.in)
		if err != nil {
			t.Fatalf("%q: %v", tc.in, err)
		}
		if q.Search != tc.search || strings.Join(q.Terms, "|") != strings.Join(tc.terms, "|") {
			t.Errorf("%q: got %q %q, want %q %q", tc.in, q.Search, q.Terms, tc.search, tc.terms)
		}
	}
	for _, bad := range []string{"", "   ", "-only -negated", `""`, strings.Repeat("a", maxQueryRunes+1),
		strings.TrimSpace(strings.Repeat("w ", maxQueryTerms+1))} {
		if _, err := parseQuery(bad); errs.CodeOf(err) != errs.CodeValidationFailed {
			t.Errorf("%q: want validation_failed, got %v", bad, err)
		}
	}
	// 256 runes of a multibyte script are still accepted.
	if _, err := parseQuery(strings.Repeat("é", maxQueryRunes)); err != nil {
		t.Fatal(err)
	}
}

func runeSlice(s string, h FullTextHighlight) string {
	r := []rune(s)
	return string(r[h.Start:h.End])
}

func TestBuildSnippet(t *testing.T) {
	long := strings.Repeat("lorem ipsum dolor sit amet ", 20) + "the Invoice 2026-1043 is attached " +
		strings.Repeat("consectetur adipiscing elit ", 20)
	snip, hl := buildSnippet(long, []string{"invoice"}, 80)
	if !strings.HasPrefix(snip, "…") || !strings.HasSuffix(snip, "…") {
		t.Fatalf("ellipses: %q", snip)
	}
	if utf8.RuneCountInString(snip) > 80+2 {
		t.Fatalf("too long: %d %q", utf8.RuneCountInString(snip), snip)
	}
	if len(hl) != 1 || runeSlice(snip, hl[0]) != "Invoice" {
		t.Fatalf("highlights %v in %q", hl, snip)
	}
	if strings.Contains(snip, "<") {
		t.Fatal("snippet must be plain text")
	}
	// Starts at a word: no partial word after the leading ellipsis.
	if after := []rune(snip)[1]; after == ' ' {
		t.Fatalf("starts with a space: %q", snip)
	}

	// Diacritic- and case-insensitive, code-point offsets with multibyte text.
	snip, hl = buildSnippet("Größe: la FATTURA è pronta, caffè incluso", []string{"fattura", "CAFFE"}, 160)
	if len(hl) != 2 || runeSlice(snip, hl[0]) != "FATTURA" || runeSlice(snip, hl[1]) != "caffè" {
		t.Fatalf("highlights %v in %q", hl, snip)
	}

	// Whole words first: "art" matches the word, not "start".
	snip, hl = buildSnippet("start of the art show", []string{"art"}, 160)
	if len(hl) != 1 || hl[0].Start != 13 {
		t.Fatalf("whole word: %v in %q", hl, snip)
	}
	// Substring fallback when the term never occurs as a word.
	_, hl = buildSnippet("see invoices.pdf", []string{"invoice"}, 160)
	if len(hl) != 1 {
		t.Fatalf("substring fallback: %v", hl)
	}

	// Phrase terms and whitespace collapsing.
	snip, hl = buildSnippet("payment\n\n   due   tomorrow", []string{"payment due"}, 160)
	if snip != "payment due tomorrow" || len(hl) != 1 || runeSlice(snip, hl[0]) != "payment due" {
		t.Fatalf("phrase: %q %v", snip, hl)
	}

	// No match in the text: the start of the text, no highlights, never nil.
	snip, hl = buildSnippet(strings.Repeat("word ", 100), []string{"subjectonly"}, 40)
	if strings.HasPrefix(snip, "…") || !strings.HasSuffix(snip, "…") || hl == nil || len(hl) != 0 {
		t.Fatalf("no match: %q %v", snip, hl)
	}
	if snip, hl = buildSnippet("", []string{"x"}, 40); snip != "" || hl == nil {
		t.Fatalf("empty: %q %v", snip, hl)
	}

	// A match longer than the window is never cut.
	snip, hl = buildSnippet("aaaa "+strings.Repeat("b", 50)+" cccc", []string{strings.Repeat("b", 50)}, 20)
	if len(hl) != 1 || runeSlice(snip, hl[0]) != strings.Repeat("b", 50) {
		t.Fatalf("long match: %q %v", snip, hl)
	}
}

func TestBodyText(t *testing.T) {
	text, trunc := bodyText("Hello  world\r\n\r\n\r\nsecond\tline", "<p>ignored</p>")
	if text != "Hello world\nsecond line" || trunc {
		t.Fatalf("%q %v", text, trunc)
	}
	text, _ = bodyText("  ", `<html><head><title>T</title><style>p{}</style></head><body><p>Dear <b>Marco</b>,</p>`+
		`<script>alert(1)</script><div>see the<br>invoice</div><table><tr><td>a</td><td>b</td></tr></table></body></html>`)
	if text != "Dear Marco,\nsee the\ninvoice\na b" {
		t.Fatalf("html: %q", text)
	}
	big := strings.Repeat("é", MaxTextBytes) // 2 bytes each
	text, trunc = bodyText(big, "")
	if !trunc || len(text) > MaxTextBytes || !utf8.ValidString(text) {
		t.Fatalf("truncate: len=%d trunc=%v valid=%v", len(text), trunc, utf8.ValidString(text))
	}
	text, _ = bodyText("ok \xff\xfe bytes", "")
	if !utf8.ValidString(text) || text != "ok bytes" {
		t.Fatalf("invalid utf8: %q", text)
	}
}

func TestAddrStrings(t *testing.T) {
	if got := addrString(&index.Address{Name: " Billing ", Address: "billing@example.com"}); got != "Billing <billing@example.com>" {
		t.Fatal(got)
	}
	if got := addrString(&index.Address{Address: "a@example.com"}); got != "a@example.com" {
		t.Fatal(got)
	}
	if addrString(nil) != "" {
		t.Fatal("nil")
	}
	got := addrStrings([]index.Address{{Address: "a@x.test"}, {}, {Name: "B", Address: "b@x.test"}})
	if strings.Join(got, ",") != "a@x.test,B <b@x.test>" {
		t.Fatal(got)
	}
}

func TestClassify(t *testing.T) {
	s := &Service{}
	ctx := context.Background()
	ev := events.Event{ID: "evt_x", Type: events.MessageReceived}
	if s.classify(ctx, ev, nil, 1) != nil {
		t.Fatal("nil error acks")
	}
	for _, code := range []errs.Code{errs.CodeNotFound, errs.CodeValidationFailed, errs.CodeMailAuthFailed, errs.CodeConflict} {
		if err := s.classify(ctx, ev, errs.New(code, "x"), 1); err != nil {
			t.Errorf("%s: want ack, got %v", code, err)
		}
	}
	var delayed *bus.RetryAfter
	err := s.classify(ctx, ev, errs.New(errs.CodeConnectionBusy, "busy"), 1)
	if !errors.As(err, &delayed) || delayed.Delay != busyRetry {
		t.Fatalf("busy: %v", err)
	}
	err = s.classify(ctx, ev, errs.New(errs.CodeMailTimeout, "slow"), 3)
	if !errors.As(err, &delayed) || delayed.Delay != 20*time.Second {
		t.Fatalf("timeout: %v", err)
	}
	if err := s.classify(ctx, ev, errs.New(errs.CodeMailTimeout, "slow"), maxDeliveries); err != nil {
		t.Fatalf("gives up after maxDeliveries: %v", err)
	}
	var discard *bus.Discard
	if err := s.classify(ctx, ev, bus.DiscardMessage(errors.New("bad")), 1); !errors.As(err, &discard) {
		t.Fatalf("discard kept: %v", err)
	}
	if retryDelay(1) != retryBase || retryDelay(100) != retryMax {
		t.Fatal("retryDelay bounds")
	}
}

func TestScopeAccount(t *testing.T) {
	acc := store.NewID(store.PrefixAccount)
	other := store.NewID(store.PrefixAccount)
	key := func(c tenancy.Caller) context.Context {
		c.Kind, c.APIKeyID, c.OrganizationID = tenancy.CallerAPIKey, "key_x", "org_1"
		return tenancy.WithCaller(context.Background(), c)
	}
	// Account-bound key: forced to its account, refused for another one.
	got, err := scopeAccount(key(tenancy.Caller{Scopes: []string{"messages:read"}, AccountID: acc}), "")
	if err != nil || got != acc {
		t.Fatalf("bound: %q %v", got, err)
	}
	if _, err := scopeAccount(key(tenancy.Caller{Scopes: []string{"messages:read"}, AccountID: acc}), other); errs.CodeOf(err) != errs.CodeForbidden {
		t.Fatalf("bound other: %v", err)
	}
	// Per-account scope: fine with that account named, refused organisation-wide.
	granular := key(tenancy.Caller{Scopes: []string{"messages:read:" + acc}})
	if got, err := scopeAccount(granular, acc); err != nil || got != acc {
		t.Fatalf("granular named: %q %v", got, err)
	}
	if _, err := scopeAccount(granular, ""); errs.CodeOf(err) != errs.CodeScopeMissing {
		t.Fatalf("granular org-wide: %v", err)
	}
	if _, err := scopeAccount(granular, other); errs.CodeOf(err) != errs.CodeScopeMissing {
		t.Fatalf("granular other: %v", err)
	}
	// Unrelated scope.
	if _, err := scopeAccount(key(tenancy.Caller{Scopes: []string{"events:read"}}), ""); errs.CodeOf(err) != errs.CodeScopeMissing {
		t.Fatalf("unrelated: %v", err)
	}
	// Users: any role reads.
	viewer := tenancy.WithCaller(context.Background(), tenancy.Caller{Kind: tenancy.CallerUser, UserID: "usr_1", OrganizationID: "org_1", Role: tenancy.RoleViewer})
	if got, err := scopeAccount(viewer, ""); err != nil || got != "" {
		t.Fatalf("viewer: %q %v", got, err)
	}
	if _, err := scopeAccount(context.Background(), ""); errs.CodeOf(err) != errs.CodeUnauthenticated {
		t.Fatalf("anonymous: %v", err)
	}
}

func TestValidateSearch(t *testing.T) {
	f, err := validateSearch(SearchInput{Q: "invoice"}, "")
	if err != nil || f.Page != 1 || f.Limit != defaultPageSize {
		t.Fatalf("defaults: %+v %v", f, err)
	}
	now := time.Now()
	for _, in := range []SearchInput{
		{Q: "x", Limit: maxPageSize + 1},
		{Q: "x", Page: totalCap/10 + 1, Limit: 10},
		{Q: "x", From: now, To: now.Add(-time.Hour)},
		{Q: ""},
	} {
		if _, err := validateSearch(in, ""); errs.CodeOf(err) != errs.CodeValidationFailed {
			t.Errorf("%+v: want validation_failed, got %v", in, err)
		}
	}
	if _, err := validateSearch(SearchInput{Q: "x", Page: totalCap / 10, Limit: 10}, ""); err != nil {
		t.Fatalf("last page within the cap: %v", err)
	}
}

func TestSwitchCatalogue(t *testing.T) {
	sw, ok := features.DefaultRegistry().Get(SwitchName)
	if !ok {
		t.Fatal("search.fullText missing from the catalogue")
	}
	if sw.Default.Bool == nil || *sw.Default.Bool {
		t.Fatal("default must be off")
	}
	if !features.Allows(sw, features.ScopeOrganization) || !features.Allows(sw, features.ScopeAccount) || features.Allows(sw, features.ScopeGlobal) {
		t.Fatalf("scopes %v", sw.Scopes)
	}
}

func TestSwitchCache(t *testing.T) {
	now := time.Unix(1000, 0)
	s := &Switches{Now: func() time.Time { return now }}
	s.remember("acc:a", true)
	if v, ok := s.cached("acc:a"); !ok || !v {
		t.Fatal("cached")
	}
	now = now.Add(defaultSwitchTTL + time.Second)
	if _, ok := s.cached("acc:a"); ok {
		t.Fatal("expired")
	}
	s.remember("acc:a", true)
	s.Forget("a")
	if _, ok := s.cached("acc:a"); ok {
		t.Fatal("forgotten")
	}
	off := &Switches{TTL: -1}
	off.remember("acc:a", true)
	if _, ok := off.cached("acc:a"); ok {
		t.Fatal("negative TTL disables the cache")
	}
}

func TestAcquireTurnSerialisesAnAccount(t *testing.T) {
	s := &Service{MinInterval: 30 * time.Millisecond}
	ctx := context.Background()
	rel, err := s.acquireTurn(ctx, "acc_a")
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan time.Time, 1)
	go func() {
		r2, err := s.acquireTurn(ctx, "acc_a")
		if err == nil {
			got <- time.Now()
			r2()
		}
	}()
	// Another account is not held up.
	relB, err := s.acquireTurn(ctx, "acc_b")
	if err != nil {
		t.Fatal(err)
	}
	relB()
	select {
	case <-got:
		t.Fatal("second fetch of the same account ran while the first held the turn")
	case <-time.After(20 * time.Millisecond):
	}
	released := time.Now()
	rel()
	select {
	case at := <-got:
		if at.Sub(released) < 25*time.Millisecond {
			t.Fatalf("no gap after release: %v", at.Sub(released))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("second fetch never ran")
	}
	cctx, cancel := context.WithCancel(ctx)
	rel3, _ := s.acquireTurn(ctx, "acc_c")
	cancel()
	if _, err := s.acquireTurn(cctx, "acc_c"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled wait: %v", err)
	}
	rel3()
	time.Sleep(50 * time.Millisecond)
	s.turnMu.Lock()
	left := len(s.turns)
	s.turnMu.Unlock()
	if left != 0 {
		t.Fatalf("turns leak: %d", left)
	}
}
