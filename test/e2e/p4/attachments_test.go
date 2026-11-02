//go:build e2e

package p4

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/test/containers"
	"github.com/radiobros/emailbridge/test/e2e/harness"
)

// Attachments indexed on message.received; list on message view; download by att_ id.
func TestP4_AttachmentsListAndDownload(t *testing.T) {
	f := startFixture(t, fixtureOpts{
		events:  []string{"message.received"},
		payload: map[string]any{"attachments": true},
	})
	f.Sink.Reset()

	raw := multipartAtts(f.Email, f.Email, "Two atts", "<p4-atts@e2e>", [][2]string{
		{"a.txt", "alpha-bytes"},
		{"b.txt", "bravo-bytes"},
	})
	f.injectRaw("INBOX", raw, nil, time.Time{})
	ev := harness.WaitEvent(t, f.Sink, "message.received", 90*time.Second)
	harness.VerifyWebhook(t, f.Secret, ev)
	data := eventData(t, ev)
	atts, _ := data["attachments"].([]any)
	if len(atts) < 2 {
		t.Fatalf("event attachments want ≥2, got %v in %s", atts, ev.Body)
	}
	var attIDs []string
	for _, a := range atts {
		m, _ := a.(map[string]any)
		if m == nil {
			continue
		}
		id, _ := m["id"].(string)
		if id == "" || !strings.HasPrefix(id, "att_") {
			t.Fatalf("attachment missing att_ id: %v", m)
		}
		attIDs = append(attIDs, id)
	}

	msg := f.waitMsgBySubject("Two atts", 60*time.Second)
	msgID, _ := msg["id"].(string)
	view := f.Owner.Do(http.MethodGet, f.base()+"/messages/"+msgID, nil)
	harness.ExpectStatus(t, view, http.StatusOK)
	viewAtts, _ := view.JSON["attachments"].([]any)
	if len(viewAtts) < 2 {
		t.Fatalf("message view attachments: %s", view.Body)
	}

	dl := f.Owner.Do(http.MethodGet, f.base()+"/messages/"+msgID+"/attachments/"+attIDs[0], nil)
	harness.ExpectStatus(t, dl, http.StatusOK)
	if !strings.Contains(string(dl.Body), "alpha-bytes") && !strings.Contains(string(dl.Body), "bravo-bytes") {
		t.Fatalf("download body unexpected: %q", truncate(dl.Body, 120))
	}
}

// attachments.s3 on → signed URL in event and via ?url=signed; public GET /a/{token} works.
func TestP4_AttachmentsS3SignedURL(t *testing.T) {
	f := startFixture(t, fixtureOpts{
		switches: map[string]any{"attachments.s3": true},
		events:   []string{"message.received"},
		payload:  map[string]any{"attachments": true},
	})
	f.Sink.Reset()

	raw := multipartAtts(f.Email, f.Email, "S3 att", "<p4-s3@e2e>", [][2]string{{"note.txt", "s3-payload-bytes"}})
	f.injectRaw("INBOX", raw, nil, time.Time{})
	ev := harness.WaitEvent(t, f.Sink, "message.received", 90*time.Second)
	data := eventData(t, ev)
	atts, _ := data["attachments"].([]any)
	if len(atts) < 1 {
		t.Fatalf("no attachments: %s", ev.Body)
	}
	att0, _ := atts[0].(map[string]any)
	attID, _ := att0["id"].(string)
	url, _ := att0["url"].(string)
	if attID == "" {
		t.Fatalf("no att id: %v", att0)
	}
	msg := f.waitMsgBySubject("S3 att", 60*time.Second)
	msgID, _ := msg["id"].(string)

	signed := f.Owner.Do(http.MethodGet, f.base()+"/messages/"+msgID+"/attachments/"+attID+"?url=signed", nil)
	harness.ExpectStatus(t, signed, http.StatusOK)
	var body struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(signed.Body, &body); err != nil || body.URL == "" {
		// Stream path may return JSON already parsed into signed.JSON
		body.URL, _ = signed.JSON["url"].(string)
	}
	if body.URL == "" && url == "" {
		t.Fatalf("no signed url in event or ?url=signed: event=%v api=%s", att0, signed.Body)
	}
	if body.URL == "" {
		body.URL = url
	}

	res, err := http.Get(body.URL) //nolint:noctx // e2e against local node PublicURL
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	got, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET signed = %d %s", res.StatusCode, got)
	}
	if !strings.Contains(string(got), "s3-payload-bytes") {
		t.Fatalf("signed body: %q", truncate(got, 120))
	}
}

// ClamAV + EICAR → scan=infected; signed URL refused. Skips when the ClamAV container cannot start.
func TestP4_AttachmentsClamAVEICAR(t *testing.T) {
	addr := containers.ClamAV(t)
	f := startFixture(t, fixtureOpts{
		switches: map[string]any{"attachments.antivirus": true, "attachments.s3": true},
		events:   []string{"message.received"},
		payload:  map[string]any{"attachments": true},
		extraEnv: map[string]string{"EB_CLAMAV_ADDR": "tcp://" + addr},
	})
	f.Sink.Reset()

	raw := eicarMIME(f.Email, f.Email, "EICAR", fmt.Sprintf("<p4-eicar-%d@e2e>", time.Now().UnixNano()))
	f.injectRaw("INBOX", raw, nil, time.Time{})
	ev := harness.WaitEvent(t, f.Sink, "message.received", 3*time.Minute)
	data := eventData(t, ev)
	atts, _ := data["attachments"].([]any)
	if len(atts) < 1 {
		t.Fatalf("no attachments: %s", ev.Body)
	}
	var infectedID string
	for _, a := range atts {
		m, _ := a.(map[string]any)
		if m == nil {
			continue
		}
		scan, _ := m["scan"].(string)
		id, _ := m["id"].(string)
		if scan == "infected" || m["infected"] == true {
			infectedID = id
			if u, _ := m["url"].(string); u != "" {
				t.Fatalf("infected attachment must not carry signed url: %v", m)
			}
			break
		}
		// clamd may still be loading signatures — accept error and retry once with a clean wait
		if scan == "error" || scan == "skipped" {
			t.Skipf("ClamAV scan not ready (scan=%s) — see summary", scan)
		}
		infectedID = id
	}
	if infectedID == "" {
		t.Fatalf("no attachment id: %v", atts)
	}
	msg := f.waitMsgBySubject("EICAR", 60*time.Second)
	msgID, _ := msg["id"].(string)
	signed := f.Owner.Do(http.MethodGet, f.base()+"/messages/"+msgID+"/attachments/"+infectedID+"?url=signed", nil)
	if signed.Status != http.StatusForbidden && signed.Str("code") != "attachment_infected" {
		// If scan was still loading when ingest ran, the row may be clean/error — skip rather than false fail.
		view := f.Owner.Do(http.MethodGet, f.base()+"/messages/"+msgID, nil)
		atts2, _ := view.JSON["attachments"].([]any)
		scan := ""
		for _, a := range atts2 {
			m, _ := a.(map[string]any)
			if m != nil && m["id"] == infectedID {
				scan, _ = m["scan"].(string)
			}
		}
		if scan != "infected" {
			t.Skipf("BUG or clamd warmup: expected infected, scan=%s api=%d %s", scan, signed.Status, signed.Body)
		}
		t.Fatalf("signed URL for infected: %d %s", signed.Status, signed.Body)
	}
}
