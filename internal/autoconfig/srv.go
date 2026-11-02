package autoconfig

import (
	"context"
	"net"
	"strings"
)

func (s *Service) trySRV(ctx context.Context, domain string) *Result {
	cctx, cancel := context.WithTimeout(ctx, dnsTO)
	defer cancel()
	out := &Result{Source: SourceSRV}
	if srv := firstSRV(cctx, s.DNS, "imaps", "tcp", domain); srv != nil {
		out.IMAP = &Server{Host: trimDot(srv.Target), Port: int(srv.Port), TLS: tlsImplicit, User: userEmail}
	} else if srv := firstSRV(cctx, s.DNS, "imap", "tcp", domain); srv != nil {
		tlsMode := tlsStartTLS
		if srv.Port == 993 {
			tlsMode = tlsImplicit
		}
		out.IMAP = &Server{Host: trimDot(srv.Target), Port: int(srv.Port), TLS: tlsMode, User: userEmail}
	}
	if srv := firstSRV(cctx, s.DNS, "submissions", "tcp", domain); srv != nil {
		out.SMTP = &Server{Host: trimDot(srv.Target), Port: int(srv.Port), TLS: tlsImplicit, User: userEmail}
	} else if srv := firstSRV(cctx, s.DNS, "submission", "tcp", domain); srv != nil {
		tlsMode := tlsStartTLS
		if srv.Port == 465 {
			tlsMode = tlsImplicit
		}
		out.SMTP = &Server{Host: trimDot(srv.Target), Port: int(srv.Port), TLS: tlsMode, User: userEmail}
	}
	if out.IMAP == nil && out.SMTP == nil {
		return nil
	}
	return out
}

func firstSRV(ctx context.Context, r Resolver, service, proto, domain string) *net.SRV {
	if r == nil {
		return nil
	}
	_, records, err := r.LookupSRV(ctx, service, proto, domain)
	if err != nil || len(records) == 0 {
		return nil
	}
	return records[0]
}

func trimDot(h string) string {
	return strings.TrimSuffix(h, ".")
}
