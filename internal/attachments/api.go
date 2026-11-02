package attachments

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

// Register mounts the authenticated attachment route and the public /a/{token} handler.
func (s *Service) Register(srv *httpapi.Server) {
	ext := httpapi.Phase("P4")
	httpapi.Register(srv, huma.Operation{
		OperationID: "getAccountMessageAttachment", Method: http.MethodGet,
		Path:    "/v1/accounts/{accountId}/messages/{messageId}/attachments/{attachmentId}",
		Summary: "Download a message attachment",
		Description: "Streams the decoded attachment bytes with its `Content-Type`, `Content-Disposition` (file name), " +
			"`ETag` and the virus-scan verdict in `EB-Scan`. The bytes come from S3 when `attachments.s3` stored the " +
			"object, else from the account's mail server or the raw archive. Infected attachments are still streamed " +
			"here (so admins can inspect them, `EB-Scan: infected`). With `url=signed` the answer is JSON " +
			"`{ url, expiresAt }` instead: a public `/a/{token}` link valid for `attachments.signedUrlTtl` (default 15 " +
			"minutes, at most 24 hours) — only for S3-stored, not infected attachments (`403 feature_disabled` / `403 " +
			"attachment_infected` otherwise). Requires the `read` permission (every role; API keys `messages:read`, an " +
			"account-bound key only for its account). Read-only.",
		Metadata: httpapi.ProblemDocs(
			httpapi.ProblemDoc{Code: errs.CodeUnauthenticated}, httpapi.ProblemDoc{Code: errs.CodeForbidden},
			httpapi.ProblemDoc{Code: errs.CodeScopeMissing, Detail: "The API key lacks the messages:read scope, or is bound to another account."},
			httpapi.ProblemDoc{Code: errs.CodeFeatureDisabled, Detail: "Attachment S3 storage is not enabled for this attachment."},
			httpapi.ProblemDoc{Code: errs.CodeAttachmentInfected, Detail: "The attachment failed the virus scan."},
			httpapi.ProblemDoc{Code: errs.CodeAttachmentNotFound, Detail: "The attachment does not exist."},
			httpapi.ProblemDoc{Code: errs.CodeMessageNotFound, Detail: "The message is no longer on the server."},
			httpapi.ProblemDoc{Code: errs.CodeConnectionBusy}, httpapi.ProblemDoc{Code: errs.CodeRateLimited}),
		Tags:       []string{"accounts"},
		Extensions: httpapi.WithScope(ext, "messages:read"),
		Responses: map[string]*huma.Response{
			"200": {Description: "Attachment bytes or signed URL",
				Headers: streamHeaders(),
				Content: map[string]*huma.MediaType{
					"application/octet-stream": {
						Schema:  &huma.Schema{Type: huma.TypeString, Format: "binary", Description: "The decoded attachment; the real Content-Type is the part's MIME type (application/pdf, image/png …)."},
						Example: examplePDFBytes,
					},
					"application/json": {
						Schema:  srv.API.OpenAPI().Components.Schemas.Schema(reflect.TypeFor[SignedAttachmentURL](), true, ""),
						Example: map[string]any{"url": exampleSignedURL, "expiresAt": "2026-10-27T11:00:00Z"},
					},
				}},
		},
	}, s.getAttachment)

	srv.Router.Get("/a/{token}", s.serveSigned)
	srv.Document(&huma.Operation{
		OperationID: "getSignedAttachment", Method: http.MethodGet, Path: "/a/{token}",
		Summary: "Download an attachment by signed URL", Tags: []string{"accounts"},
		Description: "Public download behind the `url` returned by `GET …/attachments/{attachmentId}?url=signed`; the HMAC " +
			"token is the credential (bound to the organisation and the attachment, expires after " +
			"`attachments.signedUrlTtl`). Streams the bytes with the same headers as the authenticated download. A " +
			"tampered token is `403 signed_url_invalid`, an expired one `403 signed_url_expired`, an infected attachment " +
			"`403 attachment_infected`. Read-only; no cookie or API key needed.",
		Parameters: []*huma.Param{{
			Name: "token", In: "path", Required: true,
			Description: "Signed token from the url field (base64url payload, a dot, base64url HMAC-SHA256).",
			Schema:      &huma.Schema{Type: huma.TypeString}, Example: exampleSignedToken,
		}},
		Security: []map[string][]string{},
		Responses: map[string]*huma.Response{"200": {
			Description: "The attachment bytes.", Headers: streamHeaders(),
			Content: map[string]*huma.MediaType{"application/octet-stream": {
				Schema:  &huma.Schema{Type: huma.TypeString, Format: "binary", Description: "The decoded attachment; the real Content-Type is the part's MIME type."},
				Example: examplePDFBytes,
			}},
		}},
		Metadata: httpapi.ProblemDocs(
			httpapi.ProblemDoc{Code: errs.CodeSignedURLInvalid, Detail: "The signed attachment URL is invalid."},
			httpapi.ProblemDoc{Code: errs.CodeSignedURLExpired, Detail: "The signed attachment URL has expired."},
			httpapi.ProblemDoc{Code: errs.CodeAttachmentInfected, Detail: "The attachment failed the virus scan."},
			httpapi.ProblemDoc{Code: errs.CodeAttachmentNotFound, Detail: "The attachment does not exist."}),
		Extensions: httpapi.Phase("P4"),
	})
}

// Documentation examples (W70).
const (
	exampleSignedToken = "djF8MTc5MzA5ODgwMHxvcmdfMDFtNWNhcXBrMGthc3Q2M3ljMTN3cHkyZDl8YXR0XzAxbTVjbmFlZTBwM2FxajczZDNuNmVqMzhw.xTuEv7r9QFvEX0W1pZk8LUgxevLClt4aZPXrV6cI11Q" //nolint:gosec // G101: documentation example signed URL token, not a real secret.
	exampleSignedURL   = "https://emailbridge.example.com/a/" + exampleSignedToken
	examplePDFBytes    = "%PDF-1.7\n1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n…"
)

// streamHeaders documents the headers of an attachment stream (setDocHeaders).
func streamHeaders() map[string]*huma.Param {
	str := func() *huma.Schema { return &huma.Schema{Type: huma.TypeString} }
	return map[string]*huma.Param{
		"Content-Disposition": {Description: "attachment (or inline) with the ASCII file name, plus filename* (RFC 5987) for non-ASCII names.",
			Schema: str(), Example: `attachment; filename="invoice-2026-1043.pdf"`},
		"ETag": {Description: "SHA-256 of the decoded bytes, when known.", Schema: str(),
			Example: `"9b2f5c1d7e3a4086b1c2d3e4f5a6b7c8d9e0f1a2b3c4d5e6f7a8b9c0d1e2f3a4"`},
		"EB-Scan": {Description: "Virus-scan verdict when known: clean, infected, error or skipped.", Schema: str(), Example: "clean"},
	}
}

type getAttIn struct {
	AccountID    string `path:"accountId" pattern:"^acc_[0-9a-z]{26}$" doc:"Account ID (acc_…)." example:"acc_01m5cvnpb031g57sqct6mzhq94"`
	MessageID    string `path:"messageId" pattern:"^msg_[0-9a-z]{26}$" doc:"Message ID (msg_…)." example:"msg_01m5ck6pf04wm1cyx6q10s4x5w"`
	AttachmentID string `path:"attachmentId" pattern:"^att_[0-9a-z]{26}$" doc:"Attachment ID (att_…) from the message's attachments list." example:"att_01m5cnaee0p3aqj73d3n6ej38p"`
	URL          string `query:"url" enum:"signed" doc:"signed: return a short-lived public download URL as JSON instead of the bytes." example:"signed"`
}

func (s *Service) getAttachment(ctx context.Context, in *getAttIn) (*huma.StreamResponse, error) {
	if err := tenancy.RequireReadAccount(ctx, "messages", in.AccountID); err != nil {
		return nil, err
	}
	if strings.EqualFold(in.URL, "signed") {
		body, err := s.SignedURL(ctx, in.AccountID, in.MessageID, in.AttachmentID)
		if err != nil {
			return nil, err
		}
		payload, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		return &huma.StreamResponse{Body: func(hctx huma.Context) {
			hctx.SetHeader("Content-Type", "application/json")
			hctx.SetStatus(http.StatusOK)
			_, _ = hctx.BodyWriter().Write(payload)
		}}, nil
	}
	d, rc, err := s.Open(ctx, in.AccountID, in.MessageID, in.AttachmentID)
	if err != nil {
		return nil, err
	}
	return streamDoc(d, rc), nil
}

func (s *Service) serveSigned(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	d, rc, err := s.OpenSigned(r.Context(), token)
	if err != nil {
		httpapi.WriteProblem(w, r, err)
		return
	}
	defer func() { _ = rc.Close() }()
	writeHeaders(w, d)
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, rc)
}

func streamDoc(d *Doc, rc io.ReadCloser) *huma.StreamResponse {
	return &huma.StreamResponse{Body: func(hctx huma.Context) {
		defer func() { _ = rc.Close() }()
		setDocHeaders(hctx.SetHeader, d)
		hctx.SetStatus(http.StatusOK)
		_, _ = io.Copy(hctx.BodyWriter(), rc)
	}}
}

func writeHeaders(w http.ResponseWriter, d *Doc) {
	setDocHeaders(w.Header().Set, d)
}

func setDocHeaders(set func(k, v string), d *Doc) {
	ct := d.ContentType
	if ct == "" {
		ct = "application/octet-stream"
	}
	set("Content-Type", ct)
	set("Content-Disposition", contentDisposition(d.Disposition, d.Filename))
	if d.ETag != "" {
		set("ETag", `"`+d.ETag+`"`)
	}
	if d.Scan != "" {
		set("EB-Scan", d.Scan)
	}
}
