package fulltext

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/radiobros/emailbridge/internal/errs"
)

// query is a parsed search text.
type query struct {
	// Search is the `$text.$search` string: words, "quoted phrases" and -excluded words, re-serialised so
	// stray quotes or dashes can never change the meaning.
	Search string
	// Terms are the positive words and phrases, used to place and highlight the snippet.
	Terms []string
}

// parseQuery validates and normalises the user's search text. Syntax (MongoDB text search): words match
// any of them; "a phrase" must appear as written; -word excludes messages containing word. Matching is
// case- and diacritic-insensitive and whole-word (no stemming).
func parseQuery(raw string) (query, error) {
	q := strings.TrimSpace(strings.ToValidUTF8(raw, ""))
	if q == "" {
		return query{}, errs.New(errs.CodeValidationFailed, "q is required.").WithSafe("field", "q")
	}
	if utf8.RuneCountInString(q) > maxQueryRunes {
		return query{}, errs.New(errs.CodeValidationFailed, "The search text is too long.").WithSafe("max", maxQueryRunes)
	}
	q = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, q)

	var parts, terms []string
	add := func(part, term string) error {
		if len(parts) >= maxQueryTerms {
			return errs.New(errs.CodeValidationFailed, "The search text has too many words.").WithSafe("max", maxQueryTerms)
		}
		parts = append(parts, part)
		if term != "" {
			terms = append(terms, term)
		}
		return nil
	}
	for q != "" {
		q = strings.TrimLeftFunc(q, unicode.IsSpace)
		if q == "" {
			break
		}
		if q[0] == '"' {
			end := strings.IndexByte(q[1:], '"')
			var phrase string
			if end < 0 {
				phrase, q = q[1:], ""
			} else {
				phrase, q = q[1:1+end], q[2+end:]
			}
			phrase = strings.Join(strings.Fields(phrase), " ")
			if phrase == "" {
				continue
			}
			if err := add(`"`+phrase+`"`, phrase); err != nil {
				return query{}, err
			}
			continue
		}
		end := strings.IndexFunc(q, func(r rune) bool { return unicode.IsSpace(r) || r == '"' })
		var word string
		if end < 0 {
			word, q = q, ""
		} else {
			word, q = q[:end], q[end:]
		}
		negated := strings.HasPrefix(word, "-")
		word = strings.Trim(word, "-")
		if word == "" {
			continue
		}
		if negated {
			if err := add("-"+word, ""); err != nil {
				return query{}, err
			}
			continue
		}
		if err := add(word, word); err != nil {
			return query{}, err
		}
	}
	if len(terms) == 0 {
		return query{}, errs.New(errs.CodeValidationFailed, "q needs at least one word that is not excluded.").WithSafe("field", "q")
	}
	return query{Search: strings.Join(parts, " "), Terms: terms}, nil
}
