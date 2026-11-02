package gmail

import (
	"context"
	"strings"
	"time"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/mailbox"
)

// Ops implements mailbox.Ops over the Gmail API.
type Ops struct {
	Client    *Client
	Tokens    TokenSource
	AccountID string
	User      string // mailbox email (SA subject); empty → /users/me
}

func (o *Ops) client() *Client {
	cl := o.Client
	if u := strings.TrimSpace(o.User); u != "" {
		cl = cl.ForUser(u)
	}
	return cl
}

func (o *Ops) token(ctx context.Context) (string, error) {
	if o == nil || o.Client == nil || o.Tokens == nil {
		return "", errs.New(errs.CodeInternalError, "Gmail mailbox ops are not configured.")
	}
	tok, user, err := o.Tokens.AccessToken(ctx, o.AccountID)
	if err != nil {
		return "", err
	}
	if o.User == "" && user != "" {
		o.User = user
	}
	return tok, nil
}

// FetchRaw implements mailbox.Ops.
func (o *Ops) FetchRaw(ctx context.Context, providerMsgID string, maxBytes int64) ([]byte, error) {
	tok, err := o.token(ctx)
	if err != nil {
		return nil, err
	}
	m, err := o.client().GetMessage(ctx, tok, providerMsgID, "raw")
	if err != nil {
		return nil, err
	}
	raw := m.Raw
	if maxBytes > 0 && int64(len(raw)) > maxBytes {
		raw = raw[:maxBytes]
	}
	return raw, nil
}

// Search implements mailbox.Ops.
func (o *Ops) Search(ctx context.Context, mailboxPath string, q mailbox.SearchQuery) ([]string, []uint32, error) {
	if q.UseOutlookSearch {
		return nil, nil, mailbox.Unsupported("useOutlookSearch is only available for Microsoft Graph accounts.")
	}
	if q.UIDMin != nil || q.UIDMax != nil {
		return nil, nil, mailbox.Unsupported("UID range search is not available for Gmail API accounts.")
	}
	tok, err := o.token(ctx)
	if err != nil {
		return nil, nil, err
	}
	labelID := LabelForPath(mailboxPath)
	raw := strings.TrimSpace(q.GmailRaw)
	if raw == "" {
		raw = buildGmailQuery(q)
	}
	list, err := o.client().SearchMessages(ctx, tok, labelID, raw)
	if err != nil {
		return nil, nil, err
	}
	ids := make([]string, len(list))
	for i, m := range list {
		ids[i] = m.ID
	}
	return ids, nil, nil
}

func buildGmailQuery(q mailbox.SearchQuery) string {
	var parts []string
	if q.Text != "" {
		parts = append(parts, q.Text)
	}
	if q.Subject != "" {
		parts = append(parts, "subject:("+q.Subject+")")
	}
	if q.From != "" {
		parts = append(parts, "from:("+q.From+")")
	}
	if q.To != "" {
		parts = append(parts, "to:("+q.To+")")
	}
	if q.HeaderName != "" {
		parts = append(parts, "rfc822msgid:("+q.HeaderValue+")")
		_ = q.HeaderName
	}
	if q.Seen != nil && *q.Seen {
		parts = append(parts, "is:read")
	}
	if q.Unseen != nil && *q.Unseen {
		parts = append(parts, "is:unread")
	}
	if q.Flagged != nil && *q.Flagged {
		parts = append(parts, "is:starred")
	}
	if q.Larger != nil {
		parts = append(parts, "larger:"+itoa(*q.Larger))
	}
	if q.Smaller != nil {
		parts = append(parts, "smaller:"+itoa(*q.Smaller))
	}
	for _, lab := range q.Labels {
		if lab != "" {
			parts = append(parts, "label:("+lab+")")
		}
	}
	return strings.Join(parts, " ")
}

func itoa(n int64) string {
	if n < 0 {
		n = 0
	}
	var b [20]byte
	i := len(b)
	for {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
		if n == 0 {
			break
		}
	}
	return string(b[i:])
}

// SetFlags implements mailbox.Ops.
func (o *Ops) SetFlags(ctx context.Context, providerMsgID string, _ uint32, _ string, _ uint32, u mailbox.FlagUpdate) error {
	tok, err := o.token(ctx)
	if err != nil {
		return err
	}
	add, remove := flagsToLabels(u)
	if len(add) == 0 && len(remove) == 0 {
		return nil
	}
	return o.client().Modify(ctx, tok, providerMsgID, add, remove)
}

func flagsToLabels(u mailbox.FlagUpdate) (add, remove []string) {
	mapOne := func(f string, toAdd bool) {
		var lab string
		switch f {
		case `\Seen`:
			if toAdd {
				remove = append(remove, "UNREAD")
			} else {
				add = append(add, "UNREAD")
			}
			return
		case `\Flagged`:
			lab = "STARRED"
		case `\Draft`:
			lab = "DRAFT"
		default:
			return
		}
		if toAdd {
			add = append(add, lab)
		} else {
			remove = append(remove, lab)
		}
	}
	for _, f := range u.Add {
		mapOne(f, true)
	}
	for _, f := range u.Remove {
		mapOne(f, false)
	}
	if u.Set != nil {
		add, remove = nil, nil
		hasSeen, hasFlagged := false, false
		for _, f := range u.Set {
			switch f {
			case `\Seen`:
				hasSeen = true
			case `\Flagged`:
				hasFlagged = true
			}
		}
		if hasSeen {
			remove = append(remove, "UNREAD")
		} else {
			add = append(add, "UNREAD")
		}
		if hasFlagged {
			add = append(add, "STARRED")
		} else {
			remove = append(remove, "STARRED")
		}
	}
	return add, remove
}

// Move implements mailbox.Ops.
func (o *Ops) Move(ctx context.Context, providerMsgID string, _ uint32, srcPath string, _ uint32, destPath string) error {
	tok, err := o.token(ctx)
	if err != nil {
		return err
	}
	add := LabelForPath(destPath)
	remove := LabelForPath(srcPath)
	var addL, remL []string
	if add != "" {
		addL = []string{add}
	}
	if remove != "" {
		remL = []string{remove}
	}
	return o.client().Modify(ctx, tok, providerMsgID, addL, remL)
}

// Delete implements mailbox.Ops.
func (o *Ops) Delete(ctx context.Context, providerMsgID string, _ uint32, _ string, _ uint32) error {
	tok, err := o.token(ctx)
	if err != nil {
		return err
	}
	if err := o.client().Trash(ctx, tok, providerMsgID); err != nil {
		// Simulator may lack /trash — fall back to TRASH label.
		return o.client().Modify(ctx, tok, providerMsgID, []string{"TRASH"}, []string{"INBOX"})
	}
	return nil
}

// Upload implements mailbox.Ops.
func (o *Ops) Upload(ctx context.Context, in mailbox.UploadInput) (*mailbox.UploadResult, error) {
	tok, err := o.token(ctx)
	if err != nil {
		return nil, err
	}
	label := LabelForPath(in.MailboxPath)
	var labels []string
	if label != "" {
		labels = []string{label}
	}
	date := in.InternalDate
	if date.IsZero() {
		date = time.Now().UTC()
	}
	id, _, err := o.client().Insert(ctx, tok, in.Raw, labels, date)
	if err != nil {
		return nil, err
	}
	return &mailbox.UploadResult{ProviderID: id, Path: in.MailboxPath}, nil
}

// CreateMailbox implements mailbox.Ops.
func (o *Ops) CreateMailbox(ctx context.Context, path string, _ bool) error {
	tok, err := o.token(ctx)
	if err != nil {
		return err
	}
	name := strings.TrimPrefix(path, "Label/")
	if name == "" || LabelForPath(path) != "" {
		return mailbox.Unsupported("System Gmail labels cannot be created.")
	}
	_, err = o.client().CreateLabel(ctx, tok, name)
	return err
}

// RenameMailbox implements mailbox.Ops.
func (o *Ops) RenameMailbox(context.Context, string, string) error {
	return mailbox.Unsupported("Renaming Gmail labels via the Mailbox API is not supported yet.")
}

// DeleteMailbox implements mailbox.Ops.
func (o *Ops) DeleteMailbox(ctx context.Context, path string) error {
	if LabelForPath(path) != "" {
		return mailbox.Unsupported("System Gmail labels cannot be deleted.")
	}
	return mailbox.Unsupported("Deleting Gmail labels via the Mailbox API requires the label id.")
}

// Subscribe implements mailbox.Ops.
func (o *Ops) Subscribe(context.Context, string, bool) error {
	return mailbox.Unsupported("Subscribe is not available for Gmail API accounts.")
}

// CategoriesAndLabels implements mailbox.Ops.
func (o *Ops) CategoriesAndLabels(ctx context.Context, providerMsgID string) (categories, labels []string, err error) {
	tok, err := o.token(ctx)
	if err != nil {
		return nil, nil, err
	}
	m, err := o.client().GetMessage(ctx, tok, providerMsgID, "metadata")
	if err != nil {
		return nil, nil, err
	}
	for _, id := range m.LabelIDs {
		if strings.HasPrefix(id, "CATEGORY_") {
			categories = append(categories, id)
			continue
		}
		if isUserLabelID(id) {
			labels = append(labels, id)
		}
	}
	return categories, labels, nil
}
