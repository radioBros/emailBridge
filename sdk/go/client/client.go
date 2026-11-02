// Package client is a thin EmailBridge HTTP wrapper matching the TypeScript SDK surface:
// Bearer auth, EB-Organization, Idempotency-Key, RFC 9457 errors, 429/503 retries,
// cursor/page list helpers. Generated types live in github.com/radiobros/emailbridge/sdk/go/openapi.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"iter"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Page is a cursor/page list body as returned by EmailBridge list endpoints.
type Page[T any] struct {
	Data        []T     `json:"data"`
	NextCursor  *string `json:"nextCursor"`
	Total       *int64  `json:"total,omitempty"`
	TotalCapped *bool   `json:"totalCapped,omitempty"`
}

// Options configures a Client.
type Options struct {
	BaseURL        string
	APIKey         string
	OrganizationID string
	HTTPClient     *http.Client
	MaxRetries     int // default 3; attempts = MaxRetries+1 for 429/503
	Header         http.Header
}

// RequestOptions are per-call overrides.
type RequestOptions struct {
	Query          map[string]string
	Header         http.Header
	Body           any
	IdempotencyKey string
}

// Client talks to the EmailBridge REST API.
type Client struct {
	baseURL        string
	apiKey         string
	organizationID string
	http           *http.Client
	maxRetries     int
	header         http.Header
}

// New builds a Client. BaseURL should be the API origin without a trailing slash.
func New(opts Options) *Client {
	hc := opts.HTTPClient
	if hc == nil {
		hc = http.DefaultClient
	}
	retries := opts.MaxRetries
	if retries == 0 {
		retries = 3
	}
	base := strings.TrimRight(opts.BaseURL, "/")
	h := http.Header{}
	for k, vs := range opts.Header {
		for _, v := range vs {
			h.Add(k, v)
		}
	}
	return &Client{
		baseURL:        base,
		apiKey:         opts.APIKey,
		organizationID: opts.OrganizationID,
		http:           hc,
		maxRetries:     retries,
		header:         h,
	}
}

// Request performs an HTTP call, decoding a JSON body into dest when non-nil.
// 204/205 responses leave dest untouched and return nil.
func (c *Client) Request(ctx context.Context, method, path string, dest any, opts RequestOptions) error {
	u, err := c.buildURL(path, opts.Query)
	if err != nil {
		return err
	}

	var bodyBytes []byte
	if opts.Body != nil {
		bodyBytes, err = json.Marshal(opts.Body)
		if err != nil {
			return err
		}
	}

	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		var rdr io.Reader
		if bodyBytes != nil {
			rdr = bytes.NewReader(bodyBytes)
		}
		req, err := http.NewRequestWithContext(ctx, method, u, rdr)
		if err != nil {
			return err
		}
		c.applyHeaders(req, opts)
		if bodyBytes != nil && req.Header.Get("Content-Type") == "" {
			req.Header.Set("Content-Type", "application/json")
		}

		res, err := c.http.Do(req)
		if err != nil {
			return err
		}
		raw, readErr := io.ReadAll(res.Body)
		_ = res.Body.Close()
		if readErr != nil {
			return readErr
		}

		if res.StatusCode == http.StatusTooManyRequests || res.StatusCode == http.StatusServiceUnavailable {
			if attempt >= c.maxRetries {
				return &ProblemError{Problem: parseProblem(raw, res.StatusCode), Header: res.Header.Clone()}
			}
			if err := sleep(ctx, retryAfter(res.Header, attempt)); err != nil {
				return err
			}
			continue
		}

		if res.StatusCode == http.StatusNoContent || res.StatusCode == http.StatusResetContent {
			return nil
		}

		if res.StatusCode < 200 || res.StatusCode >= 300 {
			return &ProblemError{Problem: parseProblem(raw, res.StatusCode), Header: res.Header.Clone()}
		}

		if dest == nil || len(raw) == 0 {
			return nil
		}
		if err := json.Unmarshal(raw, dest); err != nil {
			return fmt.Errorf("client: decode response: %w", err)
		}
		return nil
	}
	return fmt.Errorf("client: request failed")
}

// Get is Request with method GET.
func (c *Client) Get(ctx context.Context, path string, dest any, opts RequestOptions) error {
	return c.Request(ctx, http.MethodGet, path, dest, opts)
}

// Post is Request with method POST.
func (c *Client) Post(ctx context.Context, path string, dest any, opts RequestOptions) error {
	return c.Request(ctx, http.MethodPost, path, dest, opts)
}

// Patch is Request with method PATCH.
func (c *Client) Patch(ctx context.Context, path string, dest any, opts RequestOptions) error {
	return c.Request(ctx, http.MethodPatch, path, dest, opts)
}

// Put is Request with method PUT.
func (c *Client) Put(ctx context.Context, path string, dest any, opts RequestOptions) error {
	return c.Request(ctx, http.MethodPut, path, dest, opts)
}

// Delete is Request with method DELETE.
func (c *Client) Delete(ctx context.Context, path string, dest any, opts RequestOptions) error {
	return c.Request(ctx, http.MethodDelete, path, dest, opts)
}

// IterateOptions configures cursor iteration.
type IterateOptions struct {
	Query  map[string]string
	Header http.Header
	Limit  int
}

// IterateCursor yields items from a `{ data, nextCursor }` list endpoint until the cursor is exhausted.
func IterateCursor[T any](ctx context.Context, c *Client, path string, opts IterateOptions) iter.Seq2[T, error] {
	return func(yield func(T, error) bool) {
		q := map[string]string{}
		for k, v := range opts.Query {
			q[k] = v
		}
		if opts.Limit > 0 {
			q["limit"] = strconv.Itoa(opts.Limit)
		}
		var zero T
		for {
			var page Page[T]
			if err := c.Get(ctx, path, &page, RequestOptions{Query: q, Header: opts.Header}); err != nil {
				yield(zero, err)
				return
			}
			for _, item := range page.Data {
				if !yield(item, nil) {
					return
				}
			}
			if page.NextCursor == nil || *page.NextCursor == "" {
				return
			}
			q["cursor"] = *page.NextCursor
		}
	}
}

// ListPage fetches one page-number style list (`page` + `limit` query params).
func ListPage[T any](ctx context.Context, c *Client, path string, page int, opts IterateOptions) (Page[T], error) {
	q := map[string]string{}
	for k, v := range opts.Query {
		q[k] = v
	}
	q["page"] = strconv.Itoa(page)
	if opts.Limit > 0 {
		q["limit"] = strconv.Itoa(opts.Limit)
	}
	var out Page[T]
	err := c.Get(ctx, path, &out, RequestOptions{Query: q, Header: opts.Header})
	return out, err
}

func (c *Client) applyHeaders(req *http.Request, opts RequestOptions) {
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	for k, vs := range c.header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	for k, vs := range opts.Header {
		for _, v := range vs {
			req.Header.Set(k, v)
		}
	}
	if c.organizationID != "" && req.Header.Get("EB-Organization") == "" {
		req.Header.Set("EB-Organization", c.organizationID)
	}
	if opts.IdempotencyKey != "" {
		req.Header.Set("Idempotency-Key", opts.IdempotencyKey)
	}
}

func (c *Client) buildURL(path string, query map[string]string) (string, error) {
	p := strings.TrimPrefix(path, "/")
	base := c.baseURL + "/"
	u, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	rel, err := url.Parse(p)
	if err != nil {
		return "", err
	}
	out := u.ResolveReference(rel)
	q := out.Query()
	for k, v := range query {
		if v == "" {
			continue
		}
		q.Set(k, v)
	}
	out.RawQuery = q.Encode()
	return out.String(), nil
}

func retryAfter(h http.Header, attempt int) time.Duration {
	ra := h.Get("Retry-After")
	if ra != "" {
		if secs, err := strconv.ParseFloat(ra, 64); err == nil {
			if secs < 0 {
				secs = 0
			}
			return time.Duration(secs * float64(time.Second))
		}
		if when, err := http.ParseTime(ra); err == nil {
			d := time.Until(when)
			if d < 0 {
				return 0
			}
			return d
		}
	}
	d := 500 * time.Millisecond * time.Duration(1<<attempt)
	if d > 30*time.Second {
		d = 30 * time.Second
	}
	return d
}

func sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
