// Package httpapi is the HTTP platform of the api role (S2 §2, S10 §2.3, §10): the chi router, the huma
// API that generates the OpenAPI document, the middleware every request passes through, health
// endpoints, problem responses for router errors, and the listener with graceful shutdown.
package httpapi

import (
	"context"
	"encoding/json"
	"io/fs"
	"log/slog"
	"net/http"
	"net/netip"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/errs"
)

// Options configure the server (values from S9 §2.2 and §2.5).
type Options struct {
	Addr              string
	PublicURL         string
	TrustedProxies    []netip.Prefix
	CORSOrigins       []string
	MaxBody           int64
	RequestTimeout    time.Duration
	RequestTimeoutMax time.Duration
	Env               string // development | staging | production
	Version           string
	// SessionCookie is the admin session cookie name (Limen, S10 §2.1).
	SessionCookie string
	// AccessLog receives one record per request (api category); SecurityLog receives rejections.
	AccessLog   *slog.Logger
	SecurityLog *slog.Logger
	// Checkers returns the readiness checkers (app.App.Checkers).
	Checkers func() []app.Checker
	// Authenticated reports whether a request carries valid credentials (wired by the auth module);
	// /readyz shows check details only then.
	Authenticated func(r *http.Request) bool
	// OnPanic is called for recovered panics (crash log + error reporting).
	OnPanic func(r *http.Request, recovered any, stack []byte)
	// OnReportable is called once per errs.Reportable problem written (W51 / S12 §12).
	OnReportable func(ctx context.Context, err error)
	// SPA serves the admin single-page app for non-API paths (nil until the web build is embedded).
	SPA fs.FS
}

func (o *Options) defaults() {
	if o.Addr == "" {
		o.Addr = ":8080"
	}
	if o.MaxBody <= 0 {
		o.MaxBody = 32 << 20
	}
	if o.RequestTimeout <= 0 {
		o.RequestTimeout = 30 * time.Second
	}
	if o.RequestTimeoutMax < o.RequestTimeout {
		o.RequestTimeoutMax = 120 * time.Second
	}
	if o.SessionCookie == "" {
		o.SessionCookie = "eb_session"
	}
	if o.AccessLog == nil {
		o.AccessLog = slog.Default()
	}
	if o.SecurityLog == nil {
		o.SecurityLog = slog.Default()
	}
}

// Server holds the router and the huma API. Domain modules register their operations with Register;
// raw handlers (Limen, WebSocket, SSE) are mounted on Router.
type Server struct {
	Router chi.Router
	API    huma.API
	o      Options
	csrf   *http.CrossOriginProtection
	late   atomic.Pointer[func(http.Handler) http.Handler]
}

// AddMiddleware appends a middleware that runs after the platform chain (CSRF, timeouts …) and before
// every route — for modules that start after routes exist (auth session, caller resolution). chi forbids
// Use after routes, so these live in one late slot composed in registration order.
func (s *Server) AddMiddleware(mw func(http.Handler) http.Handler) {
	for {
		old := s.late.Load()
		next := mw
		if old != nil {
			prev := *old
			next = func(h http.Handler) http.Handler { return prev(mw(h)) }
		}
		if s.late.CompareAndSwap(old, &next) {
			return
		}
	}
}

func (s *Server) lateMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if mw := s.late.Load(); mw != nil {
			(*mw)(next).ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// New builds the router, middleware chain and huma API.
func New(o Options) (*Server, error) {
	o.defaults()
	errs.InstallHuma()
	s := &Server{o: o}
	cop := http.NewCrossOriginProtection()
	for _, origin := range s.trustedOrigins() {
		if err := cop.AddTrustedOrigin(origin); err != nil {
			return nil, err
		}
	}
	s.csrf = cop

	r := chi.NewRouter()
	r.Use(s.withServer, s.recoverer, s.requestID, s.clientIP, s.trace, s.securityHeaders, s.cors, s.accessLog, s.bodyLimit, s.timeout, s.csrfCheck, s.lateMiddleware)
	r.NotFound(func(w http.ResponseWriter, req *http.Request) {
		if s.o.SPA != nil && !isAPIPath(req.URL.Path) && req.Method == http.MethodGet {
			s.spa().ServeHTTP(w, req)
			return
		}
		WriteProblem(w, req, errs.New(errs.CodeNotFound, "No route matches "+req.URL.Path+"."))
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, req *http.Request) {
		p := problemFor(req, errs.New(errs.CodeMalformedRequest, "Method "+req.Method+" is not allowed here."))
		p.Status = http.StatusMethodNotAllowed
		writeProblemObj(w, p)
	})
	r.Get("/healthz", s.healthz)
	r.Get("/readyz", s.readyz)

	cfg := huma.DefaultConfig("EmailBridge API", o.Version)
	cfg.OpenAPIPath = "/v1/openapi"
	cfg.DocsPath = "" // the docs UI loads scripts from a CDN, which the CSP forbids (S13 §8)
	cfg.SchemasPath = "/v1/schemas"
	cfg.CreateHooks = nil // no "$schema" link in every response body
	if o.PublicURL != "" {
		cfg.Servers = []*huma.Server{{URL: o.PublicURL}}
	}
	cfg.Components.SecuritySchemes = map[string]*huma.SecurityScheme{
		"apiKey":  {Type: "http", Scheme: "bearer", BearerFormat: "ebk_…"},
		"session": {Type: "apiKey", In: "cookie", Name: o.SessionCookie},
	}
	cfg.Components.Schemas = huma.NewMapRegistry("#/components/schemas/", uniqueSchemaNamer())
	s.API = humachi.New(r, cfg)
	s.API.OpenAPI().OnAddOperation = append(s.API.OpenAPI().OnAddOperation, documentOperation)
	s.Router = r
	s.documentProbes()
	return s, nil
}

func (s *Server) trustedOrigins() []string {
	var out []string
	if s.o.PublicURL != "" {
		out = append(out, originOf(s.o.PublicURL))
	}
	for _, o := range s.o.CORSOrigins {
		out = append(out, originOf(o))
	}
	return out
}

// ServeHTTP makes Server an http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.Router.ServeHTTP(w, r) }

// Options returns the effective options.
func (s *Server) Options() Options { return s.o }

func (s *Server) healthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

func (s *Server) readyz(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	type check struct {
		OK    bool   `json:"ok"`
		Error string `json:"error,omitempty"`
	}
	checks := map[string]check{}
	ready := true
	if s.o.Checkers != nil {
		for _, c := range s.o.Checkers() {
			cctx, cancel := contextWithTimeout(ctx, 3*time.Second)
			err := c.Ready(cctx)
			cancel()
			ck := check{OK: err == nil}
			if err != nil {
				ready = false
				ck.Error = err.Error()
			}
			checks[c.Name()] = ck
		}
	}
	body := map[string]any{"status": "ready"}
	status := http.StatusOK
	if !ready {
		body["status"] = "not_ready"
		status = http.StatusServiceUnavailable
	}
	if s.o.Authenticated != nil && s.o.Authenticated(r) {
		body["checks"] = checks
	} else if !ready {
		names := []string{}
		for n, c := range checks {
			if !c.OK {
				names = append(names, n)
			}
		}
		body["failing"] = names
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// uniqueSchemaNamer keeps huma's default schema names (the Go type name) and prefixes the package name
// only when a different type already registered that name (audit.ListResult vs apikeys.KeyList…), so
// two packages with the same type name never panic the registry at startup.
func uniqueSchemaNamer() func(t reflect.Type, hint string) string {
	var mu sync.Mutex
	owners := map[string]reflect.Type{}
	return func(t reflect.Type, hint string) string {
		name := huma.DefaultSchemaNamer(t, hint)
		base := t
		for base.Kind() == reflect.Pointer || base.Kind() == reflect.Slice || base.Kind() == reflect.Array || base.Kind() == reflect.Map {
			base = base.Elem()
		}
		if base.Name() == "" || base.PkgPath() == "" {
			return name // anonymous or builtin: huma's hint-based name
		}
		mu.Lock()
		defer mu.Unlock()
		if prev, ok := owners[name]; !ok || prev == t {
			owners[name] = t
			return name
		}
		pkg := base.PkgPath()[strings.LastIndex(base.PkgPath(), "/")+1:]
		qualified := strings.ToUpper(pkg[:1]) + pkg[1:] + name
		owners[qualified] = t
		return qualified
	}
}
