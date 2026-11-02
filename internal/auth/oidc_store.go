package auth

import (
	"context"
	"errors"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/radiobros/emailbridge/internal/crypto"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/store"
)

func (s *Service) oidcColl() *mongo.Collection {
	if s.mongo == nil {
		return nil
	}
	return s.mongo.Collection(OIDCProvidersCollection)
}

func (s *Service) listOIDCProviders(ctx context.Context) ([]OIDCProvider, error) {
	cur, err := s.oidcColl().Find(ctx, bson.M{}, options.Find().SetSort(bson.D{{Key: "buttonLabel", Value: 1}}))
	if err != nil {
		return nil, err
	}
	defer func() { _ = cur.Close(ctx) }()
	var out []OIDCProvider
	if err := cur.All(ctx, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = []OIDCProvider{}
	}
	return out, nil
}

func (s *Service) listPublicOIDCProviders(ctx context.Context) ([]OIDCPublicProvider, error) {
	cur, err := s.oidcColl().Find(ctx, bson.M{"enabled": true}, options.Find().SetSort(bson.D{{Key: "buttonLabel", Value: 1}}).
		SetProjection(bson.M{"_id": 1, "buttonLabel": 1}))
	if err != nil {
		return nil, err
	}
	defer func() { _ = cur.Close(ctx) }()
	var rows []OIDCProvider
	if err := cur.All(ctx, &rows); err != nil {
		return nil, err
	}
	out := make([]OIDCPublicProvider, 0, len(rows))
	for _, r := range rows {
		out = append(out, OIDCPublicProvider{ID: r.ID, ButtonLabel: r.ButtonLabel})
	}
	return out, nil
}

func (s *Service) getOIDCProvider(ctx context.Context, id string) (*OIDCProvider, error) {
	var p OIDCProvider
	err := s.oidcColl().FindOne(ctx, bson.M{"_id": id}).Decode(&p)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, errs.New(errs.CodeNotFound, "No such OIDC provider.")
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func (s *Service) decryptOIDCSecret(ctx context.Context, p *OIDCProvider) (string, error) {
	if s.Cipher == nil {
		return "", errors.New("auth: cipher required for OIDC secrets")
	}
	return s.Cipher.DecryptString(ctx, p.ClientSecret, crypto.AAD{
		Collection: OIDCProvidersCollection, Field: "clientSecret", DocID: p.ID,
	})
}

func (s *Service) encryptOIDCSecret(ctx context.Context, id, plain string) (crypto.EncryptedString, error) {
	if s.Cipher == nil {
		return crypto.EncryptedString{}, errors.New("auth: cipher required for OIDC secrets")
	}
	return s.Cipher.EncryptString(ctx, "", plain, crypto.AAD{
		Collection: OIDCProvidersCollection, Field: "clientSecret", DocID: id,
	})
}

type oidcCreateInput struct {
	IssuerURL           string
	ClientID            string
	ClientSecret        string
	Scopes              []string
	ButtonLabel         string
	AllowedEmailDomains []string
	GroupClaim          string
	GroupRoles          map[string]string
	AutoProvision       bool
	Enabled             *bool
}

func (s *Service) createOIDCProvider(ctx context.Context, in oidcCreateInput) (*OIDCProvider, error) {
	if err := validateOIDCCreate(in); err != nil {
		return nil, err
	}
	roles, err := normalizeGroupRoles(in.GroupRoles)
	if err != nil {
		return nil, err
	}
	id := store.NewID(store.PrefixOIDC)
	enc, err := s.encryptOIDCSecret(ctx, id, in.ClientSecret)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	p := OIDCProvider{
		ID: id, IssuerURL: stringsTrimSlash(in.IssuerURL), ClientID: in.ClientID,
		ClientSecret: enc, Scopes: defaultOIDCScopes(in.Scopes), ButtonLabel: stringsTrim(in.ButtonLabel),
		AllowedEmailDomains: normalizeDomains(in.AllowedEmailDomains), GroupClaim: stringsTrim(in.GroupClaim),
		GroupRoles: roles, AutoProvision: in.AutoProvision, Enabled: enabled,
		CreatedAt: now, UpdatedAt: now,
	}
	if _, err := s.oidcColl().InsertOne(ctx, p); err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return nil, errs.New(errs.CodeConflict, "An OIDC provider with this issuer and client id already exists.")
		}
		return nil, err
	}
	return &p, nil
}

type oidcPatchInput struct {
	IssuerURL           *string
	ClientID            *string
	ClientSecret        *string
	Scopes              *[]string
	ButtonLabel         *string
	AllowedEmailDomains *[]string
	GroupClaim          *string
	GroupRoles          *map[string]string
	AutoProvision       *bool
	Enabled             *bool
}

func (s *Service) patchOIDCProvider(ctx context.Context, id string, in oidcPatchInput) (*OIDCProvider, error) {
	p, err := s.getOIDCProvider(ctx, id)
	if err != nil {
		return nil, err
	}
	set := bson.M{"updatedAt": time.Now().UTC()}
	if in.IssuerURL != nil {
		u := stringsTrimSlash(*in.IssuerURL)
		if u == "" {
			return nil, errs.New(errs.CodeValidationFailed, "issuerUrl is required.")
		}
		set["issuerUrl"] = u
		p.IssuerURL = u
	}
	if in.ClientID != nil {
		if stringsTrim(*in.ClientID) == "" {
			return nil, errs.New(errs.CodeValidationFailed, "clientId is required.")
		}
		set["clientId"] = stringsTrim(*in.ClientID)
		p.ClientID = stringsTrim(*in.ClientID)
	}
	if in.ClientSecret != nil {
		if stringsTrim(*in.ClientSecret) == "" {
			return nil, errs.New(errs.CodeValidationFailed, "clientSecret cannot be empty.")
		}
		enc, err := s.encryptOIDCSecret(ctx, id, *in.ClientSecret)
		if err != nil {
			return nil, err
		}
		set["clientSecret"] = enc
		p.ClientSecret = enc
	}
	if in.Scopes != nil {
		set["scopes"] = defaultOIDCScopes(*in.Scopes)
		p.Scopes = defaultOIDCScopes(*in.Scopes)
	}
	if in.ButtonLabel != nil {
		if stringsTrim(*in.ButtonLabel) == "" {
			return nil, errs.New(errs.CodeValidationFailed, "buttonLabel is required.")
		}
		set["buttonLabel"] = stringsTrim(*in.ButtonLabel)
		p.ButtonLabel = stringsTrim(*in.ButtonLabel)
	}
	if in.AllowedEmailDomains != nil {
		set["allowedEmailDomains"] = normalizeDomains(*in.AllowedEmailDomains)
		p.AllowedEmailDomains = normalizeDomains(*in.AllowedEmailDomains)
	}
	if in.GroupClaim != nil {
		set["groupClaim"] = stringsTrim(*in.GroupClaim)
		p.GroupClaim = stringsTrim(*in.GroupClaim)
	}
	if in.GroupRoles != nil {
		roles, err := normalizeGroupRoles(*in.GroupRoles)
		if err != nil {
			return nil, err
		}
		set["groupRoles"] = roles
		p.GroupRoles = roles
	}
	if in.AutoProvision != nil {
		set["autoProvision"] = *in.AutoProvision
		p.AutoProvision = *in.AutoProvision
	}
	if in.Enabled != nil {
		set["enabled"] = *in.Enabled
		p.Enabled = *in.Enabled
	}
	p.UpdatedAt = set["updatedAt"].(time.Time)
	res, err := s.oidcColl().UpdateOne(ctx, bson.M{"_id": id}, bson.M{"$set": set})
	if err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return nil, errs.New(errs.CodeConflict, "An OIDC provider with this issuer and client id already exists.")
		}
		return nil, err
	}
	if res.MatchedCount == 0 {
		return nil, errs.New(errs.CodeNotFound, "No such OIDC provider.")
	}
	return p, nil
}

func (s *Service) deleteOIDCProvider(ctx context.Context, id string) error {
	res, err := s.oidcColl().DeleteOne(ctx, bson.M{"_id": id})
	if err != nil {
		return err
	}
	if res.DeletedCount == 0 {
		return errs.New(errs.CodeNotFound, "No such OIDC provider.")
	}
	return nil
}

func validateOIDCCreate(in oidcCreateInput) error {
	if stringsTrimSlash(in.IssuerURL) == "" {
		return errs.New(errs.CodeValidationFailed, "issuerUrl is required.")
	}
	if stringsTrim(in.ClientID) == "" {
		return errs.New(errs.CodeValidationFailed, "clientId is required.")
	}
	if stringsTrim(in.ClientSecret) == "" {
		return errs.New(errs.CodeValidationFailed, "clientSecret is required.")
	}
	if stringsTrim(in.ButtonLabel) == "" {
		return errs.New(errs.CodeValidationFailed, "buttonLabel is required.")
	}
	if _, err := normalizeGroupRoles(in.GroupRoles); err != nil {
		return err
	}
	return nil
}

// normalizeGroupRoles validates values as "org_<ulid>:<role>" and drops empty keys.
func normalizeGroupRoles(in map[string]string) (map[string]string, error) {
	if len(in) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(in))
	for g, raw := range in {
		g = strings.TrimSpace(g)
		raw = strings.TrimSpace(raw)
		if g == "" {
			continue
		}
		orgID, roleStr, cut := strings.Cut(raw, ":")
		orgID, roleStr = strings.TrimSpace(orgID), strings.TrimSpace(roleStr)
		if !cut || !store.IsID(store.PrefixOrganization, orgID) {
			return nil, errs.New(errs.CodeValidationFailed, "groupRoles values must be org_<id>:<role>.")
		}
		switch roleStr {
		case "owner", "admin", "operator", "viewer":
		default:
			return nil, errs.New(errs.CodeValidationFailed, "groupRoles roles must be owner, admin, operator or viewer.")
		}
		out[g] = orgID + ":" + roleStr
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

func stringsTrim(s string) string { return strings.TrimSpace(s) }

func stringsTrimSlash(s string) string {
	return strings.TrimRight(strings.TrimSpace(s), "/")
}

func normalizeDomains(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, d := range in {
		d = strings.ToLower(strings.TrimSpace(d))
		if d == "" || seen[d] {
			continue
		}
		seen[d] = true
		out = append(out, d)
	}
	return out
}
