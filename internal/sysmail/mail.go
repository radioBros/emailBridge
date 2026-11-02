// Package sysmail renders and delivers EmailBridge system mail (S10 §7): MFA codes, invitations,
// password resets, email verification and alerts. Jobs are published to EMAILBRIDGE_SYSMAIL and sent
// by a worker over SMTP or through a connected account (EB_SYSMAIL_MODE=account).
package sysmail

// Mail kinds (S10 §2.4 / §7).
const (
	KindMFACode           = "mfa_code"
	KindInvitation        = "invitation"
	KindPasswordReset     = "password_reset"
	KindEmailVerification = "email_verification"
	KindAlert             = "alert"
)

// Modes (EB_SYSMAIL_MODE).
const (
	ModeOff     = "off"
	ModeSMTP    = "smtp"
	ModeAccount = "account"
)

// Mail is one system-mail job.
type Mail struct {
	Kind   string            `json:"kind"`
	To     string            `json:"to"`
	Locale string            `json:"locale"`
	Data   map[string]string `json:"data,omitempty"`
}

// job is the JSON payload on eb.sysmail.
type job struct {
	ID     string            `json:"id"`
	Kind   string            `json:"kind"`
	To     string            `json:"to"`
	Locale string            `json:"locale"`
	Data   map[string]string `json:"data,omitempty"`
}
