// Package bus is EmailBridge's NATS layer (S4): an embedded or external NATS JetStream connection, the
// streams and KV buckets every node declares at startup, partitioned subjects, de-duplicated publishing
// and the lease primitive (claim / renew-with-TTL / release) used for account ownership, partitions and
// leader election. It is the only package that imports nats.go and nats-server (S0 §3.4).
package bus

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// Mode selects how the node reaches NATS (S9 EB_NATS_MODE).
type Mode string

// Modes.
const (
	ModeEmbedded Mode = "embedded"
	ModeExternal Mode = "external"
)

// Options configure Open.
type Options struct {
	Mode      Mode
	NodeID    string
	URL       string // external: comma-separated server URLs
	CredsFile string // external: credentials file path
	DataDir   string // embedded: JetStream store directory root ($EB_DATA_DIR)
	Listen    string // embedded: optional client listener "host:port"; empty = in-process only
	Replicas  int    // stream/KV replicas (1 small, 3 medium/large)
	Log       *slog.Logger
}

// Bus is an open NATS connection with JetStream.
type Bus struct {
	Conn   *nats.Conn
	JS     jetstream.JetStream
	srv    *server.Server
	o      Options
	leases jetstream.KeyValue
	cache  jetstream.KeyValue
	nodes  jetstream.KeyValue

	// self is this node's last registry entry and draining its drain flag (S4 §6), both carried by every
	// heartbeat so a draining node stays draining in eb-nodes.
	selfMu   sync.Mutex
	self     *NodeInfo
	draining atomic.Bool

	loadMu     sync.Mutex
	load       LoadProvider
	accountsFn func() int
	partLoad   map[string]int

	pauseMu   sync.Mutex
	paused    map[string]bool // stream name → paused
	pauseWait map[string]chan struct{}
}

// Open connects (starting the embedded server when Mode is embedded) and declares every stream and
// bucket idempotently (S4 §1). The caller holds the embedded lock when Mode is embedded.
func Open(ctx context.Context, o Options) (*Bus, error) {
	if o.Replicas <= 0 {
		o.Replicas = 1
	}
	if o.Log == nil {
		o.Log = slog.Default()
	}
	b := &Bus{o: o}
	var err error
	switch o.Mode {
	case ModeEmbedded, "":
		err = b.startEmbedded()
	case ModeExternal:
		err = b.connectExternal()
	default:
		err = fmt.Errorf("bus: unknown mode %q", o.Mode)
	}
	if err != nil {
		return nil, err
	}
	js, err := jetstream.New(b.Conn)
	if err != nil {
		b.Close()
		return nil, fmt.Errorf("bus: jetstream: %w", err)
	}
	b.JS = js
	if err := b.declare(ctx); err != nil {
		b.Close()
		return nil, err
	}
	return b, nil
}

func (b *Bus) startEmbedded() error {
	if b.o.DataDir == "" {
		return errors.New("bus: embedded mode needs a data directory")
	}
	opts := &server.Options{
		ServerName: "emailbridge-" + b.o.NodeID,
		JetStream:  true,
		StoreDir:   filepath.Join(b.o.DataDir, "nats"),
		DontListen: b.o.Listen == "",
		NoSigs:     true,
		NoLog:      true,
	}
	if b.o.Listen != "" {
		host, port, err := splitHostPort(b.o.Listen)
		if err != nil {
			return err
		}
		opts.Host, opts.Port = host, port
	}
	srv, err := server.NewServer(opts)
	if err != nil {
		return fmt.Errorf("bus: embedded server: %w", err)
	}
	go srv.Start()
	if !srv.ReadyForConnections(30 * time.Second) {
		srv.Shutdown()
		return errors.New("bus: embedded server not ready after 30s")
	}
	nc, err := nats.Connect(srv.ClientURL(), nats.InProcessServer(srv), nats.Name("emailbridge/"+b.o.NodeID))
	if err != nil {
		srv.Shutdown()
		return fmt.Errorf("bus: in-process connect: %w", err)
	}
	b.srv, b.Conn = srv, nc
	return nil
}

func (b *Bus) connectExternal() error {
	if b.o.URL == "" {
		return errors.New("bus: external mode needs EB_NATS_URL")
	}
	opts := []nats.Option{
		nats.Name("emailbridge/" + b.o.NodeID),
		nats.MaxReconnects(-1),
		nats.ReconnectWait(2 * time.Second),
		nats.ReconnectJitter(500*time.Millisecond, 2*time.Second),
		nats.DisconnectErrHandler(func(_ *nats.Conn, err error) {
			b.o.Log.Warn("nats disconnected", "err", err)
		}),
		nats.ReconnectHandler(func(c *nats.Conn) { b.o.Log.Info("nats reconnected", "server", c.ConnectedUrlRedacted()) }),
	}
	if b.o.CredsFile != "" {
		opts = append(opts, nats.UserCredentials(b.o.CredsFile))
	}
	nc, err := nats.Connect(b.o.URL, opts...)
	if err != nil {
		return fmt.Errorf("bus: connect: %w", err)
	}
	b.Conn = nc
	return nil
}

// Ready reports whether the connection is up and JetStream answers.
func (b *Bus) Ready(ctx context.Context) error {
	if b == nil || b.Conn == nil || !b.Conn.IsConnected() {
		return errors.New("bus: not connected")
	}
	_, err := b.JS.AccountInfo(ctx)
	return err
}

// Close drains the connection and stops the embedded server.
func (b *Bus) Close() {
	if b.Conn != nil {
		_ = b.Conn.Drain()
		deadline := time.Now().Add(5 * time.Second)
		for !b.Conn.IsClosed() && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		b.Conn.Close()
	}
	if b.srv != nil {
		b.srv.Shutdown()
		b.srv.WaitForShutdown()
	}
}
