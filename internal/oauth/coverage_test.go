package oauth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

func TestCreateRequiresAuth(t *testing.T) {
	s := &Service{Repo: &Repo{}}
	_, err := s.Create(context.Background(), CreateInput{})
	if errs.CodeOf(err) != errs.CodeUnauthenticated && errs.CodeOf(err) != errs.CodeForbidden {
		// Require returns unauthenticated when no caller
		if err == nil {
			t.Fatal("expected error")
		}
	}
}

func TestGetBadID(t *testing.T) {
	ctx := tenancy.WithCaller(store.WithOrganization(context.Background(), "org_1"), tenancy.Caller{
		Kind: tenancy.CallerUser, Role: tenancy.RoleOwner, OrganizationID: "org_1",
	})
	s := &Service{Repo: &Repo{}}
	_, err := s.Get(ctx, "bad")
	if errs.CodeOf(err) != errs.CodeNotFound {
		t.Fatal(err)
	}
}

func TestCallbackErrorRedirect(t *testing.T) {
	s := &Service{PublicURL: "https://eb.example", State: &memState{}}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/callbacks/gmail?error=access_denied", nil)
	s.HandleCallback(context.Background(), w, r, ProviderGmail)
	if w.Code != http.StatusFound {
		t.Fatal(w.Code)
	}
}

func TestCallbackBadState(t *testing.T) {
	s := &Service{PublicURL: "https://eb.example", State: &memState{}}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/callbacks/gmail?code=x&state=nope", nil)
	s.HandleCallback(context.Background(), w, r, ProviderGmail)
	if w.Code != http.StatusFound {
		t.Fatal(w.Code)
	}
}

func TestRedeemBadToken(t *testing.T) {
	s := &Service{PublicURL: "https://eb.example"}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/v1/connect-links/bad/redeem", nil)
	s.RedeemConnectLink(context.Background(), w, r, "bad")
	if w.Code == http.StatusFound {
		t.Fatal("expected problem")
	}
}

func TestHttpClientFallback(t *testing.T) {
	s := &Service{}
	if s.httpClient() == nil {
		t.Fatal()
	}
	s.now = func() time.Time { return time.Unix(0, 0).UTC() }
	if !s.clock().Equal(time.Unix(0, 0).UTC()) {
		t.Fatal()
	}
}

func TestModuleName(t *testing.T) {
	m := &Module{}
	if m.Name() != "oauth" {
		t.Fatal(m.Name())
	}
	if len(m.Roles()) == 0 {
		t.Fatal()
	}
	_ = m.Stop(context.Background())
}

func TestPresetsMailru(t *testing.T) {
	s := &Service{}
	a, tok, _ := s.endpoints(&App{Provider: ProviderMailru})
	if a == "" || tok == "" {
		t.Fatal()
	}
}

func TestValidateEmptyName(t *testing.T) {
	err := validateApp(&App{Name: "", Provider: ProviderGmail, ClientID: "c"})
	if errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatal(err)
	}
}

func TestConnectLinkTTLClamp(t *testing.T) {
	if minLinkTTL > defaultLinkTTL || maxLinkTTL < defaultLinkTTL {
		t.Fatal()
	}
}
