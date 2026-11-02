package messages

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/mailbox"
)

func TestValidateSearch_caps(t *testing.T) {
	big := strings.Repeat("x", 2000)
	if err := validateSearch(SearchInput{GmailRaw: big}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("gmailRaw: %v", err)
	}
	labs := make([]string, 21)
	for i := range labs {
		labs[i] = "l"
	}
	if err := validateSearch(SearchInput{Labels: labs}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("labels: %v", err)
	}
	n := int64(-1)
	if err := validateSearch(SearchInput{Larger: &n}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("larger: %v", err)
	}
	if err := validateSearch(SearchInput{}); err != nil {
		t.Fatal(err)
	}
}

func TestToSearchQuery(t *testing.T) {
	since := time.Now().UTC()
	q := toSearchQuery(SearchInput{
		Text: "a", Header: &HeaderCrit{Name: "X-T", Value: "1"}, Since: &since, UseOutlookSearch: true,
	})
	if q.Text != "a" || q.HeaderName != "X-T" || !q.UseOutlookSearch || q.Since == nil {
		t.Fatalf("%+v", q)
	}
}

func TestEmbedCIDMapper(t *testing.T) {
	raw := []byte("From: a\r\nMIME-Version: 1.0\r\nContent-Type: multipart/related; boundary=b\r\n\r\n" +
		"--b\r\nContent-Type: text/html\r\n\r\n<img src=\"cid:pic@x\">\r\n" +
		"--b\r\nContent-Type: image/png\r\nContent-ID: <pic@x>\r\nContent-Transfer-Encoding: base64\r\n\r\n" +
		"aGVsbG8=\r\n--b--\r\n")
	fn := embedCIDMapper(raw, embedPartMax, embedTotalMax)
	got := fn("pic@x")
	if !strings.HasPrefix(got, "data:image/png;base64,") {
		t.Fatalf("got %q", got)
	}
	if fn("missing") != "" {
		t.Fatal("missing cid")
	}
}

func TestIsAPIProvider(t *testing.T) {
	if !isAPIProvider("gmail") || !isAPIProvider("graph") || isAPIProvider("imap") {
		t.Fatal("provider check")
	}
}

type memAtts struct{ rows []AttachmentView }

func (m memAtts) ListByMessage(context.Context, string, string) ([]AttachmentView, error) {
	return m.rows, nil
}

func TestLoadAttachments(t *testing.T) {
	s := &Service{Attachments: memAtts{rows: []AttachmentView{{ID: "att_1", ContentType: "text/plain", Size: 1}}}}
	got, err := s.loadAttachments(context.Background(), "acc", "msg")
	if err != nil || len(got) != 1 {
		t.Fatalf("%v %v", got, err)
	}
}

func TestUnsupportedViaMailbox(t *testing.T) {
	err := mailbox.Unsupported("x")
	if errs.CodeOf(err) != errs.CodeUnsupportedForProvider {
		t.Fatal(err)
	}
}
