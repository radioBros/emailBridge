package gmail

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
)

// OIDCVerifier validates the Pub/Sub push Authorization bearer token.
type OIDCVerifier interface {
	Verify(ctx context.Context, rawToken, audience string) error
}

// StaticVerifier accepts a single test token (simulator / unit tests).
type StaticVerifier struct {
	Token string
}

// Verify implements OIDCVerifier.
func (v StaticVerifier) Verify(_ context.Context, rawToken, _ string) error {
	if v.Token != "" && rawToken == v.Token {
		return nil
	}
	return errUnauthorized
}

var errUnauthorized = &httpError{code: http.StatusUnauthorized, msg: "invalid oidc token"}

type httpError struct {
	code int
	msg  string
}

func (e *httpError) Error() string { return e.msg }

// NudgeFunc wakes the sync runner for an account after a push.
type NudgeFunc func(ctx context.Context, accountID string)

// PushHandler serves POST /callbacks/gmail.
type PushHandler struct {
	Audience string // {EB_CALLBACK_URL}/callbacks/gmail
	Verify   OIDCVerifier
	Lookup   FindByEmail
	Nudge    NudgeFunc
	Log      *slog.Logger
	// RequirePushSA, when true (EB_ENV=production), refuses requests unless PushServiceAccount is set.
	RequirePushSA      bool
	PushServiceAccount string

	mu   sync.Mutex
	seen map[string]struct{} // pubsub messageId dedupe (bounded)
}

const seenCap = 10_000

// ServeHTTP implements http.Handler.
func (h *PushHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.RequirePushSA && strings.TrimSpace(h.PushServiceAccount) == "" {
		if h.Log != nil {
			h.Log.Warn("gmail push refused: set EB_GMAIL_PUSH_SERVICE_ACCOUNT in production")
		}
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	auth := r.Header.Get("Authorization")
	raw := strings.TrimPrefix(auth, "Bearer ")
	if raw == "" || raw == auth {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if h.Verify == nil || h.Verify.Verify(r.Context(), raw, h.Audience) != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	var env struct {
		Message struct {
			Data        string `json:"data"`
			MessageID   string `json:"messageId"`
			PublishTime string `json:"publishTime"`
		} `json:"message"`
		Subscription string `json:"subscription"`
	}
	if err := json.Unmarshal(body, &env); err != nil || env.Message.Data == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if env.Message.MessageID != "" && h.duplicate(env.Message.MessageID) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	rawData, err := base64.StdEncoding.DecodeString(env.Message.Data)
	if err != nil {
		rawData, err = base64.URLEncoding.DecodeString(env.Message.Data)
	}
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	var notify struct {
		EmailAddress string `json:"emailAddress"`
		HistoryID    any    `json:"historyId"`
	}
	if err := json.Unmarshal(rawData, &notify); err != nil || notify.EmailAddress == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if h.Lookup == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	accID, _, err := h.Lookup(r.Context(), strings.ToLower(notify.EmailAddress))
	if err != nil || accID == "" {
		// Ack to avoid Pub/Sub retries for unknown mailboxes.
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if h.Nudge != nil {
		h.Nudge(r.Context(), accID)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *PushHandler) duplicate(id string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.seen == nil {
		h.seen = map[string]struct{}{}
	}
	if _, ok := h.seen[id]; ok {
		return true
	}
	if len(h.seen) >= seenCap {
		h.seen = map[string]struct{}{}
	}
	h.seen[id] = struct{}{}
	return false
}
