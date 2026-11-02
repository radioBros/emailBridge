package connect

import (
	"strings"
	"time"

	"github.com/radiobros/emailbridge/internal/errs"
)

// Observed is the identity seen after a successful authentication.
type Observed struct {
	Email        string
	IMAPUser     string
	OAuthSubject string
}

// Pin builds an Identity from an observed auth.
func Pin(o Observed, at time.Time) Identity {
	return Identity{
		Email:        strings.ToLower(strings.TrimSpace(o.Email)),
		IMAPUser:     strings.ToLower(strings.TrimSpace(o.IMAPUser)),
		OAuthSubject: strings.TrimSpace(o.OAuthSubject),
		PinnedAt:     at.UTC().Truncate(time.Millisecond),
	}
}

// Match reports whether observed matches the pin.
func Match(pin *Identity, o Observed) bool {
	if pin == nil || pin.Email == "" {
		return true
	}
	email := strings.ToLower(strings.TrimSpace(o.Email))
	if email != pin.Email {
		return false
	}
	imap := strings.ToLower(strings.TrimSpace(o.IMAPUser))
	if pin.IMAPUser != "" {
		return imap == pin.IMAPUser
	}
	if pin.OAuthSubject != "" {
		return strings.TrimSpace(o.OAuthSubject) == pin.OAuthSubject
	}
	return true
}

// CheckIdentity returns 409 identity_mismatch when pin exists and observed differs.
func CheckIdentity(pin *Identity, o Observed) error {
	if Match(pin, o) {
		return nil
	}
	return errs.New(errs.CodeIdentityMismatch,
		"The credentials authenticate as a different mailbox identity. Re-pin the identity to continue.")
}

// GuardCredentialChange refuses a change unless rePin is true or the new identity matches.
func GuardCredentialChange(pin *Identity, o Observed, rePin bool) (*Identity, error) {
	if pin == nil || pin.Email == "" {
		p := Pin(o, time.Now())
		return &p, nil
	}
	if rePin {
		p := Pin(o, time.Now())
		return &p, nil
	}
	if err := CheckIdentity(pin, o); err != nil {
		return nil, err
	}
	return pin, nil
}
