package embeddings

import (
	"context"
	"errors"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/fulltext"
	"github.com/radiobros/emailbridge/internal/index"
)

// task is one message to embed.
type task struct {
	org, acc, msg string
	tries         int
}

// enqueue adds a task without ever blocking; a full queue drops it (logged at most once a minute — the
// reindex job and the sweep's backfill catch dropped messages).
func (s *Service) enqueue(t task) bool {
	s.init()
	select {
	case s.queue <- t:
		return true
	default:
	}
	s.dropMu.Lock()
	s.dropCount++
	now := s.now()
	report := now.Sub(s.lastDrop) >= time.Minute
	n := s.dropCount
	if report {
		s.lastDrop, s.dropCount = now, 0
	}
	s.dropMu.Unlock()
	if report {
		s.log().Warn("embedding queue full: messages dropped; a reindex or the sweep embeds them later", "dropped", n)
	}
	return false
}

// RunWorkers embeds queued messages until ctx ends (Workers goroutines, default 2).
func (s *Service) RunWorkers(ctx context.Context) {
	s.init()
	n := s.Workers
	if n <= 0 {
		n = defaultWorkers
	}
	var wg sync.WaitGroup
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case t := <-s.queue:
					s.process(ctx, t)
				}
			}
		}()
	}
	wg.Wait()
}

// process embeds one queued message; a retryable failure is queued again after a back-off, up to
// defaultMaxTries tries.
func (s *Service) process(ctx context.Context, t task) {
	ctx = systemCtx(ctx, t.org, t.acc)
	_, err := s.EmbedMessage(ctx, t.org, t.acc, t.msg)
	if err == nil || ctx.Err() != nil {
		return
	}
	attrs := []any{"messageId", t.msg, "code", string(errs.CodeOf(err)), "attempt", t.tries + 1}
	if !errs.Retryable(err) || t.tries+1 >= defaultMaxTries {
		s.log().InfoContext(ctx, "message not embedded; a reindex can add it later", append(attrs, "err", err)...)
		return
	}
	base := s.RetryBase
	if base <= 0 {
		base = defaultRetryBase
	}
	delay := base << t.tries
	s.log().DebugContext(ctx, "embedding will retry", append(attrs, "err", err, "delay", delay.String())...)
	t.tries++
	time.AfterFunc(delay, func() {
		if ctx.Err() == nil {
			s.enqueue(t)
		}
	})
}

// prepared is a text-store row cut into chunks.
type prepared struct {
	row    *fulltext.Row
	chunks []string
	hash   string
}

func prepare(row *fulltext.Row, model string) prepared {
	src := sourceText(row.Subject, row.Text)
	return prepared{row: row, chunks: chunkText(src, chunkRunes, chunkOverlap, maxChunksPerMessage), hash: textHash(model, src)}
}

// EmbedMessage embeds one message from the text store. It reports false without an error when there is
// nothing to do: semantic search is not available for the account, the message has no text row, or its
// vectors are current. Provider errors are returned for the caller to classify.
func (s *Service) EmbedMessage(ctx context.Context, org, acc, msgID string) (bool, error) {
	reason, model, err := s.state(ctx, org, acc, false)
	if err != nil || reason != ReasonNone {
		return false, err
	}
	row, err := s.textRow(ctx, org, acc, msgID)
	if err != nil || row == nil {
		return false, err
	}
	p := prepare(row, model)
	have, err := s.Repo.Hashes(ctx, org, acc, model, []string{msgID})
	if err != nil {
		return false, err
	}
	if have[msgID] == p.hash {
		return false, nil
	}
	if _, err := s.embedGroup(ctx, org, acc, model, []prepared{p}); err != nil {
		return false, err
	}
	return true, nil
}

// textRow loads a text-store row (nil when it is gone).
func (s *Service) textRow(ctx context.Context, org, acc, msgID string) (*fulltext.Row, error) {
	var r fulltext.Row
	err := s.DB.Collection(fulltext.Collection).FindOne(ctx, bson.M{"_id": msgID, "organizationId": org, "accountId": acc}).Decode(&r)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, nil
	}
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "embeddings.text_row")
	}
	return &r, nil
}

// embedGroup embeds the chunks of a few messages in one provider request (at most embedBatch chunks), after
// waiting for the organisation's rate limit, and writes them. It returns the number of chunks written.
func (s *Service) embedGroup(ctx context.Context, org, acc, model string, group []prepared) (int, error) {
	var inputs []string
	for _, p := range group {
		inputs = append(inputs, p.chunks...)
	}
	if len(inputs) > 0 {
		if err := s.waitOrg(ctx, org); err != nil {
			return 0, err
		}
	}
	var vecs [][]float32
	if len(inputs) > 0 {
		emb, err := s.AI.Embed(ctx, org, inputs)
		if err != nil {
			return 0, err
		}
		if emb.Model != model {
			// The settings changed between the check and the request: these vectors belong to another model.
			return 0, errs.New(errs.CodeServiceUnavailable, "The embedding model changed; try again.")
		}
		vecs = emb.Vectors
	}
	now := s.now().Truncate(time.Millisecond)
	written, k := 0, 0
	for _, p := range group {
		chunks := make([]Chunk, 0, len(p.chunks))
		for _, text := range p.chunks {
			v, ok := unit(vecs[k])
			k++
			if !ok {
				continue // a zero vector matches nothing; leave the chunk out
			}
			chunks = append(chunks, Chunk{
				ID: chunkID(p.row.MessageID, len(chunks)), OrganizationID: org, AccountID: acc, MessageID: p.row.MessageID,
				MailboxID: p.row.MailboxID, Chunk: len(chunks), Text: text, Vector: bson.NewVector(v), Model: model,
				Dims: len(v), TextHash: p.hash, Date: p.row.Date, CreatedAt: now,
			})
		}
		if err := s.Repo.ReplaceMessage(ctx, org, acc, p.row.MessageID, chunks); err != nil {
			return written, err
		}
		written += len(chunks)
		// The message may have left the text store while it was being embedded: its Removed call found no
		// vectors then, so take them back out now.
		if gone, err := s.textRow(ctx, org, acc, p.row.MessageID); err == nil && gone == nil {
			if _, err := s.Repo.DeleteMessages(ctx, org, acc, []string{p.row.MessageID}); err != nil {
				return written, err
			}
		}
	}
	return written, nil
}

// waitOrg waits for the organisation's background rate limit.
func (s *Service) waitOrg(ctx context.Context, org string) error {
	s.init()
	wait := s.orgRL.reserve(org, s.now())
	if wait <= 0 {
		return nil
	}
	t := time.NewTimer(wait)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// liveMessages returns which of ids are still in the message index of the account.
func (s *Service) liveMessages(ctx context.Context, org, acc string, ids []string) (map[string]bool, error) {
	out := make(map[string]bool, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	cur, err := s.DB.Collection(index.Messages).Find(ctx, bson.M{"_id": bson.M{"$in": ids}, "organizationId": org, "accountId": acc},
		options.Find().SetProjection(bson.M{"_id": 1}))
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "embeddings.live")
	}
	defer func() { _ = cur.Close(ctx) }()
	for cur.Next(ctx) {
		id, _ := cur.Current.Lookup("_id").StringValueOK()
		out[id] = true
	}
	if err := cur.Err(); err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "embeddings.live")
	}
	return out, nil
}
