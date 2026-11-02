//go:build e2e && chaos

// Package chaos holds the scripted-fault scenarios of S14 §6 (tags e2e + chaos, `task test:chaos`).
package chaos

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/simulator"
	"github.com/radiobros/emailbridge/test/e2e/harness"
)

// A node killed while it waits for the reply to the end of DATA: the server may have accepted the message.
// Under atMostOnce the message must end as outcomeUnknown and never be sent again; under standard it is
// retried and delivered (S6 §3.7, S14 §6).
func TestSendCrashAfterData(t *testing.T) {
	for _, tc := range []struct {
		policy    string
		wantState string
	}{
		{"atMostOnce", "outcomeUnknown"},
		{"standard", "sent"},
	} {
		t.Run(tc.policy, func(t *testing.T) {
			sim := harness.StartSim(t)
			n := harness.StartP1Node(t, nil)
			owner, _ := n.OwnerSession(t)

			user := fmt.Sprintf("crash%d", time.Now().UnixNano())
			email := user + "@sim.e2e.test"
			sim.CreateSimUser(t, user, "sim-secret")
			body := harness.AccountCreateBody(t, sim, user, "sim-secret", email, "Crash "+tc.policy,
				map[string]any{"sandbox.enabled": false}, tc.policy)
			acc := owner.Do(http.MethodPost, "/v1/accounts", body)
			harness.ExpectStatus(t, acc, http.StatusCreated)
			accID := acc.Str("id")
			owner.WaitJSON(t, "/v1/accounts/"+accID, 60*time.Second, "connected", func(r harness.Resp) bool {
				return r.Status == http.StatusOK && r.Str("state") == "connected"
			})

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()
			// The server takes the body, then holds the final reply long enough for the node to die waiting.
			if err := sim.Client.SetSMTPScript(ctx, user, simulator.SMTPScript{"dataEnd": {SlowDataEnd: 15 * time.Second}}); err != nil {
				t.Fatal(err)
			}
			r := owner.Do(http.MethodPost, "/v1/accounts/"+accID+"/outbound-messages", map[string]any{
				"to": []map[string]any{{"address": email}}, "subject": "crash-" + tc.policy, "text": "x",
				"policy": tc.policy,
			})
			harness.ExpectStatus(t, r, http.StatusAccepted)
			id := r.Str("id")

			harness.WaitUntil(t, 60*time.Second, "worker waiting after DATA", func() bool {
				st := lastStage(owner.Do(http.MethodGet, "/v1/outbound-messages/"+id, nil))
				return st == "data" || st == "dataEnd" // body on the wire, final reply pending
			})
			n.Kill()
			if err := sim.Client.ClearSMTPScript(ctx, user); err != nil {
				t.Fatal(err)
			}
			n.Restart() // the owner's session lives in MongoDB, so the same client stays signed in

			// The abandoned send is recovered after its heartbeat goes stale (30 s) by the restarted node.
			out := owner.WaitJSON(t, "/v1/outbound-messages/"+id, 3*time.Minute, tc.wantState, func(r harness.Resp) bool {
				st := r.Str("state")
				return r.Status == http.StatusOK && (st == "outcomeUnknown" || st == "sent" || st == "failed")
			})
			if out.Str("state") != tc.wantState {
				t.Fatalf("state=%s, want %s: %s", out.Str("state"), tc.wantState, out.Body)
			}

			// The first session finishes on the simulator side after the delay; count once it has.
			copies := func() int {
				msgs, err := sim.Client.ListSMTPMessages(ctx)
				if err != nil {
					t.Fatal(err)
				}
				return len(msgs)
			}
			switch tc.policy {
			case "atMostOnce":
				harness.WaitUntil(t, 30*time.Second, "first copy recorded", func() bool { return copies() >= 1 })
				time.Sleep(10 * time.Second) // room for a wrong resend
				if got := copies(); got != 1 {
					t.Fatalf("atMostOnce: the server received %d copies, want exactly 1", got)
				}
			case "standard":
				harness.WaitUntil(t, 30*time.Second, "original + retry recorded", func() bool { return copies() >= 2 })
			}
		})
	}
}

// lastStage returns the stage of the outbound message's last attempt, or "".
func lastStage(r harness.Resp) string {
	if r.Status != http.StatusOK {
		return ""
	}
	attempts, _ := r.JSON["attempts"].([]any)
	if len(attempts) == 0 {
		return ""
	}
	last, _ := attempts[len(attempts)-1].(map[string]any)
	stage, _ := last["stage"].(string)
	return stage
}
