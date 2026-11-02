package sync

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/radiobros/emailbridge/internal/bus"
	"github.com/radiobros/emailbridge/internal/egress"
	"github.com/radiobros/emailbridge/internal/events"
	"github.com/radiobros/emailbridge/internal/index"
	"github.com/radiobros/emailbridge/internal/ippool"
	"github.com/radiobros/emailbridge/internal/mailproto/imapc"
	"github.com/radiobros/emailbridge/internal/protolog"
)

// LeasePrefix is the account lease key prefix (S4 §5: acc.<accountId>).
const LeasePrefix = "acc."

// CmdHandoffReady is sent by a hand-off target to the current owner once it has pre-connected the account
// (S4 §6). It is a request: the owner answers after it stopped the account and released the lease.
const CmdHandoffReady = "handoffReady"

// handoffMarker is appended to the lease value while the owner hands the account to another node.
const handoffMarker = ";handoff="

// Bus is the part of *bus.Bus the manager needs.
type Bus interface {
	Claim(ctx context.Context, key string, value []byte, ttl time.Duration) (uint64, error)
	Renew(ctx context.Context, key string, value []byte, epoch uint64, ttl time.Duration) (uint64, error)
	Release(ctx context.Context, key string, epoch uint64) error
	LeaseHolder(ctx context.Context, key string) (*bus.LeaseInfo, error)
	LiveNodes(ctx context.Context) ([]bus.NodeInfo, error)
	SetDraining(ctx context.Context, on bool) error
	SubscribeCore(subject string, h func(subject string, data []byte)) (func() error, error)
	SubscribeCoreReply(subject string, h func(subject string, data []byte) []byte) (func() error, error)
	RequestCore(ctx context.Context, subject string, data []byte) ([]byte, error)
	SetAccountsProvider(fn func() int)
}

// CapacityMarker is optionally implemented by Accounts: it records that no node can take an account
// (state disconnected, reason capacity, S4 §6 connection budget).
type CapacityMarker interface {
	Unplaced(ctx context.Context, accountID string) error
}

// CommandMessage is the payload of eb.node.<nodeId>.sync.
type CommandMessage struct {
	AccountID string `json:"accountId"`
	Command
}

// Manager places accounts on the sync nodes (rendezvous hashing over the live, non-draining sync nodes in
// eb-nodes, S4 §6), holds their leases and runs one runner per owned account. When placement moves an
// account, the owner hands it off: it marks the lease, the target pre-connects and asks for the account,
// the owner stops (flushing its writes) and releases, and the target claims it at once and syncs from the
// stored baselines. A target that never asks gets the account by plain release after HandoffTimeout.
type Manager struct {
	NodeID   string
	Bus      Bus
	Accounts Accounts
	Index    *index.Index
	Events   *events.Writer
	Needs    PayloadNeeds
	Config   Config
	Dial     imapc.DialFunc
	// Egress, when set, dials with per-account proxy / local address / IP pool (W57). Dial is the fallback.
	Egress *egress.Dialer
	// IPPool selects a local bind address when the account has no singular localAddress (W57).
	IPPool  *ippool.Selector
	Tap     *protolog.Recorder
	Archive Archiver
	// Outbound correlates bounces and complaints with outbound messages (P5.4); nil = extracted IDs only.
	Outbound OutboundLookup
	// Suppress records hard bounces and complaints on suppression lists (P5.2); nil = off.
	Suppress Suppressor
	// Backends run accounts whose provider is not IMAP (P6.2 gmail, P6.3 graph), keyed by provider.
	Backends map[string]Backend
	// Scorer scores inbound mail (P8.3 Rspamd); nil = off.
	Scorer SpamScorer
	// Attachments indexes attachment rows, extracts to S3 and scans (P4.3 / W48); nil = off.
	Attachments AttachmentIngest
	// Reporter receives recovered runner panics (S12 §12, W51); nil = log only.
	Reporter PanicReporter
	// PEC handles PEC envelopes and receipts (P9); nil = off.
	PEC            *PEC
	LeaseTTL       time.Duration // EB_LEASE_TTL (30 s)
	Tick           time.Duration // placement loop (5 s)
	MaxAccounts    int           // EB_SYNC_MAX_ACCOUNTS (0 = unlimited)
	ConnectRate    int           // EB_SYNC_CONNECT_RATE: new claims per tick (0 = 20)
	HandoffTimeout time.Duration // how long an owner waits for the target's handoffReady (default 30 s)
	Log            *slog.Logger
	// Leases, when set, records claim / lost / released / handed_off for Prometheus (S12 §9).
	Leases LeaseObserver

	mu       sync.Mutex
	held     map[string]*owned
	marked   map[string]time.Time // capacity marks, at most one per account per LeaseTTL
	taking   map[string]bool      // hand-off take-overs in flight
	metrics  metrics
	draining atomic.Bool
	wake     chan struct{}
	renewNow chan struct{}
	takeSem  chan struct{}
	bg       sync.WaitGroup // background stops and take-overs; Run waits for them before returning
}

// renewWorkers bounds the concurrent lease renewals of one node: 2,000 accounts at a few milliseconds per
// CAS renew in well under a second, independent of the placement tick.
const renewWorkers = 16

// takeOverWorkers bounds concurrent hand-off take-overs (each pre-connects one IMAP session).
const takeOverWorkers = 16

func (m *Manager) leaseChange(kind string) {
	if m.Leases != nil {
		m.Leases.LeaseChange(kind)
	}
}

type owned struct {
	// leaseMu serialises Renew and Release of this lease, so a renewal racing a stop can never leave the
	// lease re-extended under a revision the release no longer knows. epoch and stopped are guarded by it.
	leaseMu   sync.Mutex
	epoch     uint64
	stopped   bool
	renewedAt time.Time
	cancel    context.CancelFunc
	done      chan struct{}
	cmds      chan Command
	handoffTo string // target node while handing off
	handoffAt time.Time
}

func (m *Manager) defaults() {
	if m.LeaseTTL <= 0 {
		m.LeaseTTL = 30 * time.Second
	}
	if m.Tick <= 0 {
		m.Tick = 5 * time.Second
	}
	if m.ConnectRate <= 0 {
		m.ConnectRate = 20
	}
	if m.HandoffTimeout == 0 {
		m.HandoffTimeout = 30 * time.Second
	}
	if m.Log == nil {
		m.Log = slog.Default()
	}
	if m.Needs == nil {
		m.Needs = NoNeeds{}
	}
	m.mu.Lock()
	if m.held == nil {
		m.held = map[string]*owned{}
	}
	if m.marked == nil {
		m.marked = map[string]time.Time{}
	}
	if m.wake == nil {
		m.wake = make(chan struct{}, 1)
	}
	if m.renewNow == nil {
		m.renewNow = make(chan struct{}, 1)
	}
	if m.taking == nil {
		m.taking = map[string]bool{}
	}
	if m.takeSem == nil {
		m.takeSem = make(chan struct{}, takeOverWorkers)
	}
	m.mu.Unlock()
}

// Run places and runs accounts until ctx ends, then stops every runner and releases the leases.
func (m *Manager) Run(ctx context.Context) {
	m.defaults()
	m.Bus.SetAccountsProvider(func() int { return len(m.Owned()) })
	if unsub, err := m.Bus.SubscribeCoreReply(bus.NodeSyncSubject(m.NodeID), m.onCommand); err != nil {
		m.Log.Error("sync commands unavailable; commands wait in pendingCommands", "err", err)
	} else {
		defer func() { _ = unsub() }()
	}
	if unsub, err := m.Bus.SubscribeCore(bus.ClusterDrainSubject(m.NodeID), func(string, []byte) {
		m.startDraining(context.WithoutCancel(ctx))
	}); err == nil {
		defer func() { _ = unsub() }()
	}
	// Lease renewal runs in its own loop: a slow placement tick (many claims, hand-offs, a slow MongoDB) must
	// never delay renewals past the TTL — that expired every lease of a busy node at once (P2.4 load run).
	renewDone := make(chan struct{})
	go func() {
		defer close(renewDone)
		m.renewLoop(ctx)
	}()
	t := time.NewTicker(m.Tick)
	defer t.Stop()
	for {
		m.tick(ctx)
		select {
		case <-ctx.Done():
			<-renewDone
			m.stopAll(context.WithoutCancel(ctx))
			m.bg.Wait()
			return
		case <-t.C:
		case <-m.wake:
		}
	}
}

// renewLoop renews due leases every LeaseTTL/6 (a lease is due at a third of its TTL, so it is renewed
// before half of it has passed), and at once when a hand-off marker must be published.
func (m *Manager) renewLoop(ctx context.Context) {
	every := max(m.LeaseTTL/6, 100*time.Millisecond)
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-m.renewNow:
		}
		m.renewAll(ctx)
	}
}

func (m *Manager) kickRenew() {
	select {
	case m.renewNow <- struct{}{}:
	default:
	}
}

// Drain marks the node draining so placement moves its accounts to other nodes through hand-offs, and
// waits until it holds none or ctx ends (the caller then cancels Run, which releases what is left).
func (m *Manager) Drain(ctx context.Context) {
	m.defaults()
	if _, ok := ctx.Deadline(); !ok { // never block a shutdown on a peer that does not take over
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, m.HandoffTimeout+2*m.Tick)
		defer cancel()
	}
	m.startDraining(ctx)
	t := time.NewTicker(100 * time.Millisecond)
	defer t.Stop()
	for len(m.Owned()) > 0 {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (m *Manager) startDraining(ctx context.Context) {
	if m.draining.Swap(true) {
		return
	}
	m.Log.Info("sync node draining: accounts move to other nodes")
	if err := m.Bus.SetDraining(ctx, true); err != nil {
		m.Log.Warn("cannot mark the node draining in eb-nodes; other nodes see it when it leaves", "err", err)
	}
	m.kick()
}

func (m *Manager) kick() {
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

// handoffReply is the answer to a handoffReady request.
type handoffReply struct {
	Released bool `json:"released"`
}

// onCommand delivers a sync command to the account's runner, or (handoffReady) gives the account to the
// node that asks for it. Only handoffReady gets a reply.
func (m *Manager) onCommand(_ string, data []byte) []byte {
	var cm CommandMessage
	if json.Unmarshal(data, &cm) != nil || cm.AccountID == "" {
		m.Log.Warn("malformed sync command ignored")
		return nil
	}
	m.mu.Lock()
	o := m.held[cm.AccountID]
	handoffTo := ""
	if o != nil {
		handoffTo = o.handoffTo
	}
	m.mu.Unlock()
	if cm.Cmd == CmdHandoffReady {
		node, _ := cm.Args["node"].(string)
		released := false
		if o != nil && node != "" && handoffTo == node {
			m.stop(context.Background(), cm.AccountID, true) // the runner flushes its writes and logs out first
			released = true
			m.leaseChange(LeaseHandedOff)
			m.Log.Info("account handed off", "account_id", cm.AccountID, "to", node)
		}
		out, _ := json.Marshal(handoffReply{Released: released})
		return out
	}
	if o == nil {
		m.Log.Debug("sync command for an account this node does not hold", "account_id", cm.AccountID, "cmd", cm.Cmd)
		return nil // not ours (any more): the API stored it in pendingCommands
	}
	m.Log.Debug("sync command received", "account_id", cm.AccountID, "cmd", cm.Cmd)
	select {
	case o.cmds <- cm.Command:
	default:
		m.Log.Warn("sync command dropped: queue full", "account_id", cm.AccountID, "cmd", cm.Cmd)
	}
	return nil
}

// syncNodes are the live, non-draining nodes running the sync role. This node is included unless it drains.
func (m *Manager) syncNodes(ctx context.Context) []string {
	var ids []string
	if !m.draining.Load() {
		ids = append(ids, m.NodeID)
	}
	nodes, err := m.Bus.LiveNodes(ctx)
	if err != nil {
		return ids
	}
	for _, n := range nodes {
		if n.NodeID != m.NodeID && !n.Draining && slices.Contains(n.Roles, "sync") {
			ids = append(ids, n.NodeID)
		}
	}
	return ids
}

func (m *Manager) tick(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	placeable, err := m.Accounts.Placeable(ctx)
	if err != nil {
		m.Log.Warn("account list unavailable; keeping current accounts", "err", err) // renewLoop keeps the leases
		return
	}
	nodes := m.syncNodes(ctx)
	want := map[string]bool{}
	for _, id := range placeable {
		if len(nodes) > 0 && bus.OwnerOf(LeasePrefix+id, nodes) == m.NodeID {
			want[id] = true
		}
	}
	m.rebalance(ctx, placeable, nodes, want)
	m.claimWanted(ctx, placeable, want)
}

// rebalance stops finished runners and hands off (or releases) the accounts this node should not hold.
func (m *Manager) rebalance(ctx context.Context, placeable, nodes []string, want map[string]bool) {
	stillPlaceable := make(map[string]bool, len(placeable))
	for _, id := range placeable {
		stillPlaceable[id] = true
	}
	now := time.Now()
	var drop []string
	marked := false
	m.mu.Lock()
	for id, o := range m.held {
		select {
		case <-o.done:
			drop = append(drop, id)
			continue
		default:
		}
		if want[id] {
			if o.handoffTo != "" { // placement moved back: cancel the hand-off
				o.handoffTo, o.renewedAt = "", time.Time{}
			}
			continue
		}
		target := ""
		if stillPlaceable[id] && len(nodes) > 0 {
			target = bus.OwnerOf(LeasePrefix+id, nodes)
		}
		switch {
		case target == "" || target == m.NodeID || m.HandoffTimeout < 0:
			drop = append(drop, id) // disabled, deleted, or nowhere to go: release now
		case o.handoffTo != target:
			o.handoffTo, o.handoffAt, o.renewedAt = target, now, time.Time{} // renewAll publishes the marker now
			marked = true
			m.Log.Info("handing off account", "account_id", id, "to", target)
		case now.Sub(o.handoffAt) > m.HandoffTimeout:
			m.Log.Warn("hand-off target did not take the account in time; releasing it", "account_id", id, "to", target)
			drop = append(drop, id)
		}
	}
	m.mu.Unlock()
	if marked {
		m.kickRenew()
	}
	// Stops wait for the runner to flush and log out: run them in the background so the tick stays short.
	for _, id := range drop {
		m.stopAsync(ctx, id, true)
	}
}

// claimWanted claims free accounts placed on this node (rate-limited) and takes over accounts another node is
// handing to this one.
func (m *Manager) claimWanted(ctx context.Context, placeable []string, want map[string]bool) {
	claims, unplaced := 0, 0
	for _, id := range placeable {
		if !want[id] {
			continue
		}
		m.mu.Lock()
		_, have := m.held[id]
		n := len(m.held)
		m.mu.Unlock()
		if have {
			continue
		}
		if m.MaxAccounts > 0 && n >= m.MaxAccounts {
			unplaced++
			m.markUnplaced(ctx, id)
			continue
		}
		if claims >= m.ConnectRate {
			continue
		}
		claims++
		if err := m.claim(ctx, id, nil); errors.Is(err, bus.ErrLeaseHeld) {
			m.takeOverAsync(ctx, id)
		}
	}
	m.metrics.unplaced.Store(int64(unplaced))
}

// markUnplaced records the capacity reason on an account nobody holds (at most once per LeaseTTL).
func (m *Manager) markUnplaced(ctx context.Context, id string) {
	cm, ok := m.Accounts.(CapacityMarker)
	if !ok {
		return
	}
	m.mu.Lock()
	last := m.marked[id]
	if time.Since(last) < m.LeaseTTL {
		m.mu.Unlock()
		return
	}
	m.marked[id] = time.Now()
	m.mu.Unlock()
	if h, err := m.Bus.LeaseHolder(ctx, LeasePrefix+id); err != nil || h != nil {
		return // held elsewhere (or unknown): not unplaced
	}
	if err := cm.Unplaced(ctx, id); err != nil && ctx.Err() == nil {
		m.Log.Warn("cannot record the capacity reason", "account_id", id, "err", err)
	}
	m.Log.Warn("no sync capacity for account", "account_id", id, "max_accounts", m.MaxAccounts)
}

// leaseOwner splits a lease value into the owner node and the hand-off target (if any).
func leaseOwner(v []byte) (node, handoffTo string) {
	s := string(v)
	if i := strings.Index(s, handoffMarker); i >= 0 {
		return s[:i], s[i+len(handoffMarker):]
	}
	return s, ""
}

// takeOverAsync runs takeOver in the background (bounded, at most one per account), so a pre-connect or a slow
// holder never blocks the placement tick.
func (m *Manager) takeOverAsync(ctx context.Context, id string) {
	m.mu.Lock()
	if m.taking[id] {
		m.mu.Unlock()
		return
	}
	m.taking[id] = true
	m.mu.Unlock()
	m.bg.Add(1)
	go func() {
		defer m.bg.Done()
		defer func() {
			m.mu.Lock()
			delete(m.taking, id)
			m.mu.Unlock()
		}()
		select {
		case m.takeSem <- struct{}{}:
		case <-ctx.Done():
			return
		}
		defer func() { <-m.takeSem }()
		m.takeOver(ctx, id)
	}()
}

// takeOver runs the target side of a hand-off: when the holder marked the lease for this node, pre-connect
// the account, ask the holder for it, then claim it and start syncing on the pre-connected session.
func (m *Manager) takeOver(ctx context.Context, id string) {
	h, err := m.Bus.LeaseHolder(ctx, LeasePrefix+id)
	if err != nil || h == nil {
		return
	}
	owner, target := leaseOwner(h.Value)
	if target != m.NodeID || owner == "" || owner == m.NodeID {
		return // not (yet) handed to us; the holder marks the lease on its next tick
	}
	acc, err := m.Accounts.Load(ctx, id)
	if err != nil {
		return
	}
	pctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	pre, err := m.preConnect(pctx, acc)
	cancel()
	if err != nil {
		m.Log.Warn("hand-off pre-connect failed; the holder releases after its timeout", "account_id", id, "err", err)
		return
	}
	body, _ := json.Marshal(CommandMessage{AccountID: id, Command: Command{Cmd: CmdHandoffReady, Args: map[string]any{"node": m.NodeID}, At: time.Now().UTC()}})
	rctx, rcancel := context.WithTimeout(ctx, 20*time.Second)
	reply, err := m.Bus.RequestCore(rctx, bus.NodeSyncSubject(owner), body)
	rcancel()
	var hr handoffReply
	if err != nil || json.Unmarshal(reply, &hr) != nil || !hr.Released {
		closeQuietly(pre)
		return
	}
	if err := m.claim(ctx, id, pre); err != nil {
		closeQuietly(pre)
	}
}

// preConnect opens and authenticates the account's IMAP session (the same options as a runner) without
// syncing, so a hand-off only costs the time to stop the old owner.
func (m *Manager) preConnect(ctx context.Context, acc *Account) (*imapc.Client, error) {
	r := &runner{acc: acc, cfg: m.Config, dial: m.Dial, egress: m.Egress, ippool: m.IPPool, tap: m.Tap, now: time.Now}
	r.cfg.defaults()
	return r.connect(ctx)
}

func closeQuietly(c *imapc.Client) {
	if c == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = c.Logout(ctx)
}

func (m *Manager) renewAll(ctx context.Context) {
	now := time.Now()
	type due struct {
		id string
		o  *owned
	}
	var list []due
	m.mu.Lock()
	for id, o := range m.held {
		if now.Sub(o.renewedAt) >= m.LeaseTTL/3 {
			list = append(list, due{id, o})
		}
	}
	m.mu.Unlock()
	sem := make(chan struct{}, renewWorkers)
	var wg sync.WaitGroup
	for _, d := range list {
		if ctx.Err() != nil {
			break
		}
		sem <- struct{}{}
		wg.Add(1)
		go func() {
			defer func() { <-sem; wg.Done() }()
			m.renewOne(ctx, d.id, d.o)
		}()
	}
	wg.Wait()
}

// renewOne renews one lease (publishing the hand-off marker when one is set). A lost lease stops the runner
// without releasing: another node owns the account now.
func (m *Manager) renewOne(ctx context.Context, id string, o *owned) {
	o.leaseMu.Lock()
	defer o.leaseMu.Unlock()
	if o.stopped {
		return
	}
	m.mu.Lock()
	value := []byte(m.NodeID)
	if o.handoffTo != "" {
		value = []byte(m.NodeID + handoffMarker + o.handoffTo)
	}
	m.mu.Unlock()
	started := time.Now()
	epoch, err := m.Bus.Renew(ctx, LeasePrefix+id, value, o.epoch, m.LeaseTTL)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		o.stopped = true
		m.Log.Warn("account lease lost; stopping the account", "account_id", id, "err", err)
		m.leaseChange(LeaseLost)
		m.mu.Lock()
		if m.held[id] == o {
			delete(m.held, id)
		}
		m.mu.Unlock()
		m.bg.Add(1)
		go func() {
			defer m.bg.Done()
			m.finish(context.WithoutCancel(ctx), id, o, false)
		}()
		return
	}
	o.epoch = epoch
	m.mu.Lock()
	o.renewedAt = started
	m.mu.Unlock()
}

// claim takes the lease and starts the account; pre is a pre-connected session from a hand-off (or nil).
func (m *Manager) claim(ctx context.Context, id string, pre *imapc.Client) error {
	epoch, err := m.Bus.Claim(ctx, LeasePrefix+id, []byte(m.NodeID), m.LeaseTTL)
	if err != nil {
		if !errors.Is(err, bus.ErrLeaseHeld) && ctx.Err() == nil {
			m.Log.Warn("account claim failed", "account_id", id, "err", err)
		}
		return err
	}
	m.leaseChange(LeaseClaimed)
	pending, err := m.Accounts.Claimed(ctx, id, m.NodeID, epoch)
	if err == nil {
		var acc *Account
		if acc, err = m.Accounts.Load(ctx, id); err == nil {
			m.start(ctx, acc, epoch, pending, pre)
			return nil
		}
	}
	m.Log.Warn("account start failed; releasing", "account_id", id, "err", err)
	_ = m.Bus.Release(context.WithoutCancel(ctx), LeasePrefix+id, epoch)
	m.leaseChange(LeaseReleased)
	return err
}

func (m *Manager) start(ctx context.Context, acc *Account, epoch uint64, pending []Command, pre *imapc.Client) {
	rctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	o := &owned{epoch: epoch, renewedAt: time.Now(), cancel: cancel, done: make(chan struct{}), cmds: make(chan Command, 16)}
	for _, c := range pending {
		select {
		case o.cmds <- c:
		default:
		}
	}
	r := &runner{acc: acc, epoch: epoch, nodeID: m.NodeID, ix: m.Index, ev: m.Events, accounts: m.Accounts, needs: m.Needs,
		cfg: m.Config, log: m.Log, dial: m.Dial, egress: m.Egress, ippool: m.IPPool, tap: m.Tap, archive: m.Archive, outbound: m.Outbound, suppress: m.Suppress, scorer: m.Scorer, attachments: m.Attachments, pec: m.PEC, metrics: &m.metrics, cmds: o.cmds, pre: pre}
	m.mu.Lock()
	m.held[acc.ID] = o
	delete(m.marked, acc.ID)
	m.mu.Unlock()
	go func() {
		defer close(o.done)
		defer func() {
			if rec := recover(); rec != nil { // one account's bug never takes down the others (S5 §1)
				stack := debug.Stack()
				m.Log.Error("account runner panicked; it restarts on the next placement tick", "account_id", acc.ID, "panic", rec, "stack", string(stack))
				if m.Reporter != nil {
					m.Reporter.CapturePanic(rctx, rec, stack, map[string]string{"component": "sync", "accountId": acc.ID})
				}
			}
		}()
		if acc.Provider != "" && acc.Provider != ProviderIMAP {
			_ = r.runBackend(rctx, m.Backends[acc.Provider])
			return
		}
		_ = r.run(rctx)
	}()
}

// stop ends a runner and (when release) gives the lease back, and returns when both are done.
func (m *Manager) stop(ctx context.Context, id string, release bool) {
	if o := m.detach(id); o != nil {
		m.finish(ctx, id, o, release)
	}
}

// stopAsync detaches the account at once (it no longer counts as held) and finishes the stop in the
// background; Run waits for it before returning.
func (m *Manager) stopAsync(ctx context.Context, id string, release bool) {
	o := m.detach(id)
	if o == nil {
		return
	}
	m.bg.Add(1)
	go func() {
		defer m.bg.Done()
		m.finish(context.WithoutCancel(ctx), id, o, release)
	}()
}

func (m *Manager) detach(id string) *owned {
	m.mu.Lock()
	defer m.mu.Unlock()
	o := m.held[id]
	delete(m.held, id)
	return o
}

// finish cancels the runner, waits until it flushed and logged out, then releases the lease (when release
// and the lease was not already lost).
func (m *Manager) finish(ctx context.Context, id string, o *owned, release bool) {
	m.mu.Lock()
	handed := o.handoffTo != ""
	m.mu.Unlock()
	o.cancel()
	<-o.done
	o.leaseMu.Lock()
	defer o.leaseMu.Unlock()
	lost := o.stopped
	o.stopped = true
	if !release || lost {
		return
	}
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	if err := m.Bus.Release(rctx, LeasePrefix+id, o.epoch); err != nil && !errors.Is(err, bus.ErrLeaseLost) {
		m.Log.Warn("account lease release failed; it expires after the TTL", "account_id", id, "err", err)
	} else if err == nil && !handed {
		// hand-offs are counted as handed_off by the caller, not as a plain release
		m.leaseChange(LeaseReleased)
	}
}

func (m *Manager) stopAll(ctx context.Context) {
	m.mu.Lock()
	ids := make([]string, 0, len(m.held))
	for id := range m.held {
		ids = append(ids, id)
	}
	m.mu.Unlock()
	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Add(1)
		go func() { defer wg.Done(); m.stop(ctx, id, true) }()
	}
	wg.Wait()
}

// Owned lists the accounts this node runs.
func (m *Manager) Owned() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, 0, len(m.held))
	for id := range m.held {
		out = append(out, id)
	}
	slices.Sort(out)
	return out
}

// Draining reports whether this node is draining.
func (m *Manager) Draining() bool { return m.draining.Load() }

// Stats returns the engine counters.
func (m *Manager) Stats() Stats {
	return Stats{Accounts: int64(len(m.Owned())), Passes: m.metrics.passes.Load(), FullPasses: m.metrics.fullPasses.Load(),
		NewMessages: m.metrics.newMsgs.Load(), IdleRestarts: m.metrics.idleRestarts.Load(), Errors: m.metrics.errors.Load(),
		Unplaced: m.metrics.unplaced.Load()}
}
