package attachments

import (
	"strings"
	"testing"
)

func TestContentDisposition(t *testing.T) {
	d := contentDisposition("attachment", "report.pdf")
	if !strings.Contains(d, `filename="report.pdf"`) || !strings.HasPrefix(d, "attachment") {
		t.Fatal(d)
	}
	d = contentDisposition("inline", "café.txt")
	if !strings.Contains(d, "filename*=UTF-8''") || !strings.HasPrefix(d, "inline") {
		t.Fatal(d)
	}
	d = contentDisposition("", "")
	if d != "attachment" {
		t.Fatal(d)
	}
	if asciiFilename("a\"b") != "a_b" {
		t.Fatal(asciiFilename(`a"b`))
	}
}
