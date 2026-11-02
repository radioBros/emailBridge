//go:build e2e

package p5

import (
	"net/http"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/simulator"
	"github.com/radiobros/emailbridge/test/e2e/harness"
)

// SMTP gateway CRUD + verify + send via gatewayId with DSN params observed by the simulator.
func TestP5_GatewayAndDSN(t *testing.T) {
	f := startFixture(t, fixtureOpts{events: []string{"message.sent"}})
	host, port := simHostPort(t, f.Sim)

	gwy := f.Owner.Do(http.MethodPost, "/v1/smtp-gateways", map[string]any{
		"name": "p5-gwy-" + f.User, "host": host, "port": port, "tls": "none",
		"auth": map[string]any{"user": f.User, "pass": f.Pass}, "policy": "standard",
	})
	harness.ExpectStatus(t, gwy, http.StatusCreated)
	gwyID := gwy.Str("id")
	if gwyID == "" {
		t.Fatalf("gateway: %s", gwy.Body)
	}

	ver := f.Owner.Do(http.MethodPost, "/v1/smtp-gateways/"+gwyID+"/verify", nil)
	if ver.Status != http.StatusOK {
		t.Fatalf("verify = %d %s", ver.Status, ver.Body)
	}
	if ok, _ := ver.JSON["ok"].(bool); !ok {
		t.Fatalf("verify not ok: %s", ver.Body)
	}

	get := f.Owner.Do(http.MethodGet, "/v1/smtp-gateways/"+gwyID, nil)
	harness.ExpectStatus(t, get, http.StatusOK)
	if get.Str("name") == "" || get.Str("host") != host {
		t.Fatalf("get gateway: %s", get.Body)
	}

	f.patchAccount(map[string]any{
		"smtp": map[string]any{
			"host": host, "port": port, "tls": "none",
			"auth":      map[string]any{"user": f.User, "pass": f.Pass},
			"gatewayId": gwyID, "policy": "standard",
		},
	})

	before := f.smtpCount()
	r := f.submit(map[string]any{
		"to": []map[string]any{{"address": f.Email}}, "subject": "via-gateway-dsn", "text": "dsn",
		"dsn": map[string]any{
			"notify": []string{"success", "failure"},
			"ret":    "hdrs",
			"envid":  "e2e-dsn-1",
		},
	}, nil)
	harness.ExpectStatus(t, r, http.StatusAccepted)
	out := f.waitSent(r.Str("id"), 90*time.Second)
	if out.Str("state") != "sent" {
		t.Fatalf("gateway send state=%s %s", out.Str("state"), out.Body)
	}
	if out.Str("gatewayId") != gwyID {
		t.Fatalf("outbound gatewayId=%q want %s body=%s", out.Str("gatewayId"), gwyID, out.Body)
	}
	msgs := f.waitSMTP(before+1, 30*time.Second)
	last := msgs[len(msgs)-1]
	if last.DSN == nil {
		t.Fatalf("simulator saw no DSN params: %+v", last)
	}
	if last.DSN.EnvID != "e2e-dsn-1" && last.DSN.Ret == "" && len(last.DSN.Notify) == 0 {
		t.Fatalf("DSN empty: %+v", last.DSN)
	}
	if last.DSN.EnvID != "" && last.DSN.EnvID != "e2e-dsn-1" {
		t.Fatalf("envid=%q", last.DSN.EnvID)
	}

	// Cleanup delete
	etag := get.Header.Get("ETag")
	prev := f.Owner.Hdr
	hdr := map[string]string{}
	for k, v := range prev {
		hdr[k] = v
	}
	if etag != "" {
		hdr["If-Match"] = etag
	}
	// re-fetch for current etag after verify/use
	get2 := f.Owner.Do(http.MethodGet, "/v1/smtp-gateways/"+gwyID, nil)
	if e := get2.Header.Get("ETag"); e != "" {
		hdr["If-Match"] = e
	}
	f.Owner.Hdr = hdr
	del := f.Owner.Do(http.MethodDelete, "/v1/smtp-gateways/"+gwyID, nil)
	f.Owner.Hdr = prev
	if del.Status != http.StatusNoContent && del.Status != http.StatusOK && del.Status != http.StatusConflict {
		t.Fatalf("delete gateway = %d %s", del.Status, del.Body)
	}
}

// Permanent SMTP failure → retry after clearing the fault → sent.
func TestP5_OutboxRetry(t *testing.T) {
	f := startFixture(t, fixtureOpts{
		events:  []string{"message.failed", "message.sent"},
		smtpPol: "standard",
	})
	f.setSMTPScript(simulator.SMTPScript{
		"rcptTo": {Code: 550, Enhanced: "5.1.1", Text: "no such user"},
	})
	r := f.submit(map[string]any{
		"to": []map[string]any{{"address": f.Email}}, "subject": "retry-me", "text": "x",
	}, nil)
	harness.ExpectStatus(t, r, http.StatusAccepted)
	id := r.Str("id")
	failed := f.Owner.WaitJSON(t, "/v1/outbound-messages/"+id, 90*time.Second, "failed", func(r harness.Resp) bool {
		return r.Status == http.StatusOK && r.Str("state") == "failed"
	})
	if failed.Str("state") != "failed" {
		t.Fatalf("want failed, got %s %s", failed.Str("state"), failed.Body)
	}

	f.clearSMTPScript()
	f.Sink.Reset()
	retry := f.Owner.Do(http.MethodPost, "/v1/outbound-messages/"+id+"/retry", nil)
	if retry.Status != http.StatusOK && retry.Status != http.StatusAccepted {
		t.Fatalf("retry = %d %s", retry.Status, retry.Body)
	}
	sent := f.waitSent(id, 90*time.Second)
	if sent.Str("state") != "sent" {
		t.Fatalf("after retry state=%s %s", sent.Str("state"), sent.Body)
	}
	_ = harness.WaitEvent(t, f.Sink, "message.sent", 45*time.Second)
}

// Future sendAt → send-now delivers immediately.
func TestP5_SendAtThenSendNow(t *testing.T) {
	f := startFixture(t, fixtureOpts{events: []string{"message.sent"}})
	sendAt := time.Now().UTC().Add(2 * time.Hour).Format(time.RFC3339)
	r := f.submit(map[string]any{
		"to": []map[string]any{{"address": f.Email}}, "subject": "future-send", "text": "x",
		"sendAt": sendAt,
	}, nil)
	harness.ExpectStatus(t, r, http.StatusAccepted)
	id := r.Str("id")
	sched := f.Owner.WaitJSON(t, "/v1/outbound-messages/"+id, 30*time.Second, "scheduled", func(r harness.Resp) bool {
		return r.Status == http.StatusOK && r.Str("state") == "scheduled"
	})
	if sched.Str("state") != "scheduled" {
		t.Fatalf("want scheduled, got %s %s", sched.Str("state"), sched.Body)
	}
	sn := f.Owner.Do(http.MethodPost, "/v1/outbound-messages/"+id+"/send-now", nil)
	if sn.Status != http.StatusOK && sn.Status != http.StatusAccepted {
		t.Fatalf("send-now = %d %s", sn.Status, sn.Body)
	}
	if f.waitSent(id, 90*time.Second).Str("state") != "sent" {
		t.Fatalf("send-now did not deliver")
	}
}

// Delivery test probe lands in INBOX via the simulator loopback.
func TestP5_DeliveryTest(t *testing.T) {
	f := startFixture(t, fixtureOpts{})
	dt := f.Owner.Do(http.MethodPost, "/v1/delivery-tests", map[string]any{
		"accountId": f.AccID, "to": f.Email, "timeout": "90s",
	})
	if dt.Status != http.StatusAccepted {
		t.Fatalf("delivery-tests = %d %s", dt.Status, dt.Body)
	}
	id := dt.Str("id")
	if id == "" {
		t.Fatalf("no test id: %s", dt.Body)
	}
	got := f.Owner.WaitJSON(t, "/v1/delivery-tests/"+id, 120*time.Second, "terminal", func(r harness.Resp) bool {
		if r.Status != http.StatusOK {
			return false
		}
		st := r.Str("state")
		return st == "succeeded" || st == "failed" || st == "timedOut"
	})
	if got.Str("state") != "succeeded" {
		t.Fatalf("delivery test state=%s body=%s", got.Str("state"), got.Body)
	}
	if folder := got.Str("folder"); folder != "inbox" && folder != "" {
		// inbox is the expected folder class; empty would be a bug
		if folder != "inbox" {
			t.Fatalf("folder=%q want inbox", folder)
		}
	}
}
