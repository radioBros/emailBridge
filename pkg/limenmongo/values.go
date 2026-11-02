package limenmongo

import (
	"math"
	"reflect"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// fieldName maps a Limen column to the MongoDB field: "id" is stored as "_id".
func fieldName(column string) string {
	if column == "id" {
		return "_id"
	}
	return column
}

// deref flattens pointers (Limen's ToStorage uses *string, *time.Time, …) and named string types the way
// a SQL driver would, so values are stored and compared as plain BSON scalars.
func deref(v any) any {
	if v == nil {
		return nil
	}
	rv := reflect.ValueOf(v)
	for rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return nil
		}
		rv = rv.Elem()
	}
	switch x := rv.Interface().(type) {
	case time.Time:
		return x.UTC()
	case string, []byte, bool:
		return x
	}
	if rv.Kind() == reflect.String {
		return rv.String()
	}
	return rv.Interface()
}

// toInt64 converts any Go or BSON integer (or an integral float) to int64.
func toInt64(v any) (int64, bool) {
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return rv.Int(), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		u := rv.Uint()
		if u > math.MaxInt64 {
			return 0, false
		}
		return int64(u), true
	case reflect.Float32, reflect.Float64:
		f := rv.Float()
		if f != math.Trunc(f) || f > math.MaxInt64 || f < math.MinInt64 {
			return 0, false
		}
		return int64(f), true
	}
	return 0, false
}

// coerce converts v to the Go type Limen expects for a column of type colType: int/int32 → int32,
// int64 → int64, time.Time → UTC time.Time, map[string]any → map. Values that do not fit are returned
// unchanged (Limen then sees what was stored, like with any other driver).
func coerce(colType string, v any) any {
	v = deref(v)
	if v == nil {
		return nil
	}
	switch colType {
	case typeInt, typeInt32:
		if n, ok := toInt64(v); ok && n >= math.MinInt32 && n <= math.MaxInt32 {
			return int32(n)
		}
	case typeInt64:
		if n, ok := toInt64(v); ok {
			return n
		}
	case typeTime:
		if dt, ok := v.(bson.DateTime); ok {
			return dt.Time().UTC()
		}
	case typeMap:
		if m := normalize(v); m != nil {
			if mm, ok := m.(map[string]any); ok {
				return mm
			}
		}
	}
	return v
}

// normalize converts driver types read from MongoDB to plain Go values.
func normalize(v any) any {
	switch x := v.(type) {
	case bson.DateTime:
		return x.Time().UTC()
	case time.Time:
		return x.UTC()
	case bson.M:
		m := make(map[string]any, len(x))
		for k, vv := range x {
			m[k] = normalize(vv)
		}
		return m
	case bson.D:
		m := make(map[string]any, len(x))
		for _, e := range x {
			m[e.Key] = normalize(e.Value)
		}
		return m
	case map[string]any:
		m := make(map[string]any, len(x))
		for k, vv := range x {
			m[k] = normalize(vv)
		}
		return m
	case bson.A:
		arr := make([]any, len(x))
		for i, vv := range x {
			arr[i] = normalize(vv)
		}
		return arr
	case bson.Binary:
		return x.Data
	case bson.ObjectID:
		return x.Hex()
	default:
		return v
	}
}

// toArray turns any slice or array value into a BSON array for $in / $nin; ok=false when v is not one.
func toArray(v any) (bson.A, bool) {
	v = deref(v)
	if v == nil {
		return nil, false
	}
	if _, isBytes := v.([]byte); isBytes {
		return nil, false
	}
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array {
		return nil, false
	}
	out := make(bson.A, rv.Len())
	for i := range rv.Len() {
		out[i] = deref(rv.Index(i).Interface())
	}
	return out, true
}
