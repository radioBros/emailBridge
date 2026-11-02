package integrations

import "context"

// OutboundSubmitter sends mail on behalf of an integration (wired to submit.Service). idempotencyKey makes a
// re-delivered callback send at most once.
type OutboundSubmitter interface {
	SubmitOutbound(ctx context.Context, accountID string, to []string, subject, text string, headers map[string]string, idempotencyKey string) error
}
