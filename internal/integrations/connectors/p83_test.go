package connectors

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestP83AllIDs(t *testing.T) {
	all := P83All()
	if len(all) != 18 {
		t.Fatalf("len=%d want 18", len(all))
	}
	seen := map[string]bool{}
	for _, d := range all {
		if d.ID == "" || d.Version == "" || d.DocsPath == "" || d.Category == "" {
			t.Fatalf("incomplete %+v", d)
		}
		if seen[d.ID] {
			t.Fatalf("dup %s", d.ID)
		}
		seen[d.ID] = true
		if d.TestFunc == nil && d.Tester == "" {
			t.Fatalf("%s missing test", d.ID)
		}
	}
	for _, id := range P83IDs() {
		if !seen[id] {
			t.Fatalf("missing %s", id)
		}
	}
}

func TestRspamdCheck(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/checkv2" || r.Method != http.MethodPost {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Header.Get("Password") != "pw" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		_, _ = io.ReadAll(r.Body)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"score":  4.5,
			"action": "add header",
			"symbols": map[string]any{
				"R_SPAM": map[string]any{"score": 2.0},
				"SIMPLE": 1.5,
			},
		})
	}))
	defer srv.Close()
	res, err := RspamdCheck(context.Background(), srv.Client(), RspamdOptions{
		BaseURL: srv.URL, Password: "pw",
	}, []byte("From: a@b.c\r\n\r\nhi"))
	if err != nil || res.Score != 4.5 || res.Action == "" || res.Symbols["R_SPAM"] != 2.0 || res.Symbols["SIMPLE"] != 1.5 {
		t.Fatalf("%+v %v", res, err)
	}
	_, err = RspamdCheck(context.Background(), srv.Client(), RspamdOptions{BaseURL: srv.URL}, []byte("x"))
	if err == nil {
		t.Fatal("expected auth fail")
	}
}

func TestSendingTesters(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	cases := []struct {
		def Def
		cfg map[string]any
		sec map[string]string
	}{
		{Postmark(), map[string]any{"baseUrl": srv.URL, "from": "a@b.c"}, map[string]string{"serverToken": "t"}},
		{Mailgun(), map[string]any{"baseUrl": srv.URL, "domain": "ex.com", "from": "a@b.c"}, map[string]string{"apiKey": "k"}},
		{SendGrid(), map[string]any{"baseUrl": srv.URL, "from": "a@b.c"}, map[string]string{"apiKey": "k"}},
		{Brevo(), map[string]any{"baseUrl": srv.URL, "from": "a@b.c"}, map[string]string{"apiKey": "k"}},
		{SES(), map[string]any{"baseUrl": srv.URL, "region": "eu-west-1", "from": "a@b.c", "accessKey": "A"}, map[string]string{"secretKey": "S"}},
	}
	for _, c := range cases {
		tr, err := RunTest(context.Background(), c.def, TestInput{Config: c.cfg, Secrets: c.sec, Client: srv.Client()})
		if err != nil || !tr.OK {
			t.Fatalf("%s: %+v %v", c.def.ID, tr, err)
		}
	}
}

func TestStorageUploadAndTest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		switch {
		case strings.Contains(r.URL.Path, "/drive/v3/about"):
			_ = json.NewEncoder(w).Encode(map[string]any{"user": map[string]any{"emailAddress": "u@ex.com"}})
		case strings.Contains(r.URL.Path, "/upload/drive"):
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "file-1"})
		case strings.Contains(r.URL.Path, "/me/drive") && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "drive"})
		case strings.Contains(r.URL.Path, "/content"):
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "item-1"})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	tr, err := RunTest(context.Background(), GDrive(), TestInput{
		Config: map[string]any{"baseUrl": srv.URL}, Secrets: map[string]string{"accessToken": "t"}, Client: srv.Client(),
	})
	if err != nil || !tr.OK {
		t.Fatalf("gdrive test: %+v %v", tr, err)
	}
	id, err := GDriveUpload(context.Background(), srv.Client(), srv.URL, "t", UploadMeta{Name: "a.txt", FolderID: "f"}, []byte("hi"))
	if err != nil || id != "file-1" {
		t.Fatalf("upload %s %v", id, err)
	}
	tr, err = RunTest(context.Background(), OneDrive(), TestInput{
		Config: map[string]any{"baseUrl": srv.URL}, Secrets: map[string]string{"accessToken": "t"}, Client: srv.Client(),
	})
	if err != nil || !tr.OK {
		t.Fatalf("onedrive test: %+v %v", tr, err)
	}
	id, err = OneDriveUpload(context.Background(), srv.Client(), srv.URL, "t", UploadMeta{Name: "a.txt", FolderPath: "EB"}, []byte("hi"))
	if err != nil || id != "item-1" {
		t.Fatalf("od upload %s %v", id, err)
	}
}

func TestCRMLogMail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		switch {
		case strings.Contains(r.URL.Path, "/contacts/search"):
			_ = json.NewEncoder(w).Encode(map[string]any{"results": []map[string]any{{"id": "c1"}}})
		case strings.Contains(r.URL.Path, "/objects/emails"):
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "e1"})
		case strings.Contains(r.URL.Path, "/contacts"):
			_ = json.NewEncoder(w).Encode(map[string]any{"results": []any{}})
		case strings.Contains(r.URL.Path, "/persons/search"):
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"items": []map[string]any{{"item": map[string]any{"id": 9}}}}})
		case strings.Contains(r.URL.Path, "/notes"):
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"id": 42}})
		case strings.Contains(r.URL.Path, "/users/me"):
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true})
		default:
			t.Logf("path=%s body=%s", r.URL.Path, body)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	tr, err := RunTest(context.Background(), HubSpot(), TestInput{
		Config: map[string]any{"baseUrl": srv.URL}, Secrets: map[string]string{"accessToken": "t"}, Client: srv.Client(),
	})
	if err != nil || !tr.OK {
		t.Fatalf("hubspot test %+v %v", tr, err)
	}
	id, err := HubSpotLogMail(context.Background(), srv.Client(), srv.URL, "t", MailEngagement{
		Address: "a@b.c", Subject: "s", Body: "b", Direction: "inbound",
	})
	if err != nil || id != "e1" {
		t.Fatalf("%s %v", id, err)
	}
	tr, err = RunTest(context.Background(), Pipedrive(), TestInput{
		Config: map[string]any{"baseUrl": srv.URL, "companyDomain": "acme"}, Secrets: map[string]string{"apiToken": "t"}, Client: srv.Client(),
	})
	if err != nil || !tr.OK {
		t.Fatalf("pipedrive test %+v %v", tr, err)
	}
	nid, err := PipedriveLogMail(context.Background(), srv.Client(), srv.URL, "t", "acme", MailEngagement{
		Address: "a@b.c", Subject: "s", Body: "b", Direction: "outbound",
	})
	if err != nil || nid != "42" {
		t.Fatalf("%s %v", nid, err)
	}
}

func TestSSOAndObservability(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "openid-configuration") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"issuer": srv.URL})
	}))
	defer srv.Close()
	for _, d := range []Def{SSOAuthentik(), SSOKeycloak(), SSOEntra(), SSOGoogle(), SSOOkta()} {
		tr, err := RunTest(context.Background(), d, TestInput{
			Config: map[string]any{"issuerUrl": srv.URL, "clientId": "c"}, Secrets: map[string]string{"clientSecret": "s"}, Client: srv.Client(),
		})
		if err != nil || !tr.OK {
			t.Fatalf("%s: %+v %v", d.ID, tr, err)
		}
	}
	tr, err := RunTest(context.Background(), Sentry(), TestInput{Secrets: map[string]string{"dsn": "https://x@example/1"}})
	if err != nil || !tr.OK {
		t.Fatalf("sentry %+v %v", tr, err)
	}
	root, _ := os.Getwd()
	for root != "" && filepath.Base(root) != "emailBridge" {
		parent := filepath.Dir(root)
		if parent == root {
			break
		}
		root = parent
	}
	dash := filepath.Join(root, "deploy", "observability", "presets", "grafana", "emailbridge.json")
	col := filepath.Join(root, "deploy", "observability", "presets", "otel", "collector.yaml")
	tr, err = RunTest(context.Background(), Grafana(), TestInput{Config: map[string]any{"dashboardPath": dash}})
	if err != nil || !tr.OK {
		t.Fatalf("grafana %+v %v path=%s", tr, err, dash)
	}
	tr, err = RunTest(context.Background(), OTel(), TestInput{Config: map[string]any{"collectorPath": col}})
	if err != nil || !tr.OK {
		t.Fatalf("otel %+v %v", tr, err)
	}
	tr, err = RunTest(context.Background(), Loki(), TestInput{})
	if err != nil || !tr.OK {
		t.Fatalf("loki %+v %v", tr, err)
	}
}
