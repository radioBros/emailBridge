package tracking

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/httpapi"
)

// Documentation examples of signed tracking tokens (W70; HMAC with a documentation key).
//
//nolint:gosec // G101: example tokens for the OpenAPI document, signed with a throwaway key, not credentials
const (
	exampleOpenToken  = "MXwxODAwODcyMTAwfG98b3JnXzAxbTVjYXFwazBrYXN0NjN5YzEzd3B5MmQ5fG91dF8wMW01ZGd2NjEwZjRrN3IydDl3M3k1ejhhYnxhbm5hLnJvc3NpQGV4YW1wbGUub3JnfDA.p2iVk0xJTe7wGhT-weUZ0A-hdgMaGsgLUnl5Z7F-h5c"
	exampleClickToken = "MXwxODAwODcyMTAwfGN8b3JnXzAxbTVjYXFwazBrYXN0NjN5YzEzd3B5MmQ5fG91dF8wMW01ZGd2NjEwZjRrN3IydDl3M3k1ejhhYnxhbm5hLnJvc3NpQGV4YW1wbGUub3JnfDI.1nyPxpY3iY1fs8DB7ynoazI2OgqFK82bQmaJ82KMW5U"
	exampleUnsubToken = "MXwxODAwODcyMTAwfHV8b3JnXzAxbTVjYXFwazBrYXN0NjN5YzEzd3B5MmQ5fG91dF8wMW01ZGd2NjEwZjRrN3IydDl3M3k1ejhhYnxhbm5hLnJvc3NpQGV4YW1wbGUub3JnfDA.yMVfi4O5dJJD_LZdWx97WR9k3pm3seyHkmFAD1dPdyE"
)

// documentRoutes documents the public tracking and unsubscribe routes mounted in Start (raw chi handlers).
func (m *Module) documentRoutes(srv *httpapi.Server) {
	tokenParam := func(desc, example string) []*huma.Param {
		return []*huma.Param{{
			Name: "token", In: "path", Required: true, Description: desc,
			Schema: &huma.Schema{Type: huma.TypeString}, Example: example,
		}}
	}
	tokenProblems := []httpapi.ProblemDoc{
		{Code: errs.CodeSignedURLInvalid, Detail: "The tracking token is invalid."},
		{Code: errs.CodeSignedURLExpired, Detail: "The tracking token has expired."},
	}
	public := []map[string][]string{}
	ext := httpapi.Phase("P5")
	htmlPage := func(desc, example string) map[string]*huma.MediaType {
		return map[string]*huma.MediaType{"text/html": {
			Schema:  &huma.Schema{Type: huma.TypeString, Description: desc},
			Example: example,
		}}
	}
	srv.Document(&huma.Operation{
		OperationID: "trackOpen", Method: http.MethodGet, Path: "/t/o/{token}",
		Summary: "Open-tracking pixel", Tags: []string{"tracking"},
		Description: "The 1×1 transparent GIF EmailBridge adds to HTML messages when the `tracking.opens` switch is on; " +
			"loading it records an open for the recipient in the token and emits `tracking.opened` (with a likely-bot " +
			"flag for mail-security scanners and proxies; the IP is truncated). Public: the signed token is the " +
			"credential (valid 90 days by default). Never cached (`Cache-Control: no-store`). A tampered token is `403 " +
			"signed_url_invalid`, an expired one `403 signed_url_expired`.",
		Parameters: tokenParam("Signed open token from the pixel URL.", exampleOpenToken),
		Security:   public,
		Responses: map[string]*huma.Response{"200": {
			Description: "The tracking pixel.",
			Headers: map[string]*huma.Param{"Cache-Control": {Description: "Never cache the pixel, so every open is seen.",
				Schema: &huma.Schema{Type: huma.TypeString}, Example: "no-store, no-cache, must-revalidate, private"}},
			Content: map[string]*huma.MediaType{"image/gif": {
				Schema:  &huma.Schema{Type: huma.TypeString, Format: "binary", Description: "A 42-byte transparent 1×1 GIF."},
				Example: "GIF89a\x01\x00\x01\x00…",
			}},
		}},
		Metadata:   httpapi.ProblemDocs(tokenProblems...),
		Extensions: ext,
	})
	srv.Document(&huma.Operation{
		OperationID: "trackClick", Method: http.MethodGet, Path: "/t/c/{token}",
		Summary: "Click-tracking redirect", Tags: []string{"tracking"},
		Description: "The link EmailBridge puts in place of every link of an HTML message when the `tracking.clicks` " +
			"switch is on. It records the click (`tracking.clicked` with the destination URL) and redirects (`302`) to " +
			"the original URL stored when the message was sent — the destination is not in the token, so the " +
			"redirect cannot be abused as an open redirect. Public: the signed token is the credential. An unknown " +
			"link is `404 not_found`; a tampered or expired token `403 signed_url_invalid` / `signed_url_expired`.",
		Parameters: tokenParam("Signed click token (message, recipient and link number).", exampleClickToken),
		Security:   public,
		Responses: map[string]*huma.Response{"302": {
			Description: "Redirect to the original link.",
			Headers: map[string]*huma.Param{
				"Location": {Description: "The original URL of the link.", Schema: &huma.Schema{Type: huma.TypeString},
					Example: "https://billing.example.com/i/2026-1043"},
				"Cache-Control": {Description: "Never cached, so every click is seen.", Schema: &huma.Schema{Type: huma.TypeString},
					Example: "no-store"},
			},
		}},
		Metadata: httpapi.ProblemDocs(append(append([]httpapi.ProblemDoc{}, tokenProblems...),
			httpapi.ProblemDoc{Code: errs.CodeNotFound, Detail: "The tracked link does not exist."})...),
		Extensions: ext,
	})
	srv.Document(&huma.Operation{
		OperationID: "unsubscribePage", Method: http.MethodGet, Path: "/u/{token}",
		Summary: "Unsubscribe confirmation page", Tags: []string{"tracking"},
		Description: "The `List-Unsubscribe` link of a message sent with unsubscribe headers: a small HTML page, in the " +
			"recipient's language (`Accept-Language`, 9 languages), asking to confirm; its button POSTs to the same " +
			"URL. Viewing the page changes nothing (link scanners cannot unsubscribe anyone). Public: the signed token " +
			"is the credential.",
		Parameters: tokenParam("Signed unsubscribe token from the List-Unsubscribe URL.", exampleUnsubToken),
		Security:   public,
		Responses: map[string]*huma.Response{"200": {
			Description: "Confirmation page.",
			Content: htmlPage("HTML confirmation page with a one-click form.",
				`<!doctype html>`+"\n"+`<html lang="en">`+"\n<head>…<title>Unsubscribe</title>…</head>\n<body><main>\n"+
					`<p>Unsubscribe <strong>anna.rossi@example.org</strong>?</p>`+"\n"+
					`<form method="post"><input type="hidden" name="List-Unsubscribe" value="One-Click">`+"\n"+
					`<button type="submit">Unsubscribe</button></form>`+"\n</main></body>\n</html>"),
		}},
		Metadata:   httpapi.ProblemDocs(tokenProblems...),
		Extensions: ext,
	})
	srv.Document(&huma.Operation{
		OperationID: "unsubscribeOneClick", Method: http.MethodPost, Path: "/u/{token}",
		Summary: "Unsubscribe (RFC 8058 one-click)", Tags: []string{"tracking"},
		Description: "Unsubscribes the recipient in the token: the address is added to the organisation's `unsubscribe` " +
			"suppression list (scoped to the message's mailing list when it had a `List-Id`), `list.unsubscribed` is " +
			"emitted and a short HTML confirmation is returned. This is the target of `List-Unsubscribe-Post: " +
			"List-Unsubscribe=One-Click` (mail providers POST it directly) and of the confirmation page's button. " +
			"Repeating it is harmless. Public: the signed token is the credential.",
		Parameters: tokenParam("Signed unsubscribe token from the List-Unsubscribe URL.", exampleUnsubToken),
		Security:   public,
		RequestBody: &huma.RequestBody{Content: map[string]*huma.MediaType{"application/x-www-form-urlencoded": {
			Schema: &huma.Schema{Type: huma.TypeObject, Properties: map[string]*huma.Schema{
				"List-Unsubscribe": {Type: huma.TypeString, Description: "RFC 8058: always One-Click (the body is not required)."},
			}},
			Example: map[string]any{"List-Unsubscribe": "One-Click"},
		}}},
		Responses: map[string]*huma.Response{"200": {
			Description: "Unsubscribed.",
			Content: htmlPage("HTML confirmation.",
				`<!doctype html>`+"\n"+`<html lang="en">`+"\n<head>…<title>Unsubscribed</title>…</head>\n<body><main>\n"+
					`<p><strong>anna.rossi@example.org</strong> has been unsubscribed.</p>`+"\n</main></body>\n</html>"),
		}},
		Metadata:   httpapi.ProblemDocs(tokenProblems...),
		Extensions: ext,
	})
}
