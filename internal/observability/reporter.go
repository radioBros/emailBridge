package observability

import (
	"context"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/getsentry/sentry-go"

	"github.com/radiobros/emailbridge/internal/logging"
)

// reportRateLimit caps outbound events sharing a fingerprint (W51 / S12 §12).
const (
	reportRateLimitMax    = 20
	reportRateLimitWindow = 60 * time.Second
)

// ReporterOptions configure a Sentry-compatible reporter (EB_SENTRY_* / connector).
type ReporterOptions struct {
	DSN              string
	Environment      string
	Release          string
	SampleRate       float64
	TracesSampleRate float64
	SendPII          bool
	NodeID           string
	Role             string
	// Reportable decides whether an error should be sent. Wiring passes errs.Reportable;
	// this package does not import internal/errs.
	Reportable func(error) bool
}

// Reporter sends errors to a Sentry-compatible DSN using its own Client and Hub (not the global hub).
// The active hub is stored in an atomic.Pointer so ApplyConnector / ClearConnector can swap under load.
type Reporter struct {
	hub        atomic.Pointer[sentry.Hub]
	reportable func(error) bool
	envLocked  bool // true when constructed with a non-empty EB_SENTRY_DSN
	base       ReporterOptions

	rateMu sync.Mutex
	rate   map[string][]time.Time
}

// NewReporter builds a Reporter. Empty DSN yields a disabled reporter that may later be
// configured via ApplyConnector (unless a later call sets env via a non-empty DSN here).
func NewReporter(o ReporterOptions) (*Reporter, error) {
	r := &Reporter{reportable: o.Reportable, base: o, envLocked: strings.TrimSpace(o.DSN) != "", rate: map[string][]time.Time{}}
	if o.DSN == "" {
		return r, nil
	}
	hub, err := newHub(o)
	if err != nil {
		return nil, err
	}
	r.hub.Store(hub)
	return r, nil
}

func newHub(o ReporterOptions) (*sentry.Hub, error) {
	sample := o.SampleRate
	if sample == 0 {
		sample = 1.0
	}
	client, err := sentry.NewClient(sentry.ClientOptions{
		Dsn:              o.DSN,
		Environment:      o.Environment,
		Release:          o.Release,
		SampleRate:       sample,
		TracesSampleRate: o.TracesSampleRate,
		DataCollection: &sentry.DataCollection{
			UserInfo:    sentry.Set(o.SendPII),
			Cookies:     &sentry.KeyValueCollectionBehavior{Mode: sentry.CollectionOff},
			HTTPBodies:  []sentry.BodyType{},
			QueryParams: &sentry.KeyValueCollectionBehavior{Mode: sentry.CollectionOff},
		},
		BeforeSend: scrubBeforeSend(o.SendPII, o.NodeID, o.Role),
	})
	if err != nil {
		return nil, err
	}
	return sentry.NewHub(client, sentry.NewScope()), nil
}

func scrubBeforeSend(sendPII bool, node, role string) func(*sentry.Event, *sentry.EventHint) *sentry.Event {
	return func(event *sentry.Event, _ *sentry.EventHint) *sentry.Event {
		if event == nil {
			return nil
		}
		if event.Request != nil {
			event.Request.Data = ""
			event.Request.Cookies = ""
			event.Request.QueryString = ""
			if event.Request.URL != "" {
				if u, err := url.Parse(event.Request.URL); err == nil {
					u.RawQuery = ""
					u.Fragment = ""
					event.Request.URL = u.String()
				}
			}
			if event.Request.Headers != nil {
				for k := range event.Request.Headers {
					switch strings.ToLower(k) {
					case "authorization", "cookie", "set-cookie":
						delete(event.Request.Headers, k)
					}
				}
			}
		}
		if !sendPII {
			event.User = sentry.User{}
		}
		if event.Tags == nil {
			event.Tags = map[string]string{}
		}
		if node != "" {
			event.Tags["node"] = node
		}
		if role != "" {
			event.Tags["role"] = role
		}
		return event
	}
}

// EnvLocked reports whether EB_SENTRY_DSN was set at construction (connector config is ignored).
func (r *Reporter) EnvLocked() bool {
	return r != nil && r.envLocked
}

// Enabled reports whether a hub is currently configured.
func (r *Reporter) Enabled() bool {
	return r != nil && r.hub.Load() != nil
}

// ApplyConnector configures the reporter from an installed Sentry connector when the env DSN is empty.
// No-op (and nil error) when EnvLocked. Empty DSN clears the connector-sourced client.
func (r *Reporter) ApplyConnector(o ReporterOptions) error {
	if r == nil || r.envLocked {
		return nil
	}
	if strings.TrimSpace(o.DSN) == "" {
		r.hub.Store(nil)
		return nil
	}
	merged := r.base
	merged.DSN = o.DSN
	if o.Environment != "" {
		merged.Environment = o.Environment
	}
	if o.SampleRate != 0 {
		merged.SampleRate = o.SampleRate
	}
	if o.Release != "" {
		merged.Release = o.Release
	}
	if o.Reportable != nil {
		merged.Reportable = o.Reportable
	}
	hub, err := newHub(merged)
	if err != nil {
		return err
	}
	r.hub.Store(hub)
	return nil
}

// ClearConnector disables connector-sourced reporting. No-op when EnvLocked.
func (r *Reporter) ClearConnector() {
	if r == nil || r.envLocked {
		return
	}
	r.hub.Store(nil)
}

// CaptureError reports err when the Reportable predicate accepts it.
func (r *Reporter) CaptureError(ctx context.Context, err error, tags map[string]string) {
	if r == nil || err == nil {
		return
	}
	hub := r.hub.Load()
	if hub == nil {
		return
	}
	if r.reportable != nil && !r.reportable(err) {
		return
	}
	tags = mergeIDTags(ctx, tags)
	if !r.allow(fingerprint(tags, "error")) {
		return
	}
	hub.WithScope(func(scope *sentry.Scope) {
		applyTags(scope, tags)
		hub.CaptureException(err)
	})
}

// CapturePanic reports a recovered panic.
func (r *Reporter) CapturePanic(ctx context.Context, recovered any, stack []byte, tags map[string]string) {
	if r == nil || recovered == nil {
		return
	}
	hub := r.hub.Load()
	if hub == nil {
		return
	}
	tags = mergeIDTags(ctx, tags)
	if !r.allow(fingerprint(tags, "panic")) {
		return
	}
	hub.WithScope(func(scope *sentry.Scope) {
		applyTags(scope, tags)
		if len(stack) > 0 {
			scope.SetContext("crash", sentry.Context{"stack": string(stack)})
		}
		hub.Recover(recovered)
	})
}

func (r *Reporter) allow(fp string) bool {
	if r == nil {
		return false
	}
	now := time.Now()
	r.rateMu.Lock()
	defer r.rateMu.Unlock()
	if r.rate == nil {
		r.rate = map[string][]time.Time{}
	}
	cut := now.Add(-reportRateLimitWindow)
	kept := r.rate[fp][:0]
	for _, t := range r.rate[fp] {
		if t.After(cut) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= reportRateLimitMax {
		r.rate[fp] = kept
		return false
	}
	r.rate[fp] = append(kept, now)
	return true
}

func fingerprint(tags map[string]string, kind string) string {
	code, component := kind, ""
	if tags != nil {
		if c := tags["code"]; c != "" {
			code = c
		}
		component = tags["component"]
	}
	return code + "|" + component
}

func mergeIDTags(ctx context.Context, tags map[string]string) map[string]string {
	ids := logging.IDsFrom(ctx)
	out := tags
	if out == nil {
		out = map[string]string{}
	} else {
		cp := make(map[string]string, len(out)+6)
		for k, v := range out {
			cp[k] = v
		}
		out = cp
	}
	set := func(k, v string) {
		if v == "" {
			return
		}
		if _, ok := out[k]; !ok {
			out[k] = v
		}
	}
	set("requestId", ids.RequestID)
	set("traceId", ids.TraceID)
	set("organizationId", ids.OrganizationID)
	set("accountId", ids.AccountID)
	set("jobId", ids.JobID)
	return out
}

// Flush waits up to timeout for buffered events to be sent.
func (r *Reporter) Flush(timeout time.Duration) {
	if r == nil {
		return
	}
	hub := r.hub.Load()
	if hub == nil {
		return
	}
	hub.Flush(timeout)
}

func applyTags(scope *sentry.Scope, tags map[string]string) {
	if tags == nil {
		return
	}
	for k, v := range tags {
		scope.SetTag(k, v)
	}
	// Fingerprint = code + component when present (S12 §12).
	var ordered []string
	if c, ok := tags["code"]; ok {
		ordered = append(ordered, c)
	}
	if c, ok := tags["component"]; ok {
		ordered = append(ordered, c)
	}
	if len(ordered) > 0 {
		scope.SetFingerprint(ordered)
	}
}
