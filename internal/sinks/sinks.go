package sinks

import (
	"context"
	"fmt"
	"time"

	"github.com/radiobros/emailbridge/internal/egress"
	"github.com/radiobros/emailbridge/internal/errs"
)

// Kind constants match webhookEndpoints.kind (S1).
const (
	KindNATS   = "nats"
	KindKafka  = "kafka"
	KindSQS    = "sqs"
	KindSNS    = "sns"
	KindPubSub = "pubsub"
)

// Message is one delivery payload for a sink (S7 §6).
type Message struct {
	ID        string // event id (dedup)
	Type      string // CloudEvents type
	Subject   string // CloudEvents subject (optional)
	AccountID string
	Headers   map[string]string // optional CE / custom attrs
	Body      []byte
}

// Sink sends messages to one configured destination.
type Sink interface {
	Send(ctx context.Context, m Message) error
	Classify(err error) errs.Kind
	Close() error
}

// Clock provides the current time (injectable in tests).
type Clock interface {
	Now() time.Time
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now().UTC() }

// Deps are shared dependencies for sink adapters.
type Deps struct {
	Dial *egress.Dialer
	// AllowAmbientCredentials permits the AWS default credential chain and Google ADC
	// when sinkConfig omits explicit keys. Wired from sinks.ambientCredentials (default false).
	AllowAmbientCredentials bool
}

// errAmbientDisabledMsg is returned when a cloud sink has no explicit keys and
// ambient instance identity is disabled (sinks.ambientCredentials=false).
const errAmbientDisabledMsg = "explicit credentials required; ambient instance credentials are disabled"

// New builds a sink for kind using already-decrypted cfg (S7 §6).
func New(kind string, cfg Config, deps Deps) (Sink, error) {
	switch kind {
	case KindNATS:
		if cfg.NATS == nil {
			return nil, errs.New(errs.CodeValidationFailed, "nats sinkConfig required")
		}
		if err := cfg.NATS.Validate(); err != nil {
			return nil, err
		}
		return newNATS(*cfg.NATS, deps)
	case KindKafka:
		if cfg.Kafka == nil {
			return nil, errs.New(errs.CodeValidationFailed, "kafka sinkConfig required")
		}
		if err := cfg.Kafka.Validate(); err != nil {
			return nil, err
		}
		return newKafka(*cfg.Kafka, deps)
	case KindSQS:
		if cfg.SQS == nil {
			return nil, errs.New(errs.CodeValidationFailed, "sqs sinkConfig required")
		}
		if err := cfg.SQS.Validate(deps.AllowAmbientCredentials); err != nil {
			return nil, err
		}
		return newSQS(context.Background(), *cfg.SQS, deps)
	case KindSNS:
		if cfg.SNS == nil {
			return nil, errs.New(errs.CodeValidationFailed, "sns sinkConfig required")
		}
		if err := cfg.SNS.Validate(deps.AllowAmbientCredentials); err != nil {
			return nil, err
		}
		return newSNS(context.Background(), *cfg.SNS, deps)
	case KindPubSub:
		if cfg.PubSub == nil {
			return nil, errs.New(errs.CodeValidationFailed, "pubsub sinkConfig required")
		}
		if err := cfg.PubSub.Validate(deps.AllowAmbientCredentials); err != nil {
			return nil, err
		}
		return newPubSub(context.Background(), *cfg.PubSub, deps)
	default:
		return nil, errs.New(errs.CodeValidationFailed, fmt.Sprintf("unknown sink kind %q", kind))
	}
}
