package observability

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/radiobros/emailbridge/internal/store"
)

const collectorInterval = 30 * time.Second

// startCollectors refreshes gauges from registered sources until ctx ends.
func (m *Metrics) startCollectors(ctx context.Context) {
	if m == nil {
		return
	}
	m.refresh(ctx)
	t := time.NewTicker(collectorInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.refresh(ctx)
		}
	}
}

func (m *Metrics) refresh(ctx context.Context) {
	m.sources.mu.Lock()
	syncNode, syncMax, syncStats := m.sources.syncNode, m.sources.syncMax, m.sources.syncStats
	relayLag := m.sources.relayLag
	streams := m.sources.streams
	logDisk := m.sources.logDisk
	mongoFn := m.sources.mongo
	m.sources.mu.Unlock()

	if syncStats != nil {
		accounts, unplaced := syncStats()
		m.SetSyncStats(syncNode, accounts, unplaced, syncMax)
	}
	if relayLag != nil {
		lag, err := relayLag(ctx)
		if err == nil {
			m.SetRelayLagSeconds(lag.Seconds())
		}
	}
	if streams != nil {
		stats, err := streams(ctx)
		if err == nil {
			for _, s := range stats {
				m.SetStreamStats(s.Stream, s.Pending, s.AckPending, s.Redeliveries)
			}
		}
	}
	if logDisk != nil {
		usage, err := logDisk.MetricDiskUsage()
		if err == nil {
			for cat, n := range usage {
				m.domain.logDiskBytes.WithLabelValues(cat).Set(float64(n))
			}
			m.domain.logDiskCap.Set(float64(logDisk.DiskCap()))
		}
	}
	if mongoFn != nil {
		counts, err := mongoFn(ctx)
		if err == nil {
			m.applyMongo(counts)
		}
	}
}

func (m *Metrics) applyMongo(c MongoCounts) {
	seen := map[string]bool{}
	for st, n := range c.AccountState {
		m.domain.accountState.WithLabelValues(st).Set(float64(n))
		seen[st] = true
	}
	for _, st := range AccountStates {
		if !seen[st] {
			m.domain.accountState.WithLabelValues(st).Set(0)
		}
	}
	m.domain.breakerOpen.Reset()
	for org, n := range c.BreakerOpen {
		if org == "" {
			org = "none"
		}
		m.domain.breakerOpen.WithLabelValues(org).Set(float64(n))
	}
	m.domain.insecureTLS.Set(float64(c.InsecureTLS))
	m.domain.ruleErrorsRecent.Set(float64(c.RuleErrorsRecent))
}

// MongoCollector builds MongoCounts from the shared *store.DB (S12 §9 collectors).
func MongoCollector(db *store.DB) MongoCountsFunc {
	if db == nil {
		return nil
	}
	return func(ctx context.Context) (MongoCounts, error) {
		var out MongoCounts
		out.AccountState = map[string]int64{}
		out.BreakerOpen = map[string]int64{}

		cur, err := db.Collection("accounts").Aggregate(ctx, mongo.Pipeline{
			{{Key: "$group", Value: bson.D{{Key: "_id", Value: "$state"}, {Key: "n", Value: bson.D{{Key: "$sum", Value: 1}}}}}},
		})
		if err != nil {
			return out, err
		}
		var stateRows []struct {
			ID string `bson:"_id"`
			N  int64  `bson:"n"`
		}
		if err := cur.All(ctx, &stateRows); err != nil {
			return out, err
		}
		for _, r := range stateRows {
			if r.ID != "" {
				out.AccountState[r.ID] = r.N
			}
		}

		n, err := db.Collection("accounts").CountDocuments(ctx, bson.M{
			"switches.accounts.ignoreCertErrors": true,
		})
		if err != nil {
			return out, err
		}
		out.InsecureTLS = n

		bcur, err := db.Collection("webhookEndpoints").Aggregate(ctx, mongo.Pipeline{
			{{Key: "$match", Value: bson.M{"breaker.state": "open"}}},
			{{Key: "$group", Value: bson.D{
				{Key: "_id", Value: "$organizationId"},
				{Key: "n", Value: bson.D{{Key: "$sum", Value: 1}}},
			}}},
		})
		if err != nil {
			return out, err
		}
		var breakerRows []struct {
			ID string `bson:"_id"`
			N  int64  `bson:"n"`
		}
		if err := bcur.All(ctx, &breakerRows); err != nil {
			return out, err
		}
		for _, r := range breakerRows {
			out.BreakerOpen[r.ID] = r.N
		}

		since := time.Now().UTC().Add(-15 * time.Minute)
		rn, err := db.Collection("ruleErrors").CountDocuments(ctx, bson.M{
			"at": bson.M{"$gte": since},
		})
		if err != nil {
			return out, err
		}
		out.RuleErrorsRecent = rn
		return out, nil
	}
}
