package observability

import (
	"context"
	"errors"
	"net"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/radiobros/emailbridge/internal/logging"
)

// Metrics wraps a private Prometheus registry (no global default) and the S12 §9 domain metrics.
type Metrics struct {
	reg     *prometheus.Registry
	srv     *http.Server
	domain  *domainMetrics
	sources sources
}

// NewMetrics creates a registry with Go/process collectors and every S12 §9 domain metric.
func NewMetrics() *Metrics {
	reg := prometheus.NewRegistry()
	_ = reg.Register(collectors.NewGoCollector())
	_ = reg.Register(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	d := newDomainMetrics()
	for _, c := range d.collectors() {
		_ = reg.Register(c)
	}
	d.zeroGauges()
	return &Metrics{reg: reg, domain: d}
}

// Register adds a domain collector.
func (m *Metrics) Register(c prometheus.Collector) error {
	return m.reg.Register(c)
}

// ObserveLogs registers a collector that scrapes logging.System.Stats().
func (m *Metrics) ObserveLogs(sys *logging.System) error {
	if sys == nil {
		return errors.New("observability: nil logging.System")
	}
	return m.reg.Register(newLogStatsCollector(sys))
}

// Registry exposes the underlying registry (tests / advanced wiring).
func (m *Metrics) Registry() *prometheus.Registry { return m.reg }

// Serve starts an HTTP server on addr exposing only /metrics. Empty addr is a no-op.
// It returns when ctx is cancelled or the server fails.
func (m *Metrics) Serve(ctx context.Context, addr string) error {
	if addr == "" {
		<-ctx.Done()
		return ctx.Err()
	}
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(m.reg, promhttp.HandlerOpts{}))
	m.srv = &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}
	errCh := make(chan error, 1)
	go func() {
		errCh <- m.srv.ListenAndServe()
	}()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = m.srv.Shutdown(shutdownCtx)
		if serveErr := <-errCh; serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			return errors.Join(ctx.Err(), serveErr)
		}
		return ctx.Err()
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

// Shutdown stops the metrics server if running.
func (m *Metrics) Shutdown(ctx context.Context) error {
	if m.srv == nil {
		return nil
	}
	return m.srv.Shutdown(ctx)
}

type logStatsCollector struct {
	sys *logging.System

	records    *prometheus.Desc
	dropped    *prometheus.Desc
	sinkErrors *prometheus.Desc
	uncat      *prometheus.Desc
	bufferLen  *prometheus.Desc
}

func newLogStatsCollector(sys *logging.System) *logStatsCollector {
	return &logStatsCollector{
		sys: sys,
		records: prometheus.NewDesc("eb_log_records_total",
			"Log records accepted into the buffer", []string{"category", "level"}, nil),
		dropped: prometheus.NewDesc("eb_log_dropped_total",
			"Log records dropped under back-pressure", []string{"category", "level"}, nil),
		sinkErrors: prometheus.NewDesc("eb_log_sink_errors_total",
			"Log sink write failures", nil, nil),
		uncat: prometheus.NewDesc("eb_log_uncategorised_total",
			"Records logged without a known category", nil, nil),
		bufferLen: prometheus.NewDesc("eb_log_buffer_len",
			"Current log buffer length", nil, nil),
	}
}

func (c *logStatsCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.records
	ch <- c.dropped
	ch <- c.sinkErrors
	ch <- c.uncat
	ch <- c.bufferLen
}

func (c *logStatsCollector) Collect(ch chan<- prometheus.Metric) {
	st := c.sys.Stats()
	for cat, levels := range st.Records {
		for level, n := range levels {
			ch <- prometheus.MustNewConstMetric(c.records, prometheus.CounterValue, float64(n), string(cat), level)
		}
	}
	for cat, levels := range st.Dropped {
		for level, n := range levels {
			ch <- prometheus.MustNewConstMetric(c.dropped, prometheus.CounterValue, float64(n), string(cat), level)
		}
	}
	ch <- prometheus.MustNewConstMetric(c.sinkErrors, prometheus.CounterValue, float64(st.SinkErrors))
	ch <- prometheus.MustNewConstMetric(c.uncat, prometheus.CounterValue, float64(st.Uncategorised))
	ch <- prometheus.MustNewConstMetric(c.bufferLen, prometheus.GaugeValue, float64(st.BufferLen))
}
