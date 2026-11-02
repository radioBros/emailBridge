// Package imapc is EmailBridge's IMAP client interface over go-imap v2 (the only package that imports
// it, S0 §3.4). It adds what the beta library lacks (SP1): owned deadlines on every command, IDLE cycles
// with a DONE timeout that detects half-open connections, a seq→UID map for the selected mailbox (the
// library delivers unsolicited FETCH without UIDs), protocol-log tapping and error classification.
package imapc

import (
	"context"
	"net"
	"time"

	"github.com/radiobros/emailbridge/internal/protolog"
)

// TLSMode of the connection (S1 accounts.imap.tls).
type TLSMode string

// TLS modes.
const (
	TLSImplicit TLSMode = "implicit"
	TLSStartTLS TLSMode = "starttls"
	TLSNone     TLSMode = "none"
)

// DialFunc opens the TCP connection; production passes the egress dialer (mail purpose, S10 §8).
type DialFunc func(ctx context.Context, network, addr string) (net.Conn, error)

// Options configure Dial.
type Options struct {
	Addr               string // host:port
	TLS                TLSMode
	InsecureSkipVerify bool // accounts.ignoreCertErrors (warned about in the admin)
	Username, Password string
	// AccessToken, when set, authenticates with XOAUTH2 (preferred) or OAUTHBEARER instead of password (P6.1).
	AccessToken    string
	ID             map[string]string // IMAP ID fields sent when advertised (name, version, vendor, support-url)
	Dial           DialFunc
	CommandTimeout time.Duration // default 120 s
	DoneTimeout    time.Duration // default 30 s: no tagged reply to DONE → connection is dead
	// Tap records the protocol transcript (masked) when not nil.
	Tap     protolog.Sink
	TapMode protolog.Mode
	TapConn string // connection ID shown in protocol logs
	Updates int    // capacity of the updates channel (default 1024)
	// QResync sends ENABLE QRESYNC when the server offers it (switch sync.qresync, S5 §4). With it on,
	// the server reports expunges as VANISHED and SelectQResync is available.
	QResync bool
	// DisableRev2 never ENABLE IMAP4rev2 and strips the capability (W57 / accounts.imap.disableRev2).
	DisableRev2 bool
}

// UIDRange is an inclusive UID range.
type UIDRange struct{ Start, Stop uint32 }

// QResyncData is what SELECT … (QRESYNC (uidvalidity modseq)) reported before its tagged OK: UIDs expunged
// since modseq (as ranges — a server may report ranges far larger than the mailbox) and messages whose
// flags changed since modseq.
type QResyncData struct {
	Vanished []UIDRange
	Changed  []Message
}

// UpdateKind of an unsolicited server update.
type UpdateKind int

// Update kinds.
const (
	UpdateExists   UpdateKind = iota + 1 // new message count (new mail)
	UpdateExpunge                        // a message was expunged (UID resolved through the seq map when known)
	UpdateFlags                          // flags/modseq of a message changed
	UpdateOverflow                       // updates were dropped: run a full pass
)

// Update is an unsolicited server notification for the selected mailbox.
type Update struct {
	Kind        UpdateKind
	NumMessages uint32
	SeqNum      uint32
	UID         uint32 // 0 when unknown
	Flags       []string
	ModSeq      uint64
}

// MailboxInfo is one LIST entry with its STATUS.
type MailboxInfo struct {
	Name       string
	Delimiter  string
	Attrs      []string
	SpecialUse string // \Sent, \Drafts, \Junk, \Trash, \Archive, \All, \Flagged or ""
	Status     *Status
}

// Status of a mailbox.
type Status struct {
	Messages      uint32
	UIDNext       uint32
	UIDValidity   uint32
	HighestModSeq uint64
}

// SelectInfo is the state after SELECT/EXAMINE.
type SelectInfo struct {
	Name          string
	Exists        uint32
	UIDNext       uint32
	UIDValidity   uint32
	HighestModSeq uint64
	ReadOnly      bool
}

// Address is an envelope address.
type Address struct {
	Name    string `json:"name,omitempty"`
	Address string `json:"address"`
}

// Envelope of a message.
type Envelope struct {
	Date      time.Time
	Subject   string
	From      []Address
	Sender    []Address
	ReplyTo   []Address
	To        []Address
	Cc        []Address
	Bcc       []Address
	InReplyTo []string
	MessageID string
}

// Part is a node of the body structure (leaf parts only carry media type details).
type Part struct {
	Path        []int  // IMAP part path, e.g. [1 2]
	MediaType   string // lowercase type/subtype
	Charset     string
	Encoding    string
	Size        uint32
	Disposition string // inline | attachment | ""
	Filename    string
	ContentID   string
	Children    []*Part
}

// Message is the fetched metadata of one message.
type Message struct {
	UID          uint32
	SeqNum       uint32
	Flags        []string
	ModSeq       uint64
	InternalDate time.Time
	Size         int64
	Envelope     *Envelope
	Body         *Part
	Header       []byte            // requested header fields (raw)
	Sections     map[string][]byte // other requested sections by key "1.2" or "TEXT"
}

// FetchOptions select what Fetch returns.
type FetchOptions struct {
	Flags, Envelope, Body, InternalDate, Size, ModSeq bool
	HeaderFields                                      []string // fetched with BODY.PEEK[HEADER.FIELDS (...)]
	FullHeader                                        bool     // BODY.PEEK[HEADER]
	ChangedSince                                      uint64   // CONDSTORE
}
