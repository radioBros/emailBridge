package messages

import (
	"github.com/danielgtaylor/huma/v2"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/httpapi"
)

// OpenAPI documentation of the P4.1 routes (W70): problem declarations and whole-body examples. Field-level
// docs and examples live on the types (types.go, api.go).

const (
	exAccountID  = "acc_01m5cvnpb031g57sqct6mzhq94"
	exInboxID    = "mbx_01m5cez6h04ejt72gcx84wfqjf"
	exArchiveID  = "mbx_01m5ch2yg0he3zdbefkydy9cjb"
	exMessageID  = "msg_01m5ck6pf04wm1cyx6q10s4x5w"
	exMessage2ID = "msg_01m5cshyc0q8tywzeefhqwnkz7"
	exDraftID    = "drf_01m5cqe6d0nx6xdjvzyprw8twh"
	exCursor     = "eyJpZCI6Im1zZ18wMW01Y3NoeWMwcTh0eXd6ZWVmaHF3bmt6NyIsImZpZWxkIjoiaW50ZXJuYWxEYXRlIiwiZGVzYyI6dHJ1ZSwia2V5IjoiMjAyNi0xMC0yN1QwODo0MTowMloifQ"
)

// Problems shared by the routes (the declared codes replace the default example of their status).
var (
	pAuth = []httpapi.ProblemDoc{
		{Code: errs.CodeUnauthenticated}, {Code: errs.CodeForbidden},
		{Code: errs.CodeScopeMissing, Detail: "The API key lacks the messages:read / messages:write scope, or is bound to another account."},
	}
	pAccount = httpapi.ProblemDoc{Code: errs.CodeNotFound, Detail: "The account does not exist."}
	// pRemote are the problems of a call that opens the account's on-demand IMAP / provider connection.
	pRemote = []httpapi.ProblemDoc{
		{Code: errs.CodeConnectionBusy},
		{Code: errs.CodeRateLimited},
		{Code: errs.CodeMailConnectFailed, Detail: "Could not connect to imap.example.com:993."},
		{Code: errs.CodeMailAuthFailed, Detail: "The IMAP server rejected the account credentials."},
		{Code: errs.CodeMailProtocolError, Detail: "The IMAP server answered NO to the command."},
		{Code: errs.CodeMailTimeout, Detail: "The IMAP server did not answer in time."},
		{Code: errs.CodeUnsupportedForProvider, Detail: "Mailbox API for this provider is not configured on this node."},
	}
	pUIDValidity = httpapi.ProblemDoc{Code: errs.CodeUidValidityMismatch, Detail: "The mailbox UIDVALIDITY changed; refresh and retry."}
	pMailbox     = httpapi.ProblemDoc{Code: errs.CodeMailboxNotFound, Detail: "The mailbox does not exist."}
	pMessage     = httpapi.ProblemDoc{Code: errs.CodeMessageNotFound, Detail: "The message does not exist."}
	pDraft       = httpapi.ProblemDoc{Code: errs.CodeDraftNotFound, Detail: "The draft does not exist."}
	pCursor      = httpapi.ProblemDoc{Code: errs.CodeValidationFailed, Detail: "The cursor is invalid."}
)

// problems joins problem groups into huma.Operation.Metadata.
func problems(groups ...any) map[string]any {
	var docs []httpapi.ProblemDoc
	for _, g := range groups {
		switch v := g.(type) {
		case httpapi.ProblemDoc:
			docs = append(docs, v)
		case []httpapi.ProblemDoc:
			docs = append(docs, v...)
		}
	}
	return httpapi.ProblemDocs(docs...)
}

func jsonExample(v any) map[string]*huma.MediaType {
	return map[string]*huma.MediaType{"application/json": {Example: v}}
}

func jsonExamples(ex map[string]*huma.Example) map[string]*huma.MediaType {
	return map[string]*huma.MediaType{"application/json": {Examples: ex}}
}

func exMailbox() map[string]any {
	return map[string]any{
		"id": exInboxID, "accountId": exAccountID, "path": "INBOX", "delimiter": "/", "specialUse": `\Inbox`,
		"uidValidity": 1729500000, "uidNext": 4812, "highestModseq": 918273, "exists": 4795,
		"syncMode": "idle", "lastSyncAt": "2026-10-27T09:58:12Z", "status": "active",
	}
}

func exArchiveMailbox() map[string]any {
	return map[string]any{
		"id": exArchiveID, "accountId": exAccountID, "path": "Archive/2026", "delimiter": "/",
		"uidValidity": 1729500007, "uidNext": 213, "highestModseq": 40112, "exists": 212,
		"syncMode": "poll", "lastSyncAt": "2026-10-27T09:51:40Z", "status": "active",
	}
}

func exMailboxList() map[string]any {
	return map[string]any{"data": []any{exMailbox(), exArchiveMailbox()}, "nextCursor": nil}
}

func exAddress(name, addr string) map[string]any {
	return map[string]any{"name": name, "address": addr}
}

func exMessageMeta() map[string]any {
	return map[string]any{
		"id": exMessageID, "accountId": exAccountID, "mailboxId": exInboxID,
		"uidValidity": 1729500000, "uid": 4811, "modseq": 918270, "flags": []any{`\Seen`},
		"messageIdHeader": "<20261027095512.4f2a@example.com>", "emailId": "M8e1f4a7c92d5b3e6",
		"threadId": "T5b0c9d2e71a4f863", "internalDate": "2026-10-27T09:55:14Z", "size": 48213,
		"subject": "Invoice 2026-1043", "date": "2026-10-27T09:55:12Z",
		"from": []any{exAddress("Billing", "billing@example.com")},
		"to":   []any{exAddress("Anna Rossi", "anna.rossi@example.org")},
	}
}

func exMessage2Meta() map[string]any {
	return map[string]any{
		"id": exMessage2ID, "accountId": exAccountID, "mailboxId": exInboxID,
		"uidValidity": 1729500000, "uid": 4807, "modseq": 918101, "flags": []any{},
		"messageIdHeader": "<CAF3k9x2Lq7@mail.example.net>", "emailId": "M8e1f4a6b01c2d9a4",
		"threadId": "T5b0c9d2e1107fa22", "internalDate": "2026-10-27T08:41:02Z", "size": 9120,
		"subject": "Delivery slot for order ORD-58213", "date": "2026-10-27T08:40:58Z",
		"from": []any{exAddress("Logistics", "logistics@example.net")},
		"to":   []any{exAddress("Anna Rossi", "anna.rossi@example.org")},
	}
}

func exMessageList() map[string]any {
	return map[string]any{"data": []any{exMessageMeta(), exMessage2Meta()}, "nextCursor": exCursor}
}

func exMessageFull() map[string]any {
	m := exMessageMeta()
	m["cc"] = []any{exAddress("Accounts", "accounts@example.org")}
	m["text"] = "Please find the invoice attached."
	m["html"] = `<p>Please find the invoice <b>2026-1043</b> attached.</p><img src="https://tracker.example.net/p.gif">`
	m["webSafeHtml"] = "<p>Please find the invoice <b>2026-1043</b> attached.</p>"
	m["headers"] = map[string]any{
		"Message-Id": "<20261027095512.4f2a@example.com>", "Return-Path": "<billing@example.com>",
		"Content-Type": `multipart/mixed; boundary="b1_4f2a"`,
	}
	m["attachments"] = []any{map[string]any{
		"id": "att_01m5cnaee0p3aqj73d3n6ej38p", "filename": "invoice-2026-1043.pdf",
		"contentType": "application/pdf", "size": 38412, "scan": "clean",
	}}
	return m
}

func exDraft() map[string]any {
	return map[string]any{
		"id": exDraftID, "accountId": exAccountID, "path": "Drafts", "uid": 57, "uidValidity": 1729500003,
		"messageIdHeader": "<quote-58213@example.com>", "subject": "Quote for the November order", "size": 2841,
		"createdAt": "2026-10-27T10:02:41Z", "updatedAt": "2026-10-27T10:02:41Z",
	}
}

func exDraftList() map[string]any {
	second := map[string]any{
		"id": "drf_01m5cxsea095mxgq6awmywhkrs", "accountId": exAccountID, "path": "Drafts", "uid": 58,
		"uidValidity": 1729500003, "messageIdHeader": "<20261027103011.9c1e@example.com>",
		"subject": "Re: Delivery slot for order ORD-58213", "size": 1964,
		"createdAt": "2026-10-27T10:30:11Z", "updatedAt": "2026-10-27T10:30:11Z",
	}
	return map[string]any{"data": []any{exDraft(), second}, "nextCursor": nil}
}
