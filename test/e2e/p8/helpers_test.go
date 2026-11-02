//go:build e2e

package p8

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/radiobros/emailbridge/test/e2e/harness"
	"github.com/radiobros/emailbridge/test/sink"
)

func etag(v any) string {
	switch n := v.(type) {
	case float64:
		return fmt.Sprintf(`"v%d"`, int64(n))
	case int64:
		return fmt.Sprintf(`"v%d"`, n)
	case json.Number:
		return `"v` + n.String() + `"`
	default:
		return fmt.Sprintf(`"v%v"`, v)
	}
}

func install(t *testing.T, owner *harness.Client, typ string, body map[string]any) harness.Resp {
	t.Helper()
	r := owner.Do(http.MethodPost, "/v1/integrations/"+typ+"/install", body)
	harness.ExpectStatus(t, r, http.StatusCreated)
	if r.Str("id") == "" {
		t.Fatalf("install %s: missing id %s", typ, r.Body)
	}
	return r
}

func waitLastActionOK(t *testing.T, owner *harness.Client, id string, timeout time.Duration) harness.Resp {
	t.Helper()
	return owner.WaitJSON(t, "/v1/integration-instances/"+id, timeout, "lastActionOK", func(r harness.Resp) bool {
		if r.Status != http.StatusOK {
			return false
		}
		ok, _ := r.JSON["lastActionOK"].(bool)
		return ok
	})
}

func connectAccount(t *testing.T, owner *harness.Client, sim *harness.SimEnv, user, pass, email, name string) string {
	t.Helper()
	sim.CreateSimUser(t, user, pass)
	body := harness.AccountCreateBody(t, sim, user, pass, email, name, map[string]any{"sandbox.enabled": false}, "standard")
	acc := owner.Do(http.MethodPost, "/v1/accounts", body)
	harness.ExpectStatus(t, acc, http.StatusCreated)
	id := acc.Str("id")
	owner.WaitJSON(t, "/v1/accounts/"+id, 90*time.Second, "connected", func(r harness.Resp) bool {
		return r.Status == http.StatusOK && r.Str("state") == "connected"
	})
	return id
}

func rawWithAttachment(from, to, subject, mid, body, filename, b64 string) []byte {
	if mid == "" {
		mid = fmt.Sprintf("<%d@e2e>", time.Now().UnixNano())
	}
	return []byte(fmt.Sprintf(
		"From: %s\r\nTo: %s\r\nSubject: %s\r\nMessage-ID: %s\r\nDate: %s\r\n"+
			"MIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=b1\r\n\r\n"+
			"--b1\r\nContent-Type: text/plain\r\n\r\n%s\r\n"+
			"--b1\r\nContent-Type: application/pdf\r\nContent-Disposition: attachment; filename=\"%s\"\r\n"+
			"Content-Transfer-Encoding: base64\r\n\r\n%s\r\n--b1--\r\n",
		from, to, subject, mid, time.Now().UTC().Format(time.RFC1123Z), body, filename, b64,
	))
}

func enableEndpointAttachments(t *testing.T, owner *harness.Client, endpointID string) {
	t.Helper()
	get := owner.Do(http.MethodGet, "/v1/webhook-endpoints/"+endpointID, nil)
	harness.ExpectStatus(t, get, http.StatusOK)
	match := get.Header.Get("ETag")
	if match == "" {
		match = etag(get.JSON["version"])
	}
	prev := owner.Hdr
	hdr := map[string]string{}
	for k, v := range prev {
		hdr[k] = v
	}
	hdr["If-Match"] = match
	owner.Hdr = hdr
	r := owner.Do(http.MethodPatch, "/v1/webhook-endpoints/"+endpointID, map[string]any{
		"payload": map[string]any{"attachments": true, "text": "plain"},
	})
	owner.Hdr = prev
	harness.ExpectStatus(t, r, http.StatusOK)
}

func firstEndpointID(inst harness.Resp) string {
	cr, _ := inst.JSON["createdResources"].(map[string]any)
	if cr == nil {
		return ""
	}
	eps, _ := cr["endpoints"].([]any)
	if len(eps) == 0 {
		return ""
	}
	s, _ := eps[0].(string)
	return s
}

func signFeatherDesk(token string, ts int64, body []byte) string {
	mac := hmac.New(sha256.New, []byte(token))
	_, _ = fmt.Fprintf(mac, "%d.", ts)
	_, _ = mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func postCallback(t *testing.T, base, instanceID string, body []byte, ts int64, sig string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, base+"/integrations/"+instanceID+"/callback", strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if ts > 0 {
		req.Header.Set("X-FeatherDesk-Timestamp", fmt.Sprintf("%d", ts))
	}
	if sig != "" {
		req.Header.Set("X-FeatherDesk-Signature", sig)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	raw, _ := io.ReadAll(res.Body)
	return res.StatusCode, raw
}

// fakeHubSpot answers contact search + email engagement create.
func fakeHubSpot(t *testing.T) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var engages atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		_ = r.Body.Close()
		switch {
		case strings.Contains(r.URL.Path, "contacts/search"):
			_, _ = w.Write([]byte(`{"results":[{"id":"c1"}]}`))
		case strings.HasSuffix(r.URL.Path, "/emails") || strings.Contains(r.URL.Path, "/crm/v3/objects/emails"):
			engages.Add(1)
			_, _ = w.Write([]byte(`{"id":"e1"}`))
		case strings.Contains(r.URL.Path, "/contacts"):
			_, _ = w.Write([]byte(`{"results":[]}`))
		default:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &engages
}

// fakePipedrive answers person search + note create.
func fakePipedrive(t *testing.T) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var notes atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		_ = r.Body.Close()
		switch {
		case strings.Contains(r.URL.Path, "persons/search"):
			_, _ = w.Write([]byte(`{"data":{"items":[{"item":{"id":9}}]}}`))
		case strings.Contains(r.URL.Path, "/notes"):
			notes.Add(1)
			_, _ = w.Write([]byte(`{"data":{"id":3}}`))
		case strings.Contains(r.URL.Path, "/users/me"):
			_, _ = w.Write([]byte(`{"data":{"id":1}}`))
		default:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &notes
}

// fakeOAuthDrive serves token refresh + Drive multipart upload.
func fakeOAuthDrive(t *testing.T) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var uploads atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		_ = r.Body.Close()
		switch {
		case r.Method == http.MethodPost && (r.URL.Path == "/" || strings.Contains(r.URL.Path, "token") || r.Header.Get("Content-Type") == "application/x-www-form-urlencoded"):
			_, _ = w.Write([]byte(`{"access_token":"atok","token_type":"Bearer","expires_in":3600}`))
		case strings.Contains(r.URL.Path, "/upload/drive/v3/files") || strings.Contains(r.URL.Path, "/drive/v3/files"):
			uploads.Add(1)
			_, _ = w.Write([]byte(`{"id":"file_1"}`))
		case strings.Contains(r.URL.Path, "/drive/v3/about"):
			_, _ = w.Write([]byte(`{"user":{"emailAddress":"u@x"}}`))
		default:
			if len(raw) > 0 && r.Method == http.MethodPost {
				uploads.Add(1)
				_, _ = w.Write([]byte(`{"id":"file_1"}`))
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &uploads
}

// fakeOAuthOneDrive serves token refresh + Graph content upload.
func fakeOAuthOneDrive(t *testing.T) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var uploads atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		_ = r.Body.Close()
		switch {
		case r.Method == http.MethodPost && (strings.Contains(r.URL.Path, "token") || r.Header.Get("Content-Type") == "application/x-www-form-urlencoded" || r.URL.Path == "/"):
			_, _ = w.Write([]byte(`{"access_token":"atok","token_type":"Bearer","expires_in":3600}`))
		case r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/content"):
			uploads.Add(1)
			_, _ = w.Write([]byte(`{"id":"od_1"}`))
		case strings.Contains(r.URL.Path, "/me/drive"):
			_, _ = w.Write([]byte(`{"id":"drive"}`))
		default:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &uploads
}

// fakeSentry counts envelope/store posts from sentry-go.
func fakeSentry(t *testing.T) (*httptest.Server, *atomic.Int64, string) {
	t.Helper()
	var hits atomic.Int64
	var mu sync.Mutex
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		_ = r.Body.Close()
		hits.Add(1)
		mu.Lock()
		paths = append(paths, r.Method+" "+r.URL.Path)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "http://")
	dsn := "http://public@" + host + "/1"
	return srv, &hits, dsn
}

func corruptInstanceSecret(t *testing.T, n *harness.Node, instanceID, field string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	client, err := mongo.Connect(options.Client().ApplyURI(n.MongoURI))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Disconnect(context.Background()) })
	// Non-zero EncryptedString shape so decrypt is attempted (and fails → internal_error).
	_, err = client.Database(n.Database).Collection("integrationInstances").UpdateOne(ctx,
		bson.M{"_id": instanceID},
		bson.M{"$set": bson.M{"secrets." + field: bson.M{
			"kid": "k1", "dek": "dk_01hxxxxxxxxxxxxxxxxxxxxxxx",
			"nonce": []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12},
			"ct":    []byte("not-valid-ciphertext-bytes!!"),
		}}},
	)
	if err != nil {
		t.Fatal(err)
	}
}

func waitHits(t *testing.T, hits *atomic.Int64, want int64, timeout time.Duration, what string) {
	t.Helper()
	harness.WaitUntil(t, timeout, what, func() bool { return hits.Load() >= want })
}

func waitSinkBlocks(t *testing.T, sk *sink.Server, timeout time.Duration) (sink.Request, bool) {
	t.Helper()
	var found sink.Request
	ok := false
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		for _, r := range sk.Requests() {
			var b map[string]any
			if json.Unmarshal(r.Body, &b) == nil {
				if _, has := b["blocks"]; has {
					return r, true
				}
				if txt, _ := b["text"].(string); strings.Contains(txt, "New mail") {
					return r, true
				}
			}
			if strings.Contains(string(r.Body), "New mail") {
				return r, true
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	return found, ok
}

func orgBSession(t *testing.T, n *harness.Node, owner *harness.Client) (*harness.Client, string) {
	t.Helper()
	slug := fmt.Sprintf("org-b-p8-%d", time.Now().UnixNano()%1_000_000)
	ob := owner.Do(http.MethodPost, "/v1/organizations", map[string]any{"name": "Org B P8", "slug": slug})
	harness.ExpectStatus(t, ob, http.StatusCreated)
	orgB := ob.Str("id")
	email := fmt.Sprintf("member-b-%d@e2e.test", time.Now().UnixNano()%1_000_000)
	pass := harness.OwnerPassword
	token := n.SeedInvite(t, orgB, email, "owner")
	acc := n.NewClient()
	r := acc.Do(http.MethodPost, "/v1/invitations/accept", map[string]any{
		"token": token, "password": pass, "name": "Member B",
	})
	if r.Status != http.StatusOK && r.Status != http.StatusCreated {
		t.Fatalf("accept invite = %d %s", r.Status, r.Body)
	}
	bSess := n.NewClient()
	r = bSess.SignIn(email, pass)
	harness.ExpectStatus(t, r, http.StatusOK)
	secret, used := bSess.EnrolTOTP(pass)
	b := n.SignInWithTOTP(t, email, pass, secret, used)
	b.Hdr = map[string]string{"EB-Organization": orgB}
	return b, orgB
}
