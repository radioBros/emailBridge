package notify

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/radiobros/emailbridge/internal/errs"
)

func fingerprint(err error, tags map[string]string) string {
	code := string(errs.CodeOf(err))
	kind := string(errs.KindOf(err))
	comp := tags["component"]
	msg := err.Error()
	// Collapse digits to reduce cardinality.
	var b strings.Builder
	for _, r := range msg {
		if r >= '0' && r <= '9' {
			b.WriteByte('#')
			continue
		}
		b.WriteRune(r)
	}
	raw := fmt.Sprintf("%s|%s|%s|%s", comp, kind, code, b.String())
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:12])
}
