package authserver

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/radiobros/emailbridge/internal/accounts"
	"github.com/radiobros/emailbridge/internal/crypto"
	"github.com/radiobros/emailbridge/internal/egress"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/features"
	"github.com/radiobros/emailbridge/internal/settings"
	"github.com/radiobros/emailbridge/internal/store"
)

// Credentials is the connect-time credential seam (W54).
type Credentials interface {
	Resolve(ctx context.Context, accountID, protocol string) (user, pass, oauthToken string, err error)
}

// Resolver implements Credentials against the organisation credential server.
type Resolver struct {
	DB      *store.DB
	Cipher  *crypto.Cipher
	Egress  *egress.Dialer
	HTTP    *http.Client // tests
	Log     *slog.Logger
	cache   *memCache
	breaker *breaker
	now     func() time.Time
}

// NewResolver constructs a Resolver.
func NewResolver(db *store.DB, cipher *crypto.Cipher, dial *egress.Dialer, log *slog.Logger) *Resolver {
	return &Resolver{
		DB: db, Cipher: cipher, Egress: dial, Log: log,
		cache: newMemCache(), breaker: newBreaker(),
	}
}

func (r *Resolver) clock() time.Time {
	if r.now != nil {
		return r.now()
	}
	return time.Now().UTC()
}

// Resolve fetches credentials for accountID/protocol when accounts.authServer is on.
func (r *Resolver) Resolve(ctx context.Context, accountID, protocol string) (user, pass, oauthToken string, err error) {
	if r == nil || r.DB == nil {
		return "", "", "", errs.New(errs.CodeAuthServerMisconfigured, "Credential server is not configured.")
	}
	protocol = strings.ToLower(strings.TrimSpace(protocol))
	switch protocol {
	case ProtocolIMAP, ProtocolSMTP, ProtocolOAuth:
	default:
		return "", "", "", errs.New(errs.CodeValidationFailed, "protocol must be imap, smtp or oauth.")
	}
	acc, err := r.loadAccount(ctx, accountID)
	if err != nil {
		return "", "", "", err
	}
	if !r.switchOn(ctx, acc) {
		return "", "", "", errs.New(errs.CodeFeatureDisabled, "accounts.authServer is disabled for this account.")
	}
	cfg, err := r.orgConfig(ctx, acc.OrganizationID)
	if err != nil {
		return "", "", "", err
	}
	key := cacheKey(acc.OrganizationID, accountID, protocol)
	if c, ok := r.cache.get(key, r.clock()); ok {
		return c.User, c.Pass, c.AccessToken, nil
	}
	if !r.breaker.allow(r.clock()) {
		return "", "", "", errs.New(errs.CodeAuthServerUnavailable, "Credential server circuit breaker is open.")
	}
	creds, err := r.fetch(ctx, cfg.URL, cfg.Secret, cfg.Timeout, Request{
		AccountID: accountID, ExternalID: acc.ExternalID, Protocol: protocol,
	})
	r.breaker.record(r.clock(), err == nil)
	if err != nil {
		_ = r.markAuthError(ctx, acc, err)
		return "", "", "", err
	}
	ttl := cfg.TTL
	if !creds.ExpiresAt.IsZero() {
		until := time.Until(creds.ExpiresAt)
		if until > 0 && until < ttl {
			ttl = until
		}
	}
	r.cache.put(key, creds, r.clock().Add(ttl))
	return creds.User, creds.Pass, creds.AccessToken, nil
}

type accountDoc struct {
	ID             string         `bson:"_id"`
	OrganizationID string         `bson:"organizationId"`
	ExternalID     *string        `bson:"externalId,omitempty"`
	Switches       map[string]any `bson:"switches,omitempty"`
	UseAuthServer  bool           `bson:"useAuthServer,omitempty"`
}

func (r *Resolver) loadAccount(ctx context.Context, accountID string) (*accountDoc, error) {
	var doc accountDoc
	err := r.DB.Collection(accounts.Collection).FindOne(ctx, bson.M{"_id": accountID}).Decode(&doc)
	if err != nil {
		return nil, errs.New(errs.CodeNotFound, "The account does not exist.")
	}
	return &doc, nil
}

func (r *Resolver) switchOn(ctx context.Context, a *accountDoc) bool {
	if !a.UseAuthServer {
		return false
	}
	reg := features.DefaultRegistry()
	sw, ok := reg.Get(SwitchName)
	if !ok {
		return false
	}
	vals := features.Values{}
	if a.Switches != nil {
		if v, ok := a.Switches[SwitchName].(bool); ok {
			vals.Lower = &features.Setting{Value: features.BoolValue(v)}
		}
	}
	read := func(docID string) *features.Setting {
		var doc settings.Doc
		err := r.DB.Collection(settings.SettingsCollection).FindOne(ctx, bson.M{"_id": docID}).Decode(&doc)
		if err != nil || doc.Switches == nil {
			return nil
		}
		if sv, ok := doc.Switches[SwitchName]; ok {
			if b, ok := sv.Value.(bool); ok {
				return &features.Setting{Value: features.BoolValue(b), Locked: sv.Locked}
			}
		}
		return nil
	}
	if features.Allows(sw, features.ScopeGlobal) {
		vals.Global = read(settings.DocIDGlobal)
	}
	vals.Organization = read(settings.OrgDocID(a.OrganizationID))
	v, _ := features.Resolve(sw, "production", vals)
	return v.Bool != nil && *v.Bool
}

type orgCfg struct {
	URL     string
	Secret  string
	TTL     time.Duration
	Timeout time.Duration
}

func (r *Resolver) orgConfig(ctx context.Context, orgID string) (*orgCfg, error) {
	var doc settings.Doc
	err := r.DB.Collection(settings.SettingsCollection).FindOne(ctx, bson.M{"_id": settings.OrgDocID(orgID)}).Decode(&doc)
	if err != nil {
		return nil, errs.New(errs.CodeAuthServerMisconfigured, "Organisation credential server settings are missing.")
	}
	url, _ := doc.Values["authServer.url"].(string)
	url = strings.TrimSpace(url)
	if url == "" {
		return nil, errs.New(errs.CodeAuthServerMisconfigured, "authServer.url is not set.")
	}
	if !strings.HasPrefix(strings.ToLower(url), "https://") && !strings.HasPrefix(strings.ToLower(url), "http://") {
		return nil, errs.New(errs.CodeAuthServerMisconfigured, "authServer.url must be an absolute HTTP(S) URL.")
	}
	secret, err := r.decryptSecret(ctx, orgID, doc.ID, doc.Values["authServer.hmacSecret"])
	if err != nil {
		return nil, err
	}
	if secret == "" {
		return nil, errs.New(errs.CodeAuthServerMisconfigured, "authServer.hmacSecret is not set.")
	}
	ttl := defaultTTL
	if s, ok := doc.Values["authServer.ttl"].(string); ok && s != "" {
		if d, err := time.ParseDuration(s); err == nil && d > 0 {
			ttl = d
		}
	}
	if ttl > maxTTL {
		ttl = maxTTL
	}
	timeout := defaultTimeout
	if s, ok := doc.Values["authServer.timeout"].(string); ok && s != "" {
		if d, err := time.ParseDuration(s); err == nil && d > 0 {
			timeout = d
		}
	}
	if timeout > maxTimeout {
		timeout = maxTimeout
	}
	return &orgCfg{URL: url, Secret: secret, TTL: ttl, Timeout: timeout}, nil
}

func (r *Resolver) decryptSecret(ctx context.Context, orgID, docID string, raw any) (string, error) {
	if raw == nil {
		return "", nil
	}
	if s, ok := raw.(string); ok {
		return s, nil
	}
	// Prefer round-trip through BSON so EncryptedString / Binary decode cleanly.
	b, err := bson.Marshal(bson.M{"v": raw})
	if err == nil {
		var wrap struct {
			V struct {
				Secret bool                   `bson:"_secret"`
				Last4  string                 `bson:"last4"`
				ES     crypto.EncryptedString `bson:",inline"`
			} `bson:"v"`
		}
		if uerr := bson.Unmarshal(b, &wrap); uerr == nil && wrap.V.Secret {
			if r.Cipher == nil {
				return "", errs.New(errs.CodeAuthServerMisconfigured, "Cipher is not configured.")
			}
			plain, derr := r.Cipher.DecryptString(ctx, wrap.V.ES, crypto.AAD{
				Collection: settings.SettingsCollection, Field: "authServer.hmacSecret", DocID: docID,
			})
			if derr != nil {
				return "", errs.Wrap(derr, errs.CodeAuthServerMisconfigured, "authServer.hmacSecret")
			}
			_ = orgID
			return plain, nil
		}
	}
	m, ok := raw.(bson.M)
	if !ok {
		if mm, ok2 := raw.(map[string]any); ok2 {
			m = bson.M(mm)
		} else {
			return "", errs.New(errs.CodeAuthServerMisconfigured, "authServer.hmacSecret is invalid.")
		}
	}
	if secret, _ := m["_secret"].(bool); !secret {
		if s, ok := m["value"].(string); ok {
			return s, nil
		}
		return "", nil
	}
	es := crypto.EncryptedString{}
	if v, ok := m["kid"].(string); ok {
		es.KID = v
	}
	if v, ok := m["dek"].(string); ok {
		es.DEK = v
	}
	es.Nonce = asBytes(m["nonce"])
	es.CT = asBytes(m["ct"])
	if r.Cipher == nil {
		return "", errs.New(errs.CodeAuthServerMisconfigured, "Cipher is not configured.")
	}
	plain, derr := r.Cipher.DecryptString(ctx, es, crypto.AAD{
		Collection: settings.SettingsCollection, Field: "authServer.hmacSecret", DocID: docID,
	})
	if derr != nil {
		return "", errs.Wrap(derr, errs.CodeAuthServerMisconfigured, "authServer.hmacSecret")
	}
	_ = orgID
	return plain, nil
}

func asBytes(v any) []byte {
	switch t := v.(type) {
	case []byte:
		return t
	case string:
		return []byte(t)
	default:
		// primitive.Binary and similar
		type binary interface{ Data() []byte }
		if b, ok := v.(interface{ Bytes() []byte }); ok {
			return b.Bytes()
		}
		_ = binary(nil)
		return nil
	}
}

func (r *Resolver) markAuthError(ctx context.Context, a *accountDoc, cause error) error {
	now := r.clock().Truncate(time.Millisecond)
	code := string(errs.CodeOf(cause))
	if code == "" {
		code = string(errs.CodeAuthServerFailed)
	}
	_, err := r.DB.Collection(accounts.Collection).UpdateOne(ctx,
		bson.M{"_id": a.ID, "organizationId": a.OrganizationID},
		bson.M{"$set": bson.M{
			"state": accounts.StateAuthError,
			"stateReason": accounts.StateReason{
				Kind: string(errs.KindAuth), Code: code,
				Message: "Credential server failed", At: now,
			},
			"authFailingSince": now,
			"updatedAt":        now,
		}},
	)
	return err
}

// CacheLen exposes cache size for tests.
func (r *Resolver) CacheLen() int {
	if r == nil || r.cache == nil {
		return 0
	}
	return r.cache.Len()
}
