package submit

import (
	"context"
	"strings"
	"unicode"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tracking"
)

// TrackingInjector rewrites HTML / List-Unsubscribe for outbound build (P5.2 / W53).
type TrackingInjector interface {
	SwitchState(ctx context.Context, org, accountID string) (opens, clicks, unsub bool)
	TrackState(ctx context.Context, org, accountID string) tracking.TrackState
	Apply(ctx context.Context, in tracking.PrepareInput) (*tracking.PrepareResult, error)
}

func (s *Service) applyTracking(ctx context.Context, org, accountID, outboundID string, req *SubmitRequest) error {
	if req.Raw != "" {
		return nil
	}
	listID, err := normalizeListID(req.ListID)
	if err != nil {
		return err
	}
	req.ListID = listID
	if s.Tracking == nil {
		if listID != "" {
			if req.Headers == nil {
				req.Headers = map[string]string{}
			}
			if _, ok := headerGetCI(req.Headers, "List-Id"); !ok {
				req.Headers["List-Id"] = "<" + listID + ">"
			}
		}
		return nil
	}
	st := s.Tracking.TrackState(ctx, org, accountID)
	opens, clicks := tracking.EffectiveTracking(st, req.TrackOpens, req.TrackClicks)
	unsub := st.Unsub
	if !opens && !clicks && !unsub && listID == "" {
		return nil
	}
	if req.HTML == "" && !unsub && listID == "" {
		return nil
	}
	res, err := s.Tracking.Apply(ctx, tracking.PrepareInput{
		OrgID: org, AccountID: accountID, OutboundID: outboundID,
		Recipient: primaryRecipient(*req), HTML: req.HTML, Headers: req.Headers,
		TrackOpens: opens, TrackClicks: clicks, ListUnsub: unsub, ListID: listID,
	})
	if err != nil {
		return err
	}
	if res != nil {
		req.HTML = res.HTML
		req.Headers = res.Headers
	}
	return nil
}

func headerGetCI(h map[string]string, name string) (string, bool) {
	for k, v := range h {
		if strings.EqualFold(k, name) {
			return v, true
		}
	}
	return "", false
}

func primaryRecipient(req SubmitRequest) string {
	for _, list := range [][]AddressInput{req.To, req.Cc, req.Bcc} {
		for _, a := range list {
			if a.Address != "" {
				return strings.ToLower(a.Address)
			}
		}
	}
	return ""
}

func needsOutboundIDEarly(ctx context.Context, s *Service, org, accountID string, req SubmitRequest) bool {
	if req.Raw != "" {
		return false
	}
	if strings.TrimSpace(req.ListID) != "" {
		return true
	}
	if s.Tracking == nil {
		return false
	}
	st := s.Tracking.TrackState(ctx, org, accountID)
	opens, clicks := tracking.EffectiveTracking(st, req.TrackOpens, req.TrackClicks)
	return opens || clicks || st.Unsub
}

func allocOutboundID() string { return store.NewID(store.PrefixOutbound) }

func normalizeListID(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	raw = strings.Trim(raw, "<>")
	if raw == "" {
		return "", nil
	}
	if len(raw) > 200 {
		return "", errs.New(errs.CodeValidationFailed, "listId must be at most 200 characters.")
	}
	for _, r := range raw {
		if r == '"' || r == '<' || r == '>' || r == '\r' || r == '\n' || !unicode.IsPrint(r) {
			return "", errs.New(errs.CodeValidationFailed, "listId contains invalid characters.")
		}
	}
	return raw, nil
}
