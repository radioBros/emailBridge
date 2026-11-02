package realtime

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/radiobros/emailbridge/internal/bus"
	"github.com/radiobros/emailbridge/internal/events"
)

// StreamSubscriber is the part of *bus.Bus the event fan-in uses.
type StreamSubscriber interface {
	SubscribeStream(ctx context.Context, stream, filter string, all bool, h func(subject string, data []byte)) (func(), error)
}

// topicsFor maps an event to the realtime topics that carry it (S13 §6).
func topicsFor(typ string) []string {
	var out []string
	if strings.HasPrefix(typ, "account.") || strings.HasPrefix(typ, "mailbox.") || strings.HasPrefix(typ, "message.") {
		out = append(out, "account.events")
	}
	switch typ {
	case events.AccountAdded, events.AccountUpdated, events.AccountDeleted, events.AccountStateChanged,
		events.AccountInitialized, events.AccountAuthFailed, events.AccountAuthSucceeded, events.AccountConnectFailed:
		out = append(out, "accounts.state")
	case events.MessageSent, events.MessageDeliveryAttemptFailed, events.MessageFailed, events.MessageCaptured:
		out = append(out, "outbox.updates")
	}
	return out
}

// FanInEvents delivers new events from EMAILBRIDGE_EVENTS to this node's sockets only (S4 §4
// `rt-<nodeId>`: an ephemeral deliver-new consumer per api node — every node sees every event, so local
// delivery avoids duplicates). Gaps after a reconnect are fine: clients refetch (S13 §6).
func (h *Hub) FanInEvents(ctx context.Context, sub StreamSubscriber) (stop func(), err error) {
	return sub.SubscribeStream(ctx, bus.StreamEvents, "eb.evt.>", false, func(_ string, data []byte) {
		var e events.Event
		if json.Unmarshal(data, &e) != nil || e.EBOrg == "" {
			return
		}
		keys := map[string]string{}
		if e.EBAccount != "" {
			keys["accountId"] = e.EBAccount
		}
		for _, topic := range topicsFor(e.Type) {
			h.dispatch(Message{Topic: topic, Type: e.Type, Data: data, Org: e.EBOrg, Keys: keys})
		}
	})
}
