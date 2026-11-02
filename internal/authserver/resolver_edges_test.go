package authserver

import (
	"context"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/errs"
)

func TestResolveNilAndBadProtocol(t *testing.T) {
	t.Parallel()
	var r *Resolver
	if _, _, _, err := r.Resolve(context.Background(), "acc", "imap"); errs.CodeOf(err) != errs.CodeAuthServerMisconfigured {
		t.Fatalf("%v", err)
	}
	r = &Resolver{cache: newMemCache(), breaker: newBreaker(), now: time.Now}
	if _, _, _, err := r.Resolve(context.Background(), "acc", "imap"); errs.CodeOf(err) != errs.CodeAuthServerMisconfigured {
		t.Fatalf("nil DB: %v", err)
	}
}

func TestCacheLenAndKey(t *testing.T) {
	t.Parallel()
	c := newMemCache()
	c.put("a", Creds{User: "u"}, time.Now().Add(time.Minute))
	if c.Len() != 1 {
		t.Fatal(c.Len())
	}
	if cacheKey("o", "a", "imap") != "o|a|imap" {
		t.Fatal(cacheKey("o", "a", "imap"))
	}
	r := &Resolver{cache: c}
	if r.CacheLen() != 1 {
		t.Fatal(r.CacheLen())
	}
	if (*Resolver)(nil).CacheLen() != 0 {
		t.Fatal("nil")
	}
}

func TestAsBytes(t *testing.T) {
	t.Parallel()
	if string(asBytes([]byte("ab"))) != "ab" || string(asBytes("cd")) != "cd" || asBytes(1) != nil {
		t.Fatal("asBytes")
	}
}

func TestHTTPClientFallback(t *testing.T) {
	t.Parallel()
	r := &Resolver{}
	c := r.httpClient(time.Second)
	if c == nil || c.Timeout != time.Second {
		t.Fatal(c)
	}
}
