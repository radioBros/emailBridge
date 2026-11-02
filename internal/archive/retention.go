package archive

import (
	"context"
	"log/slog"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/radiobros/emailbridge/internal/config"
	"github.com/radiobros/emailbridge/internal/index"
)

const (
	retentionBatch   = 100
	defaultRetention = time.Hour
)

type accountRetention struct {
	ID               string `bson:"_id"`
	OrganizationID   string `bson:"organizationId"`
	ArchiveRetention string `bson:"archiveRetention,omitempty"`
}

// RetentionDuty runs until ctx ends: deletes archive objects older than each account's retention
// and clears archiveRef (batched, idempotent).
func (s *Service) RetentionDuty(ctx context.Context) {
	every := s.RetentionEvery
	if every <= 0 {
		every = defaultRetention
	}
	t := time.NewTicker(every)
	defer t.Stop()
	s.SweepRetention(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.SweepRetention(ctx)
		}
	}
}

// SweepRetention runs one retention pass over every account with a positive archiveRetention.
func (s *Service) SweepRetention(ctx context.Context) {
	cur, err := s.DB.Collection("accounts").Find(ctx, bson.M{
		"archiveRetention": bson.M{"$exists": true, "$nin": []any{"", "0"}},
	}, options.Find().SetProjection(bson.M{
		"_id": 1, "organizationId": 1, "archiveRetention": 1,
	}))
	if err != nil {
		s.log().LogAttrs(ctx, slog.LevelWarn, "archive retention list accounts", slog.Any("err", err))
		return
	}
	defer func() { _ = cur.Close(ctx) }()
	for cur.Next(ctx) {
		var a accountRetention
		if err := cur.Decode(&a); err != nil {
			continue
		}
		if err := s.sweepAccount(ctx, a); err != nil && ctx.Err() == nil {
			s.log().LogAttrs(ctx, slog.LevelWarn, "archive retention sweep",
				slog.String("accountId", a.ID), slog.Any("err", err))
		}
		if ctx.Err() != nil {
			return
		}
	}
}

func (s *Service) sweepAccount(ctx context.Context, a accountRetention) error {
	d, err := config.ParseDuration(a.ArchiveRetention)
	if err != nil || d <= 0 {
		return nil // "0" / invalid → keep
	}
	cutoff := s.now().Add(-d)
	for { // messages gone from the mailbox keep their archive in a tombstone (index.ArchivedMessages)
		n, err := s.sweepTombstones(ctx, a.ID, cutoff)
		if err != nil {
			return err
		}
		if n == 0 {
			break
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	for {
		n, err := s.sweepBatch(ctx, a.ID, cutoff)
		if err != nil {
			return err
		}
		if n == 0 {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
}

func (s *Service) sweepBatch(ctx context.Context, accountID string, cutoff time.Time) (int, error) {
	cur, err := s.DB.Collection(index.Messages).Find(ctx, bson.M{
		"accountId":    accountID,
		"archiveRef":   bson.M{"$exists": true, "$ne": ""},
		"internalDate": bson.M{"$lt": cutoff},
	}, options.Find().SetLimit(retentionBatch).SetProjection(bson.M{"_id": 1, "archiveRef": 1}))
	if err != nil {
		return 0, err
	}
	defer func() { _ = cur.Close(ctx) }()
	n := 0
	for cur.Next(ctx) {
		var m struct {
			ID         string `bson:"_id"`
			ArchiveRef string `bson:"archiveRef"`
		}
		if err := cur.Decode(&m); err != nil {
			continue
		}
		if err := s.Store.Delete(ctx, m.ArchiveRef); err != nil {
			return n, err
		}
		_, err := s.DB.Collection(index.Messages).UpdateOne(ctx,
			bson.M{"_id": m.ID, "archiveRef": m.ArchiveRef},
			bson.M{"$unset": bson.M{"archiveRef": ""}})
		if err != nil {
			return n, err
		}
		n++
	}
	return n, cur.Err()
}

// sweepTombstones deletes the archive objects of expired tombstones, then the tombstones.
func (s *Service) sweepTombstones(ctx context.Context, accountID string, cutoff time.Time) (int, error) {
	cur, err := s.DB.Collection(index.ArchivedMessages).Find(ctx, bson.M{
		"accountId":    accountID,
		"internalDate": bson.M{"$lt": cutoff},
	}, options.Find().SetLimit(retentionBatch).SetProjection(bson.M{"_id": 1, "archiveRef": 1}))
	if err != nil {
		return 0, err
	}
	defer func() { _ = cur.Close(ctx) }()
	n := 0
	for cur.Next(ctx) {
		var m struct {
			ID         string `bson:"_id"`
			ArchiveRef string `bson:"archiveRef"`
		}
		if err := cur.Decode(&m); err != nil {
			continue
		}
		if m.ArchiveRef != "" {
			if err := s.Store.Delete(ctx, m.ArchiveRef); err != nil {
				return n, err
			}
		}
		if _, err := s.DB.Collection(index.ArchivedMessages).DeleteOne(ctx, bson.M{"_id": m.ID}); err != nil {
			return n, err
		}
		n++
	}
	return n, cur.Err()
}
