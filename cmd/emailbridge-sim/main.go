// Command emailbridge-sim is the shipped provider simulator (IMAP/SMTP + control API).
// See docs/ops/simulator.md.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/radiobros/emailbridge/simulator"
	simpec "github.com/radiobros/emailbridge/simulator/pec"
)

func main() {
	var opts simulator.Options
	flag.StringVar(&opts.IMAPPlain, "imap", envOr("EB_SIM_IMAP", "127.0.0.1:1143"), "IMAP plain/STARTTLS listen (host:port)")
	flag.StringVar(&opts.IMAPTLS, "imap-tls", envOr("EB_SIM_IMAP_TLS", "127.0.0.1:1993"), "IMAP implicit TLS listen")
	flag.StringVar(&opts.SMTPPlain, "smtp", envOr("EB_SIM_SMTP", "127.0.0.1:1025"), "SMTP plain/STARTTLS listen")
	flag.StringVar(&opts.SMTPTLS, "smtp-tls", envOr("EB_SIM_SMTP_TLS", "127.0.0.1:1465"), "SMTP implicit TLS listen")
	flag.StringVar(&opts.Control, "control", envOr("EB_SIM_CONTROL", "127.0.0.1:18080"), "HTTP control API listen")
	pecDomain := flag.String("pec-domain", envOr("EB_SIM_PEC_DOMAIN", ""), "enable PEC mode for this domain (e.g. pec.sim.test)")
	pecCA := flag.String("pec-ca", envOr("EB_SIM_PEC_CA", ""), "write PEC test CA PEM to this path (for EB_PEC_TRUST_ANCHORS)")
	tlsOnly := flag.Bool("tls-only", false, "print a note that IMAP/SMTP TLS listeners use an ephemeral self-signed cert (always on)")
	flag.Parse()
	if *tlsOnly {
		fmt.Fprintln(os.Stderr, "emailbridge-sim: TLS listeners always use a process-local self-signed certificate; clients must skip verify or trust the printed ports only.")
	}
	opts.Logger = log.New(os.Stderr, "emailbridge-sim: ", log.LstdFlags)
	if *pecDomain != "" {
		opts.PEC = &simpec.Config{Domains: []simpec.Domain{{
			Name: *pecDomain, Provider: "Gestore Simulato S.p.A.", // IT
			SystemAddress: "posta-certificata@" + *pecDomain,
		}}}
	}

	sim, err := simulator.New(opts)
	if err != nil {
		log.Fatal(err)
	}
	if *pecCA != "" {
		if sim.PEC() == nil {
			log.Fatal("emailbridge-sim: -pec-ca requires -pec-domain")
		}
		if err := sim.PEC().WriteCAPEM(*pecCA); err != nil {
			log.Fatal(err)
		}
	}
	if err := sim.Start(); err != nil {
		log.Fatal(err)
	}
	fmt.Println(sim.AddressesJSON())

	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	<-ch
	_ = sim.Stop()
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
