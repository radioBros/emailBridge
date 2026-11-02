package deliverytest

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"time"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/jobs"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/submit"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

const (
	defaultTimeout = 120 * time.Second
	maxTimeout     = 10 * time.Minute
)

// Submitter queues a probe outbound message.
type Submitter interface {
	Submit(ctx context.Context, accountID string, req submit.SubmitRequest, key string, includeRaw bool) (*submit.SubmitResult, error)
}

// Service creates and reads delivery tests.
type Service struct {
	Repo   *Repo
	Jobs   *jobs.Service
	Submit Submitter
	// Env is EB_ENV; when production, To must match TestRecipient when set.
	Env string
	// TestRecipient is the allow-listed address (EB_TEST_MAIL_USER / sandbox rewrite target).
	TestRecipient string
	// PollEvery is the index poll interval (tests inject a short value).
	PollEvery time.Duration
	// Now is injectable for tests.
	Now func() time.Time
	// Find overrides index lookup (tests).
	Find func(ctx context.Context, accountID, messageIDHeader string) (*Hit, error)
	// LoadRaw loads RFC 822 for auth results (optional).
	LoadRaw func(ctx context.Context, accountID, messageID, archiveRef string) ([]byte, error)
	// TrustedAR are Authentication-Results authserv-ids trusted for FromHeaders.
	TrustedAR []string
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s *Service) pollEvery() time.Duration {
	if s.PollEvery > 0 {
		return s.PollEvery
	}
	return 500 * time.Millisecond
}

// Create starts a delivery test job.
func (s *Service) Create(ctx context.Context, in CreateInput) (*Accepted, error) {
	if err := tenancy.RequireAccount(ctx, tenancy.PermSend, in.AccountID); err != nil {
		return nil, err
	}
	if !store.IsID(store.PrefixAccount, in.AccountID) {
		return nil, errs.New(errs.CodeNotFound, "The account does not exist.")
	}
	to := strings.TrimSpace(in.To)
	if to == "" || !strings.Contains(to, "@") {
		return nil, errs.New(errs.CodeValidationFailed, "to must be an email address.")
	}
	if err := s.checkRecipient(to); err != nil {
		return nil, err
	}
	timeout, err := parseTimeout(in.Timeout)
	if err != nil {
		return nil, err
	}
	org, ok := store.OrganizationFrom(ctx)
	if !ok {
		return nil, store.ErrNoTenant
	}
	if s.Jobs == nil || s.Submit == nil {
		return nil, errs.New(errs.CodeInternalError, "Delivery tests are not configured.")
	}
	now := s.now().Truncate(time.Millisecond)
	t := &Test{
		ID: store.NewID(store.PrefixDeliveryTest), OrganizationID: org, AccountID: in.AccountID,
		To: to, State: StateQueued, Timeout: timeout, CreatedAt: now,
	}
	if c, ok := tenancy.CallerFrom(ctx); ok {
		t.CreatedBy = c.Actor()
	}
	if err := s.Repo.insert(ctx, t); err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "deliverytest.create")
	}
	job, err := s.Jobs.Create(ctx, JobKind, jobPayload{TestID: t.ID})
	if err != nil {
		return nil, err
	}
	t.JobID = job.ID
	_ = s.Repo.replace(ctx, t)
	return &Accepted{ID: t.ID, JobID: job.ID, State: StateQueued}, nil
}

// Get returns one delivery test.
func (s *Service) Get(ctx context.Context, id string) (*Test, error) {
	if err := tenancy.RequireRead(ctx, "outbound"); err != nil {
		return nil, err
	}
	if !store.IsID(store.PrefixDeliveryTest, id) {
		return nil, errs.New(errs.CodeNotFound, "The delivery test does not exist.")
	}
	return s.Repo.get(ctx, id)
}

func (s *Service) checkRecipient(to string) error {
	env := s.Env
	if env == "" {
		env = os.Getenv("EB_ENV")
	}
	if env != "production" {
		return nil
	}
	want := strings.TrimSpace(s.TestRecipient)
	if want == "" {
		want = strings.TrimSpace(os.Getenv("EB_TEST_MAIL_USER"))
	}
	if want == "" {
		return nil
	}
	if !strings.EqualFold(to, want) {
		return errs.New(errs.CodeValidationFailed, "Delivery tests in production may only target the configured test mailbox.")
	}
	return nil
}

func parseTimeout(raw string) (time.Duration, error) {
	if strings.TrimSpace(raw) == "" {
		return defaultTimeout, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return 0, errs.New(errs.CodeValidationFailed, "timeout must be a positive Go duration.")
	}
	if d > maxTimeout {
		return 0, errs.New(errs.CodeValidationFailed, "timeout must be at most 10m.")
	}
	return d, nil
}

func encodeProblem(p *errs.Problem) string {
	if p == nil {
		return ""
	}
	b, _ := json.Marshal(p)
	return string(b)
}
