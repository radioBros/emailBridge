package attachments

import (
	"context"

	ebsync "github.com/radiobros/emailbridge/internal/sync"
)

// SyncAPI adapts Service to sync.AttachmentsAPI for LazyAttachments wiring.
type SyncAPI struct{ Svc *Service }

var _ ebsync.AttachmentsAPI = SyncAPI{}

// Register implements sync.AttachmentsAPI.
func (a SyncAPI) Register(ctx context.Context, organizationID, accountID, messageID string, metas []ebsync.AttachmentMeta) ([]ebsync.AttachmentResult, error) {
	if a.Svc == nil {
		return nil, nil
	}
	in := RegisterInput{OrganizationID: organizationID, AccountID: accountID, MessageID: messageID}
	for _, m := range metas {
		in.Metas = append(in.Metas, Meta{
			PartID: m.PartID, Filename: m.Filename, ContentType: m.ContentType,
			Size: m.Size, ContentID: m.ContentID, Disposition: m.Disposition,
		})
	}
	res, err := a.Svc.RegisterMetas(ctx, in)
	return toSyncResults(res), err
}

// Ingest implements sync.AttachmentsAPI.
func (a SyncAPI) Ingest(ctx context.Context, organizationID, accountID, messageID string, raw []byte, storeS3, scan bool) ([]ebsync.AttachmentResult, error) {
	if a.Svc == nil {
		return nil, nil
	}
	res, err := a.Svc.Ingest(ctx, IngestInput{
		OrganizationID: organizationID, AccountID: accountID, MessageID: messageID,
		Raw: raw, StoreS3: storeS3, Scan: scan,
	})
	return toSyncResults(res), err
}

// EraseMessage implements sync.AttachmentsAPI.
func (a SyncAPI) EraseMessage(ctx context.Context, accountID, messageID string) error {
	if a.Svc == nil {
		return nil
	}
	_, err := a.Svc.EraseMessage(ctx, accountID, messageID)
	return err
}

func toSyncResults(in []Result) []ebsync.AttachmentResult {
	if len(in) == 0 {
		return nil
	}
	out := make([]ebsync.AttachmentResult, len(in))
	for i, r := range in {
		out[i] = ebsync.AttachmentResult{ID: r.ID, PartID: r.PartID, URL: r.URL, Scan: r.Scan}
	}
	return out
}
