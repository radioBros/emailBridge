package apikeys

import (
	"context"
	"testing"

	"github.com/radiobros/emailbridge/internal/errs"
)

func TestTakeKeyRate(t *testing.T) {
	if err := takeKeyRate(context.Background(), "k1", nil); err != nil {
		t.Fatal(err)
	}
	if err := takeKeyRate(context.Background(), "k1", &RateLimit{RPS: 0, Burst: 10}); err != nil {
		t.Fatal(err)
	}
	key := "k-rl-" + t.Name()
	rl := &RateLimit{RPS: 100, Burst: 2}
	if err := takeKeyRate(context.Background(), key, rl); err != nil {
		t.Fatal(err)
	}
	if err := takeKeyRate(context.Background(), key, rl); err != nil {
		t.Fatal(err)
	}
	if err := takeKeyRate(context.Background(), key, rl); errs.CodeOf(err) != errs.CodeRateLimited {
		t.Fatalf("want rate_limited, got %v", err)
	}
}
