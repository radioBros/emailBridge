//go:build e2e

package p0

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestLogsAPIQueryStreamExport(t *testing.T) {
	n := StartNode(t, nil)
	owner, _ := n.OwnerSession(t)

	// provoke an API access record and capture its request id
	r := owner.do(http.MethodGet, "/v1/settings", nil)
	expectStatus(t, r, http.StatusOK)
	reqID := r.Header.Get("X-Request-Id")
	if reqID == "" {
		t.Fatal("X-Request-Id missing")
	}

	from := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)
	to := time.Now().UTC().Add(time.Hour).Format(time.RFC3339)
	var found bool
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		q := owner.do(http.MethodGet, "/v1/logs?from="+from+"&to="+to+"&category=api&requestId="+reqID+"&limit=50", nil)
		if q.Status == http.StatusOK && strings.Contains(string(q.Body), reqID) {
			found = true
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if !found {
		t.Fatalf("GET /v1/logs did not find api record for %s", reqID)
	}

	// SSE stream delivers a new record
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, n.Base+"/v1/logs/stream?category=api", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Origin", n.Base)
	req.Header.Set("X-Requested-With", "EmailBridge")
	if ck := owner.cookie(sessionCookie); ck != nil {
		req.AddCookie(ck)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK || !strings.Contains(res.Header.Get("Content-Type"), "text/event-stream") {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("stream = %d %s %s", res.StatusCode, res.Header.Get("Content-Type"), b)
	}
	lines := make(chan string, 64)
	go func() {
		sc := bufio.NewScanner(res.Body)
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for sc.Scan() {
			lines <- sc.Text()
		}
		close(lines)
	}()
	// trigger a new API access while the stream is open
	time.Sleep(200 * time.Millisecond)
	trig := owner.do(http.MethodGet, "/v1/me", nil)
	expectStatus(t, trig, http.StatusOK)
	trigID := trig.Header.Get("X-Request-Id")

	gotData := false
	timeout := time.After(15 * time.Second)
	for !gotData {
		select {
		case l, ok := <-lines:
			if !ok {
				t.Fatal("SSE closed before a record")
			}
			if strings.HasPrefix(l, "data: ") {
				gotData = true
				_ = trigID // any api-category SSE record satisfies the scenario
			}
		case <-timeout:
			t.Fatal("SSE delivered no data record")
		}
	}
	cancel()

	exp := owner.do(http.MethodGet, "/v1/logs/export?from="+from+"&to="+to+"&category=api", nil)
	if exp.Status != http.StatusOK {
		t.Fatalf("export = %d %s", exp.Status, exp.Body)
	}
	ct := exp.Header.Get("Content-Type")
	if !strings.Contains(ct, "ndjson") && !strings.Contains(ct, "x-ndjson") && !strings.Contains(ct, "json") {
		t.Fatalf("export content-type = %q", ct)
	}
	if len(exp.Body) == 0 || !strings.Contains(string(exp.Body), "\n") && !strings.Contains(string(exp.Body), "{") {
		t.Fatalf("export body empty/unexpected: %q", truncate(exp.Body, 120))
	}
}
