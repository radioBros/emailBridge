package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/logging"
	"github.com/radiobros/emailbridge/internal/store"
)

// Runner settings.
const (
	DefaultConcurrency = 4
	heartbeatEvery     = 10 * time.Second
	staleAfter         = 60 * time.Second
	maxAttempts        = 5
)

// Run claims and runs jobs until ctx ends (a scheduler duty). Running jobs are cancelled with ctx and
// re-queued by the next runner when their heartbeat goes stale.
func (s *Service) Run(ctx context.Context, concurrency int) {
	if concurrency <= 0 {
		concurrency = DefaultConcurrency
	}
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	defer wg.Wait()
	poll := time.NewTicker(time.Second)
	defer poll.Stop()
	lastSweep := time.Time{}
	for ctx.Err() == nil {
		if time.Since(lastSweep) > staleAfter/2 {
			s.requeueStale(ctx)
			lastSweep = time.Now()
		}
		for ctx.Err() == nil {
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			j, err := s.claim(ctx)
			if err != nil || j == nil {
				<-sem
				if err != nil && ctx.Err() == nil {
					s.log().Warn("job claim failed", "err", err)
				}
				break
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer func() { <-sem }()
				s.execute(ctx, j)
			}()
		}
		select {
		case <-ctx.Done():
			return
		case <-poll.C:
		case <-s.wakeCh():
		}
	}
}

func (s *Service) log() *slog.Logger {
	if s.Log == nil {
		return slog.Default()
	}
	return s.Log
}

// kinds lists the job kinds this node has handlers for: each node runs a runner and claims only those, so
// handlers registered by role-specific modules (accounts on api nodes, …) run where they exist.
func (s *Service) kinds() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.handlers))
	for k := range s.handlers {
		out = append(out, k)
	}
	return out
}

// claim moves the oldest queued job (of a kind this node handles) to running.
func (s *Service) claim(ctx context.Context) (*Job, error) {
	now := time.Now().UTC()
	var j Job
	kinds := s.kinds()
	if len(kinds) == 0 {
		return nil, nil
	}
	err := s.DB.Collection(Collection).FindOneAndUpdate(ctx,
		bson.M{"state": Queued, "kind": bson.M{"$in": kinds}},
		bson.M{"$set": bson.M{"state": Running, "startedAt": now, "heartbeatAt": now, "runner": s.NodeID}, "$inc": bson.M{"attempts": 1}},
		options.FindOneAndUpdate().SetSort(bson.D{{Key: "_id", Value: 1}}).SetReturnDocument(options.After),
	).Decode(&j)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	j.decodeText()
	return &j, nil
}

// requeueStale puts running jobs whose runner stopped heartbeating back in the queue (or fails them after
// maxAttempts).
func (s *Service) requeueStale(ctx context.Context) {
	coll := s.DB.Collection(Collection)
	cutoff := time.Now().UTC().Add(-staleAfter)
	if _, err := coll.UpdateMany(ctx, bson.M{"state": Running, "heartbeatAt": bson.M{"$lt": cutoff}, "attempts": bson.M{"$lt": maxAttempts}},
		bson.M{"$set": bson.M{"state": Queued}, "$unset": bson.M{"runner": "", "heartbeatAt": ""}}); err != nil && ctx.Err() == nil {
		s.log().Warn("stale job sweep failed", "err", err)
	}
	p := errs.ProblemFrom(errs.New(errs.CodeInternalError, "The job was interrupted too many times."), "", "")
	text, _ := json.Marshal(p)
	now := time.Now().UTC()
	_, _ = coll.UpdateMany(ctx, bson.M{"state": Running, "heartbeatAt": bson.M{"$lt": cutoff}, "attempts": bson.M{"$gte": maxAttempts}},
		bson.M{"$set": bson.M{"state": Failed, "error": string(text), "finishedAt": now, "expireAt": now.Add(s.retention())}})
}

type reporter struct {
	s         *Service
	id        string
	cancelled atomic.Bool
}

func (r *reporter) Progress(ctx context.Context, done, total int64) {
	_, _ = r.s.DB.Collection(Collection).UpdateOne(ctx, bson.M{"_id": r.id, "state": Running},
		bson.M{"$set": bson.M{"progress": Progress{Done: done, Total: total}}})
}

func (r *reporter) Cancelled() bool { return r.cancelled.Load() }

func (s *Service) execute(ctx context.Context, j *Job) {
	ctx = logging.With(ctx, func(ids *logging.IDs) { ids.OrganizationID, ids.JobID = j.OrganizationID, j.ID })
	if j.OrganizationID != "" {
		ctx = store.WithOrganization(ctx, j.OrganizationID)
	}
	h, ok := s.handler(j.Kind)
	rep := &reporter{s: s, id: j.ID}
	jctx, cancel := context.WithCancel(ctx)
	defer cancel()
	// heartbeat + cancellation watch
	hbDone := make(chan struct{})
	go func() {
		defer close(hbDone)
		t := time.NewTicker(heartbeatEvery)
		defer t.Stop()
		for {
			select {
			case <-jctx.Done():
				return
			case <-t.C:
			}
			var cur Job
			err := s.DB.Collection(Collection).FindOneAndUpdate(jctx, bson.M{"_id": j.ID, "state": Running, "runner": s.NodeID},
				bson.M{"$set": bson.M{"heartbeatAt": time.Now().UTC()}}).Decode(&cur)
			if errors.Is(err, mongo.ErrNoDocuments) { // taken over or finished elsewhere: stop
				cancel()
				return
			}
			if err == nil && cur.CancelRequested {
				rep.cancelled.Store(true)
				cancel() // the handler's ctx ends; it returns and the job is recorded as cancelled
				return
			}
		}
	}()
	var result any
	var err error
	if !ok {
		err = errs.New(errs.CodeInternalError, "No handler is registered for this job kind.").WithSafe("kind", j.Kind)
	} else {
		result, err = s.runSafely(jctx, h, j, rep)
	}
	cancel()
	<-hbDone
	if ctx.Err() != nil {
		return // node stopping: the job stays running and is re-queued when its heartbeat goes stale
	}
	s.finish(ctx, j, result, err, rep.Cancelled())
}

func (s *Service) runSafely(ctx context.Context, h Handler, j *Job, r Reporter) (result any, err error) {
	return runSafelyWith(ctx, h, j, r, s.CrashReporter)
}

func runSafelyWith(ctx context.Context, h Handler, j *Job, r Reporter, crash CrashReporter) (result any, err error) {
	defer func() {
		if rec := recover(); rec != nil {
			stack := debug.Stack()
			err = errs.New(errs.CodeInternalError, "The job crashed.")
			if crash != nil {
				tags := map[string]string{"component": "jobs", "kind": j.Kind}
				if j.ID != "" {
					tags["jobId"] = j.ID
				}
				crash.CapturePanic(ctx, rec, stack, tags)
			}
		}
	}()
	return h(ctx, j, r)
}

func (s *Service) finish(ctx context.Context, j *Job, result any, err error, cancelled bool) {
	now := time.Now().UTC()
	set := bson.M{"finishedAt": now, "expireAt": now.Add(s.retention())}
	switch {
	case err == nil:
		set["state"] = Succeeded
		if result != nil {
			if b, merr := json.Marshal(result); merr == nil {
				set["result"] = string(b)
			}
		}
	case cancelled:
		set["state"] = Cancelled
	default:
		set["state"] = Failed
		b, _ := json.Marshal(errs.ProblemFrom(err, "", ""))
		set["error"] = string(b)
		s.log().Warn("job failed", "job_id", j.ID, "kind", j.Kind, "err", err)
	}
	if _, uerr := s.DB.Collection(Collection).UpdateOne(context.WithoutCancel(ctx), bson.M{"_id": j.ID, "runner": s.NodeID}, bson.M{"$set": set}); uerr != nil {
		s.log().Warn("job outcome not recorded; it will be re-run", "job_id", j.ID, "err", uerr)
	}
}

type inlineReporter struct {
	cancelled func() bool
	progress  func(done, total int64)
}

func (r inlineReporter) Progress(_ context.Context, done, total int64) {
	if r.progress != nil {
		r.progress(done, total)
	}
}

func (r inlineReporter) Cancelled() bool { return r.cancelled != nil && r.cancelled() }

// RunNow runs the handler of kind synchronously inside the caller (a job that composes other jobs, e.g.
// organisation erasure running each account's erasure): no queueing, so a composite job can never wait on
// runner slots held by itself. The sub-run is not stored as its own job.
func (s *Service) RunNow(ctx context.Context, kind string, organizationID string, payload any, cancelled func() bool) (any, error) {
	h, ok := s.handler(kind)
	if !ok {
		return nil, errs.New(errs.CodeInternalError, "No handler is registered for this job kind.").WithSafe("kind", kind)
	}
	var text string
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return nil, errs.Wrap(err, errs.CodeInternalError, "jobs.runNow")
		}
		text = string(b)
	}
	j := &Job{ID: store.NewID(store.PrefixJob), OrganizationID: organizationID, Kind: kind, State: Running, CreatedAt: time.Now().UTC(), PayloadText: text}
	j.decodeText()
	if organizationID != "" {
		ctx = store.WithOrganization(ctx, organizationID)
	}
	return runSafelyWith(ctx, h, j, inlineReporter{cancelled: cancelled}, nil)
}
