package gmail_test

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	simgmail "github.com/radiobros/emailbridge/simulator/gmail"
)

func TestServer_putGetHistorySend(t *testing.T) {
	s := simgmail.New()
	defer s.Close()
	s.SetToken("a@ex.com", "tok")
	raw := []byte("From: a@ex.com\r\nTo: b@ex.com\r\nSubject: Hi\r\nMessage-ID: <1@ex>\r\n\r\nbody")
	id, thr, hid := s.PutMessage("a@ex.com", raw, []string{"INBOX", "UNREAD"})
	if id == "" || thr == "" || hid == 0 {
		t.Fatalf("put %s %s %d", id, thr, hid)
	}

	req, _ := http.NewRequest(http.MethodGet, s.URL()+"/gmail/v1/users/me/messages/"+id+"?format=raw", nil)
	req.Header.Set("Authorization", "Bearer tok")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d", res.StatusCode)
	}
	var msg map[string]any
	_ = json.NewDecoder(res.Body).Decode(&msg)
	rawB64, _ := msg["raw"].(string)
	got, err := base64.RawURLEncoding.DecodeString(rawB64)
	if err != nil || !strings.Contains(string(got), "Subject: Hi") {
		t.Fatalf("raw %v %q", err, got)
	}

	req2, _ := http.NewRequest(http.MethodGet, s.URL()+"/gmail/v1/users/me/history?startHistoryId=1", nil)
	req2.Header.Set("Authorization", "Bearer tok")
	res2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res2.Body.Close() }()
	body, _ := io.ReadAll(res2.Body)
	if res2.StatusCode != http.StatusOK || !strings.Contains(string(body), id) {
		t.Fatalf("history %d %s", res2.StatusCode, body)
	}

	s.ExpireHistory("a@ex.com")
	req3, _ := http.NewRequest(http.MethodGet, s.URL()+"/gmail/v1/users/me/history?startHistoryId=1", nil)
	req3.Header.Set("Authorization", "Bearer tok")
	res3, err := http.DefaultClient.Do(req3)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res3.Body.Close() }()
	if res3.StatusCode != http.StatusNotFound {
		t.Fatalf("expire want 404 got %d", res3.StatusCode)
	}

	payload := s.PushPayload("a@ex.com")
	if !strings.Contains(string(payload), "a@ex.com") {
		t.Fatalf("push %s", payload)
	}
}

func TestServer_tokenAndWatch(t *testing.T) {
	s := simgmail.New()
	defer s.Close()
	res, err := http.PostForm(s.URL()+"/token", map[string][]string{
		"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"},
		"subject":    {"sa@ex.com"},
		"assertion":  {"x"},
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
	body := strings.NewReader(`{"topicName":"projects/p/topics/t"}`)
	req, _ := http.NewRequest(http.MethodPost, s.URL()+"/gmail/v1/users/me/watch", body)
	req.Header.Set("Authorization", "Bearer "+access)
	req.Header.Set("Content-Type", "application/json")
	res2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res2.Body.Close() }()
	if res2.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(res2.Body)
		t.Fatalf("%d %s", res2.StatusCode, b)
	}
}
