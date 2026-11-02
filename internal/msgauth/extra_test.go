package msgauth

import (
	"context"
	"errors"
	"net/textproto"
	"strings"
	"testing"

	"github.com/emersion/go-msgauth/dmarc"
)

func TestFromHeadersMalformedSkipped(t *testing.T) {
	h := textproto.MIMEHeader{}
	h.Add("Authentication-Results", `not-a-valid-header-at-all;;;`)
	h.Add("Authentication-Results", `good.example; spf=pass smtp.mailfrom=a@b.com`)
	got, err := FromHeaders(h, []string{"good.example"})
	if err != nil {
		t.Fatal(err)
	}
	if got.SPF.Result != "pass" {
		t.Fatalf("%+v", got)
	}
}

func TestMapHeadersNilAndCanonical(t *testing.T) {
	var m MapHeaders
	if m.Values("X") != nil {
		t.Fatal("nil map")
	}
	m = MapHeaders{"X-Test": {"a"}}
	if got := m.Values("x-test"); len(got) != 1 || got[0] != "a" {
		t.Fatalf("%v", got)
	}
}

func TestEmptyResultsJSONShape(t *testing.T) {
	r := emptyResults(SourceHeader)
	if r.DKIM == nil || len(r.DKIM) != 0 || r.SPF.Result != "none" || r.Source != SourceHeader {
		t.Fatalf("%+v", r)
	}
}

func TestEvaluateDMARCTempAndPerm(t *testing.T) {
	item := evaluateDMARC("example.com", Item{Result: "pass", Domain: "example.com"}, nil, func(string) (*dmarc.Record, error) {
		return nil, errors.New("dns boom")
	})
	if item.Result != "permerror" {
		t.Fatal(item)
	}
	item = evaluateDMARC("example.com", Item{}, nil, func(string) (*dmarc.Record, error) {
		return nil, dmarc.ErrNoPolicy
	})
	if item.Result != "none" {
		t.Fatal(item)
	}
}

func TestFromDomainOfErrors(t *testing.T) {
	if _, err := fromDomainOf([]byte("not email")); err == nil {
		t.Fatal("expected error")
	}
	raw := []byte("From: not-an-address\r\n\r\nbody")
	if _, err := fromDomainOf(raw); err == nil {
		t.Fatal("expected parse address error")
	}
}

func TestVerifyBadFrom(t *testing.T) {
	raw := []byte("Subject: hi\r\n\r\nnobody")
	got, err := Verify(context.Background(), raw, Item{Result: "none"}, Resolver{
		LookupTXT: func(string) ([]string, error) { return nil, nil },
		DMARC:     func(string) (*dmarc.Record, error) { return nil, dmarc.ErrNoPolicy },
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.DMARC.Result != "permerror" {
		t.Fatalf("%+v", got.DMARC)
	}
}

func TestSelectorFromDKIMSig(t *testing.T) {
	if got := selectorFromDKIMSig("v=1; a=rsa-sha256; s=sel1; d=ex.com"); got != "sel1" {
		t.Fatal(got)
	}
	if got := selectorFromDKIMSig("nope"); got != "" {
		t.Fatal(got)
	}
}

func TestDKIMSelectorsFromMessageEmpty(t *testing.T) {
	if got := dkimSelectorsFromMessage([]byte("bad")); len(got) != 0 {
		t.Fatal(got)
	}
}

func TestSPFDomainHelo(t *testing.T) {
	// via FromHeaders with helo only
	h := textproto.MIMEHeader{}
	h.Add("Authentication-Results", `a.example; spf=neutral smtp.helo=mail.example.com`)
	got, _ := FromHeaders(h, []string{"a.example"})
	if got.SPF.Domain != "mail.example.com" {
		t.Fatalf("%+v", got.SPF)
	}
}

func TestAlignedEdge(t *testing.T) {
	if aligned("", "a.com", dmarc.AlignmentRelaxed) {
		t.Fatal("empty")
	}
	if !aligned("sub.example.com", "example.com", dmarc.AlignmentRelaxed) {
		t.Fatal("sub")
	}
}

func TestNormalizeMore(t *testing.T) {
	if normalizeResult("weird") != "weird" {
		t.Fatal()
	}
}

func TestVerifyNilLookupUsesDefaultDMARC(t *testing.T) {
	// Ensure Verify with nil DMARC lookup doesn't panic on tiny message without From domain handling.
	raw := []byte("From: joe@football.example.com\r\n\r\nHi")
	got, err := Verify(context.Background(), raw, Item{Result: "none"}, Resolver{
		LookupTXT: func(string) ([]string, error) { return nil, &netNotFound{name: "x"} },
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Source != SourceVerify {
		t.Fatal(got.Source)
	}
	_ = strings.Contains
}
