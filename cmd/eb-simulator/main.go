// Command eb-simulator runs the EmailBridge provider simulator (IMAP/SMTP + control API).
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/radiobros/emailbridge/simulator"
)

func main() {
	var opts simulator.Options
	flag.StringVar(&opts.IMAPPlain, "imap", envOr("EB_SIM_IMAP", "127.0.0.1:0"), "IMAP plain/STARTTLS listen")
	flag.StringVar(&opts.IMAPTLS, "imap-tls", envOr("EB_SIM_IMAP_TLS", "127.0.0.1:0"), "IMAP implicit TLS listen")
	flag.StringVar(&opts.SMTPPlain, "smtp", envOr("EB_SIM_SMTP", "127.0.0.1:0"), "SMTP plain/STARTTLS listen")
	flag.StringVar(&opts.SMTPTLS, "smtp-tls", envOr("EB_SIM_SMTP_TLS", "127.0.0.1:0"), "SMTP implicit TLS listen")
	flag.StringVar(&opts.Control, "control", envOr("EB_SIM_CONTROL", "127.0.0.1:0"), "control API listen")
	flag.Parse()
	opts.Logger = log.New(os.Stderr, "eb-simulator: ", log.LstdFlags)

	sim, err := simulator.New(opts)
	if err != nil {
		log.Fatal(err)
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
