package exports

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/radiobros/emailbridge/internal/attachments"
	"github.com/radiobros/emailbridge/internal/audit"
	"github.com/radiobros/emailbridge/internal/crypto"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/index"
	"github.com/radiobros/emailbridge/internal/jobs"
	"github.com/radiobros/emailbridge/internal/objstore"
	"github.com/radiobros/emailbridge/internal/store"
)

type jobPayload struct {
	ExportID  string `json:"exportId"`
	AccountID string `json:"accountId"`
}

// PutCounter counts S3 Put calls (tests).
type PutCounter interface {
	IncPut()
}

// RegisterHandlers wires the account.export job kind.
func (s *Service) RegisterHandlers() {
	if s.Jobs == nil {
		return
	}
	s.Jobs.Handle(JobKind, s.handleJob)
}

func (s *Service) handleJob(ctx context.Context, j *jobs.Job, r jobs.Reporter) (any, error) {
	var p jobPayload
	if len(j.Payload) > 0 {
		if err := json.Unmarshal(j.Payload, &p); err != nil {
			return nil, errs.New(errs.CodeValidationFailed, "Invalid export job payload.")
		}
	}
	if p.ExportID == "" || p.AccountID == "" {
		return nil, errs.New(errs.CodeValidationFailed, "exportId and accountId are required.")
	}
	org := j.OrganizationID
	ctx = store.WithOrganization(ctx, org)
	e, err := s.Repo.getByID(ctx, org, p.ExportID)
	if err != nil {
		return nil, err
	}
	switch e.State {
	case StateSucceeded, StateCancelled, StateExpired, StateFailed:
		return viewOf(e), nil
	}

	now := s.now()
	if e.StartedAt == nil {
		e.StartedAt = &now
	}
	e.State = StateRunning
	e.JobID = j.ID
	if err := s.Repo.replace(ctx, e); err != nil {
		return nil, err
	}

	total, err := s.countMessages(ctx, e)
	if err != nil {
		return nil, s.failJob(ctx, e, err)
	}
	prog := Progress{Done: 0, Total: total, NextPart: 1}
	if e.Progress != nil {
		if e.Progress.Checkpoint != nil {
			prog.Checkpoint = e.Progress.Checkpoint
			prog.Done = e.Progress.Done
		}
		if e.Progress.NextPart > 0 {
			prog.NextPart = e.Progress.NextPart
		}
		prog.PartCount = e.Progress.PartCount
	}
	parts := append([]string(nil), e.Parts...)
	_ = s.Repo.updateProgress(ctx, e.ID, prog, StateRunning)
	r.Progress(ctx, prog.Done, prog.Total)

	prefix, err := objstore.ExportPrefix(org, e.ID)
	if err != nil {
		return nil, s.failJob(ctx, e, err)
	}

	fileKey, err := s.ensureFileKey(ctx, e)
	if err != nil {
		return nil, s.failJob(ctx, e, err)
	}

	// Fresh start: clear leftover parts under the prefix.
	if prog.Checkpoint == nil && len(parts) == 0 && s.Objects != nil {
		_, _ = s.Objects.DeletePrefix(ctx, prefix)
	}

	cur, err := s.messageCursor(ctx, e, prog.Checkpoint)
	if err != nil {
		return nil, s.failJob(ctx, e, err)
	}
	defer func() { _ = cur.Close(ctx) }()

	batchN := s.batch()
	var batch []index.Message
	var byteSize int64
	for _, k := range parts {
		if s.Objects != nil {
			if info, err := s.Objects.Stat(ctx, k); err == nil {
				byteSize += info.Size
			}
		}
	}

	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		var ndjson bytes.Buffer
		for _, m := range batch {
			if r.Cancelled() {
				return s.cancelJob(ctx, e, prefix)
			}
			line, err := s.buildLine(ctx, e, m)
			if err != nil {
				return err
			}
			ndjson.Write(mustJSON(line))
			prog.Done++
			prog.Checkpoint = &Checkpoint{MailboxID: m.MailboxID, UID: m.UID}
		}
		batch = batch[:0]

		var gzBuf bytes.Buffer
		zw := gzip.NewWriter(&gzBuf)
		if _, err := zw.Write(ndjson.Bytes()); err != nil {
			return err
		}
		if err := zw.Close(); err != nil {
			return err
		}
		encBlob, err := sealAESGCM(fileKey, gzBuf.Bytes())
		if err != nil {
			return err
		}
		partKey := fmt.Sprintf("%spart-%06d.ndjson.gz", prefix, prog.NextPart)
		if s.Objects != nil {
			if err := s.Objects.Put(ctx, partKey, bytes.NewReader(encBlob), int64(len(encBlob)), objstore.ObjectMeta{
				Organization: org, Account: e.AccountID, ContentType: "application/octet-stream",
			}); err != nil {
				return errs.Wrap(err, errs.CodeInternalError, "exports.part")
			}
			if s.PutHook != nil {
				s.PutHook.IncPut()
			}
		}
		parts = append(parts, partKey)
		byteSize += int64(len(encBlob))
		prog.NextPart++
		prog.PartCount = len(parts)
		if err := s.Repo.appendPart(ctx, e.ID, partKey, prog); err != nil {
			return err
		}
		r.Progress(ctx, prog.Done, prog.Total)
		return nil
	}

	for cur.Next(ctx) {
		if r.Cancelled() {
			return nil, s.cancelJob(ctx, e, prefix)
		}
		var m index.Message
		if err := cur.Decode(&m); err != nil {
			return nil, s.failJob(ctx, e, errs.Wrap(err, errs.CodeInternalError, "exports.decode"))
		}
		batch = append(batch, m)
		if len(batch) >= batchN {
			if err := flush(); err != nil {
				if errors.Is(err, errCancelled) {
					return viewOf(e), errCancelled
				}
				return nil, s.failJob(ctx, e, err)
			}
		}
	}
	if err := cur.Err(); err != nil {
		return nil, s.failJob(ctx, e, errs.Wrap(err, errs.CodeInternalError, "exports.cursor"))
	}
	if err := flush(); err != nil {
		if errors.Is(err, errCancelled) {
			return viewOf(e), errCancelled
		}
		return nil, s.failJob(ctx, e, err)
	}
	if r.Cancelled() {
		return nil, s.cancelJob(ctx, e, prefix)
	}

	finished := s.now()
	expire := finished.Add(defaultRetention)
	if err := s.Repo.setSucceeded(ctx, e.ID, parts, prog.Done, byteSize, prog, finished, expire); err != nil {
		return nil, err
	}
	e.State = StateSucceeded
	e.Parts = parts
	e.MessageCount = prog.Done
	e.ByteSize = byteSize
	e.FinishedAt = &finished
	e.ExpireAt = &expire
	e.Progress = &prog
	_ = s.Repo.DB.WithTx(ctx, func(tx context.Context) error {
		return s.emitCompleted(tx, e)
	})
	s.recordAudit(ctx, "export.completed", e.ID, audit.Changes{
		"messageCount": {After: prog.Done},
		"byteSize":     {After: e.ByteSize},
		"partCount":    {After: len(parts)},
	})
	return viewOf(e), nil
}

func (s *Service) ensureFileKey(ctx context.Context, e *Export) ([]byte, error) {
	if s.Cipher == nil {
		return nil, errs.New(errs.CodeInternalError, "Export cipher is not configured.")
	}
	if !e.Encryption.IsZero() {
		return s.Cipher.Decrypt(ctx, e.Encryption, crypto.AAD{
			Collection: Collection, Field: "fileKey", DocID: e.ID,
		})
	}
	fileKey := make([]byte, 32)
	if _, err := rand.Read(fileKey); err != nil {
		return nil, err
	}
	wrapped, err := s.Cipher.Encrypt(ctx, e.OrganizationID, fileKey, crypto.AAD{
		Collection: Collection, Field: "fileKey", DocID: e.ID,
	})
	if err != nil {
		return nil, err
	}
	if err := s.Repo.setEncryption(ctx, e.ID, wrapped); err != nil {
		return nil, err
	}
	e.Encryption = wrapped
	return fileKey, nil
}

var errCancelled = errors.New("exports: cancelled")

func (s *Service) cancelJob(ctx context.Context, e *Export, prefix string) error {
	now := s.now()
	_ = s.Repo.markCancelled(ctx, e.ID, now)
	if s.Objects != nil && prefix != "" {
		_, _ = s.Objects.DeletePrefix(ctx, prefix)
	}
	e.State = StateCancelled
	e.FinishedAt = &now
	s.recordAudit(ctx, "export.cancelled", e.ID, nil)
	return errCancelled
}

func (s *Service) failJob(ctx context.Context, e *Export, cause error) error {
	p := errs.ProblemFrom(cause, "", "")
	now := s.now()
	_ = s.Repo.markFailed(ctx, e.ID, p, now)
	e.State = StateFailed
	e.Error = p
	e.FinishedAt = &now
	code, msg := "", ""
	if p != nil {
		code, msg = string(p.Code), p.Detail
	}
	_ = s.Repo.DB.WithTx(ctx, func(tx context.Context) error {
		return s.emitFailed(tx, e, code, msg)
	})
	s.recordAudit(ctx, "export.failed", e.ID, audit.Changes{
		"code": {After: code},
	})
	return cause
}

func viewOf(e *Export) map[string]any {
	return map[string]any{
		"id": e.ID, "state": e.State, "messageCount": e.MessageCount, "byteSize": e.ByteSize,
	}
}

func (s *Service) messageFilter(e *Export, cp *Checkpoint) bson.M {
	f := bson.M{"accountId": e.AccountID, "organizationId": e.OrganizationID}
	if len(e.Filters.FolderIDs) > 0 {
		f["mailboxId"] = bson.M{"$in": e.Filters.FolderIDs}
	}
	if e.Filters.Since != nil || e.Filters.Before != nil {
		dr := bson.M{}
		if e.Filters.Since != nil {
			dr["$gte"] = *e.Filters.Since
		}
		if e.Filters.Before != nil {
			dr["$lt"] = *e.Filters.Before
		}
		f["internalDate"] = dr
	}
	if cp != nil {
		f["$or"] = []bson.M{
			{"mailboxId": bson.M{"$gt": cp.MailboxID}},
			{"mailboxId": cp.MailboxID, "uid": bson.M{"$gt": cp.UID}},
		}
	}
	return f
}

func (s *Service) countMessages(ctx context.Context, e *Export) (int64, error) {
	n, err := s.Repo.DB.Collection(index.Messages).CountDocuments(ctx, s.messageFilter(e, nil))
	if err != nil {
		return 0, errs.Wrap(err, errs.CodeInternalError, "exports.count")
	}
	return n, nil
}

func (s *Service) messageCursor(ctx context.Context, e *Export, cp *Checkpoint) (*mongo.Cursor, error) {
	opts := options.Find().SetSort(bson.D{{Key: "mailboxId", Value: 1}, {Key: "uid", Value: 1}})
	cur, err := s.Repo.DB.Collection(index.Messages).Find(ctx, s.messageFilter(e, cp), opts)
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "exports.find")
	}
	return cur, nil
}

func (s *Service) buildLine(ctx context.Context, e *Export, m index.Message) (Line, error) {
	line := Line{
		ID: m.ID, MailboxID: m.MailboxID, MailboxPath: s.MailboxPath(ctx, m.MailboxID),
		UID: m.UID, UIDValidity: m.UIDValidity, Flags: m.Flags,
		MessageIDHeader: m.MessageIDHeader, EmailID: m.EmailID, ThreadID: m.ThreadID,
		InternalDate: m.InternalDate, Size: m.Size,
	}
	inc := e.Filters.Include
	if inc.Raw && m.ArchiveRef != "" && s.Objects != nil {
		if rc, _, err := s.Objects.Get(ctx, m.ArchiveRef); err == nil {
			raw, _ := io.ReadAll(rc)
			_ = rc.Close()
			line.Raw = encodeB64(raw)
		}
	}
	if inc.Attachments {
		line.Attachments = s.loadAttachments(ctx, e, m.ID, inc.Attachments)
	}
	return line, nil
}

func (s *Service) loadAttachments(ctx context.Context, e *Export, messageID string, withData bool) []LineAttachment {
	cur, err := s.Repo.DB.Collection(attachments.Collection).Find(ctx, bson.M{
		"organizationId": e.OrganizationID, "accountId": e.AccountID, "messageId": messageID,
	})
	if err != nil {
		return nil
	}
	defer func() { _ = cur.Close(ctx) }()
	var out []LineAttachment
	for cur.Next(ctx) {
		var d attachments.Doc
		if err := cur.Decode(&d); err != nil {
			continue
		}
		la := LineAttachment{ID: d.ID, Filename: d.Filename, ContentType: d.ContentType, Size: d.Size}
		if withData && d.ObjectKey != "" && s.Objects != nil {
			if rc, _, err := s.Objects.Get(ctx, d.ObjectKey); err == nil {
				b, _ := io.ReadAll(rc)
				_ = rc.Close()
				la.Data = encodeB64(b)
			}
		}
		out = append(out, la)
	}
	return out
}
