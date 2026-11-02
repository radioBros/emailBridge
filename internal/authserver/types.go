package authserver

import "time"

const (
	SwitchName     = "accounts.authServer"
	defaultTTL     = 5 * time.Minute
	maxTTL         = 15 * time.Minute
	defaultTimeout = 5 * time.Second
	maxTimeout     = 30 * time.Second

	ProtocolIMAP  = "imap"
	ProtocolSMTP  = "smtp"
	ProtocolOAuth = "oauth"
)

// Request is POSTed to authServer.url.
type Request struct {
	AccountID  string  `json:"accountId"`
	ExternalID *string `json:"externalId,omitempty"`
	Protocol   string  `json:"protocol"`
}

// Response is the credential server JSON body.
type Response struct {
	User        string `json:"user,omitempty"`
	Pass        string `json:"pass,omitempty"`
	AccessToken string `json:"accessToken,omitempty"`
	Username    string `json:"username,omitempty"`
	ExpiresIn   int    `json:"expiresIn,omitempty"`
}

// Creds is the resolved credential set (never logged).
type Creds struct {
	User        string
	Pass        string
	AccessToken string
	ExpiresAt   time.Time
}
