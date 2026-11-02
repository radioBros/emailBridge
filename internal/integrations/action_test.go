package integrations

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/radiobros/emailbridge/internal/integrations/connectors"
	"github.com/radiobros/emailbridge/internal/sinks"
)

func TestParseActionPayload(t *testing.T) {
	body, _ := json.Marshal(map[string]any{
		"type": "message.received",
		"data": map[string]any{
			"accountId": "acc_1", "messageId": "msg_1", "subject": "Hi",
			"from": map[string]any{"address": "a@b.c"},
			"text": map[string]any{"plain": "hello"},
			"attachments": []any{
				map[string]any{"id": "att_1", "filename": "f.pdf", "contentType": "application/pdf"},
			},
		},
	})
	p, err := parseActionPayload(sinks.Message{Type: "message.received", Body: body, AccountID: "acc_x"})
	if err != nil || p.Address != "a@b.c" || p.Subject != "Hi" || p.Body != "hello" || len(p.Attachments) != 1 {
		t.Fatalf("%+v %v", p, err)
	}
	sent, _ := json.Marshal(map[string]any{"type": "message.sent", "data": map[string]any{
		"to": []any{map[string]any{"address": "z@z.z"}}, "subject": "Out",
	}})
	p2, err := parseActionPayload(sinks.Message{Type: "message.sent", Body: sent})
	if err != nil || p2.Direction != "out" || p2.Address != "z@z.z" {
		t.Fatalf("%+v %v", p2, err)
	}
}

func TestDispatchHubSpotAndPipedrive(t *testing.T) {
	var hs, pd int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = raw
		switch {
		case strings.Contains(r.URL.Path, "contacts/search"):
			hs++
			_, _ = w.Write([]byte(`{"results":[{"id":"c1"}]}`))
		case strings.Contains(r.URL.Path, "/emails"):
			hs++
			_, _ = w.Write([]byte(`{"id":"e1"}`))
		case strings.Contains(r.URL.Path, "persons/search"):
			pd++
			_, _ = w.Write([]byte(`{"data":{"items":[{"item":{"id":9}}]}}`))
		case strings.Contains(r.URL.Path, "/notes"):
			pd++
			_, _ = w.Write([]byte(`{"data":{"id":3}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	svc := &Service{HTTP: srv.Client()}
	eng := connectors.MailEngagement{Address: "a@b.c", Subject: "S", Body: "B", Direction: "in"}
	id, err := connectors.HubSpotLogMail(context.Background(), srv.Client(), srv.URL, "tok", eng)
	if err != nil || id != "e1" || hs < 2 {
		t.Fatalf("hubspot %q %v calls=%d", id, err, hs)
	}
	id, err = connectors.PipedriveLogMail(context.Background(), srv.Client(), srv.URL, "tok", "co", eng)
	if err != nil || id != "3" || pd < 2 {
		t.Fatalf("pipedrive %q %v calls=%d", id, err, pd)
	}
	_, err = svc.dispatchAction(context.Background(), &Instance{
		Type: connectors.TypeHubSpot, Config: map[string]any{"baseUrl": srv.URL},
	}, map[string]string{"accessToken": "tok"}, srv.Client(), actionPayload{
		Address: "a@b.c", Subject: "S", Body: "B", Direction: "in",
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestGatewaySpecFrom(t *testing.T) {
	def := Materialize(connectors.SES())
	spec := gatewaySpecFrom(def, map[string]any{
		"region": "eu-west-1", "from": "a@b.c", "accessKey": "AK",
	}, map[string]string{"secretKey": "SK"})
	if spec.Kind != "ses" || spec.Region != "eu-west-1" || spec.SecretKey != "SK" || spec.AccessKey != "AK" {
		t.Fatalf("%+v", spec)
	}
	pm := Materialize(connectors.Postmark())
	spec2 := gatewaySpecFrom(pm, map[string]any{"from": "x@y.z"}, map[string]string{"serverToken": "tok"})
	if spec2.APIKey != "tok" {
		t.Fatalf("%+v", spec2)
	}
}

func TestTruncateErr(t *testing.T) {
	if truncateErr("abc", 2) != "ab" || truncateErr("ab", 10) != "ab" {
		t.Fatal()
	}
}
