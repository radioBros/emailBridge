package messages

import (
	"bytes"
	"io"
	"strings"

	"github.com/emersion/go-message"
)

type cidPart struct {
	ct   string
	body []byte
}

func collectInlineCIDs(raw []byte, partMax int) map[string]cidPart {
	out := map[string]cidPart{}
	ent, err := message.Read(bytes.NewReader(raw))
	if err != nil {
		return out
	}
	walkCID(ent, partMax, out)
	return out
}

func walkCID(e *message.Entity, partMax int, out map[string]cidPart) {
	if e == nil {
		return
	}
	mr := e.MultipartReader()
	if mr == nil {
		cid := strings.Trim(strings.TrimSpace(e.Header.Get("Content-Id")), "<>")
		if cid == "" {
			return
		}
		ctype, _, _ := e.Header.ContentType()
		b, _ := io.ReadAll(io.LimitReader(e.Body, int64(partMax)+1))
		if len(b) > partMax {
			b = b[:partMax]
		}
		out[strings.ToLower(cid)] = cidPart{ct: ctype, body: b}
		return
	}
	for {
		p, err := mr.NextPart()
		if err != nil {
			return
		}
		walkCID(p, partMax, out)
	}
}
