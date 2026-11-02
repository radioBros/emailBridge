package submit

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/logging"
	"github.com/radiobros/emailbridge/internal/mailproto/mime"
	"github.com/radiobros/emailbridge/internal/objstore"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

func (s *Service) mergeCap() int {
	if s.MergeMax > 0 {
		return s.MergeMax
	}
	return MaxMergeRecipients
}

func (s *Service) submitMerge(ctx context.Context, org, accountID string, acc *SMTPAccount, req SubmitRequest, key, idemID, hash string, includeRaw bool) (*SubmitResult, error) {
	if len(req.To) > 0 || len(req.Cc) > 0 || len(req.Bcc) > 0 {
		return nil, errs.New(errs.CodeValidationFailed, "Use recipients or to/cc/bcc, not both.")
	}
	if req.Raw != "" {
		return nil, errs.New(errs.CodeRawAndFields, "Mail merge cannot use raw.")
	}
	if len(req.Recipients) > s.mergeCap() {
		return nil, errs.New(errs.CodeMailMergeTooLarge,
			fmt.Sprintf("Mail merge allows at most %d recipients.", s.mergeCap())).
			WithSafe("limit", s.mergeCap()).WithSafe("count", len(req.Recipients))
	}
	baseVars := map[string]any{}
	if req.Template != nil {
		baseVars = req.Template.Variables
	}
	type builtRow struct {
		req        SubmitRequest
		built      *mime.Built
		envelope   Envelope
		bcc        []string
		outboundID string
	}
	if err := s.checkSuppression(ctx, org, req); err != nil {
		return nil, err
	}
	rows := make([]builtRow, 0, len(req.Recipients))
	for i, rec := range req.Recipients {
		if len(rec.To) == 0 && len(rec.Cc) == 0 && len(rec.Bcc) == 0 {
			return nil, errs.New(errs.CodeValidationFailed, "Each merge recipient needs to, cc or bcc.").
				WithSafe("index", i)
		}
		one := req
		one.Recipients = nil
		one.To, one.Cc, one.Bcc = rec.To, rec.Cc, rec.Bcc
		if one.Template != nil {
			tpl := *one.Template
			tpl.Variables = mergeVars(baseVars, rec.Variables)
			one.Template = &tpl
		} else if len(rec.Variables) > 0 {
			return nil, errs.New(errs.CodeValidationFailed, "recipient.variables require a template.")
		}
		prepared, err := s.prepareRequest(ctx, accountID, acc, one)
		if err != nil {
			return nil, err
		}
		var oid string
		if needsOutboundIDEarly(ctx, s, org, accountID, prepared) {
			oid = allocOutboundID()
			if err := s.applyTracking(ctx, org, accountID, oid, &prepared); err != nil {
				return nil, err
			}
		}
		built, envelope, bcc, err := build(prepared, acc)
		if err != nil {
			return nil, err
		}
		size := int64(len(built.Raw))
		if s.MaxMessageSize > 0 && size > s.MaxMessageSize {
			return nil, errs.New(errs.CodeMessageTooLarge,
				fmt.Sprintf("The message is %d bytes; the limit is %d bytes.", size, s.MaxMessageSize)).
				WithSafe("size", size).WithSafe("limit", s.MaxMessageSize).WithSafe("index", i)
		}
		rows = append(rows, builtRow{req: prepared, built: built, envelope: envelope, bcc: bcc, outboundID: oid})
	}
	if req.DryRun {
		out := &MergeDryRun{Results: make([]DryRunResult, 0, len(rows))}
		for _, row := range rows {
			r := DryRunResult{MessageID: row.built.MessageID, Size: int64(len(row.built.Raw)), Envelope: row.envelope}
			if includeRaw {
				r.Raw = base64.StdEncoding.EncodeToString(row.built.Raw)
			}
			out.Results = append(out.Results, r)
		}
		return &SubmitResult{MergeDryRun: out, Status: http.StatusOK}, nil
	}
	if err := s.checkQuota(ctx, org, accountID); err != nil {
		return nil, err
	}
	now := s.now()
	messages := make([]*Message, 0, len(rows))
	accepted := make([]Accepted, 0, len(rows))
	for _, row := range rows {
		m := s.newOutbound(ctx, org, accountID, acc, row.req, row.built, row.envelope, row.bcc, key, now)
		if row.outboundID != "" {
			m.ID = row.outboundID
		}
		rawRef, err := objstore.OutboundKey(org, accountID, m.ID)
		if err != nil {
			return nil, errs.Wrap(err, errs.CodeInternalError, "submit.key")
		}
		if err := s.Objects.Put(ctx, rawRef, bytes.NewReader(row.built.Raw), m.Size,
			objstore.ObjectMeta{Organization: org, Account: accountID, ContentType: "message/rfc822"}); err != nil {
			return nil, errs.Wrap(err, errs.CodeInternalError, "submit.store")
		}
		m.RawRef = rawRef
		messages = append(messages, m)
		accepted = append(accepted, Accepted{ID: m.ID, MessageID: m.MessageIDHeader, State: m.State, SendAt: m.SendAt})
	}
	mergeBody := &MergeAccepted{Messages: accepted}
	if err := s.commitMerge(ctx, messages, idemID, hash, mergeBody); err != nil {
		if res, rerr := s.replayConflict(ctx, err, idemID, hash); res != nil || rerr != nil {
			return res, rerr
		}
		return nil, err
	}
	for _, m := range messages {
		if m.State == StateQueued {
			s.publish(ctx, m, 0)
		}
	}
	s.log().InfoContext(ctx, "outbound mail merge submitted", "count", len(messages))
	return &SubmitResult{MergeAccepted: mergeBody, Status: http.StatusAccepted}, nil
}

func (s *Service) commitMerge(ctx context.Context, messages []*Message, idemID, hash string, body *MergeAccepted) error {
	return s.Repo.DB.WithTx(ctx, func(txCtx context.Context) error {
		for _, m := range messages {
			if err := s.Repo.insert(txCtx, m); err != nil {
				return err
			}
		}
		if idemID == "" {
			return nil
		}
		raw, err := json.Marshal(body)
		if err != nil {
			return errs.Wrap(err, errs.CodeInternalError, "submit.idempotency")
		}
		now := s.now()
		return s.Repo.putIdempotency(txCtx, &idempotencyRecord{
			ID: idemID, OrganizationID: messages[0].OrganizationID, AccountID: messages[0].AccountID,
			Method: http.MethodPost, Route: submitRoute, RequestHash: hash,
			Status: http.StatusAccepted, Response: string(raw), CreatedAt: now,
			ExpireAt: now.Add(s.idempotencyTTL()),
		})
	})
}

func (s *Service) newOutbound(ctx context.Context, org, accountID string, acc *SMTPAccount, req SubmitRequest, built *mime.Built, envelope Envelope, bcc []string, key string, now time.Time) *Message {
	m := &Message{
		ID: store.NewID(store.PrefixOutbound), OrganizationID: org, AccountID: accountID,
		State: StateQueued, Policy: resolvePolicy(req.Policy, acc.Policy, s.Policy),
		MessageIDHeader: built.MessageID, Envelope: envelope, Bcc: bcc, Subject: req.Subject,
		Size: int64(len(built.Raw)), MaxAttempts: resolveAttempts(req.MaxAttempts, acc.MaxAttempts, s.MaxAttempts),
		IdempotencyKey: key, CopyToSent: copySettings(req.CopyToSent, acc), CreatedAt: now,
		NextAttemptAt: &now, Version: 1, TraceParent: logging.TraceParent(ctx),
		ListID: req.ListID,
	}
	if acc.BackoffBase > 0 {
		m.BackoffBase = acc.BackoffBase.String()
	}
	if c, ok := tenancy.CallerFrom(ctx); ok {
		m.CreatedBy = c.Actor()
		if c.Kind == tenancy.CallerSystem {
			m.System = true
			m.CopyToSent = CopyToSent{Path: m.CopyToSent.Path, State: CopySkipped}
		}
	}
	if req.SendAt != nil && req.SendAt.After(now) {
		at := req.SendAt.UTC()
		m.State, m.SendAt, m.NextAttemptAt = StateScheduled, &at, &at
	}
	return m
}
