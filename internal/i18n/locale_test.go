package i18n

import "testing"

func TestNormalize(t *testing.T) {
	cases := map[string]string{
		"": "en", "EN": "en", "it-IT": "it", "zh-TW": "zh", "zh-CN": "zh",
		"pt-PT": "pt", "pt-BR": "pt", "de-AT": "de", "fr": "fr", "xx": "en",
		"ru-RU;q=0.9": "ru",
	}
	for in, want := range cases {
		if got := Normalize(in); got != want {
			t.Fatalf("Normalize(%q)=%q want %q", in, got, want)
		}
	}
}

func TestFromAcceptLanguage(t *testing.T) {
	if got := FromAcceptLanguage("ru"); got != "ru" {
		t.Fatalf("ru: %q", got)
	}
	if got := FromAcceptLanguage("ru-RU,ru;q=0.9,en;q=0.8"); got != "ru" {
		t.Fatalf("ru list: %q", got)
	}
	if got := FromAcceptLanguage("xx,yy"); got != "en" {
		t.Fatalf("unknown: %q", got)
	}
	if got := FromAcceptLanguage(""); got != "en" {
		t.Fatalf("empty: %q", got)
	}
	if got := FromAcceptLanguage("fr;q=0.2,de;q=0.8"); got != "de" {
		t.Fatalf("q: %q", got)
	}
}

func TestIsSupported(t *testing.T) {
	if !IsSupported("ja") || IsSupported("xx") {
		t.Fatal("IsSupported")
	}
	if len(Supported) != 9 {
		t.Fatalf("want 9 got %d", len(Supported))
	}
}

func TestHasSupportedPrimary(t *testing.T) {
	for _, tag := range []string{"it", "it-IT", "de-CH", "pt_PT", "zh-Hans-CN", "EN", "ja-JP"} {
		if !HasSupportedPrimary(tag) {
			t.Fatalf("want supported: %q", tag)
		}
	}
	for _, tag := range []string{"xx", "xx-YY", "nope", "sv-SE"} {
		if HasSupportedPrimary(tag) {
			t.Fatalf("want unsupported: %q", tag)
		}
	}
}
