package sinks

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/radiobros/emailbridge/internal/errs"
)

type fakeSink struct {
	closed atomic.Bool
	id     string
}

func (f *fakeSink) Send(context.Context, Message) error { return nil }
func (f *fakeSink) Classify(error) errs.Kind            { return errs.KindTemporary }
func (f *fakeSink) Close() error {
	f.closed.Store(true)
	return nil
}

func TestCacheGetEvictClose(t *testing.T) {
	t.Parallel()
	// Exercise cache mechanics with a manual map injection via Evict/Close on real New failures,
	// and a thin stub path: put through unexported field after constructing.
	c := NewCache(Deps{})
	s1 := &fakeSink{id: "1"}
	s2 := &fakeSink{id: "2"}
	c.mu.Lock()
	c.m[cacheKey("ep1", 1)] = s1
	c.m[cacheKey("ep1", 2)] = s2
	c.mu.Unlock()

	got, err := c.Get("nats", Config{EndpointID: "ep1", Version: 1, NATS: &NATSConfig{URL: "nats://127.0.0.1:1", Subject: "x"}})
	if err != nil {
		t.Fatal(err)
	}
	if got != s1 {
		t.Fatal("expected cached sink")
	}

	c.Evict("ep1", 1)
	if !s1.closed.Load() {
		t.Fatal("evict should close")
	}
	c.mu.Lock()
	_, still := c.m[cacheKey("ep1", 1)]
	c.mu.Unlock()
	if still {
		t.Fatal("key should be gone")
	}

	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if !s2.closed.Load() {
		t.Fatal("close should close remaining")
	}
	if err := c.Close(); err != nil {
		t.Fatal("close idempotent")
	}
}

func TestNewUnknownKind(t *testing.T) {
	t.Parallel()
	_, err := New("http", Config{}, Deps{})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestNewMissingKindConfig(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{KindNATS, KindKafka, KindSQS, KindSNS, KindPubSub} {
		_, err := New(kind, Config{}, Deps{})
		if err == nil {
			t.Fatalf("%s: expected error", kind)
		}
	}
}
