package pecreceipts

import (
	"context"
	"errors"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/pec"
)

// Outbound PEC states.
const (
	StateAccepted           = "accepted"
	StateNotAccepted        = "notAccepted"
	StateDelivered          = "delivered"
	StatePartiallyDelivered = "partiallyDelivered"
	StateFailed             = "failed"
)

// Delivery states of one recipient.
const (
	DeliveryDelivered = "delivered"
	DeliveryError     = "error"
	DeliveryWarning   = "warning"
)

// PECDelivery is one recipient's PEC outcome.
type PECDelivery struct {
	Recipient string    `bson:"recipient" json:"recipient" doc:"Certified recipient" example:"studio.rossi@pec.example.it"`
	State     string    `bson:"state" json:"state" doc:"Outcome for this recipient: delivered, error or warning (preavviso di mancata consegna)" example:"delivered"`
	At        time.Time `bson:"at" json:"at" doc:"Time of the receipt that set the state" example:"2026-10-27T08:30:07Z"`
	ReceiptID string    `bson:"receiptId" json:"receiptId" doc:"Receipt that set the state (rcp_…)" example:"rcp_01m5d20y80n4ps0apz3e6baqq3"`
}

// OutboundPECStatus is outboundMessages.pec.
type OutboundPECStatus struct {
	State      string     `bson:"state" json:"state" doc:"Overall PEC state: accepted, notAccepted, delivered, partiallyDelivered or failed" example:"delivered"`
	AcceptedAt *time.Time `bson:"acceptedAt,omitempty" json:"acceptedAt,omitempty" doc:"When the sender's provider accepted the message (ricevuta di accettazione)" example:"2026-10-27T08:30:03Z"`
	// Certified are the certified (PEC) recipients listed in the acceptance; ordinary ones get no delivery receipt.
	Certified  []string      `bson:"certified,omitempty" json:"-"`
	Deliveries []PECDelivery `bson:"deliveries" json:"deliveries" doc:"One outcome per certified recipient, from the delivery receipts"`
	Receipts   []string      `bson:"receipts" json:"-"` // receipt IDs applied (idempotence)
}

// applyToOutbound folds a receipt into the outbound message's PEC status (read-modify-write inside the
// caller's transaction; one account has one sync owner, and the transaction serialises concurrent writers).
func (r *Repo) applyToOutbound(tx context.Context, rc *Receipt) error {
	coll := r.DB.Collection(outboundCollection)
	var doc struct {
		PEC *OutboundPECStatus `bson:"pec"`
	}
	err := coll.FindOne(tx, bson.M{"_id": *rc.OutboundID, "organizationId": rc.OrganizationID},
		options.FindOne().SetProjection(bson.M{"pec": 1})).Decode(&doc)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil
	}
	if err != nil {
		return errs.Wrap(err, errs.CodeInternalError, "pecreceipts.outbound")
	}
	st := doc.PEC
	if st == nil {
		st = &OutboundPECStatus{}
	}
	if !Apply(st, rc) {
		return nil
	}
	if _, err := coll.UpdateOne(tx, bson.M{"_id": *rc.OutboundID, "organizationId": rc.OrganizationID},
		bson.M{"$set": bson.M{"pec": st}}); err != nil {
		return errs.Wrap(err, errs.CodeInternalError, "pecreceipts.outbound")
	}
	return nil
}

// Apply folds rc into st and reports whether anything changed. It is idempotent per receipt.
func Apply(st *OutboundPECStatus, rc *Receipt) bool {
	for _, id := range st.Receipts {
		if id == rc.ID {
			return false
		}
	}
	st.Receipts = append(st.Receipts, rc.ID)
	switch rc.Type {
	case pec.ReceiptNonAcceptance:
		st.State = StateNotAccepted
		return true
	case pec.ReceiptAcceptance:
		at := rc.At
		st.AcceptedAt = &at
		st.Certified = st.Certified[:0]
		for _, p := range rc.Recipients {
			if p.Kind != "esterno" {
				st.Certified = append(st.Certified, strings.ToLower(p.Address))
			}
		}
	case pec.ReceiptDelivery:
		setDelivery(st, recipientOf(rc), DeliveryDelivered, rc)
	case pec.ReceiptDeliveryError, pec.ReceiptVirus:
		setDelivery(st, recipientOf(rc), DeliveryError, rc)
	case pec.ReceiptDeliveryNotice:
		setDelivery(st, recipientOf(rc), DeliveryWarning, rc)
	default:
		return true // taking charge: recorded, no state change
	}
	if st.State != StateNotAccepted {
		st.State = compute(st)
	}
	return true
}

func recipientOf(rc *Receipt) string {
	if rc.DeliveredTo != "" {
		return strings.ToLower(rc.DeliveredTo)
	}
	for _, p := range rc.Recipients {
		if p.Kind != "esterno" {
			return strings.ToLower(p.Address)
		}
	}
	return ""
}

// setDelivery records a recipient's outcome; a final outcome (delivered / error) is never downgraded to a warning.
func setDelivery(st *OutboundPECStatus, recipient, state string, rc *Receipt) {
	for i := range st.Deliveries {
		d := &st.Deliveries[i]
		if d.Recipient != recipient {
			continue
		}
		if state == DeliveryWarning && d.State != DeliveryWarning {
			return
		}
		d.State, d.At, d.ReceiptID = state, rc.At, rc.ID
		return
	}
	st.Deliveries = append(st.Deliveries, PECDelivery{Recipient: recipient, State: state, At: rc.At, ReceiptID: rc.ID})
}

func compute(st *OutboundPECStatus) string {
	targets := st.Certified
	if len(targets) == 0 {
		for _, d := range st.Deliveries {
			targets = append(targets, d.Recipient)
		}
	}
	if len(targets) == 0 {
		return StateAccepted
	}
	byRcpt := map[string]string{}
	for _, d := range st.Deliveries {
		byRcpt[d.Recipient] = d.State
	}
	delivered, failed := 0, 0
	for _, t := range targets {
		switch byRcpt[t] {
		case DeliveryDelivered:
			delivered++
		case DeliveryError:
			failed++
		}
	}
	switch {
	case delivered == len(targets):
		return StateDelivered
	case failed == len(targets):
		return StateFailed
	case delivered > 0:
		return StatePartiallyDelivered
	}
	return StateAccepted
}
