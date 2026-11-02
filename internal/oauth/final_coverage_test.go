package oauth

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/crypto"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

func TestCheckIfMatchVariants(t *testing.T) {
	if err := checkIfMatch("v1", 1); err != nil {
		t.Fatal(err)
	}
	if err := checkIfMatch(`"v1"`, 1); err != nil {
		t.Fatal(err)
	}
}

func TestViewOfOutlookAndGeneric(t *testing.T) {
	v := ViewOf(&App{Provider: ProviderOutlook, ClientSecret: crypto.EncryptedString{DEK: "d", CT: []byte{1}}})
	if v.AuthorizeURL == "" || !v.HasClientSecret {
		t.Fatal(v)
	}
	v2 := ViewOf(&App{Provider: ProviderGeneric, AuthorizeURL: "https://a", TokenURL: "https://t"})
	if v2.AuthorizeURL != "https://a" {
		t.Fatal(v2)
	}
}

func TestCallbackBaseTrim(t *testing.T) {
	if callbackBase("https://x/") != "https://x" {
		t.Fatal()
	}
}

func TestStartNoOrg(t *testing.T) {
	ctx := tenancy.WithCaller(context.Background(), tenancy.Caller{
		Kind: tenancy.CallerUser, Role: tenancy.RoleOwner,
	})
	s := &Service{Repo: &Repo{}}
	_, err := s.Start(ctx, store.NewID(store.PrefixAccount), StartInput{AppID: store.NewID(store.PrefixOAuthApp)})
	if err == nil {
		t.Fatal()
	}
}

func TestCreateNoOrg(t *testing.T) {
	ctx := tenancy.WithCaller(context.Background(), tenancy.Caller{
		Kind: tenancy.CallerUser, Role: tenancy.RoleOwner,
	})
	s := &Service{Repo: &Repo{}}
	_, err := s.Create(ctx, CreateInput{Name: "x", Provider: ProviderGmail, ClientID: "c", ClientSecret: "s"})
	if err == nil {
		t.Fatal()
	}
}

func TestListNoPerm(t *testing.T) {
	if _, err := (&Service{}).List(context.Background(), ListFilter{}); err == nil {
		t.Fatal()
	}
}

func TestDeleteNoPerm(t *testing.T) {
	if err := (&Service{}).Delete(context.Background(), "x", ""); err == nil {
		t.Fatal()
	}
}

func TestDisconnectNoPerm(t *testing.T) {
	if err := (&Service{}).Disconnect(context.Background(), store.NewID(store.PrefixAccount)); err == nil {
		t.Fatal()
	}
}

func TestVerifyNoPerm(t *testing.T) {
	if _, err := (&Service{}).Verify(context.Background(), store.NewID(store.PrefixOAuthApp)); err == nil {
		t.Fatal()
	}
}

func TestCreateLinkNoPerm(t *testing.T) {
	if _, err := (&Service{}).CreateConnectLink(context.Background(), ConnectLinkCreate{
		AccountID: store.NewID(store.PrefixAccount), AppID: store.NewID(store.PrefixOAuthApp),
	}); err == nil {
		t.Fatal()
	}
}

func TestPatchNoPerm(t *testing.T) {
	if _, err := (&Service{}).Patch(context.Background(), "x", "", PatchInput{}); err == nil {
		t.Fatal()
	}
}

func TestGetNoPerm(t *testing.T) {
	if _, err := (&Service{}).Get(context.Background(), store.NewID(store.PrefixOAuthApp)); err == nil {
		t.Fatal()
	}
}

func TestMigrationShape(t *testing.T) {
	m := Migration(30)
	if m.Number != 30 || m.Name == "" || m.Up == nil {
		t.Fatal(m)
	}
}

func TestOauthErrorCodesExist(t *testing.T) {
	codes := []errs.Code{
		errs.CodeOauthAppNameTaken, errs.CodeOauthAppDisabled, errs.CodeOauthStateInvalid,
		errs.CodeOauthExchangeFailed, errs.CodeOauthRevoked, errs.CodeOauthNotConnected,
		errs.CodeConnectLinkInvalid,
	}
	for _, c := range codes {
		if c == "" {
			t.Fatal()
		}
	}
}

func TestValidateCreateNoSecret(t *testing.T) {
	if err := validateCreate(CreateInput{Name: "x", Provider: ProviderGmail, ClientID: "c"}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatal(err)
	}
}

func TestTakeStateNilStore(t *testing.T) {
	s := &Service{}
	if _, err := s.takeState(context.Background(), "x"); errs.CodeOf(err) != errs.CodeOauthStateInvalid {
		t.Fatal(err)
	}
}

func TestAuditNils(t *testing.T) {
	s := &Service{}
	s.audit(context.Background(), "x", "id", nil)
	s.auditAccount(context.Background(), "x", "id", nil)
}

func TestHttpClientDefaultTimeout(t *testing.T) {
	s := &Service{HTTP: &http.Client{Timeout: time.Second}}
	if s.httpClient().Timeout != time.Second {
		t.Fatal()
	}
}
