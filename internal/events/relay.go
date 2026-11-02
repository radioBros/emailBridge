package events

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/radiobros/emailbridge/internal/bus"
	"github.com/radiobros/emailbridge/internal/logging"
	"github.com/radiobros/emailbridge/internal/store"
)

// Publisher is the part of *bus.Bus the relay needs.
type Publisher interface {
	Publish(ctx context.Context, subject string, data []byte, msgID string, headers map[string]string) (bool, error)
}

// Relay publishes unpublished events to EMAILBRIDGE_EVENTS (S4 §3): one worker per relay partition held by
// this node (bus.Balancer, leases relay.<p>), in _id order, each publish acknowledged by JetStream before
// the event is marked published. A crash between ack and mark republishes with the same Nats-Msg-Id, which
// JetStream drops inside its dedup window. New events wake the worker through a change stream; a 1 s poll
// covers change-stream outages.
type Relay struct {
	DB         *store.DB
	Pub        Publisher
	Partitions int
	Batch      int           // default 200
	Poll       time.Duration // default 1 s
	Log        *slog.Logger

	mu   sync.Mutex
	wake map[int]chan struct{}
}

func (r *Relay) defaults() {
	if r.Batch <= 0 {
		r.Batch = 200
	}
	if r.Poll <= 0 {
		r.Poll = time.Second
	}
	if r.Partitions <= 0 {
		r.Partitions = 64
	}
	if r.Log == nil {
		r.Log = slog.Default()
	}
	r.mu.Lock()
	if r.wake == nil {
		r.wake = map[int]chan struct{}{}
	}
	r.mu.Unlock()
}

func (r *Relay) wakeCh(p int) chan struct{} {
	r.mu.Lock()
	defer r.mu.Unlock()
	ch, ok := r.wake[p]
	if !ok {
		ch = make(chan struct{}, 1)
		r.wake[p] = ch
	}
	return ch
}

func (r *Relay) kick(p int) {
	select {
	case r.wakeCh(p) <- struct{}{}:
	default:
	}
}

// Watch tails inserts into the events collection and wakes the matching partition worker until ctx ends.
// It restarts the change stream after errors (polling keeps the relay correct meanwhile).
func (r *Relay) Watch(ctx context.Context) {
	r.defaults()
	pipeline := mongo.Pipeline{
		{{Key: "$match", Value: bson.M{"operationType": "insert"}}},
		{{Key: "$project", Value: bson.M{"fullDocument.partition": 1}}},
	}
	for ctx.Err() == nil {
		cs, err := r.DB.Collection(Collection).Watch(ctx, pipeline)
		if err != nil {
			if ctx.Err() == nil {
				r.Log.Warn("event change stream unavailable; relay polls", "err", err)
			}
			sleep(ctx, 5*time.Second)
			continue
		}
		for cs.Next(ctx) {
			var ev struct {
				FullDocument struct {
					Partition int `bson:"partition"`
				} `bson:"fullDocument"`
			}
			if cs.Decode(&ev) == nil {
				r.kick(ev.FullDocument.Partition)
			}
		}
		if err := cs.Err(); err != nil && ctx.Err() == nil {
			r.Log.Warn("event change stream ended; restarting", "err", err)
		}
		_ = cs.Close(context.WithoutCancel(ctx))
		sleep(ctx, time.Second)
	}
}

// Work publishes partition p until ctx ends (bus.Balancer's Work).
func (r *Relay) Work(ctx context.Context, p int, _ uint64) {
	r.defaults()
	t := time.NewTicker(r.Poll)
	defer t.Stop()
	backoff := time.Duration(0)
	for ctx.Err() == nil {
		if _, err := r.Drain(ctx, p); err != nil {
			if ctx.Err() != nil {
				return
			}
			backoff = min(max(backoff*2, 250*time.Millisecond), 10*time.Second)
			r.Log.Warn("event relay publish failed; retrying", "partition", p, "err", err, "backoff", backoff.String())
			sleep(ctx, backoff)
			continue
		}
		backoff = 0
		select {
		case <-ctx.Done():
			return
		case <-r.wakeCh(p):
		case <-t.C:
		}
	}
}

// Drain publishes every unpublished event of partition p, in order, and returns how many it published.
func (r *Relay) Drain(ctx context.Context, p int) (int, error) {
	r.defaults()
	total := 0
	coll := r.DB.Collection(Collection)
	for {
		cur, err := coll.Find(ctx, bson.M{"partition": p, "published": false},
			options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}).SetLimit(int64(r.Batch)).SetHint("outbox"))
		if err != nil {
			return total, err
		}
		var docs []doc
		if err := cur.All(ctx, &docs); err != nil {
			return total, err
		}
		if len(docs) == 0 {
			return total, nil
		}
		ids := make([]string, 0, len(docs))
		var pubErr error
		for _, d := range docs {
			body, err := json.Marshal(d.event())
			if err != nil {
				pubErr = err
				break
			}
			if err := r.publish(ctx, p, d, body); err != nil {
				pubErr = err
				break // keep order: never publish a later event before an earlier one
			}
			ids = append(ids, d.ID)
		}
		if len(ids) > 0 {
			now := time.Now().UTC()
			if _, err := coll.UpdateMany(context.WithoutCancel(ctx), bson.M{"_id": bson.M{"$in": ids}},
				bson.M{"$set": bson.M{"published": true, "publishedAt": now}}); err != nil {
				return total, err // republished later with the same Nats-Msg-Id: deduplicated
			}
			total += len(ids)
		}
		if pubErr != nil {
			return total, pubErr
		}
		if len(docs) < r.Batch {
			return total, nil
		}
	}
}

// publish sends one event under a producer span that continues the event's trace; the NATS message carries
// the span's traceparent header (S12 §10).
func (r *Relay) publish(ctx context.Context, p int, d doc, body []byte) error {
	ctx, span := logging.StartSpan(ctx, "events.publish", d.TraceParent, trace.SpanKindProducer,
		attribute.String("eb.event.type", d.Type), attribute.Int("eb.partition", p))
	defer span.End()
	headers := map[string]string{"Ce-Type": d.Type}
	if tp := logging.TraceParent(ctx); tp != "" {
		headers["traceparent"] = tp
	}
	_, err := r.Pub.Publish(ctx, bus.EventSubject(p, d.OrganizationID, d.Type), body, d.ID, headers)
	if err != nil {
		span.SetStatus(codes.Error, "publish failed")
	}
	return err
}

// Lag is the age of the oldest unpublished event (0 when none): alert when > 30 s (S12).
func (r *Relay) Lag(ctx context.Context) (time.Duration, error) {
	var d doc
	err := r.DB.Collection(Collection).FindOne(ctx, bson.M{"published": false},
		options.FindOne().SetSort(bson.D{{Key: "_id", Value: 1}}).SetProjection(bson.M{"time": 1})).Decode(&d)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return time.Since(d.Time), nil
}

func sleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}
