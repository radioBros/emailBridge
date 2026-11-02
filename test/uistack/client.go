package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"
)

// Client is a cookie-jar HTTP client with EmailBridge CSRF headers.
type Client struct {
	Base string
	C    *http.Client
	Hdr  map[string]string
}

func newClient(base string) *Client {
	jar, _ := cookiejar.New(nil)
	return &Client{Base: base, C: &http.Client{
		Jar: jar, Timeout: 60 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}, Hdr: map[string]string{}}
}

// Resp is one HTTP response.
type Resp struct {
	Status int
	Header http.Header
	Body   []byte
	JSON   map[string]any
}

func (r Resp) Str(k string) string {
	s, _ := r.JSON[k].(string)
	return s
}

func (c *Client) Do(method, path string, body any) Resp {
	var rd io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		rd = strings.NewReader(b)
	case []byte:
		rd = bytes.NewReader(b)
	default:
		raw, _ := json.Marshal(b)
		rd = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, c.Base+path, rd)
	if err != nil {
		panic(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Origin", c.Base)
	req.Header.Set("X-Requested-With", "EmailBridge")
	for k, v := range c.Hdr {
		req.Header.Set(k, v)
	}
	res, err := c.C.Do(req)
	if err != nil {
		panic(err)
	}
	defer func() { _ = res.Body.Close() }()
	raw, _ := io.ReadAll(res.Body)
	out := Resp{Status: res.StatusCode, Header: res.Header, Body: raw}
	_ = json.Unmarshal(raw, &out.JSON)
	return out
}

func (c *Client) Cookie(name string) *http.Cookie {
	u, _ := url.Parse(c.Base)
	for _, ck := range c.C.Jar.Cookies(u) {
		if ck.Name == name {
			return ck
		}
	}
	return nil
}

func (c *Client) mustOK(r Resp, want int, what string) Resp {
	if r.Status != want {
		panic(fmt.Sprintf("%s: want %d got %d %s", what, want, r.Status, r.Body))
	}
	return r
}

func (c *Client) waitJSON(path string, timeout time.Duration, what string, pred func(Resp) bool) Resp {
	deadline := time.Now().Add(timeout)
	var last Resp
	for {
		last = c.Do(http.MethodGet, path, nil)
		if pred(last) {
			return last
		}
		if time.Now().After(deadline) {
			panic(fmt.Sprintf("timeout waiting for %s: last=%d %s", what, last.Status, last.Body))
		}
		time.Sleep(250 * time.Millisecond)
	}
}
