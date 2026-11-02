//go:build e2e

package p7

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/test/e2e/harness"
	"github.com/radiobros/emailbridge/test/sink"
)

// searchHits runs GET /v1/messages/search and returns the hits.
func searchHits(t *testing.T, c *harness.Client, query string) (harness.Resp, []map[string]any) {
	t.Helper()
	r := c.Do(http.MethodGet, "/v1/messages/search?"+query, nil)
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

// G3: a message that arrives is indexed through the event stream and found by organisation-wide full-text
// search; the reindex job runs; an account-bound key is held to its account; the rows go with the account.
func TestP7_FullTextSearch(t *testing.T) {
	f := startFixture(t, nil, []string{"message.received"}, sink.Options{})
	word := fmt.Sprintf("zebrafish%d", time.Now().UnixNano())

	// Off by default: the organisation is told so.
	r, _ := searchHits(t, f.Owner, "q="+word)
	harness.ExpectProblem(t, r, http.StatusForbidden, "feature_disabled")

	enableOrgSwitch(t, f.Owner, "search.fullText", true)
	subj := "Full-text " + word
	f.inject(subj, "<"+word+"@e2e>", "Hello, the order of the "+word+" tank ships tomorrow. Regards.")
	_ = harness.WaitEvent(t, f.Sink, "message.received", 60*time.Second)
	msgID := waitMessageID(t, f.Owner, f.AccID, subj, 30*time.Second)

	var hit map[string]any
	harness.WaitUntil(t, 60*time.Second, "full-text hit", func() bool {
		r, hits := searchHits(t, f.Owner, "q="+url.QueryEscape(word+" tank"))
		if r.Status != http.StatusOK {
			return false
		}
		for _, h := range hits {
			if h["messageId"] == msgID {
				hit = h
				return true
			}
		}
		return false
	})
	if hit["accountId"] != f.AccID || hit["subject"] != subj || hit["mailboxId"] == "" {
		t.Fatalf("hit: %#v", hit)
	}
	snippet, _ := hit["snippet"].(string)
	hls, _ := hit["highlights"].([]any)
	if len(hls) == 0 {
		t.Fatalf("no highlights in %q", snippet)
	}
	first, _ := hls[0].(map[string]any)
	start, _ := first["start"].(float64)
	end, _ := first["end"].(float64)
	if got := string([]rune(snippet)[int(start):int(end)]); got != word && got != "tank" {
		t.Fatalf("highlight %q in %q", got, snippet)
	}
	// Exclusion and a phrase that is not there.
	if _, hits := searchHits(t, f.Owner, "q="+url.QueryEscape(word+" -tank")); len(hits) != 0 {
		t.Fatalf("excluded word still matched: %v", hits)
	}
	if _, hits := searchHits(t, f.Owner, "q="+url.QueryEscape(`"tank `+word+`"`)); len(hits) != 0 {
		t.Fatalf("phrase in the wrong order matched: %v", hits)
	}

	// Reindex: the message is there already, so the job finds it indexed.
	rx := f.Owner.Do(http.MethodPost, "/v1/accounts/"+f.AccID+"/fulltext/reindex?limit=100", nil)
	harness.ExpectStatus(t, rx, http.StatusAccepted)
	job, _ := rx.JSON["job"].(map[string]any)
	jobID, _ := job["id"].(string)
	if jobID == "" || rx.Header.Get("Location") != "/v1/jobs/"+jobID {
		t.Fatalf("reindex: %s", rx.Body)
	}
	done := f.Owner.WaitJSON(t, "/v1/jobs/"+jobID, 60*time.Second, "reindex job", func(r harness.Resp) bool {
		s := r.Str("state")
		return s == "succeeded" || s == "failed" || s == "cancelled"
	})
	if done.Str("state") != "succeeded" {
		t.Fatalf("reindex job: %s", done.Body)
	}
	res, _ := done.JSON["result"].(map[string]any)
	if n, _ := res["alreadyIndexed"].(float64); n < 1 {
		t.Fatalf("reindex result: %#v", res)
	}

	// An account-bound key sees only its account and is refused another one.
	keyResp := f.Owner.Do(http.MethodPost, "/v1/api-keys", map[string]any{
		"name": "fts-acc-" + word, "scopes": []string{"messages:read"}, "accountId": f.AccID,
	})
	harness.ExpectStatus(t, keyResp, http.StatusCreated)
	accKey := f.Node.WithBearer(keyResp.Str("token"))
	if r, hits := searchHits(t, accKey, "q="+word); r.Status != http.StatusOK || len(hits) != 1 {
		t.Fatalf("bound key: %d %s", r.Status, r.Body)
	}
	other := "acc_" + strings.Repeat("0", 26)
	r, _ = searchHits(t, accKey, "q="+word+"&accountId="+other)
	harness.ExpectProblem(t, r, http.StatusForbidden, "forbidden")

	// The rows go with the account.
	del := f.Owner.Do(http.MethodDelete, "/v1/accounts/"+f.AccID, nil)
	harness.ExpectStatus(t, del, http.StatusAccepted)
	delJob, _ := del.JSON["job"].(map[string]any)
	delID, _ := delJob["id"].(string)
	f.Owner.WaitJSON(t, "/v1/jobs/"+delID, 90*time.Second, "erasure job", func(r harness.Resp) bool {
		return r.Str("state") == "succeeded"
	})
	if _, hits := searchHits(t, f.Owner, "q="+word); len(hits) != 0 {
		t.Fatalf("erased account still found: %v", hits)
	}
}
