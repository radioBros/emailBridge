package embeddings

import (
	"context"
	"math"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/fulltext"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

// scanBatch is the cursor batch size of the vector scan.
const scanBatch = 500

// SearchInput is GET /v1/accounts/{accountId}/semantic-search.
type SearchInput struct {
	AccountID string
	Q         string
	Limit     int
	From, To  time.Time
}

// scored is one chunk's similarity.
type scored struct {
	id    string // chunk _id
	score float64
}

// candidate is a message with its best chunks (best first).
type candidate struct {
	messageID string
	best      []scored
}

func (c *candidate) top() float64 { return c.best[0].score }

// keep records a chunk score, keeping the k best of the message.
func (c *candidate) keep(sc scored, k int) {
	i := sort.Search(len(c.best), func(i int) bool { return c.best[i].score < sc.score })
	if i >= k {
		return
	}
	c.best = append(c.best, scored{})
	copy(c.best[i+1:], c.best[i:])
	c.best[i] = sc
	if len(c.best) > k {
		c.best = c.best[:k]
	}
}

// rank orders candidates by their best chunk (ties: message ID descending, newest first).
func rank(m map[string]*candidate) []*candidate {
	out := make([]*candidate, 0, len(m))
	for _, c := range m {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].top() != out[j].top() {
			return out[i].top() > out[j].top()
		}
		return out[i].messageID > out[j].messageID
	})
	return out
}

// checkText validates a query or question: trimmed, not empty, at most maxRunes code points, no control
// characters other than line breaks and tabs.
func checkText(field, raw string, maxRunes int) (string, error) {
	q := strings.TrimSpace(raw)
	if q == "" {
		return "", errs.New(errs.CodeValidationFailed, field+" is required.")
	}
	if !utf8.ValidString(q) {
		return "", errs.New(errs.CodeValidationFailed, field+" is not valid UTF-8.")
	}
	if utf8.RuneCountInString(q) > maxRunes {
		return "", errs.New(errs.CodeValidationFailed, field+" is too long.").WithSafe("max", maxRunes)
	}
	for _, r := range q {
		if unicode.IsControl(r) && r != '\n' && r != '\t' && r != '\r' {
			return "", errs.New(errs.CodeValidationFailed, field+" contains control characters.")
		}
	}
	return q, nil
}

// scanFilter is the chunk filter of a query.
func scanFilter(org, acc, model string, dims int, from, to time.Time) bson.M {
	f := bson.M{"organizationId": org, "accountId": acc, "model": model, "dims": dims}
	date := bson.M{}
	if !from.IsZero() {
		date["$gte"] = from.UTC()
	}
	if !to.IsZero() {
		date["$lte"] = to.UTC()
	}
	if len(date) > 0 {
		f["date"] = date
	}
	return f
}

// countCapped refuses a query over more than MaxChunks chunks (before the provider is called).
func (s *Service) countCapped(ctx context.Context, org, acc, model string, from, to time.Time) (int64, error) {
	f := scanFilter(org, acc, model, 0, from, to)
	delete(f, "dims") // every chunk of the model counts, whatever its dimensions
	limit := s.maxChunks()
	n, err := s.Repo.Count(ctx, f, int64(limit)+1)
	if err != nil {
		return 0, err
	}
	if n > int64(limit) {
		return n, errs.New(errs.CodeValidationFailed,
			"The account has too many embedded chunks for one query; narrow the search with a date range.").
			WithSafe("max", limit)
	}
	return n, nil
}

// scan streams the account's chunks of the model and keeps, per message, the perMessage most similar to q.
func (s *Service) scan(ctx context.Context, org, acc, model string, q []float32, from, to time.Time, perMessage int) (map[string]*candidate, int64, error) {
	cur, err := s.DB.Collection(Collection).Find(ctx, scanFilter(org, acc, model, len(q), from, to),
		options.Find().SetProjection(bson.M{"messageId": 1, "vector": 1}).SetBatchSize(scanBatch))
	if err != nil {
		return nil, 0, errs.Wrap(err, errs.CodeInternalError, "embeddings.scan")
	}
	defer func() { _ = cur.Close(ctx) }()
	out := map[string]*candidate{}
	var n int64
	limit := int64(s.maxChunks())
	for cur.Next(ctx) {
		n++
		if n > limit { // rows written since the count: stop at the cap rather than run long
			break
		}
		id, _ := cur.Current.Lookup("_id").StringValueOK()
		msg, _ := cur.Current.Lookup("messageId").StringValueOK()
		_, data, ok := cur.Current.Lookup("vector").BinaryOK()
		if !ok || msg == "" {
			continue
		}
		score, ok := dotPacked(q, data)
		if !ok {
			continue
		}
		c := out[msg]
		if c == nil {
			c = &candidate{messageID: msg}
			out[msg] = c
		}
		c.keep(scored{id: id, score: score}, perMessage)
	}
	if err := cur.Err(); err != nil {
		return nil, 0, errs.Wrap(err, errs.CodeInternalError, "embeddings.scan")
	}
	return out, min(n, limit), nil
}

// queryVector embeds a query or question.
func (s *Service) queryVector(ctx context.Context, org, model, text string) ([]float32, error) {
	emb, err := s.AI.Embed(ctx, org, []string{text})
	if err != nil {
		return nil, err
	}
	if emb.Model != model || len(emb.Vectors) != 1 {
		return nil, errs.New(errs.CodeServiceUnavailable, "The embedding model changed; try again.")
	}
	q, ok := unit(emb.Vectors[0])
	if !ok {
		return nil, errs.New(errs.CodeServiceUnavailable, "The embeddings provider returned an empty vector.")
	}
	return q, nil
}

// textMeta is the part of a text-store row a result shows.
type textMeta struct {
	ID        string    `bson:"_id"`
	MailboxID string    `bson:"mailboxId"`
	Path      string    `bson:"path"`
	Subject   string    `bson:"subject"`
	From      string    `bson:"from"`
	Date      time.Time `bson:"date"`
}

// hydrate loads the text-store rows of the candidates that are still in the message index; the vectors of
// the others are deleted (the lazy half of "vectors follow the index").
func (s *Service) hydrate(ctx context.Context, org, acc string, cands []*candidate) (map[string]textMeta, error) {
	ids := make([]string, len(cands))
	for i, c := range cands {
		ids[i] = c.messageID
	}
	out := make(map[string]textMeta, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	cur, err := s.DB.Collection(fulltext.Collection).Find(ctx, bson.M{"_id": bson.M{"$in": ids}, "organizationId": org, "accountId": acc},
		options.Find().SetProjection(bson.M{"mailboxId": 1, "path": 1, "subject": 1, "from": 1, "date": 1}))
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "embeddings.hydrate")
	}
	defer func() { _ = cur.Close(ctx) }()
	for cur.Next(ctx) {
		var m textMeta
		if err := cur.Decode(&m); err != nil {
			return nil, errs.Wrap(err, errs.CodeInternalError, "embeddings.hydrate")
		}
		out[m.ID] = m
	}
	if err := cur.Err(); err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "embeddings.hydrate")
	}
	live, err := s.liveMessages(ctx, org, acc, ids)
	if err != nil {
		return nil, err
	}
	var orphans []string
	for _, id := range ids {
		if _, ok := out[id]; !ok || !live[id] {
			delete(out, id)
			orphans = append(orphans, id)
		}
	}
	if len(orphans) > 0 {
		if _, err := s.Repo.DeleteMessages(ctx, org, acc, orphans); err != nil {
			s.log().WarnContext(ctx, "embeddings orphan cleanup failed", "err", err)
		}
	}
	return out, nil
}

// round4 rounds a score for the API.
func round4(x float64) float64 { return math.Round(x*10000) / 10000 }

// Search finds the messages of an account closest in meaning to the query.
func (s *Service) Search(ctx context.Context, in SearchInput) (*SemanticSearchResult, error) {
	if err := tenancy.RequireReadAccount(ctx, "messages", in.AccountID); err != nil {
		return nil, err
	}
	q, err := checkText("q", in.Q, maxQueryRunes)
	if err != nil {
		return nil, err
	}
	limit := in.Limit
	if limit == 0 {
		limit = defaultSearchLimit
	}
	if limit < 1 || limit > maxSearchLimit {
		return nil, errs.New(errs.CodeValidationFailed, "limit must be between 1 and 50.").WithSafe("max", maxSearchLimit)
	}
	if !in.From.IsZero() && !in.To.IsZero() && in.To.Before(in.From) {
		return nil, errs.New(errs.CodeValidationFailed, "to must not be before from.")
	}
	org, model, err := s.ready(ctx, in.AccountID)
	if err != nil {
		return nil, err
	}
	s.init()
	if err := s.limitUser(ctx, s.searchRL, org); err != nil {
		return nil, err
	}
	if _, err := s.countCapped(ctx, org, in.AccountID, model, in.From, in.To); err != nil {
		return nil, err
	}
	qv, err := s.queryVector(ctx, org, model, q)
	if err != nil {
		return nil, err
	}
	cands, scanned, err := s.scan(ctx, org, in.AccountID, model, qv, in.From, in.To, 1)
	if err != nil {
		return nil, err
	}
	ranked := rank(cands)
	// A few extra in case some messages left the index since they were embedded.
	ranked = ranked[:min(len(ranked), limit+limit/2+2)]
	meta, err := s.hydrate(ctx, org, in.AccountID, ranked)
	if err != nil {
		return nil, err
	}
	var chunkIDs []string
	var picked []*candidate
	for _, c := range ranked {
		if _, ok := meta[c.messageID]; !ok {
			continue
		}
		picked = append(picked, c)
		chunkIDs = append(chunkIDs, c.best[0].id)
		if len(picked) == limit {
			break
		}
	}
	texts, err := s.Repo.Texts(ctx, org, in.AccountID, chunkIDs)
	if err != nil {
		return nil, err
	}
	out := &SemanticSearchResult{Data: make([]SemanticHit, 0, len(picked)), Model: model, Chunks: scanned}
	for _, c := range picked {
		m := meta[c.messageID]
		out.Data = append(out.Data, SemanticHit{
			MessageID: c.messageID, AccountID: in.AccountID, MailboxID: m.MailboxID, Path: m.Path,
			Subject: m.Subject, From: m.From, Date: m.Date, Snippet: snippet(texts[c.best[0].id], snippetRunes),
			Score: round4(c.top()),
		})
	}
	return out, nil
}
