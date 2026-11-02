package logging

import (
	"context"
	"log/slog"
	"math/rand/v2"
	"runtime"
	"strings"
	"time"
)

// handler is the slog.Handler behind every logger of a System.
type handler struct {
	s             *System
	cat           Category
	component     string
	attrs         []groupedAttr
	groups        []string
	uncategorised bool
}

type groupedAttr struct {
	prefix string
	attr   slog.Attr
}

// Enabled implements slog.Handler; a disabled level costs one atomic load.
func (h *handler) Enabled(_ context.Context, l slog.Level) bool {
	cs, ok := h.s.settings.Load().Categories[h.cat]
	if !ok {
		return l >= LevelInfo
	}
	return cs.Enabled && l >= cs.Level
}

// Handle implements slog.Handler.
func (h *handler) Handle(ctx context.Context, r slog.Record) error {
	st := h.s.settings.Load()
	if cs, ok := st.Categories[h.cat]; ok && r.Level < LevelWarn {
		if p, has := cs.Sampling[r.Level]; has && p < 1 && rand.Float64() >= p { //nolint:gosec // G404: sampling, not security
			return nil
		}
	}
	if h.uncategorised {
		h.s.counters.uncategorised.Add(1)
	}
	t := r.Time
	if t.IsZero() {
		t = time.Now()
	}
	t = t.UTC()
	rec := Record{
		ID:        h.s.newID(t),
		Time:      t,
		Level:     r.Level,
		LevelName: LevelName(r.Level),
		Category:  h.cat,
		Component: h.component,
		Node:      h.s.o.Node,
		Role:      h.s.o.Role,
		Msg:       r.Message,
		IDs:       IDsFrom(ctx),
	}
	red := redactor{cat: h.cat, hash: st.HashAddresses, hashKey: h.s.o.HashKey}
	attrs := map[string]any{}
	put := func(prefix string, a slog.Attr) {
		if a.Equal(slog.Attr{}) {
			return
		}
		key := prefix + a.Key
		switch strings.ToLower(a.Key) {
		case "kind":
			if prefix == "" {
				rec.Kind = a.Value.Resolve().String()
				return
			}
		case "code":
			if prefix == "" && h.cat != CatAuth {
				rec.Code = a.Value.Resolve().String()
				return
			}
		case "stack":
			if prefix == "" && h.cat == CatCrash {
				rec.Stack = a.Value.Resolve().String()
				return
			}
		case "err", "error":
			if err, ok := a.Value.Resolve().Any().(error); ok && prefix == "" {
				if h.s.o.Classifier != nil {
					k, c := h.s.o.Classifier(err)
					if rec.Kind == "" {
						rec.Kind = k
					}
					if rec.Code == "" {
						rec.Code = c
					}
				}
			}
		}
		attrs[key] = red.attr(a.Key, a.Value)
	}
	for _, ga := range h.attrs {
		put(ga.prefix, ga.attr)
	}
	prefix := groupPrefix(h.groups)
	r.Attrs(func(a slog.Attr) bool {
		put(prefix, a)
		return true
	})
	if len(attrs) > 0 {
		rec.Attrs = attrs
	}
	if rec.Level >= LevelError && h.cat == CatCrash && rec.Stack == "" {
		buf := make([]byte, 16*1024)
		rec.Stack = string(buf[:runtime.Stack(buf, false)])
	}
	h.s.push(rec)
	if rec.Level >= LevelFatal {
		fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		return h.s.Flush(fctx)
	}
	return nil
}

func groupPrefix(groups []string) string {
	if len(groups) == 0 {
		return ""
	}
	return strings.Join(groups, ".") + "."
}

// WithAttrs implements slog.Handler.
func (h *handler) WithAttrs(as []slog.Attr) slog.Handler {
	n := *h
	n.attrs = append(append([]groupedAttr{}, h.attrs...), make([]groupedAttr, 0, len(as))...)
	prefix := groupPrefix(h.groups)
	for _, a := range as {
		n.attrs = append(n.attrs, groupedAttr{prefix: prefix, attr: a})
	}
	return &n
}

// WithGroup implements slog.Handler.
func (h *handler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	n := *h
	n.groups = append(append([]string{}, h.groups...), name)
	return &n
}
