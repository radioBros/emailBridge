//go:build integration

package webhook

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/radiobros/emailbridge/internal/rules"
	"github.com/radiobros/emailbridge/internal/store"
)

const mappedFakeKey = "fake-env-secret-for-mapped-body"

// capturingReceiver records request bodies.
func capturingReceiver(t *testing.T) (string, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(b))
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)
	return srv.URL, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), bodies...)
	}
}

func rawDelivery(t *testing.T, h *webhookHarness, id string) string {
	t.Helper()
	var raw bson.M
	if err := h.db.Collection(DeliveriesCollection).FindOne(context.Background(), bson.M{"_id": id}).Decode(&raw); err != nil {
		t.Fatal(err)
	}
	b, err := bson.MarshalExtJSON(raw, false, false)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func markFinal(t *testing.T, h *webhookHarness, id string) {
	t.Helper()
	if _, err := h.db.Collection(DeliveriesCollection).UpdateOne(context.Background(), bson.M{"_id": id},
		bson.M{"$set": bson.M{"state": StateSucceeded}}); err != nil {
		t.Fatal(err)
	}
}

func deliveryRow(t *testing.T, h *webhookHarness, id string) *Delivery {
	t.Helper()
	var d Delivery
	if err := h.db.Collection(DeliveriesCollection).FindOne(context.Background(), bson.M{"_id": id}).Decode(&d); err != nil {
		t.Fatal(err)
	}
	return &d
}

// Mapped bodies (which can carry settings.env secrets) are encrypted at rest, delivered decrypted, and
// re-sealed for a replay; legacy plaintext rows still deliver and replay encrypted.
func TestIntegrationMappedBodyEncryptedAtRest(t *testing.T) {
	h := newWebhookHarness(t)
	ctx := h.ctx
	if err := rules.W73Migration(45).Up(ctx, h.db); err != nil {
		t.Fatal(err)
	}
	h.rules.EnvLoader = func(context.Context, string) (map[string]string, []string, error) {
		return map[string]string{"apiKey": mappedFakeKey}, []string{"apiKey"}, nil
	}
	url, bodies := capturingReceiver(t)
	ep, err := h.service.CreateEndpoint(ctx, EndpointCreate{
		Name: "mapped", URL: url, Events: []string{"*"}, Secret: testSecret(81),
		Rule: &EndpointRule{Filter: `type == "message.received"`, Map: `{"key": env.apiKey, "id": data.messageId}`},
	})
	if err != nil {
		t.Fatal(err)
	}
	accountID := store.NewID(store.PrefixAccount)
	now := time.Now().UTC().Truncate(time.Second)
	ev := h.w73Event(t, h.orgID, accountID, "INBOX", "mapped-1", nil, now)
	if err := h.service.RouteEvent(ctx, ev); err != nil {
		t.Fatal(err)
	}
	rows := h.w73Deliveries(t, h.orgID, ev.ID)
	if len(rows) != 1 || rows[0].MappedBody != "" || rows[0].MappedBodyEnc.IsZero() {
		t.Fatalf("mapped body must be stored encrypted: %+v", rows)
	}
	if raw := rawDelivery(t, h, rows[0].ID); strings.Contains(raw, mappedFakeKey) || strings.Contains(raw, `"mappedBody"`) {
		t.Fatalf("plaintext secret in the stored delivery: %s", raw)
	}
	// The ciphertext is bound to its delivery id (AAD).
	moved := rows[0]
	moved.ID = store.NewID(store.PrefixDelivery)
	if _, _, err := h.service.openMappedBody(ctx, &moved); err == nil {
		t.Fatal("ciphertext must not open under another delivery id")
	}

	worker, err := h.service.Repo.endpointForWorker(ctx, h.orgID, ep.ID)
	if err != nil {
		t.Fatal(err)
	}
	out, err := h.service.attempt(ctx, &rows[0], worker)
	if err != nil || !out.Success {
		t.Fatalf("attempt: %+v %v", out, err)
	}
	if got := bodies(); len(got) != 1 || !strings.Contains(got[0], mappedFakeKey) {
		t.Fatalf("receiver must get the decrypted mapped body: %v", got)
	}

	markFinal(t, h, rows[0].ID)
	replay, err := h.service.Replay(ctx, rows[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	stored := deliveryRow(t, h, replay.ID)
	if stored.MappedBody != "" || stored.MappedBodyEnc.IsZero() {
		t.Fatalf("replay must be encrypted: %+v", stored)
	}
	plain, ok, err := h.service.openMappedBody(ctx, stored)
	if err != nil || !ok || !strings.Contains(plain, mappedFakeKey) {
		t.Fatalf("replay body: %q %v %v", plain, ok, err)
	}

	// Legacy row written before encryption: plaintext mappedBody.
	legacyID := store.NewID(store.PrefixDelivery)
	ev2 := h.w73Event(t, h.orgID, accountID, "INBOX", "legacy-1", nil, now)
	legacyBody := `{"key":"` + mappedFakeKey + `","legacy":true}`
	if _, err := h.db.Collection(DeliveriesCollection).InsertOne(context.Background(), bson.M{
		"_id": legacyID, "organizationId": h.orgID, "accountId": accountID, "eventId": ev2.ID,
		"eventType": ev2.Type, "endpointId": ep.ID, "state": StateSucceeded, "attempts": bson.A{},
		"isReplay": false, "mappedBody": legacyBody, "createdAt": now, "updatedAt": now,
		"expireAt": now.Add(time.Hour), "version": 1,
	}); err != nil {
		t.Fatal(err)
	}
	legacy := deliveryRow(t, h, legacyID)
	if _, err := h.service.attempt(ctx, legacy, worker); err != nil {
		t.Fatal(err)
	}
	if got := bodies(); len(got) != 2 || got[1] != legacyBody {
		t.Fatalf("legacy plaintext body must still deliver: %v", got)
	}
	lr, err := h.service.Replay(ctx, legacyID)
	if err != nil {
		t.Fatal(err)
	}
	if raw := rawDelivery(t, h, lr.ID); strings.Contains(raw, mappedFakeKey) {
		t.Fatalf("replay of a legacy row must be written encrypted: %s", raw)
	}
	if plain, ok, err := h.service.openMappedBody(ctx, deliveryRow(t, h, lr.ID)); err != nil || !ok || plain != legacyBody {
		t.Fatalf("legacy replay body: %q %v %v", plain, ok, err)
	}
}
