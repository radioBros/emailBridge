package msa

import (
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/emersion/go-smtp"
)

// Options configure the MSA listeners.
type Options struct {
	Enabled           bool
	Addr              string // STARTTLS / plain
	AddrTLS           string // implicit TLS
	Domain            string
	TLSCertFile       string
	TLSKeyFile        string
	MaxMessageBytes   int64
	MaxRecipients     int
	RatePerAccount    int
	AuthRatePerIP     int
	AllowInsecureAuth bool
}

// Server runs one or two go-smtp listeners.
type Server struct {
	opts    Options
	backend *Backend
	log     *slog.Logger

	mu      sync.Mutex
	servers []*smtp.Server
}

// NewServer builds an MSA server (not listening yet).
func NewServer(opts Options, be *Backend, log *slog.Logger) *Server {
	if log == nil {
		log = slog.Default()
	}
	if opts.Domain == "" {
		opts.Domain = "localhost"
	}
	if opts.MaxMessageBytes <= 0 {
		opts.MaxMessageBytes = 25 << 20
	}
	if opts.MaxRecipients <= 0 {
		opts.MaxRecipients = 100
	}
	be.MaxBytes = opts.MaxMessageBytes
	be.MaxRcpt = opts.MaxRecipients
	return &Server{opts: opts, backend: be, log: log}
}

// Start listens on the configured addresses. No-op when disabled or both addrs empty.
func (s *Server) Start() error {
	if !s.opts.Enabled {
		return nil
	}
	var tlsConf *tls.Config
	if s.opts.TLSCertFile != "" && s.opts.TLSKeyFile != "" {
		cert, err := tls.LoadX509KeyPair(s.opts.TLSCertFile, s.opts.TLSKeyFile)
		if err != nil {
			return fmt.Errorf("msa tls: %w", err)
		}
		tlsConf = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
	}
	if s.opts.Addr != "" {
		srv := s.newSMTP(tlsConf, false)
		ln, err := net.Listen("tcp", s.opts.Addr)
		if err != nil {
			return fmt.Errorf("msa listen %s: %w", s.opts.Addr, err)
		}
		s.track(srv)
		go s.serve(srv, ln, s.opts.Addr)
	}
	if s.opts.AddrTLS != "" {
		if tlsConf == nil {
			return fmt.Errorf("msa: EB_SMTP_SERVER_ADDR_TLS requires TLS cert/key")
		}
		srv := s.newSMTP(tlsConf, true)
		ln, err := tls.Listen("tcp", s.opts.AddrTLS, tlsConf)
		if err != nil {
			return fmt.Errorf("msa listen tls %s: %w", s.opts.AddrTLS, err)
		}
		s.track(srv)
		go s.serve(srv, ln, s.opts.AddrTLS)
	}
	return nil
}

func (s *Server) newSMTP(tlsConf *tls.Config, implicit bool) *smtp.Server {
	srv := smtp.NewServer(s.backend)
	srv.Domain = s.opts.Domain
	srv.MaxMessageBytes = s.opts.MaxMessageBytes
	srv.MaxRecipients = s.opts.MaxRecipients
	srv.ReadTimeout = 60 * time.Second
	srv.WriteTimeout = 60 * time.Second
	srv.AllowInsecureAuth = s.opts.AllowInsecureAuth
	if !implicit && tlsConf != nil {
		srv.TLSConfig = tlsConf
	}
	return srv
}

func (s *Server) track(srv *smtp.Server) {
	s.mu.Lock()
	s.servers = append(s.servers, srv)
	s.mu.Unlock()
}

func (s *Server) serve(srv *smtp.Server, ln net.Listener, addr string) {
	s.log.Info("msa listening", "addr", addr)
	if err := srv.Serve(ln); err != nil {
		s.log.Info("msa stopped", "addr", addr, "err", err.Error())
	}
}

// Close stops all listeners.
func (s *Server) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var first error
	for _, srv := range s.servers {
		if err := srv.Close(); err != nil && first == nil {
			first = err
		}
	}
	s.servers = nil
	return first
}
