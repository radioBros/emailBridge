package sinks

import (
	"context"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/egress"
)

func TestNewKafkaSASLAndTLSOptions(t *testing.T) {
	t.Parallel()
	deps := Deps{Dial: egress.NewDialer(egress.Policy{})}
	// Construction succeeds (client is lazy); close immediately.
	s, err := New(KindKafka, Config{Kafka: &KafkaConfig{
		Brokers: []string{"127.0.0.1:1"}, Topic: "t",
		TLS: true, SASLMechanism: "PLAIN", SASLUsername: "u", SASLPassword: "p",
	}}, deps)
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Close()

	s, err = New(KindKafka, Config{Kafka: &KafkaConfig{
		Brokers: []string{"127.0.0.1:1"}, Topic: "t",
		SASLMechanism: "SCRAM-SHA-256", SASLUsername: "u", SASLPassword: "p",
	}}, Deps{})
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Close()

	s, err = New(KindKafka, Config{Kafka: &KafkaConfig{
		Brokers: []string{"127.0.0.1:1"}, Topic: "t",
		TLS: true, SASLMechanism: "SCRAM-SHA-512", SASLUsername: "u", SASLPassword: "p",
	}}, Deps{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	_ = s.Send(ctx, Message{ID: "e", AccountID: "a", Type: "t", Body: []byte("b")})
	_ = s.Close()
}

func TestNewSQSAndSNSClients(t *testing.T) {
	t.Parallel()
	deps := Deps{Dial: egress.NewDialer(egress.Policy{})}
	s, err := New(KindSQS, Config{SQS: &SQSConfig{
		QueueURL: "http://127.0.0.1:9/000/q.fifo", Region: "us-east-1",
		Endpoint: "http://127.0.0.1:9", AccessKeyID: "a", SecretAccessKey: "b",
	}}, deps)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	_ = s.Send(ctx, Message{ID: "e", AccountID: "a", Body: []byte("x")})
	_ = s.Classify(context.DeadlineExceeded)
	_ = s.Close()

	s, err = New(KindSNS, Config{SNS: &SNSConfig{
		TopicARN: "arn:aws:sns:us-east-1:123456789012:t.fifo", Region: "us-east-1",
		Endpoint: "http://127.0.0.1:9", AccessKeyID: "a", SecretAccessKey: "b",
	}}, deps)
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Send(ctx, Message{ID: "e", AccountID: "a", Body: []byte("x")})
	_ = s.Close()
}

func TestNewPubSubEmulatorDial(t *testing.T) {
	t.Parallel()
	deps := Deps{Dial: egress.NewDialer(egress.Policy{}), AllowAmbientCredentials: true}
	s, err := New(KindPubSub, Config{PubSub: &PubSubConfig{
		Project: "p", Topic: "t-{accountId}", EmulatorHost: "127.0.0.1:9",
	}}, deps)
	if err != nil {
		// dial/check may fail at construction depending on client init
		t.Log(err)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	_ = s.Send(ctx, Message{ID: "e", AccountID: "a", Type: "ty", Body: []byte("x")})
	_ = s.Close()
}

func TestNewNATSCredsFile(t *testing.T) {
	t.Parallel()
	// Invalid creds content → connect should fail, but exercises temp-file path.
	_, err := New(KindNATS, Config{NATS: &NATSConfig{
		URL: "nats://127.0.0.1:1", Subject: "eb.{type}",
		Creds: "-----BEGIN NATS USER JWT-----\nbad\n",
	}}, Deps{Dial: egress.NewDialer(egress.Policy{})})
	if err == nil {
		t.Fatal("expected failure")
	}
}

func TestCacheLifecycleWithKafka(t *testing.T) {
	t.Parallel()
	c := NewCache(Deps{})
	cfg := Config{EndpointID: "ep", Version: 3, Kafka: &KafkaConfig{Brokers: []string{"127.0.0.1:1"}, Topic: "t"}}
	s1, err := c.Get(KindKafka, cfg)
	if err != nil {
		t.Fatal(err)
	}
	s2, err := c.Get(KindKafka, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if s1 != s2 {
		t.Fatal("expected reuse")
	}
	c.Evict("ep", 3)
	_ = c.Close()
}
