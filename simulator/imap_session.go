package simulator

import (
	"net"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
)

type byeWriter interface {
	Bye(text string) error
}

// faultSession wraps a mem session with per-user faults and capability gates.
type faultSession struct {
	inner  imapserver.Session
	sim    *Simulator
	user   string
	state  *userState
	conn   net.Conn
	bye    byeWriter
	authed bool
}

var (
	_ imapserver.SessionIMAP4rev2 = (*faultSession)(nil)
)

func (s *faultSession) gate() error {
	if s.state == nil {
		return nil
	}
	err, drop := s.state.beforeCmd()
	if drop {
		if s.bye != nil {
			_ = s.bye.Bye("connection dropped")
		}
		if s.conn != nil {
			_ = s.conn.Close()
		}
		if err != nil {
			return err
		}
		return &imap.Error{Type: imap.StatusResponseTypeBye, Text: "connection dropped"}
	}
	return err
}

func (s *faultSession) Close() error {
	if s.authed && s.state != nil {
		s.state.sessionClosed()
		s.authed = false
	}
	if s.inner != nil {
		return s.inner.Close()
	}
	return nil
}

func (s *faultSession) Login(username, password string) error {
	st := s.sim.stateOf(username)
	if err := st.loginFault(); err != nil {
		return err
	}
	if err := s.inner.Login(username, password); err != nil {
		return err
	}
	s.user = username
	s.state = st
	s.authed = true
	st.sessionOpened()
	return nil
}

func (s *faultSession) Select(mailbox string, options *imap.SelectOptions) (*imap.SelectData, error) {
	if err := s.gate(); err != nil {
		return nil, err
	}
	return s.inner.Select(mailbox, options)
}

func (s *faultSession) Create(mailbox string, options *imap.CreateOptions) error {
	if err := s.gate(); err != nil {
		return err
	}
	return s.inner.Create(mailbox, options)
}

func (s *faultSession) Delete(mailbox string) error {
	if err := s.gate(); err != nil {
		return err
	}
	return s.inner.Delete(mailbox)
}

func (s *faultSession) Rename(mailbox, newName string, options *imap.RenameOptions) error {
	if err := s.gate(); err != nil {
		return err
	}
	return s.inner.Rename(mailbox, newName, options)
}

func (s *faultSession) Subscribe(mailbox string) error {
	if err := s.gate(); err != nil {
		return err
	}
	return s.inner.Subscribe(mailbox)
}

func (s *faultSession) Unsubscribe(mailbox string) error {
	if err := s.gate(); err != nil {
		return err
	}
	return s.inner.Unsubscribe(mailbox)
}

func (s *faultSession) List(w *imapserver.ListWriter, ref string, patterns []string, options *imap.ListOptions) error {
	if err := s.gate(); err != nil {
		return err
	}
	if s.state != nil && !s.state.getCaps().SpecialUse && options != nil {
		cp := *options
		cp.ReturnSpecialUse = false
		cp.SelectSpecialUse = false
		options = &cp
	}
	return s.inner.List(w, ref, patterns, options)
}

func (s *faultSession) Status(mailbox string, options *imap.StatusOptions) (*imap.StatusData, error) {
	if err := s.gate(); err != nil {
		return nil, err
	}
	return s.inner.Status(mailbox, options)
}

func (s *faultSession) Append(mailbox string, r imap.LiteralReader, options *imap.AppendOptions) (*imap.AppendData, error) {
	if err := s.gate(); err != nil {
		return nil, err
	}
	return s.inner.Append(mailbox, r, options)
}

func (s *faultSession) Poll(w *imapserver.UpdateWriter, allowExpunge bool) error {
	return s.inner.Poll(w, allowExpunge)
}

func (s *faultSession) Idle(w *imapserver.UpdateWriter, stop <-chan struct{}) error {
	if s.state != nil && s.state.neverDone() {
		// Half-open: ignore DONE forever (client DoneTimeout → mail_timeout).
		select {}
	}
	if s.state != nil && !s.state.getCaps().IDLE {
		return &imap.Error{Type: imap.StatusResponseTypeBad, Text: "IDLE disabled"}
	}
	return s.inner.Idle(w, stop)
}

func (s *faultSession) Unselect() error {
	if err := s.gate(); err != nil {
		return err
	}
	return s.inner.Unselect()
}

func (s *faultSession) Expunge(w *imapserver.ExpungeWriter, uids *imap.UIDSet) error {
	if err := s.gate(); err != nil {
		return err
	}
	return s.inner.Expunge(w, uids)
}

func (s *faultSession) Search(kind imapserver.NumKind, criteria *imap.SearchCriteria, options *imap.SearchOptions) (*imap.SearchData, error) {
	if err := s.gate(); err != nil {
		return nil, err
	}
	return s.inner.Search(kind, criteria, options)
}

func (s *faultSession) Fetch(w *imapserver.FetchWriter, numSet imap.NumSet, options *imap.FetchOptions) error {
	if err := s.gate(); err != nil {
		return err
	}
	if s.state != nil {
		if d := s.state.slowFetch(); d > 0 {
			time.Sleep(d)
		}
	}
	return s.inner.Fetch(w, numSet, options)
}

func (s *faultSession) Store(w *imapserver.FetchWriter, numSet imap.NumSet, flags *imap.StoreFlags, options *imap.StoreOptions) error {
	if err := s.gate(); err != nil {
		return err
	}
	return s.inner.Store(w, numSet, flags, options)
}

func (s *faultSession) Copy(numSet imap.NumSet, dest string) (*imap.CopyData, error) {
	if err := s.gate(); err != nil {
		return nil, err
	}
	return s.inner.Copy(numSet, dest)
}

func (s *faultSession) Move(w *imapserver.MoveWriter, numSet imap.NumSet, dest string) error {
	if err := s.gate(); err != nil {
		return err
	}
	if s.state != nil && !s.state.getCaps().MOVE {
		return &imap.Error{Type: imap.StatusResponseTypeBad, Text: "MOVE disabled"}
	}
	mover, ok := s.inner.(imapserver.SessionMove)
	if !ok {
		return &imap.Error{Type: imap.StatusResponseTypeBad, Text: "MOVE not supported"}
	}
	return mover.Move(w, numSet, dest)
}

func (s *faultSession) Namespace() (*imap.NamespaceData, error) {
	if ns, ok := s.inner.(imapserver.SessionNamespace); ok {
		return ns.Namespace()
	}
	return &imap.NamespaceData{}, nil
}
