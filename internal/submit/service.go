package submit

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/radiobros/emailbridge/internal/audit"
	"github.com/radiobros/emailbridge/internal/bus"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/events"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/ippool"
	"github.com/radiobros/emailbridge/internal/logging"
	"github.com/radiobros/emailbridge/internal/mailproto/mime"
	"github.com/radiobros/emailbridge/internal/objstore"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/submit/policy"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

// Account states submit refuses. SMTP stays available in authError, where only IMAP failed (S6 §2.1).
const (
	accountDisabled = "disabled"
	accountDeleting = "deleting"
)

// submitRoute is the route part of the idempotency key (S1 `idempotencyKeys`).
const submitRoute = "/v1/accounts/{accountId}/outbound-messages"

// Service is the submit API and the send pipeline.
type Service struct {
	Repo      *Repo
	Accounts  AccountSMTP
	Objects   RawStore
	Events    *events.Writer
	Publisher Publisher
	Pool      *Pool
	Sent      SentCopier
	Sandbox   SandboxGate
	Gmail     GmailSender // P6.2: provider=gmail sends via Gmail API
	Graph     GraphSender // P6.3: provider=graph sends via Graph sendMail
	Log       *slog.Logger
	NodeID    string

	// EHLO is the fallback EHLO name: the host of EB_PUBLIC_URL (S6 §4).
	EHLO string
	// IPPool selects outbound local addresses when LocalAddress is empty (W57).
	IPPool *ippool.Selector
	// MaxMessageSize is EB_SUBMIT_MAX_MESSAGE_SIZE.
	MaxMessageSize int64
	// Policy, MaxAttempts, BackoffBase and BackoffJitter are the EB_SUBMIT_* defaults.
	Policy         string
	MaxAttempts    int
	BackoffBase    time.Duration
	BackoffJitter  float64
	IdempotencyTTL time.Duration
	// QuotaSendsPerDay (organisation) and QuotaSendsPerMinute (account) are EB_QUOTA_SENDS_* (0 = off).
	QuotaSendsPerDay    int
	QuotaSendsPerMinute int
	// CopyRetryDelay is the pause between copy-to-Sent attempts (S6 §5).
	CopyRetryDelay time.Duration
	// Now and Rand are injectable for tests.
	Now  func() time.Time
	Rand func() float64
	// OnStage runs after each stage has been persisted. It is the seam the crash tests use: an error
	// aborts the attempt without recording it, exactly as a dying worker would.
	OnStage func(ctx context.Context, m *Message, stage string) error

	// Templates renders Handlebars templates (P5.1); nil refuses template submits.
	Templates TemplateRenderer
	// Messages loads indexed/archived raw sources for reply/forward (P5.1).
	Messages MessageSource
	// MergeMax caps recipients length (0 → MaxMergeRecipients).
	MergeMax int
	// Gateways resolves organisation SMTP gateways (P5.3); nil refuses gatewayId.
	Gateways GatewayResolver
	// Audit records outbound.retried / outbound.send_now (P5.3); nil skips.
	Audit *audit.Writer
	// Suppression checks lists before send (P5.2); nil skips.
	Suppression SuppressionChecker
	// Tracking rewrites HTML / List-Unsubscribe (P5.2); nil skips.
	Tracking TrackingInjector
	// Throttle is the P5.5 send-window / rate-limit / pre-send gate; nil skips.
	Throttle ThrottleGate
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

// SubmitResult is what the API returns for one submit: a dry run, an acceptance, or a replay.
type SubmitResult struct {
	Accepted      *Accepted
	DryRun        *DryRunResult
	MergeAccepted *MergeAccepted
	MergeDryRun   *MergeDryRun
	Status        int
	Replayed      bool
}

// TemplateRenderer renders a stored template for submit (implemented by internal/templates.Service.RenderParts).
type TemplateRenderer interface {
	RenderParts(ctx context.Context, id string, version *int, vars map[string]any) (subject, text, html string, err error)
}

// MessageSource loads the raw RFC 822 of an indexed message for reply/forward (P5.1).
type MessageSource interface {
	Raw(ctx context.Context, accountID, messageID string) ([]byte, error)
}

// Submit validates, builds, stores and enqueues one message (S6 §2).
func (s *Service) Submit(ctx context.Context, accountID string, req SubmitRequest, key string, includeRaw bool) (*SubmitResult, error) {
	if err := tenancy.RequireAccount(ctx, tenancy.PermSend, accountID); err != nil {
		return nil, err
	}
	org, ok := store.OrganizationFrom(ctx)
	if !ok {
		return nil, store.ErrNoTenant
	}
	if !store.IsID(store.PrefixAccount, accountID) {
		return nil, errs.New(errs.CodeNotFound, "The account does not exist.")
	}
	idemID, hash, err := s.idempotencyOf(org, accountID, key, req)
	if err != nil {
		return nil, err
	}
	if idemID != "" {
		if res, err := s.replay(ctx, idemID, hash); res != nil || err != nil {
			return res, err
		}
	}
	acc, err := s.account(ctx, org, accountID)
	if err != nil {
		return nil, err
	}
	if len(req.Recipients) > 0 {
		return s.submitMerge(ctx, org, accountID, acc, req, key, idemID, hash, includeRaw)
	}
	req, err = s.prepareRequest(ctx, accountID, acc, req)
	if err != nil {
		return nil, err
	}
	if err := s.checkSuppression(ctx, org, req); err != nil {
		return nil, err
	}
	outboundID := ""
	if needsOutboundIDEarly(ctx, s, org, accountID, req) {
		outboundID = allocOutboundID()
		if err := s.applyTracking(ctx, org, accountID, outboundID, &req); err != nil {
			return nil, err
		}
	}
	built, envelope, bcc, err := build(req, acc)
	if err != nil {
		return nil, err
	}
	size := int64(len(built.Raw))
	if s.MaxMessageSize > 0 && size > s.MaxMessageSize {
		return nil, errs.New(errs.CodeMessageTooLarge,
			fmt.Sprintf("The message is %d bytes; the limit is %d bytes.", size, s.MaxMessageSize)).
			WithSafe("size", size).WithSafe("limit", s.MaxMessageSize)
	}
	if req.DryRun {
		out := &DryRunResult{MessageID: built.MessageID, Size: size, Envelope: envelope}
		if includeRaw {
			out.Raw = base64.StdEncoding.EncodeToString(built.Raw)
		}
		return &SubmitResult{DryRun: out, Status: http.StatusOK}, nil
	}
	if err := s.checkQuota(ctx, org, accountID); err != nil {
		return nil, err
	}

	now := s.now()
	if outboundID == "" {
		outboundID = store.NewID(store.PrefixOutbound)
	}
	m := &Message{
		ID: outboundID, OrganizationID: org, AccountID: accountID,
		State: StateQueued, Policy: resolvePolicy(req.Policy, acc.Policy, s.Policy),
		MessageIDHeader: built.MessageID, Envelope: envelope, Bcc: bcc, Subject: req.Subject,
		Size: size, MaxAttempts: resolveAttempts(req.MaxAttempts, acc.MaxAttempts, s.MaxAttempts),
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
			// System mail carries one-time secrets: never into a Sent folder that is synced, indexed and
			// reported to the organisation's webhooks (S10 §7).
			m.System = true
			m.CopyToSent = CopyToSent{Path: m.CopyToSent.Path, State: CopySkipped}
		}
	}
	if req.SendAt != nil && req.SendAt.After(now) {
		at := req.SendAt.UTC()
		m.State, m.SendAt, m.NextAttemptAt = StateScheduled, &at, &at
	}
	if req.Subject == "" && req.Raw != "" {
		m.Subject = rawSubject(built.Raw)
	}
	if req.DSN != nil {
		m.DSN = req.DSN
	}
	if req.GatewayID != "" {
		m.GatewayID = req.GatewayID
	} else if acc.GatewayID != "" {
		m.GatewayID = acc.GatewayID
	}
	ctx = logging.With(ctx, func(id *logging.IDs) { id.OutboundID = m.ID; id.AccountID = accountID })

	rawRef, err := objstore.OutboundKey(org, accountID, m.ID)
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "submit.key")
	}
	if err := s.Objects.Put(ctx, rawRef, bytes.NewReader(built.Raw), size,
		objstore.ObjectMeta{Organization: org, Account: accountID, ContentType: "message/rfc822"}); err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "submit.store")
	}
	m.RawRef = rawRef

	accepted := &Accepted{ID: m.ID, MessageID: m.MessageIDHeader, State: m.State, SendAt: m.SendAt}
	if err := s.commit(ctx, m, idemID, hash, accepted); err != nil {
		if res, rerr := s.replayConflict(ctx, err, idemID, hash); res != nil || rerr != nil {
			return res, rerr
		}
		return nil, err
	}
	if m.State == StateQueued {
		// A scheduled message is published by the due duty: publishing it now would burn its
		// Nats-Msg-Id in the dedup window and the real job would be dropped (S4 §4).
		s.publish(ctx, m, 0)
	}
	s.log().InfoContext(ctx, "outbound message submitted", "state", m.State, "policy", m.Policy,
		"recipients", len(m.Envelope.Recipients(m.Bcc)), "size", m.Size)
	return &SubmitResult{Accepted: accepted, Status: http.StatusAccepted}, nil
}

// commit inserts the message and, when the request carried a key, its idempotency record in one
// transaction (S6 §2.6).
func (s *Service) commit(ctx context.Context, m *Message, idemID, hash string, accepted *Accepted) error {
	return s.Repo.DB.WithTx(ctx, func(txCtx context.Context) error {
		if err := s.Repo.insert(txCtx, m); err != nil {
			return err
		}
		if idemID == "" {
			return nil
		}
		body, err := json.Marshal(accepted)
		if err != nil {
			return errs.Wrap(err, errs.CodeInternalError, "submit.idempotency")
		}
		now := s.now()
		return s.Repo.putIdempotency(txCtx, &idempotencyRecord{
			ID: idemID, OrganizationID: m.OrganizationID, AccountID: m.AccountID,
			Method: http.MethodPost, Route: submitRoute, RequestHash: hash,
			Status: http.StatusAccepted, Response: string(body), CreatedAt: now,
			ExpireAt: now.Add(s.idempotencyTTL()),
		})
	})
}

// publish enqueues one send attempt. Nats-Msg-Id is outboundId/attemptNo, so the API, the scheduler sweep
// and a redelivery can never double-enqueue the same attempt (S4 §4, S6 §6.2).
func (s *Service) publish(ctx context.Context, m *Message, attempt int) {
	payload, err := json.Marshal(job{OutboundID: m.ID, AccountID: m.AccountID, OrganizationID: m.OrganizationID, AttemptNo: attempt})
	if err != nil {
		s.log().ErrorContext(ctx, "cannot encode a submit job", "error", err)
		return
	}
	// The JetStream message ID deduplicates repeated publishes of the same dispatch (two scheduler ticks, two
	// nodes). It includes the due time, so a message that is rescheduled, sent now or retried is a new dispatch:
	// with "<id>/<attempt>" alone, send-now after a send window or a pre-send hold was dropped as a duplicate
	// (final pass, P5 e2e).
	msgID := fmt.Sprintf("%s/%d", m.ID, attempt)
	if m.NextAttemptAt != nil {
		msgID += "/" + strconv.FormatInt(m.NextAttemptAt.UnixMilli(), 36)
	}
	if _, err := s.Publisher.Publish(ctx, bus.SubmitSubject(m.AccountID), payload, msgID, nil); err != nil {
		// The message stays queued with nextAttemptAt in the past; the scheduler publishes it (S6 §2.7).
		s.log().WarnContext(ctx, "cannot publish a submit job; the scheduler will retry", "error", err)
	}
}

func (s *Service) idempotencyTTL() time.Duration {
	if s.IdempotencyTTL > 0 {
		return s.IdempotencyTTL
	}
	return 24 * time.Hour
}

// idempotencyOf validates the header and returns the record ID and the request hash ("" without a key).
func (s *Service) idempotencyOf(org, accountID, key string, req SubmitRequest) (string, string, error) {
	if key == "" {
		return "", "", nil
	}
	if len(key) > 255 {
		return "", "", errs.New(errs.CodeValidationFailed, "Idempotency-Key must be 1 to 255 printable ASCII characters.")
	}
	for _, r := range key {
		if r < 0x20 || r > 0x7e {
			return "", "", errs.New(errs.CodeValidationFailed, "Idempotency-Key must be 1 to 255 printable ASCII characters.")
		}
	}
	hash, err := requestHash(req)
	if err != nil {
		return "", "", err
	}
	return idempotencyID(org, accountID, http.MethodPost, submitRoute, key), hash, nil
}

// replay returns the stored response of a completed request with the same key (S2 §2).
func (s *Service) replay(ctx context.Context, idemID, hash string) (*SubmitResult, error) {
	rec, err := s.Repo.idempotency(ctx, idemID)
	if err != nil || rec == nil {
		return nil, err
	}
	return replayOf(rec, hash)
}

// replayConflict turns the duplicate-key error of two concurrent identical requests into a replay.
func (s *Service) replayConflict(ctx context.Context, err error, idemID, hash string) (*SubmitResult, error) {
	if idemID == "" || !mongo.IsDuplicateKeyError(err) {
		return nil, nil
	}
	rec, lookupErr := s.Repo.idempotency(ctx, idemID)
	if lookupErr != nil || rec == nil {
		return nil, errs.New(errs.CodeIdempotencyInProgress, "A request with this Idempotency-Key is still running.")
	}
	return replayOf(rec, hash)
}

func replayOf(rec *idempotencyRecord, hash string) (*SubmitResult, error) {
	if rec.RequestHash != hash {
		return nil, errs.New(errs.CodeIdempotencyKeyReused, "This Idempotency-Key was used with a different request body.")
	}
	var merge MergeAccepted
	if err := json.Unmarshal([]byte(rec.Response), &merge); err == nil && len(merge.Messages) > 0 {
		return &SubmitResult{MergeAccepted: &merge, Status: rec.Status, Replayed: true}, nil
	}
	var accepted Accepted
	if err := json.Unmarshal([]byte(rec.Response), &accepted); err != nil {
		return nil, errs.New(errs.CodeIdempotencyInProgress, "A request with this Idempotency-Key is still running.")
	}
	return &SubmitResult{Accepted: &accepted, Status: rec.Status, Replayed: true}, nil
}

// account resolves the SMTP settings and refuses accounts that may not send (S6 §2.1).
func (s *Service) account(ctx context.Context, org, accountID string) (*SMTPAccount, error) {
	acc, err := s.Accounts.SMTP(ctx, accountID)
	if err != nil {
		return nil, err
	}
	if acc == nil || acc.OrganizationID != org {
		return nil, errs.New(errs.CodeNotFound, "The account does not exist.")
	}
	switch acc.State {
	case accountDeleting:
		return nil, errs.New(errs.CodeAccountDeleting, "The account is being deleted.")
	case accountDisabled:
		return nil, errs.New(errs.CodeAccountDisabled, "The account is disabled; resume it before sending.")
	}
	if acc.Host == "" {
		return nil, errs.New(errs.CodeValidationFailed, "The account has no SMTP configuration.")
	}
	return acc, nil
}

// checkQuota enforces the send quotas (S9 §2.12): the instance-wide EB_QUOTA_SENDS_* and the organisation's
// own quotas (sendsPerDay, sendsPerAccountMinute); the stricter limit wins. A reached quota is a 429 with
// Retry-After = when the oldest send of the window ages out, and RateLimit-* headers.
func (s *Service) checkQuota(ctx context.Context, org, accountID string) error {
	now := s.now()
	oq, err := tenancy.LoadOrgQuotas(ctx, s.Repo.DB, org)
	if err != nil {
		return err
	}
	if limit, fromOrg := tenancy.EffectiveLimit(s.QuotaSendsPerDay, oq.SendsPerDay); limit > 0 {
		var cause *errs.E
		if fromOrg {
			cause = tenancy.OrgQuotaExceeded(tenancy.QuotaSendsPerDay, limit)
		} else {
			cause = errs.New(errs.CodeQuotaSendsExceeded, "The organisation reached its daily send quota.").WithSafe("limit", limit)
		}
		if err := s.windowQuota(ctx, bson.M{"organizationId": org}, now, 24*time.Hour, limit, cause); err != nil {
			return err
		}
	}
	if limit, fromOrg := tenancy.EffectiveLimit(s.QuotaSendsPerMinute, oq.SendsPerAccountMinute); limit > 0 {
		var cause *errs.E
		if fromOrg {
			cause = tenancy.OrgQuotaExceeded(tenancy.QuotaSendsPerAccountMinute, limit)
		} else {
			cause = errs.New(errs.CodeQuotaSendsExceeded, "The account reached its per-minute send quota.").WithSafe("limit", limit)
		}
		if err := s.windowQuota(ctx, bson.M{"accountId": accountID}, now, time.Minute, limit, cause); err != nil {
			return err
		}
	}
	return nil
}

// windowQuota counts the sends matching filter in the rolling window and returns cause (with the limiter
// state) when limit is reached.
func (s *Service) windowQuota(ctx context.Context, filter bson.M, now time.Time, window time.Duration, limit int, cause *errs.E) error {
	since := now.Add(-window)
	n, err := s.Repo.sendsSince(ctx, filter, since, int64(limit))
	if err != nil {
		return errs.Wrap(err, errs.CodeInternalError, "submit.quota")
	}
	if n < int64(limit) {
		return nil
	}
	reset := window
	if oldest, ok, err := s.Repo.oldestSendSince(ctx, filter, since); err == nil && ok {
		if d := oldest.Add(window).Sub(now); d > 0 {
			reset = d
		}
	}
	info := errs.RateInfo{Limit: limit, Remaining: 0, Reset: reset}
	httpapi.NoteRateLimit(ctx, info)
	return cause.WithRate(info)
}

// build turns a request into the bytes on the wire and the stored envelope (S6 §2.3). bcc holds the
// envelope-only recipients of a structured request: mime never writes them into the headers, so they live
// in the document (never in the API view) and are added to RCPT TO.
func build(req SubmitRequest, acc *SMTPAccount) (*mime.Built, Envelope, []string, error) {
	structured := len(req.To) > 0 || len(req.Cc) > 0 || len(req.Bcc) > 0 || req.From != nil ||
		req.Subject != "" || req.Text != "" || req.HTML != "" || len(req.Attachments) > 0 || len(req.Headers) > 0
	switch {
	case req.Raw != "" && structured:
		return nil, Envelope{}, nil, errs.New(errs.CodeRawAndFields, "Send either raw or the structured fields, not both.")
	case req.Envelope != nil && req.Raw == "":
		return nil, Envelope{}, nil, errs.New(errs.CodeValidationFailed, "envelope is only allowed with raw.")
	case req.Raw != "":
		return buildRaw(req)
	case !structured:
		return nil, Envelope{}, nil, errs.New(errs.CodeValidationFailed, "A message needs raw or the structured fields.")
	}
	from := []mime.Address{{Name: acc.Name, Address: acc.Email}}
	if req.From != nil {
		from = []mime.Address{{Name: req.From.Name, Address: req.From.Address}}
	}
	built, err := mime.Build(mime.Message{
		From: from, To: addresses(req.To), Cc: addresses(req.Cc), Bcc: addresses(req.Bcc),
		ReplyTo: addresses(req.ReplyTo), Subject: req.Subject, Text: req.Text, HTML: req.HTML,
		Headers: req.Headers, MessageID: req.MessageID, Attachments: attachments(req.Attachments),
	})
	if err != nil {
		return nil, Envelope{}, nil, err
	}
	env := Envelope{From: built.Envelope.From, To: plain(req.To), Cc: plain(req.Cc), BccCount: len(req.Bcc)}
	return built, env, plain(req.Bcc), nil
}

func addresses(in []AddressInput) []mime.Address {
	out := make([]mime.Address, 0, len(in))
	for _, a := range in {
		out = append(out, mime.Address{Name: a.Name, Address: a.Address})
	}
	return out
}

func plain(in []AddressInput) []string {
	out := make([]string, 0, len(in))
	for _, a := range in {
		out = append(out, a.Address)
	}
	return out
}

func attachments(in []AttachmentInput) []mime.Attachment {
	out := make([]mime.Attachment, 0, len(in))
	for _, a := range in {
		content, err := base64.StdEncoding.DecodeString(a.Content)
		if err != nil {
			content = nil
		}
		out = append(out, mime.Attachment{
			Filename: a.Filename, ContentType: a.ContentType, Content: content,
			Disposition: a.Disposition, ContentID: a.ContentID,
		})
	}
	return out
}

// rawSubject reads the Subject of a raw message for the outbox list (decoded, capped).
func rawSubject(raw []byte) string {
	head, _, _ := bytes.Cut(raw, []byte("\r\n\r\n"))
	for line := range strings.Lines(string(head)) {
		if value, ok := strings.CutPrefix(line, "Subject:"); ok {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func resolvePolicy(values ...string) string {
	for _, v := range values {
		if v == policy.AtMostOnce {
			return policy.AtMostOnce
		}
		if v == policy.Standard {
			return policy.Standard
		}
	}
	return policy.Standard
}

func resolveAttempts(message *int, values ...int) int {
	if message != nil && *message > 0 {
		return *message
	}
	for _, v := range values {
		if v > 0 {
			return v
		}
	}
	return policy.DefaultMaxAttempts
}

// copySettings resolves the copy-to-Sent settings of one message (S6 §5).
func copySettings(req *CopyToSentInput, acc *SMTPAccount) CopyToSent {
	mode := acc.CopyToSent
	if mode == "" {
		mode = CopyAuto
	}
	out := CopyToSent{Path: acc.CopyToSentPath, State: CopyPending}
	switch mode {
	case CopyNever:
		out.Enabled = false
	case CopyAlways:
		out.Enabled = true
	default:
		out.Enabled = !FilesSentMail(acc.Host)
	}
	if req != nil {
		if req.Enabled != nil {
			out.Enabled = *req.Enabled
		}
		if req.Path != "" {
			out.Path = req.Path
		}
	}
	if !out.Enabled {
		out.State = CopySkipped
	}
	return out
}

// Get returns one message of the caller's organisation.
func (s *Service) Get(ctx context.Context, id string) (*Message, error) {
	if _, ok := tenancy.CallerFrom(ctx); !ok {
		return nil, errs.New(errs.CodeUnauthenticated, "Authentication is required.")
	}
	m, err := s.Repo.get(ctx, id)
	if err != nil {
		return nil, err
	}
	// An account-bound or account-scoped key reads its own account's messages only (like List).
	if err := tenancy.RequireReadAccount(ctx, "outbound", m.AccountID); err != nil {
		return nil, err
	}
	return m, nil
}

// Cancel cancels a scheduled or queued message (S2 §4.3).
func (s *Service) Cancel(ctx context.Context, id, ifMatch string) (*Message, error) {
	m, err := s.Repo.get(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := tenancy.RequireAccount(ctx, tenancy.PermSend, m.AccountID); err != nil {
		return nil, err
	}
	if err := checkIfMatch(ifMatch, m.Version); err != nil {
		return nil, err
	}
	if slices.Contains(FinalStates, m.State) || m.State == StateSending {
		return nil, errs.New(errs.CodeOutboundNotCancellable, "Only scheduled or queued messages can be cancelled.").
			WithSafe("state", m.State)
	}
	if err := s.Repo.cancel(ctx, m); err != nil {
		return nil, err
	}
	return s.Repo.get(ctx, id)
}

// List returns the outbox page of the caller's organisation.
func (s *Service) List(ctx context.Context, f ListFilter) (*List, error) {
	if err := tenancy.RequireRead(ctx, "outbound"); err != nil {
		return nil, err
	}
	if f.State != "" && !validState(f.State) {
		return nil, errs.New(errs.CodeValidationFailed, "Unknown outbound state.").WithSafe("state", f.State)
	}
	if c, ok := tenancy.CallerFrom(ctx); ok && c.Kind == tenancy.CallerAPIKey && c.AccountID != "" {
		if f.AccountID != "" && f.AccountID != c.AccountID {
			return nil, errs.New(errs.CodeForbidden, "The API key is bound to a different account.")
		}
		f.AccountID = c.AccountID
	}
	if ids, limited := tenancy.ScopedIDs(ctx, "accounts"); limited {
		if len(ids) == 0 {
			return &List{Data: []View{}}, nil
		}
		if f.AccountID != "" {
			if !slices.Contains(ids, f.AccountID) {
				return &List{Data: []View{}}, nil
			}
		} else {
			f.AccountIDs = ids
		}
	}
	return s.Repo.list(ctx, f)
}

// buildRaw builds a raw submission (S6 §2.3). Without an explicit envelope the recipients come from the To,
// Cc and Bcc headers; with one (SMTP submission), RCPT TO decides who receives the message. Either way the
// visible recipients are the To / Cc header addresses that are also envelope recipients, and every other
// envelope recipient is a blind copy: kept like a structured Bcc (never in the API view, added to RCPT TO and,
// for header-addressed APIs, as a Bcc header), while the transmitted copy carries no Bcc header.
func buildRaw(req SubmitRequest) (*mime.Built, Envelope, []string, error) {
	raw, err := base64.StdEncoding.DecodeString(req.Raw)
	if err != nil {
		return nil, Envelope{}, nil, errs.New(errs.CodeValidationFailed, "raw must be base64-encoded RFC 822.")
	}
	var explicit *mime.Envelope
	if req.Envelope != nil {
		to := make([]string, 0, len(req.Envelope.To))
		for _, a := range req.Envelope.To {
			if a = strings.TrimSpace(a); a != "" {
				to = append(to, a)
			}
		}
		if len(to) == 0 {
			return nil, Envelope{}, nil, errs.New(errs.CodeNoRecipients, "envelope.to needs at least one recipient.")
		}
		explicit = &mime.Envelope{From: strings.TrimSpace(req.Envelope.From), Recipients: to}
	}
	built, err := mime.BuildRaw(raw, explicit)
	if err != nil {
		return nil, Envelope{}, nil, err
	}
	pending := map[string]bool{}
	for _, r := range built.Envelope.Recipients {
		pending[strings.ToLower(r)] = true
	}
	take := func(header []string) []string {
		var out []string
		for _, a := range header {
			if k := strings.ToLower(a); pending[k] {
				out = append(out, a)
				delete(pending, k)
			}
		}
		return out
	}
	env := Envelope{From: built.Envelope.From, To: take(built.HeaderTo), Cc: take(built.HeaderCc)}
	var bcc []string
	for _, r := range built.Envelope.Recipients {
		if pending[strings.ToLower(r)] {
			bcc = append(bcc, r)
			delete(pending, strings.ToLower(r))
		}
	}
	env.BccCount = len(bcc)
	return built, env, bcc, nil
}
