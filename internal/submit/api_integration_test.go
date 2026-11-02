//go:build integration

package submit

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/submit/policy"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

// codeOf is errs.CodeOf with "" for success (errs.CodeOf(nil) is internal_error).
func codeOf(err error) errs.Code {
	if err == nil {
		return ""
	}
	return errs.CodeOf(err)
}

// caller returns a context for a user of one organisation in one role.
func (h *harness) caller(org string, role tenancy.Role) context.Context {
	return tenancy.WithCaller(context.Background(), tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: store.NewID(store.PrefixUser),
		OrganizationID: org, Role: role,
	})
}

// key returns a context for an API key, optionally bound to one account.
func (h *harness) key(org, accountID string, scopes ...string) context.Context {
	return tenancy.WithCaller(context.Background(), tenancy.Caller{
		Kind: tenancy.CallerAPIKey, APIKeyID: store.NewID(store.PrefixAPIKey),
		OrganizationID: org, Scopes: scopes, AccountID: accountID,
	})
}

func TestIntegrationRolesAndScopes(t *testing.T) {
	h := newHarness(t)
	for _, tc := range []struct {
		name    string
		ctx     context.Context
		code    errs.Code
		canRead bool
	}{
		{"owner", h.caller(h.org, tenancy.RoleOwner), "", true},
		{"admin", h.caller(h.org, tenancy.RoleAdmin), "", true},
		{"operator", h.caller(h.org, tenancy.RoleOperator), "", true},
		{"viewer", h.caller(h.org, tenancy.RoleViewer), errs.CodeForbidden, true},
		{"key outbound:write", h.key(h.org, "", "outbound:write"), "", true},
		{"key logs:read", h.key(h.org, "", "logs:read"), errs.CodeScopeMissing, false}, // reads need outbound:read (S10 §3)
		{"key bound to the account", h.key(h.org, h.acc.ID, "outbound:write"), "", true},
		{"key bound elsewhere", h.key(h.org, store.NewID(store.PrefixAccount), "outbound:write"), errs.CodeForbidden, true},
		{"no caller", context.Background(), errs.CodeUnauthenticated, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := h.svc.Submit(tc.ctx, h.acc.ID, SubmitRequest{
				To: []AddressInput{{Address: testTo}}, Subject: "RBAC", Text: "x",
			}, "", false)
			if got := codeOf(err); got != tc.code {
				t.Fatalf("submit: %v (code %s, want %s)", err, got, tc.code)
			}
			_, err = h.svc.List(tc.ctx, ListFilter{})
			if tc.canRead && err != nil {
				t.Fatalf("list: %v", err)
			}
			if !tc.canRead && errs.CodeOf(err) != errs.CodeUnauthenticated && errs.CodeOf(err) != errs.CodeScopeMissing {
				t.Fatalf("list without read access: %v", err)
			}
		})
	}
	// Cancelling needs the send permission too.
	id := h.submit(SubmitRequest{})
	if _, err := h.svc.Cancel(h.caller(h.org, tenancy.RoleViewer), id, ""); errs.CodeOf(err) != errs.CodeForbidden {
		t.Fatalf("viewer cancel: %v", err)
	}
	if _, err := h.svc.Get(h.caller(h.org, tenancy.RoleViewer), id); err != nil {
		t.Fatalf("viewer read: %v", err)
	}
}

func TestIntegrationTenantIsolation(t *testing.T) {
	h := newHarness(t)
	id := h.submit(SubmitRequest{})
	other := h.caller(store.NewID(store.PrefixOrganization), tenancy.RoleOwner)
	if _, err := h.svc.Get(other, id); errs.CodeOf(err) != errs.CodeNotFound {
		t.Fatalf("cross-tenant get: %v", err)
	}
	if _, err := h.svc.Cancel(other, id, ""); errs.CodeOf(err) != errs.CodeNotFound {
		t.Fatalf("cross-tenant cancel: %v", err)
	}
	list, err := h.svc.List(other, ListFilter{})
	if err != nil || len(list.Data) != 0 {
		t.Fatalf("cross-tenant list: %+v (%v)", list, err)
	}
	// An account of another organisation cannot be used, even by its owner.
	foreign := &SMTPAccount{
		ID: store.NewID(store.PrefixAccount), OrganizationID: store.NewID(store.PrefixOrganization),
		State: "connected", Email: testUser, Host: h.acc.Host, Port: h.acc.Port,
	}
	h.accounts.put(foreign)
	if _, err := h.svc.Submit(h.ctx, foreign.ID, SubmitRequest{
		To: []AddressInput{{Address: testTo}}, Text: "x",
	}, "", false); errs.CodeOf(err) != errs.CodeNotFound {
		t.Fatalf("foreign account: %v", err)
	}
	// An account-bound key only sees its own account in the outbox.
	bound := h.key(h.org, store.NewID(store.PrefixAccount), "outbound:write")
	list, err = h.svc.List(bound, ListFilter{})
	if err != nil || len(list.Data) != 0 {
		t.Fatalf("bound key list: %+v (%v)", list, err)
	}
	if _, err := h.svc.List(bound, ListFilter{AccountID: h.acc.ID}); errs.CodeOf(err) != errs.CodeForbidden {
		t.Fatalf("bound key asking for another account: %v", err)
	}
}

func TestIntegrationIdempotentSubmit(t *testing.T) {
	h := newHarness(t)
	req := SubmitRequest{To: []AddressInput{{Address: testTo}}, Subject: "Once", Text: "only once"}
	first, err := h.svc.Submit(h.ctx, h.acc.ID, req, "key-1", false)
	if err != nil || first.Replayed || first.Status != http.StatusAccepted {
		t.Fatalf("first submit: %+v (%v)", first, err)
	}
	second, err := h.svc.Submit(h.ctx, h.acc.ID, req, "key-1", false)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if !second.Replayed || second.Accepted.ID != first.Accepted.ID {
		t.Fatalf("replay returned %+v", second.Accepted)
	}
	count, err := h.db.Collection(Collection).CountDocuments(h.ctx, map[string]any{"organizationId": h.org})
	if err != nil || count != 1 {
		t.Fatalf("stored %d messages (%v)", count, err)
	}
	// The same key with a different body is a conflict.
	other := req
	other.Subject = "Twice"
	if _, err := h.svc.Submit(h.ctx, h.acc.ID, other, "key-1", false); errs.CodeOf(err) != errs.CodeIdempotencyKeyReused {
		t.Fatalf("reused key: %v", err)
	}
	// A different key sends a second message.
	third, err := h.svc.Submit(h.ctx, h.acc.ID, req, "key-2", false)
	if err != nil || third.Accepted.ID == first.Accepted.ID {
		t.Fatalf("second key: %+v (%v)", third, err)
	}
	for _, bad := range []string{strings.Repeat("k", 256), "key\n1", "ke\x00y"} {
		if _, err := h.svc.Submit(h.ctx, h.acc.ID, req, bad, false); errs.CodeOf(err) != errs.CodeValidationFailed {
			t.Fatalf("bad key %q: %v", bad, err)
		}
	}
	// The record expires after the TTL window (S2 §2).
	var rec struct {
		ExpireAt time.Time `bson:"expireAt"`
		Status   int       `bson:"status"`
	}
	if err := h.db.Collection(IdempotencyCollection).FindOne(h.ctx,
		map[string]any{"_id": idempotencyID(h.org, h.acc.ID, http.MethodPost, submitRoute, "key-1")}).Decode(&rec); err != nil {
		t.Fatal(err)
	}
	if rec.Status != http.StatusAccepted || rec.ExpireAt.Before(time.Now().Add(23*time.Hour)) {
		t.Fatalf("idempotency record: %+v", rec)
	}
}

func TestIntegrationValidation(t *testing.T) {
	h := newHarness(t)
	raw := encode64("From: a@b.test\r\nTo: c@d.test\r\nSubject: R\r\n\r\nbody\r\n")
	for _, tc := range []struct {
		name string
		req  SubmitRequest
		code errs.Code
	}{
		{"empty", SubmitRequest{}, errs.CodeValidationFailed},
		{"raw and fields", SubmitRequest{Raw: raw, Subject: "x", Text: "y"}, errs.CodeRawAndFields},
		{"raw not base64", SubmitRequest{Raw: "not base64 ***"}, errs.CodeValidationFailed},
		{"no recipients", SubmitRequest{Subject: "x", Text: "y"}, errs.CodeNoRecipients},
		{"bad address", SubmitRequest{To: []AddressInput{{Address: "not-an-address"}}, Text: "y"}, errs.CodeInvalidAddress},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := h.svc.Submit(h.ctx, h.acc.ID, tc.req, "", false); errs.CodeOf(err) != tc.code {
				t.Fatalf("%v (code %s, want %s)", err, errs.CodeOf(err), tc.code)
			}
		})
	}
	if _, err := h.svc.Submit(h.ctx, "not-an-id", SubmitRequest{Text: "y"}, "", false); errs.CodeOf(err) != errs.CodeNotFound {
		t.Fatalf("bad account id: %v", err)
	}
	if _, err := h.svc.List(h.ctx, ListFilter{State: "nonsense"}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatal("unknown state filter accepted")
	}
	if _, err := h.svc.List(h.ctx, ListFilter{Sort: "password"}); errs.CodeOf(err) != errs.CodeSortNotAllowed {
		t.Fatal("unknown sort accepted")
	}
}

func TestIntegrationMessageTooLarge(t *testing.T) {
	h := newHarness(t)
	h.svc.MaxMessageSize = 512
	_, err := h.svc.Submit(h.ctx, h.acc.ID, SubmitRequest{
		To: []AddressInput{{Address: testTo}}, Subject: "Big", Text: strings.Repeat("x", 2048),
	}, "", false)
	if errs.CodeOf(err) != errs.CodeMessageTooLarge {
		t.Fatalf("%v", err)
	}
}

func TestIntegrationQuotas(t *testing.T) {
	h := newHarness(t)
	h.svc.QuotaSendsPerMinute = 1
	h.submit(SubmitRequest{})
	_, err := h.svc.Submit(h.ctx, h.acc.ID, SubmitRequest{
		To: []AddressInput{{Address: testTo}}, Text: "second",
	}, "", false)
	if errs.CodeOf(err) != errs.CodeQuotaSendsExceeded {
		t.Fatalf("per-account quota: %v", err)
	}
	h.svc.QuotaSendsPerMinute, h.svc.QuotaSendsPerDay = 0, 1
	if _, err := h.svc.Submit(h.ctx, h.acc.ID, SubmitRequest{
		To: []AddressInput{{Address: testTo}}, Text: "third",
	}, "", false); errs.CodeOf(err) != errs.CodeQuotaSendsExceeded {
		t.Fatalf("organisation quota: %v", err)
	}
	// A dry run is not a send and is not counted.
	res, err := h.svc.Submit(h.ctx, h.acc.ID, SubmitRequest{
		To: []AddressInput{{Address: testTo}}, Text: "dry", DryRun: true,
	}, "", true)
	if err != nil || res.DryRun == nil || res.DryRun.Raw == "" || res.Status != http.StatusOK {
		t.Fatalf("dry run: %+v (%v)", res, err)
	}
	if res.DryRun.Size == 0 || res.DryRun.MessageID == "" || len(res.DryRun.Envelope.To) != 1 {
		t.Fatalf("dry-run result: %+v", res.DryRun)
	}
}

func TestIntegrationAccountStates(t *testing.T) {
	h := newHarness(t)
	for _, tc := range []struct {
		state string
		code  errs.Code
	}{
		{"connected", ""},
		{"authError", ""}, // only IMAP failed; SMTP stays available (S6 §2.1)
		{"disabled", errs.CodeAccountDisabled},
		{"deleting", errs.CodeAccountDeleting},
	} {
		h.acc.State = tc.state
		h.accounts.put(h.acc)
		_, err := h.svc.Submit(h.ctx, h.acc.ID, SubmitRequest{
			To: []AddressInput{{Address: testTo}}, Text: tc.state,
		}, "", false)
		if codeOf(err) != tc.code {
			t.Errorf("state %s: %v (want %s)", tc.state, err, tc.code)
		}
	}
	h.acc.State, h.acc.Host = "connected", ""
	h.accounts.put(h.acc)
	if _, err := h.svc.Submit(h.ctx, h.acc.ID, SubmitRequest{
		To: []AddressInput{{Address: testTo}}, Text: "no smtp",
	}, "", false); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatal("an account without SMTP settings was accepted")
	}
}

func TestIntegrationListPagination(t *testing.T) {
	h := newHarness(t)
	ids := make([]string, 0, 5)
	for range 5 {
		ids = append(ids, h.submit(SubmitRequest{}))
	}
	page, err := h.svc.List(h.ctx, ListFilter{Limit: 2})
	if err != nil || len(page.Data) != 2 || page.NextCursor == nil {
		t.Fatalf("first page: %+v (%v)", page, err)
	}
	if page.Data[0].ID != ids[4] {
		t.Fatalf("newest first: %s", page.Data[0].ID)
	}
	seen := map[string]bool{}
	cursor := *page.NextCursor
	for _, v := range page.Data {
		seen[v.ID] = true
	}
	for cursor != "" {
		next, err := h.svc.List(h.ctx, ListFilter{Limit: 2, Cursor: cursor})
		if err != nil {
			t.Fatal(err)
		}
		for _, v := range next.Data {
			if seen[v.ID] {
				t.Fatalf("%s came twice", v.ID)
			}
			seen[v.ID] = true
		}
		cursor = ""
		if next.NextCursor != nil {
			cursor = *next.NextCursor
		}
	}
	if len(seen) != 5 {
		t.Fatalf("the cursor walk saw %d of 5", len(seen))
	}
	paged, err := h.svc.List(h.ctx, ListFilter{Limit: 2, Page: 2, IncludeTotal: true, Sort: "createdAt"})
	if err != nil || len(paged.Data) != 2 || paged.Total == nil || *paged.Total != 5 || *paged.TotalCapped {
		t.Fatalf("page 2: %+v (%v)", paged, err)
	}
	if _, err := h.svc.List(h.ctx, ListFilter{Cursor: "***"}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatal("a broken cursor was accepted")
	}
	// Filters.
	filtered, err := h.svc.List(h.ctx, ListFilter{State: StateQueued, AccountID: h.acc.ID,
		MessageID: h.get(ids[0]).MessageIDHeader, To: testTo, From: testUser})
	if err != nil || len(filtered.Data) != 1 || filtered.Data[0].ID != ids[0] {
		t.Fatalf("filters: %+v (%v)", filtered, err)
	}
}

// TestIntegrationHTTPRoundTrip drives the four operations over HTTP, with the ETag and If-Match handling.
func TestIntegrationHTTPRoundTrip(t *testing.T) {
	h := newHarness(t)
	server, err := httpapi.New(httpapi.Options{})
	if err != nil {
		t.Fatal(err)
	}
	module := &Module{service: h.svc}
	module.registerAPI(server)
	owner := h.caller(h.org, tenancy.RoleOwner)

	do := func(method, path, body string, header map[string]string, ctx context.Context) *httptest.ResponseRecorder {
		var reader *strings.Reader
		if body != "" {
			reader = strings.NewReader(body)
		} else {
			reader = strings.NewReader("")
		}
		request := httptest.NewRequest(method, path, reader).WithContext(ctx)
		request.Header.Set("Content-Type", "application/json")
		for k, v := range header {
			request.Header.Set(k, v)
		}
		response := httptest.NewRecorder()
		server.ServeHTTP(response, request)
		return response
	}

	// A dry run answers 200 with the built message.
	dry := do(http.MethodPost, "/v1/accounts/"+h.acc.ID+"/outbound-messages?includeRaw=true",
		`{"to":[{"address":"`+testTo+`"}],"subject":"Dry","text":"x","dryRun":true}`, nil, owner)
	if dry.Code != http.StatusOK {
		t.Fatalf("dry run: %d %s", dry.Code, dry.Body.String())
	}
	var dryBody DryRunResult
	if err := json.Unmarshal(dry.Body.Bytes(), &dryBody); err != nil {
		t.Fatal(err)
	}
	if _, err := base64.StdEncoding.DecodeString(dryBody.Raw); err != nil || dryBody.MessageID == "" {
		t.Fatalf("dry-run body: %+v", dryBody)
	}

	// A real submit answers 202 with Location, and replays with Idempotent-Replayed.
	payload := `{"to":[{"address":"` + testTo + `"}],"subject":"HTTP","text":"x","policy":"atMostOnce"}`
	accepted := do(http.MethodPost, "/v1/accounts/"+h.acc.ID+"/outbound-messages", payload,
		map[string]string{"Idempotency-Key": "http-1"}, owner)
	if accepted.Code != http.StatusAccepted {
		t.Fatalf("submit: %d %s", accepted.Code, accepted.Body.String())
	}
	var body Accepted
	if err := json.Unmarshal(accepted.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if accepted.Header().Get("Location") != "/v1/outbound-messages/"+body.ID {
		t.Fatalf("Location: %q", accepted.Header().Get("Location"))
	}
	replay := do(http.MethodPost, "/v1/accounts/"+h.acc.ID+"/outbound-messages", payload,
		map[string]string{"Idempotency-Key": "http-1"}, owner)
	if replay.Code != http.StatusAccepted || replay.Header().Get("Idempotent-Replayed") != "true" {
		t.Fatalf("replay: %d %v", replay.Code, replay.Header())
	}

	got := do(http.MethodGet, "/v1/outbound-messages/"+body.ID, "", nil, owner)
	if got.Code != http.StatusOK {
		t.Fatalf("get: %d %s", got.Code, got.Body.String())
	}
	etag := got.Header().Get("ETag")
	var view View
	if err := json.Unmarshal(got.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if view.Policy != policy.AtMostOnce || view.State != StateQueued || etag == "" {
		t.Fatalf("view: %+v (etag %q)", view, etag)
	}

	listed := do(http.MethodGet, "/v1/outbound-messages?limit=10&state=queued", "", nil, owner)
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), body.ID) {
		t.Fatalf("list: %d %s", listed.Code, listed.Body.String())
	}

	stale := do(http.MethodDelete, "/v1/outbound-messages/"+body.ID, "",
		map[string]string{"If-Match": `"v99"`}, owner)
	if stale.Code != http.StatusConflict && stale.Code != http.StatusPreconditionFailed {
		t.Fatalf("stale If-Match: %d %s", stale.Code, stale.Body.String())
	}
	cancelled := do(http.MethodDelete, "/v1/outbound-messages/"+body.ID, "",
		map[string]string{"If-Match": etag}, owner)
	if cancelled.Code != http.StatusOK || !strings.Contains(cancelled.Body.String(), StateCancelled) {
		t.Fatalf("cancel: %d %s", cancelled.Code, cancelled.Body.String())
	}
	if again := do(http.MethodDelete, "/v1/outbound-messages/"+body.ID, "", nil, owner); again.Code == http.StatusOK {
		t.Fatalf("cancelling twice: %d %s", again.Code, again.Body.String())
	}
	missing := do(http.MethodGet, "/v1/outbound-messages/"+store.NewID(store.PrefixOutbound), "", nil, owner)
	if missing.Code != http.StatusNotFound {
		t.Fatalf("unknown message: %d", missing.Code)
	}
	forbidden := do(http.MethodPost, "/v1/accounts/"+h.acc.ID+"/outbound-messages", payload, nil,
		h.caller(h.org, tenancy.RoleViewer))
	if forbidden.Code != http.StatusForbidden {
		t.Fatalf("viewer submit: %d %s", forbidden.Code, forbidden.Body.String())
	}
}
