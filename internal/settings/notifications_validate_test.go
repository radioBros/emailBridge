package settings

import "testing"

// W75: /v1/me/settings notifications section validation.
func TestValidateNotificationsSettings(t *testing.T) {
	ok := []map[string]any{
		{},
		{"notifications": map[string]any{
			"mutedCategories": []any{"pec"},
			"email": map[string]any{
				"enabled": false, "digest": "daily", "minSeverity": "critical",
				"categories": map[string]any{"webhooks": true},
			},
			"quietHours": map[string]any{"start": "22:00", "end": "07:00", "tz": "Europe/Rome"},
		}},
	}
	for i, s := range ok {
		if err := validateNotificationsSettings(s); err != nil {
			t.Fatalf("ok[%d]: %v", i, err)
		}
	}
	bad := []map[string]any{
		{"notifications": map[string]any{"mutedCategories": "pec"}},
		{"notifications": map[string]any{"mutedCategories": []any{1}}},
		{"notifications": map[string]any{"email": map[string]any{"enabled": "yes"}}},
		{"notifications": map[string]any{"email": map[string]any{"digest": "weekly"}}},
		{"notifications": map[string]any{"email": map[string]any{"minSeverity": "fatal"}}},
		{"notifications": map[string]any{"email": map[string]any{"categories": map[string]any{"webhooks": "on"}}}},
		{"notifications": map[string]any{"email": map[string]any{"categories": map[string]any{"$where": true}}}},
		{"notifications": map[string]any{"quietHours": map[string]any{"start": 22}}},
	}
	for i, s := range bad {
		if err := validateNotificationsSettings(s); err == nil {
			t.Fatalf("bad[%d] accepted: %v", i, s)
		}
	}
}
