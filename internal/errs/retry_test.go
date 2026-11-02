package errs

import (
	"net/http"
	"testing"
	"time"
)

func TestTokenBucketInfoAndSeconds(t *testing.T) {
	full := TokenBucketInfo(10, 10, 9) // one token used: full again in 6 s
	if full.Limit != 10 || full.Remaining != 9 || full.Reset != 6*time.Second {
		t.Fatalf("%+v", full)
	}
	empty := TokenBucketInfo(5, 5, 0.5) // half a token: next one in 6 s
	if empty.Remaining != 0 || empty.Reset != 6*time.Second {
		t.Fatalf("%+v", empty)
	}
	if r := TokenBucketInfo(5, 0, 5); r.Reset != 0 {
		t.Fatalf("%+v", r)
	}
	if Seconds(0) != "1" || Seconds(1500*time.Millisecond) != "2" || Seconds(time.Minute) != "60" {
		t.Fatal("Seconds")
	}
	h := http.Header{}
	SetRateHeaders(h, RateInfo{Limit: 3, Remaining: -2, Reset: -time.Second})
	if h.Get("RateLimit-Remaining") != "0" || h.Get("RateLimit-Reset") != "0" || h.Get("RateLimit-Limit") != "3" {
		t.Fatalf("%v", h)
	}
}

func TestProblemHeaders(t *testing.T) {
	if h := ProblemFrom(New(CodeNotFound, "x"), "", "").GetHeaders(); h != nil {
		t.Fatalf("404 has headers %v", h)
	}
	if h := ProblemFrom(New(CodeRateLimited, "x"), "", "").GetHeaders(); h.Get("Retry-After") != "60" {
		t.Fatalf("default %v", h)
	}
	rate := RateInfo{Limit: 2, Reset: 4 * time.Second}
	if h := ProblemFrom(New(CodeRateLimited, "x").WithRate(rate), "", "").GetHeaders(); h.Get("Retry-After") != "4" || h.Get("RateLimit-Limit") != "2" {
		t.Fatalf("rate %v", h)
	}
	if h := ProblemFrom(New(CodeQuotaAccountsExceeded, "x").WithoutRetryAfter(), "", "").GetHeaders(); h.Get("Retry-After") != "" {
		t.Fatalf("opt-out %v", h)
	}
	// A plain problem built elsewhere with status 429 still answers Retry-After.
	if h := (&Problem{Status: http.StatusTooManyRequests}).GetHeaders(); h.Get("Retry-After") != "60" {
		t.Fatalf("plain %v", h)
	}
	var nilP *Problem
	if nilP.GetHeaders() != nil {
		t.Fatal("nil")
	}
}
