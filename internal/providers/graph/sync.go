package graph

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/radiobros/emailbridge/internal/events"
	"github.com/radiobros/emailbridge/internal/events/types"
	"github.com/radiobros/emailbridge/internal/index"
	"github.com/radiobros/emailbridge/internal/store"
	ebsync "github.com/radiobros/emailbridge/internal/sync"
)

// Runner performs Graph delta sync for one account and emits S3 event drafts.
type Runner struct {
	Client *Client
	Tokens TokenSource
	Sink   DraftSink
	Needs  Needs
	State  *SyncState // mutable; caller persists (tests)
	Repo   *StateRepo // optional Mongo persistence
	Log    *slog.Logger
	Config Config
	Now    func() time.Time
	wake   chan struct{}

	// Engine hooks (sync.BackendEnv) — when set, events + index rows share one fenced Write.
	Write       func(ctx context.Context, drafts []events.Draft, fn func(tx context.Context) error) error
	Draft       func(typ, subject string, data any) events.Draft
	Index       *index.Index
	Epoch       uint64
	Attachments ebsync.AttachmentIngest

	// known is a session cache keyed by Graph message id when Index is nil (unit tests). Never persisted.
	known map[string]*index.Message
}

func (r *Runner) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now().UTC()
}

func (r *Runner) ensureState(acc Account) {
	if r.State == nil {
		r.State = &SyncState{}
	}
	st := r.State
	st.AccountID = acc.ID
	st.OrganizationID = acc.OrganizationID
	if st.UIDValidity == 0 {
		st.UIDValidity = 1
	}
	normalizeState(st)
}

// SyncOnce lists folders and applies delta for each watched mailbox.
func (r *Runner) SyncOnce(ctx context.Context, acc Account) error {
	r.Config.defaults()
	r.ensureState(acc)
	if r.Client == nil {
		return errors.New("graph: nil client")
	}
	if acc.UPN != "" && r.Client.UPN != acc.UPN {
		r.Client = r.Client.ForUser(acc.UPN) // never mutate the shared module client
	}
	token, _, err := r.Tokens.AccessToken(ctx, acc.ID)
	if err != nil {
		return err
	}
	if err := r.ensureMailboxes(ctx, acc, token); err != nil {
		return err
	}
	folders, err := r.Client.ListFolders(ctx, token)
	if err != nil {
		return err
	}
	watched := watchedSet(acc.Paths)
	for _, f := range folders {
		path, special := MailboxPath(f.WellKnownName, f.DisplayName)
		if !isWatched(path, watched) {
			continue
		}
		r.State.FolderIDs[path] = f.ID
		if err := r.syncFolder(ctx, acc, token, f.ID, path, special); err != nil {
			return err
		}
	}
	r.State.UpdatedAt = r.now()
	if r.Repo != nil {
		return r.Repo.Save(ctx, r.State)
	}
	return nil
}

func (r *Runner) ensureMailboxes(ctx context.Context, acc Account, token string) error {
	folders, err := r.Client.ListFolders(ctx, token)
	if err != nil {
		return err
	}
	watched := watchedSet(acc.Paths)
	var mboxes []*index.Mailbox
	for _, f := range folders {
		path, special := MailboxPath(f.WellKnownName, f.DisplayName)
		if !isWatched(path, watched) {
			continue
		}
		r.State.FolderIDs[path] = f.ID
		if r.State.MailboxIDs[path] != "" {
			continue
		}
		mb := indexMailbox(acc, r.State, path, special)
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

func (r *Runner) syncFolder(ctx context.Context, acc Account, token, folderID, path, special string) error {
	link := r.State.DeltaLinks[folderID]
	page, err := r.Client.DeltaMessages(ctx, token, folderID, link)
	if errors.Is(err, ErrSyncStateNotFound) {
		return r.fullResync(ctx, acc, token, folderID, path, special)
	}
	if err != nil {
		return err
	}
	for page != nil {
		if err := r.applyPage(ctx, acc, token, path, special, page.Messages, link != ""); err != nil {
			return err
		}
		if page.NextLink != "" {
			page, err = r.Client.DeltaMessages(ctx, token, folderID, page.NextLink)
			if err != nil {
				return err
			}
			continue
		}
		if page.DeltaLink != "" {
			r.State.DeltaLinks[folderID] = page.DeltaLink
		}
		break
	}
	return nil
}

func (r *Runner) applyPage(ctx context.Context, acc Account, token, path, special string, msgs []Message, emit bool) error {
	ids := make([]string, 0, len(msgs))
	for _, m := range msgs {
		if m.ID != "" {
			ids = append(ids, m.ID)
		}
	}
	existing, err := r.lookupProviders(ctx, acc.ID, ids)
	if err != nil {
		return err
	}
	mb := indexMailbox(acc, r.State, path, special)
	var drafts []events.Draft
	var toInsert []*index.Message
	for _, m := range msgs {
		if m.Removed {
			d, err := r.applyRemoved(ctx, acc, path, special, mb.ID, m, existing[m.ID], emit)
			if err != nil {
				return err
			}
			if d != nil {
				drafts = append(drafts, *d)
			}
			continue
		}
		if ex := existing[m.ID]; ex != nil {
			d, err := r.applyFlags(ctx, acc, path, special, mb.ID, m, ex, emit)
			if err != nil {
				return err
			}
			if d != nil {
				drafts = append(drafts, *d)
			}
			continue
		}
		entry, raw, err := r.indexNew(ctx, acc, token, path, special, m)
		if err != nil || entry == nil {
			return err
		}
		m.Raw = raw
		data := BuildReceived(acc, mb.ID, path, special, entry.UID, r.State.UIDValidity, m, raw, r.Needs)
		data.MessageID = entry.ID
		if r.Index != nil {
			seen, _ := r.Index.SeenElsewhere(ctx, acc.ID, mb.ID, m.ID)
			data.SeenBefore = seen
		}
		mb.UIDNext = r.State.NextUID[path]
		silent := !emit || (!acc.NotifyFrom.IsZero() && m.Received.Before(acc.NotifyFrom))
		if !silent {
			r.enrichAttachments(ctx, acc, data, raw)
			drafts = append(drafts, r.draftOf(acc, events.MessageReceived, data.MessageID, data))
		}
		toInsert = append(toInsert, entry)
	}
	if len(drafts) == 0 && len(toInsert) == 0 {
		return nil
	}
	return r.commit(ctx, acc, drafts, toInsert, []*index.Mailbox{mb})
}

func (r *Runner) applyRemoved(ctx context.Context, acc Account, path, special, mbxID string, m Message, ex *index.Message, emit bool) (*events.Draft, error) {
	if !emit || ex == nil {
		return nil, nil
	}
	delete(r.known, m.ID)
	if r.Attachments != nil {
		_ = r.Attachments.EraseMessage(ctx, acc.ID, ex.ID)
	}
	data := &types.MessageDeleted{
		AccountRef: types.AccountRef{AccountID: acc.ID, ExternalID: acc.ExternalID},
		MessageID:  ex.ID, Mailbox: types.Mailbox{MailboxID: mbxID, Path: path, SpecialUse: special},
		UID: ex.UID, MessageIDHeader: ex.MessageIDHeader,
	}
	draft := r.draftOf(acc, events.MessageDeleted, ex.ID, data)
	if r.Write != nil && r.Index != nil {
		if err := r.Write(ctx, []events.Draft{draft}, func(tx context.Context) error {
			return r.Index.DeleteMessages(tx, []string{ex.ID})
		}); err != nil {
			return nil, err
		}
		return nil, nil
	}
	return &draft, nil
}

func (r *Runner) enrichAttachments(ctx context.Context, acc Account, data *types.MessageReceived, raw []byte) {
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

func (r *Runner) flush(ctx context.Context, acc Account) error {
	r.ensureState(acc)
	if r.Index != nil {
		for _, id := range r.State.MailboxIDs {
			known, _ := r.Index.MessageStates(ctx, id)
			var ids []string
			for _, e := range known {
				ids = append(ids, e.ID)
			}
			if r.Write != nil {
				_ = r.Write(ctx, nil, func(tx context.Context) error {
					_, err := r.Index.ClearMailbox(tx, id)
					return err
				})
			}
			if r.Attachments != nil {
				for _, mid := range ids {
					_ = r.Attachments.EraseMessage(ctx, acc.ID, mid)
				}
			}
		}
	}
	r.State.DeltaLinks = map[string]string{}
	r.State.MailboxIDs = map[string]string{}
	r.State.NextUID = map[string]uint32{}
	r.State.UIDValidity++
	r.known = map[string]*index.Message{}
	if r.Repo != nil {
		return r.Repo.Save(ctx, r.State)
	}
	return nil
}

func (r *Runner) applyFlags(ctx context.Context, acc Account, path, special, mbxID string, m Message, ex *index.Message, emit bool) (*events.Draft, error) {
	flags := FlagsFromGraph(m.IsRead, m.Flagged, m.IsDraft)
	old := ex.Flags
	ex.Flags = flags
	if r.known != nil {
		r.known[m.ID] = ex
	}
	if !emit || sameFlags(old, flags) {
		return nil, nil
	}
	data := &types.MessageUpdated{
		AccountRef: types.AccountRef{AccountID: acc.ID, ExternalID: acc.ExternalID},
		MessageID:  ex.ID, Mailbox: types.Mailbox{MailboxID: mbxID, Path: path, SpecialUse: special},
		UID: ex.UID, Flags: flagDiff(old, flags),
	}
	draft := r.draftOf(acc, events.MessageUpdated, ex.ID, data)
	if r.Write != nil && r.Index != nil {
		if err := r.Write(ctx, []events.Draft{draft}, func(tx context.Context) error {
			return r.Index.UpdateFlags(tx, ex.ID, flags, 0)
		}); err != nil {
			return nil, err
		}
		return nil, nil
	}
	return &draft, nil
}

func (r *Runner) lookupProviders(ctx context.Context, accountID string, ids []string) (map[string]*index.Message, error) {
	out := map[string]*index.Message{}
	if r.Index != nil {
		rows, err := r.Index.MessagesByProviderIDs(ctx, accountID, ids)
		if err != nil {
			return nil, err
		}
		for _, m := range rows {
			if m != nil && m.ProviderID != "" {
				out[m.ProviderID] = m
			}
		}
		return out, nil
	}
	for _, id := range ids {
		if m := r.known[id]; m != nil {
			out[id] = m
		}
	}
	return out, nil
}

func (r *Runner) fullResync(ctx context.Context, acc Account, token, folderID, path, special string) error {
	oldUV := r.State.UIDValidity
	r.State.UIDValidity++
	if r.State.UIDValidity == 0 {
		r.State.UIDValidity = 1
	}
	oldID := r.State.MailboxIDs[path]
	r.State.NextUID[path] = 1
	delete(r.State.DeltaLinks, folderID)
	if r.known != nil {
		for id, m := range r.known {
			if m.MailboxID == oldID {
				delete(r.known, id)
			}
		}
	}
	if oldID != "" && r.Index != nil && r.Write != nil {
		_ = r.Write(ctx, nil, func(tx context.Context) error {
			_, err := r.Index.ClearMailbox(tx, oldID)
			return err
		})
	}

	mb := indexMailbox(acc, r.State, path, special)
	page, err := r.Client.DeltaMessages(ctx, token, folderID, "")
	if err != nil {
		return err
	}
	n := 0
	var msgs []*index.Message
	for page != nil {
		for _, m := range page.Messages {
			if m.Removed {
				continue
			}
			entry, _, err := r.indexNew(ctx, acc, token, path, special, m)
			if err != nil {
				return err
			}
			if entry != nil {
				msgs = append(msgs, entry)
				n++
			}
		}
		if page.NextLink != "" {
			page, err = r.Client.DeltaMessages(ctx, token, folderID, page.NextLink)
			if err != nil {
				return err
			}
			continue
		}
		if page.DeltaLink != "" {
			r.State.DeltaLinks[folderID] = page.DeltaLink
		}
		break
	}
	mb.UIDNext = r.State.NextUID[path]
	mb.Exists = uint32(n) // #nosec G115 -- message count fits uint32 mailbox Exists
	draft := r.draftOf(acc, events.MailboxReset, mb.ID, &types.MailboxReset{
		AccountRef:     types.AccountRef{AccountID: acc.ID, ExternalID: acc.ExternalID},
		Mailbox:        types.Mailbox{MailboxID: mb.ID, Path: path, SpecialUse: special},
		OldUIDValidity: oldUV,
		NewUIDValidity: r.State.UIDValidity,
		Messages:       n,
	})
	return r.commit(ctx, acc, []events.Draft{draft}, msgs, []*index.Mailbox{mb})
}

func (r *Runner) indexNew(ctx context.Context, acc Account, token, path, special string, m Message) (*index.Message, []byte, error) {
	uid := r.State.NextUID[path]
	if uid == 0 {
		uid = 1
	}
	r.State.NextUID[path] = uid + 1
	ourID := store.NewID(store.PrefixMessage)
	var raw []byte
	if r.shouldFetchMIME(m) {
		if b, err := r.Client.GetMIME(ctx, token, m.ID); err == nil {
			raw = b
			m.Raw = b
			if m.Size == 0 {
				m.Size = int64(len(b))
			}
		}
	} else if len(m.Raw) > 0 {
		raw = m.Raw
	}
	mbxID := r.State.MailboxIDs[path]
	data := BuildReceived(acc, mbxID, path, special, uid, r.State.UIDValidity, m, raw, Needs{})
	data.MessageID = ourID
	entry := indexEntry(acc, r.State, data, m.ID, m.ConversationID)
	if r.known == nil {
		r.known = map[string]*index.Message{}
	}
	r.known[m.ID] = entry
	return entry, raw, nil
}

func (r *Runner) shouldFetchMIME(m Message) bool {
	if r.Config.SkipMIME || len(m.Raw) > 0 {
		return false
	}
	return true
}

func (r *Runner) commit(ctx context.Context, acc Account, drafts []events.Draft, msgs []*index.Message, mboxes []*index.Mailbox) error {
	if r.Write != nil {
		return r.Write(ctx, drafts, func(tx context.Context) error {
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
			if len(msgs) == 0 {
				return nil
			}
			return r.Index.InsertMessages(tx, msgs)
		})
	}
	for _, d := range drafts {
		if err := r.emit(ctx, acc, d.Type, d.Subject, d.Data); err != nil {
			return err
		}
	}
	return nil
}

func (r *Runner) emit(ctx context.Context, acc Account, typ, subject string, data any) error {
	if r.Sink == nil {
		return nil
	}
	return r.Sink.Emit(ctx, acc.OrganizationID, acc.ID, typ, subject, data)
}

// EnsureSubscription creates or renews a change-notification subscription.
func (r *Runner) EnsureSubscription(ctx context.Context, acc Account, renewBefore time.Duration) error {
	r.ensureState(acc)
	if acc.NotificationURL == "" {
		return nil
	}
	token, _, err := r.Tokens.AccessToken(ctx, acc.ID)
	if err != nil {
		return err
	}
	now := r.now()
	if r.State.SubscriptionID != "" && r.State.SubscriptionExpires != nil {
		if r.State.SubscriptionExpires.After(now.Add(renewBefore)) {
			return nil
		}
		exp := now.Add(3 * 24 * time.Hour)
		sub, err := r.Client.RenewSubscription(ctx, token, r.State.SubscriptionID, exp)
		if err == nil {
			r.State.SubscriptionExpires = &sub.Expiration
			r.State.UpdatedAt = now
			if r.Repo != nil {
				return r.Repo.Save(ctx, r.State)
			}
			return nil
		}
		// fall through to create
	}
	if r.State.ClientState == "" {
		r.State.ClientState = randomState()
	}
	resource := "/me/mailFolders('inbox')/messages"
	if acc.UPN != "" {
		resource = "/users/" + acc.UPN + "/mailFolders('inbox')/messages"
	}
	exp := now.Add(3 * 24 * time.Hour)
	sub, err := r.Client.CreateSubscription(ctx, token, Subscription{
		ChangeType: "created,updated,deleted", NotificationURL: acc.NotificationURL,
		Resource: resource, Expiration: exp, ClientState: r.State.ClientState,
	})
	if err != nil {
		return err
	}
	r.State.SubscriptionID = sub.ID
	r.State.SubscriptionExpires = &sub.Expiration
	r.State.UpdatedAt = now
	if r.Repo != nil {
		return r.Repo.Save(ctx, r.State)
	}
	return nil
}

func randomState() string {
	var b [32]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// NotificationURLFromCallback builds {EB_CALLBACK_URL}/callbacks/graph.
func NotificationURLFromCallback(callbackURL string) string {
	base := strings.TrimRight(strings.TrimSpace(callbackURL), "/")
	if base == "" {
		return ""
	}
	return base + "/callbacks/graph"
}
