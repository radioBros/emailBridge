package sysmail

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/bus"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/store"
)

type fakeAccountSender struct {
	mu    sync.Mutex
	calls []sendCall
	err   error
	id    string
}

type sendCall struct {
	accountID, from, to string
	raw                 []byte
}

func (f *fakeAccountSender) SendSystem(_ context.Context, accountID, from, to string, raw []byte) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, sendCall{accountID: accountID, from: from, to: to, raw: append([]byte(nil), raw...)})
	if f.err != nil {
		return "", f.err
	}
	if f.id == "" {
		return "out_test", nil
	}
	return f.id, nil
}

func (f *fakeAccountSender) last() sendCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) == 0 {
		return sendCall{}
	}
	return f.calls[len(f.calls)-1]
}

type fakeChecker struct {
	err error
	n   int
}

func (c *fakeChecker) Check(context.Context, string) error {
	c.n++
	return c.err
}

func testAccountID(t *testing.T) string {
	t.Helper()
	return store.NewID(store.PrefixAccount)
}

func TestActivateAccountValidation(t *testing.T) {
	t.Parallel()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	id := testAccountID(t)
	sender := &fakeAccountSender{}

	s := New(Options{Mode: ModeAccount, AccountID: "", Account: sender, Log: log}, &memPub{})
	if s.ActivateAccount(context.Background()) {
		t.Fatal("empty AccountID must fail")
	}
	if err := s.Enqueue(context.Background(), Mail{Kind: KindAlert, To: "test@example.com"}); errs.CodeOf(err) != errs.CodeFeatureDisabled {
		t.Fatalf("got %v", err)
	}

	s = New(Options{Mode: ModeAccount, AccountID: "not-an-id", Account: sender, Log: log}, &memPub{})
	if s.ActivateAccount(context.Background()) {
		t.Fatal("bad id must fail")
	}

	s = New(Options{Mode: ModeAccount, AccountID: id, Account: nil, Log: log}, &memPub{})
	if s.ActivateAccount(context.Background()) {
		t.Fatal("nil sender must fail")
	}

	check := &fakeChecker{err: errs.New(errs.CodeAccountDisabled, "disabled")}
	s = New(Options{Mode: ModeAccount, AccountID: id, Account: sender, CheckAccount: check, Log: log}, &memPub{})
	if s.ActivateAccount(context.Background()) {
		t.Fatal("disabled account must fail")
	}
	if check.n != 1 {
		t.Fatal("checker not called")
	}

	check = &fakeChecker{}
	s = New(Options{Mode: ModeAccount, AccountID: id, Account: sender, CheckAccount: check, Log: log}, &memPub{})
	if !s.ActivateAccount(context.Background()) {
		t.Fatal("want ok")
	}
	if !s.accountEnabled() {
		t.Fatal("accountOK")
	}
}

func TestEnqueueAccountWhenEnabled(t *testing.T) {
	t.Parallel()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	pub := &memPub{}
	id := testAccountID(t)
	s := New(Options{
		Mode: ModeAccount, AccountID: id, Account: &fakeAccountSender{}, From: "noreply@example.com",
		Env: "development", Log: log,
	}, pub)
	if !s.ActivateAccount(context.Background()) {
		t.Fatal("activate")
	}
	if err := s.Enqueue(context.Background(), Mail{
		Kind: KindAlert, To: "test@example.com", Locale: "en",
		Data: map[string]string{"Title": "t", "Body": "b"},
	}); err != nil {
		t.Fatal(err)
	}
	if pub.n != 1 {
		t.Fatalf("publish n=%d", pub.n)
	}
}

func TestAccountWorkerAllKindsAndPolicy(t *testing.T) {
	t.Parallel()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	id := testAccountID(t)
	from := "EmailBridge <noreply@example.com>"
	fake := &fakeAccountSender{id: "out_sys_1"}
	s := New(Options{
		Mode: ModeAccount, AccountID: id, Account: fake, From: from,
		Env: "development", AllowList: []string{"test@example.com"}, Log: log,
	}, &memPub{})
	if !s.ActivateAccount(context.Background()) {
		t.Fatal("activate")
	}

	kinds := []struct {
		kind string
		data map[string]string
	}{
		{KindMFACode, map[string]string{"Code": "654321"}},
		{KindInvitation, map[string]string{"Name": "Ada", "InviteURL": "https://eb/i", "InviterName": "Bob", "OrgName": "Acme"}},
		{KindPasswordReset, map[string]string{"ResetURL": "https://eb/r"}},
		{KindEmailVerification, map[string]string{"VerifyURL": "https://eb/v"}},
		{KindAlert, map[string]string{"Title": "Disk", "Body": "full"}},
	}
	for _, k := range kinds {
		j, _ := json.Marshal(job{ID: "job_" + k.kind, Kind: k.kind, To: "test@example.com", Locale: "en", Data: k.data})
		if err := s.HandleJob(context.Background(), j); err != nil {
			t.Fatalf("%s: %v", k.kind, err)
		}
		c := fake.last()
		if c.accountID != id || c.from != from || c.to != "test@example.com" || len(c.raw) == 0 {
			t.Fatalf("%s: call %+v", k.kind, c)
		}
		if !strings.Contains(string(c.raw), "multipart/alternative") && !strings.Contains(string(c.raw), "text/plain") {
			t.Fatalf("%s: bad MIME", k.kind)
		}
	}
	fake.mu.Lock()
	nCalls := len(fake.calls)
	fake.mu.Unlock()
	if nCalls != len(kinds) {
		t.Fatalf("calls %d", nCalls)
	}

	j, _ := json.Marshal(job{ID: "job_drop", Kind: KindAlert, To: "evil@x.com", Locale: "en", Data: map[string]string{"Title": "t", "Body": "b"}})
	if err := s.HandleJob(context.Background(), j); err != nil {
		t.Fatal(err)
	}
	fake.mu.Lock()
	after := len(fake.calls)
	fake.mu.Unlock()
	if after != nCalls {
		t.Fatal("allow-list must drop before submit")
	}
}

func TestAccountWorkerSubmitFailure(t *testing.T) {
	t.Parallel()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	id := testAccountID(t)
	fake := &fakeAccountSender{err: errs.New(errs.CodeSMTPTemporary, "later")}
	s := New(Options{
		Mode: ModeAccount, AccountID: id, Account: fake, From: "a@b", Env: "production", Log: log,
	}, &memPub{})
	if !s.ActivateAccount(context.Background()) {
		t.Fatal("activate")
	}
	j, _ := json.Marshal(job{ID: "job_r", Kind: KindAlert, To: "x@y.com", Locale: "en", Data: map[string]string{"Title": "t", "Body": "b"}})
	err := s.HandleJob(context.Background(), j)
	if !errs.Retryable(err) {
		t.Fatalf("want retryable, got %v", err)
	}

	fake.err = errs.New(errs.CodeAccountDisabled, "gone")
	if err := s.HandleJob(context.Background(), j); err != nil {
		t.Fatal("permanent must ack", err)
	}
}

func TestAccountWorkerUnavailableAcks(t *testing.T) {
	t.Parallel()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := New(Options{Mode: ModeAccount, AccountID: testAccountID(t), From: "a@b", Env: "production", Log: log}, &memPub{})
	j, _ := json.Marshal(job{ID: "1", Kind: KindAlert, To: "x@y.com", Data: map[string]string{"Title": "t", "Body": "b"}})
	if err := s.HandleJob(context.Background(), j); err != nil {
		t.Fatal(err)
	}
}

func TestModuleBindAccountSender(t *testing.T) {
	b, err := bus.Open(context.Background(), bus.Options{Mode: bus.ModeEmbedded, DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(b.Close)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	a := app.New(app.Options{NodeID: "n1", Log: log})
	app.Provide(a, b)
	id := testAccountID(t)
	m := &Module{Options: Options{Mode: ModeAccount, AccountID: id, From: "noreply@example.com", Env: "production", Log: log}}
	if err := m.Start(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	svc, ok := app.Get[*Service](a)
	if !ok {
		t.Fatal("service")
	}
	if err := svc.Enqueue(context.Background(), Mail{Kind: KindAlert, To: "u@x.com", Data: map[string]string{"Title": "t", "Body": "b"}}); errs.CodeOf(err) != errs.CodeFeatureDisabled {
		t.Fatalf("before bind: %v", err)
	}

	fake := &fakeAccountSender{id: "out_m"}
	check := &fakeChecker{}
	m.BindAccountSender(context.Background(), fake, check)
	if check.n != 1 || !svc.accountEnabled() {
		t.Fatalf("bound check=%d ok=%v", check.n, svc.accountEnabled())
	}
	if m.stop == nil {
		t.Fatal("worker should start after bind")
	}

	if err := svc.Enqueue(context.Background(), Mail{
		Kind: KindAlert, To: "user@example.com", Locale: "en",
		Data: map[string]string{"Title": "Hello", "Body": "World"},
	}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		fake.mu.Lock()
		n := len(fake.calls)
		fake.mu.Unlock()
		if n >= 1 {
			c := fake.last()
			if c.to != "user@example.com" || !strings.Contains(string(c.raw), "Hello") {
				t.Fatalf("call %+v", c)
			}
			_ = m.Stop(context.Background())
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("timeout waiting for account submit")
}

func TestModuleAccountStartWithSender(t *testing.T) {
	b, err := bus.Open(context.Background(), bus.Options{Mode: bus.ModeEmbedded, DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(b.Close)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	a := app.New(app.Options{NodeID: "n1", Log: log})
	app.Provide(a, b)
	id := testAccountID(t)
	fake := &fakeAccountSender{}
	m := &Module{Options: Options{
		Mode: ModeAccount, AccountID: id, Account: fake, CheckAccount: &fakeChecker{},
		From: "a@b", Env: "production", Log: log,
	}}
	if err := m.Start(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	if m.stop == nil {
		t.Fatal("worker should start when sender already set")
	}
	_ = m.Stop(context.Background())
}

func TestBindAccountSenderWrongMode(t *testing.T) {
	t.Parallel()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	m := &Module{Options: Options{Mode: ModeSMTP, Log: log}}
	a := app.New(app.Options{NodeID: "n1", Log: log})
	if err := m.Start(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	m.BindAccountSender(context.Background(), &fakeAccountSender{}, nil)
	if m.svc.accountEnabled() {
		t.Fatal("smtp mode must not enable account")
	}
}

func TestSetAccountNilService(t *testing.T) {
	t.Parallel()
	(*Service)(nil).SetAccount(nil, nil)
	if (*Service)(nil).ActivateAccount(context.Background()) {
		t.Fatal()
	}
	(*Module)(nil).BindAccountSender(context.Background(), nil, nil)
}
