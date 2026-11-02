package mailbox

import (
	"context"
	"strings"
	"time"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/mailproto/imapc"
	ebsync "github.com/radiobros/emailbridge/internal/sync"
)

// IMAPDial acquires an on-demand IMAP client for an account.
type IMAPDial func(ctx context.Context, acc *ebsync.Account) (cl *imapc.Client, release func(discard bool), err error)

// IMAPOps implements Ops over IMAP (including Gmail-over-IMAP X-GM extensions when present).
type IMAPOps struct {
	Account *ebsync.Account
	Dial    IMAPDial
}

func (o *IMAPOps) with(ctx context.Context, fn func(cl *imapc.Client) error) error {
	cl, release, err := o.Dial(ctx, o.Account)
	if err != nil {
		return err
	}
	err = fn(cl)
	release(err != nil)
	return err
}

func (o *IMAPOps) selectUV(ctx context.Context, cl *imapc.Client, path string, expectUV uint32) error {
	info, err := cl.Select(ctx, path, false)
	if err != nil {
		return err
	}
	if expectUV != 0 && info.UIDValidity != expectUV {
		return errs.New(errs.CodeUidValidityMismatch,
			"The mailbox UIDVALIDITY changed; refresh and retry.").
			WithSafe("expected", expectUV).WithSafe("actual", info.UIDValidity)
	}
	return nil
}

// FetchRaw implements Ops.
func (o *IMAPOps) FetchRaw(ctx context.Context, _ string, maxBytes int64) ([]byte, error) {
	return nil, Unsupported("FetchRaw on IMAP requires a selected UID; use the messages IMAP path.")
}

// FetchRawUID fetches raw by mailbox path + UID.
func (o *IMAPOps) FetchRawUID(ctx context.Context, path string, uid, uidValidity uint32, maxBytes int64) ([]byte, error) {
	var raw []byte
	err := o.with(ctx, func(cl *imapc.Client) error {
		if err := o.selectUV(ctx, cl, path, uidValidity); err != nil {
			return err
		}
		var err error
		raw, err = cl.Raw(ctx, uid, maxBytes)
		return err
	})
	return raw, err
}

// Search implements Ops.
func (o *IMAPOps) Search(ctx context.Context, mailboxPath string, q SearchQuery) ([]string, []uint32, error) {
	if q.UseOutlookSearch {
		return nil, nil, Unsupported("useOutlookSearch is only available for Microsoft Graph accounts.")
	}
	var uids []uint32
	err := o.with(ctx, func(cl *imapc.Client) error {
		if _, err := cl.Select(ctx, mailboxPath, false); err != nil {
			return err
		}
		if q.GmailRaw != "" || len(q.Labels) > 0 {
			if !cl.Has("X-GM-EXT-1") {
				return Unsupported("Gmail search extensions require X-GM-EXT-1 on the IMAP server.")
			}
		}
		crit := imapc.SearchCriteria{
			Text: q.Text, Subject: q.Subject, From: q.From, To: q.To,
			Seen: q.Seen, Unseen: q.Unseen, Flagged: q.Flagged,
			Since: q.Since, Before: q.Before,
			HeaderName: q.HeaderName, HeaderValue: q.HeaderValue,
			Larger: q.Larger, Smaller: q.Smaller,
			UIDMin: q.UIDMin, UIDMax: q.UIDMax,
			GmailRaw: q.GmailRaw, Labels: q.Labels,
		}
		var err error
		uids, err = cl.Search(ctx, crit)
		return err
	})
	return nil, uids, err
}

// SetFlags implements Ops.
func (o *IMAPOps) SetFlags(ctx context.Context, _ string, uid uint32, mailboxPath string, uidValidity uint32, u FlagUpdate) error {
	return o.with(ctx, func(cl *imapc.Client) error {
		if err := o.selectUV(ctx, cl, mailboxPath, uidValidity); err != nil {
			return err
		}
		uids := []uint32{uid}
		if u.Set != nil {
			return cl.StoreFlagsSet(ctx, uids, u.Set)
		}
		if len(u.Add) > 0 {
			if err := cl.StoreFlags(ctx, uids, true, u.Add); err != nil {
				return err
			}
		}
		if len(u.Remove) > 0 {
			return cl.StoreFlags(ctx, uids, false, u.Remove)
		}
		return nil
	})
}

// Move implements Ops.
func (o *IMAPOps) Move(ctx context.Context, _ string, uid uint32, srcPath string, uidValidity uint32, destPath string) error {
	return o.with(ctx, func(cl *imapc.Client) error {
		if err := o.selectUV(ctx, cl, srcPath, uidValidity); err != nil {
			return err
		}
		return cl.Move(ctx, []uint32{uid}, destPath)
	})
}

// Delete implements Ops.
func (o *IMAPOps) Delete(ctx context.Context, _ string, uid uint32, mailboxPath string, uidValidity uint32) error {
	return o.with(ctx, func(cl *imapc.Client) error {
		if err := o.selectUV(ctx, cl, mailboxPath, uidValidity); err != nil {
			return err
		}
		if err := cl.StoreFlags(ctx, []uint32{uid}, true, []string{`\Deleted`}); err != nil {
			return err
		}
		return cl.Expunge(ctx, []uint32{uid})
	})
}

// Upload implements Ops.
func (o *IMAPOps) Upload(ctx context.Context, in UploadInput) (*UploadResult, error) {
	date := in.InternalDate
	if date.IsZero() {
		date = time.Now().UTC()
	}
	var uid, uv uint32
	err := o.with(ctx, func(cl *imapc.Client) error {
		st, err := cl.Status(ctx, in.MailboxPath)
		if err != nil {
			return err
		}
		uv = st.UIDValidity
		u, err := cl.Append(ctx, in.MailboxPath, in.Flags, date, in.Raw)
		if err != nil {
			return err
		}
		uid = u
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &UploadResult{UID: uid, UIDValidity: uv, Path: in.MailboxPath}, nil
}

// CreateMailbox implements Ops.
func (o *IMAPOps) CreateMailbox(ctx context.Context, path string, subscribe bool) error {
	return o.with(ctx, func(cl *imapc.Client) error {
		if err := cl.Create(ctx, path); err != nil {
			return err
		}
		if subscribe {
			return cl.Subscribe(ctx, path)
		}
		return nil
	})
}

// RenameMailbox implements Ops.
func (o *IMAPOps) RenameMailbox(ctx context.Context, from, to string) error {
	return o.with(ctx, func(cl *imapc.Client) error {
		return cl.Rename(ctx, from, to)
	})
}

// DeleteMailbox implements Ops.
func (o *IMAPOps) DeleteMailbox(ctx context.Context, path string) error {
	return o.with(ctx, func(cl *imapc.Client) error {
		return cl.Delete(ctx, path)
	})
}

// Subscribe implements Ops.
func (o *IMAPOps) Subscribe(ctx context.Context, path string, on bool) error {
	return o.with(ctx, func(cl *imapc.Client) error {
		if on {
			return cl.Subscribe(ctx, path)
		}
		return cl.Unsubscribe(ctx, path)
	})
}

// CategoriesAndLabels is a no-op on plain IMAP (use FetchGMLabelsUID for Gmail-over-IMAP).
func (o *IMAPOps) CategoriesAndLabels(context.Context, string) (categories, labels []string, err error) {
	return nil, nil, nil
}

// FetchGMLabelsUID returns categories/labels when the server advertises X-GM-EXT-1.
// Full X-GM-LABELS FETCH needs a Gmail-capable IMAP stack; until then we only confirm the capability.
func (o *IMAPOps) FetchGMLabelsUID(ctx context.Context, path string, uid, uidValidity uint32) (categories, labels []string, err error) {
	err = o.with(ctx, func(cl *imapc.Client) error {
		if !cl.Has("X-GM-EXT-1") {
			return nil
		}
		return o.selectUV(ctx, cl, path, uidValidity)
	})
	return nil, nil, err
}

// SplitGMLabels partitions Gmail IMAP labels into CATEGORY_* and the rest (user labels).
func SplitGMLabels(all []string) (categories, labels []string) {
	for _, l := range all {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		if strings.HasPrefix(l, "CATEGORY_") {
			categories = append(categories, l)
			continue
		}
		switch l {
		case "\\Inbox", "\\Sent", "\\Draft", "\\Trash", "\\Junk", "\\Flagged", "\\Important",
			"INBOX", "SENT", "DRAFT", "TRASH", "SPAM", "STARRED", "UNREAD", "IMPORTANT", "CHAT":
			continue
		}
		labels = append(labels, l)
	}
	return categories, labels
}
