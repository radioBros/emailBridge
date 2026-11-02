package throttle

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Window is a send-window configuration (P5.5 §4).
type Window struct {
	TimeZone string `json:"timeZone"`
	Days     []int  `json:"days"`  // Monday=1 .. Sunday=7; empty = every day
	Start    string `json:"start"` // HH:MM
	End      string `json:"end"`   // HH:MM exclusive
}

// ParseWindow decodes JSON; empty → nil (no restriction).
func ParseWindow(raw any) (*Window, error) {
	if raw == nil {
		return nil, nil
	}
	var b []byte
	switch v := raw.(type) {
	case string:
		if strings.TrimSpace(v) == "" || v == "{}" {
			return nil, nil
		}
		b = []byte(v)
	case []byte:
		if len(v) == 0 || string(v) == "{}" {
			return nil, nil
		}
		b = v
	case map[string]any:
		if len(v) == 0 {
			return nil, nil
		}
		b, _ = json.Marshal(v)
	default:
		b, _ = json.Marshal(v)
	}
	var w Window
	if err := json.Unmarshal(b, &w); err != nil {
		return nil, err
	}
	if strings.TrimSpace(w.Start) == "" && strings.TrimSpace(w.End) == "" {
		return nil, nil
	}
	if _, _, err := parseHHMM(w.Start); err != nil {
		return nil, fmt.Errorf("start: %w", err)
	}
	if _, _, err := parseHHMM(w.End); err != nil {
		return nil, fmt.Errorf("end: %w", err)
	}
	return &w, nil
}

func parseHHMM(s string) (h, m int, err error) {
	parts := strings.Split(strings.TrimSpace(s), ":")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("want HH:MM")
	}
	h, err = strconv.Atoi(parts[0])
	if err != nil || h < 0 || h > 23 {
		return 0, 0, fmt.Errorf("bad hour")
	}
	m, err = strconv.Atoi(parts[1])
	if err != nil || m < 0 || m > 59 {
		return 0, 0, fmt.Errorf("bad minute")
	}
	return h, m, nil
}

func (w *Window) loc() *time.Location {
	if w == nil || strings.TrimSpace(w.TimeZone) == "" {
		return time.UTC
	}
	loc, err := time.LoadLocation(w.TimeZone)
	if err != nil {
		return time.UTC
	}
	return loc
}

func (w *Window) dayAllowed(isoDow int) bool {
	if w == nil || len(w.Days) == 0 {
		return true
	}
	for _, d := range w.Days {
		if d == isoDow {
			return true
		}
	}
	return false
}

// isoWeekday returns Monday=1 … Sunday=7.
func isoWeekday(t time.Time) int {
	wd := int(t.Weekday())
	if wd == 0 {
		return 7
	}
	return wd
}

// Contains reports whether t falls inside the window in the configured zone.
func (w *Window) Contains(t time.Time) bool {
	if w == nil {
		return true
	}
	loc := w.loc()
	local := t.In(loc)
	if !w.dayAllowed(isoWeekday(local)) {
		return false
	}
	sh, sm, _ := parseHHMM(w.Start)
	eh, em, _ := parseHHMM(w.End)
	startMin := sh*60 + sm
	endMin := eh*60 + em
	cur := local.Hour()*60 + local.Minute()
	if startMin == endMin {
		return true // degenerate: always open on allowed days
	}
	if startMin < endMin {
		return cur >= startMin && cur < endMin
	}
	// overnight
	return cur >= startMin || cur < endMin
}

// NextOpen returns the next instant at or after t when the window opens (UTC).
func (w *Window) NextOpen(t time.Time) time.Time {
	if w == nil || w.Contains(t) {
		return t.UTC()
	}
	loc := w.loc()
	sh, sm, _ := parseHHMM(w.Start)
	local := t.In(loc)
	// Search up to 8 days ahead for the next allowed day+start.
	for i := 0; i < 8; i++ {
		day := local.AddDate(0, 0, i)
		candidate := time.Date(day.Year(), day.Month(), day.Day(), sh, sm, 0, 0, loc)
		if i == 0 && !candidate.After(local) {
			// today's start already passed — try end-of-overnight or tomorrow
			eh, em, _ := parseHHMM(w.End)
			startMin := sh*60 + sm
			endMin := eh*60 + em
			cur := local.Hour()*60 + local.Minute()
			if startMin > endMin && cur < endMin {
				// still inside overnight from yesterday: already "open" — shouldn't reach here
				return t.UTC()
			}
			continue
		}
		if w.dayAllowed(isoWeekday(candidate)) && !candidate.Before(local) {
			return candidate.UTC()
		}
		if i == 0 {
			continue
		}
		if w.dayAllowed(isoWeekday(candidate)) {
			return candidate.UTC()
		}
	}
	// Fallback: tomorrow at start
	n := local.AddDate(0, 0, 1)
	return time.Date(n.Year(), n.Month(), n.Day(), sh, sm, 0, 0, loc).UTC()
}
