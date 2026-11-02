package apikeys

import (
	"net/http"
	"net/netip"
	"path"
	"strings"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/httpapi"
)

func validateRestrictions(r *Restrictions) error {
	if r == nil {
		return nil
	}
	for _, cidr := range r.IPs {
		cidr = strings.TrimSpace(cidr)
		if _, err := netip.ParsePrefix(cidr); err != nil {
			if _, aerr := netip.ParseAddr(cidr); aerr == nil {
				continue
			}
			return errs.New(errs.CodeValidationFailed, "Invalid IP CIDR in restrictions.").WithSafe("cidr", cidr)
		}
	}
	for _, pat := range r.Referrers {
		if strings.TrimSpace(pat) == "" {
			return errs.New(errs.CodeValidationFailed, "Referrer patterns must be non-empty.")
		}
		if _, err := path.Match(pat, ""); err != nil {
			return errs.New(errs.CodeValidationFailed, "Invalid referrer pattern.").WithSafe("pattern", pat)
		}
	}
	return nil
}

func checkIP(ctxIP netip.Addr, r *Restrictions) error {
	if r == nil || len(r.IPs) == 0 {
		return nil
	}
	if !ctxIP.IsValid() {
		return errs.New(errs.CodeAPIKeyIPDenied, "This API key is restricted by client IP.")
	}
	client := ctxIP.Unmap()
	for _, cidr := range r.IPs {
		cidr = strings.TrimSpace(cidr)
		if p, err := netip.ParsePrefix(cidr); err == nil {
			if unmapPrefix(p).Contains(client) {
				return nil
			}
			continue
		}
		if addr, err := netip.ParseAddr(cidr); err == nil && addr.Unmap() == client {
			return nil
		}
	}
	return errs.New(errs.CodeAPIKeyIPDenied, "This API key is restricted by client IP.")
}

// unmapPrefix turns an IPv4-mapped IPv6 prefix into the corresponding IPv4 prefix so Contains
// matches dual-stack clients (::ffff:a.b.c.d) against IPv4 restrictions.
func unmapPrefix(p netip.Prefix) netip.Prefix {
	p = p.Masked()
	a := p.Addr()
	u := a.Unmap()
	if u == a {
		return p
	}
	bits := p.Bits()
	if bits >= 96 {
		bits -= 96
	} else {
		bits = 0
	}
	out, err := u.Prefix(bits)
	if err != nil {
		return p
	}
	return out.Masked()
}

func checkReferrer(r *http.Request, rest *Restrictions) error {
	if rest == nil || len(rest.Referrers) == 0 {
		return nil
	}
	ref := strings.TrimSpace(r.Header.Get("Referer"))
	if ref == "" {
		ref = strings.TrimSpace(r.Header.Get("Referrer"))
	}
	for _, pat := range rest.Referrers {
		ok, err := path.Match(pat, ref)
		if err == nil && ok {
			return nil
		}
	}
	return errs.New(errs.CodeAPIKeyReferrerDenied, "This API key is restricted by referrer.")
}

func clientIP(r *http.Request) netip.Addr {
	return httpapi.ClientIP(r.Context())
}
