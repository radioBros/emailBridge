package fulltext

import (
	"context"
	"time"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

// SearchInput is GET /v1/messages/search.
type SearchInput struct {
	Q         string
	AccountID string
	From, To  time.Time
	Page      int
	Limit     int
}

// scopeAccount applies the read rules of the caller: an account-bound API key only ever searches its own
// account; with an account named, the caller needs read access to that account's messages; without one,
// read access to the messages of the whole organisation (a key holding only per-account scopes must name the
// account).
func scopeAccount(ctx context.Context, accountID string) (string, error) {
	if c, ok := tenancy.CallerFrom(ctx); ok && c.Kind == tenancy.CallerAPIKey && c.AccountID != "" {
		if accountID != "" && accountID != c.AccountID {
			return "", errs.New(errs.CodeForbidden, "The API key is bound to a different account.")
		}
		accountID = c.AccountID
	}
	if accountID != "" {
		return accountID, tenancy.RequireReadAccount(ctx, "messages", accountID)
	}
	return "", tenancy.RequireRead(ctx, "messages")
}

// Search runs a full-text search over the caller's organisation.
func (s *Service) Search(ctx context.Context, in SearchInput) (*FullTextSearchResult, error) {
	accountID, err := scopeAccount(ctx, in.AccountID)
	if err != nil {
		return nil, err
	}
	org, ok := store.OrganizationFrom(ctx)
	if !ok {
		return nil, store.ErrNoTenant
	}
	on, err := s.Switches.OrgEnabled(ctx, org)
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "fulltext.switch")
	}
	if !on {
		return nil, errs.New(errs.CodeFeatureDisabled, "Full-text search is switched off for this organisation.").
			WithSafe("switch", SwitchName)
	}
	f, err := validateSearch(in, accountID)
	if err != nil {
		return nil, err
	}
	rows, total, capped, err := s.Repo.search(ctx, f)
	if err != nil {
		return nil, err
	}
	// Rows follow the message index: a row whose message left the index (a delete the indexer has not
	// applied yet, a flush, a reset) is not shown, and is deleted now.
	ids := make([]string, len(rows))
	for i := range rows {
		ids[i] = rows[i].ID
	}
	live, err := s.Repo.liveIDs(ctx, org, ids)
	if err != nil {
		return nil, err
	}
	out := &FullTextSearchResult{Data: make([]FullTextHit, 0, len(rows)), Total: total, TotalCapped: capped}
	var orphans []string
	for i := range rows {
		r := &rows[i]
		if !live[r.ID] {
			orphans = append(orphans, r.ID)
			continue
		}
		snippet, hl := buildSnippet(r.Text, f.Query.Terms, snippetRunes)
		to := r.To
		if to == nil {
			to = []string{}
		}
		out.Data = append(out.Data, FullTextHit{
			MessageID: r.MessageID, AccountID: r.AccountID, MailboxID: r.MailboxID, Path: r.Path,
			Subject: r.Subject, From: r.From, To: to, Date: r.Date, Size: r.Size,
			Snippet: snippet, Highlights: hl, Score: r.Score,
		})
	}
	if len(orphans) > 0 {
		if n, err := s.Repo.deleteOrphans(ctx, org, orphans); err != nil {
			s.log().WarnContext(ctx, "full-text orphan cleanup failed", "err", err)
		} else if n > 0 {
			out.Total = max(0, out.Total-n)
		}
	}
	return out, nil
}

// validateSearch checks the parameters and builds the filter.
func validateSearch(in SearchInput, accountID string) (searchFilter, error) {
	q, err := parseQuery(in.Q)
	if err != nil {
		return searchFilter{}, err
	}
	f := searchFilter{Query: q, AccountID: accountID, From: in.From, To: in.To, Page: in.Page, Limit: in.Limit}
	if f.Page <= 0 {
		f.Page = 1
	}
	if f.Limit <= 0 {
		f.Limit = defaultPageSize
	}
	if f.Limit > maxPageSize {
		return searchFilter{}, errs.New(errs.CodeValidationFailed, "limit must be between 1 and 100.").WithSafe("max", maxPageSize)
	}
	if (f.Page-1)*f.Limit >= totalCap {
		return searchFilter{}, errs.New(errs.CodeValidationFailed, "Only the first 1000 matches can be paged; refine the search.").
			WithSafe("max", totalCap)
	}
	if !f.From.IsZero() && !f.To.IsZero() && f.To.Before(f.From) {
		return searchFilter{}, errs.New(errs.CodeValidationFailed, "to must not be before from.")
	}
	return f, nil
}
