// Package jobs runs long operations answered with 202 (S2 §2, S1 `jobs`): erasure, resync, exports,
// replays. A job is a document; the runner — a scheduler duty, so one node in the cluster — claims queued
// jobs, runs the handler registered for their kind with a heartbeat, and records the outcome. A job whose
// runner died (stale heartbeat) is queued again, so handlers must be idempotent.
package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

// Collection holds jobs.
const Collection = "jobs"

// States.
const (
	Queued    = "queued"
	Running   = "running"
	Succeeded = "succeeded"
	Failed    = "failed"
	Cancelled = "cancelled"
)

// Progress of a running job.
type Progress struct {
	Done  int64 `json:"done" bson:"done" doc:"Units of work finished." example:"1800"`
	Total int64 `json:"total" bson:"total" doc:"Units of work in total (0 while unknown)." example:"4200"`
}

// Job is S1 `jobs` and the API's Job schema.
type Job struct {
	ID              string          `json:"id" bson:"_id" doc:"Job ID (job_…)." example:"job_01m5cnaee0w96fc46z350nc6q0"`
	OrganizationID  string          `json:"organizationId,omitempty" bson:"organizationId,omitempty" doc:"Organisation the job belongs to; absent for instance-level jobs." example:"org_01m5caqpk0kast63yc13wpy2d9"`
	Kind            string          `json:"kind" bson:"kind" doc:"What the job does, e.g. organization.erasure, erasure (one account), account.export, config.apply, deliveries.replay." example:"organization.erasure"`
	State           string          `json:"state" bson:"state" enum:"queued,running,succeeded,failed,cancelled" doc:"Lifecycle state; succeeded, failed and cancelled are final." example:"running"`
	Progress        *Progress       `json:"progress,omitempty" bson:"progress,omitempty" doc:"Progress reported by the running job."`
	Result          json.RawMessage `json:"result,omitempty" bson:"-" doc:"Kind-specific result once succeeded." example:"{\"accountsErased\":42,\"messagesErased\":181233}"`
	Error           *errs.Problem   `json:"error,omitempty" bson:"-" doc:"Why the job failed (RFC 9457 problem), once failed."`
	CreatedBy       string          `json:"createdBy,omitempty" bson:"createdBy,omitempty" doc:"User or API key that started the job." example:"usr_01m5ccvej0dqefkn0xhkcq2q6z"`
	CreatedAt       time.Time       `json:"createdAt" bson:"createdAt" doc:"When the job was queued." example:"2026-10-24T15:00:00Z"`
	StartedAt       *time.Time      `json:"startedAt,omitempty" bson:"startedAt,omitempty" doc:"When a runner picked it up." example:"2026-10-24T15:00:02Z"`
	FinishedAt      *time.Time      `json:"finishedAt,omitempty" bson:"finishedAt,omitempty" doc:"When it reached a final state." example:"2026-10-24T15:07:41Z"`
	Payload         json.RawMessage `json:"-" bson:"-"`
	ResultText      string          `json:"-" bson:"result,omitempty"`
	ErrorText       string          `json:"-" bson:"error,omitempty"`
	PayloadText     string          `json:"-" bson:"payload,omitempty"`
	Runner          string          `json:"-" bson:"runner,omitempty"`
	HeartbeatAt     *time.Time      `json:"-" bson:"heartbeatAt,omitempty"`
	CancelRequested bool            `json:"-" bson:"cancelRequested,omitempty"`
	Attempts        int             `json:"-" bson:"attempts"`
	ExpireAt        *time.Time      `json:"-" bson:"expireAt,omitempty"`
}

func (j *Job) decodeText() {
	if j.ResultText != "" {
		j.Result = json.RawMessage(j.ResultText)
	}
	if j.PayloadText != "" {
		j.Payload = json.RawMessage(j.PayloadText)
	}
	if j.ErrorText != "" {
		var p errs.Problem
		if json.Unmarshal([]byte(j.ErrorText), &p) == nil {
			j.Error = &p
		}
	}
}

// Reporter lets a handler report progress and notice cancellation.
type Reporter interface {
	Progress(ctx context.Context, done, total int64)
	Cancelled() bool
}

// Handler runs one job. It returns a JSON-marshalable result or an error (an *errs.E becomes the job's
// problem). It must be idempotent: a job is re-run when its runner dies.
type Handler func(ctx context.Context, j *Job, r Reporter) (result any, err error)

// CrashReporter reports recovered panics (optional; wired to observability.Reporter).
type CrashReporter interface {
	CapturePanic(ctx context.Context, recovered any, stack []byte, tags map[string]string)
}

// Service creates and reads jobs and holds the handler registry.
type Service struct {
	DB        *store.DB
	Retention func() time.Duration // EB_RETENTION_JOBS (default 30 days after finishing)
	Log       *slog.Logger
	NodeID    string
	// CrashReporter, when set, receives panics recovered in job handlers (W51).
	CrashReporter CrashReporter

	mu       sync.Mutex
	handlers map[string]Handler
	wake     chan struct{}
}

// Handle registers the handler of a kind (call at module start).
func (s *Service) Handle(kind string, h Handler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.handlers == nil {
		s.handlers = map[string]Handler{}
	}
	s.handlers[kind] = h
}

func (s *Service) handler(kind string) (Handler, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	h, ok := s.handlers[kind]
	return h, ok
}

// Create queues a job in the caller's organisation (store.OrganizationFrom; empty for instance-level jobs)
// with payload marshalled to JSON. It may run inside the caller's transaction (the job then exists only if
// the change commits). The runner is woken on this node; other nodes' runners find it by polling.
func (s *Service) Create(ctx context.Context, kind string, payload any) (*Job, error) {
	var text string
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return nil, errs.Wrap(err, errs.CodeInternalError, "jobs.create")
		}
		text = string(b)
	}
	j := &Job{ID: store.NewID(store.PrefixJob), Kind: kind, State: Queued, CreatedAt: time.Now().UTC().Truncate(time.Millisecond), PayloadText: text}
	if org, ok := store.OrganizationFrom(ctx); ok {
		j.OrganizationID = org
	}
	if c, ok := tenancy.CallerFrom(ctx); ok {
		j.CreatedBy = c.UserID
		if c.Kind == tenancy.CallerAPIKey {
			j.CreatedBy = c.APIKeyID
		}
	}
	if _, err := s.DB.Collection(Collection).InsertOne(ctx, j); err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "jobs.create")
	}
	j.decodeText()
	s.kick()
	return j, nil
}

// Get returns a job of the caller's organisation (instance-level jobs only to callers without one).
func (s *Service) Get(ctx context.Context, id string) (*Job, error) {
	filter := bson.M{"_id": id}
	if org, ok := store.OrganizationFrom(ctx); ok {
		filter["organizationId"] = org
	} else {
		filter["organizationId"] = bson.M{"$exists": false}
	}
	var j Job
	if err := s.DB.Collection(Collection).FindOne(ctx, filter).Decode(&j); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, errs.New(errs.CodeNotFound, "The job does not exist.")
		}
		return nil, errs.Wrap(err, errs.CodeInternalError, "jobs.get")
	}
	j.decodeText()
	return &j, nil
}

// Cancel cancels a queued job, or asks a running one to stop (handlers see Reporter.Cancelled).
func (s *Service) Cancel(ctx context.Context, id string) error {
	j, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	coll := s.DB.Collection(Collection)
	now := time.Now().UTC()
	switch j.State {
	case Queued:
		res, err := coll.UpdateOne(ctx, bson.M{"_id": id, "state": Queued}, bson.M{"$set": bson.M{"state": Cancelled, "finishedAt": now, "expireAt": now.Add(s.retention())}})
		if err != nil {
			return errs.Wrap(err, errs.CodeInternalError, "jobs.cancel")
		}
		if res.ModifiedCount == 1 {
			return nil
		}
		return s.Cancel(ctx, id) // started meanwhile
	case Running:
		_, err := coll.UpdateOne(ctx, bson.M{"_id": id, "state": Running}, bson.M{"$set": bson.M{"cancelRequested": true}})
		if err != nil {
			return errs.Wrap(err, errs.CodeInternalError, "jobs.cancel")
		}
		return nil
	}
	return errs.New(errs.CodeConflict, fmt.Sprintf("The job is already %s.", j.State))
}

func (s *Service) retention() time.Duration {
	if s.Retention != nil && s.Retention() > 0 {
		return s.Retention()
	}
	return 30 * 24 * time.Hour
}

func (s *Service) kick() {
	s.mu.Lock()
	if s.wake == nil {
		s.wake = make(chan struct{}, 1)
	}
	ch := s.wake
	s.mu.Unlock()
	select {
	case ch <- struct{}{}:
	default:
	}
}

func (s *Service) wakeCh() chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.wake == nil {
		s.wake = make(chan struct{}, 1)
	}
	return s.wake
}

// Migration creates the jobs indexes (claim order, tenant reads, TTL after finishing).
func Migration(number int) store.Migration {
	return store.Migration{Number: number, Name: "jobs indexes", Up: func(ctx context.Context, db *store.DB) error {
		return db.EnsureIndexes(ctx, Collection,
			mongo.IndexModel{Keys: bson.D{{Key: "state", Value: 1}, {Key: "_id", Value: 1}}},
			mongo.IndexModel{Keys: bson.D{{Key: "organizationId", Value: 1}, {Key: "_id", Value: -1}}},
			mongo.IndexModel{Keys: bson.D{{Key: "expireAt", Value: 1}}, Options: options.Index().SetExpireAfterSeconds(0)},
		)
	}}
}
