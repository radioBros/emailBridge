package notify

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/tenancy"
)

func testLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestFingerprintStable(t *testing.T) {
	err := errors.New("boom 123 detail")
	a := fingerprint(err, map[string]string{"component": "http", "code": "internal_error"})
	b := fingerprint(errors.New("boom 999 detail"), map[string]string{"component": "http", "code": "internal_error"})
	if a != b {
		t.Fatalf("digits should collapse: %s vs %s", a, b)
	}
	c := fingerprint(err, map[string]string{"component": "sync"})
	if a == c {
		t.Fatal("component should change fingerprint")
	}
}

func TestSanitizeLink(t *testing.T) {
	if sanitizeLink("https://evil.test/x") != "" {
		t.Fatal("external rejected")
	}
	if sanitizeLink("//evil") != "" {
		t.Fatal("protocol-relative rejected")
	}
	if got := sanitizeLink("/webhooks/whe_1"); got != "/webhooks" {
		t.Fatalf("webhook detail → list: %q", got)
	}
	if got := sanitizeLink("/organization/api-keys"); got != "/api-keys" {
		t.Fatalf("legacy api-keys: %q", got)
	}
	if got := sanitizeLink("/accounts/acc_1"); got != "/accounts/acc_1" {
		t.Fatalf("other relative kept: %q", got)
	}
}

func TestSeverityRank(t *testing.T) {
	if severityRank[SeverityCritical] <= severityRank[SeverityWarning] {
		t.Fatal("rank")
	}
}

func TestSplitSK(t *testing.T) {
	got := splitSK("org_1\x00webhook.failing:whe_1")
	if got[0] != "org_1" || got[1] != "webhook.failing:whe_1" {
		t.Fatalf("%q %q", got[0], got[1])
	}
}

func TestHumanize(t *testing.T) {
	if humanizeKey("notify.webhook.failing.title") == "" {
		t.Fatal()
	}
	if renderTitle(&Notification{TitleKey: "notify.x", TitleParams: map[string]string{"a": "1"}}) == "" {
		t.Fatal()
	}
}

func TestUserPrefsQuietHours(t *testing.T) {
	p := UserPrefs{QuietStart: "22:00", QuietEnd: "06:00", QuietTZ: "UTC"}
	if !p.InQuietHours(time.Date(2026, 10, 27, 23, 0, 0, 0, time.UTC)) {
		t.Fatal("expected quiet")
	}
	if p.InQuietHours(time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)) {
		t.Fatal("expected not quiet")
	}
}

func TestUserPrefsWantsEmail(t *testing.T) {
	p := UserPrefs{EmailEnabled: true, EmailSet: true, MinSeverity: SeverityWarning, Categories: map[string]bool{CatWebhooks: true}}
	n := &Notification{Severity: SeverityInfo, Category: CatWebhooks}
	if p.WantsEmail(n, tenancy.RoleOwner) {
		t.Fatal("info below min")
	}
	n.Severity = SeverityCritical
	if !p.WantsEmail(n, tenancy.RoleViewer) {
		t.Fatal("explicit opt-in wins over the viewer default")
	}
	p.Categories[CatWebhooks] = false
	if p.WantsEmail(n, tenancy.RoleOwner) {
		t.Fatal("category off")
	}
}

// TestEmailRoleDefaults: unset toggle -> on for owner/admin, off for operator/viewer; a saved value wins.
func TestEmailRoleDefaults(t *testing.T) {
	unset := UserPrefs{}
	for role, want := range map[tenancy.Role]bool{
		tenancy.RoleOwner: true, tenancy.RoleAdmin: true, tenancy.RoleOperator: false, tenancy.RoleViewer: false, "": false,
	} {
		if got := unset.EmailOnFor(role); got != want {
			t.Fatalf("unset %q = %v, want %v", role, got, want)
		}
		if !(UserPrefs{EmailSet: true, EmailEnabled: true}).EmailOnFor(role) {
			t.Fatalf("explicit on %q", role)
		}
		if (UserPrefs{EmailSet: true, EmailEnabled: false}).EmailOnFor(role) {
			t.Fatalf("explicit off %q", role)
		}
	}
	if (UserPrefs{}).WantsEmail(nil, tenancy.RoleOwner) {
		t.Fatal("nil notification")
	}
}

func TestCaptureErrorRaises(t *testing.T) {
	e := &Engine{ch: make(chan op, 8), recent: map[string]time.Time{}, Now: time.Now, Log: testLog()}
	e.CaptureError(context.Background(), errors.New("x"), map[string]string{"component": "http", "code": "internal_error"})
	select {
	case o := <-e.ch:
		if o.kind != opRaise || o.p.Category != CatInternal || o.p.Scope != ScopeInstance {
			t.Fatalf("%+v", o)
		}
	default:
		t.Fatal("expected enqueue")
	}
}

func TestRaiseDedupWindow(t *testing.T) {
	now := time.Now()
	e := &Engine{ch: make(chan op, 8), recent: map[string]time.Time{}, Now: func() time.Time { return now }, Log: testLog()}
	p := Problem{Scope: "org_1", Key: "k", Category: CatWebhooks, Severity: SeverityWarning, TitleKey: "t", DetailKey: "d"}
	e.Raise(context.Background(), p)
	e.Raise(context.Background(), p)
	if len(e.ch) != 1 {
		t.Fatalf("dedup want 1 got %d", len(e.ch))
	}
}

func TestUnique(t *testing.T) {
	if len(unique([]string{"a", "a", "", "b"})) != 2 {
		t.Fatal()
	}
}
