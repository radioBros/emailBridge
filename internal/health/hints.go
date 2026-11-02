package health

import (
	"strings"
	"time"

	"github.com/radiobros/emailbridge/internal/accounts"
)

type hintDef struct {
	code     string
	severity Severity
	title    string
	detail   string
}

var hintCatalog = map[string]hintDef{
	HintAppPassword: {
		code: HintAppPassword, severity: SeverityCritical,
		title:  "App password required",
		detail: "Gmail, iCloud and Yahoo often block normal passwords for IMAP. Create an app-specific password in the provider account settings and use that instead.",
	},
	HintIMAPDisabled: {
		code: HintIMAPDisabled, severity: SeverityCritical,
		title:  "IMAP disabled at provider",
		detail: "The provider rejected IMAP access. Enable IMAP (or “less secure apps” / mail client access) in the mailbox settings, then reconnect.",
	},
	HintWrongHostPortTLS: {
		code: HintWrongHostPortTLS, severity: SeverityWarning,
		title:  "Wrong host, port or TLS mode",
		detail: "Check IMAP host, port and TLS (implicit on 993, STARTTLS on 143). Autoconfig can suggest the right values for this domain.",
	},
	HintCertificate: {
		code: HintCertificate, severity: SeverityWarning,
		title:  "TLS certificate problem",
		detail: "The mail server certificate failed verification. Fix the certificate on the server, or only as a last resort enable ignore-certificate-errors for this account.",
	},
	HintOAuthRevoked: {
		code: HintOAuthRevoked, severity: SeverityCritical,
		title:  "OAuth consent revoked",
		detail: "The refresh token is no longer valid. Reconnect the account and grant consent again.",
	},
	HintRateLimited: {
		code: HintRateLimited, severity: SeverityWarning,
		title:  "Rate limited / too many connections",
		detail: "The provider limited connections. Wait a few minutes, reduce concurrent sessions, then retry.",
	},
	HintMailboxFull: {
		code: HintMailboxFull, severity: SeverityCritical,
		title:  "Mailbox full",
		detail: "The mailbox is over quota. Free space at the provider so sync and delivery can continue.",
	},
	HintAuthFailed: {
		code: HintAuthFailed, severity: SeverityCritical,
		title:  "Authentication failed",
		detail: "Username or password was rejected. Update credentials or reconnect with OAuth.",
	},
	HintConnectFailed: {
		code: HintConnectFailed, severity: SeverityWarning,
		title:  "Connection failed",
		detail: "Could not reach the mail server. Check network, firewall and that the host is reachable.",
	},
	HintCapacity: {
		code: HintCapacity, severity: SeverityInfo,
		title:  "Cluster capacity",
		detail: "No sync node has room for this account right now. It will be placed when capacity frees up.",
	},
	HintIgnoreCertErrors: {
		code: HintIgnoreCertErrors, severity: SeverityWarning,
		title:  "TLS certificate verification disabled",
		detail: "This account accepts invalid TLS certificates. Turn off ignore-certificate-errors after fixing the server certificate.",
	},
}

func makeHint(code string) Hint {
	d := hintCatalog[code]
	return Hint{Code: d.code, Severity: d.severity, Title: d.title, Detail: d.detail}
}

// providerFamily guesses gmail/icloud/yahoo/outlook from email domain or IMAP host.
func providerFamily(email, host, provider string) string {
	p := strings.ToLower(strings.TrimSpace(provider))
	if p == "gmail" || p == "graph" {
		return p
	}
	h := strings.ToLower(host)
	dom := domainOf(email)
	switch {
	case strings.Contains(h, "gmail") || strings.Contains(h, "google") ||
		dom == "gmail.com" || dom == "googlemail.com":
		return "gmail"
	case strings.Contains(h, "mail.me.com") || strings.Contains(h, "icloud") ||
		dom == "icloud.com" || dom == "me.com" || dom == "mac.com":
		return "icloud"
	case strings.Contains(h, "yahoo") || dom == "yahoo.com" || dom == "ymail.com":
		return "yahoo"
	case strings.Contains(h, "outlook") || strings.Contains(h, "office365") ||
		strings.Contains(h, "hotmail") || strings.HasSuffix(dom, "onmicrosoft.com"):
		return "outlook"
	default:
		if p != "" {
			return p
		}
		return "imap"
	}
}

func domainOf(email string) string {
	i := strings.LastIndexByte(email, '@')
	if i < 0 || i+1 >= len(email) {
		return ""
	}
	return strings.ToLower(email[i+1:])
}

func textBlob(a *accounts.Account, liveMsg string) string {
	var b strings.Builder
	if a.StateReason != nil {
		b.WriteString(a.StateReason.Code)
		b.WriteByte(' ')
		b.WriteString(a.StateReason.Message)
		b.WriteByte(' ')
		b.WriteString(a.StateReason.Kind)
	}
	for _, e := range a.RecentErrors {
		b.WriteByte(' ')
		b.WriteString(e.Code)
		b.WriteByte(' ')
		b.WriteString(e.Kind)
		b.WriteByte(' ')
		b.WriteString(e.Response)
		b.WriteByte(' ')
		b.WriteString(e.Hint)
	}
	if liveMsg != "" {
		b.WriteByte(' ')
		b.WriteString(liveMsg)
	}
	return strings.ToLower(b.String())
}

func codesBlob(a *accounts.Account, liveCode string) string {
	parts := make([]string, 0, 4+len(a.RecentErrors))
	if a.StateReason != nil {
		parts = append(parts, a.StateReason.Code, a.StateReason.Kind)
	}
	for _, e := range a.RecentErrors {
		parts = append(parts, e.Code, e.Kind)
	}
	if liveCode != "" {
		parts = append(parts, liveCode)
	}
	return strings.ToLower(strings.Join(parts, " "))
}

func looksAppPasswordProvider(family string) bool {
	return family == "gmail" || family == "icloud" || family == "yahoo"
}

func collectHints(a *accounts.Account, liveCode, liveMsg string) []Hint {
	blob := textBlob(a, liveMsg)
	codes := codesBlob(a, liveCode)
	family := providerFamily(a.Email, a.IMAP.Host, a.Provider)
	seen := map[string]struct{}{}
	var out []Hint
	add := func(code string) {
		if _, ok := seen[code]; ok {
			return
		}
		if _, ok := hintCatalog[code]; !ok {
			return
		}
		seen[code] = struct{}{}
		out = append(out, makeHint(code))
	}

	if strings.Contains(codes, "oauth_revoked") || strings.Contains(blob, "invalid_grant") ||
		strings.Contains(blob, "consent") && strings.Contains(blob, "revok") {
		add(HintOAuthRevoked)
	}
	if strings.Contains(blob, "mailbox full") || strings.Contains(blob, "over quota") ||
		strings.Contains(blob, "quota exceeded") || strings.Contains(codes, "mailbox_full") {
		add(HintMailboxFull)
	}
	if strings.Contains(blob, "too many connections") || strings.Contains(blob, "[limit]") ||
		strings.Contains(codes, "rate_limited") || strings.Contains(blob, "rate limit") {
		add(HintRateLimited)
	}
	if strings.Contains(codes, "mail_tls_failed") || strings.Contains(blob, "certificate") ||
		strings.Contains(blob, "x509") || strings.Contains(blob, "tls:") {
		add(HintCertificate)
	}
	if strings.Contains(blob, "imap access is disabled") || strings.Contains(blob, "web login required") ||
		strings.Contains(blob, "imap is not enabled") || strings.Contains(blob, "please log in via your web") {
		add(HintIMAPDisabled)
	}
	authFail := a.State == accounts.StateAuthError ||
		liveCode == "mail_auth_failed" ||
		strings.Contains(codes, "mail_auth_failed") ||
		strings.Contains(codes, "authenticationfailed")
	if authFail {
		if looksAppPasswordProvider(family) && a.OAuth == nil {
			add(HintAppPassword)
		} else if !hasHint(seen, HintOAuthRevoked) {
			add(HintAuthFailed)
		}
	}
	if a.StateReason != nil && a.StateReason.Code == accounts.ReasonCodeCapacity {
		add(HintCapacity)
	}
	if wrongHostPortTLS(a, codes, blob) {
		add(HintWrongHostPortTLS)
	}
	if (a.State == accounts.StateConnectError || strings.Contains(codes, "mail_connect_failed") ||
		liveCode == "mail_connect_failed") && !hasHint(seen, HintWrongHostPortTLS) &&
		!hasHint(seen, HintCertificate) && !hasHint(seen, HintRateLimited) {
		add(HintConnectFailed)
	}
	if a.Switches != nil {
		if v, ok := a.Switches["accounts.ignoreCertErrors"].(bool); ok && v {
			add(HintIgnoreCertErrors)
		}
	}

	// Sort: severity desc, code asc.
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j].Severity.Rank() > out[i].Severity.Rank() ||
				(out[j].Severity.Rank() == out[i].Severity.Rank() && out[j].Code < out[i].Code) {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

func hasHint(seen map[string]struct{}, code string) bool {
	_, ok := seen[code]
	return ok
}

func wrongHostPortTLS(a *accounts.Account, codes, blob string) bool {
	tls := strings.ToLower(a.IMAP.TLS)
	port := a.IMAP.Port
	if (port == 993 && tls == "none") || (port == 143 && tls == "implicit") ||
		(port == 465 && tls == "starttls") {
		return true
	}
	family := providerFamily(a.Email, a.IMAP.Host, a.Provider)
	host := strings.ToLower(a.IMAP.Host)
	if family == "gmail" && host != "" && !strings.Contains(host, "gmail") && !strings.Contains(host, "google") {
		if a.State == accounts.StateConnectError || a.State == accounts.StateAuthError ||
			strings.Contains(codes, "mail_connect_failed") {
			return true
		}
	}
	if strings.Contains(codes, "mail_capability_missing") {
		return true
	}
	_ = blob
	return false
}

func severityFromState(state string, reason *accounts.StateReason) Severity {
	switch state {
	case accounts.StateAuthError:
		return SeverityCritical
	case accounts.StateConnectError:
		if reason != nil && (reason.Code == "mail_tls_failed" || reason.Kind == "auth" || reason.Kind == "config") {
			return SeverityCritical
		}
		return SeverityWarning
	case accounts.StateDisconnected:
		if reason != nil && reason.Code == accounts.ReasonCodeCapacity {
			return SeverityInfo
		}
		return SeverityWarning
	case accounts.StateDisabled, accounts.StateDeleting:
		return SeverityInfo
	case accounts.StateConnecting, accounts.StateSyncing:
		return SeverityInfo
	case accounts.StateConnected, accounts.StateNew:
		return SeverityOK
	default:
		return SeverityInfo
	}
}

func severityFromHints(hints []Hint) Severity {
	s := SeverityOK
	for _, h := range hints {
		s = MaxSeverity(s, h.Severity)
	}
	return s
}

func staleSync(a *accounts.Account, now time.Time) bool {
	if a.State != accounts.StateConnected || a.LastSyncAt == nil {
		return false
	}
	return now.Sub(*a.LastSyncAt) > 24*time.Hour
}

func summarize(sev Severity, state string, hints []Hint, stale bool) string {
	if len(hints) > 0 {
		return hints[0].Title
	}
	if stale {
		return "Last sync is older than 24 hours"
	}
	switch sev {
	case SeverityOK:
		if state == accounts.StateNew {
			return "Not connected yet"
		}
		return "Healthy"
	case SeverityInfo:
		switch state {
		case accounts.StateConnecting:
			return "Connecting"
		case accounts.StateSyncing:
			return "Syncing"
		case accounts.StateDisabled:
			return "Disabled"
		case accounts.StateDeleting:
			return "Deleting"
		default:
			return "Needs attention"
		}
	case SeverityWarning:
		return "Connection issues"
	default:
		return "Critical account issue"
	}
}
