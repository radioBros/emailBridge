//go:build e2e

package p5

import (
	"net/http"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/test/e2e/harness"
)

// Send window closed → outbound scheduled; send-now releases it.
func TestP5_SendWindowAndSendNow(t *testing.T) {
	f := startFixture(t, fixtureOpts{events: []string{"message.sent"}})
	enableOrgSwitch(t, f.Owner, "sending.sendWindows", true)
	// Only Monday (ISO 1) — always closed on any other weekday, deterministic.
	patchOrgSetting(t, f.Owner, "sending.sendWindows.config", map[string]any{
		"timeZone": "UTC",
		"days":     []int{1},
		"start":    "00:00",
		"end":      "23:59",
	})
	// If somehow today is Monday UTC, flip to Sunday-only.
	if time.Now().UTC().Weekday() == time.Monday {
		patchOrgSetting(t, f.Owner, "sending.sendWindows.config", map[string]any{
			"timeZone": "UTC", "days": []int{7}, "start": "00:00", "end": "23:59",
		})
	}

	r := f.submit(map[string]any{
		"to": []map[string]any{{"address": f.Email}}, "subject": "window-hold", "text": "x",
	}, nil)
	if r.Status != http.StatusAccepted {
		t.Fatalf("submit = %d %s", r.Status, r.Body)
	}
	id := r.Str("id")
	out := f.Owner.WaitJSON(t, "/v1/outbound-messages/"+id, 60*time.Second, "scheduled", func(r harness.Resp) bool {
		return r.Status == http.StatusOK && r.Str("state") == "scheduled"
	})
	if out.Str("state") != "scheduled" {
		t.Fatalf("want scheduled, got %s %s", out.Str("state"), out.Body)
	}

	// Worker re-checks the window on every claim — turn it off before send-now.
	enableOrgSwitch(t, f.Owner, "sending.sendWindows", false)
	sn := f.Owner.Do(http.MethodPost, "/v1/outbound-messages/"+id+"/send-now", nil)
	if sn.Status != http.StatusOK && sn.Status != http.StatusAccepted {
		t.Fatalf("send-now = %d %s", sn.Status, sn.Body)
	}
	if sn.Str("state") != "queued" && sn.Str("state") != "sending" && sn.Str("state") != "sent" {
		// API accepted; state may already be queued
		_ = sn
	}
	sent := waitSentOrStuck(t, f, id, 45*time.Second)
	if sent.Str("state") != "sent" {
		t.Fatal("BUG: send-now after send-window schedule leaves outbound queued — JetStream Msg-Id " +
			"outboundId/0 is deduplicated (first claim already consumed it); see submit.SendNow + publish — " +
			"state=" + sent.Str("state"))
	}
	_ = harness.WaitEvent(t, f.Sink, "message.sent", 45*time.Second)
}

// Per-domain throttle delays the second message; account switch off bypasses it.
func TestP5_ThrottleDomainAndAccountOff(t *testing.T) {
	f := startFixture(t, fixtureOpts{})
	enableOrgSwitch(t, f.Owner, "sending.throttle", true)
	// Nested dotted domain keys are rejected by settings validation — use domainPerMinute.
	patchOrgSetting(t, f.Owner, "sending.throttle.limits", map[string]any{
		"domainPerMinute": 1,
		"retryAfter":      "2m",
	})

	r1 := f.submit(map[string]any{
		"to": []map[string]any{{"address": f.Email}}, "subject": "thr-1", "text": "a",
	}, nil)
	if r1.Status != http.StatusAccepted {
		t.Fatalf("submit1 = %d %s", r1.Status, r1.Body)
	}
	id1 := r1.Str("id")
	if f.waitSent(id1, 90*time.Second).Str("state") != "sent" {
		t.Fatalf("first send not sent: %s", id1)
	}

	r2 := f.submit(map[string]any{
		"to": []map[string]any{{"address": f.Email}}, "subject": "thr-2", "text": "b",
	}, nil)
	if r2.Status != http.StatusAccepted {
		t.Fatalf("submit2 = %d %s", r2.Status, r2.Body)
	}
	id2 := r2.Str("id")
	var delayed harness.Resp
	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		delayed = f.Owner.Do(http.MethodGet, "/v1/outbound-messages/"+id2, nil)
		if delayed.Status != http.StatusOK {
			time.Sleep(200 * time.Millisecond)
			continue
		}
		st := delayed.Str("state")
		if st == "sent" || st == "captured" {
			t.Fatalf("second send was not throttled: %s", delayed.Body)
		}
		if st == "queued" || st == "scheduled" {
			if na, _ := delayed.JSON["nextAttemptAt"].(string); na != "" {
				break
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	if delayed.Str("state") == "sent" || delayed.Str("state") == "" {
		t.Fatalf("second send was not throttled: %s", delayed.Body)
	}

	// Account with throttle switch off should send immediately despite org limit.
	f2 := startFixture(t, fixtureOpts{
		switches: map[string]any{"sending.throttle": false},
	})
	enableOrgSwitch(t, f2.Owner, "sending.throttle", true)
	patchOrgSetting(t, f2.Owner, "sending.throttle.limits", map[string]any{
		"domainPerMinute": 1,
		"retryAfter":      "2m",
	})
	// Burn the one token.
	burn := f2.submit(map[string]any{
		"to": []map[string]any{{"address": f2.Email}}, "subject": "thr-burn", "text": "x",
	}, nil)
	harness.ExpectStatus(t, burn, http.StatusAccepted)
	_ = f2.waitSent(burn.Str("id"), 90*time.Second)

	bypass := f2.submit(map[string]any{
		"to": []map[string]any{{"address": f2.Email}}, "subject": "thr-bypass", "text": "y",
	}, nil)
	harness.ExpectStatus(t, bypass, http.StatusAccepted)
	got := f2.waitSent(bypass.Str("id"), 90*time.Second)
	if got.Str("state") != "sent" {
		t.Fatalf("account throttle off still delayed: %s %s", got.Str("state"), got.Body)
	}
}

// Manual suppression blocks submit; list-scoped unsubscribe via listId.
func TestP5_SuppressionAndListScopedUnsub(t *testing.T) {
	f := startFixture(t, fixtureOpts{
		events: []string{"message.sent", "list.unsubscribed", "tracking.opened"},
	})
	enableOrgSwitch(t, f.Owner, "sending.suppression", true)
	enableOrgSwitch(t, f.Owner, "sending.listUnsubscribe", true)
	enableOrgSwitch(t, f.Owner, "tracking.opens", true)
	enableOrgSwitch(t, f.Owner, "tracking.clicks", true)

	// Manual list blocks immediately.
	lst := f.Owner.Do(http.MethodPost, "/v1/suppression-lists", map[string]any{
		"name": "manual-p5", "kind": "manual",
	})
	harness.ExpectStatus(t, lst, http.StatusCreated)
	listID := lst.Str("id")
	ent := f.Owner.Do(http.MethodPost, "/v1/suppression-lists/"+listID+"/entries", map[string]any{
		"address": f.Email, "reason": "e2e block",
	})
	if ent.Status != http.StatusCreated && ent.Status != http.StatusOK {
		t.Fatalf("add entry = %d %s", ent.Status, ent.Body)
	}
	blocked := f.submit(map[string]any{
		"to": []map[string]any{{"address": f.Email}}, "subject": "blocked", "text": "no",
	}, nil)
	harness.ExpectProblem(t, blocked, http.StatusUnprocessableEntity, "recipient_suppressed")
	_ = ent

	// Remove the whole manual list so later list-scoped scenarios can send.
	getL := f.Owner.Do(http.MethodGet, "/v1/suppression-lists/"+listID, nil)
	prev := f.Owner.Hdr
	hdr := map[string]string{}
	for k, v := range prev {
		hdr[k] = v
	}
	if e := getL.Header.Get("ETag"); e != "" {
		hdr["If-Match"] = e
	}
	f.Owner.Hdr = hdr
	del := f.Owner.Do(http.MethodDelete, "/v1/suppression-lists/"+listID, nil)
	f.Owner.Hdr = prev
	if del.Status != http.StatusNoContent && del.Status != http.StatusOK {
		t.Fatalf("delete list = %d %s", del.Status, del.Body)
	}

	// List A → unsub → list B ok → list A suppressed; global unsub blocks all.
	before := f.smtpCount()
	ra := f.submit(map[string]any{
		"to": []map[string]any{{"address": f.Email}}, "subject": "list-A",
		"html": htmlTrackBody(), "listId": "news.a.example.com",
	}, nil)
	harness.ExpectStatus(t, ra, http.StatusAccepted)
	ida := ra.Str("id")
	if f.waitSent(ida, 90*time.Second).Str("state") != "sent" {
		t.Fatalf("list A not sent")
	}
	msgs := f.waitSMTP(before+1, 30*time.Second)
	raw := msgs[len(msgs)-1].Data
	if extractToken(raw, reListID) != "news.a.example.com" {
		t.Fatalf("List-Id missing/wrong in MIME:\n%s", raw)
	}
	unsubTok := extractToken(raw, reUnsub)
	if unsubTok == "" {
		t.Fatalf("no List-Unsubscribe token in MIME:\n%s", raw)
	}
	f.Sink.Reset()
	res := publicPOST(t, f.Node.Base, "/u/"+unsubTok, "List-Unsubscribe=One-Click",
		"application/x-www-form-urlencoded")
	defer func() { _ = res.Body.Close() }()
	body := readBody(res)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("unsub POST = %d %s", res.StatusCode, body)
	}
	_ = harness.WaitEvent(t, f.Sink, "list.unsubscribed", 45*time.Second)

	rb := f.submit(map[string]any{
		"to": []map[string]any{{"address": f.Email}}, "subject": "list-B",
		"html": htmlTrackBody(), "listId": "news.b.example.com",
	}, nil)
	if rb.Status != http.StatusAccepted {
		t.Fatalf("list B should send after A-unsub: %d %s", rb.Status, rb.Body)
	}
	if f.waitSent(rb.Str("id"), 90*time.Second).Str("state") != "sent" {
		t.Fatalf("list B not sent")
	}

	ra2 := f.submit(map[string]any{
		"to": []map[string]any{{"address": f.Email}}, "subject": "list-A-again",
		"html": htmlTrackBody(), "listId": "news.a.example.com",
	}, nil)
	harness.ExpectProblem(t, ra2, http.StatusUnprocessableEntity, "recipient_suppressed")

	// Global unsub (no listId) blocks every later send.
	f2 := startFixture(t, fixtureOpts{})
	enableOrgSwitch(t, f2.Owner, "sending.suppression", true)
	enableOrgSwitch(t, f2.Owner, "sending.listUnsubscribe", true)
	enableOrgSwitch(t, f2.Owner, "tracking.opens", true)
	before2 := f2.smtpCount()
	rg := f2.submit(map[string]any{
		"to": []map[string]any{{"address": f2.Email}}, "subject": "global-unsub",
		"html": htmlTrackBody(),
	}, nil)
	harness.ExpectStatus(t, rg, http.StatusAccepted)
	_ = f2.waitSent(rg.Str("id"), 90*time.Second)
	raw2 := f2.waitSMTP(before2+1, 30*time.Second)
	tok := extractToken(raw2[len(raw2)-1].Data, reUnsub)
	if tok == "" {
		t.Fatalf("no global unsub token")
	}
	res2 := publicPOST(t, f2.Node.Base, "/u/"+tok, "List-Unsubscribe=One-Click",
		"application/x-www-form-urlencoded")
	defer func() { _ = res2.Body.Close() }()
	_ = readBody(res2)
	if res2.StatusCode != http.StatusOK {
		t.Fatalf("global unsub = %d", res2.StatusCode)
	}
	later := f2.submit(map[string]any{
		"to": []map[string]any{{"address": f2.Email}}, "subject": "after-global",
		"text": "x", "listId": "any.list.example.com",
	}, nil)
	harness.ExpectProblem(t, later, http.StatusUnprocessableEntity, "recipient_suppressed")
}

// Pre-send CEL hold → scheduled; send-now should release (product bug if stuck).
func TestP5_PreSendHoldAndSendNow(t *testing.T) {
	f := startFixture(t, fixtureOpts{events: []string{"message.sent"}})
	enableOrgSwitch(t, f.Owner, "sending.preSendHook", true)
	patchOrgSetting(t, f.Owner, "sending.preSendHook.cel", `"hold"`)

	r := f.submit(map[string]any{
		"to": []map[string]any{{"address": f.Email}}, "subject": "presend-hold", "text": "x",
	}, nil)
	harness.ExpectStatus(t, r, http.StatusAccepted)
	id := r.Str("id")
	held := f.Owner.WaitJSON(t, "/v1/outbound-messages/"+id, 60*time.Second, "held", func(r harness.Resp) bool {
		return r.Status == http.StatusOK && r.Str("state") == "scheduled"
	})
	if held.Str("state") != "scheduled" {
		t.Fatalf("want scheduled hold, got %s %s", held.Str("state"), held.Body)
	}
	enableOrgSwitch(t, f.Owner, "sending.preSendHook", false)
	sn := f.Owner.Do(http.MethodPost, "/v1/outbound-messages/"+id+"/send-now", nil)
	if sn.Status != http.StatusOK && sn.Status != http.StatusAccepted {
		t.Fatalf("send-now hold = %d %s", sn.Status, sn.Body)
	}
	sent := waitSentOrStuck(t, f, id, 45*time.Second)
	if sent.Str("state") != "sent" {
		t.Fatal("BUG: send-now after pre-send hold leaves outbound queued — JetStream Msg-Id " +
			"outboundId/0 deduplicated after the hold claim; see submit.SendNow + publish — state=" +
			sent.Str("state"))
	}
}

// Pre-send CEL reject → permanent failed (one attempt).
func TestP5_PreSendReject(t *testing.T) {
	f := startFixture(t, fixtureOpts{events: []string{"message.failed", "message.sent"}})
	enableOrgSwitch(t, f.Owner, "sending.preSendHook", true)
	patchOrgSetting(t, f.Owner, "sending.preSendHook.cel", `"reject"`)
	rj := f.submit(map[string]any{
		"to": []map[string]any{{"address": f.Email}}, "subject": "presend-reject", "text": "x",
	}, nil)
	harness.ExpectStatus(t, rj, http.StatusAccepted)
	id := rj.Str("id")
	// Reject should fail permanently on the first attempt — poll briefly for that shape.
	var got harness.Resp
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		got = f.Owner.Do(http.MethodGet, "/v1/outbound-messages/"+id, nil)
		if got.Status != http.StatusOK {
			time.Sleep(200 * time.Millisecond)
			continue
		}
		st := got.Str("state")
		attempts, _ := got.JSON["attempts"].([]any)
		if st == "failed" {
			if len(attempts) == 1 {
				return // happy path
			}
			// Failed after retries — still wrong shape for a permanent pre-send reject.
			t.Fatal("BUG: presend reject reached failed only after retries (want 1 permanent attempt); " +
				"suspect submit/policy.fromError missing CodePresendRejected — body=" + string(got.Body))
		}
		if st == "sent" {
			t.Fatal("BUG: pre-send CEL reject allowed the send; body=" + string(got.Body))
		}
		if len(attempts) >= 1 {
			if m, _ := attempts[0].(map[string]any); m != nil {
				if kind, _ := m["kind"].(string); kind == "temporary" {
					t.Fatal("BUG: pre-send CEL reject classified as temporary (retries) instead of permanent failed; " +
						"request CEL=\"reject\" expected state=failed kind=permanent code=presend_rejected; " +
						"actual attempts[0].kind=temporary — suspect internal/submit/policy/policy.go fromError — body=" +
						string(got.Body))
				}
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("want failed on reject, got %s %s", got.Str("state"), got.Body)
}
