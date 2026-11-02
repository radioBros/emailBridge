package sync

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/radiobros/emailbridge/internal/egress"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/events"
	"github.com/radiobros/emailbridge/internal/events/types"
	"github.com/radiobros/emailbridge/internal/index"
	"github.com/radiobros/emailbridge/internal/ippool"
	"github.com/radiobros/emailbridge/internal/mailproto/imapc"
	"github.com/radiobros/emailbridge/internal/protolog"
)

// Config holds the engine settings (S9 §2.8).
type Config struct {
	ReconnectMin     time.Duration // EB_SYNC_RECONNECT_MIN
	ReconnectMax     time.Duration // EB_SYNC_RECONNECT_MAX
	MaxIdle          time.Duration // EB_SYNC_MAX_IDLE (25 min)
	ResyncDelay      time.Duration // EB_SYNC_RESYNC_DELAY: other watched folders
	NoIdlePoll       time.Duration // EB_SYNC_NOIDLE_POLL
	FullResync       time.Duration // EB_SYNC_FULL_RESYNC (0 = only on demand)
	FetchBatch       int           // EB_SYNC_FETCH_BATCH
	CatchupMax       time.Duration // EB_SYNC_CATCHUP_MAX
	AuthDisableAfter time.Duration // EB_SYNC_AUTH_DISABLE_AFTER
	IMAPID           map[string]string
	CommandTimeout   time.Duration
	DoneTimeout      time.Duration
}

func (c *Config) defaults() {
	set := func(d *time.Duration, v time.Duration) {
		if *d <= 0 {
			*d = v
		}
	}
	set(&c.ReconnectMin, 5*time.Second)
	set(&c.ReconnectMax, 10*time.Minute)
	set(&c.MaxIdle, 25*time.Minute)
	set(&c.ResyncDelay, 5*time.Minute)
	set(&c.NoIdlePoll, time.Minute)
	set(&c.CatchupMax, 7*24*time.Hour)
	set(&c.AuthDisableAfter, 72*time.Hour)
	if c.FetchBatch <= 0 {
		c.FetchBatch = 100
	}
}

// errReconnect ends a session on purpose (reconnect / configChanged commands).
var errReconnect = errors.New("sync: reconnect requested")

// errStop ends the runner for good (account disabled).
var errStop = errors.New("sync: account stopped")

// runner owns one account while this node holds its lease.
type runner struct {
	acc         *Account
	pre         *imapc.Client // pre-connected session from a hand-off, used by the first session
	epoch       uint64
	nodeID      string
	ix          *index.Index
	ev          *events.Writer
	accounts    Accounts
	needs       PayloadNeeds
	cfg         Config
	log         *slog.Logger
	dial        imapc.DialFunc
	egress      *egress.Dialer     // W57 per-account network dial
	ippool      *ippool.Selector   // W57 outbound IP pool
	archive     Archiver           // inbound.archiveRaw store, nil = off
	outbound    OutboundLookup     // bounce correlation (P5.4), nil = by extracted IDs only
	suppress    Suppressor         // hard bounces / complaints → suppression lists (P5.2), nil = off
	scorer      SpamScorer         // inbound spam scoring (P8.3), nil = off
	pec         *PEC               // PEC envelopes and receipts (P9), nil = off
	attachments AttachmentIngest   // attachment rows (P4.3 / W48), nil = off
	tap         *protolog.Recorder // protocol logs for accounts with protocolLogs.enabled (S12 §7), nil = off
	now         func() time.Time
	metrics     *metrics

	cmds        chan Command
	mailboxes   map[string]*index.Mailbox
	lastFull    map[string]time.Time
	initialized bool
	lastAlert   time.Time // last auth_failed / connect_failed event (at most hourly)
}

func (r *runner) init() {
	r.cfg.defaults()
	if r.now == nil {
		r.now = func() time.Time { return time.Now().UTC() }
	}
	if r.needs == nil {
		r.needs = NoNeeds{}
	}
	if r.metrics == nil {
		r.metrics = &metrics{}
	}
	if r.lastFull == nil {
		r.lastFull = map[string]time.Time{}
	}
}

// run keeps the account connected until ctx ends, the lease is lost (fenced) or the account is disabled.
func (r *runner) run(ctx context.Context) error {
	r.init()
	attempt := 0
	for ctx.Err() == nil {
		err := r.session(ctx)
		switch {
		case ctx.Err() != nil:
			r.released()
			return nil
		case errors.Is(err, index.ErrFenced):
			r.log.WarnContext(ctx, "account owned by another node; stopping", "account_id", r.acc.ID)
			return err
		case errors.Is(err, errStop):
			return nil
		case errors.Is(err, errReconnect):
			attempt = 0
			continue
		case err == nil:
			attempt = 0
			continue
		}
		attempt++
		if serr := r.failed(ctx, err); serr != nil {
			if errors.Is(serr, errStop) || errors.Is(serr, index.ErrFenced) {
				return nil
			}
		}
		wait := backoff(attempt, r.cfg.ReconnectMin, r.cfg.ReconnectMax, errs.KindOf(err), randInt64)
		if r.waitOrCommand(ctx, wait) {
			attempt = 0
		}
	}
	r.released()
	return nil
}

// released records "disconnected" while this node still holds the lease (the manager releases it right
// after), so the next owner starts from disconnected (S5 §2).
func (r *runner) released() {
	if r.acc.State == StateDisabled || r.acc.State == StateDeleting {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	r.transition(ctx, StateDisconnected, nil)
}

// waitOrCommand sleeps d; reconnect/configChanged/resync commands cut it short (returns true).
func (r *runner) waitOrCommand(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return false
		case <-t.C:
			return false
		case c := <-r.cmds:
			switch c.Cmd {
			case CmdReconnect, CmdConfigChanged, CmdResync, CmdFlush:
				if c.Cmd == CmdConfigChanged {
					r.reload(ctx)
				}
				if c.Cmd == CmdFlush {
					_ = r.flushAccount(ctx)
				}
				return true
			}
		}
	}
}

func (r *runner) reload(ctx context.Context) {
	acc, err := r.accounts.Load(ctx, r.acc.ID)
	if err != nil {
		r.log.WarnContext(ctx, "account reload failed; keeping the previous settings", "account_id", r.acc.ID, "err", err)
		return
	}
	r.acc = acc
}

func (r *runner) transition(ctx context.Context, to string, reason *types.Reason) {
	if r.acc.State == to {
		return
	}
	if err := r.accounts.Transition(ctx, r.acc.ID, r.epoch, to, reason); err != nil {
		if ctx.Err() == nil {
			r.log.WarnContext(ctx, "account state not recorded", "account_id", r.acc.ID, "to", to, "err", err)
		}
		return
	}
	r.acc.State = to
}

func reasonOf(err error) *types.Reason {
	if err == nil {
		return nil
	}
	return &types.Reason{Kind: string(errs.KindOf(err)), Code: string(errs.CodeOf(err)), Message: redact(err.Error())}
}

// redact trims server text for storage (no credentials ever reach it: LOGIN/AUTH are masked by imapc).
func redact(s string) string {
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > 512 {
		s = s[:512]
	}
	return s
}

// failed records a session failure: state, error timeline, auth/connect events (first, then at most
// hourly) and auto-disable after EB_SYNC_AUTH_DISABLE_AFTER (S5 §2, §7).
func (r *runner) failed(ctx context.Context, err error) error {
	r.metrics.error(string(errs.KindOf(err)))
	state := failureState(err)
	now := r.now()
	entry := bson.M{"at": now, "kind": string(errs.KindOf(err)), "code": string(errs.CodeOf(err)), "protocol": "imap", "response": redact(err.Error())}
	var drafts []events.Draft
	set := bson.M{"lastErrorAt": now}
	alert := r.lastAlert.IsZero() || now.Sub(r.lastAlert) >= time.Hour
	if state == StateAuthError {
		since := now
		if r.acc.AuthFailingSince != nil {
			since = *r.acc.AuthFailingSince
		} else {
			set["authFailingSince"] = now
			r.acc.AuthFailingSince = &now
			alert = true
		}
		if since.Add(r.cfg.AuthDisableAfter).Before(now) {
			r.transition(ctx, StateDisabled, reasonOf(err))
			r.log.WarnContext(ctx, "account disabled after continuous authentication failures", "account_id", r.acc.ID, "failing_since", since)
			_ = r.write(ctx, nil, func(tx context.Context) error { return r.pushError(tx, entry, set) })
			return errStop
		}
		if alert {
			disableAt := since.Add(r.cfg.AuthDisableAfter)
			drafts = append(drafts, r.draft(events.AccountAuthFailed, r.acc.ID, &types.AccountAuthFailed{AccountRef: r.accRef(), Protocol: "imap",
				Code: string(errs.CodeOf(err)), Response: redact(err.Error()), FailingSince: since, DisableAt: &disableAt}))
		}
	} else if alert {
		drafts = append(drafts, r.draft(events.AccountConnectFailed, r.acc.ID, &types.AccountConnectFailed{AccountRef: r.accRef(), Protocol: "imap",
			Code: string(errs.CodeOf(err)), Kind: string(errs.KindOf(err)), Response: redact(err.Error())}))
	}
	if alert {
		r.lastAlert = now
	}
	r.transition(ctx, state, reasonOf(err))
	r.log.WarnContext(ctx, "account connection failed", "account_id", r.acc.ID, "state", state, "err", err)
	return r.write(ctx, drafts, func(tx context.Context) error { return r.pushError(tx, entry, set) })
}

func (r *runner) pushError(tx context.Context, entry, set bson.M) error {
	_, err := r.ix.DB.Collection(index.Accounts).UpdateOne(tx, bson.M{"_id": r.acc.ID},
		bson.M{"$set": set, "$push": bson.M{"recentErrors": bson.M{"$each": []bson.M{entry}, "$slice": -20}}})
	return err
}

func (r *runner) succeeded(ctx context.Context) error {
	if r.acc.AuthFailingSince == nil {
		return nil
	}
	failedFor := r.now().Sub(*r.acc.AuthFailingSince).Round(time.Second).String()
	err := r.write(ctx, []events.Draft{r.draft(events.AccountAuthSucceeded, r.acc.ID, &types.AccountAuthSucceeded{AccountRef: r.accRef(), Protocol: "imap", FailedFor: failedFor})},
		func(tx context.Context) error {
			_, err := r.ix.DB.Collection(index.Accounts).UpdateOne(tx, bson.M{"_id": r.acc.ID}, bson.M{"$unset": bson.M{"authFailingSince": ""}})
			return err
		})
	if err == nil {
		r.acc.AuthFailingSince = nil
		r.lastAlert = time.Time{}
	}
	return err
}

func (r *runner) connect(ctx context.Context) (*imapc.Client, error) {
	o := imapc.Options{Addr: r.acc.IMAPAddr, TLS: r.acc.IMAPTLS, InsecureSkipVerify: r.acc.IgnoreCertErrors,
		ID: MergeIMAPID(r.cfg.IMAPID, r.acc.IMAPID), Dial: r.dialFor(r.acc),
		CommandTimeout: r.cfg.CommandTimeout, DoneTimeout: r.cfg.DoneTimeout, TapConn: r.acc.ID + "/main",
		QResync: r.acc.QResync, DisableRev2: r.acc.DisableRev2}
	if err := AuthOptions(ctx, r.acc, &o); err != nil {
		return nil, err
	}
	if r.tap != nil && r.acc.ProtocolLogs {
		raw := r.acc.RawDebugUntil != nil && r.now().Before(*r.acc.RawDebugUntil)
		o.Tap = r.tap.Sink(r.acc.OrganizationID, r.acc.ID, raw)
		if raw {
			o.TapMode = protolog.Raw
		}
	}
	return imapc.Dial(ctx, o)
}

func (r *runner) dialFor(acc *Account) imapc.DialFunc {
	if r.egress == nil {
		return r.dial
	}
	local := ""
	if r.ippool != nil {
		local = r.ippool.ResolveLocal(acc.OrganizationID, acc.ID, "imap", acc.LocalAddress, acc.LocalAddresses, acc.IMAPStrategy)
	} else {
		local = acc.LocalAddress
	}
	return r.egress.MailDialFunc(egress.MailNetwork{Proxy: acc.Proxy, LocalAddr: local})
}

// session connects, syncs every watched folder and then idles on the main folder until an error or a
// reconnect command.
func (r *runner) session(ctx context.Context) error {
	r.transition(ctx, StateConnecting, nil)
	c, pre := r.pre, r.pre != nil // a hand-off target arrives with the session already open (S4 §6)
	r.pre = nil
	if !pre {
		var err error
		if c, err = r.connect(ctx); err != nil {
			return err
		}
	}
	defer func() {
		lctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		_ = c.Logout(lctx)
		cancel()
	}()
	if err := r.succeeded(ctx); err != nil {
		return err
	}
	mbs, err := r.ix.Mailboxes(ctx, r.acc.ID)
	if err != nil {
		return err
	}
	r.mailboxes = mbs
	r.initialized = false
	for _, m := range mbs {
		if m.Status == "active" {
			r.initialized = true
			break
		}
	}
	if r.initialized {
		r.transition(ctx, StateConnected, nil)
	} else {
		r.transition(ctx, StateSyncing, nil)
	}
	start := r.now()
	main, err := r.pass(ctx, c, false)
	if err != nil {
		return err
	}
	if !r.initialized {
		if err := r.announceInitialized(ctx, start); err != nil {
			return err
		}
		r.initialized = true
		r.transition(ctx, StateConnected, nil)
	}
	if main == "" {
		return r.pollLoop(ctx, c, "")
	}
	if c.Has("IDLE") {
		return r.idleLoop(ctx, c, main)
	}
	return r.pollLoop(ctx, c, main)
}

func (r *runner) announceInitialized(ctx context.Context, start time.Time) error {
	msgs := 0
	for _, m := range r.mailboxes {
		n, err := r.ix.DB.Collection(index.Messages).CountDocuments(ctx, bson.M{"mailboxId": m.ID})
		if err != nil {
			return err
		}
		msgs += int(n)
	}
	return r.write(ctx, []events.Draft{r.draft(events.AccountInitialized, r.acc.ID, &types.AccountInitialized{AccountRef: r.accRef(),
		Mailboxes: len(r.mailboxes), Messages: msgs, DurationMs: r.now().Sub(start).Milliseconds()})}, func(context.Context) error { return nil })
}

// pass lists the folders, removes vanished ones, syncs every watched folder (the main folder last, so it
// stays selected) and returns the main folder's name ("" when none is watched).
func (r *runner) pass(ctx context.Context, c *imapc.Client, full bool) (string, error) {
	list, err := c.List(ctx)
	if err != nil {
		return "", err
	}
	w := watched(list, r.acc.Paths)
	present := map[string]bool{}
	for _, m := range w {
		present[m.Name] = true
	}
	for path, m := range r.mailboxes {
		if !present[path] {
			if err := r.removeFolder(ctx, m); err != nil {
				return "", err
			}
		}
	}
	idle := idleFolders(w, r.acc.IdlePaths, 1)
	main := ""
	if len(idle) > 0 {
		main = idle[0]
	}
	var mainInfo *imapc.MailboxInfo
	for i := range w {
		if w[i].Name == main {
			mainInfo = &w[i]
			continue
		}
		if !full && r.unchanged(w[i]) {
			continue
		}
		if err := r.syncFolder(ctx, c, w[i], full); err != nil {
			return "", err
		}
	}
	if mainInfo != nil {
		if err := r.syncFolder(ctx, c, *mainInfo, full); err != nil {
			return "", err
		}
	}
	r.metrics.pass(full)
	return main, nil
}

// unchanged reports that LIST-STATUS shows no change since the stored baseline (STATUS first, S5 §4).
func (r *runner) unchanged(info imapc.MailboxInfo) bool {
	m := r.mailboxes[info.Name]
	if m == nil || m.Status != "active" || info.Status == nil || r.dueFull(m) {
		return false
	}
	st := info.Status
	return st.UIDValidity == m.UIDValidity && st.UIDNext == m.UIDNext && st.Messages == m.Exists &&
		(st.HighestModSeq == 0 || st.HighestModSeq == m.HighestModSeq)
}

// idleLoop IDLEs on the main folder, reacting to updates, commands and the resync timer.
func (r *runner) idleLoop(ctx context.Context, c *imapc.Client, main string) error {
	maxIdle := r.cfg.MaxIdle
	if r.acc.MaxIdle > 0 {
		maxIdle = r.acc.MaxIdle
	}
	resync := r.cfg.ResyncDelay
	if r.acc.ResyncDelay > 0 {
		resync = r.acc.ResyncDelay
	}
	nextResync := r.now().Add(resync)
	for ctx.Err() == nil {
		cycle := min(maxIdle, max(time.Until(nextResync), time.Second))
		updates, cmds, err := r.idleOnce(ctx, c, cycle)
		if err != nil {
			return err
		}
		if err := r.applyUpdates(ctx, c, main, updates); err != nil {
			return err
		}
		full, requested := false, false
		for _, cmd := range cmds {
			switch cmd.Cmd {
			case CmdReconnect:
				return errReconnect
			case CmdConfigChanged:
				r.reload(ctx)
				return errReconnect
			case CmdFlush:
				if err := r.flushAccount(ctx); err != nil {
					return err
				}
				return errReconnect // reconnect + silent baseline
			case CmdResync:
				requested, full = true, full || cmd.full()
			}
		}
		if requested || !r.now().Before(nextResync) {
			if _, err := r.pass(ctx, c, full); err != nil {
				return err
			}
			nextResync = r.now().Add(resync)
		}
	}
	return nil
}

// idleOnce runs one IDLE cycle and returns the updates and commands that arrived (which also end it).
func (r *runner) idleOnce(ctx context.Context, c *imapc.Client, cycle time.Duration) ([]imapc.Update, []Command, error) {
	wake := make(chan struct{})
	stop := make(chan struct{})
	var once sync.Once
	wakeUp := func() { once.Do(func() { close(wake) }) }
	var updates []imapc.Update
	var cmds []Command
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case u := <-c.Updates():
				updates = append(updates, u)
				wakeUp()
			case cmd := <-r.cmds:
				cmds = append(cmds, cmd)
				wakeUp()
			case <-stop:
				return
			}
		}
	}()
	err := c.IdleUntil(ctx, cycle, wake)
	close(stop)
	wg.Wait()
	r.metrics.idleRestart()
	return updates, cmds, err
}

// applyUpdates handles unsolicited updates of the main folder (S5 §4): EXISTS → new mail, FETCH → flag
// change, EXPUNGE → deletion; an overflow or an unresolved expunge triggers a diff.
func (r *runner) applyUpdates(ctx context.Context, c *imapc.Client, main string, ups []imapc.Update) error {
	if len(ups) == 0 {
		return nil
	}
	m := r.mailboxes[main]
	if m == nil {
		return nil
	}
	newMail, overflow, unresolved := false, false, false
	flagUIDs := map[uint32]imapc.Update{}
	var expunged []uint32
	for _, u := range ups {
		switch u.Kind {
		case imapc.UpdateExists:
			newMail = true
		case imapc.UpdateFlags:
			if u.UID == 0 {
				unresolved = true
				continue
			}
			flagUIDs[u.UID] = u
		case imapc.UpdateExpunge:
			if u.UID == 0 {
				unresolved = true
				continue
			}
			expunged = append(expunged, u.UID)
		case imapc.UpdateOverflow:
			overflow = true
		}
	}
	if overflow {
		info := imapc.MailboxInfo{Name: main, SpecialUse: m.SpecialUse}
		return r.syncFolder(ctx, c, info, true)
	}
	if newMail {
		if _, err := r.fetchNew(ctx, c, m, 0); err != nil {
			return err
		}
	}
	if len(flagUIDs) > 0 {
		uids := make([]uint32, 0, len(flagUIDs))
		cur := map[uint32]*imapc.Message{}
		for uid, u := range flagUIDs {
			uids = append(uids, uid)
			cur[uid] = &imapc.Message{UID: uid, Flags: u.Flags, ModSeq: u.ModSeq}
		}
		known, err := r.ix.MessagesByUID(ctx, m.ID, uids)
		if err != nil {
			return err
		}
		if err := r.applyFlags(ctx, m, known, cur); err != nil {
			return err
		}
	}
	if len(expunged) > 0 {
		known, err := r.ix.MessagesByUID(ctx, m.ID, expunged)
		if err != nil {
			return err
		}
		gone := map[uint32]bool{}
		for _, u := range expunged {
			gone[u] = true
		}
		if err := r.applyDeletions(ctx, m, known, func(u uint32) bool { return !gone[u] }); err != nil {
			return err
		}
	}
	if unresolved {
		return r.deletionDiff(ctx, c, m)
	}
	return nil
}

// pollLoop is used when the server has no IDLE: the main folder every EB_SYNC_NOIDLE_POLL, the others on
// the resync timer.
func (r *runner) pollLoop(ctx context.Context, c *imapc.Client, main string) error {
	resync := r.cfg.ResyncDelay
	if r.acc.ResyncDelay > 0 {
		resync = r.acc.ResyncDelay
	}
	nextResync := r.now().Add(resync)
	t := time.NewTicker(r.cfg.NoIdlePoll)
	defer t.Stop()
	for {
		full, requested := false, false
		select {
		case <-ctx.Done():
			return nil
		case cmd := <-r.cmds:
			switch cmd.Cmd {
			case CmdReconnect:
				return errReconnect
			case CmdConfigChanged:
				r.reload(ctx)
				return errReconnect
			case CmdFlush:
				if err := r.flushAccount(ctx); err != nil {
					return err
				}
				return errReconnect
			case CmdResync:
				requested, full = true, full || cmd.full()
			}
		case <-t.C:
		}
		if requested || !r.now().Before(nextResync) {
			if _, err := r.pass(ctx, c, full); err != nil {
				return err
			}
			nextResync = r.now().Add(resync)
			continue
		}
		if m := r.mailboxes[main]; m != nil {
			if err := r.syncFolder(ctx, c, imapc.MailboxInfo{Name: main, SpecialUse: m.SpecialUse}, false); err != nil {
				return err
			}
		} else if err := c.Noop(ctx); err != nil {
			return err
		}
	}
}
