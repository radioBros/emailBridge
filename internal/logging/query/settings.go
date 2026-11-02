package query

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/radiobros/emailbridge/internal/config"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/logging"
)

// LogSettings is the API form of the runtime log settings (S12 §6, OpenAPI LogSettings). In a PATCH every
// field is optional and only the given ones change (JSON Merge Patch on objects); sizes are strings such
// as "1GiB", durations strings such as "14d".
type LogSettings struct {
	Stdout        *string                        `json:"stdout,omitempty" enum:"off,json,text" doc:"Also write logs to stdout: off, json or text." example:"json"`
	DiskCap       *string                        `json:"diskCap,omitempty" doc:"Total disk cap for log files per node (size string, at least 1MiB)." example:"5GiB"`
	HashAddresses *bool                          `json:"hashAddresses,omitempty" doc:"Replace email addresses in log records by a keyed hash." example:"true"`
	Categories    map[string]LogCategorySettings `json:"categories,omitempty" doc:"Per-category settings keyed by category name (system, api, auth, sync …)." example:"{\"sync\":{\"enabled\":true,\"level\":\"info\",\"retention\":\"14d\",\"cap\":\"1GiB\"}}"`
}

// LogCategorySettings are one category's settings.
type LogCategorySettings struct {
	Enabled   *bool        `json:"enabled,omitempty" doc:"Whether the category is written at all." example:"true"`
	Level     *string      `json:"level,omitempty" enum:"trace,debug,info,warn,error,fatal" doc:"Minimum level written." example:"info"`
	Retention *string      `json:"retention,omitempty" doc:"How long records are kept (1h to 3650d)." example:"14d"`
	Cap       *string      `json:"cap,omitempty" doc:"Disk cap of the category per node (at least 1MiB)." example:"1GiB"`
	Sampling  *LogSampling `json:"sampling,omitempty" doc:"Fraction of trace/debug/info records kept (warn and above always kept)."`
}

// LogSampling is the kept fraction per level (warn and above are always kept).
type LogSampling struct {
	Trace *float64 `json:"trace,omitempty" minimum:"0" maximum:"1" doc:"Kept fraction of trace records." example:"0.01"`
	Debug *float64 `json:"debug,omitempty" minimum:"0" maximum:"1" doc:"Kept fraction of debug records." example:"0.25"`
	Info  *float64 `json:"info,omitempty" minimum:"0" maximum:"1" doc:"Kept fraction of info records." example:"1"`
}

// Bounds of the settings values.
const (
	minRetention = time.Hour
	maxRetention = 3650 * 24 * time.Hour
	minCap       = int64(1 << 20)
)

func formatDuration(d time.Duration) string {
	if d > 0 && d%(24*time.Hour) == 0 {
		return fmt.Sprintf("%dd", d/(24*time.Hour))
	}
	return d.String()
}

func p[T any](v T) *T { return &v }

// settingsToAPI renders the current settings.
func settingsToAPI(st logging.Settings) LogSettings {
	stdout := st.Stdout
	if stdout == "" {
		stdout = "off"
	}
	out := LogSettings{Stdout: &stdout, DiskCap: p(config.Size(st.DiskCap).String()), HashAddresses: p(st.HashAddresses),
		Categories: map[string]LogCategorySettings{}}
	for _, c := range logging.Categories {
		cs, ok := st.Categories[c]
		if !ok {
			continue
		}
		api := LogCategorySettings{Enabled: p(cs.Enabled), Level: p(logging.LevelName(cs.Level)),
			Retention: p(formatDuration(cs.Retention)), Cap: p(config.Size(cs.Cap).String())}
		if len(cs.Sampling) > 0 {
			s := &LogSampling{}
			for l, v := range cs.Sampling {
				switch logging.LevelName(l) {
				case "trace":
					s.Trace = p(v)
				case "debug":
					s.Debug = p(v)
				case "info":
					s.Info = p(v)
				}
			}
			api.Sampling = s
		}
		out.Categories[string(c)] = api
	}
	return out
}

// etag is a content hash of the rendered settings ("v<hash>", S2 ETag form).
func etag(s LogSettings) string {
	b, _ := json.Marshal(s)
	sum := sha256.Sum256(b)
	return `"v` + hex.EncodeToString(sum[:8]) + `"`
}

// applyPatch merges patch onto cur and validates every changed value.
func applyPatch(cur logging.Settings, patch LogSettings) (logging.Settings, error) {
	out := cur
	out.Categories = make(map[logging.Category]logging.CategorySettings, len(cur.Categories))
	for c, cs := range cur.Categories {
		if cs.Sampling != nil {
			m := make(map[slog.Level]float64, len(cs.Sampling))
			for k, v := range cs.Sampling {
				m[k] = v
			}
			cs.Sampling = m
		}
		out.Categories[c] = cs
	}
	if patch.Stdout != nil {
		switch *patch.Stdout {
		case "off", "json", "text":
			out.Stdout = *patch.Stdout
		default:
			return cur, validation("stdout must be off, json or text.")
		}
	}
	if patch.DiskCap != nil {
		n, err := config.ParseSize(*patch.DiskCap)
		if err != nil || int64(n) < minCap {
			return cur, validation("diskCap must be a size of at least 1MiB, e.g. \"5GiB\".")
		}
		out.DiskCap = int64(n)
	}
	if patch.HashAddresses != nil {
		out.HashAddresses = *patch.HashAddresses
	}
	names := make([]string, 0, len(patch.Categories))
	for name := range patch.Categories {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		c := logging.Category(name)
		if !c.Valid() {
			return cur, validation(fmt.Sprintf("categories.%s: unknown log category.", name))
		}
		cs, err := applyCategory(out.Categories[c], patch.Categories[name], name)
		if err != nil {
			return cur, err
		}
		out.Categories[c] = cs
	}
	return out, nil
}

func applyCategory(cs logging.CategorySettings, pc LogCategorySettings, name string) (logging.CategorySettings, error) {
	if pc.Enabled != nil {
		cs.Enabled = *pc.Enabled
	}
	if pc.Level != nil {
		l, err := logging.ParseLevel(*pc.Level)
		if err != nil || strings.TrimSpace(*pc.Level) == "" {
			return cs, validation(fmt.Sprintf("categories.%s.level: unknown level.", name))
		}
		cs.Level = l
	}
	if pc.Retention != nil {
		d, err := config.ParseDuration(*pc.Retention)
		if err != nil || d < minRetention || d > maxRetention {
			return cs, validation(fmt.Sprintf("categories.%s.retention: a duration between 1h and 3650d, e.g. \"14d\".", name))
		}
		cs.Retention = d
	}
	if pc.Cap != nil {
		n, err := config.ParseSize(*pc.Cap)
		if err != nil || int64(n) < minCap {
			return cs, validation(fmt.Sprintf("categories.%s.cap: a size of at least 1MiB, e.g. \"1GiB\".", name))
		}
		cs.Cap = int64(n)
	}
	if s := pc.Sampling; s != nil {
		if cs.Sampling == nil {
			cs.Sampling = map[slog.Level]float64{}
		}
		for _, x := range []struct {
			level slog.Level
			v     *float64
		}{{logging.LevelTrace, s.Trace}, {logging.LevelDebug, s.Debug}, {logging.LevelInfo, s.Info}} {
			if x.v == nil {
				continue
			}
			if *x.v < 0 || *x.v > 1 {
				return cs, validation(fmt.Sprintf("categories.%s.sampling: fractions between 0 and 1.", name))
			}
			if *x.v == 1 {
				delete(cs.Sampling, x.level) // keep everything
			} else {
				cs.Sampling[x.level] = *x.v
			}
		}
		if len(cs.Sampling) == 0 {
			cs.Sampling = nil
		}
	}
	return cs, nil
}

func versionMismatch() error {
	return errs.New(errs.CodeVersionMismatch, "The log settings changed since they were read (If-Match).")
}
