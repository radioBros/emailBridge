//go:build e2e && load

// Package load is the load harness of S14 §5 (P2.4): a real EmailBridge (one node, or a role-split cluster)
// against the in-process provider simulator, N accounts, mail injected at a fixed rate, a webhook sink, and a
// report of event-to-webhook latency, lost and duplicated deliveries. Never against the live mail server.
//
//	EB_LOAD_ACCOUNTS (200)  EB_LOAD_RATE events/s (20)  EB_LOAD_DURATION (2m)  EB_LOAD_NODES (1 or ≥ 3)
//	EB_LOAD_ENDPOINT_CONCURRENCY (32)  EB_LOAD_SEND_ACCOUNTS (20)  EB_LOAD_SEND_RATE sends/s (10)
//
// Run: task test:load (results in load-results/<timestamp>.json, gitignored).
package load

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	gosync "sync"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/radiobros/emailbridge/simulator/mem"
	"github.com/radiobros/emailbridge/test/e2e/harness"
	"github.com/radiobros/emailbridge/test/sink"
)

func envInt(key string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(key)); err == nil && v > 0 {
		return v
	}
	return def
}

func envDur(key string, def time.Duration) time.Duration {
	if v, err := time.ParseDuration(os.Getenv(key)); err == nil && v > 0 {
		return v
	}
	return def
}

// Report is what one load run measured.
type Report struct {
	At            time.Time `json:"at"`
	Nodes         int       `json:"nodes"`
	Accounts      int       `json:"accounts"`
	RatePerSec    int       `json:"ratePerSec"`
	Duration      string    `json:"duration"`
	ConnectAll    string    `json:"connectAll"`
	Injected      int       `json:"injected"`
	Delivered     int       `json:"delivered"`
	Lost          int       `json:"lost"`
	Duplicates    int       `json:"duplicates"`
	LatencyP50Ms  int64     `json:"latencyP50Ms"`
	LatencyP95Ms  int64     `json:"latencyP95Ms"`
	LatencyP99Ms  int64     `json:"latencyP99Ms"`
	LatencyMaxMs  int64     `json:"latencyMaxMs"`
	DeliveredRate float64   `json:"deliveredPerSec"`
}

func TestLoadEventToWebhook(t *testing.T) {
	accounts := envInt("EB_LOAD_ACCOUNTS", 200)
	rate := envInt("EB_LOAD_RATE", 20)
	duration := envDur("EB_LOAD_DURATION", 2*time.Minute)
	nodes := envInt("EB_LOAD_NODES", 1)

	sim := harness.StartSim(t)
	tuning := map[string]string{
		"EB_SYNC_CONNECT_RATE": "500/s", "EB_SYNC_MAX_ACCOUNTS": strconv.Itoa(accounts + 100),
		"EB_RATE_API_ORG": "5000/s,burst=5000", "EB_RATE_API_KEY": "5000/s,burst=5000",
	}
	var api *harness.Node
	if nodes >= 3 {
		env := harness.ClusterEnv(t, "load-api", "api,scheduler")
		for k, v := range tuning {
			env[k] = v
		}
		api = harness.StartP1Node(t, env)
		for i := range nodes - 2 {
			harness.StartPeer(t, api, fmt.Sprintf("load-sync-%d", i+1), "sync,submit", tuning)
		}
		harness.StartPeer(t, api, "load-webhook-1", "webhook", tuning)
	} else {
		api = harness.StartP1Node(t, tuning)
	}
	owner, _ := api.OwnerSession(t)

	sk := harness.StartSink(t, sink.Options{})
	ep := owner.Do(http.MethodPost, "/v1/webhook-endpoints", map[string]any{
		"name": "load", "kind": "http", "url": sk.URL, "secret": harness.WebhookSecret(),
		"events": []string{"message.received"},
		"retry":  map[string]any{"maxAttempts": 10, "backoffBase": "500ms", "backoffJitter": 0.1, "timeout": "5s"},
		// One endpoint receives every account's events (the LegacyApp shape): its throughput is
		// maxConcurrency / delivery time, so the harness sets it like a high-volume receiver would.
		"delivery": map[string]any{"maxConcurrency": envInt("EB_LOAD_ENDPOINT_CONCURRENCY", 32)},
	})
	harness.ExpectStatus(t, ep, http.StatusCreated)

	// Accounts: simulator users created in-process, registered through the API without the live verify.
	users := make([]*mem.User, accounts)
	host, port := harness.HostPort(t, sim.Addr.IMAPPlain)
	shost, sport := harness.HostPort(t, sim.Addr.SMTPPlain)
	start := time.Now()
	var wg gosync.WaitGroup
	errs := make(chan string, accounts)
	sem := make(chan struct{}, 8)
	for i := range accounts {
		name := fmt.Sprintf("load%05d", i)
		u, err := sim.Sim.CreateUser(name, "pw")
		if err != nil {
			t.Fatal(err)
		}
		users[i] = u
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer func() { <-sem; wg.Done() }()
			r := owner.Do(http.MethodPost, "/v1/accounts", map[string]any{
				"name": name, "email": name + "@sim.load.test", "skipVerify": true,
				"imap":     map[string]any{"host": host, "port": port, "tls": "none", "auth": map[string]any{"user": name, "pass": "pw"}},
				"smtp":     map[string]any{"host": shost, "port": sport, "tls": "none", "auth": map[string]any{"user": name, "pass": "pw"}},
				"sync":     map[string]any{"paths": []string{"INBOX"}, "idlePaths": []string{"INBOX"}},
				"switches": map[string]any{"sandbox.enabled": false},
			})
			if r.Status != http.StatusCreated {
				errs <- fmt.Sprintf("%s: %d %s", name, r.Status, r.Body)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Fatalf("create account: %s", e)
	}
	stopPlacement := samplePlacement(t, api)
	owner.WaitJSON(t, "/v1/accounts?state=connected&page=1&limit=1&includeTotal=true", 10*time.Minute, "all accounts connected", func(r harness.Resp) bool {
		n, _ := r.JSON["total"].(float64)
		return r.Status == http.StatusOK && int(n) == accounts
	})
	stopPlacement()
	connectAll := time.Since(start)
	t.Logf("%d accounts connected in %s", accounts, connectAll.Round(time.Millisecond))
	time.Sleep(3 * time.Second) // let the silent first syncs settle
	sk.Reset()

	sent := map[string]time.Time{}
	var mu gosync.Mutex
	stopSampler := samplePipeline(t, api, sk, func() int {
		mu.Lock()
		defer mu.Unlock()
		return len(sent)
	})
	defer stopSampler()

	// Paced injection; the send time of every Message-ID is kept for the latency.
	tick := time.NewTicker(time.Second / time.Duration(rate))
	stop := time.After(duration)
	seq := 0
inject:
	for {
		select {
		case <-stop:
			break inject
		case <-tick.C:
			seq++
			mid := fmt.Sprintf("<load-%d@x>", seq)
			u := users[rand.IntN(accounts)]
			raw := fmt.Appendf(nil, "From: load@x\r\nTo: %s@sim.load.test\r\nSubject: load %d\r\nMessage-ID: %s\r\n\r\nbody\r\n", u.Username(), seq, mid)
			mu.Lock()
			sent[mid] = time.Now()
			mu.Unlock()
			if _, _, err := u.InjectMessage("INBOX", raw, nil, time.Now()); err != nil {
				t.Fatal(err)
			}
		}
	}
	tick.Stop()
	injectEnd := time.Now()

	// Wait for the tail (bounded), then measure.
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) && len(harness.FilterEvents(sk.Requests(), "message.received")) < seq {
		time.Sleep(500 * time.Millisecond)
	}
	var lat []time.Duration
	seen := map[string]int{}
	var last time.Time
	for _, r := range harness.FilterEvents(sk.Requests(), "message.received") {
		seen[r.Headers.Get("webhook-id")]++
		var env struct {
			Data struct {
				MessageIDHeader string `json:"messageIdHeader"`
			} `json:"data"`
		}
		if json.Unmarshal(r.Body, &env) != nil {
			continue
		}
		if at, ok := sent[env.Data.MessageIDHeader]; ok && seen[r.Headers.Get("webhook-id")] == 1 {
			lat = append(lat, r.Time.Sub(at))
			if r.Time.After(last) {
				last = r.Time
			}
		}
	}
	slices.Sort(lat)
	pct := func(p float64) int64 {
		if len(lat) == 0 {
			return 0
		}
		return lat[min(len(lat)-1, int(float64(len(lat))*p))].Milliseconds()
	}
	dups := 0
	for _, c := range seen {
		dups += c - 1
	}
	rep := Report{At: time.Now().UTC(), Nodes: nodes, Accounts: accounts, RatePerSec: rate, Duration: duration.String(),
		ConnectAll: connectAll.Round(time.Millisecond).String(), Injected: seq, Delivered: len(lat),
		Lost: seq - len(lat), Duplicates: dups, LatencyP50Ms: pct(0.50), LatencyP95Ms: pct(0.95), LatencyP99Ms: pct(0.99)}
	if len(lat) > 0 {
		rep.LatencyMaxMs = lat[len(lat)-1].Milliseconds()
		rep.DeliveredRate = float64(len(lat)) / last.Sub(injectEnd.Add(-duration)).Seconds()
	}
	out, _ := json.MarshalIndent(rep, "", "  ")
	t.Logf("load report:\n%s", out)
	dir := filepath.Join(harness.FindRepoRoot(), "load-results")
	if err := os.MkdirAll(dir, 0o750); err == nil {
		_ = os.WriteFile(filepath.Join(dir, rep.At.Format("20060102-150405")+".json"), out, 0o600)
	}
	if rep.Lost > 0 || rep.Duplicates > 0 {
		t.Fatalf("lost %d, duplicated %d (S14 §5: no event lost or duplicated)", rep.Lost, rep.Duplicates)
	}
}

// SubmitReport is what one send-load run measured (S14 §5 "API submit p95").
type SubmitReport struct {
	At          time.Time `json:"at"`
	Accounts    int       `json:"accounts"`
	RatePerSec  int       `json:"ratePerSec"`
	Duration    string    `json:"duration"`
	Submitted   int       `json:"submitted"`
	Rejected    int       `json:"rejected"`
	Sent        int       `json:"sent"`
	SubmitP50Ms int64     `json:"submitP50Ms"`
	SubmitP95Ms int64     `json:"submitP95Ms"`
	SubmitP99Ms int64     `json:"submitP99Ms"`
	SubmitMaxMs int64     `json:"submitMaxMs"`
	// GET /v1/accounts/{id} timed alongside each submit: the cost of the HTTP, auth and tenancy layers
	// alone, so a slow submit can be told apart from a slow stack.
	BaselineP50Ms int64  `json:"baselineP50Ms"`
	BaselineP95Ms int64  `json:"baselineP95Ms"`
	DrainAfter    string `json:"drainAfter"` // last submit → every message accepted by the simulator's SMTP
}

// TestLoadSubmit sends EB_LOAD_SEND_RATE messages/s (10) for EB_LOAD_DURATION over EB_LOAD_SEND_ACCOUNTS
// accounts (20) through POST /v1/accounts/{id}/outbound-messages and measures the API latency and how long
// the queue takes to drain into the simulator's SMTP server. Nothing leaves the machine.
func TestLoadSubmit(t *testing.T) {
	accounts := envInt("EB_LOAD_SEND_ACCOUNTS", 20)
	rate := envInt("EB_LOAD_SEND_RATE", 10)
	duration := envDur("EB_LOAD_DURATION", time.Minute)

	sim := harness.StartSim(t)
	api := harness.StartP1Node(t, map[string]string{
		"EB_RATE_API_ORG": "5000/s,burst=5000", "EB_RATE_API_KEY": "5000/s,burst=5000",
	})
	owner, _ := api.OwnerSession(t)
	ids := make([]string, accounts)
	emails := make([]string, accounts)
	for i := range accounts {
		user := fmt.Sprintf("send%05d", i)
		emails[i] = user + "@sim.load.test"
		sim.CreateSimUser(t, user, "pw")
		// Every recipient shares one domain, so the per-domain politeness throttle (60/min by default) would cap
		// the run at ~60 sends a minute: this test measures the API and the send pipeline, not the throttle.
		body := harness.AccountCreateBody(t, sim, user, "pw", emails[i], user,
			map[string]any{"sandbox.enabled": false, "sending.throttle": false}, "standard")
		body["skipVerify"] = true
		r := owner.Do(http.MethodPost, "/v1/accounts", body)
		harness.ExpectStatus(t, r, http.StatusCreated)
		ids[i] = r.Str("id")
	}
	owner.WaitJSON(t, "/v1/accounts?state=connected&page=1&limit=1&includeTotal=true", 5*time.Minute, "accounts connected", func(r harness.Resp) bool {
		n, _ := r.JSON["total"].(float64)
		return r.Status == http.StatusOK && int(n) == accounts
	})
	ctx := t.Context()
	if err := sim.Client.ClearSMTPMessages(ctx); err != nil {
		t.Fatal(err)
	}

	var (
		mu       gosync.Mutex
		lat      []time.Duration
		base     []time.Duration
		rejected int
		wg       gosync.WaitGroup
	)
	tick := time.NewTicker(time.Second / time.Duration(rate))
	stop := time.After(duration)
	n := 0
send:
	for {
		select {
		case <-stop:
			break send
		case <-tick.C:
		}
		n++
		i := n % accounts
		wg.Add(1)
		go func(n, i int) { // submits overlap like independent API clients
			defer wg.Done()
			c := api.NewClient()
			c.C.Jar = owner.C.Jar
			c.Hdr, c.IP = owner.Hdr, owner.IP
			start := time.Now()
			r := c.Do(http.MethodPost, "/v1/accounts/"+ids[i]+"/outbound-messages", map[string]any{
				"to": []map[string]any{{"address": emails[(i+1)%accounts]}}, "subject": fmt.Sprintf("load send %d", n), "text": "x",
			})
			d := time.Since(start)
			bStart := time.Now()
			b := c.Do(http.MethodGet, "/v1/accounts/"+ids[i], nil)
			bd := time.Since(bStart)
			mu.Lock()
			defer mu.Unlock()
			if b.Status == http.StatusOK {
				base = append(base, bd)
			}
			if r.Status != http.StatusAccepted {
				rejected++
				return
			}
			lat = append(lat, d)
		}(n, i)
	}
	tick.Stop()
	wg.Wait()
	lastSubmit := time.Now()

	submitted := len(lat)
	sent := 0
	deadline := time.Now().Add(3 * time.Minute)
	for time.Now().Before(deadline) {
		msgs, err := sim.Client.ListSMTPMessages(ctx)
		if err == nil {
			sent = len(msgs)
		}
		if sent >= submitted {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	slices.Sort(lat)
	pct := func(p float64) int64 {
		if len(lat) == 0 {
			return 0
		}
		return lat[min(len(lat)-1, int(float64(len(lat))*p))].Milliseconds()
	}
	slices.Sort(base)
	bpct := func(p float64) int64 {
		if len(base) == 0 {
			return 0
		}
		return base[min(len(base)-1, int(float64(len(base))*p))].Milliseconds()
	}
	rep := SubmitReport{BaselineP50Ms: bpct(0.50), BaselineP95Ms: bpct(0.95), At: time.Now().UTC(), Accounts: accounts, RatePerSec: rate, Duration: duration.String(),
		Submitted: submitted, Rejected: rejected, Sent: sent, SubmitP50Ms: pct(0.50), SubmitP95Ms: pct(0.95),
		SubmitP99Ms: pct(0.99), DrainAfter: time.Since(lastSubmit).Round(time.Millisecond).String()}
	if len(lat) > 0 {
		rep.SubmitMaxMs = lat[len(lat)-1].Milliseconds()
	}
	out, _ := json.MarshalIndent(rep, "", "  ")
	t.Logf("submit load report:\n%s", out)
	dir := filepath.Join(harness.FindRepoRoot(), "load-results")
	if err := os.MkdirAll(dir, 0o750); err == nil {
		_ = os.WriteFile(filepath.Join(dir, "submit-"+rep.At.Format("20060102-150405")+".json"), out, 0o600)
	}
	if rejected > 0 || sent < submitted {
		t.Fatalf("rejected %d, sent %d of %d", rejected, sent, submitted)
	}
}

// samplePipeline logs, every 5 s, how far each stage of the pipeline has got — injected, events stored,
// published by the relay, deliveries created by the router, deliveries succeeded, requests at the sink — plus
// the relay lag and stream backlogs from /metrics, so a run that falls behind shows where.
func samplePipeline(t *testing.T, n *harness.Node, sk *sink.Server, injected func() int) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	cl, err := mongo.Connect(options.Client().ApplyURI(n.MongoURI))
	if err != nil {
		t.Logf("sampler: %v", err)
		cancel()
		return func() {}
	}
	db := cl.Database(n.Database)
	count := func(coll string, f bson.M) int64 {
		c, _ := db.Collection(coll).CountDocuments(ctx, f)
		return c
	}
	metrics := func() string {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+n.Env("EB_METRICS_ADDR")+"/metrics", nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return ""
		}
		defer func() { _ = resp.Body.Close() }()
		body, _ := io.ReadAll(resp.Body)
		var keep []string
		for line := range strings.Lines(string(body)) {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "eb_relay_lag_seconds") || (strings.HasPrefix(line, "eb_stream_") && !strings.HasSuffix(line, " 0")) {
				keep = append(keep, strings.TrimPrefix(line, "eb_"))
			}
		}
		return strings.Join(keep, " ")
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		tick := time.NewTicker(5 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			}
			t.Logf("pipeline: injected=%d events=%d published=%d deliveries=%d succeeded=%d sink=%d | %s",
				injected(), count("events", bson.M{"type": "message.received"}),
				count("events", bson.M{"type": "message.received", "published": true}),
				count("deliveries", bson.M{}), count("deliveries", bson.M{"state": "succeeded"}),
				len(sk.Requests()), metrics())
		}
	}()
	return func() {
		cancel()
		<-done
		_ = cl.Disconnect(context.Background())
	}
}

// samplePlacement logs, every 10 s while accounts connect, the accounts by state and by owning node.
func samplePlacement(t *testing.T, n *harness.Node) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	cl, err := mongo.Connect(options.Client().ApplyURI(n.MongoURI))
	if err != nil {
		cancel()
		return func() {}
	}
	coll := cl.Database(n.Database).Collection("accounts")
	group := func(field string) map[string]int {
		out := map[string]int{}
		cur, err := coll.Aggregate(ctx, mongo.Pipeline{{{Key: "$group", Value: bson.M{"_id": "$" + field, "n": bson.M{"$sum": 1}}}}})
		if err != nil {
			return out
		}
		var rows []struct {
			ID any `bson:"_id"`
			N  int `bson:"n"`
		}
		_ = cur.All(ctx, &rows)
		for _, r := range rows {
			out[fmt.Sprint(r.ID)] = r.N
		}
		return out
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		tick := time.NewTicker(10 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			}
			t.Logf("placement: state=%v owner=%v reason=%v", group("state"), group("owner.nodeId"), group("stateReason.code"))
		}
	}()
	return func() {
		cancel()
		<-done
		_ = cl.Disconnect(context.Background())
	}
}
