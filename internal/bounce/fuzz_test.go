package bounce

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func FuzzClassify(f *testing.F) {
	seeds := [][]byte{
		nil,
		[]byte(""),
		[]byte("From: a@b.com\r\n\r\nhi"),
		[]byte("not a message at all \x00\xff"),
		[]byte("Content-Type: multipart/report; report-type=delivery-status; boundary=x\r\n\r\n--x\r\n\r\n--x--\r\n"),
	}
	dir := filepath.Join("testdata", "corpus")
	if entries, err := os.ReadDir(dir); err == nil {
		n := 0
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".eml") {
				continue
			}
			raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				continue
			}
			seeds = append(seeds, raw)
			n++
			if n >= 20 {
				break
			}
		}
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		r, err := Classify(raw)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if r == nil {
			t.Fatal("nil result")
		}
		_ = r.Kind
	})
}
