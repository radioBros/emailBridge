package mjml

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	mjmlgo "github.com/Boostport/mjml-go"
)

const valid = `<mjml>
  <mj-body>
    <mj-section>
      <mj-column>
        <mj-text>Hello {{name}}</mj-text>
        <mj-button href="https://example.com/i/{{url invoice.number}}?a={{a}}">Open</mj-button>
      </mj-column>
    </mj-section>
  </mj-body>
</mjml>`

func TestCompileValid(t *testing.T) {
	c := New(1, 0)
	if c.Timeout() != DefaultTimeout {
		t.Fatalf("timeout default: %v", c.Timeout())
	}
	html, err := c.Compile(context.Background(), valid)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Hello {{name}}", `href="https://example.com/i/{{url invoice.number}}?a={{a}}"`} {
		if !strings.Contains(html, want) {
			t.Fatalf("compiled HTML lacks %q:\n%s", want, html)
		}
	}
	if !strings.Contains(strings.ToLower(html), "<!doctype html>") {
		t.Fatal("no doctype")
	}
	if strings.Contains(html, "fonts.googleapis.com") {
		t.Fatal("default web-font links must be off")
	}
}

func TestCompileInvalidReportsEveryProblem(t *testing.T) {
	src := "<mjml>\n<mj-body>\n<mj-section>\n<mj-column>\n<mj-text foo=\"bar\">x</mj-text>\n<mj-bogus></mj-bogus>\n" +
		"<mj-image width=\"abc\" src=\"https://example.com/a.png\"/>\n</mj-column>\n</mj-section>\n</mj-body>\n</mjml>"
	_, err := Default().Compile(context.Background(), src)
	var ce *CompileError
	if !errors.As(err, &ce) {
		t.Fatalf("want *CompileError, got %T %v", err, err)
	}
	if len(ce.Messages) != 3 {
		t.Fatalf("want 3 messages, got %q", ce.Messages)
	}
	for _, m := range ce.Messages {
		if !strings.HasPrefix(m, "Line ") || strings.Contains(m, "ValidationError") || strings.Contains(m, " of .") {
			t.Fatalf("message not cleaned: %q", m)
		}
	}
	if !strings.Contains(ce.Messages[1], "mj-bogus") || !strings.HasPrefix(ce.Messages[1], "Line 6,") {
		t.Fatalf("line / tag: %q", ce.Messages[1])
	}
	if !strings.Contains(ce.Error(), "mj-bogus") {
		t.Fatal(ce.Error())
	}
}

func TestCompileNotMJML(t *testing.T) {
	_, err := Default().Compile(context.Background(), "just text, no mjml")
	var ce *CompileError
	if !errors.As(err, &ce) || len(ce.Messages) == 0 {
		t.Fatalf("want *CompileError, got %v", err)
	}
}

func TestCompileTimeoutOnSlowCompiler(t *testing.T) {
	release := make(chan struct{})
	var running atomic.Int32
	c := New(1, 30*time.Millisecond)
	c.toHTML = func(context.Context, string, ...mjmlgo.ToHTMLOption) (string, error) {
		running.Add(1)
		<-release
		return "<html>late</html>", nil
	}
	start := time.Now()
	if _, err := c.Compile(context.Background(), valid); !errors.Is(err, ErrTimeout) {
		t.Fatalf("want ErrTimeout, got %v", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("deadline not honoured")
	}
	// The abandoned compile still holds the only slot: the next call times out waiting for it and never
	// starts a second compile.
	if _, err := c.Compile(context.Background(), valid); !errors.Is(err, ErrTimeout) {
		t.Fatalf("want ErrTimeout while the slot is busy, got %v", err)
	}
	if running.Load() != 1 {
		t.Fatalf("compiles started: %d", running.Load())
	}
	close(release)
	deadline := time.Now().Add(2 * time.Second)
	for len(c.sem) != 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if len(c.sem) != 0 {
		t.Fatal("slot not released after the abandoned compile ended")
	}
}

func TestCompileCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := New(1, time.Second).Compile(ctx, valid); !errors.Is(err, ErrTimeout) {
		t.Fatalf("want ErrTimeout, got %v", err)
	}
}

func TestCompileRealTimeout(t *testing.T) {
	big := "<mjml><mj-body>" + strings.Repeat(`<mj-section><mj-column><mj-text>Hello {{name}}</mj-text></mj-column></mj-section>`, 150) +
		"</mj-body></mjml>"
	if _, err := New(1, time.Millisecond).Compile(context.Background(), big); !errors.Is(err, ErrTimeout) {
		t.Fatalf("want ErrTimeout, got %v", err)
	}
}

func TestCompilePanicAndFailureAndEmpty(t *testing.T) {
	c := New(1, time.Second)
	c.toHTML = func(context.Context, string, ...mjmlgo.ToHTMLOption) (string, error) { panic("boom") }
	if _, err := c.Compile(context.Background(), valid); err == nil || !strings.Contains(err.Error(), "panic") {
		t.Fatalf("panic: %v", err)
	}
	if len(c.sem) != 0 {
		t.Fatal("slot leaked after panic")
	}
	c.toHTML = func(context.Context, string, ...mjmlgo.ToHTMLOption) (string, error) {
		return "", errors.New("wasm trap")
	}
	_, err := c.Compile(context.Background(), valid)
	var ce *CompileError
	if err == nil || errors.As(err, &ce) || errors.Is(err, ErrTimeout) {
		t.Fatalf("internal failure must be neither CompileError nor timeout: %v", err)
	}
	c.toHTML = func(context.Context, string, ...mjmlgo.ToHTMLOption) (string, error) { return "  ", nil }
	if _, err := c.Compile(context.Background(), valid); !errors.As(err, &ce) {
		t.Fatalf("empty output: %v", err)
	}
	c.toHTML = func(context.Context, string, ...mjmlgo.ToHTMLOption) (string, error) {
		return "", &mjmlgo.Error{Message: "pointer form"}
	}
	if _, err := c.Compile(context.Background(), valid); !errors.As(err, &ce) || ce.Messages[0] != "pointer form" {
		t.Fatalf("pointer error: %v", err)
	}
}

func TestMessagesOf(t *testing.T) {
	e := mjmlgo.Error{Message: "x"}
	e.Details = append(e.Details, struct {
		Line    int    `json:"line"`
		Message string `json:"message"`
		TagName string `json:"tagName"`
	}{Line: 4, Message: "bad", TagName: "mj-text"})
	if got := messagesOf(e); len(got) != 1 || got[0] != "Line 4, mj-text: bad" {
		t.Fatalf("details: %q", got)
	}
	long := strings.Repeat("a", maxMessageLen+50)
	var lines []string
	for i := 0; i < maxMessages+5; i++ {
		lines = append(lines, long)
	}
	got := messagesOf(mjmlgo.Error{Message: strings.Join(lines, "\n")})
	if len(got) != maxMessages || !strings.HasSuffix(got[0], "…") {
		t.Fatalf("caps: %d %q", len(got), got[0][len(got[0])-5:])
	}
	if got := messagesOf(mjmlgo.Error{Message: "ValidationError: \n"}); len(got) != 1 || got[0] != "Invalid MJML." {
		t.Fatalf("fallback: %q", got)
	}
	if (&CompileError{}).Error() != "mjml: invalid MJML" {
		t.Fatal("empty CompileError text")
	}
}
