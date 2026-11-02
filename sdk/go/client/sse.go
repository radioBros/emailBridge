package client

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// SSEEvent is one Server-Sent Event from GET /v1/events/stream.
type SSEEvent struct {
	ID    string
	Event string
	Data  string
}

// StreamEventsOptions configures StreamEvents.
type StreamEventsOptions struct {
	Types       string
	AccountID   string
	LastEventID string
	Header      http.Header
	OnEvent     func(SSEEvent) error
}

// StreamEvents consumes GET /v1/events/stream until the body ends or ctx is cancelled.
// Sends Last-Event-ID when LastEventID is set.
func (c *Client) StreamEvents(ctx context.Context, opts StreamEventsOptions) error {
	if opts.OnEvent == nil {
		return fmt.Errorf("client: StreamEvents OnEvent is required")
	}
	u, err := url.Parse(c.baseURL + "/v1/events/stream")
	if err != nil {
		return err
	}
	q := u.Query()
	if opts.Types != "" {
		q.Set("types", opts.Types)
	}
	if opts.AccountID != "" {
		q.Set("accountId", opts.AccountID)
	}
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	for k, vs := range c.header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	for k, vs := range opts.Header {
		for _, v := range vs {
			req.Header.Set(k, v)
		}
	}
	if c.organizationID != "" && req.Header.Get("EB-Organization") == "" {
		req.Header.Set("EB-Organization", c.organizationID)
	}
	if opts.LastEventID != "" {
		req.Header.Set("Last-Event-ID", opts.LastEventID)
	}

	res, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		return fmt.Errorf("client: SSE stream failed: %d %s", res.StatusCode, strings.TrimSpace(string(raw)))
	}
	return readSSE(res.Body, opts.OnEvent)
}

func readSSE(r io.Reader, onEvent func(SSEEvent) error) error {
	sc := bufio.NewScanner(r)
	// SSE events can be larger than the default 64KiB token.
	buf := make([]byte, 0, 64*1024)
	sc.Buffer(buf, 1024*1024)

	var block strings.Builder
	flush := func() error {
		b := block.String()
		block.Reset()
		ev := parseSSEBlock(b)
		if ev == nil {
			return nil
		}
		return onEvent(*ev)
	}

	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			if err := flush(); err != nil {
				return err
			}
			continue
		}
		if block.Len() > 0 {
			block.WriteByte('\n')
		}
		block.WriteString(line)
	}
	if err := sc.Err(); err != nil {
		return err
	}
	return flush()
}

func parseSSEBlock(block string) *SSEEvent {
	block = strings.ReplaceAll(block, "\r", "")
	var id, event string
	var data []string
	for _, line := range strings.Split(block, "\n") {
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		field, value, _ := strings.Cut(line, ":")
		if strings.HasPrefix(value, " ") {
			value = value[1:]
		}
		switch field {
		case "id":
			id = value
		case "event":
			event = value
		case "data":
			data = append(data, value)
		}
	}
	if id == "" && event == "" && len(data) == 0 {
		return nil
	}
	return &SSEEvent{ID: id, Event: event, Data: strings.Join(data, "\n")}
}
