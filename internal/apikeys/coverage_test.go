package apikeys

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/netip"
	"testing"
	"time"

	"github.com/thecodearcher/limen"
	apikey "github.com/thecodearcher/limen/plugins/api-key"

	"github.com/radiobros/emailbridge/internal/errs"
)

func discardLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestMapLimenAndVerifyErrs(t *testing.T) {
	cases := []struct {
		err  error
		code errs.Code
	}{
		{apikey.ErrInvalidAPIKey, errs.CodeAPIKeyInvalid},
		{apikey.ErrAPIKeyDisabled, errs.CodeAPIKeyRevoked},
		{apikey.ErrAPIKeyExpired, errs.CodeAPIKeyExpired},
		{apikey.ErrInsufficientPermissions, errs.CodeScopeMissing},
		{limen.ErrForbidden, errs.CodeForbidden},
		{limen.ErrRecordNotFound, errs.CodeNotFound},
		{limen.NewLimenError("x", 401, nil), errs.CodeAPIKeyInvalid},
		{limen.NewLimenError("x", 403, nil), errs.CodeForbidden},
		{limen.NewLimenError("x", 404, nil), errs.CodeNotFound},
		{errors.New("boom"), errs.CodeInternalError},
	}
	for _, c := range cases {
		if got := errs.CodeOf(mapLimenErr(c.err, "op")); got != c.code {
			t.Fatalf("%v → %s want %s", c.err, got, c.code)
		}
	}
	if mapLimenErr(nil, "op") != nil {
		t.Fatal("nil")
	}
	checks := []struct {
		err  error
		code errs.Code
	}{
		{apikey.ErrRateLimitExceeded, errs.CodeRateLimited},
		{apikey.ErrInvalidAPIKey, errs.CodeAPIKeyInvalid},
		{apikey.ErrAPIKeyDisabled, errs.CodeAPIKeyRevoked},
		{apikey.ErrAPIKeyExpired, errs.CodeAPIKeyExpired},
		{apikey.ErrInsufficientPermissions, errs.CodeScopeMissing},
		{limen.NewLimenError("x", 401, nil), errs.CodeAPIKeyInvalid},
	}
	for _, c := range checks {
		if got := errs.CodeOf(mapVerifyErr(c.err)); got != c.code {
			t.Fatalf("verify %v → %s", c.err, got)
		}
	}
}

func TestNullableUnmarshal(t *testing.T) {
	var ns nullableString
	if err := json.Unmarshal([]byte(`null`), &ns); err != nil || !ns.Present || !ns.Null {
		t.Fatal(ns, err)
	}
	ns = nullableString{}
	if err := json.Unmarshal([]byte(`"acc_x"`), &ns); err != nil || ns.Null || ns.Value != "acc_x" {
		t.Fatal(ns, err)
	}
	var nt nullableTime
	if err := json.Unmarshal([]byte(`null`), &nt); err != nil || !nt.Null {
		t.Fatal(nt, err)
	}
	nt = nullableTime{}
	if err := json.Unmarshal([]byte(`"2026-12-01T00:00:00Z"`), &nt); err != nil || nt.Value.Year() != 2026 {
		t.Fatal(nt, err)
	}
	if err := json.Unmarshal([]byte(`"not-a-time"`), &nt); err == nil {
		t.Fatal("bad time")
	}
}

func TestSortKeyAndCursorPaths(t *testing.T) {
	now := time.Now().UTC()
	k := Key{ID: "key_a", Name: "n", CreatedAt: now, LastUsedAt: &now}
	if sortKey(k, "name") != "n" || sortKey(k, "createdAt") == nil || sortKey(k, "lastUsedAt") == nil || sortKey(k, "_id") != "key_a" {
		t.Fatal("sortKey")
	}
	if sortKey(Key{ID: "key_b"}, "lastUsedAt") != nil {
		t.Fatal("nil lastUsed")
	}
	if _, err := decodeCursor(encodeCursor(cursorPayload{})); err == nil {
		t.Fatal("empty id")
	}
	if _, err := cursorFilter(cursorPayload{ID: "key_a", Key: now.Format(time.RFC3339Nano)}, "createdAt", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := cursorFilter(cursorPayload{ID: "key_a", Key: "bad"}, "createdAt", -1); err == nil {
		t.Fatal("bad time key")
	}
	if _, err := cursorFilter(cursorPayload{ID: "key_a", Key: now.Format(time.RFC3339)}, "lastUsedAt", -1); err != nil {
		t.Fatal(err)
	}
}

func TestRestrictionsEdge(t *testing.T) {
	if err := validateRestrictions(&Restrictions{Referrers: []string{""}}); err == nil {
		t.Fatal("empty referrer")
	}
	if err := validateRestrictions(&Restrictions{Referrers: []string{"["}}); err == nil {
		t.Fatal("bad pattern")
	}
	r := &Restrictions{IPs: []string{"203.0.113.9"}}
	if err := checkIP(netip.MustParseAddr("203.0.113.9"), r); err != nil {
		t.Fatal(err)
	}
	if err := checkIP(netip.Addr{}, r); err == nil {
		t.Fatal("invalid addr")
	}
	req, _ := http.NewRequest(http.MethodGet, "/", nil)
	_ = checkReferrer(req, &Restrictions{Referrers: []string{"*"}})
}

func TestPurgeNilCacheCloneRateRoles(t *testing.T) {
	s := &Service{log: discardLog()}
	s.purgeVerifiedCache(context.Background(), "")
	s.purgeVerifiedCache(context.Background(), "hash")
	if cloneRateLimit(&RateLimit{RPS: 1, Burst: 2}).Burst != 2 {
		t.Fatal("rate")
	}
	m := &Module{}
	if len(m.Roles()) != 1 {
		t.Fatal(m.Roles())
	}
	_ = New(nil, nil, nil, nil, nil) // default log
}
