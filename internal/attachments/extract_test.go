package attachments

import (
	"strings"
	"testing"
)

func sampleMIME() []byte {
	return []byte("MIME-Version: 1.0\r\n" +
		"Content-Type: multipart/mixed; boundary=b\r\n" +
		"\r\n" +
		"--b\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n" +
		"\r\n" +
		"hello\r\n" +
		"--b\r\n" +
		"Content-Type: application/pdf\r\n" +
		"Content-Disposition: attachment; filename=\"a.pdf\"\r\n" +
		"Content-Transfer-Encoding: base64\r\n" +
		"\r\n" +
		"cGRm\r\n" +
		"--b--\r\n")
}

func TestListAndExtractParts(t *testing.T) {
	parts, err := listParts(sampleMIME())
	if err != nil {
		t.Fatal(err)
	}
	if len(parts) != 1 || parts[0].Filename != "a.pdf" || string(parts[0].Body) != "pdf" {
		t.Fatalf("%+v", parts)
	}
	p, err := extractPart(sampleMIME(), parts[0].PartID)
	if err != nil || p.ETag == "" || !strings.EqualFold(p.ContentType, "application/pdf") {
		t.Fatalf("%+v %v", p, err)
	}
	if _, err := extractPart(sampleMIME(), "9.9"); err == nil {
		t.Fatal("expected missing part")
	}
}
