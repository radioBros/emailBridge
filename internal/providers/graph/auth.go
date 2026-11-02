package graph

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/radiobros/emailbridge/internal/errs"
)

// TokenURLForTenant builds the Microsoft identity platform token URL from an oauth app tenantId.
// override is used only in tests (simulator). Empty tenant defaults to "common".
func TokenURLForTenant(tenantID, override string) string {
	if override != "" {
		return override
	}
	if tenantID == "" {
		tenantID = "common"
	}
	return "https://login.microsoftonline.com/" + tenantID + "/oauth2/v2.0/token"
}

// ClientCredentials fetches an app-only token (P6.3 §6).
type ClientCredentials struct {
	HTTP     *http.Client
	TokenURL string // from TokenURLForTenant; override only for tests
	TenantID string // used when TokenURL is empty
	ClientID string
	Secret   string
	Scope    string // default https://graph.microsoft.com/.default
}

// Token returns access_token from client_credentials grant.
func (c *ClientCredentials) Token(ctx context.Context) (string, time.Time, error) {
	if c == nil || c.ClientID == "" || c.Secret == "" {
		return "", time.Time{}, errs.New(errs.CodeValidationFailed, "client_id and client_secret are required for client_credentials.")
	}
	scope := c.Scope
	if scope == "" {
		scope = "https://graph.microsoft.com/.default"
	}
	tokenURL := c.TokenURL
	if tokenURL == "" {
		tokenURL = TokenURLForTenant(c.TenantID, "")
	}
	form := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {c.ClientID},
		"client_secret": {c.Secret},
		"scope":         {scope},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", time.Time{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	cli := c.HTTP
	if cli == nil {
		cli = http.DefaultClient
	}
	res, err := cli.Do(req)
	if err != nil {
		return "", time.Time{}, errs.Wrap(err, errs.CodeServiceUnavailable, "graph.token")
	}
	defer func() { _ = res.Body.Close() }()
	b, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return "", time.Time{}, errs.Wrap(err, errs.CodeServiceUnavailable, "graph.token.read")
	}
	if res.StatusCode >= 400 {
		return "", time.Time{}, errs.New(errs.CodeOauthExchangeFailed, "client_credentials token request failed.")
	}
	var out struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(b, &out); err != nil || out.AccessToken == "" {
		return "", time.Time{}, errs.New(errs.CodeOauthExchangeFailed, "client_credentials response missing access_token.")
	}
	exp := time.Now().UTC().Add(time.Duration(out.ExpiresIn) * time.Second)
	if out.ExpiresIn <= 0 {
		exp = time.Now().UTC().Add(time.Hour)
	}
	return out.AccessToken, exp, nil
}

// AccessToken adapts ClientCredentials to TokenSource (username = upn).
type AppOnlySource struct {
	Creds *ClientCredentials
	UPN   string
}

// AccessToken implements TokenSource.
func (a AppOnlySource) AccessToken(ctx context.Context, _ string) (token, username string, err error) {
	tok, _, err := a.Creds.Token(ctx)
	if err != nil {
		return "", "", err
	}
	return tok, a.UPN, nil
}

// DelegatedSource authenticates with a parent account's token source while addressing a shared
// mailbox UPN (W54 G24). Callers must use Client.ForUser(UPN) — never mutate a shared client.
type DelegatedSource struct {
	Parent TokenSource
	UPN    string
}

// AccessToken returns the parent token with the shared mailbox as username.
func (d DelegatedSource) AccessToken(ctx context.Context, accountID string) (token, username string, err error) {
	if d.Parent == nil {
		return "", "", errs.New(errs.CodeOauthNotConnected, "Delegated parent token source is not configured.")
	}
	tok, _, err := d.Parent.AccessToken(ctx, accountID)
	if err != nil {
		return "", "", err
	}
	return tok, d.UPN, nil
}
