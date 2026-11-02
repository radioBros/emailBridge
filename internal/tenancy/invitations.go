package tenancy

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/radiobros/emailbridge/internal/config"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/store"
)

const (
	inviteTTL         = 72 * time.Hour
	inviteTokLen      = 32
	defaultInviteRate = 10 // matches EB_RATE_LOGIN default 10/min
)

// newInviteToken returns a 32-byte base64url token and its sha256 hex hash (S10 §2.4).
func newInviteToken() (token, hash string, err error) {
	raw := make([]byte, inviteTokLen)
	if _, err = rand.Read(raw); err != nil {
		return "", "", err
	}
	token = base64.RawURLEncoding.EncodeToString(raw)
	sum := sha256.Sum256([]byte(token))
	return token, hex.EncodeToString(sum[:]), nil
}

func hashInviteToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func inviteTokenMatch(storedHash, token string) bool {
	want, err := hex.DecodeString(storedHash)
	if err != nil || len(want) != sha256.Size {
		return false
	}
	got := sha256.Sum256([]byte(token))
	return subtle.ConstantTimeCompare(want, got[:]) == 1
}

// inviteLimiter is a per-IP sliding window (EB_RATE_LOGIN via Module.InviteRate).
type inviteLimiter struct {
	mu     sync.Mutex
	hits   map[string][]time.Time
	n      int
	window time.Duration
}

func newInviteLimiter(n int, window time.Duration) *inviteLimiter {
	if n <= 0 {
		n = defaultInviteRate
	}
	if window <= 0 {
		window = time.Minute
	}
	return &inviteLimiter{hits: map[string][]time.Time{}, n: n, window: window}
}

func newInviteLimiterFromRate(r config.Rate) *inviteLimiter {
	return newInviteLimiter(r.Count, r.Period)
}

func (l *inviteLimiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	cut := now.Add(-l.window)
	keep := l.hits[key][:0]
	for _, t := range l.hits[key] {
		if t.After(cut) {
			keep = append(keep, t)
		}
	}
	if len(keep) >= l.n {
		l.hits[key] = keep
		return false
	}
	l.hits[key] = append(keep, now)
	return true
}

func (m *Module) checkInviteRate(ctx context.Context) error {
	ip := httpapi.ClientIP(ctx).String()
	if ip == "" {
		ip = "unknown"
	}
	if m.inviteLim == nil {
		m.inviteLim = newInviteLimiterFromRate(m.InviteRate)
	}
	if !m.inviteLim.allow(ip) {
		return errs.New(errs.CodeRateLimited, "Too many invitation attempts; wait and try again.")
	}
	return nil
}

type tokenBody struct {
	Token    string `json:"token" doc:"Invitation token from the link fragment (/accept-invite#<token>)." example:"Zk3q9mVb2xT7rLpQ8sWc4nYh6dJf1aGe5uKo0iRt3Ew"`
	Password string `json:"password,omitempty" doc:"Password for the new sign-in, 12–128 characters. Only when the caller is not signed in." example:"correct-horse-battery-staple"`
	Name     string `json:"name,omitempty" doc:"Display name for the new user (default: the email address). Only when not signed in." example:"Luca Bianchi"`
}

type lookupIn struct {
	Body struct {
		Token string `json:"token" doc:"Invitation token from the link fragment." example:"Zk3q9mVb2xT7rLpQ8sWc4nYh6dJf1aGe5uKo0iRt3Ew"`
	}
}

type lookupOut struct {
	Body struct {
		Email            string `json:"email" doc:"Invited email address." example:"luca.bianchi@example.com"`
		OrganizationName string `json:"organizationName" doc:"Name of the inviting organisation." example:"Acme Legal"`
		InviterName      string `json:"inviterName" doc:"Name (or email) of the member who sent the invitation." example:"Maria Rossi"`
		ExistingUser     bool   `json:"existingUser" doc:"True when a user with this email exists: sign in first, then accept." example:"false"`
	}
}

type acceptIn struct {
	Body tokenBody
}

type acceptOut struct {
	Body struct {
		MembershipID   string `json:"membershipId" doc:"The now active membership." example:"mem_01m5ch2yg0fs9wazx4v0ck93t7"`
		OrganizationID string `json:"organizationId" doc:"Organisation joined." example:"org_01m5caqpk0kast63yc13wpy2d9"`
		UserID         string `json:"userId" doc:"User who joined (new or signed-in)." example:"usr_01m5d20y80z8mv4gqjfp3m7140"`
	}
}

// inviteExpired documents the 410 invitation_expired an expired invitation returns (see loadPendingInvite).
var inviteExpired = httpapi.ProblemDoc{Code: errs.CodeInvitationExpired, Detail: "This invitation has expired."}

func (m *Module) registerInvite(srv *httpapi.Server) {
	ext := httpapi.Phase("P0")
	httpapi.Register(srv, huma.Operation{
		OperationID: "lookupInvitation", Method: http.MethodPost, Path: "/v1/invitations/lookup",
		Summary: "Look up an organisation invitation", Tags: []string{"members"},
		Description: "Shows who invited whom to which organisation, so the accept page can greet the invitee and choose " +
			"between \"sign in and accept\" and \"create a password\". Public (the token is the credential; it is " +
			"sent in the body, never in a URL). Rate limited per client IP (`EB_RATE_LOGIN`, default 10 per " +
			"minute). An unknown or used token gives `404 not_found`, an expired one `410 invitation_expired` " +
			"(72 hours after it was sent). Read-only.",
		Metadata: httpapi.ProblemDocs(httpapi.ProblemDoc{Code: errs.CodeNotFound}, httpapi.ProblemDoc{Code: errs.CodeRateLimited},
			httpapi.ProblemDoc{Code: errs.CodeValidationFailed}, inviteExpired),
		Security:   []map[string][]string{},
		Extensions: ext,
	}, m.lookupInvitation)
	httpapi.Register(srv, huma.Operation{
		OperationID: "acceptInvitation", Method: http.MethodPost, Path: "/v1/invitations/accept",
		Summary: "Accept an organisation invitation", Tags: []string{"members"},
		Description: "Activates the pending membership. A signed-in user accepts for themselves (the invitation's " +
			"email must match theirs, else `403`); an anonymous caller creates the user with `password` (12–128 " +
			"characters) and optional `name` — if that email already has a user, `409` asks them to sign in first. " +
			"The token works once; an expired one gives `410 invitation_expired`. Rate limited per IP like lookup. " +
			"Writes a `member.accepted` audit record.",
		Metadata: httpapi.ProblemDocs(httpapi.ProblemDoc{Code: errs.CodeNotFound}, httpapi.ProblemDoc{Code: errs.CodeForbidden},
			httpapi.ProblemDoc{Code: errs.CodeConflict}, httpapi.ProblemDoc{Code: errs.CodeValidationFailed},
			httpapi.ProblemDoc{Code: errs.CodeRateLimited}, inviteExpired),
		RequestBody: &huma.RequestBody{Content: map[string]*huma.MediaType{"application/json": {Examples: map[string]*huma.Example{
			"newUser":      {Summary: "Anonymous invitee creates a sign-in", Value: map[string]any{"token": "Zk3q9mVb2xT7rLpQ8sWc4nYh6dJf1aGe5uKo0iRt3Ew", "password": "correct-horse-battery-staple", "name": "Luca Bianchi"}}, //nolint:gosec // G101: documentation example, not a credential
			"signedInUser": {Summary: "Signed-in user accepts", Value: map[string]any{"token": "Zk3q9mVb2xT7rLpQ8sWc4nYh6dJf1aGe5uKo0iRt3Ew"}},                                                                                  //nolint:gosec // G101: documentation example, not a credential
		}}}},
		Security:   []map[string][]string{{}, {"session": {}}},
		Extensions: httpapi.Phase("P0"),
	}, m.acceptInvitation)
}

func (m *Module) lookupInvitation(ctx context.Context, in *lookupIn) (*lookupOut, error) {
	if err := m.checkInviteRate(ctx); err != nil {
		return nil, err
	}
	mem, err := m.loadPendingInvite(ctx, in.Body.Token)
	if err != nil {
		return nil, err
	}
	org, err := m.service.Repo.Organization(ctx, mem.OrganizationID)
	if err != nil {
		return nil, err
	}
	inviter := mem.InvitedBy
	if store.IsID(store.PrefixUser, mem.InvitedBy) {
		if u, err := m.service.Repo.UserByID(ctx, mem.InvitedBy); err == nil {
			inviter = u.Name
			if inviter == "" {
				inviter = u.Email
			}
		}
	}
	email := strings.ToLower(strings.TrimSpace(mem.Invite.Email))
	_, err = m.service.Repo.UserByEmail(ctx, email)
	existing := err == nil
	if err != nil && !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	out := &lookupOut{}
	out.Body.Email = email
	out.Body.OrganizationName = org.Name
	out.Body.InviterName = inviter
	out.Body.ExistingUser = existing
	return out, nil
}

func (m *Module) acceptInvitation(ctx context.Context, in *acceptIn) (*acceptOut, error) {
	if err := m.checkInviteRate(ctx); err != nil {
		return nil, err
	}
	mem, err := m.loadPendingInvite(ctx, in.Body.Token)
	if err != nil {
		return nil, err
	}
	inviteEmail := strings.ToLower(strings.TrimSpace(mem.Invite.Email))

	var user *User
	if c, ok := CallerFrom(ctx); ok && c.Kind == CallerUser && c.UserID != "" {
		user, err = m.service.Repo.UserByID(ctx, c.UserID)
		if err != nil {
			return nil, err
		}
		if !emailsEqual(user.Email, inviteEmail) {
			return nil, errs.New(errs.CodeForbidden, "This invitation was sent to a different email address.")
		}
	} else {
		pass := in.Body.Password
		if len(pass) < 12 || len(pass) > 128 {
			return nil, errs.New(errs.CodeValidationFailed, "Password must be between 12 and 128 characters.")
		}
		if m.Creator == nil {
			return nil, errs.New(errs.CodeInternalError, "User creation is not configured.")
		}
		_, err := m.service.Repo.UserByEmail(ctx, inviteEmail)
		switch {
		case err == nil:
			return nil, errs.New(errs.CodeConflict, "An account with this email already exists; sign in and accept the invitation while signed in.")
		case !errors.Is(err, ErrNotFound):
			return nil, err
		}
		authID, err := m.Creator(ctx, inviteEmail, pass)
		if err != nil {
			return nil, err
		}
		name := strings.TrimSpace(in.Body.Name)
		if name == "" {
			name = inviteEmail
		}
		user = &User{AuthUserID: authID, Email: inviteEmail, Name: name, LastOrganizationID: mem.OrganizationID}
		if err := m.service.Repo.CreateUser(ctx, user); err != nil {
			return nil, err
		}
	}

	now := time.Now().UTC()
	updated, err := m.service.Repo.UpdateMembership(ctx, mem.ID, mem.Version, bson.M{
		"userId": user.ID, "email": user.Email, "acceptedAt": now,
	}, bson.M{"invite": ""})
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, errs.New(errs.CodeConflict, "This invitation was already accepted.")
		}
		return nil, err
	}
	_ = m.service.Repo.SetLastOrganization(ctx, user.ID, mem.OrganizationID)
	m.service.audit(ctx, "member.accepted", "membership", updated.ID, nil)
	out := &acceptOut{}
	out.Body.MembershipID = updated.ID
	out.Body.OrganizationID = updated.OrganizationID
	out.Body.UserID = user.ID
	return out, nil
}

// loadPendingInvite resolves a plaintext token to a pending membership. The token is never logged.
func (m *Module) loadPendingInvite(ctx context.Context, token string) (*Membership, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, errs.New(errs.CodeNotFound, "Invitation not found.")
	}
	hash := hashInviteToken(token)
	mem, err := m.service.Repo.MembershipByInviteHash(ctx, hash)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, errs.New(errs.CodeNotFound, "Invitation not found.")
		}
		return nil, err
	}
	if mem.Invite == nil || !inviteTokenMatch(mem.Invite.TokenHash, token) {
		return nil, errs.New(errs.CodeNotFound, "Invitation not found.")
	}
	if time.Now().UTC().After(mem.Invite.ExpiresAt) {
		return nil, errs.New(errs.CodeInvitationExpired, "This invitation has expired.")
	}
	return mem, nil
}

func emailsEqual(a, b string) bool {
	a, b = strings.ToLower(strings.TrimSpace(a)), strings.ToLower(strings.TrimSpace(b))
	if len(a) != len(b) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// inviteURL builds the mail link with the token in the URL fragment (never sent to the server).
func inviteURL(public, token string) string {
	public = strings.TrimRight(public, "/")
	frag := "/accept-invite#" + token
	if public == "" {
		return frag
	}
	return public + frag
}

// CheckMeExportRate limits GET /v1/me/export to 5 exports per user per hour on this node: the export reads
// every membership and audit record of the user, so it is not something to poll.
func (m *Module) CheckMeExportRate(ctx context.Context) error {
	c, _ := CallerFrom(ctx)
	key := c.UserID
	if key == "" {
		key = "ip:" + httpapi.ClientIP(ctx).String()
	}
	m.exportOnce.Do(func() { m.exportLim = newInviteLimiter(5, time.Hour) })
	if !m.exportLim.allow(key) {
		return errs.New(errs.CodeRateLimited, "Too many exports; wait and try again.")
	}
	return nil
}
