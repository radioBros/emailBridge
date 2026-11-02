package sinks

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/sns"
)

type fakeSNS struct {
	in *sns.PublishInput
}

func (f *fakeSNS) Publish(_ context.Context, params *sns.PublishInput, _ ...func(*sns.Options)) (*sns.PublishOutput, error) {
	f.in = params
	return &sns.PublishOutput{}, nil
}

func TestSNSSendFIFOAttributes(t *testing.T) {
	t.Parallel()
	fake := &fakeSNS{}
	s := &snsSink{
		cfg:    SNSConfig{TopicARN: "arn:aws:sns:us-east-1:123456789012:t.fifo", Region: "us-east-1"},
		client: fake,
	}
	if err := s.Send(context.Background(), Message{
		ID: "evt_9", Type: "t", AccountID: "acc", Body: []byte("hi"),
	}); err != nil {
		t.Fatal(err)
	}
	if fake.in.MessageGroupId == nil || *fake.in.MessageGroupId != "acc" {
		t.Fatal("group")
	}
	if fake.in.MessageDeduplicationId == nil || *fake.in.MessageDeduplicationId != "evt_9" {
		t.Fatal("dedup")
	}
	if fake.in.MessageAttributes["eventId"].StringValue == nil || *fake.in.MessageAttributes["eventId"].StringValue != "evt_9" {
		t.Fatal("eventId attr")
	}
	_ = s.Close()
}
