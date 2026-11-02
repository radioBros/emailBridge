package sync

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"time"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/bus"
	"github.com/radiobros/emailbridge/internal/egress"
	"github.com/radiobros/emailbridge/internal/events"
	"github.com/radiobros/emailbridge/internal/index"
	"github.com/radiobros/emailbridge/internal/ippool"
	"github.com/radiobros/emailbridge/internal/observability"
	"github.com/radiobros/emailbridge/internal/protolog"
	"github.com/radiobros/emailbridge/internal/store"
)

// Module runs the Manager on sync nodes. Accounts must be set by the wiring (internal/accounts adapter);
// IMAP connections dial through the egress dialer (mail purpose) when one is provided.
type Module struct {
	Accounts       Accounts
	Needs          PayloadNeeds
	Config         Config
	LeaseTTL       time.Duration
	Tick           time.Duration
	HandoffTimeout time.Duration
	MaxAccounts    int
	ConnectRate    int
	Tap            *protolog.Recorder
	Archive        Archiver
	// Outbound correlates bounces with outbound messages (P5.4); set by cmd/ wiring.
	Outbound OutboundLookup
	// Suppress records hard bounces and complaints (P5.2); set by cmd/ wiring.
	Suppress Suppressor
	// Backends run non-IMAP accounts (P6); set by cmd/ wiring.
	Backends map[string]Backend
	// Scorer scores inbound mail (P8.3); set by cmd/ wiring.
	Scorer SpamScorer
	// PEC handles PEC envelopes and receipts (P9); set by cmd/ wiring.
	PEC *PEC
	// Attachments indexes inbound attachment rows (P4.3 / W48); set by cmd/ wiring (lazy adapter).
	Attachments AttachmentIngest
	Log         *slog.Logger

	mgr    *Manager
	cancel context.CancelFunc
	done   chan struct{}
}

// Name implements app.Module.
func (m *Module) Name() string { return "sync" }

// Roles implements app.Module.
func (m *Module) Roles() []app.Role { return []app.Role{app.RoleSync} }

// Start implements app.Module.
func (m *Module) Start(ctx context.Context, a *app.App) error {
	if m.Accounts == nil {
		return errors.New("sync: no account source wired")
	}
	db, err := app.MustGet[*store.DB](a)
	if err != nil {
		return err
	}
	b, err := app.MustGet[*bus.Bus](a)
	if err != nil {
		return err
	}
	ev, err := app.MustGet[*events.Writer](a)
	if err != nil {
		return err
	}
	log := m.Log
	if log == nil {
		log = a.Log
	}
	mgr := &Manager{NodeID: a.NodeID, Bus: b, Accounts: m.Accounts, Index: &index.Index{DB: db}, Events: ev, Needs: m.Needs,
		Config: m.Config, Tap: m.Tap, Archive: m.Archive, Outbound: m.Outbound, Suppress: m.Suppress, Backends: m.Backends, Scorer: m.Scorer, PEC: m.PEC,
		Attachments: m.Attachments, LeaseTTL: m.LeaseTTL, Tick: m.Tick, HandoffTimeout: m.HandoffTimeout,
		MaxAccounts: m.MaxAccounts, ConnectRate: m.ConnectRate, Log: log}
	if rep, ok := app.Get[*observability.Reporter](a); ok && rep != nil {
		mgr.Reporter = rep
	}
	if d, ok := app.Get[*egress.Dialer](a); ok {
		mgr.Egress = d
		mgr.Dial = func(ctx context.Context, network, addr string) (net.Conn, error) {
			return d.DialContext(ctx, network, addr, egress.Mail)
		}
	}
	if pool, ok := app.Get[*ippool.Selector](a); ok {
		mgr.IPPool = pool
	}
	if mtr, ok := app.Get[*observability.Metrics](a); ok {
		mgr.Leases = mtr
		mtr.ObserveSync(a.NodeID, m.MaxAccounts, func() (accounts, unplaced int64) {
			st := mgr.Stats()
			return st.Accounts, st.Unplaced
		})
	}
	m.mgr = mgr
	app.Provide(a, mgr)
	rctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	m.cancel = cancel
	m.done = make(chan struct{})
	go func() { defer close(m.done); mgr.Run(rctx) }()
	return nil
}

// Stop implements app.Module: the node drains (its accounts move to other sync nodes through hand-offs,
// within the shutdown grace), then every remaining runner stops and its lease is released.
func (m *Module) Stop(ctx context.Context) error {
	if m.mgr != nil {
		m.mgr.Drain(ctx)
	}
	if m.cancel != nil {
		m.cancel()
		<-m.done
	}
	return nil
}
