//go:build integration

package submit

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/index"
	"github.com/radiobros/emailbridge/internal/objstore"
	"github.com/radiobros/emailbridge/internal/store"
)

func TestIntegrationMailMergeIdempotent(t *testing.T) {
	h := newHarness(t)
	h.svc.Templates = &stubTemplates{subject: "Hello", text: "Hi there"}

	req := SubmitRequest{
		Template: &TemplateRef{ID: "tpl_01jb0000000000000000000001", Variables: map[string]any{"name": "base"}},
		Recipients: []MergeRecipient{
			{To: []AddressInput{{Address: "a@example.test"}}, Variables: map[string]any{"name": "Ada"}},
			{To: []AddressInput{{Address: "b@example.test"}}},
		},
	}
	res, err := h.svc.Submit(h.ctx, h.acc.ID, req, "merge-key-1", false)
	if err != nil {
		t.Fatal(err)
	}
	if res.MergeAccepted == nil || len(res.MergeAccepted.Messages) != 2 {
		t.Fatalf("%+v", res)
	}
	first := res.MergeAccepted.Messages[0].ID

	replay, err := h.svc.Submit(h.ctx, h.acc.ID, req, "merge-key-1", false)
	if err != nil {
		t.Fatal(err)
	}
	if !replay.Replayed || replay.MergeAccepted == nil || len(replay.MergeAccepted.Messages) != 2 {
		t.Fatalf("replay: %+v", replay)
	}
	if replay.MergeAccepted.Messages[0].ID != first {
		t.Fatalf("replay ids differ: %s vs %s", replay.MergeAccepted.Messages[0].ID, first)
	}

	for _, err := range h.work() {
		if err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if h.delivered() >= 2 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("simulator delivered %d, want ≥2", h.delivered())
}

func TestIntegrationReplyForwardSMTP(t *testing.T) {
	h := newHarness(t)
	raw := sampleRFC822()
	msgID := store.NewID(store.PrefixMessage)
	archiveKey := "archive/" + msgID
	if err := h.objects.Put(h.ctx, archiveKey, bytes.NewReader(raw), int64(len(raw)),
		objstore.ObjectMeta{Organization: h.org, Account: h.acc.ID, ContentType: "message/rfc822"}); err != nil {
		t.Fatal(err)
	}
	_, err := h.db.Collection(index.Messages).InsertOne(h.ctx, bson.M{
		"_id": msgID, "organizationId": h.org, "accountId": h.acc.ID, "archiveRef": archiveKey,
	})
	if err != nil {
		t.Fatal(err)
	}
	h.svc.Messages = &IndexArchiveSource{DB: h.db, Objects: h.objects}

	res, err := h.svc.Submit(h.ctx, h.acc.ID, SubmitRequest{
		Text:      "Got it",
		Reference: &Reference{MessageID: msgID, Action: "reply"},
	}, "", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, err := range h.work() {
		if err != nil {
			t.Fatal(err)
		}
	}
	m := h.get(res.Accepted.ID)
	if m.State != StateSent {
		t.Fatalf("reply state: %+v", m)
	}
	rc, _, err := h.objects.Get(h.ctx, m.RawRef)
	if err != nil {
		t.Fatal(err)
	}
	buf := &bytes.Buffer{}
	_, _ = buf.ReadFrom(rc)
	_ = rc.Close()
	body := buf.String()
	if !strings.Contains(body, "In-Reply-To: <orig@example.test>") ||
		!strings.Contains(body, "References:") ||
		!strings.Contains(strings.ToLower(body), "subject: re: hello there") {
		t.Fatalf("reply raw:\n%s", body)
	}

	inline := true
	fwd, err := h.svc.Submit(h.ctx, h.acc.ID, SubmitRequest{
		To:        []AddressInput{{Address: testTo}},
		Text:      "FYI",
		Reference: &Reference{MessageID: msgID, Action: "forward", Inline: &inline},
	}, "", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, err := range h.work() {
		if err != nil {
			t.Fatal(err)
		}
	}
	fm := h.get(fwd.Accepted.ID)
	fr, _, err := h.objects.Get(h.ctx, fm.RawRef)
	if err != nil {
		t.Fatal(err)
	}
	fbuf := &bytes.Buffer{}
	_, _ = fbuf.ReadFrom(fr)
	_ = fr.Close()
	if !strings.Contains(strings.ToLower(fbuf.String()), "subject: fwd: hello there") {
		t.Fatalf("forward raw:\n%s", fbuf.String())
	}

	_, err = h.svc.Submit(h.ctx, h.acc.ID, SubmitRequest{
		Reference: &Reference{MessageID: store.NewID(store.PrefixMessage), Action: "reply"},
	}, "", false)
	if errs.CodeOf(err) != errs.CodeMessageNotFound {
		t.Fatalf("missing archive: %v", err)
	}
}
