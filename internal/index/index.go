// Package index is the mailbox and message index of the sync engine (S1 §3): folder baselines
// (uidValidity, uidNext, highestModseq) and minimal per-message entries (uid, flags, modseq, Message-ID,
// emailId) — never subject, addresses or bodies (S11). Every write runs in a transaction fenced by the
// account's lease epoch (S4 §5): a stale owner's transaction fails with ErrFenced.
package index

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/radiobros/emailbridge/internal/store"
)

// Collections.
const (
	Mailboxes = "mailboxes"
	Messages  = "messages"
	// ArchivedMessages keeps the archive reference of messages gone from the mailbox (inbound.archiveRaw), so
	// the archived source stays downloadable and retention still finds the object.
	ArchivedMessages = "archivedMessages"
	Accounts         = "accounts"
)

// ErrFenced means this node no longer owns the account (its lease epoch is stale): stop the account.
var ErrFenced = errors.New("index: account owned by another node (stale lease epoch)")

// Mailbox is a folder's index entry and baseline.
type Mailbox struct {
	ID             string     `bson:"_id"`
	OrganizationID string     `bson:"organizationId"`
	AccountID      string     `bson:"accountId"`
	Path           string     `bson:"path"`
	Delimiter      string     `bson:"delimiter,omitempty"`
	SpecialUse     string     `bson:"specialUse,omitempty"`
	UIDValidity    uint32     `bson:"uidValidity"`
	UIDNext        uint32     `bson:"uidNext"`
	HighestModSeq  uint64     `bson:"highestModseq"`
	Exists         uint32     `bson:"exists"`
	SyncMode       string     `bson:"syncMode,omitempty"` // idle | poll
	LastSyncAt     *time.Time `bson:"lastSyncAt,omitempty"`
	Status         string     `bson:"status"` // active | deleted
}

// Message is a message's index entry (minimal by design).
type Message struct {
	ID              string    `bson:"_id"`
	OrganizationID  string    `bson:"organizationId"`
	AccountID       string    `bson:"accountId"`
	MailboxID       string    `bson:"mailboxId"`
	UIDValidity     uint32    `bson:"uidValidity"`
	UID             uint32    `bson:"uid"`
	ModSeq          uint64    `bson:"modseq"`
	Flags           []string  `bson:"flags"`
	MessageIDHeader string    `bson:"messageIdHeader,omitempty"`
	EmailID         string    `bson:"emailId"`
	ThreadID        string    `bson:"threadId,omitempty"`
	InternalDate    time.Time `bson:"internalDate"`
	Size            int64     `bson:"size"`
	ArchiveRef      string    `bson:"archiveRef,omitempty"` // S3 key when inbound.archiveRaw stored the raw source
	IndexedAt       time.Time `bson:"indexedAt"`
	// Summary from the IMAP envelope (P4.4: the message browser lists without an IMAP round trip).
	Subject string     `bson:"subject,omitempty"`
	From    *Address   `bson:"from,omitempty"`
	To      []Address  `bson:"to,omitempty"` // at most SummaryRecipients
	Date    *time.Time `bson:"date,omitempty"`
	// ProviderID is the provider's own message ID for API backends (Gmail message ID, Graph message ID), P6:
	// per-message provider state lives on the index rows, never in one per-account document (16 MiB limit).
	ProviderID string `bson:"providerId,omitempty"`
}

// Address is an indexed address (display name + addr-spec).
type Address struct {
	Name    string `bson:"name,omitempty"`
	Address string `bson:"address"`
}

// SummaryRecipients caps the To list stored in the index; SummarySubject caps the subject (bytes).
const (
	SummaryRecipients = 10
	SummarySubject    = 256
)

// EmailID is the provider-independent message identity (S1): SHA-256 of Message-ID, internal date and
// size, used when the server offers no EMAILID/X-GM-MSGID.
func EmailID(messageID string, internalDate time.Time, size int64) string {
	h := sha256.Sum256([]byte(messageID + "|" + internalDate.UTC().Format(time.RFC3339) + "|" + strconv.FormatInt(size, 10)))
	return hex.EncodeToString(h[:16])
}

// Index is the repository.
type Index struct {
	DB *store.DB
}

// Fenced runs fn in a transaction that first checks (and touches) accounts.owner.epoch == epoch; when the
// account has another owner the transaction aborts with ErrFenced.
func (ix *Index) Fenced(ctx context.Context, accountID string, epoch uint64, fn func(ctx context.Context) error) error {
	return ix.DB.WithTx(ctx, func(tx context.Context) error {
		res, err := ix.DB.Collection(Accounts).UpdateOne(tx,
			bson.M{"_id": accountID, "owner.epoch": int64(epoch)}, //nolint:gosec // G115: KV revisions fit in int64
			bson.M{"$set": bson.M{"lastSyncAt": time.Now().UTC()}})
		if err != nil {
			return err
		}
		if res.MatchedCount == 0 {
			return ErrFenced
		}
		return fn(tx)
	})
}

// Mailboxes returns the account's active folders keyed by path.
func (ix *Index) Mailboxes(ctx context.Context, accountID string) (map[string]*Mailbox, error) {
	cur, err := ix.DB.Collection(Mailboxes).Find(ctx, bson.M{"accountId": accountID, "status": "active"})
	if err != nil {
		return nil, err
	}
	var list []*Mailbox
	if err := cur.All(ctx, &list); err != nil {
		return nil, err
	}
	out := make(map[string]*Mailbox, len(list))
	for _, m := range list {
		out[m.Path] = m
	}
	return out, nil
}

// SaveMailbox upserts a folder (inside a fenced transaction).
func (ix *Index) SaveMailbox(ctx context.Context, m *Mailbox) error {
	_, err := ix.DB.Collection(Mailboxes).ReplaceOne(ctx, bson.M{"_id": m.ID}, m, options.Replace().SetUpsert(true))
	return err
}

// DeleteMailbox marks a folder deleted and removes its message entries.
func (ix *Index) DeleteMailbox(ctx context.Context, m *Mailbox) (int64, error) {
	if _, err := ix.DB.Collection(Mailboxes).UpdateOne(ctx, bson.M{"_id": m.ID}, bson.M{"$set": bson.M{"status": "deleted"}}); err != nil {
		return 0, err
	}
	return ix.ClearMailbox(ctx, m.ID)
}

// ClearMailbox removes every message entry of a folder (UIDVALIDITY reset, deletion).
func (ix *Index) ClearMailbox(ctx context.Context, mailboxID string) (int64, error) {
	res, err := ix.DB.Collection(Messages).DeleteMany(ctx, bson.M{"mailboxId": mailboxID})
	if err != nil {
		return 0, err
	}
	return res.DeletedCount, nil
}

// MessageStates returns uid → (id, flags, modseq, messageIdHeader) for a folder: the diff baseline. Index
// entries are small, so a folder of 1M messages costs about 100 MB once per full pass; condstore mode only
// loads the changed UIDs (MessagesByUID).
func (ix *Index) MessageStates(ctx context.Context, mailboxID string) (map[uint32]*Message, error) {
	cur, err := ix.DB.Collection(Messages).Find(ctx, bson.M{"mailboxId": mailboxID},
		options.Find().SetProjection(bson.M{"uid": 1, "flags": 1, "modseq": 1, "messageIdHeader": 1, "emailId": 1}))
	if err != nil {
		return nil, err
	}
	defer func() { _ = cur.Close(ctx) }()
	out := map[uint32]*Message{}
	for cur.Next(ctx) {
		var m Message
		if err := cur.Decode(&m); err != nil {
			return nil, err
		}
		out[m.UID] = &m
	}
	return out, cur.Err()
}

// MessagesByUID loads the entries of the given UIDs in a folder.
func (ix *Index) MessagesByUID(ctx context.Context, mailboxID string, uids []uint32) (map[uint32]*Message, error) {
	if len(uids) == 0 {
		return map[uint32]*Message{}, nil
	}
	in := make([]int64, len(uids))
	for i, u := range uids {
		in[i] = int64(u)
	}
	cur, err := ix.DB.Collection(Messages).Find(ctx, bson.M{"mailboxId": mailboxID, "uid": bson.M{"$in": in}})
	if err != nil {
		return nil, err
	}
	var list []*Message
	if err := cur.All(ctx, &list); err != nil {
		return nil, err
	}
	out := make(map[uint32]*Message, len(list))
	for _, m := range list {
		out[m.UID] = m
	}
	return out, nil
}

// UIDRange is an inclusive UID range.
type UIDRange struct{ Start, Stop uint32 }

// MessagesInRanges returns the indexed messages of a mailbox whose UID falls in any of the ranges (QRESYNC
// VANISHED reports, which may cover far more UIDs than exist). The query is bounded by the index, never by
// the size of the ranges; ranges are queried in chunks.
func (ix *Index) MessagesInRanges(ctx context.Context, mailboxID string, ranges []UIDRange) (map[uint32]*Message, error) {
	out := map[uint32]*Message{}
	const chunk = 500
	for i := 0; i < len(ranges); i += chunk {
		end := min(i+chunk, len(ranges))
		or := make([]bson.M, 0, end-i)
		for _, r := range ranges[i:end] {
			lo, hi := r.Start, r.Stop
			if lo > hi {
				lo, hi = hi, lo
			}
			or = append(or, bson.M{"uid": bson.M{"$gte": int64(lo), "$lte": int64(hi)}})
		}
		cur, err := ix.DB.Collection(Messages).Find(ctx, bson.M{"mailboxId": mailboxID, "$or": or})
		if err != nil {
			return nil, err
		}
		var list []*Message
		if err := cur.All(ctx, &list); err != nil {
			return nil, err
		}
		for _, m := range list {
			out[m.UID] = m
		}
	}
	return out, nil
}

// InsertMessages adds index entries (inside a fenced transaction); duplicates (re-run after a crash) are
// ignored.
func (ix *Index) InsertMessages(ctx context.Context, msgs []*Message) error {
	if len(msgs) == 0 {
		return nil
	}
	docs := make([]any, len(msgs))
	for i, m := range msgs {
		docs[i] = m
	}
	_, err := ix.DB.Collection(Messages).InsertMany(ctx, docs, options.InsertMany().SetOrdered(false))
	var bwe mongo.BulkWriteException
	if errors.As(err, &bwe) {
		for _, we := range bwe.WriteErrors {
			if we.Code != 11000 {
				return err
			}
		}
		return nil
	}
	return err
}

// UpdateFlags sets flags/modseq of entries.
func (ix *Index) UpdateFlags(ctx context.Context, id string, flags []string, modseq uint64) error {
	_, err := ix.DB.Collection(Messages).UpdateOne(ctx, bson.M{"_id": id}, bson.M{"$set": bson.M{"flags": flags, "modseq": int64(modseq)}}) //nolint:gosec // G115
	return err
}

// DeleteMessages removes entries by ID.
func (ix *Index) DeleteMessages(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	// Archived messages leave a tombstone first (same transaction when ctx carries one): the message ID keeps
	// resolving to its archived source, and archive retention keeps finding the object.
	cur, err := ix.DB.Collection(Messages).Find(ctx, bson.M{"_id": bson.M{"$in": ids}, "archiveRef": bson.M{"$exists": true, "$gt": ""}})
	if err != nil {
		return err
	}
	var archived []*Message
	if err := cur.All(ctx, &archived); err != nil {
		return err
	}
	if len(archived) > 0 {
		// Upserts, not inserts: a duplicate-key error would abort the surrounding transaction.
		now := time.Now().UTC()
		models := make([]mongo.WriteModel, 0, len(archived))
		for _, m := range archived {
			models = append(models, mongo.NewReplaceOneModel().SetFilter(bson.M{"_id": m.ID}).SetUpsert(true).SetReplacement(bson.M{
				"organizationId": m.OrganizationID, "accountId": m.AccountID, "mailboxId": m.MailboxID,
				"uid": int64(m.UID), "messageIdHeader": m.MessageIDHeader, "internalDate": m.InternalDate,
				"size": m.Size, "archiveRef": m.ArchiveRef, "deletedAt": now}))
		}
		if _, err := ix.DB.Collection(ArchivedMessages).BulkWrite(ctx, models); err != nil {
			return err
		}
	}
	_, err = ix.DB.Collection(Messages).DeleteMany(ctx, bson.M{"_id": bson.M{"$in": ids}})
	return err
}

// SeenElsewhere reports whether the account already indexes emailId in another folder (a move, S5 §4.1).
func (ix *Index) SeenElsewhere(ctx context.Context, accountID, mailboxID, emailID string) (bool, error) {
	err := ix.DB.Collection(Messages).FindOne(ctx, bson.M{"accountId": accountID, "emailId": emailID, "mailboxId": bson.M{"$ne": mailboxID}},
		options.FindOne().SetProjection(bson.M{"_id": 1})).Err()
	if errors.Is(err, mongo.ErrNoDocuments) {
		return false, nil
	}
	return err == nil, err
}

// MessagesByProviderIDs loads index rows for the given provider message IDs (Gmail/Graph), batched $in.
func (ix *Index) MessagesByProviderIDs(ctx context.Context, accountID string, providerIDs []string) ([]*Message, error) {
	if len(providerIDs) == 0 {
		return nil, nil
	}
	const chunk = 500
	out := make([]*Message, 0, len(providerIDs))
	for i := 0; i < len(providerIDs); i += chunk {
		end := min(i+chunk, len(providerIDs))
		cur, err := ix.DB.Collection(Messages).Find(ctx, bson.M{
			"accountId": accountID, "providerId": bson.M{"$in": providerIDs[i:end]},
		})
		if err != nil {
			return nil, err
		}
		var list []*Message
		if err := cur.All(ctx, &list); err != nil {
			return nil, err
		}
		out = append(out, list...)
	}
	return out, nil
}

// Migration creates the index indexes (S1 §3).
func Migration(number int) store.Migration {
	return store.Migration{Number: number, Name: "mailbox and message index", Up: func(ctx context.Context, db *store.DB) error {
		if err := db.EnsureIndexes(ctx, Mailboxes,
			mongo.IndexModel{Keys: bson.D{{Key: "accountId", Value: 1}, {Key: "path", Value: 1}}, Options: options.Index().SetUnique(true).SetPartialFilterExpression(bson.M{"status": "active"})},
			mongo.IndexModel{Keys: bson.D{{Key: "accountId", Value: 1}, {Key: "_id", Value: 1}}},
		); err != nil {
			return fmt.Errorf("mailboxes: %w", err)
		}
		return db.EnsureIndexes(ctx, Messages,
			mongo.IndexModel{Keys: bson.D{{Key: "mailboxId", Value: 1}, {Key: "uid", Value: 1}}, Options: options.Index().SetUnique(true)},
			mongo.IndexModel{Keys: bson.D{{Key: "accountId", Value: 1}, {Key: "emailId", Value: 1}}},
			mongo.IndexModel{Keys: bson.D{{Key: "accountId", Value: 1}, {Key: "_id", Value: 1}}},
			mongo.IndexModel{Keys: bson.D{{Key: "accountId", Value: 1}, {Key: "messageIdHeader", Value: 1}}},
		)
	}}
}

// ThreadIndexMigration indexes messages by conversation (P4.4: the thread view lists a thread across folders).
func ThreadIndexMigration(number int) store.Migration {
	return store.Migration{Number: number, Name: "message thread index", Up: func(ctx context.Context, db *store.DB) error {
		return db.EnsureIndexes(ctx, Messages, mongo.IndexModel{
			Keys:    bson.D{{Key: "accountId", Value: 1}, {Key: "threadId", Value: 1}, {Key: "internalDate", Value: -1}},
			Options: options.Index().SetName("account_thread_date"),
		})
	}}
}

// ProviderIDMigration indexes messages by provider message ID (P6 Gmail / Graph backends).
func ProviderIDMigration(number int) store.Migration {
	return store.Migration{Number: number, Name: "message provider id index", Up: func(ctx context.Context, db *store.DB) error {
		return db.EnsureIndexes(ctx, Messages, mongo.IndexModel{
			Keys:    bson.D{{Key: "accountId", Value: 1}, {Key: "providerId", Value: 1}},
			Options: options.Index().SetName("account_provider_id").SetPartialFilterExpression(bson.M{"providerId": bson.M{"$exists": true}}),
		})
	}}
}
