package accounts

// Event is a state-machine stimulus (S5 §2). Sync-engine events go through Service.Transition;
// the API may apply EventDisable and EventDelete only (plus EventResume via POST …/resume).
type Event string

// State-machine events.
const (
	EventLeaseClaimed       Event = "leaseClaimed"
	EventLoggedInFirstSync  Event = "loggedInFirstSync"
	EventLoggedInIndexed    Event = "loggedInIndexed"
	EventInitialSyncDone    Event = "initialSyncDone"
	EventAuthRejected       Event = "authRejected"
	EventConnectFailed      Event = "connectFailed"
	EventConnectionLost     Event = "connectionLost"
	EventBackoffElapsed     Event = "backoffElapsed"
	EventCredentialsChanged Event = "credentialsChanged"
	EventReconnect          Event = "reconnect" // reconnect / configChanged while live (S5 §2)
	EventAuthDisableTimeout Event = "authDisableTimeout"
	EventResume             Event = "resume"
	EventLeaseReleased      Event = "leaseReleased"
	EventDisable            Event = "disable"
	EventDelete             Event = "delete"
)

// AllEvents lists every event (for exhaustive tests).
var AllEvents = []Event{
	EventLeaseClaimed, EventLoggedInFirstSync, EventLoggedInIndexed, EventInitialSyncDone,
	EventAuthRejected, EventConnectFailed, EventConnectionLost, EventBackoffElapsed,
	EventCredentialsChanged, EventReconnect, EventAuthDisableTimeout, EventResume, EventLeaseReleased,
	EventDisable, EventDelete,
}

// Apply returns the next state for (from, event). ok is false when the transition is illegal.
func Apply(from string, event Event) (to string, ok bool) {
	switch event {
	case EventLeaseClaimed:
		switch from {
		case StateNew, StateDisconnected:
			return StateConnecting, true
		}
	case EventLoggedInFirstSync:
		if from == StateConnecting {
			return StateSyncing, true
		}
	case EventLoggedInIndexed:
		if from == StateConnecting {
			return StateConnected, true
		}
	case EventInitialSyncDone:
		if from == StateSyncing {
			return StateConnected, true
		}
	case EventAuthRejected:
		if from == StateConnecting {
			return StateAuthError, true
		}
	case EventConnectFailed:
		if from == StateConnecting {
			return StateConnectError, true
		}
	case EventConnectionLost:
		switch from {
		case StateConnected, StateSyncing:
			return StateConnectError, true
		}
	case EventBackoffElapsed, EventCredentialsChanged:
		switch from {
		case StateAuthError, StateConnectError:
			return StateConnecting, true
		case StateDisabled:
			if event == EventCredentialsChanged {
				return StateConnecting, true
			}
		}
	case EventReconnect:
		switch from {
		case StateConnected, StateSyncing:
			return StateConnecting, true
		}
	case EventAuthDisableTimeout:
		if from == StateAuthError {
			return StateDisabled, true
		}
	case EventResume:
		if from == StateDisabled {
			return StateConnecting, true
		}
	case EventLeaseReleased:
		switch from {
		case StateConnected, StateConnecting, StateSyncing, StateAuthError, StateConnectError:
			return StateDisconnected, true
		}
	case EventDisable:
		switch from {
		case StateNew, StateConnecting, StateSyncing, StateConnected,
			StateAuthError, StateConnectError, StateDisconnected:
			return StateDisabled, true
		}
	case EventDelete:
		if from != StateDeleting && from != "" {
			return StateDeleting, true
		}
	}
	return "", false
}
