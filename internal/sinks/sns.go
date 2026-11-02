package sinks

import (
	"context"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	"github.com/aws/aws-sdk-go-v2/service/sns/types"

	"github.com/radiobros/emailbridge/internal/errs"
)

type snsAPI interface {
	Publish(ctx context.Context, params *sns.PublishInput, optFns ...func(*sns.Options)) (*sns.PublishOutput, error)
}

type snsSink struct {
	cfg    SNSConfig
	client snsAPI
	mu     sync.Mutex
	closed bool
}

func newSNS(ctx context.Context, cfg SNSConfig, deps Deps) (*snsSink, error) {
	awsCfg, err := loadAWSConfig(ctx, cfg.Region, cfg.Endpoint, cfg.AccessKeyID, cfg.SecretAccessKey, deps.Dial)
	if err != nil {
		return nil, err
	}
	client := sns.NewFromConfig(awsCfg, func(o *sns.Options) {
		if cfg.Endpoint != "" {
			o.BaseEndpoint = aws.String(cfg.Endpoint)
		}
	})
	return &snsSink{cfg: cfg, client: client}, nil
}

func (s *snsSink) Send(ctx context.Context, m Message) error {
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return errs.New(errs.CodeServiceUnavailable, "sns sink closed")
	}
	in := &sns.PublishInput{
		TopicArn: aws.String(s.cfg.TopicARN),
		Message:  aws.String(string(m.Body)),
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
	if IsFIFOTopic(s.cfg.TopicARN) {
		in.MessageGroupId = aws.String(m.AccountID)
		in.MessageDeduplicationId = aws.String(m.ID)
	}
	_, err := s.client.Publish(ctx, in)
	return safeErr("sinks.sns", err)
}

func (s *snsSink) Classify(err error) errs.Kind { return classifyAWS(err) }

func (s *snsSink) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return nil
}
