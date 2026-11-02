// Package protolog records per-account IMAP/SMTP transcripts (S12 §7). It wraps a net.Conn, splits both
// directions into lines and masks credentials before anything is recorded — always, also in raw debug
// mode. Message content is replaced by a size marker unless raw debug is on.
package protolog

import (
	"bytes"
	"net"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Direction of a line.
const (
	Client = 'C'
	Server = 'S'
)

// Line is one recorded protocol line.
type Line struct {
	Dir     byte
	Proto   string // imap | smtp
	ConnID  string
	Text    string
	Bytes   int
	Elapsed time.Duration // since the connection opened
}

// Sink receives lines (the log system's protocol category, P1.16 API reads it back).
type Sink func(Line)

// Mode controls content capture.
type Mode int

const (
	// Normal masks credentials and replaces message content (literals, DATA) with a size marker.
	Normal Mode = iota
	// Raw keeps message content (raw debug mode, auto-expiring, audited) but still masks credentials.
	Raw
)

const maxLine = 2048

// Conn is a net.Conn that taps its traffic.
type Conn struct {
	net.Conn
	proto   string
	id      string
	sink    Sink
	mode    Mode
	start   time.Time
	mu      sync.Mutex
	rd, wr  lineSplitter
	masker  masker
	lastCmd string
}

// Wrap taps c. proto is "imap" or "smtp".
func Wrap(c net.Conn, proto, connID string, mode Mode, sink Sink) *Conn {
	t := &Conn{Conn: c, proto: proto, id: connID, sink: sink, mode: mode, start: time.Now()}
	t.masker = masker{proto: proto}
	t.rd.emit = func(b []byte) { t.record(Server, b) }
	t.wr.emit = func(b []byte) { t.record(Client, b) }
	return t
}

// Read implements net.Conn.
func (t *Conn) Read(p []byte) (int, error) {
	n, err := t.Conn.Read(p)
	if n > 0 {
		t.mu.Lock()
		t.rd.feed(p[:n], t.mode)
		t.mu.Unlock()
	}
	return n, err
}

// Write implements net.Conn.
func (t *Conn) Write(p []byte) (int, error) {
	n, err := t.Conn.Write(p)
	if n > 0 {
		t.mu.Lock()
		t.wr.feed(p[:n], t.mode)
		t.mu.Unlock()
	}
	return n, err
}

func (t *Conn) record(dir byte, raw []byte) {
	text := strings.TrimRight(string(raw), "\r\n")
	if t.proto == "smtp" {
		if dir == Client {
			t.lastCmd = strings.ToUpper(strings.TrimSpace(text))
		} else if strings.HasPrefix(text, "354") && t.lastCmd == "DATA" {
			t.wr.inData = true // the client now streams the message body until "."
		}
	}
	text = t.masker.mask(dir, text)
	n := len(raw)
	if len(text) > maxLine && t.mode == Normal {
		text = text[:maxLine] + "…"
	}
	if t.sink != nil {
		t.sink(Line{Dir: dir, Proto: t.proto, ConnID: t.id, Text: text, Bytes: n, Elapsed: time.Since(t.start)})
	}
}

// lineSplitter turns a byte stream into lines, handling IMAP literals ({N} / {N+}) and SMTP DATA bodies.
type lineSplitter struct {
	buf       bytes.Buffer
	literal   int  // bytes of literal still to swallow
	litTotal  int  // size of the current literal
	inData    bool // SMTP DATA body (client side) until "."
	dataBytes int
	emit      func([]byte)
}

var literalRe = regexp.MustCompile(`\{(\d+)\+?\}\r?\n?$`)

func (s *lineSplitter) feed(p []byte, mode Mode) {
	for len(p) > 0 {
		if s.literal > 0 {
			n := min(s.literal, len(p))
			if mode == Raw {
				s.buf.Write(p[:n])
			}
			s.literal -= n
			p = p[n:]
			if s.literal == 0 && mode == Normal {
				s.buf.WriteString("{literal " + strconv.Itoa(s.litTotal) + " bytes}")
			}
			continue
		}
		i := bytes.IndexByte(p, '\n')
		if i < 0 {
			s.buf.Write(p)
			if s.buf.Len() > 1<<20 { // runaway line: flush
				s.flush()
			}
			return
		}
		s.buf.Write(p[:i+1])
		p = p[i+1:]
		line := s.buf.Bytes()
		if s.inData {
			if bytes.Equal(bytes.TrimRight(line, "\r\n"), []byte(".")) {
				if mode == Normal {
					s.emit([]byte("{data " + strconv.Itoa(s.dataBytes) + " bytes}"))
				}
				s.inData, s.dataBytes = false, 0
				s.flush()
				continue
			}
			s.dataBytes += len(line)
			if mode == Raw {
				s.flush()
			} else {
				s.buf.Reset()
			}
			continue
		}
		if m := literalRe.FindSubmatch(line); m != nil {
			n, _ := strconv.Atoi(string(m[1]))
			if n > 0 {
				s.literal, s.litTotal = n, n
				continue // the literal (and what follows on the same line) belongs to this line
			}
		}
		s.flush()
	}
}

func (s *lineSplitter) flush() {
	if s.buf.Len() == 0 {
		return
	}
	b := append([]byte(nil), s.buf.Bytes()...)
	s.buf.Reset()
	s.emit(b)
}

// masker removes credentials (S12 §7: LOGIN, AUTHENTICATE/AUTH payloads and continuations, XOAUTH2).
type masker struct {
	proto       string
	authPending int // client continuation lines still to mask (SMTP AUTH LOGIN = 2, IMAP AUTHENTICATE = 1+)
	imapAuthTag string
}

var (
	imapLoginRe = regexp.MustCompile(`(?i)^(\S+ (?:UID )?LOGIN) .*$`)
	imapAuthRe  = regexp.MustCompile(`(?i)^(\S+) (AUTHENTICATE \S+)(?: .*)?$`)
	smtpAuthRe  = regexp.MustCompile(`(?i)^(AUTH (\S+))(?: .*)?$`)
)

const masked = "[masked]"

func (m *masker) mask(dir byte, line string) string {
	if dir == Server {
		if m.proto == "imap" && m.imapAuthTag != "" && strings.HasPrefix(line, m.imapAuthTag+" ") {
			m.imapAuthTag, m.authPending = "", 0
		}
		if m.proto == "smtp" && m.authPending > 0 && !strings.HasPrefix(line, "334") {
			m.authPending = 0
		}
		return line
	}
	if m.authPending > 0 {
		if m.proto == "smtp" {
			m.authPending--
		}
		return masked
	}
	switch m.proto {
	case "imap":
		if sm := imapLoginRe.FindStringSubmatch(line); sm != nil {
			return sm[1] + " " + masked
		}
		if sm := imapAuthRe.FindStringSubmatch(line); sm != nil {
			m.imapAuthTag, m.authPending = sm[1], 1<<30 // until the tagged reply
			return sm[1] + " " + sm[2] + " " + masked
		}
	case "smtp":
		if sm := smtpAuthRe.FindStringSubmatch(line); sm != nil {
			// Continuations (after 334 replies) are masked until the server answers with anything else.
			m.authPending = 2
			return sm[1] + " " + masked
		}
	}
	return line
}
