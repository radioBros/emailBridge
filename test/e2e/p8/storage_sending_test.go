//go:build e2e

package p8

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/test/e2e/harness"
)

// Google Drive uploads attachment bytes through the integration sink (httptest token + Drive).
func TestP8_GDriveUpload(t *testing.T) {
	sim := harness.StartSim(t)
	n := harness.StartP1Node(t, nil)
	owner, _ := n.OwnerSession(t)

	drv, uploads := fakeOAuthDrive(t)
	inst := install(t, owner, "gdrive", map[string]any{
		"name": "gdrive-e2e",
		"config": map[string]any{
			"clientId": "cid",
			"baseUrl":  drv.URL,
			"tokenUrl": drv.URL + "/token",
			"folderId": "folder1",
			"filter":   `type == "message.received"`,
		},
		"secrets": map[string]string{
			"clientSecret": "sec", "refreshToken": "rt", "accessToken": "atok",
		},
	})
	id := inst.Str("id")
	epID := firstEndpointID(inst)
	if epID == "" {
		t.Fatalf("no endpoint: %s", inst.Body)
	}
	// Install does not set payload.attachments; enable it so sync + delivery carry attachment ids.
	enableEndpointAttachments(t, owner, epID)

	user := fmt.Sprintf("gd%d", time.Now().UnixNano()%1_000_000)
	email := user + "@sim.e2e.test"
	_ = connectAccount(t, owner, sim, user, "sim-secret", email, "GDAcc")
	time.Sleep(500 * time.Millisecond)

	raw := rawWithAttachment(email, email, "drive file", "<gd-p8@e2e>", "hi", "note.pdf", "JVBERi0xLjQK")
	sim.InjectRaw(t, user, "INBOX", raw, nil)

	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if uploads.Load() >= 1 {
			_ = waitLastActionOK(t, owner, id, 30*time.Second)
			return
		}
		view := owner.Do(http.MethodGet, "/v1/integration-instances/"+id, nil)
		if view.Status == http.StatusOK {
			errMsg, _ := view.JSON["lastActionError"].(string)
			if strings.Contains(errMsg, "no organisation in context") {
				t.Fatalf("BUG: %s", "gdrive action AttachmentReader lacks org on ctx — request path via kind=integration delivery; expected upload + lastActionOK; actual lastActionError=")
			}
			if ok, _ := view.JSON["lastActionOK"].(bool); ok && uploads.Load() == 0 {
				t.Fatalf("BUG: %s", "gdrive lastActionOK without Drive upload — install endpoint omits payload.attachments (connectors/storage.go)")
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("Drive upload not observed; last instance=%s", owner.Do(http.MethodGet, "/v1/integration-instances/"+id, nil).Body)
}

// OneDrive uploads attachment bytes through the integration sink.
func TestP8_OneDriveUpload(t *testing.T) {
	sim := harness.StartSim(t)
	n := harness.StartP1Node(t, nil)
	owner, _ := n.OwnerSession(t)

	od, uploads := fakeOAuthOneDrive(t)
	inst := install(t, owner, "onedrive", map[string]any{
		"name": "onedrive-e2e",
		"config": map[string]any{
			"clientId":   "cid",
			"baseUrl":    od.URL,
			"tokenUrl":   od.URL + "/token",
			"folderPath": "EmailBridge",
			"filter":     `type == "message.received"`,
		},
		"secrets": map[string]string{
			"clientSecret": "sec", "refreshToken": "rt", "accessToken": "atok",
		},
	})
	id := inst.Str("id")
	epID := firstEndpointID(inst)
	if epID == "" {
		t.Fatalf("no endpoint: %s", inst.Body)
	}
	enableEndpointAttachments(t, owner, epID)

	user := fmt.Sprintf("od%d", time.Now().UnixNano()%1_000_000)
	email := user + "@sim.e2e.test"
	_ = connectAccount(t, owner, sim, user, "sim-secret", email, "ODAcc")
	time.Sleep(500 * time.Millisecond)

	raw := rawWithAttachment(email, email, "od file", "<od-p8@e2e>", "hi", "note.pdf", "JVBERi0xLjQK")
	sim.InjectRaw(t, user, "INBOX", raw, nil)

	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if uploads.Load() >= 1 {
			_ = waitLastActionOK(t, owner, id, 30*time.Second)
			return
		}
		view := owner.Do(http.MethodGet, "/v1/integration-instances/"+id, nil)
		if view.Status == http.StatusOK {
			errMsg, _ := view.JSON["lastActionError"].(string)
			if strings.Contains(errMsg, "no organisation in context") {
				t.Fatalf("BUG: %s", "onedrive action AttachmentReader lacks org on ctx — actual lastActionError=")
			}
			if ok, _ := view.JSON["lastActionOK"].(bool); ok && uploads.Load() == 0 {
				t.Fatalf("BUG: %s", "onedrive lastActionOK without Graph upload — install endpoint omits payload.attachments (connectors/storage.go)")
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("OneDrive upload not observed; last instance=%s", owner.Do(http.MethodGet, "/v1/integration-instances/"+id, nil).Body)
}

// SES install creates a gateway; uninstall removes it.
func TestP8_SESGatewayLifecycle(t *testing.T) {
	n := harness.StartP1Node(t, nil)
	owner, _ := n.OwnerSession(t)

	probe, _ := fakeHubSpot(t) // any httptest that returns 200 on GET /
	inst := install(t, owner, "ses", map[string]any{
		"name": "ses-e2e",
		"config": map[string]any{
			"region":    "eu-west-1",
			"from":      "sender@e2e.test",
			"accessKey": "AKIAEXAMPLE",
			"baseUrl":   probe.URL,
		},
		"secrets": map[string]string{"secretKey": "secret"},
	})
	id := inst.Str("id")
	gwy := inst.Str("gatewayId")
	if gwy == "" {
		cr, _ := inst.JSON["createdResources"].(map[string]any)
		if gs, _ := cr["gateways"].([]any); len(gs) > 0 {
			gwy, _ = gs[0].(string)
		}
	}
	if gwy == "" {
		t.Fatalf("SES install created no gateway: %s", inst.Body)
	}
	get := owner.Do(http.MethodGet, "/v1/smtp-gateways/"+gwy, nil)
	harness.ExpectStatus(t, get, http.StatusOK)
	if get.Str("kind") != "ses" {
		t.Fatalf("gateway kind=%q body=%s", get.Str("kind"), get.Body)
	}

	prev := owner.Hdr
	owner.Hdr = map[string]string{"If-Match": etag(inst.JSON["version"])}
	del := owner.Do(http.MethodDelete, "/v1/integration-instances/"+id, nil)
	owner.Hdr = prev
	if del.Status != http.StatusNoContent && del.Status != http.StatusOK {
		t.Fatalf("uninstall = %d %s", del.Status, del.Body)
	}
	gone := owner.Do(http.MethodGet, "/v1/smtp-gateways/"+gwy, nil)
	if gone.Status != http.StatusNotFound {
		t.Fatalf("gateway after uninstall = %d %s", gone.Status, gone.Body)
	}
}
