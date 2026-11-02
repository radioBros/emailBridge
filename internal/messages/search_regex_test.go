package messages

import (
	"regexp"
	"strings"
	"testing"

	"github.com/radiobros/emailbridge/internal/errs"
)

func TestSearchRegexEscapesAndCaps(t *testing.T) {
	rx, err := searchRegex(`a.b*(c)|[d]^$\`)
	if err != nil {
		t.Fatal(err)
	}
	pat, _ := rx["$regex"].(string)
	if rx["$options"] != "i" {
		t.Fatal("case-insensitive")
	}
	re := regexp.MustCompile("(?i)" + pat)
	if !re.MatchString(`xx A.B*(C)|[D]^$\ yy`) || re.MatchString("aXb") {
		t.Fatalf("pattern %q is not literal", pat)
	}
	if _, err := searchRegex(strings.Repeat("é", maxSearchLen)); err != nil {
		t.Fatalf("at the cap (runes): %v", err)
	}
	if _, err := searchRegex(strings.Repeat("x", maxSearchLen+1)); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("over the cap: %v", err)
	}
}
