package messages

import (
	"bytes"
	"encoding/base64"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/submit"
)

func TestWriteAttachment(t *testing.T) {
	var hdr string
	var buf bytes.Buffer
	writeAttachment(func(k, v string) { hdr = k + ":" + v }, &buf, "application/pdf", []byte("%PDF"))
	if hdr != "Content-Type:application/pdf" || buf.String() != "%PDF" {
		t.Fatalf("%q %q", hdr, buf.String())
	}
}

func TestChunkUIDs(t *testing.T) {
	uids := make([]uint32, 450)
	for i := range uids {
		uids[i] = uint32(i + 1)
	}
	chunks := chunkUIDs(uids, 200)
	if len(chunks) != 3 || len(chunks[0]) != 200 || len(chunks[1]) != 200 || len(chunks[2]) != 50 {
		t.Fatalf("%v", lens(chunks))
	}
	if chunkUIDs(nil, 200) != nil {
		t.Fatal("nil")
	}
}

func lens(chunks [][]uint32) []int {
	out := make([]int, len(chunks))
	for i, c := range chunks {
		out[i] = len(c)
	}
	return out
}

func TestParseMsgSort(t *testing.T) {
	f, d, err := parseMsgSort("")
	if err != nil || f != "internalDate" || d != -1 {
		t.Fatalf("%s %d %v", f, d, err)
	}
	f, d, err = parseMsgSort("size")
	if err != nil || f != "size" || d != 1 {
		t.Fatalf("%s %d %v", f, d, err)
	}
	f, d, err = parseMsgSort("-id")
	if err != nil || f != "_id" || d != -1 {
		t.Fatalf("%s %d %v", f, d, err)
	}
	if _, _, err := parseMsgSort("subject"); errs.CodeOf(err) != errs.CodeSortNotAllowed {
		t.Fatalf("%v", err)
	}
}

func TestCursorRoundTrip(t *testing.T) {
	p := cursorPayload{ID: "msg_01hxxxxxxxxxxxxxxxxxxxxxx", Field: "internalDate", Desc: true, Key: "2026-01-01T00:00:00Z"}
	s := encodeCursor(p)
	got, err := decodeCursor(s)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != p.ID || got.Field != p.Field || !got.Desc {
		t.Fatalf("%+v", got)
	}
	if _, err := decodeCursor("!!!"); err == nil {
		t.Fatal("want err")
	}
	empty := encodeCursor(cursorPayload{})
	if _, err := decodeCursor(empty); err == nil {
		t.Fatal("empty id")
	}
	if _, err := decodeCursor(base64.RawURLEncoding.EncodeToString([]byte("{"))); err == nil {
		t.Fatal("bad json")
	}
}

func TestCursorFilter(t *testing.T) {
	cf, err := cursorFilter(cursorPayload{ID: "msg_a"}, "_id", 1)
	if err != nil || cf["_id"] == nil {
		t.Fatalf("%v %v", cf, err)
	}
	cf, err = cursorFilter(cursorPayload{ID: "msg_a", Key: "2026-01-02T03:04:05.000000006Z"}, "internalDate", -1)
	if err != nil {
		t.Fatal(err)
	}
	if cf["$or"] == nil {
		t.Fatalf("%v", cf)
	}
	cf, err = cursorFilter(cursorPayload{ID: "msg_a", Key: "2026-01-02T03:04:05Z"}, "internalDate", 1)
	if err != nil || cf["$or"] == nil {
		t.Fatalf("%v %v", cf, err)
	}
	if _, err := cursorFilter(cursorPayload{ID: "msg_a", Key: "not-a-date"}, "internalDate", 1); err == nil {
		t.Fatal()
	}
	cf, err = cursorFilter(cursorPayload{ID: "msg_a", Key: int64(9)}, "size", -1)
	if err != nil || cf["$or"] == nil {
		t.Fatalf("%v %v", cf, err)
	}
}

func TestMsgSortKey(t *testing.T) {
	m := &msgDoc{ID: "msg_x", Size: 9, UID: 3, InternalDate: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)}
	if msgSortKey(m, "size").(int64) != 9 {
		t.Fatal()
	}
	if msgSortKey(m, "uid").(uint32) != 3 {
		t.Fatal()
	}
	if msgSortKey(m, "_id").(string) != "msg_x" {
		t.Fatal()
	}
}

func TestDecodeRaw(t *testing.T) {
	raw := []byte("From: a@b\r\n\r\nhi")
	b64 := base64.StdEncoding.EncodeToString(raw)
	got, err := decodeRaw(b64)
	if err != nil || string(got) != string(raw) {
		t.Fatalf("%q %v", got, err)
	}
	if _, err := decodeRaw(""); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatal(err)
	}
	if _, err := decodeRaw("!!!!"); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatal(err)
	}
	big := base64.StdEncoding.EncodeToString(make([]byte, maxUpload+1))
	if _, err := decodeRaw(big); errs.CodeOf(err) != errs.CodeMessageTooLarge {
		t.Fatal(err)
	}
}

func TestBuildDraftMIME(t *testing.T) {
	raw, subj, mid, err := buildDraftMIME(DraftInput{
		From:    &submit.AddressInput{Name: "A", Address: "a@ex.test"},
		To:      []submit.AddressInput{{Address: "b@ex.test"}},
		Cc:      []submit.AddressInput{{Address: "c@ex.test"}},
		Subject: "Hello", Text: "body",
	})
	if err != nil || subj != "Hello" || mid == "" || len(raw) == 0 {
		t.Fatalf("%q %q %v", subj, mid, err)
	}
	b64 := base64.StdEncoding.EncodeToString([]byte("From: x@y\r\nSubject: Raw\r\nMessage-ID: <r@x>\r\n\r\nz"))
	_, subj, mid, err = buildDraftMIME(DraftInput{Raw: b64})
	if err != nil || subj != "Raw" || mid != "<r@x>" {
		t.Fatalf("%q %q %v", subj, mid, err)
	}
}

func TestParsePartPath(t *testing.T) {
	p, err := parsePartPath("1.2")
	if err != nil || len(p) != 2 || p[0] != 1 || p[1] != 2 {
		t.Fatalf("%v %v", p, err)
	}
	if _, err := parsePartPath(""); err == nil {
		t.Fatal()
	}
	if _, err := parsePartPath("0"); err == nil {
		t.Fatal()
	}
	if _, err := parsePartPath("a"); err == nil {
		t.Fatal()
	}
}

func TestViews(t *testing.T) {
	m := &mbxDoc{ID: "mbx_1", AccountID: "acc_1", Path: "INBOX", Status: "active", UIDValidity: 1}
	if mbxView(m).Path != "INBOX" {
		t.Fatal()
	}
	msg := &msgDoc{ID: "msg_1", AccountID: "acc_1", MailboxID: "mbx_1", UID: 1, EmailID: "e", Flags: []string{`\Seen`}}
	if msgView(msg).Flags[0] != `\Seen` {
		t.Fatal()
	}
	d := &draftDoc{ID: "drf_1", AccountID: "acc_1", Path: "Drafts", Size: 3}
	if draftView(d).Size != 3 {
		t.Fatal()
	}
}

func TestChunkUIDsDefault(t *testing.T) {
	uids := make([]uint32, 5)
	chunks := chunkUIDs(uids, 0)
	if len(chunks) != 1 || len(chunks[0]) != 5 {
		t.Fatal(lens(chunks))
	}
}
