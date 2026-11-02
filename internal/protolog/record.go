package protolog

import (
	"fmt"

	"github.com/radiobros/emailbridge/internal/logging/store"
)

// ProtocolLogRecord is one transcript line as returned by the protocol-logs API (OpenAPI ProtocolLogRecord).
type ProtocolLogRecord struct {
	ID        string `json:"id" doc:"Log record ID (log_…)" example:"log_01m5dckp30mdexhj4qp140dym4"`
	Time      int64  `json:"time" doc:"Nanoseconds UTC since the Unix epoch" example:"1792833262318000000"`
	Dir       string `json:"dir" doc:"C: sent by EmailBridge (client); S: sent by the mail server" example:"S"`
	Proto     string `json:"proto" doc:"imap or smtp" example:"imap"`
	ConnID    string `json:"connId,omitempty" doc:"Connection: <accountId>/main (IMAP sync), <accountId>/sent, or an SMTP session" example:"acc_01m5cvnpb031g57sqct6mzhq94/main"`
	Line      string `json:"line" doc:"The protocol line; passwords, tokens and AUTH payloads are masked, message bodies are omitted unless raw capture is on" example:"* 3 EXISTS"`
	Bytes     int    `json:"bytes,omitempty" doc:"Bytes on the wire for this line" example:"11"`
	LatencyMs int    `json:"latencyMs,omitempty" doc:"Server answer latency in milliseconds, when measured" example:"42"`
	AccountID string `json:"accountId,omitempty" doc:"Account ID (acc_…)" example:"acc_01m5cvnpb031g57sqct6mzhq94"`
}

// ProtocolLogRecordList is the list wrapper of GET …/protocol-logs.
type ProtocolLogRecordList struct {
	Data       []ProtocolLogRecord `json:"data" doc:"Transcript lines, oldest first."`
	NextCursor *string             `json:"nextCursor" doc:"Cursor of the next page; null on the last page." example:"eyJ0IjoxNzkyODMzMjYyMzE4MDAwMDAwLCJpZCI6ImxvZ18wMW01ZGNrcDMwbWRleGhqNHFwMTQwZHltNCJ9"`
	Partial    []string            `json:"partial,omitempty" doc:"Node IDs that missed the query deadline (results may be incomplete)." example:"[\"eb-node-3\"]"`
}

func fromLogRecord(r store.LogRecord) (ProtocolLogRecord, bool) {
	if r.Attrs == nil {
		return ProtocolLogRecord{}, false
	}
	dir, _ := attrString(r.Attrs["dir"])
	proto, _ := attrString(r.Attrs["proto"])
	line, _ := attrString(r.Attrs["line"])
	if dir == "" || proto == "" || line == "" {
		return ProtocolLogRecord{}, false
	}
	out := ProtocolLogRecord{
		ID: r.ID, Time: r.Time, Dir: dir, Proto: proto, Line: line,
		Bytes: attrInt(r.Attrs["bytes"]), LatencyMs: attrInt(r.Attrs["latencyMs"]),
	}
	if c, ok := attrString(r.Attrs["connId"]); ok {
		out.ConnID = c
	}
	if r.AccountID != nil {
		out.AccountID = *r.AccountID
	}
	return out, true
}

func attrString(v any) (string, bool) {
	switch t := v.(type) {
	case string:
		return t, true
	case fmt.Stringer:
		return t.String(), true
	default:
		return "", false
	}
}

func attrInt(v any) int {
	switch t := v.(type) {
	case int:
		return t
	case int64:
		return int(t)
	case float64:
		return int(t)
	default:
		return 0
	}
}

func matchProtoConn(r store.LogRecord, proto, connID string) bool {
	if proto != "" {
		p, _ := attrString(r.Attrs["proto"])
		if p != proto {
			return false
		}
	}
	if connID != "" {
		c, _ := attrString(r.Attrs["connId"])
		if c != connID {
			return false
		}
	}
	return true
}
