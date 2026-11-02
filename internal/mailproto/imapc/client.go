package imapc

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-sasl"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/protolog"
)

// Client is one IMAP connection. Methods must not be called concurrently (one goroutine per account
// connection, S5 §6), except Close and Updates.
type Client struct {
	c        *imapclient.Client
	o        Options
	caps     map[string]bool
	updates  chan Update
	mu       sync.Mutex // guards seq map and selected state
	seq      []uint32   // seq-1 → UID for the selected mailbox (0 = unknown)
	selected string
	qresync  bool         // ENABLE QRESYNC succeeded: expunges arrive as VANISHED
	collect  *QResyncData // non-nil while SelectQResync gathers VANISHED (EARLIER) and FETCH responses
	closed   chan struct{}
	once     sync.Once
}

func (o *Options) defaults() {
	if o.CommandTimeout <= 0 {
		o.CommandTimeout = 120 * time.Second
	}
	if o.DoneTimeout <= 0 {
		o.DoneTimeout = 30 * time.Second
	}
	if o.Updates <= 0 {
		o.Updates = 1024
	}
	if o.Dial == nil {
		d := &net.Dialer{Timeout: 30 * time.Second, KeepAliveConfig: net.KeepAliveConfig{Enable: true, Idle: 30 * time.Second, Interval: 10 * time.Second, Count: 3}}
		o.Dial = d.DialContext
	}
	if o.TLS == "" {
		o.TLS = TLSImplicit
	}
}

// Dial connects, authenticates (AUTHENTICATE PLAIN when offered, else LOGIN) and sends ID when
// advertised (S5 §3.1).
func Dial(ctx context.Context, o Options) (*Client, error) {
	o.defaults()
	host, _, err := net.SplitHostPort(o.Addr)
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeValidationFailed, "dial")
	}
	raw, err := o.Dial(ctx, "tcp", o.Addr)
	if err != nil {
		return nil, classify("dial", err)
	}
	tap := func(c net.Conn) net.Conn { // the tap always sees plaintext: it wraps the TLS stream, never raw TCP
		if o.Tap == nil {
			return c
		}
		return protolog.Wrap(c, "imap", o.TapConn, o.TapMode, o.Tap)
	}
	cl := &Client{o: o, updates: make(chan Update, o.Updates), closed: make(chan struct{})}
	tlsConf := &tls.Config{ServerName: host, InsecureSkipVerify: o.InsecureSkipVerify, MinVersion: tls.VersionTLS12} //nolint:gosec // G402: InsecureSkipVerify only when the account explicitly accepts invalid certificates (S9 accounts.ignoreCertErrors)
	opts := &imapclient.Options{TLSConfig: tlsConf, UnilateralDataHandler: cl.handlers()}
	var conn net.Conn
	switch o.TLS {
	case TLSImplicit:
		tc := tls.Client(raw, tlsConf)
		if err := handshake(ctx, tc); err != nil {
			_ = raw.Close()
			return nil, classify("tls", err)
		}
		conn = tap(tc)
	case TLSStartTLS:
		conn, err = startTLS(ctx, raw, tap(raw), tlsConf)
		if err != nil {
			_ = raw.Close()
			return nil, classify("starttls", err)
		}
		conn = tap(conn)
		conn = &replayConn{Conn: conn, r: io.MultiReader(strings.NewReader("* OK TLS established\r\n"), conn)}
	default:
		conn = tap(raw)
	}
	c := imapclient.New(conn, opts)
	cl.c = c
	go func() {
		<-c.Closed()
		cl.once.Do(func() { close(cl.closed) })
	}()
	if err := cl.do(ctx, "greeting", func() error { return c.WaitGreeting() }); err != nil {
		_ = c.Close()
		return nil, err
	}
	cl.refreshCaps()
	login, op, err := authFunc(cl, o)
	if err != nil {
		_ = c.Close()
		return nil, err
	}
	if err := cl.do(ctx, op, login); err != nil {
		_ = c.Close()
		return nil, err
	}
	cl.refreshCaps()
	if o.DisableRev2 {
		delete(cl.caps, "IMAP4REV2")
	}
	if cl.caps["ID"] && len(o.ID) > 0 {
		id := &imap.IDData{Name: o.ID["name"], Version: o.ID["version"], Vendor: o.ID["vendor"], SupportURL: o.ID["support-url"]}
		_ = cl.do(ctx, "id", func() error { _, err := c.ID(id).Wait(); return err })
	}
	if o.QResync && cl.caps["QRESYNC"] {
		var data *imapclient.EnableData
		if err := cl.do(ctx, "enable", func() error { var err error; data, err = c.Enable(imap.CapQResync).Wait(); return err }); err == nil && data != nil {
			if _, ok := data.Caps[imap.CapQResync]; ok {
				cl.qresync = true
			}
		}
	}
	return cl, nil
}

// QResync reports whether QRESYNC is enabled on this connection.
func (cl *Client) QResync() bool { return cl.qresync }

func authFunc(cl *Client, o Options) (func() error, string, error) {
	if o.AccessToken != "" {
		switch {
		case cl.caps["AUTH="+XOAuth2]:
			return func() error {
				return cl.c.Authenticate(NewXOAuth2Client(o.Username, o.AccessToken))
			}, "authenticate", nil
		case cl.caps["AUTH="+sasl.OAuthBearer]:
			return func() error {
				return cl.c.Authenticate(sasl.NewOAuthBearerClient(&sasl.OAuthBearerOptions{
					Username: o.Username, Token: o.AccessToken,
				}))
			}, "authenticate", nil
		default:
			return nil, "", errs.New(errs.CodeMailAuthFailed, "The server offers no supported OAuth authentication mechanism (XOAUTH2 or OAUTHBEARER).")
		}
	}
	login := func() error { return cl.c.Login(o.Username, o.Password).Wait() }
	op := "login"
	if cl.caps["AUTH=PLAIN"] {
		op = "authenticate"
		login = func() error { return cl.c.Authenticate(sasl.NewPlainClient("", o.Username, o.Password)) }
	}
	return login, op, nil
}

// startTLS negotiates STARTTLS on the plaintext connection itself (pt is raw, possibly tapped) and
// returns the TLS connection. go-imap's own STARTTLS swaps the stream internally, which would leave
// the protocol tap on ciphertext. imapclient is then given a replayed greeting without capabilities,
// so capabilities are re-read over TLS (RFC 3501 §6.2.1).
func startTLS(ctx context.Context, raw, pt net.Conn, conf *tls.Config) (net.Conn, error) {
	dl := time.Now().Add(30 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(dl) {
		dl = d
	}
	_ = raw.SetDeadline(dl)
	br := bufio.NewReader(pt)
	greeting, err := br.ReadString('\n')
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(greeting, "* OK") {
		return nil, fmt.Errorf("imapc: unexpected greeting %q", strings.TrimSpace(greeting))
	}
	if _, err := io.WriteString(pt, "eb0 STARTTLS\r\n"); err != nil {
		return nil, err
	}
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return nil, err
		}
		if strings.HasPrefix(line, "* ") {
			continue
		}
		if !strings.HasPrefix(strings.ToUpper(line), "EB0 OK") {
			return nil, errs.New(errs.CodeMailTLSFailed, "The server refused STARTTLS.")
		}
		break
	}
	if br.Buffered() > 0 {
		return nil, errors.New("imapc: data after STARTTLS reply (possible injection)")
	}
	tc := tls.Client(raw, conf)
	if err := handshake(ctx, tc); err != nil {
		return nil, err
	}
	_ = raw.SetDeadline(time.Time{})
	return tc, nil
}

// replayConn reads from r (a replayed greeting, then the connection).
type replayConn struct {
	net.Conn
	r io.Reader
}

func (c *replayConn) Read(b []byte) (int, error) { return c.r.Read(b) }

func handshake(ctx context.Context, tc *tls.Conn) error {
	hctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return tc.HandshakeContext(hctx)
}

func (cl *Client) refreshCaps() {
	caps := map[string]bool{}
	for cp := range cl.c.Caps() {
		caps[strings.ToUpper(string(cp))] = true
	}
	cl.caps = caps
}

// Caps returns the server capabilities (upper case).
func (cl *Client) Caps() map[string]bool { return cl.caps }

// Has reports a capability.
func (cl *Client) Has(capability string) bool { return cl.caps[strings.ToUpper(capability)] }

// Updates delivers unsolicited updates of the selected mailbox. When the consumer falls behind, one
// UpdateOverflow replaces the dropped updates.
func (cl *Client) Updates() <-chan Update { return cl.updates }

// Closed is closed when the connection ends.
func (cl *Client) Closed() <-chan struct{} { return cl.closed }

// Close closes the connection immediately.
func (cl *Client) Close() error { return cl.c.Close() }

// Logout ends the session politely (bounded by the command timeout), then closes.
func (cl *Client) Logout(ctx context.Context) error {
	err := cl.do(ctx, "logout", func() error { return cl.c.Logout().Wait() })
	_ = cl.c.Close()
	return err
}

// do runs a blocking go-imap call with our deadline: if ctx or the command timeout expires first, the
// connection is closed (which unblocks the call) and the account reconnects (SP1: the library has no
// timeout on a half-open connection).
func (cl *Client) do(ctx context.Context, op string, f func() error) error {
	cctx, cancel := context.WithTimeout(ctx, cl.o.CommandTimeout)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- f() }()
	select {
	case err := <-done:
		return classify(op, err)
	case <-cctx.Done():
		_ = cl.c.Close()
		<-done //nolint:errcheck // the call fails once the connection is closed; our timeout error supersedes it
		if ctx.Err() != nil {
			return classify(op, ctx.Err())
		}
		return classify(op, ErrDead)
	}
}

func (cl *Client) push(u Update) {
	select {
	case cl.updates <- u:
	default:
		select { // drop one and signal overflow
		case <-cl.updates:
		default:
		}
		select {
		case cl.updates <- Update{Kind: UpdateOverflow}:
		default:
		}
	}
}

func (cl *Client) handlers() *imapclient.UnilateralDataHandler {
	return &imapclient.UnilateralDataHandler{
		Expunge: func(seqNum uint32) {
			cl.mu.Lock()
			var uid uint32
			if i := int(seqNum) - 1; i >= 0 && i < len(cl.seq) {
				uid = cl.seq[i]
				cl.seq = append(cl.seq[:i], cl.seq[i+1:]...)
			}
			cl.mu.Unlock()
			cl.push(Update{Kind: UpdateExpunge, SeqNum: seqNum, UID: uid})
		},
		Mailbox: func(d *imapclient.UnilateralDataMailbox) {
			if d.NumMessages == nil {
				return
			}
			n := *d.NumMessages
			if int64(n) > maxSeqMap { // a hostile or broken server must not make us allocate gigabytes
				cl.mu.Lock()
				cl.seq = nil
				cl.mu.Unlock()
				cl.push(Update{Kind: UpdateOverflow, NumMessages: n})
				return
			}
			cl.mu.Lock()
			for len(cl.seq) < int(n) {
				cl.seq = append(cl.seq, 0)
			}
			cl.mu.Unlock()
			cl.push(Update{Kind: UpdateExists, NumMessages: n})
		},
		Vanished: func(d *imapclient.VanishedData) {
			cl.mu.Lock()
			if d.Earlier { // answers to SELECT … (QRESYNC …): kept for the caller, never applied to the seq map
				if cl.collect != nil {
					for _, r := range d.UIDs {
						cl.collect.Vanished = append(cl.collect.Vanished, UIDRange{Start: uint32(r.Start), Stop: uint32(r.Stop)})
					}
				}
				cl.mu.Unlock()
				return
			}
			// VANISHED replaces EXPUNGE for the selected mailbox: drop those UIDs from the seq map (later
			// sequence numbers shift down) and report each known UID as an expunge.
			var gone []uint32
			kept := cl.seq[:0]
			for _, uid := range cl.seq {
				if uid != 0 && d.UIDs.Contains(imap.UID(uid)) {
					gone = append(gone, uid)
					continue
				}
				kept = append(kept, uid)
			}
			cl.seq = kept
			cl.mu.Unlock()
			for _, uid := range gone {
				cl.push(Update{Kind: UpdateExpunge, UID: uid})
			}
		},
		Fetch: func(m *imapclient.FetchMessageData) {
			buf, err := m.Collect()
			if err != nil {
				return
			}
			uid := uint32(buf.UID)
			cl.mu.Lock()
			if cl.collect != nil && uid != 0 { // a change reported by SELECT … (QRESYNC …)
				cl.collect.Changed = append(cl.collect.Changed, Message{UID: uid, SeqNum: buf.SeqNum, Flags: flagStrings(buf.Flags), ModSeq: buf.ModSeq})
				cl.mu.Unlock()
				return
			}
			i := int(buf.SeqNum) - 1
			if uid == 0 && i >= 0 && i < len(cl.seq) {
				uid = cl.seq[i]
			} else if uid != 0 && i >= 0 && i < len(cl.seq) {
				cl.seq[i] = uid
			}
			cl.mu.Unlock()
			cl.push(Update{Kind: UpdateFlags, SeqNum: buf.SeqNum, UID: uid, Flags: flagStrings(buf.Flags), ModSeq: buf.ModSeq})
		},
	}
}

// UIDForSeq resolves a sequence number of the selected mailbox.
func (cl *Client) UIDForSeq(seq uint32) uint32 {
	cl.mu.Lock()
	defer cl.mu.Unlock()
	if i := int(seq) - 1; i >= 0 && i < len(cl.seq) {
		return cl.seq[i]
	}
	return 0
}

func flagStrings(fs []imap.Flag) []string {
	out := make([]string, len(fs))
	for i, f := range fs {
		out[i] = string(f)
	}
	sort.Strings(out)
	return out
}

var specialUses = []string{`\All`, `\Archive`, `\Drafts`, `\Flagged`, `\Junk`, `\Sent`, `\Trash`}

// List returns every mailbox with SPECIAL-USE and STATUS (LIST-STATUS when offered, else STATUS per folder).
func (cl *Client) List(ctx context.Context) ([]MailboxInfo, error) {
	opts := &imap.ListOptions{}
	if cl.Has("SPECIAL-USE") {
		opts.ReturnSpecialUse = true
	}
	statusOpts := &imap.StatusOptions{NumMessages: true, UIDNext: true, UIDValidity: true, HighestModSeq: cl.Has("CONDSTORE")}
	if cl.Has("LIST-STATUS") {
		opts.ReturnStatus = statusOpts
	}
	var list []*imap.ListData
	if err := cl.do(ctx, "list", func() error {
		var err error
		list, err = cl.c.List("", "*", opts).Collect()
		return err
	}); err != nil {
		return nil, err
	}
	out := make([]MailboxInfo, 0, len(list))
	for _, l := range list {
		mi := MailboxInfo{Name: l.Mailbox}
		if l.Delim != 0 {
			mi.Delimiter = string(l.Delim)
		}
		noSelect := false
		for _, a := range l.Attrs {
			mi.Attrs = append(mi.Attrs, string(a))
			for _, su := range specialUses {
				if strings.EqualFold(string(a), su) {
					mi.SpecialUse = su
				}
			}
			if strings.EqualFold(string(a), `\Noselect`) || strings.EqualFold(string(a), `\NonExistent`) {
				noSelect = true
			}
		}
		if strings.EqualFold(mi.Name, "INBOX") {
			mi.Name = "INBOX"
		}
		if l.Status != nil {
			mi.Status = toStatus(l.Status)
		} else if !noSelect && !cl.Has("LIST-STATUS") {
			st, err := cl.Status(ctx, mi.Name)
			if err == nil {
				mi.Status = st
			}
		}
		out = append(out, mi)
	}
	return out, nil
}

func toStatus(s *imap.StatusData) *Status {
	st := &Status{UIDNext: uint32(s.UIDNext), UIDValidity: s.UIDValidity, HighestModSeq: s.HighestModSeq}
	if s.NumMessages != nil {
		st.Messages = *s.NumMessages
	}
	return st
}

// Status returns STATUS of one mailbox.
func (cl *Client) Status(ctx context.Context, name string) (*Status, error) {
	var sd *imap.StatusData
	err := cl.do(ctx, "status", func() error {
		var err error
		sd, err = cl.c.Status(name, &imap.StatusOptions{NumMessages: true, UIDNext: true, UIDValidity: true, HighestModSeq: cl.Has("CONDSTORE")}).Wait()
		return err
	})
	if err != nil {
		return nil, err
	}
	return toStatus(sd), nil
}

// Select opens a mailbox (EXAMINE when readOnly) with CONDSTORE when offered, and builds the seq→UID map.
func (cl *Client) Select(ctx context.Context, name string, readOnly bool) (*SelectInfo, error) {
	return cl.selectWith(ctx, name, readOnly, &imap.SelectOptions{ReadOnly: readOnly, CondStore: cl.Has("CONDSTORE")})
}

// SelectQResync opens a mailbox with QRESYNC (RFC 7162 §3.2.5) for a folder last seen at uidValidity and
// modSeq. The returned data lists what was expunged and changed since then. When the UIDVALIDITY in the
// returned SelectInfo differs from uidValidity, the server ignored the parameters: the caller resets the
// folder. Requires QResync().
func (cl *Client) SelectQResync(ctx context.Context, name string, readOnly bool, uidValidity uint32, modSeq uint64) (*SelectInfo, *QResyncData, error) {
	if !cl.qresync {
		return nil, nil, errs.New(errs.CodeInternalError, "imapc: QRESYNC is not enabled on this connection")
	}
	data := &QResyncData{}
	cl.mu.Lock()
	cl.collect = data
	cl.mu.Unlock()
	opts := &imap.SelectOptions{ReadOnly: readOnly, QResync: &imap.SelectQResync{UIDValidity: uidValidity, ModSeq: modSeq}}
	info, err := cl.selectWith(ctx, name, readOnly, opts)
	cl.mu.Lock()
	cl.collect = nil
	cl.mu.Unlock()
	if err != nil {
		return nil, nil, err
	}
	return info, data, nil
}

func (cl *Client) selectWith(ctx context.Context, name string, readOnly bool, opts *imap.SelectOptions) (*SelectInfo, error) {
	var sd *imap.SelectData
	err := cl.do(ctx, "select", func() error {
		var err error
		sd, err = cl.c.Select(name, opts).Wait()
		return err
	})
	if err != nil {
		return nil, err
	}
	info := &SelectInfo{Name: name, Exists: sd.NumMessages, UIDNext: uint32(sd.UIDNext), UIDValidity: sd.UIDValidity, HighestModSeq: sd.HighestModSeq, ReadOnly: readOnly}
	var seq []uint32
	if sd.NumMessages > 0 && int64(sd.NumMessages) <= maxSeqMap {
		var msgs []*imapclient.FetchMessageBuffer
		var all imap.SeqSet
		all.AddRange(1, 0) // 1:*
		if err := cl.do(ctx, "fetch", func() error {
			var err error
			msgs, err = cl.c.Fetch(all, &imap.FetchOptions{UID: true}).Collect()
			return err
		}); err != nil {
			return nil, err
		}
		seq = make([]uint32, sd.NumMessages)
		for _, m := range msgs {
			if i := int(m.SeqNum) - 1; i >= 0 && i < len(seq) {
				seq[i] = uint32(m.UID)
			}
		}
	}
	cl.mu.Lock()
	cl.seq, cl.selected = seq, name
	cl.mu.Unlock()
	return info, nil
}

// Selected returns the selected mailbox name.
func (cl *Client) Selected() string {
	cl.mu.Lock()
	defer cl.mu.Unlock()
	return cl.selected
}

func uidSet(from, to uint32) imap.UIDSet {
	var s imap.UIDSet
	s.AddRange(imap.UID(from), imap.UID(to)) // to = 0 means "*"
	return s
}

// UIDs lists every UID of the selected mailbox (for the full-mode deletion diff).
func (cl *Client) UIDs(ctx context.Context) ([]uint32, error) {
	var data *imap.SearchData
	err := cl.do(ctx, "search", func() error {
		var err error
		data, err = cl.c.UIDSearch(&imap.SearchCriteria{}, nil).Wait()
		return err
	})
	if err != nil {
		return nil, err
	}
	all := data.AllUIDs()
	out := make([]uint32, len(all))
	for i, u := range all {
		out[i] = uint32(u)
	}
	return out, nil
}

// Fetch returns the selected items for UIDs in [from, to] (to = 0 means up to the newest).
func (cl *Client) Fetch(ctx context.Context, from, to uint32, fo FetchOptions) ([]Message, error) {
	return cl.fetch(ctx, uidSet(from, to), fo)
}

// FetchUIDs fetches an explicit UID list.
func (cl *Client) FetchUIDs(ctx context.Context, uids []uint32, fo FetchOptions) ([]Message, error) {
	if len(uids) == 0 {
		return nil, nil
	}
	set := make([]imap.UID, len(uids))
	for i, u := range uids {
		set[i] = imap.UID(u)
	}
	return cl.fetch(ctx, imap.UIDSetNum(set...), fo)
}

func (cl *Client) fetch(ctx context.Context, set imap.UIDSet, fo FetchOptions) ([]Message, error) {
	opts := &imap.FetchOptions{UID: true, Flags: fo.Flags, Envelope: fo.Envelope, InternalDate: fo.InternalDate, RFC822Size: fo.Size}
	if fo.ModSeq && cl.Has("CONDSTORE") {
		opts.ModSeq = true
	}
	if fo.ChangedSince > 0 && cl.Has("CONDSTORE") {
		opts.ChangedSince = fo.ChangedSince
	}
	if fo.Body {
		opts.BodyStructure = &imap.FetchItemBodyStructure{Extended: true}
	}
	var hdrSection *imap.FetchItemBodySection
	if fo.FullHeader {
		hdrSection = &imap.FetchItemBodySection{Specifier: imap.PartSpecifierHeader, Peek: true}
	} else if len(fo.HeaderFields) > 0 {
		hdrSection = &imap.FetchItemBodySection{Specifier: imap.PartSpecifierHeader, HeaderFields: fo.HeaderFields, Peek: true}
	}
	if hdrSection != nil {
		opts.BodySection = append(opts.BodySection, hdrSection)
	}
	var bufs []*imapclient.FetchMessageBuffer
	if err := cl.do(ctx, "fetch", func() error {
		var err error
		bufs, err = cl.c.Fetch(set, opts).Collect()
		return err
	}); err != nil {
		return nil, err
	}
	out := make([]Message, 0, len(bufs))
	for _, b := range bufs {
		m := Message{UID: uint32(b.UID), SeqNum: b.SeqNum, Flags: flagStrings(b.Flags), ModSeq: b.ModSeq, InternalDate: b.InternalDate, Size: b.RFC822Size}
		if b.Envelope != nil {
			m.Envelope = toEnvelope(b.Envelope)
		}
		if b.BodyStructure != nil {
			m.Body = toPart(b.BodyStructure, nil)
		}
		for _, s := range b.BodySection {
			if hdrSection != nil && s.Section.Specifier == imap.PartSpecifierHeader && len(s.Section.Part) == 0 {
				m.Header = s.Bytes
			}
		}
		cl.mu.Lock()
		if i := int(b.SeqNum) - 1; i >= 0 && i < len(cl.seq) && m.UID != 0 {
			cl.seq[i] = m.UID
		}
		cl.mu.Unlock()
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UID < out[j].UID })
	return out, nil
}

// Section fetches one body part (e.g. [1 2]) of a message, at most max bytes (0 = whole part).
func (cl *Client) Section(ctx context.Context, uid uint32, part []int, max int64) ([]byte, error) {
	sec := &imap.FetchItemBodySection{Part: part, Peek: true}
	if max > 0 {
		sec.Partial = &imap.SectionPartial{Offset: 0, Size: max}
	}
	var bufs []*imapclient.FetchMessageBuffer
	if err := cl.do(ctx, "fetch", func() error {
		var err error
		bufs, err = cl.c.Fetch(imap.UIDSetNum(imap.UID(uid)), &imap.FetchOptions{UID: true, BodySection: []*imap.FetchItemBodySection{sec}}).Collect()
		return err
	}); err != nil {
		return nil, err
	}
	for _, b := range bufs {
		for _, s := range b.BodySection {
			return s.Bytes, nil
		}
	}
	return nil, errs.New(errs.CodeMessageNotFound, "The message vanished before it could be fetched.")
}

// Raw fetches the whole RFC 822 message (BODY.PEEK[]), at most max bytes (0 = unlimited).
func (cl *Client) Raw(ctx context.Context, uid uint32, max int64) ([]byte, error) {
	return cl.Section(ctx, uid, nil, max)
}

// Append stores a message (copy to Sent, upload) and returns its UID when the server reports it
// (UIDPLUS); 0 otherwise — find it by Message-ID then (S5 §8).
func (cl *Client) Append(ctx context.Context, mailbox string, flags []string, date time.Time, raw []byte) (uint32, error) {
	fs := make([]imap.Flag, len(flags))
	for i, f := range flags {
		fs[i] = imap.Flag(f)
	}
	var ad *imap.AppendData
	err := cl.do(ctx, "append", func() error {
		cmd := cl.c.Append(mailbox, int64(len(raw)), &imap.AppendOptions{Flags: fs, Time: date})
		if _, err := cmd.Write(raw); err != nil {
			return err
		}
		if err := cmd.Close(); err != nil {
			return err
		}
		var err error
		ad, err = cmd.Wait()
		return err
	})
	if err != nil {
		return 0, err
	}
	return uint32(ad.UID), nil
}

// Create creates a mailbox.
func (cl *Client) Create(ctx context.Context, name string) error {
	return cl.do(ctx, "create", func() error { return cl.c.Create(name, nil).Wait() })
}

// Rename renames a mailbox.
func (cl *Client) Rename(ctx context.Context, from, to string) error {
	return cl.do(ctx, "rename", func() error { return cl.c.Rename(from, to, nil).Wait() })
}

// Subscribe marks a mailbox subscribed.
func (cl *Client) Subscribe(ctx context.Context, name string) error {
	return cl.do(ctx, "subscribe", func() error { return cl.c.Subscribe(name).Wait() })
}

// Unsubscribe marks a mailbox unsubscribed.
func (cl *Client) Unsubscribe(ctx context.Context, name string) error {
	return cl.do(ctx, "unsubscribe", func() error { return cl.c.Unsubscribe(name).Wait() })
}

// Delete deletes a mailbox.
func (cl *Client) Delete(ctx context.Context, name string) error {
	return cl.do(ctx, "delete", func() error { return cl.c.Delete(name).Wait() })
}

// SearchMessageID finds UIDs with the given Message-ID header in the selected mailbox.
func (cl *Client) SearchMessageID(ctx context.Context, messageID string) ([]uint32, error) {
	var data *imap.SearchData
	err := cl.do(ctx, "search", func() error {
		var err error
		data, err = cl.c.UIDSearch(&imap.SearchCriteria{Header: []imap.SearchCriteriaHeaderField{{Key: "Message-ID", Value: messageID}}}, nil).Wait()
		return err
	})
	if err != nil {
		return nil, err
	}
	all := data.AllUIDs()
	out := make([]uint32, len(all))
	for i, u := range all {
		out[i] = uint32(u)
	}
	return out, nil
}

// StoreFlags adds or removes flags on UIDs of the selected mailbox.
func (cl *Client) StoreFlags(ctx context.Context, uids []uint32, add bool, flags []string) error {
	op := imap.StoreFlagsDel
	if add {
		op = imap.StoreFlagsAdd
	}
	return cl.storeFlags(ctx, uids, op, flags)
}

// StoreFlagsSet replaces the flags on UIDs of the selected mailbox.
func (cl *Client) StoreFlagsSet(ctx context.Context, uids []uint32, flags []string) error {
	return cl.storeFlags(ctx, uids, imap.StoreFlagsSet, flags)
}

func (cl *Client) storeFlags(ctx context.Context, uids []uint32, op imap.StoreFlagsOp, flags []string) error {
	if len(uids) == 0 {
		return nil
	}
	set := make([]imap.UID, len(uids))
	for i, u := range uids {
		set[i] = imap.UID(u)
	}
	fs := make([]imap.Flag, len(flags))
	for i, f := range flags {
		fs[i] = imap.Flag(f)
	}
	return cl.do(ctx, "store", func() error {
		_, err := cl.c.Store(imap.UIDSetNum(set...), &imap.StoreFlags{Op: op, Flags: fs, Silent: true}, nil).Collect()
		return err
	})
}

// Copy copies UIDs to another mailbox.
func (cl *Client) Copy(ctx context.Context, uids []uint32, dest string) error {
	if len(uids) == 0 {
		return nil
	}
	set := make([]imap.UID, len(uids))
	for i, u := range uids {
		set[i] = imap.UID(u)
	}
	return cl.do(ctx, "copy", func() error { _, err := cl.c.Copy(imap.UIDSetNum(set...), dest).Wait(); return err })
}

// SearchCriteria is a subset of IMAP SEARCH keys used by the mailbox API (P4.1).
type SearchCriteria struct {
	Text, Subject, From, To string
	Seen, Unseen, Flagged   *bool
	Since, Before           *time.Time
	HeaderName, HeaderValue string
	Larger, Smaller         *int64
	UIDMin, UIDMax          *uint32
	GmailRaw                string
	Labels                  []string
}

// Search runs UID SEARCH with the given criteria on the selected mailbox.
func (cl *Client) Search(ctx context.Context, q SearchCriteria) ([]uint32, error) {
	crit := &imap.SearchCriteria{}
	if q.Text != "" {
		crit.Text = []string{q.Text}
	}
	if q.Subject != "" {
		crit.Header = append(crit.Header, imap.SearchCriteriaHeaderField{Key: "Subject", Value: q.Subject})
	}
	if q.From != "" {
		crit.Header = append(crit.Header, imap.SearchCriteriaHeaderField{Key: "From", Value: q.From})
	}
	if q.To != "" {
		crit.Header = append(crit.Header, imap.SearchCriteriaHeaderField{Key: "To", Value: q.To})
	}
	if q.HeaderName != "" {
		crit.Header = append(crit.Header, imap.SearchCriteriaHeaderField{Key: q.HeaderName, Value: q.HeaderValue})
	}
	if q.Seen != nil && *q.Seen {
		crit.Flag = append(crit.Flag, imap.FlagSeen)
	}
	if q.Unseen != nil && *q.Unseen {
		crit.NotFlag = append(crit.NotFlag, imap.FlagSeen)
	}
	if q.Flagged != nil && *q.Flagged {
		crit.Flag = append(crit.Flag, imap.FlagFlagged)
	}
	if q.Flagged != nil && !*q.Flagged {
		crit.NotFlag = append(crit.NotFlag, imap.FlagFlagged)
	}
	if q.Since != nil {
		crit.Since = q.Since.UTC()
	}
	if q.Before != nil {
		crit.Before = q.Before.UTC()
	}
	if q.Larger != nil {
		crit.Larger = *q.Larger
	}
	if q.Smaller != nil {
		crit.Smaller = *q.Smaller
	}
	if q.UIDMin != nil || q.UIDMax != nil {
		var set imap.UIDSet
		start, stop := imap.UID(1), imap.UID(0)
		if q.UIDMin != nil {
			start = imap.UID(*q.UIDMin)
		}
		if q.UIDMax != nil {
			stop = imap.UID(*q.UIDMax)
		}
		set.AddRange(start, stop)
		crit.UID = []imap.UIDSet{set}
	}
	// GmailRaw / Labels: X-GM-RAW via Not+Or is not in go-imap; callers must ensure X-GM-EXT-1 and
	// pass GmailRaw which we encode as a HEADER surrogate when the server is Gmail (best-effort Text).
	if q.GmailRaw != "" {
		crit.Text = append(crit.Text, q.GmailRaw)
	}
	for _, lab := range q.Labels {
		if lab != "" {
			crit.Text = append(crit.Text, lab)
		}
	}
	var data *imap.SearchData
	err := cl.do(ctx, "search", func() error {
		var err error
		data, err = cl.c.UIDSearch(crit, nil).Wait()
		return err
	})
	if err != nil {
		return nil, err
	}
	all := data.AllUIDs()
	out := make([]uint32, len(all))
	for i, u := range all {
		out[i] = uint32(u)
	}
	return out, nil
}

// Move moves UIDs to another mailbox (MOVE, or COPY + delete + expunge without it).
func (cl *Client) Move(ctx context.Context, uids []uint32, dest string) error {
	if len(uids) == 0 {
		return nil
	}
	set := make([]imap.UID, len(uids))
	for i, u := range uids {
		set[i] = imap.UID(u)
	}
	us := imap.UIDSetNum(set...)
	if cl.Has("MOVE") {
		return cl.do(ctx, "move", func() error { _, err := cl.c.Move(us, dest).Wait(); return err })
	}
	if err := cl.Copy(ctx, uids, dest); err != nil {
		return err
	}
	if err := cl.StoreFlags(ctx, uids, true, []string{string(imap.FlagDeleted)}); err != nil {
		return err
	}
	return cl.Expunge(ctx, uids)
}

// Expunge removes UIDs flagged \Deleted (UID EXPUNGE when UIDPLUS is offered).
func (cl *Client) Expunge(ctx context.Context, uids []uint32) error {
	if cl.Has("UIDPLUS") && len(uids) > 0 {
		set := make([]imap.UID, len(uids))
		for i, u := range uids {
			set[i] = imap.UID(u)
		}
		return cl.do(ctx, "expunge", func() error { return cl.c.UIDExpunge(imap.UIDSetNum(set...)).Close() })
	}
	return cl.do(ctx, "expunge", func() error { return cl.c.Expunge().Close() })
}

// Noop sends NOOP (liveness check and a chance for the server to send updates).
func (cl *Client) Noop(ctx context.Context) error {
	return cl.do(ctx, "noop", func() error { return cl.c.Noop().Wait() })
}

// Idle runs one IDLE cycle: it returns nil after d (or when ctx ends) once the server has answered
// DONE. If DONE is not answered within DoneTimeout the connection is closed and ErrDead (classified as
// mail_timeout) is returned — SP1 showed this is the only reliable half-open detector.
func (cl *Client) Idle(ctx context.Context, d time.Duration) error {
	return cl.IdleUntil(ctx, d, nil)
}

// IdleUntil idles like Idle but also ends the cycle gracefully (DONE) when wake receives or is closed —
// the sync engine wakes IDLE for commands and timers without tearing the connection down. ctx remains the
// connection's lifetime: cancelling it ends the cycle too.
func (cl *Client) IdleUntil(ctx context.Context, d time.Duration, wake <-chan struct{}) error {
	var cmd *imapclient.IdleCommand
	if err := cl.do(ctx, "idle", func() error {
		var err error
		cmd, err = cl.c.Idle()
		return err
	}); err != nil {
		return err
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	case <-wake:
	case <-cl.closed:
		return classify("idle", net.ErrClosed)
	}
	done := make(chan error, 1)
	go func() {
		if err := cmd.Close(); err != nil {
			done <- err
			return
		}
		done <- cmd.Wait()
	}()
	select {
	case err := <-done:
		return classify("idle", err)
	case <-time.After(cl.o.DoneTimeout):
		_ = cl.c.Close()
		return classify("idle", ErrDead)
	}
}

// toEnvelope converts the library envelope.
func toEnvelope(e *imap.Envelope) *Envelope {
	conv := func(as []imap.Address) []Address {
		out := make([]Address, 0, len(as))
		for _, a := range as {
			if a.IsGroupStart() || a.IsGroupEnd() {
				continue
			}
			out = append(out, Address{Name: a.Name, Address: a.Addr()})
		}
		return out
	}
	irt := make([]string, 0, len(e.InReplyTo))
	for _, id := range e.InReplyTo {
		irt = append(irt, MsgID(id))
	}
	return &Envelope{Date: e.Date, Subject: e.Subject, From: conv(e.From), Sender: conv(e.Sender), ReplyTo: conv(e.ReplyTo),
		To: conv(e.To), Cc: conv(e.Cc), Bcc: conv(e.Bcc), InReplyTo: irt, MessageID: MsgID(e.MessageID)}
}

// MsgID returns a Message-ID in its RFC 5322 form "<id>" (go-imap strips the brackets).
func MsgID(id string) string {
	id = strings.TrimSpace(id)
	if id == "" || strings.HasPrefix(id, "<") {
		return id
	}
	return "<" + id + ">"
}

func toPart(bs imap.BodyStructure, path []int) *Part {
	switch b := bs.(type) {
	case *imap.BodyStructureMultiPart:
		p := &Part{Path: path, MediaType: b.MediaType()}
		for i, ch := range b.Children {
			p.Children = append(p.Children, toPart(ch, append(append([]int(nil), path...), i+1)))
		}
		return p
	case *imap.BodyStructureSinglePart:
		if path == nil {
			path = []int{1}
		}
		p := &Part{Path: path, MediaType: b.MediaType(), Charset: b.Params["charset"], Encoding: strings.ToLower(b.Encoding), Size: b.Size, ContentID: strings.Trim(b.ID, "<>")}
		if d := b.Disposition(); d != nil {
			p.Disposition = strings.ToLower(d.Value)
			p.Filename = d.Params["filename"]
		}
		if p.Filename == "" {
			p.Filename = b.Params["name"]
		}
		return p
	}
	return &Part{Path: path}
}

// PathString renders a part path as "1.2".
func PathString(path []int) string {
	parts := make([]string, len(path))
	for i, n := range path {
		parts[i] = strconv.Itoa(n)
	}
	return strings.Join(parts, ".")
}

// maxSeqMap bounds the seq→UID map (4 bytes per message): larger mailboxes skip UID resolution of
// unsolicited updates and run full passes instead.
const maxSeqMap = 5_000_000

// ErrNotSelected is returned by mailbox operations without a selected mailbox.
var ErrNotSelected = errors.New("imapc: no mailbox selected")
