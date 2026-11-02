// Package embeddings is the optional semantic search and "ask the mailbox" of G1 (switch `ai.embeddings`,
// off by default), built on the G3 text store.
//
// # Data flow
//
// G3 keeps the plain text of every indexed message in `messageText` (package fulltext). This package hangs
// on its extension point (fulltext.Observer): after a row is written, the message is queued; a small worker
// pool cuts subject + body into overlapping chunks, embeds them with the organisation's embeddings endpoint
// (ai.Service.Embed: an OpenAI-compatible `/embeddings`, through the egress policy and the PII rule of the AI
// settings) and stores one `messageEmbeddings` document per chunk. Deletions in the text store delete the
// vectors. Nothing here ever blocks sync or full-text indexing: the queue is bounded and drops when full, a
// failed request is retried a few times with back-off, and the reindex job and the sweep fill any gap.
//
// # Search and ask
//
// A query is embedded and compared (cosine similarity, vectors are stored unit-length) with every chunk of
// the account, streamed from MongoDB; at most MaxChunks chunks per query (`validation_failed` beyond, narrow
// by date). Search returns the best messages (best chunk as snippet); ask gives the best chunks to the main
// AI provider under a strict prompt and returns the answer with the cited messages.
//
// # Switch
//
// `ai.embeddings` follows the G3 rule: the organisation value is the master, an account can opt out. It is
// effective for an account only while full-text search is on for it (no text, nothing to embed), the
// `ai.enabled` switch allows AI for it, and the AI settings have a usable embeddings endpoint.
package embeddings

import (
	"strconv"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// Collection is the vector store.
const Collection = "messageEmbeddings"

// SwitchName is the feature switch (organisation and account scope, default off).
const SwitchName = "ai.embeddings"

// JobKind is the reindex job kind.
const JobKind = "embeddings.reindex"

const (
	// chunkRunes is the target chunk length (code points); chunkOverlap is repeated at the start of the next.
	chunkRunes   = 1000
	chunkOverlap = 150
	// maxChunksPerMessage bounds the cost of one long message (about the first 27 000 characters are embedded).
	maxChunksPerMessage = 32
	// embedBatch is the number of chunks per provider request (a message always fits in one request).
	embedBatch = 32
	// DefaultMaxChunks is the default cap of chunks compared per query.
	DefaultMaxChunks = 50000
	// maxQueryRunes caps the search text; maxQuestionRunes the question.
	maxQueryRunes    = 500
	maxQuestionRunes = 1000
	// Search page size; ask excerpts.
	defaultSearchLimit = 10
	maxSearchLimit     = 50
	defaultAskLimit    = 6
	maxAskLimit        = 12
	// askChunksPerMessage is how many excerpts of one message ask may use.
	askChunksPerMessage = 2
	// askContextChars bounds the excerpts sent to the provider; askPromptChars the whole user prompt.
	askContextChars = 12000
	askPromptChars  = 16000
	// askTimeout bounds one ask (query embedding, scan, completion).
	askTimeout = 50 * time.Second
	// snippetRunes is the snippet length of a search hit.
	snippetRunes = 300
	// DefaultReindexLimit / MaxReindexLimit bound the reindex job.
	DefaultReindexLimit = 5000
	MaxReindexLimit     = 50000
)

// Chunk is one `messageEmbeddings` document.
type Chunk struct {
	ID             string      `bson:"_id"` // <messageId>:<chunk>
	OrganizationID string      `bson:"organizationId"`
	AccountID      string      `bson:"accountId"`
	MessageID      string      `bson:"messageId"`
	MailboxID      string      `bson:"mailboxId"`
	Chunk          int         `bson:"chunk"`
	Text           string      `bson:"text"`
	Vector         bson.Vector `bson:"vector"` // float32, unit length (BSON binary subtype 9)
	Model          string      `bson:"model"`
	Dims           int         `bson:"dims"`
	TextHash       string      `bson:"textHash"` // of model + embedded text: unchanged messages are not embedded again
	Date           time.Time   `bson:"date"`
	CreatedAt      time.Time   `bson:"createdAt"`
}

// chunkID is the deterministic _id of a chunk (every write is an idempotent upsert).
func chunkID(messageID string, n int) string {
	return messageID + ":" + strconv.Itoa(n)
}

// Reason says why semantic search is not available for an account.
type Reason string

// Reasons (Status.Reason).
const (
	ReasonNone          Reason = ""
	ReasonSwitchOff     Reason = "switch_off"     // ai.embeddings is off for the organisation or the account opted out
	ReasonFullTextOff   Reason = "fulltext_off"   // search.fullText is off for the account: there is no text to embed
	ReasonAIOff         Reason = "ai_off"         // ai.enabled is off for the account
	ReasonNotConfigured Reason = "not_configured" // the AI settings have no usable embeddings endpoint
)

// Hit is one semantic search result.
type SemanticHit struct {
	MessageID string    `json:"messageId" doc:"Message ID (msg_…)." example:"msg_01m5ck6pf04wm1cyx6q10s4x5w"`
	AccountID string    `json:"accountId" doc:"Account the message belongs to (acc_…)." example:"acc_01m5cvnpb031g57sqct6mzhq94"`
	MailboxID string    `json:"mailboxId" doc:"Folder holding the message (mbx_…)." example:"mbx_01m5cez6h04ejt72gcx84wfqjf"`
	Path      string    `json:"path" doc:"Folder path when the message was indexed." example:"INBOX"`
	Subject   string    `json:"subject" doc:"Decoded Subject header." example:"Convocazione assemblea condominiale"`
	From      string    `json:"from" doc:"Sender as \"Name <address>\" (or just the address)." example:"Amministrazione <studio@example.com>"`
	Date      time.Time `json:"date" doc:"Date header, or when the server received the message." example:"2026-10-27T09:55:12Z"`
	Snippet   string    `json:"snippet" doc:"Plain-text excerpt of the best matching part (at most about 300 characters, … where cut). Never HTML." example:"…the ordinary assembly is called for 14 November at 18:00 in the meeting room…"`
	Score     float64   `json:"score" doc:"Cosine similarity of the best chunk with the query (−1…1, higher is closer)." example:"0.8312"`
}

// SearchResult is the GET /v1/accounts/{accountId}/semantic-search body.
type SemanticSearchResult struct {
	Data   []SemanticHit `json:"data" doc:"The closest messages, best first (one per message)."`
	Model  string        `json:"model" doc:"Embedding model used." example:"text-embedding-3-small"`
	Chunks int64         `json:"chunks" doc:"Number of chunks compared." example:"5120"`
}

// Citation is a message an answer is based on.
type Citation struct {
	MessageID string    `json:"messageId" doc:"Message ID (msg_…)." example:"msg_01m5ck6pf04wm1cyx6q10s4x5w"`
	AccountID string    `json:"accountId" doc:"Account (acc_…)." example:"acc_01m5cvnpb031g57sqct6mzhq94"`
	MailboxID string    `json:"mailboxId" doc:"Folder holding the message (mbx_…)." example:"mbx_01m5cez6h04ejt72gcx84wfqjf"`
	Subject   string    `json:"subject" doc:"Decoded Subject header." example:"Convocazione assemblea condominiale"`
	From      string    `json:"from" doc:"Sender." example:"Amministrazione <studio@example.com>"`
	Date      time.Time `json:"date" doc:"Message date." example:"2026-10-27T09:55:12Z"`
	Score     float64   `json:"score" doc:"Similarity of the message's best excerpt with the question." example:"0.8312"`
}

// AskResult is the POST /v1/accounts/{accountId}/ask body.
type AskResult struct {
	Answer    string     `json:"answer" doc:"The model's answer, based only on the excerpts; it names the messages it used as [msg_…]. Plain text. Empty when the account has no embedded messages." example:"The next assembly is on 14 November at 18:00 [msg_01m5ck6pf04wm1cyx6q10s4x5w]."`
	Citations []Citation `json:"citations" doc:"The messages the answer cites (only messages that were among the excerpts), in order of first mention."`
	Excerpts  int        `json:"excerpts" doc:"Number of excerpts given to the model." example:"6"`
	Model     string     `json:"model" doc:"Embedding model used to find the excerpts." example:"text-embedding-3-small"`
}

// Status is the GET /v1/accounts/{accountId}/embeddings body.
type Status struct {
	AccountID       string `json:"accountId" doc:"Account (acc_…)." example:"acc_01m5cvnpb031g57sqct6mzhq94"`
	Enabled         bool   `json:"enabled" doc:"Semantic search and ask are available for the account." example:"true"`
	Reason          Reason `json:"reason,omitempty" enum:"switch_off,fulltext_off,ai_off,not_configured" doc:"Why not (when enabled is false): switch_off (ai.embeddings off or the account opted out), fulltext_off (search.fullText off for the account), ai_off (ai.enabled off), not_configured (no usable embeddings endpoint in the AI settings)." example:"switch_off"`
	Model           string `json:"model,omitempty" doc:"Embedding model in use." example:"text-embedding-3-small"`
	Messages        int64  `json:"messages" doc:"Messages of the account with vectors of the current model." example:"1830"`
	Chunks          int64  `json:"chunks" doc:"Stored chunks of the current model." example:"5120"`
	IndexedMessages int64  `json:"indexedMessages" doc:"Messages in the full-text store (what can be embedded)." example:"1912"`
	MaxChunks       int    `json:"maxChunks" doc:"Most chunks one query compares; narrow by date beyond." example:"50000"`
}

// ReindexResult is the result of a finished reindex job.
type ReindexResult struct {
	AccountID       string `json:"accountId"`
	Scanned         int64  `json:"scanned"`
	Embedded        int64  `json:"embedded"`
	AlreadyEmbedded int64  `json:"alreadyEmbedded"`
	Skipped         int64  `json:"skipped"`
	Failed          int64  `json:"failed"`
	Chunks          int64  `json:"chunks"`
}
