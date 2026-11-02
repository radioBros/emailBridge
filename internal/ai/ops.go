package ai

import (
	"context"
	"strings"
	"time"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

// SummarizeMessage summarises a stored message.
func (s *Service) SummarizeMessage(ctx context.Context, accountID, messageID string) (string, error) {
	text, err := s.loadMessageText(ctx, accountID, messageID)
	if err != nil {
		return "", err
	}
	out, err := s.run(ctx, "", accountID, FeatureSummary,
		"Summarise the following email text in 3–5 sentences.", text, nil)
	if err != nil {
		return "", err
	}
	s.auditOp(ctx, "ai.summarize", accountID, messageID)
	return out, nil
}

// ClassifyMessage classifies a stored message.
func (s *Service) ClassifyMessage(ctx context.Context, accountID, messageID string, labels []string) (string, error) {
	text, err := s.loadMessageText(ctx, accountID, messageID)
	if err != nil {
		return "", err
	}
	if len(labels) == 0 {
		return "", errs.New(errs.CodeValidationFailed, "At least one label is required.")
	}
	sys := "Classify the email into exactly one of these labels: " + strings.Join(labels, ", ") + ". Reply with the label only."
	out, err := s.run(ctx, "", accountID, FeatureClassification, sys, text, labels)
	if err != nil {
		return "", err
	}
	s.auditOp(ctx, "ai.classify", accountID, messageID)
	return out, nil
}

// ExtractMessage extracts structured data from a stored message.
func (s *Service) ExtractMessage(ctx context.Context, accountID, messageID, schema string) (string, error) {
	text, err := s.loadMessageText(ctx, accountID, messageID)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(schema) == "" {
		return "", errs.New(errs.CodeValidationFailed, "schema is required.")
	}
	sys := "Extract structured data as JSON matching this schema description: " + schema + ". Reply with JSON only."
	out, err := s.run(ctx, "", accountID, FeatureExtraction, sys, text, nil)
	if err != nil {
		return "", err
	}
	s.auditOp(ctx, "ai.extract", accountID, messageID)
	return out, nil
}

// RiskMessage assesses risk of a stored message.
func (s *Service) RiskMessage(ctx context.Context, accountID, messageID string) (risk, rationale string, err error) {
	text, err := s.loadMessageText(ctx, accountID, messageID)
	if err != nil {
		return "", "", err
	}
	sys := "Assess the security/phishing risk of this email. First line: one of low, medium, high, unknown. Second line: short rationale."
	out, err := s.run(ctx, "", accountID, FeatureRisk, sys, text, []string{"low", "medium", "high", "unknown"})
	if err != nil {
		return "", "", err
	}
	risk, rationale = splitRisk(out)
	s.auditOp(ctx, "ai.risk", accountID, messageID)
	return risk, rationale, nil
}

// EnrichMessageReceived adds ai.{summary,category,risk} for webhook payloads (fail-open).
func (s *Service) EnrichMessageReceived(ctx context.Context, org, accountID string, data map[string]any) map[string]any {
	if data == nil || !s.aiEnabled(ctx, org, accountID) {
		return nil
	}
	text := textFromEventData(data)
	if strings.TrimSpace(text) == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	out := map[string]any{}
	if sum, err := s.run(ctx, org, accountID, FeatureSummary,
		"Summarise in one short sentence.", text, nil); err == nil && sum != "" {
		out["summary"] = sum
	}
	if label, err := s.run(ctx, org, accountID, FeatureClassification,
		"Classify as one of: general, sales, support, spam, urgent. Reply with the label only.",
		text, []string{"general", "sales", "support", "spam", "urgent"}); err == nil && label != "" {
		out["category"] = label
	}
	if risk, err := s.run(ctx, org, accountID, FeatureRisk,
		"Reply with exactly one word: low, medium, high, or unknown.",
		text, []string{"low", "medium", "high", "unknown"}); err == nil && risk != "" {
		out["risk"] = risk
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (s *Service) loadMessageText(ctx context.Context, accountID, messageID string) (string, error) {
	if err := tenancy.RequireReadAccount(ctx, "messages", accountID); err != nil {
		return "", err
	}
	if s.Messages == nil {
		return "", errs.New(errs.CodeInternalError, "message loader not configured")
	}
	org, _ := store.OrganizationFrom(ctx)
	_ = org
	max := defaultBodyMax
	if cfg, err := s.Repo.Get(ctx, org); err == nil && cfg != nil && cfg.BodyMaxChars > 0 {
		max = cfg.BodyMaxChars
	}
	return s.Messages.PlainText(ctx, accountID, messageID, max)
}

func (s *Service) auditOp(ctx context.Context, action, accountID, messageID string) {
	if s.Audit != nil {
		s.Audit(ctx, action, accountID, messageID)
	}
}

func textFromEventData(data map[string]any) string {
	if text, ok := data["text"].(map[string]any); ok {
		if p, _ := text["plain"].(string); strings.TrimSpace(p) != "" {
			return p
		}
		if h, _ := text["html"].(string); strings.TrimSpace(h) != "" {
			return h
		}
	}
	if sub, _ := data["subject"].(string); sub != "" {
		return sub
	}
	return ""
}

func splitRisk(out string) (risk, rationale string) {
	lines := strings.SplitN(strings.TrimSpace(out), "\n", 2)
	risk = strings.ToLower(strings.TrimSpace(lines[0]))
	switch risk {
	case "low", "medium", "high", "unknown":
	default:
		risk = matchLabel(out, []string{"low", "medium", "high", "unknown"})
		if risk == "" {
			risk = "unknown"
		}
	}
	if len(lines) > 1 {
		rationale = strings.TrimSpace(lines[1])
	}
	return risk, rationale
}
