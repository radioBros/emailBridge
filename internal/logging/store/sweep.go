package store

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/radiobros/emailbridge/internal/logging"
)

// Deletion reasons recorded with every deleted day file.
const (
	ReasonRetention   = "retention"
	ReasonCategoryCap = "category_cap"
	ReasonDiskCap     = "disk_cap"
)

// Deletion is one day file removed by the sweeper.
type Deletion struct {
	Category logging.Category
	Day      string
	Bytes    int64
	Reason   string
}

func (s *Store) sweepLoop(ctx context.Context) {
	defer s.done.Done()
	t := time.NewTicker(s.o.SweepEvery)
	defer t.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-t.C:
			sctx, cancel := context.WithTimeout(ctx, s.o.SweepEvery)
			if _, err := s.Sweep(sctx); err != nil {
				s.o.Log.WarnContext(sctx, "log sweep incomplete", "err", err)
			}
			cancel()
		}
	}
}

// Sweep enforces retention, the per-category caps and the node disk cap (S12 §5):
//
//  1. day files whose whole day is older than the category's retention are deleted;
//  2. while a category exceeds its cap, its oldest day files are deleted;
//  3. while the node total exceeds DiskCap, categories lose their oldest days in logging.EvictionOrder().
//
// The current UTC day is never deleted by a cap (it holds the records being written). Every deletion
// writes a system info record. Files being read are skipped and retried on the next sweep.
func (s *Store) Sweep(ctx context.Context) ([]Deletion, error) {
	st := s.o.Settings()
	now := s.o.Now().UTC()
	today := dayOf(now)
	files := map[logging.Category][]dayFile{}
	var deleted []Deletion
	var errs []error
	del := func(f dayFile, reason string) bool {
		if err := ctx.Err(); err != nil {
			errs = append(errs, err)
			return false
		}
		if err := s.removeDay(f.cat, f.day); err != nil {
			errs = append(errs, err)
			return false
		}
		d := Deletion{Category: f.cat, Day: f.day, Bytes: f.bytes, Reason: reason}
		deleted = append(deleted, d)
		s.o.Log.LogAttrs(ctx, slog.LevelInfo, "log day file deleted", slog.String("logCategory", string(f.cat)),
			slog.String("day", f.day), slog.Int64("bytes", f.bytes), slog.String("reason", reason))
		return true
	}
	// 1. retention
	for _, c := range logging.Categories {
		list, err := s.days(c)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		cs, ok := st.Categories[c]
		var keep []dayFile
		for _, f := range list {
			if ok && cs.Retention > 0 && f.day < today {
				start, _ := time.Parse(dayLayout, f.day)
				if !start.Add(24*time.Hour).After(now.Add(-cs.Retention)) && del(f, ReasonRetention) {
					continue
				}
			}
			keep = append(keep, f)
		}
		files[c] = keep
	}
	// 2. per-category caps
	var total int64
	for _, c := range logging.Categories {
		cs := st.Categories[c]
		files[c] = evictOldest(files[c], today, cs.Cap, ReasonCategoryCap, del)
		total += sum(files[c])
	}
	// 3. node disk cap, lowest-priority categories first
	if st.DiskCap > 0 && total > st.DiskCap {
		for _, c := range logging.EvictionOrder() {
			before := sum(files[c])
			files[c] = evictOldest(files[c], today, before-(total-st.DiskCap), ReasonDiskCap, del)
			total -= before - sum(files[c])
			if total <= st.DiskCap {
				break
			}
		}
	}
	return deleted, errors.Join(errs...)
}

// evictOldest deletes the oldest files (never today's) while the list's total exceeds limit. limit <= 0
// on a category cap means "no cap"; the disk-cap caller passes the bytes the category may keep.
func evictOldest(list []dayFile, today string, limit int64, reason string, del func(dayFile, string) bool) []dayFile {
	if reason == ReasonCategoryCap && limit <= 0 {
		return list
	}
	if limit < 0 {
		limit = 0
	}
	total := sum(list)
	var keep []dayFile
	for i, f := range list {
		if total > limit && f.day < today && del(f, reason) {
			total -= f.bytes
			continue
		}
		keep = append(keep, list[i])
	}
	return keep
}

func sum(list []dayFile) int64 {
	var n int64
	for _, f := range list {
		n += f.bytes
	}
	return n
}
