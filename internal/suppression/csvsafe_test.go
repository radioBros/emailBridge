package suppression

import "testing"

func TestCSVSafeFormulaGuard(t *testing.T) {
	cases := map[string]string{
		"=HYPERLINK(1)": "'=HYPERLINK(1)",
		"+1":            "'+1",
		"-2":            "'-2",
		"@SUM(A1)":      "'@SUM(A1)",
		"\tx":           "'\tx",
		"\rx":           "'\rx",
		"plain":         "plain",
		"":              "",
		"a=b":           "a=b",
	}
	for in, want := range cases {
		if got := csvSafe(in); got != want {
			t.Fatalf("csvSafe(%q) = %q, want %q", in, got, want)
		}
		if back := unescapeCSVCell(csvSafe(in)); back != in {
			t.Fatalf("round trip %q → %q", in, back)
		}
	}
	if unescapeCSVCell("'plain") != "'plain" || unescapeCSVCell("'") != "'" {
		t.Fatal("only our own escape is removed")
	}
}
