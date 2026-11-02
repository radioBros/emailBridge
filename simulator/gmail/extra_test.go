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

func TestServer_labelsModifyDeleteListSend(t *testing.T) {
	s := simgmail.New()
	defer s.Close()
	s.EnsureUser("a@ex.com")
	s.SetToken("a@ex.com", "tok")
	s.AddUserLabel("a@ex.com", "Label_1", "Work")

	raw := []byte("From: a@ex.com\r\nTo: b@ex.com\r\nSubject: Z\r\n\r\nbody")
	id, _, _ := s.PutMessage("a@ex.com", raw, []string{"INBOX", "UNREAD"})

	req, _ := http.NewRequest(http.MethodGet, s.URL()+"/gmail/v1/users/me/labels", nil)
	req.Header.Set("Authorization", "Bearer tok")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		t.Fatal(res.StatusCode)
	}

	req, _ = http.NewRequest(http.MethodGet, s.URL()+"/gmail/v1/users/me/messages?labelIds=INBOX", nil)
	req.Header.Set("Authorization", "Bearer tok")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if !strings.Contains(string(body), id) {
		t.Fatalf("list %s", body)
	}

	req, _ = http.NewRequest(http.MethodGet, s.URL()+"/gmail/v1/users/me/messages/"+id+"?format=metadata", nil)
	req.Header.Set("Authorization", "Bearer tok")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatal(res.StatusCode)
	}

	modBody := strings.NewReader(`{"addLabelIds":["STARRED"],"removeLabelIds":["UNREAD"]}`)
	req, _ = http.NewRequest(http.MethodPost, s.URL()+"/gmail/v1/users/me/messages/"+id+"/modify", modBody)
	req.Header.Set("Authorization", "Bearer tok")
	req.Header.Set("Content-Type", "application/json")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatal(res.StatusCode)
	}

	req, _ = http.NewRequest(http.MethodGet, s.URL()+"/gmail/v1/users/me/history?startHistoryId=1", nil)
	req.Header.Set("Authorization", "Bearer tok")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	hb, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if !strings.Contains(string(hb), "labelsAdded") && !strings.Contains(string(hb), "STARRED") {
		t.Fatalf("history after modify %s", hb)
	}

	s.DeleteMessage("a@ex.com", id)
	req, _ = http.NewRequest(http.MethodGet, s.URL()+"/gmail/v1/users/me/history?startHistoryId=1", nil)
	req.Header.Set("Authorization", "Bearer tok")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	hb, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if !strings.Contains(string(hb), "messagesDeleted") {
		t.Fatalf("history delete %s", hb)
	}

	sendRaw := base64.RawURLEncoding.EncodeToString(raw)
	req, _ = http.NewRequest(http.MethodPost, s.URL()+"/gmail/v1/users/me/messages",
		strings.NewReader(`{"raw":"`+sendRaw+`"}`))
	req.Header.Set("Authorization", "Bearer tok")
	req.Header.Set("Content-Type", "application/json")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var sent map[string]any
	_ = json.NewDecoder(res.Body).Decode(&sent)
	_ = res.Body.Close()
	if sent["id"] == nil {
		t.Fatal("send")
	}

	req, _ = http.NewRequest(http.MethodGet, s.URL()+"/gmail/v1/users/me/profile", nil)
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatal(res.StatusCode)
	}
	req, _ = http.NewRequest(http.MethodGet, s.URL()+"/gmail/v1/users/other@ex.com/profile", nil)
	req.Header.Set("Authorization", "Bearer tok")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatal(res.StatusCode)
	}
	req, _ = http.NewRequest(http.MethodGet, s.URL()+"/gmail/v1/users/me/messages/missing", nil)
	req.Header.Set("Authorization", "Bearer tok")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Fatal(res.StatusCode)
	}
	req, _ = http.NewRequest(http.MethodGet, s.URL()+"/nope", nil)
	req.Header.Set("Authorization", "Bearer tok")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Fatal(res.StatusCode)
	}
	_ = s.HistoryID("a@ex.com")
	_ = s.HistoryID("missing@ex.com")
}
