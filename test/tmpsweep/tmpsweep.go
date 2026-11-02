// Package tmpsweep removes temp folders that earlier test runs left behind.
//
// The e2e harness and the UI test stack build an emailbridge binary (≈100 MB) into a fresh temp folder
// on every run. A run that is killed, or a package whose process exits without cleanup, leaves that
// folder behind; hundreds of runs filled the system drive. Builders call Sweep before creating a new
// folder so the leftovers never pile up.
package tmpsweep

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Sweep removes folders directly under os.TempDir() whose name starts with prefix and that were last
// modified more than olderThan ago. It is best effort: a folder whose binary is still running cannot
// be removed on Windows and is simply skipped, so a live run is never disturbed.
func Sweep(prefix string, olderThan time.Duration) {
	root := os.TempDir()
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-olderThan)
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), prefix) {
			continue
		}
		info, err := e.Info()
		if err != nil || info.ModTime().After(cutoff) {
			continue
		}
		_ = os.RemoveAll(filepath.Join(root, e.Name()))
	}
}
