package gateways

import (
	"context"
	"strings"
	"time"

	"github.com/radiobros/emailbridge/internal/audit"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

// CreateFromIntegration inserts an API gateway for a sending-provider connector (W52).
// Requires settings:write (install path) instead of send.
func (s *Service) CreateFromIntegration(ctx context.Context, in CreateInput) (*Gateway, error) {
	if err := tenancy.Require(ctx, tenancy.PermSettingsWrite); err != nil {
		return nil, err
	}
	return s.createUnchecked(ctx, in)
}

// DeleteFromIntegration removes a gateway owned by an integration instance.
func (s *Service) DeleteFromIntegration(ctx context.Context, id string) error {
	if err := tenancy.Require(ctx, tenancy.PermSettingsWrite); err != nil {
		return err
	}
	g, err := s.Repo.get(ctx, id)
	if err != nil {
		return err
	}
	if err := s.Repo.delete(ctx, g); err != nil {
		return err
	}
	s.audit(ctx, "smtpGateway.deleted", id, audit.Changes{"name": {Before: g.Name}, "source": {After: "integration"}})
	return nil
}

// SetEnabledFromIntegration toggles a gateway owned by an integration.
func (s *Service) SetEnabledFromIntegration(ctx context.Context, id string, enabled bool) error {
	if err := tenancy.Require(ctx, tenancy.PermSettingsWrite); err != nil {
		return err
	}
	g, err := s.Repo.get(ctx, id)
	if err != nil {
		return err
	}
	old := *g
	g.Enabled = enabled
	g.UpdatedAt = time.Now().UTC().Truncate(time.Millisecond)
	g.Version++
	if err := s.Repo.replace(ctx, g, old.Version); err != nil {
		return err
	}
	return nil
}

// UpdateFromIntegration patches API config/credentials for an integration-owned gateway.
func (s *Service) UpdateFromIntegration(ctx context.Context, id string, in CreateInput) error {
	if err := tenancy.Require(ctx, tenancy.PermSettingsWrite); err != nil {
		return err
	}
	g, err := s.Repo.get(ctx, id)
	if err != nil {
		return err
	}
	old := *g
	if in.API != nil {
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
	if name := strings.TrimSpace(in.Name); name != "" {
		g.Name = name
	}
	g.UpdatedAt = time.Now().UTC().Truncate(time.Millisecond)
	g.Version++
	return s.Repo.replace(ctx, g, old.Version)
}

func (s *Service) createUnchecked(ctx context.Context, in CreateInput) (*Gateway, error) {
	if err := validateCreate(in); err != nil {
		return nil, err
	}
	org, ok := store.OrganizationFrom(ctx)
	if !ok {
		return nil, errs.New(errs.CodeForbidden, "An organisation context is required.")
	}
	kind := normalizeKind(in.Kind)
	if kind == KindSMTP {
		return nil, errs.New(errs.CodeValidationFailed, "integration gateways must be API kinds")
	}
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
	if err := s.Repo.insert(ctx, g); err != nil {
		return nil, err
	}
	s.audit(ctx, "smtpGateway.created", g.ID, audit.Changes{
		"name": {After: g.Name}, "kind": {After: kind}, "source": {After: "integration"},
	})
	return g, nil
}
