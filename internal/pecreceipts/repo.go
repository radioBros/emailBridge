// Package pecreceipts stores PEC receipts (S17 §5), links them to the outbound messages they are about and keeps
// the outbound message's PEC state. Sync writes through it inside its fenced transaction; the API reads it.
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
	"github.com/radiobros/emailbridge/internal/store"
)

// Collection is the S1 collection.
const Collection = "pecReceipts"

const outboundCollection = "outboundMessages"

// Evidence is the evidence-archive reference of a receipt (P9.2).
type Evidence struct {
	Key         string     `bson:"key" json:"key" doc:"Object key of the archived signed receipt in the evidence bucket" example:"evidence/org_01m5caqpk0kast63yc13wpy2d9/acc_01m5cvnpb031g57sqct6mzhq94/2026/10/rcp_01m5d20y80n4ps0apz3e6baqq3.eml"`
	LockedUntil *time.Time `bson:"lockedUntil,omitempty" json:"lockedUntil,omitempty" doc:"Object Lock retention end (WORM), when the bucket uses Object Lock" example:"2036-10-27T08:30:09Z"`
	TSA         bool       `bson:"tsa,omitempty" json:"tsa,omitempty" doc:"An RFC 3161 timestamp token is archived next to the receipt" example:"true"`
}

// Receipt is one stored PEC receipt (S17 §5).
type Receipt struct {
	ID                      string          `bson:"_id" json:"id" doc:"Receipt ID (rcp_…)" example:"rcp_01m5d20y80n4ps0apz3e6baqq3"`
	OrganizationID          string          `bson:"organizationId" json:"-"`
	AccountID               string          `bson:"accountId" json:"accountId" doc:"Account that received the receipt (acc_…)" example:"acc_01m5cvnpb031g57sqct6mzhq94"`
	MessageID               string          `bson:"messageId" json:"messageId" doc:"The receipt message in the index (msg_…)" example:"msg_01m5cnaee070z834j1neyr6a58"`
	OutboundID              *string         `bson:"outboundId" json:"outboundId" doc:"Outbound message the receipt is about, when EmailBridge sent the original; null otherwise" example:"out_01m5cqe6d02twj9xw9b9vyh5b4"`
	Type                    string          `bson:"type" json:"type" doc:"Receipt type (X-Ricevuta): accettazione, non-accettazione, presa-in-carico, avvenuta-consegna, errore-consegna, preavviso-errore-consegna or rilevazione-virus" example:"avvenuta-consegna"`
	ReceiptKind             string          `bson:"receiptKind,omitempty" json:"receiptKind,omitempty" doc:"completa, breve or sintetica" example:"completa"`
	Error                   string          `bson:"error,omitempty" json:"error,omitempty" doc:"daticert errore value (nessuno = none)" example:"nessuno"`
	ErrorDetail             string          `bson:"errorDetail,omitempty" json:"errorDetail,omitempty" doc:"daticert errore-esteso text, when the receipt reports an error" example:"5.1.1 - Utente non valido"`
	TransportID             string          `bson:"transportId" json:"transportId" doc:"PEC transport identifier (identificativo)" example:"opec210312.20261027093006.123456.789.1.53@pec.example.it"`
	OriginalMessageIDHeader string          `bson:"originalMessageIdHeader,omitempty" json:"originalMessageIdHeader,omitempty" doc:"Message-ID of the receipt message itself" example:"<opec210312.20261027093007.654321.111.1.53@pec.example.it>"`
	RefMessageIDHeader      string          `bson:"refMessageIdHeader,omitempty" json:"refMessageIdHeader,omitempty" doc:"X-Riferimento-Message-ID: the original message's Message-ID" example:"<01m5cqe6d02twj9xw9b9vyh5b4@mail.example.com>"`
	Sender                  string          `bson:"sender,omitempty" json:"sender,omitempty" doc:"Sender of the original message (daticert mittente)" example:"reception@pec.example.com"`
	Recipients              []pec.Recipient `bson:"recipients,omitempty" json:"recipients,omitempty" doc:"Addressees listed in daticert.xml"`
	DeliveredTo             string          `bson:"deliveredTo" json:"deliveredTo,omitempty" doc:"Recipient the receipt confirms (daticert consegna)" example:"studio.rossi@pec.example.it"`
	Provider                string          `bson:"provider,omitempty" json:"provider,omitempty" doc:"PEC provider that issued the receipt (daticert gestore-emittente)" example:"pec.example.it"`
	At                      time.Time       `bson:"at" json:"at" doc:"Date and time stated in the receipt" example:"2026-10-27T08:30:07Z"`
	Signature               pec.Signature   `bson:"signature" json:"signature" doc:"S/MIME check of the receipt"`
	ParseError              string          `bson:"parseError,omitempty" json:"parseError,omitempty" doc:"Why daticert.xml could not be parsed, when it could not" example:"daticert.xml: missing <identificativo>"`
	SHA256                  string          `bson:"sha256" json:"sha256" doc:"SHA-256 of the raw signed receipt (hex)" example:"9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"`
	Evidence                *Evidence       `bson:"evidence,omitempty" json:"evidence,omitempty" doc:"Evidence archive reference; omitted when pec.evidenceArchive was off when the receipt arrived"`
	CreatedAt               time.Time       `bson:"createdAt" json:"createdAt" doc:"When EmailBridge stored the receipt" example:"2026-10-27T08:30:09Z"`
}

// Repo is the receipts store.
type Repo struct{ DB *store.DB }

func (r *Repo) coll() *mongo.Collection { return r.DB.Collection(Collection) }

// EnsureIndexes creates the S17 §5 indexes; the unique key makes a receipt seen twice one row.
func EnsureIndexes(ctx context.Context, db *store.DB) error {
	return db.EnsureIndexes(ctx, Collection,
		mongo.IndexModel{Keys: bson.D{{Key: "organizationId", Value: 1}, {Key: "accountId", Value: 1}, {Key: "at", Value: -1}}},
		mongo.IndexModel{Keys: bson.D{{Key: "organizationId", Value: 1}, {Key: "outboundId", Value: 1}, {Key: "at", Value: 1}}},
		mongo.IndexModel{
			Keys: bson.D{{Key: "organizationId", Value: 1}, {Key: "accountId", Value: 1}, {Key: "transportId", Value: 1},
				{Key: "type", Value: 1}, {Key: "deliveredTo", Value: 1}},
			Options: options.Index().SetUnique(true).SetName("receipt_unique"),
		},
	)
}

// Migration registers the indexes.
func Migration(number int) store.Migration {
	return store.Migration{Number: number, Name: "pecReceipts indexes", Up: EnsureIndexes}
}

// Exists reports whether this receipt (organisation, account, transport ID, type, recipient) is stored already —
// a re-delivered or re-synced receipt must not emit pec.receipt twice.
func (r *Repo) Exists(ctx context.Context, org, account, transportID, typ, deliveredTo string) (bool, error) {
	n, err := r.coll().CountDocuments(ctx, bson.M{"organizationId": org, "accountId": account, "transportId": transportID,
		"type": typ, "deliveredTo": deliveredTo}, options.Count().SetLimit(1))
	if err != nil {
		return false, errs.Wrap(err, errs.CodeInternalError, "pecreceipts.exists")
	}
	return n > 0, nil
}

// LinkOutbound finds the outbound message a receipt is about: same organisation and account, Message-ID header
// equal to the receipt's reference (S17 §5.1). Empty when EmailBridge did not send it.
func (r *Repo) LinkOutbound(ctx context.Context, org, account, messageIDHeader string) (string, error) {
	id := strings.TrimSpace(messageIDHeader)
	if id == "" {
		return "", nil
	}
	var doc struct {
		ID string `bson:"_id"`
	}
	err := r.DB.Collection(outboundCollection).FindOne(ctx,
		bson.M{"organizationId": org, "accountId": account, "messageIdHeader": bson.M{"$in": idForms(id)}},
		options.FindOne().SetProjection(bson.M{"_id": 1})).Decode(&doc)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return "", nil
	}
	if err != nil {
		return "", errs.Wrap(err, errs.CodeInternalError, "pecreceipts.link")
	}
	return doc.ID, nil
}

// idForms returns the Message-ID with and without angle brackets: providers and callers differ.
func idForms(id string) []string {
	bare := strings.TrimSuffix(strings.TrimPrefix(id, "<"), ">")
	return []string{"<" + bare + ">", bare}
}

// Insert stores a receipt inside the caller's transaction and updates the linked outbound message's PEC state.
// It upserts on the unique key instead of inserting: inside a MongoDB transaction a duplicate-key error aborts
// the whole transaction, so an already stored receipt must be a no-op, not an error.
func (r *Repo) Insert(tx context.Context, rc *Receipt) error {
	res, err := r.coll().UpdateOne(tx, bson.M{"organizationId": rc.OrganizationID, "accountId": rc.AccountID,
		"transportId": rc.TransportID, "type": rc.Type, "deliveredTo": rc.DeliveredTo},
		bson.M{"$setOnInsert": rc}, options.UpdateOne().SetUpsert(true))
	if err != nil {
		return errs.Wrap(err, errs.CodeInternalError, "pecreceipts.insert")
	}
	if res.UpsertedCount == 0 || rc.OutboundID == nil {
		return nil
	}
	return r.applyToOutbound(tx, rc)
}

// Get returns one receipt of the organisation.
func (r *Repo) Get(ctx context.Context, org, id string) (*Receipt, error) {
	var rc Receipt
	err := r.coll().FindOne(ctx, bson.M{"_id": id, "organizationId": org}).Decode(&rc)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, errs.New(errs.CodeNotFound, "The PEC receipt does not exist.")
	}
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "pecreceipts.get")
	}
	return &rc, nil
}
