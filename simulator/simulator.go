// Package simulator is EmailBridge's fake IMAP/SMTP provider for tests (S14 §2.2).
package simulator

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-smtp"

	"github.com/radiobros/emailbridge/simulator/mem"
	simpec "github.com/radiobros/emailbridge/simulator/pec"
)

// Options configure Listen addresses. Empty strings mean "127.0.0.1:0" (random port).
type Options struct {
	IMAPPlain    string // STARTTLS + plain auth allowed
	IMAPTLS      string // implicit TLS
	IMAPStartTLS string // alias of plain when set; if empty, IMAPPlain is used for STARTTLS
	SMTPPlain    string
	SMTPTLS      string // implicit TLS
	Control      string // HTTP control API
	Logger       *log.Logger
	// PEC enables PEC simulator mode (S17 §11). Nil = ordinary mail only.
	PEC *simpec.Config
}

// Addresses are the bound listen addresses (host:port).
type Addresses struct {
	IMAPPlain string `json:"imapPlain"`
	IMAPTLS   string `json:"imapTLS"`
	SMTPPlain string `json:"smtpPlain"`
	SMTPTLS   string `json:"smtpTLS"`
	Control   string `json:"control"`
}

// Simulator is one process serving IMAP + SMTP + control API.
type Simulator struct {
	opts   Options
	store  *mem.Server
	tlsCfg *tls.Config
	log    *log.Logger

	mu     sync.Mutex
	states map[string]*userState

	imapSrv   *imapserver.Server
	smtpPlain *smtp.Server
	smtpTLS   *smtp.Server
	httpSrv   *http.Server

	lns  []net.Listener
	addr Addresses

	smtpMu   sync.Mutex
	smtpRecv []ReceivedSMTP
	smtpSeq  int64

	oauthOnce   sync.Once
	oauthMu     sync.Mutex
	oauthTokens map[string]string // username(lower) → access token (P6.1)

	fleetCancel context.CancelFunc
	wg          sync.WaitGroup

	pec *simpec.Engine
}

// New builds a simulator (not started).
func New(opts Options) (*Simulator, error) {
	tlsCfg, err := selfSignedTLS()
	if err != nil {
		return nil, err
	}
	lg := opts.Logger
	if lg == nil {
		lg = log.New(io.Discard, "", 0)
	}
	sim := &Simulator{
		opts:   opts,
		store:  mem.New(),
		tlsCfg: tlsCfg,
		log:    lg,
		states: make(map[string]*userState),
	}
	if opts.PEC != nil {
		eng, err := simpec.New(opts.PEC, sim.pecInject)
		if err != nil {
			return nil, err
		}
		sim.pec = eng
	}
	return sim, nil
}

// PEC returns the PEC engine (nil when PEC mode is off).
func (sim *Simulator) PEC() *simpec.Engine { return sim.pec }

func (sim *Simulator) pecInject(user, mailbox string, raw []byte) error {
	u := sim.store.User(user)
	if u == nil {
		return fmt.Errorf("user %q not found", user)
	}
	_, _, err := u.InjectMessage(mailbox, raw, nil, time.Now())
	return err
}

func (sim *Simulator) stateOf(user string) *userState {
	sim.mu.Lock()
	defer sim.mu.Unlock()
	st, ok := sim.states[user]
	if !ok {
		st = newUserState()
		sim.states[user] = st
	}
	return st
}

// SessionStats returns the IMAP session counters of a user.
func (sim *Simulator) SessionStats(user string) SessionStats { return sim.stateOf(user).sessionStats() }

// Store exposes the mailbox store (tests / control).
func (sim *Simulator) Store() *mem.Server { return sim.store }

// Addr returns bound addresses (valid after Start).
func (sim *Simulator) Addr() Addresses { return sim.addr }

// Start listens on all configured addresses.
func (sim *Simulator) Start() error {
	caps := imap.CapSet{
		imap.CapIMAP4rev1:    {},
		imap.CapNamespace:    {},
		imap.CapUIDPlus:      {},
		imap.CapESearch:      {},
		imap.CapListExtended: {},
		imap.CapListStatus:   {},
		imap.CapMove:         {},
		imap.CapSpecialUse:   {},
		imap.CapLiteralPlus:  {},
		imap.CapChildren:     {},
	}

	sim.imapSrv = imapserver.New(&imapserver.Options{
		NewSession: func(c *imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			inner := sim.store.NewSession()
			return &faultSession{inner: inner, sim: sim, conn: c.NetConn(), bye: c}, nil, nil
		},
		Caps:         caps,
		TLSConfig:    sim.tlsCfg,
		InsecureAuth: true,
	})

	plainAddr := orAddr(sim.opts.IMAPPlain, "127.0.0.1:0")
	tlsAddr := orAddr(sim.opts.IMAPTLS, "127.0.0.1:0")
	smtpPlainAddr := orAddr(sim.opts.SMTPPlain, "127.0.0.1:0")
	smtpTLSAddr := orAddr(sim.opts.SMTPTLS, "127.0.0.1:0")
	ctrlAddr := orAddr(sim.opts.Control, "127.0.0.1:0")

	imapPlain, err := net.Listen("tcp", plainAddr)
	if err != nil {
		return err
	}
	imapTLSRaw, err := net.Listen("tcp", tlsAddr)
	if err != nil {
		_ = imapPlain.Close()
		return err
	}
	imapTLS := tls.NewListener(imapTLSRaw, sim.tlsCfg)

	smtpPlainLn, err := net.Listen("tcp", smtpPlainAddr)
	if err != nil {
		_ = imapPlain.Close()
		_ = imapTLSRaw.Close()
		return err
	}
	smtpTLSRaw, err := net.Listen("tcp", smtpTLSAddr)
	if err != nil {
		_ = imapPlain.Close()
		_ = imapTLSRaw.Close()
		_ = smtpPlainLn.Close()
		return err
	}
	smtpTLSLn := tls.NewListener(smtpTLSRaw, sim.tlsCfg)

	ctrlLn, err := net.Listen("tcp", ctrlAddr)
	if err != nil {
		_ = imapPlain.Close()
		_ = imapTLSRaw.Close()
		_ = smtpPlainLn.Close()
		_ = smtpTLSRaw.Close()
		return err
	}

	sim.lns = []net.Listener{imapPlain, imapTLSRaw, smtpPlainLn, smtpTLSRaw, ctrlLn}
	sim.addr = Addresses{
		IMAPPlain: imapPlain.Addr().String(),
		IMAPTLS:   imapTLS.Addr().String(),
		SMTPPlain: smtpPlainLn.Addr().String(),
		SMTPTLS:   smtpTLSLn.Addr().String(),
		Control:   ctrlLn.Addr().String(),
	}

	sim.smtpPlain = newSMTPServer(sim, false)
	sim.smtpTLS = newSMTPServer(sim, true)

	mux := sim.controlMux()
	sim.httpSrv = &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}

	sim.wg.Add(4)
	go func() { defer sim.wg.Done(); _ = sim.imapSrv.Serve(imapPlain) }()
	go func() { defer sim.wg.Done(); _ = sim.imapSrv.Serve(imapTLS) }()
	go func() { defer sim.wg.Done(); _ = sim.smtpPlain.Serve(smtpPlainLn) }()
	go func() { defer sim.wg.Done(); _ = sim.smtpTLS.Serve(smtpTLSLn) }()
	sim.wg.Add(1)
	go func() { defer sim.wg.Done(); _ = sim.httpSrv.Serve(ctrlLn) }()

	return nil
}

func newSMTPServer(sim *Simulator, implicitTLS bool) *smtp.Server {
	s := smtp.NewServer(&smtpBackend{sim: sim})
	s.Domain = "simulator.emailbridge.test"
	s.AllowInsecureAuth = true
	s.EnableSMTPUTF8 = true
	s.EnableDSN = true
	s.MaxMessageBytes = 25 << 20
	s.ReadTimeout = 5 * time.Minute
	s.WriteTimeout = 5 * time.Minute
	if implicitTLS {
		s.TLSConfig = sim.tlsCfg
	} else {
		s.TLSConfig = sim.tlsCfg // STARTTLS
	}
	_ = implicitTLS
	return s
}

func orAddr(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// Stop shuts everything down.
func (sim *Simulator) Stop() error {
	if sim.fleetCancel != nil {
		sim.fleetCancel()
	}
	if sim.pec != nil {
		sim.pec.Stop()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if sim.httpSrv != nil {
		_ = sim.httpSrv.Shutdown(ctx)
	}
	if sim.imapSrv != nil {
		_ = sim.imapSrv.Close()
	}
	if sim.smtpPlain != nil {
		_ = sim.smtpPlain.Close()
	}
	if sim.smtpTLS != nil {
		_ = sim.smtpTLS.Close()
	}
	for _, ln := range sim.lns {
		_ = ln.Close()
	}
	sim.wg.Wait()
	return nil
}

// AddressesJSON prints the bound ports as one JSON object.
func (sim *Simulator) AddressesJSON() string {
	b, _ := json.Marshal(sim.addr)
	return string(b)
}

// CreateUser adds a user with default folders.
func (sim *Simulator) CreateUser(username, password string) (*mem.User, error) {
	if sim.store.User(username) != nil {
		return nil, fmt.Errorf("user %q already exists", username)
	}
	u := mem.NewUser(username, password)
	u.EnsureDefaults()
	sim.store.AddUser(u)
	_ = sim.stateOf(username)
	return u, nil
}

// DeleteUser removes a user.
func (sim *Simulator) DeleteUser(username string) bool {
	sim.mu.Lock()
	delete(sim.states, username)
	sim.mu.Unlock()
	return sim.store.DeleteUser(username)
}

// Reset clears users, faults, and SMTP log.
func (sim *Simulator) Reset() {
	if sim.fleetCancel != nil {
		sim.fleetCancel()
		sim.fleetCancel = nil
	}
	sim.store.Reset()
	sim.mu.Lock()
	sim.states = make(map[string]*userState)
	sim.mu.Unlock()
	sim.ClearSMTP()
	if sim.pec != nil {
		sim.pec.Reset()
	}
}

// TLSConfig returns the self-signed config (for test clients that need the cert).
func (sim *Simulator) TLSConfig() *tls.Config { return sim.tlsCfg.Clone() }
