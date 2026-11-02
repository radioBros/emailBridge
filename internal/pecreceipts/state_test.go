package pecreceipts

import (
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/pec"
)

func rcpt(id, typ, to string) *Receipt {
	return &Receipt{ID: id, Type: typ, DeliveredTo: to, At: time.Unix(1_800_000_000, 0)}
}

func TestApplyStateMachine(t *testing.T) {
	st := &OutboundPECStatus{}
	acc := rcpt("r1", pec.ReceiptAcceptance, "")
	acc.Recipients = []pec.Recipient{{Address: "A@pec.test", Kind: "certificato"}, {Address: "b@pec.test", Kind: "certificato"}, {Address: "x@mail.test", Kind: "esterno"}}
	if !Apply(st, acc) || st.State != StateAccepted || st.AcceptedAt == nil || len(st.Certified) != 2 {
		t.Fatalf("accepted: %+v", st)
	}
	if Apply(st, acc) {
		t.Fatal("the same receipt applies once")
	}
	Apply(st, rcpt("r2", pec.ReceiptDeliveryNotice, "a@pec.test"))
	if st.State != StateAccepted || st.Deliveries[0].State != DeliveryWarning {
		t.Fatalf("warning: %+v", st)
	}
	Apply(st, rcpt("r3", pec.ReceiptDelivery, "a@pec.test"))
	if st.State != StatePartiallyDelivered || st.Deliveries[0].State != DeliveryDelivered {
		t.Fatalf("partial: %+v", st)
	}
	Apply(st, rcpt("r4", pec.ReceiptDeliveryNotice, "a@pec.test")) // a late warning never downgrades
	if st.Deliveries[0].State != DeliveryDelivered {
		t.Fatal("downgraded")
	}
	Apply(st, rcpt("r5", pec.ReceiptDelivery, "b@pec.test"))
	if st.State != StateDelivered {
		t.Fatalf("delivered (the ordinary recipient never blocks it): %+v", st)
	}
	Apply(st, rcpt("r6", pec.ReceiptTakingCharge, "b@pec.test"))
	if st.State != StateDelivered {
		t.Fatal("taking charge changes nothing")
	}

	failed := &OutboundPECStatus{}
	Apply(failed, rcpt("f1", pec.ReceiptDeliveryError, "a@pec.test"))
	if failed.State != StateFailed {
		t.Fatalf("all recipients failed (before the acceptance arrived): %+v", failed)
	}
	virus := &OutboundPECStatus{}
	v := rcpt("v1", pec.ReceiptVirus, "")
	v.Recipients = []pec.Recipient{{Address: "c@pec.test"}}
	Apply(virus, v)
	if virus.Deliveries[0].Recipient != "c@pec.test" || virus.State != StateFailed {
		t.Fatalf("virus: %+v", virus)
	}
	no := &OutboundPECStatus{}
	Apply(no, rcpt("n1", pec.ReceiptNonAcceptance, ""))
	Apply(no, rcpt("n2", pec.ReceiptDelivery, "a@pec.test"))
	if no.State != StateNotAccepted {
		t.Fatalf("non-acceptance is final: %+v", no)
	}
	if compute(&OutboundPECStatus{}) != StateAccepted {
		t.Fatal("no targets")
	}
	if got := idForms("<a@b>"); got[0] != "<a@b>" || got[1] != "a@b" {
		t.Fatal(got)
	}
}
