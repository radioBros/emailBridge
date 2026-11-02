package sync

import (
	"context"
	"net/textproto"
	"testing"

	"github.com/radiobros/emailbridge/internal/events/types"
	"github.com/radiobros/emailbridge/internal/mailproto/mime"
	"github.com/radiobros/emailbridge/internal/msgauth"
)

func TestFillAuthResults_headerMode(t *testing.T) {
	r := &runner{acc: &Account{ID: "acc"}, log: discardLog()}
	hdr := mime.Header{
		"authentication-results": []string{`mx.trusted.test; spf=pass smtp.mailfrom=example.com; dkim=pass header.d=example.com`},
	}
	d := &types.MessageReceived{}
	r.fillAuthResults(context.Background(), hdr, Needs{AuthResults: "header", TrustedAuth: []string{"mx.trusted.test"}}, nil, d)
	if d.AuthResults == nil || d.AuthResults.Source != "header" {
		t.Fatalf("%+v", d.AuthResults)
	}
	if d.AuthResults.SPF == nil || d.AuthResults.SPF.Result != "pass" {
		t.Fatalf("spf %+v", d.AuthResults.SPF)
	}
}

func TestFillAuthResults_headerNoTrusted(t *testing.T) {
	r := &runner{acc: &Account{ID: "acc"}, log: discardLog()}
	hdr := mime.Header{"authentication-results": []string{`mx.x; spf=pass smtp.mailfrom=example.com`}}
	d := &types.MessageReceived{}
	r.fillAuthResults(context.Background(), hdr, Needs{AuthResults: "header"}, nil, d)
	if d.AuthResults != nil {
		t.Fatalf("%+v", d.AuthResults)
	}
}

func TestFillAuthResults_verifyEmptyRaw(t *testing.T) {
	r := &runner{acc: &Account{ID: "acc"}, log: discardLog()}
	d := &types.MessageReceived{}
	r.fillAuthResults(context.Background(), mime.Header{}, Needs{AuthResults: "verify"}, nil, d)
	if d.AuthResults != nil {
		t.Fatal("expected nil")
	}
}

func TestConvertAuth_mapsItems(t *testing.T) {
	res := &msgauth.Results{
		Source: msgauth.SourceVerify,
		SPF:    msgauth.Item{Result: "pass", Domain: "ex.com"},
		DKIM:   []msgauth.Item{{Result: "pass", Domain: "ex.com", Selector: "s"}},
		DMARC:  msgauth.Item{Result: "pass", Domain: "ex.com"},
	}
	out := convertAuth(res)
	if out.Source != "verify" || out.SPF.Result != "pass" || len(out.DKIM) != 1 || out.DKIM[0].Selector != "s" {
		t.Fatalf("%+v", out)
	}
	_ = textproto.CanonicalMIMEHeaderKey("Authentication-Results")
}

func TestEraseAttachmentsBestEffort(t *testing.T) {
	mem := &memAtt{}
	r := &runner{acc: &Account{ID: "acc"}, attachments: mem, log: discardLog()}
	r.eraseAttachmentsBestEffort(context.Background(), []string{"m1", "m2"})
	if len(mem.erased) != 2 {
		t.Fatalf("%+v", mem.erased)
	}
}

func TestHeaderList_authModes(t *testing.T) {
	f, full := headerList(Needs{AuthResults: "verify"})
	if full || !containsFold(f, "Authentication-Results") {
		t.Fatalf("%v %v", f, full)
	}
	_, full = headerList(Needs{Headers: []string{"*"}})
	if !full {
		t.Fatal("full")
	}
}

func containsFold(in []string, want string) bool {
	for _, s := range in {
		if s == want {
			return true
		}
	}
	return false
}

func TestFetchRawShared_skipsWhenUnneeded(t *testing.T) {
	r := &runner{acc: &Account{}}
	raw, err := r.fetchRawShared(context.Background(), nil, nil, Needs{})
	if err != nil || raw != nil {
		t.Fatalf("%v %v", raw, err)
	}
}
