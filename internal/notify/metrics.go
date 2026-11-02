package notify

import (
	"context"

	"github.com/prometheus/client_golang/prometheus"
)

// Metrics holds open-problem gauges.
type Metrics struct {
	open *prometheus.GaugeVec
}

// NewMetrics builds gauges (register with the process registry from wiring).
func NewMetrics() *Metrics {
	return &Metrics{
		open: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "eb_notifications_open",
			Help: "Open system notifications by category and severity",
		}, []string{"category", "severity"}),
	}
}

// Collector returns the prometheus collector.
func (m *Metrics) Collector() prometheus.Collector {
	if m == nil {
		return nil
	}
	return m.open
}

// Refresh sets gauges from the repo.
func (m *Metrics) Refresh(ctx context.Context, repo *Repo) {
	if m == nil || repo == nil {
		return
	}
	counts, err := repo.OpenGauge(ctx)
	if err != nil {
		return
	}
	m.open.Reset()
	for k, n := range counts {
		m.open.WithLabelValues(k[0], k[1]).Set(float64(n))
	}
}
