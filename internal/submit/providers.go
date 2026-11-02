package submit

import "strings"

// sentFilingHosts are the SMTP hosts of providers that file every submitted message in the Sent folder
// themselves; `copyToSent: auto` must not APPEND a second copy for them (S6 §5). A provider is added here
// and nowhere else. Plain Dovecot/Postfix servers are deliberately absent: they do not file sent mail.
var sentFilingHosts = []string{
	"smtp.gmail.com",
	"smtp-relay.gmail.com",
	"smtp.googlemail.com",
	"smtp.office365.com",
	"smtp-mail.outlook.com",
	"smtp.outlook.com",
	"smtp.office.com",
}

// FilesSentMail reports whether the provider behind an SMTP host stores sent mail by itself.
func FilesSentMail(host string) bool {
	h := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	for _, known := range sentFilingHosts {
		if h == known || strings.HasSuffix(h, "."+known) {
			return true
		}
	}
	return false
}
