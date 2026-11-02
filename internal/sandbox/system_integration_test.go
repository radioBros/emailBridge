//go:build integration

package sandbox

import (
	"testing"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/submit"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

// System mail carries login codes and reset links; every member can download captures, so the sandbox keeps
// only the envelope and subject, and the send pipeline never copies it to Sent (S10 §7).
func TestIntegrationSystemMailCapturedWithoutBody(t *testing.T) {
	h := newHarness(t, "development", false)
	h.setPolicy(true, PolicyCapture, "")
	sys := tenancy.WithCaller(h.ctx, tenancy.Caller{Kind: tenancy.CallerSystem, OrganizationID: h.org})
	res, err := h.submit.Submit(sys, h.acc.ID, submit.SubmitRequest{
		To: []submit.AddressInput{{Address: testTo}}, Subject: "Reset your password", Text: "token-123",
	}, "", false)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	h.work()
	m := h.getOutbound(res.Accepted.ID)
	if !m.System || m.State != submit.StateCaptured || m.CopyToSent.Enabled || m.CopyToSent.State != submit.CopySkipped {
		t.Fatalf("outbound: system=%v state=%s copy=%+v", m.System, m.State, m.CopyToSent)
	}
	list, err := h.svc.List(h.ctx, ListFilter{})
	if err != nil || len(list.Data) != 1 {
		t.Fatalf("list: %v %+v", err, list)
	}
	row, err := h.svc.Get(h.ctx, list.Data[0].ID)
	if err != nil || row.RawRef != "" || row.Subject != "Reset your password" {
		t.Fatalf("capture row: %v %+v", err, row)
	}
	if _, _, err := h.svc.Raw(h.ctx, row.ID); errs.CodeOf(err) != errs.CodeNotFound {
		t.Fatalf("raw download of system mail: %v", err)
	}
}
