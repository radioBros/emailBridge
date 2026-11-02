package notify

import (
	"context"
	"log/slog"
	"strconv"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/radiobros/emailbridge/internal/store"
)

// Watchers runs leader-only health scans that Raise/Resolve problems.
type Watchers struct {
	Engine *Engine
	Repo   *Repo
	DB     *store.DB
	Log    *slog.Logger
	Now    func() time.Time
	// BusInfo optionally supplies JetStream stream/consumer stats (nil = skip queue depth checks).
	BusInfo func(ctx context.Context) []QueueStat
	// ClusterNodes optionally lists live nodes (nil = skip node-missing).
	ClusterNodes func(ctx context.Context) []NodeStat
}

// QueueStat is one JetStream / work queue observation.
type QueueStat struct {
	Name       string
	OrgID      string // empty → instance
	Pending    int64
	AckPending int64
	OldestAge  time.Duration
	PausedAt   *time.Time
}

// NodeStat is one cluster node heartbeat.
type NodeStat struct {
	ID       string
	LastSeen time.Time
}

func (w *Watchers) now() time.Time {
	if w.Now != nil {
		return w.Now().UTC()
	}
	return time.Now().UTC()
}

func (w *Watchers) log() *slog.Logger {
	if w.Log != nil {
		return w.Log
	}
	return slog.Default()
}

// Duty is the scheduler leader loop: every watcher once per `notifications.watchers.interval`.
func (w *Watchers) Duty(ctx context.Context) {
	every(ctx, func(ctx context.Context) time.Duration { return watcherInterval(ctx, w.DB) }, w.Tick)
}

// minWatcherInterval bounds `notifications.watchers.interval` from below.
const minWatcherInterval = 5 * time.Second

// pollEvery is how often every() re-reads its interval, so a changed setting applies within seconds.
var pollEvery = time.Second

// watcherInterval is `notifications.watchers.interval` (default 1m, at least minWatcherInterval).
func watcherInterval(ctx context.Context, db *store.DB) time.Duration {
	d := SettingDuration(ctx, db, "", "notifications.watchers.interval", time.Minute)
	if d < minWatcherInterval {
		d = minWatcherInterval
	}
	return d
}

// every runs fn at once and then whenever interval() has elapsed since the last run, until ctx ends. The
// interval is re-read every pollEvery, so lowering it takes effect without waiting for the old period.
func every(ctx context.Context, interval func(context.Context) time.Duration, fn func(context.Context)) {
	fn(ctx)
	last := time.Now()
	t := time.NewTicker(pollEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if time.Since(last) >= interval(ctx) {
			fn(ctx)
			last = time.Now()
		}
	}
}

// Tick runs every watcher once.
func (w *Watchers) Tick(ctx context.Context) {
	w.watchWebhooks(ctx)
	w.watchAccounts(ctx)
	w.watchQueues(ctx)
	w.watchSending(ctx)
	w.watchPlatform(ctx)
	w.watchSecurity(ctx)
	w.watchIntegrations(ctx)
	// pec: no-op until P9 receipts present (watchPEC reserved)
}

func (w *Watchers) watchWebhooks(ctx context.Context) {
	thr := SettingInt(ctx, w.DB, "", "notifications.webhooks.failureThreshold", 5)
	if thr < 1 {
		thr = 1
	}
	dlGrowth := SettingInt(ctx, w.DB, "", "notifications.webhooks.deadLetterGrowth", 10)
	cur, err := w.DB.Collection("webhookEndpoints").Find(ctx, bson.M{},
		options.Find().SetProjection(bson.M{
			"_id": 1, "organizationId": 1, "name": 1, "enabled": 1,
			"breaker.state": 1, "breaker.failures": 1, "disabledReason": 1,
		}))
	if err != nil {
		w.log().Warn("notify webhooks watcher", "err", err.Error())
		return
	}
	defer func() { _ = cur.Close(ctx) }()
	openWanted := map[string]bool{}
	now := w.now()
	for cur.Next(ctx) {
		var ep struct {
			ID      string `bson:"_id"`
			Org     string `bson:"organizationId"`
			Name    string `bson:"name"`
			Enabled bool   `bson:"enabled"`
			Breaker struct {
				State    string `bson:"state"`
				Failures int    `bson:"failures"`
			} `bson:"breaker"`
			DisabledReason *struct {
				Code string `bson:"code"`
			} `bson:"disabledReason"`
		}
		if cur.Decode(&ep) != nil || ep.Org == "" {
			continue
		}
		// Failing: N consecutive failed attempts, or the circuit breaker is open / probing, or the endpoint
		// was switched off by the delivery worker because it kept failing (HTTP 410 / continuous failure).
		autoDisabled := !ep.Enabled && ep.DisabledReason != nil && ep.DisabledReason.Code != ""
		breakerOpen := ep.Breaker.State == "open" || ep.Breaker.State == "halfOpen"
		failing := autoDisabled || breakerOpen || (ep.Enabled && ep.Breaker.Failures >= thr)
		key := "webhook.failing:" + ep.ID
		if failing {
			openWanted[ep.Org+"\x00"+key] = true
			sev := SeverityWarning
			if breakerOpen || autoDisabled {
				sev = SeverityCritical
			}
			reason := "failures"
			switch {
			case autoDisabled:
				reason = "disabled:" + ep.DisabledReason.Code
			case breakerOpen:
				reason = "breaker:" + ep.Breaker.State
			}
			w.Engine.Raise(ctx, Problem{
				Scope: ep.Org, Key: key, Category: CatWebhooks, Severity: sev,
				TitleKey: "notify.webhook.failing.title", DetailKey: "notify.webhook.failing.detail",
				TitleParams: map[string]string{"name": ep.Name},
				DetailParams: map[string]string{
					"endpointId": ep.ID, "failures": strconv.Itoa(ep.Breaker.Failures), "reason": reason,
				},
				Link: "/webhooks",
				Data: map[string]any{"endpointId": ep.ID, "failures": ep.Breaker.Failures, "reason": reason},
			})
		}
		// dead-letter growth per endpoint
		n, _ := w.DB.Collection("deliveries").CountDocuments(ctx, bson.M{
			"endpointId": ep.ID, "state": "deadLettered",
			"updatedAt": bson.M{"$gte": now.Add(-time.Hour)},
		})
		dlKey := "webhook.deadletter:" + ep.ID
		if int(n) >= dlGrowth {
			openWanted[ep.Org+"\x00"+dlKey] = true
			w.Engine.Raise(ctx, Problem{
				Scope: ep.Org, Key: dlKey, Category: CatWebhooks, Severity: SeverityWarning,
				TitleKey: "notify.webhook.deadletter.title", DetailKey: "notify.webhook.deadletter.detail",
				TitleParams:  map[string]string{"name": ep.Name},
				DetailParams: map[string]string{"count": strconv.FormatInt(n, 10)},
				Link:         "/webhooks",
				Data:         map[string]any{"endpointId": ep.ID, "count": n},
			})
		}
	}
	w.resolveMissing(ctx, CatWebhooks, openWanted)
}

func (w *Watchers) watchAccounts(ctx context.Context) {
	stall := SettingDuration(ctx, w.DB, "", "notifications.accounts.syncStall", 2*time.Hour)
	certDays := SettingInt(ctx, w.DB, "", "notifications.accounts.certExpiryDays", 14)
	cur, err := w.DB.Collection("accounts").Find(ctx, bson.M{"deletedAt": bson.M{"$exists": false}},
		options.Find().SetProjection(bson.M{
			"_id": 1, "organizationId": 1, "name": 1, "email": 1, "state": 1,
			"lastSyncAt": 1, "certExpiresAt": 1,
		}))
	if err != nil {
		w.log().Warn("notify accounts watcher", "err", err.Error())
		return
	}
	defer func() { _ = cur.Close(ctx) }()
	openWanted := map[string]bool{}
	now := w.now()
	for cur.Next(ctx) {
		var a struct {
			ID       string     `bson:"_id"`
			Org      string     `bson:"organizationId"`
			Name     string     `bson:"name"`
			Email    string     `bson:"email"`
			State    string     `bson:"state"`
			LastSync *time.Time `bson:"lastSyncAt"`
			CertExp  *time.Time `bson:"certExpiresAt"`
		}
		if cur.Decode(&a) != nil {
			continue
		}
		raise := func(suffix, cat, sev, title, detail string) {
			key := suffix + ":" + a.ID
			openWanted[a.Org+"\x00"+key] = true
			w.Engine.Raise(ctx, Problem{
				Scope: a.Org, Key: key, Category: cat, Severity: sev,
				TitleKey: title, DetailKey: detail,
				TitleParams:  map[string]string{"name": a.Name, "email": a.Email},
				DetailParams: map[string]string{"accountId": a.ID, "state": a.State},
				Link:         "/accounts/" + a.ID, Data: map[string]any{"accountId": a.ID},
			})
		}
		switch a.State {
		case "authError":
			raise("account.auth", CatAccounts, SeverityCritical, "notify.account.auth.title", "notify.account.auth.detail")
		case "connectError":
			raise("account.connect", CatAccounts, SeverityCritical, "notify.account.connect.title", "notify.account.connect.detail")
		case "disabled":
			raise("account.disabled", CatAccounts, SeverityInfo, "notify.account.disabled.title", "notify.account.disabled.detail")
		}
		if a.LastSync != nil && now.Sub(*a.LastSync) > stall && a.State == "connected" {
			raise("account.syncStall", CatAccounts, SeverityWarning, "notify.account.syncStall.title", "notify.account.syncStall.detail")
		}
		if a.CertExp != nil && a.CertExp.Before(now.Add(time.Duration(certDays)*24*time.Hour)) && a.CertExp.After(now) {
			raise("account.cert", CatAccounts, SeverityWarning, "notify.account.cert.title", "notify.account.cert.detail")
		}
	}
	w.resolveMissing(ctx, CatAccounts, openWanted)
}

func (w *Watchers) watchQueues(ctx context.Context) {
	if w.BusInfo == nil {
		return
	}
	pendingMin := SettingDuration(ctx, w.DB, "", "notifications.queues.pendingMinutes", 15*time.Minute)
	oldest := SettingDuration(ctx, w.DB, "", "notifications.queues.oldestAge", 30*time.Minute)
	pausedFor := SettingDuration(ctx, w.DB, "", "notifications.queues.pausedLongerThan", time.Hour)
	openWanted := map[string]bool{}
	now := w.now()
	for _, q := range w.BusInfo(ctx) {
		scope := q.OrgID
		if scope == "" {
			scope = ScopeInstance
		}
		key := "queue.pending:" + q.Name
		if q.Pending > 0 && q.OldestAge >= pendingMin {
			openWanted[scope+"\x00"+key] = true
			w.Engine.Raise(ctx, Problem{
				Scope: scope, Key: key, Category: CatQueues, Severity: SeverityWarning,
				TitleKey: "notify.queue.pending.title", DetailKey: "notify.queue.pending.detail",
				TitleParams:  map[string]string{"queue": q.Name},
				DetailParams: map[string]string{"pending": strconv.FormatInt(q.Pending, 10)},
				Link:         "/cluster", Data: map[string]any{"queue": q.Name, "pending": q.Pending},
			})
		}
		ageKey := "queue.oldest:" + q.Name
		if q.OldestAge > oldest {
			openWanted[scope+"\x00"+ageKey] = true
			w.Engine.Raise(ctx, Problem{
				Scope: scope, Key: ageKey, Category: CatQueues, Severity: SeverityCritical,
				TitleKey: "notify.queue.oldest.title", DetailKey: "notify.queue.oldest.detail",
				TitleParams: map[string]string{"queue": q.Name},
				Link:        "/cluster", Data: map[string]any{"queue": q.Name},
			})
		}
		pauseKey := "queue.paused:" + q.Name
		if q.PausedAt != nil && now.Sub(*q.PausedAt) > pausedFor {
			openWanted[scope+"\x00"+pauseKey] = true
			w.Engine.Raise(ctx, Problem{
				Scope: scope, Key: pauseKey, Category: CatQueues, Severity: SeverityWarning,
				TitleKey: "notify.queue.paused.title", DetailKey: "notify.queue.paused.detail",
				TitleParams: map[string]string{"queue": q.Name},
				Link:        "/cluster", Data: map[string]any{"queue": q.Name},
			})
		}
	}
	w.resolveMissing(ctx, CatQueues, openWanted)
}

func (w *Watchers) watchSending(ctx context.Context) {
	pct := SettingInt(ctx, w.DB, "", "notifications.sending.failureRatePct", 20)
	since := w.now().Add(-time.Hour)
	// Aggregate outbound failures per org/account in the last hour.
	pipe := []bson.M{
		{"$match": bson.M{"updatedAt": bson.M{"$gte": since}, "state": bson.M{"$in": []string{"failed", "bounced"}}}},
		{"$group": bson.M{"_id": bson.M{"org": "$organizationId", "acc": "$accountId"}, "n": bson.M{"$sum": 1}}},
	}
	cur, err := w.DB.Collection("outboundMessages").Aggregate(ctx, pipe)
	if err != nil {
		return
	}
	defer func() { _ = cur.Close(ctx) }()
	openWanted := map[string]bool{}
	for cur.Next(ctx) {
		var row struct {
			ID struct {
				Org, Acc string
			} `bson:"_id"`
			N int `bson:"n"`
		}
		if cur.Decode(&row) != nil || row.ID.Org == "" {
			continue
		}
		total, _ := w.DB.Collection("outboundMessages").CountDocuments(ctx, bson.M{
			"organizationId": row.ID.Org, "accountId": row.ID.Acc, "updatedAt": bson.M{"$gte": since},
		})
		if total < 10 {
			continue
		}
		rate := float64(row.N) * 100 / float64(total)
		if rate < float64(pct) {
			continue
		}
		key := "sending.failures:" + row.ID.Acc
		openWanted[row.ID.Org+"\x00"+key] = true
		w.Engine.Raise(ctx, Problem{
			Scope: row.ID.Org, Key: key, Category: CatSending, Severity: SeverityWarning,
			TitleKey: "notify.sending.failures.title", DetailKey: "notify.sending.failures.detail",
			DetailParams: map[string]string{"accountId": row.ID.Acc, "rate": strconv.Itoa(int(rate))},
			Link:         "/accounts/" + row.ID.Acc + "/outbox",
			Data:         map[string]any{"accountId": row.ID.Acc, "rate": rate},
		})
	}
	w.resolveMissing(ctx, CatSending, openWanted)
}

func (w *Watchers) watchPlatform(ctx context.Context) {
	openWanted := map[string]bool{}
	missingAfter := SettingDuration(ctx, w.DB, "", "notifications.platform.nodeMissing", 2*time.Minute)
	if w.ClusterNodes != nil {
		now := w.now()
		for _, n := range w.ClusterNodes(ctx) {
			if now.Sub(n.LastSeen) > missingAfter {
				key := "platform.node:" + n.ID
				openWanted[ScopeInstance+"\x00"+key] = true
				w.Engine.Raise(ctx, Problem{
					Scope: ScopeInstance, Key: key, Category: CatPlatform, Severity: SeverityCritical,
					TitleKey: "notify.platform.node.title", DetailKey: "notify.platform.node.detail",
					TitleParams: map[string]string{"node": n.ID},
					Link:        "/cluster", Data: map[string]any{"nodeId": n.ID},
				})
			}
		}
	}
	// migrations pending marker in settings/locks — best-effort
	n, _ := w.DB.Collection("migrations").CountDocuments(ctx, bson.M{"pending": true})
	if n > 0 {
		key := "platform.migrations"
		openWanted[ScopeInstance+"\x00"+key] = true
		w.Engine.Raise(ctx, Problem{
			Scope: ScopeInstance, Key: key, Category: CatPlatform, Severity: SeverityWarning,
			TitleKey: "notify.platform.migrations.title", DetailKey: "notify.platform.migrations.detail",
			Link: "/cluster",
		})
	}
	w.resolveMissing(ctx, CatPlatform, openWanted)
}

func (w *Watchers) watchSecurity(ctx context.Context) {
	days := SettingInt(ctx, w.DB, "", "notifications.security.apiKeyExpiryDays", 7)
	until := w.now().Add(time.Duration(days) * 24 * time.Hour)
	cur, err := w.DB.Collection("apiKeys").Find(ctx, bson.M{
		"expiresAt": bson.M{"$gt": w.now(), "$lte": until},
	}, options.Find().SetProjection(bson.M{"_id": 1, "organizationId": 1, "name": 1, "expiresAt": 1}))
	if err != nil {
		return
	}
	defer func() { _ = cur.Close(ctx) }()
	openWanted := map[string]bool{}
	for cur.Next(ctx) {
		var k struct {
			ID   string    `bson:"_id"`
			Org  string    `bson:"organizationId"`
			Name string    `bson:"name"`
			Exp  time.Time `bson:"expiresAt"`
		}
		if cur.Decode(&k) != nil {
			continue
		}
		key := "security.apikey:" + k.ID
		openWanted[k.Org+"\x00"+key] = true
		w.Engine.Raise(ctx, Problem{
			Scope: k.Org, Key: key, Category: CatSecurity, Severity: SeverityWarning,
			TitleKey: "notify.security.apikey.title", DetailKey: "notify.security.apikey.detail",
			TitleParams: map[string]string{"name": k.Name},
			Link:        "/api-keys", Data: map[string]any{"apiKeyId": k.ID},
		})
	}
	// MFA disabled for org owners
	mcur, err := w.DB.Collection("memberships").Find(ctx, bson.M{"role": "owner"})
	if err == nil {
		defer func() { _ = mcur.Close(ctx) }()
		for mcur.Next(ctx) {
			var m struct {
				UserID string `bson:"userId"`
				Org    string `bson:"organizationId"`
			}
			if mcur.Decode(&m) != nil {
				continue
			}
			var u struct {
				MFAEnabled bool `bson:"mfaEnabled"`
			}
			_ = w.DB.Collection("users").FindOne(ctx, bson.M{"_id": m.UserID}).Decode(&u)
			// If field absent, skip (auth may track MFA elsewhere).
			_ = u
		}
	}
	w.resolveMissing(ctx, CatSecurity, openWanted)
}

func (w *Watchers) watchIntegrations(ctx context.Context) {
	// Failing = the last connector action failed (a later success clears lastActionError).
	cur, err := w.DB.Collection("integrationInstances").Find(ctx, bson.M{
		"enabled": true, "lastActionAt": bson.M{"$exists": true}, "lastActionOK": bson.M{"$ne": true},
		"lastActionError": bson.M{"$nin": []any{"", nil}},
	}, options.Find().SetProjection(bson.M{"_id": 1, "organizationId": 1, "type": 1, "name": 1}))
	if err != nil {
		return
	}
	defer func() { _ = cur.Close(ctx) }()
	openWanted := map[string]bool{}
	for cur.Next(ctx) {
		var inst struct {
			ID, Org, Connector, Name string
		}
		var raw bson.M
		if cur.Decode(&raw) != nil {
			continue
		}
		inst.ID, _ = raw["_id"].(string)
		inst.Org, _ = raw["organizationId"].(string)
		inst.Connector, _ = raw["type"].(string)
		inst.Name, _ = raw["name"].(string)
		if inst.ID == "" || inst.Org == "" {
			continue
		}
		key := "integrations.failing:" + inst.ID
		openWanted[inst.Org+"\x00"+key] = true
		w.Engine.Raise(ctx, Problem{
			Scope: inst.Org, Key: key, Category: CatIntegrations, Severity: SeverityWarning,
			TitleKey: "notify.integrations.failing.title", DetailKey: "notify.integrations.failing.detail",
			TitleParams: map[string]string{"name": inst.Name, "connector": inst.Connector},
			Link:        "/integrations", Data: map[string]any{"integrationId": inst.ID},
		})
	}
	w.resolveMissing(ctx, CatIntegrations, openWanted)
}

// resolveMissing resolves open problems in category whose keys are no longer wanted.
func (w *Watchers) resolveMissing(ctx context.Context, category string, wanted map[string]bool) {
	open, err := w.Repo.OpenKeys(ctx, category)
	if err != nil {
		return
	}
	for sk := range open {
		if wanted[sk] {
			continue
		}
		// sk = scope\0key
		parts := splitSK(sk)
		if parts[0] == "" {
			continue
		}
		w.Engine.Resolve(ctx, parts[0], parts[1])
	}
}

func splitSK(sk string) [2]string {
	for i := 0; i < len(sk); i++ {
		if sk[i] == 0 {
			return [2]string{sk[:i], sk[i+1:]}
		}
	}
	return [2]string{}
}
