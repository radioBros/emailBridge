package errs

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

// InstallHuma overrides huma.NewError and huma.NewErrorWithContext so huma's
// built-in errors render as our Problem. This is the one deliberate package-level
// mutable assignment in EmailBridge (documented exception to the no-global-mutable-state rule).
func InstallHuma() {
	huma.NewError = newHumaError
	huma.NewErrorWithContext = func(_ huma.Context, status int, msg string, errs ...error) huma.StatusError {
		return newHumaError(status, msg, errs...)
	}
}

func newHumaError(status int, msg string, errs ...error) huma.StatusError {
	code, kind := mapHTTPStatus(status)
	info, ok := catalog[code]
	title := http.StatusText(status)
	if ok && info.Title != "" {
		title = info.Title
	}
	p := &Problem{
		Type:   problemTypeBase + string(code),
		Title:  title,
		Status: status,
		Detail: msg,
		Code:   code,
		Kind:   kind,
	}
	for _, e := range errs {
		if d, ok := e.(huma.ErrorDetailer); ok {
			ed := d.ErrorDetail()
			path := ""
			message := ""
			if ed != nil {
				path = ed.Location
				message = ed.Message
			}
			p.Errors = append(p.Errors, ProblemItem{
				Path:    path,
				Code:    "invalid",
				Message: message,
			})
		}
	}
	return p
}

func mapHTTPStatus(status int) (Code, Kind) {
	switch status {
	case http.StatusUnprocessableEntity:
		return CodeValidationFailed, KindValidation
	case http.StatusNotFound:
		return CodeNotFound, KindNotFound
	case http.StatusMethodNotAllowed:
		return CodeMalformedRequest, KindValidation
	case http.StatusRequestEntityTooLarge:
		return CodePayloadTooLarge, KindValidation
	case http.StatusUnsupportedMediaType:
		return CodeUnsupportedMediaType, KindValidation
	case http.StatusTooManyRequests:
		return CodeRateLimited, KindRateLimited
	default:
		if status >= 500 {
			return CodeInternalError, KindInternal
		}
		if status >= 400 {
			return CodeMalformedRequest, KindValidation
		}
		return CodeInternalError, KindInternal
	}
}
