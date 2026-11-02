package notify

import (
	"context"
	"errors"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/radiobros/emailbridge/internal/errs"
)

// EmailRecords returns the user's email records for the given notifications, keyed by notification id.
func (r *Repo) EmailRecords(ctx context.Context, userID string, ids []string) (map[string]EmailRecord, error) {
	out := map[string]EmailRecord{}
	if userID == "" || len(ids) == 0 {
		return out, nil
	}
	cur, err := r.DB.Collection(EmailsCollection).Find(ctx, bson.M{"userId": userID, "notificationId": bson.M{"$in": ids}})
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "notify.emailRecords")
	}
	var rows []EmailRecord
	if err := cur.All(ctx, &rows); err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "notify.emailRecords")
	}
	for _, row := range rows {
		out[row.NotificationID] = row
	}
	return out, nil
}

// RecordEmails stores that userID was emailed about every notification in ns at now.
func (r *Repo) RecordEmails(ctx context.Context, userID string, ns []*Notification, digest bool, now time.Time) error {
	for _, n := range ns {
		rec := EmailRecord{
			ID: userID + ":" + n.ID, UserID: userID, NotificationID: n.ID, OrganizationID: n.OrganizationID,
			Severity: n.Severity, Digest: digest, EmailedAt: now.UTC(),
		}
		_, err := r.DB.Collection(EmailsCollection).ReplaceOne(ctx, bson.M{"_id": rec.ID}, rec, options.Replace().SetUpsert(true))
		if err != nil {
			return errs.Wrap(err, errs.CodeInternalError, "notify.recordEmail")
		}
	}
	return nil
}

// LastDigestAt returns when userID last received a digest (zero when never).
func (r *Repo) LastDigestAt(ctx context.Context, userID string) (time.Time, error) {
	var rec EmailRecord
	err := r.DB.Collection(EmailsCollection).FindOne(ctx, bson.M{"userId": userID, "digest": true},
		options.FindOne().SetSort(bson.D{{Key: "emailedAt", Value: -1}})).Decode(&rec)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, errs.Wrap(err, errs.CodeInternalError, "notify.lastDigest")
	}
	return rec.EmailedAt, nil
}

// InstanceOwnerIDs returns the ids of every instance owner (recipients of instance-scope problems).
func (r *Repo) InstanceOwnerIDs(ctx context.Context) ([]string, error) {
	cur, err := r.DB.Collection("users").Find(ctx, bson.M{"instanceRole": "owner"}, options.Find().SetProjection(bson.M{"_id": 1}))
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "notify.instanceOwners")
	}
	defer func() { _ = cur.Close(ctx) }()
	var ids []string
	for cur.Next(ctx) {
		var u struct {
			ID string `bson:"_id"`
		}
		if cur.Decode(&u) == nil && u.ID != "" {
			ids = append(ids, u.ID)
		}
	}
	return ids, cur.Err()
}

// OpenForDigest returns open notifications, newest first, capped at limit.
func (r *Repo) OpenForDigest(ctx context.Context, limit int64) ([]*Notification, error) {
	cur, err := r.DB.Collection(Collection).Find(ctx, bson.M{"state": StateOpen},
		options.Find().SetSort(bson.D{{Key: "lastSeen", Value: -1}}).SetLimit(limit))
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "notify.openForDigest")
	}
	var rows []*Notification
	if err := cur.All(ctx, &rows); err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "notify.openForDigest")
	}
	return rows, nil
}
