package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// MigrationsCollection records applied migrations: {_id: number, name, checksum, appliedAt, durationMs}.
const MigrationsCollection = "migrations"

const migrationLock = "migrations"

// Migration is one forward-only, idempotent schema step (indexes, backfills in batches).
type Migration struct {
	Number int
	Name   string
	Up     func(ctx context.Context, db *DB) error
}

func (m Migration) checksum() string {
	h := sha256.Sum256([]byte(strconv.Itoa(m.Number) + "|" + m.Name))
	return hex.EncodeToString(h[:8])
}

// ErrMigrationsPending is returned when another node is still migrating after WaitTimeout.
var ErrMigrationsPending = errors.New("store: migrations pending (another node is migrating)")

// Migrator applies migrations under the "migrations" lock so that exactly one node migrates while the
// others wait (no init container, S1 §8).
type Migrator struct {
	DB         *DB
	Holder     string // node ID
	Migrations []Migration
	Log        *slog.Logger
	// LockTTL defaults to 60 s; the lock is renewed every TTL/3.
	LockTTL time.Duration
	// WaitTimeout bounds how long a non-holder waits for the holder (default 5 min).
	WaitTimeout time.Duration
	// PollInterval while waiting (default 2 s).
	PollInterval time.Duration
	// AllowOlderBinary permits starting against a database migrated by a newer binary (rollback drills).
	AllowOlderBinary bool
}

// Status describes the migration state of the database relative to this binary.
type Status struct {
	Applied []int // applied migration numbers, ascending
	Known   int   // highest number this binary knows
	Pending []int // known but not applied
	Newer   []int // applied but unknown to this binary (database is newer)
}

func (m *Migrator) validate() error {
	seen := map[int]bool{}
	last := 0
	for _, mg := range m.Migrations {
		switch {
		case mg.Number <= 0:
			return fmt.Errorf("store: migration %q has number %d (must be > 0)", mg.Name, mg.Number)
		case seen[mg.Number]:
			return fmt.Errorf("store: migration number %d used twice", mg.Number)
		case mg.Number < last:
			return fmt.Errorf("store: migrations must be listed in ascending order (%d after %d)", mg.Number, last)
		case mg.Up == nil || mg.Name == "":
			return fmt.Errorf("store: migration %d needs a name and an Up function", mg.Number)
		}
		seen[mg.Number] = true
		last = mg.Number
	}
	return nil
}

// Status reads the applied migrations.
func (m *Migrator) Status(ctx context.Context) (Status, error) {
	if err := m.validate(); err != nil {
		return Status{}, err
	}
	applied, err := m.applied(ctx)
	if err != nil {
		return Status{}, err
	}
	st := Status{}
	known := map[int]Migration{}
	for _, mg := range m.Migrations {
		known[mg.Number] = mg
		if mg.Number > st.Known {
			st.Known = mg.Number
		}
	}
	for n, rec := range applied {
		st.Applied = append(st.Applied, n)
		mg, ok := known[n]
		if !ok {
			st.Newer = append(st.Newer, n)
			continue
		}
		if rec.Checksum != mg.checksum() {
			return Status{}, fmt.Errorf("store: migration %d was applied as %q but this binary calls it %q (renamed migration)", n, rec.Name, mg.Name)
		}
	}
	for _, mg := range m.Migrations {
		if _, ok := applied[mg.Number]; !ok {
			st.Pending = append(st.Pending, mg.Number)
		}
	}
	slices.Sort(st.Applied)
	slices.Sort(st.Newer)
	return st, nil
}

type appliedRec struct {
	Number   int    `bson:"_id"`
	Name     string `bson:"name"`
	Checksum string `bson:"checksum"`
}

func (m *Migrator) applied(ctx context.Context) (map[int]appliedRec, error) {
	cur, err := m.DB.Collection(MigrationsCollection).Find(ctx, bson.M{})
	if err != nil {
		return nil, fmt.Errorf("store: read migrations: %w", err)
	}
	var recs []appliedRec
	if err := cur.All(ctx, &recs); err != nil {
		return nil, fmt.Errorf("store: read migrations: %w", err)
	}
	out := make(map[int]appliedRec, len(recs))
	for _, r := range recs {
		out[r.Number] = r
	}
	return out, nil
}

func (m *Migrator) defaults() {
	if m.LockTTL <= 0 {
		m.LockTTL = 60 * time.Second
	}
	if m.WaitTimeout <= 0 {
		m.WaitTimeout = 5 * time.Minute
	}
	if m.PollInterval <= 0 {
		m.PollInterval = 2 * time.Second
	}
	if m.Log == nil {
		m.Log = slog.Default()
	}
}

// Run brings the database up to this binary's migrations, or waits for the node that is doing it.
func (m *Migrator) Run(ctx context.Context) error {
	m.defaults()
	st, err := m.Status(ctx)
	if err != nil {
		return err
	}
	if len(st.Newer) > 0 && !m.AllowOlderBinary {
		return fmt.Errorf("store: database has migrations %v unknown to this binary (it is older than the database); refusing to start", st.Newer)
	}
	if len(st.Pending) == 0 {
		return nil
	}
	deadline := time.Now().Add(m.WaitTimeout)
	for {
		lock, err := m.DB.TryLock(ctx, migrationLock, m.Holder, m.LockTTL)
		switch {
		case err == nil:
			defer func() { _ = lock.Release(context.WithoutCancel(ctx)) }()
			return m.apply(ctx, lock)
		case !errors.Is(err, ErrLockHeld):
			return err
		}
		// Another node is migrating: wait until it is done.
		st, err := m.Status(ctx)
		if err != nil {
			return err
		}
		if len(st.Pending) == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return ErrMigrationsPending
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(m.PollInterval):
		}
	}
}

func (m *Migrator) apply(ctx context.Context, lock *Lock) error {
	// Re-read under the lock: the previous holder may have applied some steps.
	st, err := m.Status(ctx)
	if err != nil {
		return err
	}
	pending := map[int]bool{}
	for _, n := range st.Pending {
		pending[n] = true
	}
	mctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		select {
		case <-lock.Lost():
			cancel()
		case <-mctx.Done():
		}
	}()
	for _, mg := range m.Migrations {
		if !pending[mg.Number] {
			continue
		}
		t0 := time.Now()
		m.Log.Info("applying migration", "number", mg.Number, "name", mg.Name)
		if err := mg.Up(mctx, m.DB); err != nil {
			if mctx.Err() != nil && ctx.Err() == nil {
				return fmt.Errorf("store: migration lock lost during %d %s", mg.Number, mg.Name)
			}
			return fmt.Errorf("store: migration %d %s: %w", mg.Number, mg.Name, err)
		}
		rec := bson.M{"_id": mg.Number, "name": mg.Name, "checksum": mg.checksum(),
			"appliedAt": time.Now().UTC(), "durationMs": time.Since(t0).Milliseconds(), "appliedBy": m.Holder}
		if _, err := m.DB.Collection(MigrationsCollection).InsertOne(mctx, rec); err != nil && !mongo.IsDuplicateKeyError(err) {
			return fmt.Errorf("store: record migration %d: %w", mg.Number, err)
		}
	}
	return nil
}

// EnsureIndexes creates the given indexes on coll (idempotent; same name and keys is a no-op).
func (d *DB) EnsureIndexes(ctx context.Context, coll string, models ...mongo.IndexModel) error {
	if len(models) == 0 {
		return nil
	}
	_, err := d.Collection(coll).Indexes().CreateMany(ctx, models, options.CreateIndexes())
	if err != nil {
		return fmt.Errorf("store: indexes on %s: %w", coll, err)
	}
	return nil
}
