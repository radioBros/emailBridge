package throttle

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestBucketsTakeAndDelay(t *testing.T) {
	c := NewMemoryCounter()
	b := &Buckets{Counter: c, Limits: Limits{DomainPerMinute: 2, Providers: map[string]int{"gmail": 2}, RetryAfter: "5s"},
		Now: func() time.Time { return time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC) }}
	ctx := context.Background()
	org := "org_1"
	domains := []string{"gmail.com"}

	r, err := b.Take(ctx, org, domains)
	if err != nil || !r.OK {
		t.Fatalf("1: %+v %v", r, err)
	}
	r, err = b.Take(ctx, org, domains)
	if err != nil || !r.OK {
		t.Fatalf("2: %+v %v", r, err)
	}
	r, err = b.Take(ctx, org, domains)
	if err != nil || r.OK || r.Delay < 5*time.Second {
		t.Fatalf("3 over budget: %+v %v", r, err)
	}
}

func TestBucketsTwoNodesShareCounter(t *testing.T) {
	shared := NewMemoryCounter()
	now := time.Date(2026, 10, 26, 15, 30, 0, 0, time.UTC)
	mk := func() *Buckets {
		return &Buckets{Counter: shared, Limits: Limits{DomainPerMinute: 10, RetryAfter: "1s"}, Now: func() time.Time { return now }}
	}
	ctx := context.Background()
	var wg sync.WaitGroup
	var okN, delayN int
	var mu sync.Mutex
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b := mk()
			for range 8 {
				r, err := b.Take(ctx, "org_x", []string{"example.com"})
				if err != nil {
					t.Errorf("take: %v", err)
					return
				}
				mu.Lock()
				if r.OK {
					okN++
				} else {
					delayN++
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if okN > 10 {
		t.Fatalf("ok=%d delay=%d want ok≤10", okN, delayN)
	}
	if okN+delayN != 16 {
		t.Fatalf("ok=%d delay=%d", okN, delayN)
	}
	if delayN == 0 {
		t.Fatal("expected some delays under shared budget")
	}
}

func TestBucketsDomainOverride(t *testing.T) {
	c := NewMemoryCounter()
	b := &Buckets{Counter: c, Limits: Limits{DomainPerMinute: 100, Domains: map[string]int{"partner.example": 1}, RetryAfter: "1s"},
		Now: func() time.Time { return time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC) }}
	ctx := context.Background()
	r, _ := b.Take(ctx, "o", []string{"partner.example"})
	if !r.OK {
		t.Fatal("first")
	}
	r, _ = b.Take(ctx, "o", []string{"partner.example"})
	if r.OK {
		t.Fatal("second should delay")
	}
}

func TestProviderAndDomainHelpers(t *testing.T) {
	if DomainOf("A@Gmail.COM") != "gmail.com" || ProviderOf("gmail.com") != "gmail" {
		t.Fatal("gmail")
	}
	if ProviderOf("unknown.example") != "" {
		t.Fatal("unknown")
	}
	ds := DomainsFrom([]string{"a@x.com", "b@x.com", "c@y.com", "bad"})
	if len(ds) != 2 {
		t.Fatalf("%v", ds)
	}
}

func TestParseLimits(t *testing.T) {
	l := ParseLimits(`{"domainPerMinute":5,"retryAfter":"10s","providers":{"gmail":3}}`)
	if l.DomainPerMinute != 5 || l.RetryDelay() != 10*time.Second || l.ProviderLimit("gmail") != 3 {
		t.Fatalf("%+v", l)
	}
	l = ParseLimits(nil)
	if l.DomainPerMinute != 60 {
		t.Fatal(l)
	}
}
