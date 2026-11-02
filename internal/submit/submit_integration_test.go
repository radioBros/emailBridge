//go:build integration

package submit

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/radiobros/emailbridge/internal/bus"
	"github.com/radiobros/emailbridge/internal/egress"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/events"
	"github.com/radiobros/emailbridge/internal/mailproto/smtpc"
	"github.com/radiobros/emailbridge/internal/objstore"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/submit/policy"
	"github.com/radiobros/emailbridge/internal/tenancy"
	"github.com/radiobros/emailbridge/simulator"
	"github.com/radiobros/emailbridge/simulator/simtest"
)

const (
	testUser = "sender@example.test"
	testPass = "s3cret-sim"
	testTo   = "rcpt@example.test"
)

// ---- doubles ----

// memObjects is the in-process RawStore: the S3 of the integration tests (SeaweedFS in S14).
type memObjects struct {
	mu   sync.Mutex
	data map[string][]byte
}

func (m *memObjects) Put(_ context.Context, key string, r io.Reader, _ int64, _ objstore.ObjectMeta) error {
	raw, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.data == nil {
		m.data = map[string][]byte{}
	}
	m.data[key] = raw
	return nil
}

func (m *memObjects) Get(_ context.Context, key string) (io.ReadCloser, objstore.ObjectInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	raw, ok := m.data[key]
	if !ok {
		return nil, objstore.ObjectInfo{}, objstore.ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(raw)), objstore.ObjectInfo{Key: key, Size: int64(len(raw))}, nil
}

// stubAccounts stands in for the cmd adapter over internal/accounts.
type stubAccounts struct {
	mu   sync.Mutex
	rows map[string]*SMTPAccount
	err  error
}

func (s *stubAccounts) SMTP(_ context.Context, accountID string) (*SMTPAccount, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return nil, s.err
	}
	acc, ok := s.rows[accountID]
	if !ok {
		return nil, errs.New(errs.CodeNotFound, "The account does not exist.")
	}
	copied := *acc
	return &copied, nil
}

func (s *stubAccounts) put(acc *SMTPAccount) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.rows == nil {
		s.rows = map[string]*SMTPAccount{}
	}
	s.rows[acc.ID] = acc
}

// stubSent counts appendSent requests (S6 §5).
type stubSent struct {
	mu     sync.Mutex
	calls  int
	fail   error
	failed int
}

func (s *stubSent) CopyToSent(_ context.Context, _, _ string, raw io.Reader, _ time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if _, err := io.ReadAll(raw); err != nil {
		return err
	}
	if s.fail != nil && s.calls > s.failed {
		return s.fail
	}
	return nil
}

func (s *stubSent) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

// capturePublisher keeps the jobs the service publishes so a test can deliver them when it wants.
type capturePublisher struct {
	mu   sync.Mutex
	jobs []capturedJob
	seen map[string]bool
}

type capturedJob struct {
	subject string
	msgID   string
	data    []byte
}

func (p *capturePublisher) Publish(_ context.Context, subject string, data []byte, msgID string, _ map[string]string) (bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.seen == nil {
		p.seen = map[string]bool{}
	}
	if p.seen[msgID] { // JetStream deduplicates on Nats-Msg-Id (S4 §4)
		return true, nil
	}
	p.seen[msgID] = true
	p.jobs = append(p.jobs, capturedJob{subject: subject, msgID: msgID, data: append([]byte(nil), data...)})
	return false, nil
}

func (p *capturePublisher) drain() []capturedJob {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := p.jobs
	p.jobs = nil
	return out
}

// ---- harness ----

type harness struct {
	t        *testing.T
	db       *store.DB
	svc      *Service
	objects  *memObjects
	accounts *stubAccounts
	sent     *stubSent
	pub      *capturePublisher
	sim      *simtest.Env
	ctx      context.Context
	org      string
	acc      *SMTPAccount
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	uri := os.Getenv("EB_TEST_MONGO_URI")
	if uri == "" {
		uri = "mongodb://127.0.0.1:27017/?replicaSet=rs0&directConnection=true"
	}
	root := context.Background()
	db, err := store.Connect(root, store.Options{
		URI: uri, Database: fmt.Sprintf("ebtest_submit_%d", time.Now().UnixNano()), ConnectTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Skipf("no MongoDB: %v", err)
	}
	t.Cleanup(func() { _ = db.DB.Drop(root); _ = db.Close(root) })
	for _, migration := range []store.Migration{Migration(1), events.Migration(1)} {
		if err := migration.Up(root, db); err != nil {
			t.Fatal(err)
		}
	}
	sim := simtest.Start(t)
	if err := sim.Client.CreateUser(root, testUser, testPass); err != nil {
		t.Fatal(err)
	}
	host, port := splitAddr(t, sim.Addr.SMTPPlain)
	org := store.NewID(store.PrefixOrganization)
	acc := &SMTPAccount{
		ID: store.NewID(store.PrefixAccount), OrganizationID: org, State: "connected",
		Email: testUser, Name: "Sender", Host: host, Port: port, TLS: string(smtpc.TLSNone),
		User: testUser, Password: testPass, Policy: policy.Standard, MaxAttempts: 3,
		BackoffBase: time.Millisecond, CopyToSent: CopyAlways, CopyToSentPath: "Sent",
	}
	h := &harness{
		t: t, db: db, objects: &memObjects{}, accounts: &stubAccounts{}, sent: &stubSent{},
		pub: &capturePublisher{}, sim: sim, org: org, acc: acc,
	}
	h.accounts.put(acc)
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	h.svc = &Service{
		Repo: &Repo{DB: db}, Accounts: h.accounts, Objects: h.objects, Publisher: h.pub,
		Events: &events.Writer{DB: db, Partitions: 4}, Sent: h.sent, Log: quiet, NodeID: "node-test",
		EHLO: "eb.test", MaxMessageSize: 1 << 20, Policy: policy.Standard, MaxAttempts: 3,
		BackoffBase: time.Millisecond, BackoffJitter: 0, CopyRetryDelay: time.Millisecond,
		QuotaSendsPerDay: 0, QuotaSendsPerMinute: 0,
	}
	h.svc.Pool = &Pool{
		Dial: h.svc.dial(egress.NewDialer(egress.Policy{})), PerAccount: 2, MaxMessages: 100, Idle: time.Minute,
	}
	t.Cleanup(h.svc.Pool.Close)
	h.ctx = tenancy.WithCaller(root, tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: store.NewID(store.PrefixUser),
		OrganizationID: org, Role: tenancy.RoleOwner,
	})
	return h
}

func splitAddr(t *testing.T, addr string) (string, int) {
	t.Helper()
	host, portText, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	return host, port
}

// submit queues one plain message and returns its ID.
func (h *harness) submit(req SubmitRequest) string {
	h.t.Helper()
	if req.To == nil && req.Raw == "" {
		req.To = []AddressInput{{Address: testTo}}
	}
	if req.Subject == "" && req.Raw == "" {
		req.Subject = "Integration"
	}
	if req.Text == "" && req.Raw == "" {
		req.Text = "Hello from the integration suite."
	}
	res, err := h.svc.Submit(h.ctx, h.acc.ID, req, "", false)
	if err != nil {
		h.t.Fatalf("submit: %v", err)
	}
	return res.Accepted.ID
}

// work delivers every published job to the worker once and returns the handler errors.
func (h *harness) work() []error {
	h.t.Helper()
	var out []error
	for _, j := range h.pub.drain() {
		out = append(out, h.svc.Work(h.ctx, bus.Message{Subject: j.subject, Data: j.data, NumDelivered: 1}))
	}
	return out
}

// redeliver delivers the one job of a message again, as JetStream would after a nak.
func (h *harness) redeliver(id string) error {
	h.t.Helper()
	data, err := json.Marshal(job{OutboundID: id, AccountID: h.acc.ID, OrganizationID: h.org})
	if err != nil {
		h.t.Fatal(err)
	}
	return h.svc.Work(h.ctx, bus.Message{Subject: bus.SubmitSubject(h.acc.ID), Data: data, NumDelivered: 2})
}

func (h *harness) get(id string) *Message {
	h.t.Helper()
	m, err := h.svc.Repo.forWorker(h.ctx, h.org, id)
	if err != nil {
		h.t.Fatalf("get %s: %v", id, err)
	}
	return m
}

// expire backdates the heartbeat so the message looks abandoned (S6 §3.7).
func (h *harness) expire(id string) {
	h.t.Helper()
	old := time.Now().UTC().Add(-10 * time.Minute)
	if _, err := h.db.Collection(Collection).UpdateOne(h.ctx, bson.M{"_id": id},
		bson.M{"$set": bson.M{"heartbeatAt": old}}); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) script(script simulator.SMTPScript) {
	h.t.Helper()
	if err := h.sim.Client.SetSMTPScript(h.ctx, testUser, script); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) clearScript() {
	h.t.Helper()
	if err := h.sim.Client.ClearSMTPScript(h.ctx, testUser); err != nil {
		h.t.Fatal(err)
	}
}

func encode64(text string) string { return base64.StdEncoding.EncodeToString([]byte(text)) }

// delivered counts the messages the simulator accepted (its DATA log).
func (h *harness) delivered() int { return len(h.sim.Sim.ListSMTP()) }

func (h *harness) events(id string) []string {
	h.t.Helper()
	cur, err := h.db.Collection(events.Collection).Find(h.ctx, bson.M{"subject": id})
	if err != nil {
		h.t.Fatal(err)
	}
	defer func() { _ = cur.Close(h.ctx) }()
	var rows []struct {
		Type string `bson:"type"`
	}
	if err := cur.All(h.ctx, &rows); err != nil {
		h.t.Fatal(err)
	}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Type)
	}
	return out
}

func (h *harness) eventData(id, eventType string) map[string]any {
	h.t.Helper()
	var row struct {
		Data string `bson:"data"`
	}
	if err := h.db.Collection(events.Collection).FindOne(h.ctx,
		bson.M{"subject": id, "type": eventType}).Decode(&row); err != nil {
		h.t.Fatalf("event %s of %s: %v", eventType, id, err)
	}
	out := map[string]any{}
	if err := json.Unmarshal([]byte(row.Data), &out); err != nil {
		h.t.Fatal(err)
	}
	return out
}

func has(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

// ---- the happy path ----

func TestIntegrationSubmitToSent(t *testing.T) {
	h := newHarness(t)
	id := h.submit(SubmitRequest{})

	m := h.get(id)
	if m.State != StateQueued || m.RawRef == "" || m.Size == 0 || m.MessageIDHeader == "" {
		t.Fatalf("after submit: %+v", m)
	}
	if m.Policy != policy.Standard || m.MaxAttempts != 3 {
		t.Fatalf("resolved policy: %s/%d", m.Policy, m.MaxAttempts)
	}
	for _, err := range h.work() {
		if err != nil {
			t.Fatalf("worker: %v", err)
		}
	}
	m = h.get(id)
	if m.State != StateSent || m.SentAt == nil || m.Worker != "" || m.HeartbeatAt != nil {
		t.Fatalf("after send: %+v", m)
	}
	if len(m.Attempts) != 1 || m.Attempts[0].Stage != string(smtpc.StageDataEnd) ||
		m.Attempts[0].Kind != string(policy.KindSuccess) {
		t.Fatalf("attempt: %+v", m.Attempts)
	}
	if !has(h.events(id), events.MessageSent) {
		t.Fatalf("events: %v", h.events(id))
	}
	data := h.eventData(id, events.MessageSent)
	if data["messageIdHeader"] != m.MessageIDHeader {
		t.Fatalf("message.sent messageIdHeader: %v", data["messageIdHeader"])
	}
	if h.delivered() != 1 {
		t.Fatalf("the simulator accepted %d messages", h.delivered())
	}
	// Copy to Sent runs after the state change and never changes it (S6 §5).
	if h.sent.count() != 1 {
		t.Fatalf("copy to Sent called %d times", h.sent.count())
	}
	if m = h.get(id); m.CopyToSent.State != CopyDone || m.State != StateSent {
		t.Fatalf("copyToSent: %+v (state %s)", m.CopyToSent, m.State)
	}
}

func TestIntegrationMessageIDPreservedOnTheWire(t *testing.T) {
	h := newHarness(t)
	id := h.submit(SubmitRequest{MessageID: "<Kept.Byte-For-Byte@example.com>"})
	h.work()
	if m := h.get(id); m.MessageIDHeader != "<Kept.Byte-For-Byte@example.com>" {
		t.Fatalf("stored message id: %q", m.MessageIDHeader)
	}
	received := h.sim.Sim.ListSMTP()
	if len(received) != 1 {
		t.Fatalf("delivered %d", len(received))
	}
	if !bytes.Contains(received[0].Data, []byte("<Kept.Byte-For-Byte@example.com>")) {
		t.Fatal("the Message-ID is not on the wire byte-for-byte")
	}
}

func TestIntegrationBccOnlyInTheEnvelope(t *testing.T) {
	h := newHarness(t)
	id := h.submit(SubmitRequest{
		To:  []AddressInput{{Address: testTo}},
		Bcc: []AddressInput{{Address: "hidden@example.test"}},
	})
	m := h.get(id)
	if m.Envelope.BccCount != 1 || len(m.Envelope.To) != 1 {
		t.Fatalf("envelope: %+v", m.Envelope)
	}
	h.work()
	received := h.sim.Sim.ListSMTP()
	if len(received) != 1 || len(received[0].To) != 2 {
		t.Fatalf("rcpt recipients: %+v", received)
	}
	if bytes.Contains(received[0].Data, []byte("hidden@example.test")) {
		t.Fatal("the Bcc address must not appear in the transmitted headers")
	}
	view := ViewOf(h.get(id))
	if len(view.Envelope.To) != 1 || view.Envelope.BccCount != 1 {
		t.Fatalf("the API view must not list bcc addresses: %+v", view.Envelope)
	}
}

func TestIntegrationRawSubmit(t *testing.T) {
	h := newHarness(t)
	raw := "From: " + testUser + "\r\nTo: " + testTo + "\r\nSubject: Raw\r\n" +
		"Bcc: hidden@example.test\r\nMessage-ID: <raw-1@example.com>\r\n\r\nBody\r\n"
	id := h.submit(SubmitRequest{Raw: encode64(raw)})
	h.work()
	m := h.get(id)
	if m.State != StateSent || m.MessageIDHeader != "<raw-1@example.com>" || m.Subject != "Raw" {
		t.Fatalf("raw submit: %+v", m)
	}
	received := h.sim.Sim.ListSMTP()
	if len(received[0].To) != 2 {
		t.Fatalf("the envelope of a raw message comes from its headers: %v", received[0].To)
	}
	if bytes.Contains(received[0].Data, []byte("Bcc:")) {
		t.Fatal("the Bcc header must be stripped from the transmitted copy")
	}
}

// ---- failures, retries and policies ----

func TestIntegrationTemporaryFailureThenSent(t *testing.T) {
	h := newHarness(t)
	h.script(simulator.SMTPScript{"mailFrom": {Code: 451, Enhanced: "4.3.0", Text: "please retry"}})
	id := h.submit(SubmitRequest{})
	h.work()

	m := h.get(id)
	if m.State != StateQueued || m.NextAttemptAt == nil || len(m.Attempts) != 1 {
		t.Fatalf("after a temporary failure: %+v", m)
	}
	if m.Attempts[0].Kind != string(errs.KindTemporary) || m.Attempts[0].Code != 451 {
		t.Fatalf("attempt: %+v", m.Attempts[0])
	}
	if !has(h.events(id), events.MessageDeliveryAttemptFailed) {
		t.Fatalf("events: %v", h.events(id))
	}
	h.clearScript()
	// The scheduler duty publishes the due retry; here we deliver it directly.
	if err := h.redeliver(id); err != nil {
		t.Fatalf("retry: %v", err)
	}
	m = h.get(id)
	if m.State != StateSent || len(m.Attempts) != 2 {
		t.Fatalf("after the retry: %s with %d attempts", m.State, len(m.Attempts))
	}
	if h.delivered() != 1 {
		t.Fatalf("delivered %d", h.delivered())
	}
}

func TestIntegrationPermanentFailure(t *testing.T) {
	h := newHarness(t)
	h.script(simulator.SMTPScript{"rcptTo": {Code: 550, Enhanced: "5.1.1", Text: "no such user"}})
	id := h.submit(SubmitRequest{})
	h.work()

	m := h.get(id)
	if m.State != StateFailed || m.FinalError == nil || m.NextAttemptAt != nil {
		t.Fatalf("after a permanent failure: %+v", m)
	}
	if m.FinalError.Code != errs.CodeSMTPPermanent {
		t.Fatalf("final error: %+v", m.FinalError)
	}
	if len(m.Attempts) != 1 || m.Attempts[0].Kind != string(errs.KindPermanent) {
		t.Fatalf("attempt: %+v", m.Attempts)
	}
	data := h.eventData(id, events.MessageFailed)
	if data["outcomeUnknown"] != false {
		t.Fatalf("message.failed: %v", data)
	}
	if h.delivered() != 0 {
		t.Fatal("nothing may be delivered after a refused recipient")
	}
}

func TestIntegrationAttemptsExhausted(t *testing.T) {
	h := newHarness(t)
	h.script(simulator.SMTPScript{"mailFrom": {Code: 451, Text: "later"}})
	id := h.submit(SubmitRequest{MaxAttempts: intPtr(2)})
	h.work()
	if m := h.get(id); m.State != StateQueued {
		t.Fatalf("first attempt: %s", m.State)
	}
	if err := h.redeliver(id); err != nil {
		t.Fatal(err)
	}
	m := h.get(id)
	if m.State != StateFailed || len(m.Attempts) != 2 {
		t.Fatalf("after maxAttempts: %s with %d attempts", m.State, len(m.Attempts))
	}
	if !has(h.events(id), events.MessageFailed) {
		t.Fatalf("events: %v", h.events(id))
	}
}

func TestIntegrationAuthFailureEmitsAccountAuthFailed(t *testing.T) {
	h := newHarness(t)
	h.script(simulator.SMTPScript{"auth": {Code: 535, Enhanced: "5.7.8", Text: "bad credentials"}})
	id := h.submit(SubmitRequest{})
	h.work()

	m := h.get(id)
	if m.State != StateQueued || m.NextAttemptAt == nil {
		t.Fatalf("auth failures are retried: %+v", m)
	}
	if delay := m.NextAttemptAt.Sub(m.Attempts[0].At); delay < policy.MinAuth {
		t.Fatalf("auth backoff is %s, want at least %s", delay, policy.MinAuth)
	}
	if m.Attempts[0].Kind != string(errs.KindAuth) {
		t.Fatalf("attempt: %+v", m.Attempts[0])
	}
	if !has(h.events(h.acc.ID), events.AccountAuthFailed) {
		t.Fatalf("account events: %v", h.events(h.acc.ID))
	}
	data := h.eventData(h.acc.ID, events.AccountAuthFailed)
	if data["protocol"] != "smtp" {
		t.Fatalf("account.auth_failed: %v", data)
	}
}

func TestIntegrationRateLimitedBackoff(t *testing.T) {
	h := newHarness(t)
	h.script(simulator.SMTPScript{"rcptTo": {Code: 452, Enhanced: "4.7.25", Text: "too many messages"}})
	id := h.submit(SubmitRequest{})
	h.work()
	m := h.get(id)
	if m.Attempts[0].Kind != string(errs.KindRateLimited) {
		t.Fatalf("attempt: %+v", m.Attempts[0])
	}
	if delay := m.NextAttemptAt.Sub(m.Attempts[0].At); delay < policy.MinRateLimited {
		t.Fatalf("throttle backoff is %s, want at least %s", delay, policy.MinRateLimited)
	}
}

// TestIntegrationDropAfterData is the drop-after-DATA case of S6 §9: the server took the body and never
// answered, so standard retries while atMostOnce ends in outcomeUnknown.
func TestIntegrationDropAfterData(t *testing.T) {
	for _, tc := range []struct {
		policy string
		state  string
	}{
		{policy.Standard, StateQueued},
		{policy.AtMostOnce, StateOutcomeUnknown},
	} {
		t.Run(tc.policy, func(t *testing.T) {
			h := newHarness(t)
			h.script(simulator.SMTPScript{"dataEnd": {DropAfter: true}})
			id := h.submit(SubmitRequest{Policy: tc.policy})
			h.work()

			m := h.get(id)
			if m.State != tc.state {
				t.Fatalf("state %s, want %s", m.State, tc.state)
			}
			if m.Attempts[0].Kind != string(errs.KindOutcomeUnknown) {
				t.Fatalf("attempt: %+v", m.Attempts[0])
			}
			if h.delivered() != 1 {
				t.Fatalf("the server received %d copies", h.delivered())
			}
			if tc.policy == policy.AtMostOnce {
				data := h.eventData(id, events.MessageFailed)
				if data["outcomeUnknown"] != true {
					t.Fatalf("message.failed: %v", data)
				}
				// A redelivered job must not send a second copy.
				if err := h.redeliver(id); err != nil {
					t.Fatal(err)
				}
				if h.delivered() != 1 {
					t.Fatalf("atMostOnce sent the message twice: %d copies", h.delivered())
				}
				return
			}
			if !has(h.events(id), events.MessageDeliveryAttemptFailed) {
				t.Fatalf("events: %v", h.events(id))
			}
			h.clearScript()
			if err := h.redeliver(id); err != nil {
				t.Fatal(err)
			}
			if m = h.get(id); m.State != StateSent {
				t.Fatalf("standard retries an unknown outcome: %s", m.State)
			}
			if h.delivered() != 2 {
				t.Fatalf("standard may duplicate: %d copies", h.delivered())
			}
		})
	}
}

// TestIntegrationCrashAtEveryStage kills the worker at each stage for both policies and checks what the
// redelivered job does (S6 §3.7, §6.2): nothing was sent before `data`, so both policies retry; from
// `data` on the outcome is unknown, so atMostOnce never sends again.
func TestIntegrationCrashAtEveryStage(t *testing.T) {
	stages := []string{
		string(smtpc.StageMailFrom), string(smtpc.StageRcptTo),
		string(smtpc.StageData), string(smtpc.StageDataEnd),
	}
	for _, stage := range stages {
		for _, p := range []string{policy.Standard, policy.AtMostOnce} {
			t.Run(stage+"/"+p, func(t *testing.T) {
				h := newHarness(t)
				crash := errors.New("worker killed")
				h.svc.OnStage = func(_ context.Context, _ *Message, at string) error {
					if at == stage {
						return crash
					}
					return nil
				}
				id := h.submit(SubmitRequest{Policy: p})
				for _, err := range h.work() {
					if !errors.Is(err, crash) {
						t.Fatalf("the crash must nak the job, got %v", err)
					}
				}
				// The worker left the message claimed, with the stage it had reached.
				m := h.get(id)
				if m.State != StateSending || m.Worker == "" {
					t.Fatalf("after the crash: %+v", m)
				}
				if got := m.Attempts[0].Stage; got != stage {
					t.Fatalf("persisted stage %q, want %q", got, stage)
				}
				delivered := h.delivered()
				if stage != string(smtpc.StageDataEnd) && delivered != 0 {
					t.Fatalf("nothing may be delivered when the crash was at %s", stage)
				}

				// A job that comes back while the worker still lives must wait.
				h.svc.OnStage = nil
				if err := h.redeliver(id); !errors.Is(err, errBusyElsewhere) {
					t.Fatalf("a live worker keeps the job, got %v", err)
				}
				h.expire(id)
				if err := h.redeliver(id); err != nil {
					t.Fatalf("recovery: %v", err)
				}
				m = h.get(id)
				afterBody := stage == string(smtpc.StageData) || stage == string(smtpc.StageDataEnd)
				switch {
				case afterBody && p == policy.AtMostOnce:
					if m.State != StateOutcomeUnknown {
						t.Fatalf("atMostOnce after a crash at %s: %s", stage, m.State)
					}
					if h.delivered() != delivered {
						t.Fatalf("atMostOnce sent the message again: %d copies", h.delivered())
					}
					if h.eventData(id, events.MessageFailed)["outcomeUnknown"] != true {
						t.Fatal("message.failed must report an unknown outcome")
					}
				default:
					if m.State != StateQueued || m.NextAttemptAt == nil {
						t.Fatalf("the crash must be a retriable attempt: %+v", m)
					}
					if err := h.redeliver(id); err != nil {
						t.Fatalf("resend: %v", err)
					}
					if m = h.get(id); m.State != StateSent {
						t.Fatalf("the resend must succeed: %s", m.State)
					}
					if h.delivered() != delivered+1 {
						t.Fatalf("the resend delivered %d copies", h.delivered())
					}
				}
			})
		}
	}
}

// TestIntegrationPartialRecipientRejection checks the recording of a message that some recipients refused
// while others took it. The simulator scripts replies per stage, not per recipient, so the accepted and
// refused mix is produced from an smtpc result (S6 §3.4).
func TestIntegrationPartialRecipientRejection(t *testing.T) {
	h := newHarness(t)
	id := h.submit(SubmitRequest{To: []AddressInput{{Address: testTo}, {Address: "gone@example.test"}}})
	m, err := h.svc.Repo.claim(h.ctx, h.org, id, "node-test", time.Now().UTC())
	if err != nil || m == nil {
		t.Fatalf("claim: %v", err)
	}
	started := time.Now().UTC()
	idx, err := h.svc.Repo.startAttempt(h.ctx, m, Attempt{At: started, Stage: string(smtpc.StageConnect)})
	if err != nil {
		t.Fatal(err)
	}
	res := &smtpc.Result{
		Response: "250 2.0.0 Ok: queued as 4242", Accepted: []string{testTo},
		Rejected: []smtpc.Rejection{{Address: "gone@example.test", Code: 550, Enhanced: "5.1.1", Message: "no such user"}},
	}
	if err := h.svc.finish(h.ctx, m, h.acc, idx, started, res, nil); err != nil {
		t.Fatal(err)
	}
	m = h.get(id)
	if m.State != StateSent || len(m.Attempts[0].Rejected) != 1 {
		t.Fatalf("partial rejection: %+v", m.Attempts)
	}
	if m.Attempts[0].Rejected[0].Address != "gone@example.test" {
		t.Fatalf("rejected: %+v", m.Attempts[0].Rejected)
	}
	data := h.eventData(id, events.MessageSent)
	rejected, ok := data["rejected"].([]any)
	if !ok || len(rejected) != 1 {
		t.Fatalf("message.sent must list the refused recipients: %v", data["rejected"])
	}
}

// ---- cancellation, scheduling, pool ----

func TestIntegrationCancelWhileQueued(t *testing.T) {
	h := newHarness(t)
	id := h.submit(SubmitRequest{})
	m, err := h.svc.Cancel(h.ctx, id, "")
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if m.State != StateCancelled {
		t.Fatalf("state: %s", m.State)
	}
	for _, err := range h.work() {
		if err != nil {
			t.Fatalf("the worker must ack a cancelled message: %v", err)
		}
	}
	if h.delivered() != 0 {
		t.Fatal("a cancelled message must not be sent")
	}
	if m = h.get(id); m.State != StateCancelled || len(m.Attempts) != 0 {
		t.Fatalf("after the worker: %+v", m)
	}
	if _, err := h.svc.Cancel(h.ctx, id, ""); errs.CodeOf(err) != errs.CodeOutboundNotCancellable {
		t.Fatalf("cancelling twice: %v", err)
	}
}

func TestIntegrationCancelAfterSentIsRefused(t *testing.T) {
	h := newHarness(t)
	id := h.submit(SubmitRequest{})
	h.work()
	if _, err := h.svc.Cancel(h.ctx, id, ""); errs.CodeOf(err) != errs.CodeOutboundNotCancellable {
		t.Fatalf("cancelling a sent message: %v", err)
	}
}

func TestIntegrationScheduledMessageWaitsForItsTime(t *testing.T) {
	h := newHarness(t)
	later := time.Now().UTC().Add(time.Hour)
	id := h.submit(SubmitRequest{SendAt: &later})
	m := h.get(id)
	if m.State != StateScheduled || m.SendAt == nil {
		t.Fatalf("scheduled: %+v", m)
	}
	// Not due: the duty leaves it alone.
	h.pub.drain()
	h.runDue()
	if len(h.pub.drain()) != 0 {
		t.Fatal("a message that is not due must not be published")
	}
	soon := time.Now().UTC().Add(-time.Second)
	if _, err := h.db.Collection(Collection).UpdateOne(h.ctx, bson.M{"_id": id},
		bson.M{"$set": bson.M{"sendAt": soon, "nextAttemptAt": soon}}); err != nil {
		t.Fatal(err)
	}
	h.runDue()
	if m = h.get(id); m.State != StateQueued {
		t.Fatalf("the duty must queue a due message: %s", m.State)
	}
	for _, err := range h.work() {
		if err != nil {
			t.Fatal(err)
		}
	}
	if m = h.get(id); m.State != StateSent {
		t.Fatalf("state: %s", m.State)
	}
}

// runDue runs one pass of the due duty.
func (h *harness) runDue() {
	h.t.Helper()
	ctx, cancel := context.WithCancel(h.ctx)
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	h.svc.Due(ctx)
}

// runSweep runs one pass of the stale-sending sweep.
func (h *harness) runSweep() {
	h.t.Helper()
	ctx, cancel := context.WithCancel(h.ctx)
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	h.svc.Sweep(ctx)
}

func TestIntegrationSweepRecoversAbandonedMessages(t *testing.T) {
	h := newHarness(t)
	id := h.submit(SubmitRequest{})
	if _, err := h.svc.Repo.claim(h.ctx, h.org, id, "dead-node", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	h.expire(id)
	h.runSweep()
	m := h.get(id)
	if m.State != StateQueued || len(m.Attempts) != 1 || m.Attempts[0].Kind != string(errs.KindTemporary) {
		t.Fatalf("after the sweep: %+v", m)
	}
	if !has(h.events(id), events.MessageDeliveryAttemptFailed) {
		t.Fatalf("events: %v", h.events(id))
	}
}

func TestIntegrationPoolReusesAndLimits(t *testing.T) {
	h := newHarness(t)
	first := h.submit(SubmitRequest{})
	second := h.submit(SubmitRequest{})
	for _, err := range h.work() {
		if err != nil {
			t.Fatal(err)
		}
	}
	if h.get(first).State != StateSent || h.get(second).State != StateSent {
		t.Fatal("both messages must be sent")
	}
	if h.delivered() != 2 {
		t.Fatalf("delivered %d", h.delivered())
	}
	// One pooled session served both messages (RSET in between, S6 §4).
	lease, err := h.svc.Pool.Acquire(h.ctx, h.acc)
	if err != nil {
		t.Fatal(err)
	}
	if lease.Session.Messages() != 2 {
		t.Fatalf("the session sent %d messages; the pool did not reuse it", lease.Session.Messages())
	}
	lease.Release()

	// The per-account limit is enforced without waiting.
	h.svc.Pool.PerAccount = 1
	h.svc.Pool.Close()
	busy, err := h.svc.Pool.Acquire(h.ctx, h.acc)
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Release()
	if _, err := h.svc.Pool.Acquire(h.ctx, h.acc); !errors.Is(err, ErrPoolBusy) {
		t.Fatalf("over the limit: %v", err)
	}
	id := h.submit(SubmitRequest{})
	results := h.work()
	if len(results) != 1 || !errors.Is(results[0], ErrPoolBusy) {
		t.Fatalf("a busy account must nak with a delay: %v", results)
	}
	if m := h.get(id); m.State != StateQueued || len(m.Attempts) != 0 {
		t.Fatalf("a busy pool must not count an attempt: %+v", m)
	}
}

func TestIntegrationCopyToSentFailureKeepsSent(t *testing.T) {
	h := newHarness(t)
	h.sent.fail = errors.New("append refused")
	id := h.submit(SubmitRequest{})
	h.work()
	m := h.get(id)
	if m.State != StateSent {
		t.Fatalf("a failed copy must not change the state: %s", m.State)
	}
	if m.CopyToSent.State != CopyFailed || h.sent.count() != CopyMaxAttempts {
		t.Fatalf("copyToSent %+v after %d attempts", m.CopyToSent, h.sent.count())
	}
}

func TestIntegrationCopyToSentSkippedForProviderFiling(t *testing.T) {
	h := newHarness(t)
	h.acc.CopyToSent = CopyAuto
	h.acc.Host = "smtp.gmail.com"
	h.accounts.put(h.acc)
	m := h.get(h.submit(SubmitRequest{}))
	if m.CopyToSent.Enabled || m.CopyToSent.State != CopySkipped {
		t.Fatalf("auto must skip providers that file sent mail: %+v", m.CopyToSent)
	}
	if !FilesSentMail("smtp.gmail.com") || FilesSentMail("mail.example.com") {
		t.Fatal("the provider table is wrong")
	}
}

// TestIntegrationEndToEndOverNATS runs the module's real path: JetStream work queue, durable consumer,
// worker, state machine.
func TestIntegrationEndToEndOverNATS(t *testing.T) {
	h := newHarness(t)
	eventBus, err := bus.Open(h.ctx, bus.Options{Mode: bus.ModeEmbedded, NodeID: "test", DataDir: t.TempDir()})
	if err != nil {
		t.Skipf("no embedded NATS: %v", err)
	}
	t.Cleanup(eventBus.Close)
	h.svc.Publisher = eventBus
	stop, err := eventBus.ConsumePartition(h.ctx, bus.StreamSubmit, "submit-test", bus.SubmitSubject("*"), 4, h.svc.Work)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stop)

	id := h.submit(SubmitRequest{})
	deadline := time.Now().Add(30 * time.Second)
	for {
		if m := h.get(id); m.State == StateSent {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the message is still %s", h.get(id).State)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if h.delivered() != 1 {
		t.Fatalf("delivered %d", h.delivered())
	}
}

func intPtr(v int) *int { return &v }
