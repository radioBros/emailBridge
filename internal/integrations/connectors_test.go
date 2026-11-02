package integrations

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSlackAndZapierTestHooks(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	slack := slackDefinition()
	tr, err := slack.Test(context.Background(), TestInput{
		Config: map[string]any{"webhookUrl": srv.URL},
		Client: srv.Client(),
	})
	if err != nil || !tr.OK {
		t.Fatalf("%+v %v", tr, err)
	}
	if gotBody == "" || !strings.Contains(gotBody, "Slack") {
		t.Fatalf("body %q", gotBody)
	}

	zap := ZapierDefinition()
	tr, err = zap.Test(context.Background(), TestInput{
		Config: map[string]any{"hookUrl": srv.URL},
		Client: srv.Client(),
	})
	if err != nil || !tr.OK {
		t.Fatalf("%+v %v", tr, err)
	}
}

func TestRspamdTestHook(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ping" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Header.Get("Password") != "pw" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("pong"))
	}))
	defer srv.Close()

	def := RspamdDefinition()
	tr, err := def.Test(context.Background(), TestInput{
		Config:  map[string]any{"baseUrl": srv.URL},
		Secrets: map[string]string{"password": "pw"},
		Client:  srv.Client(),
	})
	if err != nil || !tr.OK {
		t.Fatalf("%+v %v", tr, err)
	}
	tr, err = def.Test(context.Background(), TestInput{
		Config: map[string]any{"baseUrl": srv.URL},
		Client: srv.Client(),
	})
	if err != nil || tr.OK {
		t.Fatalf("expected fail without password: %+v", tr)
	}
}
