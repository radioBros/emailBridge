package submit

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/radiobros/emailbridge/internal/bus"
	"github.com/radiobros/emailbridge/internal/throttle"
)

// ThrottleGate is the P5.5 worker hook (implemented by *throttle.Gate).
type ThrottleGate interface {
	BeforeSandbox(ctx context.Context, m throttle.Message) throttle.Decision
	AfterSandbox(ctx context.Context, m throttle.Message) throttle.Decision
}

func (s *Service) throttleMsg(m *Message, acc *SMTPAccount, recipients []string) throttle.Message {
	tm := throttle.Message{
		OrganizationID: m.OrganizationID,
		AccountID:      m.AccountID,
		From:           m.Envelope.From,
		To:             append([]string(nil), m.Envelope.To...),
		Cc:             append([]string(nil), m.Envelope.Cc...),
		Bcc:            append([]string(nil), m.Bcc...),
		BccCount:       m.Envelope.BccCount,
		Size:           m.Size,
	}
	if len(recipients) > 0 {
		tm.To, tm.Cc, tm.Bcc = recipients, nil, nil
		tm.BccCount = 0
	}
	if acc != nil && acc.ExternalID != nil {
		tm.ExternalID = *acc.ExternalID
	}
	return tm
}

// applyThrottleBefore runs window + rate limit after claim; may requeue or schedule.
// Returns (done, err): done means the worker should stop (message handled or delayed).
func (s *Service) applyThrottleBefore(ctx context.Context, m *Message, acc *SMTPAccount) (done bool, err error) {
	if s.Throttle == nil {
		return false, nil
	}
	d := s.Throttle.BeforeSandbox(ctx, s.throttleMsg(m, acc, nil))
	switch d.Action {
	case throttle.ActionDelay:
		delay := d.Delay
		if delay <= 0 {
			delay = 30 * time.Second
		}
		if err := s.requeue(ctx, m, delay); err != nil {
			return true, err
		}
		return true, bus.DelayRetry(delay, errBusyElsewhere)
	case throttle.ActionSchedule:
		if err := s.scheduleGate(ctx, m, d.Until, d.Reason, d.Detail); err != nil {
			return true, err
		}
		return true, nil
	default:
		return false, nil
	}
}

// applyThrottleAfter runs the pre-send CEL hook after sandbox.
// Returns (done, err). Reject fails via failBeforeSession when Problem is set — caller handles.
func (s *Service) applyThrottleAfter(ctx context.Context, m *Message, acc *SMTPAccount, rewriteTo []string) (done bool, reject error, err error) {
	if s.Throttle == nil {
		return false, nil, nil
	}
	d := s.Throttle.AfterSandbox(ctx, s.throttleMsg(m, acc, rewriteTo))
	switch d.Action {
	case throttle.ActionHold:
		if err := s.holdGate(ctx, m, d.Reason, d.Detail); err != nil {
			return true, nil, err
		}
		return true, nil, nil
	case throttle.ActionReject:
		return true, d.Problem, nil
	default:
		return false, nil, nil
	}
}

func (s *Service) scheduleGate(ctx context.Context, m *Message, until time.Time, reason, detail string) error {
	if until.IsZero() {
		until = s.now().Add(time.Hour)
	}
	_, err := s.Repo.coll().UpdateOne(ctx, bson.M{"_id": m.ID, "state": StateSending},
		bson.M{
			"$set": bson.M{
				"state": StateScheduled, "sendAt": until, "nextAttemptAt": until,
				"scheduleReason": reason, "scheduleDetail": detail,
			},
			"$inc":   bson.M{"version": 1},
			"$unset": bson.M{"worker": "", "heartbeatAt": ""},
		})
	return err
}

func (s *Service) holdGate(ctx context.Context, m *Message, reason, detail string) error {
	_, err := s.Repo.coll().UpdateOne(ctx, bson.M{"_id": m.ID, "state": StateSending},
		bson.M{
			"$set": bson.M{
				"state": StateScheduled, "scheduleReason": reason, "scheduleDetail": detail,
			},
			"$inc": bson.M{"version": 1},
			"$unset": bson.M{
				"worker": "", "heartbeatAt": "", "sendAt": "", "nextAttemptAt": "",
			},
		})
	return err
}
