package logging

import (
	"context"
	crand "crypto/rand"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/oklog/ulid/v2"
)

// ErrorClassifier extracts an error's kind and problem code (S2 §5). It is injected by the binary's
// wiring so this package does not import internal/errs.
type ErrorClassifier func(err error) (kind, code string)

// Options configure a System.
type Options struct {
	Node     string
	Role     string
	Settings Settings
	// HashKey is the instance key for address hashing (derived from EB_AUTH_SECRET by the wiring).
	HashKey []byte
	// BufferSize is the bounded queue size (default 65,536 records).
	BufferSize int
	// BatchSize and FlushEvery control batching (defaults 1,000 records / 200 ms).
	BatchSize  int
	FlushEvery time.Duration
	// Stdout receives the optional mirror (default os.Stdout).
	Stdout io.Writer
	// Fallback receives sink errors (default os.Stderr). It is rate-limited.
	Fallback   io.Writer
	Classifier ErrorClassifier
}

// System is the log system: the factory for loggers and the asynchronous writer to the sinks.
type System struct {
	o        Options
	settings atomic.Pointer[Settings]
	sinks    atomic.Pointer[[]Sink]
	stdout   *stdoutSink

	ch      chan Record
	flushCh chan chan struct{}
	stop    chan struct{}
	done    chan struct{}
	closed  atomic.Bool

	counters counters

	entropyMu sync.Mutex
	entropy   *ulid.MonotonicEntropy

	fallbackMu   sync.Mutex
	lastFallback time.Time
}

const (
	nLevels     = 6  // trace, debug, info, warn, error, fatal
	nCategories = 15 // len(Categories); checked by a test
)

func levelIndex(l slog.Level) int {
	switch LevelName(l) {
	case "trace":
		return 0
	case "debug":
		return 1
	case "info":
		return 2
	case "warn":
		return 3
	case "error":
		return 4
	default:
		return 5
	}
}

var levelNames = [nLevels]string{"trace", "debug", "info", "warn", "error", "fatal"}

type counters struct {
	records       [nCategories][nLevels]atomic.Uint64
	dropped       [nCategories][nLevels]atomic.Uint64
	reported      [nCategories][nLevels]uint64 // touched only by the writer goroutine
	sinkErrors    atomic.Uint64
	uncategorised atomic.Uint64
}

func catIndex(c Category) int {
	for i, k := range Categories {
		if k == c {
			return i
		}
	}
	return 0 // system
}

// New starts a System. Call Close to flush and stop it.
func New(o Options) *System {
	if o.BufferSize <= 0 {
		o.BufferSize = 65536
	}
	if o.BatchSize <= 0 {
		o.BatchSize = 1000
	}
	if o.FlushEvery <= 0 {
		o.FlushEvery = 200 * time.Millisecond
	}
	if o.Stdout == nil {
		o.Stdout = os.Stdout
	}
	if o.Fallback == nil {
		o.Fallback = os.Stderr
	}
	if o.Settings.Categories == nil {
		o.Settings = DefaultSettings()
	}
	s := &System{
		o:       o,
		ch:      make(chan Record, o.BufferSize),
		flushCh: make(chan chan struct{}),
		stop:    make(chan struct{}),
		done:    make(chan struct{}),
		entropy: ulid.Monotonic(crand.Reader, 0),
	}
	s.stdout = &stdoutSink{w: o.Stdout}
	st := o.Settings.clone()
	s.settings.Store(&st)
	s.stdout.setFormat(st.Stdout)
	empty := []Sink{}
	s.sinks.Store(&empty)
	go s.run()
	return s
}

// For returns a logger for category c and component (e.g. "sync.idle"). An unknown category is logged
// as system and counted as uncategorised.
func (s *System) For(c Category, component string) *slog.Logger {
	if !c.Valid() {
		return slog.New(&handler{s: s, cat: CatSystem, component: component, uncategorised: true})
	}
	return slog.New(&handler{s: s, cat: c, component: component})
}

// Default returns the logger to install with slog.SetDefault: records logged through the plain slog
// API land in the system category and are counted as uncategorised (S12 §2).
func (s *System) Default() *slog.Logger {
	return slog.New(&handler{s: s, cat: CatSystem, component: "default", uncategorised: true})
}

// Apply replaces the runtime settings (S12 §6); it takes effect for the next record.
func (s *System) Apply(st Settings) {
	c := st.clone()
	s.settings.Store(&c)
	s.stdout.setFormat(c.Stdout)
}

// Settings returns the current settings.
func (s *System) Settings() Settings { return s.settings.Load().clone() }

// AddSink attaches a sink (the SQLite store attaches itself when it starts).
func (s *System) AddSink(k Sink) {
	for {
		old := s.sinks.Load()
		next := append(append([]Sink{}, *old...), k)
		if s.sinks.CompareAndSwap(old, &next) {
			return
		}
	}
}

// RemoveSink detaches a sink by name.
func (s *System) RemoveSink(name string) {
	for {
		old := s.sinks.Load()
		var next []Sink
		for _, k := range *old {
			if k.Name() != name {
				next = append(next, k)
			}
		}
		if s.sinks.CompareAndSwap(old, &next) {
			return
		}
	}
}

func (s *System) newID(t time.Time) string {
	s.entropyMu.Lock()
	id := ulid.MustNew(ulid.Timestamp(t), s.entropy)
	s.entropyMu.Unlock()
	return "log_" + strings.ToLower(id.String())
}

// push enqueues a record without ever blocking (S12 §2 back-pressure).
func (s *System) push(r Record) {
	ci, li := catIndex(r.Category), levelIndex(r.Level)
	if s.closed.Load() {
		s.counters.dropped[ci][li].Add(1)
		return
	}
	fill := float64(len(s.ch)) / float64(cap(s.ch))
	switch {
	case fill >= 0.95 && r.Level < LevelWarn,
		fill >= 0.80 && r.Level < LevelInfo:
		s.counters.dropped[ci][li].Add(1)
		return
	}
	select {
	case s.ch <- r:
		s.counters.records[ci][li].Add(1)
	default:
		s.counters.dropped[ci][li].Add(1)
	}
}

func (s *System) run() {
	defer close(s.done)
	batch := make([]Record, 0, s.o.BatchSize)
	ticker := time.NewTicker(s.o.FlushEvery)
	defer ticker.Stop()
	flush := func() {
		if d := s.dropReport(); d != nil {
			batch = append(batch, *d)
		}
		if len(batch) == 0 {
			return
		}
		s.write(batch)
		batch = batch[:0]
	}
	for {
		select {
		case r := <-s.ch:
			batch = append(batch, r)
			if len(batch) >= s.o.BatchSize {
				flush()
			}
		case <-ticker.C:
			flush()
		case ack := <-s.flushCh:
			s.drainInto(&batch, flush)
			flush()
			close(ack)
		case <-s.stop:
			s.drainInto(&batch, flush)
			flush()
			return
		}
	}
}

func (s *System) drainInto(batch *[]Record, flush func()) {
	for {
		select {
		case r := <-s.ch:
			*batch = append(*batch, r)
			if len(*batch) >= s.o.BatchSize {
				flush()
			}
		default:
			return
		}
	}
}

func (s *System) write(batch []Record) {
	if s.stdout.enabled() {
		_ = s.stdout.Write(context.Background(), batch)
	}
	for _, k := range *s.sinks.Load() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err := k.Write(ctx, batch)
		cancel()
		if err != nil {
			s.counters.sinkErrors.Add(1)
			s.fallback("log sink %s failed for %d records: %v", k.Name(), len(batch), err)
		}
	}
}

// dropReport turns new drop counts into one system warn record (nothing is dropped silently).
func (s *System) dropReport() *Record {
	total := uint64(0)
	attrs := map[string]any{}
	for ci := range Categories {
		for li := 0; li < nLevels; li++ {
			now := s.counters.dropped[ci][li].Load()
			delta := now - s.counters.reported[ci][li]
			if delta == 0 {
				continue
			}
			s.counters.reported[ci][li] = now
			total += delta
			attrs[string(Categories[ci])+"."+levelNames[li]] = delta
		}
	}
	if total == 0 {
		return nil
	}
	now := time.Now().UTC()
	return &Record{ID: s.newID(now), Time: now, Level: LevelWarn, LevelName: "warn", Category: CatSystem,
		Component: "logging", Node: s.o.Node, Role: s.o.Role, Msg: "log records dropped",
		Attrs: map[string]any{"dropped": total, "byCategoryLevel": attrs}}
}

func (s *System) fallback(format string, args ...any) {
	s.fallbackMu.Lock()
	defer s.fallbackMu.Unlock()
	if time.Since(s.lastFallback) < 10*time.Second {
		return
	}
	s.lastFallback = time.Now()
	_, _ = fmt.Fprintf(s.o.Fallback, "emailbridge logging: "+format+"\n", args...)
}

// Flush waits until every record enqueued before the call has been written to the sinks.
func (s *System) Flush(ctx context.Context) error {
	if s.closed.Load() {
		return nil
	}
	ack := make(chan struct{})
	select {
	case s.flushCh <- ack:
	case <-ctx.Done():
		return ctx.Err()
	case <-s.done:
		return nil
	}
	select {
	case <-ack:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Close stops accepting records, writes what is queued and stops the writer.
func (s *System) Close(ctx context.Context) error {
	if !s.closed.CompareAndSwap(false, true) {
		return nil
	}
	close(s.stop)
	select {
	case <-s.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Stats is a snapshot of the log system's counters (exported as Prometheus metrics by P0.09).
type Stats struct {
	Records       map[Category]map[string]uint64
	Dropped       map[Category]map[string]uint64
	SinkErrors    uint64
	Uncategorised uint64
	BufferLen     int
	BufferCap     int
}

// Stats returns the current counters.
func (s *System) Stats() Stats {
	st := Stats{Records: map[Category]map[string]uint64{}, Dropped: map[Category]map[string]uint64{},
		SinkErrors: s.counters.sinkErrors.Load(), Uncategorised: s.counters.uncategorised.Load(),
		BufferLen: len(s.ch), BufferCap: cap(s.ch)}
	for ci, c := range Categories {
		for li := 0; li < nLevels; li++ {
			if n := s.counters.records[ci][li].Load(); n > 0 {
				if st.Records[c] == nil {
					st.Records[c] = map[string]uint64{}
				}
				st.Records[c][levelNames[li]] = n
			}
			if n := s.counters.dropped[ci][li].Load(); n > 0 {
				if st.Dropped[c] == nil {
					st.Dropped[c] = map[string]uint64{}
				}
				st.Dropped[c][levelNames[li]] = n
			}
		}
	}
	return st
}
