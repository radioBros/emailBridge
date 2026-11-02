package sync

import "sync/atomic"

// LeaseObserver records lease lifecycle events for eb_lease_changes_total (S12 §9).
type LeaseObserver interface {
	LeaseChange(kind string)
}

// Lease change kinds (must match observability).
const (
	LeaseClaimed   = "claimed"
	LeaseLost      = "lost"
	LeaseReleased  = "released"
	LeaseHandedOff = "handed_off"
)

// metrics are the engine counters (S5 §9); the observability module exports them.
type metrics struct {
	passes, fullPasses, newMsgs, idleRestarts, errors atomic.Int64
	unplaced                                          atomic.Int64 // accounts placed here but over EB_SYNC_MAX_ACCOUNTS
}

func (m *metrics) pass(full bool) {
	m.passes.Add(1)
	if full {
		m.fullPasses.Add(1)
	}
}

func (m *metrics) newMessages(n int) { m.newMsgs.Add(int64(n)) }
func (m *metrics) idleRestart()      { m.idleRestarts.Add(1) }
func (m *metrics) error(string)      { m.errors.Add(1) }

// Stats is a snapshot of the counters.
type Stats struct {
	Accounts, Passes, FullPasses, NewMessages, IdleRestarts, Errors, Unplaced int64
}
