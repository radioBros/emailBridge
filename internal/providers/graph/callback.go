package graph

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sync"
)

// Nudger wakes an account sync after a Graph notification.
// accountID is the EmailBridge account (Claude wires this to accounts.Service.NudgeSync).
type Nudger interface {
	NudgeGraph(ctx context.Context, accountID string) error
}

// CallbackHandler serves POST /callbacks/graph (validation + notifications).
// In-memory maps are an optimisation; MongoDB StateRepo is authoritative so any api node can accept
// notifications created on a sync node.
type CallbackHandler struct {
	Mu            sync.RWMutex
	State         map[string]*SyncState // subscriptionId → state (cache)
	ByClientState map[string]*SyncState
	Repo          *StateRepo
	Nudge         Nudger
}

// Register caches sync state by subscription (optional optimisation after create/renew).
func (h *CallbackHandler) Register(st *SyncState) {
	if st == nil || st.SubscriptionID == "" {
		return
	}
	h.Mu.Lock()
	defer h.Mu.Unlock()
	if h.State == nil {
		h.State = map[string]*SyncState{}
	}
	if h.ByClientState == nil {
		h.ByClientState = map[string]*SyncState{}
	}
	h.State[st.SubscriptionID] = st
	if st.ClientState != "" {
		h.ByClientState[st.ClientState] = st
	}
}

const maxValidationToken = 512

// ServeHTTP implements http.Handler.
func (h *CallbackHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method", http.StatusMethodNotAllowed)
		return
	}
	if tok := r.URL.Query().Get("validationToken"); tok != "" {
		if err := validateValidationToken(tok); err != nil {
			http.Error(w, "bad validationToken", http.StatusBadRequest)
			return
		}
		// Graph subscription handshake requires echoing the token as text/plain.
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(tok)) //nolint:gosec // G705: required Graph validation echo, not HTML
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "read", http.StatusBadRequest)
		return
	}
	var env2 struct {
		Value []struct {
			SubscriptionID string `json:"subscriptionId"`
			ClientState    string `json:"clientState"`
			ChangeType     string `json:"changeType"`
			LifecycleEvent string `json:"lifecycleEvent"`
		} `json:"value"`
	}
	if err := json.Unmarshal(body, &env2); err != nil {
		http.Error(w, "json", http.StatusBadRequest)
		return
	}
	for _, n := range env2.Value {
		st := h.lookup(r.Context(), n.SubscriptionID, n.ClientState)
		if st == nil || subtle.ConstantTimeCompare([]byte(st.ClientState), []byte(n.ClientState)) != 1 {
			http.Error(w, `{"title":"Unauthorized","status":401}`, http.StatusUnauthorized)
			return
		}
		if h.Nudge != nil && st.AccountID != "" {
			_ = h.Nudge.NudgeGraph(r.Context(), st.AccountID)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, `{"ok":true}`)
}

func (h *CallbackHandler) lookup(ctx context.Context, subID, clientState string) *SyncState {
	h.Mu.RLock()
	if subID != "" && h.State != nil {
		if st := h.State[subID]; st != nil {
			h.Mu.RUnlock()
			return st
		}
	}
	if clientState != "" && h.ByClientState != nil {
		if st := h.ByClientState[clientState]; st != nil {
			h.Mu.RUnlock()
			return st
		}
	}
	h.Mu.RUnlock()

	if h.Repo != nil && subID != "" {
		st, err := h.Repo.BySubscription(ctx, subID)
		if err == nil && st != nil {
			h.Register(st)
			return st
		}
	}
	return nil
}

// HandleValidation is a convenience for tests: returns the response body for a validation request.
func HandleValidation(w http.ResponseWriter, validationToken string) {
	if err := validateValidationToken(validationToken); err != nil {
		http.Error(w, "bad validationToken", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(validationToken))
}

func validateValidationToken(tok string) error {
	if len(tok) > maxValidationToken {
		return errValidationToken
	}
	for i := 0; i < len(tok); i++ {
		c := tok[i]
		if c < 0x20 && c != '\t' {
			return errValidationToken
		}
	}
	return nil
}

var errValidationToken = errors.New("invalid validationToken")
