//go:build integration

package integrations

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/integrations/connectors"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

type mockSubmit struct {
	mu  sync.Mutex
	got []submitCall
	err error
}

type submitCall struct {
	AccountID string
	To        []string
	Subject   string
	Text      string
	Headers   map[string]string
	Org       string
	Key       string
}

func (m *mockSubmit) SubmitOutbound(ctx context.Context, accountID string, to []string, subject, text string, headers map[string]string, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	org, _ := store.OrganizationFrom(ctx)
	m.got = append(m.got, submitCall{AccountID: accountID, To: append([]string(nil), to...), Subject: subject, Text: text, Headers: headers, Org: org, Key: key})
	return m.err
}

func TestHandleCallbackSignedAndRejects(t *testing.T) {
	db := testDB(t)
	cipher := testCipher(t, db)
	org := store.NewID(store.PrefixOrganization)
	acc := store.NewID(store.PrefixAccount)
	sub := &mockSubmit{}
	eps, rts, sts := newFakeEndpoints(), newFakeRoutes(), newFakeSettings()
	svc := &Service{
		Repo: &Repo{DB: db}, Registry: DefaultRegistry(), Cipher: cipher, Submit: sub,
		Endpoints: eps, Routes: rts, Settings: sts, limiter: newCallbackLimiter(60),
	}
	ctx := orgCtx(org, tenancy.RoleOwner)
	v, _, err := svc.Install(ctx, TypeFeatherDesk, InstallInput{
		Config: map[string]any{
			"baseUrl": "https://desk.example", "accountId": acc, "mailboxId": "",
		},
		Secrets: map[string]string{"apiKey": "k", "callbackToken": "tok"},
	})
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"to":"user@example.com","subject":"Re: hi","bodyText":"thanks","inReplyTo":"<m1@x>","ticketId":"T1"}`)
	now := time.Now().UTC()
	ts := now.Unix()
	sig := connectors.SignFeatherDesk("tok", ts, body)

	req := httptest.NewRequest(http.MethodPost, "/integrations/"+v.ID+"/callback", bytes.NewReader(body))
	req.Header.Set("X-FeatherDesk-Timestamp", fmt.Sprintf("%d", ts))
	req.Header.Set("X-FeatherDesk-Signature", sig)
	if err := svc.HandleCallback(context.Background(), v.ID, req); err != nil {
		t.Fatal(err)
	}
	sub.mu.Lock()
	if len(sub.got) != 1 || sub.got[0].AccountID != acc || sub.got[0].Org != org || sub.got[0].To[0] != "user@example.com" {
		t.Fatalf("%+v", sub.got)
	}
	if sub.got[0].Headers["In-Reply-To"] != "<m1@x>" {
		t.Fatalf("headers %+v", sub.got[0].Headers)
	}
	sub.mu.Unlock()

	// Bad signature → 401
	req2 := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	req2.Header.Set("X-FeatherDesk-Timestamp", fmt.Sprintf("%d", ts))
	req2.Header.Set("X-FeatherDesk-Signature", "sha256=deadbeef")
	err = svc.HandleCallback(context.Background(), v.ID, req2)
	if errs.CodeOf(err) != errs.CodeUnauthenticated {
		t.Fatalf("bad sig: %v", err)
	}

	// Old timestamp → 401
	old := now.Add(-10 * time.Minute).Unix()
	req3 := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	req3.Header.Set("X-FeatherDesk-Timestamp", fmt.Sprintf("%d", old))
	req3.Header.Set("X-FeatherDesk-Signature", connectors.SignFeatherDesk("tok", old, body))
	err = svc.HandleCallback(context.Background(), v.ID, req3)
	if errs.CodeOf(err) != errs.CodeUnauthenticated {
		t.Fatalf("old ts: %v", err)
	}
}

func TestRunDigestsAndRawCallback(t *testing.T) {
	db := testDB(t)
	cipher := testCipher(t, db)
	var posts int
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts++
		w.WriteHeader(http.StatusOK)
	}))
	defer hook.Close()
	org := store.NewID(store.PrefixOrganization)
	eps, rts, sts := newFakeEndpoints(), newFakeRoutes(), newFakeSettings()
	svc := &Service{
		Repo: &Repo{DB: db}, Registry: DefaultRegistry(), Cipher: cipher, DB: db, HTTP: hook.Client(),
		Endpoints: eps, Routes: rts, Settings: sts, limiter: newCallbackLimiter(60),
	}
	ctx := orgCtx(org, tenancy.RoleOwner)
	v, _, err := svc.Install(ctx, TypeMattermost, InstallInput{
		Config: map[string]any{"webhookUrl": hook.URL, "digestMinutes": 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	_, _ = db.Collection("messages").InsertOne(ctx, map[string]any{
		"_id": store.NewID(store.PrefixMessage), "organizationId": org, "accountId": store.NewID(store.PrefixAccount),
		"mailboxId": "mbx_1", "subject": "Digest me", "internalDate": now.Add(-time.Second),
		"indexedAt": now, "emailId": "e9", "uid": 9, "uidValidity": 1, "flags": []string{},
	})
	svc.runDigests(context.Background())
	if posts < 1 {
		t.Fatalf("posts=%d", posts)
	}
	// rawCallback success + 401
	m := &Module{svc: svc}
	body := []byte(`{"to":"a@b.c","bodyText":"x","subject":"s"}`)
	// install featherdesk for callback
	acc := store.NewID(store.PrefixAccount)
	fv, _, err := svc.Install(ctx, TypeFeatherDesk, InstallInput{
		Config:  map[string]any{"baseUrl": "https://desk.example", "accountId": acc},
		Secrets: map[string]string{"apiKey": "k", "callbackToken": "tok"},
	})
	if err != nil {
		t.Fatal(err)
	}
	sub := &mockSubmit{}
	svc.Submit = sub
	ts := time.Now().UTC().Unix()
	sig := connectors.SignFeatherDesk("tok", ts, body)
	req := httptest.NewRequest(http.MethodPost, "/integrations/"+fv.ID+"/callback", bytes.NewReader(body))
	req.Header.Set("X-FeatherDesk-Timestamp", fmt.Sprintf("%d", ts))
	req.Header.Set("X-FeatherDesk-Signature", sig)
	rr := httptest.NewRecorder()
	// chi URL param: set via context
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("instanceId", fv.ID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	m.rawCallback(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("status %d body %s", rr.Code, rr.Body.String())
	}
	reqBad := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	rctx2 := chi.NewRouteContext()
	rctx2.URLParams.Add("instanceId", fv.ID)
	reqBad = reqBad.WithContext(context.WithValue(context.Background(), chi.RouteCtxKey, rctx2))
	rr2 := httptest.NewRecorder()
	m.rawCallback(rr2, reqBad)
	if rr2.Code != http.StatusUnauthorized {
		t.Fatalf("want 401 got %d", rr2.Code)
	}
	_ = v
}

func TestCallbackRateLimit(t *testing.T) {
	l := newCallbackLimiter(2)
	now := time.Now()
	if err := l.take("a", now); err != nil {
		t.Fatal(err)
	}
	if err := l.take("a", now); err != nil {
		t.Fatal(err)
	}
	if err := l.take("a", now); errs.CodeOf(err) != errs.CodeRateLimited {
		t.Fatalf("%v", err)
	}
}

func TestDigestPostsToMockWebhook(t *testing.T) {
	db := testDB(t)
	cipher := testCipher(t, db)
	var gotBody string
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b := new(bytes.Buffer)
		_, _ = b.ReadFrom(r.Body)
		gotBody = b.String()
		w.WriteHeader(http.StatusOK)
	}))
	defer hook.Close()

	org := store.NewID(store.PrefixOrganization)
	eps, rts, sts := newFakeEndpoints(), newFakeRoutes(), newFakeSettings()
	svc := &Service{
		Repo: &Repo{DB: db}, Registry: DefaultRegistry(), Cipher: cipher, DB: db, HTTP: hook.Client(),
		Endpoints: eps, Routes: rts, Settings: sts,
	}
	ctx := orgCtx(org, tenancy.RoleOwner)
	v, _, err := svc.Install(ctx, TypeSlack, InstallInput{
		Config: map[string]any{"webhookUrl": hook.URL, "digestMinutes": 5},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Seed index messages.
	now := time.Now().UTC()
	_, err = db.Collection("messages").InsertMany(ctx, []any{
		map[string]any{
			"_id": store.NewID(store.PrefixMessage), "organizationId": org, "accountId": store.NewID(store.PrefixAccount),
			"mailboxId": "mbx_1", "subject": "Hello digest", "internalDate": now.Add(-time.Minute),
			"indexedAt": now, "emailId": "e1", "uid": 1, "uidValidity": 1, "flags": []string{},
		},
		map[string]any{
			"_id": store.NewID(store.PrefixMessage), "organizationId": org, "accountId": store.NewID(store.PrefixAccount),
			"mailboxId": "mbx_1", "subject": "Second", "internalDate": now.Add(-30 * time.Second),
			"indexedAt": now, "emailId": "e2", "uid": 2, "uidValidity": 1, "flags": []string{},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	inst, err := svc.Repo.get(ctx, v.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.postDigest(ctx, inst, now, 5); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(gotBody, "Hello digest") || !strings.Contains(gotBody, "2 new") {
		t.Fatalf("body %q", gotBody)
	}
	inst2, _ := svc.Repo.get(ctx, v.ID)
	if inst2.LastDigestAt == nil || inst2.LastDigestInternalAt == nil {
		t.Fatal("cursor not saved")
	}
	gotBody = ""
	// Second run with same cursor should post empty and advance (no new mail).
	if err := svc.postDigest(ctx, inst2, now.Add(time.Minute), 5); err != nil {
		t.Fatal(err)
	}
	if gotBody != "" {
		t.Fatalf("expected no post on empty, got %q", gotBody)
	}
}

func TestCallbackKeyIsStablePerReply(t *testing.T) {
	a := callbackKey("int_1", "<m1@fd>", []byte("x"))
	if a != callbackKey("int_1", " <m1@fd> ", []byte("other body")) {
		t.Fatal("same reply ID must give the same key")
	}
	if a == callbackKey("int_2", "<m1@fd>", []byte("x")) {
		t.Fatal("keys are per instance")
	}
	b := callbackKey("int_1", "", []byte("body"))
	if b != callbackKey("int_1", "", []byte("body")) || b == callbackKey("int_1", "", []byte("body2")) {
		t.Fatal("without an ID the body decides")
	}
}
