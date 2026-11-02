//go:build integration

package embeddings

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/radiobros/emailbridge/internal/accounts"
	"github.com/radiobros/emailbridge/internal/ai"
	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/audit"
	"github.com/radiobros/emailbridge/internal/crypto"
	"github.com/radiobros/emailbridge/internal/egress"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/events"
	"github.com/radiobros/emailbridge/internal/features"
	"github.com/radiobros/emailbridge/internal/fulltext"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/index"
	"github.com/radiobros/emailbridge/internal/jobs"
	"github.com/radiobros/emailbridge/internal/settings"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

func testDB(t *testing.T) *store.DB {
	t.Helper()
	uri := os.Getenv("EB_TEST_MONGO_URI")
	if uri == "" {
		uri = "mongodb://127.0.0.1:27017/?replicaSet=rs0&directConnection=true"
	}
	ctx := context.Background()
	db, err := store.Connect(ctx, store.Options{
		URI: uri, Database: fmt.Sprintf("ebtest_embeddings_%d", time.Now().UnixNano()), ConnectTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Skipf("no MongoDB: %v", err)
	}
	t.Cleanup(func() { _ = db.DB.Drop(ctx); _ = db.Close(ctx) })
	for _, mg := range []store.Migration{crypto.Migration(1), audit.Migration(3), jobs.Migration(8), accounts.Migration(9),
		index.Migration(10), ai.Migration(30), fulltext.Migration(49), Migration(50)} {
		if err := mg.Up(ctx, db); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

// embedMock is a fake OpenAI-compatible /embeddings endpoint. The vector of a text is a bag of its words
// hashed into 64 dimensions, so texts sharing words are close — enough to test ranking without a model.
type embedMock struct {
	srv      *httptest.Server
	base     string
	requests atomic.Int64
	inputs   atomic.Int64
	fail     atomic.Int64 // answer 500 to this many requests
	dims     int
	mu       sync.Mutex
	lastAuth string
}

var wordRE = regexp.MustCompile(`[\p{L}\p{N}]+`)

func bagOfWords(text string, dims int) []float32 {
	v := make([]float32, dims)
	for _, w := range wordRE.FindAllString(strings.ToLower(text), -1) {
		if len(w) < 3 {
			continue
		}
		h := fnv.New32a()
		_, _ = h.Write([]byte(w))
		v[h.Sum32()%uint32(dims)]++
	}
	return v
}

func startEmbedMock(t *testing.T) *embedMock {
	t.Helper()
	m := &embedMock{dims: 64}
	m.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/embeddings" {
			http.NotFound(w, r)
			return
		}
		m.requests.Add(1)
		m.mu.Lock()
		m.lastAuth = r.Header.Get("Authorization")
		m.mu.Unlock()
		if m.fail.Load() > 0 {
			m.fail.Add(-1)
			http.Error(w, `{"error":"down"}`, http.StatusInternalServerError)
			return
		}
		var req struct {
			Model string   `json:"model"`
			Input []string `json:"input"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		m.inputs.Add(int64(len(req.Input)))
		data := make([]map[string]any, len(req.Input))
		for i, in := range req.Input {
			data[i] = map[string]any{"index": i, "embedding": bagOfWords(in, m.dims)}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data, "model": req.Model})
	}))
	t.Cleanup(m.srv.Close)
	// The egress policy allows http only for allow-listed host names: use "localhost", not the IP.
	m.base = strings.Replace(m.srv.URL, "127.0.0.1", "localhost", 1) + "/v1"
	return m
}

// fakeFetcher returns canned bodies for the full-text indexer.
type fakeFetcher struct {
	mu     sync.Mutex
	bodies map[string]string
}

func (f *fakeFetcher) FetchText(_ context.Context, _, messageID string) (string, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.bodies[messageID], "", nil
}

var citeRE = regexp.MustCompile(`EXCERPT \[(msg_[0-9a-z]{26})\]`)

type fixture struct {
	t      *testing.T
	ctx    context.Context
	db     *store.DB
	org    string
	mock   *embedMock
	ai     *ai.Service
	fts    *fulltext.Service
	svc    *Service
	fetch  *fakeFetcher
	prompt atomic.Value // last user prompt seen by the completion mock
	cancel context.CancelFunc
}

const fakeID = "msg_zzzzzzzzzzzzzzzzzzzzzzzzzz"

func newFixture(t *testing.T) *fixture {
	db := testDB(t)
	f := &fixture{t: t, ctx: context.Background(), db: db, org: store.NewID(store.PrefixOrganization),
		mock: startEmbedMock(t), fetch: &fakeFetcher{bodies: map[string]string{}}}
	key := make([]byte, 32)
	for i := range key {
		key[i] = 5
	}
	kek, err := crypto.NewLocal([]crypto.LocalKey{{ID: "k1", Key: key}}, "")
	if err != nil {
		t.Fatal(err)
	}
	cipher := &crypto.Cipher{KEK: kek, Store: &crypto.MongoKeyStore{DB: db}, NewID: func() string { return store.NewID(store.PrefixDataKey) }}
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	f.ai = &ai.Service{
		Repo: &ai.Repo{DB: db}, Cipher: cipher, DB: db, Registry: features.DefaultRegistry(), Env: "test",
		Egress: egress.NewDialer(egress.Policy{AllowHosts: []string{"localhost"}}), HTTP: f.mock.srv.Client(),
		Providers: map[string]ai.Provider{ai.ProviderOpenAI: &ai.MockProvider{Fn: func(_ context.Context, _, _, sys, user string) (string, error) {
			f.prompt.Store(user)
			m := citeRE.FindStringSubmatch(user)
			if m == nil || !strings.Contains(sys, "untrusted") {
				return "The messages found do not answer the question.", nil
			}
			return "The assembly is on 14 November at 18:00 [" + m[1] + "]. See also [" + fakeID + "].", nil
		}}},
	}
	fullSw := &fulltext.Switches{DB: db, Registry: features.DefaultRegistry(), Env: "test", TTL: -1}
	f.fts = &fulltext.Service{DB: db, Repo: &fulltext.Repo{DB: db}, Fetcher: f.fetch, MinInterval: -1, Switches: fullSw,
		Jobs: &jobs.Service{DB: db}, Log: quiet}
	f.svc = &Service{
		DB: db, Repo: &Repo{DB: db}, AI: f.ai, Log: quiet, Audit: &audit.Writer{DB: db},
		Switches:  &fulltext.Switches{DB: db, Registry: features.DefaultRegistry(), Env: "test", TTL: -1, Name: SwitchName},
		FullText:  fullSw,
		Jobs:      &jobs.Service{DB: db},
		RetryBase: 20 * time.Millisecond, RequestsPerMinute: -1, SearchPerMinute: -1, AskPerMinute: -1,
	}
	f.ai.EmbeddingsChanged = f.svc.ModelChanged
	f.fts.AddObserver(f.svc)
	f.svc.Jobs.Handle(JobKind, f.svc.handleReindex)
	runCtx, cancel := context.WithCancel(context.Background())
	f.cancel = cancel
	done := make(chan struct{})
	go func() { f.svc.RunWorkers(runCtx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return f
}

func (f *fixture) setOrgSwitches(sw map[string]bool) {
	f.t.Helper()
	set := bson.M{}
	for k, v := range sw {
		set[k] = bson.M{"value": v}
	}
	if _, err := f.db.Collection(settings.SettingsCollection).UpdateOne(f.ctx, bson.M{"_id": settings.OrgDocID(f.org)},
		bson.M{"$set": bson.M{"switches": set}}, options.UpdateOne().SetUpsert(true)); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) enableAll() {
	f.setOrgSwitches(map[string]bool{fulltext.SwitchName: true, SwitchName: true, "ai.enabled": true})
}

func (f *fixture) configureAI(model string) {
	f.t.Helper()
	key := "sk-test-main"
	in := ai.PutInput{Provider: ai.ProviderOpenAI, BaseURL: f.mock.base, Model: "gpt-mock", APIKey: &key}
	if model != "" {
		in.Embeddings = &ai.EmbeddingsPut{Model: model}
	}
	if _, err := f.ai.PutSettings(userCtx(f.org, tenancy.RoleOwner), in); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) account(switches map[string]any) string {
	f.t.Helper()
	id := store.NewID(store.PrefixAccount)
	if _, err := f.db.Collection(accounts.Collection).InsertOne(f.ctx, bson.M{
		"_id": id, "organizationId": f.org, "state": "connected", "switches": switches, "name": "A", "email": id + "@x.test",
	}); err != nil {
		f.t.Fatal(err)
	}
	return id
}

var uidSeq atomic.Uint32

// message puts a message into the index (with its folder) and gives it a body.
func (f *fixture) message(acc, subject, body string, date time.Time) string {
	f.t.Helper()
	mbx := "mbx_" + strings.TrimPrefix(acc, "acc_")
	_, _ = f.db.Collection(index.Mailboxes).UpdateOne(f.ctx, bson.M{"_id": mbx},
		bson.M{"$set": bson.M{"organizationId": f.org, "accountId": acc, "path": "INBOX", "status": "active"}}, options.UpdateOne().SetUpsert(true))
	id := store.NewID(store.PrefixMessage)
	d := date
	if _, err := f.db.Collection(index.Messages).InsertOne(f.ctx, index.Message{
		ID: id, OrganizationID: f.org, AccountID: acc, MailboxID: mbx, UID: uidSeq.Add(1), EmailID: id,
		InternalDate: date, Size: int64(len(body)), IndexedAt: time.Now().UTC(), Subject: subject,
		From: &index.Address{Name: "Admin", Address: "admin@example.com"}, Date: &d,
	}); err != nil {
		f.t.Fatal(err)
	}
	f.fetch.mu.Lock()
	f.fetch.bodies[id] = body
	f.fetch.mu.Unlock()
	return id
}

// received runs the full-text indexer on a message.received event (which notifies the embedder).
func (f *fixture) received(acc, msgID string) {
	f.t.Helper()
	data, _ := json.Marshal(map[string]any{"accountId": acc, "messageId": msgID})
	if err := f.fts.HandleEvent(f.ctx, events.Event{ID: store.NewID(store.PrefixEvent), Type: events.MessageReceived,
		EBOrg: f.org, EBAccount: acc, Data: data}); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) event(typ, acc string, data map[string]any) {
	f.t.Helper()
	raw, _ := json.Marshal(data)
	if err := f.fts.HandleEvent(f.ctx, events.Event{ID: store.NewID(store.PrefixEvent), Type: typ, EBOrg: f.org, EBAccount: acc, Data: raw}); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) chunks(filter bson.M) int64 {
	f.t.Helper()
	n, err := f.db.Collection(Collection).CountDocuments(f.ctx, filter)
	if err != nil {
		f.t.Fatal(err)
	}
	return n
}

func (f *fixture) waitChunks(filter bson.M, want func(int64) bool, what string) {
	f.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		n := f.chunks(filter)
		if want(n) {
			return
		}
		if time.Now().After(deadline) {
			f.t.Fatalf("%s: %d chunks", what, n)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func userCtx(org string, role tenancy.Role) context.Context {
	ctx := store.WithOrganization(context.Background(), org)
	return tenancy.WithCaller(ctx, tenancy.Caller{Kind: tenancy.CallerUser, UserID: "usr_" + strings.Repeat("u", 26), OrganizationID: org, Role: role})
}

func keyCtx(org, boundAccount string, scopes ...string) context.Context {
	ctx := store.WithOrganization(context.Background(), org)
	return tenancy.WithCaller(ctx, tenancy.Caller{Kind: tenancy.CallerAPIKey, APIKeyID: store.NewID(store.PrefixAPIKey),
		OrganizationID: org, Scopes: scopes, AccountID: boundAccount})
}

// corpus indexes three messages and waits until they are embedded.
func (f *fixture) corpus(acc string) (assembly, deed, invoice string) {
	now := time.Now().UTC().Truncate(time.Second)
	assembly = f.message(acc, "Convocation of the condominium assembly",
		"Dear owners, the ordinary condominium assembly is called for 14 November at 18:00 in the meeting room. "+
			"Agenda: budget approval, roof repairs, election of the administrator.", now.Add(-48*time.Hour))
	deed = f.message(acc, "Deed of sale draft",
		"Good morning, the notary sends the draft deed of sale for the apartment. Please check the cadastral data "+
			"and the price before the signing at the notary office.", now.Add(-24*time.Hour))
	invoice = f.message(acc, "Invoice 2026-1043",
		"Please find the invoice for electricity supply attached. Payment due within 30 days.", now)
	for _, id := range []string{assembly, deed, invoice} {
		f.received(acc, id)
	}
	f.waitChunks(bson.M{"accountId": acc, "chunk": 0}, func(n int64) bool { return n == 3 }, "corpus embedded")
	return assembly, deed, invoice
}

func TestIntegrationEmbedOnIndexSearchAndAsk(t *testing.T) {
	f := newFixture(t)
	f.enableAll()
	f.configureAI("")
	acc := f.account(nil)
	other := f.account(nil)
	assembly, deed, _ := f.corpus(acc)

	var c Chunk
	if err := f.db.Collection(Collection).FindOne(f.ctx, bson.M{"_id": chunkID(assembly, 0)}).Decode(&c); err != nil {
		t.Fatal(err)
	}
	if c.Model != ai.DefaultEmbeddingModel || c.Dims != 64 || c.AccountID != acc || c.OrganizationID != f.org ||
		c.MailboxID == "" || c.Date.IsZero() || c.TextHash == "" || !strings.HasPrefix(c.Text, "Convocation of the condominium assembly") {
		t.Fatalf("chunk %+v", c)
	}
	if v, ok := c.Vector.Float32OK(); !ok || len(v) != 64 {
		t.Fatal("vector must be a float32 BSON vector")
	}
	f.mock.mu.Lock()
	auth := f.mock.lastAuth
	f.mock.mu.Unlock()
	if auth != "Bearer sk-test-main" {
		t.Fatalf("main key reused for embeddings: %q", auth)
	}

	// Indexing the same text again is not sent to the provider again.
	before := f.mock.requests.Load()
	if ok, err := f.svc.EmbedMessage(systemCtx(f.ctx, f.org, acc), f.org, acc, assembly); err != nil || ok {
		t.Fatalf("unchanged message re-embedded: %v %v", ok, err)
	}
	if f.mock.requests.Load() != before {
		t.Fatal("no provider call for an unchanged message")
	}

	owner := userCtx(f.org, tenancy.RoleOwner)
	res, err := f.svc.Search(owner, SearchInput{AccountID: acc, Q: "when is the condominium assembly meeting"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Data) != 3 || res.Data[0].MessageID != assembly || res.Model != ai.DefaultEmbeddingModel || res.Chunks != 3 {
		t.Fatalf("ranking: %+v", res)
	}
	h := res.Data[0]
	if h.Subject != "Convocation of the condominium assembly" || h.MailboxID == "" || h.Path != "INBOX" || h.AccountID != acc ||
		!strings.Contains(h.Snippet, "assembly") || h.Score <= res.Data[1].Score || h.From == "" {
		t.Fatalf("hit %+v", h)
	}
	if res, err := f.svc.Search(owner, SearchInput{AccountID: acc, Q: "notary deed signing", Limit: 1}); err != nil ||
		len(res.Data) != 1 || res.Data[0].MessageID != deed {
		t.Fatalf("deed: %+v %v", res, err)
	}
	// Date filter: only the last day.
	if res, err := f.svc.Search(owner, SearchInput{AccountID: acc, Q: "condominium assembly", From: time.Now().Add(-30 * time.Hour)}); err != nil ||
		len(res.Data) != 2 || res.Data[0].MessageID == assembly || res.Data[1].MessageID == assembly {
		t.Fatalf("date filter: %+v %v", res, err)
	}
	for _, in := range []SearchInput{{AccountID: acc}, {AccountID: acc, Q: "x", Limit: 51},
		{AccountID: acc, Q: "x", From: time.Now(), To: time.Now().Add(-time.Hour)}} {
		if _, err := f.svc.Search(owner, in); errs.CodeOf(err) != errs.CodeValidationFailed {
			t.Fatalf("%+v: %v", in, err)
		}
	}
	// The per-query cap: beyond it the caller is asked to narrow by date.
	f.svc.MaxChunks = 2
	if _, err := f.svc.Search(owner, SearchInput{AccountID: acc, Q: "assembly"}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("cap: %v", err)
	}
	if _, err := f.svc.Ask(owner, AskInput{AccountID: acc, Question: "When is the assembly?"}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("ask cap: %v", err)
	}
	// A date range brings a large mailbox under the cap: search and ask work on the narrowed range.
	lastDay := time.Now().Add(-30 * time.Hour)
	if res, err := f.svc.Search(owner, SearchInput{AccountID: acc, Q: "notary deed", From: lastDay}); err != nil || len(res.Data) != 2 {
		t.Fatalf("search under the cap: %+v %v", res, err)
	}
	if ans, err := f.svc.Ask(owner, AskInput{AccountID: acc, Question: "notary deed signing?", From: lastDay}); err != nil || ans.Excerpts != 2 ||
		len(ans.Citations) != 1 || ans.Citations[0].MessageID == assembly {
		t.Fatalf("ask under the cap: %+v %v", ans, err)
	}
	if _, err := f.svc.Ask(owner, AskInput{AccountID: acc, Question: "x", From: time.Now(), To: lastDay}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("ask range: %v", err)
	}
	f.svc.MaxChunks = 0
	// Another tenant's account does not exist for this caller.
	if _, err := f.svc.Search(userCtx(store.NewID(store.PrefixOrganization), tenancy.RoleOwner), SearchInput{AccountID: acc, Q: "assembly"}); errs.CodeOf(err) != errs.CodeNotFound {
		t.Fatalf("other tenant: %v", err)
	}

	// API keys: an account-bound key only for its account; a granular key for its account.
	if _, err := f.svc.Search(keyCtx(f.org, other, "messages:read"), SearchInput{AccountID: acc, Q: "assembly"}); errs.CodeOf(err) != errs.CodeForbidden {
		t.Fatalf("bound key, other account: %v", err)
	}
	if res, err := f.svc.Search(keyCtx(f.org, acc, "messages:read"), SearchInput{AccountID: acc, Q: "assembly"}); err != nil || len(res.Data) == 0 {
		t.Fatalf("bound key, own account: %v", err)
	}
	if _, err := f.svc.Search(keyCtx(f.org, "", "messages:read:"+other), SearchInput{AccountID: acc, Q: "assembly"}); errs.CodeOf(err) != errs.CodeScopeMissing {
		t.Fatalf("granular key, other account: %v", err)
	}
	if _, err := f.svc.Ask(keyCtx(f.org, other, "messages:read"), AskInput{AccountID: acc, Question: "when?"}); errs.CodeOf(err) != errs.CodeForbidden {
		t.Fatalf("bound key ask: %v", err)
	}

	// Ask: the answer cites the excerpt it used; an invented ID is dropped.
	ans, err := f.svc.Ask(owner, AskInput{AccountID: acc, Question: "When is the next condominium assembly?"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ans.Answer, "14 November") || strings.Contains(ans.Answer, fakeID) || len(ans.Citations) != 1 ||
		ans.Citations[0].MessageID != assembly || ans.Citations[0].MailboxID == "" || ans.Citations[0].Subject == "" || ans.Excerpts != 3 {
		t.Fatalf("ask: %+v", ans)
	}
	prompt, _ := f.prompt.Load().(string)
	if !strings.Contains(prompt, "Question: When is the next condominium assembly?") || strings.Contains(prompt, "admin@example.com") {
		t.Fatalf("prompt (addresses are hashed with piiRedact on): %s", prompt)
	}
	var rec audit.Record
	if err := f.db.Collection(audit.Collection).FindOne(f.ctx, bson.M{"action": "ai.ask", "changes.question.after": "When is the next condominium assembly?"}).Decode(&rec); err != nil {
		t.Fatal("audit record of ask: ", err)
	}
	raw, _ := bson.MarshalExtJSON(rec, false, false)
	if !strings.Contains(string(raw), "next condominium assembly") || strings.Contains(string(raw), "14 November") || rec.Target.ID != acc {
		t.Fatalf("audit holds the question, never the answer: %s", raw)
	}
	if _, err := f.svc.Ask(owner, AskInput{AccountID: acc, Question: " "}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatal("empty question")
	}
	// An account with nothing embedded: an empty answer without a provider call.
	f.prompt.Store("")
	if ans, err := f.svc.Ask(owner, AskInput{AccountID: other, Question: "anything?"}); err != nil || ans.Answer != "" || ans.Excerpts != 0 {
		t.Fatalf("empty account: %+v %v", ans, err)
	}
	if p, _ := f.prompt.Load().(string); p != "" {
		t.Fatal("no completion without excerpts")
	}

	// Per-user rate limit of ask.
	f2 := &Service{DB: f.db, Repo: f.svc.Repo, AI: f.ai, Switches: f.svc.Switches, FullText: f.svc.FullText, AskPerMinute: 0.001, Log: f.svc.Log}
	var limited error
	for range 5 {
		if _, err := f2.Ask(owner, AskInput{AccountID: acc, Question: "When is the assembly?"}); err != nil {
			limited = err
			break
		}
	}
	if errs.CodeOf(limited) != errs.CodeRateLimited {
		t.Fatalf("rate limit: %v", limited)
	}

	// Status.
	st, err := f.svc.Status(owner, acc)
	if err != nil || !st.Enabled || st.Messages != 3 || st.Chunks != 3 || st.IndexedMessages != 3 || st.Model != ai.DefaultEmbeddingModel {
		t.Fatalf("status %+v %v", st, err)
	}
}

func TestIntegrationGatingAndPurge(t *testing.T) {
	f := newFixture(t)
	f.configureAI("")
	acc := f.account(nil)
	owner := userCtx(f.org, tenancy.RoleOwner)

	// Everything off: nothing is embedded, the API says why.
	f.setOrgSwitches(map[string]bool{fulltext.SwitchName: true, "ai.enabled": true})
	m := f.message(acc, "Hello", "assembly on Monday", time.Now())
	f.received(acc, m)
	time.Sleep(200 * time.Millisecond)
	if n := f.chunks(bson.M{"accountId": acc}); n != 0 {
		t.Fatalf("switch off: %d chunks", n)
	}
	if st, _ := f.svc.Status(owner, acc); st.Enabled || st.Reason != ReasonSwitchOff {
		t.Fatalf("%+v", st)
	}
	if _, err := f.svc.Search(owner, SearchInput{AccountID: acc, Q: "x"}); errs.CodeOf(err) != errs.CodeFeatureDisabled {
		t.Fatal(err)
	}
	f.setOrgSwitches(map[string]bool{fulltext.SwitchName: true, SwitchName: true})
	if st, _ := f.svc.Status(owner, acc); st.Reason != ReasonAIOff {
		t.Fatalf("ai off: %+v", st)
	}
	f.setOrgSwitches(map[string]bool{SwitchName: true, "ai.enabled": true})
	if st, _ := f.svc.Status(owner, acc); st.Reason != ReasonFullTextOff {
		t.Fatalf("fulltext off: %+v", st)
	}
	// An account cannot opt in alone while the organisation is off.
	optIn := f.account(map[string]any{SwitchName: true})
	f.setOrgSwitches(map[string]bool{fulltext.SwitchName: true, "ai.enabled": true})
	if st, _ := f.svc.Status(owner, optIn); st.Reason != ReasonSwitchOff {
		t.Fatalf("opt-in alone: %+v", st)
	}

	f.enableAll()
	if _, err := f.svc.Status(owner, store.NewID(store.PrefixAccount)); errs.CodeOf(err) != errs.CodeNotFound {
		t.Fatalf("missing account: %v", err)
	}
	a1 := f.message(acc, "One", "first message about the roof repairs", time.Now())
	a2 := f.message(acc, "Two", "second message about the garden", time.Now())
	f.received(acc, a1)
	f.received(acc, a2)
	f.waitChunks(bson.M{"accountId": acc}, func(n int64) bool { return n == 2 }, "embedded")

	// message.deleted removes the message's vectors (through the full-text observer call).
	f.event(events.MessageDeleted, acc, map[string]any{"accountId": acc, "messageId": a1})
	if f.chunks(bson.M{"messageId": a1}) != 0 || f.chunks(bson.M{"messageId": a2}) != 1 {
		t.Fatal("message delete")
	}
	// mailbox.deleted removes the folder's vectors.
	f.event(events.MailboxDeleted, acc, map[string]any{"accountId": acc, "mailbox": map[string]any{"mailboxId": "mbx_" + strings.TrimPrefix(acc, "acc_")}})
	if f.chunks(bson.M{"accountId": acc}) != 0 {
		t.Fatal("mailbox delete")
	}

	// The account opts out: account.updated purges at once.
	f.received(acc, a2)
	f.waitChunks(bson.M{"accountId": acc}, func(n int64) bool { return n == 1 }, "re-embedded")
	if _, err := f.db.Collection(accounts.Collection).UpdateOne(f.ctx, bson.M{"_id": acc}, bson.M{"$set": bson.M{"switches": bson.M{SwitchName: false}}}); err != nil {
		t.Fatal(err)
	}
	f.event(events.AccountUpdated, acc, map[string]any{"accountId": acc, "changed": []string{"switches"}})
	if f.chunks(bson.M{"accountId": acc}) != 0 {
		t.Fatal("opt-out must purge at once")
	}
	if _, err := f.db.Collection(accounts.Collection).UpdateOne(f.ctx, bson.M{"_id": acc}, bson.M{"$unset": bson.M{"switches": ""}}); err != nil {
		t.Fatal(err)
	}

	// The organisation switch goes off: the sweep purges.
	f.received(acc, a2)
	f.waitChunks(bson.M{"accountId": acc}, func(n int64) bool { return n == 1 }, "re-embedded")
	f.setOrgSwitches(map[string]bool{fulltext.SwitchName: true, "ai.enabled": true})
	if n, err := f.svc.PurgeOnce(f.ctx); err != nil || n != 1 || f.chunks(bson.M{"accountId": acc}) != 0 {
		t.Fatalf("sweep: %d %v", n, err)
	}

	// A model change deletes the vectors of the old model.
	f.enableAll()
	f.received(acc, a2)
	f.waitChunks(bson.M{"accountId": acc, "model": ai.DefaultEmbeddingModel}, func(n int64) bool { return n == 1 }, "re-embedded")
	f.configureAI("other-model")
	f.waitChunks(bson.M{"accountId": acc}, func(n int64) bool { return n == 0 }, "model change purge")
	if st, _ := f.svc.Status(owner, acc); st.Model != "other-model" || st.Chunks != 0 {
		t.Fatalf("%+v", st)
	}
	// Backfill (the sweep) embeds recent rows that have no vectors of the current model.
	if n, err := f.svc.BackfillOnce(f.ctx); err != nil || n != 1 || f.chunks(bson.M{"accountId": acc, "model": "other-model"}) != 1 {
		t.Fatalf("backfill: %d %v", n, err)
	}

	// Account erasure deletes exactly the account's vectors.
	keep := f.account(nil)
	k := f.message(keep, "Keep", "keep this message about the elevator", time.Now())
	f.received(keep, k)
	f.waitChunks(bson.M{"accountId": keep}, func(n int64) bool { return n == 1 }, "keep embedded")
	js := &jobs.Service{DB: f.db}
	as := &accounts.Service{DB: f.db, Jobs: js}
	as.RegisterHandlers()
	if _, err := js.RunNow(f.ctx, "erasure", f.org, map[string]string{"accountId": acc, "organizationId": f.org}, nil); err != nil {
		t.Fatal(err)
	}
	if f.chunks(bson.M{"accountId": acc}) != 0 || f.chunks(bson.M{"accountId": keep}) != 1 {
		t.Fatal("account erasure")
	}
	if e, ok := tenancy.RegistryByName(Collection); !ok || e.Class != tenancy.EraseBoth {
		t.Fatalf("registry entry: %+v", e)
	}
	// account.deleted: the full-text purge tells the observer, which purges again (no-op, never fails).
	f.event(events.AccountDeleted, keep, map[string]any{"accountId": keep})
	if f.chunks(bson.M{"accountId": keep}) != 0 {
		t.Fatal("account.deleted")
	}
}

func TestIntegrationRetriesAndReindex(t *testing.T) {
	f := newFixture(t)
	f.enableAll()
	f.configureAI("")
	acc := f.account(nil)
	owner := userCtx(f.org, tenancy.RoleOwner)

	// Two failed requests: the worker retries with back-off and succeeds.
	f.mock.fail.Store(2)
	m := f.message(acc, "Retry", "a message that needs a retry", time.Now())
	f.received(acc, m)
	f.waitChunks(bson.M{"messageId": m}, func(n int64) bool { return n == 1 }, "retried")

	// Rows that existed before the switch (written to the text store without the observer).
	var ids []string
	for i := range 5 {
		id := f.message(acc, fmt.Sprintf("Old %d", i), strings.Repeat(fmt.Sprintf("older message number %d with words. ", i), 60), time.Now().Add(-time.Duration(i+1)*time.Hour))
		ids = append(ids, id)
		if err := f.fts.Repo.Upsert(f.ctx, &fulltext.Row{OrganizationID: f.org, AccountID: acc, MessageID: id,
			MailboxID: "mbx_" + strings.TrimPrefix(acc, "acc_"), Subject: fmt.Sprintf("Old %d", i),
			Text: strings.Repeat(fmt.Sprintf("older message number %d with words. ", i), 60), Date: time.Now().Add(-time.Duration(i+1) * time.Hour)}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.svc.StartReindex(owner, acc, 0); err != nil {
		t.Fatal(err)
	}
	if j, err := f.svc.StartReindex(owner, acc, 10); err != nil || j.Kind != JobKind {
		t.Fatalf("dedupe: %v", err)
	}
	if _, err := f.svc.StartReindex(owner, acc, MaxReindexLimit+1); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatal("limit")
	}
	if _, err := f.svc.StartReindex(userCtx(f.org, tenancy.RoleViewer), acc, 0); errs.CodeOf(err) != errs.CodeForbidden {
		t.Fatalf("viewer: %v", err)
	}
	reqs := f.mock.requests.Load()
	out, err := f.svc.Jobs.RunNow(f.ctx, JobKind, f.org, reindexPayload{AccountID: acc, Limit: 100}, nil)
	if err != nil {
		t.Fatal(err)
	}
	r := out.(ReindexResult)
	if r.Scanned != 6 || r.Embedded != 5 || r.AlreadyEmbedded != 1 || r.Failed != 0 || r.Chunks < 10 {
		t.Fatalf("reindex: %+v", r)
	}
	// Several messages per request (each long message has 2–3 chunks, 32 chunks per request).
	if got := f.mock.requests.Load() - reqs; got != 1 {
		t.Fatalf("requests: %d", got)
	}
	out, err = f.svc.Jobs.RunNow(f.ctx, JobKind, f.org, reindexPayload{AccountID: acc, Limit: 100}, nil)
	if err != nil || out.(ReindexResult).AlreadyEmbedded != 6 || out.(ReindexResult).Embedded != 0 {
		t.Fatalf("idempotent: %+v %v", out, err)
	}
	// A provider that keeps failing: the job counts the messages failed and goes on.
	if _, err := f.db.Collection(Collection).DeleteMany(f.ctx, bson.M{"messageId": ids[0]}); err != nil {
		t.Fatal(err)
	}
	f.mock.fail.Store(100)
	out, err = f.svc.Jobs.RunNow(f.ctx, JobKind, f.org, reindexPayload{AccountID: acc, Limit: 100}, nil)
	f.mock.fail.Store(0)
	if err != nil || out.(ReindexResult).Failed != 1 {
		t.Fatalf("failing provider: %+v %v", out, err)
	}
	// Not configured: anthropic without an embeddings block.
	key := "sk-ant"
	if _, err := f.ai.PutSettings(owner, ai.PutInput{Provider: ai.ProviderAnthropic, Model: "claude", APIKey: &key, Embeddings: &ai.EmbeddingsPut{}}); err != nil {
		t.Fatal(err)
	}
	if st, _ := f.svc.Status(owner, acc); st.Reason != ReasonNotConfigured {
		t.Fatalf("%+v", st)
	}
	if _, err := f.svc.Search(owner, SearchInput{AccountID: acc, Q: "x"}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("not configured: %v", err)
	}
}

func TestIntegrationModuleHTTP(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	a := app.New(app.Options{Roles: app.RoleSet{app.RoleAPI: true}, NodeID: "n1", Log: quiet})
	app.Provide(a, db)
	js := &jobs.Service{DB: db}
	app.Provide(a, js)
	fts := &fulltext.Service{DB: db, Repo: &fulltext.Repo{DB: db}, Switches: &fulltext.Switches{DB: db, Registry: features.DefaultRegistry(), Env: "test", TTL: -1}}
	app.Provide(a, fts)
	aiSvc := &ai.Service{Repo: &ai.Repo{DB: db}, DB: db, Registry: features.DefaultRegistry(), Env: "test"}
	app.Provide(a, aiSvc)
	srv, err := httpapi.New(httpapi.Options{AccessLog: quiet, SecurityLog: quiet})
	if err != nil {
		t.Fatal(err)
	}
	app.Provide(a, srv)
	m := &Module{Env: "test", Registry: features.DefaultRegistry(), Log: quiet}
	if err := m.Start(ctx, a); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Stop(ctx) })
	if m.Name() != "embeddings" || m.Service() == nil || m.Service().Jobs == nil || aiSvc.EmbeddingsChanged == nil {
		t.Fatal("module wiring")
	}
	m.Service().Switches.TTL = -1
	org := store.NewID(store.PrefixOrganization)
	acc := store.NewID(store.PrefixAccount)
	if _, err := db.Collection(accounts.Collection).InsertOne(ctx, bson.M{"_id": acc, "organizationId": org, "state": "connected"}); err != nil {
		t.Fatal(err)
	}
	do := func(method, target, body string) *httptest.ResponseRecorder {
		var rd io.Reader
		if body != "" {
			rd = strings.NewReader(body)
		}
		r := httptest.NewRequest(method, target, rd).WithContext(userCtx(org, tenancy.RoleOwner))
		if body != "" {
			r.Header.Set("Content-Type", "application/json")
		}
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, r)
		return w
	}
	w := do(http.MethodGet, "/v1/accounts/"+acc+"/embeddings", "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"reason":"switch_off"`) || !strings.Contains(w.Body.String(), `"enabled":false`) {
		t.Fatalf("status: %d %s", w.Code, w.Body.String())
	}
	if w := do(http.MethodGet, "/v1/accounts/"+acc+"/semantic-search?q="+url.QueryEscape("assembly date"), ""); w.Code != http.StatusForbidden ||
		!strings.Contains(w.Body.String(), "feature_disabled") {
		t.Fatalf("search off: %d %s", w.Code, w.Body.String())
	}
	if w := do(http.MethodGet, "/v1/accounts/"+acc+"/semantic-search", ""); w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("missing q: %d", w.Code)
	}
	if w := do(http.MethodPost, "/v1/accounts/"+acc+"/ask", `{"question":"When?"}`); w.Code != http.StatusForbidden {
		t.Fatalf("ask off: %d %s", w.Code, w.Body.String())
	}
	if w := do(http.MethodPost, "/v1/accounts/"+acc+"/ask", `{"question":"`+strings.Repeat("q", 1001)+`"}`); w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("long question: %d", w.Code)
	}
	if w := do(http.MethodPost, "/v1/accounts/"+acc+"/embeddings/reindex", ""); w.Code != http.StatusForbidden {
		t.Fatalf("reindex off: %d %s", w.Code, w.Body.String())
	}
	if w := do(http.MethodGet, "/v1/accounts/"+store.NewID(store.PrefixAccount)+"/embeddings", ""); w.Code != http.StatusNotFound {
		t.Fatalf("missing account: %d", w.Code)
	}
	// With the switches on but no AI settings: the endpoint is not configured.
	if _, err := db.Collection(settings.SettingsCollection).InsertOne(ctx, bson.M{"_id": settings.OrgDocID(org), "switches": bson.M{
		SwitchName: bson.M{"value": true}, fulltext.SwitchName: bson.M{"value": true}, "ai.enabled": bson.M{"value": true}}}); err != nil {
		t.Fatal(err)
	}
	if w := do(http.MethodGet, "/v1/accounts/"+acc+"/embeddings", ""); !strings.Contains(w.Body.String(), `"reason":"not_configured"`) {
		t.Fatalf("not configured: %s", w.Body.String())
	}
	if w := do(http.MethodPost, "/v1/accounts/"+acc+"/embeddings/reindex", ""); w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("reindex not configured: %d %s", w.Code, w.Body.String())
	}
}
