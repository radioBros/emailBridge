package autoconfig

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func clientWith(fn roundTripFunc) *http.Client {
	return &http.Client{Transport: fn, Timeout: time.Second}
}

type fakeDNS struct {
	srv map[string][]*net.SRV
	mx  []*net.MX
	err error
}

func (f fakeDNS) LookupSRV(_ context.Context, service, proto, name string) (string, []*net.SRV, error) {
	if f.err != nil {
		return "", nil, f.err
	}
	key := service + "." + proto + "." + name
	return "", f.srv[key], nil
}

func (f fakeDNS) LookupMX(context.Context, string) ([]*net.MX, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.mx, nil
}

func TestBuiltinGmail(t *testing.T) {
	s := NewService(nil)
	s.HTTP = clientWith(func(*http.Request) (*http.Response, error) {
		t.Fatal("no http for builtin")
		return nil, nil
	})
	r, err := s.Discover(context.Background(), "Ada <User@Gmail.com>")
	if err != nil {
		t.Fatal(err)
	}
	if r.Source != SourceBuiltin || r.OAuthHint != HintGmail || r.IMAP.Host != "imap.gmail.com" {
		t.Fatalf("%+v", r)
	}
	if r.Email != "user@gmail.com" {
		t.Fatalf("email %s", r.Email)
	}
}

func TestBuiltinPECAndOnMicrosoft(t *testing.T) {
	s := NewService(nil)
	for _, email := range []string{"a@arubapec.it", "b@legalmail.it", "c@postecert.it", "d@contoso.onmicrosoft.com"} {
		r, err := s.Discover(context.Background(), email)
		if err != nil || !r.complete() {
			t.Fatalf("%s: %+v %v", email, r, err)
		}
	}
}

func TestCacheHit(t *testing.T) {
	s := NewService(nil)
	r1, _ := s.Discover(context.Background(), "a@yahoo.com")
	s.HTTP = clientWith(func(*http.Request) (*http.Response, error) {
		t.Fatal("cache should skip network")
		return nil, nil
	})
	r2, err := s.Discover(context.Background(), "b@yahoo.com")
	if err != nil || r2.Source != r1.Source || r2.IMAP.Host != r1.IMAP.Host {
		t.Fatalf("%+v %+v", r1, r2)
	}
	if r2.Email != "b@yahoo.com" {
		t.Fatal(r2.Email)
	}
}

func TestISPDB(t *testing.T) {
	xmlBody := `<?xml version="1.0"?>
<clientConfig version="1.1">
  <emailProvider id="ex">
    <incomingServer type="imap">
      <hostname>imap.example.test</hostname><port>993</port><socketType>SSL</socketType>
      <username>%EMAILADDRESS%</username>
    </incomingServer>
    <outgoingServer type="smtp">
      <hostname>smtp.example.test</hostname><port>587</port><socketType>STARTTLS</socketType>
      <username>%EMAILLOCALPART%</username>
    </outgoingServer>
  </emailProvider>
</clientConfig>`
	s := NewService(nil)
	s.DNS = fakeDNS{}
	s.HTTP = clientWith(func(req *http.Request) (*http.Response, error) {
		if strings.Contains(req.URL.Host, "thunderbird") || strings.Contains(req.URL.Path, "autoconfig") {
			return &http.Response{
				StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(xmlBody)),
				Header: http.Header{"Content-Type": []string{"application/xml"}},
			}, nil
		}
		return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(bytes.NewReader(nil))}, nil
	})
	r, err := s.Discover(context.Background(), "u@example.test")
	if err != nil || r.Source != SourceISPDB || r.IMAP.Host != "imap.example.test" || r.SMTP.User != userLocal {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestAutodiscoverPOX(t *testing.T) {
	pox := `<?xml version="1.0"?>
<Autodiscover xmlns="http://schemas.microsoft.com/exchange/autodiscover/outlook/responseschema/2006a">
  <Response><Account>
    <Protocol><Type>IMAP</Type><Server>imap.ad.test</Server><Port>993</Port><SSL>on</SSL></Protocol>
    <Protocol><Type>SMTP</Type><Server>smtp.ad.test</Server><Port>587</Port><SSL>off</SSL></Protocol>
  </Account></Response>
</Autodiscover>`
	s := NewService(nil)
	s.DNS = fakeDNS{}
	s.HTTP = clientWith(func(req *http.Request) (*http.Response, error) {
		if strings.Contains(req.URL.Path, "autodiscover.json") {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"Protocol":"Autodiscoverv1","Url":"https://ad.example/autodiscover/autodiscover.xml"}`)),
			}, nil
		}
		if req.Method == http.MethodPost {
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(pox))}, nil
		}
		return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(bytes.NewReader(nil))}, nil
	})
	r, err := s.Discover(context.Background(), "u@adcorp.test")
	if err != nil || r.Source != SourceAutodiscover || r.IMAP.Host != "imap.ad.test" {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestSRVAndMX(t *testing.T) {
	s := NewService(nil)
	s.HTTP = clientWith(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(bytes.NewReader(nil))}, nil
	})
	s.DNS = fakeDNS{
		srv: map[string][]*net.SRV{
			"imaps.tcp.srvonly.test":       {{Target: "imap.srvonly.test.", Port: 993}},
			"submissions.tcp.srvonly.test": {{Target: "smtp.srvonly.test.", Port: 465}},
		},
	}
	r, err := s.Discover(context.Background(), "a@srvonly.test")
	if err != nil || r.Source != SourceSRV || r.IMAP.Host != "imap.srvonly.test" {
		t.Fatalf("%+v %v", r, err)
	}

	s2 := NewService(nil)
	s2.Cache = newCache(cacheTTL)
	s2.HTTP = clientWith(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(bytes.NewReader(nil))}, nil
	})
	s2.DNS = fakeDNS{mx: []*net.MX{{Host: "aspmx.l.google.com.", Pref: 1}}}
	r2, err := s2.Discover(context.Background(), "x@mxguess.test")
	if err != nil || r2.Source != SourceMX || r2.OAuthHint != HintGmail {
		t.Fatalf("%+v %v", r2, err)
	}
}

func TestSoftMissAndValidation(t *testing.T) {
	s := NewService(nil)
	s.HTTP = clientWith(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(bytes.NewReader(nil))}, nil
	})
	s.DNS = fakeDNS{}
	r, err := s.Discover(context.Background(), "nobody@unknown-zz.test")
	if err != nil || r.Source != SourceNone || r.IMAP != nil {
		t.Fatalf("%+v %v", r, err)
	}
	if _, err := s.Discover(context.Background(), "not-an-email"); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("%v", err)
	}
}

func TestDiscoverForOrgRateAndPerm(t *testing.T) {
	s := NewService(nil)
	ctx := context.Background()
	if _, err := s.DiscoverForOrg(ctx, "a@gmail.com"); errs.CodeOf(err) != errs.CodeUnauthenticated {
		t.Fatalf("expected unauthenticated, got %v", err)
	}
	ctx = tenancy.WithCaller(ctx, tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: "u", OrganizationID: "org_1", Role: tenancy.RoleViewer,
	})
	s.clock = func() time.Time { return time.Unix(0, 0) }
	for i := 0; i < 30; i++ {
		if _, err := s.DiscoverForOrg(ctx, "a@gmail.com"); err != nil {
			t.Fatalf("i=%d %v", i, err)
		}
	}
	if _, err := s.DiscoverForOrg(ctx, "a@gmail.com"); errs.CodeOf(err) != errs.CodeRateLimited {
		t.Fatalf("%v", err)
	}
}

func TestParseMozillaAndPOXHelpers(t *testing.T) {
	if socketTLS("SSL") != tlsImplicit || socketTLS("STARTTLS") != tlsStartTLS || socketTLS("x") != tlsNone {
		t.Fatal("socketTLS")
	}
	if mozillaUser("%EMAILADDRESS%") != userEmail || mozillaUser("%EMAILLOCALPART%") != userLocal {
		t.Fatal("user")
	}
	r, err := parseMozillaXML([]byte(`<clientConfig><emailProvider/></clientConfig>`))
	if err == nil || r != nil {
		t.Fatal("empty")
	}
}

func TestMergeAndComplete(t *testing.T) {
	a := &Result{IMAP: &Server{Host: "i"}}
	b := &Result{SMTP: &Server{Host: "s"}, OAuthHint: HintGmail}
	m := merge(a, b)
	if !m.complete() || m.OAuthHint != HintGmail {
		t.Fatalf("%+v", m)
	}
	if merge(nil, b) != b || merge(a, nil) != a {
		t.Fatal("nil merge")
	}
}

func TestModuleGet(t *testing.T) {
	m := &Module{svc: NewService(nil)}
	ctx := tenancy.WithCaller(context.Background(), tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: "u", OrganizationID: "org_1", Role: tenancy.RoleViewer,
	})
	out, err := m.get(ctx, &getIn{Email: "a@icloud.com"})
	if err != nil || out.Body.Source != SourceBuiltin {
		t.Fatalf("%+v %v", out, err)
	}
}
