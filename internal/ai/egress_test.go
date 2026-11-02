package ai

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"strings"
	"testing"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/egress"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/features"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

func TestCheckBaseURLScheme(t *testing.T) {
	svc := &Service{}
	if err := svc.checkBaseURL("https://api.openai.com/v1"); err != nil {
		t.Fatal(err)
	}
	if err := svc.checkBaseURL("http://127.0.0.1:11434"); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("loopback http: %v", err)
	}
	if err := svc.checkBaseURL("http://169.254.169.254/"); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("metadata http: %v", err)
	}
	if err := svc.checkBaseURL("ftp://evil.test/"); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("ftp: %v", err)
	}
	d := egress.NewDialer(egress.Policy{
		BlockPrivate: true, BlockLinkLocal: true, AllowHosts: []string{"ollama.internal"},
	})
	svc.Egress = d
	if err := svc.checkBaseURL("http://ollama.internal:11434"); err != nil {
		t.Fatal("allow-listed http", err)
	}
	if err := svc.checkBaseURL("http://other.internal/"); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("non-allowlisted http: %v", err)
	}
}

func TestProviderRefusesPrivateBaseURL(t *testing.T) {
	p := &OpenAICompat{BaseURL: "http://127.0.0.1:9"}
	_, err := p.Complete(context.Background(), "k", "m", "s", "u")
	if err == nil {
		t.Fatal("want dial refuse")
	}
	if !errors.Is(err, egress.ErrBlocked) && errs.CodeOf(err) != errs.CodeEndpointURLBlocked {
		t.Fatalf("want blocked, got %v", err)
	}
	p2 := &OpenAICompat{BaseURL: "http://169.254.169.254/"}
	_, err = p2.Complete(context.Background(), "k", "m", "s", "u")
	if err == nil {
		t.Fatal("want metadata refuse")
	}
	if !errors.Is(err, egress.ErrBlocked) && errs.CodeOf(err) != errs.CodeEndpointURLBlocked {
		t.Fatalf("want blocked metadata, got %v", err)
	}
}

func TestMapProviderDialErr(t *testing.T) {
	if mapProviderDialErr(nil) != nil {
		t.Fatal("nil")
	}
	err := mapProviderDialErr(egress.ErrBlocked)
	if errs.CodeOf(err) != errs.CodeEndpointURLBlocked {
		t.Fatal(err)
	}
	other := errors.New("x")
	if got := mapProviderDialErr(other); !errors.Is(got, other) {
		t.Fatal("passthrough")
	}
}

func TestHTTPDoerUsesEgress(t *testing.T) {
	d := egress.NewDialer(egress.Policy{BlockPrivate: true, BlockLinkLocal: true})
	d.Resolver = mapResolver{"api.openai.com": {netip.MustParseAddr("93.184.216.34")}}
	svc := &Service{Egress: d}
	if svc.httpDoer() == nil {
		t.Fatal("nil doer")
	}
	svc.HTTP = doerFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"choices":[{"message":{"content":"x"}}]}`))}, nil
	})
	p, err := svc.providerOf(&Config{Provider: ProviderOpenAI, Model: "m", BaseURL: "https://api.openai.com/v1"})
	if err != nil {
		t.Fatal(err)
	}
	out, err := p.Complete(context.Background(), "k", "m", "s", "u")
	if err != nil || out != "x" {
		t.Fatalf("%q %v", out, err)
	}
}

type mapResolver map[string][]netip.Addr

func (m mapResolver) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	if a, ok := m[host]; ok {
		return a, nil
	}
	return nil, errors.New("nxdomain")
}

func TestPutSettingsRejectsHTTPLoopback(t *testing.T) {
	org := store.NewID(store.PrefixOrganization)
	svc := &Service{Repo: &memStore{}, Cipher: testCipher(t), Env: "test"}
	key := "sk"
	_, err := svc.PutSettings(orgCtx(org, tenancy.RoleOwner), PutInput{
		Provider: ProviderLocal, BaseURL: "http://127.0.0.1:11434", Model: "llama", APIKey: &key,
	})
	if errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("got %v", err)
	}
}

func TestHostAllowListedEdges(t *testing.T) {
	if hostAllowListed(nil, "x") || hostAllowListed(egress.NewDialer(egress.Policy{}), "") {
		t.Fatal("nil/empty")
	}
	d := egress.NewDialer(egress.Policy{AllowHosts: []string{"a.example"}})
	if !hostAllowListed(d, "A.Example.") || hostAllowListed(d, "b.example") {
		t.Fatal("hosts")
	}
}

func TestAnthropicDialBlocked(t *testing.T) {
	p := &Anthropic{BaseURL: "http://127.0.0.1:9"}
	_, err := p.Complete(context.Background(), "k", "m", "s", "u")
	if err == nil {
		t.Fatal("want refuse")
	}
	if !errors.Is(err, egress.ErrBlocked) && errs.CodeOf(err) != errs.CodeEndpointURLBlocked {
		t.Fatalf("got %v", err)
	}
	p2 := &Anthropic{BaseURL: "https://x.test", HTTP: doerFunc(func(*http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("wrap: %w", egress.ErrBlocked)
	})}
	_, err = p2.Complete(context.Background(), "k", "m", "s", "u")
	if errs.CodeOf(err) != errs.CodeEndpointURLBlocked {
		t.Fatalf("mapped: %v", err)
	}
	p3 := &OpenAICompat{BaseURL: "https://x.test", HTTP: doerFunc(func(*http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("wrap: %w", egress.ErrBlocked)
	})}
	_, err = p3.Complete(context.Background(), "k", "m", "s", "u")
	if errs.CodeOf(err) != errs.CodeEndpointURLBlocked {
		t.Fatalf("openai mapped: %v", err)
	}
}

func TestModuleStartWiresEgress(t *testing.T) {
	srv, err := httpapi.New(httpapi.Options{Addr: ":0", Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	a := app.New(app.Options{Roles: app.RoleSet{app.RoleAPI: true}})
	app.Provide(a, &store.DB{})
	app.Provide(a, testCipher(t))
	app.Provide(a, srv)
	d := egress.NewDialer(egress.Policy{BlockPrivate: true})
	app.Provide(a, d)
	m := &Module{Env: "test", Registry: features.DefaultRegistry()}
	if err := m.Start(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	if m.Service().Egress != d {
		t.Fatal("egress not wired")
	}
}
