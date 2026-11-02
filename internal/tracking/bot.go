package tracking

import (
	"net/http"
	"strings"
)

// LikelyBot reports prefetch / privacy-proxy / crawler hits (flagged, not dropped).
func LikelyBot(r *http.Request) bool {
	if r == nil {
		return false
	}
	if r.Method == http.MethodHead {
		return true
	}
	dest := strings.ToLower(r.Header.Get("Sec-Fetch-Dest"))
	if dest == "nested-document" || dest == "iframe" {
		return true
	}
	purpose := strings.ToLower(r.Header.Get("Purpose"))
	if purpose == "prefetch" || strings.EqualFold(r.Header.Get("X-Purpose"), "preview") {
		return true
	}
	ua := strings.ToLower(r.Header.Get("User-Agent"))
	if ua == "" {
		return true
	}
	markers := []string{
		"googleimageproxy", "ggpht.com", "yahoo mail proxy", "via.giphy.com",
		"appleimageproxy", "privacy.apple.com", "facebookexternalhit",
		"bot", "crawler", "spider", "preview", "slurp", "bingpreview",
		"outlook-iOS", // some prefetch UAs
	}
	for _, m := range markers {
		if strings.Contains(ua, m) {
			return true
		}
	}
	return false
}
