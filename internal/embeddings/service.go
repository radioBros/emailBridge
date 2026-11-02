package embeddings

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/radiobros/emailbridge/internal/accounts"
	"github.com/radiobros/emailbridge/internal/ai"
	"github.com/radiobros/emailbridge/internal/audit"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/fulltext"
	"github.com/radiobros/emailbridge/internal/jobs"
	"github.com/radiobros/emailbridge/internal/logging"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

// AI is what this package needs from the AI module (*ai.Service implements it).
type AI interface {
	// AIEnabled reports the `ai.enabled` switch for the account.
	AIEnabled(ctx context.Context, org, accountID string) bool
	// EmbeddingsModel is the model of the organisation's usable embeddings endpoint ("" = none).
	EmbeddingsModel(ctx context.Context, org string) (string, error)
	// Embed embeds at most ai.MaxEmbedInputs inputs in one request.
	Embed(ctx context.Context, org string, inputs []string) (*ai.Embedding, error)
	// Complete calls the main provider.
	Complete(ctx context.Context, org, system, user string, maxChars int) (string, error)
}

// Defaults of the tunables.
const (
	defaultRequestsPerMinute = 60 // background embedding requests per organisation and node
	defaultRequestBurst      = 10
	defaultSearchPerMinute   = 30 // per user (or API key)
	defaultSearchBurst       = 10
	defaultAskPerMinute      = 10
	defaultAskBurst          = 3
	defaultQueueSize         = 5000
	defaultWorkers           = 2
	defaultMaxTries          = 4
	defaultRetryBase         = 5 * time.Second
)

// Service is the embedder, semantic search, ask and the reindex job.
type Service struct {
	DB   *store.DB
	Repo *Repo
	AI   AI
	// Switches resolves `ai.embeddings` (the G3 resolver with Name = SwitchName); FullText is the
	// `search.fullText` resolver of the full-text service.
	Switches *fulltext.Switches
	FullText *fulltext.Switches
	Jobs     *jobs.Service
	Audit    *audit.Writer
	Log      *slog.Logger
	Now      func() time.Time

	// MaxChunks caps the chunks compared per query (default 50 000).
	MaxChunks int
	// RequestsPerMinute limits background embedding requests per organisation on this node (default 60,
	// burst 10; negative = no limit). Search and ask are limited per user instead.
	RequestsPerMinute float64
	// SearchPerMinute / AskPerMinute limit interactive calls per user or API key (defaults 30 and 10;
	// negative = no limit).
	SearchPerMinute, AskPerMinute float64
	// QueueSize bounds the embed queue (default 5000); Workers embed from it (default 2).
	QueueSize, Workers int
	// RetryBase is the first retry delay of a failed embedding (default 5 s, doubled per try, 4 tries).
	RetryBase time.Duration
	// SweepEvery is the purge / backfill sweep interval (default 15 min).
	SweepEvery time.Duration

	initOnce  sync.Once
	orgRL     *limiter
	searchRL  *limiter
	askRL     *limiter
	queue     chan task
	lastDrop  time.Time
	dropMu    sync.Mutex
	dropCount int
}

func (s *Service) init() {
	s.initOnce.Do(func() {
		rpm := s.RequestsPerMinute
		if rpm == 0 {
			rpm = defaultRequestsPerMinute
		}
		s.orgRL = newLimiter(rpm, defaultRequestBurst)
		sp := s.SearchPerMinute
		if sp == 0 {
			sp = defaultSearchPerMinute
		}
		s.searchRL = newLimiter(sp, defaultSearchBurst)
		ap := s.AskPerMinute
		if ap == 0 {
			ap = defaultAskPerMinute
		}
		s.askRL = newLimiter(ap, defaultAskBurst)
		n := s.QueueSize
		if n <= 0 {
			n = defaultQueueSize
		}
		s.queue = make(chan task, n)
	})
}

func (s *Service) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s *Service) maxChunks() int {
	if s.MaxChunks > 0 {
		return s.MaxChunks
	}
	return DefaultMaxChunks
}

// systemCtx is the context of background work for an account: tenant scope, a system caller and the
// correlation IDs.
func systemCtx(ctx context.Context, orgID, accountID string) context.Context {
	ctx = store.WithOrganization(ctx, orgID)
	ctx = tenancy.WithCaller(ctx, tenancy.Caller{Kind: tenancy.CallerSystem, OrganizationID: orgID})
	return logging.With(ctx, func(ids *logging.IDs) { ids.OrganizationID, ids.AccountID = orgID, accountID })
}

// state resolves whether semantic search is available for an account and with which model. fresh skips the
// switch caches (purges and jobs decide on the current value).
func (s *Service) state(ctx context.Context, org, acc string, fresh bool) (Reason, string, error) {
	on, err := s.switchOn(ctx, s.Switches, org, acc, fresh)
	if err != nil {
		return "", "", errs.Wrap(err, errs.CodeInternalError, "embeddings.switch")
	}
	if !on {
		return ReasonSwitchOff, "", nil
	}
	if on, err = s.switchOn(ctx, s.FullText, org, acc, fresh); err != nil {
		return "", "", errs.Wrap(err, errs.CodeInternalError, "embeddings.fulltext_switch")
	}
	if !on {
		return ReasonFullTextOff, "", nil
	}
	if !s.AI.AIEnabled(ctx, org, acc) {
		return ReasonAIOff, "", nil
	}
	model, err := s.AI.EmbeddingsModel(ctx, org)
	if err != nil {
		return "", "", err
	}
	if model == "" {
		return ReasonNotConfigured, "", nil
	}
	return ReasonNone, model, nil
}

func (s *Service) switchOn(ctx context.Context, sw *fulltext.Switches, org, acc string, fresh bool) (bool, error) {
	if sw == nil {
		return false, nil
	}
	if fresh {
		return sw.AccountEnabledFresh(ctx, org, acc)
	}
	return sw.AccountEnabled(ctx, org, acc)
}

// unavailable is the API error of a Reason.
func unavailable(r Reason) error {
	switch r {
	case ReasonNotConfigured:
		return errs.New(errs.CodeValidationFailed, "Embeddings are not configured in the AI settings.").WithSafe("reason", string(r))
	case ReasonFullTextOff:
		return errs.New(errs.CodeFeatureDisabled, "Semantic search needs full-text search on for this account.").
			WithSafe("switch", fulltext.SwitchName).WithSafe("reason", string(r))
	case ReasonAIOff:
		return errs.New(errs.CodeFeatureDisabled, "The AI plugin is disabled for this account.").
			WithSafe("switch", "ai.enabled").WithSafe("reason", string(r))
	default:
		return errs.New(errs.CodeFeatureDisabled, "Semantic search is switched off for this account.").
			WithSafe("switch", SwitchName).WithSafe("reason", string(r))
	}
}

// ready checks that the account exists in the caller's organisation and that semantic search is available,
// and returns the organisation and the model.
func (s *Service) ready(ctx context.Context, acc string) (string, string, error) {
	org, ok := store.OrganizationFrom(ctx)
	if !ok {
		return "", "", store.ErrNoTenant
	}
	if err := s.accountUsable(ctx, org, acc); err != nil {
		return "", "", err
	}
	reason, model, err := s.state(ctx, org, acc, false)
	if err != nil {
		return "", "", err
	}
	if reason != ReasonNone {
		return "", "", unavailable(reason)
	}
	return org, model, nil
}

// accountUsable returns not_found for an account outside the organisation and account_deleting for one
// being deleted.
func (s *Service) accountUsable(ctx context.Context, org, acc string) error {
	var d struct {
		State string `bson:"state"`
	}
	err := s.DB.Collection(accounts.Collection).FindOne(ctx, bson.M{"_id": acc, "organizationId": org},
		options.FindOne().SetProjection(bson.M{"state": 1})).Decode(&d)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return errs.New(errs.CodeNotFound, "The account does not exist.").WithSafe("accountId", acc)
	}
	if err != nil {
		return errs.Wrap(err, errs.CodeInternalError, "embeddings.account")
	}
	if d.State == accounts.StateDeleting {
		return errs.New(errs.CodeAccountDeleting, "The account is being deleted.")
	}
	return nil
}

// limitUser applies a per-user (or per-key) limit to an interactive call.
func (s *Service) limitUser(ctx context.Context, l *limiter, org string) error {
	actor := "anonymous"
	if c, ok := tenancy.CallerFrom(ctx); ok {
		actor = c.Actor()
	}
	if ok, wait := l.allow(org+"/"+actor, s.now()); !ok {
		return errs.New(errs.CodeRateLimited, "Too many semantic searches or questions; try again shortly.").
			WithRetryAfter(max(wait, time.Second))
	}
	return nil
}

// Status is GET /v1/accounts/{accountId}/embeddings: whether semantic search is available and how much of
// the account is embedded.
func (s *Service) Status(ctx context.Context, acc string) (*Status, error) {
	if err := tenancy.RequireReadAccount(ctx, "messages", acc); err != nil {
		return nil, err
	}
	org, ok := store.OrganizationFrom(ctx)
	if !ok {
		return nil, store.ErrNoTenant
	}
	if err := s.accountUsable(ctx, org, acc); err != nil {
		return nil, err
	}
	reason, model, err := s.state(ctx, org, acc, false)
	if err != nil {
		return nil, err
	}
	if model == "" && reason != ReasonNotConfigured {
		// Unavailable for the account but configured: still report what is stored (until a purge removes it).
		if model, err = s.AI.EmbeddingsModel(ctx, org); err != nil {
			return nil, err
		}
	}
	st := &Status{AccountID: acc, Enabled: reason == ReasonNone, Reason: reason, Model: model, MaxChunks: s.maxChunks()}
	if model != "" {
		base := bson.M{"organizationId": org, "accountId": acc, "model": model}
		if st.Chunks, err = s.Repo.Count(ctx, base, 0); err != nil {
			return nil, err
		}
		if st.Messages, err = s.Repo.Count(ctx, bson.M{"organizationId": org, "accountId": acc, "model": model, "chunk": 0}, 0); err != nil {
			return nil, err
		}
	}
	n, err := s.DB.Collection(fulltext.Collection).CountDocuments(ctx, bson.M{"organizationId": org, "accountId": acc})
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "embeddings.status")
	}
	st.IndexedMessages = n
	return st, nil
}

// PurgeAccount deletes every chunk of an account.
func (s *Service) PurgeAccount(ctx context.Context, acc string) (int64, error) {
	n, err := s.Repo.DeleteAccount(ctx, acc)
	if err != nil {
		return 0, err
	}
	if s.Switches != nil {
		s.Switches.Forget(acc)
	}
	if n > 0 {
		s.log().InfoContext(ctx, "embeddings of the account purged", "accountId", acc, "chunks", n)
	}
	return n, nil
}

// ModelChanged is ai.Service.EmbeddingsChanged: the organisation's embedding model changed (or the endpoint
// became unusable), so the vectors of other models are deleted. It runs in the background: a large
// organisation may have many chunks.
func (s *Service) ModelChanged(ctx context.Context, org string) {
	ctx = logging.With(context.WithoutCancel(ctx), func(ids *logging.IDs) { ids.OrganizationID = org })
	go func() {
		ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
		defer cancel()
		model, err := s.AI.EmbeddingsModel(ctx, org)
		if err != nil {
			s.log().WarnContext(ctx, "embeddings model change: settings unreadable; the sweep retries", "err", err)
			return
		}
		n, err := s.Repo.DeleteStale(ctx, org, "", model)
		if err != nil {
			s.log().WarnContext(ctx, "embeddings model change: purge failed; the sweep retries", "err", err)
			return
		}
		s.log().InfoContext(ctx, "embeddings of the previous model deleted", "model", model, "chunks", n)
	}()
}
