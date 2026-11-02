package graph

import (
	"context"
	"strings"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/mailbox"
)

// Ops implements mailbox.Ops over Microsoft Graph.
type Ops struct {
	Client    *Client
	Tokens    TokenSource
	AccountID string
	UPN       string
}

func (o *Ops) client() *Client {
	cl := o.Client
	if u := strings.TrimSpace(o.UPN); u != "" {
		cl = cl.ForUser(u)
	}
	return cl
}

func (o *Ops) token(ctx context.Context) (string, error) {
	if o == nil || o.Client == nil || o.Tokens == nil {
		return "", errs.New(errs.CodeInternalError, "Graph mailbox ops are not configured.")
	}
	tok, user, err := o.Tokens.AccessToken(ctx, o.AccountID)
	if err != nil {
		return "", err
	}
	if o.UPN == "" && user != "" {
		o.UPN = user
	}
	return tok, nil
}

func (o *Ops) folderID(ctx context.Context, tok, path string) (string, error) {
	folders, err := o.client().ListFolders(ctx, tok)
	if err != nil {
		return "", err
	}
	want := strings.ToLower(path)
	for _, f := range folders {
		p, _ := MailboxPath(f.WellKnownName, f.DisplayName)
		if strings.EqualFold(p, path) || strings.EqualFold(f.DisplayName, path) ||
			strings.EqualFold(f.WellKnownName, path) || strings.EqualFold(f.ID, path) {
			return f.ID, nil
		}
		if strings.ToLower(p) == want {
			return f.ID, nil
		}
	}
	return "", errs.New(errs.CodeMailboxNotFound, "The mailbox does not exist.").WithSafe("path", path)
}

// FetchRaw implements mailbox.Ops.
func (o *Ops) FetchRaw(ctx context.Context, providerMsgID string, maxBytes int64) ([]byte, error) {
	tok, err := o.token(ctx)
	if err != nil {
		return nil, err
	}
	raw, err := o.client().GetMIME(ctx, tok, providerMsgID)
	if err != nil {
		return nil, err
	}
	if maxBytes > 0 && int64(len(raw)) > maxBytes {
		raw = raw[:maxBytes]
	}
	return raw, nil
}

// Search implements mailbox.Ops.
func (o *Ops) Search(ctx context.Context, mailboxPath string, q mailbox.SearchQuery) ([]string, []uint32, error) {
	if q.GmailRaw != "" || len(q.Labels) > 0 {
		return nil, nil, mailbox.Unsupported("Gmail search criteria are not available for Graph accounts.")
	}
	if q.UIDMin != nil || q.UIDMax != nil {
		return nil, nil, mailbox.Unsupported("UID range search is not available for Graph accounts.")
	}
	tok, err := o.token(ctx)
	if err != nil {
		return nil, nil, err
	}
	folderID := ""
	if mailboxPath != "" {
		folderID, err = o.folderID(ctx, tok, mailboxPath)
		if err != nil {
			return nil, nil, err
		}
	}
	text := q.Text
	if text == "" {
		text = q.Subject
	}
	if text == "" {
		text = q.From
	}
	list, err := o.client().SearchMessages(ctx, tok, folderID, text, q.UseOutlookSearch)
	if err != nil {
		return nil, nil, err
	}
	ids := make([]string, 0, len(list))
	for _, m := range list {
		ids = append(ids, m.ID)
	}
	return ids, nil, nil
}

// SetFlags implements mailbox.Ops.
func (o *Ops) SetFlags(ctx context.Context, providerMsgID string, _ uint32, _ string, _ uint32, u mailbox.FlagUpdate) error {
	tok, err := o.token(ctx)
	if err != nil {
		return err
	}
	body := map[string]any{}
	apply := func(f string, on bool) {
		switch f {
		case `\Seen`:
			body["isRead"] = on
		case `\Flagged`:
			if on {
				body["flag"] = map[string]any{"flagStatus": "flagged"}
			} else {
				body["flag"] = map[string]any{"flagStatus": "notFlagged"}
			}
		}
	}
	for _, f := range u.Add {
		apply(f, true)
	}
	for _, f := range u.Remove {
		apply(f, false)
	}
	if u.Set != nil {
		body = map[string]any{"isRead": false, "flag": map[string]any{"flagStatus": "notFlagged"}}
		for _, f := range u.Set {
			apply(f, true)
		}
	}
	if len(body) == 0 {
		return nil
	}
	return o.client().Patch(ctx, tok, providerMsgID, body)
}

// Move implements mailbox.Ops.
func (o *Ops) Move(ctx context.Context, providerMsgID string, _ uint32, _ string, _ uint32, destPath string) error {
	tok, err := o.token(ctx)
	if err != nil {
		return err
	}
	destID, err := o.folderID(ctx, tok, destPath)
	if err != nil {
		return err
	}
	return o.client().Move(ctx, tok, providerMsgID, destID)
}

// Delete implements mailbox.Ops.
func (o *Ops) Delete(ctx context.Context, providerMsgID string, _ uint32, _ string, _ uint32) error {
	tok, err := o.token(ctx)
	if err != nil {
		return err
	}
	return o.client().Delete(ctx, tok, providerMsgID)
}

// Upload implements mailbox.Ops.
func (o *Ops) Upload(ctx context.Context, in mailbox.UploadInput) (*mailbox.UploadResult, error) {
	tok, err := o.token(ctx)
	if err != nil {
		return nil, err
	}
	folderID, err := o.folderID(ctx, tok, in.MailboxPath)
	if err != nil {
		return nil, err
	}
	id, err := o.client().CreateMIME(ctx, tok, folderID, in.Raw)
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
	_, err = o.client().CreateFolder(ctx, tok, path)
	return err
}

// RenameMailbox implements mailbox.Ops.
func (o *Ops) RenameMailbox(ctx context.Context, from, to string) error {
	tok, err := o.token(ctx)
	if err != nil {
		return err
	}
	id, err := o.folderID(ctx, tok, from)
	if err != nil {
		return err
	}
	return o.client().RenameFolder(ctx, tok, id, to)
}

// DeleteMailbox implements mailbox.Ops.
func (o *Ops) DeleteMailbox(ctx context.Context, path string) error {
	tok, err := o.token(ctx)
	if err != nil {
		return err
	}
	id, err := o.folderID(ctx, tok, path)
	if err != nil {
		return err
	}
	return o.client().DeleteFolder(ctx, tok, id)
}

// Subscribe implements mailbox.Ops.
func (o *Ops) Subscribe(context.Context, string, bool) error {
	return mailbox.Unsupported("Subscribe is not available for Microsoft Graph accounts.")
}

// CategoriesAndLabels implements mailbox.Ops (Graph has no Gmail categories).
func (o *Ops) CategoriesAndLabels(context.Context, string) ([]string, []string, error) {
	return nil, nil, nil
}
