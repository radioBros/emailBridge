package logging

import "context"

// IDs are the correlation IDs a record carries (S12 §2–3, §10). They travel in context.Context and are
// added to every record logged with that context.
type IDs struct {
	RequestID      string
	TraceID        string
	SpanID         string
	OrganizationID string
	AccountID      string
	EventID        string
	DeliveryID     string
	OutboundID     string
	Actor          string // usr_… or key_…
	JobID          string
}

type idsKey struct{}

// IDsFrom returns the IDs stored in ctx (zero value if none).
func IDsFrom(ctx context.Context) IDs {
	if ctx == nil {
		return IDs{}
	}
	if v, ok := ctx.Value(idsKey{}).(IDs); ok {
		return v
	}
	return IDs{}
}

// With returns a context whose IDs are the current ones updated by set. Typical use:
//
//	ctx = logging.With(ctx, func(id *logging.IDs) { id.AccountID = acc.ID })
func With(ctx context.Context, set func(*IDs)) context.Context {
	ids := IDsFrom(ctx)
	set(&ids)
	return context.WithValue(ctx, idsKey{}, ids)
}
