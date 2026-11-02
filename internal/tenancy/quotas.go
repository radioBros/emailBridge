package tenancy

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/store"
)

// Organisation quotas (S9 §2.12, owner decision 2026-10-27). An organisation's `quotas` object may set any of
// the keys below; each mirrors an instance-wide EB_QUOTA_* limit and is enforced IN ADDITION to it — the
// stricter (smaller non-zero) limit wins. Unset, null or 0 = no organisation limit (unlimited unless the
// instance sets one). Values are whole numbers ≥ 0; unknown keys are rejected.
const (
	// QuotaAccounts caps the accounts of the organisation (mirrors EB_QUOTA_ACCOUNTS).
	QuotaAccounts = "accounts"
	// QuotaSendsPerDay caps the messages the organisation submits in a rolling 24 h (EB_QUOTA_SENDS_PER_DAY).
	QuotaSendsPerDay = "sendsPerDay"
	// QuotaSendsPerAccountMinute caps the messages each account of the organisation submits in a rolling
	// minute (mirrors EB_QUOTA_SENDS_PER_ACCOUNT_MINUTE; per account, not organisation-wide).
	QuotaSendsPerAccountMinute = "sendsPerAccountMinute"
	// QuotaEndpoints caps webhook endpoints of the organisation (mirrors EB_QUOTA_ENDPOINTS).
	QuotaEndpoints = "endpoints"
)

// quotaKeys is the closed set of organisation quota keys.
var quotaKeys = map[string]bool{
	QuotaAccounts: true, QuotaSendsPerDay: true, QuotaSendsPerAccountMinute: true, QuotaEndpoints: true,
}

// QuotaKeys lists the organisation quota keys, sorted.
func QuotaKeys() []string {
	out := make([]string, 0, len(quotaKeys))
	for k := range quotaKeys {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// maxQuotaValue bounds a quota value (keeps it an exact integer in JSON and int64).
const maxQuotaValue = 1 << 40

// OrgQuotas are an organisation's resolved quota limits; 0 = no organisation limit.
type OrgQuotas struct {
	Accounts              int
	SendsPerDay           int
	SendsPerAccountMinute int
	Endpoints             int
}

// Get returns the limit of key (0 for an unknown key).
func (q OrgQuotas) Get(key string) int {
	switch key {
	case QuotaAccounts:
		return q.Accounts
	case QuotaSendsPerDay:
		return q.SendsPerDay
	case QuotaSendsPerAccountMinute:
		return q.SendsPerAccountMinute
	case QuotaEndpoints:
		return q.Endpoints
	}
	return 0
}

// NormalizeQuotas validates a quotas object from the API and returns it with whole-number int64 values;
// null values are dropped (= unset). nil in → nil out.
func NormalizeQuotas(in map[string]any) (map[string]any, error) {
	if in == nil {
		return nil, nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		if !quotaKeys[k] {
			return nil, errs.New(errs.CodeValidationFailed,
				fmt.Sprintf("Unknown quota %q; allowed: %s.", k, strings.Join(QuotaKeys(), ", "))).WithSafe("quota", k)
		}
		if v == nil {
			continue
		}
		n, ok := quotaInt(v)
		if !ok || n < 0 || n > maxQuotaValue {
			return nil, errs.New(errs.CodeValidationFailed,
				fmt.Sprintf("Quota %q must be a whole number from 0 (unlimited) up to %d.", k, int64(maxQuotaValue))).WithSafe("quota", k)
		}
		out[k] = n
	}
	return out, nil
}

// quotaInt reads a whole number from a JSON (float64) or BSON (int32/int64/float64) value.
func quotaInt(v any) (int64, bool) {
	switch x := v.(type) {
	case int:
		return int64(x), true
	case int32:
		return int64(x), true
	case int64:
		return x, true
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) || x != math.Trunc(x) || math.Abs(x) > maxQuotaValue {
			return 0, false
		}
		return int64(x), true
	}
	return 0, false
}

// parseOrgQuotas reads stored quotas leniently: malformed or unknown entries count as unset.
func parseOrgQuotas(m map[string]any) OrgQuotas {
	var q OrgQuotas
	get := func(k string) int {
		n, ok := quotaInt(m[k])
		if !ok || n <= 0 || n > maxQuotaValue {
			return 0
		}
		return int(n)
	}
	q.Accounts = get(QuotaAccounts)
	q.SendsPerDay = get(QuotaSendsPerDay)
	q.SendsPerAccountMinute = get(QuotaSendsPerAccountMinute)
	q.Endpoints = get(QuotaEndpoints)
	return q
}

// LoadOrgQuotas reads the quotas of organisation org. An unknown organisation has no quotas.
func LoadOrgQuotas(ctx context.Context, db *store.DB, org string) (OrgQuotas, error) {
	if db == nil || db.DB == nil || org == "" {
		return OrgQuotas{}, nil
	}
	var doc struct {
		Quotas map[string]any `bson:"quotas"`
	}
	err := db.Collection(OrganizationsCollection).FindOne(ctx, bson.M{"_id": org},
		options.FindOne().SetProjection(bson.M{"quotas": 1})).Decode(&doc)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return OrgQuotas{}, nil
	}
	if err != nil {
		return OrgQuotas{}, errs.Wrap(err, errs.CodeInternalError, "tenancy.quotas")
	}
	return parseOrgQuotas(doc.Quotas), nil
}

// EffectiveLimit combines an instance limit and an organisation limit (0 = none): the smaller non-zero one
// wins. fromOrg reports whether the organisation's limit is the one in force (it wins ties).
func EffectiveLimit(instance, org int) (limit int, fromOrg bool) {
	switch {
	case org > 0 && (instance <= 0 || org <= instance):
		return org, true
	case instance > 0:
		return instance, false
	}
	return 0, false
}

// OrgQuotaExceeded is the problem of a reached organisation quota (organization_quota_exceeded, 429).
func OrgQuotaExceeded(key string, limit int) *errs.E {
	return errs.New(errs.CodeOrganizationQuotaExceeded,
		fmt.Sprintf("The organisation's %s quota (%d) is reached.", key, limit)).
		WithSafe("quota", key).WithSafe("limit", limit)
}
