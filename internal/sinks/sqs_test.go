package sinks

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/sqs"
)

type fakeSQS struct {
	in *sqs.SendMessageInput
}

func (f *fakeSQS) SendMessage(_ context.Context, params *sqs.SendMessageInput, _ ...func(*sqs.Options)) (*sqs.SendMessageOutput, error) {
	f.in = params
	return &sqs.SendMessageOutput{}, nil
}

func TestSQSSendFIFOAttributes(t *testing.T) {
	t.Parallel()
	fake := &fakeSQS{}
	s := &sqsSink{
		cfg:    SQSConfig{QueueURL: "https://sqs.us-east-1.amazonaws.com/123/q.fifo", Region: "us-east-1"},
		client: fake,
	}
	err := s.Send(context.Background(), Message{
		ID: "evt_1", Type: "message.received", AccountID: "acc_1",
		Body: []byte(`{"ok":true}`), Headers: map[string]string{"x": "y"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if fake.in == nil {
		t.Fatal("no call")
	}
	if fake.in.MessageGroupId == nil || *fake.in.MessageGroupId != "acc_1" {
		t.Fatalf("group: %v", fake.in.MessageGroupId)
	}
	if fake.in.MessageDeduplicationId == nil || *fake.in.MessageDeduplicationId != "evt_1" {
		t.Fatalf("dedup: %v", fake.in.MessageDeduplicationId)
	}
	if fake.in.MessageAttributes["eventId"].StringValue == nil || *fake.in.MessageAttributes["eventId"].StringValue != "evt_1" {
		t.Fatalf("attrs: %+v", fake.in.MessageAttributes)
	}
	_ = s.Close()
	_ = s.Close()
	if err := s.Send(context.Background(), Message{ID: "x", Body: []byte("y")}); err == nil {
		t.Fatal("expected closed error")
	}
}

func TestSQSSendStandardNoFIFOFields(t *testing.T) {
	t.Parallel()
	fake := &fakeSQS{}
	s := &sqsSink{
		cfg:    SQSConfig{QueueURL: "https://sqs.us-east-1.amazonaws.com/123/q", Region: "us-east-1"},
		client: fake,
	}
	if err := s.Send(context.Background(), Message{ID: "evt", AccountID: "a", Body: []byte("b")}); err != nil {
		t.Fatal(err)
	}
	if fake.in.MessageGroupId != nil || fake.in.MessageDeduplicationId != nil {
		t.Fatal("standard queue must not set fifo fields")
	}
}
