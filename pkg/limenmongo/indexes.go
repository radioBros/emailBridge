package limenmongo

import (
	"context"
	"fmt"

	"github.com/thecodearcher/limen"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// bsonTypes lists the BSON types a value of a Limen column type is stored as; a unique index over a
// nullable column only covers documents whose value has one of them, so several NULLs never collide
// (SQL semantics).
func bsonTypes(colType string) bson.A {
	switch colType {
	case typeInt, typeInt32, typeInt64:
		return bson.A{"int", "long", "double", "decimal"}
	case typeBool:
		return bson.A{"bool"}
	case typeTime:
		return bson.A{"date"}
	case typeMap:
		return bson.A{"object"}
	default:
		return bson.A{"string"}
	}
}

// IndexModels returns the indexes EnsureIndexes creates, per collection.
func (a *Adapter) IndexModels() map[string][]mongo.IndexModel {
	out := map[string][]mongo.IndexModel{}
	for _, name := range a.schema.TableNames() {
		t := a.schema.Table(name)
		coll := a.prefix + name
		ttlCol, wantTTL := a.ttlColumns[name]
		for _, ix := range t.Indexes {
			if len(ix.Columns) == 0 {
				continue
			}
			keys := bson.D{}
			partial := bson.M{}
			for _, c := range ix.Columns {
				keys = append(keys, bson.E{Key: fieldName(c), Value: 1})
				if col, ok := t.Column(c); ok && col.IsNullable && ix.Unique {
					partial[fieldName(c)] = bson.M{"$type": bsonTypes(col.Type)}
				}
			}
			io := options.Index().SetName(ix.Name).SetUnique(ix.Unique)
			if len(partial) > 0 {
				io.SetPartialFilterExpression(partial)
			}
			if wantTTL && len(ix.Columns) == 1 && ix.Columns[0] == ttlCol && !ix.Unique {
				io.SetExpireAfterSeconds(0) // the schema's own index on the expiry column becomes the TTL index
				wantTTL = false
			}
			out[coll] = append(out[coll], mongo.IndexModel{Keys: keys, Options: io})
		}
		if wantTTL {
			if _, ok := t.Column(ttlCol); ok {
				out[coll] = append(out[coll], mongo.IndexModel{
					Keys:    bson.D{{Key: fieldName(ttlCol), Value: 1}},
					Options: options.Index().SetName("ttl_" + name + "_" + ttlCol).SetExpireAfterSeconds(0),
				})
			}
		}
	}
	return out
}

// EnsureIndexes creates the schema's unique and plain indexes and the TTL indexes (idempotent).
func (a *Adapter) EnsureIndexes(ctx context.Context) error {
	models := a.IndexModels()
	for _, name := range a.schema.TableNames() {
		coll := a.prefix + name
		if len(models[coll]) == 0 {
			continue
		}
		if _, err := a.Collection(limen.SchemaTableName(name)).Indexes().CreateMany(ctx, models[coll]); err != nil {
			return fmt.Errorf("limenmongo: indexes on %s: %w", coll, err)
		}
	}
	return nil
}
