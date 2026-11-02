package throttle

import "strings"

// Known provider families and their domains (P5.5 §3).
var providerDomains = map[string]string{
	"gmail.com": "gmail", "googlemail.com": "gmail",
	"outlook.com": "outlook", "hotmail.com": "outlook", "live.com": "outlook", "msn.com": "outlook",
	"yahoo.com": "yahoo", "ymail.com": "yahoo", "yahoo.it": "yahoo",
	"icloud.com": "icloud", "me.com": "icloud", "mac.com": "icloud",
	"libero.it": "libero", "virgilio.it": "libero",
	"aruba.it": "aruba", "pec.aruba.it": "aruba",
}

// DomainOf returns the lowercased domain of an email address, or "" if none.
func DomainOf(addr string) string {
	addr = strings.TrimSpace(strings.ToLower(addr))
	if i := strings.LastIndexByte(addr, '@'); i >= 0 && i+1 < len(addr) {
		return addr[i+1:]
	}
	return ""
}

// ProviderOf maps a destination domain to a provider family, or "" when unknown.
func ProviderOf(domain string) string {
	return providerDomains[strings.ToLower(strings.TrimSpace(domain))]
}

// DomainsFrom extracts unique non-empty domains from addresses.
func DomainsFrom(addrs []string) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, a := range addrs {
		d := DomainOf(a)
		if d == "" {
			continue
		}
		if _, ok := seen[d]; ok {
			continue
		}
		seen[d] = struct{}{}
		out = append(out, d)
	}
	return out
}
