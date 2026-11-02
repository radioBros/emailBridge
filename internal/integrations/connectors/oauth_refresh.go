package connectors

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// OAuthTokenInput is the pasted client + refresh token used by storage connectors (W52).
type OAuthTokenInput struct {
	TokenURL     string
	ClientID     string
	ClientSecret string
	RefreshToken string
	AccessToken  string
	Provider     string // gdrive | onedrive
}

// RefreshOAuthToken returns a usable access token, refreshing when needed.
func RefreshOAuthToken(ctx context.Context, client *http.Client, in OAuthTokenInput) (string, error) {
	if strings.TrimSpace(in.AccessToken) != "" && strings.TrimSpace(in.RefreshToken) == "" {
		return strings.TrimSpace(in.AccessToken), nil
	}
	if strings.TrimSpace(in.RefreshToken) == "" {
		if tok := strings.TrimSpace(in.AccessToken); tok != "" {
			return tok, nil
		}
		return "", fmt.Errorf("oauth: refreshToken or accessToken required")
	}
	if strings.TrimSpace(in.ClientID) == "" || strings.TrimSpace(in.ClientSecret) == "" {
		if tok := strings.TrimSpace(in.AccessToken); tok != "" {
			return tok, nil
		}
		return "", fmt.Errorf("oauth: clientId and clientSecret required to refresh")
	}
	tokenURL := strings.TrimSpace(in.TokenURL)
	if tokenURL == "" {
		switch in.Provider {
		case TypeGDrive:
			tokenURL = "https://oauth2.googleapis.com/token" //nolint:gosec // G101: well-known OAuth endpoint URL, not a credential
		case TypeOneDrive:
			tokenURL = "https://login.microsoftonline.com/common/oauth2/v2.0/token" //nolint:gosec // G101: well-known OAuth endpoint URL, not a credential
		default:
			return "", fmt.Errorf("oauth: tokenUrl required")
		}
	}
	if client == nil {
		client = http.DefaultClient
	}
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {in.RefreshToken},
		"client_id":     {in.ClientID},
		"client_secret": {in.ClientSecret},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = res.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return "", fmt.Errorf("oauth refresh: HTTP %d: %s", res.StatusCode, truncate(string(raw), 200))
	}
	var out struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || out.AccessToken == "" {
		return "", fmt.Errorf("oauth refresh: invalid token response")
	}
	return out.AccessToken, nil
}
