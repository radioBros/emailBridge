// Package store is EmailBridge's MongoDB layer: the client and its lifecycle module, resource IDs,
// transactions, the tenant guard and the lease-guarded migration runner (S1 §1, §8).
package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readconcern"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"
	"go.mongodb.org/mongo-driver/v2/mongo/writeconcern"
)

// DB wraps the client and the EmailBridge database.
type DB struct {
	Client *mongo.Client
	DB     *mongo.Database
}

// Options configure Connect.
type Options struct {
	URI      string
	Database string
	// AppName is reported to MongoDB (visible in server logs and currentOp).
	AppName string
	// ConnectTimeout bounds the initial connection and ping (default 20 s).
	ConnectTimeout time.Duration
}

// Connect opens the client with majority read/write concerns (required for the transactional outbox,
// S4 §3) and verifies the deployment is a replica set, because transactions need one (S1 §8).
func Connect(ctx context.Context, o Options) (*DB, error) {
	if o.URI == "" || o.Database == "" {
		return nil, errors.New("store: URI and database are required")
	}
	timeout := o.ConnectTimeout
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	co := options.Client().
		ApplyURI(o.URI).
		SetAppName(o.AppName).
		SetReadConcern(readconcern.Majority()).
		SetWriteConcern(writeconcern.Majority()).
		SetRetryWrites(true).
		SetRetryReads(true)
	client, err := mongo.Connect(co)
	if err != nil {
		return nil, fmt.Errorf("store: connect: %w", err)
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := client.Ping(cctx, readpref.Primary()); err != nil {
		_ = client.Disconnect(context.WithoutCancel(ctx))
		return nil, fmt.Errorf("store: ping primary: %w", err)
	}
	db := &DB{Client: client, DB: client.Database(o.Database)}
	if err := db.requireReplicaSet(cctx); err != nil {
		_ = client.Disconnect(context.WithoutCancel(ctx))
		return nil, err
	}
	return db, nil
}

func (d *DB) requireReplicaSet(ctx context.Context) error {
	var hello struct {
		SetName string `bson:"setName"`
		Msg     string `bson:"msg"`
	}
	if err := d.Client.Database("admin").RunCommand(ctx, map[string]any{"hello": 1}).Decode(&hello); err != nil {
		return fmt.Errorf("store: hello: %w", err)
	}
	if hello.SetName == "" && hello.Msg != "isdbgrid" {
		return errors.New("store: MongoDB must run as a replica set (transactions are required); see S15 for the single-node setup")
	}
	return nil
}

// Collection returns a collection of the EmailBridge database.
func (d *DB) Collection(name string) *mongo.Collection { return d.DB.Collection(name) }

// Ping checks the primary is reachable (used by /readyz).
func (d *DB) Ping(ctx context.Context) error { return d.Client.Ping(ctx, readpref.Primary()) }

// Close disconnects the client.
func (d *DB) Close(ctx context.Context) error { return d.Client.Disconnect(ctx) }

// WithTx runs fn inside a transaction with majority write concern. The driver retries fn on transient
// transaction errors and retries the commit on unknown commit results, so fn must be idempotent with
// respect to everything outside the database. The ctx passed to fn carries the session: every
// operation inside fn must use it.
func (d *DB) WithTx(ctx context.Context, fn func(ctx context.Context) error) error {
	sess, err := d.Client.StartSession()
	if err != nil {
		return fmt.Errorf("store: start session: %w", err)
	}
	defer sess.EndSession(context.WithoutCancel(ctx))
	txo := options.Transaction().SetWriteConcern(writeconcern.Majority()).SetReadConcern(readconcern.Snapshot())
	_, err = sess.WithTransaction(ctx, func(sc context.Context) (any, error) {
		return nil, fn(sc)
	}, txo)
	return err
}
