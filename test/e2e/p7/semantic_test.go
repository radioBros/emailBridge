//go:build e2e

package p7

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/test/e2e/harness"
	"github.com/radiobros/emailbridge/test/sink"
)

// semanticMock is an OpenAI-compatible fake with /embeddings (bag of words hashed into 64 dimensions: texts
// that share words are close) and /chat/completions (cites the first excerpt it was given).
type semanticMock struct {
	URL         string
	embedCalls  atomic.Int64
	chatCalls   atomic.Int64
	lastPrompt  atomic.Value
	excerptIDRe *regexp.Regexp
}

var semWordRE = regexp.MustCompile(`[\p{L}\p{N}]+`)

func semBag(text string) []float32 {
	v := make([]float32, 64)
	for _, w := range semWordRE.FindAllString(strings.ToLower(text), -1) {
		if len(w) < 3 {
			continue
		}
		h := fnv.New32a()
		_, _ = h.Write([]byte(w))
		v[h.Sum32()%64]++
	}
	return v
}

func startSemanticMock(t *testing.T) *semanticMock {
	t.Helper()
	m := &semanticMock{excerptIDRe: regexp.MustCompile(`EXCERPT \[(msg_[0-9a-z]{26})\]`)}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/embeddings", func(w http.ResponseWriter, r *http.Request) {
		m.embedCalls.Add(1)
		var req struct {
			Input []string `json:"input"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		data := make([]map[string]any, len(req.Input))
		for i, in := range req.Input {
			data[i] = map[string]any{"index": i, "embedding": semBag(in)}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	})
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		m.chatCalls.Add(1)
		var req struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		content := "The messages found do not answer the question."
		for _, msg := range req.Messages {
			if msg.Role == "user" {
				m.lastPrompt.Store(msg.Content)
				if id := m.excerptIDRe.FindStringSubmatch(msg.Content); id != nil {
					content = "The heating is repaired on Thursday [" + id[1] + "]."
				}
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": content}}}})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	m.URL = localhostBaseURL(srv.URL)
	return m
}

func semanticHits(t *testing.T, c *harness.Client, accID, query string) (harness.Resp, []map[string]any) {
	t.Helper()
	r := c.Do(http.MethodGet, "/v1/accounts/"+accID+"/semantic-search?"+query, nil)
	var hits []map[string]any
	if data, ok := r.JSON["data"].([]any); ok {
		for _, raw := range data {
			if m, ok := raw.(map[string]any); ok {
				hits = append(hits, m)
			}
		}
	}
	return r, hits
}

// G1: with full-text search, the AI plugin and ai.embeddings on, a message that arrives is embedded through the
// event stream (full-text indexer → embeddings observer), found by meaning, and used to answer a question with a
// citation; an account-bound key is held to its account; the vectors go with the account.
func TestP7_SemanticSearchAndAsk(t *testing.T) {
	mock := startSemanticMock(t)
	f := startFixture(t, nil, []string{"message.received"}, sink.Options{})

	// Off by default.
	r, _ := semanticHits(t, f.Owner, f.AccID, "q=heating")
	harness.ExpectProblem(t, r, http.StatusForbidden, "feature_disabled")
	st := f.Owner.Do(http.MethodGet, "/v1/accounts/"+f.AccID+"/embeddings", nil)
	harness.ExpectStatus(t, st, http.StatusOK)
	if st.Str("reason") != "switch_off" {
		t.Fatalf("status: %s", st.Body)
	}

	enableOrgSwitch(t, f.Owner, "search.fullText", true)
	enableOrgSwitch(t, f.Owner, "ai.enabled", true)
	enableOrgSwitch(t, f.Owner, "ai.embeddings", true)
	// Not configured yet: the reason says so.
	harness.WaitUntil(t, 30*time.Second, "not configured", func() bool {
		return f.Owner.Do(http.MethodGet, "/v1/accounts/"+f.AccID+"/embeddings", nil).Str("reason") == "not_configured"
	})
	put := f.Owner.Do(http.MethodPut, "/v1/ai/settings", map[string]any{
		"provider": "local", "baseUrl": mock.URL + "/v1", "model": "e2e-chat", "apiKey": "sk-e2e-test",
		"embeddings": map[string]any{"model": "e2e-embed"},
	})
	if put.Status != http.StatusOK || !strings.Contains(string(put.Body), `"effectiveModel":"e2e-embed"`) || strings.Contains(string(put.Body), "sk-e2e-test") {
		t.Fatalf("ai settings: %d %s", put.Status, put.Body)
	}
	harness.WaitUntil(t, 30*time.Second, "semantic search available", func() bool {
		r := f.Owner.Do(http.MethodGet, "/v1/accounts/"+f.AccID+"/embeddings", nil)
		enabled, _ := r.JSON["enabled"].(bool)
		return enabled
	})

	word := fmt.Sprintf("radiator%d", time.Now().UnixNano())
	subj := "Heating repair " + word
	f.inject(subj, "<"+word+"@e2e>", "Good morning, the technician repairs the central heating boiler and the "+word+" on Thursday morning.")
	_ = harness.WaitEvent(t, f.Sink, "message.received", 60*time.Second)
	msgID := waitMessageID(t, f.Owner, f.AccID, subj, 30*time.Second)

	var hit map[string]any
	harness.WaitUntil(t, 90*time.Second, "semantic hit", func() bool {
		r, hits := semanticHits(t, f.Owner, f.AccID, "q="+url.QueryEscape("when is the boiler heating repaired"))
		if r.Status != http.StatusOK || len(hits) == 0 {
			return false
		}
		hit = hits[0]
		return hit["messageId"] == msgID
	})
	if hit["subject"] != subj || hit["mailboxId"] == "" || hit["snippet"] == "" {
		t.Fatalf("hit: %#v", hit)
	}
	if mock.embedCalls.Load() < 2 {
		t.Fatal("the message and the query must go to the embeddings endpoint")
	}

	ask := f.Owner.Do(http.MethodPost, "/v1/accounts/"+f.AccID+"/ask", map[string]any{"question": "When is the heating repaired?"})
	harness.ExpectStatus(t, ask, http.StatusOK)
	cites, _ := ask.JSON["citations"].([]any)
	if len(cites) != 1 || !strings.Contains(ask.Str("answer"), "Thursday") {
		t.Fatalf("ask: %s", ask.Body)
	}
	if c, _ := cites[0].(map[string]any); c["messageId"] != msgID || c["mailboxId"] == "" {
		t.Fatalf("citation: %#v", cites[0])
	}
	if p, _ := mock.lastPrompt.Load().(string); !strings.Contains(p, "EXCERPT ["+msgID+"]") {
		t.Fatalf("prompt: %q", p)
	}

	// Reindex: the message is embedded already.
	rx := f.Owner.Do(http.MethodPost, "/v1/accounts/"+f.AccID+"/embeddings/reindex?limit=100", nil)
	harness.ExpectStatus(t, rx, http.StatusAccepted)
	job, _ := rx.JSON["job"].(map[string]any)
	jobID, _ := job["id"].(string)
	done := f.Owner.WaitJSON(t, "/v1/jobs/"+jobID, 60*time.Second, "embeddings reindex", func(r harness.Resp) bool {
		s := r.Str("state")
		return s == "succeeded" || s == "failed" || s == "cancelled"
	})
	if done.Str("state") != "succeeded" {
		t.Fatalf("reindex job: %s", done.Body)
	}
	if res, _ := done.JSON["result"].(map[string]any); res["alreadyEmbedded"].(float64) < 1 {
		t.Fatalf("reindex result: %#v", res)
	}

	// An account-bound key: its own account works, another is refused.
	keyResp := f.Owner.Do(http.MethodPost, "/v1/api-keys", map[string]any{
		"name": "sem-acc-" + word, "scopes": []string{"messages:read"}, "accountId": f.AccID,
	})
	harness.ExpectStatus(t, keyResp, http.StatusCreated)
	accKey := f.Node.WithBearer(keyResp.Str("token"))
	if r, hits := semanticHits(t, accKey, f.AccID, "q=heating"); r.Status != http.StatusOK || len(hits) == 0 {
		t.Fatalf("bound key: %d %s", r.Status, r.Body)
	}
	other := "acc_" + strings.Repeat("0", 26)
	r, _ = semanticHits(t, accKey, other, "q=heating")
	harness.ExpectProblem(t, r, http.StatusForbidden, "forbidden")

	// The account opts out (the Settings-tab toggle): refused at once, and the vectors are purged through the
	// account.updated event.
	if st := f.Owner.Do(http.MethodGet, "/v1/accounts/"+f.AccID+"/embeddings", nil); st.JSON["messages"].(float64) < 1 {
		t.Fatalf("status before opt-out: %s", st.Body)
	}
	patchAccountSwitches(t, f.Owner, f.AccID, map[string]any{"ai.embeddings": false})
	harness.WaitUntil(t, 30*time.Second, "opt-out refused", func() bool {
		r, _ := semanticHits(t, f.Owner, f.AccID, "q=heating")
		return r.Status == http.StatusForbidden
	})
	harness.WaitUntil(t, 60*time.Second, "opt-out purge", func() bool {
		st := f.Owner.Do(http.MethodGet, "/v1/accounts/"+f.AccID+"/embeddings", nil)
		n, ok := st.JSON["messages"].(float64)
		return st.Str("reason") == "switch_off" && ok && n == 0
	})

	// The vectors go with the account.
	del := f.Owner.Do(http.MethodDelete, "/v1/accounts/"+f.AccID, nil)
	harness.ExpectStatus(t, del, http.StatusAccepted)
	delJob, _ := del.JSON["job"].(map[string]any)
	delID, _ := delJob["id"].(string)
	f.Owner.WaitJSON(t, "/v1/jobs/"+delID, 90*time.Second, "erasure job", func(r harness.Resp) bool {
		return r.Str("state") == "succeeded"
	})
	r, _ = semanticHits(t, f.Owner, f.AccID, "q=heating")
	harness.ExpectProblem(t, r, http.StatusNotFound, "not_found")
}
