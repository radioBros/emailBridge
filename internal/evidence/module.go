package evidence

import (
	"context"
	"crypto/x509"
	"log/slog"
	"net/http"
	"time"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/jobs"
)

// Module wires the evidence Store (Claude adds it to modules.go / wiring.go).
type Module struct {
	Options Options
	Log     *slog.Logger

	store *Store
}

// Name implements app.Module.
func (m *Module) Name() string { return "evidence" }

// Roles implements app.Module (platform: sync + api use it).
func (m *Module) Roles() []app.Role { return nil }

// Start opens the store, runs Check when a bucket is configured, provides *Store, registers the TSA retry handler.
func (m *Module) Start(ctx context.Context, a *app.App) error {
	o := m.Options
	if o.Backend == nil && o.Bucket == "" {
		// Evidence optional until pec.evidenceArchive is on; provide a nil-safe no-op? Prefer skip.
		return nil
	}
	s, err := Open(o)
	if err != nil {
		return err
	}
	if o.Bucket != "" || o.Backend != nil {
		if err := s.Check(ctx); err != nil {
			// Do not fail boot — health warning; Archive refuses until Check passes after fix.
			if m.Log != nil {
				m.Log.Warn("evidence Object Lock check failed; archiving refused", "err", err.Error())
			} else if a.Log != nil {
				a.Log.Warn("evidence Object Lock check failed; archiving refused", "err", err.Error())
			}
		}
	}
	m.store = s
	app.Provide(a, s)
	if js, ok := app.Get[*jobs.Service](a); ok {
		s.RegisterHandlers(js)
	}
	return nil
}

// Stop implements app.Module.
func (m *Module) Stop(context.Context) error { return nil }

// Store returns the started store (nil if evidence bucket was not configured).
func (m *Module) Store() *Store { return m.store }

// OptionsFromEnv is a helper Claude can call when wiring (reads already-loaded config fields).
func OptionsFromEnv(endpoint, region, access, secret, bucket string, pathStyle bool, lockMode string, retention time.Duration, anchors []*x509.Certificate, httpClient *http.Client) Options {
	return Options{
		Endpoint: endpoint, Region: region, AccessKey: access, SecretKey: secret,
		Bucket: bucket, PathStyle: pathStyle, LockMode: lockMode, Retention: retention,
		TSAAnchors: anchors, HTTPClient: httpClient,
	}
}
