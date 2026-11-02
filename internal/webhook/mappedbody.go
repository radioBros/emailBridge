package webhook

import (
	"context"

	"github.com/radiobros/emailbridge/internal/crypto"
	"github.com/radiobros/emailbridge/internal/errs"
)

// mappedBodyField is the AAD field name of Delivery.MappedBodyEnc.
const mappedBodyField = "mappedBody"

func mappedBodyAAD(deliveryID string) crypto.AAD {
	return crypto.AAD{Collection: DeliveriesCollection, Field: mappedBodyField, DocID: deliveryID}
}

// sealMappedBody encrypts a rule-mapped body into d (bound to d.ID). The plaintext field is cleared: mapped
// bodies can contain settings.env secrets (e.g. a receiver's API key) and are never stored in the clear.
func (s *Service) sealMappedBody(ctx context.Context, d *Delivery, plain string) error {
	d.MappedBody = ""
	d.MappedBodyEnc = crypto.EncryptedString{}
	if plain == "" {
		return nil
	}
	if s.Cipher == nil {
		return errs.New(errs.CodeInternalError, "webhook: cipher not configured")
	}
	enc, err := s.Cipher.EncryptString(ctx, d.OrganizationID, plain, mappedBodyAAD(d.ID))
	if err != nil {
		return errs.Wrap(err, errs.CodeInternalError, "webhook.mappedBody.encrypt")
	}
	d.MappedBodyEnc = enc
	return nil
}

// openMappedBody returns the mapped body of d, decrypting it, or the legacy plaintext of rows written before
// encryption (migration-free read). ok is false when the delivery has no mapped body.
func (s *Service) openMappedBody(ctx context.Context, d *Delivery) (body string, ok bool, err error) {
	if !d.MappedBodyEnc.IsZero() {
		if s.Cipher == nil {
			return "", false, errs.New(errs.CodeInternalError, "webhook: cipher not configured")
		}
		plain, err := s.Cipher.DecryptString(ctx, d.MappedBodyEnc, mappedBodyAAD(d.ID))
		if err != nil {
			return "", false, errs.Wrap(err, errs.CodeInternalError, "webhook.mappedBody.decrypt")
		}
		return plain, true, nil
	}
	if d.MappedBody != "" {
		return d.MappedBody, true, nil
	}
	return "", false, nil
}
