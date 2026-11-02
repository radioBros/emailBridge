//go:build integration

package sandbox

import (
	"context"
	"io"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/radiobros/emailbridge/internal/events"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/submit"
)

func TestIntegrationPoliciesThroughWorker(t *testing.T) {
	h := newHarness(t, "development", true) // real S3 + simulator
	h.clearSMTP()

	t.Run("capture", func(t *testing.T) {
		h.setPolicy(true, PolicyCapture, "")
		h.clearSMTP()
		id := h.submitMsg()
		h.work()
		m := h.getOutbound(id)
		if m.State != submit.StateCaptured {
			t.Fatalf("state: %s", m.State)
		}
		msgs, err := h.sim.Client.ListSMTPMessages(h.ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(msgs) != 0 {
			t.Fatalf("SMTP received %d messages", len(msgs))
		}
		list, err := h.svc.List(h.ctx, ListFilter{AccountID: h.acc.ID})
		if err != nil || len(list.Data) != 1 {
			t.Fatalf("captures: %+v (%v)", list, err)
		}
		cap := list.Data[0]
		if cap.OutboundID != id || cap.Subject != "Sandbox" {
			t.Fatalf("%+v", cap)
		}
		raw, body, err := h.svc.Raw(h.ctx, cap.ID)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = body.Close() }()
		data, _ := io.ReadAll(body)
		if raw.ID != cap.ID || len(data) == 0 {
			t.Fatalf("raw: id=%s len=%d", raw.ID, len(data))
		}
		found := false
		for _, ev := range h.eventsOf(id) {
			if ev.Type == events.MessageCaptured {
				found = true
				break
			}
		}
		if !found {
			t.Fatal("missing message.captured")
		}
	})

	t.Run("drop", func(t *testing.T) {
		h.setPolicy(true, PolicyDrop, "")
		h.clearSMTP()
		before, _ := h.svc.List(h.ctx, ListFilter{})
		nBefore := len(before.Data)
		id := h.submitMsg()
		h.work()
		m := h.getOutbound(id)
		if m.State != submit.StateCaptured {
			t.Fatalf("state: %s", m.State)
		}
		msgs, err := h.sim.Client.ListSMTPMessages(h.ctx)
		if err != nil || len(msgs) != 0 {
			t.Fatalf("SMTP: %d (%v)", len(msgs), err)
		}
		after, _ := h.svc.List(h.ctx, ListFilter{})
		if len(after.Data) != nBefore {
			t.Fatalf("drop must not create capturedMessages: before=%d after=%d", nBefore, len(after.Data))
		}
		found := false
		for _, ev := range h.eventsOf(id) {
			if ev.Type == events.MessageCaptured {
				found = true
			}
		}
		if !found {
			t.Fatal("missing message.captured for drop")
		}
	})

	t.Run("rewrite", func(t *testing.T) {
		h.setPolicy(true, PolicyRewrite, rewrite)
		h.clearSMTP()
		id := h.submitMsg()
		h.work()
		m := h.getOutbound(id)
		if m.State != submit.StateSent {
			t.Fatalf("state: %s", m.State)
		}
		if len(m.Attempts) == 0 || !m.Attempts[0].Rewritten {
			t.Fatalf("attempt must be rewritten: %+v", m.Attempts)
		}
		msgs, err := h.sim.Client.ListSMTPMessages(h.ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(msgs) != 1 {
			t.Fatalf("SMTP count: %d", len(msgs))
		}
		if len(msgs[0].To) != 1 || msgs[0].To[0] != rewrite {
			t.Fatalf("RCPT TO: %v", msgs[0].To)
		}
	})

	t.Run("allow", func(t *testing.T) {
		h.setPolicy(true, PolicyAllow, "")
		h.clearSMTP()
		id := h.submitMsg()
		h.work()
		m := h.getOutbound(id)
		if m.State != submit.StateSent {
			t.Fatalf("state: %s", m.State)
		}
		msgs, err := h.sim.Client.ListSMTPMessages(h.ctx)
		if err != nil || len(msgs) != 1 {
			t.Fatalf("SMTP: %d (%v)", len(msgs), err)
		}
		if msgs[0].To[0] != testTo {
			t.Fatalf("RCPT TO: %v", msgs[0].To)
		}
	})
}

func TestIntegrationProductionDefaultOff(t *testing.T) {
	h := newHarness(t, "production", false)
	// No settings doc → catalogue default: enabled=false, policy=allow.
	h.clearSMTP()
	id := h.submitMsg()
	h.work()
	m := h.getOutbound(id)
	if m.State != submit.StateSent {
		t.Fatalf("production default must send: %s", m.State)
	}
	msgs, err := h.sim.Client.ListSMTPMessages(h.ctx)
	if err != nil || len(msgs) != 1 {
		t.Fatalf("SMTP: %d (%v)", len(msgs), err)
	}
}

func TestIntegrationDevelopmentDefaultCapture(t *testing.T) {
	h := newHarness(t, "development", false)
	// No settings → enabled=true, policy=capture.
	h.clearSMTP()
	id := h.submitMsg()
	h.work()
	m := h.getOutbound(id)
	if m.State != submit.StateCaptured {
		t.Fatalf("dev default must capture: %s", m.State)
	}
	msgs, err := h.sim.Client.ListSMTPMessages(h.ctx)
	if err != nil || len(msgs) != 0 {
		t.Fatalf("SMTP: %d (%v)", len(msgs), err)
	}
}

func TestIntegrationAccountSwitchOverrides(t *testing.T) {
	h := newHarness(t, "development", false)
	h.setPolicy(true, PolicyCapture, "")
	h.setAccountEnabled(false) // account wins → send
	h.clearSMTP()
	id := h.submitMsg()
	h.work()
	if h.getOutbound(id).State != submit.StateSent {
		t.Fatal("account switch must disable sandbox")
	}
}

func TestIntegrationRewriteRequiresAddress(t *testing.T) {
	h := newHarness(t, "development", false)
	h.setPolicy(true, PolicyRewrite, "")
	id := h.submitMsg()
	h.work()
	m := h.getOutbound(id)
	// Gate error becomes a failed attempt before the session; message stays queued for retry.
	if m.State != submit.StateQueued && m.State != submit.StateFailed {
		t.Fatalf("state: %s attempts=%+v", m.State, m.Attempts)
	}
	if len(m.Attempts) == 0 {
		t.Fatal("expected an attempt recording the rewriteTo validation failure")
	}
}

func TestIntegrationRetentionSweep(t *testing.T) {
	h := newHarness(t, "development", false)
	h.setPolicy(true, PolicyCapture, "")
	id := h.submitMsg()
	h.work()
	list, err := h.svc.List(h.ctx, ListFilter{})
	if err != nil || len(list.Data) == 0 {
		t.Fatalf("list: %+v (%v)", list, err)
	}
	capID := list.Data[0].ID
	var row Captured
	if err := h.db.Collection(Collection).FindOne(h.ctx, bson.M{"_id": capID}).Decode(&row); err != nil {
		t.Fatal(err)
	}
	past := time.Now().UTC().Add(-time.Hour)
	if _, err := h.db.Collection(Collection).UpdateOne(h.ctx, bson.M{"_id": capID},
		bson.M{"$set": bson.M{"expireAt": past}}); err != nil {
		t.Fatal(err)
	}
	if row.RawRef != "" && h.mem != nil && !h.mem.has(row.RawRef) {
		t.Fatal("expected S3 object before sweep")
	}
	n, err := h.svc.sweepBatch(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("sweep: n=%d err=%v", n, err)
	}
	if h.mem != nil && row.RawRef != "" && h.mem.has(row.RawRef) {
		t.Fatal("S3 object should be gone")
	}
	if _, err := h.svc.Get(h.ctx, capID); err == nil {
		t.Fatal("capture should be deleted")
	}
	_ = id
}

func TestIntegrationGateNilSandboxSends(t *testing.T) {
	h := newHarness(t, "development", false)
	h.submit.Sandbox = nil
	h.clearSMTP()
	id := h.submitMsg()
	h.work()
	if h.getOutbound(id).State != submit.StateSent {
		t.Fatal("nil sandbox must send")
	}
}

func TestIntegrationCaptureUsesOrgScopedKey(t *testing.T) {
	h := newHarness(t, "development", false)
	h.setPolicy(true, PolicyCapture, "")
	id := h.submitMsg()
	h.work()
	list, _ := h.svc.List(h.ctx, ListFilter{})
	var row Captured
	_ = h.db.Collection(Collection).FindOne(h.ctx, bson.M{"_id": list.Data[0].ID}).Decode(&row)
	if row.RawRef == "" || row.OrganizationID != h.org {
		t.Fatalf("%+v", row)
	}
	wantPrefix := "sandbox/" + h.org + "/"
	if len(row.RawRef) < len(wantPrefix) || row.RawRef[:len(wantPrefix)] != wantPrefix {
		t.Fatalf("key %q", row.RawRef)
	}
	_ = id
	_ = store.PrefixCapture
}
