package mime

import (
	"fmt"
	"strings"
	"testing"

	"github.com/radiobros/emailbridge/internal/errs"
)

// nested builds a message with `levels` nested multipart/mixed containers around one text part.
func nested(levels int) []byte {
	var b strings.Builder
	b.WriteString("From: a@example.test\r\nSubject: deep\r\nMIME-Version: 1.0\r\n")
	for i := 0; i < levels; i++ {
		fmt.Fprintf(&b, "Content-Type: multipart/mixed; boundary=\"b%d\"\r\n\r\n--b%d\r\n", i, i)
	}
	b.WriteString("Content-Type: text/plain\r\n\r\ninner\r\n")
	for i := levels - 1; i >= 0; i-- {
		fmt.Fprintf(&b, "--b%d--\r\n", i)
	}
	return []byte(b.String())
}

func TestWalkPartsDepthLimit(t *testing.T) {
	ok, err := ParseEntity(nested(MaxPartDepth))
	if err != nil {
		t.Fatal(err)
	}
	plain, _, _, err := TextParts(ok, 0)
	if err != nil || !strings.Contains(plain, "inner") {
		t.Fatalf("depth %d: plain=%q err=%v", MaxPartDepth, plain, err)
	}

	deep, err := ParseEntity(nested(MaxPartDepth + 2))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := TextParts(deep, 0); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("TextParts past the limit: err=%v", err)
	}
	deep, err = ParseEntity(nested(MaxPartDepth + 2)) // an Entity is a one-pass stream
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AttachmentsMeta(deep); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("AttachmentsMeta past the limit: err=%v", err)
	}
}
