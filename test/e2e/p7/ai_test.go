//go:build e2e

package p7

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/test/e2e/harness"
	"github.com/radiobros/emailbridge/test/sink"
)

func patchAccountSwitches(t *testing.T, owner *harness.Client, accID string, switches map[string]any) {
	t.Helper()
	got := owner.Do(http.MethodGet, "/v1/accounts/"+accID, nil)
	harness.ExpectStatus(t, got, http.StatusOK)
	etag := got.Header.Get("ETag")
	prev := owner.Hdr
	hdr := map[string]string{}
	for k, v := range prev {
		hdr[k] = v
	}
	if etag != "" {
		hdr["If-Match"] = etag
	}
	owner.Hdr = hdr
	r := owner.Do(http.MethodPatch, "/v1/accounts/"+accID, map[string]any{"switches": switches})
	owner.Hdr = prev
	if r.Status != http.StatusOK {
		t.Fatalf("patch switches: %d %s", r.Status, r.Body)
	}
}

func TestP7_AI_OpsEnrichmentAndAccountSwitch(t *testing.T) {
	mock := startOpenAIMock(t)
	f := startFixture(t, map[string]any{"ai.enabled": true}, []string{"message.received"}, sink.Options{})

	enableOrgSwitch(t, f.Owner, "ai.enabled", true)
	put := f.Owner.Do(http.MethodPut, "/v1/ai/settings", map[string]any{
		"provider":  "local",
		"baseUrl":   mock.URL + "/v1",
		"model":     "e2e-mock",
		"apiKey":    "sk-e2e-test",
		"features":  []string{"summary", "classification", "extraction", "risk"},
		"piiRedact": false,
	})
	if put.Status != http.StatusOK {
		t.Fatalf("ai settings: %d %s", put.Status, put.Body)
	}

	subj := fmt.Sprintf("ai-ops-%d", time.Now().UnixNano())
	f.inject(subj, "<"+subj+"@e2e>", "Please help with my support ticket about billing.")
	_ = harness.WaitEvent(t, f.Sink, "message.received", 60*time.Second)
	msgID := waitMessageID(t, f.Owner, f.AccID, subj, 30*time.Second)

	sum := f.Owner.Do(http.MethodPost, "/v1/accounts/"+f.AccID+"/messages/"+msgID+"/ai/summarize", map[string]any{})
	if sum.Status != http.StatusOK || sum.Str("summary") == "" {
		t.Fatalf("summarize: %d %s", sum.Status, sum.Body)
	}
	cls := f.Owner.Do(http.MethodPost, "/v1/accounts/"+f.AccID+"/messages/"+msgID+"/ai/classify", map[string]any{
		"labels": []string{"sales", "support", "spam"},
	})
	if cls.Status != http.StatusOK || cls.Str("label") == "" {
		t.Fatalf("classify: %d %s", cls.Status, cls.Body)
	}
	ext := f.Owner.Do(http.MethodPost, "/v1/accounts/"+f.AccID+"/messages/"+msgID+"/ai/extract", map[string]any{
		"schema": "object with ok bool",
	})
	if ext.Status != http.StatusOK || ext.Str("data") == "" {
		t.Fatalf("extract: %d %s", ext.Status, ext.Body)
	}
	risk := f.Owner.Do(http.MethodPost, "/v1/accounts/"+f.AccID+"/messages/"+msgID+"/ai/risk", map[string]any{})
	if risk.Status != http.StatusOK || risk.Str("risk") == "" {
		t.Fatalf("risk: %d %s", risk.Status, risk.Body)
	}
	if mock.Calls < 4 {
		t.Fatalf("mock provider calls=%d want ≥4", mock.Calls)
	}

	patchAccountSwitches(t, f.Owner, f.AccID, map[string]any{"ai.enabled": false, "sandbox.enabled": false})
	off := f.Owner.Do(http.MethodPost, "/v1/accounts/"+f.AccID+"/messages/"+msgID+"/ai/summarize", map[string]any{})
	if off.Status != http.StatusForbidden || problemCode(off) != "feature_disabled" {
		t.Fatalf("ai off: want 403 feature_disabled, got %d %s", off.Status, off.Body)
	}

	patchAccountSwitches(t, f.Owner, f.AccID, map[string]any{"ai.enabled": true, "sandbox.enabled": false})

	f.Sink.Reset()
	before := mock.Calls
	ep := f.Owner.Do(http.MethodPost, "/v1/webhook-endpoints", map[string]any{
		"name": "p7-ai-enrich", "kind": "http", "url": f.Sink.URL,
		"events": []string{"message.received"}, "secret": harness.WebhookSecret(),
		"retry":   defaultRetry(),
		"payload": map[string]any{"text": "plain", "aiEnrichment": true},
	})
	harness.ExpectStatus(t, ep, http.StatusCreated)

	subj2 := fmt.Sprintf("ai-enrich-%d", time.Now().UnixNano())
	f.inject(subj2, "<"+subj2+"@e2e>", "Urgent sales offer for enterprise.")
	_ = harness.WaitEvent(t, f.Sink, "message.received", 60*time.Second)
	var ai map[string]any
	var lastBody []byte
	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) && ai == nil {
		for _, req := range f.Sink.Requests() {
			if harness.EventType(req) != "message.received" {
				continue
			}
			var env map[string]any
			if json.Unmarshal(req.Body, &env) != nil {
				continue
			}
			data, _ := env["data"].(map[string]any)
			if data == nil {
				continue
			}
			lastBody = req.Body
			if a, ok := data["ai"].(map[string]any); ok && len(a) > 0 {
				ai = a
				break
			}
		}
		if ai == nil {
			time.Sleep(200 * time.Millisecond)
		}
	}
	if ai == nil {
		if mock.Calls <= before {
			t.Fatalf("aiEnrichment set but the AI enricher was never called")
		}
		t.Fatalf("want data.ai on an enriched delivery (enricher called %d× after mark), last=%s",
			mock.Calls-before, lastBody)
	}
	if ai["summary"] == nil && ai["category"] == nil && ai["risk"] == nil {
		t.Fatalf("data.ai empty: %#v body=%s", ai, lastBody)
	}

	mock.Fail = true
	f.Sink.Reset()
	subj3 := fmt.Sprintf("ai-failopen-%d", time.Now().UnixNano())
	f.inject(subj3, "<"+subj3+"@e2e>", "another message")
	req3 := harness.WaitEvent(t, f.Sink, "message.received", 60*time.Second)
	if len(req3.Body) == 0 {
		t.Fatal("fail-open: no delivery")
	}
}
