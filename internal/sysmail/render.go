package sysmail

import (
	"bytes"
	"embed"
	htmltemplate "html/template"
	"io/fs"
	"path"
	"strings"
	texttemplate "text/template"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/i18n"
	"github.com/radiobros/emailbridge/internal/mailproto/mime"
)

//go:embed templates/*/*.html templates/*/*.txt
var templateFS embed.FS

// English subjects are the fallback for every locale until a dedicated subject exists.
var subjectsEN = map[string]string{
	KindMFACode:           "Your EmailBridge verification code",
	KindInvitation:        "You are invited to EmailBridge",
	KindPasswordReset:     "Reset your EmailBridge password",
	KindEmailVerification: "Verify your EmailBridge email",
	KindAlert:             "EmailBridge alert",
}

//nolint:gosec // G101: localised subject lines, not credentials
var subjects = map[string]map[string]string{
	"en": subjectsEN,
	"it": {
		KindMFACode:           "Il tuo codice di verifica EmailBridge",
		KindInvitation:        "Invito a EmailBridge",
		KindPasswordReset:     "Reimposta la password EmailBridge",
		KindEmailVerification: "Verifica la tua email EmailBridge",
		KindAlert:             "Avviso EmailBridge",
	},
}

var knownKinds = map[string]bool{
	KindMFACode: true, KindInvitation: true, KindPasswordReset: true,
	KindEmailVerification: true, KindAlert: true,
}

// Rendered is the MIME-ready output of a template.
type Rendered struct {
	Subject string
	Text    string
	HTML    string
	Raw     []byte
}

// Render builds subject + text/html bodies and a multipart/alternative MIME message.
// Missing locale templates fall back to English so a missing translation never breaks mail.
func Render(kind, locale, from, to string, data map[string]string) (*Rendered, error) {
	if !knownKinds[kind] {
		return nil, errs.New(errs.CodeValidationFailed, "unknown system mail kind")
	}
	locale = NormalizeLocale(locale)
	if data == nil {
		data = map[string]string{}
	}
	text, used, err := execTextFallback(locale, kind+".txt", data)
	if err != nil {
		return nil, err
	}
	html, _, err := execHTMLFallback(locale, kind+".html", data)
	if err != nil {
		return nil, err
	}
	_ = used
	subject := subjectFor(locale, kind)
	if kind == KindAlert && data["Title"] != "" {
		subject = data["Title"]
	}
	built, err := mime.Build(mime.Message{
		From:    []mime.Address{{Name: "EmailBridge", Address: extractAddr(from)}},
		To:      []mime.Address{{Address: to}},
		Subject: subject,
		Text:    text,
		HTML:    html,
	})
	if err != nil {
		return nil, err
	}
	return &Rendered{Subject: subject, Text: text, HTML: html, Raw: built.Raw}, nil
}

// NormalizeLocale maps any input to one of the nine product codes (default en).
func NormalizeLocale(l string) string {
	return i18n.Normalize(l)
}

func subjectFor(locale, kind string) string {
	if m, ok := subjects[locale]; ok {
		if s := m[kind]; s != "" {
			return s
		}
	}
	return subjectsEN[kind]
}

func extractAddr(from string) string {
	from = strings.TrimSpace(from)
	if i := strings.LastIndexByte(from, '<'); i >= 0 {
		if j := strings.IndexByte(from[i:], '>'); j > 0 {
			return from[i+1 : i+j]
		}
	}
	return from
}

func execTextFallback(locale, file string, data map[string]string) (string, string, error) {
	for _, loc := range []string{locale, "en"} {
		name := path.Join("templates", loc, file)
		s, err := execText(name, data)
		if err == nil {
			return s, loc, nil
		}
		if loc == "en" {
			return "", loc, err
		}
	}
	return "", "", errs.New(errs.CodeInternalError, "sysmail.template")
}

func execHTMLFallback(locale, file string, data map[string]string) (string, string, error) {
	for _, loc := range []string{locale, "en"} {
		name := path.Join("templates", loc, file)
		s, err := execHTML(name, data)
		if err == nil {
			return s, loc, nil
		}
		if loc == "en" {
			return "", loc, err
		}
	}
	return "", "", errs.New(errs.CodeInternalError, "sysmail.template")
}

func execText(name string, data map[string]string) (string, error) {
	b, err := fs.ReadFile(templateFS, name)
	if err != nil {
		return "", errs.Wrap(err, errs.CodeInternalError, "sysmail.template")
	}
	t, err := texttemplate.New(name).Parse(string(b))
	if err != nil {
		return "", errs.Wrap(err, errs.CodeInternalError, "sysmail.template")
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", errs.Wrap(err, errs.CodeInternalError, "sysmail.template")
	}
	return buf.String(), nil
}

func execHTML(name string, data map[string]string) (string, error) {
	b, err := fs.ReadFile(templateFS, name)
	if err != nil {
		return "", errs.Wrap(err, errs.CodeInternalError, "sysmail.template")
	}
	t, err := htmltemplate.New(name).Parse(string(b))
	if err != nil {
		return "", errs.Wrap(err, errs.CodeInternalError, "sysmail.template")
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", errs.Wrap(err, errs.CodeInternalError, "sysmail.template")
	}
	return buf.String(), nil
}

// HasTemplate reports whether both text and HTML templates exist for kind/locale.
func HasTemplate(kind, locale string) bool {
	locale = NormalizeLocale(locale)
	_, err1 := fs.ReadFile(templateFS, path.Join("templates", locale, kind+".txt"))
	_, err2 := fs.ReadFile(templateFS, path.Join("templates", locale, kind+".html"))
	return err1 == nil && err2 == nil
}
