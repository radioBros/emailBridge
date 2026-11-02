package integrations

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/radiobros/emailbridge/internal/index"
)

const digestTick = time.Minute

// DigestDuty is the leader-elected chat new-mail digest loop.
func (s *Service) DigestDuty(ctx context.Context) {
	t := time.NewTicker(digestTick)
	defer t.Stop()
	s.runDigests(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.runDigests(ctx)
		}
	}
}

func (s *Service) runDigests(ctx context.Context) {
	if s.Repo == nil || s.DB == nil {
		return
	}
	rows, err := s.Repo.listDigestCandidates(ctx)
	if err != nil || len(rows) == 0 {
		return
	}
	now := time.Now().UTC()
	for i := range rows {
		inst := &rows[i]
		mins := digestMinutesOf(inst.Config)
		if mins <= 0 {
			continue
		}
		if inst.LastDigestAt != nil && now.Sub(*inst.LastDigestAt) < time.Duration(mins)*time.Minute {
			continue
		}
		_ = s.postDigest(ctx, inst, now, mins)
	}
}

func digestMinutesOf(cfg map[string]any) int {
	if cfg == nil {
		return 0
	}
	switch v := cfg["digestMinutes"].(type) {
	case int:
		return v
	case int32:
		return int(v)
	case int64:
		return int(v)
	case float64:
		return int(v)
	case json.Number:
		n, _ := v.Int64()
		return int(n)
	default:
		return 0
	}
}

type digestHit struct {
	Subject      string    `bson:"subject"`
	InternalDate time.Time `bson:"internalDate"`
}

func (s *Service) postDigest(ctx context.Context, inst *Instance, now time.Time, mins int) error {
	since := now.Add(-time.Duration(mins) * time.Minute)
	if inst.LastDigestInternalAt != nil {
		since = *inst.LastDigestInternalAt
	}
	filter := bson.M{
		"organizationId": inst.OrganizationID,
		"internalDate":   bson.M{"$gt": since},
		"subject":        bson.M{"$exists": true, "$ne": ""},
	}
	total, err := s.DB.Collection(index.Messages).CountDocuments(ctx, filter)
	if err != nil {
		return err
	}
	if total == 0 {
		// Still advance cursor so empty windows do not re-scan forever.
		return s.Repo.saveDigestCursor(ctx, inst.ID, now, now)
	}
	opts := options.Find().SetSort(bson.D{{Key: "internalDate", Value: 1}}).SetLimit(10).
		SetProjection(bson.M{"subject": 1, "internalDate": 1})
	cur, err := s.DB.Collection(index.Messages).Find(ctx, filter, opts)
	if err != nil {
		return err
	}
	defer func() { _ = cur.Close(ctx) }()
	var hits []digestHit
	if err := cur.All(ctx, &hits); err != nil {
		return err
	}
	var latest time.Time
	subjects := make([]string, 0, len(hits))
	for _, h := range hits {
		subjects = append(subjects, h.Subject)
		if h.InternalDate.After(latest) {
			latest = h.InternalDate
		}
	}
	if latest.IsZero() {
		latest = now
	}
	// Max internalDate among all matching (not only first 10) for cursor.
	var maxDoc struct {
		InternalDate time.Time `bson:"internalDate"`
	}
	err = s.DB.Collection(index.Messages).FindOne(ctx, filter,
		options.FindOne().SetSort(bson.D{{Key: "internalDate", Value: -1}}).SetProjection(bson.M{"internalDate": 1}),
	).Decode(&maxDoc)
	if err == nil && !maxDoc.InternalDate.IsZero() {
		latest = maxDoc.InternalDate
	}

	url, body, err := s.digestPayload(ctx, inst, int(total), subjects)
	if err != nil || url == "" {
		return err
	}
	client := s.client()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "EmailBridge-Digest/1")
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	_, _ = io.Copy(io.Discard, res.Body)
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("digest HTTP %d", res.StatusCode)
	}
	return s.Repo.saveDigestCursor(ctx, inst.ID, now, latest)
}

func (s *Service) digestPayload(ctx context.Context, inst *Instance, total int, subjects []string) (url string, body []byte, err error) {
	def, ok := s.reg().Get(inst.Type)
	if !ok {
		return "", nil, fmt.Errorf("unknown type")
	}
	secrets, err := s.decryptSecrets(ctx, inst, def)
	if err != nil {
		return "", nil, err
	}
	cfg := inst.Config
	if cfg == nil {
		cfg = map[string]any{}
	}
	text := fmt.Sprintf("EmailBridge digest: %d new message(s)", total)
	if len(subjects) > 0 {
		text += "\n• " + strings.Join(subjects, "\n• ")
		if total > len(subjects) {
			text += fmt.Sprintf("\n… and %d more", total-len(subjects))
		}
	}
	switch inst.Type {
	case TypeTelegram:
		token := secrets["botToken"]
		chatID, _ := cfg["chatId"].(string)
		if token == "" || chatID == "" {
			return "", nil, fmt.Errorf("telegram config incomplete")
		}
		base, _ := cfg["_apiBase"].(string)
		if base == "" {
			base = "https://api.telegram.org"
		}
		url = strings.TrimRight(base, "/") + "/bot" + token + "/sendMessage"
		body, _ = json.Marshal(map[string]any{"chat_id": chatID, "text": text})
		return url, body, nil
	default:
		u, _ := cfg["webhookUrl"].(string)
		u = strings.TrimSpace(u)
		if u == "" {
			return "", nil, fmt.Errorf("webhookUrl required")
		}
		// Slack-friendly; Teams/Discord/Mattermost accept a top-level text/content field reasonably.
		payload := map[string]any{"text": text}
		if inst.Type == TypeDiscord {
			payload = map[string]any{"content": text}
		}
		if inst.Type == TypeTeams {
			payload = map[string]any{
				"@type": "MessageCard", "@context": "https://schema.org/extensions",
				"summary": "EmailBridge digest", "title": "New mail digest", "text": text,
			}
		}
		body, _ = json.Marshal(payload)
		return u, body, nil
	}
}
