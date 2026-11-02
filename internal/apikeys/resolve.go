package apikeys

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/thecodearcher/limen"
	apikey "github.com/thecodearcher/limen/plugins/api-key"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/radiobros/emailbridge/internal/auth"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

// Resolve implements tenancy.KeyResolver: Bearer token → Caller, or (nil, nil) when absent.
func (s *Service) Resolve(r *http.Request) (*tenancy.Caller, error) {
	token, ok := bearerToken(r)
	if !ok {
		return nil, nil
	}
	if token == "" {
		return nil, errs.New(errs.CodeAPIKeyInvalid, "The API key is invalid.")
	}
	if !strings.HasPrefix(token, auth.APIKeyPrefix) {
		return nil, errs.New(errs.CodeAPIKeyInvalid, "The API key is invalid.")
	}

	ctx := r.Context()
	lk, err := s.keys().Verify(ctx, token, nil)
	if err != nil {
		return nil, mapVerifyErr(err)
	}
	meta, err := s.byLimenKeyID(ctx, fmtID(lk.ID))
	if err != nil {
		return nil, err
	}
	if meta.RevokedAt != nil {
		return nil, errs.New(errs.CodeAPIKeyRevoked, "The API key has been revoked.")
	}
	if meta.ExpiresAt != nil && !meta.ExpiresAt.After(time.Now().UTC()) {
		return nil, errs.New(errs.CodeAPIKeyExpired, "The API key has expired.")
	}
	if err := checkIP(clientIP(r), meta.Restrictions); err != nil {
		return nil, err
	}
	if err := checkReferrer(r, meta.Restrictions); err != nil {
		return nil, err
	}
	if err := takeKeyRate(ctx, meta.ID, meta.RateLimit); err != nil {
		return nil, err
	}
	s.touchLastUsed(ctx, meta.ID, meta.LastUsedAt)
	return &tenancy.Caller{
		Kind:           tenancy.CallerAPIKey,
		APIKeyID:       meta.ID,
		OrganizationID: meta.OrganizationID,
		Scopes:         append([]string(nil), meta.Scopes...),
		AccountID:      meta.AccountID,
	}, nil
}

// bearerToken extracts an Authorization Bearer credential (RFC 7235 §2.1: scheme is
// case-insensitive). ok is false when the header is absent or uses another scheme.
func bearerToken(r *http.Request) (token string, ok bool) {
	raw := r.Header.Get("Authorization")
	if raw == "" {
		return "", false
	}
	const prefix = "bearer "
	if len(raw) >= len(prefix) && strings.EqualFold(raw[:len(prefix)], prefix) {
		return strings.TrimSpace(raw[len(prefix):]), true
	}
	if strings.EqualFold(strings.TrimSpace(raw), "Bearer") {
		return "", true // scheme present, empty credential
	}
	return "", false
}

func (s *Service) byLimenKeyID(ctx context.Context, limenID string) (Key, error) {
	var k Key
	err := s.DB.Collection(Collection).FindOne(ctx, bson.M{"limenKeyId": limenID}).Decode(&k)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return Key{}, errs.New(errs.CodeAPIKeyInvalid, "The API key is invalid.")
	}
	if err != nil {
		return Key{}, err
	}
	return k, nil
}

func mapVerifyErr(err error) error {
	switch {
	case errors.Is(err, apikey.ErrInvalidAPIKey):
		return errs.New(errs.CodeAPIKeyInvalid, "The API key is invalid.")
	case errors.Is(err, apikey.ErrAPIKeyDisabled):
		return errs.New(errs.CodeAPIKeyRevoked, "The API key has been revoked.")
	case errors.Is(err, apikey.ErrAPIKeyExpired):
		return errs.New(errs.CodeAPIKeyExpired, "The API key has expired.")
	case errors.Is(err, apikey.ErrInsufficientPermissions):
		return errs.New(errs.CodeScopeMissing, "The API key lacks the required scope.")
	case errors.Is(err, apikey.ErrRateLimitExceeded):
		return errs.New(errs.CodeRateLimited, "API key rate limit exceeded.")
	}
	var le *limen.LimenError
	if errors.As(err, &le) && le.Status() == 401 {
		return errs.New(errs.CodeAPIKeyInvalid, "The API key is invalid.")
	}
	return mapLimenErr(err, "verify")
}

func fmtID(id any) string {
	if s, ok := id.(string); ok {
		return s
	}
	return fmt.Sprint(id)
}
