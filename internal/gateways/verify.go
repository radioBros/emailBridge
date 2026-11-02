package gateways

import (
	"context"
	"errors"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/radiobros/emailbridge/internal/crypto"
	"github.com/radiobros/emailbridge/internal/egress"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/mailproto/smtpc"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

const verifyTimeout = 30 * time.Second
const verifyRate = 5.0

type tokenBucket struct {
	tokens float64
	last   time.Time
}

// Verify connects, says EHLO and AUTH; it does not send mail. API gateways are not supported.
func (s *Service) Verify(ctx context.Context, id string) (*VerifyResult, error) {
	if err := tenancy.Require(ctx, tenancy.PermSend); err != nil {
		return nil, err
	}
	g, err := s.Repo.get(ctx, id)
	if err != nil {
		return nil, err
	}
	if g.IsAPI() {
		return nil, errs.New(errs.CodeValidationFailed, "Verify is only supported for SMTP gateways.")
	}
	if org, ok := store.OrganizationFrom(ctx); ok {
		if err := s.takeVerifyToken(org); err != nil {
			return nil, err
		}
	}
	pass, err := s.Cipher.DecryptString(ctx, g.Auth.Pass, crypto.AAD{
		Collection: Collection, Field: "auth.pass", DocID: g.ID,
	})
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "gateways.decrypt")
	}
	start := time.Now()
	cctx, cancel := context.WithTimeout(ctx, verifyTimeout)
	defer cancel()
	addr := net.JoinHostPort(g.Host, strconv.Itoa(g.Port))
	opts := smtpc.SessionOptions{
		Addr: addr, TLS: smtpc.TLSMode(g.TLS), Username: g.Auth.User, Password: pass,
		EHLO:           firstNonEmpty(g.EHLOName, s.EHLO, "localhost"),
		ConnectTimeout: verifyTimeout, CommandTimeout: verifyTimeout,
		InsecureSkipVerify: g.IgnoreCertErrors,
	}
	if s.Egress != nil {
		proxy, local := g.Proxy, g.LocalAddress
		opts.Dial = func(ctx context.Context, network, address string) (net.Conn, error) {
			return s.Egress.DialContextOpts(ctx, network, address, egress.Mail, egress.DialOptions{
				ProxyURL: proxy, LocalAddr: local,
			})
		}
	}
	sess, err := smtpc.Open(cctx, opts)
	if err != nil {
		s.audit(ctx, "smtpGateway.verified", id, nil)
		return verifyErr(err, start), nil
	}
	defer func() { _ = sess.Close() }()
	caps := []string{}
	for _, ext := range []string{"PIPELINING", "8BITMIME", "SMTPUTF8", "SIZE", "STARTTLS", "AUTH", "DSN"} {
		if sess.Supports(ext) {
			caps = append(caps, ext)
		}
	}
	s.audit(ctx, "smtpGateway.verified", id, nil)
	return &VerifyResult{OK: true, LatencyMs: int(time.Since(start).Milliseconds()), Capabilities: caps}, nil
}

func verifyErr(err error, start time.Time) *VerifyResult {
	code := string(errs.CodeOf(err))
	detail := redactServerText(err.Error())
	if errors.Is(err, egress.ErrBlocked) {
		code = string(errs.CodeMailConnectFailed)
		detail = "destination blocked by egress policy"
	}
	return &VerifyResult{
		OK: false, LatencyMs: int(time.Since(start).Milliseconds()),
		Code: code, Detail: detail,
	}
}

func redactServerText(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 512 {
		s = s[:512]
	}
	lower := strings.ToLower(s)
	for _, bad := range []string{"password", "pass=", "secret"} {
		if strings.Contains(lower, bad) {
			return "[redacted]"
		}
	}
	return s
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

var (
	verifyMu sync.Mutex
	verifyRL = map[string]*tokenBucket{}
)

func (s *Service) takeVerifyToken(org string) error {
	verifyMu.Lock()
	defer verifyMu.Unlock()
	b := verifyRL[org]
	now := time.Now()
	if b == nil {
		b = &tokenBucket{tokens: verifyRate, last: now}
		verifyRL[org] = b
	}
	elapsed := now.Sub(b.last).Minutes()
	b.tokens += elapsed * verifyRate
	if b.tokens > verifyRate {
		b.tokens = verifyRate
	}
	b.last = now
	if b.tokens < 1 {
		return errs.New(errs.CodeRateLimited, "Gateway verify is limited to 5 requests per minute per organisation.")
	}
	b.tokens--
	return nil
}
