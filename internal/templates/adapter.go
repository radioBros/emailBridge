package templates

import "context"

// RenderParts is the submit-facing result of RenderForSubmit.
func (s *Service) RenderParts(ctx context.Context, id string, version *int, vars map[string]any) (subject, text, html string, err error) {
	out, err := s.RenderForSubmit(ctx, id, version, vars)
	if err != nil {
		return "", "", "", err
	}
	return out.Subject, out.Text, out.HTML, nil
}
