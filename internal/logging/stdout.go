package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"sync"
	"sync/atomic"
)

// stdoutSink is the optional stdout mirror (S12 §2): JSON lines or human-readable text.
type stdoutSink struct {
	w      io.Writer
	mu     sync.Mutex
	format atomic.Value // "off", "json", "text"
}

func (s *stdoutSink) Name() string { return "stdout" }

func (s *stdoutSink) setFormat(f string) {
	if f != "json" && f != "text" {
		f = "off"
	}
	s.format.Store(f)
}

func (s *stdoutSink) enabled() bool {
	f, _ := s.format.Load().(string)
	return f == "json" || f == "text"
}

// Write renders the batch in the current format.
func (s *stdoutSink) Write(_ context.Context, batch []Record) error {
	f, _ := s.format.Load().(string)
	var buf bytes.Buffer
	for i := range batch {
		switch f {
		case "json":
			b, err := json.Marshal(flatRecord(&batch[i]))
			if err != nil {
				continue
			}
			buf.Write(b)
			buf.WriteByte('\n')
		case "text":
			writeText(&buf, &batch[i])
		default:
			return nil
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.w.Write(buf.Bytes())
	return err
}

// flatRecord is the JSON shape of a record outside the store: IDs flattened next to the core fields.
func flatRecord(r *Record) map[string]any {
	m := map[string]any{
		"id": r.ID, "time": r.Time.Format("2006-01-02T15:04:05.000000Z07:00"), "level": r.LevelName,
		"category": string(r.Category), "msg": r.Msg,
	}
	add := func(k, v string) {
		if v != "" {
			m[k] = v
		}
	}
	add("component", r.Component)
	add("node", r.Node)
	add("role", r.Role)
	add("kind", r.Kind)
	add("code", r.Code)
	add("stack", r.Stack)
	add("requestId", r.IDs.RequestID)
	add("traceId", r.IDs.TraceID)
	add("spanId", r.IDs.SpanID)
	add("organizationId", r.IDs.OrganizationID)
	add("accountId", r.IDs.AccountID)
	add("eventId", r.IDs.EventID)
	add("deliveryId", r.IDs.DeliveryID)
	add("outboundId", r.IDs.OutboundID)
	add("actor", r.IDs.Actor)
	add("jobId", r.IDs.JobID)
	if len(r.Attrs) > 0 {
		m["attrs"] = r.Attrs
	}
	return m
}

func writeText(buf *bytes.Buffer, r *Record) {
	fmt.Fprintf(buf, "%s %-5s [%s] %s %s", r.Time.Format("2006-01-02T15:04:05.000Z"), r.LevelName, r.Category, r.Component, r.Msg)
	keys := make([]string, 0, len(r.Attrs))
	for k := range r.Attrs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(buf, " %s=%v", k, r.Attrs[k])
	}
	for _, kv := range [][2]string{{"requestId", r.IDs.RequestID}, {"accountId", r.IDs.AccountID}, {"code", r.Code}} {
		if kv[1] != "" {
			fmt.Fprintf(buf, " %s=%s", kv[0], kv[1])
		}
	}
	buf.WriteByte('\n')
}
