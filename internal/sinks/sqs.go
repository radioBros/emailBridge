package sinks

import (
	"context"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"

	"github.com/radiobros/emailbridge/internal/errs"
)

type sqsAPI interface {
	SendMessage(ctx context.Context, params *sqs.SendMessageInput, optFns ...func(*sqs.Options)) (*sqs.SendMessageOutput, error)
}

type sqsSink struct {
	cfg    SQSConfig
	client sqsAPI
	mu     sync.Mutex
	closed bool
}

func newSQS(ctx context.Context, cfg SQSConfig, deps Deps) (*sqsSink, error) {
	awsCfg, err := loadAWSConfig(ctx, cfg.Region, cfg.Endpoint, cfg.AccessKeyID, cfg.SecretAccessKey, deps.Dial)
	if err != nil {
		return nil, err
	}
	client := sqs.NewFromConfig(awsCfg, func(o *sqs.Options) {
		if cfg.Endpoint != "" {
			o.BaseEndpoint = aws.String(cfg.Endpoint)
		}
	})
	return &sqsSink{cfg: cfg, client: client}, nil
}

func (s *sqsSink) Send(ctx context.Context, m Message) error {
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return errs.New(errs.CodeServiceUnavailable, "sqs sink closed")
	}
	in := &sqs.SendMessageInput{
		QueueUrl:    aws.String(s.cfg.QueueURL),
		MessageBody: aws.String(string(m.Body)),
	}
	attrs := messageAttributes(m)
	if len(attrs) > 0 {
		in.MessageAttributes = map[string]types.MessageAttributeValue{}
		for k, v := range attrs {
			in.MessageAttributes[k] = types.MessageAttributeValue{
				DataType:    aws.String("String"),
				StringValue: aws.String(v),
			}
		}
	}
	if IsFIFOQueue(s.cfg.QueueURL) {
		in.MessageGroupId = aws.String(m.AccountID)
		in.MessageDeduplicationId = aws.String(m.ID)
	}
	_, err := s.client.SendMessage(ctx, in)
	return safeErr("sinks.sqs", err)
}

func (s *sqsSink) Classify(err error) errs.Kind { return classifyAWS(err) }

func (s *sqsSink) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return nil
}
