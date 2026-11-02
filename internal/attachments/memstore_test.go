package attachments

import (
	"context"
	"sync"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/store"
)

// memDocs is an in-memory docStore for unit tests.
type memDocs struct {
	mu   sync.Mutex
	byID map[string]*Doc
}

func newMemDocs() *memDocs {
	return &memDocs{byID: map[string]*Doc{}}
}

func (m *memDocs) get(_ context.Context, accountID, messageID, attID string) (*Doc, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.byID[attID]
	if !ok || d.AccountID != accountID || d.MessageID != messageID {
		return nil, errs.New(errs.CodeAttachmentNotFound, "The attachment does not exist.")
	}
	cp := *d
	return &cp, nil
}

func (m *memDocs) getByID(_ context.Context, orgID, attID string) (*Doc, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.byID[attID]
	if !ok || d.OrganizationID != orgID {
		return nil, errs.New(errs.CodeAttachmentNotFound, "The attachment does not exist.")
	}
	cp := *d
	return &cp, nil
}

func (m *memDocs) findByPart(_ context.Context, accountID, messageID, partID string) (*Doc, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, d := range m.byID {
		if d.AccountID == accountID && d.MessageID == messageID && d.PartID == partID {
			cp := *d
			return &cp, nil
		}
	}
	return nil, nil
}

func (m *memDocs) upsert(_ context.Context, d *Doc) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := *d
	m.byID[d.ID] = &cp
	return nil
}

func (m *memDocs) listByMessage(_ context.Context, accountID, messageID string) ([]*Doc, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []*Doc
	for _, d := range m.byID {
		if d.AccountID == accountID && d.MessageID == messageID {
			cp := *d
			out = append(out, &cp)
		}
	}
	return out, nil
}

func (m *memDocs) deleteMessage(_ context.Context, accountID, messageID string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var n int64
	for id, d := range m.byID {
		if d.AccountID == accountID && d.MessageID == messageID {
			delete(m.byID, id)
			n++
		}
	}
	return n, nil
}

func (m *memDocs) ensure(context.Context) error { return nil }

var _ docStore = (*memDocs)(nil)
var _ = store.PrefixAttachment
