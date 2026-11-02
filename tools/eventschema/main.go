// Command eventschema writes the event JSON Schemas generated from internal/events/types to api/events
// (S3 §2). With -check it fails when the committed files differ (CI drift check).
package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/radiobros/emailbridge/internal/events"
)

func main() {
	dir := flag.String("dir", "api/events", "output directory")
	check := flag.Bool("check", false, "fail if the files on disk differ from the generated ones")
	flag.Parse()
	if err := run(*dir, *check); err != nil {
		fmt.Fprintln(os.Stderr, "eventschema:", err)
		os.Exit(1)
	}
}

func run(dir string, check bool) error {
	files, err := events.SchemaFiles()
	if err != nil {
		return err
	}
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	var drift []string
	for _, n := range names {
		p := filepath.Join(dir, n)
		if check {
			cur, err := os.ReadFile(p) //nolint:gosec // G304: generated file names under the given directory
			if err != nil || !bytes.Equal(cur, files[n]) {
				drift = append(drift, n)
			}
			continue
		}
		if err := os.WriteFile(p, files[n], 0o644); err != nil { //nolint:gosec // G306: public schema files
			return err
		}
	}
	if len(drift) > 0 {
		return fmt.Errorf("schemas out of date (run go run ./tools/eventschema): %v", drift)
	}
	if !check {
		fmt.Printf("wrote %d schemas to %s\n", len(names), dir)
	}
	return nil
}
