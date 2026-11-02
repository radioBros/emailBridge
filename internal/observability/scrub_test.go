package observability

import (
	"testing"

	"github.com/getsentry/sentry-go"
)

func TestScrubBeforeSend(t *testing.T) {
	fn := scrubBeforeSend(false, "node-x", "role-y")
	ev := &sentry.Event{
		Request: &sentry.Request{
			URL:         "https://example.com/path?token=secret",
			Data:        "BODY",
			Cookies:     "a=b",
			QueryString: "token=secret",
			Headers: map[string]string{
				"Authorization": "Bearer x",
				"Cookie":        "a=b",
				"Set-Cookie":    "a=b",
				"X-Other":       "ok",
			},
		},
		User: sentry.User{Email: "a@b.c"},
	}
	out := fn(ev, nil)
	if out.Request.Data != "" || out.Request.Cookies != "" || out.Request.QueryString != "" {
		t.Fatalf("%+v", out.Request)
	}
	if out.Request.URL != "https://example.com/path" {
		t.Fatalf("url %q", out.Request.URL)
	}
	if _, ok := out.Request.Headers["Authorization"]; ok {
		t.Fatal("auth header kept")
	}
	if out.User.Email != "" {
		t.Fatal("user not cleared")
	}
	if out.Tags["node"] != "node-x" || out.Tags["role"] != "role-y" {
		t.Fatalf("tags %+v", out.Tags)
	}
	if fn(nil, nil) != nil {
		t.Fatal("nil event")
	}
	fnPII := scrubBeforeSend(true, "", "")
	ev2 := &sentry.Event{User: sentry.User{Email: "keep@x"}}
	out2 := fnPII(ev2, nil)
	if out2.User.Email != "keep@x" {
		t.Fatal("PII should keep user")
	}
}
