// Package idempotency makes every POST accept an Idempotency-Key (S2 §2, S1 `idempotencyKeys`): the same
// key with the same request replays the stored response with `Idempotent-Replayed: true`, the same key
// with a different request is `409 idempotency_key_reused`, and a duplicate that arrives while the first is
// still running is `409 idempotency_in_progress`.
//
// It is one middleware after caller resolution, so handlers need no code. The send endpoint keeps its own
// transactional implementation (its record is written in the same transaction as the message, S6 §2.6) and
// is exempt here. Only successful (2xx) responses are stored: a failed request changed nothing, so a retry
// with the same key simply runs again.
//
// Secrets are never stored: before a JSON response is saved, every string field whose name marks a secret
// (the log-redaction rules: `token`, `secret`, `password`, `apiKey` …) is replaced by "[redacted]". A replay
// therefore confirms that the resource exists but never shows a one-time token or secret again. A non-JSON
// success body is not stored at all.
//
// Limit: the record is completed after the handler returns. If the node dies between the handler's commit
// and that write, the record stays "running" until Stale passes, and a retry after that runs the request
// again. Endpoints where a duplicate is harmful keep a transactional record of their own, like submit.
package idempotency

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/logging"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

const (
	// Collection is shared with internal/submit, which owns its TTL index (S1 `idempotencyKeys`).
	Collection = "idempotencyKeys"
	// Header is the request header (S2 §2).
	Header = "Idempotency-Key"
	// ReplayedHeader marks a replayed response.
	ReplayedHeader = "Idempotent-Replayed"

	// maxStored caps the stored response body; larger responses are not stored (a retry runs again).
	maxStored = 1 << 20

	stateRunning = "running"
	stateDone    = "done"
)

// replayHeaders are the response headers kept with a stored response. Never cookies.
var replayHeaders = []string{"Content-Type", "Location", "Content-Location", "ETag"}

// record is one key. Submit's records carry no state and are always complete.
type record struct {
	ID             string            `bson:"_id"`
	OrganizationID string            `bson:"organizationId"`
	Method         string            `bson:"method"`
	Route          string            `bson:"route"`
	RequestHash    string            `bson:"requestHash"`
	State          string            `bson:"state,omitempty"`
	Status         int               `bson:"status,omitempty"`
	Response       string            `bson:"response,omitempty"`
	Headers        map[string]string `bson:"headers,omitempty"`
	CreatedAt      time.Time         `bson:"createdAt"`
	ExpireAt       time.Time         `bson:"expireAt"`
}

// Middleware implements the key handling. Add it after caller resolution (it scopes keys per organisation).
type Middleware struct {
	DB *store.DB
	// TTL is EB_IDEMPOTENCY_TTL (default 24 h).
	TTL time.Duration
	// Stale is when a running record counts as abandoned by a dead node (default 2 × the longest request).
	Stale time.Duration
	Log   *slog.Logger
	Now   func() time.Time
}

func (m *Middleware) now() time.Time {
	if m.Now != nil {
		return m.Now().UTC()
	}
	return time.Now().UTC()
}

func (m *Middleware) ttl() time.Duration {
	if m.TTL > 0 {
		return m.TTL
	}
	return 24 * time.Hour
}

func (m *Middleware) stale() time.Duration {
	if m.Stale > 0 {
		return m.Stale
	}
	return 4 * time.Minute
}

func (m *Middleware) log() *slog.Logger {
	if m.Log != nil {
		return m.Log
	}
	return slog.Default()
}

// Exempt reports paths that handle the key themselves or must never be replayed: sign-in flows (a replay
// would hand out a session) and the send endpoint (transactional record of its own).
func Exempt(path string) bool {
	return strings.HasPrefix(path, "/v1/auth/") || strings.HasSuffix(path, "/outbound-messages")
}

// ValidKey reports a key of 1–255 printable ASCII characters.
func ValidKey(key string) bool {
	if key == "" || len(key) > 255 {
		return false
	}
	for i := 0; i < len(key); i++ {
		if key[i] < 0x20 || key[i] > 0x7e {
			return false
		}
	}
	return true
}

// ID is the record ID: SHA-256 of scope|method|route|key (S1; the account is part of the route here).
func ID(scope, method, route, key string) string {
	sum := sha256.Sum256([]byte(scope + "||" + method + "|" + route + "|" + key))
	return hex.EncodeToString(sum[:])
}

// Hash is the request fingerprint: the query string and the body, JSON in canonical form (key order and
// whitespace do not matter).
func Hash(rawQuery string, body []byte) string {
	canonical := body
	if len(bytes.TrimSpace(body)) > 0 && json.Valid(body) {
		dec := json.NewDecoder(bytes.NewReader(body))
		dec.UseNumber()
		var v any
		if err := dec.Decode(&v); err == nil {
			if b, err := json.Marshal(v); err == nil {
				canonical = b
			}
		}
	}
	h := sha256.New()
	_, _ = h.Write([]byte(rawQuery))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write(canonical)
	return hex.EncodeToString(h.Sum(nil))
}

// Handler is the middleware.
func (m *Middleware) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get(Header)
		if r.Method != http.MethodPost || key == "" || Exempt(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		if !ValidKey(key) {
			httpapi.WriteProblem(w, r, errs.New(errs.CodeValidationFailed, "Idempotency-Key must be 1 to 255 printable ASCII characters."))
			return
		}
		c, ok := tenancy.CallerFrom(r.Context())
		if !ok {
			next.ServeHTTP(w, r) // unauthenticated: the handler answers 401; nothing to protect
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				httpapi.WriteProblem(w, r, errs.New(errs.CodePayloadTooLarge, "The request body is too large."))
				return
			}
			httpapi.WriteProblem(w, r, errs.New(errs.CodeValidationFailed, "The request body could not be read."))
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))

		scope := c.OrganizationID
		if scope == "" {
			scope = "actor:" + c.Actor()
		}
		rec := &record{
			ID: ID(scope, r.Method, r.URL.Path, key), OrganizationID: c.OrganizationID,
			Method: r.Method, Route: r.URL.Path, RequestHash: Hash(r.URL.RawQuery, body),
		}
		owner, replay, err := m.claim(r.Context(), rec)
		switch {
		case err != nil:
			httpapi.WriteProblem(w, r, err)
			return
		case replay != nil:
			writeReplay(w, replay)
			return
		case !owner:
			httpapi.WriteProblem(w, r, errs.New(errs.CodeIdempotencyInProgress, "A request with this Idempotency-Key is still running."))
			return
		}

		rw := &recorder{ResponseWriter: w, status: http.StatusOK}
		finished := false
		defer func() {
			if !finished { // the handler panicked: let a retry run again
				m.release(rec.ID)
			}
		}()
		next.ServeHTTP(rw, r)
		finished = true
		if rw.status >= 200 && rw.status < 300 && !rw.overflow {
			if body, ok := redactSecrets(rw.body.Bytes()); ok {
				m.complete(rec.ID, rw, body)
				return
			}
		}
		m.release(rec.ID)
	})
}

// claim inserts a running record. It returns owner=true when this request runs, a record to replay when an
// identical request already finished, or an error for a reused key.
func (m *Middleware) claim(ctx context.Context, rec *record) (owner bool, replay *record, err error) {
	coll := m.DB.Collection(Collection)
	for range 2 {
		now := m.now()
		rec.State, rec.CreatedAt, rec.ExpireAt = stateRunning, now, now.Add(m.ttl())
		if _, err := coll.InsertOne(ctx, rec); err == nil {
			return true, nil, nil
		} else if !mongo.IsDuplicateKeyError(err) {
			return false, nil, errs.Wrap(err, errs.CodeInternalError, "idempotency.claim")
		}
		var existing record
		err := coll.FindOne(ctx, bson.M{"_id": rec.ID}).Decode(&existing)
		if errors.Is(err, mongo.ErrNoDocuments) {
			continue // expired between the insert and the read
		}
		if err != nil {
			return false, nil, errs.Wrap(err, errs.CodeInternalError, "idempotency.claim")
		}
		if existing.RequestHash != rec.RequestHash {
			return false, nil, errs.New(errs.CodeIdempotencyKeyReused, "This Idempotency-Key was used with a different request.")
		}
		if existing.State != stateRunning {
			return false, &existing, nil
		}
		if now.Sub(existing.CreatedAt) < m.stale() {
			return false, nil, nil
		}
		// The first request's node died: take the key over, once.
		res, err := coll.UpdateOne(ctx,
			bson.M{"_id": rec.ID, "state": stateRunning, "createdAt": existing.CreatedAt},
			bson.M{"$set": bson.M{"createdAt": now, "expireAt": now.Add(m.ttl())}})
		if err != nil {
			return false, nil, errs.Wrap(err, errs.CodeInternalError, "idempotency.claim")
		}
		return res.ModifiedCount == 1, nil, nil
	}
	return false, nil, nil
}

// redactSecrets returns the response body to store: JSON with secret-named string fields replaced, an empty
// body as is, and ok=false for anything else (not stored, so a retry runs again).
func redactSecrets(body []byte) ([]byte, bool) {
	if len(bytes.TrimSpace(body)) == 0 {
		return body, true
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, false
	}
	out, err := json.Marshal(redactValue(v))
	if err != nil {
		return nil, false
	}
	return out, true
}

func redactValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, child := range t {
			if _, isString := child.(string); isString && logging.SensitiveKey(k) {
				t[k] = logging.Redacted
				continue
			}
			t[k] = redactValue(child)
		}
	case []any:
		for i := range t {
			t[i] = redactValue(t[i])
		}
	}
	return v
}

// complete stores the response. It runs after the client may have gone, so it uses its own context.
func (m *Middleware) complete(id string, rw *recorder, body []byte) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	headers := map[string]string{}
	for _, h := range replayHeaders {
		if v := rw.Header().Get(h); v != "" {
			headers[h] = v
		}
	}
	_, err := m.DB.Collection(Collection).UpdateOne(ctx,
		bson.M{"_id": id, "state": stateRunning},
		bson.M{"$set": bson.M{"state": stateDone, "status": rw.status, "response": string(body), "headers": headers}})
	if err != nil {
		m.log().WarnContext(ctx, "cannot store an idempotent response; a retry with the same key will run again", "error", err)
		m.release(id)
	}
}

// release deletes a running record so a retry with the same key runs again.
func (m *Middleware) release(id string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := m.DB.Collection(Collection).DeleteOne(ctx, bson.M{"_id": id, "state": stateRunning}); err != nil {
		m.log().WarnContext(ctx, "cannot release an idempotency key; it frees itself when stale", "error", err)
	}
}

func writeReplay(w http.ResponseWriter, rec *record) {
	for k, v := range rec.Headers {
		w.Header().Set(k, v)
	}
	w.Header().Set(ReplayedHeader, "true")
	status := rec.Status
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	_, _ = io.WriteString(w, rec.Response)
}

// recorder passes the response through and keeps a copy of it (up to maxStored).
type recorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
	body        bytes.Buffer
	overflow    bool
}

func (r *recorder) WriteHeader(code int) {
	if !r.wroteHeader {
		r.status, r.wroteHeader = code, true
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *recorder) Write(b []byte) (int, error) {
	r.wroteHeader = true
	if !r.overflow {
		if r.body.Len()+len(b) > maxStored {
			r.overflow = true
			r.body.Reset()
		} else {
			r.body.Write(b)
		}
	}
	return r.ResponseWriter.Write(b)
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (r *recorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }
