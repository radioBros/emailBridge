// Package events defines the CloudEvents 1.0 envelope used by EmailBridge webhooks (S3 §1).
package events

import (
	"encoding/json"
	"time"
)

// Envelope is the CloudEvents 1.0 JSON envelope (S3 §1).
type Envelope struct {
	SpecVersion     string          `json:"specversion"`
	ID              string          `json:"id"`
	Type            string          `json:"type"`
	Source          string          `json:"source"`
	Subject         string          `json:"subject,omitempty"`
	Time            time.Time       `json:"time"`
	DataContentType string          `json:"datacontenttype,omitempty"`
	DataSchema      string          `json:"dataschema,omitempty"`
	EBOrg           string          `json:"eborg,omitempty"`
	EBAccount       string          `json:"ebaccount,omitempty"`
	EBSchema        int             `json:"ebschema,omitempty"`
	TraceParent     string          `json:"traceparent,omitempty"`
	Data            json.RawMessage `json:"data,omitempty"`
}
