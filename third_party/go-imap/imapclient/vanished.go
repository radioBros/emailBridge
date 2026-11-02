package imapclient

// EmailBridge patch: QRESYNC VANISHED responses (RFC 7162 §3.2.10). See third_party/go-imap/PATCHES.md.

import (
	"fmt"

	"github.com/emersion/go-imap/v2"
)

// VanishedData is a VANISHED response.
type VanishedData struct {
	// Earlier is true for "VANISHED (EARLIER)": UIDs expunged before this point (answers to
	// SELECT … (QRESYNC …) and UID FETCH … (VANISHED)). They do not change the current message count.
	Earlier bool
	UIDs    imap.UIDSet
}

// handleVanished parses `VANISHED [(EARLIER) SP] uid-set`. Without EARLIER the response replaces EXPUNGE for
// the selected mailbox, so the message count drops by the number of UIDs, like one EXPUNGE per UID.
func (c *Client) handleVanished() error {
	var data VanishedData
	if c.dec.Special('(') {
		var atom string
		if !c.dec.ExpectAtom(&atom) || !c.dec.ExpectSpecial(')') || !c.dec.ExpectSP() {
			return c.dec.Err()
		}
		if atom != "EARLIER" {
			return fmt.Errorf("in VANISHED: unknown tag %q", atom)
		}
		data.Earlier = true
	}
	if !c.dec.ExpectUIDSet(&data.UIDs) {
		return c.dec.Err()
	}

	if !data.Earlier {
		n := uidSetCount(data.UIDs)
		c.mutex.Lock()
		if c.state == imap.ConnStateSelected && c.mailbox != nil {
			c.mailbox = c.mailbox.copy()
			if n >= c.mailbox.NumMessages {
				c.mailbox.NumMessages = 0
			} else {
				c.mailbox.NumMessages -= n
			}
		}
		c.mutex.Unlock()
	}

	if handler := c.options.unilateralDataHandler().Vanished; handler != nil {
		handler(&data)
	}
	return nil
}

// uidSetCount counts the UIDs of a set whose ranges are all bounded (VANISHED never uses "*").
func uidSetCount(set imap.UIDSet) uint32 {
	var n uint32
	for _, r := range set {
		start, stop := r.Start, r.Stop
		if stop == 0 || start == 0 { // "*" never appears in VANISHED; count it as one UID defensively
			n++
			continue
		}
		if stop < start {
			start, stop = stop, start
		}
		n += uint32(stop-start) + 1
	}
	return n
}
