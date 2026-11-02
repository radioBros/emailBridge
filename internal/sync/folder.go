package sync

import (
	"context"
	"log/slog"
	"net/textproto"
	"slices"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/radiobros/emailbridge/internal/content"
	"github.com/radiobros/emailbridge/internal/events"
	"github.com/radiobros/emailbridge/internal/events/types"
	"github.com/radiobros/emailbridge/internal/index"
	"github.com/radiobros/emailbridge/internal/logging"
	"github.com/radiobros/emailbridge/internal/mailproto/imapc"
	"github.com/radiobros/emailbridge/internal/mailproto/mime"
	"github.com/radiobros/emailbridge/internal/msgauth"
	"github.com/radiobros/emailbridge/internal/pec"
	"github.com/radiobros/emailbridge/internal/pecreceipts"
	"github.com/radiobros/emailbridge/internal/store"
)

// Mailbox status values beyond index's active/deleted: a folder stays "indexing" until its silent
// baseline completed, so a crash mid-baseline resumes the baseline instead of treating a half-built index
// as complete.
const statusIndexing = "indexing"

// baseHeaders are always fetched for new messages (identity and threading).
var baseHeaders = []string{"Message-ID", "In-Reply-To", "References"}

func (r *runner) mbRef(m *index.Mailbox) types.Mailbox {
	return types.Mailbox{MailboxID: m.ID, Path: m.Path, SpecialUse: m.SpecialUse}
}

func (r *runner) accRef() types.AccountRef {
	return types.AccountRef{AccountID: r.acc.ID, ExternalID: r.acc.ExternalID}
}

// syncFolder runs one pass over a folder (S5 §4): baseline, UIDVALIDITY reset, new mail, flag changes and
// deletions. full forces the full flag/deletion diff. The folder stays selected afterwards.
func (r *runner) syncFolder(ctx context.Context, c *imapc.Client, info imapc.MailboxInfo, full bool) error {
	if stored := r.mailboxes[info.Name]; !full && c.QResync() && stored != nil && stored.Status != statusIndexing && stored.HighestModSeq > 0 {
		return r.syncFolderQResync(ctx, c, info, stored)
	}
	sel, err := c.Select(ctx, info.Name, true)
	if err != nil {
		return err
	}
	stored := r.mailboxes[info.Name]
	switch {
	case stored == nil || stored.Status == statusIndexing:
		return r.baseline(ctx, c, info, sel, stored, nil)
	case stored.UIDValidity != sel.UIDValidity:
		return r.reset(ctx, c, info, sel, stored)
	}
	newCount, err := r.fetchNew(ctx, c, stored, sel.UIDNext)
	if err != nil {
		return err
	}
	condstore := c.Has("CONDSTORE") && stored.HighestModSeq > 0 && sel.HighestModSeq > 0
	switch {
	case full || !condstore && r.dueFull(stored):
		err = r.fullDiff(ctx, c, stored)
	case condstore:
		if sel.HighestModSeq > stored.HighestModSeq {
			err = r.changedSince(ctx, c, stored)
		}
		if err == nil && sel.Exists != stored.Exists+uint32(newCount) { //nolint:gosec // G115: counts are small
			err = r.deletionDiff(ctx, c, stored)
		}
	}
	if err != nil {
		return err
	}
	return r.saveBaseline(ctx, stored, sel.UIDNext, sel.HighestModSeq, sel.Exists)
}

// syncFolderQResync is the qresync mode of S5 §4: one SELECT … (QRESYNC (uidvalidity modseq)) reports the
// UIDs expunged and the flags changed since the stored modseq, so no full diff is needed. A requested full
// sync (mode full) still takes the full-diff path as a safety net.
func (r *runner) syncFolderQResync(ctx context.Context, c *imapc.Client, info imapc.MailboxInfo, stored *index.Mailbox) error {
	sel, qr, err := c.SelectQResync(ctx, info.Name, true, stored.UIDValidity, stored.HighestModSeq)
	if err != nil {
		return err
	}
	if sel.UIDValidity != stored.UIDValidity { // the server ignored our parameters: the folder was recreated
		return r.reset(ctx, c, info, sel, stored)
	}
	if _, err := r.fetchNew(ctx, c, stored, sel.UIDNext); err != nil {
		return err
	}
	if len(qr.Changed) > 0 {
		uids := make([]uint32, 0, len(qr.Changed))
		cur := make(map[uint32]*imapc.Message, len(qr.Changed))
		for i := range qr.Changed {
			uids = append(uids, qr.Changed[i].UID)
			cur[qr.Changed[i].UID] = &qr.Changed[i]
		}
		known, err := r.ix.MessagesByUID(ctx, stored.ID, uids)
		if err != nil {
			return err
		}
		if err := r.applyFlags(ctx, stored, known, cur); err != nil {
			return err
		}
	}
	if len(qr.Vanished) > 0 {
		ranges := make([]index.UIDRange, len(qr.Vanished))
		for i, v := range qr.Vanished {
			ranges[i] = index.UIDRange{Start: v.Start, Stop: v.Stop}
		}
		known, err := r.ix.MessagesInRanges(ctx, stored.ID, ranges)
		if err != nil {
			return err
		}
		if err := r.applyDeletions(ctx, stored, known, func(uint32) bool { return false }); err != nil {
			return err
		}
	}
	return r.saveBaseline(ctx, stored, sel.UIDNext, sel.HighestModSeq, sel.Exists)
}

func (r *runner) dueFull(m *index.Mailbox) bool {
	if r.cfg.FullResync <= 0 {
		return false
	}
	last := r.lastFull[m.ID]
	return last.IsZero() || r.now().Sub(last) >= r.cfg.FullResync
}

// saveBaseline records the folder's server state after a pass.
func (r *runner) saveBaseline(ctx context.Context, m *index.Mailbox, uidNext uint32, modseq uint64, exists uint32) error {
	if uidNext > m.UIDNext {
		m.UIDNext = uidNext
	}
	if modseq > 0 {
		m.HighestModSeq = modseq
	}
	m.Exists = exists
	now := r.now()
	m.LastSyncAt = &now
	return r.ix.Fenced(ctx, r.acc.ID, r.epoch, func(tx context.Context) error { return r.ix.SaveMailbox(tx, m) })
}

// listUIDs returns every UID ≥ from in the selected folder (UID FETCH from:* returns the last message even
// when from is beyond it, so the result is filtered).
func listUIDs(ctx context.Context, c *imapc.Client, from uint32) ([]uint32, error) {
	if from == 0 {
		from = 1
	}
	msgs, err := c.Fetch(ctx, from, 0, imapc.FetchOptions{})
	if err != nil {
		return nil, err
	}
	out := make([]uint32, 0, len(msgs))
	for _, m := range msgs {
		if m.UID >= from {
			out = append(out, m.UID)
		}
	}
	slices.Sort(out)
	return out, nil
}

// listUIDsSince returns UIDs with INTERNALDATE ≥ since (UID SEARCH SINCE), for syncFrom baselines.
func listUIDsSince(ctx context.Context, c *imapc.Client, since time.Time) ([]uint32, error) {
	since = since.UTC()
	uids, err := c.Search(ctx, imapc.SearchCriteria{Since: &since})
	if err != nil {
		return nil, err
	}
	slices.Sort(uids)
	return uids, nil
}

func chunks(uids []uint32, n int) [][]uint32 {
	if n <= 0 {
		n = 100
	}
	var out [][]uint32
	for len(uids) > n {
		out = append(out, uids[:n])
		uids = uids[n:]
	}
	if len(uids) > 0 {
		out = append(out, uids)
	}
	return out
}

// baseline silently indexes a new folder (or resumes an interrupted baseline, or rebuilds after a reset).
// The baseline uidNext is the server's UIDNEXT at the start, so mail arriving meanwhile is still reported.
func (r *runner) baseline(ctx context.Context, c *imapc.Client, info imapc.MailboxInfo, sel *imapc.SelectInfo, m *index.Mailbox, resetFrom *uint32) error {
	created := m == nil
	if created {
		m = &index.Mailbox{ID: store.NewID(store.PrefixMailbox), OrganizationID: r.acc.OrganizationID, AccountID: r.acc.ID,
			Path: info.Name, Delimiter: info.Delimiter, SpecialUse: applySpecialUse(info.Name, info.SpecialUse, r.acc.SpecialUse), UIDValidity: sel.UIDValidity,
			UIDNext: sel.UIDNext, HighestModSeq: sel.HighestModSeq, Status: statusIndexing, SyncMode: "poll"}
		var drafts []events.Draft
		if r.initialized && resetFrom == nil { // a folder that appeared later (not the account's first sync)
			drafts = append(drafts, r.draft(events.MailboxCreated, m.ID, &types.MailboxChanged{AccountRef: r.accRef(), Mailbox: r.mbRef(m)}))
		}
		if err := r.write(ctx, drafts, func(tx context.Context) error { return r.ix.SaveMailbox(tx, m) }); err != nil {
			return err
		}
		r.mailboxes[m.Path] = m
	} else if su := applySpecialUse(m.Path, m.SpecialUse, r.acc.SpecialUse); su != m.SpecialUse {
		m.SpecialUse = su
	}
	baselineNext := m.UIDNext
	var uids []uint32
	var err error
	if !r.acc.SyncFrom.IsZero() && resetFrom == nil && (created || m.Status == statusIndexing) {
		uids, err = listUIDsSince(ctx, c, r.acc.SyncFrom)
	} else {
		uids, err = listUIDs(ctx, c, 1)
	}
	if err != nil {
		return err
	}
	var old []uint32
	for _, u := range uids {
		if u < baselineNext {
			old = append(old, u)
		}
	}
	indexed := 0
	for _, batch := range chunks(old, r.cfg.FetchBatch*5) {
		msgs, err := c.FetchUIDs(ctx, batch, imapc.FetchOptions{Flags: true, InternalDate: true, Size: true, ModSeq: true,
			Envelope: true, HeaderFields: baseHeaders})
		if err != nil {
			return err
		}
		entries := make([]*index.Message, 0, len(msgs))
		for i := range msgs {
			entries = append(entries, r.entry(m, &msgs[i], headerValue(msgs[i].Header, "Message-ID"), parseHeaders(msgs[i].Header)))
		}
		if err := r.write(ctx, nil, func(tx context.Context) error { return r.ix.InsertMessages(tx, entries) }); err != nil {
			return err
		}
		indexed += len(entries)
	}
	m.Status = "active"
	var drafts []events.Draft
	if resetFrom != nil {
		drafts = append(drafts, r.draft(events.MailboxReset, m.ID, &types.MailboxReset{AccountRef: r.accRef(), Mailbox: r.mbRef(m),
			OldUIDValidity: *resetFrom, NewUIDValidity: m.UIDValidity, Messages: indexed}))
	}
	if err := r.write(ctx, drafts, func(tx context.Context) error { return r.ix.SaveMailbox(tx, m) }); err != nil {
		return err
	}
	r.lastFull[m.ID] = r.now()
	// mail that arrived during the baseline (uid ≥ baseline) is new mail: re-list (serverNext 0) because the
	// SELECT snapshot predates it
	n, err := r.fetchNew(ctx, c, m, 0)
	if err != nil {
		return err
	}
	return r.saveBaseline(ctx, m, sel.UIDNext, sel.HighestModSeq, sel.Exists+uint32(n)) //nolint:gosec // G115: counts are small
}

// reset handles a UIDVALIDITY change: drop the folder's index and rebuild it silently with new IDs.
func (r *runner) reset(ctx context.Context, c *imapc.Client, info imapc.MailboxInfo, sel *imapc.SelectInfo, m *index.Mailbox) error {
	oldValidity := m.UIDValidity
	known, _ := r.ix.MessageStates(ctx, m.ID)
	var purgeIDs []string
	for _, e := range known {
		purgeIDs = append(purgeIDs, e.ID)
	}
	m.UIDValidity, m.UIDNext, m.HighestModSeq, m.Status = sel.UIDValidity, sel.UIDNext, sel.HighestModSeq, statusIndexing
	if err := r.write(ctx, nil, func(tx context.Context) error {
		if _, err := r.ix.ClearMailbox(tx, m.ID); err != nil {
			return err
		}
		return r.ix.SaveMailbox(tx, m)
	}); err != nil {
		return err
	}
	r.eraseAttachmentsBestEffort(ctx, purgeIDs)
	return r.baseline(ctx, c, info, sel, m, &oldValidity)
}

// entry builds the index entry of a fetched message.
func (r *runner) entry(m *index.Mailbox, msg *imapc.Message, messageID string, hdr mime.Header) *index.Message {
	e := &index.Message{ID: store.NewID(store.PrefixMessage), OrganizationID: r.acc.OrganizationID, AccountID: r.acc.ID,
		MailboxID: m.ID, UIDValidity: m.UIDValidity, UID: msg.UID, ModSeq: msg.ModSeq, Flags: orEmpty(normFlags(msg.Flags)),
		MessageIDHeader: messageID, EmailID: index.EmailID(messageID, msg.InternalDate, msg.Size),
		InternalDate: msg.InternalDate.UTC(), Size: msg.Size, IndexedAt: r.now()}
	subject := ""
	if env := msg.Envelope; env != nil {
		subject = env.Subject
		e.Subject = truncate(env.Subject, index.SummarySubject)
		if len(env.From) > 0 {
			e.From = &index.Address{Name: env.From[0].Name, Address: env.From[0].Address}
		}
		for i, a := range env.To {
			if i >= index.SummaryRecipients {
				break
			}
			e.To = append(e.To, index.Address{Name: a.Name, Address: a.Address})
		}
		if !env.Date.IsZero() {
			d := env.Date.UTC()
			e.Date = &d
		}
	}
	e.ThreadID = content.ThreadID(messageID, firstOr(hdr["in-reply-to"], ""), strings.Fields(strings.Join(hdr["references"], " ")), subject)
	return e
}

// fetchNew indexes and reports messages with UID ≥ the folder's stored uidNext (S5 §4.1, §5).
func (r *runner) fetchNew(ctx context.Context, c *imapc.Client, m *index.Mailbox, serverNext uint32) (int, error) {
	if serverNext != 0 && serverNext <= m.UIDNext {
		return 0, nil
	}
	hadBaseline := m.Status == "active"
	storedNext := m.UIDNext
	uids, err := listUIDs(ctx, c, storedNext)
	if err != nil || len(uids) == 0 {
		return 0, err
	}
	// New mail starts a trace: the events of this fetch carry it to routing and webhook delivery (S12 §10).
	// Passes that find nothing new start no span, so idle accounts export nothing.
	ctx, span := logging.StartSpan(ctx, "sync.fetch", "", trace.SpanKindClient, attribute.Int("eb.messages", len(uids)))
	defer span.End()
	needs, err := r.needs.Needs(ctx, r.acc.OrganizationID)
	if err != nil {
		needs = Needs{}
	}
	needs = r.applyAccountNeeds(needs)
	// BODYSTRUCTURE is always fetched: attachment rows (the download API) exist for every reportable message,
	// not only when an endpoint asks for attachment metadata (decision 2026-10-27).
	opts := imapc.FetchOptions{Flags: true, Envelope: true, InternalDate: true, Size: true, ModSeq: true, Body: true}
	opts.HeaderFields, opts.FullHeader = headerList(needs)
	if r.acc.BounceDetection && !opts.FullHeader {
		opts.HeaderFields = append(opts.HeaderFields, bounceHeaders...)
	}
	if r.acc.PEC && !opts.FullHeader {
		opts.HeaderFields = append(opts.HeaderFields, pec.CandidateHeaders...)
	}
	total := 0
	for _, batch := range chunks(uids, r.cfg.FetchBatch) {
		msgs, err := c.FetchUIDs(ctx, batch, opts)
		if err != nil {
			return total, err
		}
		got := map[uint32]bool{}
		entries := make([]*index.Message, 0, len(msgs))
		var drafts []events.Draft
		var receipts []*pecreceipts.Receipt
		seenReceipts := map[string]string{} // a receipt twice in one batch is stored once
		maxUID := uint32(0)
		for i := range msgs {
			msg := &msgs[i]
			got[msg.UID] = true
			maxUID = max(maxUID, msg.UID)
			hdr := parseHeaders(msg.Header)
			mid := imapc.MsgID(firstOr(hdr["message-id"], msg.Envelope.MessageID))
			e := r.entry(m, msg, mid, hdr)
			entries = append(entries, e)
			if !reportable(hadBaseline, storedNext, msg.UID, msg.InternalDate, r.acc.NotifyFrom, r.now(), r.cfg.CatchupMax) {
				continue
			}
			raw, rawErr := r.fetchRawShared(ctx, c, msg, needs)
			if r.acc.ArchiveRaw && r.archive != nil {
				if rawErr != nil {
					return total, rawErr
				}
				key, err := r.archive.PutRaw(ctx, r.acc.OrganizationID, r.acc.ID, e.ID, msg.InternalDate, raw)
				if err != nil {
					return total, err
				}
				e.ArchiveRef = key
			}
			data, err := r.received(ctx, c, m, msg, e, hdr, needs, raw)
			if err != nil {
				return total, err
			}
			r.ingestAttachments(ctx, e.ID, msg.Body, data, raw, msg.Size)
			r.scoreSpam(ctx, c, msg, e, data)
			extra := r.classifyBounce(ctx, c, msg, e, hdr, data)
			rc, pecDrafts, err := r.detectPEC(ctx, c, msg, e, hdr, data, seenReceipts)
			if err != nil {
				return total, err // retried with the whole batch: evidence is written before the receipt event
			}
			if rc != nil {
				receipts = append(receipts, rc)
			}
			drafts = append(drafts, r.draft(events.MessageReceived, e.ID, data))
			drafts = append(drafts, extra...)
			drafts = append(drafts, pecDrafts...)
		}
		for _, u := range batch {
			if !got[u] && hadBaseline && u >= storedNext {
				drafts = append(drafts, r.draft(events.MessageMissing, store.NewID(store.PrefixMessage), &types.MessageMissing{AccountRef: r.accRef(), Mailbox: r.mbRef(m), UID: u}))
			}
			maxUID = max(maxUID, u)
		}
		next := max(m.UIDNext, maxUID+1)
		if err := r.write(ctx, drafts, func(tx context.Context) error {
			if err := r.ix.InsertMessages(tx, entries); err != nil {
				return err
			}
			for _, rc := range receipts {
				if err := r.pec.Receipts.Insert(tx, rc); err != nil {
					return err
				}
			}
			mm := *m
			mm.UIDNext = next
			return r.ix.SaveMailbox(tx, &mm)
		}); err != nil {
			return total, err
		}
		m.UIDNext = next
		total += len(entries)
		r.metrics.newMessages(len(drafts))
	}
	return total, nil
}

func headerList(n Needs) (fields []string, full bool) {
	fields = append([]string(nil), baseHeaders...)
	if n.AuthResults == "header" || n.AuthResults == "verify" {
		fields = append(fields, "Authentication-Results")
	}
	for _, h := range n.Headers {
		if h == "*" {
			return nil, true
		}
		fields = append(fields, h)
	}
	return fields, false
}

// applyAccountNeeds overlays account-level auth + allow switches onto organisation payload needs.
func (r *runner) applyAccountNeeds(n Needs) Needs {
	if r.acc.AuthResults != "" {
		n.AuthResults = r.acc.AuthResults
		n.TrustedAuth = r.acc.TrustedAuth
	}
	n.WebSafeHTML = n.WebSafeHTML && !r.acc.DenyWebSafeHTML
	n.CalendarEvents = n.CalendarEvents && !r.acc.DenyCalendarEvents
	n.ReplyText = n.ReplyText && !r.acc.DenyReplyText
	return n
}

// fetchRawShared fetches the capped raw once when archive, body parts, verify, or attachment ingest need it.
func (r *runner) fetchRawShared(ctx context.Context, c *imapc.Client, msg *imapc.Message, n Needs) ([]byte, error) {
	need := r.acc.ArchiveRaw || n.Text || n.WebSafeHTML || n.CalendarEvents || n.ReplyText ||
		n.AuthResults == "verify" || r.acc.AttachmentsS3 || r.acc.AttachmentsAV
	if !need {
		return nil, nil
	}
	capN := int64(archiveCap)
	if !r.acc.ArchiveRaw {
		capN = rawTextCap
	}
	if n.AuthResults == "verify" && capN < msgauth.MaxRawBytes {
		capN = msgauth.MaxRawBytes
	}
	return c.Raw(ctx, msg.UID, capN)
}

func parseHeaders(raw []byte) mime.Header {
	if len(raw) == 0 {
		return mime.Header{}
	}
	h, err := mime.ParseHeaders(append(append([]byte(nil), raw...), '\r', '\n'))
	if err != nil {
		return mime.Header{}
	}
	return h
}

func headerValue(raw []byte, name string) string {
	return imapc.MsgID(firstOr(parseHeaders(raw)[strings.ToLower(name)], ""))
}

func firstOr(v []string, def string) string {
	if len(v) > 0 && strings.TrimSpace(v[0]) != "" {
		return strings.TrimSpace(v[0])
	}
	return def
}

// received builds the message.received payload (S3 §3.5) with the parts the organisation needs.
func (r *runner) received(ctx context.Context, c *imapc.Client, m *index.Mailbox, msg *imapc.Message, e *index.Message, hdr mime.Header, n Needs, raw []byte) (*types.MessageReceived, error) {
	env := msg.Envelope
	if env == nil {
		env = &imapc.Envelope{}
	}
	d := &types.MessageReceived{AccountRef: r.accRef(), MessageID: e.ID, Mailbox: r.mbRef(m), UID: msg.UID, UIDValidity: m.UIDValidity,
		EmailID: e.EmailID, ThreadID: content.ThreadID(e.MessageIDHeader, firstOr(hdr["in-reply-to"], ""),
			strings.Fields(strings.Join(hdr["references"], " ")), env.Subject), MessageIDHeader: e.MessageIDHeader,
		InternalDate: msg.InternalDate.UTC(), Size: msg.Size, Flags: orEmpty(normFlags(msg.Flags)), Labels: []string{},
		To: addrs(env.To), Cc: addrs(env.Cc), Subject: env.Subject, ReplyTo: addrs(env.ReplyTo)}
	if !env.Date.IsZero() {
		t := env.Date.UTC()
		d.Date = &t
	}
	if len(env.From) > 0 {
		a := types.Address(env.From[0])
		d.From = &a
	}
	if len(env.Sender) > 0 && (len(env.From) == 0 || env.Sender[0].Address != env.From[0].Address) {
		a := types.Address(env.Sender[0])
		d.Sender = &a
	}
	if len(env.Bcc) > 0 {
		d.Bcc = addrs(env.Bcc)
	}
	if v := firstOr(hdr["in-reply-to"], ""); v != "" {
		d.InReplyTo = imapc.MsgID(v)
	}
	if refs := strings.Fields(strings.Join(hdr["references"], " ")); len(refs) > 0 {
		d.References = refs
	}
	if len(n.Headers) > 0 {
		d.Headers = selectHeaders(hdr, n.Headers)
	}
	r.fillAuthResults(ctx, hdr, n, raw, d)
	if n.Attachments && msg.Body != nil {
		d.Attachments = attachments(msg.Body)
	}
	if n.Text || n.WebSafeHTML || n.CalendarEvents || n.ReplyText {
		r.bodyParts(ctx, c, msg.UID, n, d, raw)
	}
	seen, err := r.ix.SeenElsewhere(ctx, r.acc.ID, m.ID, e.EmailID)
	if err != nil {
		return nil, err
	}
	d.SeenBefore = seen
	if d.To == nil {
		d.To = []types.Address{}
	}
	if d.Cc == nil {
		d.Cc = []types.Address{}
	}
	return d, nil
}

func (r *runner) fillAuthResults(ctx context.Context, hdr mime.Header, n Needs, raw []byte, d *types.MessageReceived) {
	if n.AuthResults == "" {
		return
	}
	mh := msgauth.MapHeaders{}
	for k, v := range hdr {
		mh[textproto.CanonicalMIMEHeaderKey(k)] = v
	}
	switch n.AuthResults {
	case "header":
		if len(n.TrustedAuth) == 0 {
			return
		}
		if res, err := msgauth.FromHeaders(mh, n.TrustedAuth); err == nil && res != nil {
			d.AuthResults = convertAuth(res)
		}
	case "verify":
		if len(raw) == 0 {
			return
		}
		spf := msgauth.Item{}
		if len(n.TrustedAuth) > 0 {
			if hr, err := msgauth.FromHeaders(mh, n.TrustedAuth); err == nil && hr != nil {
				spf = hr.SPF
			}
		}
		res, err := msgauth.Verify(ctx, raw, spf, msgauth.Resolver{})
		if err != nil {
			r.log.LogAttrs(ctx, slog.LevelWarn, "sync: auth verify failed",
				slog.String("account_id", r.acc.ID), slog.Any("err", err))
			return
		}
		d.AuthResults = convertAuth(res)
	}
}

func addrs(in []imapc.Address) []types.Address {
	if len(in) == 0 {
		return nil
	}
	out := make([]types.Address, len(in))
	for i, a := range in {
		out[i] = types.Address(a)
	}
	return out
}

func selectHeaders(h mime.Header, names []string) map[string][]string {
	out := map[string][]string{}
	for _, n := range names {
		if n == "*" {
			for k, v := range h {
				out[k] = v
			}
			return out
		}
		if v, ok := h[strings.ToLower(n)]; ok {
			out[strings.ToLower(n)] = v
		}
	}
	return out
}

// threadID groups a conversation by its root Message-ID (first References entry, else In-Reply-To, else
// the message itself).

func convertAuth(r *msgauth.Results) *types.AuthResults {
	item := func(i msgauth.Item) *types.AuthResult {
		return &types.AuthResult{Result: i.Result, Domain: i.Domain, Selector: i.Selector}
	}
	out := &types.AuthResults{SPF: item(r.SPF), DMARC: item(r.DMARC), ARC: item(r.ARC), DKIM: []types.AuthResult{}, Source: string(r.Source)}
	for _, d := range r.DKIM {
		out.DKIM = append(out.DKIM, *item(d))
	}
	return out
}

func attachments(p *imapc.Part) []types.AttachmentInfo {
	var out []types.AttachmentInfo
	var walk func(p *imapc.Part)
	walk = func(p *imapc.Part) {
		if len(p.Children) > 0 {
			for _, ch := range p.Children {
				walk(ch)
			}
			return
		}
		if p.Disposition == "attachment" || p.Filename != "" {
			out = append(out, types.AttachmentInfo{Filename: p.Filename, ContentType: p.MediaType, Size: int64(p.Size),
				ContentID: p.ContentID, Disposition: p.Disposition, PartID: imapc.PathString(p.Path)})
		}
	}
	walk(p)
	if out == nil {
		out = []types.AttachmentInfo{}
	}
	return out
}

// archiveCap bounds the raw source stored by inbound.archiveRaw.
const archiveCap = 50 << 20

// rawTextCap bounds the raw fetch used to extract text parts (matches msgauth.MaxRawBytes).
const rawTextCap = 10 << 20

// bodyParts fills text / web-safe HTML / calendar / replyText from raw (already fetched when possible).
func (r *runner) bodyParts(ctx context.Context, c *imapc.Client, uid uint32, n Needs, d *types.MessageReceived, raw []byte) {
	maxText := n.TextMax
	if maxText <= 0 {
		maxText = 256 << 10
	}
	var err error
	if len(raw) == 0 {
		raw, err = c.Raw(ctx, uid, rawTextCap)
		if err != nil {
			if n.Text {
				d.Text = &types.TextContent{Truncated: true}
			}
			return
		}
	}
	capped := len(raw) >= rawTextCap
	ent, err := mime.ParseEntity(raw)
	if err != nil {
		if n.Text {
			d.Text = &types.TextContent{Truncated: true, Size: int64(len(raw))}
		}
		return
	}
	plain, html, truncated, err := mime.TextParts(ent, int(maxText))
	switch {
	case err != nil && n.Text:
		d.Text = &types.TextContent{Truncated: true, Size: int64(len(raw))}
	case n.Text:
		d.Text = &types.TextContent{Plain: plain, HTML: html, Truncated: truncated || capped, Size: int64(len(plain) + len(html))}
	}
	if n.WebSafeHTML && err == nil && html != "" {
		d.WebSafeHTML = content.SanitizeHTML(html, content.HTMLOptions{MaxBytes: int(maxText)}).HTML
	}
	if n.CalendarEvents {
		parts, _ := mime.PartsOfType(raw, calendarPartMax, "text/calendar", "application/ics")
		for _, ics := range parts {
			if evs, err := content.ParseCalendar(ics); err == nil {
				d.CalendarEvents = append(d.CalendarEvents, evs...)
			}
		}
	}
	if n.ReplyText && err == nil {
		if rt := content.ReplyText(plain); rt != "" {
			d.ReplyText = rt
		} else if html != "" {
			d.ReplyText = content.ReplyTextFromHTML(html)
		}
	}
}

// calendarPartMax caps one text/calendar part (invitations are small; a huge one is not an invitation).
const calendarPartMax = 1 << 20

// changedSince applies CONDSTORE flag changes of previously indexed messages.
func (r *runner) changedSince(ctx context.Context, c *imapc.Client, m *index.Mailbox) error {
	msgs, err := c.Fetch(ctx, 1, 0, imapc.FetchOptions{Flags: true, ModSeq: true, ChangedSince: m.HighestModSeq})
	if err != nil {
		return err
	}
	uids := make([]uint32, 0, len(msgs))
	cur := map[uint32]*imapc.Message{}
	for i := range msgs {
		uids = append(uids, msgs[i].UID)
		cur[msgs[i].UID] = &msgs[i]
	}
	known, err := r.ix.MessagesByUID(ctx, m.ID, uids)
	if err != nil {
		return err
	}
	return r.applyFlags(ctx, m, known, cur)
}

// fullDiff compares every UID and flag set with the index: flag changes and deletions.
func (r *runner) fullDiff(ctx context.Context, c *imapc.Client, m *index.Mailbox) error {
	msgs, err := c.Fetch(ctx, 1, 0, imapc.FetchOptions{Flags: true, ModSeq: c.Has("CONDSTORE")})
	if err != nil {
		return err
	}
	cur := map[uint32]*imapc.Message{}
	for i := range msgs {
		cur[msgs[i].UID] = &msgs[i]
	}
	known, err := r.ix.MessageStates(ctx, m.ID)
	if err != nil {
		return err
	}
	if err := r.applyFlags(ctx, m, known, cur); err != nil {
		return err
	}
	if err := r.applyDeletions(ctx, m, known, func(u uint32) bool { return cur[u] != nil }); err != nil {
		return err
	}
	r.lastFull[m.ID] = r.now()
	return nil
}

// deletionDiff finds expunged messages by comparing the server's UID set with the index.
func (r *runner) deletionDiff(ctx context.Context, c *imapc.Client, m *index.Mailbox) error {
	server, err := c.UIDs(ctx)
	if err != nil {
		return err
	}
	set := make(map[uint32]bool, len(server))
	for _, u := range server {
		set[u] = true
	}
	known, err := r.ix.MessageStates(ctx, m.ID)
	if err != nil {
		return err
	}
	return r.applyDeletions(ctx, m, known, func(u uint32) bool { return set[u] })
}

func (r *runner) applyFlags(ctx context.Context, m *index.Mailbox, known map[uint32]*index.Message, cur map[uint32]*imapc.Message) error {
	type change struct {
		e     *index.Message
		flags []string
		mod   uint64
	}
	var changes []change
	var drafts []events.Draft
	for uid, e := range known {
		s := cur[uid]
		if s == nil || flagsEqual(e.Flags, s.Flags) {
			continue
		}
		added, removed := flagDiff(e.Flags, s.Flags)
		changes = append(changes, change{e, orEmpty(normFlags(s.Flags)), s.ModSeq})
		drafts = append(drafts, r.draft(events.MessageUpdated, e.ID, &types.MessageUpdated{AccountRef: r.accRef(), MessageID: e.ID, Mailbox: r.mbRef(m), UID: uid,
			Flags: types.FlagChange{Added: added, Removed: removed, Current: orEmpty(normFlags(s.Flags))}}))
	}
	if len(changes) == 0 {
		return nil
	}
	return r.write(ctx, drafts, func(tx context.Context) error {
		for _, ch := range changes {
			if err := r.ix.UpdateFlags(tx, ch.e.ID, ch.flags, ch.mod); err != nil {
				return err
			}
		}
		return nil
	})
}

func (r *runner) applyDeletions(ctx context.Context, m *index.Mailbox, known map[uint32]*index.Message, exists func(uint32) bool) error {
	var ids []string
	var drafts []events.Draft
	for uid, e := range known {
		if uid >= m.UIDNext || exists(uid) {
			continue
		}
		ids = append(ids, e.ID)
		drafts = append(drafts, r.draft(events.MessageDeleted, e.ID, &types.MessageDeleted{AccountRef: r.accRef(), MessageID: e.ID, Mailbox: r.mbRef(m), UID: uid, MessageIDHeader: e.MessageIDHeader}))
	}
	if len(ids) == 0 {
		return nil
	}
	if err := r.write(ctx, drafts, func(tx context.Context) error { return r.ix.DeleteMessages(tx, ids) }); err != nil {
		return err
	}
	r.eraseAttachmentsBestEffort(ctx, ids)
	return nil
}

// removeFolder handles a folder that disappeared from the server list.
func (r *runner) removeFolder(ctx context.Context, m *index.Mailbox) error {
	known, _ := r.ix.MessageStates(ctx, m.ID)
	var ids []string
	for _, e := range known {
		ids = append(ids, e.ID)
	}
	err := r.write(ctx, []events.Draft{r.draft(events.MailboxDeleted, m.ID, &types.MailboxChanged{AccountRef: r.accRef(), Mailbox: r.mbRef(m)})},
		func(tx context.Context) error { _, err := r.ix.DeleteMailbox(tx, m); return err })
	if err == nil {
		delete(r.mailboxes, m.Path)
		r.eraseAttachmentsBestEffort(ctx, ids)
	}
	return err
}

// applySpecialUse forces SpecialUse from account overrides (role → path).
func applySpecialUse(path, server string, overrides map[string]string) string {
	if len(overrides) == 0 {
		return server
	}
	roleFlag := map[string]string{
		"sent": `\Sent`, "trash": `\Trash`, "junk": `\Junk`, "drafts": `\Drafts`, "archive": `\Archive`,
	}
	for role, p := range overrides {
		if p == path {
			if f, ok := roleFlag[strings.ToLower(role)]; ok {
				return f
			}
		}
	}
	return server
}

// draft builds an event draft of this account.
func (r *runner) draft(typ, subject string, data any) events.Draft {
	return events.Draft{Type: typ, OrganizationID: r.acc.OrganizationID, AccountID: r.acc.ID, Subject: subject, Time: r.now(), Data: data}
}

// write runs fn and stores drafts in one fenced transaction.
func (r *runner) write(ctx context.Context, drafts []events.Draft, fn func(tx context.Context) error) error {
	return r.ix.Fenced(ctx, r.acc.ID, r.epoch, func(tx context.Context) error {
		if err := fn(tx); err != nil {
			return err
		}
		if len(drafts) > 0 {
			if _, err := r.ev.Emit(tx, drafts...); err != nil {
				return err
			}
		}
		return nil
	})
}
