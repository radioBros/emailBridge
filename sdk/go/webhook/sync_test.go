package webhook

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestInSyncWithServer fails if sdk/go/webhook/verify.go drifts from pkg/webhookverify/verify.go.
func TestInSyncWithServer(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	sdkVerify := filepath.Join(filepath.Dir(thisFile), "verify.go")
	serverVerify := filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "pkg", "webhookverify", "verify.go"))

	sdkSrc, err := os.ReadFile(sdkVerify)
	if err != nil {
		t.Fatal(err)
	}
	serverSrc, err := os.ReadFile(serverVerify)
	if err != nil {
		t.Fatal(err)
	}

	norm := func(b []byte, pkg, errPrefix string) []byte {
		s := string(b)
		// Drop file header comments that mention the other path.
		lines := strings.Split(s, "\n")
		var keep []string
		for _, line := range lines {
			if strings.HasPrefix(line, "// Package ") || strings.HasPrefix(line, "// This file is a copy") {
				continue
			}
			keep = append(keep, line)
		}
		s = strings.Join(keep, "\n")
		s = strings.ReplaceAll(s, "package "+pkg, "package webhookverify")
		s = strings.ReplaceAll(s, errPrefix+": ", "webhookverify: ")
		return []byte(s)
	}

	a := norm(sdkSrc, "webhook", "webhook")
	b := norm(serverSrc, "webhookverify", "webhookverify")
	if !bytes.Equal(a, b) {
		t.Fatalf("sdk/go/webhook/verify.go drifted from pkg/webhookverify/verify.go — copy the server file and adjust package/error prefixes")
	}
}
