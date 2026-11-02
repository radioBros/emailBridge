package messages

import (
	"context"
	"strings"

	"github.com/radiobros/emailbridge/internal/mailbox"
	"github.com/radiobros/emailbridge/internal/mailbox/ondemand"
	"github.com/radiobros/emailbridge/internal/mailproto/imapc"
	ebsync "github.com/radiobros/emailbridge/internal/sync"
)

// OpsFactory builds a mailbox.Ops for an account (set by wiring / tests for gmail/graph).
type OpsFactory func(ctx context.Context, acc *ebsync.Account) (mailbox.Ops, error)

func (s *Service) opsFor(ctx context.Context, acc *ebsync.Account) (mailbox.Ops, error) {
	switch strings.ToLower(strings.TrimSpace(acc.Provider)) {
	case "gmail", "graph":
		if s.Providers != nil {
			return s.Providers(ctx, acc)
		}
		return nil, mailbox.Unsupported("Mailbox API for this provider is not configured on this node.")
	default:
		return &mailbox.IMAPOps{Account: acc, Dial: s.imapDial}, nil
	}
}

func (s *Service) imapDial(ctx context.Context, acc *ebsync.Account) (*imapc.Client, func(discard bool), error) {
	lease, err := s.Pool.Acquire(ctx, acc)
	if err != nil {
		return nil, nil, ondemand.AcquireErr(err)
	}
	return lease.Client, func(discard bool) {
		if discard {
			lease.Discard()
			return
		}
		lease.Release()
	}, nil
}

func isAPIProvider(provider string) bool {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "gmail", "graph":
		return true
	default:
		return false
	}
}
