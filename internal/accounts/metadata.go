package accounts

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/logging"
	"github.com/radiobros/emailbridge/internal/store"
)

// Free-form account metadata (G4): the caller's own small JSON object — CRM IDs, tenant codes, tags.

const (
	// MetadataMaxKeys is the most keys one account's metadata may hold.
	MetadataMaxKeys = 32
	// MetadataMaxValueBytes caps one string value (UTF-8 bytes).
	MetadataMaxValueBytes = 1024
	// MetadataMaxBytes caps the whole object, serialised as JSON.
	MetadataMaxBytes = 8 * 1024
	// MetadataMaxFilters is the most `metadata.<key>` filters one list request may use.
	MetadataMaxFilters = 3

	// fieldErrorsKey carries []errs.ProblemItem on a validation error so the API can return problem `errors[]`.
	fieldErrorsKey = "fieldErrors"
)

var metadataKeyRe = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)

// validMetadataKey reports whether k may be used as a metadata key.
func validMetadataKey(k string) bool { return metadataKeyRe.MatchString(k) }

// fieldInvalid is a validation_failed error that names the offending field; path is the problem item
// location (body.metadata.<key>, query.metadata.<key>, body.metadata).
func fieldInvalid(path, code, msg string) *errs.E {
	return errs.New(errs.CodeValidationFailed, msg).
		WithSafe("field", path).
		WithSafe(fieldErrorsKey, []errs.ProblemItem{{Path: path, Code: code, Message: msg}})
}

// fieldErrorsOf returns the field-level items attached by fieldInvalid, or nil.
func fieldErrorsOf(err error) []errs.ProblemItem {
	var e *errs.E
	if !errors.As(err, &e) || e.Code != errs.CodeValidationFailed {
		return nil
	}
	items, _ := e.Safe[fieldErrorsKey].([]errs.ProblemItem)
	return items
}

// withFieldErrors turns a validation error that carries field items into a problem with `errors[]`; any
// other error is returned unchanged (httpapi.Register renders it). A huma.StatusError passes through
// Register untouched, so the request and trace IDs are filled here.
func withFieldErrors(ctx context.Context, err error) error {
	items := fieldErrorsOf(err)
	if len(items) == 0 {
		return err
	}
	ids := logging.IDsFrom(ctx)
	p := errs.ProblemFrom(err, ids.RequestID, ids.TraceID)
	p.Errors = items
	return p
}

// normalizeMetadataValue checks one value and returns its stored form: string, float64, bool or nil.
// Numbers are stored as doubles so a value reads back the same from MongoDB and JSON.
func normalizeMetadataValue(key string, v any) (any, error) {
	path := "body.metadata." + key
	switch x := v.(type) {
	case nil:
		return nil, nil
	case string:
		if len(x) > MetadataMaxValueBytes {
			return nil, fieldInvalid(path, "too_long",
				fmt.Sprintf("metadata.%s must be at most %d bytes.", key, MetadataMaxValueBytes))
		}
		return x, nil
	case bool:
		return x, nil
	case float64:
		return finiteNumber(path, key, x)
	case float32:
		return finiteNumber(path, key, float64(x))
	case int:
		return float64(x), nil
	case int32:
		return float64(x), nil
	case int64:
		return float64(x), nil
	case json.Number:
		f, err := x.Float64()
		if err != nil {
			return nil, fieldInvalid(path, "invalid", fmt.Sprintf("metadata.%s is not a valid number.", key))
		}
		return finiteNumber(path, key, f)
	default:
		return nil, fieldInvalid(path, "invalid_type",
			fmt.Sprintf("metadata.%s must be a string, number, boolean or null.", key))
	}
}

func finiteNumber(path, key string, f float64) (any, error) {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return nil, fieldInvalid(path, "invalid", fmt.Sprintf("metadata.%s is not a finite number.", key))
	}
	return f, nil
}

// normalizeMetadata validates a whole metadata object and returns its stored form (a new map). nil and an
// empty object both yield nil (no metadata). Keys are checked in sorted order so the reported error is stable.
func normalizeMetadata(in map[string]any) (map[string]any, error) {
	if len(in) == 0 {
		return nil, nil
	}
	if len(in) > MetadataMaxKeys {
		return nil, fieldInvalid("body.metadata", "too_many_keys",
			fmt.Sprintf("metadata may hold at most %d keys.", MetadataMaxKeys))
	}
	keys := make([]string, 0, len(in))
	for k := range in {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make(map[string]any, len(in))
	for _, k := range keys {
		if !validMetadataKey(k) {
			return nil, fieldInvalid("body.metadata."+k, "invalid_key",
				"metadata keys must be 1–64 characters of A–Z, a–z, 0–9, '_', '.' or '-'.").WithSafe("key", k)
		}
		v, err := normalizeMetadataValue(k, in[k])
		if err != nil {
			return nil, err
		}
		out[k] = v
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return nil, fieldInvalid("body.metadata", "invalid", "metadata is not a valid JSON object.")
	}
	if len(raw) > MetadataMaxBytes {
		return nil, fieldInvalid("body.metadata", "too_large",
			fmt.Sprintf("metadata must be at most %d bytes serialised as JSON.", MetadataMaxBytes))
	}
	return out, nil
}

// decodeMetadataPatch parses a PATCH `metadata` member: JSON null clears every key (clear=true), an object is
// a JSON Merge Patch of the stored object (a member set to null removes that key). Numbers are decoded as
// float64 (no UseNumber, which would store them as strings).
func decodeMetadataPatch(raw json.RawMessage) (patch map[string]any, clear bool, err error) {
	trimmed := bytes.TrimSpace(raw)
	if bytes.Equal(trimmed, []byte("null")) {
		return nil, true, nil
	}
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, false, fieldInvalid("body.metadata", "invalid_type", "metadata must be an object or null.")
	}
	if err := json.Unmarshal(trimmed, &patch); err != nil {
		return nil, false, fieldInvalid("body.metadata", "invalid_type", "metadata must be an object or null.")
	}
	if patch == nil {
		patch = map[string]any{}
	}
	return patch, false, nil
}

// mergeMetadata applies a JSON Merge Patch (RFC 7396) to cur and validates the result. cur is not modified.
// A patch member set to null removes the key; any other member replaces it. The result is nil when empty.
func mergeMetadata(cur, patch map[string]any) (map[string]any, error) {
	merged := make(map[string]any, len(cur)+len(patch))
	for k, v := range cur {
		merged[k] = v
	}
	keys := make([]string, 0, len(patch))
	for k := range patch {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := patch[k]
		if !validMetadataKey(k) {
			return nil, fieldInvalid("body.metadata."+k, "invalid_key",
				"metadata keys must be 1–64 characters of A–Z, a–z, 0–9, '_', '.' or '-'.").WithSafe("key", k)
		}
		if v == nil {
			delete(merged, k)
			continue
		}
		merged[k] = v
	}
	return normalizeMetadata(merged)
}

// metadataEqual reports whether two normalised metadata objects hold the same keys and values.
func metadataEqual(a, b map[string]any) bool {
	if len(a) != len(b) {
		return false
	}
	for k, av := range a {
		bv, ok := b[k]
		if !ok || av != bv {
			return false
		}
	}
	return true
}

// copyMetadata returns a shallow copy (values are scalars), or nil for an empty object.
func copyMetadata(m map[string]any) map[string]any {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// metadataListFilter validates the `metadata.<key>=<value>` list filters and returns the Mongo condition
// (string equality on each key, all must match), or nil when there are none. Keys are passed to $getField as
// values, never interpolated into a field path, so keys with '.' match the literal key.
func metadataListFilter(f map[string]string) (bson.M, error) {
	if len(f) == 0 {
		return nil, nil
	}
	if len(f) > MetadataMaxFilters {
		return nil, fieldInvalid("query.metadata", "too_many_filters",
			fmt.Sprintf("At most %d metadata filters are allowed.", MetadataMaxFilters))
	}
	keys := make([]string, 0, len(f))
	for k := range f {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	conds := make([]any, 0, len(keys))
	for _, k := range keys {
		if !validMetadataKey(k) {
			return nil, fieldInvalid("query.metadata."+k, "invalid_key",
				"metadata keys must be 1–64 characters of A–Z, a–z, 0–9, '_', '.' or '-'.").WithSafe("key", k)
		}
		v := f[k]
		if len(v) > MetadataMaxValueBytes {
			return nil, fieldInvalid("query.metadata."+k, "too_long",
				fmt.Sprintf("metadata.%s filter value must be at most %d bytes.", k, MetadataMaxValueBytes))
		}
		conds = append(conds, bson.M{"$eq": bson.A{
			bson.M{"$getField": bson.M{"field": bson.M{"$literal": k}, "input": "$metadata"}},
			bson.M{"$literal": v},
		}})
	}
	if len(conds) == 1 {
		return bson.M{"$expr": conds[0]}, nil
	}
	return bson.M{"$expr": bson.M{"$and": conds}}, nil
}

// RuleAccount loads the `account` variable of the rules engine (S8 §2): id, externalId, email, name and
// metadata (G4). An unknown account yields an empty map (the engine fills the defaults).
func RuleAccount(ctx context.Context, db *store.DB, orgID, accountID string) (map[string]any, error) {
	var acc struct {
		ID         string         `bson:"_id"`
		ExternalID *string        `bson:"externalId"`
		Email      string         `bson:"email"`
		Name       string         `bson:"name"`
		Metadata   map[string]any `bson:"metadata"`
	}
	err := db.Collection(Collection).FindOne(ctx, bson.M{"_id": accountID, "organizationId": orgID},
		options.FindOne().SetProjection(bson.M{"externalId": 1, "email": 1, "name": 1, "metadata": 1})).Decode(&acc)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := map[string]any{"id": acc.ID, "email": acc.Email, "name": acc.Name, "metadata": copyMetadata(acc.Metadata)}
	if out["metadata"] == nil {
		out["metadata"] = map[string]any{}
	}
	if acc.ExternalID != nil {
		out["externalId"] = *acc.ExternalID
	}
	return out, nil
}
