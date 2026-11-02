package simulator

import (
	"io"
	"strconv"
	"time"

	"github.com/emersion/go-sasl"
	"github.com/emersion/go-smtp"
)

// ReceivedSMTP is one accepted SMTP message.
type ReceivedSMTP struct {
	ID       string         `json:"id"`
	User     string         `json:"user,omitempty"`
	From     string         `json:"from"`
	To       []string       `json:"to"`
	Data     []byte         `json:"-"`
	DataSize int            `json:"dataSize"`
	Stages   []SMTPStageLog `json:"stages"`
	At       time.Time      `json:"at"`
	// DSN captures RFC 3461 parameters when the client requested them (P5.3).
	DSN *ReceivedDSN `json:"dsn,omitempty"`
}

// ReceivedDSN is the DSN options observed on MAIL FROM / RCPT TO.
type ReceivedDSN struct {
	Ret    string   `json:"ret,omitempty"`
	EnvID  string   `json:"envid,omitempty"`
	Notify []string `json:"notify,omitempty"`
}

// SMTPStageLog records what happened at a stage.
type SMTPStageLog struct {
	Stage string `json:"stage"`
	Code  int    `json:"code,omitempty"`
	Text  string `json:"text,omitempty"`
}

type smtpBackend struct {
	sim *Simulator
}

func (b *smtpBackend) NewSession(c *smtp.Conn) (smtp.Session, error) {
	return &smtpSession{sim: b.sim, conn: c}, nil
}

type smtpSession struct {
	sim    *Simulator
	conn   *smtp.Conn
	user   string
	from   string
	to     []string
	stages []SMTPStageLog
	authed bool
	dsn    *ReceivedDSN
}

func (s *smtpSession) AuthMechanisms() []string {
	mechs := []string{sasl.Plain, sasl.Login}
	if s.sim != nil {
		mechs = append(mechs, s.sim.smtpOAuthMechanisms()...)
	}
	return mechs
}

func (s *smtpSession) Auth(mech string) (sasl.Server, error) {
	switch mech {
	case sasl.Plain:
		return sasl.NewPlainServer(s.checkAuth), nil
	case sasl.Login:
		return &loginServer{auth: s.checkAuth}, nil
	case "XOAUTH2":
		return s.authXOAuth2()
	case sasl.OAuthBearer:
		return s.authOAuthBearer()
	default:
		return nil, smtp.ErrAuthUnknownMechanism
	}
}

// loginServer is a minimal SASL LOGIN server (obsolete but still offered by many MTAs).
type loginServer struct {
	step int
	user string
	auth func(identity, username, password string) error
}

func (a *loginServer) Next(response []byte) (challenge []byte, done bool, err error) {
	switch a.step {
	case 0: // optional initial response = username
		if response == nil {
			a.step = 1
			return []byte("Username:"), false, nil
		}
		a.user = string(response)
		a.step = 2
		return []byte("Password:"), false, nil
	case 1: // username after challenge
		a.user = string(response)
		a.step = 2
		return []byte("Password:"), false, nil
	case 2: // password
		err = a.auth("", a.user, string(response))
		return nil, true, err
	default:
		return nil, true, sasl.ErrUnexpectedClientResponse
	}
}

func (s *smtpSession) checkAuth(_, username, password string) error {
	if f, ok := s.stageFor(username, "auth"); ok {
		if err := smtpErr(f); err != nil {
			s.logStage("auth", f.Code, f.Text)
			return err
		}
	}
	u := s.sim.store.User(username)
	if u == nil || u.Login(username, password) != nil {
		s.logStage("auth", 535, "auth failed")
		return smtp.ErrAuthFailed
	}
	s.user = username
	s.authed = true
	s.logStage("auth", 235, "ok")
	return nil
}

func (s *smtpSession) stageFor(user, stage string) (SMTPStageFault, bool) {
	if user == "" {
		user = s.user
	}
	if user == "" {
		return SMTPStageFault{}, false
	}
	return s.sim.stateOf(user).smtpStage(stage)
}

func (s *smtpSession) logStage(stage string, code int, text string) {
	s.stages = append(s.stages, SMTPStageLog{Stage: stage, Code: code, Text: text})
}

func (s *smtpSession) Mail(from string, opts *smtp.MailOptions) error {
	if !s.authed {
		return smtp.ErrAuthRequired
	}
	if f, ok := s.stageFor(s.user, "mailFrom"); ok {
		if err := smtpErr(f); err != nil {
			s.logStage("mailFrom", f.Code, f.Text)
			return err
		}
	}
	s.from = from
	if opts != nil && (opts.Return != "" || opts.EnvelopeID != "") {
		if s.dsn == nil {
			s.dsn = &ReceivedDSN{}
		}
		s.dsn.Ret = string(opts.Return)
		s.dsn.EnvID = opts.EnvelopeID
	}
	s.logStage("mailFrom", 250, "ok")
	return nil
}

func (s *smtpSession) Rcpt(to string, opts *smtp.RcptOptions) error {
	if !s.authed {
		return smtp.ErrAuthRequired
	}
	if f, ok := s.stageFor(s.user, "rcptTo"); ok {
		if err := smtpErr(f); err != nil {
			s.logStage("rcptTo", f.Code, f.Text)
			return err
		}
	}
	s.to = append(s.to, to)
	if opts != nil && len(opts.Notify) > 0 {
		if s.dsn == nil {
			s.dsn = &ReceivedDSN{}
		}
		for _, n := range opts.Notify {
			s.dsn.Notify = append(s.dsn.Notify, string(n))
		}
	}
	s.logStage("rcptTo", 250, "ok")
	return nil
}

func (s *smtpSession) Data(r io.Reader) error {
	if !s.authed {
		return smtp.ErrAuthRequired
	}
	if f, ok := s.stageFor(s.user, "data"); ok {
		if err := smtpErr(f); err != nil {
			s.logStage("data", f.Code, f.Text)
			return err
		}
	}
	body, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	s.logStage("data", 354, "end data")

	end, hasEnd := s.stageFor(s.user, "dataEnd")
	if hasEnd {
		if end.SlowDataEnd > 0 {
			time.Sleep(end.SlowDataEnd)
		}
		if end.DropAfter {
			s.logStage("dataEnd", 0, "drop after body")
			s.sim.recordSMTP(s, body)
			dropConn(s.conn.Conn())
			return &smtp.SMTPError{Code: 421, Message: "connection closed"}
		}
		if end.NeverAnswer {
			s.logStage("dataEnd", 0, "never answer")
			s.sim.recordSMTP(s, body)
			select {}
		}
		if err := smtpErr(end); err != nil {
			s.logStage("dataEnd", end.Code, end.Text)
			s.sim.recordSMTP(s, body)
			return err
		}
	}
	s.logStage("dataEnd", 250, "queued")
	s.sim.recordSMTP(s, body)
	s.sim.deliverSMTP(s.user, s.from, s.to, body)
	return nil
}

func (s *smtpSession) Reset() {
	s.from = ""
	s.to = nil
}

func (s *smtpSession) Logout() error { return nil }

func (sim *Simulator) recordSMTP(s *smtpSession, body []byte) {
	sim.smtpMu.Lock()
	defer sim.smtpMu.Unlock()
	sim.smtpSeq++
	cp := append([]byte(nil), body...)
	rec := ReceivedSMTP{
		ID: strconv.FormatInt(sim.smtpSeq, 10), User: s.user, From: s.from,
		To: append([]string(nil), s.to...), Data: cp, DataSize: len(cp),
		Stages: append([]SMTPStageLog(nil), s.stages...), At: time.Now(),
	}
	if s.dsn != nil {
		d := *s.dsn
		d.Notify = append([]string(nil), s.dsn.Notify...)
		rec.DSN = &d
	}
	sim.smtpRecv = append(sim.smtpRecv, rec)
}

func (sim *Simulator) deliverSMTP(user, from string, to []string, body []byte) {
	u := sim.store.User(user)
	if u == nil {
		return
	}
	_, _, _ = u.InjectMessage("Sent", body, []string{`\Seen`}, time.Now())
	if sim.pec != nil && sim.pec.IsPECAddress(user) {
		sim.pec.OnPECSend(user, from, to, body)
		return
	}
	_, _, _ = u.InjectMessage("INBOX", body, nil, time.Now())
	if sim.pec != nil {
		sim.pec.OnOrdinaryToPEC(from, to, body)
	}
}

// ListSMTP returns received SMTP messages (newest last).
func (sim *Simulator) ListSMTP() []ReceivedSMTP {
	sim.smtpMu.Lock()
	defer sim.smtpMu.Unlock()
	out := make([]ReceivedSMTP, len(sim.smtpRecv))
	copy(out, sim.smtpRecv)
	return out
}

// ClearSMTP drops the received log.
func (sim *Simulator) ClearSMTP() {
	sim.smtpMu.Lock()
	sim.smtpRecv = nil
	sim.smtpMu.Unlock()
}
