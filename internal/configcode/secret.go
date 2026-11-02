package configcode

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

var (
	secretRefRE = regexp.MustCompile(`^\$\{secret:([A-Za-z_][A-Za-z0-9_]*)\}$`)
	envRefRE    = regexp.MustCompile(`^\$\{([A-Za-z_][A-Za-z0-9_]*)\}$`)
)

// EnvSource loads an organisation's settings.env values (secrets decrypted) and the names of the keys that are
// stored as secrets. *settings.Service implements it (RuleEnv).
type EnvSource interface {
	RuleEnv(ctx context.Context, orgID string) (map[string]string, []string, error)
}

// IsSecretRef reports whether s is a ${secret:NAME} or ${NAME} placeholder.
func IsSecretRef(s string) bool {
	s = strings.TrimSpace(s)
	return secretRefRE.MatchString(s) || envRefRE.MatchString(s)
}

// SecretName extracts the secret name from a secret reference.
func SecretName(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if m := secretRefRE.FindStringSubmatch(s); len(m) == 2 {
		return m[1], true
	}
	if m := envRefRE.FindStringSubmatch(s); len(m) == 2 {
		return m[1], true
	}
	return "", false
}

// FormatSecretRef returns ${secret:NAME}.
func FormatSecretRef(name string) string {
	return "${secret:" + name + "}"
}

// ResolveOrgSecret expands a ${secret:NAME} (or ${NAME}) reference from the calling organisation's own
// settings.env secrets. It never reads the process environment or files: a reference can only reach values
// the organisation stored itself (security fix: an organisation admin could otherwise read server secrets
// such as EB_AUTH_SECRET or the KEK). Literal values are rejected; an unknown name, or a key that is not
// stored as a secret, is a validation error naming the key — never its value.
func ResolveOrgSecret(ctx context.Context, src EnvSource, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", nil
	}
	name, ok := SecretName(ref)
	if !ok {
		return "", errs.New(errs.CodeValidationFailed,
			"Secrets must use ${secret:NAME} (or ${NAME}); inline secret values are not allowed.")
	}
	c, ok := tenancy.CallerFrom(ctx)
	if !ok || strings.TrimSpace(c.OrganizationID) == "" {
		return "", errs.New(errs.CodeForbidden, "An organisation context is required to resolve secrets.")
	}
	if src == nil {
		return "", errs.New(errs.CodeInternalError, "The settings service is not configured.")
	}
	env, secretKeys, err := src.RuleEnv(ctx, c.OrganizationID)
	if err != nil {
		return "", err
	}
	v, found := env[name]
	if !found || !slices.Contains(secretKeys, name) || v == "" {
		return "", errs.New(errs.CodeValidationFailed,
			fmt.Sprintf("Secret %s is not defined: add it to this organisation's settings env as a secret value.", name)).
			WithSafe("name", name)
	}
	return v, nil
}

// redactSecretChange returns "(changed)" or "(unchanged)" for plan output.
func redactSecretChange(changed bool) string {
	if changed {
		return "(changed)"
	}
	return "(unchanged)"
}
