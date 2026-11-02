package sync

import (
	"context"
	"strings"

	"github.com/radiobros/emailbridge/internal/bounce"
	"github.com/radiobros/emailbridge/internal/events"
	"github.com/radiobros/emailbridge/internal/events/types"
	"github.com/radiobros/emailbridge/internal/index"
	"github.com/radiobros/emailbridge/internal/mailproto/imapc"
	"github.com/radiobros/emailbridge/internal/mailproto/mime"
)

// OutboundLookup finds the outbound message (out_…) a bounce or complaint refers to, by the outbound ID the
// classifier extracted or by the original Message-ID; "" when there is none. Wired from the submit
// repository in cmd/ so the sync engine does not read another domain's collection.
type OutboundLookup func(ctx context.Context, organizationID, outboundID, messageIDHeader string) (string, error)

// Suppressor records hard bounces and complaints on the organisation's system suppression lists (P5.2/P5.4).
// Calls are idempotent upserts, so a retried batch records nothing twice. Wired in cmd/.
type Suppressor interface {
	AddBounce(ctx context.Context, org, address, reason string) error
	AddComplaint(ctx context.Context, org, address, reason string) error
}

// bounceHeaders are fetched with every new message when inbound.bounceDetection is on: they let
// likelyMachineGenerated pick the few messages worth a raw fetch.
var bounceHeaders = []string{"Return-Path", "Content-Type", "Auto-Submitted", "X-Autoreply", "X-Autorespond",
	"Precedence", "X-Auto-Response-Suppress", "X-Failed-Recipients"}

// bounceRawCap bounds the raw fetch of a candidate (a DSN with the full original attached can be large; the
// report and headers come first).
const bounceRawCap = 2 << 20

// diagnosticMax caps a remote diagnostic in events.
const diagnosticMax = 512

// likelyMachineGenerated is the cheap header pre-filter in front of the classifier: a null reverse-path
// (RFC 5321 DSNs), a multipart/report (DSN, ARF), RFC 3834 auto-reply markers, an X-Failed-Recipients
// header, or a mailer-daemon / postmaster sender. Anything else is ordinary mail and costs nothing more.
func likelyMachineGenerated(hdr mime.Header, env *imapc.Envelope) bool {
	if rp := strings.TrimSpace(firstOr(hdr["return-path"], "")); rp == "<>" {
		return true
	}
	if strings.HasPrefix(strings.ToLower(firstOr(hdr["content-type"], "")), "multipart/report") {
		return true
	}
	if as := strings.ToLower(strings.TrimSpace(firstOr(hdr["auto-submitted"], ""))); as != "" && as != "no" {
		return true
	}
	if len(hdr["x-autoreply"]) > 0 || len(hdr["x-autorespond"]) > 0 || len(hdr["x-failed-recipients"]) > 0 {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(firstOr(hdr["precedence"], ""))) {
	case "auto_reply", "auto-reply":
		return true
	}
	if env != nil {
		for _, a := range env.From {
			local, _, _ := strings.Cut(strings.ToLower(a.Address), "@")
			switch local {
			case "mailer-daemon", "postmaster", "mail-delivery-subsystem", "mailerdaemon":
				return true
			}
		}
	}
	return false
}

// classifyBounce runs the classifier on a candidate new message, enriches message.received and returns the
// extra message.bounced / message.complained drafts. Classification failures never stop the sync.
func (r *runner) classifyBounce(ctx context.Context, c *imapc.Client, msg *imapc.Message, e *index.Message, hdr mime.Header, d *types.MessageReceived) []events.Draft {
	if !r.acc.BounceDetection || !likelyMachineGenerated(hdr, msg.Envelope) {
		return nil
	}
	raw, err := c.Raw(ctx, msg.UID, bounceRawCap)
	if err != nil {
		r.log.Debug("bounce classification skipped: raw fetch failed", "uid", msg.UID, "err", err)
		return nil
	}
	res, err := bounce.Classify(raw)
	if err != nil || res == nil {
		return nil
	}
	switch res.Kind {
	case bounce.KindAutoReply:
		d.IsAutoReply = true
		return nil
	case bounce.KindChallengeResponse:
		d.IsAutoReply = true
		d.Bounce = &types.BounceInfo{Kind: string(res.Kind)}
		return nil
	case bounce.KindDSNBounce, bounce.KindHeuristicBounce, bounce.KindComplaint:
	default:
		return nil
	}
	outboundID := res.OutboundID
	if r.outbound != nil {
		if id, err := r.outbound(ctx, r.acc.OrganizationID, res.OutboundID, res.OriginalMessageID); err == nil {
			outboundID = id
		}
	}
	info := &types.BounceInfo{Kind: string(res.Kind), BounceType: string(res.BounceType), OutboundID: outboundID,
		OriginalMessageID: res.OriginalMessageID}
	for _, rc := range res.Recipients {
		info.Recipients = append(info.Recipients, types.BounceRecipient{Address: rc.Address, Action: rc.Action,
			Status: rc.Status, Diagnostic: truncate(rc.Diagnostic, diagnosticMax), BounceType: string(rc.BounceType)})
	}
	d.Bounce = info

	var drafts []events.Draft
	if res.Kind == bounce.KindComplaint {
		recipient := ""
		if len(res.FinalRecipients) > 0 {
			recipient = res.FinalRecipients[0]
		}
		drafts = append(drafts, r.draft(events.MessageComplained, e.ID, &types.MessageComplained{
			AccountRef: r.accRef(), MessageID: e.ID, OutboundID: outboundID, MessageIDHeader: res.OriginalMessageID,
			Recipient: recipient, FeedbackType: res.FeedbackType}))
		if r.suppress != nil && recipient != "" {
			if err := r.suppress.AddComplaint(ctx, r.acc.OrganizationID, recipient, "complaint: "+res.FeedbackType); err != nil {
				r.log.Warn("cannot record the complaint on the suppression list", "err", err)
			}
		}
		return drafts
	}
	for _, rc := range info.Recipients {
		bt := rc.BounceType
		if bt == "" {
			bt = string(res.BounceType)
		}
		if bt == "" {
			bt = string(bounce.BounceSoft) // unknown severity: never suppress on a guess
		}
		action := rc.Action
		if action == "" {
			action = "failed"
		}
		drafts = append(drafts, r.draft(events.MessageBounced, e.ID, &types.MessageBounced{
			AccountRef: r.accRef(), MessageID: e.ID, OutboundID: outboundID, MessageIDHeader: res.OriginalMessageID,
			Recipient: rc.Address, Action: action, Status: rc.Status, Diagnostic: rc.Diagnostic, BounceType: bt, MTA: res.MTA}))
		// Only structured (RFC 3464) hard bounces suppress automatically: a soft bounce may succeed later, and a
		// heuristic bounce is a guess from free text (an independent corpus showed human mail about failed
		// deliveries classified as hard bounces) — it is reported, never acted on by itself.
		if bt == string(bounce.BounceHard) && res.Kind == bounce.KindDSNBounce && r.suppress != nil && rc.Address != "" {
			if err := r.suppress.AddBounce(ctx, r.acc.OrganizationID, rc.Address, strings.TrimSpace(rc.Status+" "+rc.Diagnostic)); err != nil {
				r.log.Warn("cannot record the hard bounce on the suppression list", "err", err)
			}
		}
	}
	return drafts
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !isRuneStart(s[n]) {
		n--
	}
	return s[:n]
}

func isRuneStart(b byte) bool { return b&0xC0 != 0x80 }
