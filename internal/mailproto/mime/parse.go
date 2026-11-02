package mime

import (
	"bytes"
	"errors"
	"io"
	"mime"
	"slices"
	"strconv"
	"strings"

	"github.com/emersion/go-message"
	_ "github.com/emersion/go-message/charset" // charset → UTF-8 registration (S5 §4.1)
	gomail "github.com/emersion/go-message/mail"

	"github.com/radiobros/emailbridge/internal/errs"
)

// ParseHeaders returns lowercased header names → values (S3 §3.5).
func ParseHeaders(raw []byte) (Header, error) {
	hdr, _, err := splitHeaderBody(raw)
	if err != nil {
		return nil, err
	}
	fields, err := parseRawHeaderFields(hdr)
	if err != nil {
		return nil, err
	}
	out := make(Header, len(fields))
	for _, f := range fields {
		out[f.name] = append(out[f.name], f.value)
	}
	return out, nil
}

// DecodeSubject returns the RFC 2047-decoded Subject, or "" if absent.
func DecodeSubject(h Header) (string, error) {
	vals := h["subject"]
	if len(vals) == 0 {
		return "", nil
	}
	dec := new(mime.WordDecoder)
	s, err := dec.DecodeHeader(vals[0])
	if err != nil {
		return vals[0], err
	}
	return s, nil
}

// DecodeAddresses returns RFC 2047-decoded addresses for a header key
// (from, to, cc, bcc, reply-to, sender).
func DecodeAddresses(h Header, key string) ([]Address, error) {
	vals := h[strings.ToLower(key)]
	if len(vals) == 0 {
		return nil, nil
	}
	var all []Address
	for _, v := range vals {
		list, err := gomail.ParseAddressList(v)
		if err != nil {
			return nil, errs.New(errs.CodeInvalidAddress, err.Error())
		}
		for _, a := range list {
			all = append(all, Address{Name: a.Name, Address: a.Address})
		}
	}
	return all, nil
}

// MessageIDHeader returns the Message-ID header value unchanged (byte-for-byte string).
func MessageIDHeader(h Header) string {
	vals := h["message-id"]
	if len(vals) == 0 {
		return ""
	}
	return vals[0]
}

// ParseEntity reads a full message into a go-message Entity.
func ParseEntity(raw []byte) (*message.Entity, error) {
	e, err := message.Read(bytes.NewReader(raw))
	if err != nil && !message.IsUnknownCharset(err) {
		return nil, err
	}
	return e, nil
}

// TextParts extracts text/plain and text/html bodies as UTF-8, capped at max
// bytes (DefaultTextMax when max <= 0). truncated is true when either part was capped.
func TextParts(e *message.Entity, max int) (plain, html string, truncated bool, err error) {
	if e == nil {
		return "", "", false, nil
	}
	if max <= 0 {
		max = DefaultTextMax
	}
	var plainB, htmlB strings.Builder
	var plainTrunc, htmlTrunc bool
	err = walkParts(e, "", func(partID string, p *message.Entity) error {
		ctype, _, _ := p.Header.ContentType()
		ctype = strings.ToLower(ctype)
		disp, _, _ := p.Header.ContentDisposition()
		if strings.EqualFold(disp, "attachment") {
			return nil
		}
		switch ctype {
		case "text/plain":
			if plainB.Len() > 0 {
				return nil
			}
			s, trunc, rerr := readCapped(p.Body, max)
			if rerr != nil {
				return rerr
			}
			plainB.WriteString(s)
			plainTrunc = trunc
		case "text/html":
			if htmlB.Len() > 0 {
				return nil
			}
			s, trunc, rerr := readCapped(p.Body, max)
			if rerr != nil {
				return rerr
			}
			htmlB.WriteString(s)
			htmlTrunc = trunc
		}
		_ = partID
		return nil
	})
	return plainB.String(), htmlB.String(), plainTrunc || htmlTrunc, err
}

// AttachmentsMeta returns metadata for non-multipart leaf parts that are
// attachments or have a Content-ID / filename (S3 §3.5).
func AttachmentsMeta(e *message.Entity) ([]AttachmentMeta, error) {
	if e == nil {
		return nil, nil
	}
	var out []AttachmentMeta
	err := walkParts(e, "", func(partID string, p *message.Entity) error {
		ctype, _, _ := p.Header.ContentType()
		if strings.HasPrefix(strings.ToLower(ctype), "multipart/") {
			return nil
		}
		disp, params, _ := p.Header.ContentDisposition()
		cid := strings.TrimSpace(p.Header.Get("Content-Id"))
		filename := params["filename"]
		if filename == "" {
			_, ctParams, _ := p.Header.ContentType()
			filename = ctParams["name"]
		}
		isAttach := strings.EqualFold(disp, "attachment")
		isInline := strings.EqualFold(disp, "inline") && cid != ""
		// Skip the primary text bodies unless disposition says attachment.
		cl := strings.ToLower(ctype)
		if (cl == "text/plain" || cl == "text/html") && !isAttach {
			return nil
		}
		if !isAttach && !isInline && filename == "" && cid == "" {
			return nil
		}
		size, _ := io.Copy(io.Discard, p.Body)
		if disp == "" && cid != "" {
			disp = "inline"
		}
		if disp == "" {
			disp = "attachment"
		}
		out = append(out, AttachmentMeta{
			Filename:    filename,
			ContentType: ctype,
			Size:        size,
			ContentID:   strings.Trim(cid, "<>"),
			Disposition: strings.ToLower(disp),
			PartID:      partID,
		})
		return nil
	})
	return out, err
}

func readCapped(r io.Reader, max int) (string, bool, error) {
	limited := io.LimitReader(r, int64(max)+1)
	b, err := io.ReadAll(limited)
	if err != nil {
		return "", false, err
	}
	trunc := len(b) > max
	if trunc {
		b = b[:max]
	}
	return string(b), trunc, nil
}

// MaxPartDepth caps multipart nesting. Every level holds an open part reader, so hostile mail with
// thousands of nested boundaries would otherwise cost unbounded memory and stack.
const MaxPartDepth = 32

func walkParts(e *message.Entity, parentID string, fn func(partID string, p *message.Entity) error) error {
	return walkPartsDepth(e, parentID, 0, fn)
}

func walkPartsDepth(e *message.Entity, parentID string, depth int, fn func(partID string, p *message.Entity) error) error {
	if depth > MaxPartDepth {
		return errs.New(errs.CodeValidationFailed, "MIME nesting deeper than "+strconv.Itoa(MaxPartDepth)+" levels")
	}
	mr := e.MultipartReader()
	if mr == nil {
		id := parentID
		if id == "" {
			id = "1"
		}
		return fn(id, e)
	}
	i := 0
	for {
		p, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil && !message.IsUnknownCharset(err) {
			return err
		}
		i++
		id := strconv.Itoa(i)
		if parentID != "" {
			id = parentID + "." + id
		}
		if p.MultipartReader() != nil {
			if err := walkPartsDepth(p, id, depth+1, fn); err != nil {
				return err
			}
			continue
		}
		if err := fn(id, p); err != nil {
			return err
		}
	}
}

// PartsOfType parses raw and returns the decoded bodies of every part whose content type is one of ctypes
// (lower-case), attachments included, each capped at maxPart bytes (DefaultTextMax when <= 0). It parses raw
// itself because reading a part consumes it (TextParts on the same entity would leave nothing).
func PartsOfType(raw []byte, maxPart int, ctypes ...string) ([][]byte, error) {
	e, err := ParseEntity(raw)
	if err != nil {
		return nil, err
	}
	if maxPart <= 0 {
		maxPart = DefaultTextMax
	}
	var out [][]byte
	err = walkParts(e, "", func(_ string, p *message.Entity) error {
		ctype, _, _ := p.Header.ContentType()
		if !slices.Contains(ctypes, strings.ToLower(ctype)) {
			return nil
		}
		s, _, rerr := readCapped(p.Body, maxPart)
		if rerr != nil {
			return rerr
		}
		out = append(out, []byte(s))
		return nil
	})
	return out, err
}
