package attachments

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"strconv"
	"strings"

	"github.com/emersion/go-message"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/mailproto/mime"
)

// extracted is one decoded MIME part body plus metadata.
type extracted struct {
	PartID      string
	Filename    string
	ContentType string
	ContentID   string
	Disposition string
	Body        []byte
	ETag        string
}

func listParts(raw []byte) ([]extracted, error) {
	e, err := mime.ParseEntity(raw)
	if err != nil {
		return nil, err
	}
	var out []extracted
	err = walkExtract(e, "", 0, func(partID string, p *message.Entity) error {
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
		cl := strings.ToLower(ctype)
		if (cl == "text/plain" || cl == "text/html") && !isAttach {
			return nil
		}
		if !isAttach && !isInline && filename == "" && cid == "" {
			return nil
		}
		body, err := io.ReadAll(p.Body)
		if err != nil {
			return err
		}
		if disp == "" && cid != "" {
			disp = "inline"
		}
		if disp == "" {
			disp = "attachment"
		}
		sum := sha256.Sum256(body)
		out = append(out, extracted{
			PartID: partID, Filename: filename, ContentType: ctype,
			ContentID: strings.Trim(cid, "<>"), Disposition: strings.ToLower(disp),
			Body: body, ETag: hex.EncodeToString(sum[:]),
		})
		return nil
	})
	return out, err
}

func extractPart(raw []byte, partID string) (*extracted, error) {
	parts, err := listParts(raw)
	if err != nil {
		return nil, err
	}
	for i := range parts {
		if parts[i].PartID == partID {
			return &parts[i], nil
		}
	}
	return nil, errs.New(errs.CodeAttachmentNotFound, "The attachment does not exist.")
}

func walkExtract(e *message.Entity, parentID string, depth int, fn func(partID string, p *message.Entity) error) error {
	const maxDepth = 32
	if depth > maxDepth {
		return errs.New(errs.CodeValidationFailed, "MIME nesting deeper than "+strconv.Itoa(maxDepth)+" levels")
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
			if err := walkExtract(p, id, depth+1, fn); err != nil {
				return err
			}
			continue
		}
		if err := fn(id, p); err != nil {
			return err
		}
	}
}

func rawFromReader(r io.ReadCloser) ([]byte, error) {
	defer func() { _ = r.Close() }()
	return io.ReadAll(r)
}

func bytesReader(b []byte) io.ReadCloser {
	return io.NopCloser(bytes.NewReader(b))
}
