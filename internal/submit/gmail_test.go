package submit

import (
	"context"
	"testing"
)

type stubGmail struct {
	err error
	id  string
}

func (s stubGmail) Send(context.Context, string, []byte) (string, string, error) {
	if s.err != nil {
		return "", "", s.err
	}
	return s.id, "thr", nil
}

func TestGmailSenderInterface(t *testing.T) {
	var s GmailSender = stubGmail{id: "gm1"}
	id, thr, err := s.Send(context.Background(), "acc", []byte("raw"))
	if err != nil || id != "gm1" || thr != "thr" {
		t.Fatalf("%v %s %s", err, id, thr)
	}
	if providerGmail != "gmail" {
		t.Fatal(providerGmail)
	}
}
