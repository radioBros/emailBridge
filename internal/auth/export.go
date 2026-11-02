package auth

import (
	"context"
	"fmt"
	"time"

	"github.com/radiobros/emailbridge/internal/tenancy"
)

// ExportSessions lists a user's active sessions for GET /v1/me/export (S11 §6): the same view as the sessions
// API — never the token.
func (s *Service) ExportSessions(ctx context.Context, authUserID, currentSessionID string) ([]tenancy.ExportSession, error) {
	sessions, err := s.limen.ListSessions(ctx, authUserID)
	if err != nil {
		return nil, fmt.Errorf("auth: export sessions: %w", err)
	}
	out := make([]tenancy.ExportSession, 0, len(sessions))
	for _, sess := range sessions {
		v := viewSession(sess, currentSessionID)
		out = append(out, tenancy.ExportSession{
			ID: v.ID, Current: v.Current, CreatedAt: v.CreatedAt, LastActiveAt: v.LastActiveAt,
			ExpiresAt: v.ExpiresAt, UserAgent: v.UserAgent, IP: v.IP, Method: v.Method,
		})
	}
	return out, nil
}

// ExportPasskeys lists a user's passkey names for the export — no credential IDs or public keys.
func (s *Service) ExportPasskeys(ctx context.Context, authUserID string) ([]tenancy.ExportPasskey, error) {
	if s.passkeys == nil {
		return nil, nil
	}
	keys, err := s.passkeys.FindPasskeysByUserID(ctx, authUserID)
	if err != nil {
		return nil, fmt.Errorf("auth: export passkeys: %w", err)
	}
	out := make([]tenancy.ExportPasskey, 0, len(keys))
	for _, k := range keys {
		p := tenancy.ExportPasskey{ID: fmt.Sprint(k.ID)}
		if k.Name != nil {
			p.Name = *k.Name
		}
		if !k.CreatedAt.IsZero() {
			at := k.CreatedAt.UTC().Truncate(time.Second)
			p.CreatedAt = &at
		}
		out = append(out, p)
	}
	return out, nil
}
