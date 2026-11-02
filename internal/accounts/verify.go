package accounts

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/radiobros/emailbridge/internal/egress"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/mailproto/imapc"
	"github.com/radiobros/emailbridge/internal/mailproto/smtpc"
	"github.com/radiobros/emailbridge/internal/store"
	ebsync "github.com/radiobros/emailbridge/internal/sync"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

const verifyTimeout = 30 * time.Second
const verifyRate = 5.0 // per minute per organisation

// Verify tests IMAP and/or SMTP settings without saving (S2 §4.2).
func (s *Service) Verify(ctx context.Context, in VerifyRequest) (*VerifyResult, error) {
	if err := tenancy.Require(ctx, tenancy.PermAccountsWrite); err != nil {
		return nil, err
	}
	if in.IMAP == nil && in.SMTP == nil {
		return nil, errs.New(errs.CodeValidationFailed, "At least one of imap or smtp is required.")
	}
	if in.IMAP != nil {
		if err := validateIMAPInput(*in.IMAP, true); err != nil {
			return nil, err
		}
	}
	if in.SMTP != nil {
		if err := validateSMTPInput(*in.SMTP, true); err != nil {
			return nil, err
		}
	}
	if org, ok := store.OrganizationFrom(ctx); ok {
		if err := s.takeVerifyToken(ctx, org); err != nil {
			return nil, err
		}
	}

	out := &VerifyResult{}
	var wg sync.WaitGroup
	var mu sync.Mutex
	skipTLS := in.IgnoreCertErrors
	if in.IMAP != nil {
		wg.Add(1)
		go func(cfg IMAPConfigInput) {
			defer wg.Done()
			r := s.verifyIMAP(ctx, cfg, skipTLS)
			mu.Lock()
			out.IMAP = &r
			mu.Unlock()
		}(*in.IMAP)
	}
	if in.SMTP != nil {
		wg.Add(1)
		go func(cfg SMTPConfigInput) {
			defer wg.Done()
			r := s.verifySMTP(ctx, cfg, skipTLS)
			mu.Lock()
			out.SMTP = &r
			mu.Unlock()
		}(*in.SMTP)
	}
	wg.Wait()
	return out, nil
}

func (s *Service) takeVerifyToken(ctx context.Context, org string) error {
	s.verifyMu.Lock()
	defer s.verifyMu.Unlock()
	if s.verifyRL == nil {
		s.verifyRL = map[string]*tokenBucket{}
	}
	b := s.verifyRL[org]
	now := time.Now()
	if b == nil {
		b = &tokenBucket{tokens: verifyRate, last: now}
		s.verifyRL[org] = b
	}
	elapsed := now.Sub(b.last).Minutes()
	b.tokens += elapsed * verifyRate
	if b.tokens > verifyRate {
		b.tokens = verifyRate
	}
	b.last = now
	if b.tokens < 1 {
		info := errs.TokenBucketInfo(verifyRate, verifyRate, b.tokens)
		httpapi.NoteRateLimit(ctx, info)
		return errs.New(errs.CodeRateLimited, "Account verify is limited to 5 requests per minute per organisation.").WithRate(info)
	}
	b.tokens--
	httpapi.NoteRateLimit(ctx, errs.TokenBucketInfo(verifyRate, verifyRate, b.tokens))
	return nil
}

func (s *Service) verifyIMAP(ctx context.Context, in IMAPConfigInput, insecure bool) VerifyProtocolResult {
	start := time.Now()
	cctx, cancel := context.WithTimeout(ctx, verifyTimeout)
	defer cancel()
	addr := net.JoinHostPort(in.Host, fmt.Sprintf("%d", in.Port))
	id := ebsync.MergeIMAPID(s.IMAPID, ebsync.AccountIMAPID(in.IDName, in.IDVersion, in.IDVendor, in.IDSupportURL))
	if len(id) == 0 {
		id = map[string]string{"name": "EmailBridge"}
	}
	cl, err := imapc.Dial(cctx, imapc.Options{
		Addr: addr, TLS: imapc.TLSMode(in.TLS), Username: in.Auth.User, Password: in.Auth.Pass,
		Dial: s.dialMail, CommandTimeout: verifyTimeout, InsecureSkipVerify: insecure,
		ID: id, DisableRev2: in.DisableRev2,
	})
	if err != nil {
		return verifyErr(err, start)
	}
	defer func() { _ = cl.Close() }()
	list, err := cl.List(cctx)
	if err != nil {
		return verifyErr(err, start)
	}
	caps := make([]string, 0, len(cl.Caps()))
	for c, ok := range cl.Caps() {
		if ok {
			caps = append(caps, c)
		}
	}
	sort.Strings(caps)
	special := map[string]string{}
	for _, m := range list {
		if m.SpecialUse == "" {
			continue
		}
		key := strings.TrimPrefix(strings.ToLower(m.SpecialUse), `\`)
		special[key] = m.Name
	}
	return VerifyProtocolResult{
		OK: true, LatencyMs: int(time.Since(start).Milliseconds()),
		Capabilities: caps, SpecialUse: special,
	}
}

func (s *Service) verifySMTP(ctx context.Context, in SMTPConfigInput, insecure bool) VerifyProtocolResult {
	start := time.Now()
	cctx, cancel := context.WithTimeout(ctx, verifyTimeout)
	defer cancel()
	addr := net.JoinHostPort(in.Host, fmt.Sprintf("%d", in.Port))
	sess, err := smtpc.Open(cctx, smtpc.SessionOptions{
		Addr: addr, TLS: smtpc.TLSMode(in.TLS), Username: in.Auth.User, Password: in.Auth.Pass,
		Dial: s.dialMail, ConnectTimeout: verifyTimeout, CommandTimeout: verifyTimeout,
		InsecureSkipVerify: insecure,
	})
	if err != nil {
		return verifyErr(err, start)
	}
	defer func() { _ = sess.Close() }()
	caps := []string{}
	for _, ext := range []string{"PIPELINING", "8BITMIME", "SMTPUTF8", "SIZE", "STARTTLS", "AUTH"} {
		if sess.Supports(ext) {
			caps = append(caps, ext)
		}
	}
	return VerifyProtocolResult{
		OK: true, LatencyMs: int(time.Since(start).Milliseconds()), Capabilities: caps,
	}
}

func verifyErr(err error, start time.Time) VerifyProtocolResult {
	code := string(errs.CodeOf(err))
	detail := redactServerText(err.Error())
	if errors.Is(err, egress.ErrBlocked) {
		code = string(errs.CodeMailConnectFailed)
		detail = "destination blocked by egress policy"
	}
	return VerifyProtocolResult{
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
