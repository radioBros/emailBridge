package archive

import (
	"context"
	"fmt"
	"io"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

// Register adds getAccountMessageRaw and getAccountArchiveMessage.
//
// Note (P1): paths that do not end in /stream, /export or /download keep the normal request
// timeout — large messages may need EB-Timeout raised until a streaming path is added.
func (s *Service) Register(srv *httpapi.Server) {
	ext := httpapi.Phase("P1")
	problems := func(extra ...httpapi.ProblemDoc) map[string]any {
		return httpapi.ProblemDocs(append([]httpapi.ProblemDoc{
			{Code: errs.CodeUnauthenticated}, {Code: errs.CodeForbidden},
			{Code: errs.CodeScopeMissing, Detail: "The API key lacks the messages:read scope, or is bound to another account."},
		}, extra...)...)
	}
	emlHeaders := func(withSource bool) map[string]*huma.Param {
		h := map[string]*huma.Param{
			"Content-Disposition": {Description: "Download as <messageId>.eml.", Schema: &huma.Schema{Type: huma.TypeString},
				Example: `attachment; filename="msg_01m5ck6pf04wm1cyx6q10s4x5w.eml"`},
		}
		if withSource {
			h["EB-Source"] = &huma.Param{Description: "Where the bytes came from: mailbox (the mail server) or archive (the raw archive copy).",
				Schema: &huma.Schema{Type: huma.TypeString, Enum: []any{SourceMailbox, SourceArchive}}, Example: SourceMailbox}
		}
		return h
	}
	httpapi.Register(srv, huma.Operation{
		OperationID: "getAccountMessageRaw", Method: http.MethodGet,
		Path:    "/v1/accounts/{accountId}/messages/{messageId}/raw",
		Summary: "Raw RFC 822 from mailbox",
		Description: "Downloads the complete original message (`message/rfc822`, as `<messageId>.eml`). It is fetched from " +
			"the mail server; when the message is gone there (or the server cannot be reached) and the raw archive " +
			"(switch `inbound.archiveRaw`) holds a copy, that copy is served instead — the `EB-Source` header says which (`mailbox` " +
			"or `archive`). Large messages may need a higher `EB-Timeout`. Requires the `read` permission (every role; " +
			"API keys `messages:read`, an account-bound key only for its account). Read-only.",
		Metadata: problems(
			httpapi.ProblemDoc{Code: errs.CodeMessageNotFound, Detail: "The message is gone from the mailbox and was not archived."},
			httpapi.ProblemDoc{Code: errs.CodeArchiveNotFound, Detail: "Archive not found."}),
		Tags:       []string{"accounts"},
		Extensions: httpapi.WithScope(ext, "messages:read"),
		Responses: map[string]*huma.Response{"200": {Description: "Raw message", Headers: emlHeaders(true),
			Content: map[string]*huma.MediaType{"message/rfc822": {
				Schema:  &huma.Schema{Type: huma.TypeString, Format: "binary", Description: "The message exactly as stored on the server (CRLF line ends)."},
				Example: exampleEML,
			}}}},
	}, s.getRaw)
	httpapi.Register(srv, huma.Operation{
		OperationID: "getAccountArchiveMessage", Method: http.MethodGet,
		Path:    "/v1/accounts/{accountId}/archive/{messageId}",
		Summary: "Raw source from archive only",
		Description: "Downloads the raw archive copy of the message (`message/rfc822`) without contacting the mail " +
			"server — also for messages deleted on the server while `inbound.archiveRaw` was on. A message never archived " +
			"(switch off, or outside retention) is `404 archive_not_found`. Requires the `read` permission (every role; " +
			"API keys `messages:read`, an account-bound key only for its account). Read-only.",
		Metadata: problems(
			httpapi.ProblemDoc{Code: errs.CodeMessageNotFound, Detail: "The message does not exist."},
			httpapi.ProblemDoc{Code: errs.CodeArchiveNotFound, Detail: "Archive not found."}),
		Tags:       []string{"accounts"},
		Extensions: httpapi.WithScope(ext, "messages:read"),
		Responses: map[string]*huma.Response{"200": {Description: "Archived raw message", Headers: emlHeaders(false),
			Content: map[string]*huma.MediaType{"message/rfc822": {
				Schema:  &huma.Schema{Type: huma.TypeString, Format: "binary", Description: "The archived message exactly as received."},
				Example: exampleEML,
			}}}},
	}, s.getArchive)
}

// exampleEML is the documentation example of a raw message (W70).
const exampleEML = "Return-Path: <billing@example.com>\r\nFrom: Billing <billing@example.com>\r\n" +
	"To: Anna Rossi <anna.rossi@example.org>\r\nSubject: Invoice 2026-1043\r\n" +
	"Date: Tue, 27 Oct 2026 09:55:12 +0000\r\nMessage-ID: <20261027095512.4f2a@example.com>\r\n" +
	"MIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nPlease find the invoice attached.\r\n"

type rawIn struct {
	AccountID string `path:"accountId" pattern:"^acc_[0-9a-z]{26}$" doc:"Account ID (acc_…)." example:"acc_01m5cvnpb031g57sqct6mzhq94"`
	MessageID string `path:"messageId" pattern:"^msg_[0-9a-z]{26}$" doc:"Message ID (msg_…)." example:"msg_01m5ck6pf04wm1cyx6q10s4x5w"`
}

func (s *Service) getRaw(ctx context.Context, in *rawIn) (*huma.StreamResponse, error) {
	if err := tenancy.RequireReadAccount(ctx, "messages", in.AccountID); err != nil {
		return nil, err
	}
	res, err := s.resolveRaw(ctx, in.AccountID, in.MessageID)
	if err != nil {
		return nil, err
	}
	return streamEML(in.MessageID, res, true), nil
}

func (s *Service) getArchive(ctx context.Context, in *rawIn) (*huma.StreamResponse, error) {
	if err := tenancy.RequireReadAccount(ctx, "messages", in.AccountID); err != nil {
		return nil, err
	}
	res, err := s.resolveArchive(ctx, in.AccountID, in.MessageID)
	if err != nil {
		return nil, err
	}
	return streamEML(in.MessageID, res, false), nil
}

func streamEML(messageID string, res *rawResult, ebSource bool) *huma.StreamResponse {
	return &huma.StreamResponse{Body: func(hctx huma.Context) {
		hctx.SetHeader("Content-Type", "message/rfc822")
		hctx.SetHeader("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.eml"`, messageID))
		if ebSource && res.Source != "" {
			hctx.SetHeader("EB-Source", res.Source)
		}
		w := hctx.BodyWriter()
		if res.Reader != nil {
			defer func() { _ = res.Reader.Close() }()
			_, _ = io.Copy(w, res.Reader)
			return
		}
		_, _ = w.Write(res.Body)
	}}
}
