package auth

import (
	"net/http"
	"time"

	"github.com/radiobros/emailbridge/internal/egress"
)

const oidcHTTPTimeout = 30 * time.Second

func secureDefaultDialer() *egress.Dialer {
	return egress.NewDialer(egress.Policy{BlockLinkLocal: true, BlockPrivate: true})
}

// httpClient returns the OIDC / OAuth2 HTTP client (egress when wired).
func (s *Service) httpClient() *http.Client {
	if s.HTTP != nil {
		return s.HTTP
	}
	d := s.Egress
	if d == nil {
		d = secureDefaultDialer()
	}
	return d.HTTPClient(oidcHTTPTimeout)
}
