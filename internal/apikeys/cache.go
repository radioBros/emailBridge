package apikeys

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/radiobros/emailbridge/internal/bus"
)

// Cache is the eb-cache surface used to purge Limen's verified-key entries (S10 §3).
type Cache interface {
	CacheDelete(ctx context.Context, key string) error
}

var _ Cache = (*bus.Bus)(nil)

// verifiedCacheKey is Limen's api-key store key: "{prefix}:api-key:{keyHash}".
func verifiedCacheKey(keyHash string) string {
	return fmt.Sprintf("%s:api-key:%s", limenCachePrefix, keyHash)
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func (s *Service) purgeVerifiedCache(ctx context.Context, keyHash string) {
	if s.Cache == nil || keyHash == "" {
		return
	}
	if err := s.Cache.CacheDelete(ctx, verifiedCacheKey(keyHash)); err != nil {
		s.log.WarnContext(ctx, "api key cache purge failed", "err", err)
	}
}

// touchLastUsed updates lastUsedAt at most once per minute.
func (s *Service) touchLastUsed(ctx context.Context, id string, prev *time.Time) {
	now := time.Now().UTC()
	if prev != nil && now.Sub(*prev) < time.Minute {
		return
	}
	_, _ = s.DB.Collection(Collection).UpdateOne(ctx,
		bson.M{"_id": id},
		bson.M{"$set": bson.M{"lastUsedAt": now}},
	)
}
