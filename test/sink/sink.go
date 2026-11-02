// Package sink is a scriptable HTTP webhook sink for tests (S14 §2.1).
package sink

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"time"
)

// Request is one recorded inbound HTTP request.
type Request struct {
	Method  string
	Path    string
	Headers http.Header
	Body    []byte
	Time    time.Time
}

// Options configure NewServer.
type Options struct {
	// Status is the fixed response status (default 200).
	Status int
	// Delay is applied before each response.
	Delay time.Duration
	// FailTimes makes the first N requests return FailStatus (default 500) or
	// close the connection when CloseOnFail is set; afterwards Status is used.
	FailTimes   int
	FailStatus  int
	CloseOnFail bool
}

// Server is an httptest-style webhook sink.
type Server struct {
	URL string

	mu       sync.Mutex
	opts     Options
	reqs     []Request
	attempts int
	srv      *httptest.Server
}

// NewServer starts a sink. Call Close when done.
func NewServer(opts Options) *Server {
	if opts.Status == 0 {
		opts.Status = http.StatusOK
	}
	if opts.FailStatus == 0 {
		opts.FailStatus = http.StatusInternalServerError
	}
	s := &Server{opts: opts}
	s.srv = httptest.NewUnstartedServer(http.HandlerFunc(s.serve))
	s.srv.Start()
	s.URL = s.srv.URL
	return s
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	_ = r.Body.Close()
	hdr := r.Header.Clone()
	rec := Request{Method: r.Method, Path: r.URL.RequestURI(), Headers: hdr, Body: append([]byte(nil), body...), Time: time.Now().UTC()}

	s.mu.Lock()
	s.reqs = append(s.reqs, rec)
	s.attempts++
	n := s.attempts
	opts := s.opts
	s.mu.Unlock()

	if opts.Delay > 0 {
		time.Sleep(opts.Delay)
	}
	if n <= opts.FailTimes {
		if opts.CloseOnFail {
			hj, ok := w.(http.Hijacker)
			if ok {
				conn, _, err := hj.Hijack()
				if err == nil {
					_ = conn.Close()
					return
				}
			}
		}
		w.WriteHeader(opts.FailStatus)
		return
	}
	w.WriteHeader(opts.Status)
}

// Requests returns a copy of recorded requests.
func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Request, len(s.reqs))
	copy(out, s.reqs)
	return out
}

// Reset clears recorded requests and the attempt counter.
func (s *Server) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reqs = nil
	s.attempts = 0
}

// SetOptions replaces scripting options (does not Reset).
func (s *Server) SetOptions(opts Options) {
	if opts.Status == 0 {
		opts.Status = http.StatusOK
	}
	if opts.FailStatus == 0 {
		opts.FailStatus = http.StatusInternalServerError
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.opts = opts
}

// WaitFor blocks until at least n requests are recorded or timeout elapses.
func (s *Server) WaitFor(n int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		got := len(s.reqs)
		s.mu.Unlock()
		if got >= n {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

// Close shuts the server down.
func (s *Server) Close() {
	if s.srv != nil {
		s.srv.Close()
	}
}
