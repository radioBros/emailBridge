package gmail

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/radiobros/emailbridge/internal/events"
	"github.com/radiobros/emailbridge/internal/events/types"
	"github.com/radiobros/emailbridge/internal/index"
	ebsync "github.com/radiobros/emailbridge/internal/sync"
)

// Config holds runner timings.
type Config struct {
	PullInterval time.Duration // EB_GMAIL_PULL_INTERVAL
	WatchRenew   time.Duration // EB_GMAIL_WATCH_RENEW
}

func (c *Config) defaults() {
	if c.PullInterval <= 0 {
		c.PullInterval = 60 * time.Second
	}
	if c.WatchRenew <= 0 {
		c.WatchRenew = 24 * time.Hour
	}
}

// Runner is the Gmail history-based sync backend (selected when accounts.provider=gmail).
type Runner struct {
	Client     *Client
	Tokens     TokenSource
	Sink       DraftSink
	State      *MemoryState // in-memory for unit tests; Repo used when set
	Repo       *StateRepo
	Log        *slog.Logger
	Config     Config
	Needs      Needs
	now        func() time.Time
	wake       chan struct{}
	labelNames map[string]string

	// Engine hooks (sync.BackendEnv) — when set, events + index rows share one fenced Write.
	Write       func(ctx context.Context, drafts []events.Draft, fn func(tx context.Context) error) error
	Draft       func(typ, subject string, data any) events.Draft
	Index       *index.Index
	Epoch       uint64
	Attachments ebsync.AttachmentIngest

	// known caches providerId → index rows when Index is nil (unit tests without Mongo).
	known map[string][]*index.Message
}

// MemoryState holds SyncState without Mongo (tests).
type MemoryState struct {
	S *SyncState
}

// Load returns the state.
func (m *MemoryState) Load(_ context.Context, acc *Account) (*SyncState, error) {
	if m.S == nil {
		m.S = newState(acc)
	}
	return m.S, nil
}

// Save persists the state.
func (m *MemoryState) Save(_ context.Context, st *SyncState) error {
	m.S = st
	return nil
}

func newState(acc *Account) *SyncState {
	h := sha256.Sum256([]byte(acc.ID))
	uidv := binary.BigEndian.Uint32(h[:4])
	if uidv == 0 {
		uidv = 1
	}
	return &SyncState{
		AccountID: acc.ID, OrganizationID: acc.OrganizationID,
		UIDValidity: uidv, NextUID: map[string]uint32{}, MailboxIDs: map[string]string{},
	}
}

// Wake nudges a running pull loop (Pub/Sub push).
func (r *Runner) Wake() {
	if r.wake == nil {
		return
	}
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

// SyncOnce applies history (or full resync) once. Used by tests and pull/push.
func (r *Runner) SyncOnce(ctx context.Context, acc *Account) error {
	r.Config.defaults()
	if r.now == nil {
		r.now = func() time.Time { return time.Now().UTC() }
	}
	if r.Log == nil {
		r.Log = slog.Default()
	}
	tok, _, err := r.Tokens.AccessToken(ctx, acc.ID)
	if err != nil {
		return err
	}
	st, err := r.load(ctx, acc)
	if err != nil {
		return err
	}
	if err := r.ensureMailboxes(ctx, acc, st); err != nil {
		return err
	}
	if err := r.refreshLabelNames(ctx, tok); err != nil {
		r.Log.WarnContext(ctx, "gmail labels list failed", "err", err)
	}
	if st.HistoryID == "" {
		return r.fullResync(ctx, acc, st, tok, false)
	}
	records, newHID, err := r.Client.ListHistory(ctx, tok, st.HistoryID)
	if errors.Is(err, ErrHistoryExpired) {
		return r.fullResync(ctx, acc, st, tok, true)
	}
	if err != nil {
		return err
	}
	for _, rec := range records {
		if err := r.applyHistory(ctx, acc, st, tok, rec); err != nil {
			return err
		}
	}
	if newHID != "" {
		st.HistoryID = newHID
	}
	st.UpdatedAt = r.now()
	return r.save(ctx, st)
}

// RunPull loops SyncOnce until ctx ends (pull fallback / no Pub/Sub).
func (r *Runner) RunPull(ctx context.Context, acc *Account) error {
	r.Config.defaults()
	r.wake = make(chan struct{}, 1)
	for {
		if err := r.SyncOnce(ctx, acc); err != nil && ctx.Err() == nil {
			r.Log.WarnContext(ctx, "gmail sync", "account_id", acc.ID, "err", err)
		}
		if acc.TopicName != "" {
			_ = r.ensureWatch(ctx, acc)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-r.wake:
		case <-time.After(r.Config.PullInterval):
		}
	}
}

func (r *Runner) load(ctx context.Context, acc *Account) (*SyncState, error) {
	if r.Repo != nil {
		return r.Repo.Load(ctx, acc)
	}
	if r.State == nil {
		r.State = &MemoryState{}
	}
	return r.State.Load(ctx, acc)
}

func (r *Runner) save(ctx context.Context, st *SyncState) error {
	if r.Repo != nil {
		return r.Repo.Save(ctx, st)
	}
	if r.State == nil {
		r.State = &MemoryState{}
	}
	return r.State.Save(ctx, st)
}

func (r *Runner) ensureMailboxes(ctx context.Context, acc *Account, st *SyncState) error {
	watched := watchedSet(acc.Paths)
	var mboxes []*index.Mailbox
	for _, s := range SystemMailboxes {
		if !watched[s.Path] && !watched["*"] {
			continue
		}
		if st.MailboxIDs[s.Path] != "" {
			continue
		}
		// Silent on first sight (IMAP baseline); mailbox.created only for folders that appear later.
		mb := indexMailbox(acc, st, s.Path, s.SpecialUse)
		mboxes = append(mboxes, mb)
	}
	if len(mboxes) == 0 || r.Write == nil || r.Index == nil {
		return nil
	}
	return r.Write(ctx, nil, func(tx context.Context) error {
		for _, m := range mboxes {
			if err := r.Index.SaveMailbox(tx, m); err != nil {
				return err
			}
		}
		return nil
	})
}

func (r *Runner) refreshLabelNames(ctx context.Context, tok string) error {
	labs, err := r.Client.ListLabels(ctx, tok)
	if err != nil {
		return err
	}
	r.labelNames = map[string]string{}
	for _, l := range labs {
		r.labelNames[l.ID] = l.Name
	}
	return nil
}

func (r *Runner) fullResync(ctx context.Context, acc *Account, st *SyncState, tok string, expired bool) error {
	prof, err := r.Client.GetProfile(ctx, tok)
	if err != nil {
		return err
	}
	oldMbx := map[string]string{}
	for k, v := range st.MailboxIDs {
		oldMbx[k] = v
	}
	if expired {
		st.UIDValidity++
		if st.UIDValidity == 0 {
			st.UIDValidity = 1
		}
	}
	st.NextUID = map[string]uint32{}
	r.known = nil

	watched := watchedSet(acc.Paths)
	var allDrafts []events.Draft
	var allMsgs []*index.Message
	var allMbx []*index.Mailbox

	for _, s := range SystemMailboxes {
		if !watched[s.Path] && !watched["*"] {
			continue
		}
		listed, err := r.Client.ListMessages(ctx, tok, s.ID)
		if err != nil {
			return err
		}
		mb := indexMailbox(acc, st, s.Path, s.SpecialUse)
		if expired {
			if oldID := oldMbx[s.Path]; oldID != "" && r.Index != nil && r.Write != nil {
				_ = r.Write(ctx, nil, func(tx context.Context) error {
					_, err := r.Index.ClearMailbox(tx, oldID)
					return err
				})
			}
		}
		allMbx = append(allMbx, mb)
		needFetch := r.Needs.Text || r.Needs.Attachments
		for _, lm := range listed {
			msg := &Message{ID: lm.ID, ThreadID: lm.ThreadID, LabelIDs: []string{s.ID}}
			if needFetch {
				format := "metadata"
				if r.Needs.Text || r.Needs.Attachments {
					format = "raw"
				}
				full, err := r.Client.GetMessage(ctx, tok, lm.ID, format)
				if err != nil {
					return err
				}
				msg = full
			}
			// Silent index: no message.received on full resync (no-replay), like IMAP baseline.
			data, err := MapMessage(acc, st, s.Path, s.SpecialUse, msg, Needs{}, r.labelNames)
			if err != nil {
				return err
			}
			allMsgs = append(allMsgs, indexEntry(acc, st, data, msg.ID, msg.ThreadID))
		}
		mb.UIDNext = st.NextUID[s.Path]
		mb.Exists = uint32(len(listed)) //nolint:gosec // G115: mailbox counts fit uint32
		if expired {
			d := r.draftOf(acc, events.MailboxReset, mb.ID, &types.MailboxReset{
				AccountRef:     types.AccountRef{AccountID: acc.ID, ExternalID: acc.ExternalID},
				Mailbox:        types.Mailbox{MailboxID: mb.ID, Path: s.Path, SpecialUse: s.SpecialUse},
				OldUIDValidity: st.UIDValidity - 1, NewUIDValidity: st.UIDValidity, Messages: len(listed),
			})
			allDrafts = append(allDrafts, d)
		}
	}
	st.HistoryID = prof.HistoryID
	st.UpdatedAt = r.now()
	// Chunk index writes so a large mailbox stays inside MongoDB transaction limits.
	if err := r.commit(ctx, acc, nil, nil, allMbx); err != nil {
		return err
	}
	const chunk = 500
	for i := 0; i < len(allMsgs); i += chunk {
		end := min(i+chunk, len(allMsgs))
		if err := r.commit(ctx, acc, nil, allMsgs[i:end], nil); err != nil {
			return err
		}
	}
	if err := r.commit(ctx, acc, allDrafts, nil, nil); err != nil {
		return err
	}
	return r.save(ctx, st)
}

func (r *Runner) applyHistory(ctx context.Context, acc *Account, st *SyncState, tok string, rec HistoryRecord) error {
	ids := historyProviderIDs(rec)
	byProv, err := r.loadByProvider(ctx, acc.ID, ids)
	if err != nil {
		return err
	}
	for _, id := range rec.MessagesAdded {
		if err := r.onAdded(ctx, acc, st, tok, id, byProv); err != nil {
			return err
		}
	}
	for _, id := range rec.MessagesDeleted {
		if err := r.onDeleted(ctx, acc, st, id, byProv); err != nil {
			return err
		}
	}
	for _, ch := range rec.LabelsAdded {
		if err := r.onLabels(ctx, acc, st, tok, ch.MessageID, byProv); err != nil {
			return err
		}
	}
	for _, ch := range rec.LabelsRemoved {
		if err := r.onLabels(ctx, acc, st, tok, ch.MessageID, byProv); err != nil {
			return err
		}
	}
	return nil
}

func historyProviderIDs(rec HistoryRecord) []string {
	seen := map[string]bool{}
	var ids []string
	add := func(id string) {
		if id == "" || seen[id] {
			return
		}
		seen[id] = true
		ids = append(ids, id)
	}
	for _, id := range rec.MessagesAdded {
		add(id)
	}
	for _, id := range rec.MessagesDeleted {
		add(id)
	}
	for _, ch := range rec.LabelsAdded {
		add(ch.MessageID)
	}
	for _, ch := range rec.LabelsRemoved {
		add(ch.MessageID)
	}
	return ids
}

// loadByProvider batches index lookups by providerId ($in).
func (r *Runner) loadByProvider(ctx context.Context, accountID string, ids []string) (map[string][]*index.Message, error) {
	out := map[string][]*index.Message{}
	if len(ids) == 0 {
		return out, nil
	}
	if r.Index != nil {
		rows, err := r.Index.MessagesByProviderIDs(ctx, accountID, ids)
		if err != nil {
			return nil, err
		}
		for _, m := range rows {
			if m.ProviderID == "" {
				continue
			}
			out[m.ProviderID] = append(out[m.ProviderID], m)
		}
		return out, nil
	}
	for _, id := range ids {
		if rows := r.known[id]; len(rows) > 0 {
			out[id] = rows
		}
	}
	return out, nil
}

func (r *Runner) remember(msgs []*index.Message) {
	if r.Index != nil || len(msgs) == 0 {
		return // durable rows live in the index
	}
	if r.known == nil {
		r.known = map[string][]*index.Message{}
	}
	for _, m := range msgs {
		if m == nil || m.ProviderID == "" {
			continue
		}
		list := r.known[m.ProviderID]
		replaced := false
		for i, prev := range list {
			if prev.ID == m.ID {
				list[i] = m
				replaced = true
				break
			}
		}
		if !replaced {
			list = append(list, m)
		}
		r.known[m.ProviderID] = list
	}
}

func (r *Runner) onAdded(ctx context.Context, acc *Account, st *SyncState, tok, gmailID string, byProv map[string][]*index.Message) error {
	if len(byProv[gmailID]) > 0 {
		return nil // already indexed
	}
	format := "metadata"
	if r.Needs.Text || r.Needs.Attachments {
		format = "raw"
	}
	msg, err := r.Client.GetMessage(ctx, tok, gmailID, format)
	if err != nil {
		return err
	}
	folders := FolderPaths(msg.LabelIDs, r.labelNames, acc.Paths)
	if len(folders) == 0 {
		return nil
	}
	path, special, _ := PrimaryMailbox(msg.LabelIDs, acc.Paths)
	if path == "" {
		path, special = folders[0].Path, folders[0].SpecialUse
	}
	// Primary folder first so the received event uses that mailbox.
	for i, f := range folders {
		if f.Path == path {
			folders[0], folders[i] = folders[i], folders[0]
			break
		}
	}
	silent := !acc.NotifyFrom.IsZero() && msg.InternalDate.Before(acc.NotifyFrom)
	var drafts []events.Draft
	var msgs []*index.Message
	var mboxes []*index.Mailbox
	for _, f := range folders {
		mb := indexMailbox(acc, st, f.Path, f.SpecialUse)
		mboxes = append(mboxes, mb)
		need := Needs{}
		if !silent && f.Path == path {
			need = r.Needs
		}
		data, err := MapMessage(acc, st, f.Path, f.SpecialUse, msg, need, r.labelNames)
		if err != nil {
			return err
		}
		entry := indexEntry(acc, st, data, msg.ID, msg.ThreadID)
		msgs = append(msgs, entry)
		mb.UIDNext = st.NextUID[f.Path]
		if !silent && f.Path == path {
			if r.Index != nil {
				seen, _ := r.Index.SeenElsewhere(ctx, acc.ID, data.Mailbox.MailboxID, msg.ID)
				data.SeenBefore = seen
			}
			r.enrichAttachments(ctx, acc, data, msg.Raw)
			drafts = append(drafts, r.draftOf(acc, events.MessageReceived, data.MessageID, data))
		}
	}
	_ = special
	if err := r.commit(ctx, acc, drafts, msgs, mboxes); err != nil {
		return err
	}
	if byProv != nil {
		byProv[gmailID] = append([]*index.Message(nil), msgs...)
	}
	return nil
}

func (r *Runner) enrichAttachments(ctx context.Context, acc *Account, data *types.MessageReceived, raw []byte) {
	if r.Attachments == nil || data == nil {
		return
	}
	var results []ebsync.AttachmentResult
	var err error
	if (acc.AttachmentsS3 || acc.AttachmentsAV) && len(raw) > 0 {
		results, err = r.Attachments.Ingest(ctx, acc.OrganizationID, acc.ID, data.MessageID, raw, acc.AttachmentsS3, acc.AttachmentsAV)
	} else if len(data.Attachments) > 0 {
		metas := make([]ebsync.AttachmentMeta, 0, len(data.Attachments))
		for _, a := range data.Attachments {
			metas = append(metas, ebsync.AttachmentMeta{
				PartID: a.PartID, Filename: a.Filename, ContentType: a.ContentType,
				Size: a.Size, ContentID: a.ContentID, Disposition: a.Disposition,
			})
		}
		results, err = r.Attachments.Register(ctx, acc.OrganizationID, acc.ID, data.MessageID, metas)
	}
	if err != nil || len(results) == 0 {
		return
	}
	byPart := map[string]ebsync.AttachmentResult{}
	for _, res := range results {
		byPart[res.PartID] = res
	}
	for i := range data.Attachments {
		if res, ok := byPart[data.Attachments[i].PartID]; ok {
			data.Attachments[i].ID = res.ID
			data.Attachments[i].URL = res.URL
			data.Attachments[i].Scan = res.Scan
		}
	}
}

func (r *Runner) onDeleted(ctx context.Context, acc *Account, st *SyncState, gmailID string, byProv map[string][]*index.Message) error {
	rows := byProv[gmailID]
	if len(rows) == 0 {
		return nil
	}
	primary := primaryRow(st, rows)
	path := pathForMailboxID(st, primary.MailboxID)
	special := ""
	if s, ok := systemByPath[path]; ok {
		special = s.SpecialUse
	}
	ids := make([]string, 0, len(rows))
	for _, m := range rows {
		ids = append(ids, m.ID)
	}
	if r.Attachments != nil {
		for _, id := range ids {
			_ = r.Attachments.EraseMessage(ctx, acc.ID, id)
		}
	}
	data := &types.MessageDeleted{
		AccountRef: types.AccountRef{AccountID: acc.ID, ExternalID: acc.ExternalID},
		MessageID:  primary.ID,
		Mailbox:    types.Mailbox{MailboxID: primary.MailboxID, Path: path, SpecialUse: special},
	}
	delete(byProv, gmailID)
	delete(r.known, gmailID)
	draft := r.draftOf(acc, events.MessageDeleted, primary.ID, data)
	if r.Write != nil && r.Index != nil {
		return r.Write(ctx, []events.Draft{draft}, func(tx context.Context) error {
			return r.Index.DeleteMessages(tx, ids)
		})
	}
	return r.emit(ctx, acc, events.MessageDeleted, primary.ID, data)
}

func (r *Runner) onLabels(ctx context.Context, acc *Account, st *SyncState, tok, gmailID string, byProv map[string][]*index.Message) error {
	rows := byProv[gmailID]
	if len(rows) == 0 {
		return r.onAdded(ctx, acc, st, tok, gmailID, byProv)
	}
	msg, err := r.Client.GetMessage(ctx, tok, gmailID, "metadata")
	if err != nil {
		return err
	}
	path, special, _ := PrimaryMailbox(msg.LabelIDs, acc.Paths)
	if path == "" && len(FolderPaths(msg.LabelIDs, r.labelNames, acc.Paths)) == 0 {
		return r.onDeleted(ctx, acc, st, gmailID, byProv)
	}
	if path == "" {
		folders := FolderPaths(msg.LabelIDs, r.labelNames, acc.Paths)
		path, special = folders[0].Path, folders[0].SpecialUse
	}
	primary := primaryRow(st, rows)
	prevFlags := primary.Flags
	prevLabels := labelsFromRows(st, rows)
	currFlags := FlagsFromLabels(msg.LabelIDs)
	currLabels := UserLabels(msg.LabelIDs, r.labelNames)
	fd := FlagDiff(prevFlags, currFlags)
	ld := FlagDiff(prevLabels, currLabels)
	if len(fd.Added) == 0 && len(fd.Removed) == 0 && len(ld.Added) == 0 && len(ld.Removed) == 0 {
		return nil
	}
	mbxID := st.MailboxIDs[path]
	if mbxID == "" {
		mbxID = primary.MailboxID
	}
	data := &types.MessageUpdated{
		AccountRef: types.AccountRef{AccountID: acc.ID, ExternalID: acc.ExternalID},
		MessageID:  primary.ID,
		Mailbox:    types.Mailbox{MailboxID: mbxID, Path: path, SpecialUse: special},
		Flags:      fd,
		Labels:     &ld,
	}
	draft := r.draftOf(acc, events.MessageUpdated, primary.ID, data)
	if r.Write != nil && r.Index != nil {
		return r.Write(ctx, []events.Draft{draft}, func(tx context.Context) error {
			for _, row := range rows {
				if err := r.Index.UpdateFlags(tx, row.ID, currFlags, 0); err != nil {
					return err
				}
				row.Flags = currFlags
			}
			return nil
		})
	}
	return r.emit(ctx, acc, events.MessageUpdated, primary.ID, data)
}

func primaryRow(st *SyncState, rows []*index.Message) *index.Message {
	if len(rows) == 0 {
		return nil
	}
	inv := invertMailboxIDs(st.MailboxIDs)
	// Prefer INBOX, then other system paths, else first row.
	best := rows[0]
	bestRank := 99
	for _, m := range rows {
		path := inv[m.MailboxID]
		rank := 50
		switch path {
		case "INBOX":
			rank = 0
		case "[Gmail]/Sent Mail":
			rank = 1
		case "[Gmail]/Drafts":
			rank = 2
		case "[Gmail]/Trash":
			rank = 3
		case "[Gmail]/Spam":
			rank = 4
		default:
			if strings.HasPrefix(path, "Label/") {
				rank = 80
			}
		}
		if rank < bestRank {
			best, bestRank = m, rank
		}
	}
	return best
}

func pathForMailboxID(st *SyncState, mbxID string) string {
	for path, id := range st.MailboxIDs {
		if id == mbxID {
			return path
		}
	}
	return ""
}

func invertMailboxIDs(in map[string]string) map[string]string {
	out := map[string]string{}
	for path, id := range in {
		out[id] = path
	}
	return out
}

// labelsFromRows reconstructs user labels from Label/<name> mailbox membership (no labels field on index.Message).
func labelsFromRows(st *SyncState, rows []*index.Message) []string {
	inv := invertMailboxIDs(st.MailboxIDs)
	var out []string
	seen := map[string]bool{}
	for _, m := range rows {
		path := inv[m.MailboxID]
		if !strings.HasPrefix(path, "Label/") {
			continue
		}
		name := path[len("Label/"):]
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}

func (r *Runner) draftOf(acc *Account, typ, subject string, data any) events.Draft {
	if r.Draft != nil {
		return r.Draft(typ, subject, data)
	}
	return events.Draft{Type: typ, OrganizationID: acc.OrganizationID, AccountID: acc.ID, Subject: subject, Data: data}
}

func (r *Runner) commit(ctx context.Context, acc *Account, drafts []events.Draft, msgs []*index.Message, mboxes []*index.Mailbox) error {
	if r.Write != nil {
		err := r.Write(ctx, drafts, func(tx context.Context) error {
			if r.Index == nil {
				return nil
			}
			for _, m := range mboxes {
				if m == nil {
					continue
				}
				if err := r.Index.SaveMailbox(tx, m); err != nil {
					return err
				}
			}
			return r.Index.InsertMessages(tx, msgs)
		})
		if err == nil {
			r.remember(msgs)
		}
		return err
	}
	r.remember(msgs)
	for _, d := range drafts {
		if err := r.emit(ctx, acc, d.Type, d.Subject, d.Data); err != nil {
			return err
		}
	}
	return nil
}

func (r *Runner) emit(ctx context.Context, acc *Account, typ, subject string, data any) error {
	if r.Sink == nil {
		return nil
	}
	return r.Sink.Emit(ctx, acc.OrganizationID, acc.ID, typ, subject, data)
}

func (r *Runner) ensureWatch(ctx context.Context, acc *Account) error {
	if acc.TopicName == "" {
		return nil
	}
	st, err := r.load(ctx, acc)
	if err != nil {
		return err
	}
	need := st.WatchExpiration == nil || r.now().Add(r.Config.WatchRenew).After(*st.WatchExpiration)
	if !need {
		return nil
	}
	tok, _, err := r.Tokens.AccessToken(ctx, acc.ID)
	if err != nil {
		return err
	}
	wr, err := r.Client.Watch(ctx, tok, acc.TopicName)
	if err != nil {
		return err
	}
	exp := wr.Expiration
	st.WatchExpiration = &exp
	st.TopicName = acc.TopicName
	if wr.HistoryID != "" && st.HistoryID == "" {
		st.HistoryID = wr.HistoryID
	}
	return r.save(ctx, st)
}
