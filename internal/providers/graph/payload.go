package graph

import (
	"net/textproto"
	"strings"
	"time"

	"github.com/radiobros/emailbridge/internal/content"
	"github.com/radiobros/emailbridge/internal/events/types"
	"github.com/radiobros/emailbridge/internal/mailproto/imapc"
	"github.com/radiobros/emailbridge/internal/mailproto/mime"
	"github.com/radiobros/emailbridge/internal/msgauth"
)

// BuildReceived builds a message.received payload from a Graph message + optional MIME.
// emailId / threadId are the Graph provider IDs (P6.3 §3.4).
func BuildReceived(acc Account, mailboxID, path, specialUse string, uid, uidValidity uint32, m Message, raw []byte, needs Needs) *types.MessageReceived {
	hdr := mime.Header{}
	if len(raw) > 0 {
		if h, err := mime.ParseHeaders(raw); err == nil {
			hdr = h
		}
	}
	mid := m.InternetMessageID
	if mid == "" {
		mid = firstOr(hdr["message-id"], "")
	}
	mid = imapc.MsgID(mid)

	subject := m.Subject
	if subject == "" {
		subject = firstOr(hdr["subject"], "")
	}
	from := m.From
	if from == "" {
		from = extractAddr(firstOr(hdr["from"], ""))
	}
	to := m.To
	if len(to) == 0 {
		to = splitAddrs(firstOr(hdr["to"], ""))
	}
	cc := m.Cc
	if len(cc) == 0 {
		cc = splitAddrs(firstOr(hdr["cc"], ""))
	}

	d := &types.MessageReceived{
		AccountRef:      types.AccountRef{AccountID: acc.ID, ExternalID: acc.ExternalID},
		MessageID:       "", // filled by runner
		Mailbox:         types.Mailbox{MailboxID: mailboxID, Path: path, SpecialUse: specialUse},
		UID:             uid,
		UIDValidity:     uidValidity,
		EmailID:         m.ID,
		ThreadID:        m.ConversationID,
		MessageIDHeader: mid,
		InternalDate:    m.Received,
		Size:            m.Size,
		Flags:           FlagsFromGraph(m.IsRead, m.Flagged, m.IsDraft),
		Labels:          []string{},
		Subject:         subject,
		To:              addrs(to),
		Cc:              addrs(cc),
		SeenBefore:      false,
	}
	if d.InternalDate.IsZero() {
		d.InternalDate = time.Now().UTC()
	}
	if from != "" {
		a := types.Address{Address: from}
		d.From = &a
	}
	if v := firstOr(hdr["in-reply-to"], ""); v != "" {
		d.InReplyTo = imapc.MsgID(v)
	}
	if refs := strings.Fields(strings.Join(hdr["references"], " ")); len(refs) > 0 {
		d.References = refs
	}
	if dateStr := firstOr(hdr["date"], ""); dateStr != "" {
		if t, err := parseDate(dateStr); err == nil {
			d.Date = &t
		}
	}
	if len(needs.Headers) > 0 {
		d.Headers = selectHeaders(hdr, needs.Headers)
	}
	fillPayloadExtras(d, hdr, raw, needs)
	if d.To == nil {
		d.To = []types.Address{}
	}
	if d.Cc == nil {
		d.Cc = []types.Address{}
	}
	if d.Size == 0 && len(raw) > 0 {
		d.Size = int64(len(raw))
	}
	return d
}

func fillPayloadExtras(d *types.MessageReceived, hdr mime.Header, raw []byte, needs Needs) {
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

// IMAPShapedReceived builds the comparable IMAP-style payload from the same RFC822
// (provider ids left empty for the caller to ignore in equality checks).
func IMAPShapedReceived(acc Account, mailboxID, path, specialUse string, uid, uidValidity uint32, raw []byte) *types.MessageReceived {
	hdr, _ := mime.ParseHeaders(raw)
	mid := imapc.MsgID(firstOr(hdr["message-id"], ""))
	subject := firstOr(hdr["subject"], "")
	from := extractAddr(firstOr(hdr["from"], ""))
	to := splitAddrs(firstOr(hdr["to"], ""))
	cc := splitAddrs(firstOr(hdr["cc"], ""))
	d := &types.MessageReceived{
		AccountRef:      types.AccountRef{AccountID: acc.ID, ExternalID: acc.ExternalID},
		Mailbox:         types.Mailbox{MailboxID: mailboxID, Path: path, SpecialUse: specialUse},
		UID:             uid,
		UIDValidity:     uidValidity,
		MessageIDHeader: mid,
		InternalDate:    time.Now().UTC(),
		Size:            int64(len(raw)),
		Flags:           []string{},
		Labels:          []string{},
		Subject:         subject,
		To:              addrs(to),
		Cc:              addrs(cc),
	}
	if from != "" {
		a := types.Address{Address: from}
		d.From = &a
	}
	if v := firstOr(hdr["in-reply-to"], ""); v != "" {
		d.InReplyTo = imapc.MsgID(v)
	}
	if refs := strings.Fields(strings.Join(hdr["references"], " ")); len(refs) > 0 {
		d.References = refs
	}
	if dateStr := firstOr(hdr["date"], ""); dateStr != "" {
		if t, err := parseDate(dateStr); err == nil {
			d.Date = &t
			d.InternalDate = t
		}
	}
	if d.To == nil {
		d.To = []types.Address{}
	}
	if d.Cc == nil {
		d.Cc = []types.Address{}
	}
	return d
}

func firstOr(v []string, def string) string {
	if len(v) > 0 && strings.TrimSpace(v[0]) != "" {
		return strings.TrimSpace(v[0])
	}
	return def
}

func extractAddr(v string) string {
	if i := strings.Index(v, "<"); i >= 0 {
		v = v[i+1:]
		if j := strings.Index(v, ">"); j >= 0 {
			v = v[:j]
		}
	}
	return strings.TrimSpace(v)
}

func splitAddrs(v string) []string {
	if v == "" {
		return nil
	}
	var out []string
	for _, p := range strings.Split(v, ",") {
		if a := extractAddr(strings.TrimSpace(p)); a != "" {
			out = append(out, a)
		}
	}
	return out
}

func addrs(in []string) []types.Address {
	if len(in) == 0 {
		return nil
	}
	out := make([]types.Address, len(in))
	for i, a := range in {
		out[i] = types.Address{Address: a}
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

func parseDate(s string) (time.Time, error) {
	layouts := []string{time.RFC1123Z, time.RFC1123, time.RFC822Z, time.RFC822}
	var err error
	for _, l := range layouts {
		var t time.Time
		t, err = time.Parse(l, s)
		if err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, err
}

func flagDiff(old, cur []string) types.FlagChange {
	os, cs := toSet(old), toSet(cur)
	var added, removed []string
	for f := range cs {
		if !os[f] {
			added = append(added, f)
		}
	}
	for f := range os {
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
	if cur == nil {
		cur = []string{}
	}
	return types.FlagChange{Added: added, Removed: removed, Current: append([]string(nil), cur...)}
}

func toSet(in []string) map[string]bool {
	out := map[string]bool{}
	for _, s := range in {
		out[s] = true
	}
	return out
}

func sameFlags(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	as, bs := toSet(a), toSet(b)
	for k := range as {
		if !bs[k] {
			return false
		}
	}
	return true
}
