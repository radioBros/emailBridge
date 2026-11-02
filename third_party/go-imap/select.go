package imap

// SelectOptions contains options for the SELECT or EXAMINE command.
type SelectOptions struct {
	ReadOnly  bool
	CondStore bool // requires CONDSTORE

	// QResync, when set, sends the QRESYNC parameter (RFC 7162 §3.2.5). It requires ENABLE QRESYNC and
	// implies CONDSTORE. The server answers with VANISHED (EARLIER) for UIDs expunged since ModSeq and
	// FETCH responses for messages changed since ModSeq, before the tagged OK. EmailBridge patch.
	QResync *SelectQResync
}

// SelectQResync holds the QRESYNC parameters of SELECT and EXAMINE. EmailBridge patch.
type SelectQResync struct {
	UIDValidity uint32
	ModSeq      uint64
	KnownUIDs   UIDSet // optional
}

// SelectData is the data returned by a SELECT command.
//
// In the old RFC 2060, PermanentFlags, UIDNext and UIDValidity are optional.
type SelectData struct {
	// Flags defined for this mailbox
	Flags []Flag
	// Flags that the client can change permanently
	PermanentFlags []Flag
	// Number of messages in this mailbox (aka. "EXISTS")
	NumMessages uint32
	// Sequence number of the first unseen message. Obsolete, IMAP4rev1 only.
	// Server-only, not supported in imapclient.
	FirstUnseenSeqNum uint32
	// Number of recent messages in this mailbox. Obsolete, IMAP4rev1 only.
	// Server-only, not supported in imapclient.
	NumRecent   uint32
	UIDNext     UID
	UIDValidity uint32

	List *ListData // requires IMAP4rev2

	HighestModSeq uint64 // requires CONDSTORE
}
