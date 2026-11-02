package apikeys

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/thecodearcher/limen"
	apikey "github.com/thecodearcher/limen/plugins/api-key"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/radiobros/emailbridge/internal/audit"
	"github.com/radiobros/emailbridge/internal/auth"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/features"
	"github.com/radiobros/emailbridge/internal/settings"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

const switchAccountBound = "apikeys.accountBound"

// Service creates, lists, patches and revokes API keys and resolves bearer tokens to callers.
type Service struct {
	DB    *store.DB
	Auth  *auth.Service
	Cache Cache
	Audit *audit.Writer
	Log   *slog.Logger

	log *slog.Logger
}

// New builds a Service. Auth must already have the api-key plugin registered.
func New(db *store.DB, a *auth.Service, cache Cache, aud *audit.Writer, log *slog.Logger) *Service {
	if log == nil {
		log = slog.Default()
	}
	return &Service{DB: db, Auth: a, Cache: cache, Audit: aud, Log: log, log: log}
}

func (s *Service) keys() apikey.API { return apikey.Use(s.Auth.Limen()) }

func (s *Service) limenUser(principalID string) *limen.User {
	return &limen.User{ID: principalID}
}

// Create mints a Limen key and stores EmailBridge metadata. The plaintext token is returned once.
func (s *Service) Create(ctx context.Context, in CreateInput) (Key, string, error) {
	if err := tenancy.Require(ctx, tenancy.PermAPIKeysWrite); err != nil {
		return Key{}, "", err
	}
	c, _ := tenancy.CallerFrom(ctx)
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return Key{}, "", errs.New(errs.CodeValidationFailed, "name is required.")
	}
	if err := validateScopes(in.Scopes); err != nil {
		return Key{}, "", err
	}
	if err := s.validateScopeResources(ctx, in.Scopes); err != nil {
		return Key{}, "", err
	}
	if err := validateRestrictions(in.Restrictions); err != nil {
		return Key{}, "", err
	}
	if c.Kind == tenancy.CallerAPIKey {
		if err := scopesSubset(c.Scopes, in.Scopes); err != nil {
			return Key{}, "", err
		}
	}
	if err := s.validateAccountID(ctx, strings.TrimSpace(in.AccountID)); err != nil {
		return Key{}, "", err
	}
	if in.ExpiresAt != nil && !in.ExpiresAt.After(time.Now().UTC()) {
		return Key{}, "", errs.New(errs.CodeValidationFailed, "expiresAt must be in the future.")
	}

	principal, err := s.createPrincipal(ctx, c)
	if err != nil {
		return Key{}, "", err
	}
	req := &apikey.ApiKeyCreateRequest{ProfileID: profileID, Name: name}
	if in.ExpiresAt != nil {
		sec := int64(time.Until(*in.ExpiresAt).Seconds())
		if sec < 1 {
			sec = 1
		}
		req.ExpiresIn = &sec
	}
	result, err := s.keys().Create(ctx, s.limenUser(principal), req)
	if err != nil {
		return Key{}, "", mapLimenErr(err, "create")
	}
	token := result.Key
	prefix := auth.APIKeyPrefix
	if result.Prefix != nil && *result.Prefix != "" {
		prefix = *result.Prefix
	}
	now := time.Now().UTC()
	key := Key{
		ID:             store.NewID(store.PrefixAPIKey),
		OrganizationID: c.OrganizationID,
		Name:           name,
		LimenKeyID:     fmt.Sprint(result.ID),
		LimenKeyHash:   result.KeyHash,
		LimenPrincipal: fmt.Sprint(result.PrincipalID),
		Prefix:         prefix,
		Last4:          result.Last4,
		Scopes:         append([]string(nil), in.Scopes...),
		AccountID:      strings.TrimSpace(in.AccountID),
		Restrictions:   cloneRestrictions(in.Restrictions),
		RateLimit:      cloneRateLimit(in.RateLimit),
		ExpiresAt:      cloneTime(in.ExpiresAt),
		CreatedBy:      c.Actor(),
		CreatedAt:      now,
		UpdatedAt:      now,
		Version:        1,
	}
	if _, err := s.DB.Collection(Collection).InsertOne(ctx, key); err != nil {
		_ = s.keys().Revoke(ctx, s.limenUser(principal), result.ID)
		s.purgeVerifiedCache(ctx, result.KeyHash)
		return Key{}, "", fmt.Errorf("apikeys: insert: %w", err)
	}
	s.audit(ctx, "apikey.create", key.ID, audit.Changes{
		"name":   {After: key.Name},
		"scopes": {After: key.Scopes},
	})
	return key, token, nil
}

// validateAccountID ensures a non-empty accountId exists in the caller's organisation and that
// apikeys.accountBound is on. Empty id is a no-op (unbound key).
func (s *Service) validateAccountID(ctx context.Context, accountID string) error {
	if accountID == "" {
		return nil
	}
	if !store.IsID(store.PrefixAccount, accountID) {
		return errs.New(errs.CodeValidationFailed, "accountId must be a valid account id.").WithSafe("accountId", accountID)
	}
	if err := s.requireAccountBound(ctx); err != nil {
		return err
	}
	filter, err := store.Scoped(ctx, bson.M{"_id": accountID})
	if err != nil {
		return err
	}
	err = s.DB.Collection("accounts").FindOne(ctx, filter).Err()
	if errors.Is(err, mongo.ErrNoDocuments) {
		return errs.New(errs.CodeNotFound, "Account not found.").WithSafe("accountId", accountID)
	}
	if err != nil {
		return fmt.Errorf("apikeys: accountId: %w", err)
	}
	return nil
}

func (s *Service) requireAccountBound(ctx context.Context) error {
	sw, ok := features.DefaultRegistry().Get(switchAccountBound)
	if !ok {
		return nil
	}
	org, ok := store.OrganizationFrom(ctx)
	if !ok || org == "" {
		return store.ErrNoTenant
	}
	vals := features.Values{}
	var doc settings.Doc
	_ = s.DB.Collection(settings.SettingsCollection).FindOne(ctx, bson.M{"_id": settings.OrgDocID(org)}).Decode(&doc)
	if doc.Switches != nil {
		if sv, ok := doc.Switches[switchAccountBound]; ok {
			if b, ok := sv.Value.(bool); ok {
				vals.Organization = &features.Setting{Value: features.BoolValue(b), Locked: sv.Locked}
			}
		}
	}
	v, _ := features.Resolve(sw, "production", vals)
	if v.Bool != nil && !*v.Bool {
		return errs.New(errs.CodeFeatureDisabled, "apikeys.accountBound is disabled for this organisation.")
	}
	return nil
}

func (s *Service) createPrincipal(ctx context.Context, c tenancy.Caller) (string, error) {
	switch c.Kind {
	case tenancy.CallerUser:
		if c.AuthUserID == "" {
			return "", errs.New(errs.CodeInternalError, "Missing auth user for API key creation.")
		}
		return c.AuthUserID, nil
	case tenancy.CallerAPIKey:
		cur, err := s.Get(ctx, c.APIKeyID)
		if err != nil {
			return "", err
		}
		if cur.LimenPrincipal == "" {
			return "", errs.New(errs.CodeInternalError, "API key is missing its Limen principal.")
		}
		return cur.LimenPrincipal, nil
	default:
		return "", errs.New(errs.CodeForbidden, "This caller cannot create API keys.")
	}
}

// Get returns one key in the caller's organisation.
func (s *Service) Get(ctx context.Context, id string) (Key, error) {
	if err := tenancy.RequireRead(ctx, "apikeys"); err != nil {
		return Key{}, err
	}
	if !store.IsID(store.PrefixAPIKey, id) {
		return Key{}, errs.New(errs.CodeNotFound, "API key not found.").WithSafe("keyId", id)
	}
	filter, err := store.Scoped(ctx, bson.M{"_id": id})
	if err != nil {
		return Key{}, err
	}
	var k Key
	err = s.DB.Collection(Collection).FindOne(ctx, filter).Decode(&k)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return Key{}, errs.New(errs.CodeNotFound, "API key not found.").WithSafe("keyId", id)
	}
	if err != nil {
		return Key{}, fmt.Errorf("apikeys: get: %w", err)
	}
	return k, nil
}

// Patch updates name, scopes, restrictions, account binding and expiry.
func (s *Service) Patch(ctx context.Context, id string, in PatchInput, ifMatch string) (Key, error) {
	if err := tenancy.Require(ctx, tenancy.PermAPIKeysWrite); err != nil {
		return Key{}, err
	}
	cur, err := s.Get(ctx, id)
	if err != nil {
		return Key{}, err
	}
	if cur.RevokedAt != nil {
		return Key{}, errs.New(errs.CodeConflict, "This API key has been revoked and cannot be changed.")
	}
	if ifMatch != "" && parseETag(ifMatch) != cur.Version {
		return Key{}, errs.New(errs.CodeVersionMismatch, "The resource version does not match If-Match.")
	}
	c, _ := tenancy.CallerFrom(ctx)
	changes := audit.Changes{}
	set := bson.M{}
	if in.Name != nil {
		name := strings.TrimSpace(*in.Name)
		if name == "" {
			return Key{}, errs.New(errs.CodeValidationFailed, "name must not be empty.")
		}
		changes["name"] = audit.FieldChange{Before: cur.Name, After: name}
		cur.Name, set["name"] = name, name
	}
	if in.Scopes != nil {
		if err := validateScopes(*in.Scopes); err != nil {
			return Key{}, err
		}
		if err := s.validateScopeResources(ctx, *in.Scopes); err != nil {
			return Key{}, err
		}
		if c.Kind == tenancy.CallerAPIKey {
			if err := scopesSubset(c.Scopes, *in.Scopes); err != nil {
				return Key{}, err
			}
		}
		scopes := append([]string(nil), (*in.Scopes)...)
		changes["scopes"] = audit.FieldChange{Before: cur.Scopes, After: scopes}
		cur.Scopes, set["scopes"] = scopes, scopes
	}
	if in.AccountID != nil && in.AccountID.Present {
		if in.AccountID.Null {
			changes["accountId"] = audit.FieldChange{Before: cur.AccountID, After: nil}
			cur.AccountID = ""
			set["accountId"] = ""
		} else {
			acc := strings.TrimSpace(in.AccountID.Value)
			if err := s.validateAccountID(ctx, acc); err != nil {
				return Key{}, err
			}
			changes["accountId"] = audit.FieldChange{Before: cur.AccountID, After: acc}
			cur.AccountID, set["accountId"] = acc, acc
		}
	}
	if in.Restrictions != nil {
		if err := validateRestrictions(in.Restrictions); err != nil {
			return Key{}, err
		}
		rest := cloneRestrictions(in.Restrictions)
		changes["restrictions"] = audit.FieldChange{Before: cur.Restrictions, After: rest}
		cur.Restrictions, set["restrictions"] = rest, rest
	}
	if in.RateLimit != nil {
		rl := cloneRateLimit(in.RateLimit)
		changes["rateLimit"] = audit.FieldChange{Before: cur.RateLimit, After: rl}
		cur.RateLimit, set["rateLimit"] = rl, rl
	}
	var newExpiry *time.Time
	clearExpiry := false
	if in.ExpiresAt != nil && in.ExpiresAt.Present {
		if in.ExpiresAt.Null {
			changes["expiresAt"] = audit.FieldChange{Before: cur.ExpiresAt, After: nil}
			clearExpiry = true
			cur.ExpiresAt = nil
		} else {
			if !in.ExpiresAt.Value.After(time.Now().UTC()) {
				return Key{}, errs.New(errs.CodeValidationFailed, "expiresAt must be in the future.")
			}
			t := in.ExpiresAt.Value
			changes["expiresAt"] = audit.FieldChange{Before: cur.ExpiresAt, After: t}
			cur.ExpiresAt, newExpiry = &t, &t
		}
	}
	if len(set) == 0 && newExpiry == nil && !clearExpiry {
		return cur, nil
	}
	now := time.Now().UTC()
	cur.UpdatedAt, cur.Version = now, cur.Version+1
	set["updatedAt"] = now
	set["version"] = cur.Version
	filter, err := store.Scoped(ctx, bson.M{"_id": id, "version": cur.Version - 1})
	if err != nil {
		return Key{}, err
	}
	update := bson.M{"$set": set}
	if clearExpiry {
		update["$unset"] = bson.M{"expiresAt": ""}
	} else if newExpiry != nil {
		set["expiresAt"] = *newExpiry
	}
	if in.AccountID != nil && in.AccountID.Present && in.AccountID.Null {
		if unset, ok := update["$unset"].(bson.M); ok {
			unset["accountId"] = ""
		} else {
			update["$unset"] = bson.M{"accountId": ""}
		}
		delete(set, "accountId")
	}
	res, err := s.DB.Collection(Collection).UpdateOne(ctx, filter, update)
	if err != nil {
		return Key{}, fmt.Errorf("apikeys: patch: %w", err)
	}
	if res.MatchedCount == 0 {
		return Key{}, errs.New(errs.CodeVersionMismatch, "The resource version does not match If-Match.")
	}
	if newExpiry != nil || clearExpiry || in.Name != nil {
		if err := s.syncLimenMeta(ctx, cur, newExpiry, clearExpiry); err != nil {
			s.log.WarnContext(ctx, "limen api key metadata sync failed", "err", err, "keyId", id)
		}
	}
	// Purge on every successful patch so scope / restriction / binding changes take effect immediately.
	s.purgeVerifiedCache(ctx, cur.LimenKeyHash)
	s.audit(ctx, "apikey.update", id, changes)
	return cur, nil
}

func (s *Service) syncLimenMeta(ctx context.Context, k Key, expiry *time.Time, clear bool) error {
	set := bson.M{}
	if k.Name != "" {
		set["name"] = k.Name
	}
	update := bson.M{}
	if clear {
		update["$unset"] = bson.M{"expires_at": ""}
	} else if expiry != nil {
		set["expires_at"] = *expiry
	}
	if len(set) > 0 {
		update["$set"] = set
	}
	if len(update) == 0 {
		return nil
	}
	_, err := s.DB.Collection(limenAPIKeys).UpdateOne(ctx, bson.M{"_id": k.LimenKeyID}, update)
	return err
}

// Revoke deletes the Limen key, marks our metadata revoked and purges the verified-key cache.
func (s *Service) Revoke(ctx context.Context, id string, ifMatch string) error {
	if err := tenancy.Require(ctx, tenancy.PermAPIKeysWrite); err != nil {
		return err
	}
	cur, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	if cur.RevokedAt != nil {
		return nil // idempotent
	}
	if ifMatch != "" && parseETag(ifMatch) != cur.Version {
		return errs.New(errs.CodeVersionMismatch, "The resource version does not match If-Match.")
	}
	principal := cur.LimenPrincipal
	if principal == "" {
		principal = cur.CreatedBy
	}
	if cur.LimenKeyID != "" {
		if err := s.keys().Revoke(ctx, s.limenUser(principal), cur.LimenKeyID); err != nil {
			var le *limen.LimenError
			notFound := errors.Is(err, limen.ErrRecordNotFound) || (errors.As(err, &le) && le.Status() == 404)
			if !notFound {
				return mapLimenErr(err, "revoke")
			}
		}
	}
	s.purgeVerifiedCache(ctx, cur.LimenKeyHash)
	now := time.Now().UTC()
	filter, err := store.Scoped(ctx, bson.M{"_id": id})
	if err != nil {
		return err
	}
	_, err = s.DB.Collection(Collection).UpdateOne(ctx, filter, bson.M{
		"$set": bson.M{"revokedAt": now, "updatedAt": now, "version": cur.Version + 1},
	})
	if err != nil {
		return fmt.Errorf("apikeys: revoke: %w", err)
	}
	s.audit(ctx, "apikey.revoke", id, nil)
	return nil
}

func (s *Service) audit(ctx context.Context, action, id string, changes audit.Changes) {
	if s.Audit == nil {
		return
	}
	if err := s.Audit.Record(ctx, action, audit.Target{Type: "apiKey", ID: id}, changes); err != nil {
		s.log.WarnContext(ctx, "audit record failed", "action", action, "err", err)
	}
}

func mapLimenErr(err error, op string) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, apikey.ErrInvalidAPIKey):
		return errs.New(errs.CodeAPIKeyInvalid, "The API key is invalid.")
	case errors.Is(err, apikey.ErrAPIKeyDisabled):
		return errs.New(errs.CodeAPIKeyRevoked, "The API key has been revoked.")
	case errors.Is(err, apikey.ErrAPIKeyExpired):
		return errs.New(errs.CodeAPIKeyExpired, "The API key has expired.")
	case errors.Is(err, apikey.ErrInsufficientPermissions):
		return errs.New(errs.CodeScopeMissing, "The API key lacks the required scope.")
	case errors.Is(err, limen.ErrForbidden):
		return errs.New(errs.CodeForbidden, "You cannot manage this API key.")
	case errors.Is(err, limen.ErrRecordNotFound):
		return errs.New(errs.CodeNotFound, "API key not found.")
	}
	var le *limen.LimenError
	if errors.As(err, &le) {
		switch le.Status() {
		case 401:
			return errs.New(errs.CodeAPIKeyInvalid, "The API key is invalid.")
		case 403:
			return errs.New(errs.CodeForbidden, le.Error())
		case 404:
			return errs.New(errs.CodeNotFound, "API key not found.")
		}
	}
	return errs.Wrap(err, errs.CodeInternalError, "apikeys."+op)
}

func cloneRestrictions(r *Restrictions) *Restrictions {
	if r == nil {
		return nil
	}
	out := &Restrictions{
		IPs:       append([]string(nil), r.IPs...),
		Referrers: append([]string(nil), r.Referrers...),
	}
	return out
}

func cloneRateLimit(r *RateLimit) *RateLimit {
	if r == nil {
		return nil
	}
	out := *r
	return &out
}

func cloneTime(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}

func parseETag(s string) int64 {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, `"`)
	s = strings.TrimPrefix(s, "v")
	var v int64
	_, _ = fmt.Sscan(s, &v)
	return v
}

func formatETag(v int64) string { return fmt.Sprintf(`"v%d"`, v) }

// EnsureIndexes creates the S1 indexes for apiKeys.
func EnsureIndexes(ctx context.Context, db *store.DB) error {
	return db.EnsureIndexes(ctx, Collection,
		mongo.IndexModel{Keys: bson.D{{Key: "limenKeyId", Value: 1}}, Options: options.Index().SetUnique(true)},
		mongo.IndexModel{Keys: bson.D{{Key: "organizationId", Value: 1}, {Key: "_id", Value: 1}}},
	)
}
