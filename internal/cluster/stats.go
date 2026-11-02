package cluster

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/store"
)

// StatsView is GET /v1/stats (organisation-scoped).
type StatsView struct {
	Accounts          map[string]int64 `json:"accounts" doc:"Accounts by connection state." example:"{\"connected\":38,\"syncing\":2,\"authError\":1,\"disabled\":3}"`
	OutboundMessages  map[string]int64 `json:"outboundMessages" doc:"Outbound messages by state (all time)." example:"{\"queued\":4,\"sent\":18233,\"failed\":27,\"scheduled\":1}"`
	DeliveriesLast24h map[string]int64 `json:"deliveriesLast24h" doc:"Webhook deliveries created in the last 24 hours, by state." example:"{\"succeeded\":5120,\"retrying\":6,\"deadLettered\":2}"`
	EventsLast24h     int64            `json:"eventsLast24h" doc:"Events stored in the last 24 hours." example:"5128"`
	DeadLetters       int64            `json:"deadLetters" doc:"Webhook deliveries currently dead-lettered (all time)." example:"9"`
	// KPIs are the W74 dashboard counters for ?period (default 24h).
	KPIs *KPIs `json:"kpis,omitempty" doc:"Dashboard KPIs for the requested period."`
}

// Stats aggregates organisation counters from MongoDB.
func (s *Service) Stats(ctx context.Context) (*StatsView, error) {
	org, ok := store.OrganizationFrom(ctx)
	if !ok || org == "" {
		return nil, errs.New(errs.CodeForbidden, "An organisation context is required.")
	}
	since := time.Now().UTC().Add(-24 * time.Hour)
	accounts, err := countByState(ctx, s.DB, "accounts", bson.M{"organizationId": org})
	if err != nil {
		return nil, err
	}
	outbound, err := countByState(ctx, s.DB, "outboundMessages", bson.M{"organizationId": org})
	if err != nil {
		return nil, err
	}
	deliveries, err := countByState(ctx, s.DB, "deliveries", bson.M{
		"organizationId": org,
		"createdAt":      bson.M{"$gte": since},
	})
	if err != nil {
		return nil, err
	}
	events, err := s.DB.Collection("events").CountDocuments(ctx, bson.M{
		"organizationId": org,
		"time":           bson.M{"$gte": since},
	})
	if err != nil {
		return nil, errs.New(errs.CodeInternalError, "Could not count events.").WithSafe("error", err.Error())
	}
	dead, err := s.DB.Collection("deliveries").CountDocuments(ctx, bson.M{
		"organizationId": org,
		"state":          "deadLettered",
	})
	if err != nil {
		return nil, errs.New(errs.CodeInternalError, "Could not count dead letters.").WithSafe("error", err.Error())
	}
	return &StatsView{
		Accounts: accounts, OutboundMessages: outbound, DeliveriesLast24h: deliveries,
		EventsLast24h: events, DeadLetters: dead,
	}, nil
}

func countByState(ctx context.Context, db *store.DB, coll string, match bson.M) (map[string]int64, error) {
	cur, err := db.Collection(coll).Aggregate(ctx, bson.A{
		bson.M{"$match": match},
		bson.M{"$group": bson.M{"_id": "$state", "n": bson.M{"$sum": 1}}},
	})
	if err != nil {
		return nil, errs.New(errs.CodeInternalError, "Could not aggregate counts.").WithSafe("collection", coll).WithSafe("error", err.Error())
	}
	defer func() { _ = cur.Close(ctx) }()
	out := map[string]int64{}
	for cur.Next(ctx) {
		var row struct {
			ID string `bson:"_id"`
			N  int64  `bson:"n"`
		}
		if err := cur.Decode(&row); err != nil {
			return nil, err
		}
		if row.ID == "" {
			row.ID = "unknown"
		}
		out[row.ID] = row.N
	}
	return out, cur.Err()
}
