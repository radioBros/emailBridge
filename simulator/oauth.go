package simulator

import (
	"strings"

	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-sasl"
	"github.com/emersion/go-smtp"

	"github.com/radiobros/emailbridge/simulator/mem"
)

// OAuth token registry (P6.1): tokens the simulator accepts for XOAUTH2 / OAUTHBEARER.

func (sim *Simulator) ensureOAuth() {
	sim.oauthOnce.Do(func() {
		sim.oauthTokens = make(map[string]string)
	})
}

// SetOAuthToken registers an access token for username (IMAP + SMTP).
func (sim *Simulator) SetOAuthToken(username, accessToken string) {
	sim.ensureOAuth()
	sim.oauthMu.Lock()
	defer sim.oauthMu.Unlock()
	sim.oauthTokens[strings.ToLower(username)] = accessToken
}

// ClearOAuthToken removes a registered token.
func (sim *Simulator) ClearOAuthToken(username string) {
	sim.ensureOAuth()
	sim.oauthMu.Lock()
	defer sim.oauthMu.Unlock()
	delete(sim.oauthTokens, strings.ToLower(username))
}

func (sim *Simulator) oauthTokenOK(username, token string) bool {
	sim.ensureOAuth()
	sim.oauthMu.Lock()
	defer sim.oauthMu.Unlock()
	want, ok := sim.oauthTokens[strings.ToLower(username)]
	return ok && want != "" && want == token
}

func (sim *Simulator) hasOAuthTokens() bool {
	sim.ensureOAuth()
	sim.oauthMu.Lock()
	defer sim.oauthMu.Unlock()
	return len(sim.oauthTokens) > 0
}

// AuthenticateMechanisms implements imapserver.SessionSASL when OAuth tokens are registered.
func (s *faultSession) AuthenticateMechanisms() []string {
	if s.sim == nil || !s.sim.hasOAuthTokens() {
		return nil
	}
	return []string{"XOAUTH2", sasl.OAuthBearer}
}

// Authenticate implements imapserver.SessionSASL.
func (s *faultSession) Authenticate(mech string) (sasl.Server, error) {
	switch mech {
	case "XOAUTH2":
		return newXOAuth2Server(s.acceptOAuth), nil
	case sasl.OAuthBearer:
		return sasl.NewOAuthBearerServer(func(opts sasl.OAuthBearerOptions) *sasl.OAuthBearerError {
			if err := s.acceptOAuth(opts.Username, opts.Token); err != nil {
				return &sasl.OAuthBearerError{Status: "invalid_token", Schemes: "bearer"}
			}
			return nil
		}), nil
	default:
		return nil, imapserver.ErrAuthFailed
	}
}

func (s *faultSession) acceptOAuth(username, token string) error {
	if s.sim == nil || !s.sim.oauthTokenOK(username, token) {
		return imapserver.ErrAuthFailed
	}
	u := s.sim.store.User(username)
	if u == nil {
		u = mem.NewUser(username, "")
		s.sim.store.AddUser(u)
		_ = u.Create("INBOX", nil)
	}
	inner := mem.NewUserSession(u)
	s.inner = inner
	s.user = username
	s.state = s.sim.stateOf(username)
	s.authed = true
	s.state.sessionOpened()
	return nil
}

// smtpOAuthMechanisms returns extra SMTP mechs when tokens are registered.
func (sim *Simulator) smtpOAuthMechanisms() []string {
	if !sim.hasOAuthTokens() {
		return nil
	}
	return []string{"XOAUTH2", sasl.OAuthBearer}
}

func (s *smtpSession) authXOAuth2() (sasl.Server, error) {
	return newXOAuth2Server(s.acceptOAuth), nil
}

func (s *smtpSession) authOAuthBearer() (sasl.Server, error) {
	return sasl.NewOAuthBearerServer(func(opts sasl.OAuthBearerOptions) *sasl.OAuthBearerError {
		if err := s.acceptOAuth(opts.Username, opts.Token); err != nil {
			return &sasl.OAuthBearerError{Status: "invalid_token", Schemes: "bearer"}
		}
		return nil
	}), nil
}

func (s *smtpSession) acceptOAuth(username, token string) error {
	if s.sim == nil || !s.sim.oauthTokenOK(username, token) {
		s.logStage("auth", 535, "oauth failed")
		return smtp.ErrAuthFailed
	}
	if s.sim.store.User(username) == nil {
		u := mem.NewUser(username, "")
		s.sim.store.AddUser(u)
		_ = u.Create("INBOX", nil)
	}
	s.user = username
	s.authed = true
	s.logStage("auth", 235, "ok")
	return nil
}

type xoauth2Server struct {
	done bool
	auth func(user, token string) error
	err  error
}

func newXOAuth2Server(auth func(user, token string) error) sasl.Server {
	return &xoauth2Server{auth: auth}
}

func (a *xoauth2Server) Next(response []byte) (challenge []byte, done bool, err error) {
	if a.err != nil {
		return nil, true, a.err
	}
	if a.done {
		return nil, true, sasl.ErrUnexpectedClientResponse
	}
	if response == nil {
		return []byte{}, false, nil
	}
	a.done = true
	user, token, ok := parseXOAuth2(response)
	if !ok {
		a.err = imapserver.ErrAuthFailed
		return []byte(`{"status":"invalid_request"}`), false, nil
	}
	if err := a.auth(user, token); err != nil {
		a.err = err
		return []byte(`{"status":"invalid_token"}`), false, nil
	}
	return nil, true, nil
}

func parseXOAuth2(b []byte) (user, token string, ok bool) {
	// user=…\x01auth=Bearer …\x01\x01
	parts := strings.Split(string(b), "\x01")
	for _, p := range parts {
		if strings.HasPrefix(p, "user=") {
			user = strings.TrimPrefix(p, "user=")
		}
		lower := strings.ToLower(p)
		if strings.HasPrefix(lower, "auth=bearer ") {
			token = p[len("auth=bearer "):]
		}
	}
	return user, token, user != "" && token != ""
}

// Ensure SessionSASL compile-time check.
var _ imapserver.SessionSASL = (*faultSession)(nil)
