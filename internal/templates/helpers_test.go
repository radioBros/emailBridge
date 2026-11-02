package templates

import (
	"testing"
	"time"
)

func TestAsTimeAndIsEmpty(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	cases := []any{now, now.Format(time.RFC3339), "2026-01-02", "2026-01-02 03:04:05", float64(now.Unix()), now.Unix(), int(now.Unix())}
	for _, c := range cases {
		if _, ok := asTime(c); !ok {
			t.Fatalf("asTime(%T %v)", c, c)
		}
	}
	if _, ok := asTime("nope"); ok {
		t.Fatal("bad time")
	}
	if !isEmpty(nil) || !isEmpty("") || !isEmpty(false) || isEmpty("x") || isEmpty(true) {
		t.Fatal("isEmpty")
	}
}

func TestLoadLocationAndValidateNameBodies(t *testing.T) {
	if loadLocation("").String() != "UTC" {
		t.Fatal("empty tz")
	}
	if loadLocation("Not/AZone").String() != "UTC" {
		t.Fatal("bad tz")
	}
	if _, err := validateName(""); err == nil {
		t.Fatal("empty name")
	}
	if _, err := validateName("ok"); err != nil {
		t.Fatal(err)
	}
	if err := validateBodies("", "", "", nil); err == nil {
		t.Fatal("need a body")
	}
	if err := validateBodies("s", "", "", nil); err != nil {
		t.Fatal(err)
	}
	big := stringsRepeat("x", MaxBodySource+1)
	if err := validateBodies("", big, "", nil); err == nil {
		t.Fatal("body too large")
	}
	parts := map[string]string{}
	for i := 0; i < MaxPartials+1; i++ {
		parts[string(rune('a'+i%26))+string(rune('0'+i/26))] = "x"
	}
	if err := validateBodies("s", "", "", parts); err == nil {
		t.Fatal("too many partials")
	}
	if err := validateBodies("s", "", "", map[string]string{"": "x"}); err == nil {
		t.Fatal("bad partial name")
	}
	if err := validateBodies("s", "", "", map[string]string{"p": stringsRepeat("y", MaxPartialSource+1)}); err == nil {
		t.Fatal("partial too large")
	}
	if parseETag(`"v12"`) != 12 || formatETag(3) != `"v3"` {
		t.Fatal("etag")
	}
	if defaultStr("", "en") != "en" || defaultStr("it", "en") != "it" {
		t.Fatal("defaultStr")
	}
}

func stringsRepeat(s string, n int) string {
	b := make([]byte, 0, n*len(s))
	for i := 0; i < n; i++ {
		b = append(b, s...)
	}
	return string(b)
}
