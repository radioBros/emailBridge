package autoconfig

import "strings"

type provider struct {
	domains   []string
	imap      Server
	smtp      Server
	oauthHint string
	pec       bool // a PEC (certified mail) provider: S17 §10 presets apply
}

// builtinProviders is the static table (normal email + PEC hosts as data only).
var builtinProviders = []provider{
	{
		domains:   []string{"gmail.com", "googlemail.com"},
		imap:      Server{Host: "imap.gmail.com", Port: 993, TLS: tlsImplicit, User: userEmail},
		smtp:      Server{Host: "smtp.gmail.com", Port: 465, TLS: tlsImplicit, User: userEmail},
		oauthHint: HintGmail,
	},
	{
		domains:   []string{"outlook.com", "hotmail.com", "live.com", "office365.com", "msn.com"},
		imap:      Server{Host: "outlook.office365.com", Port: 993, TLS: tlsImplicit, User: userEmail},
		smtp:      Server{Host: "smtp.office365.com", Port: 587, TLS: tlsStartTLS, User: userEmail},
		oauthHint: HintOutlook,
	},
	{
		domains: []string{"yahoo.com", "ymail.com", "yahoo.it"},
		imap:    Server{Host: "imap.mail.yahoo.com", Port: 993, TLS: tlsImplicit, User: userEmail},
		smtp:    Server{Host: "smtp.mail.yahoo.com", Port: 465, TLS: tlsImplicit, User: userEmail},
	},
	{
		domains: []string{"icloud.com", "me.com", "mac.com"},
		imap:    Server{Host: "imap.mail.me.com", Port: 993, TLS: tlsImplicit, User: userEmail},
		smtp:    Server{Host: "smtp.mail.me.com", Port: 587, TLS: tlsStartTLS, User: userLocal},
	},
	{
		domains: []string{"libero.it"},
		imap:    Server{Host: "imapmail.libero.it", Port: 993, TLS: tlsImplicit, User: userEmail},
		smtp:    Server{Host: "smtp.libero.it", Port: 465, TLS: tlsImplicit, User: userEmail},
	},
	{
		domains: []string{"virgilio.it"},
		imap:    Server{Host: "in.virgilio.it", Port: 993, TLS: tlsImplicit, User: userEmail},
		smtp:    Server{Host: "out.virgilio.it", Port: 465, TLS: tlsImplicit, User: userEmail},
	},
	{
		domains: []string{"aruba.it"},
		imap:    Server{Host: "imaps.aruba.it", Port: 993, TLS: tlsImplicit, User: userEmail},
		smtp:    Server{Host: "smtps.aruba.it", Port: 465, TLS: tlsImplicit, User: userEmail},
	},
	{
		domains: []string{"arubapec.it", "pec.it"},
		imap:    Server{Host: "imaps.pec.aruba.it", Port: 993, TLS: tlsImplicit, User: userEmail},
		smtp:    Server{Host: "smtps.pec.aruba.it", Port: 465, TLS: tlsImplicit, User: userEmail},
		pec:     true,
	},
	{
		domains: []string{"tiscali.it"},
		imap:    Server{Host: "imap.tiscali.it", Port: 993, TLS: tlsImplicit, User: userEmail},
		smtp:    Server{Host: "smtp.tiscali.it", Port: 465, TLS: tlsImplicit, User: userEmail},
	},
	{
		domains: []string{"fastwebnet.it"},
		imap:    Server{Host: "imap.fastwebnet.it", Port: 993, TLS: tlsImplicit, User: userEmail},
		smtp:    Server{Host: "smtp.fastwebnet.it", Port: 587, TLS: tlsStartTLS, User: userEmail},
	},
	{
		domains: []string{"legalmail.it"},
		imap:    Server{Host: "mbox.legalmail.it", Port: 993, TLS: tlsImplicit, User: userEmail},
		smtp:    Server{Host: "send.legalmail.it", Port: 465, TLS: tlsImplicit, User: userEmail},
		pec:     true,
	},
	{
		domains: []string{"pec.register.it", "register.it"},
		imap:    Server{Host: "pecmail.register.it", Port: 993, TLS: tlsImplicit, User: userEmail},
		smtp:    Server{Host: "pecmail.register.it", Port: 465, TLS: tlsImplicit, User: userEmail},
		pec:     true,
	},
	{
		domains: []string{"postecert.it", "pec.poste.it"},
		imap:    Server{Host: "mail.postecert.it", Port: 993, TLS: tlsImplicit, User: userEmail},
		smtp:    Server{Host: "mail.postecert.it", Port: 465, TLS: tlsImplicit, User: userEmail},
		pec:     true,
	},
}

func lookupBuiltin(domain string) *Result {
	d := strings.ToLower(strings.TrimSpace(domain))
	if strings.HasSuffix(d, ".onmicrosoft.com") {
		return &Result{
			Domain: d, Source: SourceBuiltin, OAuthHint: HintOutlook,
			IMAP: &Server{Host: "outlook.office365.com", Port: 993, TLS: tlsImplicit, User: userEmail},
			SMTP: &Server{Host: "smtp.office365.com", Port: 587, TLS: tlsStartTLS, User: userEmail},
		}
	}
	for _, p := range builtinProviders {
		for _, pd := range p.domains {
			if d == pd {
				imap, smtp := p.imap, p.smtp
				return withPEC(&Result{
					Domain: d, Source: SourceBuiltin, OAuthHint: p.oauthHint,
					IMAP: &imap, SMTP: &smtp,
				}, p.pec)
			}
		}
	}
	return nil
}

// mxProvider maps MX host substrings to a builtin domain key.
var mxHints = []struct {
	substr string
	domain string
}{
	{"google.com", "gmail.com"},
	{"googlemail.com", "gmail.com"},
	{"outlook.com", "outlook.com"},
	{"hotmail.com", "outlook.com"},
	{"mail.protection.outlook.com", "outlook.com"},
	{"yahoo.com", "yahoo.com"},
	{"yahoodns.net", "yahoo.com"},
	{"icloud.com", "icloud.com"},
	{"mail.me.com", "icloud.com"},
	{"aruba.it", "aruba.it"},
	{"arubapec.it", "arubapec.it"},
	{"libero.it", "libero.it"},
	{"virgilio.it", "virgilio.it"},
	{"tiscali.it", "tiscali.it"},
	{"legalmail", "legalmail.it"},
	{"register.it", "pec.register.it"},
	{"postecert", "postecert.it"},
}

func lookupFromMX(mxHost string) *Result {
	h := strings.ToLower(mxHost)
	for _, m := range mxHints {
		if strings.Contains(h, m.substr) {
			if r := lookupBuiltin(m.domain); r != nil {
				out := *r
				out.Source = SourceMX
				return &out
			}
		}
	}
	return nil
}

// PECDefaults are the S17 §10 settings recommended for a PEC mailbox. The send policy is a suggestion: the user
// picks it in the SMTP form; the hosted connect page, which asks nothing, applies it.
type PECDefaults struct {
	Switches   map[string]bool `json:"switches" doc:"Account switches to turn on, e.g. pec.enabled"`
	SyncPaths  []string        `json:"syncPaths" doc:"Folders to synchronise"`
	SendPolicy string          `json:"sendPolicy" doc:"SMTP send policy: atMostOnce (never retry after DATA, the PEC rule)" example:"atMostOnce"`
}

func pecDefaults() *PECDefaults {
	return &PECDefaults{Switches: map[string]bool{"pec.enabled": true}, SyncPaths: []string{"INBOX"}, SendPolicy: "atMostOnce"}
}

func withPEC(r *Result, pec bool) *Result {
	if pec && r != nil {
		r.PEC = true
		r.PECDefaults = pecDefaults()
	}
	return r
}

// IsPEC reports whether a mailbox is at a known PEC provider, by its domain or its IMAP host.
func IsPEC(domain, imapHost string) bool {
	d := strings.ToLower(strings.TrimSpace(domain))
	h := strings.ToLower(strings.TrimSpace(imapHost))
	for _, p := range builtinProviders {
		if !p.pec {
			continue
		}
		if h != "" && strings.EqualFold(p.imap.Host, h) {
			return true
		}
		for _, pd := range p.domains {
			if d == pd {
				return true
			}
		}
	}
	return false
}
