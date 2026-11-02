package connectors

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAllIDsAndRoutes(t *testing.T) {
	all := All()
	if len(all) != 7 {
		t.Fatalf("len=%d", len(all))
	}
	seen := map[string]bool{}
	for _, d := range all {
		if d.ID == "" || d.Version == "" || d.DocsPath == "" {
			t.Fatalf("incomplete %+v", d)
		}
		if seen[d.ID] {
			t.Fatalf("dup %s", d.ID)
		}
		seen[d.ID] = true
		if d.Category == CategoryChat {
			if len(d.Routes) != 4 {
				t.Fatalf("%s routes=%d", d.ID, len(d.Routes))
			}
			if len(d.Endpoints) != 1 {
				t.Fatalf("%s endpoints", d.ID)
			}
		}
	}
	for _, id := range []string{IDSlack, IDTeams, IDDiscord, IDTelegram, IDMattermost, IDFeatherDesk, IDHelpdesk} {
		if !seen[id] {
			t.Fatalf("missing %s", id)
		}
	}
}

func TestPayloadMapsNonEmpty(t *testing.T) {
	maps := []string{
		MapSlackNewMail, MapSlackDelivery, MapSlackHealth, MapSlackQuota,
		MapTeamsNewMail, MapTeamsDelivery, MapTeamsHealth, MapTeamsQuota,
		MapDiscordNewMail, MapDiscordDelivery, MapDiscordHealth, MapDiscordQuota,
		MapTelegramNewMail, MapTelegramDelivery, MapTelegramHealth, MapTelegramQuota,
		MapMattermostNewMail, MapMattermostDelivery, MapMattermostHealth, MapMattermostQuota,
		MapFeatherDeskTicket, MapHelpdeskDefault,
	}
	for _, m := range maps {
		if !strings.Contains(m, "data.") && !strings.Contains(m, "type") {
			t.Fatalf("map looks empty: %s", m)
		}
	}
}

func TestChatWebhookTesters(t *testing.T) {
	var gotPath, gotBody, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cases := []struct {
		def    Def
		cfg    map[string]any
		sec    map[string]string
		substr string
	}{
		{Slack(), map[string]any{"webhookUrl": srv.URL}, nil, "Slack"},
		{Teams(), map[string]any{"webhookUrl": srv.URL}, nil, "MessageCard"},
		{Discord(), map[string]any{"webhookUrl": srv.URL}, nil, "Discord"},
		{Mattermost(), map[string]any{"webhookUrl": srv.URL}, nil, "Mattermost"},
		{Helpdesk(), map[string]any{"webhookUrl": srv.URL}, nil, "emailbridge"},
	}
	for _, tc := range cases {
		tr, err := RunTest(context.Background(), tc.def, TestInput{Config: tc.cfg, Secrets: tc.sec, Client: srv.Client()})
		if err != nil || !tr.OK {
			t.Fatalf("%s: %+v %v", tc.def.ID, tr, err)
		}
		if !strings.Contains(gotBody, tc.substr) {
			t.Fatalf("%s body %q", tc.def.ID, gotBody)
		}
	}

	tr, err := RunTest(context.Background(), FeatherDesk(), TestInput{
		Config:  map[string]any{"baseUrl": srv.URL},
		Secrets: map[string]string{"apiKey": "k1"},
		Client:  srv.Client(),
	})
	if err != nil || !tr.OK {
		t.Fatalf("featherdesk %+v %v", tr, err)
	}
	if gotPath != "/api/v1/tickets" || gotAuth != "Bearer k1" {
		t.Fatalf("path=%s auth=%s", gotPath, gotAuth)
	}

	tr, err = RunTest(context.Background(), Telegram(), TestInput{
		Config:  map[string]any{"chatId": "42", "_apiBase": srv.URL},
		Secrets: map[string]string{"botToken": "tok"},
		Client:  srv.Client(),
	})
	if err != nil || !tr.OK {
		t.Fatalf("telegram %+v %v", tr, err)
	}
	if !strings.Contains(gotPath, "/bottok/sendMessage") {
		t.Fatalf("path %s", gotPath)
	}
}

func TestTesterFailures(t *testing.T) {
	tr, _ := RunTest(context.Background(), Slack(), TestInput{Config: map[string]any{}})
	if tr.OK {
		t.Fatal("slack")
	}
	tr, _ = RunTest(context.Background(), Telegram(), TestInput{Config: map[string]any{"chatId": "1"}})
	if tr.OK {
		t.Fatal("telegram token")
	}
	tr, _ = RunTest(context.Background(), FeatherDesk(), TestInput{Config: map[string]any{"baseUrl": "http://x"}})
	if tr.OK {
		t.Fatal("featherdesk key")
	}
}

func TestFeatherDeskReplyMappingAndHMAC(t *testing.T) {
	_, err := MapReplyToSend(FeatherDeskReply{})
	if err == nil {
		t.Fatal("empty")
	}
	send, err := MapReplyToSend(FeatherDeskReply{
		TicketID: "t1", MessageID: "m1", InReplyTo: "<x@y>", To: "a@b.c",
		Subject: "Re: hi", BodyText: "ok", AccountHint: "acc_1",
	})
	if err != nil || send.To[0] != "a@b.c" || send.Headers["In-Reply-To"] != "<x@y>" {
		t.Fatalf("%+v %v", send, err)
	}
	body := []byte(`{"to":"a@b.c","bodyText":"x"}`)
	now := time.Now().UTC()
	ts := now.Unix()
	sig := SignFeatherDesk("tok", ts, body)
	if !VerifyFeatherDeskCallback("tok", body, fmt.Sprintf("%d", ts), sig, now) {
		t.Fatal("sig")
	}
	if VerifyFeatherDeskCallback("tok", body, fmt.Sprintf("%d", ts), "bad", now) {
		t.Fatal("bad sig")
	}
	old := now.Add(-10 * time.Minute).Unix()
	if VerifyFeatherDeskCallback("tok", body, fmt.Sprintf("%d", old), SignFeatherDesk("tok", old, body), now) {
		t.Fatal("old ts")
	}
	r, err := ParseFeatherDeskReply(body)
	if err != nil || r.To != "a@b.c" {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestHTTPErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	tr, err := RunTest(context.Background(), Discord(), TestInput{
		Config: map[string]any{"webhookUrl": srv.URL}, Client: srv.Client(),
	})
	if err != nil || tr.OK {
		t.Fatalf("%+v", tr)
	}
}
