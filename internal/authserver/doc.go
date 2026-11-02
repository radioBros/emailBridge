// Package authserver fetches IMAP/SMTP/OAuth credentials from an organisation's external
// credential server at connect time (W54 G23). Secrets are cached in memory only and never
// persisted or logged.
package authserver
