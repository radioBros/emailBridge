package mime

import (
	"bytes"
	"fmt"
	"io"
	"net/mail"
	"strings"
	"time"
	"unicode"

	"github.com/emersion/go-message"
	gomail "github.com/emersion/go-message/mail"
	"github.com/oklog/ulid/v2"

	"github.com/radiobros/emailbridge/internal/errs"
)

// Build constructs a MIME message from structured fields (S6 §2).
func Build(m Message) (*Built, error) {
	return BuildWith(m, Options{})
}

// BuildWith is Build with injectable clock / boundary / entropy.
func BuildWith(m Message, opt Options) (*Built, error) {
	if err := validateCustomHeaders(m.Headers); err != nil {
		return nil, err
	}
	fromAddrs, err := parseAddrs(m.From)
	if err != nil {
		return nil, err
	}
	toAddrs, err := parseAddrs(m.To)
	if err != nil {
		return nil, err
	}
	ccAddrs, err := parseAddrs(m.Cc)
	if err != nil {
		return nil, err
	}
	bccAddrs, err := parseAddrs(m.Bcc)
	if err != nil {
		return nil, err
	}
	replyAddrs, err := parseAddrs(m.ReplyTo)
	if err != nil {
		return nil, err
	}

	recipients := make([]string, 0, len(toAddrs)+len(ccAddrs)+len(bccAddrs))
	for _, a := range append(append(toAddrs, ccAddrs...), bccAddrs...) {
		recipients = append(recipients, a.Address)
	}
	if len(recipients) == 0 {
		return nil, errs.New(errs.CodeNoRecipients, "at least one recipient is required")
	}

	var mailFrom string
	if len(fromAddrs) > 0 {
		mailFrom = fromAddrs[0].Address
	}
	if mailFrom == "" {
		return nil, errs.New(errs.CodeInvalidAddress, "From address is required")
	}

	now := m.Date
	if now.IsZero() {
		if opt.Now != nil {
			now = opt.Now()
		} else {
			now = time.Now().UTC()
		}
	}

	msgID := m.MessageID
	if msgID == "" {
		msgID, err = generateMessageID(mailFrom, opt.Entropy, now)
		if err != nil {
			return nil, err
		}
	}

	var h gomail.Header
	h.SetAddressList("From", fromAddrs)
	if len(toAddrs) > 0 {
		h.SetAddressList("To", toAddrs)
	}
	if len(ccAddrs) > 0 {
		h.SetAddressList("Cc", ccAddrs)
	}
	if len(replyAddrs) > 0 {
		h.SetAddressList("Reply-To", replyAddrs)
	}
	h.SetSubject(m.Subject)
	h.SetDate(now)
	// Message-ID kept byte-for-byte (S6 §2); do not round-trip through SetMessageID.
	h.Set("Message-Id", msgID)

	for k, v := range m.Headers {
		h.Set(k, v)
	}

	inlines, attachments := splitAttachments(m.Attachments)
	var buf bytes.Buffer
	if err := writeBody(&buf, h, m.Text, m.HTML, inlines, attachments, opt); err != nil {
		return nil, err
	}

	return &Built{
		Raw:       buf.Bytes(),
		MessageID: msgID,
		Envelope: Envelope{
			From:       mailFrom,
			Recipients: recipients,
		},
	}, nil
}

// BuildRaw prepares a raw RFC 822 message for SMTP: derive or apply envelope,
// ensure Message-ID, strip Bcc from transmitted bytes (S6 §2).
func BuildRaw(raw []byte, envelope *Envelope) (*Built, error) {
	return BuildRawWith(raw, envelope, Options{})
}

// BuildRawWith is BuildRaw with injectable entropy / clock for Message-ID generation.
func BuildRawWith(raw []byte, envelope *Envelope, opt Options) (*Built, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, errs.New(errs.CodeValidationFailed, "raw message is empty")
	}

	hdr, _, err := splitHeaderBody(raw)
	if err != nil {
		return nil, err
	}
	fields, err := parseRawHeaderFields(hdr)
	if err != nil {
		return nil, err
	}

	msgID := headerValue(fields, "message-id")
	from := headerValue(fields, "from")

	var env Envelope
	if envelope != nil {
		env = *envelope
		if env.From == "" {
			addr, aerr := firstAddress(from)
			if aerr != nil {
				return nil, aerr
			}
			env.From = addr
		}
	} else {
		addr, aerr := firstAddress(from)
		if aerr != nil {
			return nil, aerr
		}
		env.From = addr
		for _, key := range []string{"to", "cc", "bcc"} {
			v := headerValue(fields, key)
			if v == "" {
				continue
			}
			list, perr := mail.ParseAddressList(v)
			if perr != nil {
				return nil, errs.New(errs.CodeInvalidAddress, perr.Error())
			}
			for _, a := range list {
				env.Recipients = append(env.Recipients, a.Address)
			}
		}
	}
	if env.From == "" {
		return nil, errs.New(errs.CodeInvalidAddress, "From address is required")
	}
	if len(env.Recipients) == 0 {
		return nil, errs.New(errs.CodeNoRecipients, "at least one recipient is required")
	}

	out := stripBccFields(raw)
	if msgID == "" {
		now := time.Now().UTC()
		if opt.Now != nil {
			now = opt.Now()
		}
		id, gerr := generateMessageID(env.From, opt.Entropy, now)
		if gerr != nil {
			return nil, gerr
		}
		msgID = id
		out, err = insertHeader(out, "Message-ID", msgID)
		if err != nil {
			return nil, err
		}
	}

	return &Built{Raw: out, MessageID: msgID, Envelope: env,
		HeaderTo: headerAddresses(fields, "to"), HeaderCc: headerAddresses(fields, "cc")}, nil
}

// headerAddresses returns the addresses of a To / Cc header; an unparsable header yields none (with an
// explicit envelope the header is informational; without one, BuildRaw has already refused it).
func headerAddresses(fields []rawHeaderField, key string) []string {
	v := headerValue(fields, key)
	if v == "" {
		return nil
	}
	list, err := mail.ParseAddressList(v)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(list))
	for _, a := range list {
		out = append(out, a.Address)
	}
	return out
}

func validateCustomHeaders(headers map[string]string) error {
	for k, v := range headers {
		if strings.ContainsAny(k, "\r\n") || strings.ContainsAny(v, "\r\n") {
			return errs.New(errs.CodeValidationFailed, "header name or value contains CR/LF")
		}
		lk := strings.ToLower(strings.TrimSpace(k))
		switch {
		case lk == "bcc":
			return errs.New(errs.CodeValidationFailed, "custom header Bcc is refused")
		case lk == "mime-version":
			return errs.New(errs.CodeValidationFailed, "custom header MIME-Version is refused")
		case lk == "message-id":
			return errs.New(errs.CodeValidationFailed, "custom header Message-ID is refused")
		case strings.HasPrefix(lk, "content-"):
			return errs.New(errs.CodeValidationFailed, "custom Content-* headers are refused")
		}
	}
	return nil
}

func parseAddrs(in []Address) ([]*gomail.Address, error) {
	if len(in) == 0 {
		return nil, nil
	}
	out := make([]*gomail.Address, 0, len(in))
	for _, a := range in {
		addr := strings.TrimSpace(a.Address)
		if addr == "" {
			return nil, errs.New(errs.CodeInvalidAddress, "empty address")
		}
		if _, err := mail.ParseAddress(formatAddr(a.Name, addr)); err != nil {
			return nil, errs.New(errs.CodeInvalidAddress, err.Error())
		}
		out = append(out, &gomail.Address{Name: a.Name, Address: addr})
	}
	return out, nil
}

func formatAddr(name, addr string) string {
	if name == "" {
		return addr
	}
	return fmt.Sprintf("%s <%s>", name, addr)
}

func generateMessageID(fromAddr string, entropy io.Reader, at time.Time) (string, error) {
	domain := fromAddr
	if i := strings.LastIndex(fromAddr, "@"); i >= 0 {
		domain = fromAddr[i+1:]
	}
	domain = strings.Trim(domain, "<> \t")
	if domain == "" {
		return "", errs.New(errs.CodeInvalidAddress, "cannot derive Message-ID domain from From")
	}
	entropySrc := entropy
	if entropySrc == nil {
		entropySrc = ulid.DefaultEntropy()
	}
	id, err := ulid.New(ulid.Timestamp(at), entropySrc)
	if err != nil {
		return "", errs.Wrap(err, errs.CodeInternalError, "mime.generateMessageID")
	}
	return "<" + strings.ToLower(id.String()) + "@" + domain + ">", nil
}

func splitAttachments(all []Attachment) (inlines, attachments []Attachment) {
	for _, a := range all {
		d := strings.ToLower(strings.TrimSpace(a.Disposition))
		if d == "inline" && strings.TrimSpace(a.ContentID) != "" {
			inlines = append(inlines, a)
		} else {
			if d == "" {
				a.Disposition = "attachment"
			}
			attachments = append(attachments, a)
		}
	}
	return inlines, attachments
}

func closeWith(c io.Closer, err error) error {
	cerr := c.Close()
	if err != nil {
		return err
	}
	return cerr
}

func writeBody(w io.Writer, h gomail.Header, text, html string, inlines, attachments []Attachment, opt Options) error {
	hasAlt := text != "" && html != ""
	hasText := text != "" || html != ""
	hasInline := len(inlines) > 0
	hasAttach := len(attachments) > 0

	switch {
	case hasAttach:
		setMultipart(&h, "multipart/mixed", opt)
		mw, err := message.CreateWriter(w, h.Header)
		if err != nil {
			return err
		}
		err = writeRelatedOrAlt(mw, text, html, inlines, hasAlt, hasText, hasInline, opt)
		for i, a := range attachments {
			if err != nil {
				break
			}
			if aerr := writeAttachment(mw, a, false); aerr != nil {
				err = fmt.Errorf("attachment %d: %w", i, aerr)
			}
		}
		return closeWith(mw, err)
	case hasInline:
		setMultipart(&h, "multipart/related", opt)
		mw, err := message.CreateWriter(w, h.Header)
		if err != nil {
			return err
		}
		err = writeAltOrSingle(mw, text, html, hasAlt, hasText, opt)
		for i, a := range inlines {
			if err != nil {
				break
			}
			if aerr := writeAttachment(mw, a, true); aerr != nil {
				err = fmt.Errorf("inline %d: %w", i, aerr)
			}
		}
		return closeWith(mw, err)
	case hasAlt:
		setMultipart(&h, "multipart/alternative", opt)
		mw, err := message.CreateWriter(w, h.Header)
		if err != nil {
			return err
		}
		return closeWith(mw, writeTextParts(mw, text, html))
	case text != "":
		return writeSingle(w, h, "text/plain", text)
	case html != "":
		return writeSingle(w, h, "text/html", html)
	default:
		return writeSingle(w, h, "text/plain", "")
	}
}

func writeRelatedOrAlt(parent *message.Writer, text, html string, inlines []Attachment, hasAlt, hasText, hasInline bool, opt Options) error {
	if !hasInline {
		return writeAltOrSingle(parent, text, html, hasAlt, hasText, opt)
	}
	var rh message.Header
	setMultipartHeader(&rh, "multipart/related", opt)
	rw, err := parent.CreatePart(rh)
	if err != nil {
		return err
	}
	err = writeAltOrSingle(rw, text, html, hasAlt, hasText, opt)
	for i, a := range inlines {
		if err != nil {
			break
		}
		if aerr := writeAttachment(rw, a, true); aerr != nil {
			err = fmt.Errorf("inline %d: %w", i, aerr)
		}
	}
	return closeWith(rw, err)
}

func writeAltOrSingle(parent *message.Writer, text, html string, hasAlt, hasText bool, opt Options) error {
	if hasAlt {
		var ah message.Header
		setMultipartHeader(&ah, "multipart/alternative", opt)
		aw, err := parent.CreatePart(ah)
		if err != nil {
			return err
		}
		return closeWith(aw, writeTextParts(aw, text, html))
	}
	if !hasText {
		return writePart(parent, "text/plain", "quoted-printable", "", nil)
	}
	if text != "" {
		return writePart(parent, "text/plain", "quoted-printable", text, nil)
	}
	return writePart(parent, "text/html", "quoted-printable", html, nil)
}

func writeTextParts(w *message.Writer, text, html string) error {
	// Caller only invokes this for multipart/alternative (both parts present).
	if err := writePart(w, "text/plain", "quoted-printable", text, nil); err != nil {
		return err
	}
	return writePart(w, "text/html", "quoted-printable", html, nil)
}

func writeSingle(w io.Writer, h gomail.Header, ctype, body string) error {
	h.SetContentType(ctype, map[string]string{"charset": "utf-8"})
	h.Set("Content-Transfer-Encoding", "quoted-printable")
	mw, err := message.CreateWriter(w, h.Header)
	if err != nil {
		return err
	}
	_, werr := io.WriteString(mw, body)
	return closeWith(mw, werr)
}

func writePart(parent *message.Writer, ctype, cte, body string, extra map[string]string) error {
	var h message.Header
	h.SetContentType(ctype, map[string]string{"charset": "utf-8"})
	h.Set("Content-Transfer-Encoding", cte)
	for k, v := range extra {
		h.Set(k, v)
	}
	pw, err := parent.CreatePart(h)
	if err != nil {
		return err
	}
	_, werr := io.WriteString(pw, body)
	return closeWith(pw, werr)
}

func writeAttachment(parent *message.Writer, a Attachment, inline bool) error {
	ctype := a.ContentType
	if ctype == "" {
		ctype = "application/octet-stream"
	}
	var h message.Header
	params := map[string]string{}
	if a.Filename != "" {
		params["filename"] = a.Filename
		h.SetContentType(ctype, map[string]string{"name": a.Filename})
	} else {
		h.SetContentType(ctype, nil)
	}
	disp := "attachment"
	if inline {
		disp = "inline"
	} else if d := strings.ToLower(strings.TrimSpace(a.Disposition)); d != "" {
		disp = d
	}
	h.SetContentDisposition(disp, params)
	h.Set("Content-Transfer-Encoding", "base64")
	if cid := normalizeContentID(a.ContentID); cid != "" {
		h.Set("Content-Id", cid)
	}
	pw, err := parent.CreatePart(h)
	if err != nil {
		return err
	}
	_, werr := pw.Write(a.Content)
	return closeWith(pw, werr)
}

func normalizeContentID(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return ""
	}
	if strings.HasPrefix(id, "<") && strings.HasSuffix(id, ">") {
		return id
	}
	return "<" + id + ">"
}

func setMultipart(h *gomail.Header, mediaType string, opt Options) {
	params := map[string]string{}
	if opt.Boundary != nil {
		params["boundary"] = opt.Boundary()
	}
	h.SetContentType(mediaType, params)
}

func setMultipartHeader(h *message.Header, mediaType string, opt Options) {
	params := map[string]string{}
	if opt.Boundary != nil {
		params["boundary"] = opt.Boundary()
	}
	h.SetContentType(mediaType, params)
}

func firstAddress(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errs.New(errs.CodeInvalidAddress, "From address is required")
	}
	list, err := mail.ParseAddressList(raw)
	if err != nil {
		return "", errs.New(errs.CodeInvalidAddress, err.Error())
	}
	if len(list) == 0 || list[0].Address == "" {
		return "", errs.New(errs.CodeInvalidAddress, "From address is required")
	}
	return list[0].Address, nil
}

// rawHeaderField is one logical header field (name + raw value bytes including folded lines).
type rawHeaderField struct {
	name  string // lowercased
	raw   []byte // full "Name: value\r\n" including continuations
	value string // unfolded value
}

func splitHeaderBody(raw []byte) (hdr, body []byte, err error) {
	// Prefer CRLF blank line, then LF.
	if i := bytes.Index(raw, []byte("\r\n\r\n")); i >= 0 {
		return raw[:i+4], raw[i+4:], nil
	}
	if i := bytes.Index(raw, []byte("\n\n")); i >= 0 {
		return raw[:i+2], raw[i+2:], nil
	}
	// Headers only, no body.
	return raw, nil, nil
}

func parseRawHeaderFields(hdr []byte) ([]rawHeaderField, error) {
	// Normalize to \n for scanning; keep original slices from hdr for strip.
	var fields []rawHeaderField
	lines := splitHeaderLines(hdr)
	i := 0
	for i < len(lines) {
		line := lines[i]
		if len(bytes.TrimSpace(line.raw)) == 0 {
			i++
			continue
		}
		// Continuation without a start — skip.
		if len(line.raw) > 0 && (line.raw[0] == ' ' || line.raw[0] == '\t') {
			i++
			continue
		}
		colon := bytes.IndexByte(line.raw, ':')
		if colon < 0 {
			return nil, errs.New(errs.CodeValidationFailed, "malformed header line")
		}
		name := string(bytes.TrimRightFunc(line.raw[:colon], unicode.IsSpace))
		var rawBuf bytes.Buffer
		rawBuf.Write(line.raw)
		val := string(bytes.TrimSpace(line.raw[colon+1:]))
		i++
		for i < len(lines) {
			cont := lines[i]
			if len(cont.raw) == 0 || (cont.raw[0] != ' ' && cont.raw[0] != '\t') {
				break
			}
			rawBuf.Write(cont.raw)
			val += " " + string(bytes.TrimSpace(cont.raw))
			i++
		}
		fields = append(fields, rawHeaderField{
			name:  strings.ToLower(name),
			raw:   rawBuf.Bytes(),
			value: val,
		})
	}
	return fields, nil
}

type headerLine struct {
	raw []byte // includes trailing \r\n or \n
}

func splitHeaderLines(hdr []byte) []headerLine {
	var lines []headerLine
	start := 0
	for i := 0; i < len(hdr); {
		if hdr[i] == '\n' {
			end := i + 1
			lines = append(lines, headerLine{raw: hdr[start:end]})
			start = end
			i = end
			continue
		}
		i++
	}
	if start < len(hdr) {
		lines = append(lines, headerLine{raw: hdr[start:]})
	}
	return lines
}

func headerValue(fields []rawHeaderField, name string) string {
	name = strings.ToLower(name)
	for _, f := range fields {
		if f.name == name {
			return f.value
		}
	}
	return ""
}

func stripBccFields(raw []byte) []byte {
	hdr, body, err := splitHeaderBody(raw)
	if err != nil || len(hdr) == 0 {
		return raw
	}
	fields, err := parseRawHeaderFields(hdr)
	if err != nil {
		return raw
	}
	var out bytes.Buffer
	for _, f := range fields {
		if f.name == "bcc" {
			continue
		}
		out.Write(f.raw)
	}
	// Preserve the blank line separator style from original.
	if bytes.HasSuffix(hdr, []byte("\r\n\r\n")) {
		out.WriteString("\r\n")
	} else if bytes.HasSuffix(hdr, []byte("\n\n")) {
		out.WriteByte('\n')
	} else if !bytes.HasSuffix(out.Bytes(), []byte("\n")) {
		out.WriteString("\r\n")
	}
	out.Write(body)
	return out.Bytes()
}

func insertHeader(raw []byte, name, value string) ([]byte, error) {
	hdr, body, err := splitHeaderBody(raw)
	if err != nil {
		return nil, err
	}
	line := name + ": " + value + "\r\n"
	var out bytes.Buffer
	// Insert before the blank line at end of headers.
	trim := hdr
	if bytes.HasSuffix(trim, []byte("\r\n\r\n")) {
		trim = trim[:len(trim)-2] // keep one \r\n ending last header; we'll add field + \r\n
		out.Write(trim)
		out.WriteString(line)
		out.WriteString("\r\n")
	} else if bytes.HasSuffix(trim, []byte("\n\n")) {
		trim = trim[:len(trim)-1]
		out.Write(trim)
		out.WriteString(line)
		out.WriteByte('\n')
	} else {
		out.Write(hdr)
		if !bytes.HasSuffix(hdr, []byte("\n")) {
			out.WriteString("\r\n")
		}
		out.WriteString(line)
		out.WriteString("\r\n")
	}
	out.Write(body)
	return out.Bytes(), nil
}
