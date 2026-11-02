package evidence

import (
	"fmt"
	"path"
	"strings"
	"time"
)

// ObjectKey returns evidence/<org>/<account>/<yyyy>/<mm>/<id>.eml
func ObjectKey(org, account, id string, at time.Time) string {
	at = at.UTC()
	id = strings.TrimSpace(id)
	return fmt.Sprintf("evidence/%s/%s/%04d/%02d/%s.eml", org, account, at.Year(), int(at.Month()), id)
}

// ManifestKeyFor returns the .json key next to an .eml key.
func ManifestKeyFor(emlKey string) string {
	return strings.TrimSuffix(emlKey, path.Ext(emlKey)) + ".json"
}

// TSRKeyFor returns the .tsr key next to an .eml key.
func TSRKeyFor(emlKey string) string {
	return strings.TrimSuffix(emlKey, path.Ext(emlKey)) + ".tsr"
}
