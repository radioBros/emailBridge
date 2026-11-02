package submit

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math/rand/v2"
	"net"
	"strconv"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/radiobros/emailbridge/internal/bus"
	"github.com/radiobros/emailbridge/internal/egress"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/events"
	eventtypes "github.com/radiobros/emailbridge/internal/events/types"
	"github.com/radiobros/emailbridge/internal/logging"
	"github.com/radiobros/emailbridge/internal/mailproto/smtpc"
	"github.com/radiobros/emailbridge/internal/submit/policy"
)

const (
	// heartbeatEvery is how often a sending worker refreshes heartbeatAt (S6 §3.7).
	heartbeatEvery = 10 * time.Second
	// staleAfter is when a sending message counts as abandoned by its worker.
	staleAfter = 3 * heartbeatEvery
	// busyRetry is how long a job waits when the account has no free connection (S4 §4).
	busyRetry = 2 * time.Second
	// dueEvery and sweepEvery are the periods of the scheduler duties.
	dueEvery   = 15 * time.Second
	sweepEvery = 30 * time.Second
	// dueBatch bounds one duty pass.
	dueBatch = 500
)

// errBusyElsewhere keeps a job waiting while another worker is still sending the message.
var errBusyElsewhere = errors.New("submit: another worker is sending this message")

// abort is the error of a stage gate that stopped an attempt without an outcome: nothing is recorded, the
// message stays in `sending` and the job is redelivered — exactly what a crashed worker leaves behind
// (S6 §3.7). The integration tests use it to crash a worker at any stage.
type abort struct{ err error }

func (a *abort) Error() string { return a.err.Error() }
func (a *abort) Unwrap() error { return a.err }

// Work handles one job of EMAILBRIDGE_SUBMIT (S6 §3).
func (s *Service) Work(ctx context.Context, msg bus.Message) error {
	var j job
	if err := json.Unmarshal(msg.Data, &j); err != nil {
		return bus.DiscardMessage(errs.Wrap(err, errs.CodeMalformedRequest, "submit.job"))
	}
	if j.OutboundID == "" || j.OrganizationID == "" {
		return bus.DiscardMessage(errs.New(errs.CodeMalformedRequest, "The submit job is incomplete."))
	}
	ctx = logging.With(ctx, func(id *logging.IDs) {
		id.OutboundID, id.AccountID, id.OrganizationID = j.OutboundID, j.AccountID, j.OrganizationID
	})
	m, err := s.Repo.forWorker(ctx, j.OrganizationID, j.OutboundID)
	if err != nil {
		if errs.CodeOf(err) == errs.CodeNotFound {
			return nil // erased or never committed: nothing to send
		}
		return err
	}
	switch m.State {
	case StateQueued:
		ctx, span := logging.StartSpan(ctx, "smtp.send", m.TraceParent, trace.SpanKindClient,
			attribute.Int("eb.attempt", len(m.Attempts)+1))
		defer span.End()
		err := s.attempt(ctx, m)
		if err != nil {
			span.SetStatus(codes.Error, "attempt not finished")
		}
		return err
	case StateSending:
		if !s.isStale(m) {
			return bus.DelayRetry(heartbeatEvery, errBusyElsewhere)
		}
		return s.recoverStale(ctx, m)
	default:
		// scheduled (not due yet), a final state, or cancelled: the job has nothing to do.
		return nil
	}
}

func (s *Service) isStale(m *Message) bool {
	return m.HeartbeatAt == nil || m.HeartbeatAt.Before(s.now().Add(-staleAfter))
}

// attempt claims the message and makes one SMTP attempt.
func (s *Service) attempt(ctx context.Context, m *Message) error {
	claimed, err := s.Repo.claim(ctx, m.OrganizationID, m.ID, s.NodeID, s.now())
	if err != nil {
		return err
	}
	if claimed == nil {
		return nil // cancelled or already taken: ack
	}
	m = claimed
	acc, err := s.Accounts.SMTP(ctx, m.AccountID)
	if err != nil {
		return s.failBeforeSession(ctx, m, err)
	}
	if acc == nil || acc.OrganizationID != m.OrganizationID {
		return s.failBeforeSession(ctx, m, errs.New(errs.CodeNotFound, "The account does not exist."))
	}
	acc, err = s.applyGateway(ctx, m, acc)
	if err != nil {
		return s.failBeforeSession(ctx, m, err)
	}
	if done, err := s.applyThrottleBefore(ctx, m, acc); done {
		return err
	}
	var rewriteTo []string
	if s.Sandbox != nil {
		handled, rewrite, err := s.Sandbox.Gate(ctx, m, func() (io.ReadCloser, error) { return s.raw(ctx, m) })
		if err != nil {
			return s.failBeforeSession(ctx, m, err)
		}
		if handled {
			return nil // the gate wrote the final state and event (captured)
		}
		rewriteTo = rewrite
	}
	if done, reject, err := s.applyThrottleAfter(ctx, m, acc, rewriteTo); done {
		if reject != nil {
			return s.failBeforeSession(ctx, m, reject)
		}
		return err
	}
	body, err := s.raw(ctx, m)
	if err != nil {
		return s.failBeforeSession(ctx, m, err)
	}

	stopHeartbeat := s.heartbeat(ctx, m)
	defer stopHeartbeat()

	if acc.APISend != nil {
		started := s.now()
		idx, aErr := s.Repo.startAttempt(ctx, m, Attempt{At: started, Stage: string(smtpc.StageConnect), Rewritten: len(rewriteTo) > 0})
		if aErr != nil {
			_ = body.Close()
			return aErr
		}
		return s.sendAPI(ctx, m, acc, body, rewriteTo, started, idx)
	}
	switch acc.Provider {
	case providerGmail, providerGraph:
		started := s.now()
		idx, aErr := s.Repo.startAttempt(ctx, m, Attempt{At: started, Stage: string(smtpc.StageConnect), Rewritten: len(rewriteTo) > 0})
		if aErr != nil {
			_ = body.Close()
			return aErr
		}
		if acc.Provider == providerGraph {
			return s.sendGraph(ctx, m, acc, body, rewriteTo, started, idx)
		}
		return s.sendGmail(ctx, m, acc, body, rewriteTo, started, idx)
	}
	defer func() { _ = body.Close() }()

	lease, err := s.Pool.Acquire(ctx, acc)
	if errors.Is(err, ErrPoolBusy) {
		if rErr := s.requeue(ctx, m, busyRetry); rErr != nil {
			return rErr
		}
		return bus.DelayRetry(busyRetry, ErrPoolBusy)
	}
	started := s.now()
	stage := string(smtpc.StageConnect)
	if err != nil {
		stage, _ = policy.StageAndReply(err)
	}
	idx, aErr := s.Repo.startAttempt(ctx, m, Attempt{
		At: started, Stage: stage, Rewritten: len(rewriteTo) > 0,
	})
	if aErr != nil {
		lease.Discard()
		return aErr
	}
	if err != nil {
		return s.finish(ctx, m, acc, idx, started, nil, err)
	}

	res, err := lease.Session.Send(ctx, s.envelopeWith(m, rewriteTo), body, smtpc.Hooks{
		Stage: func(ctx context.Context, st smtpc.Stage) error {
			if err := s.Repo.setStage(ctx, m.ID, idx, string(st)); err != nil {
				return &abort{err: err}
			}
			if s.OnStage == nil {
				return nil
			}
			if err := s.OnStage(ctx, m, string(st)); err != nil {
				return &abort{err: err}
			}
			return nil
		},
	})
	if err != nil {
		lease.Discard()
	} else {
		lease.Release()
	}
	var stopped *abort
	if errors.As(err, &stopped) {
		// No outcome was observed: leave the message in sending and let the job come back.
		return stopped.err
	}
	return s.finish(ctx, m, acc, idx, started, res, err)
}

// raw opens the built message in object storage.
func (s *Service) raw(ctx context.Context, m *Message) (io.ReadCloser, error) {
	body, _, err := s.Objects.Get(ctx, m.RawRef)
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "submit.raw")
	}
	return body, nil
}

// envelopeWith builds the SMTP envelope, optionally replacing every RCPT TO (sandbox rewrite, S6 §7).
func (s *Service) envelopeWith(m *Message, rewriteTo []string) smtpc.Envelope {
	to := rewriteTo
	if len(to) == 0 {
		to = m.Envelope.Recipients(m.Bcc)
	}
	env := smtpc.Envelope{From: m.Envelope.From, To: to, Size: m.Size, UTF8: needsUTF8(m.Envelope.From, to)}
	if m.DSN != nil {
		env.DSN = &smtpc.DSN{Notify: m.DSN.Notify, Ret: m.DSN.Ret, EnvID: m.DSN.EnvID}
	}
	return env
}

// needsUTF8 reports whether any address needs SMTPUTF8 (non-ASCII mailbox or domain).
func needsUTF8(from string, to []string) bool {
	for _, a := range append([]string{from}, to...) {
		for _, r := range a {
			if r > 0x7f {
				return true
			}
		}
	}
	return false
}

// heartbeat refreshes heartbeatAt every heartbeatEvery until the returned stop is called (S6 §3.7).
func (s *Service) heartbeat(ctx context.Context, m *Message) func() {
	done := make(chan struct{})
	beat := context.WithoutCancel(ctx)
	go func() {
		t := time.NewTicker(heartbeatEvery)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				if err := s.Repo.heartbeat(beat, m.ID, s.NodeID, s.now()); err != nil {
					s.log().WarnContext(beat, "cannot refresh the send heartbeat", "error", err)
				}
			}
		}
	}()
	return func() { close(done) }
}

// requeue puts a claimed message back without counting an attempt (pool pressure, send windows in P5).
func (s *Service) requeue(ctx context.Context, m *Message, in time.Duration) error {
	next := s.now().Add(in)
	_, err := s.Repo.coll().UpdateOne(ctx, bson.M{"_id": m.ID, "state": StateSending},
		bson.M{
			"$set":   bson.M{"state": StateQueued, "nextAttemptAt": next},
			"$inc":   bson.M{"version": 1},
			"$unset": bson.M{"worker": "", "heartbeatAt": ""},
		})
	return err
}

// failBeforeSession records a failure that happened before any SMTP command (account, sandbox, storage).
func (s *Service) failBeforeSession(ctx context.Context, m *Message, cause error) error {
	started := s.now()
	idx, err := s.Repo.startAttempt(ctx, m, Attempt{At: started, Stage: string(smtpc.StageConnect)})
	if err != nil {
		return err
	}
	return s.finish(ctx, m, nil, idx, started, nil, cause)
}

// recoverStale applies the crash rule of S6 §3.7 to a message whose worker died: before `data` nothing
// was sent, so it counts as a temporary failure; at `data` or `dataEnd` the body may have been accepted.
func (s *Service) recoverStale(ctx context.Context, m *Message) error {
	started := s.now()
	idx := len(m.Attempts) - 1
	stage := string(smtpc.StageConnect)
	if last := m.LastAttempt(); last != nil {
		stage = last.Stage
		started = last.At
	} else {
		var err error
		if idx, err = s.Repo.startAttempt(ctx, m, Attempt{At: started, Stage: stage}); err != nil {
			return err
		}
	}
	kind, code := errs.KindTemporary, errs.CodeSMTPTemporary
	detail := "The worker stopped before the message was sent."
	if stage == string(smtpc.StageData) || stage == string(smtpc.StageDataEnd) {
		kind, code = errs.KindOutcomeUnknown, errs.CodeSMTPOutcomeUnknown
		detail = "The worker stopped while the message was being sent; the outcome is unknown."
	}
	s.log().WarnContext(ctx, "recovering a message abandoned by its worker", "stage", stage, "kind", string(kind))
	acc, _ := s.Accounts.SMTP(ctx, m.AccountID)
	return s.apply(ctx, m, acc, idx, Attempt{
		At: started, Stage: stage, Kind: string(kind),
		LatencyMs: s.now().Sub(started).Milliseconds(),
	}, kind, code, errs.New(code, detail))
}

// finish records the outcome of one attempt: a success, or a classified failure with its policy.
func (s *Service) finish(ctx context.Context, m *Message, acc *SMTPAccount, idx int, started time.Time,
	res *smtpc.Result, cause error) error {
	latency := s.now().Sub(started).Milliseconds()
	if cause == nil {
		return s.sent(ctx, m, acc, idx, started, res, latency)
	}
	stage, reply := policy.StageAndReply(cause)
	kind, code := policy.Classify(stage, reply, cause)
	a := Attempt{
		At: started, Stage: stage, Code: reply.Code, Enhanced: reply.Enhanced,
		Response: excerpt(reply.Text), Kind: string(kind), LatencyMs: latency,
	}
	return s.apply(ctx, m, acc, idx, a, kind, code, cause)
}

// sent commits the success and emits message.sent in the same transaction, then copies to Sent (S6 §3.5).
func (s *Service) sent(ctx context.Context, m *Message, acc *SMTPAccount, idx int, started time.Time,
	res *smtpc.Result, latency int64) error {
	now := s.now()
	rejected := rejections(res)
	a := Attempt{
		At: started, Stage: string(smtpc.StageDataEnd), Code: 250, Kind: string(policy.KindSuccess),
		Response: excerpt(responseOf(res)), LatencyMs: latency, Rejected: rejected,
	}
	err := s.Repo.DB.WithTx(ctx, func(txCtx context.Context) error {
		if err := s.Repo.finishAttempt(txCtx, m.ID, idx, a,
			bson.M{"state": StateSent, "sentAt": now}, "worker", "heartbeatAt", "nextAttemptAt"); err != nil {
			return err
		}
		_, err := s.Events.Emit(txCtx, events.Draft{
			Type: events.MessageSent, OrganizationID: m.OrganizationID, AccountID: m.AccountID,
			Subject: m.ID, Time: now,
			Data: eventtypes.MessageSent{
				OutboundRef: outboundRef(m, acc), SentAt: now, Attempt: idx + 1,
				SMTP:     eventtypes.SMTPReply{Host: hostOf(acc), Code: 250, Response: a.Response},
				Rejected: eventRejections(rejected),
			},
		})
		return err
	})
	if err != nil {
		return err
	}
	m.State, m.SentAt = StateSent, &now
	s.log().InfoContext(ctx, "message sent", "attempt", idx+1, "rejected", len(rejected), "latencyMs", latency)
	s.copyToSent(ctx, m, now)
	return nil
}

// apply writes the outcome of a failed attempt and the state the policy asks for, with its event, in one
// transaction (S6 §3.6, §6.2).
func (s *Service) apply(ctx context.Context, m *Message, acc *SMTPAccount, idx int, a Attempt,
	kind errs.Kind, code errs.Code, cause error) error {
	attemptNo := idx + 1
	set := policy.Settings{
		Policy: m.Policy, MaxAttempts: m.MaxAttempts, BackoffBase: s.backoffBase(m, acc),
		BackoffJitter: s.BackoffJitter, Rand: s.rand(),
	}
	decision := policy.Apply(set, kind, attemptNo, 0)
	now := s.now()
	sendError := eventtypes.SendError{
		Code: string(code), Kind: string(kind), Stage: a.Stage,
		SMTPCode: a.Code, Enhanced: a.Enhanced, Response: a.Response,
	}
	ref := outboundRef(m, acc)

	var state string
	var draft events.Draft
	fields := bson.M{}
	unset := []string{"worker", "heartbeatAt"}
	switch {
	case decision.Retry:
		next := now.Add(decision.Delay)
		state = StateQueued
		fields["nextAttemptAt"] = next
		draft = events.Draft{
			Type: events.MessageDeliveryAttemptFailed, OrganizationID: m.OrganizationID, AccountID: m.AccountID,
			Subject: m.ID, Time: now,
			Data: eventtypes.MessageDeliveryAttemptFailed{
				OutboundRef: ref, Attempt: attemptNo, MaxAttempts: set.MaxAttempts,
				NextAttemptAt: next, Error: sendError,
			},
		}
	default:
		state = StateFailed
		if decision.OutcomeUnknown {
			state = StateOutcomeUnknown
		}
		unset = append(unset, "nextAttemptAt")
		fields["finalError"] = problemJSON(errs.New(code, detailOf(cause)))
		draft = events.Draft{
			Type: events.MessageFailed, OrganizationID: m.OrganizationID, AccountID: m.AccountID,
			Subject: m.ID, Time: now,
			Data: eventtypes.MessageFailed{
				OutboundRef: ref, Attempts: attemptNo, OutcomeUnknown: decision.OutcomeUnknown, Error: sendError,
			},
		}
	}
	fields["state"] = state
	drafts := []events.Draft{draft}
	if kind == errs.KindAuth {
		// SMTP credentials were refused: report it without touching the IMAP state (S6 §4).
		drafts = append(drafts, events.Draft{
			Type: events.AccountAuthFailed, OrganizationID: m.OrganizationID, AccountID: m.AccountID,
			Subject: m.AccountID, Time: now,
			Data: eventtypes.AccountAuthFailed{
				AccountRef: ref.AccountRef, Protocol: "smtp", Code: string(code),
				Response: a.Response, FailingSince: now,
			},
		})
	}
	if err := s.Repo.DB.WithTx(ctx, func(txCtx context.Context) error {
		if err := s.Repo.finishAttempt(txCtx, m.ID, idx, a, fields, unset...); err != nil {
			return err
		}
		_, err := s.Events.Emit(txCtx, drafts...)
		return err
	}); err != nil {
		return err
	}
	m.State = state
	s.log().WarnContext(ctx, "send attempt failed", "attempt", attemptNo, "stage", a.Stage,
		"kind", string(kind), "code", string(code), "smtpCode", a.Code, "state", state,
		"retryIn", decision.Delay.String())
	return nil
}

// copyToSent appends the sent message to the Sent folder, with up to CopyMaxAttempts tries. A failure
// never changes the sent state (S6 §5).
func (s *Service) copyToSent(ctx context.Context, m *Message, sentAt time.Time) {
	if !m.CopyToSent.Enabled {
		return
	}
	if s.Sent == nil {
		if err := s.Repo.setCopyState(ctx, m.ID, CopySkipped, 0, ""); err != nil {
			s.log().WarnContext(ctx, "cannot record the copy-to-Sent result", "error", err)
		}
		return
	}
	var last error
	for attempt := 1; attempt <= CopyMaxAttempts; attempt++ {
		body, err := s.raw(ctx, m)
		if err == nil {
			err = s.Sent.CopyToSent(ctx, m.AccountID, m.ID, body, sentAt)
			_ = body.Close()
		}
		if err == nil {
			if err := s.Repo.setCopyState(ctx, m.ID, CopyDone, attempt, ""); err != nil {
				s.log().WarnContext(ctx, "cannot record the copy-to-Sent result", "error", err)
			}
			return
		}
		last = err
		if attempt < CopyMaxAttempts {
			select {
			case <-ctx.Done():
			case <-time.After(s.copyDelay()):
			}
		}
	}
	code := string(errs.CodeOf(last))
	s.log().WarnContext(ctx, "the copy to the Sent folder failed", "code", code, "error", last)
	if err := s.Repo.setCopyState(ctx, m.ID, CopyFailed, CopyMaxAttempts, code); err != nil {
		s.log().WarnContext(ctx, "cannot record the copy-to-Sent result", "error", err)
	}
}

func (s *Service) copyDelay() time.Duration {
	if s.CopyRetryDelay > 0 {
		return s.CopyRetryDelay
	}
	return 2 * time.Second
}

func (s *Service) backoffBase(m *Message, acc *SMTPAccount) time.Duration {
	if d, err := time.ParseDuration(m.BackoffBase); err == nil && d > 0 {
		return d
	}
	if acc != nil && acc.BackoffBase > 0 {
		return acc.BackoffBase
	}
	return s.BackoffBase
}

func (s *Service) rand() func() float64 {
	if s.Rand != nil {
		return s.Rand
	}
	return func() float64 { return rand.Float64() } //nolint:gosec // G404: jitter, not security
}

// applyGateway overlays gateway dial settings when the message or account selects a gateway (P5.3).
func (s *Service) applyGateway(ctx context.Context, m *Message, acc *SMTPAccount) (*SMTPAccount, error) {
	gwy := m.GatewayID
	if gwy == "" {
		gwy = acc.GatewayID
	}
	if gwy == "" {
		return acc, nil
	}
	if s.Gateways == nil {
		return nil, errs.New(errs.CodeGatewayUnavailable, "SMTP gateways are not configured.")
	}
	g, err := s.Gateways.ResolveSMTP(ctx, m.OrganizationID, gwy)
	if err != nil {
		return nil, err
	}
	out := *acc
	out.GatewayID = g.GatewayID
	out.APISend = g.APISend
	out.Host, out.Port, out.TLS = g.Host, g.Port, g.TLS
	out.User, out.Password = g.User, g.Password
	out.IgnoreCertErrors = g.IgnoreCertErrors
	if g.Policy != "" {
		out.Policy = g.Policy
	}
	if g.MaxAttempts > 0 {
		out.MaxAttempts = g.MaxAttempts
	}
	if g.BackoffBase > 0 {
		out.BackoffBase = g.BackoffBase
	}
	if g.EHLO != "" {
		out.EHLO = g.EHLO
	}
	if g.Proxy != "" {
		out.Proxy = g.Proxy
	}
	if g.LocalAddress != "" {
		out.LocalAddress = g.LocalAddress
	}
	return &out, nil
}

// dial builds the SMTP session options of an account and opens the session (S6 §4).
func (s *Service) dial(dialer *egress.Dialer) func(context.Context, *SMTPAccount) (*smtpc.Session, error) {
	return func(ctx context.Context, acc *SMTPAccount) (*smtpc.Session, error) {
		o := smtpc.SessionOptions{
			Addr: net.JoinHostPort(acc.Host, strconv.Itoa(acc.Port)), TLS: smtpc.TLSMode(acc.TLS),
			InsecureSkipVerify: acc.IgnoreCertErrors, Username: acc.User, Password: acc.Password,
			EHLO: ehloName(acc.EHLO, s.EHLO),
		}
		if acc.Token != nil && acc.GatewayID == "" { // OAuth account (gateways use their own credentials)
			tok, user, err := acc.Token(ctx)
			if err != nil {
				return nil, err
			}
			o.Password, o.AccessToken = "", tok
			if user != "" {
				o.Username = user
			}
		}
		if dialer != nil {
			proxy, local := acc.Proxy, acc.LocalAddress
			if local == "" && s.IPPool != nil {
				local = s.IPPool.ResolveLocal(acc.OrganizationID, acc.ID, "smtp", acc.LocalAddress, acc.LocalAddresses, acc.SMTPStrategy)
			} else if local != "" && s.IPPool != nil {
				local = s.IPPool.ResolveLocal(acc.OrganizationID, acc.ID, "smtp", acc.LocalAddress, nil, "")
			}
			o.Dial = dialer.MailDialFunc(egress.MailNetwork{Proxy: proxy, LocalAddr: local})
		}
		return smtpc.Open(ctx, o)
	}
}

func ehloName(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return "localhost"
}

// Due publishes every message whose time has come; it also recovers messages the API could not publish
// (S6 §2.7, §6.2). JetStream deduplicates on outboundId/attemptNo, so republishing is free.
func (s *Service) Due(ctx context.Context) {
	tick(ctx, dueEvery, func() {
		rows, err := s.Repo.due(ctx, s.now(), dueBatch)
		if err != nil {
			s.log().WarnContext(ctx, "cannot read due outbound messages", "error", err)
			return
		}
		for i := range rows {
			m := &rows[i]
			if m.State == StateScheduled {
				if err := s.Repo.queue(ctx, m.ID); err != nil {
					s.log().WarnContext(ctx, "cannot queue a scheduled message", "error", err)
					continue
				}
			}
			s.publish(ctx, m, len(m.Attempts))
		}
	})
}

// Sweep hands messages whose worker died back to the state machine (S6 §3.7).
func (s *Service) Sweep(ctx context.Context) {
	tick(ctx, sweepEvery, func() {
		rows, err := s.Repo.stale(ctx, s.now().Add(-staleAfter), dueBatch)
		if err != nil {
			s.log().WarnContext(ctx, "cannot read stale sending messages", "error", err)
			return
		}
		for i := range rows {
			m := &rows[i]
			mctx := logging.With(ctx, func(id *logging.IDs) {
				id.OutboundID, id.AccountID, id.OrganizationID = m.ID, m.AccountID, m.OrganizationID
			})
			if err := s.recoverStale(mctx, m); err != nil {
				s.log().WarnContext(mctx, "cannot recover a stale sending message", "error", err)
			}
		}
	})
}

func tick(ctx context.Context, every time.Duration, fn func()) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		fn()
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// ---- small helpers ----

func excerpt(text string) string {
	text = strings.TrimSpace(text)
	if len(text) > ResponseMax {
		return text[:ResponseMax]
	}
	return text
}

// detailOf is the redacted, human-readable reason of a failure (never raw internals).
func detailOf(err error) string { return errs.ProblemFrom(err, "", "").Detail }

func responseOf(res *smtpc.Result) string {
	if res == nil {
		return ""
	}
	return res.Response
}

func rejections(res *smtpc.Result) []Reject {
	if res == nil || len(res.Rejected) == 0 {
		return nil
	}
	out := make([]Reject, 0, len(res.Rejected))
	for _, r := range res.Rejected {
		out = append(out, Reject{Address: r.Address, Code: r.Code, Enhanced: r.Enhanced, Message: excerpt(r.Message)})
	}
	return out
}

func eventRejections(in []Reject) []eventtypes.Rejection {
	if len(in) == 0 {
		return nil
	}
	out := make([]eventtypes.Rejection, 0, len(in))
	for _, r := range in {
		out = append(out, eventtypes.Rejection{Address: r.Address, Code: r.Code, Enhanced: r.Enhanced, Message: r.Message})
	}
	return out
}

func outboundRef(m *Message, acc *SMTPAccount) eventtypes.OutboundRef {
	ref := eventtypes.OutboundRef{
		AccountRef:      eventtypes.AccountRef{AccountID: m.AccountID},
		OutboundID:      m.ID,
		MessageIDHeader: m.MessageIDHeader,
		Envelope:        eventtypes.Envelope{From: m.Envelope.From, To: m.Envelope.To},
		Subject:         m.Subject,
	}
	if acc != nil {
		ref.ExternalID = acc.ExternalID
	}
	if ref.Envelope.To == nil {
		ref.Envelope.To = []string{}
	}
	return ref
}

func hostOf(acc *SMTPAccount) string {
	if acc == nil {
		return ""
	}
	return acc.Host
}
