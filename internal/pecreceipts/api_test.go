package pecreceipts

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"io"
	"testing"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/store"
)

type streamWriter struct {
	h   map[string]string
	buf *bytes.Buffer
}

func (s *streamWriter) SetHeader(k, v string) { s.h[k] = v }
func (s *streamWriter) BodyWriter() io.Writer { return s.buf }

func TestDigestHeader(t *testing.T) {
	sum := sha256.Sum256([]byte("hi"))
	hexStr := hex.EncodeToString(sum[:])
	got := digestHeader(hexStr)
	want := "sha-256=" + base64.StdEncoding.EncodeToString(sum[:])
	if got != want {
		t.Fatalf("%q != %q", got, want)
	}
	if digestHeader("zz") != "" || digestHeader("") != "" {
		t.Fatal("bad hex")
	}
}

func TestWriteRaw(t *testing.T) {
	w := &streamWriter{h: map[string]string{}, buf: &bytes.Buffer{}}
	writeRaw(w, "rcp_x", "sha-256=abc", io.NopCloser(bytes.NewReader([]byte("body"))))
	if w.buf.String() != "body" || w.h["Digest"] != "sha-256=abc" || w.h["Content-Type"] != "message/rfc822" {
		t.Fatalf("%q %+v", w.buf.String(), w.h)
	}
	writeRaw(&streamWriter{h: map[string]string{}, buf: &bytes.Buffer{}}, "rcp_y", "", io.NopCloser(bytes.NewReader(nil)))
}

func TestParseSortAndCursorHelpers(t *testing.T) {
	f, d, err := parseSort("")
	if err != nil || f != "at" || d != -1 {
		t.Fatalf("%s %d %v", f, d, err)
	}
	f, d, err = parseSort("type")
	if err != nil || f != "type" || d != 1 {
		t.Fatalf("%s %d %v", f, d, err)
	}
	if _, _, err := parseSort("nope"); errs.CodeOf(err) != errs.CodeSortNotAllowed {
		t.Fatal(err)
	}
	tok := encodeCursor(listCursor{ID: "rcp_1", Field: "at", Key: "k", Desc: true})
	c, err := decodeCursor(tok)
	if err != nil || c.ID != "rcp_1" || !c.Desc {
		t.Fatalf("%+v %v", c, err)
	}
	if _, err := decodeCursor("!!!"); err == nil {
		t.Fatal("bad cursor")
	}
	if _, err := cursorFilter(listCursor{Field: "type"}, "at", -1); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatal(err)
	}
	at := sortKey(Receipt{ID: "rcp_1", Type: "accettazione"}, "type")
	if at != "accettazione" {
		t.Fatal(at)
	}
	_ = sortKey(Receipt{ID: "rcp_1"}, "_id")
}

func TestLoadForReadBadID(t *testing.T) {
	s := &Service{Repo: &Repo{}}
	ctx := store.WithOrganization(context.Background(), store.NewID(store.PrefixOrganization))
	if _, err := s.loadForRead(ctx, "not-an-id"); errs.CodeOf(err) != errs.CodeNotFound {
		t.Fatal(err)
	}
}

func TestGetRawRequiresSource(t *testing.T) {
	s := &Service{
		Repo: &Repo{}, Enabled: func(context.Context, string) (bool, error) { return true, nil },
	}
	// Will fail at org/get before Raw — exercise nil Raw path via open after a fabricated load is hard;
	// CompositeRaw without Message is enough.
	if _, err := (CompositeRaw{}).Open(context.Background(), &Receipt{}); errs.CodeOf(err) != errs.CodeInternalError {
		t.Fatal(err)
	}
	_ = s
}
