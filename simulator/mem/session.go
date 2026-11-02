package mem

import (
	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
)

// UserSession is an authenticated IMAP session bound to a user.
type UserSession struct {
	*User
	*MailboxView
}

var _ imapserver.SessionIMAP4rev2 = (*UserSession)(nil)

// NewUserSession creates a session for user.
func NewUserSession(user *User) *UserSession {
	return &UserSession{User: user}
}

// Close releases the selected mailbox view.
func (sess *UserSession) Close() error {
	if sess != nil && sess.MailboxView != nil {
		sess.MailboxView.Close()
	}
	return nil
}

// Select opens a mailbox.
func (sess *UserSession) Select(name string, options *imap.SelectOptions) (*imap.SelectData, error) {
	mbox, err := sess.User.Mailbox(name)
	if err != nil {
		return nil, err
	}
	if sess.MailboxView != nil {
		sess.MailboxView.Close()
	}
	mbox.mutex.Lock()
	defer mbox.mutex.Unlock()
	sess.MailboxView = mbox.NewView()
	_ = options // ReadOnly is reflected in the tagged OK by imapserver
	return mbox.selectDataLocked(), nil
}

// Unselect closes the selected mailbox.
func (sess *UserSession) Unselect() error {
	if sess.MailboxView != nil {
		sess.MailboxView.Close()
		sess.MailboxView = nil
	}
	return nil
}

// Copy copies messages to dest.
func (sess *UserSession) Copy(numSet imap.NumSet, destName string) (*imap.CopyData, error) {
	dest, err := sess.User.Mailbox(destName)
	if err != nil {
		return nil, &imap.Error{
			Type: imap.StatusResponseTypeNo,
			Code: imap.ResponseCodeTryCreate,
			Text: "No such mailbox",
		}
	}
	if sess.MailboxView != nil && dest == sess.MailboxView.Mailbox {
		return nil, &imap.Error{Type: imap.StatusResponseTypeNo, Text: "Source and destination mailboxes are identical"}
	}
	var sourceUIDs, destUIDs imap.UIDSet
	sess.forEach(numSet, func(_ uint32, msg *message) {
		ad := dest.copyMsg(msg)
		sourceUIDs.AddNum(msg.uid)
		destUIDs.AddNum(ad.UID)
	})
	return &imap.CopyData{UIDValidity: dest.UIDValidity(), SourceUIDs: sourceUIDs, DestUIDs: destUIDs}, nil
}

// Move moves messages to dest.
func (sess *UserSession) Move(w *imapserver.MoveWriter, numSet imap.NumSet, destName string) error {
	dest, err := sess.User.Mailbox(destName)
	if err != nil {
		return &imap.Error{
			Type: imap.StatusResponseTypeNo,
			Code: imap.ResponseCodeTryCreate,
			Text: "No such mailbox",
		}
	}
	if sess.MailboxView != nil && dest == sess.MailboxView.Mailbox {
		return &imap.Error{Type: imap.StatusResponseTypeNo, Text: "Source and destination mailboxes are identical"}
	}
	sess.mutex.Lock()
	defer sess.mutex.Unlock()
	var sourceUIDs, destUIDs imap.UIDSet
	expunged := make(map[*message]struct{})
	sess.forEachLocked(numSet, func(_ uint32, msg *message) {
		ad := dest.copyMsg(msg)
		sourceUIDs.AddNum(msg.uid)
		destUIDs.AddNum(ad.UID)
		expunged[msg] = struct{}{}
	})
	seqNums := sess.expungeLocked(expunged)
	if err := w.WriteCopyData(&imap.CopyData{
		UIDValidity: dest.UIDValidity(),
		SourceUIDs:  sourceUIDs,
		DestUIDs:    destUIDs,
	}); err != nil {
		return err
	}
	for _, seqNum := range seqNums {
		if err := w.WriteExpunge(sess.tracker.EncodeSeqNum(seqNum)); err != nil {
			return err
		}
	}
	return nil
}

// Poll drains mailbox updates.
func (sess *UserSession) Poll(w *imapserver.UpdateWriter, allowExpunge bool) error {
	if sess.MailboxView == nil {
		return nil
	}
	return sess.MailboxView.Poll(w, allowExpunge)
}

// Idle waits for updates.
func (sess *UserSession) Idle(w *imapserver.UpdateWriter, stop <-chan struct{}) error {
	if sess.MailboxView == nil {
		<-stop
		return nil
	}
	return sess.MailboxView.Idle(w, stop)
}

// Fetch delegates to the view.
func (sess *UserSession) Fetch(w *imapserver.FetchWriter, numSet imap.NumSet, options *imap.FetchOptions) error {
	return sess.MailboxView.Fetch(w, numSet, options)
}

// Search delegates to the view.
func (sess *UserSession) Search(kind imapserver.NumKind, criteria *imap.SearchCriteria, options *imap.SearchOptions) (*imap.SearchData, error) {
	return sess.MailboxView.Search(kind, criteria, options)
}

// Store delegates to the view.
func (sess *UserSession) Store(w *imapserver.FetchWriter, numSet imap.NumSet, flags *imap.StoreFlags, options *imap.StoreOptions) error {
	return sess.MailboxView.Store(w, numSet, flags, options)
}

// Expunge delegates to the view.
func (sess *UserSession) Expunge(w *imapserver.ExpungeWriter, uids *imap.UIDSet) error {
	return sess.MailboxView.Expunge(w, uids)
}
