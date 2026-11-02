package webhook

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/rules"
	"github.com/radiobros/emailbridge/internal/store"
)

// Repo is webhook MongoDB access. API methods always use scoped filters; worker methods carry the
// organisation from an already-loaded delivery or event.
type Repo struct {
	DB *store.DB
}

func (r *Repo) insertEndpoint(ctx context.Context, endpoint *Endpoint) error {
	_, err := r.DB.Collection(EndpointsCollection).InsertOne(ctx, endpoint)
	if mongo.IsDuplicateKeyError(err) {
		return errs.New(errs.CodeEndpointNameTaken, "A webhook endpoint with this name already exists.")
	}
	return err
}

func (r *Repo) endpoint(ctx context.Context, id string) (*Endpoint, error) {
	filter, err := store.Scoped(ctx, bson.M{"_id": id})
	if err != nil {
		return nil, err
	}
	return r.endpointByFilter(ctx, filter)
}

func (r *Repo) endpointForWorker(ctx context.Context, orgID, id string) (*Endpoint, error) {
	return r.endpointByFilter(ctx, bson.M{"_id": id, "organizationId": orgID})
}

func (r *Repo) endpointByFilter(ctx context.Context, filter bson.M) (*Endpoint, error) {
	var endpoint Endpoint
	err := r.DB.Collection(EndpointsCollection).FindOne(ctx, filter).Decode(&endpoint)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, errs.New(errs.CodeNotFound, "Webhook endpoint not found.")
	}
	if err != nil {
		return nil, fmt.Errorf("webhook: get endpoint: %w", err)
	}
	return &endpoint, nil
}

func (r *Repo) replaceEndpoint(ctx context.Context, endpoint *Endpoint, oldVersion int64) error {
	filter, err := store.Scoped(ctx, bson.M{"_id": endpoint.ID, "version": oldVersion})
	if err != nil {
		return err
	}
	res, err := r.DB.Collection(EndpointsCollection).ReplaceOne(ctx, filter, endpoint)
	if mongo.IsDuplicateKeyError(err) {
		return errs.New(errs.CodeEndpointNameTaken, "A webhook endpoint with this name already exists.")
	}
	if err != nil {
		return fmt.Errorf("webhook: update endpoint: %w", err)
	}
	if res.MatchedCount == 0 {
		return errs.New(errs.CodeVersionMismatch, "The resource version does not match If-Match.")
	}
	return nil
}

func (r *Repo) deleteEndpoint(ctx context.Context, endpoint *Endpoint) error {
	filter, err := store.Scoped(ctx, bson.M{"_id": endpoint.ID, "version": endpoint.Version})
	if err != nil {
		return err
	}
	n, err := r.DB.Collection(rules.RoutesCollection).CountDocuments(ctx, bson.M{
		"organizationId": endpoint.OrganizationID,
		"endpointId":     endpoint.ID,
	})
	if err != nil {
		return err
	}
	if n > 0 {
		return errs.New(errs.CodeConflict, "Delete routes targeting this endpoint first.")
	}
	res, err := r.DB.Collection(EndpointsCollection).DeleteOne(ctx, filter)
	if err != nil {
		return err
	}
	if res.DeletedCount == 0 {
		return errs.New(errs.CodeVersionMismatch, "The resource version does not match If-Match.")
	}
	return nil
}

func (r *Repo) enabledEndpoints(ctx context.Context, orgID string) ([]Endpoint, error) {
	cur, err := r.DB.Collection(EndpointsCollection).Find(ctx, bson.M{
		"organizationId": orgID,
		"enabled":        true,
	}, options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}))
	if err != nil {
		return nil, err
	}
	defer func() { _ = cur.Close(ctx) }()
	var endpoints []Endpoint
	if err := cur.All(ctx, &endpoints); err != nil {
		return nil, err
	}
	return endpoints, nil
}

func (r *Repo) enabledRoutes(ctx context.Context, orgID string) ([]rules.Route, error) {
	cur, err := r.DB.Collection(rules.RoutesCollection).Find(ctx, bson.M{
		"organizationId": orgID,
		"enabled":        true,
	}, options.Find().SetSort(bson.D{{Key: "order", Value: 1}, {Key: "_id", Value: 1}}))
	if err != nil {
		return nil, err
	}
	defer func() { _ = cur.Close(ctx) }()
	var routes []rules.Route
	if err := cur.All(ctx, &routes); err != nil {
		return nil, err
	}
	return routes, nil
}

func (r *Repo) routeForWorker(ctx context.Context, orgID, id string) (*rules.Route, error) {
	var route rules.Route
	err := r.DB.Collection(rules.RoutesCollection).FindOne(ctx, bson.M{
		"_id": id, "organizationId": orgID,
	}).Decode(&route)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, errs.New(errs.CodeNotFound, "Route not found.")
	}
	return &route, err
}

func (r *Repo) insertDelivery(ctx context.Context, delivery *Delivery) (bool, error) {
	_, err := r.DB.Collection(DeliveriesCollection).InsertOne(ctx, delivery)
	if mongo.IsDuplicateKeyError(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("webhook: insert delivery: %w", err)
	}
	return true, nil
}

func (r *Repo) deliveryByIdentity(ctx context.Context, eventID, endpointID, routeID string) (*Delivery, error) {
	filter := bson.M{"eventId": eventID, "endpointId": endpointID}
	if routeID == "" {
		filter["routeId"] = bson.M{"$exists": false}
	} else {
		filter["routeId"] = routeID
	}
	return r.deliveryByFilter(ctx, filter)
}

func (r *Repo) delivery(ctx context.Context, id string) (*Delivery, error) {
	filter, err := store.Scoped(ctx, bson.M{"_id": id})
	if err != nil {
		return nil, err
	}
	return r.deliveryByFilter(ctx, filter)
}

func (r *Repo) deliveryForWorker(ctx context.Context, id string) (*Delivery, error) {
	return r.deliveryByFilter(ctx, bson.M{"_id": id})
}

func (r *Repo) deliveryByFilter(ctx context.Context, filter bson.M) (*Delivery, error) {
	var delivery Delivery
	err := r.DB.Collection(DeliveriesCollection).FindOne(ctx, filter).Decode(&delivery)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, errs.New(errs.CodeNotFound, "Delivery not found.")
	}
	if err != nil {
		return nil, fmt.Errorf("webhook: get delivery: %w", err)
	}
	return &delivery, nil
}

func (r *Repo) claimDelivery(ctx context.Context, id string, redelivered bool, now time.Time) (*Delivery, bool, error) {
	ready := []bson.M{
		{"state": bson.M{"$in": []string{StatePending, StateRetrying}}, "$or": []bson.M{
			{"nextAttemptAt": bson.M{"$exists": false}},
			{"nextAttemptAt": bson.M{"$lte": now}},
		}},
	}
	if redelivered {
		ready = append(ready, bson.M{"state": StateDelivering, "updatedAt": bson.M{"$lte": now.Add(-45 * time.Second)}})
	}
	filter := bson.M{"_id": id, "$or": ready}
	update := bson.M{"$set": bson.M{"state": StateDelivering, "updatedAt": now}, "$inc": bson.M{"version": 1}}
	var delivery Delivery
	err := r.DB.Collection(DeliveriesCollection).FindOneAndUpdate(
		ctx,
		filter,
		update,
		options.FindOneAndUpdate().SetReturnDocument(options.After),
	).Decode(&delivery)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return &delivery, true, nil
}

func (r *Repo) saveAttempt(
	ctx context.Context,
	delivery *Delivery,
	attempt DeliveryAttempt,
	state string,
	next *time.Time,
) error {
	update := bson.M{
		"$push": bson.M{"attempts": attempt},
		"$set": bson.M{
			"state": state, "updatedAt": time.Now().UTC(), "nextAttemptAt": next,
			"lastAttemptAt": attempt.At,
		},
		"$inc": bson.M{"version": 1},
	}
	if next == nil {
		update["$unset"] = bson.M{"nextAttemptAt": ""}
		delete(update["$set"].(bson.M), "nextAttemptAt")
	}
	res, err := r.DB.Collection(DeliveriesCollection).UpdateOne(ctx, bson.M{
		"_id": delivery.ID, "organizationId": delivery.OrganizationID, "state": StateDelivering,
	}, update)
	if err != nil {
		return err
	}
	if res.MatchedCount == 0 {
		return errs.New(errs.CodeConflict, "Delivery state changed concurrently.")
	}
	return nil
}

func (r *Repo) saveBreaker(ctx context.Context, endpoint *Endpoint) error {
	set := bson.M{
		"breaker": endpoint.Breaker, "failureStartedAt": endpoint.FailureStartedAt,
		"enabled": endpoint.Enabled, "disabledReason": endpoint.DisabledReason,
		"updatedAt": time.Now().UTC(),
	}
	res, err := r.DB.Collection(EndpointsCollection).UpdateOne(ctx, bson.M{
		"_id": endpoint.ID, "organizationId": endpoint.OrganizationID, "version": endpoint.Version,
	}, bson.M{"$set": set, "$inc": bson.M{"version": 1}})
	if err != nil {
		return err
	}
	if res.MatchedCount == 0 {
		return errs.New(errs.CodeConflict, "Webhook endpoint state changed concurrently.")
	}
	endpoint.Version++
	return nil
}

func (r *Repo) hasEarlierUnfinished(ctx context.Context, delivery *Delivery) (bool, error) {
	n, err := r.DB.Collection(DeliveriesCollection).CountDocuments(ctx, bson.M{
		"organizationId": delivery.OrganizationID,
		"endpointId":     delivery.EndpointID,
		"accountId":      delivery.AccountID,
		"_id":            bson.M{"$lt": delivery.ID},
		"state":          bson.M{"$nin": finalStates},
	}, options.Count().SetLimit(1))
	return n > 0, err
}

// Migration creates webhookEndpoints and deliveries indexes from S1 §5.
func Migration(number int) store.Migration {
	return store.Migration{Number: number, Name: "webhook indexes", Up: func(ctx context.Context, db *store.DB) error {
		if err := db.EnsureIndexes(ctx, EndpointsCollection,
			mongo.IndexModel{
				Keys:    bson.D{{Key: "organizationId", Value: 1}, {Key: "name", Value: 1}},
				Options: options.Index().SetUnique(true),
			},
			mongo.IndexModel{Keys: bson.D{{Key: "organizationId", Value: 1}, {Key: "_id", Value: 1}}},
		); err != nil {
			return err
		}
		return db.EnsureIndexes(ctx, DeliveriesCollection,
			mongo.IndexModel{Keys: bson.D{{Key: "organizationId", Value: 1}, {Key: "_id", Value: -1}}},
			mongo.IndexModel{Keys: bson.D{{Key: "organizationId", Value: 1}, {Key: "state", Value: 1}, {Key: "_id", Value: -1}}},
			mongo.IndexModel{Keys: bson.D{{Key: "endpointId", Value: 1}, {Key: "_id", Value: -1}}},
			mongo.IndexModel{
				Keys:    bson.D{{Key: "eventId", Value: 1}, {Key: "endpointId", Value: 1}, {Key: "routeId", Value: 1}},
				Options: options.Index().SetUnique(true).SetPartialFilterExpression(bson.M{"isReplay": false}),
			},
			mongo.IndexModel{Keys: bson.D{{Key: "expireAt", Value: 1}}, Options: options.Index().SetExpireAfterSeconds(0)},
		)
	}}
}

// LastDeliveryIndexMigration adds the deliveries index used for per-endpoint lastDelivery enrichment.
func LastDeliveryIndexMigration(number int) store.Migration {
	return store.Migration{Number: number, Name: "webhook deliveries last-by-endpoint", Up: func(ctx context.Context, db *store.DB) error {
		return db.EnsureIndexes(ctx, DeliveriesCollection,
			mongo.IndexModel{Keys: bson.D{
				{Key: "organizationId", Value: 1}, {Key: "endpointId", Value: 1}, {Key: "_id", Value: -1},
			}},
		)
	}}
}

// ReplayedAtIndexMigration supports bulk replay filtering of unreplayed finals by event order.
func ReplayedAtIndexMigration(number int) store.Migration {
	return store.Migration{Number: number, Name: "webhook deliveries bulk-replay", Up: func(ctx context.Context, db *store.DB) error {
		return db.EnsureIndexes(ctx, DeliveriesCollection,
			mongo.IndexModel{Keys: bson.D{
				{Key: "organizationId", Value: 1},
				{Key: "state", Value: 1},
				{Key: "replayedAt", Value: 1},
				{Key: "eventId", Value: 1},
				{Key: "_id", Value: 1},
			}},
		)
	}}
}
