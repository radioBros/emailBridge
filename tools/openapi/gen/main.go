// Command gen builds the EmailBridge OpenAPI 3.1 document from the huma API without listening
// (S2 §1, S14 §7): go run ./tools/openapi/gen [-check] [-out path].
package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/radiobros/emailbridge/internal/httpapi"
)

func main() {
	out := flag.String("out", "api/openapi/openapi.yaml", "output path")
	check := flag.Bool("check", false, "regenerate in memory and fail if out differs")
	flag.Parse()

	doc, err := generate()
	if err != nil {
		fmt.Fprintf(os.Stderr, "openapi gen: %v\n", err)
		os.Exit(1)
	}
	if *check {
		existing, err := os.ReadFile(*out)
		if err != nil {
			fmt.Fprintf(os.Stderr, "openapi gen -check: read %s: %v\n", *out, err)
			os.Exit(1)
		}
		if !bytes.Equal(normalize(existing), normalize(doc)) {
			fmt.Fprintf(os.Stderr, "openapi gen -check: %s is out of date; run go run ./tools/openapi/gen\n", *out)
			os.Exit(1)
		}
		return
	}
	if err := os.MkdirAll(filepath.Dir(*out), 0o750); err != nil {
		fmt.Fprintf(os.Stderr, "openapi gen: %v\n", err)
		os.Exit(1)
	}
	if err := os.WriteFile(*out, doc, 0o644); err != nil { //nolint:gosec // G306: committed artefact
		fmt.Fprintf(os.Stderr, "openapi gen: write: %v\n", err)
		os.Exit(1)
	}
}

func generate() ([]byte, error) {
	s, err := httpapi.New(httpapi.Options{Version: "0.1.0", Env: "development"})
	if err != nil {
		return nil, err
	}
	registerImplemented(s)
	return s.API.OpenAPI().YAML()
}

func normalize(b []byte) []byte {
	b = bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n"))
	return bytes.TrimSpace(b)
}
