package sinks

import (
	"context"
	"net"
	"strings"
	"sync"

	"cloud.google.com/go/pubsub/v2"
	"google.golang.org/api/option"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/radiobros/emailbridge/internal/egress"
	"github.com/radiobros/emailbridge/internal/errs"
)

type pubsubSink struct {
	cfg       PubSubConfig
	client    *pubsub.Client
	publisher *pubsub.Publisher
	mu        sync.Mutex
	closed    bool
}

func newPubSub(ctx context.Context, cfg PubSubConfig, deps Deps) (*pubsubSink, error) {
	opts := []option.ClientOption{}
	if cfg.CredentialsJSON != "" {
		opts = append(opts, option.WithAuthCredentialsJSON(option.ServiceAccount, []byte(cfg.CredentialsJSON)))
	}
	if cfg.EmulatorHost != "" {
		host := cfg.EmulatorHost
		h, _, err := net.SplitHostPort(host)
		if err != nil {
			h = host
		}
		var dialer func(context.Context, string) (net.Conn, error)
		if deps.Dial != nil {
			if err := deps.Dial.CheckURLHost(ctx, h, egress.HTTP); err != nil {
				return nil, err
			}
			d := deps.Dial
			dialer = func(ctx context.Context, addr string) (net.Conn, error) {
				return d.DialContext(ctx, "tcp", addr, egress.HTTP)
			}
		} else {
			nd := &net.Dialer{}
			dialer = func(ctx context.Context, addr string) (net.Conn, error) {
				return nd.DialContext(ctx, "tcp", addr)
			}
		}
		// Never set PUBSUB_EMULATOR_HOST — that mutates process-global state across tenants.
		opts = append(opts,
			option.WithEndpoint(host),
			option.WithoutAuthentication(),
			option.WithGRPCDialOption(grpc.WithTransportCredentials(insecure.NewCredentials())),
			option.WithGRPCDialOption(grpc.WithContextDialer(dialer)),
		)
	}
	client, err := pubsub.NewClient(ctx, cfg.Project, opts...)
	if err != nil {
		return nil, safeErr("sinks.pubsub", err)
	}
	s := &pubsubSink{cfg: cfg, client: client}
	if !strings.Contains(cfg.Topic, "{") {
		pub := client.Publisher(cfg.Topic)
		pub.EnableMessageOrdering = true
		s.publisher = pub
	}
	return s, nil
}

func (s *pubsubSink) Send(ctx context.Context, m Message) error {
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return errs.New(errs.CodeServiceUnavailable, "pubsub sink closed")
	}
	pub := s.publisher
	if pub == nil || strings.Contains(s.cfg.Topic, "{") {
		topic := ExpandTemplate(s.cfg.Topic, m)
		pub = s.client.Publisher(topic)
		pub.EnableMessageOrdering = true
	}
	msg := &pubsub.Message{
		Data:        m.Body,
		Attributes:  messageAttributes(m),
		OrderingKey: m.AccountID,
	}
	_, err := pub.Publish(ctx, msg).Get(ctx)
	return safeErr("sinks.pubsub", err)
}

func (s *pubsubSink) Classify(err error) errs.Kind {
	if err == nil {
		return errs.KindInternal
	}
	if k := kindFromCommon(err); k != "" {
		return k
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "permission"), strings.Contains(msg, "unauthenticated"),
		strings.Contains(msg, "unauthorized"), strings.Contains(msg, "forbidden"):
		return errs.KindAuth
	case strings.Contains(msg, "not found"), strings.Contains(msg, "notfound"):
		return errs.KindNotFound
	case strings.Contains(msg, "invalid"), strings.Contains(msg, "failed_precondition"):
		return errs.KindValidation
	case strings.Contains(msg, "resource_exhausted"), strings.Contains(msg, "resourceexhausted"),
		strings.Contains(msg, "rate"):
		return errs.KindRateLimited
	default:
		return errs.KindTemporary
	}
}

func (s *pubsubSink) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	if s.publisher != nil {
		s.publisher.Stop()
	}
	if s.client != nil {
		return s.client.Close()
	}
	return nil
}
