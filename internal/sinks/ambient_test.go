package sinks

import (
	"os"
	"strings"
	"testing"

	"github.com/radiobros/emailbridge/internal/egress"
	"github.com/radiobros/emailbridge/internal/errs"
)

func TestAmbientCredentialsRequiredByDefault(t *testing.T) {
	t.Parallel()
	deps := Deps{Dial: egress.NewDialer(egress.Policy{})} // AllowAmbientCredentials false

	_, err := New(KindSQS, Config{SQS: &SQSConfig{
		QueueURL: "https://sqs.us-east-1.amazonaws.com/123/q", Region: "us-east-1",
	}}, deps)
	if err == nil || !strings.Contains(err.Error(), errAmbientDisabledMsg) {
		t.Fatalf("sqs: %v", err)
	}
	if errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("sqs code %s", errs.CodeOf(err))
	}

	_, err = New(KindSNS, Config{SNS: &SNSConfig{
		TopicARN: "arn:aws:sns:us-east-1:123456789012:t", Region: "us-east-1",
	}}, deps)
	if err == nil || !strings.Contains(err.Error(), errAmbientDisabledMsg) {
		t.Fatalf("sns: %v", err)
	}

	_, err = New(KindPubSub, Config{PubSub: &PubSubConfig{
		Project: "p", Topic: "t", EmulatorHost: "127.0.0.1:8085",
	}}, deps)
	if err == nil || !strings.Contains(err.Error(), errAmbientDisabledMsg) {
		t.Fatalf("pubsub: %v", err)
	}
}

func TestAmbientCredentialsAllowedConstructs(t *testing.T) {
	t.Parallel()
	deps := Deps{Dial: egress.NewDialer(egress.Policy{}), AllowAmbientCredentials: true}

	s, err := New(KindSQS, Config{SQS: &SQSConfig{
		QueueURL: "http://127.0.0.1:9/000/q", Region: "us-east-1", Endpoint: "http://127.0.0.1:9",
	}}, deps)
	if err != nil {
		t.Fatalf("sqs ambient: %v", err)
	}
	_ = s.Close()

	s, err = New(KindSNS, Config{SNS: &SNSConfig{
		TopicARN: "arn:aws:sns:us-east-1:123456789012:t", Region: "us-east-1", Endpoint: "http://127.0.0.1:9",
	}}, deps)
	if err != nil {
		t.Fatalf("sns ambient: %v", err)
	}
	_ = s.Close()

	s, err = New(KindPubSub, Config{PubSub: &PubSubConfig{
		Project: "p", Topic: "t", EmulatorHost: "127.0.0.1:9",
	}}, deps)
	if err != nil {
		t.Logf("pubsub construct against closed port: %v", err)
		return
	}
	_ = s.Close()
}

func TestExplicitCredentialsWithoutAmbient(t *testing.T) {
	t.Parallel()
	deps := Deps{Dial: egress.NewDialer(egress.Policy{})}

	s, err := New(KindSQS, Config{SQS: &SQSConfig{
		QueueURL: "http://127.0.0.1:9/000/q", Region: "us-east-1", Endpoint: "http://127.0.0.1:9",
		AccessKeyID: "testing", SecretAccessKey: "testing",
	}}, deps)
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Close()

	s, err = New(KindSNS, Config{SNS: &SNSConfig{
		TopicARN: "arn:aws:sns:us-east-1:123456789012:t", Region: "us-east-1", Endpoint: "http://127.0.0.1:9",
		AccessKeyID: "testing", SecretAccessKey: "testing",
	}}, deps)
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
}

func TestPubSubNewDoesNotSetEmulatorEnv(t *testing.T) {
	t.Parallel()
	const key = "PUBSUB_EMULATOR_HOST"
	prev, had := os.LookupEnv(key)
	t.Cleanup(func() {
		if had {
			_ = os.Setenv(key, prev)
		} else {
			_ = os.Unsetenv(key)
		}
	})
	_ = os.Unsetenv(key)

	deps := Deps{AllowAmbientCredentials: true}
	s, err := New(KindPubSub, Config{PubSub: &PubSubConfig{
		Project: "p", Topic: "t", EmulatorHost: "127.0.0.1:9",
	}}, deps)
	if err != nil {
		// Construction may fail on dial; env must still be untouched.
		t.Log(err)
	} else {
		_ = s.Close()
	}
	if v, ok := os.LookupEnv(key); ok {
		t.Fatalf("PUBSUB_EMULATOR_HOST was set to %q", v)
	}
}
