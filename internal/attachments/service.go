package attachments

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/objstore"
	"github.com/radiobros/emailbridge/internal/store"
)

// Service serves attachment downloads, ingest, signed URLs and erasure.
type Service struct {
	DB        *store.DB
	Objects   *objstore.Store
	Fetcher   Fetcher
	Raw       RawSource
	Scanner   *Scanner
	SignKey   []byte
	PublicURL string
	SignedTTL time.Duration
	Log       *slog.Logger
	Now       func() time.Time

	store docStore
}

func (s *Service) init() {
	if s.store == nil {
		s.store = &mongoStore{DB: s.DB}
	}
	if s.Now == nil {
		s.Now = nowUTC
	}
}

func (s *Service) log() *slog.Logger {
	if s.Log == nil {
		return slog.Default()
	}
	return s.Log
}

// RegisterMetas upserts attachment rows from BODYSTRUCTURE / MIME metas without reading the raw body.
// Used when attachments.s3 and attachments.antivirus are both off (P4.3 §4, W48).
func (s *Service) RegisterMetas(ctx context.Context, in RegisterInput) ([]Result, error) {
	s.init()
	if in.OrganizationID == "" || in.AccountID == "" || in.MessageID == "" {
		return nil, errs.New(errs.CodeValidationFailed, "RegisterMetas requires organizationId, accountId and messageId.")
	}
	ctx = store.WithOrganization(ctx, in.OrganizationID)
	out := make([]Result, 0, len(in.Metas))
	for _, m := range in.Metas {
		if strings.TrimSpace(m.PartID) == "" {
			continue
		}
		res, err := s.registerOne(ctx, in, m)
		if err != nil {
			return nil, err
		}
		out = append(out, res)
	}
	return out, nil
}

func (s *Service) registerOne(ctx context.Context, in RegisterInput, m Meta) (Result, error) {
	existing, err := s.store.findByPart(ctx, in.AccountID, in.MessageID, m.PartID)
	if err != nil {
		return Result{}, err
	}
	d := &Doc{
		OrganizationID: in.OrganizationID,
		AccountID:      in.AccountID,
		MessageID:      in.MessageID,
		PartID:         m.PartID,
		Filename:       m.Filename,
		ContentType:    m.ContentType,
		Size:           m.Size,
		ContentID:      m.ContentID,
		Disposition:    m.Disposition,
		Scan:           ScanSkipped,
		CreatedAt:      s.Now(),
	}
	if d.ContentType == "" {
		d.ContentType = "application/octet-stream"
	}
	if d.Disposition == "" {
		d.Disposition = "attachment"
	}
	if existing != nil {
		d.ID = existing.ID
		d.CreatedAt = existing.CreatedAt
		d.ObjectKey = existing.ObjectKey
		d.ETag = existing.ETag
		d.Scan = existing.Scan
		d.Infected = existing.Infected
		if d.Size == 0 {
			d.Size = existing.Size
		}
	} else {
		d.ID = store.NewID(store.PrefixAttachment)
	}
	if err := s.store.upsert(ctx, d); err != nil {
		return Result{}, err
	}
	return Result{
		ID: d.ID, Filename: d.Filename, ContentType: d.ContentType, Size: d.Size,
		ContentID: d.ContentID, Disposition: d.Disposition, PartID: d.PartID, Scan: d.Scan,
	}, nil
}

// Ingest indexes attachments of a raw message, optionally scans and stores to S3.
func (s *Service) Ingest(ctx context.Context, in IngestInput) ([]Result, error) {
	s.init()
	if in.OrganizationID == "" || in.AccountID == "" || in.MessageID == "" {
		return nil, errs.New(errs.CodeValidationFailed, "Ingest requires organizationId, accountId and messageId.")
	}
	ctx = store.WithOrganization(ctx, in.OrganizationID)
	parts, err := listParts(in.Raw)
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "attachments.parse")
	}
	out := make([]Result, 0, len(parts))
	for _, p := range parts {
		res, err := s.ingestOne(ctx, in, p)
		if err != nil {
			return nil, err
		}
		out = append(out, res)
	}
	return out, nil
}

func (s *Service) ingestOne(ctx context.Context, in IngestInput, p extracted) (Result, error) {
	existing, err := s.store.findByPart(ctx, in.AccountID, in.MessageID, p.PartID)
	if err != nil {
		return Result{}, err
	}
	d := &Doc{
		OrganizationID: in.OrganizationID,
		AccountID:      in.AccountID,
		MessageID:      in.MessageID,
		PartID:         p.PartID,
		Filename:       p.Filename,
		ContentType:    p.ContentType,
		Size:           int64(len(p.Body)),
		ContentID:      p.ContentID,
		Disposition:    p.Disposition,
		ETag:           p.ETag,
		CreatedAt:      s.Now(),
	}
	if existing != nil {
		d.ID = existing.ID
		d.CreatedAt = existing.CreatedAt
		d.ObjectKey = existing.ObjectKey
		d.Scan = existing.Scan
		d.Infected = existing.Infected
	} else {
		d.ID = store.NewID(store.PrefixAttachment)
	}

	scanStatus := ScanSkipped
	if in.Scan {
		if s.Scanner == nil {
			scanStatus = ScanSkipped
		} else {
			sr := s.Scanner.Scan(ctx, bytes.NewReader(p.Body), int64(len(p.Body)))
			scanStatus = sr.Status
			if sr.Status == ScanError && sr.Err != nil {
				s.log().LogAttrs(ctx, slog.LevelWarn, "attachments: clamav scan failed",
					slog.String("attachmentId", d.ID), slog.Any("err", sr.Err))
			}
		}
	}
	d.Scan = scanStatus
	d.Infected = scanStatus == ScanInfected

	if in.StoreS3 && !d.Infected && s.Objects != nil {
		key, err := objstore.AttachmentKey(in.OrganizationID, in.AccountID, d.ID)
		if err != nil {
			return Result{}, errs.Wrap(err, errs.CodeInternalError, "attachments.key")
		}
		ct := p.ContentType
		if ct == "" {
			ct = "application/octet-stream"
		}
		if err := s.Objects.Put(ctx, key, bytes.NewReader(p.Body), int64(len(p.Body)), objstore.ObjectMeta{
			Organization: in.OrganizationID, Account: in.AccountID, ContentType: ct,
		}); err != nil {
			return Result{}, errs.Wrap(err, errs.CodeInternalError, "attachments.put")
		}
		d.ObjectKey = key
	}

	if err := s.store.upsert(ctx, d); err != nil {
		return Result{}, err
	}
	res := Result{
		ID: d.ID, Filename: d.Filename, ContentType: d.ContentType, Size: d.Size,
		ContentID: d.ContentID, Disposition: d.Disposition, PartID: d.PartID, Scan: d.Scan,
	}
	if d.ObjectKey != "" && !d.Infected {
		if u, exp, err := s.signedURL(d); err == nil {
			res.URL = u
			_ = exp
		}
	}
	return res, nil
}

func (s *Service) signedURL(d *Doc) (string, time.Time, error) {
	ttl := clampTTL(s.SignedTTL)
	exp := s.Now().Add(ttl)
	tok, err := Sign(s.SignKey, d.OrganizationID, d.ID, exp)
	if err != nil {
		return "", time.Time{}, err
	}
	base := strings.TrimRight(s.PublicURL, "/")
	return base + "/a/" + tok, exp, nil
}

// Open resolves attachment bytes for streaming.
func (s *Service) Open(ctx context.Context, accountID, messageID, attID string) (*Doc, io.ReadCloser, error) {
	s.init()
	d, err := s.store.get(ctx, accountID, messageID, attID)
	if err != nil {
		return nil, nil, err
	}
	rc, err := s.openBody(ctx, d)
	return d, rc, err
}

func (s *Service) openBody(ctx context.Context, d *Doc) (io.ReadCloser, error) {
	if d.ObjectKey != "" && s.Objects != nil {
		rc, _, err := s.Objects.Get(ctx, d.ObjectKey)
		if err == nil {
			return rc, nil
		}
		if !errors.Is(err, objstore.ErrNotFound) {
			return nil, errs.Wrap(err, errs.CodeInternalError, "attachments.s3")
		}
	}
	if s.Fetcher != nil {
		rc, info, err := s.Fetcher.FetchPart(ctx, d.AccountID, d.MessageID, d.PartID)
		if err == nil {
			if d.ContentType == "" && info.ContentType != "" {
				d.ContentType = info.ContentType
			}
			return rc, nil
		}
		s.log().LogAttrs(ctx, slog.LevelDebug, "attachments: fetcher miss", slog.Any("err", err))
	}
	if s.Raw == nil {
		return nil, errs.New(errs.CodeAttachmentNotFound, "The attachment content is not available.")
	}
	rc, err := s.Raw.OpenRaw(ctx, d.AccountID, d.MessageID)
	if err != nil {
		return nil, err
	}
	raw, err := rawFromReader(rc)
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "attachments.raw")
	}
	part, err := extractPart(raw, d.PartID)
	if err != nil {
		return nil, err
	}
	if d.ETag == "" {
		d.ETag = part.ETag
	}
	return bytesReader(part.Body), nil
}

// SignedURL returns a short-lived public URL when the attachment is stored and clean.
func (s *Service) SignedURL(ctx context.Context, accountID, messageID, attID string) (SignedAttachmentURL, error) {
	s.init()
	d, err := s.store.get(ctx, accountID, messageID, attID)
	if err != nil {
		return SignedAttachmentURL{}, err
	}
	if d.Infected {
		return SignedAttachmentURL{}, errs.New(errs.CodeAttachmentInfected, "The attachment failed the virus scan.")
	}
	if d.ObjectKey == "" {
		return SignedAttachmentURL{}, errs.New(errs.CodeFeatureDisabled, "Attachment S3 storage is not enabled for this attachment.")
	}
	u, exp, err := s.signedURL(d)
	if err != nil {
		return SignedAttachmentURL{}, errs.Wrap(err, errs.CodeInternalError, "attachments.sign")
	}
	return SignedAttachmentURL{URL: u, ExpiresAt: exp}, nil
}

// OpenSigned verifies the token and opens the attachment body.
func (s *Service) OpenSigned(ctx context.Context, token string) (*Doc, io.ReadCloser, error) {
	s.init()
	orgID, attID, err := VerifyToken(s.SignKey, token, s.Now())
	if err != nil {
		return nil, nil, mapTokenErr(err)
	}
	d, err := s.store.getByID(ctx, orgID, attID)
	if err != nil {
		return nil, nil, err
	}
	if d.Infected {
		return nil, nil, errs.New(errs.CodeAttachmentInfected, "The attachment failed the virus scan.")
	}
	rc, err := s.openBody(ctx, d)
	return d, rc, err
}

// EraseMessage deletes attachment docs and S3 objects for one message.
func (s *Service) EraseMessage(ctx context.Context, accountID, messageID string) (int64, error) {
	s.init()
	docs, err := s.store.listByMessage(ctx, accountID, messageID)
	if err != nil {
		return 0, err
	}
	for _, d := range docs {
		if d.ObjectKey != "" && s.Objects != nil {
			_ = s.Objects.Delete(ctx, d.ObjectKey)
		}
	}
	return s.store.deleteMessage(ctx, accountID, messageID)
}
