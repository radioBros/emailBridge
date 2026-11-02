package mem

import (
	"sync"

	"github.com/emersion/go-imap/v2/imapserver"
)

// Server holds users for the IMAP backend.
type Server struct {
	mutex sync.Mutex
	users map[string]*User
}

// New creates an empty server.
func New() *Server {
	return &Server{users: make(map[string]*User)}
}

// NewSession creates an unauthenticated IMAP session.
func (s *Server) NewSession() imapserver.Session {
	return &serverSession{server: s}
}

// User looks up a user by name.
func (s *Server) User(username string) *User {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return s.users[username]
}

// AddUser registers a user.
func (s *Server) AddUser(user *User) {
	s.mutex.Lock()
	s.users[user.username] = user
	s.mutex.Unlock()
}

// DeleteUser removes a user.
func (s *Server) DeleteUser(username string) bool {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if _, ok := s.users[username]; !ok {
		return false
	}
	delete(s.users, username)
	return true
}

// Users returns a snapshot of usernames.
func (s *Server) Users() []string {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	out := make([]string, 0, len(s.users))
	for u := range s.users {
		out = append(out, u)
	}
	return out
}

// Reset clears all users.
func (s *Server) Reset() {
	s.mutex.Lock()
	s.users = make(map[string]*User)
	s.mutex.Unlock()
}

type serverSession struct {
	*UserSession
	server *Server
}

var _ imapserver.Session = (*serverSession)(nil)

func (sess *serverSession) Login(username, password string) error {
	u := sess.server.User(username)
	if u == nil {
		return imapserver.ErrAuthFailed
	}
	if err := u.Login(username, password); err != nil {
		return err
	}
	sess.UserSession = NewUserSession(u)
	return nil
}
