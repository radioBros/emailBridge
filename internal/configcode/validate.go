package configcode

import (
	"fmt"
	"strings"

	"github.com/radiobros/emailbridge/internal/errs"
)

func validateDocument(doc *Document) error {
	if doc == nil {
		return errs.New(errs.CodeValidationFailed, "document is required.")
	}
	if doc.APIVersion != "" && doc.APIVersion != APIVersion {
		return errs.New(errs.CodeValidationFailed,
			fmt.Sprintf("apiVersion must be %s.", APIVersion)).WithSafe("apiVersion", doc.APIVersion)
	}
	if doc.Kind != "" && doc.Kind != KindOrg {
		return errs.New(errs.CodeValidationFailed,
			fmt.Sprintf("kind must be %s.", KindOrg)).WithSafe("kind", doc.Kind)
	}
	if err := checkLen("webhookEndpoints", len(doc.Spec.WebhookEndpoints)); err != nil {
		return err
	}
	if err := checkLen("routes", len(doc.Spec.Routes)); err != nil {
		return err
	}
	if err := checkLen("apiKeys", len(doc.Spec.APIKeys)); err != nil {
		return err
	}
	seenEP := map[string]struct{}{}
	for i, ep := range doc.Spec.WebhookEndpoints {
		name := strings.TrimSpace(ep.Name)
		if name == "" {
			return errs.New(errs.CodeValidationFailed, "webhookEndpoints[].name is required.").
				WithSafe("index", i)
		}
		if _, ok := seenEP[name]; ok {
			return errs.New(errs.CodeValidationFailed, "Duplicate webhook endpoint name.").
				WithSafe("name", name)
		}
		seenEP[name] = struct{}{}
		if ep.Secret != "" && !IsSecretRef(ep.Secret) {
			return errs.New(errs.CodeValidationFailed,
				"webhookEndpoints[].secret must be a ${secret:NAME} reference.").WithSafe("name", name)
		}
		for _, h := range ep.Headers {
			if h.Secret && h.Value != "" && !IsSecretRef(h.Value) {
				return errs.New(errs.CodeValidationFailed,
					"Secret header values must use ${secret:NAME}.").WithSafe("name", name)
			}
		}
		doc.Spec.WebhookEndpoints[i].Name = name
	}
	seenRT := map[string]struct{}{}
	for i, rt := range doc.Spec.Routes {
		name := strings.TrimSpace(rt.Name)
		ep := strings.TrimSpace(rt.Endpoint)
		if name == "" || ep == "" {
			return errs.New(errs.CodeValidationFailed, "routes[].name and routes[].endpoint are required.").
				WithSafe("index", i)
		}
		if _, ok := seenRT[name]; ok {
			return errs.New(errs.CodeValidationFailed, "Duplicate route name.").WithSafe("name", name)
		}
		seenRT[name] = struct{}{}
		if rt.Secret != "" && !IsSecretRef(rt.Secret) {
			return errs.New(errs.CodeValidationFailed,
				"routes[].secret must be a ${secret:NAME} reference.").WithSafe("name", name)
		}
		doc.Spec.Routes[i].Name = name
		doc.Spec.Routes[i].Endpoint = ep
	}
	seenKey := map[string]struct{}{}
	for i, k := range doc.Spec.APIKeys {
		name := strings.TrimSpace(k.Name)
		if name == "" {
			return errs.New(errs.CodeValidationFailed, "apiKeys[].name is required.").WithSafe("index", i)
		}
		if _, ok := seenKey[name]; ok {
			return errs.New(errs.CodeValidationFailed, "Duplicate API key name.").WithSafe("name", name)
		}
		seenKey[name] = struct{}{}
		doc.Spec.APIKeys[i].Name = name
	}
	// templates / smtpGateways / suppressionLists are not implemented yet — reject rather than silently ignore.
	if len(doc.Spec.Templates) > 0 {
		return errs.New(errs.CodeValidationFailed,
			"spec.templates is not supported by config-as-code yet; remove it or leave it empty.")
	}
	if len(doc.Spec.SMTPGateways) > 0 {
		return errs.New(errs.CodeValidationFailed,
			"spec.smtpGateways is not supported by config-as-code yet; remove it or leave it empty.")
	}
	if len(doc.Spec.SuppressionLists) > 0 {
		return errs.New(errs.CodeValidationFailed,
			"spec.suppressionLists is not supported by config-as-code yet; remove it or leave it empty.")
	}
	return nil
}

func checkLen(kind string, n int) error {
	if n > maxPerKind {
		return errs.New(errs.CodeValidationFailed,
			fmt.Sprintf("%s exceeds the maximum of %d entries.", kind, maxPerKind)).
			WithSafe("kind", kind).WithSafe("count", n)
	}
	return nil
}
