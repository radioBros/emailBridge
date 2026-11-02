package sync

import (
	"context"
)

// AttachmentsAPI is the subset of attachments.Service used by LazyAttachments (avoids sync→attachments import).
type AttachmentsAPI interface {
	Register(ctx context.Context, organizationID, accountID, messageID string, metas []AttachmentMeta) ([]AttachmentResult, error)
	Ingest(ctx context.Context, organizationID, accountID, messageID string, raw []byte, storeS3, scan bool) ([]AttachmentResult, error)
	EraseMessage(ctx context.Context, accountID, messageID string) error
}

// LazyAttachments resolves the attachments service at call time (module starts after sync).
type LazyAttachments struct {
	Get func() AttachmentsAPI
}

var _ AttachmentIngest = LazyAttachments{}

func (a LazyAttachments) api() AttachmentsAPI {
	if a.Get == nil {
		return nil
	}
	return a.Get()
}

// Register implements AttachmentIngest.
func (a LazyAttachments) Register(ctx context.Context, organizationID, accountID, messageID string, metas []AttachmentMeta) ([]AttachmentResult, error) {
	s := a.api()
	if s == nil {
		return nil, nil
	}
	return s.Register(ctx, organizationID, accountID, messageID, metas)
}

// Ingest implements AttachmentIngest.
func (a LazyAttachments) Ingest(ctx context.Context, organizationID, accountID, messageID string, raw []byte, storeS3, scan bool) ([]AttachmentResult, error) {
	s := a.api()
	if s == nil {
		return nil, nil
	}
	return s.Ingest(ctx, organizationID, accountID, messageID, raw, storeS3, scan)
}

// EraseMessage implements AttachmentIngest.
func (a LazyAttachments) EraseMessage(ctx context.Context, accountID, messageID string) error {
	s := a.api()
	if s == nil {
		return nil
	}
	return s.EraseMessage(ctx, accountID, messageID)
}
