package auth

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/httpapi"
)

// peekLimit bounds how much of an auth request body the guards read (Limen's bodies are tiny).
const peekLimit = 64 << 10

// peekJSON reads the JSON object body of r and restores it for the next handler. It returns nil when the
// body is not a JSON object.
func peekJSON(r *http.Request) map[string]any {
	if r.Body == nil || r.Body == http.NoBody {
		return nil
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, peekLimit+1))
	rest := r.Body
	r.Body = struct {
		io.Reader
		io.Closer
	}{io.MultiReader(bytes.NewReader(raw), rest), rest}
	if err != nil || len(raw) > peekLimit {
		return nil
	}
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return nil
	}
	return m
}

func stringField(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return s
}

// limenString reads a body field the way Limen's validator does (validator.go str/setValue): absent or
// null → ok false; non-string → bad true (Limen rejects); string → TrimSpace'd value.
func limenString(m map[string]any, key string) (value string, ok, bad bool) {
	if m == nil {
		return "", false, false
	}
	v, present := m[key]
	if !present || v == nil {
		return "", false, false
	}
	s, isStr := v.(string)
	if !isStr {
		return "", false, true
	}
	return strings.TrimSpace(s), true, false
}

// route returns the request path relative to /v1/auth ("" when outside it).
func route(r *http.Request) string {
	p, ok := strings.CutPrefix(r.URL.Path, BasePath)
	if !ok {
		return ""
	}
	if p == "" {
		return "/"
	}
	return strings.TrimRight(p, "/")
}

// statusRecorder remembers the status code an inner handler wrote.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (w *statusRecorder) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusRecorder) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(b)
}

func (w *statusRecorder) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *statusRecorder) code() int {
	if w.status == 0 {
		return http.StatusOK
	}
	return w.status
}

// problem writes an EmailBridge problem response.
func problem(w http.ResponseWriter, r *http.Request, code errs.Code, detail string) {
	httpapi.WriteProblem(w, r, errs.New(code, detail))
}

// retryAfter sets Retry-After to the seconds until t (at least 1).
func retryAfter(w http.ResponseWriter, t time.Time) {
	secs := int(time.Until(t).Seconds() + 0.999)
	if secs < 1 {
		secs = 1
	}
	w.Header().Set("Retry-After", strconv.Itoa(secs))
}
