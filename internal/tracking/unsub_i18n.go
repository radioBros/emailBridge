package tracking

import (
	"embed"
	"encoding/json"
	"fmt"
	"html"
	"strings"

	"github.com/radiobros/emailbridge/internal/i18n"
)

//go:embed locales/*.json
var unsubLocaleFS embed.FS

type unsubStrings struct {
	Title     string `json:"title"`
	Confirm   string `json:"confirm"`
	Button    string `json:"button"`
	DoneTitle string `json:"done_title"`
	DoneBody  string `json:"done_body"`
}

var unsubByLocale = loadUnsubLocales()

func loadUnsubLocales() map[string]unsubStrings {
	out := make(map[string]unsubStrings, len(i18n.Supported))
	for _, code := range i18n.Supported {
		b, err := unsubLocaleFS.ReadFile("locales/" + code + ".json")
		if err != nil {
			continue
		}
		var s unsubStrings
		if json.Unmarshal(b, &s) != nil {
			continue
		}
		out[code] = s
	}
	return out
}

func unsubStringsFor(acceptLang string) (string, unsubStrings) {
	code := i18n.FromAcceptLanguage(acceptLang)
	if s, ok := unsubByLocale[code]; ok {
		return code, s
	}
	return "en", unsubByLocale["en"]
}

func renderUnsubConfirm(acceptLang, email string) string {
	code, s := unsubStringsFor(acceptLang)
	esc := html.EscapeString(email)
	confirm := strings.ReplaceAll(s.Confirm, "{email}", "<strong>"+esc+"</strong>")
	return unsubPage(code, s.Title, `<p>`+confirm+`</p>
<form method="post"><input type="hidden" name="List-Unsubscribe" value="One-Click">
<button type="submit">`+html.EscapeString(s.Button)+`</button></form>`)
}

func renderUnsubDone(acceptLang, email string) string {
	code, s := unsubStringsFor(acceptLang)
	esc := html.EscapeString(email)
	body := strings.ReplaceAll(s.DoneBody, "{email}", "<strong>"+esc+"</strong>")
	return unsubPage(code, s.DoneTitle, `<p>`+body+`</p>`)
}

func unsubPage(lang, title, body string) string {
	return fmt.Sprintf(`<!doctype html>
<html lang="%s">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>%s</title>
<style>
body{font-family:system-ui,-apple-system,Segoe UI,Roboto,sans-serif;margin:0;padding:1.5rem;line-height:1.5;color:#1a1a1a;background:#f6f7f9}
main{max-width:28rem;margin:2rem auto;padding:1.5rem;background:#fff;border-radius:12px;box-shadow:0 1px 3px rgba(0,0,0,.08)}
button{appearance:none;border:0;border-radius:8px;padding:.7rem 1.1rem;font:inherit;font-weight:600;color:#fff;background:#4338ca;cursor:pointer}
button:hover{background:#3730a3}
</style>
</head>
<body><main>
<h1 style="font-size:1.25rem;margin:0 0 1rem">%s</h1>
%s
</main></body></html>`, html.EscapeString(lang), html.EscapeString(title), html.EscapeString(title), body)
}
