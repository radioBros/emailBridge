package queues

import (
	"regexp"
	"slices"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/submit"
	"github.com/radiobros/emailbridge/internal/sysmail"
	"github.com/radiobros/emailbridge/internal/tenancy"
	"github.com/radiobros/emailbridge/internal/webhook"
)

// source describes one cancellable queue: the collection of its domain records, how queue states map to
// stored states, and the domain's own cancellable filter and cancel update (the same the single cancel uses).
type source struct {
	queue      string
	kind       string // queue kind (GET /v1/queues)
	itemKind   string
	collection string
	idPrefix   store.Prefix
	// orgScoped records carry organizationId; system mail does not (instance owner only).
	orgScoped bool
	// readScope is the RequireRead scope of the organisation-scope list; writePerm the cancel permission.
	readScope string
	writePerm tenancy.Permission
	// states are the queue states in display order; match maps one to its stored-record filter.
	states      []string
	match       func(state string) bson.M
	listed      bson.M // every listed record (no state filter)
	cancellable bson.M // the domain's cancellable records
	inFlight    []string
	final       []string // stored states that are final (a cancel reports reason final)
	update      func(now time.Time) bson.M
	attempts    any // projection expression for the attempt count
	stateOf     func(stored string, attempts int) string
	search      func(re bson.Regex) bson.M
	hasAccount  bool
	hasEndpoint bool
	hasSystem   bool // outbound messages flagged system: true (excluded from the organisation scope)
	counts      func(c *Counts, stored string, retried bool, n int64)
}

func arraySize(field string) any {
	return bson.M{"$size": bson.M{"$ifNull": bson.A{"$" + field, bson.A{}}}}
}

func in(states ...string) bson.M { return bson.M{"state": bson.M{"$in": states}} }

var sendingSource = &source{
	queue: QueueSending, kind: KindSending, itemKind: ItemOutbound, collection: submit.Collection,
	idPrefix: store.PrefixOutbound, orgScoped: true, readScope: "outbound", writePerm: tenancy.PermSend,
	states: []string{"scheduled", "queued", "retrying", "sending"},
	match: func(state string) bson.M {
		switch state {
		case "scheduled":
			return bson.M{"state": submit.StateScheduled}
		case "queued":
			return bson.M{"state": submit.StateQueued, "attempts.0": bson.M{"$exists": false}}
		case "retrying":
			return bson.M{"state": submit.StateQueued, "attempts.0": bson.M{"$exists": true}}
		case "sending":
			return bson.M{"state": submit.StateSending}
		}
		return nil
	},
	listed:      in(submit.StateScheduled, submit.StateQueued, submit.StateSending),
	cancellable: in(submit.CancellableStates...),
	inFlight:    []string{submit.StateSending},
	final:       submit.FinalStates,
	update:      func(time.Time) bson.M { return submit.CancelUpdate() },
	attempts:    arraySize("attempts"),
	stateOf: func(stored string, attempts int) string {
		if stored == submit.StateQueued && attempts > 0 {
			return "retrying"
		}
		return stored
	},
	search: func(re bson.Regex) bson.M {
		return bson.M{"$or": bson.A{bson.M{"envelope.to": re}, bson.M{"envelope.cc": re}, bson.M{"subject": re}}}
	},
	hasAccount: true, hasSystem: true,
	counts: func(c *Counts, stored string, retried bool, n int64) {
		switch {
		case stored == submit.StateScheduled:
			c.Scheduled += n
		case stored == submit.StateQueued && retried:
			c.Retrying += n
		case stored == submit.StateQueued:
			c.Waiting += n
		case stored == submit.StateSending:
			c.InFlight += n
		}
	},
}

var webhookSource = &source{
	queue: QueueWebhook, kind: KindWebhooks, itemKind: ItemDelivery, collection: webhook.DeliveriesCollection,
	idPrefix: store.PrefixDelivery, orgScoped: true, readScope: "deliveries", writePerm: tenancy.PermWebhooksWrite,
	states: []string{"pending", "retrying", "deadLettered", "delivering"},
	match: func(state string) bson.M {
		switch state {
		case "pending":
			return bson.M{"state": webhook.StatePending}
		case "retrying":
			return bson.M{"state": webhook.StateRetrying}
		case "deadLettered":
			return bson.M{"state": webhook.StateDeadLettered}
		case "delivering":
			return bson.M{"state": webhook.StateDelivering}
		}
		return nil
	},
	listed:      in(webhook.StatePending, webhook.StateRetrying, webhook.StateDeadLettered, webhook.StateDelivering),
	cancellable: in(webhook.CancellableStates...),
	inFlight:    []string{webhook.StateDelivering},
	final:       []string{webhook.StateSucceeded, webhook.StateFailed, webhook.StateCancelled},
	update:      webhook.CancelUpdate,
	attempts:    arraySize("attempts"),
	stateOf:     func(stored string, _ int) string { return stored },
	search:      func(re bson.Regex) bson.M { return bson.M{"eventType": re} },
	hasAccount:  true, hasEndpoint: true,
	counts: func(c *Counts, stored string, _ bool, n int64) {
		switch stored {
		case webhook.StatePending:
			c.Waiting += n
		case webhook.StateRetrying:
			c.Retrying += n
		case webhook.StateDeadLettered:
			c.DeadLettered += n
		case webhook.StateDelivering:
			c.InFlight += n
		}
	},
}

var sysmailSource = &source{
	queue: QueueSysmail, kind: KindSystemMail, itemKind: ItemSysmail, collection: sysmail.RecordsCollection,
	idPrefix: store.PrefixJob, orgScoped: false,
	states: []string{"pending", "retrying", "sending"},
	match: func(state string) bson.M {
		switch state {
		case "pending":
			return bson.M{"state": sysmail.RecordPending, "attempts": bson.M{"$lte": 0}}
		case "retrying":
			return bson.M{"state": sysmail.RecordPending, "attempts": bson.M{"$gt": 0}}
		case "sending":
			return bson.M{"state": sysmail.RecordSending}
		}
		return nil
	},
	listed:      in(sysmail.RecordPending, sysmail.RecordSending),
	cancellable: in(sysmail.CancellableStates...),
	inFlight:    []string{sysmail.RecordSending},
	final:       []string{sysmail.RecordSent, sysmail.RecordFailed, sysmail.RecordDropped, sysmail.RecordCancelled},
	update:      sysmail.CancelUpdate,
	attempts:    bson.M{"$ifNull": bson.A{"$attempts", 0}},
	stateOf: func(stored string, attempts int) string {
		if stored == sysmail.RecordPending && attempts > 0 {
			return "retrying"
		}
		return stored
	},
	search: func(re bson.Regex) bson.M { return bson.M{"to": re} },
	counts: func(c *Counts, stored string, retried bool, n int64) {
		switch {
		case stored == sysmail.RecordPending && retried:
			c.Retrying += n
		case stored == sysmail.RecordPending:
			c.Waiting += n
		case stored == sysmail.RecordSending:
			c.InFlight += n
		}
	},
}

// sources are the cancellable queues by name. EMAILBRIDGE_EVENTS has none: events are facts.
var sources = map[string]*source{
	QueueSending: sendingSource,
	QueueWebhook: webhookSource,
	QueueSysmail: sysmailSource,
}

// KindOf returns the queue kind of a stream name ("" for an unknown stream).
func KindOf(queue string) string {
	if s, ok := sources[queue]; ok {
		return s.kind
	}
	if queue == QueueEvents {
		return KindEvents
	}
	return ""
}

// Cancellable reports whether a queue's records can be cancelled (false for events and unknown names).
func Cancellable(queue string) bool {
	_, ok := sources[queue]
	return ok
}

func (s *source) validState(state string) bool { return slices.Contains(s.states, state) }

func (s *source) isInFlight(stored string) bool { return slices.Contains(s.inFlight, stored) }

// scope is the caller's reach, resolved at request time and frozen into the job payload (the job runs without
// a caller).
type scope struct {
	AllOrgs         bool     `json:"allOrgs,omitempty"`
	OrgID           string   `json:"orgId,omitempty"`
	AccountLimited  bool     `json:"accountLimited,omitempty"`
	AccountIDs      []string `json:"accountIds,omitempty"`
	EndpointLimited bool     `json:"endpointLimited,omitempty"`
	EndpointIDs     []string `json:"endpointIds,omitempty"`
	IncludeSystem   bool     `json:"includeSystem,omitempty"`
}

// scopeClauses are the tenant and narrowing clauses. The organisation itself is added by store.Scoped.
func (s *source) scopeClauses(sc scope) []bson.M {
	var and []bson.M
	if s.hasSystem && !sc.IncludeSystem {
		and = append(and, bson.M{"system": bson.M{"$ne": true}})
	}
	if s.hasAccount && sc.AccountLimited {
		and = append(and, bson.M{"accountId": bson.M{"$in": nonNil(sc.AccountIDs)}})
	}
	if s.hasEndpoint && sc.EndpointLimited {
		and = append(and, bson.M{"endpointId": bson.M{"$in": nonNil(sc.EndpointIDs)}})
	}
	return and
}

// filterClauses are the user's filter clauses (state, account, endpoint, dates, search).
func (s *source) filterClauses(f Filter) []bson.M {
	var and []bson.M
	if f.State != "" {
		and = append(and, s.match(f.State))
	} else {
		and = append(and, s.listed)
	}
	if s.hasAccount && f.AccountID != "" {
		and = append(and, bson.M{"accountId": f.AccountID})
	}
	if s.hasEndpoint && f.EndpointID != "" {
		and = append(and, bson.M{"endpointId": f.EndpointID})
	}
	if f.CreatedFrom != nil || f.CreatedTo != nil {
		rng := bson.M{}
		if f.CreatedFrom != nil {
			rng["$gte"] = f.CreatedFrom.UTC()
		}
		if f.CreatedTo != nil {
			rng["$lt"] = f.CreatedTo.UTC()
		}
		and = append(and, bson.M{"createdAt": rng})
	}
	if f.Q != "" {
		and = append(and, s.search(bson.Regex{Pattern: "^" + regexp.QuoteMeta(f.Q), Options: "i"}))
	}
	return and
}

func andOf(clauses ...[]bson.M) bson.M {
	var all bson.A
	for _, c := range clauses {
		for _, m := range c {
			all = append(all, m)
		}
	}
	if len(all) == 0 {
		return bson.M{}
	}
	return bson.M{"$and": all}
}

func nonNil(ids []string) []string {
	if ids == nil {
		return []string{}
	}
	return ids
}

// projection loads only what an Item shows; the attempt count is computed by the server.
func (s *source) projection() bson.M {
	return bson.M{
		"organizationId": 1, "accountId": 1, "endpointId": 1, "eventType": 1, "state": 1,
		"envelope.to": 1, "envelope.cc": 1, "to": 1, "kind": 1, "subject": 1,
		"nextAttemptAt": 1, "sendAt": 1, "createdAt": 1, "attemptCount": s.attempts,
	}
}

// row is the projected record of any source.
type row struct {
	ID             string `bson:"_id"`
	OrganizationID string `bson:"organizationId"`
	AccountID      string `bson:"accountId"`
	EndpointID     string `bson:"endpointId"`
	EventType      string `bson:"eventType"`
	State          string `bson:"state"`
	Envelope       struct {
		To []string `bson:"to"`
		Cc []string `bson:"cc"`
	} `bson:"envelope"`
	To            string     `bson:"to"`
	Kind          string     `bson:"kind"`
	Subject       string     `bson:"subject"`
	AttemptCount  int        `bson:"attemptCount"`
	NextAttemptAt *time.Time `bson:"nextAttemptAt"`
	SendAt        *time.Time `bson:"sendAt"`
	CreatedAt     time.Time  `bson:"createdAt"`
}

func (s *source) item(r row) Item {
	it := Item{
		ID: r.ID, Queue: s.queue, Kind: s.itemKind, State: s.stateOf(r.State, r.AttemptCount),
		Attempts: r.AttemptCount, NextAttemptAt: utcPtr(r.NextAttemptAt), SendAt: utcPtr(r.SendAt),
		CreatedAt: r.CreatedAt.UTC(),
	}
	if s.orgScoped {
		it.OrganizationID = r.OrganizationID
	}
	it.Cancellable = !s.isInFlight(r.State)
	if !it.Cancellable {
		it.NotCancellableReason = ReasonInFlight
	}
	switch s.itemKind {
	case ItemOutbound:
		it.AccountID, it.Subject = r.AccountID, r.Subject
		visible := append(append([]string{}, r.Envelope.To...), r.Envelope.Cc...)
		if len(visible) > 0 {
			it.Recipient, it.RecipientCount = visible[0], len(visible)
		}
	case ItemDelivery:
		it.AccountID, it.EndpointID, it.EventType = r.AccountID, r.EndpointID, r.EventType
	case ItemSysmail:
		it.Recipient, it.MailKind = r.To, r.Kind
	}
	return it
}

func utcPtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}
