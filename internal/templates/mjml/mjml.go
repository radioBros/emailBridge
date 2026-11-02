// Package mjml compiles MJML (responsive e-mail markup) to HTML for send templates (G2).
//
// It wraps github.com/Boostport/mjml-go, the MJML reference compiler built to WebAssembly and run by
// wazero: pure Go, no cgo, no Node.js. The library prepares its WASM runtime and a pool of compiler
// instances once, in its package init at process start, so the first compile never pays that cost on a
// request path. What this wrapper adds:
//
//   - a hard deadline: the library's runtime does not stop a running compile when the context ends, so
//     Compile waits on its own and returns ErrTimeout at the deadline (the abandoned compile finishes in
//     the background and only then frees its slot);
//   - a concurrency cap (a semaphore shared by every caller of one Compiler), which also caps how many
//     WASM instances the library's pool creates;
//   - typed results: invalid MJML is a *CompileError with one readable message per problem;
//   - fixed options: strict validation, and no automatic web-font links (privacy: MJML's default font
//     list makes every recipient's mail client call Google Fonts; authors who want a web font declare it
//     with <mj-font>).
package mjml

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	mjmlgo "github.com/Boostport/mjml-go"
)

// Defaults of the shared compiler.
const (
	// DefaultTimeout bounds one compile, including the wait for a free slot.
	DefaultTimeout = 10 * time.Second
	// DefaultConcurrency is the number of compiles that may run at the same time per process.
	DefaultConcurrency = 2
	// maxMessages caps the problems reported for one source; maxMessageLen caps each message.
	maxMessages   = 20
	maxMessageLen = 300
)

// ErrTimeout means the compile did not finish before the deadline (or no slot became free in time).
var ErrTimeout = errors.New("mjml: compile did not finish in time")

// CompileError is invalid MJML: Messages holds one readable message per problem the compiler reported,
// for example "Line 3, mj-image: Attribute width has invalid value: abc …".
type CompileError struct {
	Messages []string
}

// Error implements error.
func (e *CompileError) Error() string {
	if e == nil || len(e.Messages) == 0 {
		return "mjml: invalid MJML"
	}
	return "mjml: " + strings.Join(e.Messages, "; ")
}

type toHTMLFunc func(ctx context.Context, src string, opts ...mjmlgo.ToHTMLOption) (string, error)

// Compiler compiles MJML with a deadline and a concurrency cap. The zero value is not usable; use New
// or Default.
type Compiler struct {
	sem     chan struct{}
	timeout time.Duration
	toHTML  toHTMLFunc
}

// New returns a compiler that runs at most concurrency compiles at once (minimum 1) and gives each at
// most timeout (0: DefaultTimeout).
func New(concurrency int, timeout time.Duration) *Compiler {
	if concurrency < 1 {
		concurrency = 1
	}
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	return &Compiler{sem: make(chan struct{}, concurrency), timeout: timeout, toHTML: mjmlgo.ToHTML}
}

var (
	defaultOnce     sync.Once
	defaultCompiler *Compiler
)

// Default returns the process-wide compiler (DefaultConcurrency, DefaultTimeout), created on first use.
func Default() *Compiler {
	defaultOnce.Do(func() { defaultCompiler = New(DefaultConcurrency, DefaultTimeout) })
	return defaultCompiler
}

// Timeout is the per-compile deadline.
func (c *Compiler) Timeout() time.Duration { return c.timeout }

type result struct {
	html string
	err  error
}

// Compile turns MJML into HTML. Errors: *CompileError (invalid MJML), ErrTimeout (deadline or no free
// slot), anything else is an internal compiler failure.
func (c *Compiler) Compile(ctx context.Context, src string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	if ctx.Err() != nil {
		return "", ErrTimeout
	}
	select {
	case c.sem <- struct{}{}:
	case <-ctx.Done():
		return "", ErrTimeout
	}
	done := make(chan result, 1)
	go c.run(ctx, src, done)
	select {
	case r := <-done:
		return classify(r)
	case <-ctx.Done():
		return "", ErrTimeout
	}
}

// run holds a slot until the compile really ends (also after the caller gave up), so abandoned compiles
// can never pile up beyond the concurrency cap.
func (c *Compiler) run(ctx context.Context, src string, done chan<- result) {
	defer func() { <-c.sem }()
	defer func() {
		if r := recover(); r != nil {
			done <- result{err: fmt.Errorf("mjml: compiler panic: %v", r)}
		}
	}()
	html, err := c.toHTML(ctx, src,
		mjmlgo.WithValidationLevel(mjmlgo.Strict),
		mjmlgo.WithFonts(map[string]string{}),
	)
	done <- result{html: html, err: err}
}

func classify(r result) (string, error) {
	if r.err != nil {
		var me mjmlgo.Error
		if errors.As(r.err, &me) {
			return "", &CompileError{Messages: messagesOf(me)}
		}
		var mp *mjmlgo.Error
		if errors.As(r.err, &mp) && mp != nil {
			return "", &CompileError{Messages: messagesOf(*mp)}
		}
		return "", fmt.Errorf("mjml: compiler failure: %w", r.err)
	}
	if strings.TrimSpace(r.html) == "" {
		return "", &CompileError{Messages: []string{"The MJML compiler produced no HTML."}}
	}
	return r.html, nil
}

// lineRe matches the compiler's "Line 3 of <file> (mj-image) — message" validation lines.
var lineRe = regexp.MustCompile(`^Line (\d+) of [^(]*\(([^)]*)\)\s*[—-]+\s*(.*)$`)

func messagesOf(e mjmlgo.Error) []string {
	var out []string
	add := func(m string) {
		m = strings.TrimSpace(m)
		if m == "" || len(out) >= maxMessages {
			return
		}
		if len(m) > maxMessageLen {
			m = m[:maxMessageLen] + "…"
		}
		out = append(out, m)
	}
	for _, d := range e.Details {
		add(fmt.Sprintf("Line %d, %s: %s", d.Line, d.TagName, d.Message))
	}
	if len(out) == 0 {
		for _, line := range strings.Split(e.Message, "\n") {
			line = strings.TrimSpace(line)
			line = strings.TrimSpace(strings.TrimPrefix(line, "ValidationError:"))
			if m := lineRe.FindStringSubmatch(line); m != nil {
				line = "Line " + m[1] + ", " + m[2] + ": " + strings.TrimSpace(m[3])
			}
			add(line)
		}
	}
	if len(out) == 0 {
		out = []string{"Invalid MJML."}
	}
	return out
}
