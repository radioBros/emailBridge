package sinks

import (
	"context"
	"crypto/tls"
	"net"
	"strings"
	"sync"

	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/sasl"
	"github.com/twmb/franz-go/pkg/sasl/plain"
	"github.com/twmb/franz-go/pkg/sasl/scram"

	"github.com/radiobros/emailbridge/internal/egress"
	"github.com/radiobros/emailbridge/internal/errs"
)

type kafkaSink struct {
	cfg    KafkaConfig
	cl     *kgo.Client
	mu     sync.Mutex
	closed bool
}

func newKafka(cfg KafkaConfig, deps Deps) (*kafkaSink, error) {
	opts := []kgo.Opt{
		kgo.SeedBrokers(cfg.Brokers...),
		kgo.DefaultProduceTopic(cfg.Topic),
		kgo.RequiredAcks(kgo.AllISRAcks()),
		kgo.RecordPartitioner(kgo.StickyKeyPartitioner(nil)),
		kgo.AllowAutoTopicCreation(),
	}
	if mech := strings.ToUpper(strings.TrimSpace(cfg.SASLMechanism)); mech != "" {
		var m sasl.Mechanism
		switch mech {
		case "PLAIN":
			m = plain.Auth{User: cfg.SASLUsername, Pass: cfg.SASLPassword}.AsMechanism()
		case "SCRAM-SHA-256":
			m = scram.Auth{User: cfg.SASLUsername, Pass: cfg.SASLPassword}.AsSha256Mechanism()
		case "SCRAM-SHA-512":
			m = scram.Auth{User: cfg.SASLUsername, Pass: cfg.SASLPassword}.AsSha512Mechanism()
		}
		if m != nil {
			opts = append(opts, kgo.SASL(m))
		}
	}
	// franz-go forbids Dialer + DialTLSConfig together; wrap TLS inside Dialer when both are needed.
	if deps.Dial != nil {
		d := deps.Dial
		tlsCfg := (*tls.Config)(nil)
		if cfg.TLS {
			tlsCfg = &tls.Config{MinVersion: tls.VersionTLS12}
		}
		opts = append(opts, kgo.Dialer(func(ctx context.Context, network, host string) (net.Conn, error) {
			conn, err := d.DialContext(ctx, network, host, egress.HTTP)
			if err != nil {
				return nil, err
			}
			if tlsCfg == nil {
				return conn, nil
			}
			cfg := tlsCfg.Clone()
			if cfg.ServerName == "" {
				if h, _, err := net.SplitHostPort(host); err == nil {
					cfg.ServerName = h
				} else {
					cfg.ServerName = host
				}
			}
			tc := tls.Client(conn, cfg)
			if err := tc.HandshakeContext(ctx); err != nil {
				_ = conn.Close()
				return nil, err
			}
			return tc, nil
		}))
	} else if cfg.TLS {
		opts = append(opts, kgo.DialTLSConfig(&tls.Config{MinVersion: tls.VersionTLS12}))
	}
	cl, err := kgo.NewClient(opts...)
	if err != nil {
		return nil, safeErr("sinks.kafka", err)
	}
	return &kafkaSink{cfg: cfg, cl: cl}, nil
}

func (s *kafkaSink) Send(ctx context.Context, m Message) error {
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return errs.New(errs.CodeServiceUnavailable, "kafka sink closed")
	}
	topic := ExpandTemplate(s.cfg.Topic, m)
	headers := cloudEventBinaryHeaders(m)
	rec := &kgo.Record{
		Topic: topic,
		Key:   []byte(m.AccountID),
		Value: m.Body,
	}
	for k, v := range headers {
		rec.Headers = append(rec.Headers, kgo.RecordHeader{Key: k, Value: []byte(v)})
	}
	if err := s.cl.ProduceSync(ctx, rec).FirstErr(); err != nil {
		return safeErr("sinks.kafka", err)
	}
	return nil
}

func (s *kafkaSink) Classify(err error) errs.Kind {
	if err == nil {
		return errs.KindInternal
	}
	if k := kindFromCommon(err); k != "" {
		return k
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "sasl"), strings.Contains(msg, "authentication"),
		strings.Contains(msg, "authorization"), strings.Contains(msg, "unauthorized"):
		return errs.KindAuth
	case strings.Contains(msg, "unknown topic"), strings.Contains(msg, "unknown_topic"),
		strings.Contains(msg, "not found"):
		return errs.KindNotFound
	case strings.Contains(msg, "invalid"), strings.Contains(msg, "corrupt"):
		return errs.KindValidation
	case strings.Contains(msg, "throttl"):
		return errs.KindRateLimited
	default:
		return errs.KindTemporary
	}
}

func (s *kafkaSink) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	if s.cl != nil {
		s.cl.Close()
	}
	return nil
}
