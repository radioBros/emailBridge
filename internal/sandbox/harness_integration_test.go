//go:build integration

package sandbox

import (
	"bytes"
	"context"
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
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/radiobros/emailbridge/internal/bus"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/events"
	"github.com/radiobros/emailbridge/internal/mailproto/smtpc"
	"github.com/radiobros/emailbridge/internal/objstore"
	"github.com/radiobros/emailbridge/internal/settings"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/submit"
	"github.com/radiobros/emailbridge/internal/submit/policy"
	"github.com/radiobros/emailbridge/internal/tenancy"
	"github.com/radiobros/emailbridge/simulator/simtest"
	"github.com/radiobros/emailbridge/test/containers"
)

const (
	testUser = "sender@example.test"
	testPass = "s3cret-sim"
	testTo   = "rcpt@example.test"
	rewrite  = "rewrite@example.test"
)

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

func (m *memObjects) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.data, key)
	return nil
}

func (m *memObjects) has(key string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.data[key]
	return ok
}

type stubAccounts struct {
	mu   sync.Mutex
	rows map[string]*submit.SMTPAccount
}

func (s *stubAccounts) SMTP(_ context.Context, accountID string) (*submit.SMTPAccount, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	acc, ok := s.rows[accountID]
	if !ok {
		return nil, errs.New(errs.CodeNotFound, "The account does not exist.")
	}
	copied := *acc
	return &copied, nil
}

func (s *stubAccounts) put(acc *submit.SMTPAccount) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.rows == nil {
		s.rows = map[string]*submit.SMTPAccount{}
	}
	s.rows[acc.ID] = acc
}

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
	if p.seen[msgID] {
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

// objectsAdapter adapts sandbox.Objects to submit.RawStore.
type objectsAdapter struct{ Objects }

func (a objectsAdapter) Put(ctx context.Context, key string, r io.Reader, size int64, meta objstore.ObjectMeta) error {
	return a.Objects.Put(ctx, key, r, size, meta)
}

func (a objectsAdapter) Get(ctx context.Context, key string) (io.ReadCloser, objstore.ObjectInfo, error) {
	return a.Objects.Get(ctx, key)
}

type harness struct {
	t        *testing.T
	db       *store.DB
	objects  Objects
	mem      *memObjects
	accounts *stubAccounts
	pub      *capturePublisher
	gate     *Gate
	svc      *Service
	submit   *submit.Service
	sim      *simtest.Env
	ctx      context.Context
	org      string
	acc      *submit.SMTPAccount
	env      string
}

func newHarness(t *testing.T, env string, useRealS3 bool) *harness {
	t.Helper()
	uri := os.Getenv("EB_TEST_MONGO_URI")
	if uri == "" {
		uri = "mongodb://127.0.0.1:27017/?replicaSet=rs0&directConnection=true"
	}
	root := context.Background()
	db, err := store.Connect(root, store.Options{
		URI: uri, Database: fmt.Sprintf("ebtest_sandbox_%d", time.Now().UnixNano()), ConnectTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Skipf("no MongoDB: %v", err)
	}
	t.Cleanup(func() { _ = db.DB.Drop(root); _ = db.Close(root) })
	for _, migration := range []store.Migration{submit.Migration(1), events.Migration(1), Migration(1)} {
		if err := migration.Up(root, db); err != nil {
			t.Fatal(err)
		}
	}

	var objects Objects
	var mem *memObjects
	if useRealS3 {
		ep, ak, sk := containers.S3(t)
		s3, err := objstore.Open(objstore.Options{
			Endpoint: "http://" + ep, Region: "us-east-1", AccessKey: ak, SecretKey: sk,
			Bucket: fmt.Sprintf("eb-sandbox-%d", time.Now().UnixNano()), PathStyle: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := s3.EnsureBucket(root); err != nil {
			t.Fatal(err)
		}
		objects = s3
	} else {
		mem = &memObjects{}
		objects = mem
	}

	sim := simtest.Start(t)
	if err := sim.Client.CreateUser(root, testUser, testPass); err != nil {
		t.Fatal(err)
	}
	if err := sim.Client.CreateUser(root, rewrite, testPass); err != nil {
		t.Fatal(err)
	}
	host, port := splitAddr(t, sim.Addr.SMTPPlain)
	org := store.NewID(store.PrefixOrganization)
	acc := &submit.SMTPAccount{
		ID: store.NewID(store.PrefixAccount), OrganizationID: org, State: "connected",
		Email: testUser, Name: "Sender", Host: host, Port: port, TLS: string(smtpc.TLSNone),
		User: testUser, Password: testPass, Policy: policy.Standard, MaxAttempts: 3,
		BackoffBase: time.Millisecond, CopyToSent: submit.CopyNever,
	}
	h := &harness{
		t: t, db: db, objects: objects, mem: mem, accounts: &stubAccounts{}, pub: &capturePublisher{},
		sim: sim, org: org, acc: acc, env: env,
	}
	h.accounts.put(acc)
	if _, err := db.Collection("accounts").InsertOne(root, bson.M{
		"_id": acc.ID, "organizationId": org, "email": testUser, "switches": bson.M{},
	}); err != nil {
		t.Fatal(err)
	}
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	h.gate = &Gate{
		DB: db, Objects: objects, Events: &events.Writer{DB: db, Partitions: 4},
		Env: env, Retention: 14 * 24 * time.Hour, Log: quiet,
	}
	h.svc = &Service{Repo: &Repo{DB: db}, Objects: objects, Log: quiet}
	h.submit = &submit.Service{
		Repo: &submit.Repo{DB: db}, Accounts: h.accounts, Objects: objectsAdapter{objects},
		Publisher: h.pub, Events: h.gate.Events, Sandbox: h.gate, Log: quiet, NodeID: "node-test",
		EHLO: "eb.test", MaxMessageSize: 1 << 20, Policy: policy.Standard, MaxAttempts: 3,
		BackoffBase: time.Millisecond, BackoffJitter: 0, CopyRetryDelay: time.Millisecond,
	}
	h.submit.Pool = &submit.Pool{
		Dial: func(ctx context.Context, a *submit.SMTPAccount) (*smtpc.Session, error) {
			return smtpc.Open(ctx, smtpc.SessionOptions{
				Addr: net.JoinHostPort(a.Host, strconv.Itoa(a.Port)), TLS: smtpc.TLSMode(a.TLS),
				Username: a.User, Password: a.Password, EHLO: "eb.test",
			})
		},
		PerAccount: 2, MaxMessages: 100, Idle: time.Minute,
	}
	t.Cleanup(h.submit.Pool.Close)
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

func (h *harness) setPolicy(enabled bool, policyName, rewriteTo string) {
	h.t.Helper()
	doc := bson.M{
		"_id":     settings.OrgDocID(h.org),
		"version": int64(1),
		"switches": bson.M{
			switchEnabled: bson.M{"value": enabled},
			switchPolicy:  bson.M{"value": policyName},
		},
		"values": bson.M{},
	}
	if rewriteTo != "" {
		doc["values"] = bson.M{settingRewrite: rewriteTo}
	}
	_, err := h.db.Collection(settings.SettingsCollection).ReplaceOne(h.ctx,
		bson.M{"_id": settings.OrgDocID(h.org)}, doc, options.Replace().SetUpsert(true))
	if err != nil {
		h.t.Fatalf("setPolicy: %v", err)
	}
}

func (h *harness) setAccountEnabled(enabled bool) {
	h.t.Helper()
	// Switch names contain dots; set the whole switches map so Mongo does not nest path segments.
	_, err := h.db.Collection("accounts").UpdateOne(h.ctx, bson.M{"_id": h.acc.ID},
		bson.M{"$set": bson.M{"switches": bson.M{switchEnabled: enabled}}})
	if err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) submitMsg() string {
	h.t.Helper()
	res, err := h.submit.Submit(h.ctx, h.acc.ID, submit.SubmitRequest{
		To: []submit.AddressInput{{Address: testTo}}, Subject: "Sandbox", Text: "hello sandbox",
	}, "", false)
	if err != nil {
		h.t.Fatalf("submit: %v", err)
	}
	return res.Accepted.ID
}

func (h *harness) work() {
	h.t.Helper()
	for _, j := range h.pub.drain() {
		if err := h.submit.Work(h.ctx, bus.Message{Subject: j.subject, Data: j.data, NumDelivered: 1}); err != nil {
			h.t.Fatalf("work: %v", err)
		}
	}
}

func (h *harness) getOutbound(id string) *submit.Message {
	h.t.Helper()
	m, err := h.submit.Get(h.ctx, id)
	if err != nil {
		h.t.Fatal(err)
	}
	return m
}

func (h *harness) caller(org string, role tenancy.Role) context.Context {
	return tenancy.WithCaller(context.Background(), tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: store.NewID(store.PrefixUser),
		OrganizationID: org, Role: role,
	})
}

func (h *harness) key(org, accountID string, scopes ...string) context.Context {
	return tenancy.WithCaller(context.Background(), tenancy.Caller{
		Kind: tenancy.CallerAPIKey, APIKeyID: store.NewID(store.PrefixAPIKey),
		OrganizationID: org, Scopes: scopes, AccountID: accountID,
	})
}

func (h *harness) clearSMTP() {
	_ = h.sim.Client.ClearSMTPMessages(h.ctx)
}

func (h *harness) eventsOf(outboundID string) []struct {
	Type    string `bson:"type"`
	Subject string `bson:"subject"`
} {
	h.t.Helper()
	cur, err := h.db.Collection(events.Collection).Find(h.ctx, bson.M{"subject": outboundID})
	if err != nil {
		h.t.Fatal(err)
	}
	defer func() { _ = cur.Close(h.ctx) }()
	var rows []struct {
		Type    string `bson:"type"`
		Subject string `bson:"subject"`
	}
	if err := cur.All(h.ctx, &rows); err != nil {
		h.t.Fatal(err)
	}
	return rows
}
