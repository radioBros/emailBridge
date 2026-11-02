package httpapi

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/radiobros/emailbridge/internal/errs"
)

type rlIn struct {
	Mode string `query:"mode"`
}
type rlOut struct {
	Body struct {
		OK bool `json:"ok"`
	}
}

// TestRateLimitHeaders: an operation that notes its limiter sends RateLimit-* on success and on 429; every
// 429 has Retry-After (the limiter's hint, else 60 s) unless the problem opts out; raw handlers via
// WriteProblem get the same headers.
func TestRateLimitHeaders(t *testing.T) {
	s, _ := newServer(t, Options{})
	Register(s, huma.Operation{OperationID: "rl", Method: http.MethodGet, Path: "/v1/rl"}, func(ctx context.Context, in *rlIn) (*rlOut, error) {
		switch in.Mode {
		case "ok":
			NoteRateLimit(ctx, errs.RateInfo{Limit: 5, Remaining: 3, Reset: 1500 * time.Millisecond})
			return &rlOut{}, nil
		case "limited":
			info := errs.RateInfo{Limit: 5, Remaining: 0, Reset: 12 * time.Second}
			NoteRateLimit(ctx, info)
			return nil, errs.New(errs.CodeRateLimited, "slow down").WithRate(info)
		case "plain":
			return nil, errs.New(errs.CodeRateLimited, "slow down")
		case "hint":
			return nil, errs.New(errs.CodeQuotaSendsExceeded, "quota").WithRetryAfter(90 * time.Second)
		case "noretry":
			return nil, errs.New(errs.CodeQuotaAccountsExceeded, "full").WithoutRetryAfter()
		}
		return &rlOut{}, nil
	})
	s.Router.Get("/v1/rl-raw", func(w http.ResponseWriter, r *http.Request) {
		WriteProblem(w, r, errs.New(errs.CodeRateLimited, "raw").WithRetryAfter(3*time.Second))
	})

	w := do(s, http.MethodGet, "/v1/rl?mode=ok", "", nil)
	if w.Code != 200 || w.Header().Get("RateLimit-Limit") != "5" || w.Header().Get("RateLimit-Remaining") != "3" ||
		w.Header().Get("RateLimit-Reset") != "2" || w.Header().Get("Retry-After") != "" {
		t.Fatalf("ok: %d %v", w.Code, w.Header())
	}
	w = do(s, http.MethodGet, "/v1/rl?mode=limited", "", nil)
	if w.Code != 429 || w.Header().Get("RateLimit-Remaining") != "0" || w.Header().Get("Retry-After") != "12" {
		t.Fatalf("limited: %d %v", w.Code, w.Header())
	}
	w = do(s, http.MethodGet, "/v1/rl?mode=plain", "", nil)
	if w.Code != 429 || w.Header().Get("Retry-After") != "60" || w.Header().Get("RateLimit-Limit") != "" {
		t.Fatalf("plain: %d %v", w.Code, w.Header())
	}
	if w = do(s, http.MethodGet, "/v1/rl?mode=hint", "", nil); w.Code != 429 || w.Header().Get("Retry-After") != "90" {
		t.Fatalf("hint: %d %v", w.Code, w.Header())
	}
	if w = do(s, http.MethodGet, "/v1/rl?mode=noretry", "", nil); w.Code != 429 || w.Header().Get("Retry-After") != "" {
		t.Fatalf("noretry: %d %v", w.Code, w.Header())
	}
	if w = do(s, http.MethodGet, "/v1/rl?mode=none", "", nil); w.Code != 200 || w.Header().Get("RateLimit-Limit") != "" {
		t.Fatalf("no limiter: %d %v", w.Code, w.Header())
	}
	if w = do(s, http.MethodGet, "/v1/rl-raw", "", nil); w.Code != 429 || w.Header().Get("Retry-After") != "3" {
		t.Fatalf("raw: %d %v", w.Code, w.Header())
	}
	NoteRateLimit(context.Background(), errs.RateInfo{Limit: 1}) // outside a request: no-op
}

// TestWriteProblemKeepsHandlerRetryAfter: a Retry-After the handler set itself (Limen lockouts) wins over
// the problem's default.
func TestWriteProblemKeepsHandlerRetryAfter(t *testing.T) {
	s, _ := newServer(t, Options{})
	s.Router.Get("/v1/own", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "7")
		WriteProblem(w, r, errs.New(errs.CodeRateLimited, "own"))
	})
	if w := do(s, http.MethodGet, "/v1/own", "", nil); w.Code != 429 || w.Header().Values("Retry-After")[0] != "7" || len(w.Header().Values("Retry-After")) != 1 {
		t.Fatalf("%d %v", w.Code, w.Header())
	}
}
