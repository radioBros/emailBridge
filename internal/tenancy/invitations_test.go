package tenancy

import (
	"strings"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/config"
)

func TestInviteTokenHashRoundTrip(t *testing.T) {
	tok, hash, err := newInviteToken()
	if err != nil || tok == "" || hash == "" {
		t.Fatal(tok, hash, err)
	}
	if !inviteTokenMatch(hash, tok) {
		t.Fatal("match")
	}
	if inviteTokenMatch(hash, tok+"x") {
		t.Fatal("mismatch")
	}
	if inviteTokenMatch("zz", tok) {
		t.Fatal("bad hash")
	}
	if hashInviteToken(tok) != hash {
		t.Fatal("hashInviteToken")
	}
}

func TestInviteLimiter(t *testing.T) {
	l := newInviteLimiter(2, time.Minute)
	if !l.allow("a") {
		t.Fatal("first")
	}
	if !l.allow("a") {
		t.Fatal("second")
	}
	if l.allow("a") {
		t.Fatal("third should be denied")
	}
	if !l.allow("b") {
		t.Fatal("other key")
	}
	l2 := newInviteLimiter(0, 0)
	if l2.n != defaultInviteRate || l2.window != time.Minute {
		t.Fatal("defaults")
	}
	l3 := newInviteLimiterFromRate(config.Rate{})
	if l3.n != defaultInviteRate {
		t.Fatal("rate defaults")
	}
}

func TestEmailsEqualAndInviteURL(t *testing.T) {
	if !emailsEqual("A@B.C", "a@b.c") || emailsEqual("a@b.c", "x@y.z") {
		t.Fatal("emailsEqual")
	}
	u := inviteURL("https://eb/", "sekrettok")
	if u != "https://eb/accept-invite#sekrettok" {
		t.Fatal(u)
	}
	if !strings.Contains(u, "#") || strings.Contains(u, "/sekrettok") {
		t.Fatal("token must be in fragment only", u)
	}
	if inviteURL("", "tok") != "/accept-invite#tok" {
		t.Fatal(inviteURL("", "tok"))
	}
}

func TestHelpersETagSortCursor(t *testing.T) {
	if formatETag(3) != `"v3"` || parseETag(`"v3"`) != 3 {
		t.Fatal("etag")
	}
	if checkIfMatch(`"v2"`, 3) == nil || checkIfMatch("", 3) != nil {
		t.Fatal("if-match")
	}
	f, d, err := parseListSort("-name", orgSortFields)
	if err != nil || f != "name" || d != -1 {
		t.Fatal(f, d, err)
	}
	if _, _, err := parseListSort("nope", orgSortFields); err == nil {
		t.Fatal("sort")
	}
	if clampLimit(0) != defaultLimit || clampLimit(9999) != maxLimit {
		t.Fatal("limit")
	}
	enc := encodeCursor(cursorPayload{ID: "x", Field: "_id", Desc: true})
	p, err := decodeCursor(enc)
	if err != nil || p.ID != "x" {
		t.Fatal(p, err)
	}
	if _, err := decodeCursor("%%%"); err == nil {
		t.Fatal("bad cursor")
	}
	if !mayTouchOwners(Caller{Kind: CallerUser, Role: RoleOwner}) || mayTouchOwners(Caller{Kind: CallerUser, Role: RoleAdmin}) {
		t.Fatal("mayTouchOwners")
	}
	if !mayTouchOwners(Caller{Kind: CallerAPIKey, Scopes: []string{"admin"}}) {
		t.Fatal("key admin")
	}
}

func TestInviteRoutesAreBodyOnly(t *testing.T) {
	// Route shapes: token never in path (access / proxy / Referer safe).
	paths := []string{"/v1/invitations/lookup", "/v1/invitations/accept"}
	for _, p := range paths {
		if strings.Contains(p, "{token}") || strings.Contains(p, ":token") {
			t.Fatal(p)
		}
	}
}
