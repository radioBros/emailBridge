package integrations

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestDigestMinutesOf(t *testing.T) {
	if digestMinutesOf(nil) != 0 || digestMinutesOf(map[string]any{"digestMinutes": 15}) != 15 {
		t.Fatal("int")
	}
	if digestMinutesOf(map[string]any{"digestMinutes": float64(7)}) != 7 {
		t.Fatal("float")
	}
	if digestMinutesOf(map[string]any{"digestMinutes": json.Number("3")}) != 3 {
		t.Fatal("number")
	}
	if digestMinutesOf(map[string]any{"digestMinutes": int32(2)}) != 2 {
		t.Fatal("int32")
	}
}

func TestDigestPayloadShapes(t *testing.T) {
	svc := &Service{Registry: DefaultRegistry()}
	inst := &Instance{Type: TypeSlack, Config: map[string]any{"webhookUrl": "https://hooks.example/x"}}
	u, body, err := svc.digestPayload(context.Background(), inst, 2, []string{"A", "B"})
	if err != nil || u == "" || !strings.Contains(string(body), "A") {
		t.Fatalf("%s %s %v", u, body, err)
	}
	inst.Type = TypeDiscord
	_, body, err = svc.digestPayload(context.Background(), inst, 1, []string{"Z"})
	if err != nil || !strings.Contains(string(body), `"content"`) {
		t.Fatalf("%s %v", body, err)
	}
	inst.Type = TypeTeams
	_, body, err = svc.digestPayload(context.Background(), inst, 1, nil)
	if err != nil || !strings.Contains(string(body), "MessageCard") {
		t.Fatalf("%s %v", body, err)
	}
	inst.Type = TypeTelegram
	inst.Config = map[string]any{"chatId": "1", "_apiBase": "http://localhost"}
	if _, _, err := svc.digestPayload(context.Background(), inst, 1, nil); err == nil {
		t.Fatal("expected telegram error")
	}
}

func TestDigestDutyReturnsOnCancel(t *testing.T) {
	svc := &Service{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan struct{})
	go func() { svc.DigestDuty(ctx); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("duty hung")
	}
}

func TestCallbackLimiterIndependentKeys(t *testing.T) {
	l := newCallbackLimiter(0) // default 60
	now := time.Now()
	if err := l.take("x", now); err != nil {
		t.Fatal(err)
	}
	if err := l.take("y", now); err != nil {
		t.Fatal(err)
	}
}

func TestTelegramDigestPayload(t *testing.T) {
	svc := &Service{Registry: DefaultRegistry()}
	inst := &Instance{
		Type:    TypeTelegram,
		Config:  map[string]any{"chatId": "42", "_apiBase": "https://tg.test"},
		Secrets: nil,
	}
	// decryptSecrets with empty secrets → incomplete
	u, body, err := svc.digestPayload(context.Background(), inst, 3, []string{"S"})
	if err == nil && u != "" {
		t.Fatalf("unexpected %s %s", u, body)
	}
}
