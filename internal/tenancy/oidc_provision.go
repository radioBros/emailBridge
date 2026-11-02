package tenancy

import (
	"context"
	"errors"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/store"
)

// HasPendingInvite reports whether email has any valid pending invitation
// (any organisation, not expired, not yet accepted).
func (s *Service) HasPendingInvite(ctx context.Context, email string) (bool, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" || !strings.Contains(email, "@") {
		return false, nil
	}
	n, err := s.Repo.CountMemberships(ctx, bson.M{
		"invite.email":     email,
		"invite.expiresAt": bson.M{"$gt": time.Now().UTC()},
		"acceptedAt":       nil,
	}, 1)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// ProvisionFromGroups links pending invitations for email and applies IdP group →
// "org_<id>:<role>" mappings. Creates missing memberships; upgrades roles only;
// never downgrades or removes an existing owner. Idempotent.
//
// groupRoles is the provider map (passed by auth so this package does not import auth).
// providerID is recorded in audit only.
func (s *Service) ProvisionFromGroups(ctx context.Context, authUserID, email, providerID string, groupRoles map[string]string, groups []string) error {
	email = strings.ToLower(strings.TrimSpace(email))
	if authUserID == "" || email == "" {
		return errs.New(errs.CodeValidationFailed, "auth user and email are required.")
	}
	u, err := s.ensureProfile(ctx, authUserID, email)
	if err != nil {
		return err
	}
	if err := s.acceptPendingInvites(ctx, u); err != nil {
		return err
	}
	return s.applyGroupRoles(ctx, u, providerID, groupRoles, groups)
}

func (s *Service) ensureProfile(ctx context.Context, authUserID, email string) (*User, error) {
	u, err := s.Repo.UserByAuthID(ctx, authUserID)
	if err == nil {
		return u, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	// Prefer existing profile by email (bootstrap / invite race).
	if existing, eerr := s.Repo.UserByEmail(ctx, email); eerr == nil {
		if existing.AuthUserID == "" || existing.AuthUserID == authUserID {
			if existing.AuthUserID == "" {
				_, _ = s.Repo.UpdateUser(ctx, existing.ID, existing.Version, bson.M{"authUserId": authUserID})
				existing.AuthUserID = authUserID
			}
			return existing, nil
		}
	} else if !errors.Is(eerr, ErrNotFound) {
		return nil, eerr
	}
	u = &User{AuthUserID: authUserID, Email: email, Name: email}
	if err := s.Repo.CreateUser(ctx, u); err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return s.Repo.UserByAuthID(ctx, authUserID)
		}
		return nil, err
	}
	return u, nil
}

func (s *Service) acceptPendingInvites(ctx context.Context, u *User) error {
	now := time.Now().UTC()
	list, err := s.Repo.ListMemberships(ctx, bson.M{
		"invite.email":     u.Email,
		"invite.expiresAt": bson.M{"$gt": now},
		"acceptedAt":       nil,
	}, bson.D{{Key: "_id", Value: 1}}, 100, 0)
	if err != nil {
		return err
	}
	for i := range list {
		mem := &list[i]
		updated, err := s.Repo.UpdateMembership(ctx, mem.ID, mem.Version, bson.M{
			"userId": u.ID, "email": u.Email, "acceptedAt": now,
		}, bson.M{"invite": ""})
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				continue // raced
			}
			return err
		}
		_ = s.Repo.SetLastOrganization(ctx, u.ID, updated.OrganizationID)
		s.audit(ctx, "member.accepted", "membership", updated.ID, map[string]FieldDiff{
			"via": {After: "oidc"},
		})
	}
	return nil
}

func (s *Service) applyGroupRoles(ctx context.Context, u *User, providerID string, groupRoles map[string]string, groups []string) error {
	if len(groupRoles) == 0 || len(groups) == 0 {
		return nil
	}
	seenOrg := map[string]Role{}
	for _, g := range groups {
		g = strings.TrimSpace(g)
		if g == "" {
			continue
		}
		raw, ok := groupRoles[g]
		if !ok {
			// case-insensitive key match
			for k, v := range groupRoles {
				if strings.EqualFold(k, g) {
					raw, ok = v, true
					break
				}
			}
		}
		if !ok {
			continue
		}
		orgID, role, ok := ParseGroupRoleValue(raw)
		if !ok {
			continue
		}
		if prev, exists := seenOrg[orgID]; exists {
			if roleRank(role) > roleRank(prev) {
				seenOrg[orgID] = role
			}
			continue
		}
		seenOrg[orgID] = role
	}
	for orgID, want := range seenOrg {
		if err := s.ensureMembershipRole(ctx, u, orgID, want, providerID); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) ensureMembershipRole(ctx context.Context, u *User, orgID string, want Role, providerID string) error {
	if _, err := s.Repo.Organization(ctx, orgID); err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil // stale mapping
		}
		return err
	}
	var existing Membership
	err := s.Repo.DB.Collection(MembershipsCollection).FindOne(ctx, bson.M{
		"organizationId": orgID, "userId": u.ID,
	}).Decode(&existing)
	if errors.Is(err, mongo.ErrNoDocuments) {
		mem, err := s.Repo.CreateMembership(ctx, orgID, u.ID, want)
		if err != nil {
			if mongo.IsDuplicateKeyError(err) {
				return nil
			}
			return err
		}
		_ = s.Repo.SetLastOrganization(ctx, u.ID, orgID)
		s.audit(ctx, "member.provisioned", "membership", mem.ID, map[string]FieldDiff{
			"role":       {After: string(want)},
			"providerId": {After: providerID},
			"via":        {After: "oidc"},
		})
		return nil
	}
	if err != nil {
		return err
	}
	// Never downgrade or remove an existing owner; never lower any role.
	if existing.Role == RoleOwner || roleRank(want) <= roleRank(existing.Role) {
		return nil
	}
	updated, err := s.Repo.UpdateMembership(ctx, existing.ID, existing.Version, bson.M{"role": want}, nil)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		return err
	}
	s.audit(ctx, "member.role_changed", "membership", updated.ID, map[string]FieldDiff{
		"role":       {Before: string(existing.Role), After: string(want)},
		"providerId": {After: providerID},
		"via":        {After: "oidc"},
	})
	return nil
}

// ParseGroupRoleValue parses "org_<ulid>:<role>" from an OIDC groupRoles value.
func ParseGroupRoleValue(raw string) (orgID string, role Role, ok bool) {
	raw = strings.TrimSpace(raw)
	orgID, roleStr, cut := strings.Cut(raw, ":")
	if !cut {
		return "", "", false
	}
	orgID, roleStr = strings.TrimSpace(orgID), strings.TrimSpace(roleStr)
	role = Role(roleStr)
	if !store.IsID(store.PrefixOrganization, orgID) || !role.Valid() {
		return "", "", false
	}
	return orgID, role, true
}

func roleRank(r Role) int {
	switch r {
	case RoleOwner:
		return 4
	case RoleAdmin:
		return 3
	case RoleOperator:
		return 2
	case RoleViewer:
		return 1
	default:
		return 0
	}
}
