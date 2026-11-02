package simulator_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/mailproto/imapc"
	"github.com/radiobros/emailbridge/internal/mailproto/smtpc"
	intpec "github.com/radiobros/emailbridge/internal/pec"
	"github.com/radiobros/emailbridge/simulator/simtest"
)

func waitRawInbox(t *testing.T, env *simtest.Env, user, pass string, min int) [][]byte {
	t.Helper()
	ctx := context.Background()
	cl := dialIMAP(t, env, user, pass, imapc.TLSNone)
	deadline := time.Now().Add(3 * time.Second)
	var last int
	for time.Now().Before(deadline) {
		sel, err := cl.Select(ctx, "INBOX", false)
		if err != nil {
			t.Fatal(err)
		}
		last = int(sel.Exists)
		if last >= min {
			uids, err := cl.UIDs(ctx)
			if err != nil {
				t.Fatal(err)
			}
			out := make([][]byte, 0, len(uids))
			for _, uid := range uids {
				raw, err := cl.Raw(ctx, uid, 0)
				if err != nil || len(raw) == 0 {
					continue
				}
				out = append(out, raw)
			}
			if len(out) >= min {
				return out
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%s INBOX: have %d want ≥ %d", user, last, min)
	return nil
}

func TestPECSimSendAcceptanceDelivery(t *testing.T) {
	env := simtest.StartPEC(t)
	ctx := context.Background()
	_ = env.Client.CreateUser(ctx, "sender@pec.sim.test", "p")
	_ = env.Client.CreateUser(ctx, "rcpt@pec.sim.test", "p")

	s := openSMTP(t, env, "sender@pec.sim.test", "p", smtpc.TLSNone)
	body := "From: sender@pec.sim.test\r\nTo: rcpt@pec.sim.test\r\nSubject: Verbale\r\n" +
		"Message-ID: <orig-sim@legacyapp.test>\r\n\r\nhello pec\r\n"
	if _, err := s.Send(ctx, smtpc.Envelope{
		From: "sender@pec.sim.test", To: []string{"rcpt@pec.sim.test"},
	}, strings.NewReader(body), smtpc.Hooks{}); err != nil {
		t.Fatal(err)
	}

	senderRaw := waitRawInbox(t, env, "sender@pec.sim.test", "p", 2)
	rcptRaw := waitRawInbox(t, env, "rcpt@pec.sim.test", "p", 1)
	anchors := env.Sim.PEC().Anchors()

	var kinds []string
	for _, raw := range senderRaw {
		m, err := intpec.Parse(raw, intpec.Options{Anchors: anchors})
		if err != nil {
			t.Fatalf("parse sender: %v", err)
		}
		kinds = append(kinds, m.Type)
		if !m.Signature.Valid || m.Signature.Trusted == nil || !*m.Signature.Trusted {
			t.Fatalf("sig: %+v", m.Signature)
		}
		if m.Kind == intpec.KindReceipt && m.RefMessageID != "<orig-sim@legacyapp.test>" {
			t.Fatalf("ref: %q", m.RefMessageID)
		}
	}
	joined := strings.Join(kinds, ",")
	if !strings.Contains(joined, intpec.ReceiptAcceptance) || !strings.Contains(joined, intpec.ReceiptDelivery) {
		t.Fatalf("kinds: %v", kinds)
	}

	em, err := intpec.Parse(rcptRaw[0], intpec.Options{Anchors: anchors})
	if err != nil || em.Kind != intpec.KindEnvelope || !em.Signature.Valid || !*em.Signature.Trusted {
		t.Fatalf("envelope: %+v %v", em, err)
	}

	recs, err := env.Client.ListPECReceipts(ctx, "sender@pec.sim.test")
	if err != nil || len(recs) < 2 {
		t.Fatalf("control receipts: %v %d", err, len(recs))
	}
	pem, err := env.Client.PECCA(ctx)
	if err != nil || !strings.Contains(string(pem), "BEGIN CERTIFICATE") {
		t.Fatalf("ca: %v %s", err, pem)
	}
}

func TestPECSimOrdinaryAnomaly(t *testing.T) {
	env := simtest.StartPEC(t)
	ctx := context.Background()
	_ = env.Client.CreateUser(ctx, "plain@example.com", "p")
	_ = env.Client.CreateUser(ctx, "rcpt@pec.sim.test", "p")

	s := openSMTP(t, env, "plain@example.com", "p", smtpc.TLSNone)
	body := "From: plain@example.com\r\nTo: rcpt@pec.sim.test\r\nSubject: hi\r\nMessage-ID: <a@x>\r\n\r\nx\r\n"
	if _, err := s.Send(ctx, smtpc.Envelope{
		From: "plain@example.com", To: []string{"rcpt@pec.sim.test"},
	}, strings.NewReader(body), smtpc.Hooks{}); err != nil {
		t.Fatal(err)
	}
	rcptRaw := waitRawInbox(t, env, "rcpt@pec.sim.test", "p", 1)
	m, err := intpec.Parse(rcptRaw[0], intpec.Options{Anchors: env.Sim.PEC().Anchors()})
	if err != nil || m.Kind != intpec.KindAnomaly {
		t.Fatalf("anomaly: %+v %v", m, err)
	}
}

func TestPECSimNouserFault(t *testing.T) {
	env := simtest.StartPEC(t)
	ctx := context.Background()
	_ = env.Client.CreateUser(ctx, "sender@pec.sim.test", "p")
	s := openSMTP(t, env, "sender@pec.sim.test", "p", smtpc.TLSNone)
	body := "From: sender@pec.sim.test\r\nTo: nouser@pec.sim.test\r\nSubject: x\r\nMessage-ID: <n@t>\r\n\r\nz\r\n"
	if _, err := s.Send(ctx, smtpc.Envelope{
		From: "sender@pec.sim.test", To: []string{"nouser@pec.sim.test"},
	}, strings.NewReader(body), smtpc.Hooks{}); err != nil {
		t.Fatal(err)
	}
	raws := waitRawInbox(t, env, "sender@pec.sim.test", "p", 2)
	anchors := env.Sim.PEC().Anchors()
	var sawErr bool
	for _, raw := range raws {
		m, err := intpec.Parse(raw, intpec.Options{Anchors: anchors})
		if err != nil {
			t.Fatal(err)
		}
		if m.Type == intpec.ReceiptDeliveryError && m.Cert != nil && m.Cert.Error == "no-dest" {
			sawErr = true
			if !m.Signature.Valid || !*m.Signature.Trusted {
				t.Fatalf("sig: %+v", m.Signature)
			}
		}
	}
	if !sawErr {
		t.Fatal("missing errore-consegna")
	}
}
