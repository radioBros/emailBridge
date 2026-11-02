package attachments

import (
	"net/url"
	"strconv"
	"strings"
	"unicode"
)

// contentDisposition builds an RFC 6266 Content-Disposition value with filename and filename*.
func contentDisposition(disposition, filename string) string {
	disp := strings.ToLower(strings.TrimSpace(disposition))
	if disp != "inline" {
		disp = "attachment"
	}
	if filename == "" {
		return disp
	}
	ascii := asciiFilename(filename)
	var b strings.Builder
	b.WriteString(disp)
	b.WriteString(`; filename=`)
	b.WriteString(strconv.Quote(ascii))
	if ascii != filename || needsStar(filename) {
		b.WriteString(`; filename*=UTF-8''`)
		b.WriteString(url.PathEscape(filename))
	}
	return b.String()
}

func asciiFilename(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r < 0x20 || r > 0x7e || r == '"' || r == '\\' {
			b.WriteByte('_')
			continue
		}
		b.WriteRune(r)
	}
	out := b.String()
	if out == "" {
		return "attachment"
	}
	return out
}

func needsStar(s string) bool {
	for _, r := range s {
		if r > unicode.MaxASCII || r == '"' || r == '\\' || r < 0x20 {
			return true
		}
	}
	return false
}
