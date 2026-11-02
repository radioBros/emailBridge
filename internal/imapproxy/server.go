package imapproxy

import (
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"sync"
)

// Options configure the IMAP proxy listeners.
type Options struct {
	Enabled               bool
	Addr                  string
	AddrTLS               string
	TLSCertFile           string
	TLSKeyFile            string
	MaxConnectionsPerAcct int
	AuthRatePerIP         int
	AllowInsecureAuth     bool
}

// Server accepts IMAP proxy connections.
type Server struct {
	opts    Options
	handler *connHandler
	log     *slog.Logger

	mu        sync.Mutex
	listeners []net.Listener
	wg        sync.WaitGroup
	closed    bool
}

// NewServer builds a proxy server (not listening yet).
func NewServer(opts Options, h *connHandler, log *slog.Logger) *Server {
	if log == nil {
		log = slog.Default()
	}
	return &Server{opts: opts, handler: h, log: log}
}

// Start listens. No-op when disabled.
func (s *Server) Start() error {
	if !s.opts.Enabled {
		return nil
	}
	var tlsConf *tls.Config
	if s.opts.TLSCertFile != "" && s.opts.TLSKeyFile != "" {
		cert, err := tls.LoadX509KeyPair(s.opts.TLSCertFile, s.opts.TLSKeyFile)
		if err != nil {
			return fmt.Errorf("imapproxy tls: %w", err)
		}
		tlsConf = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
	}
	s.handler.tlsConf = tlsConf
	s.handler.insecure = s.opts.AllowInsecureAuth
	if s.opts.Addr != "" {
		ln, err := net.Listen("tcp", s.opts.Addr)
		if err != nil {
			return fmt.Errorf("imapproxy listen %s: %w", s.opts.Addr, err)
		}
		s.add(ln)
		s.wg.Add(1)
		go s.accept(ln, false)
	}
	if s.opts.AddrTLS != "" {
		if tlsConf == nil {
			return fmt.Errorf("imapproxy: EB_IMAP_PROXY_ADDR_TLS requires TLS cert/key")
		}
		ln, err := tls.Listen("tcp", s.opts.AddrTLS, tlsConf)
		if err != nil {
			return fmt.Errorf("imapproxy listen tls %s: %w", s.opts.AddrTLS, err)
		}
		s.add(ln)
		s.wg.Add(1)
		go s.accept(ln, true)
	}
	return nil
}

func (s *Server) add(ln net.Listener) {
	s.mu.Lock()
	s.listeners = append(s.listeners, ln)
	s.mu.Unlock()
}

func (s *Server) accept(ln net.Listener, _ bool) {
	defer s.wg.Done()
	s.log.Info("imapproxy listening", "addr", ln.Addr().String())
	for {
		c, err := ln.Accept()
		if err != nil {
			s.mu.Lock()
			closed := s.closed
			s.mu.Unlock()
			if closed {
				return
			}
			return
		}
		go s.handler.serve(c)
	}
}

// Close stops listeners.
func (s *Server) Close() error {
	s.mu.Lock()
	s.closed = true
	var first error
	for _, ln := range s.listeners {
		if err := ln.Close(); err != nil && first == nil {
			first = err
		}
	}
	s.listeners = nil
	s.mu.Unlock()
	s.wg.Wait()
	return first
}
