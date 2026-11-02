package cluster

import (
	"context"
	"slices"
	"strings"

	"github.com/radiobros/emailbridge/internal/audit"
	"github.com/radiobros/emailbridge/internal/bus"
	"github.com/radiobros/emailbridge/internal/config"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/queues"
)

// QueueView is one work stream in GET /v1/queues. The raw stream statistics (pending, ackPending, consumers,
// keep) are for the instance owner only; items are the W72 record counts of the caller's scope.
type QueueView struct {
	Name        string         `json:"name" doc:"JetStream stream name." example:"EMAILBRIDGE_DELIVERY"`
	Kind        string         `json:"kind" enum:"sending,webhooks,systemMail,events" doc:"What the queue carries: outbound messages, webhook deliveries, system mail or events." example:"webhooks"`
	Cancellable bool           `json:"cancellable" doc:"Whether its records can be listed and cancelled on the queue page (false for events)." example:"true"`
	Pending     *uint64        `json:"pending,omitempty" doc:"Stream messages waiting to be processed, every organisation (instance owner only)." example:"1284"`
	AckPending  *int           `json:"ackPending,omitempty" doc:"Stream messages handed to a consumer and not yet acknowledged (instance owner only)." example:"16"`
	Consumers   *int           `json:"consumers,omitempty" doc:"Number of consumers on the stream (instance owner only)." example:"3"`
	Paused      bool           `json:"paused" doc:"Whether processing is paused cluster-wide." example:"false"`
	Keep        string         `json:"keep,omitempty" doc:"Message retention; absent when unlimited (instance owner only)." example:"7d"`
	Items       *queues.Counts `json:"items,omitempty" doc:"Record counts of the scope: the active organisation, or every organisation with scope=all. Absent for events, and for system mail unless the caller is the instance owner."`
}

// redact drops the instance-wide stream statistics for callers that are not the instance owner.
func (q *QueueView) redact() {
	q.Pending, q.AckPending, q.Consumers, q.Keep = nil, nil, nil, ""
}

// Queues lists the JetStream work streams of S4 §1 with live stats and persisted pause / keep.
func (s *Service) Queues(ctx context.Context) ([]QueueView, error) {
	doc, err := s.readQueueDoc(ctx)
	if err != nil {
		return nil, errs.New(errs.CodeInternalError, "Could not read queue settings.").WithSafe("error", err.Error())
	}
	out := make([]QueueView, 0, len(bus.WorkStreams()))
	for _, name := range bus.WorkStreams() {
		st, err := s.Bus.QueueStats(ctx, name)
		if err != nil {
			return nil, errs.New(errs.CodeInternalError, "Could not read queue stats.").WithSafe("queue", name).WithSafe("error", err.Error())
		}
		pending, ack, consumers := st.Pending, st.AckPending, st.Consumers
		q := QueueView{
			Name: name, Kind: queues.KindOf(name), Cancellable: queues.Cancellable(name),
			Pending: &pending, AckPending: &ack, Consumers: &consumers, Paused: st.Paused, Keep: st.Keep,
		}
		if saved, ok := doc.Queues[name]; ok {
			q.Paused = saved.Paused || st.Paused
			if saved.Keep != "" {
				q.Keep = saved.Keep
			}
		}
		out = append(out, q)
	}
	return out, nil
}

// PatchQueue applies pause / keep for one work stream.
func (s *Service) PatchQueue(ctx context.Context, name string, paused *bool, keep *string) (*QueueView, error) {
	if !slices.Contains(bus.WorkStreams(), name) {
		return nil, errs.New(errs.CodeNotFound, "Unknown queue.").WithSafe("queue", name)
	}
	doc, err := s.readQueueDoc(ctx)
	if err != nil {
		return nil, errs.New(errs.CodeInternalError, "Could not read queue settings.").WithSafe("error", err.Error())
	}
	cur := doc.Queues[name]
	if paused != nil {
		cur.Paused = *paused
		if err := s.Bus.PublishQueuePaused(name, *paused); err != nil {
			return nil, errs.New(errs.CodeInternalError, "Could not broadcast queue pause.").WithSafe("error", err.Error())
		}
	}
	if keep != nil {
		k := strings.TrimSpace(*keep)
		if k == "" {
			return nil, errs.New(errs.CodeValidationFailed, "keep must be a duration (e.g. 7d).")
		}
		if _, err := config.ParseDuration(k); err != nil {
			return nil, errs.New(errs.CodeValidationFailed, "keep must be a duration (e.g. 7d).").WithSafe("error", err.Error())
		}
		cur.Keep = k
		if err := s.Bus.SetStreamMaxAge(ctx, name, k); err != nil {
			return nil, errs.New(errs.CodeInternalError, "Could not update queue retention.").WithSafe("error", err.Error())
		}
	}
	doc.Queues[name] = cur
	doc.Version++
	if err := s.saveQueueDoc(ctx, doc); err != nil {
		return nil, errs.New(errs.CodeInternalError, "Could not persist queue settings.").WithSafe("error", err.Error())
	}
	if s.Audit != nil {
		_ = s.Audit.Record(ctx, "queue.patch", audit.Target{Type: "queue", ID: name}, nil)
	}
	list, err := s.Queues(ctx)
	if err != nil {
		return nil, err
	}
	for i := range list {
		if list[i].Name == name {
			return &list[i], nil
		}
	}
	return &QueueView{Name: name, Kind: queues.KindOf(name), Cancellable: queues.Cancellable(name), Paused: cur.Paused, Keep: cur.Keep}, nil
}
