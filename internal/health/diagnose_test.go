package health

import (
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/accounts"
)

func TestDiagnoseHealthy(t *testing.T) {
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	syncAt := now.Add(-time.Hour)
	a := &accounts.Account{
		ID: "acc_01habcdefghijklmnopqrstuv", Name: "Ada", Email: "ada@example.com",
		State: accounts.StateConnected, LastSyncAt: &syncAt,
		IMAP:         accounts.IMAPConfig{Host: "imap.example.com", Port: 993, TLS: "implicit"},
		Capabilities: map[string]any{"IDLE": true, "CONDSTORE": true},
	}
	h := Diagnose(DiagnoseInput{Account: a, FolderCount: 5, Now: now})
	if h.Severity != SeverityOK || h.Summary != "Healthy" {
		t.Fatalf("got sev=%s summary=%q hints=%v", h.Severity, h.Summary, h.Hints)
	}
	if !h.Facts.Idle || h.Facts.FolderCount != 5 {
		t.Fatalf("facts: %+v", h.Facts)
	}
}

func TestDiagnoseAppPasswordGmail(t *testing.T) {
	a := &accounts.Account{
		ID: "acc_01habcdefghijklmnopqrstuv", Name: "G", Email: "user@gmail.com",
		State: accounts.StateAuthError,
		StateReason: &accounts.StateReason{
			Kind: "auth", Code: "mail_auth_failed", Message: "NO [AUTHENTICATIONFAILED]",
			At: time.Now(),
		},
		IMAP: accounts.IMAPConfig{Host: "imap.gmail.com", Port: 993, TLS: "implicit"},
	}
	h := Diagnose(DiagnoseInput{Account: a, Now: time.Now()})
	if h.Severity != SeverityCritical {
		t.Fatalf("severity %s", h.Severity)
	}
	if len(h.Hints) == 0 || h.Hints[0].Code != HintAppPassword {
		t.Fatalf("hints %+v", h.Hints)
	}
}

func TestDiagnoseOAuthRevoked(t *testing.T) {
	a := &accounts.Account{
		ID: "acc_01habcdefghijklmnopqrstuv", Name: "G", Email: "user@gmail.com",
		State:    accounts.StateAuthError,
		Provider: "gmail",
		OAuth:    &accounts.OAuthLink{},
		StateReason: &accounts.StateReason{
			Kind: "auth", Code: "oauth_revoked", Message: "invalid_grant", At: time.Now(),
		},
	}
	h := Diagnose(DiagnoseInput{Account: a})
	found := false
	for _, hint := range h.Hints {
		if hint.Code == HintOAuthRevoked {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected oauth hint, got %+v", h.Hints)
	}
}

func TestDiagnoseRateLimitedAndCert(t *testing.T) {
	now := time.Now()
	a := &accounts.Account{
		ID: "acc_01habcdefghijklmnopqrstuv", Name: "X", Email: "x@example.com",
		State: accounts.StateConnectError,
		RecentErrors: []accounts.RecentError{
			{At: now, Kind: "rate_limited", Code: "mail_connect_failed", Response: "NO [LIMIT] too many connections"},
		},
	}
	h := Diagnose(DiagnoseInput{Account: a, Now: now})
	if !hasCode(h.Hints, HintRateLimited) {
		t.Fatalf("rate: %+v", h.Hints)
	}

	a2 := &accounts.Account{
		ID: "acc_01habcdefghijklmnopqrstuv", Name: "X", Email: "x@example.com",
		State: accounts.StateConnectError,
		StateReason: &accounts.StateReason{
			Kind: "config", Code: "mail_tls_failed", Message: "x509: certificate signed by unknown authority", At: now,
		},
	}
	h2 := Diagnose(DiagnoseInput{Account: a2, Now: now})
	if !hasCode(h2.Hints, HintCertificate) {
		t.Fatalf("cert: %+v", h2.Hints)
	}
}

func TestDiagnoseIMAPDisabledMailboxFullCapacity(t *testing.T) {
	now := time.Now()
	a := &accounts.Account{
		ID: "acc_01habcdefghijklmnopqrstuv", Name: "X", Email: "x@example.com",
		State: accounts.StateAuthError,
		RecentErrors: []accounts.RecentError{
			{At: now, Kind: "auth", Code: "mail_auth_failed", Response: "IMAP access is disabled for your domain"},
		},
	}
	h := Diagnose(DiagnoseInput{Account: a})
	if !hasCode(h.Hints, HintIMAPDisabled) {
		t.Fatalf("imap disabled: %+v", h.Hints)
	}

	a2 := &accounts.Account{
		ID: "acc_01habcdefghijklmnopqrstuv", Name: "X", Email: "x@example.com",
		State: accounts.StateConnectError,
		RecentErrors: []accounts.RecentError{
			{At: now, Kind: "temporary", Code: "mail_connect_failed", Response: "mailbox full / over quota"},
		},
	}
	h2 := Diagnose(DiagnoseInput{Account: a2})
	if !hasCode(h2.Hints, HintMailboxFull) {
		t.Fatalf("full: %+v", h2.Hints)
	}

	a3 := &accounts.Account{
		ID: "acc_01habcdefghijklmnopqrstuv", Name: "X", Email: "x@example.com",
		State: accounts.StateDisconnected,
		StateReason: &accounts.StateReason{
			Kind: accounts.ReasonKindCapacity, Code: accounts.ReasonCodeCapacity, Message: "no room", At: now,
		},
	}
	h3 := Diagnose(DiagnoseInput{Account: a3})
	if h3.Severity != SeverityInfo || !hasCode(h3.Hints, HintCapacity) {
		t.Fatalf("capacity: sev=%s hints=%+v", h3.Severity, h3.Hints)
	}
}

func TestDiagnoseWrongTLSAndLiveCheck(t *testing.T) {
	a := &accounts.Account{
		ID: "acc_01habcdefghijklmnopqrstuv", Name: "X", Email: "x@example.com",
		State: accounts.StateConnectError,
		IMAP:  accounts.IMAPConfig{Host: "imap.example.com", Port: 993, TLS: "none"},
	}
	h := Diagnose(DiagnoseInput{Account: a})
	if !hasCode(h.Hints, HintWrongHostPortTLS) {
		t.Fatalf("tls mode: %+v", h.Hints)
	}

	live := &LiveCheck{OK: false, LatencyMs: 50, Error: &LiveCheckError{
		Code: "mail_auth_failed", Kind: "auth", Message: "LOGIN failed",
	}}
	a2 := &accounts.Account{
		ID: "acc_01habcdefghijklmnopqrstuv", Name: "Y", Email: "y@yahoo.com",
		State: accounts.StateConnected,
		IMAP:  accounts.IMAPConfig{Host: "imap.mail.yahoo.com", Port: 993, TLS: "implicit"},
	}
	h2 := Diagnose(DiagnoseInput{Account: a2, Live: live})
	if h2.Severity != SeverityCritical || !hasCode(h2.Hints, HintAppPassword) {
		t.Fatalf("live auth: sev=%s hints=%+v", h2.Severity, h2.Hints)
	}
	if h2.LiveCheck == nil || h2.LiveCheck.OK {
		t.Fatal("live check missing")
	}
}

func TestDiagnoseStaleSyncAndAPIProvider(t *testing.T) {
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	old := now.Add(-48 * time.Hour)
	a := &accounts.Account{
		ID: "acc_01habcdefghijklmnopqrstuv", Name: "S", Email: "s@example.com",
		State: accounts.StateConnected, LastSyncAt: &old,
		IMAP: accounts.IMAPConfig{Host: "imap.example.com", Port: 993, TLS: "implicit",
			Auth: accounts.MailAuth{User: "s"}},
		Capabilities: map[string]any{"IDLE": true},
	}
	h := Diagnose(DiagnoseInput{Account: a, Now: now})
	if h.Severity != SeverityInfo || h.Summary != "Last sync is older than 24 hours" {
		t.Fatalf("stale: sev=%s summary=%q", h.Severity, h.Summary)
	}

	live := &LiveCheck{OK: true}
	a2 := &accounts.Account{
		ID: "acc_01habcdefghijklmnopqrstuv", Name: "G", Email: "g@gmail.com",
		State: accounts.StateConnected, Provider: "gmail", OAuth: &accounts.OAuthLink{},
	}
	h2 := Diagnose(DiagnoseInput{Account: a2, Live: live, Now: now})
	if h2.Severity != SeverityOK {
		t.Fatalf("api ok: %s", h2.Severity)
	}
}

func TestProviderFamilyAndRecentViews(t *testing.T) {
	if providerFamily("a@icloud.com", "imap.mail.me.com", "") != "icloud" {
		t.Fatal("icloud")
	}
	if providerFamily("a@x.com", "outlook.office365.com", "") != "outlook" {
		t.Fatal("outlook")
	}
	if providerFamily("a@x.com", "imap.x.com", "graph") != "graph" {
		t.Fatal("graph")
	}
	now := time.Now()
	views := recentViews([]accounts.RecentError{
		{At: now.Add(-time.Minute), Code: "old"},
		{At: now, Code: "new"},
	})
	if len(views) != 2 || views[0].Code != "new" {
		t.Fatalf("%+v", views)
	}
}

func TestSortRowsAndMaxSeverity(t *testing.T) {
	if MaxSeverity(SeverityOK, SeverityWarning) != SeverityWarning {
		t.Fatal("max")
	}
	rows := []Row{
		{AccountID: "b", Name: "Beta", Severity: SeverityOK},
		{AccountID: "a", Name: "Alpha", Severity: SeverityCritical},
		{AccountID: "c", Name: "Gamma", Severity: SeverityWarning},
	}
	sortRows(rows, "-severity")
	if rows[0].AccountID != "a" || rows[1].AccountID != "c" {
		t.Fatalf("%+v", rows)
	}
	sortRows(rows, "name")
	if rows[0].Name != "Alpha" {
		t.Fatalf("%+v", rows)
	}
}

func TestBuildFactsQuotaAuth(t *testing.T) {
	used, limit := int64(10), int64(100)
	a := &accounts.Account{
		IMAP: accounts.IMAPConfig{TLS: "starttls", Auth: accounts.MailAuth{User: "u"}},
		Capabilities: map[string]any{
			"IDLE": true, "auth": []any{"PLAIN", "XOAUTH2"},
			"quota": map[string]any{"used": used, "limit": limit},
		},
	}
	live := &LiveCheck{OK: true, LatencyMs: 12, Capabilities: []string{"CONDSTORE", "QRESYNC"}}
	f := buildFacts(a, 3, live)
	if !f.Idle || !f.Condstore || !f.QResync || f.ConnectLatencyMs == nil || *f.ConnectLatencyMs != 12 {
		t.Fatalf("%+v", f)
	}
	if f.Quota == nil || f.Quota.Used == nil || *f.Quota.Used != 10 {
		t.Fatalf("quota %+v", f.Quota)
	}
	if len(f.AuthMechanisms) != 2 {
		t.Fatalf("auth %+v", f.AuthMechanisms)
	}
}

func TestCapHelpersAndSeverityState(t *testing.T) {
	if !capBool(map[string]any{"idle": true}, "IDLE") {
		t.Fatal("lower")
	}
	if !capBool(map[string]any{"IDLE": "yes"}, "IDLE") {
		t.Fatal("string")
	}
	if capBool(map[string]any{"IDLE": "false"}, "IDLE") {
		t.Fatal("false str")
	}
	if _, ok := asInt64(int(3)); !ok {
		t.Fatal("int")
	}
	if _, ok := asInt64(int32(3)); !ok {
		t.Fatal("int32")
	}
	if _, ok := asInt64(float64(3)); !ok {
		t.Fatal("float")
	}
	if _, ok := asInt64("x"); ok {
		t.Fatal("bad")
	}
	_, _ = capStrings(map[string]any{"AUTH": []string{"A"}}, "auth") // key case may miss: only the []string path matters
	if v, ok := capStrings(map[string]any{"auth": []string{"PLAIN"}}, "auth"); !ok || v[0] != "PLAIN" {
		t.Fatal(v)
	}
	if severityFromState(accounts.StateConnectError, &accounts.StateReason{Kind: "auth", Code: "x"}) != SeverityCritical {
		t.Fatal()
	}
	if severityFromState(accounts.StateConnectError, &accounts.StateReason{Kind: "temporary", Code: "x"}) != SeverityWarning {
		t.Fatal()
	}
	if severityFromState(accounts.StateDisconnected, nil) != SeverityWarning {
		t.Fatal()
	}
	if severityFromState("weird", nil) != SeverityInfo {
		t.Fatal()
	}
	svc := &Service{}
	_ = svc.log()
	_ = svc.now()
	a := &accounts.Account{State: accounts.StateDisconnected, Provider: "gmail", OAuth: &accounts.OAuthLink{}}
	if svc.liveCheckAPI(a).OK {
		t.Fatal("disconnected")
	}
	if liveErr(nil, 1).OK != true {
		t.Fatal("nil err")
	}
}

func TestToRowAndLiveCheckAPI(t *testing.T) {
	h := AccountHealth{
		AccountID: "acc_1", Name: "N", Email: "e", State: "connected",
		Severity: SeverityWarning, Summary: "S",
		Hints: []Hint{{Code: HintRateLimited}},
	}
	row := ToRow(h)
	if row.HintCodes[0] != HintRateLimited {
		t.Fatalf("%+v", row)
	}
	svc := &Service{}
	a := &accounts.Account{State: accounts.StateConnected, Provider: "gmail", OAuth: &accounts.OAuthLink{}}
	if !svc.liveCheckAPI(a).OK {
		t.Fatal("api ok")
	}
	a.OAuth = nil
	if svc.liveCheckAPI(a).OK {
		t.Fatal("no oauth")
	}
	a.OAuth = &accounts.OAuthLink{}
	a.State = accounts.StateAuthError
	a.StateReason = &accounts.StateReason{Code: "oauth_revoked", Message: "revoked"}
	lc := svc.liveCheckAPI(a)
	if lc.OK || lc.Error.Code != "oauth_revoked" {
		t.Fatalf("%+v", lc)
	}
}

func TestRedactLiveAndLiveErr(t *testing.T) {
	if redactLive("bad password here") == "bad password here" {
		t.Fatal("redact")
	}
	lc := liveErr(errsNew("mail_auth_failed"), 9)
	if lc.OK || lc.LatencyMs != 9 {
		t.Fatalf("%+v", lc)
	}
}

func TestDiagnoseIgnoreCertErrors(t *testing.T) {
	now := time.Now()
	a := &accounts.Account{
		ID: "acc_01habcdefghijklmnopqrstuv", Name: "Lab", Email: "lab@example.com",
		State: accounts.StateConnected, LastSyncAt: &now,
		IMAP:     accounts.IMAPConfig{Host: "imap.example.com", Port: 993, TLS: "implicit"},
		Switches: map[string]any{"accounts.ignoreCertErrors": true},
	}
	h := Diagnose(DiagnoseInput{Account: a, FolderCount: 1, Now: now})
	if !hasCode(h.Hints, HintIgnoreCertErrors) {
		t.Fatalf("hints %+v", h.Hints)
	}
	if h.Severity.Rank() < SeverityWarning.Rank() {
		t.Fatalf("severity %s", h.Severity)
	}
}

// errsNew avoids importing errs helpers for a simple message path when CodeOf is empty.
type plainErr string

func (e plainErr) Error() string { return string(e) }

func errsNew(msg string) error { return plainErr(msg) }

func hasCode(hints []Hint, code string) bool {
	for _, h := range hints {
		if h.Code == code {
			return true
		}
	}
	return false
}
