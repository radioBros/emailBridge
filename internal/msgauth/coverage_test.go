package msgauth

import (
	"context"
	"testing"

	"github.com/emersion/go-msgauth/dmarc"
)

func TestEvaluateDMARCNilLookupDefault(t *testing.T) {
	// nil lookup uses dmarc.Lookup — will fail DNS in tests → permerror or none
	item := evaluateDMARC("this-domain-definitely-does-not-exist-eb-test.invalid", Item{Result: "pass", Domain: "x.com"}, nil, nil)
	if item.Result != "none" && item.Result != "temperror" && item.Result != "permerror" {
		t.Fatalf("%+v", item)
	}
}

func TestEvaluateDMARCNilRecord(t *testing.T) {
	item := evaluateDMARC("example.com", Item{}, nil, func(string) (*dmarc.Record, error) {
		return nil, nil
	})
	if item.Result != "none" {
		t.Fatal(item)
	}
}

func TestDKIMResultMapping(t *testing.T) {
	if dkimResultOf(nil) != "none" {
		t.Fatal()
	}
}

func TestFromHeadersUntrustedOnly(t *testing.T) {
	h := MapHeaders{"Authentication-Results": {`evil; spf=pass smtp.mailfrom=x.com`}}
	got, _ := FromHeaders(h, []string{"good"})
	if got.SPF.Result != "none" {
		t.Fatal(got)
	}
}

func TestVerifyEmptyRaw(t *testing.T) {
	got, err := Verify(context.Background(), []byte("From: a@b.com\r\n\r\n"), Item{Result: "fail", Domain: "b.com"}, Resolver{
		LookupTXT: func(string) ([]string, error) { return nil, nil },
		DMARC: func(string) (*dmarc.Record, error) {
			return &dmarc.Record{Policy: dmarc.PolicyNone, DKIMAlignment: dmarc.AlignmentRelaxed, SPFAlignment: dmarc.AlignmentRelaxed}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.DMARC.Result != "fail" {
		t.Fatalf("%+v", got.DMARC)
	}
}
