package submit

import "context"

// SuppressionChecker refuses sends to suppressed addresses (P5.2 / W53).
type SuppressionChecker interface {
	CheckAddresses(ctx context.Context, org string, addresses []string, mailingListID string) error
}

func (s *Service) checkSuppression(ctx context.Context, org string, req SubmitRequest) error {
	if s.Suppression == nil {
		return nil
	}
	addrs := recipientAddresses(req)
	if len(addrs) == 0 {
		return nil
	}
	listID, err := normalizeListID(req.ListID)
	if err != nil {
		return err
	}
	return s.Suppression.CheckAddresses(ctx, org, addrs, listID)
}

func recipientAddresses(req SubmitRequest) []string {
	var out []string
	add := func(list []AddressInput) {
		for _, a := range list {
			if a.Address != "" {
				out = append(out, a.Address)
			}
		}
	}
	if len(req.Recipients) > 0 {
		for _, r := range req.Recipients {
			add(r.To)
			add(r.Cc)
			add(r.Bcc)
		}
		return out
	}
	add(req.To)
	add(req.Cc)
	add(req.Bcc)
	return out
}
