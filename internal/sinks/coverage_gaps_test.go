package sinks

import (
	"errors"
	"testing"

	"github.com/aws/smithy-go"

	"github.com/radiobros/emailbridge/internal/errs"
)

func TestNilCacheGet(t *testing.T) {
	t.Parallel()
	var c *Cache
	s, err := c.Get(KindKafka, Config{Kafka: &KafkaConfig{Brokers: []string{"127.0.0.1:1"}, Topic: "t"}})
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	c.Evict("x", 1)
	_ = c.Close()
}

func TestRedactedErrorEmptyOp(t *testing.T) {
	t.Parallel()
	e := &redactedError{msg: "hello"}
	if e.Error() != "hello" {
		t.Fatal(e.Error())
	}
}

func TestClassifyAWSMessageFallbacks(t *testing.T) {
	t.Parallel()
	if classifyAWS(errors.New("User: arn is not authorized to perform")) != errs.KindAuth {
		t.Fatal("auth")
	}
	if classifyAWS(errors.New("queue does not exist")) != errs.KindNotFound {
		t.Fatal("not found")
	}
	if classifyAWS(&smithy.GenericAPIError{Code: "UnrecognizedClientException", Message: "x"}) != errs.KindAuth {
		t.Fatal()
	}
	if classifyAWS(awsHTTPErr(401, "no")) != errs.KindAuth {
		t.Fatal()
	}
	if classifyAWS(awsHTTPErr(502, "bad gateway")) != errs.KindTemporary {
		t.Fatal()
	}
}

func TestConfigValidateMoreBranches(t *testing.T) {
	t.Parallel()
	if err := (KafkaConfig{Brokers: []string{"", "h:9092"}, Topic: "t"}).Validate(); err == nil {
		t.Fatal("empty broker")
	}
	if err := (KafkaConfig{Brokers: []string{"http://user:pass@h:9092"}, Topic: "t"}).Validate(); err == nil {
		t.Fatal("userinfo broker url")
	}
	if err := (SQSConfig{QueueURL: "ftp://x", Region: "r"}).Validate(true); err == nil {
		t.Fatal("bad scheme")
	}
	if err := (SQSConfig{QueueURL: "https://q", Region: "r", Endpoint: "http://u:p@h"}).Validate(true); err == nil {
		t.Fatal("endpoint userinfo")
	}
	if err := (SNSConfig{TopicARN: "arn:aws:sns:r:1:t", Region: ""}).Validate(true); err == nil {
		t.Fatal("region")
	}
	if err := (SNSConfig{TopicARN: "arn:aws:sns:r:1:t", Region: "r", Endpoint: "http://u:p@h"}).Validate(true); err == nil {
		t.Fatal("sns endpoint")
	}
	if err := (SNSConfig{TopicARN: "arn:aws:sns:r:1:t", Region: "r", AccessKeyID: "a"}).Validate(true); err == nil {
		t.Fatal("keys")
	}
	if err := (PubSubConfig{Project: "p", Topic: "t", EmulatorHost: "user@host:1"}).Validate(true); err == nil {
		t.Fatal("emu creds")
	}
	if err := (PubSubConfig{Project: "p", Topic: "t-{bad}"}).Validate(true); err == nil {
		t.Fatal("bad placeholder")
	}
	if !IsFIFOQueue("not-a-url-but.fifo") {
		t.Fatal("fifo suffix without url")
	}
	_ = IsFIFOQueue("://bad")
}

func TestCloudEventDefaults(t *testing.T) {
	t.Parallel()
	h := cloudEventBinaryHeaders(Message{ID: "1", Type: "t"})
	if h["ce_source"] != "emailbridge" || h["content-type"] != "application/json" {
		t.Fatalf("%+v", h)
	}
	h = cloudEventBinaryHeaders(Message{ID: "1", Type: "t", Headers: map[string]string{"ce_id": "override", "ce_foo": "bar"}})
	if h["ce_foo"] != "bar" {
		t.Fatalf("%+v", h)
	}
	a := messageAttributes(Message{ID: "1", Type: "t", Headers: map[string]string{"eventId": "x", "other": "y"}})
	if a["eventId"] != "1" || a["other"] != "y" {
		t.Fatalf("%+v", a)
	}
}

func TestNATSClassifyMore(t *testing.T) {
	t.Parallel()
	s := &natsSink{}
	if s.Classify(nil) != errs.KindInternal {
		t.Fatal()
	}
	if s.Classify(errors.New("no responders")) != errs.KindTemporary {
		t.Fatal()
	}
	if s.Classify(errors.New("bad subject")) != errs.KindValidation {
		t.Fatal()
	}
}

func TestKafkaClassifyMore(t *testing.T) {
	t.Parallel()
	s := &kafkaSink{}
	if s.Classify(nil) != errs.KindInternal {
		t.Fatal()
	}
	if s.Classify(errors.New("INVALID_RECORD")) != errs.KindValidation {
		t.Fatal()
	}
}
