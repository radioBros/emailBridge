package mem

import (
	"crypto/subtle"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
)

const mailboxDelim rune = '/'

// User holds mailboxes for one account.
type User struct {
	username, password string

	mutex           sync.Mutex
	mailboxes       map[string]*Mailbox
	prevUidValidity uint32
}

// NewUser creates a user with empty mailboxes.
func NewUser(username, password string) *User {
	return &User{username: username, password: password, mailboxes: make(map[string]*Mailbox)}
}

// Username returns the login name.
func (u *User) Username() string { return u.username }

// Password returns the password (control API / SMTP auth).
func (u *User) Password() string { return u.password }

// Login checks credentials.
func (u *User) Login(username, password string) error {
	if username != u.username {
		return imapserver.ErrAuthFailed
	}
	if subtle.ConstantTimeCompare([]byte(password), []byte(u.password)) != 1 {
		return imapserver.ErrAuthFailed
	}
	return nil
}

func (u *User) mailboxLocked(name string) (*Mailbox, error) {
	mbox := u.mailboxes[name]
	if mbox == nil {
		return nil, &imap.Error{
			Type: imap.StatusResponseTypeNo,
			Code: imap.ResponseCodeNonExistent,
			Text: "No such mailbox",
		}
	}
	return mbox, nil
}

// Mailbox returns a mailbox by name.
func (u *User) Mailbox(name string) (*Mailbox, error) {
	u.mutex.Lock()
	defer u.mutex.Unlock()
	return u.mailboxLocked(name)
}

// Status returns STATUS of a mailbox.
func (u *User) Status(name string, options *imap.StatusOptions) (*imap.StatusData, error) {
	mbox, err := u.Mailbox(name)
	if err != nil {
		return nil, err
	}
	return mbox.StatusData(options), nil
}

// List writes LIST responses.
func (u *User) List(w *imapserver.ListWriter, ref string, patterns []string, options *imap.ListOptions) error {
	if len(patterns) == 0 {
		return w.WriteList(&imap.ListData{Attrs: []imap.MailboxAttr{imap.MailboxAttrNoSelect}, Delim: mailboxDelim})
	}
	// Snapshot under the lock, write after releasing it: a client that reads slowly must never block other
	// sessions or the control API (recreate, inject…) on this user.
	u.mutex.Lock()
	mboxes := make(map[string]*Mailbox, len(u.mailboxes))
	for name, mbox := range u.mailboxes {
		mboxes[name] = mbox
	}
	u.mutex.Unlock()
	var l []imap.ListData
	for name, mbox := range mboxes {
		match := false
		for _, pattern := range patterns {
			if imapserver.MatchList(name, mailboxDelim, ref, pattern) {
				match = true
				break
			}
		}
		if !match {
			continue
		}
		if data := mbox.list(options); data != nil {
			l = append(l, *data)
		}
	}
	sort.Slice(l, func(i, j int) bool { return l[i].Mailbox < l[j].Mailbox })
	for _, data := range l {
		if err := w.WriteList(&data); err != nil {
			return err
		}
	}
	return nil
}

// Append stores a message.
func (u *User) Append(mailbox string, r imap.LiteralReader, options *imap.AppendOptions) (*imap.AppendData, error) {
	mbox, err := u.Mailbox(mailbox)
	if err != nil {
		return nil, &imap.Error{
			Type: imap.StatusResponseTypeNo,
			Code: imap.ResponseCodeTryCreate,
			Text: "No such mailbox",
		}
	}
	return mbox.appendLiteral(r, options)
}

// Create creates a mailbox; applies SpecialUse from options when set.
func (u *User) Create(name string, options *imap.CreateOptions) error {
	u.mutex.Lock()
	defer u.mutex.Unlock()
	name = strings.TrimRight(name, string(mailboxDelim))
	if u.mailboxes[name] != nil {
		return &imap.Error{
			Type: imap.StatusResponseTypeNo,
			Code: imap.ResponseCodeAlreadyExists,
			Text: "Mailbox already exists",
		}
	}
	u.prevUidValidity++
	mbox := NewMailbox(name, u.prevUidValidity)
	if options != nil && len(options.SpecialUse) > 0 {
		mbox.specialUse = append([]imap.MailboxAttr(nil), options.SpecialUse...)
	}
	mbox.subscribed = true
	u.mailboxes[name] = mbox
	return nil
}

// Delete removes a mailbox.
func (u *User) Delete(name string) error {
	u.mutex.Lock()
	defer u.mutex.Unlock()
	if _, err := u.mailboxLocked(name); err != nil {
		return err
	}
	delete(u.mailboxes, name)
	return nil
}

// Rename renames a mailbox.
func (u *User) Rename(oldName, newName string, _ *imap.RenameOptions) error {
	u.mutex.Lock()
	defer u.mutex.Unlock()
	newName = strings.TrimRight(newName, string(mailboxDelim))
	mbox, err := u.mailboxLocked(oldName)
	if err != nil {
		return err
	}
	if u.mailboxes[newName] != nil {
		return &imap.Error{
			Type: imap.StatusResponseTypeNo,
			Code: imap.ResponseCodeAlreadyExists,
			Text: "Mailbox already exists",
		}
	}
	mbox.rename(newName)
	u.mailboxes[newName] = mbox
	delete(u.mailboxes, oldName)
	return nil
}

// Subscribe marks a mailbox subscribed.
func (u *User) Subscribe(name string) error {
	mbox, err := u.Mailbox(name)
	if err != nil {
		return err
	}
	mbox.SetSubscribed(true)
	return nil
}

// Unsubscribe marks a mailbox unsubscribed.
func (u *User) Unsubscribe(name string) error {
	mbox, err := u.Mailbox(name)
	if err != nil {
		return err
	}
	mbox.SetSubscribed(false)
	return nil
}

// Namespace returns the personal namespace.
func (u *User) Namespace() (*imap.NamespaceData, error) {
	return &imap.NamespaceData{Personal: []imap.NamespaceDescriptor{{Delim: mailboxDelim}}}, nil
}

// EnsureDefaults creates INBOX and common special-use folders when missing.
func (u *User) EnsureDefaults() {
	_ = u.Create("INBOX", nil)
	defaults := []struct {
		name string
		attr imap.MailboxAttr
	}{
		{"Sent", imap.MailboxAttrSent},
		{"Drafts", imap.MailboxAttrDrafts},
		{"Trash", imap.MailboxAttrTrash},
		{"Junk", imap.MailboxAttrJunk},
		{"Archive", imap.MailboxAttrArchive},
	}
	for _, d := range defaults {
		_ = u.Create(d.name, &imap.CreateOptions{SpecialUse: []imap.MailboxAttr{d.attr}})
	}
}

// InjectMessage appends raw RFC 822 into a mailbox.
func (u *User) InjectMessage(mailbox string, raw []byte, flags []string, t time.Time) (uid uint32, uidValidity uint32, err error) {
	mbox, err := u.Mailbox(mailbox)
	if err != nil {
		return 0, 0, err
	}
	fs := make([]imap.Flag, len(flags))
	for i, f := range flags {
		fs[i] = imap.Flag(f)
	}
	ad := mbox.Inject(raw, fs, t)
	return uint32(ad.UID), ad.UIDValidity, nil
}

// RecreateFolder deletes and recreates a folder with a new UIDVALIDITY (keeps special-use).
func (u *User) RecreateFolder(name string) (uidValidity uint32, err error) {
	u.mutex.Lock()
	defer u.mutex.Unlock()
	old, err := u.mailboxLocked(name)
	if err != nil {
		return 0, err
	}
	su := append([]imap.MailboxAttr(nil), old.specialUse...)
	delete(u.mailboxes, name)
	u.prevUidValidity++
	mbox := NewMailbox(name, u.prevUidValidity)
	mbox.specialUse = su
	mbox.subscribed = true
	u.mailboxes[name] = mbox
	return mbox.uidValidity, nil
}

// MailboxNames lists mailbox names.
func (u *User) MailboxNames() []string {
	u.mutex.Lock()
	defer u.mutex.Unlock()
	names := make([]string, 0, len(u.mailboxes))
	for n := range u.mailboxes {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
