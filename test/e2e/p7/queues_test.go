//go:build e2e

package p7

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/test/e2e/harness"
	"github.com/radiobros/emailbridge/test/sink"
)

// Queue page (W72), end to end on a real node with the simulator:
//   - with the Sending queue paused, scheduled and immediate sends wait in EMAILBRIDGE_SUBMIT and a failing
//     webhook endpoint leaves deliveries retry-waiting / dead-lettered;
//   - the items list and GET /v1/queues counts show them; one record is cancelled by id, the scheduled ones
//     by filter (a queues.cancel job), the rest by the instance owner's whole-queue cancel across every
//     organisation (typed confirmation);
//   - organisation B sees and cancels none of organisation A's records;
//   - after the queue resumes, the workers ack the cancelled stream entries without sending: the simulator
//     receives no mail and the records stay cancelled.
func TestP7_QueuesCancel(t *testing.T) {
	f := startFixture(t, nil, []string{"message.received"}, sink.Options{Status: http.StatusInternalServerError})
	owner := f.Owner
	ctx := context.Background()
	if err := f.Sim.Client.ClearSMTPMessages(ctx); err != nil {
		t.Fatal(err)
	}

	harness.ExpectStatus(t, owner.Do(http.MethodPatch, "/v1/queues/EMAILBRIDGE_SUBMIT", map[string]any{"paused": true}), http.StatusOK)
	t.Cleanup(func() { owner.Do(http.MethodPatch, "/v1/queues/EMAILBRIDGE_SUBMIT", map[string]any{"paused": false}) })

	submit := func(subject string, sendAt string) string {
		body := map[string]any{"to": []map[string]any{{"address": f.Email}}, "subject": subject, "text": "queued by the W72 e2e test"}
		if sendAt != "" {
			body["sendAt"] = sendAt
		}
		r := owner.Do(http.MethodPost, "/v1/accounts/"+f.AccID+"/outbound-messages", body)
		harness.ExpectStatus(t, r, http.StatusAccepted)
		return r.Str("id")
	}
	later := time.Now().UTC().Add(2 * time.Hour).Format(time.RFC3339)
	var scheduled, queued []string
	for i := 0; i < 3; i++ {
		scheduled = append(scheduled, submit(fmt.Sprintf("w72-scheduled-%d", i), later))
		queued = append(queued, submit(fmt.Sprintf("w72-queued-%d", i), ""))
	}

	// Two received messages → two deliveries to the failing endpoint.
	f.inject("w72-in-1", "<w72-in-1@e2e.test>", "one")
	f.inject("w72-in-2", "<w72-in-2@e2e.test>", "two")
	owner.WaitJSON(t, "/v1/queues/EMAILBRIDGE_DELIVERY/items", 60*time.Second, "two failing deliveries", func(r harness.Resp) bool {
		total, _ := r.JSON["total"].(float64)
		return r.Status == http.StatusOK && total == 2
	})

	// The list and the counts.
	list := owner.Do(http.MethodGet, "/v1/queues/EMAILBRIDGE_SUBMIT/items?limit=100", nil)
	harness.ExpectStatus(t, list, http.StatusOK)
	if total, _ := list.JSON["total"].(float64); total != 6 {
		t.Fatalf("submit items: %s", list.Body)
	}
	counts := queueItems(t, owner, "", "EMAILBRIDGE_SUBMIT")
	if counts["scheduled"] != 3 || counts["waiting"] != 3 {
		t.Fatalf("counts: %v", counts)
	}

	// Cancel one by id.
	r := owner.Do(http.MethodPost, "/v1/queues/EMAILBRIDGE_SUBMIT/items/cancel", map[string]any{"ids": []string{queued[0]}})
	harness.ExpectStatus(t, r, http.StatusOK)
	expectOutbound(t, owner, queued[0], "cancelled")

	// Cancel the scheduled ones by filter (a job).
	r = owner.Do(http.MethodPost, "/v1/queues/EMAILBRIDGE_SUBMIT/items/cancel", map[string]any{"filter": map[string]any{"state": "scheduled"}})
	harness.ExpectStatus(t, r, http.StatusAccepted)
	if m, _ := r.JSON["matched"].(float64); m != 3 {
		t.Fatalf("matched: %s", r.Body)
	}
	waitJob(t, owner, jobID(t, r))
	for _, id := range scheduled {
		expectOutbound(t, owner, id, "cancelled")
	}

	// Organisation B: nothing of organisation A is visible or cancellable.
	ob := owner.Do(http.MethodPost, "/v1/organizations", map[string]any{"name": "Org B Queues", "slug": fmt.Sprintf("org-b-q-%d", time.Now().UnixNano()%1_000_000)})
	harness.ExpectStatus(t, ob, http.StatusCreated)
	memberB := invitedMember(t, f.Node, ob.Str("id"), "owner")
	rb := memberB.Do(http.MethodGet, "/v1/queues/EMAILBRIDGE_SUBMIT/items", nil)
	harness.ExpectStatus(t, rb, http.StatusOK)
	if total, _ := rb.JSON["total"].(float64); total != 0 {
		t.Fatalf("org B sees A's records: %s", rb.Body)
	}
	harness.ExpectProblem(t, memberB.Do(http.MethodPost, "/v1/queues/EMAILBRIDGE_SUBMIT/items/cancel", map[string]any{"ids": []string{queued[1]}}),
		http.StatusNotFound, "not_found")
	harness.ExpectStatus(t, memberB.Do(http.MethodGet, "/v1/queues/EMAILBRIDGE_SUBMIT/items?scope=all", nil), http.StatusForbidden)
	if c := queueItems(t, memberB, "", "EMAILBRIDGE_SUBMIT"); c["waiting"] != 0 || c["scheduled"] != 0 {
		t.Fatalf("org B counts: %v", c)
	}
	rb = memberB.Do(http.MethodPost, "/v1/queues/EMAILBRIDGE_SUBMIT/items/cancel", map[string]any{"all": true, "confirm": "EMAILBRIDGE_SUBMIT"})
	if m, _ := rb.JSON["matched"].(float64); rb.Status != http.StatusOK || m != 0 {
		t.Fatalf("org B whole queue must match nothing: %d %s", rb.Status, rb.Body)
	}

	// Instance owner: the whole queue across every organisation, with the typed confirmation.
	harness.ExpectProblem(t, owner.Do(http.MethodPost, "/v1/queues/EMAILBRIDGE_SUBMIT/items/cancel",
		map[string]any{"all": true, "scope": "all", "confirm": "submit"}), http.StatusUnprocessableEntity, "queue_confirmation_required")
	r = owner.Do(http.MethodPost, "/v1/queues/EMAILBRIDGE_SUBMIT/items/cancel",
		map[string]any{"all": true, "scope": "all", "confirm": "EMAILBRIDGE_SUBMIT"})
	harness.ExpectStatus(t, r, http.StatusAccepted)
	waitJob(t, owner, jobID(t, r))
	for _, id := range queued[1:] {
		expectOutbound(t, owner, id, "cancelled")
	}
	if c := queueItems(t, owner, "all", "EMAILBRIDGE_SUBMIT"); c["waiting"] != 0 || c["scheduled"] != 0 {
		t.Fatalf("all counts after whole-queue cancel: %v", c)
	}

	// Webhook deliveries: the whole organisation queue; events are read-only.
	r = owner.Do(http.MethodPost, "/v1/queues/EMAILBRIDGE_DELIVERY/items/cancel", map[string]any{"all": true, "confirm": "EMAILBRIDGE_DELIVERY"})
	harness.ExpectStatus(t, r, http.StatusAccepted)
	waitJob(t, owner, jobID(t, r))
	owner.WaitJSON(t, "/v1/queues/EMAILBRIDGE_DELIVERY/items", 10*time.Second, "deliveries cancelled", func(r harness.Resp) bool {
		total, _ := r.JSON["total"].(float64)
		return r.Status == http.StatusOK && total == 0
	})
	harness.ExpectProblem(t, owner.Do(http.MethodPost, "/v1/queues/EMAILBRIDGE_EVENTS/items/cancel",
		map[string]any{"all": true, "confirm": "EMAILBRIDGE_EVENTS"}), http.StatusConflict, "queue_read_only")

	// Resume: the workers pull the cancelled entries and ack them without sending.
	harness.ExpectStatus(t, owner.Do(http.MethodPatch, "/v1/queues/EMAILBRIDGE_SUBMIT", map[string]any{"paused": false}), http.StatusOK)
	time.Sleep(6 * time.Second)
	msgs, err := f.Sim.Client.ListSMTPMessages(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 0 {
		t.Fatalf("cancelled mail reached the simulator: %d messages", len(msgs))
	}
	for _, id := range append(append([]string{}, queued...), scheduled...) {
		expectOutbound(t, owner, id, "cancelled")
	}
}

func queueItems(t *testing.T, c *harness.Client, scope, queue string) map[string]float64 {
	t.Helper()
	path := "/v1/queues"
	if scope != "" {
		path += "?scope=" + scope
	}
	r := c.Do(http.MethodGet, path, nil)
	harness.ExpectStatus(t, r, http.StatusOK)
	data, _ := r.JSON["data"].([]any)
	for _, raw := range data {
		q, _ := raw.(map[string]any)
		if q["name"] != queue {
			continue
		}
		items, _ := q["items"].(map[string]any)
		out := map[string]float64{}
		for k, v := range items {
			if n, ok := v.(float64); ok {
				out[k] = n
			}
		}
		return out
	}
	t.Fatalf("queue %s missing: %s", queue, r.Body)
	return nil
}

func expectOutbound(t *testing.T, c *harness.Client, id, state string) {
	t.Helper()
	r := c.Do(http.MethodGet, "/v1/outbound-messages/"+id, nil)
	harness.ExpectStatus(t, r, http.StatusOK)
	if got := r.Str("state"); got != state {
		t.Fatalf("outbound %s: state %s want %s", id, got, state)
	}
}

func jobID(t *testing.T, r harness.Resp) string {
	t.Helper()
	job, _ := r.JSON["job"].(map[string]any)
	id, _ := job["id"].(string)
	if id == "" {
		t.Fatalf("no job: %s", r.Body)
	}
	return id
}

func waitJob(t *testing.T, c *harness.Client, id string) {
	t.Helper()
	r := c.WaitJSON(t, "/v1/jobs/"+id, 60*time.Second, "job "+id, func(r harness.Resp) bool {
		s := r.Str("state")
		return r.Status == http.StatusOK && (s == "succeeded" || s == "failed" || s == "cancelled")
	})
	if r.Str("state") != "succeeded" {
		t.Fatalf("job %s: %s", id, r.Body)
	}
}
