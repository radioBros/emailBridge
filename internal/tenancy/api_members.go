package tenancy

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/mail"
	"regexp"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/logging"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/sysmail"
)

var memberSortFields = map[string]string{
	"email": "email", "role": "role", "createdAt": "createdAt", "id": "_id",
}

func (m *Module) registerMembers(srv *httpapi.Server) {
	ext := httpapi.Phase("P0")
	httpapi.Register(srv, huma.Operation{
		OperationID: "listMembers", Method: http.MethodGet, Path: "/v1/members",
		Summary: "List members", Tags: []string{"members"},
		Description: "Lists the members and pending invitations of the active organisation, with each member's name and " +
			"last sign-in. Any member (`read`; API keys need `members:read`). `q` searches the email and name " +
			"(at most 200 characters). Pagination: `limit` (default 50, max 200) with `cursor`, or `page` with " +
			"`includeTotal=true`. Sort by `email`, `role`, `createdAt` or `id` (prefix `-` for descending; default `-id`).",
		Metadata: httpapi.Problems(errs.CodeUnauthenticated, errs.CodeForbidden, errs.CodeScopeMissing, errs.CodeSortNotAllowed,
			errs.CodeValidationFailed),
		Extensions: httpapi.WithScope(ext, "members:read"),
	}, m.listMembers)
	httpapi.Register(srv, huma.Operation{
		OperationID: "createMember", Method: http.MethodPost, Path: "/v1/members",
		DefaultStatus: http.StatusCreated,
		Summary:       "Invite member", Tags: []string{"members"},
		Description: "Invites an email address to the active organisation with a role. Requires `members.write` (owners, " +
			"admins; admins cannot invite owners). Sends an invitation email whose link (`/accept-invite#<token>`) " +
			"stays valid for 72 hours; the membership is pending until accepted with `POST /v1/invitations/accept`. " +
			"An existing member or pending invitation for the same email gives `409 conflict`. Writes a " +
			"`member.invited` audit record. Supports `Idempotency-Key`.",
		Metadata: httpapi.Problems(errs.CodeUnauthenticated, errs.CodeForbidden, errs.CodeScopeMissing, errs.CodeNotFound,
			errs.CodeConflict, errs.CodeValidationFailed),
		Extensions: httpapi.WithScope(httpapi.Phase("P0"), "members:write"),
	}, m.createMember)
	httpapi.Register(srv, huma.Operation{
		OperationID: "patchMember", Method: http.MethodPatch, Path: "/v1/members/{memberId}",
		Summary: "Change member role", Tags: []string{"members"},
		Description: "Changes a member's role. Requires `members.write`; admins can neither change owners nor promote to " +
			"owner. The last owner cannot be demoted (`409 last_owner`). `If-Match` with the member's `ETag` " +
			"guards against concurrent changes. Writes a `member.role_changed` audit record; the member's next " +
			"request already uses the new permissions.",
		Metadata: httpapi.Problems(errs.CodeUnauthenticated, errs.CodeForbidden, errs.CodeNotFound, errs.CodeLastOwner,
			errs.CodeVersionMismatch, errs.CodeValidationFailed),
		Extensions: httpapi.WithScope(httpapi.Phase("P0"), "members:write"),
	}, m.patchMember)
	httpapi.Register(srv, huma.Operation{
		OperationID: "deleteMember", Method: http.MethodDelete, Path: "/v1/members/{memberId}",
		Summary: "Remove member", Tags: []string{"members"},
		Description: "Removes a member from the organisation or withdraws a pending invitation. Requires `members.write`; " +
			"admins cannot remove owners and the last owner cannot be removed (`409 last_owner`). The user's own " +
			"profile and other memberships stay. `If-Match` is honoured. Writes a `member.removed` audit record. " +
			"Answers `204` without body.",
		Metadata: httpapi.Problems(errs.CodeUnauthenticated, errs.CodeForbidden, errs.CodeNotFound, errs.CodeLastOwner,
			errs.CodeVersionMismatch),
		Extensions: httpapi.WithScope(httpapi.Phase("P0"), "members:write"),
	}, m.deleteMember)
}

type memberListIn struct {
	Limit        int    `query:"limit" doc:"Page size (default 50, at most 200)." example:"50"`
	Cursor       string `query:"cursor" doc:"Opaque cursor from the previous page's nextCursor." example:"eyJpZCI6Im1lbV8wMW01Y2gyeWcwZnM5d2F6eDR2MGNrOTN0NyIsImZpZWxkIjoiX2lkIn0"`
	Page         int    `query:"page" doc:"1-based page number (offset paging; use instead of cursor)." example:"1"`
	IncludeTotal bool   `query:"includeTotal" doc:"With page: also return total (capped at 10 000)." example:"true"`
	Sort         string `query:"sort" doc:"Sort field email, role, createdAt or id; prefix - for descending. Default -id." example:"email"`
	Q            string `query:"q" doc:"Case-insensitive search in email and name (at most 200 characters)." example:"rossi"`
}

type memberListOut struct {
	Body MembershipList
}

// MembershipList is the list response (S2 §2).
type MembershipList struct {
	Data        []Membership `json:"data" doc:"Members and pending invitations of this page."`
	NextCursor  *string      `json:"nextCursor" doc:"Cursor of the next page; null on the last page." example:"eyJpZCI6Im1lbV8wMW01Y2gyeWcwZnM5d2F6eDR2MGNrOTN0NyIsImZpZWxkIjoiX2lkIn0"`
	Total       *int64       `json:"total,omitempty" doc:"Total number of members (only with page and includeTotal)." example:"7"`
	TotalCapped *bool        `json:"totalCapped,omitempty" doc:"True when total stopped counting at 10 000." example:"false"`
}

type memberCreateIn struct {
	Body struct {
		Email string `json:"email" doc:"Email address to invite." example:"luca.bianchi@example.com"`
		Role  Role   `json:"role" doc:"Role after acceptance: owner, admin, operator or viewer (admins cannot invite owners)." example:"operator"`
	}
}

type memberCreatedOut struct {
	Status   int
	Location string `header:"Location" doc:"URL of the new membership." example:"/v1/members/mem_01m5ch2yg0fs9wazx4v0ck93t7"`
	ETag     string `header:"ETag" doc:"Version of the new membership." example:"\"v1\""`
	Body     Membership
}

type memberPatchIn struct {
	MemberID string `path:"memberId" doc:"Membership ID (mem_…)." example:"mem_01m5ch2yg0fs9wazx4v0ck93t7"`
	IfMatch  string `header:"If-Match" doc:"ETag from the last read; a stale value fails with 412 version_mismatch." example:"\"v1\""`
	Body     struct {
		Role *Role `json:"role,omitempty" doc:"New role: owner, admin, operator or viewer (required)." example:"admin"`
	}
}

type memberGetOut struct {
	ETag string `header:"ETag" doc:"Current version of the membership." example:"\"v2\""`
	Body Membership
}

type memberDeleteIn struct {
	MemberID string `path:"memberId" doc:"Membership ID (mem_…)." example:"mem_01m5ch2yg0fs9wazx4v0ck93t7"`
	IfMatch  string `header:"If-Match" doc:"ETag from the last read; a stale value fails with 412 version_mismatch." example:"\"v2\""`
}

func (m *Module) listMembers(ctx context.Context, in *memberListIn) (*memberListOut, error) {
	list, err := m.service.ListMembers(ctx, ListFilter{
		Limit: in.Limit, Cursor: in.Cursor, Page: in.Page, IncludeTotal: in.IncludeTotal, Sort: in.Sort, Q: in.Q,
	})
	if err != nil {
		return nil, err
	}
	return &memberListOut{Body: *list}, nil
}

func (m *Module) createMember(ctx context.Context, in *memberCreateIn) (*memberCreatedOut, error) {
	mem, err := m.service.InviteMember(ctx, in.Body.Email, in.Body.Role)
	if err != nil {
		return nil, err
	}
	return &memberCreatedOut{
		Status: http.StatusCreated, Location: "/v1/members/" + mem.ID,
		ETag: formatETag(mem.Version), Body: *mem,
	}, nil
}

func (m *Module) patchMember(ctx context.Context, in *memberPatchIn) (*memberGetOut, error) {
	mem, err := m.service.PatchMember(ctx, in.MemberID, in.Body.Role, in.IfMatch)
	if err != nil {
		return nil, err
	}
	return &memberGetOut{ETag: formatETag(mem.Version), Body: *mem}, nil
}

func (m *Module) deleteMember(ctx context.Context, in *memberDeleteIn) (*struct{}, error) {
	if err := m.service.DeleteMember(ctx, in.MemberID, in.IfMatch); err != nil {
		return nil, err
	}
	return nil, nil
}

// ListMembers is GET /v1/members.
// withProfiles adds each accepted member's name and last sign-in from their user profile (one query).
func (s *Service) withProfiles(ctx context.Context, members []Membership) error {
	ids := make([]string, 0, len(members))
	for _, m := range members {
		if m.UserID != "" {
			ids = append(ids, m.UserID)
		}
	}
	users, err := s.Repo.UsersByID(ctx, ids)
	if err != nil {
		return errs.Wrap(err, errs.CodeInternalError, "tenancy.members.profiles")
	}
	for i := range members {
		if u, ok := users[members[i].UserID]; ok {
			members[i].Name, members[i].LastLoginAt = u.Name, u.LastLoginAt
			if members[i].Email == "" {
				members[i].Email = u.Email
			}
		}
	}
	return nil
}

// TouchLogin records a successful sign-in (called by the auth module when it issues a session).
func (s *Service) TouchLogin(ctx context.Context, authUserID string) error {
	return s.Repo.TouchLogin(ctx, authUserID, time.Now().UTC())
}

func (s *Service) ListMembers(ctx context.Context, f ListFilter) (*MembershipList, error) {
	if err := RequireRead(ctx, "members"); err != nil {
		return nil, err
	}
	base, err := store.Scoped(ctx, bson.M{})
	if err != nil {
		return nil, err
	}
	if err := s.applyMemberSearch(ctx, base, f.Q); err != nil {
		return nil, err
	}
	sortField, sortDir, err := parseListSort(f.Sort, memberSortFields)
	if err != nil {
		return nil, err
	}
	limit := clampLimit(f.Limit)
	sortDoc := bson.D{{Key: sortField, Value: sortDir}}
	if sortField != "_id" {
		sortDoc = append(sortDoc, bson.E{Key: "_id", Value: sortDir})
	}
	filter := base
	var skip int64
	if f.Page > 0 {
		skip = int64(f.Page-1) * int64(limit)
	} else if f.Cursor != "" {
		cur, err := decodeCursor(f.Cursor)
		if err != nil {
			return nil, errs.New(errs.CodeValidationFailed, "The cursor is invalid.")
		}
		keyFilter, err := memberCursorFilter(cur, sortField, sortDir)
		if err != nil {
			return nil, err
		}
		filter = bson.M{"$and": []bson.M{base, keyFilter}}
	}
	rows, err := s.Repo.ListMemberships(ctx, filter, sortDoc, int64(limit+1), skip)
	if err != nil {
		return nil, err
	}
	out := &MembershipList{Data: rows}
	if out.Data == nil {
		out.Data = []Membership{}
	}
	if err := s.withProfiles(ctx, out.Data); err != nil {
		return nil, err
	}
	if len(rows) > limit {
		rows = rows[:limit]
		out.Data = rows
		last := rows[len(rows)-1]
		nc := encodeCursor(cursorPayload{ID: last.ID, Field: sortField, Desc: sortDir < 0, Key: memberSortKey(last, sortField)})
		out.NextCursor = &nc
	}
	if f.IncludeTotal && f.Page > 0 {
		n, err := s.Repo.CountMemberships(ctx, base, totalCap+1)
		if err != nil {
			return nil, err
		}
		capped := n > totalCap
		if capped {
			n = totalCap
		}
		out.Total, out.TotalCapped = &n, &capped
	}
	return out, nil
}

const memberUserSearchCap = 1000

func (s *Service) applyMemberSearch(ctx context.Context, base bson.M, raw string) error {
	q := strings.TrimSpace(raw)
	if q == "" {
		return nil
	}
	if len(q) > 200 {
		return errs.New(errs.CodeValidationFailed, "q must be at most 200 characters.")
	}
	ids, err := s.Repo.UserIDsMatching(ctx, q, memberUserSearchCap)
	if err != nil {
		return errs.Wrap(err, errs.CodeInternalError, "tenancy.members.search")
	}
	rx := bson.M{"$regex": regexp.QuoteMeta(q), "$options": "i"}
	base["$or"] = []bson.M{{"email": rx}, {"userId": bson.M{"$in": ids}}}
	return nil
}

func memberSortKey(m Membership, field string) any {
	switch field {
	case "email":
		return m.Email
	case "role":
		return string(m.Role)
	case "createdAt":
		return timeKey(m.CreatedAt)
	default:
		return m.ID
	}
}

func memberCursorFilter(p cursorPayload, sortField string, sortDir int) (bson.M, error) {
	cmp := "$lt"
	if sortDir > 0 {
		cmp = "$gt"
	}
	if sortField == "_id" {
		return bson.M{"_id": bson.M{cmp: p.ID}}, nil
	}
	key := p.Key
	if sortField == "createdAt" {
		t, err := parseTimeKey(key)
		if err != nil {
			return nil, errs.New(errs.CodeValidationFailed, "The cursor is invalid.")
		}
		key = t
	}
	return bson.M{"$or": []bson.M{
		{sortField: bson.M{cmp: key}},
		{sortField: key, "_id": bson.M{cmp: p.ID}},
	}}, nil
}

// InviteMember is POST /v1/members.
func (s *Service) InviteMember(ctx context.Context, email string, role Role) (*Membership, error) {
	if err := Require(ctx, PermMembersWrite); err != nil {
		return nil, err
	}
	c, _ := CallerFrom(ctx)
	email = strings.ToLower(strings.TrimSpace(email))
	if _, err := mail.ParseAddress(email); err != nil || !strings.Contains(email, "@") {
		return nil, errs.New(errs.CodeValidationFailed, "email is invalid.")
	}
	if !role.Valid() {
		return nil, errs.New(errs.CodeValidationFailed, "role is invalid.")
	}
	if role == RoleOwner && !mayTouchOwners(c) {
		return nil, errs.New(errs.CodeForbidden, "Admins cannot invite owners.")
	}
	orgID := c.OrganizationID
	if orgID == "" {
		return nil, errs.New(errs.CodeForbidden, "Organisation context is required.")
	}
	org, err := s.Repo.Organization(ctx, orgID)
	if errors.Is(err, ErrNotFound) {
		return nil, errs.New(errs.CodeNotFound, "Organisation not found.")
	}
	if err != nil {
		return nil, err
	}
	if org.Status == "deleting" {
		return nil, errs.New(errs.CodeConflict, "Organisation is being deleted.")
	}
	if u, err := s.Repo.UserByEmail(ctx, email); err == nil {
		var existing Membership
		err = s.Repo.DB.Collection(MembershipsCollection).FindOne(ctx, bson.M{
			"organizationId": orgID, "userId": u.ID,
		}).Decode(&existing)
		if err == nil {
			return nil, errs.New(errs.CodeConflict, "This user is already a member.").WithSafe("email", email)
		}
		if !errors.Is(err, mongo.ErrNoDocuments) {
			return nil, err
		}
	} else if !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	if _, err := s.Repo.PendingInviteByEmail(ctx, orgID, email); err == nil {
		return nil, errs.New(errs.CodeConflict, "An invitation for this email is already pending.").WithSafe("email", email)
	} else if !errors.Is(err, ErrNotFound) {
		return nil, err
	}

	token, hash, err := newInviteToken()
	if err != nil {
		return nil, fmt.Errorf("tenancy: invite token: %w", err)
	}
	now := time.Now().UTC()
	mem := &Membership{
		ID: store.NewID(store.PrefixMembership), OrganizationID: orgID, Email: email, Role: role,
		InvitedBy: c.Actor(), Invite: &Invite{Email: email, TokenHash: hash, ExpiresAt: now.Add(inviteTTL)},
		CreatedAt: now, UpdatedAt: now, Version: 1,
	}
	if err := s.Repo.InsertMembership(ctx, mem); err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return nil, errs.New(errs.CodeConflict, "An invitation for this email is already pending.").WithSafe("email", email)
		}
		return nil, fmt.Errorf("tenancy: invite: %w", err)
	}

	url := inviteURL(s.PublicURL, token)
	mailed := s.enqueueInvite(ctx, email, org.Name, c, url)
	if !mailed {
		log := s.Log
		if log == nil {
			log = slog.Default()
		}
		log.WarnContext(ctx, "invitation created but no system mail was sent",
			logging.Address("to", email), "organizationId", orgID, "membershipId", mem.ID,
			"token", logging.Secret(token))
	}
	s.audit(ctx, "member.invited", "membership", mem.ID, map[string]FieldDiff{
		"email": {After: email},
		"role":  {After: string(role)},
	})
	return mem, nil
}

func (s *Service) enqueueInvite(ctx context.Context, email, orgName string, c Caller, url string) bool {
	if s.Mail == nil {
		return false
	}
	inviter := c.Email
	if inviter == "" {
		inviter = c.Actor()
	}
	// Invitee has no account yet — use the inviter's language.
	locale := s.localeOfUser(ctx, c.UserID)
	err := s.Mail.Enqueue(ctx, sysmail.Mail{
		Kind: sysmail.KindInvitation, To: email, Locale: locale,
		Data: map[string]string{"InviteURL": url, "OrgName": orgName, "InviterName": inviter},
	})
	return err == nil
}

// localeOfUser returns the inviter's ui.locale (preferred) or profile locale, default en.
func (s *Service) localeOfUser(ctx context.Context, userID string) string {
	if s == nil || s.Repo == nil || userID == "" {
		return "en"
	}
	if s.Repo.DB != nil {
		if loc := uiLocaleOfUser(ctx, s.Repo.DB, userID); loc != "" {
			return loc
		}
	}
	u, err := s.Repo.UserByID(ctx, userID)
	if err != nil || u == nil {
		return "en"
	}
	return sysmail.NormalizeLocale(u.Locale)
}

// PatchMember is PATCH /v1/members/{id}.
func (s *Service) PatchMember(ctx context.Context, id string, role *Role, ifMatch string) (*Membership, error) {
	if err := Require(ctx, PermMembersWrite); err != nil {
		return nil, err
	}
	c, _ := CallerFrom(ctx)
	if role == nil {
		return nil, errs.New(errs.CodeValidationFailed, "role is required.")
	}
	if !role.Valid() {
		return nil, errs.New(errs.CodeValidationFailed, "role is invalid.")
	}
	cur, err := s.memberInCallerOrg(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := checkIfMatch(ifMatch, cur.Version); err != nil {
		return nil, err
	}
	if cur.Role == RoleOwner && !mayTouchOwners(c) {
		return nil, errs.New(errs.CodeForbidden, "Admins cannot change owners.")
	}
	if *role == RoleOwner && !mayTouchOwners(c) {
		return nil, errs.New(errs.CodeForbidden, "Admins cannot promote to owner.")
	}
	if cur.Role == RoleOwner && *role != RoleOwner {
		n, err := s.Repo.CountOwners(ctx, cur.OrganizationID)
		if err != nil {
			return nil, err
		}
		if n <= 1 {
			return nil, errs.New(errs.CodeLastOwner, "The last owner cannot be demoted.")
		}
	}
	updated, err := s.Repo.UpdateMembership(ctx, id, cur.Version, bson.M{"role": *role}, nil)
	if errors.Is(err, ErrNotFound) {
		return nil, errs.New(errs.CodeVersionMismatch, "The resource version does not match If-Match.")
	}
	if err != nil {
		return nil, err
	}
	s.audit(ctx, "member.role_changed", "membership", id, map[string]FieldDiff{
		"role": {Before: string(cur.Role), After: string(*role)},
	})
	return updated, nil
}

// DeleteMember is DELETE /v1/members/{id}.
func (s *Service) DeleteMember(ctx context.Context, id, ifMatch string) error {
	if err := Require(ctx, PermMembersWrite); err != nil {
		return err
	}
	c, _ := CallerFrom(ctx)
	cur, err := s.memberInCallerOrg(ctx, id)
	if err != nil {
		return err
	}
	if err := checkIfMatch(ifMatch, cur.Version); err != nil {
		return err
	}
	if cur.Role == RoleOwner && !mayTouchOwners(c) {
		return errs.New(errs.CodeForbidden, "Admins cannot remove owners.")
	}
	if cur.Role == RoleOwner && cur.AcceptedAt != nil {
		n, err := s.Repo.CountOwners(ctx, cur.OrganizationID)
		if err != nil {
			return err
		}
		if n <= 1 {
			return errs.New(errs.CodeLastOwner, "The last owner cannot be removed.")
		}
	}
	ver := cur.Version
	if ifMatch == "" {
		ver = 0
	}
	if err := s.Repo.DeleteMembership(ctx, cur.OrganizationID, id, ver); err != nil {
		if errors.Is(err, ErrNotFound) {
			if ifMatch != "" {
				return errs.New(errs.CodeVersionMismatch, "The resource version does not match If-Match.")
			}
			return errs.New(errs.CodeNotFound, "Member not found.").WithSafe("memberId", id)
		}
		return err
	}
	s.audit(ctx, "member.removed", "membership", id, map[string]FieldDiff{
		"role":  {Before: string(cur.Role)},
		"email": {Before: cur.Email},
	})
	return nil
}

func (s *Service) memberInCallerOrg(ctx context.Context, id string) (*Membership, error) {
	c, ok := CallerFrom(ctx)
	if !ok || c.OrganizationID == "" {
		return nil, errs.New(errs.CodeForbidden, "Organisation context is required.")
	}
	if !store.IsID(store.PrefixMembership, id) {
		return nil, errs.New(errs.CodeNotFound, "Member not found.").WithSafe("memberId", id)
	}
	m, err := s.Repo.MembershipInOrg(ctx, c.OrganizationID, id)
	if errors.Is(err, ErrNotFound) {
		return nil, errs.New(errs.CodeNotFound, "Member not found.").WithSafe("memberId", id)
	}
	return m, err
}
