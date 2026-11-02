package graph

import (
	"context"
	"strings"

	"github.com/radiobros/emailbridge/internal/errs"
)

// Sender sends mail through the Graph API (submit hotspot wiring).
type Sender struct {
	Client *Client
	Tokens TokenSource
}

// SetTokens sets the OAuth / app-only token source (wiring hotspot).
func (s *Sender) SetTokens(ts TokenSource) {
	if s != nil {
		s.Tokens = ts
	}
}

// Send implements the Graph send path: POST …/users/{upn}/sendMail with raw RFC822.
func (s *Sender) Send(ctx context.Context, accountID string, rawRFC822 []byte) (messageID, conversationID string, err error) {
	if s == nil || s.Client == nil || s.Tokens == nil {
		return "", "", errs.New(errs.CodeInternalError, "Graph sender is not configured.")
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
