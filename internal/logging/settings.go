package logging

import (
	"log/slog"
	"maps"
	"time"
)

// CategorySettings are the runtime settings of one category (S12 §4, §6).
type CategorySettings struct {
	Enabled   bool
	Level     slog.Level
	Retention time.Duration
	Cap       int64 // bytes
	// Sampling keeps this fraction of records per level (trace/debug/info only; warn+ always kept).
	Sampling map[slog.Level]float64
}

// Settings are the runtime settings of the log system. They are replaced atomically (System.Apply).
type Settings struct {
	Categories    map[Category]CategorySettings
	Stdout        string // "off", "json", "text"
	HashAddresses bool
	DiskCap       int64
}

const (
	kib = int64(1024)
	mib = 1024 * kib
	gib = 1024 * mib
	day = 24 * time.Hour
)

// DefaultSettings returns the S12 §4 defaults.
func DefaultSettings() Settings {
	c := func(level slog.Level, retention time.Duration, cap int64) CategorySettings {
		return CategorySettings{Enabled: true, Level: level, Retention: retention, Cap: cap}
	}
	return Settings{
		Stdout:  "off",
		DiskCap: 5 * gib,
		Categories: map[Category]CategorySettings{
			CatSystem:      c(LevelInfo, 30*day, 512*mib),
			CatAPI:         c(LevelInfo, 14*day, gib),
			CatAuth:        c(LevelInfo, 90*day, 256*mib),
			CatAudit:       c(LevelInfo, 365*day, 256*mib),
			CatEvent:       c(LevelInfo, 14*day, gib),
			CatSync:        c(LevelInfo, 14*day, gib),
			CatProtocol:    c(LevelDebug, 7*day, 2*gib),
			CatSend:        c(LevelInfo, 30*day, 512*mib),
			CatDelivery:    c(LevelInfo, 14*day, gib),
			CatRules:       c(LevelInfo, 14*day, 256*mib),
			CatJob:         c(LevelInfo, 14*day, 512*mib),
			CatCluster:     c(LevelInfo, 30*day, 256*mib),
			CatIntegration: c(LevelInfo, 14*day, 256*mib),
			CatSecurity:    c(LevelInfo, 90*day, 256*mib),
			CatCrash:       c(LevelError, 90*day, 128*mib),
		},
	}
}

// WithDefaultLevel returns a copy where every category whose level equals its S12 default is set to
// level instead (EB_LOG_LEVEL is a floor default, S9 §2.7). Protocol and crash keep their own defaults.
func (s Settings) WithDefaultLevel(level slog.Level) Settings {
	out := s.clone()
	for c, cs := range out.Categories {
		if c == CatProtocol || c == CatCrash {
			continue
		}
		cs.Level = level
		out.Categories[c] = cs
	}
	return out
}

// WithRetention applies EB_LOG_RETENTION to every category except the compliance ones (audit, auth,
// security, crash), which keep their longer S12 defaults. The store (P0.10) enforces retention.
func (s Settings) WithRetention(d time.Duration) Settings {
	out := s.clone()
	if d <= 0 {
		return out
	}
	for c, cs := range out.Categories {
		if c == CatAudit || c == CatAuth || c == CatSecurity || c == CatCrash {
			continue // compliance categories keep their longer defaults
		}
		cs.Retention = d
		out.Categories[c] = cs
	}
	return out
}

func (s Settings) clone() Settings {
	out := s
	out.Categories = make(map[Category]CategorySettings, len(s.Categories))
	for c, cs := range s.Categories {
		if cs.Sampling != nil {
			cs.Sampling = maps.Clone(cs.Sampling)
		}
		out.Categories[c] = cs
	}
	return out
}
