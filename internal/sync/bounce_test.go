package sync

import (
	"context"
	"errors"
	"testing"

	"github.com/radiobros/emailbridge/internal/mailproto/imapc"
	"github.com/radiobros/emailbridge/internal/mailproto/mime"
)

func TestLikelyMachineGenerated(t *testing.T) {
	from := func(addr string) *imapc.Envelope {
		return &imapc.Envelope{From: []imapc.Address{{Address: addr}}}
	}
	for _, tc := range []struct {
		name string
		hdr  mime.Header
		env  *imapc.Envelope
		want bool
	}{
		{"null reverse path", mime.Header{"return-path": {"<>"}}, from("x@example.test"), true},
		{"multipart/report", mime.Header{"content-type": {"multipart/report; report-type=delivery-status; boundary=x"}}, nil, true},
		{"auto-submitted", mime.Header{"auto-submitted": {"auto-replied"}}, nil, true},
		{"auto-submitted no", mime.Header{"auto-submitted": {"no"}}, from("a@example.test"), false},
		{"x-autoreply", mime.Header{"x-autoreply": {"yes"}}, nil, true},
		{"failed recipients", mime.Header{"x-failed-recipients": {"b@example.test"}}, nil, true},
		{"precedence auto_reply", mime.Header{"precedence": {"auto_reply"}}, nil, true},
		{"precedence bulk is not enough", mime.Header{"precedence": {"bulk"}}, from("news@example.test"), false},
		{"mailer-daemon", mime.Header{}, from("MAILER-DAEMON@mx.example.test"), true},
		{"postmaster", mime.Header{}, from("postmaster@example.test"), true},
		{"ordinary mail", mime.Header{"return-path": {"<a@example.test>"}, "content-type": {"text/plain"}}, from("a@example.test"), false},
	} {
		if got := likelyMachineGenerated(tc.hdr, tc.env); got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestTruncateKeepsRunes(t *testing.T) {
	if got := truncate("abc", 5); got != "abc" {
		t.Fatalf("short: %q", got)
	}
	if got := truncate("àèì", 3); got != "à" { // 2-byte runes: cut back to a rune boundary
		t.Fatalf("runes: %q", got)
	}
}

func TestAuthOptionsPasswordOrToken(t *testing.T) {
	acc := &Account{IMAPUser: "u", IMAPPass: "p"}
	var o imapc.Options
	if err := AuthOptions(context.Background(), acc, &o); err != nil || o.Username != "u" || o.Password != "p" || o.AccessToken != "" {
		t.Fatalf("password account: %+v %v", o, err)
	}
	acc.Token = func(context.Context) (string, string, error) { return "tok", "login@example.test", nil }
	o = imapc.Options{}
	if err := AuthOptions(context.Background(), acc, &o); err != nil || o.AccessToken != "tok" || o.Password != "" || o.Username != "login@example.test" {
		t.Fatalf("oauth account: %+v %v", o, err)
	}
	acc.Token = func(context.Context) (string, string, error) { return "", "", errors.New("revoked") }
	if err := AuthOptions(context.Background(), acc, &imapc.Options{}); err == nil {
		t.Fatal("a token error must stop the connection")
	}
}
