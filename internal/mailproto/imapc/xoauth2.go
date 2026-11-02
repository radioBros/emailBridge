package imapc

import (
	"github.com/emersion/go-sasl"
)

// XOAuth2 is the SASL mechanism name used by Gmail (and others) for OAuth2.
const XOAuth2 = "XOAUTH2"

// NewXOAuth2Client builds a SASL client for AUTHENTICATE XOAUTH2.
// Initial response: user=<user>\x01auth=Bearer <token>\x01\x01
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
	// Server may send a JSON error challenge; cancel the exchange.
	if a.step > 0 {
		return nil, sasl.ErrUnexpectedClientResponse
	}
	a.step++
	return []byte{0x01}, nil
}
