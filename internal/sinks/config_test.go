package sinks

import (
	"strings"
	"testing"
)

func TestNATSConfigValidate(t *testing.T) {
	t.Parallel()
	ok := NATSConfig{URL: "nats://localhost:4222", Subject: "eb.{type}"}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	cases := []NATSConfig{
		{Subject: "eb.{type}"},
		{URL: "nats://user:pass@localhost:4222", Subject: "eb.{type}"},
		{URL: "http://localhost:4222", Subject: "eb.{type}"},
		{URL: "nats://localhost:4222", Subject: ""},
		{URL: "nats://localhost:4222", Subject: "eb.{unknown}"},
	}
	for _, c := range cases {
		if err := c.Validate(); err == nil {
			t.Fatalf("expected error for %+v", c)
		}
	}
}

func TestKafkaConfigValidate(t *testing.T) {
	t.Parallel()
	ok := KafkaConfig{Brokers: []string{"localhost:9092"}, Topic: "eb-events"}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (KafkaConfig{Brokers: []string{"host:9092"}, Topic: "t", SASLMechanism: "PLAIN"}).Validate(); err == nil {
		t.Fatal("expected sasl username required")
	}
	if err := (KafkaConfig{Brokers: []string{"user@host:9092"}, Topic: "t"}).Validate(); err == nil {
		t.Fatal("expected credentials rejected")
	}
	if err := (KafkaConfig{Brokers: []string{"h:9092"}, Topic: "t", SASLMechanism: "GSSAPI"}).Validate(); err == nil {
		t.Fatal("expected bad mechanism")
	}
}

func TestSQSConfigValidate(t *testing.T) {
	t.Parallel()
	ok := SQSConfig{QueueURL: "https://sqs.us-east-1.amazonaws.com/123/q", Region: "us-east-1"}
	if err := ok.Validate(true); err != nil {
		t.Fatal(err)
	}
	if err := ok.Validate(false); err == nil {
		t.Fatal("expected ambient credentials rejected")
	}
	withKeys := ok
	withKeys.AccessKeyID, withKeys.SecretAccessKey = "a", "b"
	if err := withKeys.Validate(false); err != nil {
		t.Fatal(err)
	}
	if err := (SQSConfig{QueueURL: "https://user:pass@sqs.example/q", Region: "us-east-1"}).Validate(true); err == nil {
		t.Fatal("expected userinfo rejected")
	}
	if err := (SQSConfig{QueueURL: "https://sqs.example/q", Region: "us-east-1", AccessKeyID: "a"}).Validate(true); err == nil {
		t.Fatal("expected both keys")
	}
}

func TestSNSConfigValidate(t *testing.T) {
	t.Parallel()
	ok := SNSConfig{TopicARN: "arn:aws:sns:us-east-1:123456789012:topic", Region: "us-east-1"}
	if err := ok.Validate(true); err != nil {
		t.Fatal(err)
	}
	if err := ok.Validate(false); err == nil {
		t.Fatal("expected ambient credentials rejected")
	}
	if err := (SNSConfig{TopicARN: "not-an-arn", Region: "us-east-1"}).Validate(true); err == nil {
		t.Fatal("expected bad arn")
	}
}

func TestPubSubConfigValidate(t *testing.T) {
	t.Parallel()
	ok := PubSubConfig{Project: "p", Topic: "t"}
	if err := ok.Validate(true); err != nil {
		t.Fatal(err)
	}
	if err := ok.Validate(false); err == nil {
		t.Fatal("expected ambient credentials rejected")
	}
	withCreds := ok
	withCreds.CredentialsJSON = `{"type":"service_account"}`
	if err := withCreds.Validate(false); err != nil {
		t.Fatal(err)
	}
	if err := (PubSubConfig{Project: "p", Topic: "t", EmulatorHost: "http://localhost:8085"}).Validate(true); err == nil {
		t.Fatal("expected scheme rejected")
	}
}

func TestIsFIFO(t *testing.T) {
	t.Parallel()
	if !IsFIFOQueue("https://sqs.us-east-1.amazonaws.com/123/q.fifo") {
		t.Fatal("expected fifo queue")
	}
	if IsFIFOQueue("https://sqs.us-east-1.amazonaws.com/123/q") {
		t.Fatal("expected standard queue")
	}
	if !IsFIFOTopic("arn:aws:sns:us-east-1:123:t.fifo") {
		t.Fatal("expected fifo topic")
	}
}

func TestExpandTemplate(t *testing.T) {
	t.Parallel()
	got := ExpandTemplate("eb.{type}.{accountId}", Message{Type: "message.received", AccountID: "acc_1"})
	if got != "eb.message.received.acc_1" {
		t.Fatalf("got %q", got)
	}
}

func TestRedactSecrets(t *testing.T) {
	t.Parallel()
	in := `connect nats://user:s3cret@broker:4222 password=hunter2`
	out := redactSecrets(in)
	if strings.Contains(out, "s3cret") || strings.Contains(out, "hunter2") {
		t.Fatalf("secrets leaked: %q", out)
	}
	if !strings.Contains(out, "nats://") {
		t.Fatalf("url scheme lost: %q", out)
	}
}
