package oauth

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

// Verify checks authorize and token endpoint reachability (no real grant).
func (s *Service) Verify(ctx context.Context, id string) (*VerifyResult, error) {
	if err := tenancy.Require(ctx, tenancy.PermAccountsWrite); err != nil {
		return nil, err
	}
	if !store.IsID(store.PrefixOAuthApp, id) {
		return nil, errs.New(errs.CodeNotFound, "The OAuth app does not exist.")
	}
	a, err := s.Repo.getApp(ctx, id)
	if err != nil {
		return nil, err
	}
	start := time.Now()
	authURL, tokenURL, _ := s.endpoints(a)
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	authOK := s.reachable(cctx, http.MethodGet, authURL, nil)
	tokOK := s.reachable(cctx, http.MethodPost, tokenURL, url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {"eb-verify"},
		"client_secret": {"eb-verify"},
	})
	ok := authOK && tokOK
	res := &VerifyResult{
		OK: ok, LatencyMs: int(time.Since(start).Milliseconds()),
		AuthorizeReachable: &authOK, TokenReachable: &tokOK,
	}
	if !ok {
		res.Code = string(errs.CodeOauthExchangeFailed)
		res.Detail = "One or more OAuth endpoints were unreachable."
	}
	s.audit(ctx, "oauthApp.verified", id, nil)
	return res, nil
}

func (s *Service) reachable(ctx context.Context, method, raw string, form url.Values) bool {
	if strings.TrimSpace(raw) == "" {
		return false
	}
	var body *strings.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	var req *http.Request
	var err error
	if body != nil {
		req, err = http.NewRequestWithContext(ctx, method, raw, body)
		if err == nil {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
	} else {
		req, err = http.NewRequestWithContext(ctx, method, raw, nil)
	}
	if err != nil {
		return false
	}
	resp, err := s.httpClient().Do(req)
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	// Any HTTP response (including 4xx) means the endpoint is reachable.
	return true
}
