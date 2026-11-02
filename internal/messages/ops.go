package messages

import (
	"context"
	"encoding/base64"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/radiobros/emailbridge/internal/content"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/index"
	"github.com/radiobros/emailbridge/internal/mailbox"
	"github.com/radiobros/emailbridge/internal/mailproto/imapc"
	"github.com/radiobros/emailbridge/internal/mailproto/mime"
	"github.com/radiobros/emailbridge/internal/store"
	ebsync "github.com/radiobros/emailbridge/internal/sync"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

// ListFilter is GET /messages query options.
type ListFilter struct {
	MailboxID    string
	ThreadID     string // all messages of a conversation, across folders (P4.4 thread view)
	Flag         string
	Q            string
	Limit        int
	Cursor       string
	Page         int
	Sort         string
	IncludeTotal bool
}

// ListResult is a page of messages.
type ListResult struct {
	Data       []MessageView
	NextCursor *string
	Total      *int64
}

// maxSearchLen caps the `q` search text of ListMessages.
const maxSearchLen = 256

// searchRegex turns user search text into a case-insensitive literal-substring $regex. The text is escaped
// (regexp.QuoteMeta) so it can never inject regex syntax (no ReDoS, `.`/`*`/`(` match literally) and is
// capped at maxSearchLen characters.
func searchRegex(q string) (bson.M, error) {
	if utf8.RuneCountInString(q) > maxSearchLen {
		return nil, errs.New(errs.CodeValidationFailed, "The search text is too long.").WithSafe("max", maxSearchLen)
	}
	return bson.M{"$regex": regexp.QuoteMeta(q), "$options": "i"}, nil
}

// ListMessages lists indexed messages.
func (s *Service) ListMessages(ctx context.Context, accountID string, f ListFilter) (*ListResult, error) {
	if err := tenancy.RequireReadAccount(ctx, "messages", accountID); err != nil {
		return nil, err
	}
	if err := s.requireOwnAccount(ctx, accountID); err != nil {
		return nil, err
	}
	limit := f.Limit
	if limit <= 0 || limit > maxList {
		limit = maxList
	}
	sortField, sortDir, err := parseMsgSort(f.Sort)
	if err != nil {
		return nil, err
	}
	filter := bson.M{"accountId": accountID}
	if f.MailboxID != "" {
		filter["mailboxId"] = f.MailboxID
	}
	if f.ThreadID != "" {
		filter["threadId"] = f.ThreadID
	}
	if f.Flag != "" {
		filter["flags"] = f.Flag
	}
	if q := strings.TrimSpace(f.Q); q != "" {
		rx, err := searchRegex(q)
		if err != nil {
			return nil, err
		}
		filter["$or"] = []bson.M{
			{"messageIdHeader": rx},
			{"emailId": rx},
		}
	}
	filter, err = store.Scoped(ctx, filter)
	if err != nil {
		return nil, err
	}
	findOpts := options.Find().SetLimit(int64(limit + 1))
	sortDoc := bson.D{{Key: sortField, Value: sortDir}}
	if sortField != "_id" {
		sortDoc = append(sortDoc, bson.E{Key: "_id", Value: sortDir})
	}
	findOpts.SetSort(sortDoc)
	if f.Page > 0 {
		findOpts.SetSkip(int64(f.Page-1) * int64(limit))
	} else if f.Cursor != "" {
		cur, err := decodeCursor(f.Cursor)
		if err != nil {
			return nil, errs.New(errs.CodeValidationFailed, "The cursor is invalid.")
		}
		cf, err := cursorFilter(cur, sortField, sortDir)
		if err != nil {
			return nil, err
		}
		filter = bson.M{"$and": []bson.M{filter, cf}}
	}
	c, err := s.DB.Collection(index.Messages).Find(ctx, filter, findOpts)
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "messages.list")
	}
	defer func() { _ = c.Close(ctx) }()
	var rows []msgDoc
	if err := c.All(ctx, &rows); err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "messages.list")
	}
	out := &ListResult{}
	if len(rows) > limit {
		rows = rows[:limit]
		last := rows[len(rows)-1]
		nc := encodeCursor(cursorPayload{
			ID: last.ID, Field: sortField, Desc: sortDir < 0, Key: msgSortKey(&last, sortField),
		})
		out.NextCursor = &nc
	}
	out.Data = make([]MessageView, len(rows))
	for i := range rows {
		out.Data[i] = msgView(&rows[i])
	}
	if f.IncludeTotal && f.Page > 0 {
		n, err := s.DB.Collection(index.Messages).CountDocuments(ctx, filter)
		if err == nil {
			out.Total = &n
		}
	}
	return out, nil
}

// GetOptions control on-demand enrichments for GetMessage.
type GetOptions struct {
	Text                string // plain | html | both
	Headers             bool
	Envelope            bool
	MaxBytes            int64
	MarkAsSeen          bool
	EmbedAttachedImages bool
	PreProcessHTML      bool
}

const (
	embedPartMax  = 256 << 10
	embedTotalMax = 1 << 20
)

// GetMessage returns index metadata, optionally enriched from the account provider.
func (s *Service) GetMessage(ctx context.Context, accountID, messageID string, opt GetOptions) (*MessageView, error) {
	if err := tenancy.RequireReadAccount(ctx, "messages", accountID); err != nil {
		return nil, err
	}
	m, err := s.loadMessage(ctx, accountID, messageID)
	if err != nil {
		return nil, err
	}
	v := msgView(m)
	if atts, err := s.loadAttachments(ctx, accountID, messageID); err == nil {
		v.Attachments = atts
	}
	needBody := opt.Text != "" || opt.Headers || opt.Envelope || opt.MarkAsSeen || opt.EmbedAttachedImages
	if !needBody {
		return &v, nil
	}
	max := opt.MaxBytes
	if max <= 0 {
		max = defaultText
	}
	if max > hardTextCap {
		max = hardTextCap
	}
	acc, err := s.loadAccount(ctx, accountID)
	if err != nil {
		return nil, err
	}
	mbx, err := s.loadMailbox(ctx, accountID, m.MailboxID)
	if err != nil {
		return nil, err
	}
	var raw []byte
	if isAPIProvider(acc.Provider) {
		ops, err := s.opsFor(ctx, acc)
		if err != nil {
			return nil, err
		}
		if opt.MarkAsSeen {
			_ = ops.SetFlags(ctx, m.EmailID, m.UID, mbx.Path, m.UIDValidity, mailbox.FlagUpdate{Add: []string{`\Seen`}})
		}
		raw, err = ops.FetchRaw(ctx, m.EmailID, max+1)
		if err != nil {
			return nil, err
		}
		cats, labs, _ := ops.CategoriesAndLabels(ctx, m.EmailID)
		v.Categories, v.Labels = cats, labs
	} else {
		err = s.withIMAP(ctx, accountID, func(cl *imapc.Client, _ *ebsync.Account) error {
			if _, err := s.selectChecked(ctx, cl, mbx.Path, m.UIDValidity); err != nil {
				return err
			}
			if opt.MarkAsSeen {
				_ = cl.StoreFlags(ctx, []uint32{m.UID}, true, []string{`\Seen`})
			}
			var err error
			raw, err = cl.Raw(ctx, m.UID, max+1)
			return err
		})
		if err != nil {
			return nil, err
		}
		cats, labs, _ := (&mailbox.IMAPOps{Account: acc, Dial: s.imapDial}).FetchGMLabelsUID(ctx, mbx.Path, m.UID, m.UIDValidity)
		v.Categories, v.Labels = cats, labs
	}
	truncated := int64(len(raw)) > max
	if truncated {
		raw = raw[:max]
	}
	s.enrichFromRaw(&v, raw, opt, truncated, max)
	return &v, nil
}

func (s *Service) enrichFromRaw(v *MessageView, raw []byte, opt GetOptions, truncated bool, max int64) {
	hdr, err := mime.ParseHeaders(raw)
	if err == nil {
		if opt.Envelope || opt.Text != "" {
			if subj, err := mime.DecodeSubject(hdr); err == nil {
				v.Subject = subj
			}
			if from, err := mime.DecodeAddresses(hdr, "from"); err == nil {
				v.From = toAddrViews(from)
			}
			if to, err := mime.DecodeAddresses(hdr, "to"); err == nil {
				v.To = toAddrViews(to)
			}
			if cc, err := mime.DecodeAddresses(hdr, "cc"); err == nil {
				v.Cc = toAddrViews(cc)
			}
		}
		if opt.Headers {
			v.Headers = flattenHeaders(hdr)
		}
	}
	if opt.Text == "" && !opt.EmbedAttachedImages && !opt.PreProcessHTML {
		return
	}
	ent, err := mime.ParseEntity(raw)
	if err != nil {
		return
	}
	plain, html, trunc, _ := mime.TextParts(ent, int(max))
	v.TextTruncated = truncated || trunc
	switch opt.Text {
	case "html":
		v.HTML = html
	case "both":
		v.Text, v.HTML = plain, html
	case "plain":
		v.Text = plain
	default:
		if opt.EmbedAttachedImages || opt.PreProcessHTML {
			v.HTML = html
		} else if opt.Text != "" {
			v.Text = plain
		}
	}
	if v.HTML == "" {
		return
	}
	htmlOpts := content.HTMLOptions{}
	if opt.EmbedAttachedImages {
		htmlOpts.MapCID = embedCIDMapper(raw, embedPartMax, embedTotalMax)
	}
	v.WebSafeHTML = content.SanitizeHTML(v.HTML, htmlOpts).HTML
}

func embedCIDMapper(raw []byte, partMax, totalMax int) func(cid string) string {
	byCID := collectInlineCIDs(raw, partMax)
	used := 0
	return func(cid string) string {
		p, ok := byCID[strings.ToLower(strings.Trim(cid, "<>"))]
		if !ok || len(p.body) == 0 {
			return ""
		}
		if used+len(p.body) > totalMax {
			return ""
		}
		used += len(p.body)
		ct := p.ct
		if ct == "" {
			ct = "application/octet-stream"
		}
		return "data:" + ct + ";base64," + base64.StdEncoding.EncodeToString(p.body)
	}
}

func toAddrViews(in []mime.Address) []AddressView {
	out := make([]AddressView, len(in))
	for i, a := range in {
		out[i] = AddressView{Name: a.Name, Address: a.Address}
	}
	return out
}

func flattenHeaders(h mime.Header) map[string]string {
	out := make(map[string]string, len(h))
	for k, vals := range h {
		if len(vals) > 0 {
			out[k] = vals[0]
		}
	}
	return out
}

// PatchMessage changes flags on the server.
func (s *Service) PatchMessage(ctx context.Context, accountID, messageID string, in PatchMessageInput) (*MessageView, error) {
	if err := tenancy.RequireAccount(ctx, tenancy.PermMessagesWrite, accountID); err != nil {
		return nil, err
	}
	m, err := s.loadMessage(ctx, accountID, messageID)
	if err != nil {
		return nil, err
	}
	mbx, err := s.loadMailbox(ctx, accountID, m.MailboxID)
	if err != nil {
		return nil, err
	}
	acc, err := s.loadAccount(ctx, accountID)
	if err != nil {
		return nil, err
	}
	ops, err := s.opsFor(ctx, acc)
	if err != nil {
		return nil, err
	}
	err = ops.SetFlags(ctx, m.EmailID, m.UID, mbx.Path, m.UIDValidity, mailbox.FlagUpdate{
		Add: in.AddFlags, Remove: in.RemoveFlags, Set: in.SetFlags,
	})
	if err != nil {
		return nil, err
	}
	v := msgView(m)
	return &v, nil
}

// DeleteMessage flags \Deleted and expunges (or provider delete).
func (s *Service) DeleteMessage(ctx context.Context, accountID, messageID string) error {
	if err := tenancy.RequireAccount(ctx, tenancy.PermMessagesWrite, accountID); err != nil {
		return err
	}
	m, err := s.loadMessage(ctx, accountID, messageID)
	if err != nil {
		return err
	}
	mbx, err := s.loadMailbox(ctx, accountID, m.MailboxID)
	if err != nil {
		return err
	}
	acc, err := s.loadAccount(ctx, accountID)
	if err != nil {
		return err
	}
	ops, err := s.opsFor(ctx, acc)
	if err != nil {
		return err
	}
	return ops.Delete(ctx, m.EmailID, m.UID, mbx.Path, m.UIDValidity)
}

// MoveMessage moves one message to another mailbox.
func (s *Service) MoveMessage(ctx context.Context, accountID, messageID string, in MoveInput) error {
	if err := tenancy.RequireAccount(ctx, tenancy.PermMessagesWrite, accountID); err != nil {
		return err
	}
	m, err := s.loadMessage(ctx, accountID, messageID)
	if err != nil {
		return err
	}
	src, err := s.loadMailbox(ctx, accountID, m.MailboxID)
	if err != nil {
		return err
	}
	dest, err := s.resolveMailbox(ctx, accountID, in.MailboxID, in.Path)
	if err != nil {
		if in.Path == "" {
			return err
		}
		dest = &mbxDoc{Path: in.Path}
	}
	acc, err := s.loadAccount(ctx, accountID)
	if err != nil {
		return err
	}
	ops, err := s.opsFor(ctx, acc)
	if err != nil {
		return err
	}
	return ops.Move(ctx, m.EmailID, m.UID, src.Path, m.UIDValidity, dest.Path)
}

// Upload appends a raw message to a mailbox.
func (s *Service) Upload(ctx context.Context, accountID string, in UploadInput) (*UploadResult, error) {
	if err := tenancy.RequireAccount(ctx, tenancy.PermMessagesWrite, accountID); err != nil {
		return nil, err
	}
	raw, err := decodeRaw(in.Raw)
	if err != nil {
		return nil, err
	}
	path := in.Path
	var expectUV uint32
	switch {
	case in.MailboxID != "":
		mbx, err := s.loadMailbox(ctx, accountID, in.MailboxID)
		if err != nil {
			return nil, err
		}
		path = mbx.Path
		expectUV = mbx.UIDValidity
	case path != "":
		if mbx, err := s.loadMailboxByPath(ctx, accountID, path); err == nil {
			expectUV = mbx.UIDValidity
		}
	default:
		return nil, errs.New(errs.CodeValidationFailed, "mailboxId or path is required.")
	}
	if in.UIDValidity != nil {
		expectUV = *in.UIDValidity
	}
	date := s.now()
	if in.InternalDate != nil {
		date = in.InternalDate.UTC()
	}
	acc, err := s.loadAccount(ctx, accountID)
	if err != nil {
		return nil, err
	}
	if !isAPIProvider(acc.Provider) {
		// IMAP keeps the historical UV check + APPEND path (UIDPLUS).
		var uid, uv uint32
		err = s.withIMAP(ctx, accountID, func(cl *imapc.Client, _ *ebsync.Account) error {
			if expectUV != 0 {
				st, err := cl.Status(ctx, path)
				if err != nil {
					return err
				}
				if st.UIDValidity != expectUV {
					return errs.New(errs.CodeUidValidityMismatch,
						"The mailbox UIDVALIDITY changed; refresh and retry.").
						WithSafe("expected", expectUV).WithSafe("actual", st.UIDValidity)
				}
				uv = st.UIDValidity
			}
			u, err := cl.Append(ctx, path, in.Flags, date, raw)
			if err != nil {
				return err
			}
			uid = u
			if uv == 0 {
				if st, err := cl.Status(ctx, path); err == nil {
					uv = st.UIDValidity
				}
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		return &UploadResult{UID: uid, UIDValidity: uv, Path: path}, nil
	}
	ops, err := s.opsFor(ctx, acc)
	if err != nil {
		return nil, err
	}
	res, err := ops.Upload(ctx, mailbox.UploadInput{
		MailboxPath: path, Raw: raw, Flags: in.Flags, InternalDate: date,
	})
	if err != nil {
		return nil, err
	}
	return &UploadResult{UID: res.UID, UIDValidity: res.UIDValidity, Path: res.Path}, nil
}

// SearchMessages runs provider SEARCH and returns matching index rows.
func (s *Service) SearchMessages(ctx context.Context, accountID string, in SearchInput) ([]MessageView, error) {
	if err := tenancy.RequireReadAccount(ctx, "messages", accountID); err != nil {
		return nil, err
	}
	if err := validateSearch(in); err != nil {
		return nil, err
	}
	mbx, err := s.resolveMailbox(ctx, accountID, in.MailboxID, in.Path)
	if err != nil {
		return nil, err
	}
	acc, err := s.loadAccount(ctx, accountID)
	if err != nil {
		return nil, err
	}
	q := toSearchQuery(in)
	ops, err := s.opsFor(ctx, acc)
	if err != nil {
		return nil, err
	}
	providerIDs, uids, err := ops.Search(ctx, mbx.Path, q)
	if err != nil {
		return nil, err
	}
	filter := bson.M{"accountId": accountID, "mailboxId": mbx.ID}
	switch {
	case len(uids) > 0:
		filter["uid"] = bson.M{"$in": uids}
	case len(providerIDs) > 0:
		filter["emailId"] = bson.M{"$in": providerIDs}
	default:
		return []MessageView{}, nil
	}
	scoped, err := store.Scoped(ctx, filter)
	if err != nil {
		return nil, err
	}
	c, err := s.DB.Collection(index.Messages).Find(ctx, scoped)
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "messages.search")
	}
	defer func() { _ = c.Close(ctx) }()
	var rows []msgDoc
	if err := c.All(ctx, &rows); err != nil {
		return nil, err
	}
	out := make([]MessageView, len(rows))
	for i := range rows {
		out[i] = msgView(&rows[i])
	}
	return out, nil
}

func toSearchQuery(in SearchInput) mailbox.SearchQuery {
	q := mailbox.SearchQuery{
		Text: in.Text, Subject: in.Subject, From: in.From, To: in.To,
		Seen: in.Seen, Unseen: in.Unseen, Flagged: in.Flagged,
		Since: in.Since, Before: in.Before,
		Larger: in.Larger, Smaller: in.Smaller,
		UIDMin: in.UIDMin, UIDMax: in.UIDMax,
		Labels: in.Labels, GmailRaw: in.GmailRaw, UseOutlookSearch: in.UseOutlookSearch,
	}
	if in.Header != nil {
		q.HeaderName, q.HeaderValue = in.Header.Name, in.Header.Value
	}
	return q
}

func validateSearch(in SearchInput) error {
	const (
		maxHdrName  = 64
		maxHdrVal   = 256
		maxGmailRaw = 1024
		maxLabels   = 20
		maxLabelLen = 128
		maxSize     = 100 << 20
	)
	if in.Header != nil {
		if len(in.Header.Name) == 0 || len(in.Header.Name) > maxHdrName || len(in.Header.Value) > maxHdrVal {
			return errs.New(errs.CodeValidationFailed, "header name/value exceeds limits.")
		}
	}
	if in.GmailRaw != "" && len(in.GmailRaw) > maxGmailRaw {
		return errs.New(errs.CodeValidationFailed, "gmailRaw exceeds 1024 characters.")
	}
	if len(in.Labels) > maxLabels {
		return errs.New(errs.CodeValidationFailed, "labels exceeds 20 entries.")
	}
	for _, l := range in.Labels {
		if len(l) > maxLabelLen {
			return errs.New(errs.CodeValidationFailed, "a label exceeds 128 characters.")
		}
	}
	if in.Larger != nil && (*in.Larger < 0 || *in.Larger > maxSize) {
		return errs.New(errs.CodeValidationFailed, "larger is out of range.")
	}
	if in.Smaller != nil && (*in.Smaller < 0 || *in.Smaller > maxSize) {
		return errs.New(errs.CodeValidationFailed, "smaller is out of range.")
	}
	return nil
}

// GetAttachment fetches one body part by IMAP part path ("1.2").
func (s *Service) GetAttachment(ctx context.Context, accountID, messageID, attachmentID string, maxBytes int64) ([]byte, string, error) {
	if err := tenancy.RequireReadAccount(ctx, "messages", accountID); err != nil {
		return nil, "", err
	}
	part, err := parsePartPath(attachmentID)
	if err != nil {
		return nil, "", errs.New(errs.CodeAttachmentNotFound, "The attachment does not exist.").WithSafe("attachmentId", attachmentID)
	}
	m, err := s.loadMessage(ctx, accountID, messageID)
	if err != nil {
		return nil, "", err
	}
	mbx, err := s.loadMailbox(ctx, accountID, m.MailboxID)
	if err != nil {
		return nil, "", err
	}
	if maxBytes <= 0 {
		maxBytes = hardTextCap
	}
	var body []byte
	err = s.withIMAP(ctx, accountID, func(cl *imapc.Client, _ *ebsync.Account) error {
		if _, err := s.selectChecked(ctx, cl, mbx.Path, m.UIDValidity); err != nil {
			return err
		}
		var err error
		body, err = cl.Section(ctx, m.UID, part, maxBytes)
		return err
	})
	if err != nil {
		if errs.CodeOf(err) == errs.CodeMessageNotFound {
			return nil, "", errs.New(errs.CodeAttachmentNotFound, "The attachment does not exist.")
		}
		return nil, "", err
	}
	if len(body) == 0 {
		return nil, "", errs.New(errs.CodeAttachmentNotFound, "The attachment does not exist.")
	}
	return body, "application/octet-stream", nil
}

func parsePartPath(s string) ([]int, error) {
	if s == "" {
		return nil, errs.New(errs.CodeValidationFailed, "attachmentId is required.")
	}
	parts := strings.Split(s, ".")
	out := make([]int, len(parts))
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 {
			return nil, errs.New(errs.CodeValidationFailed, "attachmentId is invalid.")
		}
		out[i] = n
	}
	return out, nil
}
