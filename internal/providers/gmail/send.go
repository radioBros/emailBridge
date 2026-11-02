package gmail

import (
	"context"
	"strings"

	"github.com/radiobros/emailbridge/internal/errs"
)

// Sender sends mail through the Gmail API (submit hotspot wiring).
type Sender struct {
	Client *Client
	Tokens TokenSource
}

// SetTokens sets the OAuth / service-account token source (wiring hotspot).
func (s *Sender) SetTokens(ts TokenSource) {
	if s != nil {
		s.Tokens = ts
	}
}

// Send implements the Gmail send path: users.messages.send with raw RFC822.
// When AccessToken returns a username (service-account subject), the request uses
// /users/{email}/… instead of /users/me.
func (s *Sender) Send(ctx context.Context, accountID string, rawRFC822 []byte) (messageID, threadID string, err error) {
	if s == nil || s.Client == nil || s.Tokens == nil {
		return "", "", errs.New(errs.CodeInternalError, "Gmail sender is not configured.")
	}
	if len(rawRFC822) == 0 {
		return "", "", errs.New(errs.CodeValidationFailed, "Message body is empty.")
	}
	tok, username, err := s.Tokens.AccessToken(ctx, accountID)
	if err != nil {
		return "", "", err
	}
	cl := s.Client
	if u := strings.TrimSpace(username); u != "" {
		cl = cl.ForUser(u)
	}
	return cl.Send(ctx, tok, rawRFC822)
}
