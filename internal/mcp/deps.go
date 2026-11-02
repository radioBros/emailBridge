package mcp

import (
	"context"

	"github.com/radiobros/emailbridge/internal/accounts"
	"github.com/radiobros/emailbridge/internal/audit"
	"github.com/radiobros/emailbridge/internal/events"
	"github.com/radiobros/emailbridge/internal/messages"
	"github.com/radiobros/emailbridge/internal/submit"
	"github.com/radiobros/emailbridge/internal/webhook"
)

// Accounts lists mail accounts.
type Accounts interface {
	List(ctx context.Context, f accounts.ListFilter) (*accounts.AccountList, error)
}

// Messages lists/searches/gets indexed mail.
type Messages interface {
	ListMessages(ctx context.Context, accountID string, f messages.ListFilter) (*messages.ListResult, error)
	SearchMessages(ctx context.Context, accountID string, in messages.SearchInput) ([]messages.MessageView, error)
	GetMessage(ctx context.Context, accountID, messageID string, opt messages.GetOptions) (*messages.MessageView, error)
}

// Submitter sends outbound mail.
type Submitter interface {
	Submit(ctx context.Context, accountID string, req submit.SubmitRequest, key string, includeRaw bool) (*submit.SubmitResult, error)
}

// Deliveries lists webhook deliveries.
type Deliveries interface {
	ListDeliveries(ctx context.Context, filter webhook.DeliveryFilter) (*webhook.DeliveryList, error)
}

// Events lists CloudEvents.
type Events interface {
	List(ctx context.Context, f events.ListFilter) (*events.List, error)
}

// Auditor is satisfied by *audit.Writer.
type Auditor interface {
	Record(ctx context.Context, action string, target audit.Target, changes audit.Changes) error
}

// AIOps exposes AI message operations (W52).
type AIOps interface {
	SummarizeMessage(ctx context.Context, accountID, messageID string) (string, error)
	ClassifyMessage(ctx context.Context, accountID, messageID string, labels []string) (string, error)
	ExtractMessage(ctx context.Context, accountID, messageID, schema string) (string, error)
	RiskMessage(ctx context.Context, accountID, messageID string) (risk, rationale string, err error)
}
