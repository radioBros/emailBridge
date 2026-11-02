//go:build e2e

package security

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/radiobros/emailbridge/test/e2e/harness"
	"github.com/radiobros/emailbridge/test/sink"
)

// notMoved are collections that tie the signed-in owner to organisation A (identity, membership, sessions,
// keys); everything else that carries organizationId is moved to organisation B.
var notMoved = regexp.MustCompile(`(?i)member|user|session|auth|limen|organization|invit|apikey|passkey|totp|verification|settings`)

// Organisation A owns real resources — an account with indexed mail and an attachment, a webhook endpoint, a
// sent message — which are then moved to organisation B in the database. An owner of A, using the real IDs,
// must never read, change or delete any of them through any operation, and no list may show them. This is the
// tenant-isolation check for every operation of the API (a live-mailbox call that loaded the account without
// an organisation filter let one organisation act on another's real mailbox — final-pass finding).
func TestSecurityCrossTenantRealIDs(t *testing.T) {
	sim := harness.StartSim(t)
	n := harness.StartP1Node(t, nil)
	owner, _ := n.OwnerSession(t)
	sk := harness.StartSink(t, sink.Options{})

	me := owner.Do(http.MethodGet, "/v1/me", nil)
	harness.ExpectStatus(t, me, http.StatusOK)
	orgA := me.Str("activeOrganizationId")
	if orgA == "" {
		t.Fatalf("no active organisation: %s", me.Body)
	}

	// Resources of organisation A.
	sim.CreateSimUser(t, "xtenant", "pw")
	email := "xtenant@sim.e2e.test"
	body := harness.AccountCreateBody(t, sim, "xtenant", "pw", email, "xtenant", map[string]any{"sandbox.enabled": false}, "standard")
	body["skipVerify"] = true
	r := owner.Do(http.MethodPost, "/v1/accounts", body)
	harness.ExpectStatus(t, r, http.StatusCreated)
	accID := r.Str("id")
	owner.WaitJSON(t, "/v1/accounts/"+accID, 2*time.Minute, "account connected", func(r harness.Resp) bool {
		return r.Status == http.StatusOK && r.Str("state") == "connected"
	})
	raw := []byte("From: " + email + "\r\nTo: " + email + "\r\nSubject: tenant secret\r\nMessage-ID: <xt-1@e2e>\r\n" +
		"MIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=b1\r\n\r\n--b1\r\nContent-Type: text/plain\r\n\r\n" +
		"private body\r\n--b1\r\nContent-Type: application/pdf\r\nContent-Disposition: attachment; filename=\"secret.pdf\"\r\n" +
		"Content-Transfer-Encoding: base64\r\n\r\nJVBERi0xLjQK\r\n--b1--\r\n")
	sim.InjectRaw(t, "xtenant", "INBOX", raw, nil)
	list := owner.WaitJSON(t, "/v1/accounts/"+accID+"/messages?limit=10", 2*time.Minute, "message indexed", func(r harness.Resp) bool {
		d, _ := r.JSON["data"].([]any)
		return r.Status == http.StatusOK && len(d) > 0
	})
	msg := list.JSON["data"].([]any)[0].(map[string]any)
	msgID, _ := msg["id"].(string)
	mbxID, _ := msg["mailboxId"].(string)
	view := owner.Do(http.MethodGet, "/v1/accounts/"+accID+"/messages/"+msgID, nil)
	harness.ExpectStatus(t, view, http.StatusOK)
	attID := ""
	if atts, _ := view.JSON["attachments"].([]any); len(atts) > 0 {
		attID, _ = atts[0].(map[string]any)["id"].(string)
	}
	ep := owner.Do(http.MethodPost, "/v1/webhook-endpoints", map[string]any{
		"name": "xt", "kind": "http", "url": sk.URL, "secret": harness.WebhookSecret(), "events": []string{"message.received"},
	})
	harness.ExpectStatus(t, ep, http.StatusCreated)
	epID := ep.Str("id")
	out := owner.Do(http.MethodPost, "/v1/accounts/"+accID+"/outbound-messages", map[string]any{
		"to": []map[string]any{{"address": email}}, "subject": "out", "text": "x",
	})
	harness.ExpectStatus(t, out, http.StatusAccepted)
	outID := out.Str("id")

	real := map[string]string{"accountId": accID, "messageId": msgID, "mailboxId": mbxID, "endpointId": epID, "outboundId": outID}
	if attID != "" {
		real["attachmentId"] = attID
	}
	for k, v := range real {
		if v == "" {
			t.Fatalf("resource %s has no id", k)
		}
	}

	// Stop the account's sync first: a running runner keeps organisation A in memory and would emit new
	// events for A after the move (a test artefact, not a leak).
	owner.WaitOutboundState(t, outID, "sent", 2*time.Minute)
	harness.ExpectStatus(t, owner.Do(http.MethodPatch, "/v1/accounts/"+accID, map[string]any{"disabled": true}), http.StatusOK)
	owner.WaitJSON(t, "/v1/accounts/"+accID, time.Minute, "account disabled", func(r harness.Resp) bool {
		return r.Status == http.StatusOK && r.Str("state") == "disabled"
	})
	time.Sleep(time.Second) // let the last events of the stopped runner commit before the move

	// Organisation B, and the move.
	ob := owner.Do(http.MethodPost, "/v1/organizations", map[string]any{"name": "Org X", "slug": "org-x-tenant"})
	harness.ExpectStatus(t, ob, http.StatusCreated)
	orgB := ob.Str("id")
	moved := moveOrganisation(t, n, orgA, orgB)
	t.Logf("moved %v from %s to %s", moved, orgA, orgB)

	// Every operation with a path parameter, with the real IDs, as the owner of A.
	ops := loadOperations(t)
	sort.SliceStable(ops, func(i, j int) bool { return methodRank(ops[i].method) < methodRank(ops[j].method) })
	var bad []string
	tried := 0
	for _, op := range ops {
		if isPublic(op.path) || strings.HasSuffix(op.path, "/stream") {
			continue
		}
		if !strings.Contains(op.path, "{") {
			// /v1/me/export is the caller's own GDPR export (Art. 15): its audit trail of the caller's actions
			// names the resources they acted on, wherever those resources live now.
			if op.method == http.MethodGet && op.path != "/v1/me/export" {
				resp := owner.Do(op.method, op.path, nil)
				for name, id := range real {
					if resp.Status < 300 && strings.Contains(string(resp.Body), id) {
						bad = append(bad, fmt.Sprintf("GET %s lists organisation B's %s %s", op.path, name, id))
					}
				}
			}
			continue
		}
		path, usesReal := fillReal(op.path, real)
		if !usesReal {
			continue
		}
		tried++
		var b any
		if op.method != http.MethodGet && op.method != http.MethodDelete {
			b = map[string]any{}
		}
		resp := owner.Do(op.method, path, b)
		if resp.Status < 300 {
			bad = append(bad, fmt.Sprintf("%s %s → %d %.160s", op.method, op.path, resp.Status, resp.Body))
		}
	}
	if tried < 20 {
		t.Fatalf("only %d operations used a real ID — the sweep is not exercising the API", tried)
	}
	if len(bad) > 0 {
		t.Fatalf("%d operations let organisation A reach organisation B's resources:\n%s", len(bad), strings.Join(bad, "\n"))
	}
}

func methodRank(m string) int {
	switch m {
	case http.MethodGet:
		return 0
	case http.MethodDelete:
		return 2
	default:
		return 1
	}
}

// fillReal fills path parameters with the real IDs where known (well-formed unknown IDs otherwise) and reports
// whether at least one real ID was used.
func fillReal(path string, real map[string]string) (string, bool) {
	used := false
	out := param.ReplaceAllStringFunc(path, func(m string) string {
		name := m[1 : len(m)-1]
		if id, ok := real[name]; ok {
			used = true
			return id
		}
		if name == "id" { // generic {id}: try the account, the most sensitive resource
			if id, ok := real["accountId"]; ok && strings.Contains(path, "/accounts/") {
				used = true
				return id
			}
		}
		if p, ok := prefixes[name]; ok {
			return p + "_" + strings.Repeat("0", 26)
		}
		return "x"
	})
	return out, used
}

func moveOrganisation(t *testing.T, n *harness.Node, from, to string) []string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	client, err := mongo.Connect(options.Client().ApplyURI(n.MongoURI))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Disconnect(context.Background()) }()
	db := client.Database(n.Database)
	names, err := db.ListCollectionNames(ctx, bson.M{})
	if err != nil {
		t.Fatal(err)
	}
	var moved []string
	for _, name := range names {
		if notMoved.MatchString(name) || strings.HasPrefix(name, "system.") {
			continue
		}
		res, err := db.Collection(name).UpdateMany(ctx, bson.M{"organizationId": from}, bson.M{"$set": bson.M{"organizationId": to}})
		if err != nil {
			t.Fatalf("move %s: %v", name, err)
		}
		if res.ModifiedCount > 0 {
			moved = append(moved, fmt.Sprintf("%s(%d)", name, res.ModifiedCount))
		}
	}
	return moved
}
