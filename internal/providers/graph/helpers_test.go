package graph

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/events/types"
)

func TestTruncateAndMapStatus(t *testing.T) {
	if truncate("ab", 10) != "ab" {
		t.Fatal("short")
	}
	if got := truncate("abcdefghij", 5); got != "abcde…" {
		t.Fatal(got)
	}
	err := mapStatus(410, []byte(`{"error":{"code":"syncStateNotFound"}}`))
	if !errors.Is(err, ErrSyncStateNotFound) {
		t.Fatal(err)
	}
	err = mapStatus(401, []byte("x"))
	if err == nil {
		t.Fatal("auth")
	}
	err = mapStatus(404, []byte("x"))
	if err == nil {
		t.Fatal("404")
	}
	err = mapStatus(500, []byte(string(make([]byte, 300))))
	if err == nil || truncate(err.Error(), 10) == "" {
		t.Fatal(err)
	}
}

func TestFlagDiffSameFlags(t *testing.T) {
	d := flagDiff([]string{`\Seen`}, []string{`\Seen`, `\Flagged`})
	if len(d.Added) != 1 || d.Added[0] != `\Flagged` {
		t.Fatalf("%+v", d)
	}
	d = flagDiff([]string{`\Seen`, `\Flagged`}, []string{`\Seen`})
	if len(d.Removed) != 1 {
		t.Fatalf("%+v", d)
	}
	if !sameFlags([]string{`\Seen`}, []string{`\Seen`}) {
		t.Fatal("same")
	}
	if sameFlags([]string{`\Seen`}, []string{`\Flagged`}) {
		t.Fatal("diff")
	}
	if sameFlags([]string{`\Seen`}, []string{`\Seen`, `\Flagged`}) {
		t.Fatal("len")
	}
	_ = types.FlagChange{}
}

func TestWatchedSetAndClientDefaults(t *testing.T) {
	w := watchedSet(nil)
	if !w["*"] {
		t.Fatal("default *")
	}
	w = watchedSet([]string{"INBOX", "*"})
	if !isWatched("INBOX", w) || !isWatched("X", w) {
		t.Fatal(w)
	}
	c := &Client{}
	if c.http() == nil {
		t.Fatal("http")
	}
	if c.apiRoot() != defaultAPIRoot {
		t.Fatal(c.apiRoot())
	}
	c.BaseURL = "http://x/"
	if c.apiRoot() != "http://x/v1.0" {
		t.Fatal(c.apiRoot())
	}
}

func TestEmitNilSinkAndEnsureSubNoURL(t *testing.T) {
	r := &Runner{State: &SyncState{}, Now: func() time.Time { return time.Unix(1, 0).UTC() }}
	if err := r.emit(context.TODO(), Account{}, "t", "s", nil); err != nil {
		t.Fatal(err)
	}
	if err := r.EnsureSubscription(context.TODO(), Account{}, time.Hour); err != nil {
		t.Fatal(err)
	}
}

func TestParseMessageCc(t *testing.T) {
	var v graphMsgJSON
	v.ID = "1"
	v.ConversationID = "c"
	v.IsRead = true
	v.IsDraft = true
	v.Flag = &struct {
		FlagStatus string `json:"flagStatus"`
	}{FlagStatus: "flagged"}
	v.Received = time.Now().UTC().Format(time.RFC3339)
	v.Removed = &struct {
		Reason string `json:"reason"`
	}{Reason: "deleted"}
	m := parseMessage(v)
	if !m.Removed || !m.Flagged || !m.IsRead {
		t.Fatalf("%+v", m)
	}
}
