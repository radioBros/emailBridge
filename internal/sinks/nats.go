package sinks

import (
	"context"
	"errors"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/radiobros/emailbridge/internal/egress"
	"github.com/radiobros/emailbridge/internal/errs"
)

type natsSink struct {
	cfg    NATSConfig
	deps   Deps
	nc     *nats.Conn
	js     jetstream.JetStream
	creds  string // temp file path
	mu     sync.Mutex
	closed bool
}

type natsDialer struct {
	d *egress.Dialer
}

func (n natsDialer) Dial(network, address string) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return n.d.DialContext(ctx, network, address, egress.HTTP)
}

func newNATS(cfg NATSConfig, deps Deps) (*natsSink, error) {
	opts := []nats.Option{
		nats.Name("emailbridge-sink"),
		nats.Timeout(10 * time.Second),
		nats.MaxReconnects(0),
	}
	if deps.Dial != nil {
		opts = append(opts, nats.SetCustomDialer(natsDialer{d: deps.Dial}))
	}
	var credsPath string
	if cfg.Creds != "" {
		f, err := os.CreateTemp("", "eb-nats-*.creds")
		if err != nil {
			return nil, safeErr("sinks.nats", err)
		}
		credsPath = f.Name()
		if _, err := f.WriteString(cfg.Creds); err != nil {
			_ = f.Close()
			_ = os.Remove(credsPath)
			return nil, safeErr("sinks.nats", err)
		}
		if err := f.Close(); err != nil {
			_ = os.Remove(credsPath)
			return nil, safeErr("sinks.nats", err)
		}
		opts = append(opts, nats.UserCredentials(credsPath))
	}
	nc, err := nats.Connect(cfg.URL, opts...)
	if err != nil {
		if credsPath != "" {
			_ = os.Remove(credsPath)
		}
		return nil, safeErr("sinks.nats", err)
	}
	s := &natsSink{cfg: cfg, deps: deps, nc: nc, creds: credsPath}
	if cfg.JetStream {
		js, err := jetstream.New(nc)
		if err != nil {
			_ = s.Close()
			return nil, safeErr("sinks.nats", err)
		}
		s.js = js
	}
	return s, nil
}

func (s *natsSink) Send(ctx context.Context, m Message) error {
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return errs.New(errs.CodeServiceUnavailable, "nats sink closed")
	}
	subject := ExpandTemplate(s.cfg.Subject, m)
	msg := &nats.Msg{
		Subject: subject,
		Data:    m.Body,
		Header:  nats.Header{},
	}
	msg.Header.Set(nats.MsgIdHdr, m.ID)
	for k, v := range m.Headers {
		if strings.EqualFold(k, nats.MsgIdHdr) {
			continue
		}
		msg.Header.Set(k, v)
	}
	if s.js != nil {
		if _, ok := ctx.Deadline(); !ok {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
		}
		_, err := s.js.PublishMsg(ctx, msg, jetstream.WithMsgID(m.ID))
		return safeErr("sinks.nats", err)
	}
	if err := s.nc.PublishMsg(msg); err != nil {
		return safeErr("sinks.nats", err)
	}
	if err := s.nc.FlushTimeout(10 * time.Second); err != nil {
		return safeErr("sinks.nats", err)
	}
	return nil
}

func (s *natsSink) Classify(err error) errs.Kind {
	if err == nil {
		return errs.KindInternal
	}
	if k := kindFromCommon(err); k != "" {
		return k
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "authorization") || strings.Contains(msg, "authentication") ||
		strings.Contains(msg, "permissions violation"):
		return errs.KindAuth
	case strings.Contains(msg, "no responders"), strings.Contains(msg, "timeout"),
		strings.Contains(msg, "connection"), strings.Contains(msg, "i/o timeout"),
		strings.Contains(msg, "broken pipe"), strings.Contains(msg, "unavailable"):
		return errs.KindTemporary
	case strings.Contains(msg, "invalid"), strings.Contains(msg, "bad subject"):
		return errs.KindValidation
	default:
		if errors.Is(err, nats.ErrInvalidConnection) || errors.Is(err, nats.ErrConnectionClosed) {
			return errs.KindTemporary
		}
		return errs.KindTemporary
	}
}

func (s *natsSink) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	if s.nc != nil {
		s.nc.Close()
	}
	if s.creds != "" {
		_ = os.Remove(s.creds)
		s.creds = ""
	}
	return nil
}
