// Command uistack boots EmailBridge + the provider simulator with seeded data for admin UI tests.
// Usage: go run ./test/uistack
// Prints one JSON line with baseUrl + credentials, writes web/e2e/.stack.json, then waits until killed.
package main

import (
	"encoding/json"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
)

func main() {
	st, err := startStack()
	if err != nil {
		slog.Error("boot failed", "err", err)
		os.Exit(1)
	}
	defer st.Close()

	info := seed(st)
	raw, err := json.Marshal(info)
	if err != nil {
		slog.Error("marshal failed", "err", err)
		os.Exit(1)
	}

	root := findRepoRoot()
	outPath := filepath.Join(root, "web", "e2e", ".stack.json")
	if err := os.MkdirAll(filepath.Dir(outPath), 0o750); err != nil {
		slog.Error("mkdir failed", "err", err)
		os.Exit(1)
	}
	if err := os.WriteFile(outPath, raw, 0o600); err != nil {
		slog.Error("write stack json failed", "err", err)
		os.Exit(1)
	}

	_, _ = os.Stdout.Write(append(raw, '\n'))
	slog.Info("ready (ctrl+c to stop)", "url", info.BaseURL)

	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	<-ch
	slog.Info("shutting down")
}
