package audit

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/logging"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

// Writer appends audit records to MongoDB and mirrors them to the audit log category.
type Writer struct {
	DB *store.DB
	// Log is the audit-category slog.Logger (logging.System.For(CatAudit, …)).
	Log *slog.Logger
	// Retention sets expireAt = now+Retention when > 0 (S11 EB_RETENTION_AUDIT).
	Retention time.Duration
}

// Record writes one append-only audit entry. Actor comes from tenancy.CallerFrom (system when
// absent); IP from httpapi.ClientIP; user-agent from WithUserAgent; request/trace from logging.IDsFrom.
func (w *Writer) Record(ctx context.Context, action string, target Target, changes Changes) error {
	if w == nil || w.DB == nil {
		return fmt.Errorf("audit: writer not configured")
	}
	now := time.Now().UTC()
	rec := Record{
		ID:      store.NewID(store.PrefixAudit),
		At:      now,
		Action:  action,
		Target:  target,
		Changes: redactChanges(changes),
	}
	ids := logging.IDsFrom(ctx)
	rec.RequestID, rec.TraceID = ids.RequestID, ids.TraceID

	actor := Actor{Type: ActorSystem, ID: "system"}
	if c, ok := tenancy.CallerFrom(ctx); ok {
		switch c.Kind {
		case tenancy.CallerAPIKey:
			actor.Type, actor.ID = ActorAPIKey, c.APIKeyID
		case tenancy.CallerSystem:
			actor.Type, actor.ID = ActorSystem, "system"
		default:
			actor.Type, actor.ID = ActorUser, c.UserID
		}
		if c.OrganizationID != "" {
			org := c.OrganizationID
			rec.OrganizationID = &org
		}
	} else if org, ok := store.OrganizationFrom(ctx); ok {
		rec.OrganizationID = &org
	}
	if ip := httpapi.ClientIP(ctx); ip.IsValid() {
		actor.IP = ip.String()
	}
	actor.UserAgent = UserAgentFrom(ctx)
	rec.Actor = actor

	if w.Retention > 0 {
		exp := now.Add(w.Retention)
		rec.ExpireAt = &exp
	}

	if _, err := w.DB.Collection(Collection).InsertOne(ctx, rec); err != nil {
		return fmt.Errorf("audit: insert: %w", err)
	}
	log := w.Log
	if log == nil {
		log = slog.Default()
	}
	log.InfoContext(ctx, "audit recorded",
		"action", rec.Action,
		"targetType", rec.Target.Type,
		"targetId", rec.Target.ID,
		"actorType", string(rec.Actor.Type),
		"actorId", rec.Actor.ID,
		"auditId", rec.ID,
	)
	return nil
}

func redactChanges(c Changes) Changes {
	if len(c) == 0 {
		return nil
	}
	out := make(Changes, len(c))
	for k, fc := range c {
		out[k] = FieldChange{
			Before: redactStored(k, fc.Before),
			After:  redactStored(k, fc.After),
		}
	}
	return out
}

func redactStored(key string, v any) any {
	if v == nil {
		return nil
	}
	if _, ok := v.(Secret); ok {
		return redacted
	}
	if sensitiveKey(key) {
		return redacted
	}
	return v
}
