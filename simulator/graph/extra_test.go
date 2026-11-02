package graph_test

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	simgraph "github.com/radiobros/emailbridge/simulator/graph"
)

func mustDo(t *testing.T, req *http.Request) *http.Response {
	t.Helper()
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestServer_usersUPNAndDeleteSend(t *testing.T) {
	s := simgraph.New()
	defer s.Close()
	s.EnsureUser("tenant-user@ex.com")
	s.SetToken("tenant-user@ex.com", "tok")

	req, _ := http.NewRequest(http.MethodGet, s.URL()+"/v1.0/users/tenant-user@ex.com/mailFolders", nil)
	req.Header.Set("Authorization", "Bearer tok")
	res := mustDo(t, req)
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		t.Fatal(res.StatusCode)
	}

	raw := []byte("From: a@ex.com\r\nTo: b@ex.com\r\nSubject: Z\r\nMessage-ID: <z@ex>\r\n\r\nz")
	id, _, fid := s.PutMessage("tenant-user@ex.com", raw, "inbox")

	req2, _ := http.NewRequest(http.MethodGet, s.URL()+"/v1.0/me/mailFolders/"+fid+"/messages", nil)
	req2.Header.Set("Authorization", "Bearer tok")
	res2 := mustDo(t, req2)
	defer func() { _ = res2.Body.Close() }()
	b, _ := io.ReadAll(res2.Body)
	if !strings.Contains(string(b), id) {
		t.Fatalf("%s", b)
	}

	req3, _ := http.NewRequest(http.MethodDelete, s.URL()+"/v1.0/me/messages/"+id, nil)
	req3.Header.Set("Authorization", "Bearer tok")
	res3 := mustDo(t, req3)
	defer func() { _ = res3.Body.Close() }()
	if res3.StatusCode != http.StatusNoContent {
		t.Fatal(res3.StatusCode)
	}

	body := strings.NewReader(`{"message":{"subject":"Hi","body":{"contentType":"Text","content":"x"},"toRecipients":[{"emailAddress":{"address":"b@ex.com"}}]}}`)
	req4, _ := http.NewRequest(http.MethodPost, s.URL()+"/v1.0/me/sendMail", body)
	req4.Header.Set("Authorization", "Bearer tok")
	req4.Header.Set("Content-Type", "application/json")
	res4 := mustDo(t, req4)
	defer func() { _ = res4.Body.Close() }()
	if res4.StatusCode != http.StatusAccepted {
		bb, _ := io.ReadAll(res4.Body)
		t.Fatalf("%d %s", res4.StatusCode, bb)
	}

	req5, _ := http.NewRequest(http.MethodGet, s.URL()+"/v1.0/me", nil)
	req5.Header.Set("Authorization", "Bearer tok")
	res5 := mustDo(t, req5)
	defer func() { _ = res5.Body.Close() }()
	var me map[string]any
	_ = json.NewDecoder(res5.Body).Decode(&me)
	if me["mail"] == nil {
		t.Fatal(me)
	}

	req6, _ := http.NewRequest(http.MethodGet, s.URL()+"/v1.0/me/mailFolders('inbox')", nil)
	req6.Header.Set("Authorization", "Bearer tok")
	res6 := mustDo(t, req6)
	defer func() { _ = res6.Body.Close() }()
	if res6.StatusCode != http.StatusOK {
		t.Fatal(res6.StatusCode)
	}

	exp := time.Now().UTC().Add(time.Hour).Format(time.RFC3339)
	body7 := strings.NewReader(`{"changeType":"created","notificationUrl":"https://x/c","resource":"/me/messages","expirationDateTime":"` + exp + `","clientState":"c"}`)
	req7, _ := http.NewRequest(http.MethodPost, s.URL()+"/v1.0/subscriptions", body7)
	req7.Header.Set("Authorization", "Bearer tok")
	req7.Header.Set("Content-Type", "application/json")
	res7 := mustDo(t, req7)
	defer func() { _ = res7.Body.Close() }()
	var sub map[string]any
	_ = json.NewDecoder(res7.Body).Decode(&sub)
	sid, _ := sub["id"].(string)
	req8, _ := http.NewRequest(http.MethodDelete, s.URL()+"/v1.0/subscriptions/"+sid, nil)
	req8.Header.Set("Authorization", "Bearer tok")
	res8 := mustDo(t, req8)
	defer func() { _ = res8.Body.Close() }()
	if res8.StatusCode != http.StatusNoContent {
		t.Fatal(res8.StatusCode)
	}
	if s.Subscription(sid) != nil {
		t.Fatal("sub should be gone")
	}
}

func TestServer_unauthorized(t *testing.T) {
	s := simgraph.New()
	defer s.Close()
	res, err := http.Get(s.URL() + "/v1.0/me")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatal(res.StatusCode)
	}
}

func TestServer_getMessageSetReadSubGet(t *testing.T) {
	s := simgraph.New()
	defer s.Close()
	s.SetToken("u@ex.com", "tok")
	raw := []byte("From: u@ex.com\r\nTo: x@ex.com\r\nSubject: G\r\nMessage-ID: <g@ex>\r\n\r\ng")
	id, _, _ := s.PutMessage("u@ex.com", raw, "inbox")
	s.SetRead("u@ex.com", id, true)

	req, _ := http.NewRequest(http.MethodGet, s.URL()+"/v1.0/me/messages/"+id, nil)
	req.Header.Set("Authorization", "Bearer tok")
	res := mustDo(t, req)
	defer func() { _ = res.Body.Close() }()
	var msg map[string]any
	_ = json.NewDecoder(res.Body).Decode(&msg)
	if msg["isRead"] != true {
		t.Fatalf("%v", msg["isRead"])
	}

	exp := time.Now().UTC().Add(time.Hour).Format(time.RFC3339)
	body := strings.NewReader(`{"changeType":"created","notificationUrl":"https://x/c","resource":"/me/messages","expirationDateTime":"` + exp + `","clientState":"c"}`)
	req2, _ := http.NewRequest(http.MethodPost, s.URL()+"/v1.0/subscriptions", body)
	req2.Header.Set("Authorization", "Bearer tok")
	req2.Header.Set("Content-Type", "application/json")
	res2 := mustDo(t, req2)
	defer func() { _ = res2.Body.Close() }()
	var sub map[string]any
	_ = json.NewDecoder(res2.Body).Decode(&sub)
	sid, _ := sub["id"].(string)
	req3, _ := http.NewRequest(http.MethodGet, s.URL()+"/v1.0/subscriptions/"+sid, nil)
	req3.Header.Set("Authorization", "Bearer tok")
	res3 := mustDo(t, req3)
	defer func() { _ = res3.Body.Close() }()
	if res3.StatusCode != http.StatusOK {
		t.Fatal(res3.StatusCode)
	}
	req4, _ := http.NewRequest(http.MethodGet, s.URL()+"/v1.0/subscriptions/missing", nil)
	req4.Header.Set("Authorization", "Bearer tok")
	res4 := mustDo(t, req4)
	defer func() { _ = res4.Body.Close() }()
	if res4.StatusCode != http.StatusNotFound {
		t.Fatal(res4.StatusCode)
	}
}
