// Package autoconfig discovers IMAP/SMTP settings for an email address (P6.4).
package autoconfig

import "time"

// Sources for Result.Source.
const (
	SourceBuiltin      = "builtin"
	SourceISPDB        = "ispdb"
	SourceAutodiscover = "autodiscover"
	SourceSRV          = "srv"
	SourceMX           = "mx"
	SourceNone         = "none"
)

// OAuth hints.
const (
	HintGmail   = "gmail"
	HintOutlook = "outlook"
)

// Server is one protocol endpoint.
type Server struct {
	Host string `json:"host" doc:"Server host name" example:"imap.example.com"`
	Port int    `json:"port" doc:"TCP port" example:"993"`
	TLS  string `json:"tls" doc:"implicit (TLS from the first byte), starttls or none" example:"implicit"` // implicit | starttls | none
	User string `json:"user,omitempty" doc:"Login name to use; %EMAIL% stands for the full address, %EMAILLOCALPART% for the part before @" example:"%EMAIL%"`
}

// Result is GET /v1/autoconfig.
type Result struct {
	Email     string  `json:"email" doc:"The address that was looked up (normalised to lower case)" example:"maria.rossi@example.com"`
	Domain    string  `json:"domain" doc:"Domain of the address" example:"example.com"`
	Source    string  `json:"source" doc:"Where the settings came from: builtin (provider list), ispdb (Mozilla ISPDB), autodiscover, srv (RFC 6186 SRV records), mx (guessed from the MX host) or none (nothing found)" example:"ispdb"`
	IMAP      *Server `json:"imap,omitempty" doc:"Incoming server; omitted when none was found"`
	SMTP      *Server `json:"smtp,omitempty" doc:"Outgoing server; omitted when none was found"`
	OAuthHint string  `json:"oauthHint,omitempty" doc:"The provider prefers OAuth: gmail or outlook; omitted otherwise" example:"outlook"`
	// PEC is set for a known PEC provider (S17 §10); PECDefaults are the settings to suggest.
	PEC         bool         `json:"pec,omitempty" doc:"The domain belongs to a known Italian PEC provider" example:"false"`
	PECDefaults *PECDefaults `json:"pecDefaults,omitempty" doc:"Account settings to suggest for a PEC mailbox; only with pec"`
}

// complete reports whether both protocols are present.
func (r *Result) complete() bool {
	return r != nil && r.IMAP != nil && r.SMTP != nil && r.IMAP.Host != "" && r.SMTP.Host != ""
}

const (
	tlsImplicit = "implicit"
	tlsStartTLS = "starttls"
	tlsNone     = "none"
)

const (
	userEmail = "%EMAIL%"
	userLocal = "%EMAILLOCALPART%"
	cacheTTL  = time.Hour
	httpTO    = 8 * time.Second
	dnsTO     = 5 * time.Second
)
