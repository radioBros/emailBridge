package sync

import (
	"context"

	"github.com/radiobros/emailbridge/internal/events/types"
	"github.com/radiobros/emailbridge/internal/index"
	"github.com/radiobros/emailbridge/internal/mailproto/imapc"
)

// SpamScorer scores new inbound mail for an organisation (P8.3 Rspamd). It runs only for accounts whose
// `integrations.rspamd` switch is on (Account.SpamScoring); Score gets the raw source and returns nil when the
// organisation has no scoring target. A scoring failure never stops the sync. Wired in cmd/ from the
// integrations' Rspamd connector.
type SpamScorer interface {
	Score(ctx context.Context, organizationID string, raw []byte) (*types.SpamInfo, error)
}

// spamRawCap bounds the raw fetch for scoring (Rspamd scores the first megabytes; huge attachments add nothing).
const spamRawCap = 4 << 20

// scoreSpam adds message.received.spam when the organisation scores inbound mail.
func (r *runner) scoreSpam(ctx context.Context, c *imapc.Client, msg *imapc.Message, _ *index.Message, d *types.MessageReceived) {
	if r.scorer == nil || !r.acc.SpamScoring {
		return
	}
	raw, err := c.Raw(ctx, msg.UID, spamRawCap)
	if err != nil {
		r.log.Debug("spam scoring skipped: raw fetch failed", "uid", msg.UID, "err", err)
		return
	}
	info, err := r.scorer.Score(ctx, r.acc.OrganizationID, raw)
	if err != nil {
		r.log.Warn("spam scoring failed; the message is reported without a score", "err", err)
		return
	}
	d.Spam = info
}
