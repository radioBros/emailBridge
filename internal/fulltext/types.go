// Package fulltext is the optional full-text search over the text of an organisation's messages, across all
// its accounts (G3, switch `search.fullText`, off by default).
//
// # The text store
//
// One MongoDB collection, `messageText`, holds one row per indexed message (Row). It is a derived copy of the
// message index (`messages`): a row exists only while its message is in the index, and it is never the source
// of truth for anything else. The store is designed to be reused by later features (G1 semantic search reads
// the same rows to compute embeddings):
//
//   - `_id` and `messageId` are both the message ID (msg_…), so every write is an idempotent upsert;
//   - `text` is plain text (HTML bodies are converted, whitespace is collapsed), at most MaxTextBytes of
//     valid UTF-8, cut on a rune boundary (`textTruncated` says so);
//   - `from` and `to` are flattened "Name <address>" strings so the text index can use them;
//   - `date` is the Date header, or the INTERNALDATE when the header is missing or unparsable;
//   - `indexedAt` is when the row was last written.
//
// Indexes (migration 49): a compound text index {organizationId, subject, from, text} — every `$text` query
// must therefore name the organisation, which doubles as the tenant guard — with `default_language: "none"`
// (mail arrives in many languages; no stemming, no stop words) and weights subject 10, from 3, text 1; plus
// {organizationId, accountId, date} for filtered listings and {accountId, mailboxId} for the purges.
//
// # Writers
//
//   - The indexer (Service.HandleEvent) consumes EMAILBRIDGE_EVENTS with its own durable consumers
//     (`fulltext-<partition>`, partition leases `fts.<p>`, api nodes only): message.received upserts the row
//     after fetching the text through messages.Service (IMAP, Gmail and Graph alike); message.deleted,
//     mailbox.deleted and mailbox.reset delete rows; account.updated (switches changed) and account.deleted
//     purge the account when the switch is off for it.
//   - The reindex job (kind JobKind) walks an account's message index newest first and indexes missing rows.
//   - The purge duty (scheduler leader) deletes the rows of accounts the switch no longer covers.
//
// Account and organisation erasure delete the rows through tenancy.CollectionRegistry.
package fulltext

import (
	"context"
	"time"
)

// Collection is the text store.
const Collection = "messageText"

// SwitchName is the feature switch (organisation and account scope, default off).
const SwitchName = "search.fullText"

// JobKind is the reindex job kind.
const JobKind = "fulltext.reindex"

const (
	// MaxTextBytes caps Row.Text (bytes of UTF-8).
	MaxTextBytes = 64 << 10
	// fetchBytes is how much of the raw message the indexer fetches to find the text parts; the text comes
	// before the attachments in almost every message, so attachments are mostly not downloaded.
	fetchBytes = 1 << 20
	// maxQueryRunes caps the search text.
	maxQueryRunes = 256
	// maxQueryTerms caps the words and phrases of one query.
	maxQueryTerms = 32
	// defaultPageSize / maxPageSize bound `limit`.
	defaultPageSize = 25
	maxPageSize     = 100
	// totalCap caps `total` and how deep `page` may go (relevance paging beyond it is not useful).
	totalCap = 1000
	// snippetRunes is the width of a snippet window, without the ellipses.
	snippetRunes = 160
	// DefaultReindexLimit / MaxReindexLimit bound the reindex job.
	DefaultReindexLimit = 5000
	MaxReindexLimit     = 50000
)

// Row is one `messageText` document. See the package documentation for the field rules.
type Row struct {
	ID             string    `bson:"_id"`
	OrganizationID string    `bson:"organizationId"`
	AccountID      string    `bson:"accountId"`
	MessageID      string    `bson:"messageId"`
	MailboxID      string    `bson:"mailboxId"`
	Path           string    `bson:"path"`
	Subject        string    `bson:"subject"`
	From           string    `bson:"from"`
	To             []string  `bson:"to"`
	Date           time.Time `bson:"date"`
	Text           string    `bson:"text"`
	TextTruncated  bool      `bson:"textTruncated,omitempty"`
	Size           int64     `bson:"size"`
	IndexedAt      time.Time `bson:"indexedAt"`
}

// Observer is told about writes to the text store (an extension point for features built on top of it,
// e.g. G1 embeddings). Calls are synchronous and best effort: an observer must not block for long and its
// failures are its own (the row is already written).
type Observer interface {
	// Indexed is called after a row was upserted.
	Indexed(ctx context.Context, row *Row)
	// Removed is called after rows were deleted: the listed messages, a whole mailbox (MailboxID set) or a
	// whole account (both empty).
	Removed(ctx context.Context, r Removal)
}

// AccountObserver is an optional extension of Observer: AccountChanged is called after an account's switches
// changed (account.updated with "switches" in changed), whether or not full-text search is on for it, so a
// feature with its own switch can purge at once. Like the Observer calls it must not block for long.
type AccountObserver interface {
	AccountChanged(ctx context.Context, orgID, accountID string)
}

// Removal describes deleted rows.
type Removal struct {
	OrganizationID string
	AccountID      string
	MailboxID      string
	MessageIDs     []string
}

// Highlight is a match inside a snippet: [Start, End) in Unicode code points of the snippet string.
type FullTextHighlight struct {
	Start int `json:"start" doc:"First code point of the match in snippet (0-based)." example:"14"`
	End   int `json:"end" doc:"Code point after the match (exclusive)." example:"21"`
}

// Hit is one search result.
type FullTextHit struct {
	MessageID  string              `json:"messageId" doc:"Message ID (msg_…); open it with GET /v1/accounts/{accountId}/messages/{messageId}." example:"msg_01m5ck6pf04wm1cyx6q10s4x5w"`
	AccountID  string              `json:"accountId" doc:"Account the message belongs to (acc_…)." example:"acc_01m5cvnpb031g57sqct6mzhq94"`
	MailboxID  string              `json:"mailboxId" doc:"Folder holding the message (mbx_…)." example:"mbx_01m5cez6h04ejt72gcx84wfqjf"`
	Path       string              `json:"path" doc:"Folder path when the message was indexed." example:"INBOX"`
	Subject    string              `json:"subject" doc:"Decoded Subject header." example:"Invoice 2026-1043"`
	From       string              `json:"from" doc:"Sender as \"Name <address>\" (or just the address)." example:"Billing <billing@example.com>"`
	To         []string            `json:"to" doc:"Recipients as \"Name <address>\" (at most the first few)." example:"[\"Reception <reception@example.com>\"]"`
	Date       time.Time           `json:"date" doc:"Date header, or when the server received the message if the header is missing." example:"2026-10-27T09:55:12Z"`
	Size       int64               `json:"size" doc:"RFC 822 size in bytes." example:"48213"`
	Snippet    string              `json:"snippet" doc:"Plain-text excerpt around the first match, at most about 160 characters, with … where the text was cut. Never HTML." example:"…please find the invoice 2026-1043 attached. Payment is due within 30 days…"`
	Highlights []FullTextHighlight `json:"highlights" doc:"Matches inside snippet as code-point ranges, for the client to emphasise; empty when the match was only in the subject or sender."`
	Score      float64             `json:"score" doc:"Relevance score of the text index (higher is better; only comparable within one result)." example:"1.75"`
}

// SearchResult is the GET /v1/messages/search body.
type FullTextSearchResult struct {
	Data        []FullTextHit `json:"data" doc:"The page of matches, best first (ties: newest first)."`
	Total       int64         `json:"total" doc:"Number of matching messages, capped at 1000 (see totalCapped)." example:"37"`
	TotalCapped bool          `json:"totalCapped" doc:"True when there are more matches than total says." example:"false"`
}

// ReindexResult is the result of a finished reindex job.
type ReindexResult struct {
	AccountID      string `json:"accountId"`
	Scanned        int64  `json:"scanned"`
	Indexed        int64  `json:"indexed"`
	AlreadyIndexed int64  `json:"alreadyIndexed"`
	Skipped        int64  `json:"skipped"`
	Failed         int64  `json:"failed"`
}
