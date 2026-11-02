package smtpc

import (
	"github.com/emersion/go-sasl"

	"github.com/radiobros/emailbridge/internal/errs"
)

// XOAuth2 is the SASL mechanism name used by Gmail (and others) for OAuth2.
const XOAuth2 = "XOAUTH2"

// NewXOAuth2Client builds a SASL client for AUTH XOAUTH2.
func NewXOAuth2Client(username, accessToken string) sasl.Client {
	return &xoauth2Client{username: username, token: accessToken}
}

type xoauth2Client struct {
	username, token string
	step            int
}

func (a *xoauth2Client) Start() (mech string, ir []byte, err error) {
	ir = []byte("user=" + a.username + "\x01auth=Bearer " + a.token + "\x01\x01")
	return XOAuth2, ir, nil
}

func (a *xoauth2Client) Next(challenge []byte) ([]byte, error) {
	if a.step > 0 {
		return nil, sasl.ErrUnexpectedClientResponse
	}
	a.step++
	return []byte{0x01}, nil
}

func smtpAuthClient(s *Session, o SessionOptions) (sasl.Client, error) {
	if o.AccessToken != "" {
		switch {
		case s.c.SupportsAuth(XOAuth2):
			return NewXOAuth2Client(o.Username, o.AccessToken), nil
		case s.c.SupportsAuth(sasl.OAuthBearer):
			return sasl.NewOAuthBearerClient(&sasl.OAuthBearerOptions{
				Username: o.Username, Token: o.AccessToken,
			}), nil
		default:
			return nil, errs.New(errs.CodeSMTPAuthFailed, "The server offers no supported OAuth authentication mechanism (XOAUTH2 or OAUTHBEARER).")
		}
	}
	switch {
	case s.c.SupportsAuth("PLAIN"):
		return sasl.NewPlainClient("", o.Username, o.Password), nil
	case s.c.SupportsAuth("LOGIN"):
		return sasl.NewLoginClient(o.Username, o.Password), nil
	default:
		return nil, errs.New(errs.CodeSMTPAuthFailed, "The server offers no supported authentication mechanism (PLAIN or LOGIN).")
	}
}
