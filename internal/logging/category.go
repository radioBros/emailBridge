// Package logging is EmailBridge's internal log system front end (S12 §2–4, §10–11): an asynchronous
// slog.Handler with categories, runtime levels, context IDs, redaction and a bounded buffer that never
// blocks callers. Records go to pluggable sinks — the SQLite store (internal/logging/store, P0.10) and an
// optional stdout mirror.
package logging

import (
	"fmt"
	"log/slog"
	"strings"
)

// Category is one of the closed set of log categories (S12 §4).
type Category string

// The log categories. A new category is added to S12 §4 first.
const (
	CatSystem      Category = "system"
	CatAPI         Category = "api"
	CatAuth        Category = "auth"
	CatAudit       Category = "audit"
	CatEvent       Category = "event"
	CatSync        Category = "sync"
	CatProtocol    Category = "protocol"
	CatSend        Category = "send"
	CatDelivery    Category = "delivery"
	CatRules       Category = "rules"
	CatJob         Category = "job"
	CatCluster     Category = "cluster"
	CatIntegration Category = "integration"
	CatSecurity    Category = "security"
	CatCrash       Category = "crash"
)

// Categories lists every category in S12 §4 order.
var Categories = []Category{CatSystem, CatAPI, CatAuth, CatAudit, CatEvent, CatSync, CatProtocol, CatSend, CatDelivery, CatRules, CatJob,
	CatCluster, CatIntegration, CatSecurity, CatCrash}

// Valid reports whether c is a known category.
func (c Category) Valid() bool {
	for _, k := range Categories {
		if k == c {
			return true
		}
	}
	return false
}

// Levels beyond slog's four (S12 §2).
const (
	LevelTrace = slog.Level(-8)
	LevelDebug = slog.LevelDebug
	LevelInfo  = slog.LevelInfo
	LevelWarn  = slog.LevelWarn
	LevelError = slog.LevelError
	LevelFatal = slog.Level(12)
)

// LevelName returns the lowercase S12 name of l ("trace" … "fatal").
func LevelName(l slog.Level) string {
	switch {
	case l < LevelDebug:
		return "trace"
	case l < LevelInfo:
		return "debug"
	case l < LevelWarn:
		return "info"
	case l < LevelError:
		return "warn"
	case l < LevelFatal:
		return "error"
	default:
		return "fatal"
	}
}

// ParseLevel parses "trace", "debug", "info", "warn", "error" or "fatal" (case-insensitive).
func ParseLevel(s string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "trace":
		return LevelTrace, nil
	case "debug":
		return LevelDebug, nil
	case "info", "":
		return LevelInfo, nil
	case "warn", "warning":
		return LevelWarn, nil
	case "error":
		return LevelError, nil
	case "fatal":
		return LevelFatal, nil
	}
	return 0, fmt.Errorf("logging: unknown level %q (trace, debug, info, warn, error, fatal)", s)
}

// categoryPriority orders categories for disk-cap eviction: lowest priority first (S12 §5).
var categoryPriority = []Category{CatProtocol, CatAPI, CatDelivery, CatEvent, CatSync, CatJob, CatSend, CatRules, CatIntegration, CatSystem,
	CatCluster, CatSecurity, CatAuth, CatCrash, CatAudit}

// EvictionOrder returns the categories in the order their oldest days are deleted under disk pressure.
func EvictionOrder() []Category { return append([]Category(nil), categoryPriority...) }
