package auth

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"unicode"

	"github.com/radiobros/emailbridge/internal/errs"
)

// translate turns Limen's error responses ({"message": …[, "code": …]} or plain-text 404s) into RFC 9457
// problems with EmailBridge codes (S10 §2.1). Successful responses and responses that already are
// problems pass through unchanged; headers set by Limen (Set-Cookie, Retry-After) are kept.
func (s *Service) translate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pw := &problemWriter{ResponseWriter: w}
		next.ServeHTTP(pw, r)
		if !pw.intercepted {
			return
		}
		var body struct {
			Message string `json:"message"`
			Code    string `json:"code"`
		}
		_ = json.Unmarshal(pw.buf.Bytes(), &body)
		code := problemCode(pw.status, route(r), body.Message)
		detail := sentence(body.Message)
		if pw.status >= 500 || detail == "" {
			if pw.status >= 500 {
				s.log.ErrorContext(r.Context(), "auth request failed", "route", route(r), "status", pw.status, "err", body.Message)
			}
			detail = ""
		}
		w.Header().Del("Content-Length")
		problem(w, r, code, detail)
	})
}

// problemWriter buffers error responses (status ≥ 400) that are not already problems.
type problemWriter struct {
	http.ResponseWriter
	status      int
	intercepted bool
	wrote       bool
	buf         bytes.Buffer
}

func (w *problemWriter) WriteHeader(code int) {
	if w.wrote {
		return
	}
	w.wrote = true
	w.status = code
	if code >= 400 && !strings.HasPrefix(w.Header().Get("Content-Type"), "application/problem+json") {
		w.intercepted = true
		return
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *problemWriter) Write(b []byte) (int, error) {
	if !w.wrote {
		w.WriteHeader(http.StatusOK)
	}
	if w.intercepted {
		if w.buf.Len() < peekLimit {
			w.buf.Write(b)
		}
		return len(b), nil
	}
	return w.ResponseWriter.Write(b)
}

func (w *problemWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// messageCodes maps Limen's (stable, English) error messages to EmailBridge codes where the status alone
// is ambiguous.
var messageCodes = map[string]errs.Code{
	"invalid credential":                      errs.CodeInvalidCredentials,
	"invalid password":                        errs.CodeInvalidCredentials,
	"current password is invalid":             errs.CodeInvalidCredentials,
	"invalid code":                            errs.CodeMFAInvalid,
	"invalid otp code":                        errs.CodeMFAInvalid,
	"invalid backup code":                     errs.CodeMFAInvalid,
	"invalid 2fa challenge":                   errs.CodeMFAInvalid,
	"2fa challenge has expired":               errs.CodeMFAInvalid,
	"2fa challenge is missing":                errs.CodeMFAInvalid,
	"two factor not enabled":                  errs.CodeMFAInvalid,
	"passkey not recognized":                  errs.CodePasskeyFailed,
	"passkey verification failed":             errs.CodePasskeyFailed,
	"passkey challenge is missing or expired": errs.CodePasskeyFailed,
	"session has expired":                     errs.CodeSessionExpired,
	"origin not allowed":                      errs.CodeOriginRejected,
	"forbidden":                               errs.CodeCSRFFailed,
}

// problemCode picks the code for a Limen error response.
func problemCode(status int, path, message string) errs.Code {
	if c, ok := messageCodes[strings.ToLower(strings.TrimSpace(message))]; ok {
		return c
	}
	switch status {
	case http.StatusBadRequest:
		if strings.HasPrefix(path, "/passkeys/finish-") {
			return errs.CodePasskeyFailed
		}
		return errs.CodeMalformedRequest
	case http.StatusUnauthorized:
		switch path {
		case "/signin/credential":
			return errs.CodeInvalidCredentials
		case "/two-factor/verify", "/two-factor/finalize-setup":
			return errs.CodeMFAInvalid
		case "/passkeys/finish-authentication":
			return errs.CodePasskeyFailed
		}
		return errs.CodeUnauthenticated
	case http.StatusForbidden:
		return errs.CodeForbidden
	case http.StatusNotFound:
		return errs.CodeNotFound
	case http.StatusMethodNotAllowed:
		return errs.CodeMalformedRequest
	case http.StatusConflict:
		return errs.CodeConflict
	case http.StatusRequestEntityTooLarge:
		return errs.CodePayloadTooLarge
	case http.StatusUnsupportedMediaType:
		return errs.CodeUnsupportedMediaType
	case http.StatusUnprocessableEntity:
		return errs.CodeValidationFailed
	case http.StatusTooManyRequests:
		return errs.CodeRateLimited
	case http.StatusServiceUnavailable:
		return errs.CodeServiceUnavailable
	}
	if status >= 500 {
		return errs.CodeInternalError
	}
	return errs.CodeMalformedRequest
}

// sentence turns Limen's lower-case message into a sentence ("invalid code" → "Invalid code.").
func sentence(msg string) string {
	msg = strings.TrimSpace(msg)
	if msg == "" || strings.HasPrefix(msg, "404 page not found") {
		return ""
	}
	r := []rune(msg)
	r[0] = unicode.ToUpper(r[0])
	out := string(r)
	if !strings.HasSuffix(out, ".") {
		out += "."
	}
	return out
}
