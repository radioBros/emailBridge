package gmail

import (
	"net/mail"
	"net/textproto"
	"strings"
	"time"

	"github.com/radiobros/emailbridge/internal/content"
	"github.com/radiobros/emailbridge/internal/events/types"
	"github.com/radiobros/emailbridge/internal/mailproto/mime"
	"github.com/radiobros/emailbridge/internal/msgauth"
	"github.com/radiobros/emailbridge/internal/store"
)

// MapMessage builds message.received from a Gmail message + RFC822 (when present).
// Per-message identity for later lookups is index.Message.ProviderID (not SyncState).
func MapMessage(acc *Account, st *SyncState, path, specialUse string, msg *Message, needs Needs, labelNames map[string]string) (*types.MessageReceived, error) {
	if st.MailboxIDs == nil {
		st.MailboxIDs = map[string]string{}
	}
	mbxID := st.MailboxIDs[path]
	if mbxID == "" {
		mbxID = store.NewID(store.PrefixMailbox)
		st.MailboxIDs[path] = mbxID
	}
	if st.NextUID == nil {
		st.NextUID = map[string]uint32{}
	}
	uid := st.NextUID[path]
	if uid == 0 {
		uid = 1
	}
	st.NextUID[path] = uid + 1

	flags := FlagsFromLabels(msg.LabelIDs)
	labels := UserLabels(msg.LabelIDs, labelNames)
	ourID := store.NewID(store.PrefixMessage)

	hdr := msg.Headers
	raw := msg.Raw
	if len(raw) > 0 {
		if parsed, err := mime.ParseHeaders(raw); err == nil {
			hdr = parsed
		}
	}
	mid := firstHeader(hdr, "message-id")
	subj := firstHeader(hdr, "subject")
	if subj == "" && len(raw) > 0 {
		if s, err := mime.DecodeSubject(hdr); err == nil {
			subj = s
		}
	}

	d := &types.MessageReceived{
		AccountRef:      types.AccountRef{AccountID: acc.ID, ExternalID: acc.ExternalID},
		MessageID:       ourID,
		Mailbox:         types.Mailbox{MailboxID: mbxID, Path: path, SpecialUse: specialUse},
		UID:             uid,
		UIDValidity:     st.UIDValidity,
		EmailID:         msg.ID,
		ThreadID:        msg.ThreadID,
		MessageIDHeader: mid,
		InternalDate:    msg.InternalDate,
		Size:            msg.Size,
		Flags:           flags,
		Labels:          labels,
		Subject:         subj,
		To:              []types.Address{},
		Cc:              []types.Address{},
	}
	if d.InternalDate.IsZero() {
		d.InternalDate = time.Now().UTC()
	}
	if v := firstHeader(hdr, "in-reply-to"); v != "" {
		d.InReplyTo = v
	}
	if refs := strings.Fields(firstHeader(hdr, "references")); len(refs) > 0 {
		d.References = refs
	}
	if from := parseAddrs(hdr, "from"); len(from) > 0 {
		d.From = &from[0]
	}
	d.To = parseAddrs(hdr, "to")
	d.Cc = parseAddrs(hdr, "cc")
	d.ReplyTo = parseAddrs(hdr, "reply-to")
	if date := firstHeader(hdr, "date"); date != "" {
		if t, err := mail.ParseDate(date); err == nil {
			u := t.UTC()
			d.Date = &u
		}
	}
	if len(needs.Headers) > 0 {
		d.Headers = selectHeaders(hdr, needs.Headers)
	}
	var plain, html string
	if (needs.Text || needs.WebSafeHTML || needs.ReplyText || needs.CalendarEvents) && len(raw) > 0 {
		if ent, err := mime.ParseEntity(raw); err == nil {
			max := int(needs.TextMax)
			if max <= 0 {
				max = 256 << 10
			}
			var trunc bool
			plain, html, trunc, _ = mime.TextParts(ent, max)
			if needs.Text {
				d.Text = &types.TextContent{Plain: plain, HTML: html, Truncated: trunc, Size: int64(len(plain) + len(html))}
			}
			if needs.WebSafeHTML && html != "" {
				d.WebSafeHTML = content.SanitizeHTML(html, content.HTMLOptions{MaxBytes: max}).HTML
			}
			if needs.ReplyText {
				if rt := content.ReplyText(plain); rt != "" {
					d.ReplyText = rt
				} else if html != "" {
					d.ReplyText = content.ReplyTextFromHTML(html)
				}
			}
			if needs.CalendarEvents {
				parts, _ := mime.PartsOfType(raw, 1<<20, "text/calendar", "application/ics")
				for _, ics := range parts {
					if evs, err := content.ParseCalendar(ics); err == nil {
						d.CalendarEvents = append(d.CalendarEvents, evs...)
					}
				}
			}
		}
	}
	if needs.Attachments && len(raw) > 0 {
		d.Attachments = attachmentsFromRaw(raw)
	}
	if needs.AuthResults == "header" && len(needs.TrustedAuth) > 0 {
		mh := msgauth.MapHeaders{}
		for k, v := range hdr {
			mh[textproto.CanonicalMIMEHeaderKey(k)] = v
		}
		if res, err := msgauth.FromHeaders(mh, needs.TrustedAuth); err == nil && res != nil {
			d.AuthResults = convertAuth(res)
		}
	}
	if d.To == nil {
		d.To = []types.Address{}
	}
	if d.Cc == nil {
		d.Cc = []types.Address{}
	}
	if d.Labels == nil {
		d.Labels = []string{}
	}
	if d.Flags == nil {
		d.Flags = []string{}
	}
	return d, nil
}

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

func attachmentsFromRaw(raw []byte) []types.AttachmentInfo {
	ent, err := mime.ParseEntity(raw)
	if err != nil {
		return []types.AttachmentInfo{}
	}
	meta, err := mime.AttachmentsMeta(ent)
	if err != nil || len(meta) == 0 {
		return []types.AttachmentInfo{}
	}
	out := make([]types.AttachmentInfo, 0, len(meta))
	for _, m := range meta {
		if m.Disposition == "attachment" || m.Filename != "" {
			out = append(out, types.AttachmentInfo{
				Filename: m.Filename, ContentType: m.ContentType, Size: m.Size,
				ContentID: m.ContentID, Disposition: m.Disposition, PartID: m.PartID,
			})
		}
	}
	return out
}

func firstHeader(h map[string][]string, name string) string {
	if h == nil {
		return ""
	}
	v := h[strings.ToLower(name)]
	if len(v) == 0 {
		return ""
	}
	return strings.TrimSpace(v[0])
}

func parseAddrs(h map[string][]string, key string) []types.Address {
	if h == nil {
		return nil
	}
	list, err := mime.DecodeAddresses(mime.Header(h), key)
	if err != nil || len(list) == 0 {
		return nil
	}
	out := make([]types.Address, len(list))
	for i, a := range list {
		out[i] = types.Address{Name: a.Name, Address: a.Address}
	}
	return out
}

func selectHeaders(h map[string][]string, names []string) map[string][]string {
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

// FlagDiff computes added/removed/current for message.updated.
func FlagDiff(prev, curr []string) types.FlagChange {
	ps, cs := setOf(prev), setOf(curr)
	var added, removed []string
	for f := range cs {
		if !ps[f] {
			added = append(added, f)
		}
	}
	for f := range ps {
		if !cs[f] {
			removed = append(removed, f)
		}
	}
	if added == nil {
		added = []string{}
	}
	if removed == nil {
		removed = []string{}
	}
	if curr == nil {
		curr = []string{}
	}
	return types.FlagChange{Added: added, Removed: removed, Current: append([]string(nil), curr...)}
}

func setOf(in []string) map[string]bool {
	out := map[string]bool{}
	for _, s := range in {
		out[s] = true
	}
	return out
}
