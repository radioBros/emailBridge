package errs

// Kind classifies an error for retries, account state and alerts (S2 §5.1).
type Kind string

const (
	// KindTemporary is a transient failure that may succeed on retry.
	KindTemporary Kind = "temporary"
	// KindPermanent will not succeed without a change.
	KindPermanent Kind = "permanent"
	// KindOutcomeUnknown means the operation may have happened (e.g. lost after DATA).
	KindOutcomeUnknown Kind = "outcome_unknown"
	// KindAuth is rejected credentials (mail, OAuth, or API caller).
	KindAuth Kind = "auth"
	// KindForbidden is authenticated but not allowed.
	KindForbidden Kind = "forbidden"
	// KindValidation is bad input.
	KindValidation Kind = "validation"
	// KindConfig is misconfiguration.
	KindConfig Kind = "config"
	// KindRateLimited is our limit or a provider's.
	KindRateLimited Kind = "rate_limited"
	// KindNotFound is a missing resource.
	KindNotFound Kind = "not_found"
	// KindConflict is a state or version conflict.
	KindConflict Kind = "conflict"
	// KindInternal is a bug or broken invariant.
	KindInternal Kind = "internal"
)

// kindDefaultStatus maps a kind to the HTTP status used when an attempt-only
// code surfaces at the API (S2 §5.1).
func kindDefaultStatus(k Kind) int {
	switch k {
	case KindTemporary:
		return 503
	case KindPermanent:
		return 422
	case KindOutcomeUnknown:
		return 502
	case KindAuth:
		return 401
	case KindForbidden:
		return 403
	case KindValidation:
		return 400
	case KindConfig:
		return 422
	case KindRateLimited:
		return 429
	case KindNotFound:
		return 404
	case KindConflict:
		return 409
	case KindInternal:
		return 500
	default:
		return 500
	}
}
