package sysmail

import (
	"context"
	"strings"
	"sync/atomic"

	"github.com/radiobros/emailbridge/internal/store"
)

// AccountSender submits system mail through a connected account via the send pipeline.
// Implemented in cmd/ over submit.Service (raw submit as the system caller). Do not import
// internal/submit from this package — that would create an import cycle.
type AccountSender interface {
	SendSystem(ctx context.Context, accountID, from, to string, raw []byte) (outboundID string, err error)
}

// AccountChecker validates EB_SYSMAIL_ACCOUNT_ID at startup (missing / deleted / disabled → error).
// Implemented in cmd/ over accounts.Service.
type AccountChecker interface {
	Check(ctx context.Context, accountID string) error
}

// SetAccount wires the submit adapter and optional account probe (for late cmd/ binding after Start).
func (s *Service) SetAccount(sender AccountSender, check AccountChecker) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.o.Account = sender
	s.o.CheckAccount = check
}

// ActivateAccount validates mode=account configuration. On failure it logs a clear error and leaves
// account mode effectively feature_disabled (never panics). Safe to call from Module.Start or a
// late BindAccount hook once AccountSender is injected.
func (s *Service) ActivateAccount(ctx context.Context) bool {
	if s == nil {
		return false
	}
	mode := strings.ToLower(strings.TrimSpace(s.o.Mode))
	if mode != ModeAccount {
		s.setAccountOK(false)
		return false
	}
	id := strings.TrimSpace(s.o.AccountID)
	if id == "" {
		s.log.Error("system mail mode=account requires EB_SYSMAIL_ACCOUNT_ID; falling back to feature_disabled")
		s.setAccountOK(false)
		return false
	}
	if !store.IsID(store.PrefixAccount, id) {
		s.log.Error("system mail EB_SYSMAIL_ACCOUNT_ID is not a valid account id; falling back to feature_disabled",
			"accountId", id)
		s.setAccountOK(false)
		return false
	}
	s.mu.Lock()
	sender := s.o.Account
	check := s.o.CheckAccount
	s.mu.Unlock()
	if sender == nil {
		s.log.Error("system mail account sender not configured; falling back to feature_disabled",
			"accountId", id)
		s.setAccountOK(false)
		return false
	}
	if check != nil {
		if err := check.Check(ctx, id); err != nil {
			s.log.Error("system mail account missing, deleted or disabled; falling back to feature_disabled",
				"err", err, "accountId", id)
			s.setAccountOK(false)
			return false
		}
	}
	s.setAccountOK(true)
	s.log.Info("system mail through connected account enabled", "accountId", id)
	return true
}

func (s *Service) setAccountOK(ok bool) {
	var v int32
	if ok {
		v = 1
	}
	atomic.StoreInt32(&s.accountOK, v)
}

func (s *Service) accountEnabled() bool {
	return atomic.LoadInt32(&s.accountOK) == 1
}

func (s *Service) accountSender() AccountSender {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.o.Account
}
