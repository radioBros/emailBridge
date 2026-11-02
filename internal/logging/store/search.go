package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/radiobros/emailbridge/internal/logging"
)

// MaxSpan bounds the days one local search may touch (the API enforces 31 d; this guards direct use).
const MaxSpan = 62 * 24 * time.Hour

// MaxLimit bounds the records one local search returns.
const MaxLimit = 1000

// ErrBadFilter is returned for filters the store cannot execute (span, limit, level, FTS syntax).
var ErrBadFilter = errors.New("logstore: invalid filter")

// daysDesc lists the UTC days that overlap [from, to), newest first.
func daysDesc(from, to time.Time) []string {
	if !to.After(from) {
		return nil
	}
	first := from.UTC().Truncate(24 * time.Hour)
	last := to.UTC().Add(-time.Nanosecond).Truncate(24 * time.Hour)
	var out []string
	for d := last; !d.Before(first); d = d.Add(-24 * time.Hour) {
		out = append(out, d.Format(dayLayout))
	}
	return out
}

func (f *Filter) bounds() (from, to int64, err error) {
	if f.From.IsZero() || f.To.IsZero() {
		return 0, 0, fmt.Errorf("%w: from and to are required", ErrBadFilter)
	}
	if f.To.Sub(f.From) > MaxSpan {
		return 0, 0, fmt.Errorf("%w: span exceeds %v", ErrBadFilter, MaxSpan)
	}
	from, to = f.From.UTC().UnixNano(), f.To.UTC().UnixNano()
	if f.Before != nil && f.Before.Time+1 < to {
		to = f.Before.Time + 1 // records at Before.Time with a smaller id still qualify
	}
	return from, to, nil
}

// where builds the shared WHERE clause (constant SQL fragments, user values only as parameters).
func (f *Filter) where(from, to int64, keyset bool) (string, []any, error) {
	var b strings.Builder
	args := []any{from, to}
	b.WriteString(" WHERE time >= ? AND time < ?")
	ml, err := f.minLevel()
	if err != nil {
		return "", nil, fmt.Errorf("%w: %w", ErrBadFilter, err)
	}
	if ml != nil {
		b.WriteString(" AND lvl >= ?")
		args = append(args, int64(*ml))
	}
	for _, c := range [...]struct {
		clause string
		v      string
	}{{" AND organizationId = ?", f.OrgID}, {" AND accountId = ?", f.AccountID}, {" AND requestId = ?", f.RequestID},
		{" AND eventId = ?", f.EventID}, {" AND deliveryId = ?", f.DeliveryID}, {" AND outboundId = ?", f.OutboundID},
		{" AND traceId = ?", f.TraceID}} {
		if c.v != "" {
			b.WriteString(c.clause)
			args = append(args, c.v)
		}
	}
	if keyset && f.Before != nil {
		b.WriteString(" AND (time < ? OR (time = ? AND id < ?))")
		args = append(args, f.Before.Time, f.Before.Time, f.Before.ID)
	}
	if expr, ok := FTSQuery(f.Q); ok {
		b.WriteString(" AND seq IN (SELECT rowid FROM records_fts WHERE records_fts MATCH ?)")
		args = append(args, expr)
	}
	return b.String(), args, nil
}

// Search returns up to f.Limit records matching f, newest first by (time, id), reading only the day
// files of the selected categories that overlap the range. Day files partition time, so the search
// stops at the first day after which enough records were found.
func (s *Store) Search(ctx context.Context, f Filter) ([]LogRecord, error) {
	limit := f.Limit
	if limit <= 0 || limit > MaxLimit {
		return nil, fmt.Errorf("%w: limit must be 1..%d", ErrBadFilter, MaxLimit)
	}
	from, to, err := f.bounds()
	if err != nil {
		return nil, err
	}
	where, args, err := f.where(from, to, true)
	if err != nil {
		return nil, err
	}
	var b strings.Builder
	b.WriteString(selectCols)
	b.WriteString(" FROM records")
	b.WriteString(where)
	b.WriteString(" ORDER BY time DESC, id DESC LIMIT ?")
	q := b.String()
	args = append(args, limit)
	out := make([]LogRecord, 0, min(limit, 64))
	cats := f.categories()
	for _, day := range daysDesc(time.Unix(0, from), time.Unix(0, to)) {
		perCat := make([][]LogRecord, len(cats))
		jobs := make([]func() error, len(cats))
		for i, cat := range cats {
			jobs[i] = func() error {
				recs, err := s.queryFile(ctx, cat, day, q, args)
				perCat[i] = recs
				return err
			}
		}
		if err := runBounded(jobs); err != nil {
			return nil, err
		}
		var dayRecs []LogRecord
		for _, recs := range perCat {
			dayRecs = append(dayRecs, recs...)
		}
		SortNewestFirst(dayRecs)
		out = append(out, dayRecs...)
		if len(out) >= limit {
			return out[:limit], nil
		}
	}
	return out, nil
}

// fileParallelism bounds the day files one query reads at the same time.
const fileParallelism = 8

// runBounded runs jobs with at most fileParallelism at a time and returns their errors joined.
func runBounded(jobs []func() error) error {
	sem := make(chan struct{}, fileParallelism)
	errs := make([]error, len(jobs))
	var wg sync.WaitGroup
	for i, job := range jobs {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			errs[i] = job()
		}()
	}
	wg.Wait()
	return errors.Join(errs...)
}

// SortNewestFirst orders records by (time, id) descending.
func SortNewestFirst(recs []LogRecord) {
	sort.Slice(recs, func(i, j int) bool { return recs[j].Pos().Before(recs[i].Pos()) })
}

func (s *Store) queryFile(ctx context.Context, cat logging.Category, day, q string, args []any) ([]LogRecord, error) {
	db, release, ok, err := s.openReader(cat, day)
	if err != nil || !ok {
		return nil, err
	}
	defer release()
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		if fresh(err) {
			return nil, nil
		}
		if strings.Contains(err.Error(), "fts5") {
			return nil, fmt.Errorf("%w: search text: %w", ErrBadFilter, err)
		}
		return nil, fmt.Errorf("logstore: query %s/%s: %w", cat, day, err)
	}
	defer func() { _ = rows.Close() }()
	var out []LogRecord
	for rows.Next() {
		r, err := scanRecord(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("logstore: read %s/%s: %w", cat, day, err)
	}
	return out, nil
}

// fresh reports whether err comes from a day file whose schema is still being created by its first
// write; such a file has no records yet.
func fresh(err error) bool { return strings.Contains(err.Error(), "no such table") }

// IDTime returns the time embedded in a log_<ulid> ID.
func IDTime(id string) (time.Time, error) {
	raw, ok := strings.CutPrefix(id, "log_")
	if !ok {
		return time.Time{}, fmt.Errorf("%w: %q is not a log ID", ErrBadFilter, id)
	}
	u, err := ulid.ParseStrict(strings.ToUpper(raw))
	if err != nil {
		return time.Time{}, fmt.Errorf("%w: %q is not a log ID", ErrBadFilter, id)
	}
	return ulid.Time(u.Time()).UTC(), nil
}

// Get finds one record by ID in the day file its ULID time points to. It returns nil when the record
// is not on this node.
func (s *Store) Get(ctx context.Context, id string) (*LogRecord, error) {
	t, err := IDTime(id)
	if err != nil {
		return nil, err
	}
	day := dayOf(t)
	q := selectCols + " FROM records WHERE id = ?"
	for _, cat := range logging.Categories {
		recs, err := s.queryFile(ctx, cat, day, q, []any{id})
		if err != nil {
			return nil, err
		}
		if len(recs) > 0 {
			return &recs[0], nil
		}
	}
	return nil, nil
}

// Stats are a node's counts for a range and its disk usage.
type Stats struct {
	ByLevel    map[string]int64 `json:"byLevel"`
	ByCategory map[string]int64 `json:"byCategory"`
	Disk       map[string]int64 `json:"disk"` // bytes per category, all days
}

// Stats counts records by level and category in [f.From, f.To) (honouring f.OrgID and the other
// filters) and reports disk bytes per category.
func (s *Store) Stats(ctx context.Context, f Filter) (*Stats, error) {
	f.Before = nil
	from, to, err := f.bounds()
	if err != nil {
		return nil, err
	}
	where, args, err := f.where(from, to, false)
	if err != nil {
		return nil, err
	}
	q := "SELECT level, COUNT(*) FROM records" + where + " GROUP BY level"
	st := &Stats{ByLevel: map[string]int64{}, ByCategory: map[string]int64{}, Disk: map[string]int64{}}
	var mu sync.Mutex
	var jobs []func() error
	for _, day := range daysDesc(time.Unix(0, from), time.Unix(0, to)) {
		for _, cat := range f.categories() {
			jobs = append(jobs, func() error {
				part := &Stats{ByLevel: map[string]int64{}, ByCategory: map[string]int64{}}
				if err := s.countFile(ctx, cat, day, q, args, part); err != nil {
					return err
				}
				mu.Lock()
				defer mu.Unlock()
				for k, v := range part.ByLevel {
					st.ByLevel[k] += v
				}
				for k, v := range part.ByCategory {
					st.ByCategory[k] += v
				}
				return nil
			})
		}
	}
	if err := runBounded(jobs); err != nil {
		return nil, err
	}
	disk, err := s.DiskUsage()
	if err != nil {
		return nil, err
	}
	for c, n := range disk {
		if n > 0 {
			st.Disk[string(c)] = n
		}
	}
	return st, nil
}

func (s *Store) countFile(ctx context.Context, cat logging.Category, day, q string, args []any, st *Stats) error {
	db, release, ok, err := s.openReader(cat, day)
	if err != nil || !ok {
		return err
	}
	defer release()
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		if fresh(err) {
			return nil
		}
		if strings.Contains(err.Error(), "fts5") {
			return fmt.Errorf("%w: search text: %w", ErrBadFilter, err)
		}
		return fmt.Errorf("logstore: count %s/%s: %w", cat, day, err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var level string
		var n int64
		if err := rows.Scan(&level, &n); err != nil {
			return fmt.Errorf("logstore: count %s/%s: %w", cat, day, err)
		}
		st.ByLevel[level] += n
		st.ByCategory[string(cat)] += n
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("logstore: count %s/%s: %w", cat, day, err)
	}
	return nil
}

// eraseKeep are the categories whose records keep the account ID on erasure (S11 §5.1).
var eraseKeep = map[logging.Category]bool{logging.CatAudit: true, logging.CatSecurity: true, logging.CatCrash: true}

// Erase deletes every record carrying accountID in every category except audit, security and crash
// (GDPR account erasure, S11 §5.1) and returns the number of deleted records. It is idempotent.
func (s *Store) Erase(ctx context.Context, accountID string) (int64, error) {
	if strings.TrimSpace(accountID) == "" {
		return 0, fmt.Errorf("%w: account ID is required", ErrBadFilter)
	}
	var total int64
	for _, cat := range logging.Categories {
		if eraseKeep[cat] {
			continue
		}
		files, err := s.days(cat)
		if err != nil {
			return total, err
		}
		for _, f := range files {
			n, err := s.eraseFile(ctx, cat, f.day, accountID)
			total += n
			if err != nil {
				return total, err
			}
		}
	}
	return total, nil
}

func (s *Store) eraseFile(ctx context.Context, cat logging.Category, day, accountID string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed.Load() {
		return 0, ErrClosed
	}
	w, err := s.writerLocked(ctx, fileKey{cat: cat, day: day})
	if err != nil {
		return 0, err
	}
	res, err := w.db.ExecContext(ctx, `DELETE FROM records WHERE accountId = ?`, accountID)
	if err != nil {
		return 0, fmt.Errorf("logstore: erase %s/%s: %w", cat, day, err)
	}
	n, _ := res.RowsAffected()
	if n > 0 {
		if _, err := w.db.ExecContext(ctx, `PRAGMA incremental_vacuum`); err != nil {
			return n, fmt.Errorf("logstore: vacuum %s/%s: %w", cat, day, err)
		}
	}
	return n, nil
}

// exportPage is the page size of Export.
const exportPage = 500

// Export writes every record matching f (newest first) to w as NDJSON, one LogRecord per line, and
// returns the number of records written. f.Limit and f.Before are ignored. The caller adds compression.
func (s *Store) Export(ctx context.Context, f Filter, w io.Writer) (int64, error) {
	f.Before = nil
	f.Limit = exportPage
	enc := json.NewEncoder(w)
	var n int64
	for {
		recs, err := s.Search(ctx, f)
		if err != nil {
			return n, err
		}
		for i := range recs {
			if err := enc.Encode(&recs[i]); err != nil {
				return n, fmt.Errorf("logstore: export write: %w", err)
			}
			n++
		}
		if len(recs) < exportPage {
			return n, nil
		}
		p := recs[len(recs)-1].Pos()
		f.Before = &p
	}
}

// ExportDay exports one UTC day of the given categories (all when empty), e.g. for the S3 archive.
func (s *Store) ExportDay(ctx context.Context, day time.Time, cats []logging.Category, w io.Writer) (int64, error) {
	start := day.UTC().Truncate(24 * time.Hour)
	return s.Export(ctx, Filter{From: start, To: start.Add(24 * time.Hour), Categories: cats}, w)
}
