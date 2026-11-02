package smtpc

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/emersion/go-smtp"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/protolog"
)

// Stage of an SMTP attempt (S6 §3). The send pipeline persists the stage before it starts; StageData
// is written with majority write concern before DATA is sent (Hooks.Stage).
type Stage string

// Stages in order.
const (
	StageConnect  Stage = "connect"
	StageEHLO     Stage = "ehlo"
	StageAuth     Stage = "auth"
	StageMailFrom Stage = "mailFrom"
	StageRcptTo   Stage = "rcptTo"
	StageData     Stage = "data"
	StageDataEnd  Stage = "dataEnd"
)

// DialFunc opens the TCP connection; production passes the egress dialer (mail purpose, S10 §8).
type DialFunc func(ctx context.Context, network, addr string) (net.Conn, error)

// SessionOptions configure Open.
type SessionOptions struct {
	Addr               string // host:port
	TLS                TLSMode
	InsecureSkipVerify bool        // accounts.ignoreCertErrors
	TLSConfig          *tls.Config // overrides the default TLS configuration (tests, custom roots)
	Username, Password string
	// AccessToken, when set, authenticates with XOAUTH2 (preferred) or OAUTHBEARER instead of password (P6.1).
	AccessToken    string
	EHLO           string // default "localhost"
	Dial           DialFunc
	ConnectTimeout time.Duration // default 30 s (dial + TLS + greeting)
	CommandTimeout time.Duration // default 60 s
	DataTimeout    time.Duration // end-of-data reply, default 10 min
	// Tap records the masked transcript (plaintext, also under STARTTLS) when not nil.
	Tap     protolog.Sink
	TapMode protolog.Mode
	TapConn string
}

func (o *SessionOptions) defaults() {
	if o.TLS == "" {
		o.TLS = TLSImplicit
	}
	if o.EHLO == "" {
		o.EHLO = "localhost"
	}
	if o.ConnectTimeout <= 0 {
		o.ConnectTimeout = connectTimeout
	}
	if o.CommandTimeout <= 0 {
		o.CommandTimeout = commandTimeout
	}
	if o.DataTimeout <= 0 {
		o.DataTimeout = submissionTimeout
	}
	if o.Dial == nil {
		d := &net.Dialer{Timeout: o.ConnectTimeout}
		o.Dial = d.DialContext
	}
}

// StageError carries the stage and the SMTP reply of a failure; it is wrapped in an *errs.E whose code is
// the coarse classification (Classify). internal/submit/policy refines it from these fields.
type StageError struct {
	Stage    Stage
	Code     int    // SMTP reply code, 0 for network errors
	Enhanced string // "4.7.1" or ""
	Message  string // server text
	Err      error
}

func (e *StageError) Error() string {
	if e.Code > 0 {
		return fmt.Sprintf("smtp %s: %d %s %s", e.Stage, e.Code, e.Enhanced, e.Message)
	}
	return fmt.Sprintf("smtp %s: %v", e.Stage, e.Err)
}

func (e *StageError) Unwrap() error { return e.Err }

func stageErr(st Stage, err error) error {
	if err == nil {
		return nil
	}
	var e *errs.E
	if errors.As(err, &e) {
		return err
	}
	se := &StageError{Stage: st, Err: err}
	var sm *smtp.SMTPError
	if errors.As(err, &sm) {
		se.Code, se.Message = sm.Code, sm.Message
		if sm.EnhancedCode != smtp.NoEnhancedCode && sm.EnhancedCode != (smtp.EnhancedCode{}) {
			se.Enhanced = fmt.Sprintf("%d.%d.%d", sm.EnhancedCode[0], sm.EnhancedCode[1], sm.EnhancedCode[2])
		}
	}
	return Classify(string(st), se)
}

// Session is one authenticated SMTP connection, reusable for several messages (RSET in between). Not safe
// for concurrent use.
type Session struct {
	o        SessionOptions
	c        *smtp.Client
	conn     net.Conn // the plaintext stream go-smtp uses (TLS conn, possibly tapped)
	raw      net.Conn
	mu       sync.Mutex
	dead     bool
	messages int
}

// Open connects, says EHLO once and authenticates (PLAIN, else LOGIN) when a username is set.
func Open(ctx context.Context, o SessionOptions) (*Session, error) {
	o.defaults()
	host, _, err := net.SplitHostPort(o.Addr)
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeValidationFailed, "smtpc.connect")
	}
	cctx, cancel := context.WithTimeout(ctx, o.ConnectTimeout)
	defer cancel()
	raw, err := o.Dial(cctx, "tcp", o.Addr)
	if err != nil {
		return nil, stageErr(StageConnect, err)
	}
	s := &Session{o: o, raw: raw}
	stop := context.AfterFunc(cctx, func() { _ = raw.Close() }) // bounds TLS + greeting + EHLO + AUTH
	defer stop()
	tlsConf := &tls.Config{ServerName: host, InsecureSkipVerify: o.InsecureSkipVerify, MinVersion: tls.VersionTLS12} //nolint:gosec // G402: only when the account explicitly accepts invalid certificates
	if o.TLSConfig != nil {
		tlsConf = o.TLSConfig.Clone()
		if tlsConf.ServerName == "" {
			tlsConf.ServerName = host
		}
	}
	tap := func(c net.Conn) net.Conn {
		if o.Tap == nil {
			return c
		}
		return protolog.Wrap(c, "smtp", o.TapConn, o.TapMode, o.Tap)
	}
	var conn net.Conn
	switch o.TLS {
	case TLSImplicit:
		tc := tls.Client(raw, tlsConf)
		if err := tc.HandshakeContext(cctx); err != nil {
			_ = raw.Close()
			return nil, stageErr(StageConnect, err)
		}
		conn = tap(tc)
	case TLSStartTLS:
		tc, greeting, err := startTLS(cctx, raw, tap(raw), o.EHLO, tlsConf)
		if err != nil {
			_ = raw.Close()
			return nil, stageErr(StageConnect, err)
		}
		conn = tap(tc)
		conn = &replayConn{Conn: conn, r: io.MultiReader(strings.NewReader(greeting), conn)}
	case TLSNone:
		conn = tap(raw)
	default:
		_ = raw.Close()
		return nil, errs.New(errs.CodeValidationFailed, fmt.Sprintf("Unknown SMTP TLS mode %q.", o.TLS))
	}
	s.conn = conn
	s.c = smtp.NewClient(conn)
	s.c.CommandTimeout = o.CommandTimeout
	s.c.SubmissionTimeout = o.DataTimeout
	if err := s.c.Hello(o.EHLO); err != nil {
		s.kill()
		return nil, stageErr(StageEHLO, err)
	}
	if o.Username != "" || o.AccessToken != "" {
		a, err := smtpAuthClient(s, o)
		if err != nil {
			s.kill()
			return nil, err
		}
		if err := s.c.Auth(a); err != nil {
			s.kill()
			return nil, stageErr(StageAuth, err)
		}
	}
	if cctx.Err() != nil {
		s.kill()
		return nil, stageErr(StageConnect, cctx.Err())
	}
	return s, nil
}

// startTLS performs greeting, EHLO and STARTTLS on the plaintext connection itself so the protocol tap
// stays on plaintext (go-smtp's own STARTTLS swaps the stream internally). It returns the TLS connection
// and a replay greeting for go-smtp, which then says EHLO again over TLS (RFC 3207 §4.2).
func startTLS(ctx context.Context, raw, pt net.Conn, ehlo string, conf *tls.Config) (net.Conn, string, error) {
	if d, ok := ctx.Deadline(); ok {
		_ = raw.SetDeadline(d)
	}
	br := bufio.NewReader(pt)
	code, lines, err := readReply(br)
	if err != nil {
		return nil, "", err
	}
	if code != 220 {
		return nil, "", &smtp.SMTPError{Code: code, Message: strings.Join(lines, " ")}
	}
	if _, err := io.WriteString(pt, "EHLO "+ehlo+"\r\n"); err != nil {
		return nil, "", err
	}
	code, lines, err = readReply(br)
	if err != nil {
		return nil, "", err
	}
	if code != 250 {
		return nil, "", &smtp.SMTPError{Code: code, Message: strings.Join(lines, " ")}
	}
	has := false
	for _, l := range lines {
		if strings.EqualFold(strings.Fields(l + " x")[0], "STARTTLS") {
			has = true
		}
	}
	if !has {
		return nil, "", errs.New(errs.CodeMailTLSFailed, "The server does not offer STARTTLS.")
	}
	if _, err := io.WriteString(pt, "STARTTLS\r\n"); err != nil {
		return nil, "", err
	}
	code, lines, err = readReply(br)
	if err != nil {
		return nil, "", err
	}
	if code != 220 {
		return nil, "", &smtp.SMTPError{Code: code, Message: strings.Join(lines, " ")}
	}
	if br.Buffered() > 0 {
		return nil, "", errors.New("smtpc: data after STARTTLS reply (possible injection)")
	}
	tc := tls.Client(raw, conf)
	if err := tc.HandshakeContext(ctx); err != nil {
		return nil, "", err
	}
	_ = raw.SetDeadline(time.Time{})
	return tc, "220 TLS established\r\n", nil
}

// readReply reads one (possibly multi-line) SMTP reply.
func readReply(br *bufio.Reader) (int, []string, error) {
	var lines []string
	for {
		l, err := br.ReadString('\n')
		if err != nil {
			return 0, nil, err
		}
		l = strings.TrimRight(l, "\r\n")
		if len(l) < 3 {
			return 0, nil, fmt.Errorf("smtpc: malformed reply %q", l)
		}
		code, err := strconv.Atoi(l[:3])
		if err != nil {
			return 0, nil, fmt.Errorf("smtpc: malformed reply %q", l)
		}
		rest := ""
		if len(l) > 4 {
			rest = l[4:]
		}
		lines = append(lines, rest)
		if len(lines) > 100 {
			return 0, nil, errors.New("smtpc: reply too long")
		}
		if len(l) == 3 || l[3] == ' ' {
			return code, lines, nil
		}
	}
}

type replayConn struct {
	net.Conn
	r io.Reader
}

func (c *replayConn) Read(b []byte) (int, error) { return c.r.Read(b) }

// MaxSize returns the server's SIZE limit (0 = none advertised).
func (s *Session) MaxSize() int64 {
	if n, ok := s.c.MaxMessageSize(); ok && n > 0 {
		return int64(n)
	}
	return 0
}

// Supports reports an EHLO extension (e.g. "SMTPUTF8", "DSN").
func (s *Session) Supports(ext string) bool {
	ok, _ := s.c.Extension(ext)
	return ok
}

// Messages is the number of messages sent on this session (pool reuse limit).
func (s *Session) Messages() int { return s.messages }

// Dead reports whether the session must be discarded (after any error).
func (s *Session) Dead() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dead
}

func (s *Session) kill() {
	s.mu.Lock()
	s.dead = true
	s.mu.Unlock()
	_ = s.raw.Close()
}

// Envelope of one message.
type Envelope struct {
	From string
	To   []string
	Size int64 // bytes of the message as sent (for SIZE)
	UTF8 bool  // addresses or headers need SMTPUTF8
	// DSN is applied when the server advertises the DSN extension (RFC 3461, P5).
	DSN *DSN
}

// DSN holds RFC 3461 parameters for one send.
type DSN struct {
	Notify []string // success|failure|delay|never (lower-case API form)
	Ret    string   // full|hdrs
	EnvID  string
}

// Hooks observe an attempt. Stage is called before each stage from StageMailFrom on; an error aborts the
// attempt before that stage (for StageData: before DATA is sent, so nothing can have been accepted).
type Hooks struct {
	Stage func(ctx context.Context, st Stage) error
}

// Rejection is a recipient refused permanently while others were accepted.
type Rejection struct {
	Address  string `json:"address"`
	Code     int    `json:"code"`
	Enhanced string `json:"enhanced,omitempty"`
	Message  string `json:"message"`
}

// Result of an accepted message.
type Result struct {
	Response string      // final reply text (often the queue ID)
	Accepted []string    // recipients accepted
	Rejected []Rejection // recipients refused permanently
}

// Send transmits one message. Permanent RCPT refusals of some recipients are reported in Result while the
// others receive the message; a temporary refusal of any recipient, or refusal of all, fails the attempt.
// After a failure before DATA the session stays usable (RSET); after any failure from DATA on, or any
// network error, it is dead.
func (s *Session) Send(ctx context.Context, env Envelope, body io.Reader, h Hooks) (*Result, error) {
	if s.Dead() {
		return nil, stageErr(StageMailFrom, net.ErrClosed)
	}
	if len(env.To) == 0 {
		return nil, errs.New(errs.CodeValidationFailed, "At least one recipient is required.")
	}
	stop := context.AfterFunc(ctx, s.kill) // cancellation closes the connection; the call fails fast
	defer stop()
	enter := func(st Stage) error {
		if h.Stage == nil {
			return nil
		}
		return h.Stage(ctx, st)
	}
	if err := enter(StageMailFrom); err != nil {
		return nil, err
	}
	if max := s.MaxSize(); max > 0 && env.Size > max {
		return nil, errs.New(errs.CodeMessageTooLarge, fmt.Sprintf("The message (%d bytes) exceeds the server limit of %d bytes.", env.Size, max)).
			WithSafe("size", env.Size).WithSafe("limit", max)
	}
	if env.UTF8 && !s.Supports("SMTPUTF8") {
		return nil, errs.New(errs.CodeSMTPPermanent, "The message needs SMTPUTF8, which the server does not support.")
	}
	mo := &smtp.MailOptions{UTF8: env.UTF8}
	if s.Supports("SIZE") && env.Size > 0 {
		mo.Size = env.Size
	}
	applyMailDSN(mo, env.DSN, s.Supports("DSN"))
	if err := s.c.Mail(env.From, mo); err != nil {
		return nil, s.fail(StageMailFrom, err, true)
	}
	if err := enter(StageRcptTo); err != nil {
		s.reset()
		return nil, err
	}
	res := &Result{}
	var lastRefusal error
	rcptOpts := rcptDSN(env.DSN, s.Supports("DSN"))
	for _, rcpt := range env.To {
		err := s.c.Rcpt(rcpt, rcptOpts)
		if err == nil {
			res.Accepted = append(res.Accepted, rcpt)
			continue
		}
		var sm *smtp.SMTPError
		if !errors.As(err, &sm) || sm.Temporary() || sm.Code == 503 {
			return nil, s.fail(StageRcptTo, err, errors.As(err, &sm))
		}
		lastRefusal = err
		rj := Rejection{Address: rcpt, Code: sm.Code, Message: sm.Message}
		if sm.EnhancedCode != smtp.NoEnhancedCode && sm.EnhancedCode != (smtp.EnhancedCode{}) {
			rj.Enhanced = fmt.Sprintf("%d.%d.%d", sm.EnhancedCode[0], sm.EnhancedCode[1], sm.EnhancedCode[2])
		}
		res.Rejected = append(res.Rejected, rj)
	}
	if len(res.Accepted) == 0 {
		return nil, s.fail(StageRcptTo, lastRefusal, true)
	}
	if err := enter(StageData); err != nil {
		s.reset()
		return nil, err
	}
	w, err := s.c.Data()
	if err != nil {
		var sm *smtp.SMTPError
		if errors.As(err, &sm) { // DATA refused with a reply: nothing sent, definite failure
			return nil, s.fail(StageData, err, true)
		}
		return nil, s.fail(StageData, err, false)
	}
	if err := s.copyBody(w, body); err != nil {
		return nil, s.fail(StageData, err, false)
	}
	if err := enter(StageDataEnd); err != nil {
		s.kill() // the body is sent but not terminated: the server discards it when the connection drops
		return nil, err
	}
	resp, err := w.CloseWithResponse()
	if err != nil {
		return nil, s.fail(StageDataEnd, err, false)
	}
	s.messages++
	if resp != nil {
		res.Response = resp.StatusText
	}
	return res, nil
}

// copyBody streams the message, extending the write deadline per chunk: go-smtp sets one deadline at DATA,
// which a large message on a slow link would exceed.
func (s *Session) copyBody(w io.Writer, body io.Reader) error {
	buf := make([]byte, 64*1024)
	for {
		n, rerr := body.Read(buf)
		if n > 0 {
			_ = s.conn.SetDeadline(time.Now().Add(s.o.CommandTimeout))
			if _, err := w.Write(buf[:n]); err != nil {
				return err
			}
		}
		if errors.Is(rerr, io.EOF) {
			return nil
		}
		if rerr != nil {
			return errs.Wrap(rerr, errs.CodeInternalError, "smtpc.body") // our read failed: not a server outcome
		}
	}
}

// fail classifies err at st. A definite SMTP reply before the body keeps the session (RSET); everything
// else kills it.
func (s *Session) fail(st Stage, err error, reply bool) error {
	if reply && st != StageDataEnd {
		s.reset()
	} else {
		s.kill()
	}
	return stageErr(st, err)
}

func (s *Session) reset() {
	if s.Dead() {
		return
	}
	if err := s.c.Reset(); err != nil {
		s.kill()
	}
}

// Reset sends RSET between messages (pool reuse); a failure kills the session.
func (s *Session) Reset(ctx context.Context) error {
	if s.Dead() {
		return stageErr(StageMailFrom, net.ErrClosed)
	}
	stop := context.AfterFunc(ctx, s.kill)
	defer stop()
	if err := s.c.Reset(); err != nil {
		s.kill()
		return stageErr(StageMailFrom, err)
	}
	return nil
}

// Noop checks that the session is alive (pool health check).
func (s *Session) Noop(ctx context.Context) error {
	if s.Dead() {
		return stageErr(StageMailFrom, net.ErrClosed)
	}
	stop := context.AfterFunc(ctx, s.kill)
	defer stop()
	if err := s.c.Noop(); err != nil {
		s.kill()
		return stageErr(StageMailFrom, err)
	}
	return nil
}

func applyMailDSN(mo *smtp.MailOptions, d *DSN, ok bool) {
	if !ok || d == nil {
		return
	}
	switch strings.ToLower(d.Ret) {
	case "full":
		mo.Return = smtp.DSNReturnFull
	case "hdrs", "headers":
		mo.Return = smtp.DSNReturnHeaders
	}
	if d.EnvID != "" {
		mo.EnvelopeID = d.EnvID
	}
}

func rcptDSN(d *DSN, ok bool) *smtp.RcptOptions {
	if !ok || d == nil || len(d.Notify) == 0 {
		return nil
	}
	out := make([]smtp.DSNNotify, 0, len(d.Notify))
	for _, n := range d.Notify {
		switch strings.ToLower(n) {
		case "success":
			out = append(out, smtp.DSNNotifySuccess)
		case "failure":
			out = append(out, smtp.DSNNotifyFailure)
		case "delay", "delayed":
			out = append(out, smtp.DSNNotifyDelayed)
		case "never":
			out = append(out, smtp.DSNNotifyNever)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return &smtp.RcptOptions{Notify: out}
}

// Close says QUIT (bounded by a short deadline) and closes the connection.
func (s *Session) Close() error {
	if s.Dead() {
		return nil
	}
	_ = s.conn.SetDeadline(time.Now().Add(5 * time.Second))
	_ = s.c.Quit()
	s.kill()
	return nil
}
