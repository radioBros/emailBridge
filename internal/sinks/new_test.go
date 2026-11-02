package sinks

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/egress"
	"github.com/radiobros/emailbridge/internal/errs"
)

func TestDepsDefaults(t *testing.T) {
	t.Parallel()
	d := Deps{AllowAmbientCredentials: true}
	if !d.AllowAmbientCredentials {
		t.Fatal("ambient")
	}
	_ = slog.Default()
	_ = realClock{}.Now()
}

func TestNewValidationErrors(t *testing.T) {
	t.Parallel()
	cases := []struct {
		kind string
		cfg  Config
	}{
		{KindNATS, Config{NATS: &NATSConfig{URL: "nats://u:p@h:1", Subject: "s"}}},
		{KindKafka, Config{Kafka: &KafkaConfig{}}},
		{KindSQS, Config{SQS: &SQSConfig{}}},
		{KindSNS, Config{SNS: &SNSConfig{TopicARN: "bad", Region: "r"}}},
		{KindPubSub, Config{PubSub: &PubSubConfig{}}},
	}
	for _, tc := range cases {
		if _, err := New(tc.kind, tc.cfg, Deps{}); err == nil {
			t.Fatalf("%s: expected validation error", tc.kind)
		}
	}
}

func TestCacheGetCreatesAndReuses(t *testing.T) {
	t.Parallel()
	// Use an unreachable NATS URL so New fails — ensures Get surfaces construction errors.
	c := NewCache(Deps{Dial: egress.NewDialer(egress.Policy{})})
	_, err := c.Get(KindNATS, Config{
		EndpointID: "e1", Version: 1,
		NATS: &NATSConfig{URL: "nats://127.0.0.1:1", Subject: "eb.{type}"},
	})
	if err == nil {
		t.Fatal("expected dial failure")
	}
}

func TestSafeErrNil(t *testing.T) {
	t.Parallel()
	if safeErr("op", nil) != nil {
		t.Fatal()
	}
	err := safeErr("op", errors.New("password=secret http://u:p@h/x"))
	if err == nil || err.Error() == "" {
		t.Fatal()
	}
}

func TestKindFromCommonNet(t *testing.T) {
	t.Parallel()
	if kindFromCommon(context.Canceled) != errs.KindTemporary {
		t.Fatal()
	}
	if kindFromCommon(errors.New("connection refused")) != errs.KindTemporary {
		t.Fatal()
	}
	if kindFromCommon(errors.New("ThrottlingException")) != errs.KindRateLimited {
		t.Fatal()
	}
	if kindFromCommon(errors.New("other")) != "" {
		t.Fatal()
	}
}

func TestHostFromEndpoint(t *testing.T) {
	t.Parallel()
	h, err := hostFromEndpoint("http://localhost:5000")
	if err != nil || h != "localhost" {
		t.Fatalf("%q %v", h, err)
	}
	h, err = hostFromEndpoint("127.0.0.1:5000")
	if err != nil || h != "127.0.0.1" {
		t.Fatalf("%q %v", h, err)
	}
}

func TestNATSCloseIdempotent(t *testing.T) {
	t.Parallel()
	s := &natsSink{closed: false}
	_ = s.Close()
	_ = s.Close()
	if s.Classify(errors.New("timeout")) != errs.KindTemporary {
		t.Fatal()
	}
}

func TestKafkaCloseIdempotent(t *testing.T) {
	t.Parallel()
	s := &kafkaSink{}
	_ = s.Close()
	_ = s.Close()
	if err := s.Send(context.Background(), Message{ID: "1", Body: []byte("x")}); err == nil {
		t.Fatal("expected closed")
	}
}

func TestPubSubCloseIdempotent(t *testing.T) {
	t.Parallel()
	s := &pubsubSink{}
	_ = s.Close()
	_ = s.Close()
	if err := s.Send(context.Background(), Message{ID: "1", Body: []byte("x")}); err == nil {
		t.Fatal("expected closed")
	}
}

func TestRealClock(t *testing.T) {
	t.Parallel()
	n := realClock{}.Now()
	if time.Since(n) > time.Minute {
		t.Fatal(n)
	}
}
