package events

import (
	"encoding/json"
	"time"

	"github.com/radiobros/emailbridge/internal/content"
	"github.com/radiobros/emailbridge/internal/events/types"
)

// Documentation examples of every event (W70). SchemaFiles writes each one into its JSON Schema as
// `examples`: first the full CloudEvents envelope exactly as a webhook endpoint receives it (the POST body of a
// delivery with the default payload options), then the bare `data` object. The values are typed Go values of
// the catalogue's data type, so an example can never drift from the schema; TestEventExamples checks that
// every catalogue type has one and that its subject has the catalogue's prefix.

// Example identifiers (pattern-valid ULIDs, the same organisation and account as the OpenAPI examples).
const (
	exOrg          = "org_01m5caqpk0kast63yc13wpy2d9"
	exAccount      = "acc_01m5cvnpb031g57sqct6mzhq94"
	exMailbox      = "mbx_01m5ccvej0khs793a5tesr1an7"
	exSentMailbox  = "mbx_01m5cez6h07ymb4n8z9nha90qn"
	exMessage      = "msg_01m5ch2yg09swc93g54trswp15"
	exBounceMsg    = "msg_01m5ck6pf0bf4meam7jp1fbskf"
	exPECMessage   = "msg_01m5cnaee070z834j1neyr6a58"
	exOutbound     = "out_01m5cqe6d02twj9xw9b9vyh5b4"
	exEndpoint     = "whe_01m5cshyc0d23xkhrbz7e490pm"
	exCapture      = "cap_01m5cxsea0rtzhbgs97gnj9kqm"
	exExport       = "exp_01m5czx69032gsbkp82n4ajd1w"
	exReceipt      = "rcp_01m5d20y80n4ps0apz3e6baqq3"
	exNotification = "ntf_01m5d44p70hy8qdwczk2dxyq84"
	exAttachment   = "att_01m5d68e60kfdar6a5nn8d0mae"
	exTraceParent  = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
)

// eventExample is the example of one catalogue type.
type eventExample struct {
	ID      string    // evt_… of the envelope
	Subject string    // resource the event is about (catalogue prefix)
	Account bool      // account-level event: source and ebaccount carry the account
	Time    time.Time // envelope time
	Data    any       // value of the catalogue's Go type
}

func exTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		panic(err)
	}
	return t
}

func exTimeP(s string) *time.Time {
	t := exTime(s)
	return &t
}

func exStr(s string) *string { return &s }

var exRef = types.AccountRef{AccountID: exAccount, ExternalID: exStr("crm-48213")}

var exInbox = types.Mailbox{MailboxID: exMailbox, Path: "INBOX"}

var exOutRef = types.OutboundRef{
	AccountRef: exRef, OutboundID: exOutbound,
	MessageIDHeader: "<01m5cqe6d02twj9xw9b9vyh5b4@mail.example.com>",
	Envelope:        types.Envelope{From: "reception@example.com", To: []string{"giulia.bianchi@example.org"}},
	Subject:         "Your appointment on 4 November",
}

var eventExamples = map[string]eventExample{
	AccountAdded: {ID: "evt_01m5c8kym0qwk6dxz1bjzecsf9", Subject: exAccount, Account: true,
		Time: exTime("2026-10-20T10:02:11.482Z"),
		Data: types.AccountAdded{AccountRef: exRef, Email: "reception@example.com", Name: "Reception"}},
	AccountUpdated: {ID: "evt_01m5caqpk0sz1anw6qhh3nyjzp", Subject: exAccount, Account: true,
		Time: exTime("2026-10-22T15:31:07.120Z"),
		Data: types.AccountUpdated{AccountRef: exRef, Changed: []string{"name", "sync"}}},
	AccountDeleted: {ID: "evt_01m5ccvej0khs793a5tesr1an7", Subject: exAccount, Account: true,
		Time: exTime("2026-10-27T16:45:00.031Z"),
		Data: types.AccountDeleted{AccountRef: exRef}},
	AccountInitialized: {ID: "evt_01m5cez6h07ymb4n8z9nha90qn", Subject: exAccount, Account: true,
		Time: exTime("2026-10-20T10:04:52.906Z"),
		Data: types.AccountInitialized{AccountRef: exRef, Mailboxes: 7, Messages: 12483, DurationMs: 158342}},
	AccountStateChanged: {ID: "evt_01m5ch2yg09swc93g54trswp15", Subject: exAccount, Account: true,
		Time: exTime("2026-10-26T08:14:03.554Z"),
		Data: types.AccountStateChanged{AccountRef: exRef, From: "connected", To: "authError",
			Reason: &types.Reason{Kind: "auth", Code: "mail_auth_failed", Message: "AUTHENTICATE failed: invalid credentials"}}},
	AccountAuthFailed: {ID: "evt_01m5ck6pf0bf4meam7jp1fbskf", Subject: exAccount, Account: true,
		Time: exTime("2026-10-26T08:14:03.551Z"),
		Data: types.AccountAuthFailed{AccountRef: exRef, Protocol: "imap", Code: "mail_auth_failed",
			Response:     "NO [AUTHENTICATIONFAILED] Invalid credentials (Failure)",
			FailingSince: exTime("2026-10-26T08:14:03Z"), DisableAt: exTimeP("2026-10-31T08:14:03Z")}},
	AccountAuthSucceeded: {ID: "evt_01m5cnaee070z834j1neyr6a58", Subject: exAccount, Account: true,
		Time: exTime("2026-10-26T09:02:41.007Z"),
		Data: types.AccountAuthSucceeded{AccountRef: exRef, Protocol: "imap", FailedFor: "48m38s"}},
	AccountConnectFailed: {ID: "evt_01m5cqe6d02twj9xw9b9vyh5b4", Subject: exAccount, Account: true,
		Time: exTime("2026-10-25T03:12:09.640Z"),
		Data: types.AccountConnectFailed{AccountRef: exRef, Protocol: "smtp", Code: "mail_timeout", Kind: "temporary",
			Response: "dial tcp 203.0.113.25:465: i/o timeout"}},
	MailboxCreated: {ID: "evt_01m5cshyc0d23xkhrbz7e490pm", Subject: exMailbox, Account: true,
		Time: exTime("2026-10-21T11:20:33.218Z"),
		Data: types.MailboxChanged{AccountRef: exRef, Mailbox: types.Mailbox{MailboxID: exMailbox, Path: "Clients/2026"}}},
	MailboxDeleted: {ID: "evt_01m5cvnpb0bd5w95cza76x3zrv", Subject: exMailbox, Account: true,
		Time: exTime("2026-10-24T17:05:12.902Z"),
		Data: types.MailboxChanged{AccountRef: exRef, Mailbox: types.Mailbox{MailboxID: exMailbox, Path: "Clients/2025"}}},
	MailboxReset: {ID: "evt_01m5cxsea0rtzhbgs97gnj9kqm", Subject: exMailbox, Account: true,
		Time: exTime("2026-10-23T06:40:18.377Z"),
		Data: types.MailboxReset{AccountRef: exRef, Mailbox: exInbox, OldUIDValidity: 1698740021,
			NewUIDValidity: 1729651200, Messages: 4120}},
	MessageReceived: {ID: "evt_01m5czx69032gsbkp82n4ajd1w", Subject: exMessage, Account: true,
		Time: exTime("2026-10-27T09:14:22.318Z"),
		Data: types.MessageReceived{
			AccountRef: exRef, MessageID: exMessage, Mailbox: exInbox, UID: 48213, UIDValidity: 1729651200,
			EmailID: "a3f1c9e07b2d4e58", ThreadID: "t_9c41e2b07a", MessageIDHeader: "<CAF7x2m9Qp1@mail.example.org>",
			InReplyTo:  "<01m5cqe6d02twj9xw9b9vyh5b4@mail.example.com>",
			References: []string{"<01m5cqe6d02twj9xw9b9vyh5b4@mail.example.com>"},
			Date:       exTimeP("2026-10-27T10:14:09+01:00"), InternalDate: exTime("2026-10-27T09:14:12Z"),
			Size: 48213, Flags: []string{}, Labels: []string{},
			From:    &types.Address{Name: "Giulia Bianchi", Address: "giulia.bianchi@example.org"},
			To:      []types.Address{{Name: "Reception", Address: "reception@example.com"}},
			Cc:      []types.Address{},
			Subject: "Re: Your appointment on 4 November",
			Text: &types.TextContent{Plain: "Thank you, 4 November at 10:00 works for me.\n\nGiulia",
				Truncated: false, Size: 412},
			Attachments: []types.AttachmentInfo{{ID: exAttachment, Filename: "id-card.pdf",
				ContentType: "application/pdf", Size: 38412, Disposition: "attachment", PartID: "2", Scan: "clean"}},
			AuthResults: &types.AuthResults{
				SPF:    &types.AuthResult{Result: "pass", Domain: "example.org"},
				DKIM:   []types.AuthResult{{Result: "pass", Domain: "example.org", Selector: "s2026"}},
				DMARC:  &types.AuthResult{Result: "pass", Domain: "example.org"},
				Source: "header",
			},
			CalendarEvents: calendarExample,
			ReplyText:      "Thank you, 4 November at 10:00 works for me.",
			SeenBefore:     false,
		}},
	MessageUpdated: {ID: "evt_01m5d20y80n4ps0apz3e6baqq3", Subject: exMessage, Account: true,
		Time: exTime("2026-10-27T09:31:40.004Z"),
		Data: types.MessageUpdated{AccountRef: exRef, MessageID: exMessage, Mailbox: exInbox, UID: 48213,
			Flags: types.FlagChange{Added: []string{"\\Seen"}, Removed: []string{}, Current: []string{"\\Seen"}}}},
	MessageDeleted: {ID: "evt_01m5d44p70hy8qdwczk2dxyq84", Subject: exMessage, Account: true,
		Time: exTime("2026-10-27T11:02:19.511Z"),
		Data: types.MessageDeleted{AccountRef: exRef, MessageID: exMessage, Mailbox: exInbox, UID: 48213,
			MessageIDHeader: "<CAF7x2m9Qp1@mail.example.org>"}},
	MessageMissing: {ID: "evt_01m5d68e60kfdar6a5nn8d0mae", Subject: exMessage, Account: true,
		Time: exTime("2026-10-27T11:05:44.120Z"),
		Data: types.MessageMissing{AccountRef: exRef, Mailbox: exInbox, UID: 48190,
			MessageIDHeader: "<20261026101500.4f2a@mail.example.net>"}},
	MessageSent: {ID: "evt_01m5d8c6505c3fnkwbkknhkq61", Subject: exOutbound, Account: true,
		Time: exTime("2026-10-27T08:30:02.775Z"),
		Data: types.MessageSent{OutboundRef: exOutRef, SentAt: exTime("2026-10-27T08:30:02.701Z"), Attempt: 1,
			SMTP: types.SMTPReply{Host: "smtp.example.com", Code: 250, Enhanced: "2.0.0",
				Response: "OK: queued as 4XkQ2m1Rz9z1y"}}},
	MessageDeliveryAttemptFailed: {ID: "evt_01m5dafy40qhsm3b2qx0ef6skh", Subject: exOutbound, Account: true,
		Time: exTime("2026-10-27T08:30:04.118Z"),
		Data: types.MessageDeliveryAttemptFailed{OutboundRef: exOutRef, Attempt: 1, MaxAttempts: 10,
			NextAttemptAt: exTime("2026-10-27T08:35:04Z"),
			Error: types.SendError{Code: "smtp_temporary", Kind: "temporary", Stage: "rcptTo", SMTPCode: 451,
				Enhanced: "4.7.1", Response: "Greylisted, please try again in 300 seconds"}}},
	MessageFailed: {ID: "evt_01m5dckp30mdexhj4qp140dym4", Subject: exOutbound, Account: true,
		Time: exTime("2026-10-28T08:30:05.402Z"),
		Data: types.MessageFailed{OutboundRef: exOutRef, Attempts: 10, OutcomeUnknown: false,
			Error: types.SendError{Code: "smtp_permanent", Kind: "permanent", Stage: "rcptTo", SMTPCode: 550,
				Enhanced: "5.1.1", Response: "5.1.1 <giulia.bianchi@example.org>: Recipient address rejected: User unknown"}}},
	MessageCaptured: {ID: "evt_01m5deqe20xh52jw94xna6cv58", Subject: exOutbound, Account: true,
		Time: exTime("2026-10-27T08:30:01.093Z"),
		Data: types.MessageCaptured{OutboundRef: exOutRef, CaptureID: exCapture, Policy: "capture"}},
	MessageBounced: {ID: "evt_01m5dgv610p98z84d29j5x3pmt", Subject: exBounceMsg, Account: true,
		Time: exTime("2026-10-27T08:31:15.664Z"),
		Data: types.MessageBounced{AccountRef: exRef, MessageID: exBounceMsg, OutboundID: exOutbound,
			MessageIDHeader: "<01m5cqe6d02twj9xw9b9vyh5b4@mail.example.com>", Recipient: "giulia.bianchi@example.org",
			Action: "failed", Status: "5.1.1", Diagnostic: "smtp; 550 5.1.1 User unknown", BounceType: "hard",
			MTA: "mx1.example.org"}},
	MessageComplained: {ID: "evt_01m5djyy00twg21xrfk5ssypq7", Subject: exBounceMsg, Account: true,
		Time: exTime("2026-10-27T13:48:20.218Z"),
		Data: types.MessageComplained{AccountRef: exRef, MessageID: exBounceMsg, OutboundID: exOutbound,
			MessageIDHeader: "<01m5cqe6d02twj9xw9b9vyh5b4@mail.example.com>", Recipient: "giulia.bianchi@example.org",
			FeedbackType: "abuse"}},
	TrackingOpened: {ID: "evt_01m5dn2nz0q7h3ncd1j49hxtnb", Subject: exOutbound, Account: true,
		Time: exTime("2026-10-27T09:02:51.330Z"),
		Data: types.TrackingOpened{AccountRef: exRef, OutboundID: exOutbound, Recipient: "giulia.bianchi@example.org",
			IP: "198.51.100.0", UserAgent: "Mozilla/5.0 (iPhone; CPU iPhone OS 18_1 like Mac OS X) AppleWebKit/605.1.15",
			Bot: false}},
	TrackingClicked: {ID: "evt_01m5dq6dy0bpw4pexpdp4cjcn0", Subject: exOutbound, Account: true,
		Time: exTime("2026-10-27T09:03:17.902Z"),
		Data: types.TrackingClicked{AccountRef: exRef, OutboundID: exOutbound, Recipient: "giulia.bianchi@example.org",
			URL: "https://www.example.com/appointments/confirm?ref=48213", IP: "198.51.100.0",
			UserAgent: "Mozilla/5.0 (iPhone; CPU iPhone OS 18_1 like Mac OS X) AppleWebKit/605.1.15", Bot: false}},
	ListUnsubscribed: {ID: "evt_01m5dsa5x0v78ag81yce7hjpr3", Subject: exOutbound, Account: true,
		Time: exTime("2026-10-27T09:05:40.011Z"),
		Data: types.ListUnsubscribed{AccountRef: exRef, OutboundID: exOutbound, Recipient: "giulia.bianchi@example.org",
			IP: "198.51.100.0", UserAgent: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36"}},
	ExportCompleted: {ID: "evt_01m5dvdxw0j7a55tjavm1fmhex", Subject: exExport, Account: true,
		Time: exTime("2026-10-27T10:20:31.845Z"),
		Data: types.ExportCompleted{AccountRef: exRef, ExportID: exExport, MessageCount: 12483, ByteSize: 734003200}},
	ExportFailed: {ID: "evt_01m5dxhnv019njbyyapkhp5rev", Subject: exExport, Account: true,
		Time: exTime("2026-10-27T10:12:08.390Z"),
		Data: types.ExportFailed{AccountRef: exRef, ExportID: exExport, Code: "service_unavailable",
			Message: "Object storage is not reachable"}},
	PECReceipt: {ID: "evt_01m5dzndt02er7qagrrs531p8g", Subject: exReceipt, Account: true,
		Time: exTime("2026-10-27T08:30:09.226Z"),
		Data: types.PECReceipt{AccountRef: exRef, ReceiptID: exReceipt, MessageID: exPECMessage, OutboundID: exOutbound,
			Type: "avvenuta-consegna", ReceiptKind: "completa", Error: "nessuno",
			TransportID:        "opec210312.20261027093006.123456.789.1.53@pec.example.it",
			RefMessageIDHeader: "<01m5cqe6d02twj9xw9b9vyh5b4@mail.example.com>",
			DeliveredTo:        "studio.rossi@pec.example.it",
			Recipients:         []types.PECRecipient{{Address: "studio.rossi@pec.example.it", Kind: "certificato"}},
			Provider:           "pec.example.it", At: exTime("2026-10-27T08:30:07Z"),
			Signature: types.PECSignature{Valid: true, Trusted: func() *bool { b := true; return &b }()},
			SHA256:    "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08", PECState: "delivered"}},
	EndpointTest: {ID: "evt_01m5e1s5s0fzzsp861d0hb22qd", Subject: exEndpoint,
		Time: exTime("2026-10-27T09:40:00.512Z"),
		Data: types.EndpointTest{EndpointID: exEndpoint, Message: "EmailBridge endpoint test",
			SentAt: exTime("2026-10-27T09:40:00.512Z")}},
	SystemAlertRaised: {ID: "evt_01m5e3wxr0k0k8597dg8a9k9m3", Subject: exNotification,
		Time: exTime("2026-10-27T07:15:00.140Z"),
		Data: types.SystemAlertRaised{NotificationID: exNotification, Key: "webhook.failing:" + exEndpoint,
			Category: "webhooks", Severity: "warning", Scope: exOrg, TitleKey: "notify.webhook.failing.title",
			DetailKey: "notify.webhook.failing.detail", Link: "/webhooks/" + exEndpoint, Occurrences: 3,
			Params: map[string]string{"name": "CRM inbound"}}},
	SystemAlertResolved: {ID: "evt_01m5e60nq0z6yeyzv196qgfrt4", Subject: exNotification,
		Time: exTime("2026-10-27T07:52:31.775Z"),
		Data: types.SystemAlertResolved{NotificationID: exNotification, Key: "webhook.failing:" + exEndpoint,
			Category: "webhooks", Scope: exOrg, ResolvedAt: exTime("2026-10-27T07:52:31.770Z")}},
}

// exampleFor returns the envelope (as delivered to a webhook) and the data object of a type's example.
func exampleFor(ct Type) (envelope, data any, ok bool) {
	ex, ok := eventExamples[ct.Name]
	if !ok {
		return nil, nil, false
	}
	raw, err := json.Marshal(ex.Data)
	if err != nil {
		return nil, nil, false
	}
	e := Event{
		SpecVersion: "1.0", ID: ex.ID, Type: ct.Name, Source: "/organizations/" + exOrg, Subject: ex.Subject,
		Time: ex.Time.UTC(), DataContentType: "application/json", DataSchema: SchemaURL(ct.Name, ct.Version),
		EBOrg: exOrg, EBSchema: ct.Version, TraceParent: exTraceParent, Data: raw,
	}
	if ex.Account {
		e.Source += "/accounts/" + exAccount
		e.EBAccount = exAccount
	}
	return toJSONValue(e), toJSONValue(ex.Data), true
}

// toJSONValue round-trips v through JSON so the schema files carry plain JSON values.
func toJSONValue(v any) any {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var out any
	if json.Unmarshal(raw, &out) != nil {
		return nil
	}
	return out
}

// calendarExample is the calendarEvents of the message.received example (an invitation sent with the reply).
var calendarExample = []content.CalendarEvent{{
	Method: "REQUEST", UID: "7f3c2b9e-1d4a-4c5b-9e2f-0a1b2c3d4e5f@example.org", Sequence: 0,
	Summary: "Appointment: Giulia Bianchi", Status: "CONFIRMED",
	DTStart:   &content.CalendarTime{DateTime: exTime("2026-11-04T09:00:00Z"), TimeZone: "Europe/Rome"},
	DTEnd:     &content.CalendarTime{DateTime: exTime("2026-11-04T09:30:00Z"), TimeZone: "Europe/Rome"},
	Organizer: &content.CalendarPerson{Address: "giulia.bianchi@example.org", Name: "Giulia Bianchi"},
	Attendees: []content.CalendarPerson{{Address: "reception@example.com", Name: "Reception",
		Role: "REQ-PARTICIPANT", PartStat: "NEEDS-ACTION"}},
}}
