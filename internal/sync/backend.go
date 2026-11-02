package sync

import (
	"context"
	"log/slog"
	"time"

	"github.com/radiobros/emailbridge/internal/events"
	"github.com/radiobros/emailbridge/internal/events/types"
	"github.com/radiobros/emailbridge/internal/index"
)

// Provider values of accounts.provider (S1). An empty provider is IMAP.
const (
	ProviderIMAP  = "imap"
	ProviderGmail = "gmail"
	ProviderGraph = "graph"
)

// Backend syncs an account whose mailbox is reached through a provider API instead of IMAP (P6.2 Gmail API,
// P6.3 Microsoft Graph). The manager owns placement, the lease and the fencing epoch exactly as for IMAP
// accounts; a backend only detects changes and writes them through env. Run returns when ctx ends (the lease
// was lost or released, or the node stops); returning earlier with an error makes the manager restart it on
// the next placement tick.
type Backend interface {
	Run(ctx context.Context, acc *Account, env BackendEnv) error
}

// BackendEnv is what the engine gives a provider backend.
type BackendEnv struct {
	// Epoch is the account's fencing epoch for this ownership.
	Epoch uint64
	// Index is the shared mailbox / message index. Backends write mailboxes (labels, folders) and messages with
	// stable per-mailbox UIDs they allocate, so the messages API and the browser work for every provider.
	Index *index.Index
	// Write runs fn and stores drafts in one transaction fenced on Epoch: a node that lost the account cannot
	// write. Use it for every index change that emits events.
	Write func(ctx context.Context, drafts []events.Draft, fn func(tx context.Context) error) error
	// Draft builds an event draft for this account.
	Draft func(typ, subject string, data any) events.Draft
	// Transition records an account state change (connected, authError, …) with its reason.
	Transition func(ctx context.Context, to string, reason *types.Reason)
	// Needs is what message.received must carry for the organisation (payload options of its endpoints).
	Needs PayloadNeeds
	// Attachments indexes attachment rows for new messages (P4.3 / W48); nil = skip.
	Attachments AttachmentIngest
	// Commands are the account's sync commands (resync, reconnect, …), as for IMAP accounts.
	Commands <-chan Command
	Log      *slog.Logger
	Now      func() time.Time
}

// backendEnv builds the environment of a backend run from the runner that would otherwise run IMAP.
func (r *runner) backendEnv() BackendEnv {
	return BackendEnv{Epoch: r.epoch, Index: r.ix, Write: r.write, Draft: r.draft, Transition: r.transition,
		Needs: r.needs, Attachments: r.attachments, Commands: r.cmds, Log: r.log, Now: r.now}
}

// runBackend runs a provider backend instead of the IMAP loop; an account whose provider has no registered
// backend is put into an error state with a clear reason instead of being retried against IMAP.
func (r *runner) runBackend(ctx context.Context, b Backend) error {
	r.init()
	if b == nil {
		r.transition(ctx, StateConnectError, &types.Reason{Kind: "config", Code: "provider_unavailable",
			Message: "This node has no backend for the account's provider (" + r.acc.Provider + ")."})
		<-ctx.Done()
		return ctx.Err()
	}
	return b.Run(ctx, r.acc, r.backendEnv())
}
