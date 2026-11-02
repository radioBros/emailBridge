package logging

import (
	"context"
	"log/slog"
	"time"
)

// Record is one log record after redaction, as stored and mirrored (S12 §3).
type Record struct {
	ID        string         `json:"id"`
	Time      time.Time      `json:"time"`
	Level     slog.Level     `json:"-"`
	LevelName string         `json:"level"`
	Category  Category       `json:"category"`
	Component string         `json:"component,omitempty"`
	Node      string         `json:"node,omitempty"`
	Role      string         `json:"role,omitempty"`
	Msg       string         `json:"msg"`
	IDs       IDs            `json:"-"`
	Kind      string         `json:"kind,omitempty"`
	Code      string         `json:"code,omitempty"`
	Attrs     map[string]any `json:"attrs,omitempty"`
	Stack     string         `json:"stack,omitempty"`
}

// Sink receives batches of records. Write must not retain the slice after returning.
type Sink interface {
	Name() string
	Write(ctx context.Context, batch []Record) error
}
