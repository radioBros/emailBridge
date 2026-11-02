package auth

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/thecodearcher/limen"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/httpapi"
)

// SessionView is one of the caller's sessions (GET /v1/me/sessions). Tokens stay out of the response.
type SessionView struct {
	ID           string    `json:"id" doc:"Session ID (UUID); use it with DELETE /v1/me/sessions/{sessionId}." example:"3f6c2a1e-8b4d-4e7a-9c5f-1d2e3b4a5c6d"`
	Current      bool      `json:"current" doc:"True for the session making this request." example:"true"`
	CreatedAt    time.Time `json:"createdAt" doc:"Sign-in time (RFC 3339, UTC)." example:"2026-10-26T08:15:42Z"`
	LastActiveAt time.Time `json:"lastActiveAt" doc:"Last request seen on this session." example:"2026-10-27T10:02:11Z"`
	ExpiresAt    time.Time `json:"expiresAt" doc:"When the session expires unless it is used again." example:"2026-11-02T08:15:42Z"`
	UserAgent    string    `json:"userAgent,omitempty" doc:"Browser user agent at sign-in." example:"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0 Safari/537.36"`
	IP           string    `json:"ip,omitempty" doc:"Client IP address at sign-in." example:"203.0.113.24"`
	Method       string    `json:"method" doc:"How the session was authenticated: password, password+totp, password+otp, password+backup_code, password+passkey, passkey, oidc or unknown." example:"password+totp"`
}

type sessionsListOut struct {
	Body struct {
		Data []SessionView `json:"data" doc:"Every active session of the signed-in user, current one included."`
	}
}

type sessionIDIn struct {
	SessionID     string `path:"sessionId" doc:"Session ID from GET /v1/me/sessions." example:"3f6c2a1e-8b4d-4e7a-9c5f-1d2e3b4a5c6d"`
	Authorization string `header:"Authorization" doc:"Not used for session management: requests with an API key (Bearer ebk_…) are refused with 403." example:"Bearer ebk_3kQ9xT7mV2bL5pR8sW1cY4nH6dJ0aGe"`
}

type revokeOthersIn struct {
	Authorization string `header:"Authorization" doc:"Not used for session management: requests with an API key (Bearer ebk_…) are refused with 403." example:"Bearer ebk_3kQ9xT7mV2bL5pR8sW1cY4nH6dJ0aGe"`
}

type revokeOthersOut struct {
	Body struct {
		Revoked int `json:"revoked" doc:"Number of sessions revoked." example:"2"`
	}
}

type authHeaderIn struct {
	Authorization string `header:"Authorization" doc:"Not used for session management: requests with an API key (Bearer ebk_…) are refused with 403." example:"Bearer ebk_3kQ9xT7mV2bL5pR8sW1cY4nH6dJ0aGe"`
}

// registerSessions mounts the own-sessions API on srv (OpenAPI via httpapi.Register).
func (s *Service) registerSessions(srv *httpapi.Server) {
	ext := httpapi.Phase("P0")
	problems := httpapi.Problems(errs.CodeUnauthenticated, errs.CodeSessionExpired, errs.CodeForbidden)
	httpapi.Register(srv, huma.Operation{
		OperationID: "listMySessions", Method: http.MethodGet, Path: "/v1/me/sessions",
		Summary: "List the caller's sessions", Tags: []string{"Me"},
		Description: "Lists every active sign-in session of the signed-in user (device, IP, sign-in method, last use), " +
			"marking the current one. Session cookie only: API keys get `403 forbidden`. Allowed before MFA " +
			"enrolment is finished. Session tokens are never returned. Read-only.",
		Metadata:   problems,
		Extensions: httpapi.WithScope(ext, "me:read"),
	}, s.listMySessions)
	httpapi.Register(srv, huma.Operation{
		OperationID: "revokeMySession", Method: http.MethodDelete, Path: "/v1/me/sessions/{sessionId}",
		Summary: "Revoke one of the caller's sessions", Tags: []string{"Me"},
		Description: "Signs one of the caller's sessions out immediately (the session row and its cache entries are " +
			"deleted, so the cookie is refused on its next request). Revoking the current session signs this " +
			"browser out. Session cookie only. `404` when the ID is not one of the caller's sessions; `503` when " +
			"the session cache could not be purged (retry). Answers `204`.",
		Metadata: httpapi.Problems(errs.CodeUnauthenticated, errs.CodeSessionExpired, errs.CodeForbidden, errs.CodeNotFound,
			errs.CodeServiceUnavailable),
		Extensions: httpapi.WithScope(ext, "me:write"),
	}, s.revokeMySession)
	httpapi.Register(srv, huma.Operation{
		OperationID: "revokeOtherSessions", Method: http.MethodPost, Path: "/v1/me/sessions/revoke-others",
		Summary: "Revoke every session except the current one", Tags: []string{"Me"},
		Description: "Signs the user out everywhere else (other browsers and devices), keeping the session making the " +
			"request. Session cookie only. Returns how many sessions were revoked; repeating it is harmless " +
			"(the second call revokes 0). `503` when a session cache entry could not be purged (retry).",
		Metadata:   httpapi.Problems(errs.CodeUnauthenticated, errs.CodeSessionExpired, errs.CodeForbidden, errs.CodeServiceUnavailable),
		Extensions: httpapi.WithScope(ext, "me:write"),
	}, s.revokeOtherSessions)
}

func (s *Service) listMySessions(ctx context.Context, in *authHeaderIn) (*sessionsListOut, error) {
	id, err := requireUserSession(ctx, in.Authorization)
	if err != nil {
		return nil, err
	}
	sessions, err := s.limen.ListSessions(ctx, id.AuthUserID)
	if err != nil {
		return nil, fmt.Errorf("auth: list sessions: %w", err)
	}
	out := &sessionsListOut{}
	out.Body.Data = make([]SessionView, 0, len(sessions))
	for _, sess := range sessions {
		out.Body.Data = append(out.Body.Data, viewSession(sess, id.SessionID))
	}
	return out, nil
}

func (s *Service) revokeMySession(ctx context.Context, in *sessionIDIn) (*struct{}, error) {
	id, err := requireUserSession(ctx, in.Authorization)
	if err != nil {
		return nil, err
	}
	sess, err := s.findOwnSession(ctx, id.AuthUserID, in.SessionID)
	if err != nil {
		return nil, err
	}
	if err := s.revokeAndPurge(ctx, id.AuthUserID, sess); err != nil {
		return nil, err
	}
	return nil, nil
}

func (s *Service) revokeOtherSessions(ctx context.Context, in *revokeOthersIn) (*revokeOthersOut, error) {
	id, err := requireUserSession(ctx, in.Authorization)
	if err != nil {
		return nil, err
	}
	sessions, err := s.limen.ListSessions(ctx, id.AuthUserID)
	if err != nil {
		return nil, fmt.Errorf("auth: list sessions: %w", err)
	}
	n := 0
	for _, sess := range sessions {
		if fmt.Sprint(sess.ID) == id.SessionID {
			continue
		}
		if err := s.revokeAndPurge(ctx, id.AuthUserID, sess); err != nil {
			return nil, err
		}
		n++
	}
	out := &revokeOthersOut{}
	out.Body.Revoked = n
	return out, nil
}

// requireUserSession returns the signed-in identity, or 403 when the caller is an API key, or 401 when
// there is no session.
func requireUserSession(ctx context.Context, authorization string) (Identity, error) {
	if looksLikeAPIKey(authorization) {
		return Identity{}, errs.New(errs.CodeForbidden, "Session management requires a signed-in user.")
	}
	return Require(ctx)
}

func looksLikeAPIKey(authorization string) bool {
	const prefix = "Bearer "
	if !strings.HasPrefix(authorization, prefix) {
		return false
	}
	tok := strings.TrimSpace(authorization[len(prefix):])
	return strings.HasPrefix(tok, APIKeyPrefix)
}

func (s *Service) findOwnSession(ctx context.Context, authUserID, sessionID string) (limen.Session, error) {
	sessions, err := s.limen.ListSessions(ctx, authUserID)
	if err != nil {
		return limen.Session{}, fmt.Errorf("auth: list sessions: %w", err)
	}
	for _, sess := range sessions {
		if fmt.Sprint(sess.ID) == sessionID {
			return sess, nil
		}
	}
	return limen.Session{}, errs.New(errs.CodeNotFound, "No such session.")
}

// revokeAndPurge deletes the session row and drops Limen's cache entries so the cookie is refused on
// the next request (keys: "{prefix}:session:t:{token}" and "{prefix}:session:u:{userID}").
func (s *Service) revokeAndPurge(ctx context.Context, authUserID string, sess limen.Session) error {
	if err := s.limen.RevokeSession(ctx, sess.Token); err != nil {
		return fmt.Errorf("auth: revoke session: %w", err)
	}
	return s.purgeSessionCache(ctx, authUserID, sess.Token)
}

// purgeSessionCache fails closed: a revoked session whose cache entry survived would keep working until the
// entry expires, so the caller gets an error (and can retry) instead of a false success.
func (s *Service) purgeSessionCache(ctx context.Context, authUserID, token string) error {
	if s.kv == nil || token == "" {
		return nil
	}
	for _, key := range []string{
		fmt.Sprintf("%s:session:t:%s", cacheKeyPrefix, token),
		fmt.Sprintf("%s:session:u:%s", cacheKeyPrefix, authUserID),
	} {
		if err := s.kv.CacheDelete(ctx, key); err != nil {
			s.log.WarnContext(ctx, "session cache purge failed; revocation reported as failed", "err", err)
			return errs.Wrap(err, errs.CodeServiceUnavailable, "auth.sessions.purge")
		}
	}
	return nil
}

func viewSession(sess limen.Session, currentID string) SessionView {
	method := MethodUnknown
	if raw := sess.Raw(); raw != nil {
		if m, ok := raw[methodField].(string); ok && m != "" {
			method = m
		}
	}
	v := SessionView{
		ID:           fmt.Sprint(sess.ID),
		Current:      fmt.Sprint(sess.ID) == currentID,
		CreatedAt:    sess.CreatedAt.UTC(),
		LastActiveAt: sess.LastAccess.UTC(),
		ExpiresAt:    sess.ExpiresAt.UTC(),
		Method:       method,
	}
	if sess.Metadata != nil {
		if ua, ok := sess.Metadata["user_agent"].(string); ok {
			v.UserAgent = ua
		}
		if ip, ok := sess.Metadata["ip_address"].(string); ok {
			v.IP = ip
		}
	}
	return v
}

// sessionsPathAllowed reports whether a non-MFA session may manage its own sessions (enrolment state).
func sessionsPathAllowed(method, path string) bool {
	path = strings.TrimRight(path, "/")
	switch {
	case method == http.MethodGet && path == "/v1/me/sessions":
		return true
	case method == http.MethodPost && path == "/v1/me/sessions/revoke-others":
		return true
	case method == http.MethodDelete && strings.HasPrefix(path, "/v1/me/sessions/"):
		return true
	}
	return false
}
