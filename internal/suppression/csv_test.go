package suppression

import (
	"strings"
	"testing"
)

func TestImportCSVLimitsInvalid(t *testing.T) {
	// Pure validation path without DB: ImportCSV needs tenancy + list — covered in integration.
	if maxImport != 10_000 {
		t.Fatal(maxImport)
	}
	_ = strings.NewReader("a")
}
