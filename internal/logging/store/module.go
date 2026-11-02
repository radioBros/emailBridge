package store

import (
	"context"
	"errors"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/bus"
	"github.com/radiobros/emailbridge/internal/logging"
	"github.com/radiobros/emailbridge/internal/observability"
)

// Module runs on every node: it opens this node's store, attaches it and the tail hub to the log
// system, runs the sweeper and answers eb.logs.query for this node (when a *bus.Bus is provided). It
// provides *Store, *Hub and *Node to later modules (the query module uses *Node for local execution).
type Module struct {
	// Options configure the store; Node defaults to the app's node ID, Settings to System.Settings and
	// Log to the system-category logger of System.
	Options Options
	// System is the node's log system (required).
	System *logging.System
	// TailRate caps live tail per subscriber (default TailRate).
	TailRate int

	store *Store
	hub   *Hub
	node  *Node
	stop  func()
}

// Name implements app.Module.
func (m *Module) Name() string { return "logging.store" }

// Roles implements app.Module (platform module: every node).
func (m *Module) Roles() []app.Role { return nil }

// Start implements app.Module.
func (m *Module) Start(ctx context.Context, a *app.App) error {
	if m.System == nil {
		return errors.New("logstore: module needs the log system")
	}
	o := m.Options
	if o.Node == "" {
		o.Node = a.NodeID
	}
	if o.Settings == nil {
		o.Settings = m.System.Settings
	}
	if o.Log == nil {
		o.Log = m.System.For(logging.CatSystem, "logging.store")
	}
	s, err := Open(ctx, o)
	if err != nil {
		return err
	}
	m.store = s
	m.hub = NewHub(o.Node, m.TailRate)
	m.node = &Node{Store: s, Hub: m.hub, Log: o.Log}
	if b, ok := app.Get[*bus.Bus](a); ok && b != nil {
		m.node.Pub = b
		stop, err := m.node.Serve(b)
		if err != nil {
			m.hub.Close()
			_ = s.Close()
			return err
		}
		m.stop = stop
	}
	m.System.AddSink(s)
	m.System.AddSink(m.hub)
	if mtr, ok := app.Get[*observability.Metrics](a); ok {
		mtr.ObserveLogDisk(s)
	}
	app.Provide(a, s)
	app.Provide(a, m.hub)
	app.Provide(a, m.node)
	return nil
}

// Stop implements app.Module: it detaches the sinks after flushing what is queued, then closes.
func (m *Module) Stop(ctx context.Context) error {
	if m.store == nil {
		return nil
	}
	if m.stop != nil {
		m.stop()
	}
	_ = m.System.Flush(ctx)
	m.System.RemoveSink(TailSinkName)
	m.System.RemoveSink(SinkName)
	m.hub.Close()
	return m.store.Close()
}
