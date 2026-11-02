package tenancy

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/radiobros/emailbridge/internal/errs"
)

const (
	defaultLimit = 50
	maxLimit     = 200
	totalCap     = 10_000
)

type cursorPayload struct {
	ID    string `json:"id"`
	Key   any    `json:"key,omitempty"`
	Field string `json:"field"`
	Desc  bool   `json:"desc"`
}

func formatETag(v int64) string { return fmt.Sprintf(`"v%d"`, v) }

func parseETag(s string) int64 {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, `"`)
	s = strings.TrimPrefix(s, "v")
	var v int64
	_, _ = fmt.Sscan(s, &v)
	return v
}

func checkIfMatch(ifMatch string, version int64) error {
	if ifMatch != "" && parseETag(ifMatch) != version {
		return errs.New(errs.CodeVersionMismatch, "The resource version does not match If-Match.")
	}
	return nil
}

func encodeCursor(p cursorPayload) string {
	b, _ := json.Marshal(p)
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeCursor(s string) (cursorPayload, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return cursorPayload{}, err
	}
	var p cursorPayload
	if err := json.Unmarshal(b, &p); err != nil {
		return cursorPayload{}, err
	}
	if p.ID == "" {
		return cursorPayload{}, fmt.Errorf("empty id")
	}
	return p, nil
}

func parseListSort(s string, allowed map[string]string) (field string, dir int, err error) {
	if s == "" {
		s = "-id"
	}
	desc := strings.HasPrefix(s, "-")
	name := strings.TrimPrefix(s, "-")
	mapped, ok := allowed[name]
	if !ok {
		return "", 0, errs.New(errs.CodeSortNotAllowed, "Sort field is not allowed.").WithSafe("sort", s)
	}
	dir = 1
	if desc {
		dir = -1
	}
	return mapped, dir, nil
}

func clampLimit(n int) int {
	if n <= 0 {
		return defaultLimit
	}
	if n > maxLimit {
		return maxLimit
	}
	return n
}

func timeKey(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func parseTimeKey(v any) (time.Time, error) {
	s, ok := v.(string)
	if !ok || s == "" {
		return time.Time{}, fmt.Errorf("bad time")
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t, err = time.Parse(time.RFC3339, s)
	}
	return t, err
}

// mayTouchOwners reports whether the caller may invite/change/remove owners (S10 §4: owner yes, admin no).
func mayTouchOwners(c Caller) bool {
	switch c.Kind {
	case CallerSystem:
		return true
	case CallerAPIKey:
		return slices.Contains(c.Scopes, "admin")
	case CallerUser:
		return c.Role == RoleOwner || c.InstanceOwner
	}
	return false
}

func (s *Service) audit(ctx context.Context, action, targetType, id string, changes map[string]FieldDiff) {
	if s == nil || s.Audit == nil {
		return
	}
	if err := s.Audit.Record(ctx, action, targetType, id, changes); err != nil {
		log := s.Log
		if log == nil {
			log = slog.Default()
		}
		log.WarnContext(ctx, "audit record failed", "action", action, "err", err)
	}
}
