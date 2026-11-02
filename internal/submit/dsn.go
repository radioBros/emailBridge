package submit

import (
	"strings"

	"github.com/radiobros/emailbridge/internal/errs"
)

// validateDSN checks RFC 3461 request fields (P5.3).
func validateDSN(d *DSNRequest) error {
	if d == nil {
		return nil
	}
	if len(d.EnvID) > 100 {
		return errs.New(errs.CodeValidationFailed, "dsn.envid must be at most 100 characters.")
	}
	if d.Ret != "" {
		switch strings.ToLower(d.Ret) {
		case "full", "hdrs":
		default:
			return errs.New(errs.CodeValidationFailed, "dsn.ret must be full or hdrs.")
		}
	}
	var never, other bool
	seen := map[string]bool{}
	for _, n := range d.Notify {
		v := strings.ToLower(strings.TrimSpace(n))
		switch v {
		case "success", "failure", "delay":
			other = true
		case "never":
			never = true
		case "":
			return errs.New(errs.CodeValidationFailed, "dsn.notify entries must not be empty.")
		default:
			return errs.New(errs.CodeValidationFailed, "dsn.notify must be success, failure, delay or never.").
				WithSafe("value", n)
		}
		if seen[v] {
			continue
		}
		seen[v] = true
	}
	if never && other {
		return errs.New(errs.CodeValidationFailed, "dsn.notify cannot combine never with other values.")
	}
	return nil
}
