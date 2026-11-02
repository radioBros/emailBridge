package accounts

import "testing"

func TestStateMachineExhaustive(t *testing.T) {
	// Expected transitions from S5 §2 (+ API disable/delete/resume; P1.07 edges).
	want := map[string]map[Event]string{
		StateNew: {
			EventLeaseClaimed: StateConnecting,
			EventDisable:      StateDisabled,
			EventDelete:       StateDeleting,
		},
		StateConnecting: {
			EventLoggedInFirstSync: StateSyncing,
			EventLoggedInIndexed:   StateConnected,
			EventAuthRejected:      StateAuthError,
			EventConnectFailed:     StateConnectError,
			EventLeaseReleased:     StateDisconnected,
			EventDisable:           StateDisabled,
			EventDelete:            StateDeleting,
		},
		StateSyncing: {
			EventInitialSyncDone: StateConnected,
			EventConnectionLost:  StateConnectError,
			EventReconnect:       StateConnecting,
			EventLeaseReleased:   StateDisconnected,
			EventDisable:         StateDisabled,
			EventDelete:          StateDeleting,
		},
		StateConnected: {
			EventConnectionLost: StateConnectError,
			EventReconnect:      StateConnecting,
			EventLeaseReleased:  StateDisconnected,
			EventDisable:        StateDisabled,
			EventDelete:         StateDeleting,
		},
		StateAuthError: {
			EventBackoffElapsed:     StateConnecting,
			EventCredentialsChanged: StateConnecting,
			EventAuthDisableTimeout: StateDisabled,
			EventLeaseReleased:      StateDisconnected,
			EventDisable:            StateDisabled,
			EventDelete:             StateDeleting,
		},
		StateConnectError: {
			EventBackoffElapsed:     StateConnecting,
			EventCredentialsChanged: StateConnecting,
			EventLeaseReleased:      StateDisconnected,
			EventDisable:            StateDisabled,
			EventDelete:             StateDeleting,
		},
		StateDisconnected: {
			EventLeaseClaimed: StateConnecting,
			EventDisable:      StateDisabled,
			EventDelete:       StateDeleting,
		},
		StateDisabled: {
			EventResume:             StateConnecting,
			EventCredentialsChanged: StateConnecting,
			EventDelete:             StateDeleting,
		},
		StateDeleting: {},
	}

	for _, from := range AllStates {
		for _, ev := range AllEvents {
			got, ok := Apply(from, ev)
			exp, expOK := want[from][ev]
			if ok != expOK || got != exp {
				t.Errorf("Apply(%s, %s) = (%q, %v); want (%q, %v)", from, ev, got, ok, exp, expOK)
			}
		}
	}
}
