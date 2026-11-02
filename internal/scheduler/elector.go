// Package scheduler runs cluster-wide duties on exactly one node: a leader elected through a bus lease
// (S4 §7). This file is the elector; the duties (scheduled sends, retention sweeps, jobs) are added by the
// work packages that own them.
package scheduler

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/radiobros/emailbridge/internal/bus"
)

// LeaderKey is the lease key of the scheduler leader.
const LeaderKey = "leader.scheduler"

// Leases is the part of *bus.Bus the elector needs (a fake in tests).
type Leases interface {
	Claim(ctx context.Context, key string, value []byte, ttl time.Duration) (uint64, error)
	Renew(ctx context.Context, key string, value []byte, epoch uint64, ttl time.Duration) (uint64, error)
	Release(ctx context.Context, key string, epoch uint64) error
}

// Elector competes for leadership and runs Lead while it is leader.
type Elector struct {
	Leases Leases
	NodeID string
	TTL    time.Duration // EB_LEADER_TTL (default 15 s); renewed every TTL/3
	Log    *slog.Logger
	// Lead runs while this node is leader; its ctx is cancelled when leadership is lost or Run stops.
	Lead func(ctx context.Context)
	// OnChange is called with true when leadership is gained and false when it is lost (metrics, logs).
	OnChange func(leader bool)
}

func (e *Elector) ttl() time.Duration {
	if e.TTL > 0 {
		return e.TTL
	}
	return 15 * time.Second
}

// Run loops until ctx ends: try to claim, lead while renewals succeed, release on exit.
func (e *Elector) Run(ctx context.Context) {
	log := e.Log
	if log == nil {
		log = slog.Default()
	}
	ttl := e.ttl()
	value := []byte(e.NodeID)
	retry := time.NewTicker(ttl / 3)
	defer retry.Stop()
	for {
		epoch, err := e.Leases.Claim(ctx, LeaderKey, value, ttl)
		if err == nil {
			e.lead(ctx, log, epoch, value, ttl)
		} else if !errors.Is(err, bus.ErrLeaseHeld) && ctx.Err() == nil {
			log.Warn("leader claim failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-retry.C:
		}
	}
}

func (e *Elector) lead(ctx context.Context, log *slog.Logger, epoch uint64, value []byte, ttl time.Duration) {
	lctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	if e.OnChange != nil {
		e.OnChange(true)
	}
	log.Info("became scheduler leader", "epoch", epoch)
	go func() {
		defer close(done)
		if e.Lead != nil {
			e.Lead(lctx)
		}
	}()
	t := time.NewTicker(ttl / 3)
	defer t.Stop()
	defer func() {
		cancel()
		<-done
		if e.OnChange != nil {
			e.OnChange(false)
		}
	}()
	for {
		select {
		case <-ctx.Done():
			cancel() // stop the duties before the lease is released, so no other node runs them concurrently
			<-done
			rctx, rcancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			_ = e.Leases.Release(rctx, LeaderKey, epoch)
			rcancel()
			log.Info("released scheduler leadership")
			return
		case <-t.C:
			next, err := e.Leases.Renew(ctx, LeaderKey, value, epoch, ttl)
			if err != nil {
				if ctx.Err() != nil {
					continue // shutting down; the Done branch releases
				}
				log.Warn("lost scheduler leadership", "err", err)
				return
			}
			epoch = next
		}
	}
}
