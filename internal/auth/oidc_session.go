package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/thecodearcher/limen"

	"github.com/radiobros/emailbridge/internal/errs"
)

func randomURLString(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func (s *Service) findLimenUserByEmail(ctx context.Context, email string) (string, error) {
	email = limen.NormalizeEmail(email)
	row, err := s.db.FindOne(ctx, "users", []limen.Where{limen.Eq("email", email)}, nil)
	if err != nil {
		if errors.Is(err, limen.ErrRecordNotFound) {
			return "", nil
		}
		return "", err
	}
	return fmt.Sprint(row["id"]), nil
}

// CreateOIDCUser creates a Limen user without a password and marks the email verified.
func (s *Service) CreateOIDCUser(ctx context.Context, email string) (string, error) {
	email = limen.NormalizeEmail(email)
	if email == "" || !strings.Contains(email, "@") {
		return "", errs.New(errs.CodeValidationFailed, "A valid email address is required.")
	}
	id := uuid.NewString()
	now := time.Now().UTC()
	_, err := s.db.Create(ctx, "users", map[string]any{
		"id": id, "email": email, "email_verified_at": now, "created_at": now, "updated_at": now,
		"name": "", "two_factor_enabled": false,
	})
	if err != nil {
		return "", fmt.Errorf("auth: create oidc user: %w", err)
	}
	return id, nil
}

func (s *Service) resolveOIDCUser(ctx context.Context, p *OIDCProvider, email string, groups []string) (string, error) {
	id, err := s.findLimenUserByEmail(ctx, email)
	if err != nil {
		return "", err
	}
	if id != "" {
		if s.ProvisionHook != nil {
			_ = s.ProvisionHook(ctx, id, email, p.ID, p.GroupRoles, groups)
		}
		return id, nil
	}
	mayCreate := p.AutoProvision
	if !mayCreate && s.InviteChecker != nil {
		ok, err := s.InviteChecker(ctx, email)
		if err != nil {
			return "", err
		}
		mayCreate = ok
	}
	if !mayCreate {
		return "", errOIDCFailed
	}
	id, err = s.CreateOIDCUser(ctx, email)
	if err != nil {
		return "", err
	}
	if s.ProvisionHook != nil {
		if err := s.ProvisionHook(ctx, id, email, p.ID, p.GroupRoles, groups); err != nil {
			s.log.WarnContext(ctx, "oidc provision hook failed", "err", err)
		}
	}
	return id, nil
}

var errOIDCFailed = errs.New(errs.CodeOIDCFailed, "Sign-in with this provider failed.")

func (s *Service) issueOIDCSession(ctx context.Context, w http.ResponseWriter, r *http.Request, authUserID string) error {
	token, err := randomURLString(32)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	duration, _, _, _ := s.o.sessionTimes()
	expires := now.Add(duration)
	sid := uuid.NewString()
	meta := map[string]any{
		"ip_address": clientIPString(r),
		"user_agent": r.UserAgent(),
	}
	_, err = s.db.Create(ctx, "sessions", map[string]any{
		"id": sid, "token": token, "user_id": authUserID,
		"created_at": now, "expires_at": expires, "last_access": now,
		"metadata": meta, methodField: MethodOIDC,
	})
	if err != nil {
		return fmt.Errorf("auth: create oidc session: %w", err)
	}
	//nolint:gosec // G124: Secure follows cookie policy (may be off only in development)
	http.SetCookie(w, &http.Cookie{
		Name: SessionCookie, Value: token, Path: "/", MaxAge: int(duration.Seconds()),
		Domain: s.cookie.domain, HttpOnly: true, Secure: s.cookie.secure, SameSite: http.SameSiteStrictMode,
	})
	s.log.InfoContext(ctx, "session issued", "route", "/oidc/callback", "method", MethodOIDC)
	if s.OnLogin != nil {
		if err := s.OnLogin(ctx, authUserID); err != nil {
			s.log.WarnContext(ctx, "cannot record the sign-in time", "err", err)
		}
	}
	return nil
}

func (s *Service) putOIDCState(ctx context.Context, state string, st oidcState) error {
	raw, err := json.Marshal(st)
	if err != nil {
		return err
	}
	return s.kv.CacheSet(ctx, oidcStateKeyPref+state, raw, oidcStateTTL)
}

func (s *Service) takeOIDCState(ctx context.Context, state string) (*oidcState, error) {
	key := oidcStateKeyPref + state
	e, err := s.kv.CacheGet(ctx, key)
	if err != nil {
		return nil, err
	}
	_ = s.kv.CacheDelete(ctx, key)
	var st oidcState
	if err := json.Unmarshal(e.Value, &st); err != nil {
		return nil, err
	}
	return &st, nil
}
