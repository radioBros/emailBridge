package sync

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"time"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/events"
	"github.com/radiobros/emailbridge/internal/events/types"
	"github.com/radiobros/emailbridge/internal/evidence"
	"github.com/radiobros/emailbridge/internal/index"
	"github.com/radiobros/emailbridge/internal/mailproto/imapc"
	"github.com/radiobros/emailbridge/internal/mailproto/mime"
	"github.com/radiobros/emailbridge/internal/pec"
	"github.com/radiobros/emailbridge/internal/pecreceipts"
	"github.com/radiobros/emailbridge/internal/store"
)

// PEC configures PEC handling (P9, S17 §4): the receipts store and the signature trust anchors.
type PEC struct {
	Receipts *pecreceipts.Repo
	Anchors  *x509.CertPool // EB_PEC_TRUST_ANCHORS; nil = trust unknown
	// Evidence archives receipts and envelopes (P9.2, accounts with pec.evidenceArchive); nil = off.
	Evidence EvidenceArchiver
	// EvidencePolicy returns the organisation's TSA URL ("" = no time-stamp) and retention (0 = default).
	EvidencePolicy func(ctx context.Context, org string) (tsaURL string, retention time.Duration)
	// TSARetry queues a deferred RFC 3161 stamp after a TSA failure (jobs kind evidence.tsa-retry).
	TSARetry func(ctx context.Context, ref evidence.Ref, item evidence.Item) error
}

// EvidenceArchiver is the evidence store as sync uses it.
type EvidenceArchiver interface {
	Ready() bool
	Archive(ctx context.Context, item evidence.Item) (evidence.Ref, error)
}

// pecRawCap bounds the raw fetch of a PEC message: complete receipts carry the original with its attachments.
const pecRawCap = 64 << 20

// detectPEC handles a new message of a pec.enabled account (S17 §4): it adds message.received.pec and, for a
// receipt not stored yet, returns the receipt row (inserted by the caller inside the fenced write) and the
// pec.receipt draft. A damaged PEC message is reported with parseError, never dropped.
func (r *runner) detectPEC(ctx context.Context, c *imapc.Client, msg *imapc.Message, e *index.Message, hdr mime.Header,
	d *types.MessageReceived, seen map[string]string) (*pecreceipts.Receipt, []events.Draft, error) {
	if !r.acc.PEC || r.pec == nil {
		return nil, nil, nil
	}
	if !pec.IsCandidate(func(name string) string { return firstOr(hdr[name], "") }) {
		return nil, nil, nil
	}
	if msg.Size > pecRawCap {
		d.PEC = &types.PECInfo{Kind: string(pec.KindReceipt), ParseError: "message too large for PEC parsing"}
		return nil, nil, nil
	}
	raw, err := c.Raw(ctx, msg.UID, pecRawCap)
	if err != nil {
		r.log.Warn("PEC message not parsed: raw fetch failed", "uid", msg.UID, "err", err)
		d.PEC = &types.PECInfo{Kind: string(pec.KindReceipt), ParseError: "raw fetch failed"}
		return nil, nil, nil
	}
	m, err := pec.Parse(raw, pec.Options{Anchors: r.pec.Anchors, Now: r.now})
	if err != nil {
		return nil, nil, nil // headers looked like PEC, the message is not
	}
	info := &types.PECInfo{Kind: string(m.Kind), Type: m.Type, ReceiptKind: m.ReceiptKind, OriginalMessageID: m.RefMessageID,
		Signature: types.PECSignature{Valid: m.Signature.Valid, Trusted: m.Signature.Trusted}, ParseError: m.ParseError}
	if m.Cert != nil {
		info.TransportID, info.DeliveredTo, info.Error = m.Cert.TransportID, m.Cert.DeliveredTo, m.Cert.Error
	}
	d.PEC = info
	if !m.Signature.Valid {
		r.log.Warn("PEC message signature not valid", "uid", msg.UID, "type", m.Type, "reason", m.Signature.Error)
	}
	if m.Kind == pec.KindEnvelope {
		if _, err := r.archiveEvidence(ctx, e.ID, m, raw, msg.InternalDate); err != nil {
			return nil, nil, err
		}
	}
	if m.Kind != pec.KindReceipt {
		return nil, nil, nil
	}

	rc := r.receiptRow(m, e, raw, msg)
	key := rc.TransportID + "|" + rc.Type + "|" + rc.DeliveredTo
	if id, dup := seen[key]; dup {
		info.ReceiptID = id
		return nil, nil, nil
	}
	exists, err := r.pec.Receipts.Exists(ctx, rc.OrganizationID, rc.AccountID, rc.TransportID, rc.Type, rc.DeliveredTo)
	if err != nil || exists {
		return nil, nil, nil // stored already (re-delivered or re-synced receipt): no second pec.receipt
	}
	if id, err := r.pec.Receipts.LinkOutbound(ctx, rc.OrganizationID, rc.AccountID, rc.RefMessageIDHeader); err == nil && id != "" {
		rc.OutboundID = &id
	}
	ref, err := r.archiveEvidence(ctx, rc.ID, m, raw, rc.At)
	if err != nil {
		return nil, nil, err
	}
	if ref != nil {
		until := ref.LockedUntil
		rc.Evidence = &pecreceipts.Evidence{Key: ref.Key, LockedUntil: until, TSA: ref.TSA}
	}
	info.ReceiptID = rc.ID
	seen[key] = rc.ID
	ev := &types.PECReceipt{AccountRef: r.accRef(), ReceiptID: rc.ID, MessageID: e.ID, Type: rc.Type, ReceiptKind: rc.ReceiptKind,
		Error: rc.Error, ErrorDetail: rc.ErrorDetail, TransportID: rc.TransportID, RefMessageIDHeader: rc.RefMessageIDHeader,
		DeliveredTo: rc.DeliveredTo, Provider: rc.Provider, At: rc.At, SHA256: rc.SHA256,
		Signature: types.PECSignature{Valid: rc.Signature.Valid, Trusted: rc.Signature.Trusted}}
	if rc.OutboundID != nil {
		ev.OutboundID = *rc.OutboundID
	}
	for _, p := range rc.Recipients {
		ev.Recipients = append(ev.Recipients, types.PECRecipient{Address: p.Address, Kind: p.Kind})
	}
	return rc, []events.Draft{r.draft(events.PECReceipt, rc.ID, ev)}, nil
}

func (r *runner) receiptRow(m *pec.Message, e *index.Message, raw []byte, msg *imapc.Message) *pecreceipts.Receipt {
	sum := sha256.Sum256(raw)
	rc := &pecreceipts.Receipt{
		ID: store.NewID(store.PrefixPECReceipt), OrganizationID: r.acc.OrganizationID, AccountID: r.acc.ID,
		MessageID: e.ID, Type: m.Type, ReceiptKind: m.ReceiptKind, RefMessageIDHeader: m.RefMessageID,
		Signature: m.Signature, ParseError: m.ParseError, SHA256: hex.EncodeToString(sum[:]),
		At: msg.InternalDate.UTC(), CreatedAt: r.now().UTC(),
	}
	if c := m.Cert; c != nil {
		rc.Error, rc.ErrorDetail, rc.TransportID = c.Error, c.ErrorDetail, c.TransportID
		rc.OriginalMessageIDHeader, rc.Sender, rc.Recipients = c.OriginalMessageID, c.Sender, c.Recipients
		rc.DeliveredTo, rc.Provider = c.DeliveredTo, c.Provider
		if !c.At.IsZero() {
			rc.At = c.At
		}
	}
	if rc.TransportID == "" {
		// Without a daticert the receipt's own Message-ID keeps it unique.
		rc.TransportID = e.MessageIDHeader
	}
	return rc
}

// archiveEvidence writes a PEC message to the evidence archive (P9.2, S17 §9) for accounts with
// pec.evidenceArchive on. A store whose Object Lock check failed is skipped with a warning (health shows it) so
// sync never stalls on a configuration problem; any other storage error fails the batch, which is retried — a
// receipt is not reported before its evidence exists. A time-stamp failure keeps the evidence and queues a retry.
func (r *runner) archiveEvidence(ctx context.Context, id string, m *pec.Message, raw []byte, at time.Time) (*evidence.Ref, error) {
	if !r.acc.PECEvidence || r.pec.Evidence == nil {
		return nil, nil
	}
	if !r.pec.Evidence.Ready() {
		r.log.Warn("PEC evidence not archived: the evidence bucket failed its Object Lock check", "id", id)
		return nil, nil
	}
	item := evidence.Item{ID: id, Organization: r.acc.OrganizationID, Account: r.acc.ID, Raw: raw, ReceivedAt: at,
		Type: m.Type, Signature: map[string]any{"valid": m.Signature.Valid, "trusted": m.Signature.Trusted, "signer": m.Signature.Signer}}
	if m.Cert != nil {
		item.TransportID = m.Cert.TransportID
	}
	if r.pec.EvidencePolicy != nil {
		item.TSAURL, item.Retention = r.pec.EvidencePolicy(ctx, r.acc.OrganizationID)
	}
	ref, err := r.pec.Evidence.Archive(ctx, item)
	switch {
	case err == nil:
	case errs.CodeOf(err) == errs.CodeEvidenceTsaFailed && ref.Key != "":
		r.log.Warn("PEC evidence archived without a time-stamp; retry queued", "id", id, "err", err)
		if r.pec.TSARetry != nil {
			if qerr := r.pec.TSARetry(ctx, ref, item); qerr != nil {
				r.log.Warn("cannot queue the time-stamp retry", "id", id, "err", qerr)
			}
		}
	default:
		return nil, err
	}
	return &ref, nil
}
