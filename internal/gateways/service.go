package gateways

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/radiobros/emailbridge/internal/audit"
	"github.com/radiobros/emailbridge/internal/crypto"
	"github.com/radiobros/emailbridge/internal/egress"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

// Service is gateway CRUD + verify + dial/API resolve for submit.
type Service struct {
	Repo         *Repo
	Cipher       *crypto.Cipher
	Egress       *egress.Dialer
	Audit        *audit.Writer
	EHLO         string // fallback EHLO (host of EB_PUBLIC_URL)
	CallbackBase string // EB_CALLBACK_URL for webhookUrl in views
	Sink         DeliverySink
	HTTP         *http.Client // optional; tests / overrides egress client for Send + SNS
	limiter      *callbackLimiter
}

// Create inserts a gateway.
func (s *Service) Create(ctx context.Context, in CreateInput) (*Gateway, error) {
	if err := tenancy.Require(ctx, tenancy.PermSend); err != nil {
		return nil, err
	}
	if err := validateCreate(in); err != nil {
		return nil, err
	}
	org, ok := store.OrganizationFrom(ctx)
	if !ok {
		return nil, errs.New(errs.CodeForbidden, "An organisation context is required.")
	}
	kind := normalizeKind(in.Kind)
	if in.API != nil {
		if err := checkBaseURLEgress(ctx, s.Egress, in.API.BaseURL); err != nil {
			return nil, err
		}
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	g := &Gateway{
		ID: store.NewID(store.PrefixGateway), OrganizationID: org, Name: strings.TrimSpace(in.Name),
		Kind: kind, Policy: policyOr(in.Policy), MaxAttempts: in.MaxAttempts, BackoffBase: in.BackoffBase,
		Enabled: enabled, CreatedAt: now, UpdatedAt: now, Version: 1,
	}
	if c, ok := tenancy.CallerFrom(ctx); ok {
		g.CreatedBy = c.UserID
		if c.Kind == tenancy.CallerAPIKey {
			g.CreatedBy = c.APIKeyID
		}
	}
	if kind == KindSMTP {
		g.Host = strings.TrimSpace(in.Host)
		g.Port = in.Port
		g.TLS = in.TLS
		g.Auth = Auth{User: in.Auth.User}
		g.EHLOName = strings.TrimSpace(in.EHLOName)
		g.LocalAddress = strings.TrimSpace(in.LocalAddress)
		g.Proxy = strings.TrimSpace(in.Proxy)
		g.IgnoreCertErrors = in.IgnoreCertErrors
		pass, err := s.Cipher.EncryptString(ctx, org, in.Auth.Pass, crypto.AAD{
			Collection: Collection, Field: "auth.pass", DocID: g.ID,
		})
		if err != nil {
			return nil, errs.Wrap(err, errs.CodeInternalError, "gateways.encrypt")
		}
		g.Auth.Pass = pass
	} else {
		if in.API != nil {
			cp := *in.API
			g.API = &cp
		} else {
			g.API = &APIConfig{}
		}
		creds, err := s.encryptCreds(ctx, org, g.ID, in.Credentials, nil)
		if err != nil {
			return nil, err
		}
		g.APICreds = creds
	}
	if err := s.Repo.insert(ctx, g); err != nil {
		return nil, err
	}
	s.audit(ctx, "smtpGateway.created", g.ID, audit.Changes{
		"name": {After: g.Name}, "kind": {After: kind},
	})
	return g, nil
}

// Get returns one gateway.
func (s *Service) Get(ctx context.Context, id string) (*Gateway, error) {
	if err := tenancy.RequireRead(ctx, "gateways"); err != nil {
		return nil, err
	}
	if !store.IsID(store.PrefixGateway, id) {
		return nil, errs.New(errs.CodeNotFound, "The SMTP gateway does not exist.")
	}
	return s.Repo.get(ctx, id)
}

// List pages gateways.
func (s *Service) List(ctx context.Context, f ListFilter) (*List, error) {
	if err := tenancy.RequireRead(ctx, "gateways"); err != nil {
		return nil, err
	}
	out, err := s.Repo.list(ctx, f)
	if err != nil {
		return nil, err
	}
	if s.CallbackBase != "" {
		for i := range out.Data {
			if out.Data[i].Kind != KindSMTP && out.Data[i].Kind != "" {
				out.Data[i].WebhookURL = webhookURL(s.CallbackBase, out.Data[i].ID)
			}
		}
	}
	return out, nil
}

// Patch updates a gateway.
func (s *Service) Patch(ctx context.Context, id, ifMatch string, in PatchInput) (*Gateway, error) {
	if err := tenancy.Require(ctx, tenancy.PermSend); err != nil {
		return nil, err
	}
	g, err := s.Repo.get(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := checkIfMatch(ifMatch, g.Version); err != nil {
		return nil, err
	}
	old := *g
	kind := g.EffectiveKind()
	if in.Kind != nil {
		want := normalizeKind(*in.Kind)
		if want != kind {
			return nil, errs.New(errs.CodeGatewayKindImmutable, "Gateway kind cannot be changed after create.")
		}
	}
	if kind == KindSMTP {
		if err := refuseAPIFieldsOnSMTP(in); err != nil {
			return nil, err
		}
		if err := s.patchSMTP(ctx, g, in); err != nil {
			return nil, err
		}
		if err := validateSMTPGateway(g); err != nil {
			return nil, err
		}
	} else {
		if err := refuseSMTPFieldsOnAPI(in); err != nil {
			return nil, err
		}
		if err := s.patchAPI(ctx, g, kind, in); err != nil {
			return nil, err
		}
		if err := validateAPIConfig(kind, g.API); err != nil {
			return nil, err
		}
		if err := validatePolicyFields(g.Policy, g.MaxAttempts, g.BackoffBase); err != nil {
			return nil, err
		}
	}
	g.UpdatedAt = time.Now().UTC().Truncate(time.Millisecond)
	g.Version++
	if err := s.Repo.replace(ctx, g, old.Version); err != nil {
		return nil, err
	}
	s.audit(ctx, "smtpGateway.updated", g.ID, audit.Changes{
		"name": {Before: old.Name, After: g.Name},
	})
	return g, nil
}

func (s *Service) patchSMTP(ctx context.Context, g *Gateway, in PatchInput) error {
	if in.Name != nil {
		g.Name = strings.TrimSpace(*in.Name)
	}
	if in.Host != nil {
		g.Host = strings.TrimSpace(*in.Host)
	}
	if in.Port != nil {
		g.Port = *in.Port
	}
	if in.TLS != nil {
		g.TLS = *in.TLS
	}
	if in.EHLOName != nil {
		g.EHLOName = strings.TrimSpace(*in.EHLOName)
	}
	if in.LocalAddress != nil {
		g.LocalAddress = strings.TrimSpace(*in.LocalAddress)
	}
	if in.Proxy != nil {
		g.Proxy = strings.TrimSpace(*in.Proxy)
	}
	if in.Policy != nil {
		g.Policy = policyOr(*in.Policy)
	}
	if in.ClearMaxAttempts {
		g.MaxAttempts = nil
	} else if in.MaxAttempts != nil {
		g.MaxAttempts = in.MaxAttempts
	}
	if in.BackoffBase != nil {
		g.BackoffBase = *in.BackoffBase
	}
	if in.IgnoreCertErrors != nil {
		g.IgnoreCertErrors = *in.IgnoreCertErrors
	}
	if in.Enabled != nil {
		g.Enabled = *in.Enabled
	}
	if in.Auth != nil {
		if in.Auth.User != "" {
			g.Auth.User = in.Auth.User
		}
		if in.Auth.Pass != "" {
			pass, err := s.Cipher.EncryptString(ctx, g.OrganizationID, in.Auth.Pass, crypto.AAD{
				Collection: Collection, Field: "auth.pass", DocID: g.ID,
			})
			if err != nil {
				return errs.Wrap(err, errs.CodeInternalError, "gateways.encrypt")
			}
			g.Auth.Pass = pass
		}
	}
	return nil
}

func (s *Service) patchAPI(ctx context.Context, g *Gateway, kind string, in PatchInput) error {
	if in.Name != nil {
		g.Name = strings.TrimSpace(*in.Name)
	}
	if in.Policy != nil {
		g.Policy = policyOr(*in.Policy)
	}
	if in.ClearMaxAttempts {
		g.MaxAttempts = nil
	} else if in.MaxAttempts != nil {
		g.MaxAttempts = in.MaxAttempts
	}
	if in.BackoffBase != nil {
		g.BackoffBase = *in.BackoffBase
	}
	if in.Enabled != nil {
		g.Enabled = *in.Enabled
	}
	if in.API != nil {
		if err := checkBaseURLEgress(ctx, s.Egress, in.API.BaseURL); err != nil {
			return err
		}
		cp := *in.API
		g.API = &cp
	}
	if in.Credentials != nil {
		creds, err := s.encryptCreds(ctx, g.OrganizationID, g.ID, in.Credentials, g.APICreds)
		if err != nil {
			return err
		}
		g.APICreds = creds
	}
	_ = kind
	return nil
}

func (s *Service) encryptCreds(ctx context.Context, org, docID string, in *CredentialsInput, prev *APICreds) (*APICreds, error) {
	out := &APICreds{}
	if prev != nil {
		*out = *prev
	}
	if in == nil {
		return out, nil
	}
	set := func(field, value string, dest *crypto.EncryptedString) error {
		if value == "" {
			return nil
		}
		enc, err := s.Cipher.EncryptString(ctx, org, value, crypto.AAD{
			Collection: Collection, Field: "apiCreds." + field, DocID: docID,
		})
		if err != nil {
			return errs.Wrap(err, errs.CodeInternalError, "gateways.encrypt")
		}
		*dest = enc
		return nil
	}
	if err := set("accessKey", in.AccessKey, &out.AccessKey); err != nil {
		return nil, err
	}
	if err := set("secretKey", in.SecretKey, &out.SecretKey); err != nil {
		return nil, err
	}
	if err := set("apiKey", in.APIKey, &out.APIKey); err != nil {
		return nil, err
	}
	if err := set("webhookSecret", in.WebhookSecret, &out.WebhookSecret); err != nil {
		return nil, err
	}
	return out, nil
}

// Delete removes a gateway.
func (s *Service) Delete(ctx context.Context, id, ifMatch string) error {
	if err := tenancy.Require(ctx, tenancy.PermSend); err != nil {
		return err
	}
	g, err := s.Repo.get(ctx, id)
	if err != nil {
		return err
	}
	if err := checkIfMatch(ifMatch, g.Version); err != nil {
		return err
	}
	if err := s.refuseDeleteIfInUse(ctx, g.OrganizationID, id); err != nil {
		return err
	}
	if err := s.Repo.delete(ctx, g); err != nil {
		return err
	}
	s.audit(ctx, "smtpGateway.deleted", id, audit.Changes{"name": {Before: g.Name}})
	return nil
}

// refuseDeleteIfInUse blocks deleting a gateway still referenced by accounts.smtp.gatewayId.
func (s *Service) refuseDeleteIfInUse(ctx context.Context, orgID, gatewayID string) error {
	if s.Repo == nil || s.Repo.DB == nil {
		return nil
	}
	n, err := s.Repo.DB.Collection("accounts").CountDocuments(ctx, bson.M{
		"organizationId": orgID, "smtp.gatewayId": gatewayID,
	})
	if err != nil {
		return errs.Wrap(err, errs.CodeInternalError, "gateways.inuse")
	}
	if n > 0 {
		return errs.New(errs.CodeSMTPGatewayInUse,
			"Accounts still reference this SMTP gateway; clear smtp.gatewayId first.").
			WithSafe("count", n)
	}
	return nil
}

// DialSettings decrypts an SMTP gateway for the submit worker (no tenancy; org must match).
// Prefer ResolveSMTP, which refuses API gateways with ErrAPIGateway.
func (s *Service) DialSettings(ctx context.Context, orgID, id string) (*DialSettings, error) {
	return s.ResolveSMTP(ctx, orgID, id)
}

func (s *Service) audit(ctx context.Context, action, id string, changes audit.Changes) {
	if s.Audit == nil {
		return
	}
	_ = s.Audit.Record(ctx, action, audit.Target{Type: "smtpGateway", ID: id}, changes)
}

func policyOr(p string) string {
	switch p {
	case PolicyAtMostOnce:
		return PolicyAtMostOnce
	default:
		return PolicyStandard
	}
}

func checkIfMatch(ifMatch string, version int64) error {
	if ifMatch == "" {
		return nil
	}
	value := strings.TrimPrefix(strings.Trim(strings.TrimSpace(ifMatch), `"`), "v")
	got, err := strconv.ParseInt(value, 10, 64)
	if err != nil || got != version {
		return errs.New(errs.CodeVersionMismatch, "The resource version does not match If-Match.")
	}
	return nil
}

func formatETag(v int64) string { return fmt.Sprintf(`"v%d"`, v) }

// ViewOfService builds a View with webhook URL when CallbackBase is set.
func (s *Service) ViewOf(g *Gateway) View {
	return ViewOf(g, s.CallbackBase)
}
