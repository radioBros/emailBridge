//go:build e2e

package p0

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestTenantIsolation(t *testing.T) {
	n := StartNode(t, nil)
	owner, secret := n.OwnerSession(t)
	_ = secret

	me := owner.do(http.MethodGet, "/v1/me", nil)
	expectStatus(t, me, http.StatusOK)
	orgA, _ := me.JSON["activeOrganizationId"].(string)
	if orgA == "" {
		t.Fatalf("org A missing: %s", me.Body)
	}

	// instance owner creates org B
	r := owner.do(http.MethodPost, "/v1/organizations", map[string]any{"name": "Org B", "slug": "org-b-e2e"})
	expectStatus(t, r, http.StatusCreated)
	orgB, _ := r.JSON["id"].(string)
	if orgB == "" {
		t.Fatalf("create org B: %s", r.Body)
	}

	// seed invite (tenancy API refuses members:write for instance-owner-as-viewer in a foreign org)
	inviteEmail := "member-b@e2e.test"
	invitePass := "correct horse battery staple"
	token := n.seedInvite(t, orgB, inviteEmail, "owner")
	acc := n.Client()
	r = acc.do(http.MethodPost, "/v1/invitations/accept", map[string]any{
		"token": token, "password": invitePass, "name": "Member B",
	})
	if r.Status != http.StatusOK && r.Status != http.StatusCreated {
		t.Fatalf("accept invite = %d %s", r.Status, r.Body)
	}

	// enrol MFA for org B member
	bSess := n.Client()
	r = bSess.signIn(inviteEmail, invitePass)
	expectStatus(t, r, http.StatusOK)
	bSecret, bEnrol := bSess.enrolTOTP(invitePass)

	// API keys in each org
	r = owner.do(http.MethodPost, "/v1/api-keys", map[string]any{
		"name": "key-a", "scopes": []string{"settings:read", "logs:read", "audit:read", "members:read", "apikeys:read", "jobs:read"},
	})
	expectStatus(t, r, http.StatusCreated)
	keyA, _ := r.JSON["token"].(string)

	bSess.hdr = map[string]string{"EB-Organization": orgB}
	r = bSess.do(http.MethodPost, "/v1/api-keys", map[string]any{
		"name": "key-b", "scopes": []string{"settings:read", "logs:read", "audit:read", "members:read", "apikeys:read", "jobs:read"},
	})
	expectStatus(t, r, http.StatusCreated)
	keyB, _ := r.JSON["token"].(string)
	bSess.hdr = nil

	clientB := n.Client()
	clientB.hdr = map[string]string{"Authorization": "Bearer " + keyB}

	// Org B key cannot switch to org A
	clientB.hdr["EB-Organization"] = orgA
	expectProblem(t, clientB.do(http.MethodGet, "/v1/settings", nil), http.StatusForbidden, "organization_mismatch")
	delete(clientB.hdr, "EB-Organization")

	// Org B session cannot act as org A
	bSess2 := n.SignInWithTOTP(t, inviteEmail, invitePass, bSecret, bEnrol)
	bSess2.hdr = map[string]string{"EB-Organization": orgA}
	r = bSess2.do(http.MethodGet, "/v1/members", nil)
	if r.Status != http.StatusForbidden && r.Status != http.StatusNotFound {
		if r.Str("code") != "organization_mismatch" && r.Str("code") != "forbidden" {
			t.Fatalf("org B session on org A members = %d %s", r.Status, r.Body)
		}
	}

	// Org B key reads its own members, not A's key list when scoped
	r = clientB.do(http.MethodGet, "/v1/members", nil)
	expectStatus(t, r, http.StatusOK)
	r = clientB.do(http.MethodGet, "/v1/api-keys", nil)
	expectStatus(t, r, http.StatusOK)
	if body := string(r.Body); len(keyA) > 12 && strings.Contains(body, keyA[:12]) {
		t.Fatal("org B api-keys list leaked org A token")
	}
	r = clientB.do(http.MethodGet, "/v1/audit-log?limit=10", nil)
	if r.Status != http.StatusOK && r.Status != http.StatusForbidden {
		t.Fatalf("audit-log = %d %s", r.Status, r.Body)
	}
	r = clientB.do(http.MethodGet, "/v1/settings", nil)
	expectStatus(t, r, http.StatusOK)
	r = clientB.do(http.MethodGet, "/v1/jobs/job_00000000000000000000000000", nil)
	if r.Status != http.StatusNotFound && r.Status != http.StatusForbidden {
		t.Fatalf("foreign job = %d %s", r.Status, r.Body)
	}

	from := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)
	to := time.Now().UTC().Add(time.Hour).Format(time.RFC3339)
	r = clientB.do(http.MethodGet, "/v1/logs?from="+from+"&to="+to+"&limit=5&category=api", nil)
	if r.Status != http.StatusOK && r.Status != http.StatusForbidden {
		t.Fatalf("logs = %d %s", r.Status, r.Body)
	}
}
