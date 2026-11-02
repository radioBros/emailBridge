package messages

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/submit"
)

// Register mounts all P4.1 mailbox/message/draft routes.
func (s *Service) Register(srv *httpapi.Server) {
	ext := httpapi.Phase("P4")
	read := httpapi.WithScope(ext, "messages:read")
	write := httpapi.WithScope(ext, "messages:write")
	const readPerm = "Requires the `read` permission (every role; API keys `messages:read`, an account-bound key only for its account). "
	const writePerm = "Requires `messages.write` (owner, admin, operator; API keys `messages:write`, an account-bound key only for its account). "
	const syncNote = "The index is not written optimistically: the sync engine sees the change and emits the matching " +
		"`message.*` / `mailbox.*` events, after which list and get show it. "
	const remoteNote = "Opens the account's single on-demand IMAP connection on this node (or the Gmail / Graph API); " +
		"while another call uses it the answer is `429 connection_busy` — retry shortly. "

	httpapi.Register(srv, huma.Operation{
		OperationID: "listAccountMailboxes", Method: http.MethodGet,
		Path: "/v1/accounts/{accountId}/mailboxes", Summary: "List mailboxes",
		Description: "Lists the account's folders from the index (only `status: active`), ordered by ID. `path` returns " +
			"just the folder with exactly that path. Paged by `cursor` (`limit` 1–100, default and maximum 100); " +
			"`nextCursor` is null on the last page. " + readPerm + "Read-only; no connection to the mail server is made.",
		Metadata:  problems(pAuth, pAccount, pCursor),
		Responses: map[string]*huma.Response{"200": {Content: jsonExample(exMailboxList())}},
		Tags:      []string{"mailboxes"}, Extensions: read,
	}, s.listMailboxes)
	httpapi.Register(srv, huma.Operation{
		OperationID: "createAccountMailbox", Method: http.MethodPost,
		Path: "/v1/accounts/{accountId}/mailboxes", Summary: "Create mailbox",
		Description: "Creates a folder on the mail server (IMAP CREATE, plus SUBSCRIBE unless `subscribe: false`; a label " +
			"or folder for Gmail / Graph accounts). When the index already knows the folder the answer is its row; " +
			"otherwise `status: pending` without `id` — the ID arrives with the `mailbox.created` event after the next sync. " +
			writePerm + remoteNote,
		Metadata: problems(pAuth, pAccount, pRemote,
			httpapi.ProblemDoc{Code: errs.CodeValidationFailed, Detail: "path is required."}),
		RequestBody: &huma.RequestBody{Content: jsonExample(map[string]any{"path": "Archive/2026", "subscribe": true})},
		Responses: map[string]*huma.Response{"201": {Content: jsonExamples(map[string]*huma.Example{
			"pending": {Summary: "Created, not indexed yet", Value: map[string]any{
				"id": "", "accountId": exAccountID, "path": "Archive/2026", "uidValidity": 0, "status": "pending",
			}},
			"indexed": {Summary: "Already indexed", Value: exArchiveMailbox()},
		})}},
		DefaultStatus: http.StatusCreated, Tags: []string{"mailboxes"}, Extensions: write,
	}, s.createMailbox)
	httpapi.Register(srv, huma.Operation{
		OperationID: "getAccountMailbox", Method: http.MethodGet,
		Path: "/v1/accounts/{accountId}/mailboxes/{mailboxId}", Summary: "Get mailbox",
		Description: "Returns one indexed folder with its sync state (UIDVALIDITY, UIDNEXT, HIGHESTMODSEQ, message count, " +
			"last sync). A deleted or unknown folder is `404 mailbox_not_found`. " + readPerm + "Read-only.",
		Metadata:  problems(pAuth, pAccount, pMailbox),
		Responses: map[string]*huma.Response{"200": {Content: jsonExample(exMailbox())}},
		Tags:      []string{"mailboxes"}, Extensions: read,
	}, s.getMailbox)
	httpapi.Register(srv, huma.Operation{
		OperationID: "patchAccountMailbox", Method: http.MethodPatch,
		Path: "/v1/accounts/{accountId}/mailboxes/{mailboxId}", Summary: "Rename or subscribe mailbox",
		Description: "Renames the folder (`path`, IMAP RENAME) and/or changes its subscription (`subscribed`). After a " +
			"rename the next sync sees the folder under its new path as a new mailbox: `mailbox.deleted` for the old ID " +
			"and `mailbox.created` with a new `mailboxId` — read the new ID from the list. The answer shows the new path " +
			"with the current index row. " + writePerm + remoteNote,
		Metadata: problems(pAuth, pAccount, pMailbox, pRemote),
		RequestBody: &huma.RequestBody{Content: jsonExamples(map[string]*huma.Example{
			"rename":      {Summary: "Rename the folder", Value: map[string]any{"path": "Archive/2026-Q4"}},
			"unsubscribe": {Summary: "Unsubscribe", Value: map[string]any{"subscribed": false}},
		})},
		Responses: map[string]*huma.Response{"200": {Content: jsonExample(func() map[string]any {
			m := exArchiveMailbox()
			m["path"] = "Archive/2026-Q4"
			return m
		}())}},
		Tags: []string{"mailboxes"}, Extensions: write,
	}, s.patchMailbox)
	httpapi.Register(srv, huma.Operation{
		OperationID: "deleteAccountMailbox", Method: http.MethodDelete,
		Path: "/v1/accounts/{accountId}/mailboxes/{mailboxId}", Summary: "Delete mailbox",
		Description: "Deletes the folder and the messages in it on the mail server (IMAP DELETE) and answers `204`. " +
			"The index row and its messages go when sync sees the deletion (`mailbox.deleted`). " + writePerm + remoteNote +
			"Not reversible.",
		Metadata:      problems(pAuth, pAccount, pMailbox, pRemote),
		DefaultStatus: http.StatusNoContent, Tags: []string{"mailboxes"}, Extensions: write,
	}, s.deleteMailbox)

	httpapi.Register(srv, huma.Operation{
		OperationID: "listAccountMessages", Method: http.MethodGet,
		Path: "/v1/accounts/{accountId}/messages", Summary: "List messages",
		Description: "Lists indexed messages of the account (metadata only; bodies come from `GET …/messages/{messageId}`). " +
			"Filters: `mailboxId`, `threadId` (a conversation across folders), `flag` (exact flag such as `\\Flagged`), " +
			"`q` (part of the Message-ID header or emailId). Sort `internalDate` (default `-internalDate`, newest " +
			"first), `size`, `uid` or `id`. Paged by `cursor`, or by `page` with `includeTotal=true` for `total`; " +
			"`limit` 1–200 (default and maximum 200). " + readPerm + "Read-only; answered from the index only.",
		Metadata:  problems(pAuth, pAccount, pCursor, httpapi.ProblemDoc{Code: errs.CodeSortNotAllowed}),
		Responses: map[string]*huma.Response{"200": {Content: jsonExample(exMessageList())}},
		Tags:      []string{"messages"}, Extensions: read,
	}, s.listMessages)
	httpapi.Register(srv, huma.Operation{
		OperationID: "uploadAccountMessage", Method: http.MethodPost,
		Path: "/v1/accounts/{accountId}/messages", Summary: "Upload (APPEND) a message",
		Description: "Appends a complete RFC 822 message (base64 `raw`, ≤ 25 MiB) to a folder (IMAP APPEND, or the Gmail / " +
			"Graph import) with optional flags and INTERNALDATE, and answers `201` with the UID the server assigned. " +
			"For IMAP the folder's UIDVALIDITY is checked first (the indexed value, or `uidValidity`): a change is " +
			"`409 uid_validity_mismatch`. Nothing is sent to anyone. " + syncNote + writePerm + remoteNote,
		Metadata: problems(pAuth, pAccount, pMailbox, pRemote, pUIDValidity,
			httpapi.ProblemDoc{Code: errs.CodeValidationFailed, Detail: "raw must be base64."},
			httpapi.ProblemDoc{Code: errs.CodeMessageTooLarge, Detail: "The message exceeds the 25 MiB upload limit."}),
		RequestBody: &huma.RequestBody{Content: jsonExample(map[string]any{
			"path":         "Archive/2026",
			"raw":          "RnJvbTogQmlsbGluZyA8YmlsbGluZ0BleGFtcGxlLmNvbT4NClRvOiBBbm5hIFJvc3NpIDxhbm5hLnJvc3NpQGV4YW1wbGUub3JnPg0KU3ViamVjdDogSW52b2ljZSAyMDI2LTEwNDMNCkRhdGU6IFR1ZSwgMjcgT2N0IDIwMjYgMDk6NTU6MTIgKzAwMDANCk1lc3NhZ2UtSUQ6IDwyMDI2MTAyNzA5NTUxMi40ZjJhQGV4YW1wbGUuY29tPg0KDQpQbGVhc2UgZmluZCB0aGUgaW52b2ljZSBhdHRhY2hlZC4NCg==",
			"flags":        []any{`\Seen`},
			"internalDate": "2026-10-27T09:55:14Z",
		})},
		Responses: map[string]*huma.Response{"201": {Content: jsonExample(map[string]any{
			"uid": 4812, "uidValidity": 1729500007, "path": "Archive/2026",
		})}},
		DefaultStatus: http.StatusCreated, Tags: []string{"messages"}, Extensions: write,
	}, s.uploadMessage)
	httpapi.Register(srv, huma.Operation{
		OperationID: "batchAccountMessages", Method: http.MethodPost,
		Path: "/v1/accounts/{accountId}/messages/batch", Summary: "Batch message actions",
		Description: "Applies one action to up to 1000 messages, possibly in different folders: `flags` (add / remove / " +
			"set), `move` or `copy` (to `mailboxId` or `path`) or `delete` (\\Deleted + UID EXPUNGE). The messages are " +
			"grouped by folder and sent in UID sets of at most 200. Unknown IDs and folders that fail are listed in " +
			"`failed`; the rest is applied (not atomic). More than 1000 IDs is `422 batch_too_large`. " + syncNote +
			writePerm + remoteNote,
		Metadata: problems(pAuth, pAccount, pRemote, pMailbox,
			httpapi.ProblemDoc{Code: errs.CodeValidationFailed, Detail: "action must be flags, move, delete or copy."},
			httpapi.ProblemDoc{Code: errs.CodeBatchTooLarge, Detail: "A batch may contain at most 1000 message ids."}),
		RequestBody: &huma.RequestBody{Content: jsonExamples(map[string]*huma.Example{
			"markRead": {Summary: "Mark as read", Value: map[string]any{
				"ids": []any{exMessageID, exMessage2ID}, "action": "flags", "addFlags": []any{`\Seen`},
			}},
			"move": {Summary: "Move to a folder", Value: map[string]any{
				"ids": []any{exMessageID, exMessage2ID}, "action": "move", "mailboxId": exArchiveID,
			}},
			"delete": {Summary: "Delete", Value: map[string]any{"ids": []any{exMessage2ID}, "action": "delete"}},
		})},
		Responses: map[string]*huma.Response{"200": {Content: jsonExamples(map[string]*huma.Example{
			"all":     {Summary: "All applied", Value: map[string]any{"processed": 2}},
			"partial": {Summary: "One unknown ID", Value: map[string]any{"processed": 1, "failed": []any{"msg_01m5czx690tcwegdb2zfz7cnzn"}}},
		})}},
		Tags: []string{"messages"}, Extensions: write,
	}, s.batchMessages)
	httpapi.Register(srv, huma.Operation{
		OperationID: "searchAccountMessages", Method: http.MethodPost,
		Path: "/v1/accounts/{accountId}/messages/search", Summary: "Search messages on the server",
		Description: "Runs a search in one folder on the mail server (IMAP SEARCH; Gmail `gmailRaw` / labels; Microsoft " +
			"Graph `$search` with `useOutlookSearch`) for what the index cannot answer: body text, headers, sizes, " +
			"dates. All criteria combine with AND. The matches are returned as **index** rows, so messages sync has not " +
			"indexed yet are left out; there is no paging (`nextCursor` is always null). A criterion the provider " +
			"cannot run is `400 unsupported_for_provider`. " + readPerm + remoteNote + "Read-only.",
		Metadata: problems(pAuth, pAccount, pMailbox, pRemote,
			httpapi.ProblemDoc{Code: errs.CodeValidationFailed, Detail: "gmailRaw exceeds 1024 characters."}),
		RequestBody: &huma.RequestBody{Content: jsonExamples(map[string]*huma.Example{
			"imap": {Summary: "Unread invoices this month", Value: map[string]any{
				"mailboxId": exInboxID, "subject": "invoice", "unseen": true,
				"since": "2026-10-01T00:00:00Z", "larger": 10240,
			}},
			"header": {Summary: "By header", Value: map[string]any{
				"path": "INBOX", "header": map[string]any{"name": "X-Order-Id", "value": "ORD-58213"},
			}},
			"gmail": {Summary: "Gmail search syntax", Value: map[string]any{
				"path": "[Gmail]/All Mail", "gmailRaw": "has:attachment from:billing@example.com newer_than:30d",
			}},
		})},
		Responses: map[string]*huma.Response{"200": {Content: jsonExample(map[string]any{
			"data": []any{exMessageMeta()}, "nextCursor": nil,
		})}},
		Tags: []string{"messages"}, Extensions: read,
	}, s.searchMessages)
	httpapi.Register(srv, huma.Operation{
		OperationID: "getAccountMessage", Method: http.MethodGet,
		Path: "/v1/accounts/{accountId}/messages/{messageId}", Summary: "Get message",
		Description: "Returns the message's index metadata and attachment list. With `text` (plain, html or both), " +
			"`headers`, `envelope`, `markAsSeen` or `embedAttachedImages` the message is also fetched from the server " +
			"(at most `maxBytes`, default 256 KiB, cap 10 MiB; `textTruncated` says when the body was cut) — HTML always " +
			"comes with a sanitised `webSafeHtml`; `embedAttachedImages` inlines `cid:` images as data URLs (≤ 1 MiB). " +
			"`markAsSeen=true` adds `\\Seen` on the server. A folder whose UIDVALIDITY changed is `409 " +
			"uid_validity_mismatch`. " + readPerm + "Without enrichment options no connection is made.",
		Metadata:  problems(pAuth, pAccount, pMessage, pMailbox, pRemote, pUIDValidity),
		Responses: map[string]*huma.Response{"200": {Content: jsonExample(exMessageFull())}},
		Tags:      []string{"messages"}, Extensions: read,
	}, s.getMessage)
	httpapi.Register(srv, huma.Operation{
		OperationID: "patchAccountMessage", Method: http.MethodPatch,
		Path: "/v1/accounts/{accountId}/messages/{messageId}", Summary: "Change message flags",
		Description: "Adds (`addFlags`), removes (`removeFlags`) or replaces (`setFlags`) flags and keywords on the server " +
			"(IMAP STORE after a UIDVALIDITY check, or the Gmail / Graph equivalent). The response is the index row " +
			"**as it was before the change**: `flags` show the new state after sync emits `message.updated`. " +
			writePerm + remoteNote,
		Metadata: problems(pAuth, pAccount, pMessage, pMailbox, pRemote, pUIDValidity),
		RequestBody: &huma.RequestBody{Content: jsonExamples(map[string]*huma.Example{
			"read":    {Summary: "Mark as read and flag", Value: map[string]any{"addFlags": []any{`\Seen`, `\Flagged`}}},
			"unread":  {Summary: "Mark as unread", Value: map[string]any{"removeFlags": []any{`\Seen`}}},
			"replace": {Summary: "Replace all flags", Value: map[string]any{"setFlags": []any{`\Seen`, `\Answered`}}},
		})},
		Responses: map[string]*huma.Response{"200": {Content: jsonExample(exMessageMeta())}},
		Tags:      []string{"messages"}, Extensions: write,
	}, s.patchMessage)
	httpapi.Register(srv, huma.Operation{
		OperationID: "deleteAccountMessage", Method: http.MethodDelete,
		Path: "/v1/accounts/{accountId}/messages/{messageId}", Summary: "Delete message",
		Description: "Deletes the message on the server: `\\Deleted` + UID EXPUNGE for IMAP (permanent, not a move to " +
			"Trash), or the provider's delete for Gmail / Graph. Answers `204`; sync emits `message.deleted`. " +
			writePerm + remoteNote,
		Metadata:      problems(pAuth, pAccount, pMessage, pMailbox, pRemote, pUIDValidity),
		DefaultStatus: http.StatusNoContent, Tags: []string{"messages"}, Extensions: write,
	}, s.deleteMessage)
	httpapi.Register(srv, huma.Operation{
		OperationID: "moveAccountMessage", Method: http.MethodPost,
		Path: "/v1/accounts/{accountId}/messages/{messageId}/move", Summary: "Move message",
		Description: "Moves the message to another folder (`mailboxId`, or `path` — also a folder the index does not know " +
			"yet) with IMAP MOVE or the provider's move, and answers `204`. Sync then emits `message.deleted` for the old " +
			"ID and indexes the message in the destination under a new ID. " +
			writePerm + remoteNote,
		Metadata: problems(pAuth, pAccount, pMessage, pMailbox, pRemote, pUIDValidity,
			httpapi.ProblemDoc{Code: errs.CodeValidationFailed, Detail: "mailboxId or path is required."}),
		RequestBody: &huma.RequestBody{Content: jsonExamples(map[string]*huma.Example{
			"byId":   {Summary: "To an indexed mailbox", Value: map[string]any{"mailboxId": exArchiveID}},
			"byPath": {Summary: "To a folder path", Value: map[string]any{"path": "Archive/2026"}},
		})},
		DefaultStatus: http.StatusNoContent, Tags: []string{"messages"}, Extensions: write,
	}, s.moveMessage)
	// The attachment download route belongs to internal/attachments (P4.3), which streams from the archive
	// and falls back to Service.GetAttachment over the on-demand connection.

	httpapi.Register(srv, huma.Operation{
		OperationID: "listAccountDrafts", Method: http.MethodGet,
		Path: "/v1/accounts/{accountId}/drafts", Summary: "List drafts",
		Description: "Lists the drafts saved through this API for the account (the `drf_…` rows; drafts written by mail " +
			"clients appear as ordinary messages with `\\Draft`). Ordered by ID, paged by `cursor` (`limit` 1–100, " +
			"default and maximum 100). " + readPerm + "Read-only.",
		Metadata:  problems(pAuth, pAccount, pCursor),
		Responses: map[string]*huma.Response{"200": {Content: jsonExample(exDraftList())}},
		Tags:      []string{"drafts"}, Extensions: read,
	}, s.listDrafts)
	httpapi.Register(srv, huma.Operation{
		OperationID: "createAccountDraft", Method: http.MethodPost,
		Path: "/v1/accounts/{accountId}/drafts", Summary: "Save draft",
		Description: "Builds a MIME message from the structured fields (like a submit, recipients optional) or takes " +
			"`raw`, appends it with `\\Draft` to the account's Drafts folder (special-use `\\Drafts`, else `Drafts`) " +
			"over IMAP and answers `201` with the new `drf_…` row. Send it later with `POST …/drafts/{draftId}/send`. " +
			writePerm + remoteNote,
		Metadata: problems(pAuth, pAccount, pRemote,
			httpapi.ProblemDoc{Code: errs.CodeValidationFailed, Detail: "The draft could not be built from the given fields."}),
		RequestBody: &huma.RequestBody{Content: jsonExamples(map[string]*huma.Example{
			"structured": {Summary: "Structured draft", Value: map[string]any{
				"from":    map[string]any{"name": "Sales", "address": "sales@example.com"},
				"to":      []any{map[string]any{"name": "Marco Bianchi", "address": "marco.bianchi@example.org"}},
				"subject": "Quote for the November order",
				"text":    "Hello Marco,\n\nhere is the quote.",
				"html":    "<p>Hello Marco,</p><p>here is the quote.</p>",
				"headers": map[string]any{"X-Order-Id": "ORD-58213"},
			}},
			"raw": {Summary: "Raw MIME", Value: map[string]any{
				"raw": "RnJvbTogU2FsZXMgPHNhbGVzQGV4YW1wbGUuY29tPg0KVG86IE1hcmNvIEJpYW5jaGkgPG1hcmNvLmJpYW5jaGlAZXhhbXBsZS5vcmc+DQpTdWJqZWN0OiBRdW90ZSBmb3IgdGhlIE5vdmVtYmVyIG9yZGVyDQoNCkhlbGxvIE1hcmNvLA0KDQpoZXJlIGlzIHRoZSBxdW90ZS4NCg==",
			}},
		})},
		Responses:     map[string]*huma.Response{"201": {Content: jsonExample(exDraft())}},
		DefaultStatus: http.StatusCreated, Tags: []string{"drafts"}, Extensions: write,
	}, s.createDraft)
	httpapi.Register(srv, huma.Operation{
		OperationID: "getAccountDraft", Method: http.MethodGet,
		Path: "/v1/accounts/{accountId}/drafts/{draftId}", Summary: "Get draft",
		Description: "Returns the draft row; with `text=true` also its plain-text and HTML bodies fetched from the server " +
			"(≤ 256 KiB). " + readPerm + "Read-only.",
		Metadata: problems(pAuth, pAccount, pDraft, pRemote, pUIDValidity),
		Responses: map[string]*huma.Response{"200": {Content: jsonExample(func() map[string]any {
			d := exDraft()
			d["text"] = "Hello Marco,\n\nhere is the quote."
			d["html"] = "<p>Hello Marco,</p><p>here is the quote.</p>"
			return d
		}())}},
		Tags: []string{"drafts"}, Extensions: read,
	}, s.getDraft)
	httpapi.Register(srv, huma.Operation{
		OperationID: "deleteAccountDraft", Method: http.MethodDelete,
		Path: "/v1/accounts/{accountId}/drafts/{draftId}", Summary: "Delete draft",
		Description: "Expunges the draft from the Drafts folder (best effort: a server error does not keep the row) and " +
			"deletes the `drf_…` row; answers `204`. " + writePerm,
		Metadata:      problems(pAuth, pAccount, pDraft),
		DefaultStatus: http.StatusNoContent, Tags: []string{"drafts"}, Extensions: write,
	}, s.deleteDraft)
	httpapi.Register(srv, huma.Operation{
		OperationID: "sendAccountDraft", Method: http.MethodPost,
		Path: "/v1/accounts/{accountId}/drafts/{draftId}/send", Summary: "Send draft",
		Description: "Fetches the draft's MIME from the server and submits it like `POST …/outbound-messages` with `raw` " +
			"(recipients from its To / Cc / Bcc headers, the account's send policy, suppression and quotas apply), then " +
			"deletes the draft. Answers `202` with the queued outbound message; delivery events (`message.sent`, `message.failed` …) follow. A draft whose " +
			"UID the server never reported is `404 draft_not_found`. Send `Idempotency-Key` to make retries safe. " +
			writePerm + remoteNote,
		Metadata: problems(pAuth, pAccount, pDraft, pRemote, pUIDValidity,
			httpapi.ProblemDoc{Code: errs.CodeNoRecipients, Detail: "The draft has no recipients."},
			httpapi.ProblemDoc{Code: errs.CodeRecipientSuppressed},
			httpapi.ProblemDoc{Code: errs.CodeQuotaSendsExceeded},
			httpapi.ProblemDoc{Code: errs.CodeIdempotencyKeyReused}),
		Responses: map[string]*huma.Response{"202": {Content: jsonExample(map[string]any{
			"id": "out_01m5d44p70sfzkks0pyvmmem29", "messageId": "<quote-58213@example.com>", "state": "queued",
		})}},
		DefaultStatus: http.StatusAccepted, Tags: []string{"drafts"}, Extensions: write,
	}, s.sendDraft)
}

// AccountPath holds path parameters; exported so the OpenAPI document lists them (huma skips unexported embedded structs).
type AccountPath struct {
	AccountID string `path:"accountId" pattern:"^acc_[0-9a-z]{26}$" doc:"Account ID (acc_…)." example:"acc_01m5cvnpb031g57sqct6mzhq94"`
}

// MailboxPath holds path parameters; exported so the OpenAPI document lists them (huma skips unexported embedded structs).
type MailboxPath struct {
	AccountID string `path:"accountId" pattern:"^acc_[0-9a-z]{26}$" doc:"Account ID (acc_…)." example:"acc_01m5cvnpb031g57sqct6mzhq94"`
	MailboxID string `path:"mailboxId" pattern:"^mbx_[0-9a-z]{26}$" doc:"Mailbox ID (mbx_…)." example:"mbx_01m5ch2yg0he3zdbefkydy9cjb"`
}

// MessagePath holds path parameters; exported so the OpenAPI document lists them (huma skips unexported embedded structs).
type MessagePath struct {
	AccountID string `path:"accountId" pattern:"^acc_[0-9a-z]{26}$" doc:"Account ID (acc_…)." example:"acc_01m5cvnpb031g57sqct6mzhq94"`
	MessageID string `path:"messageId" pattern:"^msg_[0-9a-z]{26}$" doc:"Message ID (msg_…)." example:"msg_01m5ck6pf04wm1cyx6q10s4x5w"`
}

// DraftPath holds path parameters; exported so the OpenAPI document lists them (huma skips unexported embedded structs).
type DraftPath struct {
	AccountID string `path:"accountId" pattern:"^acc_[0-9a-z]{26}$" doc:"Account ID (acc_…)." example:"acc_01m5cvnpb031g57sqct6mzhq94"`
	DraftID   string `path:"draftId" pattern:"^drf_[0-9a-z]{26}$" doc:"Draft ID (drf_…)." example:"drf_01m5cqe6d0nx6xdjvzyprw8twh"`
}

type listMbxIn struct {
	AccountPath
	Path   string `query:"path" doc:"Only the folder with exactly this path." example:"INBOX"`
	Limit  int    `query:"limit" doc:"Page size 1–100 (default and maximum 100)." example:"50"`
	Cursor string `query:"cursor" doc:"Opaque cursor from the previous page's nextCursor." example:"eyJpZCI6Im1ieF8wMW01Y2V6NmgwNGVqdDcyZ2N4ODR3ZnFqZiJ9"`
}

type listOut[T any] struct {
	Body struct {
		Data       []T     `json:"data" doc:"The page of items."`
		NextCursor *string `json:"nextCursor" doc:"Cursor for the next page; null on the last page." example:"eyJpZCI6Im1ieF8wMW01Y2V6NmgwNGVqdDcyZ2N4ODR3ZnFqZiJ9"`
	}
}

func (s *Service) listMailboxes(ctx context.Context, in *listMbxIn) (*listOut[MailboxView], error) {
	data, next, err := s.ListMailboxes(ctx, in.AccountID, in.Path, in.Limit, in.Cursor)
	if err != nil {
		return nil, err
	}
	out := &listOut[MailboxView]{}
	out.Body.Data, out.Body.NextCursor = data, next
	return out, nil
}

type createMbxIn struct {
	AccountPath
	Body CreateMailboxInput
}

type mbxOut struct {
	Status int
	Body   MailboxView
}

func (s *Service) createMailbox(ctx context.Context, in *createMbxIn) (*mbxOut, error) {
	v, err := s.CreateMailbox(ctx, in.AccountID, in.Body)
	if err != nil {
		return nil, err
	}
	return &mbxOut{Status: http.StatusCreated, Body: *v}, nil
}

func (s *Service) getMailbox(ctx context.Context, in *MailboxPath) (*mbxOut, error) {
	v, err := s.GetMailbox(ctx, in.AccountID, in.MailboxID)
	if err != nil {
		return nil, err
	}
	return &mbxOut{Status: http.StatusOK, Body: *v}, nil
}

type patchMbxIn struct {
	MailboxPath
	Body PatchMailboxInput
}

func (s *Service) patchMailbox(ctx context.Context, in *patchMbxIn) (*mbxOut, error) {
	v, err := s.PatchMailbox(ctx, in.AccountID, in.MailboxID, in.Body)
	if err != nil {
		return nil, err
	}
	return &mbxOut{Status: http.StatusOK, Body: *v}, nil
}

func (s *Service) deleteMailbox(ctx context.Context, in *MailboxPath) (*struct{}, error) {
	return nil, s.DeleteMailbox(ctx, in.AccountID, in.MailboxID)
}

type listMsgIn struct {
	AccountPath
	MailboxID    string `query:"mailboxId" doc:"Only messages in this folder (mbx_…)." example:"mbx_01m5cez6h04ejt72gcx84wfqjf"`
	ThreadID     string `query:"threadId" doc:"Only the messages of this conversation, in every folder" example:"T5b0c9d2e71a4f863"`
	Flag         string `query:"flag" doc:"Only messages carrying this exact flag or keyword." example:"\\Flagged"`
	Q            string `query:"q" doc:"Case-insensitive part of the Message-ID header or emailId." example:"20261027095512.4f2a"`
	Limit        int    `query:"limit" doc:"Page size 1–200 (default and maximum 200)." example:"50"`
	Cursor       string `query:"cursor" doc:"Opaque cursor from the previous page's nextCursor (keyset paging)." example:"eyJpZCI6Im1zZ18wMW01Y3NoeWMwcTh0eXd6ZWVmaHF3bmt6NyIsImZpZWxkIjoiaW50ZXJuYWxEYXRlIiwiZGVzYyI6dHJ1ZSwia2V5IjoiMjAyNi0xMC0yN1QwODo0MTowMloifQ"`
	Page         int    `query:"page" doc:"1-based page number (offset paging; use instead of cursor)." example:"1"`
	Sort         string `query:"sort" doc:"internalDate, size, uid or id; prefix - for descending. Default -internalDate." example:"-internalDate"`
	IncludeTotal bool   `query:"includeTotal" doc:"With page: also return total (the number of matching messages)." example:"true"`
}

type msgListOut struct {
	Body struct {
		Data       []MessageView `json:"data" doc:"The page of messages (index metadata)."`
		NextCursor *string       `json:"nextCursor" doc:"Cursor for the next page; null on the last page and for search." example:"eyJpZCI6Im1zZ18wMW01Y3NoeWMwcTh0eXd6ZWVmaHF3bmt6NyIsImZpZWxkIjoiaW50ZXJuYWxEYXRlIiwiZGVzYyI6dHJ1ZSwia2V5IjoiMjAyNi0xMC0yN1QwODo0MTowMloifQ"`
		Total      *int64        `json:"total,omitempty" doc:"Number of matching messages, with page and includeTotal=true." example:"4795"`
	}
}

func (s *Service) listMessages(ctx context.Context, in *listMsgIn) (*msgListOut, error) {
	res, err := s.ListMessages(ctx, in.AccountID, ListFilter{
		MailboxID: in.MailboxID, ThreadID: in.ThreadID, Flag: in.Flag, Q: in.Q, Limit: in.Limit,
		Cursor: in.Cursor, Page: in.Page, Sort: in.Sort, IncludeTotal: in.IncludeTotal,
	})
	if err != nil {
		return nil, err
	}
	out := &msgListOut{}
	out.Body.Data, out.Body.NextCursor, out.Body.Total = res.Data, res.NextCursor, res.Total
	return out, nil
}

type getMsgIn struct {
	MessagePath
	Text                string `query:"text" doc:"Fetch the body: plain, html or both (html always comes with webSafeHtml)." example:"both"`
	Headers             bool   `query:"headers" doc:"Fetch all headers into headers." example:"false"`
	Envelope            bool   `query:"envelope" doc:"Decode Subject, From, To and Cc from the message itself." example:"true"`
	MaxBytes            int64  `query:"maxBytes" doc:"Bytes of the message to fetch (default 262144 = 256 KiB, at most 10485760 = 10 MiB); longer bodies set textTruncated." example:"262144"`
	MarkAsSeen          bool   `query:"markAsSeen" doc:"Also set \\Seen on the server." example:"true"`
	EmbedAttachedImages bool   `query:"embedAttachedImages" doc:"Inline cid: images in webSafeHtml as data URLs (each ≤ 256 KiB, together ≤ 1 MiB)." example:"false"`
	PreProcessHTML      bool   `query:"preProcessHtml" doc:"Return html and webSafeHtml even when text is not set." example:"false"`
}

type msgOut struct {
	Body MessageView
}

func (s *Service) getMessage(ctx context.Context, in *getMsgIn) (*msgOut, error) {
	v, err := s.GetMessage(ctx, in.AccountID, in.MessageID, GetOptions{
		Text: in.Text, Headers: in.Headers, Envelope: in.Envelope,
		MaxBytes: in.MaxBytes, MarkAsSeen: in.MarkAsSeen,
		EmbedAttachedImages: in.EmbedAttachedImages, PreProcessHTML: in.PreProcessHTML,
	})
	if err != nil {
		return nil, err
	}
	return &msgOut{Body: *v}, nil
}

type patchMsgIn struct {
	MessagePath
	Body PatchMessageInput
}

func (s *Service) patchMessage(ctx context.Context, in *patchMsgIn) (*msgOut, error) {
	v, err := s.PatchMessage(ctx, in.AccountID, in.MessageID, in.Body)
	if err != nil {
		return nil, err
	}
	return &msgOut{Body: *v}, nil
}

func (s *Service) deleteMessage(ctx context.Context, in *MessagePath) (*struct{}, error) {
	return nil, s.DeleteMessage(ctx, in.AccountID, in.MessageID)
}

type moveMsgIn struct {
	MessagePath
	Body MoveInput
}

func (s *Service) moveMessage(ctx context.Context, in *moveMsgIn) (*struct{}, error) {
	return nil, s.MoveMessage(ctx, in.AccountID, in.MessageID, in.Body)
}

type uploadIn struct {
	AccountPath
	Body UploadInput
}

type uploadOut struct {
	Status int
	Body   UploadResult
}

func (s *Service) uploadMessage(ctx context.Context, in *uploadIn) (*uploadOut, error) {
	v, err := s.Upload(ctx, in.AccountID, in.Body)
	if err != nil {
		return nil, err
	}
	return &uploadOut{Status: http.StatusCreated, Body: *v}, nil
}

type batchIn struct {
	AccountPath
	Body BatchInput
}

type batchOut struct {
	Body BatchResult
}

func (s *Service) batchMessages(ctx context.Context, in *batchIn) (*batchOut, error) {
	v, err := s.Batch(ctx, in.AccountID, in.Body)
	if err != nil {
		return nil, err
	}
	return &batchOut{Body: *v}, nil
}

type searchIn struct {
	AccountPath
	Body SearchInput
}

func (s *Service) searchMessages(ctx context.Context, in *searchIn) (*msgListOut, error) {
	data, err := s.SearchMessages(ctx, in.AccountID, in.Body)
	if err != nil {
		return nil, err
	}
	out := &msgListOut{}
	out.Body.Data = data
	return out, nil
}

type attachIn struct {
	MessagePath
	AttachmentID string `path:"attachmentId"`
	MaxBytes     int64  `query:"maxBytes"`
}

func (s *Service) getAttachment(ctx context.Context, in *attachIn) (*huma.StreamResponse, error) {
	body, ctype, err := s.GetAttachment(ctx, in.AccountID, in.MessageID, in.AttachmentID, in.MaxBytes)
	if err != nil {
		return nil, err
	}
	return &huma.StreamResponse{Body: func(hctx huma.Context) {
		writeAttachment(hctx.SetHeader, hctx.BodyWriter(), ctype, body)
	}}, nil
}

func writeAttachment(setHeader func(string, string), w interface{ Write([]byte) (int, error) }, ctype string, body []byte) {
	setHeader("Content-Type", ctype)
	_, _ = w.Write(body)
}

type listDraftsIn struct {
	AccountPath
	Limit  int    `query:"limit" doc:"Page size 1–100 (default and maximum 100)." example:"50"`
	Cursor string `query:"cursor" doc:"Opaque cursor from the previous page's nextCursor." example:"eyJpZCI6ImRyZl8wMW01Y3FlNmQwbng2eGRqdnp5cHJ3OHR3aCJ9"`
}

func (s *Service) listDrafts(ctx context.Context, in *listDraftsIn) (*listOut[DraftView], error) {
	data, next, err := s.ListDrafts(ctx, in.AccountID, in.Limit, in.Cursor)
	if err != nil {
		return nil, err
	}
	out := &listOut[DraftView]{}
	out.Body.Data, out.Body.NextCursor = data, next
	return out, nil
}

type createDraftIn struct {
	AccountPath
	Body DraftInput
}

type draftOut struct {
	Status int
	Body   DraftView
}

func (s *Service) createDraft(ctx context.Context, in *createDraftIn) (*draftOut, error) {
	v, err := s.CreateDraft(ctx, in.AccountID, in.Body)
	if err != nil {
		return nil, err
	}
	return &draftOut{Status: http.StatusCreated, Body: *v}, nil
}

type getDraftIn struct {
	DraftPath
	Text bool `query:"text" doc:"Also fetch the plain-text and HTML bodies from the server." example:"true"`
}

func (s *Service) getDraft(ctx context.Context, in *getDraftIn) (*draftOut, error) {
	v, err := s.GetDraft(ctx, in.AccountID, in.DraftID, in.Text)
	if err != nil {
		return nil, err
	}
	return &draftOut{Status: http.StatusOK, Body: *v}, nil
}

func (s *Service) deleteDraft(ctx context.Context, in *DraftPath) (*struct{}, error) {
	return nil, s.DeleteDraft(ctx, in.AccountID, in.DraftID)
}

type sendDraftIn struct {
	DraftPath
	IdempotencyKey string `header:"Idempotency-Key" doc:"Makes the send safe to retry: the same key replays the first answer instead of sending twice (kept 24 hours)." example:"9f1c2d7e-4b3a-4c8e-9a51-2f6d0b7c3e18"`
}

type sendDraftOut struct {
	Status int
	Body   submit.Accepted
}

func (s *Service) sendDraft(ctx context.Context, in *sendDraftIn) (*sendDraftOut, error) {
	res, err := s.SendDraft(ctx, in.AccountID, in.DraftID, in.IdempotencyKey)
	if err != nil {
		return nil, err
	}
	if res.Accepted == nil {
		return &sendDraftOut{Status: res.Status}, nil
	}
	return &sendDraftOut{Status: http.StatusAccepted, Body: *res.Accepted}, nil
}
