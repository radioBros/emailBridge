package pec

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"errors"
	"io"
	"mime"
	"mime/quotedprintable"
	"net/textproto"
	"strings"
)

// entity is one MIME entity: its header, its raw body (still transfer-encoded) and its exact source bytes.
type entity struct {
	header textproto.MIMEHeader
	body   []byte
	raw    []byte
}

const maxDepth = 8

func parseEntity(raw []byte) (*entity, error) {
	r := textproto.NewReader(bufio.NewReader(bytes.NewReader(raw)))
	h, err := r.ReadMIMEHeader()
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	_, body := splitHeaderBody(raw)
	return &entity{header: h, body: body, raw: raw}, nil
}

// splitHeaderBody returns the header block and the body of raw (empty line separator, CRLF or LF).
func splitHeaderBody(raw []byte) (head, body []byte) {
	if i := bytes.Index(raw, []byte("\r\n\r\n")); i >= 0 {
		j := bytes.Index(raw, []byte("\n\n"))
		if j < 0 || i < j {
			return raw[:i+2], raw[i+4:]
		}
	}
	if j := bytes.Index(raw, []byte("\n\n")); j >= 0 {
		return raw[:j+1], raw[j+2:]
	}
	return raw, nil
}

func (e *entity) mediaType() (string, map[string]string) {
	ct := e.header.Get("Content-Type")
	if ct == "" {
		return "text/plain", nil
	}
	mt, params, err := mime.ParseMediaType(ct)
	if err != nil {
		return strings.ToLower(strings.TrimSpace(strings.SplitN(ct, ";", 2)[0])), params
	}
	return mt, params
}

func (e *entity) filename() string {
	if cd := e.header.Get("Content-Disposition"); cd != "" {
		if _, p, err := mime.ParseMediaType(cd); err == nil && p["filename"] != "" {
			return strings.ToLower(p["filename"])
		}
	}
	_, p := e.mediaType()
	return strings.ToLower(p["name"])
}

// decoded returns the body with its Content-Transfer-Encoding removed.
func (e *entity) decoded() ([]byte, error) {
	switch strings.ToLower(strings.TrimSpace(e.header.Get("Content-Transfer-Encoding"))) {
	case "base64":
		clean := bytes.Map(func(r rune) rune {
			if r == '\r' || r == '\n' || r == ' ' || r == '\t' {
				return -1
			}
			return r
		}, e.body)
		out := make([]byte, base64.StdEncoding.DecodedLen(len(clean)))
		n, err := base64.StdEncoding.Decode(out, clean)
		if err != nil {
			// Some providers omit padding: decode without it, into a buffer sized for unpadded input.
			raw := bytes.TrimRight(clean, "=")
			alt := make([]byte, base64.RawStdEncoding.DecodedLen(len(raw)))
			if n2, err2 := base64.RawStdEncoding.Decode(alt, raw); err2 == nil {
				return alt[:n2], nil
			}
			return nil, err
		}
		return out[:n], nil
	case "quoted-printable":
		return io.ReadAll(quotedprintable.NewReader(bytes.NewReader(e.body)))
	}
	return e.body, nil
}

// parts splits a multipart body into its parts' exact source bytes (header + body, without the CRLF that
// belongs to the next delimiter, as RFC 2046 defines).
func parts(body []byte, boundary string) [][]byte {
	if boundary == "" {
		return nil
	}
	delim := []byte("--" + boundary)
	var out [][]byte
	pos := 0
	start := -1
	for {
		i := indexDelimiter(body[pos:], delim)
		if i < 0 {
			break
		}
		at := pos + i
		if start >= 0 {
			end := at
			// the line break before the delimiter belongs to the delimiter
			if end >= 2 && body[end-2] == '\r' && body[end-1] == '\n' {
				end -= 2
			} else if end >= 1 && body[end-1] == '\n' {
				end--
			}
			if end >= start {
				out = append(out, body[start:end])
			}
		}
		after := at + len(delim)
		if bytes.HasPrefix(body[after:], []byte("--")) {
			break // closing delimiter
		}
		// skip transport padding and the line break after the delimiter
		nl := bytes.IndexByte(body[after:], '\n')
		if nl < 0 {
			break
		}
		start = after + nl + 1
		pos = start
	}
	return out
}

// indexDelimiter finds delim at the start of a line.
func indexDelimiter(b, delim []byte) int {
	off := 0
	for {
		i := bytes.Index(b[off:], delim)
		if i < 0 {
			return -1
		}
		at := off + i
		if at == 0 || b[at-1] == '\n' {
			return at
		}
		off = at + 1
	}
}

// walk visits e and every nested entity (multiparts, and message/rfc822 when descend says so) depth-first.
func walk(e *entity, depth int, visit func(*entity) bool) {
	if depth > maxDepth || !visit(e) {
		return
	}
	mt, params := e.mediaType()
	if strings.HasPrefix(mt, "multipart/") {
		for _, p := range parts(e.body, params["boundary"]) {
			if child, err := parseEntity(p); err == nil {
				walk(child, depth+1, visit)
			}
		}
	}
}

// toCRLF normalises bare LF line endings to CRLF (S/MIME signs the canonical form).
func toCRLF(b []byte) []byte {
	if !bytes.Contains(b, []byte("\n")) {
		return b
	}
	var out bytes.Buffer
	out.Grow(len(b) + len(b)/40)
	for i := 0; i < len(b); i++ {
		if b[i] == '\n' && (i == 0 || b[i-1] != '\r') {
			out.WriteByte('\r')
		}
		out.WriteByte(b[i])
	}
	return out.Bytes()
}
