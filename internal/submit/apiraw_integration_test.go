//go:build integration

package submit

import (
	"context"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/radiobros/emailbridge/internal/errs"
)

type recordingAPISender struct {
	mu   sync.Mutex
	raws []string
	err  error
}

func (r *recordingAPISender) Send(_ context.Context, _ string, raw []byte) (string, string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.raws = append(r.raws, string(raw))
	if r.err != nil {
		return "", "", r.err
	}
	return "api-1", "thr-1", nil
}

type rewriteGate struct{ to string }

func (g rewriteGate) Gate(context.Context, *Message, func() (io.ReadCloser, error)) (bool, []string, error) {
	return false, []string{g.to}, nil
}

// The Gmail API delivers to the MIME headers: the Bcc must be added and a sandbox rewrite must replace every
// recipient header, or the real recipients receive sandboxed mail.
func TestIntegrationHeaderAddressedSendBccAndSandbox(t *testing.T) {
	for _, provider := range []string{providerGmail, providerGraph} {
		t.Run(provider, func(t *testing.T) {
			h := newHarness(t)
			h.acc.Provider = provider
			h.accounts.put(h.acc)
			rec := &recordingAPISender{}
			if provider == providerGmail {
				h.svc.Gmail = rec
			} else {
				h.svc.Graph = rec
			}

			id := h.submit(SubmitRequest{
				To:  []AddressInput{{Address: "real@example.test"}},
				Cc:  []AddressInput{{Address: "copy@example.test"}},
				Bcc: []AddressInput{{Address: "hidden@example.test"}},
			})
			h.work()
			if m := h.get(id); m.State != StateSent {
				t.Fatalf("state %s", m.State)
			}
			head, _, _ := strings.Cut(rec.raws[0], "\r\n\r\n")
			if !strings.Contains(head, "Bcc: hidden@example.test") || !strings.Contains(head, "real@example.test") {
				t.Fatalf("bcc missing from API raw: %q", head)
			}

			h.svc.Sandbox = rewriteGate{to: "sandbox@example.test"}
			id = h.submit(SubmitRequest{
				To:  []AddressInput{{Address: "real@example.test"}},
				Bcc: []AddressInput{{Address: "hidden@example.test"}},
			})
			h.work()
			if m := h.get(id); m.State != StateSent {
				t.Fatalf("state %s", m.State)
			}
			head, _, _ = strings.Cut(rec.raws[1], "\r\n\r\n")
			for _, gone := range []string{"real@example.test", "hidden@example.test", "copy@example.test"} {
				if strings.Contains(head, gone) {
					t.Fatalf("sandboxed API send still addresses %s: %q", gone, head)
				}
			}
			if !strings.Contains(head, "To: sandbox@example.test") {
				t.Fatalf("rewrite address missing: %q", head)
			}
		})
	}
}

// A permanent API refusal fails the message at once instead of burning every retry.
func TestIntegrationHeaderAddressedSendPermanentFailure(t *testing.T) {
	h := newHarness(t)
	h.acc.Provider = providerGmail
	h.accounts.put(h.acc)
	h.svc.Gmail = &recordingAPISender{err: errs.New(errs.CodeValidationFailed, "Invalid To header")}
	id := h.submit(SubmitRequest{})
	h.work()
	if m := h.get(id); m.State != StateFailed {
		t.Fatalf("permanent refusal must fail the message, state %s", m.State)
	}
}

type recordingAPIGateway struct {
	mu    sync.Mutex
	mails []APIMail
	err   error
}

func (r *recordingAPIGateway) send(_ context.Context, m APIMail) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.mails = append(r.mails, m)
	if r.err != nil {
		return "", r.err
	}
	return "prov-1", nil
}

// An API gateway (P8.3) gets the envelope explicitly, Bcc included, and a sandbox rewrite replaces it.
func TestIntegrationAPIGatewaySend(t *testing.T) {
	h := newHarness(t)
	gw := &recordingAPIGateway{}
	gwyID := "gwy_01HTESTAPIGATEWAY00000000000"
	h.svc.Gateways = stubGateway{acc: &SMTPAccount{GatewayID: gwyID, APISend: gw.send}}

	id := h.submit(SubmitRequest{GatewayID: gwyID,
		To:  []AddressInput{{Address: "real@example.test"}},
		Bcc: []AddressInput{{Address: "hidden@example.test"}},
	})
	h.work()
	if m := h.get(id); m.State != StateSent || m.GatewayID != gwyID {
		t.Fatalf("api send: %+v", m)
	}
	got := gw.mails[0]
	if len(got.To) != 1 || got.To[0] != "real@example.test" || len(got.Bcc) != 1 || got.Bcc[0] != "hidden@example.test" || got.OutboundID != id {
		t.Fatalf("api mail: %+v", got)
	}
	if strings.Contains(string(got.Raw), "hidden@example.test") {
		t.Fatal("the transmitted copy must not name the blind recipient")
	}

	h.svc.Sandbox = rewriteGate{to: "sandbox@example.test"}
	h.submit(SubmitRequest{GatewayID: gwyID, To: []AddressInput{{Address: "real@example.test"}}, Bcc: []AddressInput{{Address: "hidden@example.test"}}})
	h.work()
	got = gw.mails[1]
	if len(got.To) != 1 || got.To[0] != "sandbox@example.test" || len(got.Bcc) != 0 || len(got.Cc) != 0 || strings.Contains(string(got.Raw), "real@example.test") {
		t.Fatalf("sandboxed api mail: %+v %q", got, got.Raw)
	}
}

// A timeout after the request left is an unknown outcome: atMostOnce never resends it.
func TestIntegrationAPIGatewayTimeoutAtMostOnce(t *testing.T) {
	h := newHarness(t)
	gw := &recordingAPIGateway{err: errs.Wrap(context.DeadlineExceeded, errs.CodeSMTPTemporary, "gateways.Send")}
	gwyID := "gwy_01HTESTAPIGATEWAY00000000000"
	h.svc.Gateways = stubGateway{acc: &SMTPAccount{GatewayID: gwyID, APISend: gw.send, Policy: "atMostOnce"}}
	id := h.submit(SubmitRequest{GatewayID: gwyID, Policy: "atMostOnce"})
	h.work()
	m := h.get(id)
	if m.State != StateOutcomeUnknown || m.State == StateSent {
		t.Fatalf("an unknown outcome under atMostOnce must be final, state %s", m.State)
	}
	if len(gw.mails) != 1 {
		t.Fatalf("sent %d times", len(gw.mails))
	}
}
