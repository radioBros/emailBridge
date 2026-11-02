package queues

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/jobs"
	"github.com/radiobros/emailbridge/internal/submit"
	"github.com/radiobros/emailbridge/internal/sysmail"
	"github.com/radiobros/emailbridge/internal/tenancy"
	"github.com/radiobros/emailbridge/internal/webhook"
)

func TestKindsAndCancellable(t *testing.T) {
	for q, want := range map[string]string{
		QueueSending: KindSending, QueueWebhook: KindWebhooks, QueueSysmail: KindSystemMail, QueueEvents: KindEvents, "NOPE": "",
	} {
		if got := KindOf(q); got != want {
			t.Errorf("KindOf(%s) = %q want %q", q, got, want)
		}
	}
	if !Cancellable(QueueSending) || Cancellable(QueueEvents) || Cancellable("NOPE") {
		t.Fatal("Cancellable")
	}
	m := &Module{}
	if m.Name() != "queues" || m.Roles() != nil || m.Service() != nil {
		t.Fatal("module identity")
	}
	if err := m.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestSourceOf(t *testing.T) {
	if _, err := sourceOf(QueueEvents, false); errs.CodeOf(err) != errs.CodeNotFound {
		t.Fatalf("events read: %v", err)
	}
	if _, err := sourceOf(QueueEvents, true); errs.CodeOf(err) != errs.CodeQueueReadOnly {
		t.Fatalf("events write: %v", err)
	}
	if _, err := sourceOf("X", true); errs.CodeOf(err) != errs.CodeNotFound {
		t.Fatalf("unknown: %v", err)
	}
	if s, err := sourceOf(QueueWebhook, true); err != nil || s != webhookSource {
		t.Fatalf("webhook: %v", err)
	}
}

func TestSetHelpers(t *testing.T) {
	if got := intersect(false, nil, []string{"a", "b"}); len(got) != 2 {
		t.Fatalf("intersect unlimited: %v", got)
	}
	if got := intersect(true, []string{"a", "c"}, []string{"a", "b"}); len(got) != 1 || got[0] != "a" {
		t.Fatalf("intersect: %v", got)
	}
	if got := union([]string{"a"}, []string{"a", "b"}); len(got) != 2 {
		t.Fatalf("union: %v", got)
	}
	if got := nonNil(nil); got == nil || len(got) != 0 {
		t.Fatal("nonNil")
	}
	if got := andOf(); len(got) != 0 {
		t.Fatalf("andOf empty: %v", got)
	}
	if scopeName(scope{AllOrgs: true}) != ScopeAll || scopeName(scope{}) != ScopeOrganization {
		t.Fatal("scopeName")
	}
}

func TestValidate(t *testing.T) {
	from, to := time.Now(), time.Now().Add(-time.Hour)
	cases := []struct {
		src *source
		f   Filter
		ok  bool
	}{
		{sendingSource, Filter{State: "retrying"}, true},
		{sendingSource, Filter{State: "pending"}, false},
		{sendingSource, Filter{EndpointID: "whe_x"}, false},
		{sysmailSource, Filter{AccountID: "acc_x"}, false},
		{webhookSource, Filter{CreatedFrom: &from, CreatedTo: &to}, false},
		{webhookSource, Filter{EndpointID: "whe_x", AccountID: "acc_x"}, true},
	}
	for i, c := range cases {
		if err := validate(c.src, c.f); (err == nil) != c.ok {
			t.Errorf("case %d: %v", i, err)
		}
	}
	if !(Filter{}).empty() || (Filter{Q: "a"}).empty() {
		t.Fatal("empty")
	}
}

func TestItemMapping(t *testing.T) {
	now := time.Date(2026, 10, 27, 10, 0, 0, 0, time.FixedZone("CET", 3600))
	r := row{ID: "out_1", OrganizationID: "org_1", AccountID: "acc_1", State: submit.StateQueued, AttemptCount: 1, CreatedAt: now, Subject: "Hi"}
	r.Envelope.Cc = []string{"cc@example.org"}
	it := sendingSource.item(r)
	if it.State != "retrying" || it.Recipient != "cc@example.org" || it.RecipientCount != 1 || !it.Cancellable || it.CreatedAt.Location() != time.UTC {
		t.Fatalf("outbound: %+v", it)
	}
	it = webhookSource.item(row{ID: "dlv_1", OrganizationID: "org_1", State: webhook.StateDelivering, EndpointID: "whe_1", EventType: "message.sent"})
	if it.Cancellable || it.NotCancellableReason != ReasonInFlight || it.EventType != "message.sent" || it.EndpointID != "whe_1" {
		t.Fatalf("delivery: %+v", it)
	}
	it = sysmailSource.item(row{ID: "job_1", OrganizationID: "org_ignored", State: sysmail.RecordPending, To: "a@example.com", Kind: sysmail.KindMFACode})
	if it.OrganizationID != "" || it.Recipient != "a@example.com" || it.MailKind != sysmail.KindMFACode || it.State != "pending" {
		t.Fatalf("sysmail: %+v", it)
	}
	if sysmailSource.stateOf(sysmail.RecordPending, 2) != "retrying" || webhookSource.stateOf("pending", 3) != "pending" {
		t.Fatal("stateOf")
	}
	for _, src := range []*source{sendingSource, webhookSource, sysmailSource} {
		for _, st := range src.states {
			if src.match(st) == nil {
				t.Errorf("%s: no match for %s", src.queue, st)
			}
		}
		if src.match("bogus") != nil {
			t.Errorf("%s: bogus state matched", src.queue)
		}
	}
	c := &Counts{}
	sendingSource.counts(c, submit.StateScheduled, false, 2)
	sendingSource.counts(c, submit.StateQueued, true, 3)
	sendingSource.counts(c, submit.StateQueued, false, 4)
	sendingSource.counts(c, submit.StateSending, true, 1)
	webhookSource.counts(c, webhook.StateDeadLettered, true, 5)
	webhookSource.counts(c, webhook.StatePending, false, 1)
	webhookSource.counts(c, webhook.StateRetrying, true, 1)
	webhookSource.counts(c, webhook.StateDelivering, true, 1)
	sysmailSource.counts(c, sysmail.RecordPending, true, 1)
	sysmailSource.counts(c, sysmail.RecordPending, false, 1)
	sysmailSource.counts(c, sysmail.RecordSending, false, 1)
	if c.Scheduled != 2 || c.Retrying != 5 || c.Waiting != 6 || c.InFlight != 3 || c.DeadLettered != 5 {
		t.Fatalf("counts: %+v", c)
	}
}

type failHub struct{}

func (failHub) Publish(string, string, string, any, map[string]string) error {
	return errors.New("down")
}

func TestServiceDefaults(t *testing.T) {
	s := &Service{}
	if s.now().IsZero() || s.log() == nil {
		t.Fatal("defaults")
	}
	fixed := time.Date(2026, 10, 27, 10, 0, 0, 0, time.UTC)
	s.Now = func() time.Time { return fixed }
	if !s.now().Equal(fixed) {
		t.Fatal("injected now")
	}
	s.record(context.Background(), "queue.items.cancelled", QueueSending, nil) // nil audit: no-op
	s.publish("org_1", Change{Queue: QueueSending})                            // nil hub: no-op
	s.Hub = failHub{}
	s.publish("org_1", Change{Queue: QueueSending}) // error is logged, not returned
	s.publish("", Change{Queue: QueueSending})      // no organisation: skipped
}

func TestDecodePayloadAndAuthorize(t *testing.T) {
	var p cancelJob
	if err := decodePayload(&jobs.Job{}, &p); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("empty: %v", err)
	}
	if err := decodePayload(&jobs.Job{Payload: []byte(`{"queue":"EMAILBRIDGE_SUBMIT","all":true}`)}, &p); err != nil || !p.All {
		t.Fatalf("ok: %v %+v", err, p)
	}
	// A caller without an organisation context cannot use the organisation scope.
	ctx := tenancy.WithCaller(context.Background(), tenancy.Caller{Kind: tenancy.CallerUser, UserID: "usr_1", Role: tenancy.RoleAdmin})
	if _, err := authorize(ctx, sendingSource, false, false); err == nil {
		t.Fatal("no organisation accepted")
	}
	// Account-bound key with per-account scopes: the intersection.
	ctx = tenancy.WithCaller(context.Background(), tenancy.Caller{Kind: tenancy.CallerAPIKey, APIKeyID: "key_1",
		OrganizationID: "org_1", AccountID: "acc_1", Scopes: []string{"outbound:read", "accounts:read:acc_2"}})
	sc, err := authorize(ctx, sendingSource, false, false)
	if err != nil || !sc.AccountLimited || len(sc.AccountIDs) != 0 {
		t.Fatalf("intersection: %+v %v", sc, err)
	}
	ctx = tenancy.WithCaller(context.Background(), tenancy.Caller{Kind: tenancy.CallerAPIKey, APIKeyID: "key_1",
		OrganizationID: "org_1", Scopes: []string{"outbound:read", "accounts:read:acc_2"}})
	sc, err = authorize(ctx, sendingSource, false, false)
	if err != nil || !sc.AccountLimited || len(sc.AccountIDs) != 1 || sc.AccountIDs[0] != "acc_2" {
		t.Fatalf("per-account scope: %+v %v", sc, err)
	}
	if got := sendingSource.scopeClauses(sc); len(got) != 2 { // system + account
		t.Fatalf("clauses: %v", got)
	}
	sc = scope{EndpointLimited: true}
	if got := webhookSource.scopeClauses(sc); len(got) != 1 {
		t.Fatalf("endpoint clause: %v", got)
	}
}
