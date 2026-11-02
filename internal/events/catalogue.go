// Package events owns EmailBridge's event model (S3): the CloudEvents envelope, the catalogue of types, the
// transactional outbox writer (events are inserted in the same MongoDB transaction as the state change,
// S4 §3), the relay that publishes them to JetStream in order per partition, and the JSON Schemas served
// at /schemas/events/{type}/v{N}.json.
package events

import (
	"reflect"
	"sort"

	"github.com/radiobros/emailbridge/internal/events/types"
	"github.com/radiobros/emailbridge/internal/store"
)

// Type is a catalogue entry (S3 §3.1).
type Type struct {
	Name    string
	Phase   string
	Subject store.Prefix // prefix of the subject resource ID
	// AltSubject, when set, is a second accepted subject prefix (message.bounced is about the inbound bounce
	// when sync detects it, and about the outbound message when a sending provider reports it).
	AltSubject store.Prefix
	Version    int          // current major version of the data schema
	Data       reflect.Type // Go type of data (internal/events/types)
	// Synthetic events (endpoint.test) are never stored or routed.
	Synthetic bool
}

func t[T any](name string, subject store.Prefix) Type {
	return Type{Name: name, Phase: "P1", Subject: subject, Version: 1, Data: reflect.TypeFor[T]()}
}

func withAlt(ty Type, alt store.Prefix) Type {
	ty.AltSubject = alt
	return ty
}

// Event type names (S3 §3.1, P1).
const (
	AccountAdded                 = "account.added"
	AccountUpdated               = "account.updated"
	AccountDeleted               = "account.deleted"
	AccountInitialized           = "account.initialized"
	AccountStateChanged          = "account.state_changed"
	AccountAuthFailed            = "account.auth_failed"
	AccountAuthSucceeded         = "account.auth_succeeded"
	AccountConnectFailed         = "account.connect_failed"
	MailboxCreated               = "mailbox.created"
	MailboxDeleted               = "mailbox.deleted"
	MailboxReset                 = "mailbox.reset"
	MessageReceived              = "message.received"
	MessageUpdated               = "message.updated"
	MessageDeleted               = "message.deleted"
	MessageMissing               = "message.missing"
	MessageSent                  = "message.sent"
	MessageDeliveryAttemptFailed = "message.delivery_attempt_failed"
	MessageFailed                = "message.failed"
	MessageCaptured              = "message.captured"
	MessageBounced               = "message.bounced"
	MessageComplained            = "message.complained"
	TrackingOpened               = "tracking.opened"
	TrackingClicked              = "tracking.clicked"
	ListUnsubscribed             = "list.unsubscribed"
	ExportCompleted              = "export.completed"
	ExportFailed                 = "export.failed"
	PECReceipt                   = "pec.receipt"
	EndpointTest                 = "endpoint.test"
	SystemAlertRaised            = "system.alert.raised"
	SystemAlertResolved          = "system.alert.resolved"
)

var catalogue = func() map[string]Type {
	list := []Type{
		t[types.AccountAdded](AccountAdded, store.PrefixAccount),
		t[types.AccountUpdated](AccountUpdated, store.PrefixAccount),
		t[types.AccountDeleted](AccountDeleted, store.PrefixAccount),
		t[types.AccountInitialized](AccountInitialized, store.PrefixAccount),
		t[types.AccountStateChanged](AccountStateChanged, store.PrefixAccount),
		t[types.AccountAuthFailed](AccountAuthFailed, store.PrefixAccount),
		t[types.AccountAuthSucceeded](AccountAuthSucceeded, store.PrefixAccount),
		t[types.AccountConnectFailed](AccountConnectFailed, store.PrefixAccount),
		t[types.MailboxChanged](MailboxCreated, store.PrefixMailbox),
		t[types.MailboxChanged](MailboxDeleted, store.PrefixMailbox),
		t[types.MailboxReset](MailboxReset, store.PrefixMailbox),
		t[types.MessageReceived](MessageReceived, store.PrefixMessage),
		t[types.MessageUpdated](MessageUpdated, store.PrefixMessage),
		t[types.MessageDeleted](MessageDeleted, store.PrefixMessage),
		t[types.MessageMissing](MessageMissing, store.PrefixMessage),
		t[types.MessageSent](MessageSent, store.PrefixOutbound),
		t[types.MessageDeliveryAttemptFailed](MessageDeliveryAttemptFailed, store.PrefixOutbound),
		t[types.MessageFailed](MessageFailed, store.PrefixOutbound),
		t[types.MessageCaptured](MessageCaptured, store.PrefixOutbound),
		withAlt(t[types.MessageBounced](MessageBounced, store.PrefixMessage), store.PrefixOutbound),
		withAlt(t[types.MessageComplained](MessageComplained, store.PrefixMessage), store.PrefixOutbound),
		t[types.TrackingOpened](TrackingOpened, store.PrefixOutbound),
		t[types.TrackingClicked](TrackingClicked, store.PrefixOutbound),
		t[types.ListUnsubscribed](ListUnsubscribed, store.PrefixOutbound),
		t[types.ExportCompleted](ExportCompleted, store.PrefixExport),
		t[types.ExportFailed](ExportFailed, store.PrefixExport),
		t[types.PECReceipt](PECReceipt, store.PrefixPECReceipt),
		t[types.SystemAlertRaised](SystemAlertRaised, store.PrefixNotification),
		t[types.SystemAlertResolved](SystemAlertResolved, store.PrefixNotification),
	}
	et := t[types.EndpointTest](EndpointTest, store.PrefixEndpoint)
	et.Synthetic = true
	list = append(list, et)
	m := make(map[string]Type, len(list))
	for _, e := range list {
		m[e.Name] = e
	}
	return m
}()

// Lookup returns the catalogue entry of a type name.
func Lookup(name string) (Type, bool) {
	e, ok := catalogue[name]
	return e, ok
}

// Types lists the catalogue sorted by name.
func Types() []Type {
	out := make([]Type, 0, len(catalogue))
	for _, e := range catalogue {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
