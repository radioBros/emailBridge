package ai

import (
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/radiobros/emailbridge/internal/egress"
	"github.com/radiobros/emailbridge/internal/errs"
)

const providerHTTPTimeout = 60 * time.Second

// secureDefaultDialer refuses private/loopback/link-local/metadata (W35).
func secureDefaultDialer() *egress.Dialer {
	return egress.NewDialer(egress.Policy{BlockLinkLocal: true, BlockPrivate: true})
}

func (s *Service) httpDoer() HTTPDoer {
	if s.HTTP != nil {
		return s.HTTP
	}
	d := s.Egress
	if d == nil {
		d = secureDefaultDialer()
	}
	return d.HTTPClient(providerHTTPTimeout)
}

func hostAllowListed(d *egress.Dialer, host string) bool {
	if d == nil || host == "" {
		return false
	}
	h := strings.ToLower(strings.TrimSuffix(host, "."))
	for _, a := range d.Policy().AllowHosts {
		if a == h {
			return true
		}
	}
	return false
}

// checkBaseURL enforces https (or http for egress AllowHosts) before dialling.
func (s *Service) checkBaseURL(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.Scheme == "" {
		return errs.New(errs.CodeValidationFailed, "AI baseUrl is not a valid absolute URL.")
	}
	scheme := strings.ToLower(u.Scheme)
	host := u.Hostname()
	switch scheme {
	case "https":
		return nil
	case "http":
		d := s.Egress
		if d == nil {
			d = secureDefaultDialer()
		}
		if hostAllowListed(d, host) {
			return nil
		}
		return errs.New(errs.CodeValidationFailed,
			"AI baseUrl must use https unless the host is on the egress allow-list.")
	default:
		return errs.New(errs.CodeValidationFailed, "AI baseUrl scheme must be https (or http on an allow-listed host).").
			WithSafe("scheme", scheme)
	}
}

func mapProviderDialErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, egress.ErrBlocked) {
		return errs.New(errs.CodeEndpointURLBlocked, "The AI provider URL is blocked by the egress policy.")
	}
	return err
}
