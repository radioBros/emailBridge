package fulltext

import (
	"context"
	"time"
)

// defaultPurgeEvery is the purge sweep interval.
const defaultPurgeEvery = 15 * time.Minute

// PurgeDuty is the scheduler duty "fulltext.purge" (leader only): every PurgeEvery it deletes the rows of
// accounts the switch no longer covers — the organisation switched it off, the account
// opted out, or the account is gone or being deleted. An account opting out is also purged at once by the
// indexer (account.updated); the sweep is what catches organisation-level changes and anything missed.
func (s *Service) PurgeDuty(ctx context.Context) {
	every := s.PurgeEvery
	if every <= 0 {
		every = defaultPurgeEvery
	}
	first := time.NewTimer(time.Minute) // let the node settle after it became leader
	defer first.Stop()
	select {
	case <-ctx.Done():
		return
	case <-first.C:
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		if _, err := s.PurgeOnce(ctx); err != nil && ctx.Err() == nil {
			s.log().WarnContext(ctx, "full-text purge sweep failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// PurgeOnce runs one sweep and returns the number of rows deleted.
func (s *Service) PurgeOnce(ctx context.Context) (int64, error) {
	ids, err := s.Repo.AccountIDs(ctx)
	if err != nil {
		return 0, err
	}
	var total int64
	for _, acc := range ids {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		org, err := s.Repo.OrganizationOf(ctx, acc)
		if err != nil {
			return total, err
		}
		if org == "" {
			continue
		}
		on, err := s.Switches.AccountEnabledFresh(ctx, org, acc)
		if err != nil {
			return total, err
		}
		if on {
			continue
		}
		n, err := s.PurgeAccount(systemCtx(ctx, org, acc), org, acc)
		if err != nil {
			return total, err
		}
		total += n
	}
	if total > 0 {
		s.log().InfoContext(ctx, "full-text purge sweep deleted rows", "rows", total)
	}
	return total, nil
}
