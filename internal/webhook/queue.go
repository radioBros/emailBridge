package webhook

import (
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// CancellableStates are the delivery states the queue page (W72 §1) can cancel: waiting, retry-waiting and
// dead-lettered. delivering holds the delivery lease and is never cancelled.
var CancellableStates = []string{StatePending, StateRetrying, StateDeadLettered}

// CancelUpdate cancels a delivery; callers always pair it with a filter on CancellableStates. cancelled is a
// final state, so HandleDelivery acks the stream entry without sending, and strict ordering stops waiting for
// it (hasEarlierUnfinished).
func CancelUpdate(now time.Time) bson.M {
	return bson.M{
		"$set":   bson.M{"state": StateCancelled, "updatedAt": now.UTC()},
		"$unset": bson.M{"nextAttemptAt": ""},
		"$inc":   bson.M{"version": 1},
	}
}
