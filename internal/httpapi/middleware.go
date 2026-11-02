package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/oklog/ulid/v2"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/logging"
)

type ctxKey int

const clientIPKey ctxKey = iota

var requestIDRe = regexp.MustCompile(`^[A-Za-z0-9._-]{8,64}$`)

// ClientIP returns the caller's IP as established by the trusted-proxy rules.
func ClientIP(ctx context.Context) netip.Addr {
	a, _ := ctx.Value(clientIPKey).(netip.Addr)
	return a
}

// RequestID returns the request ID of ctx.
func RequestID(ctx context.Context) string { return logging.IDsFrom(ctx).RequestID }

func newRequestID() string { return "req_" + strings.ToLower(ulid.Make().String()) }

func (s *Server) requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-Id")
		if !requestIDRe.MatchString(id) {
			id = newRequestID()
		}
		w.Header().Set("X-Request-Id", id)
		ctx := logging.With(r.Context(), func(i *logging.IDs) { i.RequestID = id })
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// clientIP takes the peer address, and walks X-Forwarded-For from the right only while the hops are
// trusted proxies (EB_HTTP_TRUSTED_PROXIES); the first untrusted hop is the client.
func (s *Server) clientIP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), clientIPKey, s.resolveClientIP(r))))
	})
}

func (s *Server) trusted(a netip.Addr) bool {
	for _, p := range s.o.TrustedProxies {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

func (s *Server) resolveClientIP(r *http.Request) netip.Addr {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	peer, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}
	}
	peer = peer.Unmap()
	if !s.trusted(peer) {
		return peer
	}
	var hops []string
	for _, v := range r.Header.Values("X-Forwarded-For") {
		for _, h := range strings.Split(v, ",") {
			if h = strings.TrimSpace(h); h != "" {
				hops = append(hops, h)
			}
		}
	}
	client := peer
	for i := len(hops) - 1; i >= 0; i-- {
		a, err := netip.ParseAddr(hops[i])
		if err != nil {
			break
		}
		client = a.Unmap()
		if !s.trusted(client) {
			break
		}
	}
	return client
}

// isTLS reports whether the original request was HTTPS (directly or via a trusted proxy).
func (s *Server) isTLS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	if a, err := netip.ParseAddr(host); err == nil && s.trusted(a.Unmap()) {
		return strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
	}
	return false
}

// trace extracts or creates W3C trace context (even with OpenTelemetry export off, S12 §10).
func (s *Server) trace(next http.Handler) http.Handler {
	prop := propagation.TraceContext{}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := prop.Extract(r.Context(), propagation.HeaderCarrier(r.Header))
		ctx, span := otel.Tracer("emailbridge/httpapi").Start(ctx, r.Method+" "+r.URL.Path, trace.WithSpanKind(trace.SpanKindServer))
		defer span.End()
		sc := span.SpanContext()
		traceID, spanID := sc.TraceID().String(), sc.SpanID().String()
		if !sc.IsValid() { // no tracer provider: keep the caller's IDs or mint our own
			if in := trace.SpanContextFromContext(ctx); in.IsValid() {
				traceID = in.TraceID().String()
			} else {
				traceID = randomHex(16)
			}
			spanID = randomHex(8)
		}
		w.Header().Set("Traceparent", "00-"+traceID+"-"+spanID+"-01")
		ctx = logging.With(ctx, func(i *logging.IDs) { i.TraceID, i.SpanID = traceID, spanID })
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func isAPIPath(p string) bool {
	return strings.HasPrefix(p, "/v1/") || p == "/v1" || p == "/healthz" || p == "/readyz"
}

const csp = "default-src 'self'; img-src 'self' data: blob:; style-src 'self' 'unsafe-inline'; script-src 'self'; " +
	"connect-src 'self'; frame-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'; object-src 'none'"

// securityHeaders sets the S10 §10 headers.
func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=()")
		h.Set("Content-Security-Policy", csp)
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		if strings.HasPrefix(s.o.PublicURL, "https://") {
			h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		if isAPIPath(r.URL.Path) {
			h.Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}

// cors allows exact configured origins with credentials (S10 §10).
func (s *Server) cors(next http.Handler) http.Handler {
	allowed := map[string]bool{}
	for _, o := range s.o.CORSOrigins {
		allowed[originOf(o)] = true
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin == "" || !allowed[origin] {
			next.ServeHTTP(w, r)
			return
		}
		h := w.Header()
		h.Set("Access-Control-Allow-Origin", origin)
		h.Set("Access-Control-Allow-Credentials", "true")
		h.Add("Vary", "Origin")
		h.Set("Access-Control-Expose-Headers", "X-Request-Id, ETag, Location, RateLimit-Limit, RateLimit-Remaining, RateLimit-Reset, Retry-After, Idempotent-Replayed")
		if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
			h.Set("Access-Control-Allow-Methods", "GET, POST, PATCH, PUT, DELETE")
			h.Set("Access-Control-Allow-Headers", "Content-Type, Authorization, Idempotency-Key, If-Match, EB-Organization, EB-Timeout, X-Request-Id, X-Requested-With")
			h.Set("Access-Control-Max-Age", "600")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func originOf(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return strings.TrimRight(strings.TrimSpace(raw), "/")
	}
	return u.Scheme + "://" + u.Host
}

func (s *Server) bodyLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ContentLength > s.o.MaxBody {
			WriteProblem(w, r, errs.New(errs.CodePayloadTooLarge, fmt.Sprintf("The request body exceeds %d bytes.", s.o.MaxBody)))
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, s.o.MaxBody)
		next.ServeHTTP(w, r)
	})
}

// streaming paths keep their connection open; they are exempt from the request timeout.
// isStreaming reports long-lived responses exempt from the request timeout: SSE streams, the admin
// WebSocket and streamed exports (NDJSON log export, S12 §8).
func isStreaming(p string) bool {
	return strings.HasSuffix(p, "/stream") || strings.HasSuffix(p, "/export") || strings.HasSuffix(p, "/download") || p == "/v1/admin/ws"
}

// timeout applies EB_HTTP_REQUEST_TIMEOUT, adjustable by the EB-Timeout header (milliseconds) up to the max.
func (s *Server) timeout(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isStreaming(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		d := s.o.RequestTimeout
		if v := r.Header.Get("EB-Timeout"); v != "" {
			if ms, err := strconv.Atoi(v); err == nil && ms > 0 {
				d = time.Duration(ms) * time.Millisecond
			}
		}
		if d > s.o.RequestTimeoutMax {
			d = s.o.RequestTimeoutMax
		}
		ctx, cancel := context.WithTimeout(r.Context(), d)
		defer cancel()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func contextWithTimeout(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, d)
}

var unsafeMethods = map[string]bool{http.MethodPost: true, http.MethodPut: true, http.MethodPatch: true, http.MethodDelete: true}

// csrfCheck protects cookie-authenticated writes on /v1 (S10 §2.3): the cross-origin check (Sec-Fetch-Site
// / Origin against trusted origins) plus the X-Requested-With: EmailBridge header. Requests with an
// Authorization header (API keys) carry no ambient credentials and are exempt. Limen's own routes under
// /v1/auth have Limen's checks; there only the cross-origin check applies.
func (s *Server) csrfCheck(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !unsafeMethods[r.Method] || !strings.HasPrefix(r.URL.Path, "/v1/") || r.Header.Get("Authorization") != "" {
			next.ServeHTTP(w, r)
			return
		}
		if _, err := r.Cookie(s.o.SessionCookie); err != nil {
			next.ServeHTTP(w, r)
			return
		}
		if err := s.csrf.Check(r); err != nil {
			s.o.SecurityLog.WarnContext(r.Context(), "cross-origin write rejected", "path", r.URL.Path, "origin", r.Header.Get("Origin"), "ip", ClientIP(r.Context()).String())
			WriteProblem(w, r, errs.New(errs.CodeOriginRejected, "Cross-origin requests with a session cookie are not allowed."))
			return
		}
		if !strings.HasPrefix(r.URL.Path, "/v1/auth/") && r.Header.Get("X-Requested-With") != "EmailBridge" {
			s.o.SecurityLog.WarnContext(r.Context(), "csrf header missing", "path", r.URL.Path, "ip", ClientIP(r.Context()).String())
			WriteProblem(w, r, errs.New(errs.CodeCSRFFailed, "Cookie-authenticated writes must send X-Requested-With: EmailBridge."))
			return
		}
		next.ServeHTTP(w, r)
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
	bytes  int64
	note   *rateNote // RateLimit-* state noted by the handler (ratelimit.go)
}

func (w *statusWriter) WriteHeader(code int) {
	// A huma output struct with a `Status int` field left at zero asks for status 0, which net/http turns into
	// a panic (500). The operation succeeded, so answer its default 200 instead.
	if code == 0 {
		code = http.StatusOK
	}
	if w.status == 0 {
		w.status = code
		w.note.apply(w.Header())
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
		w.note.apply(w.Header())
	}
	n, err := w.ResponseWriter.Write(b)
	w.bytes += int64(n)
	return n, err
}

// Unwrap lets http.ResponseController reach the underlying writer (flush, hijack for WebSocket).
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// accessLog writes one api-category record per request (S12 §4): method, route template, status, latency.
func (s *Server) accessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ctx, note := withRateNote(r.Context())
		sw := &statusWriter{ResponseWriter: w, note: note}
		next.ServeHTTP(sw, r.WithContext(ctx))
		route := r.URL.Path
		if rc := chi.RouteContext(r.Context()); rc != nil && rc.RoutePattern() != "" {
			route = rc.RoutePattern()
		}
		if route == "/healthz" || route == "/readyz" {
			return
		}
		status := sw.status
		if status == 0 {
			status = http.StatusOK
		}
		level := logging.LevelInfo
		if status >= 500 {
			level = logging.LevelError
		} else if status >= 400 {
			level = logging.LevelWarn
		}
		s.o.AccessLog.Log(r.Context(), level, "request", "method", r.Method, "route", route, "status", status,
			"latencyMs", time.Since(start).Milliseconds(), "bytes", sw.bytes, "ip", ClientIP(r.Context()).String(),
			"code", sw.Header().Get(problemCodeHeader))
	})
}

// recoverer turns a panic into a 500 problem and reports it (S12 §12); it never re-panics.
func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				if rec == http.ErrAbortHandler { //nolint:errorlint // sentinel compared by identity, as net/http does
					panic(rec)
				}
				if s.o.OnPanic != nil {
					s.o.OnPanic(r, rec, debug.Stack())
				}
				WriteProblem(w, r, errs.New(errs.CodeInternalError, "An internal error occurred."))
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// problemCodeHeader repeats the problem code of an error response; the access log reads it and clients may
// use it without parsing the body.
const problemCodeHeader = "EB-Problem-Code"

func problemFor(r *http.Request, err error) *errs.Problem {
	ids := logging.IDsFrom(r.Context())
	return errs.ProblemFrom(err, ids.RequestID, ids.TraceID)
}

// WriteProblem writes err as an RFC 9457 problem (for handlers outside huma: router errors, raw handlers).
// Reportable errors are forwarded to Options.OnReportable when a Server is bound to the request.
func WriteProblem(w http.ResponseWriter, r *http.Request, err error) {
	if r != nil {
		if s, ok := r.Context().Value(serverCtxKey{}).(*Server); ok && s != nil {
			s.reportIfNeeded(r.Context(), err)
		}
	}
	writeProblemObj(w, problemFor(r, err))
}

func (s *Server) reportIfNeeded(ctx context.Context, err error) {
	if s == nil || err == nil || s.o.OnReportable == nil || !errs.Reportable(err) {
		return
	}
	s.o.OnReportable(ctx, err)
}

type serverCtxKey struct{}

// withServer stashes s on the request so package-level WriteProblem can report.
func (s *Server) withServer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), serverCtxKey{}, s)))
	})
}

func writeProblemObj(w http.ResponseWriter, p *errs.Problem) {
	// Retry-After / RateLimit-* from the problem, unless the handler (e.g. Limen) already set its own.
	for k, vs := range p.GetHeaders() {
		if w.Header().Get(k) != "" {
			continue
		}
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.Header().Set(problemCodeHeader, string(p.Code))
	w.WriteHeader(p.GetStatus())
	_ = json.NewEncoder(w).Encode(p)
}
