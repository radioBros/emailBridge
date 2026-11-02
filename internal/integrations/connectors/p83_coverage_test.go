package connectors

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAllP83IDsAndTesters(t *testing.T) {
	all := P83All()
	if len(all) < 16 {
		t.Fatalf("len=%d", len(all))
	}
	ids := P83IDs()
	if len(ids) != len(all) {
		t.Fatal(len(ids))
	}
	// Alias used by some call sites.
	_ = len(P83All())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true,"results":[{"id":"c1"}],"data":{"items":[{"item":{"id":1}}],"id":1},"id":"f1","user":{}}`))
	}))
	defer srv.Close()

	for _, d := range all {
		if d.ID == "" || d.TestFunc == nil {
			t.Fatalf("bad def %s", d.ID)
		}
		cfg := map[string]any{"baseUrl": srv.URL, "region": "eu-west-1", "domain": "ex.com",
			"companyDomain": "ex", "issuerUrl": srv.URL, "dashboardPath": ".", "collectorPath": ".",
			"from": "a@b.c", "accessKey": "ak"}
		sec := map[string]string{
			"secretKey": "sk", "serverToken": "t", "apiKey": "k", "accessToken": "tok",
			"apiToken": "t", "clientSecret": "cs", "dsn": "https://example/dsn",
		}
		// File-presence testers need real paths — override for grafana/otel.
		if d.ID == TypeGrafana || d.ID == TypeOTel {
			tr, err := d.TestFunc(context.Background(), TestInput{Config: map[string]any{
				"dashboardPath": "p83_coverage_test.go", "collectorPath": "p83_coverage_test.go",
			}})
			if err != nil || !tr.OK {
				t.Fatalf("%s file test %+v %v", d.ID, tr, err)
			}
			continue
		}
		if d.ID == TypeLoki || d.ID == TypeSentry {
			tr, err := d.TestFunc(context.Background(), TestInput{Config: cfg, Secrets: sec})
			if err != nil || !tr.OK {
				t.Fatalf("%s %+v %v", d.ID, tr, err)
			}
			continue
		}
		tr, err := RunTest(context.Background(), d, TestInput{Config: cfg, Secrets: sec, Client: srv.Client()})
		if err != nil || !tr.OK {
			t.Fatalf("%s %+v %v", d.ID, tr, err)
		}
	}
}

func TestRspamdCheckAndUploadsAndCRM(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		_ = b
		switch {
		case strings.Contains(r.URL.Path, "checkv2"):
			_, _ = w.Write([]byte(`{"score":1.5,"action":"no action","symbols":{"X":{"score":1.5},"Y":2}}`))
		case strings.Contains(r.URL.Path, "contacts/search"), strings.Contains(r.URL.Path, "objects/emails"):
			_, _ = w.Write([]byte(`{"results":[{"id":"c1"}],"id":"e1"}`))
		case strings.Contains(r.URL.Path, "persons/search"):
			_, _ = w.Write([]byte(`{"data":{"items":[{"item":{"id":9}}]}}`))
		case strings.Contains(r.URL.Path, "/notes"):
			_, _ = w.Write([]byte(`{"data":{"id":3}}`))
		case strings.Contains(r.URL.Path, "upload") || strings.Contains(r.URL.Path, "/content"):
			_, _ = w.Write([]byte(`{"id":"file1"}`))
		default:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()

	res, err := RspamdCheck(context.Background(), srv.Client(), RspamdOptions{BaseURL: srv.URL, Password: "p"}, []byte("raw"))
	if err != nil || res.Score != 1.5 || res.Symbols["X"] != 1.5 {
		t.Fatalf("%+v %v", res, err)
	}
	id, err := GDriveUpload(context.Background(), srv.Client(), srv.URL, "tok", UploadMeta{Name: "a.txt", FolderID: "f"}, []byte("x"))
	if err != nil || id == "" {
		t.Fatalf("%s %v", id, err)
	}
	id, err = OneDriveUpload(context.Background(), srv.Client(), srv.URL, "tok", UploadMeta{Name: "a.txt", FolderPath: "EB"}, []byte("x"))
	if err != nil || id == "" {
		t.Fatalf("%s %v", id, err)
	}
	eid, err := HubSpotLogMail(context.Background(), srv.Client(), srv.URL, "tok", MailEngagement{Address: "a@b.c", Subject: "s", Body: "b", Direction: "inbound"})
	if err != nil || eid == "" {
		t.Fatalf("%s %v", eid, err)
	}
	nid, err := PipedriveLogMail(context.Background(), srv.Client(), srv.URL, "tok", "co", MailEngagement{Address: "a@b.c", Subject: "s", Body: "b", Direction: "outbound"})
	if err != nil || nid == "" {
		t.Fatalf("%s %v", nid, err)
	}
}

func TestHelpersAndTruncate(t *testing.T) {
	if firstNonEmpty("", " a ") != "a" || strCfg(nil, "x") != "" || firstNonEmpty() != "" {
		t.Fatal("helpers")
	}
	if truncate("abcdef", 3) != "abc" || truncate("ab", 5) != "ab" {
		t.Fatal("truncate")
	}
	if basicAuthMailgun("k") == "" {
		t.Fatal("b64")
	}
	schema := sendingSchema(map[string]any{"x": map[string]any{"type": "string"}}, "from", "x")
	if schema["type"] != "object" {
		t.Fatal(schema)
	}
}

func TestErrorBranches(t *testing.T) {
	_, err := RspamdCheck(context.Background(), nil, RspamdOptions{}, nil)
	if err == nil {
		t.Fatal("rspamd empty")
	}
	_, err = GDriveUpload(context.Background(), nil, "", "", UploadMeta{}, nil)
	if err == nil {
		t.Fatal("gdrive")
	}
	_, err = OneDriveUpload(context.Background(), nil, "", "", UploadMeta{}, nil)
	if err == nil {
		t.Fatal("onedrive")
	}
	_, err = HubSpotLogMail(context.Background(), nil, "", "", MailEngagement{})
	if err == nil {
		t.Fatal("hubspot")
	}
	_, err = PipedriveLogMail(context.Background(), nil, "", "", "", MailEngagement{})
	if err == nil {
		t.Fatal("pipedrive")
	}
	_, err = MapReplyToSend(FeatherDeskReply{To: "a@b.c"})
	if err == nil {
		t.Fatal("empty body+subject")
	}
	_, err = ParseFeatherDeskReply([]byte(`{`))
	if err == nil {
		t.Fatal("bad json")
	}
	if VerifyFeatherDeskCallback("", []byte("x"), "1", "y", time.Now().UTC()) {
		t.Fatal("empty token")
	}
	tr, _ := Sentry().TestFunc(context.Background(), TestInput{Secrets: map[string]string{"dsn": "not-url"}})
	if tr.OK {
		t.Fatal("sentry dsn")
	}
	tr, _ = Grafana().TestFunc(context.Background(), TestInput{Config: map[string]any{"dashboardPath": "no-such-file-xyz"}})
	if tr.OK {
		t.Fatal("grafana missing")
	}
	tr, _ = OTel().TestFunc(context.Background(), TestInput{Config: map[string]any{"collectorPath": "no-such-file-xyz"}})
	if tr.OK {
		t.Fatal("otel missing")
	}
	fail := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer fail.Close()
	tr, _ = RunTest(context.Background(), Postmark(), TestInput{
		Config: map[string]any{"baseUrl": fail.URL}, Secrets: map[string]string{"serverToken": "t"}, Client: fail.Client(),
	})
	if tr.OK {
		t.Fatal("postmark fail")
	}
	tr, _ = RunTest(context.Background(), Postmark(), TestInput{Config: map[string]any{}, Client: fail.Client()})
	if tr.OK {
		t.Fatal("postmark missing token")
	}
	id, err := OneDriveUpload(context.Background(), fail.Client(), fail.URL, "tok", UploadMeta{DriveID: "d", Name: "n"}, []byte("x"))
	if err == nil || id != "" {
		t.Fatalf("onedrive drive fail %s %v", id, err)
	}
}
