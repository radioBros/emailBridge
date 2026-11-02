// Package limenmongo is a MongoDB DatabaseAdapter and TransactionalAdapter for Limen
// (github.com/thecodearcher/limen), S10 §2.5.
//
// Mapping:
//   - Limen table X is stored in collection <prefix>X (default prefix "auth_"); Limen's column names are
//     kept as field names, and column "id" is the document _id.
//   - Pointers are dereferenced on write; values are typed by the column types of Limen's discovered
//     schema (int → int32, int64 → int64, time.Time → UTC time, map[string]any → map) on write and on read,
//     because Limen's models assert exact Go types.
//   - MongoDB has no column defaults, and Limen depends on them (users.two_factor_enabled, created_at, …).
//     Create fills every missing or nil column from the schema's DefaultValue ("@now()" = current UTC time).
//   - FindOne returns limen.ErrRecordNotFound on a miss; Update/Delete report MatchedCount/DeletedCount as
//     RowsAffected (Limen uses it for compare-and-swap); limen.ArithmeticUpdate becomes $inc.
//   - BeginTx runs a MongoDB session transaction (majority write concern, snapshot read concern), so the
//     deployment must be a replica set.
//
// The schema comes from Limen's own discovered-schema file (.limen/schemas.json written by
// limen.Config{CLI: &limen.CLIConfig{Enabled: true}}). The file for EmailBridge's plugin set is embedded
// (schemas/limen-v0.2.3.json); other users pass theirs with WithSchema. EnsureIndexes creates the unique
// and plain indexes the file declares plus TTL indexes on the expiry columns.
package limenmongo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/thecodearcher/limen"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readconcern"
	"go.mongodb.org/mongo-driver/v2/mongo/writeconcern"
)

// DefaultPrefix is the collection-name prefix of Limen tables (S1 §2.1).
const DefaultPrefix = "auth_"

// Adapter implements limen.DatabaseAdapter and limen.TransactionalAdapter on a MongoDB database.
// It is safe for concurrent use; a transaction (Tx) is not.
type Adapter struct {
	db         *mongo.Database
	prefix     string
	schema     *Schema
	ttlColumns map[string]string
	now        func() time.Time
}

// Option configures New.
type Option func(*Adapter)

// WithSchema replaces the embedded schema (for a different Limen plugin set or version).
func WithSchema(s *Schema) Option { return func(a *Adapter) { a.schema = s } }

// WithPrefix sets the collection-name prefix (default "auth_").
func WithPrefix(p string) Option { return func(a *Adapter) { a.prefix = p } }

// WithTTLColumns sets the table → expiry-column map that gets TTL indexes (default sessions.expires_at
// and verifications.expires_at). An empty map disables TTL indexes.
func WithTTLColumns(m map[string]string) Option { return func(a *Adapter) { a.ttlColumns = m } }

// withClock replaces time.Now (tests).
func withClock(now func() time.Time) Option { return func(a *Adapter) { a.now = now } }

// New returns an adapter on db. Without WithSchema it uses the embedded schema file.
func New(db *mongo.Database, opts ...Option) (*Adapter, error) {
	if db == nil {
		return nil, errors.New("limenmongo: database is required")
	}
	a := &Adapter{
		db:         db,
		prefix:     DefaultPrefix,
		ttlColumns: map[string]string{"sessions": "expires_at", "verifications": "expires_at"},
		now:        time.Now,
	}
	for _, o := range opts {
		o(a)
	}
	if a.schema == nil {
		s, err := DefaultSchema()
		if err != nil {
			return nil, err
		}
		a.schema = s
	}
	return a, nil
}

// Schema returns the schema the adapter types values with.
func (a *Adapter) Schema() *Schema { return a.schema }

// Collection returns the collection that stores Limen table name.
func (a *Adapter) Collection(table limen.SchemaTableName) *mongo.Collection {
	return a.db.Collection(a.prefix + string(table))
}

// BeginTx starts a MongoDB transaction; Limen calls Commit or Rollback on the returned Tx.
func (a *Adapter) BeginTx(ctx context.Context) (limen.DatabaseTx, error) {
	sess, err := a.db.Client().StartSession()
	if err != nil {
		return nil, fmt.Errorf("limenmongo: start session: %w", err)
	}
	txo := options.Transaction().SetWriteConcern(writeconcern.Majority()).SetReadConcern(readconcern.Snapshot())
	if err := sess.StartTransaction(txo); err != nil {
		sess.EndSession(context.WithoutCancel(ctx))
		return nil, fmt.Errorf("limenmongo: start transaction: %w", err)
	}
	return &Tx{ops: ops{a: a, sess: sess}}, nil
}

// Create inserts a row, filling column defaults from the schema.
func (a *Adapter) Create(ctx context.Context, t limen.SchemaTableName, data map[string]any) (limen.DatabaseResult, error) {
	return ops{a: a}.Create(ctx, t, data)
}

// FindOne returns the first matching row or limen.ErrRecordNotFound.
func (a *Adapter) FindOne(ctx context.Context, t limen.SchemaTableName, conds []limen.Where, ob []limen.OrderBy) (map[string]any, error) {
	return ops{a: a}.FindOne(ctx, t, conds, ob)
}

// FindMany returns the matching rows.
func (a *Adapter) FindMany(ctx context.Context, t limen.SchemaTableName, conds []limen.Where, qo *limen.QueryOptions) ([]map[string]any, error) {
	return ops{a: a}.FindMany(ctx, t, conds, qo)
}

// Update sets or increments columns of every matching row.
func (a *Adapter) Update(ctx context.Context, t limen.SchemaTableName, conds []limen.Where, updates map[string]any) (limen.DatabaseResult, error) {
	return ops{a: a}.Update(ctx, t, conds, updates)
}

// Delete removes every matching row.
func (a *Adapter) Delete(ctx context.Context, t limen.SchemaTableName, conds []limen.Where) (limen.DatabaseResult, error) {
	return ops{a: a}.Delete(ctx, t, conds)
}

// Exists reports whether a row matches.
func (a *Adapter) Exists(ctx context.Context, t limen.SchemaTableName, conds []limen.Where) (bool, error) {
	return ops{a: a}.Exists(ctx, t, conds)
}

// Count counts the matching rows.
func (a *Adapter) Count(ctx context.Context, t limen.SchemaTableName, conds []limen.Where) (int64, error) {
	return ops{a: a}.Count(ctx, t, conds)
}

// Tx is a MongoDB transaction returned by BeginTx. Commit or Rollback ends it.
type Tx struct {
	ops
	done bool
}

// txEndTimeout bounds commit and abort, which must not hang a request forever.
const txEndTimeout = 15 * time.Second

// Commit commits the transaction, retrying a commit whose result is unknown.
func (t *Tx) Commit() error {
	if t.done {
		return errors.New("limenmongo: transaction already finished")
	}
	t.done = true
	ctx, cancel := context.WithTimeout(context.Background(), txEndTimeout)
	defer cancel()
	defer t.sess.EndSession(ctx)
	var err error
	for range 3 {
		err = t.sess.CommitTransaction(ctx)
		var le mongo.LabeledError
		if err == nil || !errors.As(err, &le) || !le.HasErrorLabel("UnknownTransactionCommitResult") {
			break
		}
	}
	if err != nil {
		return fmt.Errorf("limenmongo: commit: %w", err)
	}
	return nil
}

// Rollback aborts the transaction.
func (t *Tx) Rollback() error {
	if t.done {
		return errors.New("limenmongo: transaction already finished")
	}
	t.done = true
	ctx, cancel := context.WithTimeout(context.Background(), txEndTimeout)
	defer cancel()
	defer t.sess.EndSession(ctx)
	if err := t.sess.AbortTransaction(ctx); err != nil {
		return fmt.Errorf("limenmongo: abort: %w", err)
	}
	return nil
}

var (
	_ limen.DatabaseAdapter      = (*Adapter)(nil)
	_ limen.TransactionalAdapter = (*Adapter)(nil)
	_ limen.DatabaseTx           = (*Tx)(nil)
)

// ops runs the CRUD operations, inside a session transaction when sess is set.
type ops struct {
	a    *Adapter
	sess *mongo.Session
}

func (o ops) ctx(ctx context.Context) context.Context {
	if o.sess != nil {
		return mongo.NewSessionContext(ctx, o.sess)
	}
	return ctx
}

func (o ops) coll(t limen.SchemaTableName) *mongo.Collection { return o.a.Collection(t) }

func (o ops) table(t limen.SchemaTableName) *Table { return o.a.schema.Table(string(t)) }

// toDoc builds the stored document: field names, dereferenced and typed values, defaults, an _id.
func (o ops) toDoc(tbl *Table, data map[string]any) (bson.M, error) {
	doc := make(bson.M, len(data)+4)
	for k, v := range data {
		if tbl != nil {
			if c, ok := tbl.Column(k); ok {
				doc[fieldName(k)] = coerce(c.Type, v)
				continue
			}
		}
		doc[fieldName(k)] = deref(v)
	}
	if tbl != nil {
		now := o.a.now()
		for _, c := range tbl.Columns {
			f := fieldName(c.Name)
			if v, ok := doc[f]; ok && v != nil {
				continue
			}
			def, err := parseDefault(c, now)
			if err != nil {
				return nil, err
			}
			if def != nil {
				doc[f] = def
			}
		}
	}
	if id, ok := doc["_id"]; !ok || id == nil {
		doc["_id"] = uuid.NewString()
	}
	return doc, nil
}

// fromDoc converts a stored document back into a Limen row.
func (o ops) fromDoc(tbl *Table, d bson.M) map[string]any {
	out := make(map[string]any, len(d))
	for k, v := range d {
		col := k
		if k == "_id" {
			col = "id"
		}
		v = normalize(v)
		if tbl != nil {
			if c, ok := tbl.Column(col); ok {
				v = coerce(c.Type, v)
			}
		}
		out[col] = v
	}
	return out
}

func (o ops) Create(ctx context.Context, t limen.SchemaTableName, data map[string]any) (limen.DatabaseResult, error) {
	if len(data) == 0 {
		return limen.DatabaseResult{}, fmt.Errorf("limenmongo: create %s: no data to insert", t)
	}
	doc, err := o.toDoc(o.table(t), data)
	if err != nil {
		return limen.DatabaseResult{}, fmt.Errorf("limenmongo: create %s: %w", t, err)
	}
	if _, err := o.coll(t).InsertOne(o.ctx(ctx), doc); err != nil {
		return limen.DatabaseResult{}, fmt.Errorf("limenmongo: create %s: %w", t, err)
	}
	return limen.DatabaseResult{RowsAffected: 1}, nil
}

func (o ops) FindOne(ctx context.Context, t limen.SchemaTableName, conds []limen.Where, ob []limen.OrderBy) (map[string]any, error) {
	tbl := o.table(t)
	f, err := buildFilter(tbl, conds)
	if err != nil {
		return nil, err
	}
	fo := options.FindOne()
	if s := buildSort(ob); len(s) > 0 {
		fo.SetSort(s)
	}
	var d bson.M
	err = o.coll(t).FindOne(o.ctx(ctx), f, fo).Decode(&d)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, limen.ErrRecordNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("limenmongo: find %s: %w", t, err)
	}
	return o.fromDoc(tbl, d), nil
}

func (o ops) FindMany(ctx context.Context, t limen.SchemaTableName, conds []limen.Where, qo *limen.QueryOptions) ([]map[string]any, error) {
	tbl := o.table(t)
	f, err := buildFilter(tbl, conds)
	if err != nil {
		return nil, err
	}
	fo := options.Find()
	if qo != nil {
		if s := buildSort(qo.OrderBy); len(s) > 0 {
			fo.SetSort(s)
		}
		if qo.Limit > 0 {
			fo.SetLimit(int64(qo.Limit))
		}
		if qo.Offset > 0 {
			fo.SetSkip(int64(qo.Offset))
		}
	}
	cur, err := o.coll(t).Find(o.ctx(ctx), f, fo)
	if err != nil {
		return nil, fmt.Errorf("limenmongo: find %s: %w", t, err)
	}
	var docs []bson.M
	if err := cur.All(o.ctx(ctx), &docs); err != nil {
		return nil, fmt.Errorf("limenmongo: find %s: %w", t, err)
	}
	out := make([]map[string]any, len(docs))
	for i, d := range docs {
		out[i] = o.fromDoc(tbl, d)
	}
	return out, nil
}

func (o ops) Update(ctx context.Context, t limen.SchemaTableName, conds []limen.Where, updates map[string]any) (limen.DatabaseResult, error) {
	if len(updates) == 0 {
		return limen.DatabaseResult{}, nil
	}
	tbl := o.table(t)
	f, err := buildFilter(tbl, conds)
	if err != nil {
		return limen.DatabaseResult{}, err
	}
	set, inc := bson.M{}, bson.M{}
	for k, v := range updates {
		colType := ""
		if tbl != nil {
			if c, ok := tbl.Column(k); ok {
				colType = c.Type
			}
		}
		if au, ok := v.(limen.ArithmeticUpdate); ok {
			if err := au.Validate(); err != nil {
				return limen.DatabaseResult{}, fmt.Errorf("limenmongo: update %s.%s: %w", t, k, err)
			}
			inc[fieldName(k)] = coerce(colType, au.Value())
			continue
		}
		if colType != "" {
			set[fieldName(k)] = coerce(colType, v)
		} else {
			set[fieldName(k)] = deref(v)
		}
	}
	upd := bson.M{}
	if len(set) > 0 {
		upd["$set"] = set
	}
	if len(inc) > 0 {
		upd["$inc"] = inc
	}
	res, err := o.coll(t).UpdateMany(o.ctx(ctx), f, upd)
	if err != nil {
		return limen.DatabaseResult{}, fmt.Errorf("limenmongo: update %s: %w", t, err)
	}
	return limen.DatabaseResult{RowsAffected: res.MatchedCount}, nil
}

func (o ops) Delete(ctx context.Context, t limen.SchemaTableName, conds []limen.Where) (limen.DatabaseResult, error) {
	f, err := buildFilter(o.table(t), conds)
	if err != nil {
		return limen.DatabaseResult{}, err
	}
	res, err := o.coll(t).DeleteMany(o.ctx(ctx), f)
	if err != nil {
		return limen.DatabaseResult{}, fmt.Errorf("limenmongo: delete %s: %w", t, err)
	}
	return limen.DatabaseResult{RowsAffected: res.DeletedCount}, nil
}

func (o ops) Exists(ctx context.Context, t limen.SchemaTableName, conds []limen.Where) (bool, error) {
	f, err := buildFilter(o.table(t), conds)
	if err != nil {
		return false, err
	}
	err = o.coll(t).FindOne(o.ctx(ctx), f, options.FindOne().SetProjection(bson.M{"_id": 1})).Err()
	if errors.Is(err, mongo.ErrNoDocuments) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("limenmongo: exists %s: %w", t, err)
	}
	return true, nil
}

func (o ops) Count(ctx context.Context, t limen.SchemaTableName, conds []limen.Where) (int64, error) {
	f, err := buildFilter(o.table(t), conds)
	if err != nil {
		return 0, err
	}
	n, err := o.coll(t).CountDocuments(o.ctx(ctx), f)
	if err != nil {
		return 0, fmt.Errorf("limenmongo: count %s: %w", t, err)
	}
	return n, nil
}
