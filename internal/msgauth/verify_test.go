package msgauth

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/emersion/go-msgauth/dkim"
	"github.com/emersion/go-msgauth/dmarc"
)

const (
	testDomain   = "football.example.com"
	testSelector = "brisbane"
)

func testKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func pubTXT(pub *rsa.PublicKey) string {
	return "v=DKIM1; k=rsa; p=" + base64.StdEncoding.EncodeToString(x509.MarshalPKCS1PublicKey(pub))
}

func signMessage(t *testing.T, body string, key *rsa.PrivateKey) []byte {
	t.Helper()
	var b bytes.Buffer
	err := dkim.Sign(&b, strings.NewReader(body), &dkim.SignOptions{
		Domain:   testDomain,
		Selector: testSelector,
		Signer:   key,
		HeaderKeys: []string{
			"From", "To", "Subject", "Date", "Message-ID",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

const sampleMail = "From: Joe SixPack <joe@football.example.com>\r\n" +
	"To: Suzie Q <suzie@shopping.example.net>\r\n" +
	"Subject: Is dinner ready?\r\n" +
	"Date: Fri, 11 Jul 2003 21:00:37 -0700 (PDT)\r\n" +
	"Message-ID: <20030712040037.46341.5F8J@football.example.com>\r\n" +
	"\r\n" +
	"Hi.\r\n\r\nWe lost the game. Are you hungry yet?\r\n\r\nJoe."

func TestVerifyPassAndDMARC(t *testing.T) {
	key := testKey(t)
	raw := signMessage(t, sampleMail, key)
	lookup := func(name string) ([]string, error) {
		want := testSelector + "._domainkey." + testDomain
		if name == want {
			return []string{pubTXT(&key.PublicKey)}, nil
		}
		return nil, &netNotFound{name}
	}
	policy := func(domain string) (*dmarc.Record, error) {
		if domain != testDomain {
			return nil, dmarc.ErrNoPolicy
		}
		return &dmarc.Record{
			Policy:        dmarc.PolicyReject,
			DKIMAlignment: dmarc.AlignmentRelaxed,
			SPFAlignment:  dmarc.AlignmentRelaxed,
		}, nil
	}
	spf := Item{Result: "pass", Domain: testDomain}
	got, err := Verify(context.Background(), raw, spf, Resolver{LookupTXT: lookup, DMARC: policy})
	if err != nil {
		t.Fatal(err)
	}
	if got.Source != SourceVerify {
		t.Fatalf("source: %s", got.Source)
	}
	if len(got.DKIM) != 1 || got.DKIM[0].Result != "pass" || got.DKIM[0].Domain != testDomain || got.DKIM[0].Selector != testSelector {
		t.Fatalf("dkim: %+v", got.DKIM)
	}
	if got.DMARC.Result != "pass" || got.DMARC.Domain != testDomain {
		t.Fatalf("dmarc: %+v", got.DMARC)
	}
	if got.ARC.Result != "none" {
		t.Fatalf("arc: %+v", got.ARC)
	}
}

func TestVerifyTamperedBodyFailsDKIM(t *testing.T) {
	key := testKey(t)
	raw := signMessage(t, sampleMail, key)
	// Tamper with the body after the blank line.
	s := string(raw)
	s = strings.Replace(s, "We lost the game", "We WON the game", 1)
	raw = []byte(s)
	lookup := func(name string) ([]string, error) {
		return []string{pubTXT(&key.PublicKey)}, nil
	}
	got, err := Verify(context.Background(), raw, Item{Result: "pass", Domain: testDomain}, Resolver{
		LookupTXT: lookup,
		DMARC: func(string) (*dmarc.Record, error) {
			return &dmarc.Record{Policy: dmarc.PolicyNone, DKIMAlignment: dmarc.AlignmentRelaxed, SPFAlignment: dmarc.AlignmentRelaxed}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.DKIM) != 1 || got.DKIM[0].Result != "fail" {
		t.Fatalf("expected dkim fail, got %+v", got.DKIM)
	}
	// SPF still pass + aligned → DMARC pass
	if got.DMARC.Result != "pass" {
		t.Fatalf("dmarc with aligned spf: %+v", got.DMARC)
	}
}

func TestVerifyTooLarge(t *testing.T) {
	raw := make([]byte, MaxRawBytes+1)
	_, err := Verify(context.Background(), raw, Item{}, Resolver{})
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("got %v", err)
	}
}

func TestVerifyNoDMARCPolicy(t *testing.T) {
	key := testKey(t)
	raw := signMessage(t, sampleMail, key)
	lookup := func(string) ([]string, error) { return []string{pubTXT(&key.PublicKey)}, nil }
	got, err := Verify(context.Background(), raw, Item{Result: "none"}, Resolver{
		LookupTXT: lookup,
		DMARC:     func(string) (*dmarc.Record, error) { return nil, dmarc.ErrNoPolicy },
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.DMARC.Result != "none" {
		t.Fatalf("%+v", got.DMARC)
	}
}

func TestVerifyStrictAlignmentFail(t *testing.T) {
	key := testKey(t)
	// Sign as subdomain of From's organisational domain but strict alignment requires exact match.
	mailBody := strings.Replace(sampleMail, "joe@football.example.com", "joe@mail.football.example.com", 1)
	var b bytes.Buffer
	if err := dkim.Sign(&b, strings.NewReader(mailBody), &dkim.SignOptions{
		Domain:   "football.example.com",
		Selector: testSelector,
		Signer:   key,
		HeaderKeys: []string{
			"From", "To", "Subject", "Date", "Message-ID",
		},
	}); err != nil {
		t.Fatal(err)
	}
	lookup := func(string) ([]string, error) { return []string{pubTXT(&key.PublicKey)}, nil }
	got, err := Verify(context.Background(), b.Bytes(), Item{Result: "fail", Domain: "other.example"}, Resolver{
		LookupTXT: lookup,
		DMARC: func(string) (*dmarc.Record, error) {
			return &dmarc.Record{
				Policy:        dmarc.PolicyReject,
				DKIMAlignment: dmarc.AlignmentStrict,
				SPFAlignment:  dmarc.AlignmentStrict,
			}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	// From domain is mail.football.example.com; DKIM domain is football.example.com — strict fails.
	if got.DKIM[0].Result != "pass" {
		t.Fatalf("dkim should pass crypto: %+v", got.DKIM)
	}
	if got.DMARC.Result != "fail" {
		t.Fatalf("strict alignment should fail dmarc: %+v", got.DMARC)
	}
}

func TestAligned(t *testing.T) {
	if !aligned("a.example.com", "a.example.com", dmarc.AlignmentStrict) {
		t.Fatal("exact")
	}
	if aligned("a.example.com", "b.example.com", dmarc.AlignmentStrict) {
		t.Fatal("strict mismatch")
	}
	if !aligned("mail.example.com", "example.com", dmarc.AlignmentRelaxed) {
		t.Fatal("relaxed org")
	}
}

func TestVerifyCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Verify(ctx, []byte("From: a@b.com\r\n\r\n"), Item{}, Resolver{})
	if err == nil {
		t.Fatal("expected cancel")
	}
}

type netNotFound struct{ name string }

func (e *netNotFound) Error() string   { return "not found: " + e.name }
func (e *netNotFound) Timeout() bool   { return false }
func (e *netNotFound) Temporary() bool { return false }
