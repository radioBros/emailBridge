package submit

import (
	"context"
	"fmt"
	"strings"

	"github.com/radiobros/emailbridge/internal/errs"
)

// prepareRequest expands template and/or reference before build (P5.1).
func (s *Service) prepareRequest(ctx context.Context, accountID string, acc *SMTPAccount, req SubmitRequest) (SubmitRequest, error) {
	if err := validateDSN(req.DSN); err != nil {
		return req, err
	}
	if req.Template != nil && req.Raw != "" {
		return req, errs.New(errs.CodeRawAndFields, "Send either raw or a template, not both.")
	}
	if req.Template != nil {
		expanded, err := s.applyTemplate(ctx, req)
		if err != nil {
			return req, err
		}
		req = expanded
	}
	if req.Reference != nil {
		expanded, err := s.applyReference(ctx, accountID, acc, req)
		if err != nil {
			return req, err
		}
		req = expanded
	}
	return req, nil
}

func (s *Service) applyTemplate(ctx context.Context, req SubmitRequest) (SubmitRequest, error) {
	if s.Templates == nil {
		return req, errs.New(errs.CodeFeatureDisabled, "Templates are not available.")
	}
	ref := req.Template
	if ref.ID == "" {
		return req, errs.New(errs.CodeValidationFailed, "template.id is required.")
	}
	subject, text, html, err := s.Templates.RenderParts(ctx, ref.ID, ref.Version, ref.Variables)
	if err != nil {
		return req, err
	}
	req.Subject, req.Text, req.HTML = subject, text, html
	req.Template = nil // consumed; avoid re-entry and keep raw XOR structured clean
	return req, nil
}

func mergeVars(base, over map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range base {
		out[k] = v
	}
	for k, v := range over {
		out[k] = v
	}
	return out
}

func prefixSubject(prefix, subject string) string {
	subject = strings.TrimSpace(subject)
	lower := strings.ToLower(subject)
	p := strings.ToLower(prefix)
	if strings.HasPrefix(lower, p) {
		return subject
	}
	if subject == "" {
		return strings.TrimSpace(prefix)
	}
	return fmt.Sprintf("%s %s", strings.TrimSpace(prefix), subject)
}
