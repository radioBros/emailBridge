package exports

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/radiobros/emailbridge/internal/audit"
	"github.com/radiobros/emailbridge/internal/crypto"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/events"
	eventtypes "github.com/radiobros/emailbridge/internal/events/types"
	"github.com/radiobros/emailbridge/internal/features"
	"github.com/radiobros/emailbridge/internal/index"
	"github.com/radiobros/emailbridge/internal/jobs"
	"github.com/radiobros/emailbridge/internal/objstore"
	"github.com/radiobros/emailbridge/internal/settings"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

// SettingsAPI reads organisation switches.
type SettingsAPI interface {
	Effective(ctx context.Context, orgID string) (settings.SettingsBody, error)
}

// Service creates, runs and serves exports.
type Service struct {
	Repo      *Repo
	Jobs      *jobs.Service
	Objects   *objstore.Store
	Cipher    *crypto.Cipher
	Events    *events.Writer
	Audit     *audit.Writer
	Settings  SettingsAPI
	Registry  *features.Registry
	Env       string
	SignKey   []byte
	PublicURL string
	SignedTTL time.Duration
	BatchSize int
	Log       *slog.Logger
	Now       func() time.Time

	// SkipFeatureGate disables the export.enabled check (unit tests).
	SkipFeatureGate bool
	// PutHook counts S3 puts in tests (optional).
	PutHook PutCounter
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s *Service) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

func (s *Service) batch() int {
	if s.BatchSize > 0 {
		return s.BatchSize
	}
	return defaultBatch
}

func (s *Service) registry() *features.Registry {
	if s.Registry != nil {
		return s.Registry
	}
	return features.DefaultRegistry()
}

func (s *Service) requireFeature(ctx context.Context) error {
	if s.SkipFeatureGate {
		return nil
	}
	c, _ := tenancy.CallerFrom(ctx)
	org := c.OrganizationID
	sw, ok := s.registry().Get(SwitchEnabled)
	if !ok {
		return errs.New(errs.CodeFeatureDisabled, "Bulk export is disabled.")
	}
	vals := features.Values{}
	if s.Settings != nil && org != "" {
		body, err := s.Settings.Effective(ctx, org)
		if err == nil && body.Switches != nil {
			if sv, ok := body.Switches[SwitchEnabled]; ok {
				if b, ok := sv.Value.(bool); ok {
					vals.Organization = &features.Setting{Value: features.BoolValue(b), Locked: sv.Locked}
				}
			}
		}
	}
	env := s.Env
	if env == "" {
		env = "production"
	}
	v, _ := features.Resolve(sw, env, vals)
	if v.Bool != nil && !*v.Bool {
		return errs.New(errs.CodeFeatureDisabled, "export.enabled is disabled for this organisation.")
	}
	return nil
}

func normalizeInclude(in Include) (Include, error) {
	if in.MetadataOnly && (in.Raw || in.Attachments) {
		return Include{}, errs.New(errs.CodeValidationFailed, "metadataOnly cannot be combined with raw or attachments.")
	}
	if !in.Raw && !in.Attachments && !in.MetadataOnly {
		in.MetadataOnly = true
	}
	return in, nil
}

// Create queues an export job.
func (s *Service) Create(ctx context.Context, accountID string, in CreateInput) (*Accepted, error) {
	if err := tenancy.RequireAccount(ctx, tenancy.PermAccountsOperate, accountID); err != nil {
		return nil, err
	}
	if err := s.requireFeature(ctx); err != nil {
		return nil, err
	}
	if !store.IsID(store.PrefixAccount, accountID) {
		return nil, errs.New(errs.CodeNotFound, "The account does not exist.")
	}
	inc, err := normalizeInclude(in.Include)
	if err != nil {
		return nil, err
	}
	if in.Since != nil && in.Before != nil && !in.Since.Before(*in.Before) {
		return nil, errs.New(errs.CodeValidationFailed, "since must be before before.")
	}
	for _, id := range in.FolderIDs {
		if !store.IsID(store.PrefixMailbox, id) {
			return nil, errs.New(errs.CodeValidationFailed, "folderIds must be mailbox IDs.").WithSafe("folderId", id)
		}
	}
	n, err := s.Repo.countActive(ctx, accountID)
	if err != nil {
		return nil, err
	}
	if n >= maxConcurrent {
		return nil, errs.New(errs.CodeConflict, "This account already has the maximum number of active exports.")
	}
	c, _ := tenancy.CallerFrom(ctx)
	org := c.OrganizationID
	now := s.now()
	e := &Export{
		ID:             store.NewID(store.PrefixExport),
		OrganizationID: org,
		AccountID:      accountID,
		State:          StateQueued,
		Filters: Filters{
			FolderIDs: append([]string(nil), in.FolderIDs...),
			Since:     in.Since, Before: in.Before, Include: inc,
		},
		Progress:  &Progress{},
		CreatedBy: c.UserID,
		CreatedAt: now,
	}
	if c.Kind == tenancy.CallerAPIKey {
		e.CreatedBy = c.APIKeyID
	}
	if err := s.Repo.insert(ctx, e); err != nil {
		return nil, err
	}
	var job any
	if s.Jobs != nil {
		j, err := s.Jobs.Create(ctx, JobKind, map[string]string{
			"exportId": e.ID, "accountId": accountID,
		})
		if err != nil {
			return nil, err
		}
		e.JobID = j.ID
		_ = s.Repo.replace(ctx, e)
		job = j
	}
	s.recordAudit(ctx, "export.created", e.ID, audit.Changes{
		"accountId": {After: accountID},
	})
	return &Accepted{Export: *e, Job: job}, nil
}

// Get returns one export.
func (s *Service) Get(ctx context.Context, accountID, id string) (*Export, error) {
	if err := tenancy.RequireReadAccount(ctx, "exports", accountID); err != nil {
		return nil, err
	}
	if !store.IsID(store.PrefixExport, id) {
		return nil, errs.New(errs.CodeExportNotFound, "The export does not exist.")
	}
	return s.Repo.get(ctx, accountID, id)
}

// List returns a page of exports for the account.
func (s *Service) List(ctx context.Context, accountID string, f ListFilter) (*List, error) {
	if err := tenancy.RequireReadAccount(ctx, "exports", accountID); err != nil {
		return nil, err
	}
	// A foreign account answers "not found", not an empty list (tenant isolation, final pass).
	if s.Repo != nil && s.Repo.DB != nil {
		own, err := store.Scoped(ctx, bson.M{"_id": accountID})
		if err != nil {
			return nil, err
		}
		n, err := s.Repo.DB.Collection("accounts").CountDocuments(ctx, own, options.Count().SetLimit(1))
		if err != nil {
			return nil, errs.Wrap(err, errs.CodeInternalError, "exports.account_owner")
		}
		if n == 0 {
			return nil, errs.New(errs.CodeNotFound, "The account does not exist.")
		}
	}
	base, err := store.Scoped(ctx, bson.M{"accountId": accountID})
	if err != nil {
		return nil, err
	}
	sortField, sortDir, err := parseSort(f.Sort)
	if err != nil {
		return nil, err
	}
	limit := f.Limit
	if limit <= 0 {
		limit = defaultListLimit
	}
	if limit > maxListLimit {
		limit = maxListLimit
	}
	findOpts := options.Find().SetLimit(int64(limit + 1))
	sortDoc := bson.D{{Key: sortField, Value: sortDir}}
	if sortField != "_id" {
		sortDoc = append(sortDoc, bson.E{Key: "_id", Value: sortDir})
	}
	findOpts.SetSort(sortDoc)
	filter := base
	if f.Page > 0 {
		findOpts.SetSkip(int64(f.Page-1) * int64(limit))
	} else if f.Cursor != "" {
		cur, err := decodeCursor(f.Cursor)
		if err != nil {
			return nil, errs.New(errs.CodeValidationFailed, "The cursor is invalid.")
		}
		keyFilter, err := cursorFilter(cur, sortField, sortDir)
		if err != nil {
			return nil, err
		}
		filter = bson.M{"$and": []bson.M{base, keyFilter}}
	}
	cur, err := s.Repo.col().Find(ctx, filter, findOpts)
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "exports.list")
	}
	defer func() { _ = cur.Close(ctx) }()
	var rows []Export
	if err := cur.All(ctx, &rows); err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "exports.list.decode")
	}
	for i := range rows {
		rows[i].decodeError()
	}
	out := &List{Data: rows}
	if out.Data == nil {
		out.Data = []Export{}
	}
	if len(rows) > limit {
		rows = rows[:limit]
		out.Data = rows
		last := rows[len(rows)-1]
		c := encodeCursor(cursorPayload{
			ID: last.ID, Field: sortField, Desc: sortDir < 0, Key: sortKey(last, sortField),
		})
		out.NextCursor = &c
	}
	if f.IncludeTotal {
		n, err := s.Repo.col().CountDocuments(ctx, base)
		if err == nil {
			out.Total = &n
			est := false
			out.TotalIsEstimate = &est
		}
	}
	return out, nil
}

// Cancel asks the job to stop and marks the export cancelled when possible.
func (s *Service) Cancel(ctx context.Context, accountID, id string) (*Export, error) {
	if err := tenancy.RequireAccount(ctx, tenancy.PermAccountsOperate, accountID); err != nil {
		return nil, err
	}
	e, err := s.Repo.get(ctx, accountID, id)
	if err != nil {
		return nil, err
	}
	switch e.State {
	case StateSucceeded, StateFailed, StateCancelled, StateExpired:
		return nil, errs.New(errs.CodeConflict, fmt.Sprintf("The export is already %s.", e.State))
	}
	if e.JobID != "" && s.Jobs != nil {
		_ = s.Jobs.Cancel(ctx, e.JobID)
	}
	if e.State == StateQueued {
		now := s.now()
		if err := s.Repo.markCancelled(ctx, e.ID, now); err != nil {
			return nil, err
		}
		e.State = StateCancelled
		e.FinishedAt = &now
		exp := now.Add(defaultRetention)
		e.ExpireAt = &exp
	}
	s.recordAudit(ctx, "export.cancelled", e.ID, audit.Changes{"accountId": {After: accountID}})
	return e, nil
}

// OpenDownload returns the decrypted gzip NDJSON bytes for a succeeded export.
func (s *Service) OpenDownload(ctx context.Context, accountID, id string) (io.ReadCloser, *Export, error) {
	e, err := s.Repo.get(ctx, accountID, id)
	if err != nil {
		return nil, nil, err
	}
	rc, err := s.openBlob(ctx, e)
	return rc, e, err
}

func (s *Service) openBlob(ctx context.Context, e *Export) (io.ReadCloser, error) {
	switch e.State {
	case StateExpired:
		return nil, errs.New(errs.CodeExportExpired, "The export has expired.")
	case StateSucceeded:
		// ok
	default:
		return nil, errs.New(errs.CodeExportNotReady, "The export is not ready for download.")
	}
	if e.Encryption.IsZero() || s.Objects == nil || s.Cipher == nil {
		return nil, errs.New(errs.CodeExportNotReady, "The export blob is missing.")
	}
	key, err := s.Cipher.Decrypt(ctx, e.Encryption, crypto.AAD{
		Collection: Collection, Field: "fileKey", DocID: e.ID,
	})
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "exports.unwrap")
	}
	if len(e.Parts) > 0 {
		return s.openParts(ctx, key, e.Parts)
	}
	// Legacy single-object blob.
	if e.ObjectRef == "" {
		return nil, errs.New(errs.CodeExportNotReady, "The export blob is missing.")
	}
	rc, _, err := s.Objects.Get(ctx, e.ObjectRef)
	if err != nil {
		if errors.Is(err, objstore.ErrNotFound) {
			return nil, errs.New(errs.CodeExportExpired, "The export object is gone.")
		}
		return nil, errs.Wrap(err, errs.CodeInternalError, "exports.getObject")
	}
	blob, err := readAll(rc)
	_ = rc.Close()
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "exports.readObject")
	}
	pt, err := openAESGCM(key, blob)
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "exports.decrypt")
	}
	return io.NopCloser(bytes.NewReader(pt)), nil
}

func (s *Service) openParts(ctx context.Context, fileKey []byte, parts []string) (io.ReadCloser, error) {
	// Decrypt each part (encrypted gzip member), concatenate members, then wrap as one
	// gzip stream so ordinary clients (single-member gunzip) receive the full NDJSON.
	var members []byte
	for _, key := range parts {
		rc, _, err := s.Objects.Get(ctx, key)
		if err != nil {
			if errors.Is(err, objstore.ErrNotFound) {
				return nil, errs.New(errs.CodeExportExpired, "The export object is gone.")
			}
			return nil, errs.Wrap(err, errs.CodeInternalError, "exports.getPart")
		}
		blob, err := readAll(rc)
		_ = rc.Close()
		if err != nil {
			return nil, errs.Wrap(err, errs.CodeInternalError, "exports.readPart")
		}
		pt, err := openAESGCM(fileKey, blob)
		if err != nil {
			return nil, errs.Wrap(err, errs.CodeInternalError, "exports.decryptPart")
		}
		members = append(members, pt...)
	}
	ndjson, err := gunzipConcat(members)
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "exports.gunzip")
	}
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	if _, err := zw.Write(ndjson); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return io.NopCloser(bytes.NewReader(gz.Bytes())), nil
}

// SignedURL returns a short-lived public download URL.
func (s *Service) SignedURL(ctx context.Context, accountID, id string) (*SignedExportURL, error) {
	if err := tenancy.RequireReadAccount(ctx, "exports", accountID); err != nil {
		return nil, err
	}
	e, err := s.Repo.get(ctx, accountID, id)
	if err != nil {
		return nil, err
	}
	if e.State == StateExpired {
		return nil, errs.New(errs.CodeExportExpired, "The export has expired.")
	}
	if e.State != StateSucceeded {
		return nil, errs.New(errs.CodeExportNotReady, "The export is not ready for download.")
	}
	ttl := clampTTL(s.SignedTTL)
	expAt := s.now().Add(ttl)
	tok, err := Sign(s.SignKey, e.OrganizationID, e.ID, expAt)
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "exports.sign")
	}
	base := strings.TrimRight(s.PublicURL, "/")
	return &SignedExportURL{URL: base + "/e/" + tok, ExpiresAt: expAt}, nil
}

// DownloadSigned serves a public token.
func (s *Service) DownloadSigned(ctx context.Context, token string) (io.ReadCloser, *Export, error) {
	org, id, err := VerifyToken(s.SignKey, token, s.now())
	if err != nil {
		return nil, nil, mapTokenErr(err)
	}
	e, err := s.Repo.getByID(ctx, org, id)
	if err != nil {
		return nil, nil, err
	}
	rc, err := s.openBlob(ctx, e)
	return rc, e, err
}

// EraseAccount deletes export docs and S3 prefixes for an account (S11).
func (s *Service) EraseAccount(ctx context.Context, orgID, accountID string) (int64, error) {
	cur, err := s.Repo.col().Find(ctx, bson.M{"organizationId": orgID, "accountId": accountID},
		options.Find().SetProjection(bson.M{"_id": 1}))
	if err != nil {
		return 0, errs.Wrap(err, errs.CodeInternalError, "exports.erase.find")
	}
	var ids []struct {
		ID string `bson:"_id"`
	}
	if err := cur.All(ctx, &ids); err != nil {
		_ = cur.Close(ctx)
		return 0, errs.Wrap(err, errs.CodeInternalError, "exports.erase.decode")
	}
	_ = cur.Close(ctx)
	var n int64
	for _, row := range ids {
		if s.Objects != nil {
			prefix, err := objstore.ExportPrefix(orgID, row.ID)
			if err == nil {
				_, _ = s.Objects.DeletePrefix(ctx, prefix)
			}
		}
		res, err := s.Repo.col().DeleteOne(ctx, bson.M{"_id": row.ID})
		if err != nil {
			return n, errs.Wrap(err, errs.CodeInternalError, "exports.erase.delete")
		}
		n += res.DeletedCount
	}
	return n, nil
}

// RetentionDuty runs until ctx ends: sweeps expired succeeded exports hourly.
func (s *Service) RetentionDuty(ctx context.Context) {
	every := time.Hour
	t := time.NewTicker(every)
	defer t.Stop()
	s.sweepRetention(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.sweepRetention(ctx)
		}
	}
}

func (s *Service) sweepRetention(ctx context.Context) {
	rows, err := s.Repo.listDueRetention(ctx, s.now(), 100)
	if err != nil {
		s.log().Warn("exports retention list failed", "err", err)
		return
	}
	for _, e := range rows {
		if s.Objects != nil {
			prefix, err := objstore.ExportPrefix(e.OrganizationID, e.ID)
			if err == nil {
				_, _ = s.Objects.DeletePrefix(ctx, prefix)
			}
		}
		_ = s.Repo.markExpired(ctx, e.ID)
	}
}

func (s *Service) recordAudit(ctx context.Context, action, id string, ch audit.Changes) {
	if s.Audit == nil {
		return
	}
	_ = s.Audit.Record(ctx, action, audit.Target{Type: "export", ID: id}, ch)
}

func (s *Service) emitCompleted(ctx context.Context, e *Export) error {
	if s.Events == nil {
		return nil
	}
	_, err := s.Events.Emit(ctx, events.Draft{
		Type: events.ExportCompleted, OrganizationID: e.OrganizationID, AccountID: e.AccountID,
		Subject: e.ID, Data: &eventtypes.ExportCompleted{
			AccountRef: eventtypes.AccountRef{AccountID: e.AccountID},
			ExportID:   e.ID, MessageCount: e.MessageCount, ByteSize: e.ByteSize,
		},
	})
	return err
}

func (s *Service) emitFailed(ctx context.Context, e *Export, code, msg string) error {
	if s.Events == nil {
		return nil
	}
	_, err := s.Events.Emit(ctx, events.Draft{
		Type: events.ExportFailed, OrganizationID: e.OrganizationID, AccountID: e.AccountID,
		Subject: e.ID, Data: &eventtypes.ExportFailed{
			AccountRef: eventtypes.AccountRef{AccountID: e.AccountID},
			ExportID:   e.ID, Code: code, Message: msg,
		},
	})
	return err
}

// MailboxPath looks up a folder path from the index.
func (s *Service) MailboxPath(ctx context.Context, mailboxID string) string {
	var m struct {
		Path string `bson:"path"`
	}
	_ = s.Repo.DB.Collection(index.Mailboxes).FindOne(ctx, bson.M{"_id": mailboxID}).Decode(&m)
	return m.Path
}

func encodeB64(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	return base64.StdEncoding.EncodeToString(b)
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return append(b, '\n')
}
