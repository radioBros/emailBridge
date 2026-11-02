package ai

import (
	"context"
	"strings"

	"github.com/radiobros/emailbridge/internal/accounts"
	"github.com/radiobros/emailbridge/internal/messages"
)

// MessageTextFrom wraps messages.Service for AI ops.
type MessageTextFrom struct {
	Svc *messages.Service
}

// PlainText implements MessageText.
func (m MessageTextFrom) PlainText(ctx context.Context, accountID, messageID string, maxChars int) (string, error) {
	v, err := m.Svc.GetMessage(ctx, accountID, messageID, messages.GetOptions{Text: "plain", MaxBytes: int64(maxChars)})
	if err != nil {
		return "", err
	}
	if v == nil {
		return "", nil
	}
	plain := v.Text
	if strings.TrimSpace(plain) == "" && v.Subject != "" {
		plain = v.Subject
	}
	return Truncate(plain, maxChars), nil
}

// AccountSwitchesFrom reads account.switches for ai.enabled.
type AccountSwitchesFrom struct {
	Svc *accounts.Service
}

// SwitchBool implements AccountSwitches.
func (a AccountSwitchesFrom) SwitchBool(ctx context.Context, orgID, accountID, name string) (bool, bool, error) {
	_ = orgID
	acc, err := a.Svc.Get(ctx, accountID)
	if err != nil {
		return false, false, err
	}
	if acc == nil || acc.Switches == nil {
		return false, false, nil
	}
	v, ok := acc.Switches[name]
	if !ok {
		return false, false, nil
	}
	b, ok := v.(bool)
	if !ok {
		return false, false, nil
	}
	return b, true, nil
}
