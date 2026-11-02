package connectors

// P83All returns P8.3 connector manifests (sending, storage, CRM, SSO, observability).
// Rspamd scoring client lives in rspamd.go; catalogue entry remains the P8.1 definition.
func P83All() []Def {
	return []Def{
		SES(), Postmark(), Mailgun(), SendGrid(), Brevo(),
		GDrive(), OneDrive(),
		HubSpot(), Pipedrive(),
		SSOAuthentik(), SSOKeycloak(), SSOEntra(), SSOGoogle(), SSOOkta(),
		Sentry(), Grafana(), Loki(), OTel(),
	}
}

// P83IDs lists connector ids owned by P8.3.
func P83IDs() []string {
	out := make([]string, 0, 18)
	for _, d := range P83All() {
		out = append(out, d.ID)
	}
	return out
}
