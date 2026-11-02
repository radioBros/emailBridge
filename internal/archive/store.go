// Package archive stores and serves raw inbound message sources (S1 §7, S5 §3 on-demand IMAP,
// S11 §4 retention). It implements sync.Archiver for the sync engine.
package archive

import (
	"bytes"
	"context"
	"errors"
	"io"
	"time"

	"github.com/radiobros/emailbridge/internal/objstore"
)

// Store writes and reads archived RFC 822 messages in S3.
type Store struct {
	Objects *objstore.Store
}

// PutRaw implements sync.Archiver: key archive/<org>/<account>/<yyyy>/<mm>/<msgId>.eml.
func (s *Store) PutRaw(ctx context.Context, organizationID, accountID, messageID string, internalDate time.Time, raw []byte) (string, error) {
	key, err := objstore.ArchiveKey(organizationID, accountID, messageID, internalDate)
	if err != nil {
		return "", err
	}
	err = s.Objects.Put(ctx, key, bytes.NewReader(raw), int64(len(raw)), objstore.ObjectMeta{
		Organization: organizationID, Account: accountID, ContentType: "message/rfc822",
	})
	if err != nil {
		return "", err
	}
	return key, nil
}

// Open returns a reader for an archive object. The caller must Close it.
func (s *Store) Open(ctx context.Context, key string) (io.ReadCloser, objstore.ObjectInfo, error) {
	return s.Objects.Get(ctx, key)
}

// Delete removes one archive object (idempotent when missing).
func (s *Store) Delete(ctx context.Context, key string) error {
	err := s.Objects.Delete(ctx, key)
	if err != nil && !errors.Is(err, objstore.ErrNotFound) {
		return err
	}
	return nil
}
