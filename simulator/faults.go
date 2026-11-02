package simulator

import (
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-smtp"
)

// Caps toggles IMAP capabilities per user.
type Caps struct {
	IDLE       bool `json:"idle"`
	CONDSTORE  bool `json:"condstore"`
	QRESYNC    bool `json:"qresync"`
	UIDPLUS    bool `json:"uidplus"`
	SpecialUse bool `json:"specialUse"`
	MOVE       bool `json:"move"`
}

// DefaultCaps enables the capabilities the simulator exercises. Note: go-imap
// v2 beta.8's server does not advertise CONDSTORE/QRESYNC/ID in CAPABILITY
// even when listed in Options.Caps; IDLE is always advertised for IMAP4rev1.
// Toggles still drive session behaviour (MOVE off → Move errors; NeverDone, etc.).
func DefaultCaps() Caps {
	return Caps{IDLE: true, CONDSTORE: true, QRESYNC: false, UIDPLUS: true, SpecialUse: true, MOVE: true}
}

// IMAPFault is armed until cleared or Remaining commands are consumed (0 = until cleared).
type IMAPFault struct {
	Latency        time.Duration `json:"latency,omitempty"`
	LoginLimit     bool          `json:"loginLimit,omitempty"`
	AuthFail       bool          `json:"authFail,omitempty"`
	Bye            bool          `json:"bye,omitempty"`
	DropAfterCmds  int           `json:"dropAfterCmds,omitempty"`
	DropAfterBytes int           `json:"dropAfterBytes,omitempty"`
	NeverDone      bool          `json:"neverDone,omitempty"`
	SlowFetch      time.Duration `json:"slowFetch,omitempty"`
	Remaining      int           `json:"remaining,omitempty"`
	cmdsSeen       int
}

// SMTPStageFault scripts one SMTP stage reply.
type SMTPStageFault struct {
	Code        int           `json:"code"`
	Enhanced    string        `json:"enhanced,omitempty"`
	Text        string        `json:"text,omitempty"`
	DropAfter   bool          `json:"dropAfter,omitempty"`
	NeverAnswer bool          `json:"neverAnswer,omitempty"`
	SlowDataEnd time.Duration `json:"slowDataEnd,omitempty"`
}

// SMTPScript maps stage → fault (connect, ehlo, auth, mailFrom, rcptTo, data, dataEnd).
type SMTPScript map[string]SMTPStageFault

type userState struct {
	mu   sync.Mutex
	caps Caps
	imap *IMAPFault
	smtp SMTPScript
	sess SessionStats
}

// SessionStats counts a user's authenticated IMAP sessions: how many logins succeeded, how many are open now,
// and the most that were open at once (hand-off and reconnect-storm tests, S14 §5).
type SessionStats struct {
	Logins   int64 `json:"logins"`
	Open     int64 `json:"open"`
	PeakOpen int64 `json:"peakOpen"`
}

func (s *userState) sessionOpened() {
	s.mu.Lock()
	s.sess.Logins++
	s.sess.Open++
	s.sess.PeakOpen = max(s.sess.PeakOpen, s.sess.Open)
	s.mu.Unlock()
}

func (s *userState) sessionClosed() {
	s.mu.Lock()
	if s.sess.Open > 0 {
		s.sess.Open--
	}
	s.mu.Unlock()
}

func (s *userState) sessionStats() SessionStats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sess
}

func newUserState() *userState {
	return &userState{caps: DefaultCaps(), smtp: SMTPScript{}}
}

func (s *userState) getCaps() Caps {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.caps
}

func (s *userState) setCaps(c Caps) {
	s.mu.Lock()
	s.caps = c
	s.mu.Unlock()
}

func (s *userState) setIMAPFault(f *IMAPFault) {
	s.mu.Lock()
	if f != nil {
		f.cmdsSeen = 0
	}
	s.imap = f
	s.mu.Unlock()
}

func (s *userState) clearIMAPFault() {
	s.mu.Lock()
	s.imap = nil
	s.mu.Unlock()
}

func (s *userState) setSMTP(script SMTPScript) {
	s.mu.Lock()
	if script == nil {
		script = SMTPScript{}
	}
	s.smtp = script
	s.mu.Unlock()
}

func (s *userState) clearSMTP() {
	s.mu.Lock()
	s.smtp = SMTPScript{}
	s.mu.Unlock()
}

func (s *userState) smtpStage(stage string) (SMTPStageFault, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, ok := s.smtp[stage]
	return f, ok
}

func (s *userState) tickRemaining() {
	if s.imap == nil || s.imap.Remaining <= 0 {
		return
	}
	s.imap.Remaining--
	if s.imap.Remaining == 0 {
		s.imap = nil
	}
}

// beforeCmd applies latency and drop/bye. drop=true means close the conn.
func (s *userState) beforeCmd() (err error, drop bool) {
	s.mu.Lock()
	f := s.imap
	if f == nil {
		s.mu.Unlock()
		return nil, false
	}
	lat := f.Latency
	s.mu.Unlock()
	if lat > 0 {
		time.Sleep(lat)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	f = s.imap
	if f == nil {
		return nil, false
	}
	f.cmdsSeen++
	if f.Bye {
		f.Bye = false
		s.tickRemaining()
		return &imap.Error{Type: imap.StatusResponseTypeBye, Text: "forced BYE"}, true
	}
	if f.DropAfterCmds > 0 && f.cmdsSeen >= f.DropAfterCmds {
		s.tickRemaining()
		return nil, true
	}
	s.tickRemaining()
	return nil, false
}

func (s *userState) loginFault() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	f := s.imap
	if f == nil {
		return nil
	}
	if f.AuthFail {
		s.tickRemaining()
		return &imap.Error{
			Type: imap.StatusResponseTypeNo,
			Code: imap.ResponseCodeAuthenticationFailed,
			Text: "Authentication failed",
		}
	}
	if f.LoginLimit {
		s.tickRemaining()
		return &imap.Error{
			Type: imap.StatusResponseTypeNo,
			Code: imap.ResponseCodeLimit,
			Text: "Too many connections",
		}
	}
	return nil
}

func (s *userState) neverDone() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.imap != nil && s.imap.NeverDone
}

func (s *userState) slowFetch() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.imap == nil {
		return 0
	}
	return s.imap.SlowFetch
}

func parseEnhanced(s string) smtp.EnhancedCode {
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return smtp.NoEnhancedCode
	}
	a, err1 := strconv.Atoi(parts[0])
	b, err2 := strconv.Atoi(parts[1])
	c, err3 := strconv.Atoi(parts[2])
	if err1 != nil || err2 != nil || err3 != nil {
		return smtp.NoEnhancedCode
	}
	return smtp.EnhancedCode{a, b, c}
}

func smtpErr(f SMTPStageFault) error {
	if f.Code == 0 {
		return nil
	}
	text := f.Text
	if text == "" {
		text = "scripted fault"
	}
	return &smtp.SMTPError{Code: f.Code, EnhancedCode: parseEnhanced(f.Enhanced), Message: text}
}

func dropConn(c net.Conn) {
	if c != nil {
		_ = c.Close()
	}
}
