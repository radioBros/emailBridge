// Command emailbridge is the single EmailBridge binary. Subcommands:
//
//	serve        run the roles given by EB_ROLES (default: all)
//	healthcheck  call /readyz on the local listener (container healthcheck; distroless has no curl)
//	version      print build information
//
// Further subcommands (migrate, keys, secrets, config) are added by their work packages.
package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
)

// Set with -ldflags "-X main.version=… -X main.commit=… -X main.date=…".
var (
	version = "1.0.0"
	commit  = "none"
	date    = "unknown"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	cmd := "serve"
	if len(args) > 0 {
		cmd, args = args[0], args[1:]
	}
	switch cmd {
	case "serve":
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		if err := serve(ctx, args, stderr); err != nil {
			slog.New(slog.NewJSONHandler(stderr, nil)).Error("emailbridge stopped with an error", "err", err)
			return 1
		}
		return 0
	case "healthcheck":
		if err := healthcheck(args); err != nil {
			_, _ = fmt.Fprintln(stderr, "unhealthy:", err)
			return 1
		}
		return 0
	case "keys":
		return keysCmd(args, stdout, stderr)
	case "secrets":
		return secretsCmd(args, stdout, stderr)
	case "config":
		return configCmd(args, stdout, stderr)
	case "version", "--version", "-v":
		_, _ = fmt.Fprintf(stdout, "emailbridge %s (commit %s, built %s)\n", version, commit, date)
		return 0
	case "help", "--help", "-h":
		usage(stdout)
		return 0
	default:
		_, _ = fmt.Fprintf(stderr, "emailbridge: unknown command %q\n\n", cmd)
		usage(stderr)
		return 2
	}
}

func usage(w io.Writer) {
	_, _ = fmt.Fprint(w, `Usage: emailbridge <command>

Commands:
  serve        run the roles in EB_ROLES (default "all")
  healthcheck  exit 0 when the local node is ready
  keys         generate a local encryption key ("keys generate [key-id]")
  secrets      create the secret files a compose install needs ("secrets init [--dir ./secrets]")
  config       export / plan / apply an organisation's configuration as YAML ("config export|plan|apply …")
  version      print build information
`)
}
