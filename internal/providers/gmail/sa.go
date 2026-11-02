package gmail

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/radiobros/emailbridge/internal/errs"
)

// ServiceAccountKey is the Google JSON key fields we need for domain-wide delegation.
type ServiceAccountKey struct {
	Type        string `json:"type"`
	ClientEmail string `json:"client_email"`
	PrivateKey  string `json:"private_key"`
	TokenURI    string `json:"token_uri"`
}

// ParseServiceAccountKey parses a Google service-account JSON key.
func ParseServiceAccountKey(raw []byte) (*ServiceAccountKey, error) {
	var k ServiceAccountKey
	if err := json.Unmarshal(raw, &k); err != nil {
		return nil, errs.Wrap(err, errs.CodeValidationFailed, "gmail.sa.json")
	}
	if k.Type != "" && k.Type != "service_account" {
		return nil, errs.New(errs.CodeValidationFailed, "Service account key type must be service_account.")
	}
	if k.ClientEmail == "" || k.PrivateKey == "" {
		return nil, errs.New(errs.CodeValidationFailed, "Service account key is missing client_email or private_key.")
	}
	if k.Type == "" {
		k.Type = "service_account"
	}
	if k.TokenURI == "" {
		k.TokenURI = "https://oauth2.googleapis.com/token"
	}
	return &k, nil
}

// ServiceAccountSource mints access tokens via JWT bearer grant (domain-wide delegation).
type ServiceAccountSource struct {
	Key     *ServiceAccountKey
	Subject string // impersonated user email
	Scopes  []string
	Client  *Client // optional; uses TokenURL / HTTP
	HTTP    *http.Client
	now     func() time.Time
}

// NewServiceAccountSourceFromJSON builds a TokenSource from oauthApps.serviceKey JSON + subject email.
func NewServiceAccountSourceFromJSON(raw []byte, subject string, client *Client) (*ServiceAccountSource, error) {
	k, err := ParseServiceAccountKey(raw)
	if err != nil {
		return nil, err
	}
	return &ServiceAccountSource{Key: k, Subject: subject, Client: client}, nil
}

// AccessToken implements TokenSource (accountID ignored; subject is fixed).
func (s *ServiceAccountSource) AccessToken(ctx context.Context, _ string) (token, username string, err error) {
	if s.Key == nil {
		return "", "", errs.New(errs.CodeOauthNotConnected, "Service account key is not configured.")
	}
	if s.Subject == "" {
		return "", "", errs.New(errs.CodeValidationFailed, "Service account subject (impersonated user) is required.")
	}
	scopes := s.Scopes
	if len(scopes) == 0 {
		scopes = []string{"https://www.googleapis.com/auth/gmail.modify"}
	}
	assertion, err := s.signedJWT(scopes)
	if err != nil {
		return "", "", err
	}
	tokenURL := s.Key.TokenURI
	if s.Client != nil && s.Client.TokenURL != "" {
		tokenURL = s.Client.TokenURL
	}
	form := url.Values{
		"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"},
		"assertion":  {assertion},
		"subject":    {s.Subject}, // simulator convenience; Google embeds subject in JWT
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	hc := s.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	res, err := hc.Do(req)
	if err != nil {
		return "", "", errs.Wrap(err, errs.CodeServiceUnavailable, "gmail.sa.token")
	}
	defer func() { _ = res.Body.Close() }()
	b, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode >= 400 {
		return "", "", errs.New(errs.CodeOauthExchangeFailed, fmt.Sprintf("Service account token HTTP %d", res.StatusCode))
	}
	var out struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(b, &out); err != nil || out.AccessToken == "" {
		return "", "", errs.New(errs.CodeOauthExchangeFailed, "Service account token response invalid.")
	}
	return out.AccessToken, s.Subject, nil
}

func (s *ServiceAccountSource) signedJWT(scopes []string) (string, error) {
	now := time.Now().UTC()
	if s.now != nil {
		now = s.now()
	}
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	claims, _ := json.Marshal(map[string]any{
		"iss":   s.Key.ClientEmail,
		"sub":   s.Subject,
		"scope": strings.Join(scopes, " "),
		"aud":   s.Key.TokenURI,
		"iat":   now.Unix(),
		"exp":   now.Add(time.Hour).Unix(),
	})
	payload := base64.RawURLEncoding.EncodeToString(claims)
	signingInput := header + "." + payload
	key, err := parseRSAPrivateKey(s.Key.PrivateKey)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		return "", errs.Wrap(err, errs.CodeInternalError, "gmail.sa.sign")
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

func parseRSAPrivateKey(pemStr string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return nil, errs.New(errs.CodeValidationFailed, "Service account private key PEM is invalid.")
	}
	if key, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		rk, ok := key.(*rsa.PrivateKey)
		if !ok {
			return nil, errs.New(errs.CodeValidationFailed, "Service account key is not RSA.")
		}
		return rk, nil
	}
	rk, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeValidationFailed, "gmail.sa.pem")
	}
	return rk, nil
}
