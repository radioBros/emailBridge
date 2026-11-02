package main

import (
	"context"
	"errors"
	"log/slog"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/events"
	eventtypes "github.com/radiobros/emailbridge/internal/events/types"
	"github.com/radiobros/emailbridge/internal/gateways"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/suppression"
	"github.com/radiobros/emailbridge/internal/tracking"
)

// outboundAccount resolves the account (and its external ID) of an outbound message, for events raised
// outside the send path — tracking hits and provider delivery webhooks — so webhooks routed per account
// receive them. Unknown outbound IDs yield an organisation-level event.
func outboundAccount(db *store.DB) func(ctx context.Context, org, outboundID string) (string, *string) {
	return func(ctx context.Context, org, outboundID string) (string, *string) {
		if db == nil || !store.IsID(store.PrefixOutbound, outboundID) {
			return "", nil
		}
		var out struct {
			AccountID string `bson:"accountId"`
		}
		err := db.Collection("outboundMessages").FindOne(ctx, bson.M{"_id": outboundID, "organizationId": org},
			options.FindOne().SetProjection(bson.M{"accountId": 1})).Decode(&out)
		if err != nil {
			return "", nil
		}
		var acc struct {
			ExternalID *string `bson:"externalId"`
		}
		err = db.Collection("accounts").FindOne(ctx, bson.M{"_id": out.AccountID, "organizationId": org},
			options.FindOne().SetProjection(bson.M{"externalId": 1})).Decode(&acc)
		if err != nil && !errors.Is(err, mongo.ErrNoDocuments) {
			return out.AccountID, nil
		}
		return out.AccountID, acc.ExternalID
	}
}

// gatewaySink turns verified provider delivery webhooks (P8.3) into the same results as bounces detected by
// sync: hard bounces and complaints go onto the organisation's suppression lists, and message.bounced /
// message.complained are emitted with the outbound message they belong to. Soft bounces never suppress.
// Providers retry webhooks; suppression upserts are idempotent, a repeated event is emitted again.
type gatewaySink struct {
	events    *events.Writer
	suppress  *suppression.Service
	accountOf func(ctx context.Context, org, outboundID string) (string, *string)
	log       *slog.Logger
}

func (s gatewaySink) Deliver(ctx context.Context, orgID, gatewayID string, evs []gateways.DeliveryEvent) error {
	ctx = store.WithOrganization(ctx, orgID)
	for _, ev := range evs {
		accID, ext := s.accountOf(ctx, orgID, ev.OutboundID)
		ref := eventtypes.AccountRef{AccountID: accID, ExternalID: ext}
		var draft *events.Draft
		switch ev.Type {
		case gateways.DeliveryBounced:
			if ev.BounceType == "hard" && ev.Recipient != "" && s.suppress != nil {
				if err := s.suppress.AddBounce(ctx, orgID, ev.Recipient, "hard bounce reported by gateway "+gatewayID); err != nil {
					return err
				}
			}
			bt := ev.BounceType
			if bt == "" {
				bt = "soft"
			}
			draft = &events.Draft{Type: events.MessageBounced, OrganizationID: orgID, AccountID: accID, Subject: ev.OutboundID,
				Data: eventtypes.MessageBounced{AccountRef: ref, OutboundID: ev.OutboundID, Recipient: ev.Recipient,
					Action: "failed", Diagnostic: truncateRunes(ev.Diagnostic, 512), BounceType: bt, MTA: gatewayID}}
		case gateways.DeliveryComplained:
			if ev.Recipient != "" && s.suppress != nil {
				if err := s.suppress.AddComplaint(ctx, orgID, ev.Recipient, "complaint reported by gateway "+gatewayID); err != nil {
					return err
				}
			}
			draft = &events.Draft{Type: events.MessageComplained, OrganizationID: orgID, AccountID: accID, Subject: ev.OutboundID,
				Data: eventtypes.MessageComplained{AccountRef: ref, OutboundID: ev.OutboundID, Recipient: ev.Recipient,
					FeedbackType: "abuse"}}
		default:
			continue // sent / deferred / failed: the outbox already records the send attempt
		}
		if s.events == nil || !store.IsID(store.PrefixOutbound, ev.OutboundID) {
			continue // a provider event for mail EmailBridge did not send: suppressed, but no event
		}
		if _, err := s.events.Emit(ctx, *draft); err != nil {
			return err
		}
	}
	return nil
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// connectGatewaySink gives the gateways module (P8.3 delivery webhooks) its sink: suppression and events.
func connectGatewaySink(m *gateways.Module) app.Module {
	return &hook{name: "wire.gatewaySink", roles: []app.Role{app.RoleAPI}, start: func(_ context.Context, a *app.App) error {
		db, err := app.MustGet[*store.DB](a)
		if err != nil {
			return err
		}
		ev, _ := app.Get[*events.Writer](a)
		m.Sink = gatewaySink{events: ev, suppress: &suppression.Service{Repo: &suppression.Repo{DB: db}},
			accountOf: outboundAccount(db), log: a.Log}
		return nil
	}}
}

// connectTrackingAccounts lets tracking events carry the account of their outbound message.
func connectTrackingAccounts(m *tracking.Module) app.Module {
	return &hook{name: "wire.trackingAccounts", start: func(_ context.Context, a *app.App) error {
		db, err := app.MustGet[*store.DB](a)
		if err != nil {
			return err
		}
		m.AccountOf = outboundAccount(db)
		return nil
	}}
}
