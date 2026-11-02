package graph

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/events"
	"github.com/radiobros/emailbridge/internal/events/types"
	"github.com/radiobros/emailbridge/internal/index"
	"github.com/radiobros/emailbridge/internal/store"
	ebsync "github.com/radiobros/emailbridge/internal/sync"
)

// Backend is the Microsoft Graph sync.Backend registered on the sync Manager (P6.3 follow-up).
type Backend struct {
	Client *Client
	Repo   *StateRepo
	Config Config
	Log    *slog.Logger
}

// Config holds runner timings and callback base URL.
type Config struct {
	PullInterval time.Duration // EB_GRAPH_PULL_INTERVAL
	SubRenew     time.Duration // EB_GRAPH_SUB_RENEW
	CallbackURL  string        // EB_CALLBACK_URL → notificationUrl = CallbackURL + /callbacks/graph
	SkipMIME     bool          // tests: skip GetMIME when Graph envelope fields suffice
}

func (c *Config) defaults() {
	if c.PullInterval <= 0 {
		c.PullInterval = 60 * time.Second
	}
	if c.SubRenew <= 0 {
		c.SubRenew = 24 * time.Hour
	}
}

// Run implements sync.Backend.
func (b *Backend) Run(ctx context.Context, acc *ebsync.Account, env ebsync.BackendEnv) error {
	if b == nil || b.Client == nil {
		env.Transition(ctx, ebsync.StateConnectError, &types.Reason{Kind: "config", Code: "provider_unavailable",
			Message: "Graph backend is not configured."})
		<-ctx.Done()
		return ctx.Err()
	}
	log := env.Log
	if log == nil {
		log = b.Log
	}
	if log == nil {
		log = slog.Default()
	}
	now := env.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	gacc := Account{
		ID: acc.ID, OrganizationID: acc.OrganizationID, ExternalID: acc.ExternalID,
		Email: acc.Email, Paths: acc.Paths, NotifyFrom: acc.NotifyFrom, UPN: firstNonEmpty(acc.DelegatedUser, acc.Email),
		NotificationURL: NotificationURLFromCallback(b.Config.CallbackURL),
		AttachmentsS3:   acc.AttachmentsS3, AttachmentsAV: acc.AttachmentsAV,
		AuthResults: acc.AuthResults, TrustedAuth: acc.TrustedAuth,
		DenyWebSafeHTML: acc.DenyWebSafeHTML, DenyCalendarEvents: acc.DenyCalendarEvents,
		DenyReplyText: acc.DenyReplyText, SyncFrom: acc.SyncFrom, SpecialUse: acc.SpecialUse,
	}
	needs := Needs{}
	if env.Needs != nil {
		if n, err := env.Needs.Needs(ctx, acc.OrganizationID); err == nil {
			needs = Needs{
				Headers: n.Headers, Text: n.Text, TextMax: n.TextMax, Attachments: n.Attachments,
				WebSafeHTML:    n.WebSafeHTML && !acc.DenyWebSafeHTML,
				CalendarEvents: n.CalendarEvents && !acc.DenyCalendarEvents,
				ReplyText:      n.ReplyText && !acc.DenyReplyText,
			}
		}
	}
	if acc.AuthResults != "" {
		needs.AuthResults = acc.AuthResults
		needs.TrustedAuth = acc.TrustedAuth
	}
	r := &Runner{
		Client: b.Client.ForUser(gacc.UPN), Repo: b.Repo, Config: b.Config, Log: log, Needs: needs, Now: now,
		Tokens: accountToken{acc: acc}, Attachments: env.Attachments,
		Write: env.Write, Draft: env.Draft, Index: env.Index, Epoch: env.Epoch,
	}
	r.Config.defaults()
	r.wake = make(chan struct{}, 1)

	env.Transition(ctx, ebsync.StateConnecting, nil)
	if err := r.SyncOnce(ctx, gacc); err != nil {
		b.fail(ctx, env, err)
	} else {
		env.Transition(ctx, ebsync.StateConnected, nil)
	}
	_ = r.EnsureSubscription(ctx, gacc, r.Config.SubRenew)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case cmd := <-env.Commands:
			switch cmd.Cmd {
			case ebsync.CmdReconnect, ebsync.CmdConfigChanged:
				env.Transition(ctx, ebsync.StateConnecting, nil)
				if err := r.SyncOnce(ctx, gacc); err != nil {
					b.fail(ctx, env, err)
				} else {
					env.Transition(ctx, ebsync.StateConnected, nil)
				}
			case ebsync.CmdResync:
				if err := r.forceFull(ctx, gacc); err != nil {
					b.fail(ctx, env, err)
				} else {
					env.Transition(ctx, ebsync.StateConnected, nil)
				}
			case ebsync.CmdFlush:
				if err := r.flush(ctx, gacc); err != nil {
					b.fail(ctx, env, err)
				} else if err := r.forceFull(ctx, gacc); err != nil {
					b.fail(ctx, env, err)
				} else {
					env.Transition(ctx, ebsync.StateConnected, nil)
				}
			}
		case <-r.wake:
			if err := r.SyncOnce(ctx, gacc); err != nil {
				b.fail(ctx, env, err)
			} else {
				env.Transition(ctx, ebsync.StateConnected, nil)
			}
		case <-time.After(r.Config.PullInterval):
			if err := r.SyncOnce(ctx, gacc); err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				b.fail(ctx, env, err)
			} else {
				env.Transition(ctx, ebsync.StateConnected, nil)
			}
			_ = r.EnsureSubscription(ctx, gacc, r.Config.SubRenew)
		}
	}
}

func (b *Backend) fail(ctx context.Context, env ebsync.BackendEnv, err error) {
	if err == nil || ctx.Err() != nil {
		return
	}
	reason := &types.Reason{Kind: "temporary", Code: "mail_connect_failed", Message: err.Error()}
	to := ebsync.StateConnectError
	if errors.Is(err, ErrSyncStateNotFound) {
		reason.Kind, reason.Code = "temporary", "sync_state_not_found"
	}
	code := errs.CodeOf(err)
	switch code {
	case errs.CodeOauthRevoked, errs.CodeOauthNotConnected, errs.CodeMailAuthFailed, errs.CodeInvalidCredentials:
		to = ebsync.StateAuthError
		reason.Kind, reason.Code = "auth", string(code)
	}
	env.Transition(ctx, to, reason)
	if env.Log != nil {
		env.Log.WarnContext(ctx, "graph backend sync failed", "err", err, "state", to)
	}
}

type accountToken struct{ acc *ebsync.Account }

func (t accountToken) AccessToken(ctx context.Context, _ string) (string, string, error) {
	if t.acc == nil || t.acc.Token == nil {
		return "", "", errs.New(errs.CodeOauthNotConnected, "The account is not connected with OAuth.")
	}
	return t.acc.Token(ctx)
}

// Wake nudges a running Backend pull loop (change notifications).
func (r *Runner) Wake() {
	if r.wake == nil {
		return
	}
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

func (r *Runner) forceFull(ctx context.Context, acc Account) error {
	r.ensureState(acc)
	r.State.DeltaLinks = map[string]string{}
	if r.Repo != nil {
		if err := r.Repo.Save(ctx, r.State); err != nil {
			return err
		}
	}
	return r.SyncOnce(ctx, acc)
}

func indexEntry(acc Account, st *SyncState, data *types.MessageReceived, graphID, conversationID string) *index.Message {
	e := &index.Message{
		ID: data.MessageID, OrganizationID: acc.OrganizationID, AccountID: acc.ID,
		MailboxID: data.Mailbox.MailboxID, UIDValidity: data.UIDValidity, UID: data.UID,
		Flags: orEmpty(data.Flags), MessageIDHeader: data.MessageIDHeader,
		EmailID: graphID, ProviderID: graphID, ThreadID: conversationID,
		InternalDate: data.InternalDate, Size: data.Size,
		IndexedAt: time.Now().UTC(), Subject: truncateStr(data.Subject, index.SummarySubject),
	}
	if data.From != nil {
		e.From = &index.Address{Name: data.From.Name, Address: data.From.Address}
	}
	for i, a := range data.To {
		if i >= index.SummaryRecipients {
			break
		}
		e.To = append(e.To, index.Address{Name: a.Name, Address: a.Address})
	}
	if data.Date != nil {
		d := *data.Date
		e.Date = &d
	}
	_ = st
	return e
}

func indexMailbox(acc Account, st *SyncState, path, specialUse string) *index.Mailbox {
	id := st.MailboxIDs[path]
	if id == "" {
		id = store.NewID(store.PrefixMailbox)
		st.MailboxIDs[path] = id
	}
	return &index.Mailbox{
		ID: id, OrganizationID: acc.OrganizationID, AccountID: acc.ID,
		Path: path, SpecialUse: specialUse, UIDValidity: st.UIDValidity,
		UIDNext: st.NextUID[path], Status: "active", SyncMode: "poll",
	}
}

func orEmpty(f []string) []string {
	if f == nil {
		return []string{}
	}
	return f
}

func truncateStr(s string, n int) string {
	if n <= 0 || len(s) <= n {
		return s
	}
	return s[:n]
}

// draftOf builds an event draft via the engine Draft hook when set.
func (r *Runner) draftOf(acc Account, typ, subject string, data any) events.Draft {
	if r.Draft != nil {
		return r.Draft(typ, subject, data)
	}
	return events.Draft{Type: typ, OrganizationID: acc.OrganizationID, AccountID: acc.ID, Subject: subject, Data: data}
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}
