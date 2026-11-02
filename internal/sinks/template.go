package sinks

import (
	"fmt"
	"regexp"
	"strings"
)

var placeholderRE = regexp.MustCompile(`\{([a-zA-Z0-9_]+)\}`)

var allowedPlaceholders = map[string]struct{}{
	"type":      {},
	"accountId": {},
}

// ExpandTemplate replaces {type} and {accountId} in tmpl.
func ExpandTemplate(tmpl string, m Message) string {
	out := tmpl
	out = strings.ReplaceAll(out, "{type}", m.Type)
	out = strings.ReplaceAll(out, "{accountId}", m.AccountID)
	return out
}

func checkTemplate(tmpl, field string) error {
	for _, match := range placeholderRE.FindAllStringSubmatch(tmpl, -1) {
		name := match[1]
		if _, ok := allowedPlaceholders[name]; !ok {
			return validation(fmt.Sprintf("%s has unknown placeholder {%s}", field, name))
		}
	}
	return nil
}
