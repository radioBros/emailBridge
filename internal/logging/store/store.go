// Package store is the node-local log store (S12 §5): a logging.Sink that writes records into one SQLite
// file per category per UTC day under EB_LOG_DIR/<nodeId>/<category>/<YYYY-MM-DD>.db, the queries that
// read them, the retention and size-cap sweeper, GDPR erasure and NDJSON export, the live-tail hub, and
// the node's side of the eb.logs.query scatter-gather protocol (S12 §8). The cluster client and the HTTP
// API live in internal/logging/query.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/radiobros/emailbridge/internal/logging"
)

// SinkName is the logging.Sink name of the store.
const SinkName = "logstore"

const dayLayout = "2006-01-02"

// Options configure Open. No environment is read here; the wiring passes EB_LOG_DIR and the node ID.
type Options struct {
	// Dir is EB_LOG_DIR; the store writes under Dir/<Node>.
	Dir string
	// Node is this node's ID (one path segment).
	Node string
	// Settings returns the current log settings (retention, caps, disk cap). Default: the S12 defaults.
	Settings func() logging.Settings
	// Log receives one system-category record per deleted day file (S12 §5). Default: discard.
	Log *slog.Logger
	// IdleClose closes writer files unused for this long (default 2 min).
	IdleClose time.Duration
	// MaxOpen bounds the open writer files; the least recently used is closed first (default 32).
	MaxOpen int
	// MaxReaders bounds the cached read-only handles (default 64; each holds up to three file
	// descriptors: database, WAL and shared memory).
	MaxReaders int
	// SweepEvery is the sweeper period (default 10 min); negative disables the background sweeper
	// (Sweep can still be called directly).
	SweepEvery time.Duration
	// Now is the clock (tests).
	Now func() time.Time
}

// Store is the SQLite log store of one node.
type Store struct {
	o    Options
	root string

	// mu guards the writers and orders writes, erasure and deletions. Readers never take it, so queries
	// do not wait for a batch write (WAL lets them read while it runs).
	mu      sync.Mutex
	writers map[fileKey]*writer
	// rmu guards readers; lock order is mu, then rmu.
	rmu     sync.Mutex
	readers map[string]*reader // cached read-only handles per path
	closed  atomic.Bool

	stop   chan struct{}
	cancel context.CancelFunc // ends the background loops' context
	done   sync.WaitGroup
}

type fileKey struct {
	cat logging.Category
	day string
}

type writer struct {
	db   *sql.DB
	ins  *sql.Stmt
	last time.Time
	path string
}

var (
	nodeRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)
	dayRe  = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}\.db$`)
)

// ErrClosed is returned after Close.
var ErrClosed = errors.New("logstore: closed")

// Open validates the options, verifies FTS5 and starts the idle-close loop and (unless disabled) the
// sweeper.
func Open(ctx context.Context, o Options) (*Store, error) {
	if strings.TrimSpace(o.Dir) == "" {
		return nil, errors.New("logstore: EB_LOG_DIR is empty")
	}
	if strings.ContainsAny(o.Dir, "?#") {
		return nil, fmt.Errorf("logstore: log directory %q must not contain '?' or '#'", o.Dir)
	}
	if !nodeRe.MatchString(o.Node) {
		return nil, fmt.Errorf("logstore: node ID %q is not a valid path segment", o.Node)
	}
	if o.Settings == nil {
		o.Settings = logging.DefaultSettings
	}
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	if o.IdleClose <= 0 {
		o.IdleClose = 2 * time.Minute
	}
	if o.MaxOpen <= 0 {
		o.MaxOpen = 32
	}
	if o.MaxReaders <= 0 {
		o.MaxReaders = 64
	}
	if o.SweepEvery == 0 {
		o.SweepEvery = 10 * time.Minute
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if err := checkFTS5(ctx); err != nil {
		return nil, err
	}
	root := filepath.Join(o.Dir, o.Node)
	if err := os.MkdirAll(root, 0o750); err != nil {
		return nil, fmt.Errorf("logstore: create %s: %w", root, err)
	}
	// the loops outlive the start-up context; Close ends them
	loopCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	s := &Store{o: o, root: root, writers: map[fileKey]*writer{}, readers: map[string]*reader{}, stop: make(chan struct{}), cancel: cancel}
	s.done.Add(1)
	go s.idleLoop()
	if o.SweepEvery > 0 {
		s.done.Add(1)
		go s.sweepLoop(loopCtx)
	}
	return s, nil
}

// Node returns the node ID the store belongs to.
func (s *Store) Node() string { return s.o.Node }

// Name implements logging.Sink.
func (s *Store) Name() string { return SinkName }

// Close stops the background loops and closes every open file.
func (s *Store) Close() error {
	s.mu.Lock()
	if s.closed.Load() {
		s.mu.Unlock()
		return nil
	}
	s.closed.Store(true)
	close(s.stop)
	s.cancel()
	s.mu.Unlock()
	s.done.Wait()
	s.mu.Lock()
	defer s.mu.Unlock()
	var errs []error
	for k, w := range s.writers {
		errs = append(errs, w.close())
		delete(s.writers, k)
	}
	s.rmu.Lock()
	defer s.rmu.Unlock()
	for p, r := range s.readers {
		if r.refs == 0 { // a reader still in use is closed by its release
			errs = append(errs, r.db.Close())
		}
		delete(s.readers, p)
	}
	return errors.Join(errs...)
}

func (w *writer) close() error {
	return errors.Join(w.ins.Close(), w.db.Close())
}

func dayOf(t time.Time) string { return t.UTC().Format(dayLayout) }

// path returns the file of a category and day. cat must be valid and day must match dayLayout; both
// come from validated values only.
func (s *Store) path(cat logging.Category, day string) string {
	return filepath.Join(s.root, string(cat), day+".db")
}

// Write implements logging.Sink: records are grouped by (category, day) and each group is inserted in one
// transaction. Writes are serialised (the log system has one writer goroutine; the lock also orders them
// against the sweeper and erasure).
func (s *Store) Write(ctx context.Context, batch []logging.Record) error {
	if len(batch) == 0 {
		return nil
	}
	groups := map[fileKey][]LogRecord{}
	var order []fileKey
	for i := range batch {
		r := FromRecord(&batch[i])
		k := fileKey{cat: logging.Category(r.Category), day: dayOf(time.Unix(0, r.Time))}
		if _, ok := groups[k]; !ok {
			order = append(order, k)
		}
		groups[k] = append(groups[k], r)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed.Load() {
		return ErrClosed
	}
	var errs []error
	for _, k := range order {
		if err := s.insertLocked(ctx, k, groups[k]); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (s *Store) insertLocked(ctx context.Context, k fileKey, recs []LogRecord) error {
	w, err := s.writerLocked(ctx, k)
	if err != nil {
		return err
	}
	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("logstore: begin %s/%s: %w", k.cat, k.day, err)
	}
	stmt := tx.StmtContext(ctx, w.ins)
	for i := range recs {
		if _, err := stmt.ExecContext(ctx, insertArgs(&recs[i])...); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("logstore: insert %s/%s: %w", k.cat, k.day, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("logstore: commit %s/%s: %w", k.cat, k.day, err)
	}
	return nil
}

// writerLocked returns the open writer of k, opening (and creating) the file when needed.
func (s *Store) writerLocked(ctx context.Context, k fileKey) (*writer, error) {
	if w, ok := s.writers[k]; ok {
		w.last = s.o.Now()
		return w, nil
	}
	if !k.cat.Valid() {
		return nil, fmt.Errorf("logstore: unknown category %q", k.cat)
	}
	if _, err := time.Parse(dayLayout, k.day); err != nil {
		return nil, fmt.Errorf("logstore: bad day %q", k.day)
	}
	if len(s.writers) >= s.o.MaxOpen {
		s.evictLRULocked()
	}
	p := s.path(k.cat, k.day)
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		return nil, fmt.Errorf("logstore: create %s: %w", filepath.Dir(p), err)
	}
	db, err := sql.Open("sqlite", p+writerPragmas)
	if err != nil {
		return nil, fmt.Errorf("logstore: open %s: %w", p, err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := createSchema(ctx, db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("%w (%s)", err, p)
	}
	ins, err := db.PrepareContext(ctx, insertSQL)
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("logstore: prepare insert %s: %w", p, err)
	}
	w := &writer{db: db, ins: ins, last: s.o.Now(), path: p}
	s.writers[k] = w
	return w, nil
}

func (s *Store) evictLRULocked() {
	var oldest fileKey
	var found bool
	var t time.Time
	for k, w := range s.writers {
		if !found || w.last.Before(t) {
			oldest, t, found = k, w.last, true
		}
	}
	if found {
		_ = s.writers[oldest].close()
		delete(s.writers, oldest)
	}
}

func (s *Store) idleLoop() {
	defer s.done.Done()
	every := s.o.IdleClose / 2
	if every > 30*time.Second {
		every = 30 * time.Second
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-t.C:
			s.CloseIdle()
		}
	}
}

// CloseIdle closes writer files and cached readers unused for at least IdleClose. A writer checkpoints
// its WAL into the database first, so a closed day file does not keep a WAL file around.
func (s *Store) CloseIdle() {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.o.Now()
	for k, w := range s.writers {
		if now.Sub(w.last) >= s.o.IdleClose {
			_, _ = w.db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
			_ = w.close()
			delete(s.writers, k)
		}
	}
	s.rmu.Lock()
	defer s.rmu.Unlock()
	for p, r := range s.readers {
		if r.refs == 0 && now.Sub(r.last) >= s.o.IdleClose {
			_ = r.db.Close()
			delete(s.readers, p)
		}
	}
}

// OpenFiles returns the number of open writer files (metrics, tests).
func (s *Store) OpenFiles() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.writers)
}

// reader is a cached read-only handle on one day file. Opening a file costs far more than querying it
// (schema load, pragmas), so handles stay open while used recently.
type reader struct {
	db   *sql.DB
	refs int
	last time.Time
}

// openReader returns a read-only handle on an existing day file; ok is false when the file does not
// exist. Handles use a mode=ro URI, so a query never creates a file, and are registered under rmu before
// the existence check, so the sweeper cannot delete a file while it is being read. release returns the
// handle to the cache.
func (s *Store) openReader(cat logging.Category, day string) (db *sql.DB, release func(), ok bool, err error) {
	p := s.path(cat, day)
	if s.closed.Load() {
		return nil, nil, false, ErrClosed
	}
	s.rmu.Lock()
	defer s.rmu.Unlock()
	r, cached := s.readers[p]
	if !cached {
		if _, err := os.Stat(p); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil, nil, false, nil
			}
			return nil, nil, false, fmt.Errorf("logstore: stat %s: %w", p, err)
		}
		db, err := sql.Open("sqlite", readerDSN(p))
		if err != nil {
			return nil, nil, false, fmt.Errorf("logstore: open %s: %w", p, err)
		}
		db.SetMaxOpenConns(1)
		db.SetMaxIdleConns(1)
		for len(s.readers) >= s.o.MaxReaders && s.evictReaderLocked() {
		}
		r = &reader{db: db}
		s.readers[p] = r
	}
	r.refs++
	r.last = s.o.Now()
	return r.db, func() { s.releaseReader(p, r) }, true, nil
}

// readerDSN is the read-only URI of a day file ('?' and '#' are refused in the log directory; '%' is
// escaped).
func readerDSN(p string) string {
	return "file:" + strings.ReplaceAll(filepath.ToSlash(p), "%", "%25") + readerPragmas
}

func (s *Store) releaseReader(p string, r *reader) {
	s.rmu.Lock()
	defer s.rmu.Unlock()
	r.refs--
	r.last = s.o.Now()
	if s.readers[p] != r && r.refs <= 0 { // evicted or closed while in use: close it now
		_ = r.db.Close()
	}
}

// evictReaderLocked closes the least recently used idle reader (rmu held); false when every cached
// reader is in use.
func (s *Store) evictReaderLocked() bool {
	var oldest string
	var t time.Time
	for p, r := range s.readers {
		if r.refs == 0 && (oldest == "" || r.last.Before(t)) {
			oldest, t = p, r.last
		}
	}
	if oldest == "" {
		return false
	}
	_ = s.readers[oldest].db.Close()
	delete(s.readers, oldest)
	return true
}

// OpenReaders returns the number of cached reader handles (metrics, tests).
func (s *Store) OpenReaders() int {
	s.rmu.Lock()
	defer s.rmu.Unlock()
	return len(s.readers)
}

// dayFile is one day file on disk.
type dayFile struct {
	cat   logging.Category
	day   string
	bytes int64 // db + wal + shm
}

// days lists a category's day files, oldest first.
func (s *Store) days(cat logging.Category) ([]dayFile, error) {
	dir := filepath.Join(s.root, string(cat))
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("logstore: list %s: %w", dir, err)
	}
	var out []dayFile
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !dayRe.MatchString(name) {
			continue
		}
		day := strings.TrimSuffix(name, ".db")
		if _, err := time.Parse(dayLayout, day); err != nil {
			continue
		}
		out = append(out, dayFile{cat: cat, day: day, bytes: fileBytes(filepath.Join(dir, name))})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].day < out[j].day })
	return out, nil
}

func fileBytes(p string) int64 {
	var n int64
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if fi, err := os.Stat(p + suffix); err == nil {
			n += fi.Size()
		}
	}
	return n
}

// ErrFileBusy means a day file is being read and cannot be deleted now; the sweeper retries next run.
var ErrFileBusy = errors.New("logstore: day file in use")

// removeDay deletes one day file and its WAL/SHM siblings, closing its writer first.
func (s *Store) removeDay(cat logging.Category, day string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	// rmu is held through the removal so no reader registers on a file being deleted
	s.rmu.Lock()
	defer s.rmu.Unlock()
	p := s.path(cat, day)
	if r, ok := s.readers[p]; ok {
		if r.refs > 0 {
			return ErrFileBusy
		}
		_ = r.db.Close()
		delete(s.readers, p)
	}
	k := fileKey{cat: cat, day: day}
	if w, ok := s.writers[k]; ok {
		_ = w.close()
		delete(s.writers, k)
	}
	var errs []error
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if err := os.Remove(p + suffix); err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, fmt.Errorf("logstore: remove %s: %w", p+suffix, err))
		}
	}
	return errors.Join(errs...)
}

// DiskUsage returns the bytes on disk per category (all days).
func (s *Store) DiskUsage() (map[logging.Category]int64, error) {
	out := map[logging.Category]int64{}
	for _, c := range logging.Categories {
		files, err := s.days(c)
		if err != nil {
			return nil, err
		}
		for _, f := range files {
			out[c] += f.bytes
		}
	}
	return out, nil
}

// DiskCap returns the configured total log disk cap in bytes (S12 §6 / §9).
func (s *Store) DiskCap() int64 {
	if s == nil || s.o.Settings == nil {
		return 0
	}
	return s.o.Settings().DiskCap
}

// MetricDiskUsage is DiskUsage with string category labels for Prometheus.
func (s *Store) MetricDiskUsage() (map[string]int64, error) {
	raw, err := s.DiskUsage()
	if err != nil {
		return nil, err
	}
	out := make(map[string]int64, len(raw))
	for c, n := range raw {
		out[string(c)] = n
	}
	return out, nil
}
