package submit

import "go.mongodb.org/mongo-driver/v2/bson"

// CancellableStates are the stored states a message can be cancelled from (S2 §4.3, W72 §1): scheduled and
// queued (including retry-waiting). sending holds the SMTP lease and is never cancelled.
var CancellableStates = []string{StateScheduled, StateQueued}

// CancelUpdate is the update that cancels a message. The single cancel (DELETE /v1/outbound-messages/{id})
// and the queue page (W72) both apply it, always together with a filter on CancellableStates, so a message
// that a worker claimed meanwhile is never touched. The worker acks the stream entry of a cancelled message
// without sending (Work).
func CancelUpdate() bson.M {
	return bson.M{
		"$set":   bson.M{"state": StateCancelled},
		"$inc":   bson.M{"version": 1},
		"$unset": bson.M{"nextAttemptAt": "", "worker": "", "heartbeatAt": ""},
	}
}
