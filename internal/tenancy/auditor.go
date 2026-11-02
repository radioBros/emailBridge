package tenancy

import "context"

// FieldDiff is one audited field's before/after (secrets are redacted by the audit writer).
type FieldDiff struct {
	Before any
	After  any
}

// Auditor records tenancy mutations. Implemented by internal/audit (provided on the app registry)
// so this package does not import audit (audit already imports tenancy).
type Auditor interface {
	Record(ctx context.Context, action, targetType, targetID string, changes map[string]FieldDiff) error
}
