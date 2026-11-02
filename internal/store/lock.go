package store

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// LocksCollection holds named, expiring locks: {_id: name, holder, expiresAt} (S1 §8).
const LocksCollection = "locks"

// ErrLockHeld is returned by TryLock when another holder owns a live lock.
var ErrLockHeld = errors.New("store: lock held by another node")

// Lock is a held named lock. It renews itself until Release; Lost is closed if renewal fails (another
// node took the lock after expiry), and work guarded by the lock must stop then.
type Lock struct {
	db       *DB
	name     string
	holder   string
	ttl      time.Duration
	lost     chan struct{}
	stop     chan struct{}
	done     chan struct{}
	lostOnce sync.Once
}

// TryLock acquires lock name for holder if it is free, expired, or already held by holder. The lock
// expires after ttl unless renewed; it renews every ttl/3 in the background.
func (d *DB) TryLock(ctx context.Context, name, holder string, ttl time.Duration) (*Lock, error) {
	if ttl < 3*time.Second {
		return nil, fmt.Errorf("store: lock ttl %v too short", ttl)
	}
	now := time.Now().UTC()
	filter := bson.M{"_id": name, "$or": bson.A{
		bson.M{"expiresAt": bson.M{"$lt": now}},
		bson.M{"holder": holder},
	}}
	update := bson.M{"$set": bson.M{"holder": holder, "expiresAt": now.Add(ttl), "acquiredAt": now}}
	_, err := d.Collection(LocksCollection).UpdateOne(ctx, filter, update, options.UpdateOne().SetUpsert(true))
	if err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return nil, ErrLockHeld
		}
		return nil, fmt.Errorf("store: lock %s: %w", name, err)
	}
	l := &Lock{db: d, name: name, holder: holder, ttl: ttl,
		lost: make(chan struct{}), stop: make(chan struct{}), done: make(chan struct{})}
	go l.renew() //nolint:gosec // G118: renewal must outlive the acquiring request; it stops on Release
	return l, nil
}

func (l *Lock) renew() {
	defer close(l.done)
	t := time.NewTicker(l.ttl / 3)
	defer t.Stop()
	for {
		select {
		case <-l.stop:
			return
		case <-t.C:
			ctx, cancel := context.WithTimeout(context.Background(), l.ttl/3)
			res, err := l.db.Collection(LocksCollection).UpdateOne(ctx,
				bson.M{"_id": l.name, "holder": l.holder},
				bson.M{"$set": bson.M{"expiresAt": time.Now().UTC().Add(l.ttl)}})
			cancel()
			if err == nil && res.MatchedCount == 0 {
				l.markLost()
				return
			}
			// A transient error is retried at the next tick; the lock stays valid until expiresAt.
		}
	}
}

func (l *Lock) markLost() { l.lostOnce.Do(func() { close(l.lost) }) }

// Lost is closed when the lock was taken over by another holder.
func (l *Lock) Lost() <-chan struct{} { return l.lost }

// Release stops renewal and deletes the lock if still held.
func (l *Lock) Release(ctx context.Context) error {
	close(l.stop)
	<-l.done
	_, err := l.db.Collection(LocksCollection).DeleteOne(ctx, bson.M{"_id": l.name, "holder": l.holder})
	return err
}
