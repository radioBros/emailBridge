package graph_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	simgraph "github.com/radiobros/emailbridge/simulator/graph"
)

func TestServer_putDeltaMIMESend(t *testing.T) {
	s := simgraph.New()
	defer s.Close()
	s.SetToken("a@ex.com", "tok")
	raw := []byte("From: a@ex.com\r\nTo: b@ex.com\r\nSubject: Hi\r\nMessage-ID: <1@ex>\r\n\r\nbody")
	id, conv, fid := s.PutMessage("a@ex.com", raw, "inbox")
	if id == "" || conv == "" || fid == "" {
		t.Fatalf("put %s %s %s", id, conv, fid)
	}

	req, _ := http.NewRequest(http.MethodGet, s.URL()+"/v1.0/me/mailFolders/"+fid+"/messages/delta", nil)
	req.Header.Set("Authorization", "Bearer tok")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK || !strings.Contains(string(body), id) {
		t.Fatalf("delta %d %s", res.StatusCode, body)
	}
	if !strings.Contains(string(body), "deltaLink") {
		t.Fatalf("no deltaLink: %s", body)
	}

	req2, _ := http.NewRequest(http.MethodGet, s.URL()+"/v1.0/me/messages/"+id+"/$value", nil)
	req2.Header.Set("Authorization", "Bearer tok")
	res2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res2.Body.Close() }()
	mime, _ := io.ReadAll(res2.Body)
	if res2.StatusCode != http.StatusOK || !strings.Contains(string(mime), "Subject: Hi") {
		t.Fatalf("mime %d %s", res2.StatusCode, mime)
	}

	s.ExpireDelta("a@ex.com")
	req3, _ := http.NewRequest(http.MethodGet, s.URL()+"/v1.0/me/mailFolders/"+fid+"/messages/delta", nil)
	req3.Header.Set("Authorization", "Bearer tok")
	res3, err := http.DefaultClient.Do(req3)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res3.Body.Close() }()
	if res3.StatusCode != http.StatusGone {
		t.Fatalf("expire want 410 got %d", res3.StatusCode)
	}
}

func TestServer_tokenClientCredentialsAndSub(t *testing.T) {
	s := simgraph.New()
	defer s.Close()
	res, err := http.PostForm(s.URL()+"/token", map[string][]string{
		"grant_type":    {"client_credentials"},
		"client_id":     {"cid"},
		"client_secret": {"sec"},
		"scope":         {"https://graph.microsoft.com/.default"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	var tok map[string]any
	_ = json.NewDecoder(res.Body).Decode(&tok)
	access, _ := tok["access_token"].(string)
	if access == "" {
		t.Fatal("no token")
	}

	exp := time.Now().UTC().Add(48 * time.Hour).Format(time.RFC3339)
	body := bytes.NewReader([]byte(`{
		"changeType":"created,updated,deleted",
		"notificationUrl":"https://example.com/callbacks/graph",
		"resource":"/me/mailFolders('inbox')/messages",
		"expirationDateTime":"` + exp + `",
		"clientState":"secret"
	}`))
	req, _ := http.NewRequest(http.MethodPost, s.URL()+"/v1.0/subscriptions", body)
	req.Header.Set("Authorization", "Bearer "+access)
	req.Header.Set("Content-Type", "application/json")
	res2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res2.Body.Close() }()
	if res2.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(res2.Body)
		t.Fatalf("%d %s", res2.StatusCode, b)
	}
	var sub map[string]any
	_ = json.NewDecoder(res2.Body).Decode(&sub)
	sid, _ := sub["id"].(string)
	if sid == "" || s.Subscription(sid) == nil {
		t.Fatal("sub missing")
	}
	payload := s.NotifyPayload(sid)
	if !strings.Contains(string(payload), "secret") {
		t.Fatalf("notify %s", payload)
	}

	exp2 := time.Now().UTC().Add(72 * time.Hour).Format(time.RFC3339)
	req3, _ := http.NewRequest(http.MethodPatch, s.URL()+"/v1.0/subscriptions/"+sid,
		strings.NewReader(`{"expirationDateTime":"`+exp2+`"}`))
	req3.Header.Set("Authorization", "Bearer "+access)
	req3.Header.Set("Content-Type", "application/json")
	res3, err := http.DefaultClient.Do(req3)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res3.Body.Close() }()
	if res3.StatusCode != http.StatusOK {
		t.Fatalf("renew %d", res3.StatusCode)
	}
}

func TestServer_patchMoveFolders(t *testing.T) {
	s := simgraph.New()
	defer s.Close()
	s.SetToken("u@ex.com", "t")
	raw := []byte("From: u@ex.com\r\nTo: x@ex.com\r\nSubject: M\r\nMessage-ID: <m@ex>\r\n\r\nx")
	id, _, _ := s.PutMessage("u@ex.com", raw, "inbox")
	sent := s.FolderID("u@ex.com", "sentitems")

	req, _ := http.NewRequest(http.MethodPatch, s.URL()+"/v1.0/me/messages/"+id,
		strings.NewReader(`{"isRead":true,"flag":{"flagStatus":"flagged"}}`))
	req.Header.Set("Authorization", "Bearer t")
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("patch %d", res.StatusCode)
	}

	req2, _ := http.NewRequest(http.MethodPost, s.URL()+"/v1.0/me/messages/"+id+"/move",
		strings.NewReader(`{"destinationId":"`+sent+`"}`))
	req2.Header.Set("Authorization", "Bearer t")
	req2.Header.Set("Content-Type", "application/json")
	res2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res2.Body.Close() }()
	if res2.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(res2.Body)
		t.Fatalf("move %d %s", res2.StatusCode, b)
	}

	req3, _ := http.NewRequest(http.MethodGet, s.URL()+"/v1.0/me/mailFolders", nil)
	req3.Header.Set("Authorization", "Bearer t")
	res3, err := http.DefaultClient.Do(req3)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res3.Body.Close() }()
	b, _ := io.ReadAll(res3.Body)
	if !strings.Contains(string(b), "Inbox") {
		t.Fatalf("folders %s", b)
	}
}
