package mcp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/httpapi"
)

// SettingListen is the global runtime setting with the dedicated listener's address ("" = off).
const SettingListen = "mcp.listen"

// NormalizeListen turns the admin's input into a listen address: "8090" → ":8090"; "" stays "" (off).
// It accepts host:port with an IP, a host name or no host; port 0 picks a free port (tests).
func NormalizeListen(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", nil
	}
	if _, err := strconv.Atoi(s); err == nil {
		s = ":" + s
	}
	host, port, err := net.SplitHostPort(s)
	if err != nil {
		return "", errs.New(errs.CodeValidationFailed, "The MCP listen address must be a port (8090) or host:port (127.0.0.1:8090).")
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 0 || n > 65535 {
		return "", errs.New(errs.CodeValidationFailed, "The MCP port must be between 1 and 65535.").WithSafe("port", port)
	}
	if strings.ContainsAny(host, " /\\?#@") {
		return "", errs.New(errs.CodeValidationFailed, "The MCP listen host is not valid.")
	}
	return net.JoinHostPort(host, port), nil
}

// ListenerStatus is the state of the dedicated listener on this node.
type ListenerStatus struct {
	Configured string // the normalized setting ("" = off)
	Active     string // the bound address while listening
	Error      string // why the configured address could not be bound
}

// Listener runs the dedicated MCP listener. Apply starts, moves or stops it; it serves only /mcp (and
// /mcp/) through the full API handler, so authentication, rate limits and problems are the main
// listener's; every other path is 404.
type Listener struct {
	Handler http.Handler // the httpapi server
	Log     *slog.Logger

	mu     sync.Mutex
	status ListenerStatus
	srv    *http.Server
	done   chan struct{}
}

func (l *Listener) log() *slog.Logger {
	if l.Log != nil {
		return l.Log
	}
	return slog.Default()
}

// onlyMCP restricts the dedicated port to the MCP endpoint.
func onlyMCP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/mcp" && r.URL.Path != "/mcp/" {
			httpapi.WriteProblem(w, r, errs.New(errs.CodeNotFound, "This port serves only /mcp."))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Apply makes the listener match addr (already normalized). It is idempotent: the same address that is
// already bound (or already failed) is left alone.
func (l *Listener) Apply(ctx context.Context, addr string) ListenerStatus {
	l.mu.Lock()
	defer l.mu.Unlock()
	if addr == l.status.Configured && (l.srv != nil || addr == "") {
		return l.status
	}
	prev := l.status
	l.stopLocked(ctx)
	l.status = ListenerStatus{Configured: addr}
	if addr == "" {
		l.log().InfoContext(ctx, "mcp listener stopped")
		return l.status
	}
	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", addr)
	if err != nil {
		l.status.Error = bindError(err)
		if prev.Configured == addr && prev.Error == l.status.Error {
			l.log().DebugContext(ctx, "mcp listener still cannot bind", "addr", addr, "err", l.status.Error)
		} else {
			l.log().ErrorContext(ctx, "mcp listener could not bind", "addr", addr, "err", l.status.Error)
		}
		return l.status
	}
	h := l.Handler
	if h == nil {
		h = http.NotFoundHandler()
	}
	srv := &http.Server{
		Handler: onlyMCP(h), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 120 * time.Second, MaxHeaderBytes: 64 << 10,
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			l.log().Error("mcp listener stopped unexpectedly", "addr", addr, "err", err)
		}
	}()
	l.srv, l.done = srv, done
	l.status.Active = ln.Addr().String()
	l.log().InfoContext(ctx, "mcp listener started", "addr", l.status.Active)
	return l.status
}

// bindError is a short, path-free reason for a failed bind.
func bindError(err error) string {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "address already in use"), strings.Contains(msg, "Only one usage of each socket address"):
		return "address already in use"
	case strings.Contains(msg, "permission denied"), strings.Contains(msg, "access permissions"):
		return "permission denied (ports below 1024 need privileges)"
	case strings.Contains(msg, "cannot assign requested address"), strings.Contains(msg, "requested address is not valid"):
		return "the host is not an address of this node"
	case strings.Contains(msg, "no such host"):
		return "the host name does not resolve"
	}
	return fmt.Sprintf("listen failed: %s", msg)
}

// Status returns the current state.
func (l *Listener) Status() ListenerStatus {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.status
}

// Stop closes the listener (module stop).
func (l *Listener) Stop(ctx context.Context) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.stopLocked(ctx)
	l.status.Active = ""
}

func (l *Listener) stopLocked(ctx context.Context) {
	if l.srv == nil {
		return
	}
	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := l.srv.Shutdown(sctx); err != nil {
		_ = l.srv.Close()
	}
	<-l.done
	l.srv, l.done = nil, nil
}
