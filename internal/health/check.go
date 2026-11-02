package health

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/radiobros/emailbridge/internal/accounts"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/mailbox/ondemand"
)

func (s *Service) liveCheck(ctx context.Context, a *accounts.Account) *LiveCheck {
	if providerAPI(a.Provider) {
		return s.liveCheckAPI(a)
	}
	if s.Accounts == nil || s.Pool == nil {
		return &LiveCheck{OK: false, Error: &LiveCheckError{
			Code: "internal_error", Kind: "temporary",
			Message: "Live IMAP check is not configured on this node.",
		}}
	}
	cctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()
	acc, err := s.Accounts.Load(cctx, a.ID)
	if err != nil {
		return liveErr(err, 0)
	}
	start := time.Now()
	lease, err := s.Pool.Acquire(cctx, acc)
	if err != nil {
		return liveErr(ondemand.AcquireErr(err), int(time.Since(start).Milliseconds()))
	}
	defer lease.Release()

	capsMap := lease.Client.Caps()
	caps := make([]string, 0, len(capsMap))
	for c, ok := range capsMap {
		if ok {
			caps = append(caps, c)
		}
	}
	sort.Strings(caps)
	// LIST proves the session is usable beyond login.
	if _, err := lease.Client.List(cctx); err != nil {
		lease.Discard()
		return liveErr(err, int(time.Since(start).Milliseconds()))
	}
	return &LiveCheck{OK: true, LatencyMs: int(time.Since(start).Milliseconds()), Capabilities: caps}
}

func (s *Service) liveCheckAPI(a *accounts.Account) *LiveCheck {
	if a.OAuth == nil {
		return &LiveCheck{OK: false, Error: &LiveCheckError{
			Code: "mail_auth_failed", Kind: "auth",
			Message: "No OAuth link is configured for this API account.",
		}}
	}
	if a.State == accounts.StateAuthError {
		code := "mail_auth_failed"
		if a.StateReason != nil && a.StateReason.Code != "" {
			code = a.StateReason.Code
		}
		msg := "Account is in authError"
		if a.StateReason != nil && a.StateReason.Message != "" {
			msg = a.StateReason.Message
		}
		return &LiveCheck{OK: false, Error: &LiveCheckError{Code: code, Kind: "auth", Message: msg}}
	}
	if a.State == accounts.StateConnectError || a.State == accounts.StateDisconnected {
		return &LiveCheck{OK: false, Error: &LiveCheckError{
			Code: "mail_connect_failed", Kind: "temporary",
			Message: "Account is not connected.",
		}}
	}
	return &LiveCheck{OK: true}
}

func liveErr(err error, latencyMs int) *LiveCheck {
	if err == nil {
		return &LiveCheck{OK: true, LatencyMs: latencyMs}
	}
	code := string(errs.CodeOf(err))
	if code == "" || code == string(errs.CodeInternalError) {
		msg := strings.ToLower(err.Error())
		switch {
		case strings.Contains(msg, "auth"):
			code = string(errs.CodeMailAuthFailed)
		case strings.Contains(msg, "tls") || strings.Contains(msg, "certificate") || strings.Contains(msg, "x509"):
			code = string(errs.CodeMailTLSFailed)
		case strings.Contains(msg, "timeout"):
			code = string(errs.CodeMailTimeout)
		default:
			code = string(errs.CodeMailConnectFailed)
		}
	}
	kind := string(errs.KindOf(err))
	if kind == "" {
		kind = "temporary"
	}
	msg := err.Error()
	if len(msg) > 240 {
		msg = msg[:240]
	}
	// Strip obvious secrets.
	msg = redactLive(msg)
	return &LiveCheck{
		OK: false, LatencyMs: latencyMs,
		Error: &LiveCheckError{Code: code, Kind: kind, Message: msg},
	}
}

func redactLive(s string) string {
	lower := strings.ToLower(s)
	if strings.Contains(lower, "password") || strings.Contains(lower, "secret") ||
		strings.Contains(lower, "token") {
		return "authentication or credential error (details redacted)"
	}
	return s
}
