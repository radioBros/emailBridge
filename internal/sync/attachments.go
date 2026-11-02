package sync

import (
	"context"
	"log/slog"

	"github.com/radiobros/emailbridge/internal/events/types"
	"github.com/radiobros/emailbridge/internal/mailproto/imapc"
)

// applyAttachmentResults merges Register/Ingest ids into the event attachment list by partId.
func applyAttachmentResults(atts []types.AttachmentInfo, results []AttachmentResult) {
	if len(atts) == 0 || len(results) == 0 {
		return
	}
	byPart := make(map[string]AttachmentResult, len(results))
	for _, r := range results {
		if r.PartID != "" {
			byPart[r.PartID] = r
		}
	}
	for i := range atts {
		if r, ok := byPart[atts[i].PartID]; ok {
			atts[i].ID = r.ID
			if r.URL != "" {
				atts[i].URL = r.URL
			}
			if r.Scan != "" {
				atts[i].Scan = r.Scan
			}
		}
	}
}

// indexMetasFromBody lists every attachment + inline-cid leaf for Register (broader than the event list).
func indexMetasFromBody(p *imapc.Part) []AttachmentMeta {
	if p == nil {
		return nil
	}
	var out []AttachmentMeta
	var walk func(p *imapc.Part)
	walk = func(p *imapc.Part) {
		if len(p.Children) > 0 {
			for _, ch := range p.Children {
				walk(ch)
			}
			return
		}
		inline := p.Disposition == "inline" && p.ContentID != ""
		if p.Disposition == "attachment" || p.Filename != "" || inline {
			out = append(out, AttachmentMeta{
				PartID: imapc.PathString(p.Path), Filename: p.Filename, ContentType: p.MediaType,
				Size: int64(p.Size), ContentID: p.ContentID, Disposition: p.Disposition,
			})
		}
	}
	walk(p)
	return out
}

// ingestAttachments registers or ingests attachment rows for one new message. Never fails the message.
// size is the message's RFC822.SIZE: a raw shorter than it was cut at the fetch cap, and extracting parts from
// a truncated message would store corrupt attachments, so such a message gets metadata rows only.
func (r *runner) ingestAttachments(ctx context.Context, messageID string, body *imapc.Part, data *types.MessageReceived, raw []byte, size int64) {
	if r.attachments == nil || data == nil {
		return
	}
	metas := indexMetasFromBody(body)
	storeS3, scan := r.acc.AttachmentsS3, r.acc.AttachmentsAV
	var results []AttachmentResult
	var err error
	switch {
	case (storeS3 || scan) && len(raw) > 0 && (size <= 0 || int64(len(raw)) >= size):
		results, err = r.attachments.Ingest(ctx, r.acc.OrganizationID, r.acc.ID, messageID, raw, storeS3, scan)
	case len(metas) > 0:
		results, err = r.attachments.Register(ctx, r.acc.OrganizationID, r.acc.ID, messageID, metas)
	default:
		return
	}
	if err != nil {
		r.log.LogAttrs(ctx, slog.LevelWarn, "sync: attachments ingest failed",
			slog.String("account_id", r.acc.ID), slog.String("message_id", messageID), slog.Any("err", err))
		return
	}
	applyAttachmentResults(data.Attachments, results)
}

// eraseAttachmentsBestEffort deletes attachment rows for purged message ids.
func (r *runner) eraseAttachmentsBestEffort(ctx context.Context, messageIDs []string) {
	if r.attachments == nil || len(messageIDs) == 0 {
		return
	}
	for _, id := range messageIDs {
		if err := r.attachments.EraseMessage(ctx, r.acc.ID, id); err != nil {
			r.log.LogAttrs(ctx, slog.LevelWarn, "sync: attachments erase failed",
				slog.String("account_id", r.acc.ID), slog.String("message_id", id), slog.Any("err", err))
		}
	}
}
