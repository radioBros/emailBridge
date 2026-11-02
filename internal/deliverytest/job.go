package deliverytest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/jobs"
	"github.com/radiobros/emailbridge/internal/msgauth"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/submit"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

// RegisterHandlers wires the deliveryTest job kind.
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
			return nil, errs.New(errs.CodeValidationFailed, "Invalid delivery-test payload.")
		}
	}
	if p.TestID == "" {
		return nil, errs.New(errs.CodeValidationFailed, "testId is required.")
	}
	org := j.OrganizationID
	t, err := s.Repo.GetByID(ctx, org, p.TestID)
	if err != nil {
		return nil, err
	}
	if t.State == StateSucceeded || t.State == StateFailed || t.State == StateTimedOut {
		return ViewOf(t), nil // idempotent re-run
	}
	t.State = StateRunning
	t.JobID = j.ID
	if err := s.Repo.replace(ctx, t); err != nil {
		return nil, err
	}

	runCtx := tenancy.WithCaller(store.WithOrganization(ctx, org), tenancy.Caller{
		Kind: tenancy.CallerSystem, OrganizationID: org,
	})
	started := s.now()
	subject := fmt.Sprintf("EmailBridge delivery test %s", t.ID)
	res, err := s.Submit.Submit(runCtx, t.AccountID, submit.SubmitRequest{
		To:      []submit.AddressInput{{Address: t.To}},
		Subject: subject,
		Text:    "EmailBridge delivery-test probe. Safe to delete.",
	}, "", false)
	if err != nil {
		return nil, s.fail(ctx, t, err)
	}
	if res.Accepted == nil {
		return nil, s.fail(ctx, t, errs.New(errs.CodeInternalError, "Probe submit was not accepted."))
	}
	t.OutboundID = res.Accepted.ID
	mid := res.Accepted.MessageID
	t.MessageID = mid
	_ = s.Repo.replace(ctx, t)

	deadline := started.Add(t.Timeout)
	if t.Timeout <= 0 {
		deadline = started.Add(defaultTimeout)
	}
	var hit *Hit
	for {
		if r.Cancelled() {
			return nil, s.fail(ctx, t, errs.New(errs.CodeConflict, "The delivery test job was cancelled."))
		}
		if s.now().After(deadline) {
			return nil, s.timeout(ctx, t)
		}
		hit, err = s.find(ctx, t.AccountID, mid)
		if err != nil {
			return nil, s.fail(ctx, t, err)
		}
		if hit != nil {
			break
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(s.pollEvery()):
		}
	}

	finished := s.now()
	t.State = StateSucceeded
	t.Folder = classifyFolder(hit.MailboxPath, hit.SpecialUse)
	t.MailboxPath = hit.MailboxPath
	t.LatencyMs = finished.Sub(started).Milliseconds()
	t.FinishedAt = &finished
	if raw := s.loadRaw(ctx, t.AccountID, hit.MessageID, hit.ArchiveRef); len(raw) > 0 {
		t.AuthResults = authFromRaw(raw, s.TrustedAR)
	}
	if err := s.Repo.replace(ctx, t); err != nil {
		return nil, err
	}
	return ViewOf(t), nil
}

func (s *Service) find(ctx context.Context, accountID, messageIDHeader string) (*Hit, error) {
	if s.Find != nil {
		return s.Find(ctx, accountID, messageIDHeader)
	}
	if s.Repo == nil || s.Repo.DB == nil {
		return nil, nil
	}
	return FindByMessageID(ctx, s.Repo.DB, accountID, messageIDHeader)
}

func (s *Service) loadRaw(ctx context.Context, accountID, messageID, archiveRef string) []byte {
	if s.LoadRaw == nil {
		return nil
	}
	b, err := s.LoadRaw(ctx, accountID, messageID, archiveRef)
	if err != nil {
		return nil
	}
	return b
}

func (s *Service) fail(ctx context.Context, t *Test, cause error) error {
	finished := s.now()
	t.State = StateFailed
	t.FinishedAt = &finished
	t.Error = errs.ProblemFrom(cause, "", "")
	t.ErrorJSON = encodeProblem(t.Error)
	_ = s.Repo.replace(ctx, t)
	return cause
}

func (s *Service) timeout(ctx context.Context, t *Test) error {
	finished := s.now()
	t.State = StateTimedOut
	t.FinishedAt = &finished
	cause := errs.New(errs.CodeDeliveryTestTimeout, "The probe message did not appear in the account index before the timeout.")
	t.Error = errs.ProblemFrom(cause, "", "")
	t.ErrorJSON = encodeProblem(t.Error)
	_ = s.Repo.replace(ctx, t)
	return cause
}

func authFromRaw(raw []byte, trusted []string) *msgauth.Results {
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return nil
	}
	h := msgauth.MapHeaders(msg.Header)
	if len(trusted) == 0 {
		// Prefer any Authentication-Results authserv-id present.
		for _, v := range h.Values("Authentication-Results") {
			if i := strings.IndexByte(v, ';'); i > 0 {
				trusted = append(trusted, strings.TrimSpace(v[:i]))
			}
		}
	}
	got, err := msgauth.FromHeaders(h, trusted)
	if err != nil {
		return nil
	}
	return got
}
