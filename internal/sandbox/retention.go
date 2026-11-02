package sandbox

import (
	"context"
	"log/slog"
	"time"
)

const (
	retentionBatch   = 100
	defaultRetention = time.Hour
)

// RetentionDuty runs until ctx ends: deletes expired captures (S3 then Mongo). The TTL index is a
// safety net; this duty removes object storage first (EB_RETENTION_SANDBOX, S11).
func (s *Service) RetentionDuty(ctx context.Context) {
	every := s.RetentionEvery
	if every <= 0 {
		every = defaultRetention
	}
	t := time.NewTicker(every)
	defer t.Stop()
	s.SweepRetention(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.SweepRetention(ctx)
		}
	}
}

// SweepRetention runs one retention pass over expired capturedMessages.
func (s *Service) SweepRetention(ctx context.Context) {
	for {
		n, err := s.sweepBatch(ctx)
		if err != nil && ctx.Err() == nil {
			s.log().LogAttrs(ctx, slog.LevelWarn, "sandbox retention sweep", slog.Any("err", err))
			return
		}
		if n == 0 || ctx.Err() != nil {
			return
		}
	}
}

func (s *Service) sweepBatch(ctx context.Context) (int, error) {
	rows, err := s.Repo.expired(ctx, s.now(), retentionBatch)
	if err != nil {
		return 0, err
	}
	n := 0
	for i := range rows {
		c := &rows[i]
		if c.RawRef != "" && s.Objects != nil {
			if err := s.Objects.Delete(ctx, c.RawRef); err != nil {
				return n, err
			}
		}
		if err := s.Repo.deleteByID(ctx, c.ID); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}
