package notify

import (
	"context"
	"time"

	"github.com/radiobros/emailbridge/internal/audit"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

// Service is the HTTP-facing API over Engine/Repo.
type Service struct {
	Repo   *Repo
	Engine *Engine
	Prefs  *Prefs
	Audit  *audit.Writer
	Now    func() time.Time
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s *Service) callerPrefs(ctx context.Context) (tenancy.Caller, UserPrefs, error) {
	c, ok := tenancy.CallerFrom(ctx)
	if !ok {
		return tenancy.Caller{}, UserPrefs{}, errs.New(errs.CodeUnauthenticated, "Sign in required.")
	}
	prefs := UserPrefs{MinSeverity: SeverityWarning}
	if s.Prefs != nil && c.UserID != "" {
		prefs = s.Prefs.LoadUserPrefs(ctx, c.UserID, c.OrganizationID)
	}
	return c, prefs, nil
}

// List returns notifications visible to the caller.
func (s *Service) List(ctx context.Context, f ListFilter) (*Page, error) {
	if err := tenancy.RequireRead(ctx, "notifications"); err != nil {
		return nil, err
	}
	c, prefs, err := s.callerPrefs(ctx)
	if err != nil {
		return nil, err
	}
	return s.Repo.List(ctx, c.OrganizationID, c.InstanceOwner, f, c.UserID, prefs.MutedCategories)
}

// Summary returns bell counts.
func (s *Service) Summary(ctx context.Context) (*Summary, error) {
	if err := tenancy.RequireRead(ctx, "notifications"); err != nil {
		return nil, err
	}
	c, prefs, err := s.callerPrefs(ctx)
	if err != nil {
		return nil, err
	}
	return s.Repo.SummaryCounts(ctx, c.OrganizationID, c.UserID, c.InstanceOwner, prefs.MutedCategories)
}

// MarkRead marks ids as read for the current user.
func (s *Service) MarkRead(ctx context.Context, ids []string) error {
	if err := tenancy.RequireRead(ctx, "notifications"); err != nil {
		return err
	}
	c, _, err := s.callerPrefs(ctx)
	if err != nil {
		return err
	}
	if c.Kind != tenancy.CallerUser || c.UserID == "" {
		return errs.New(errs.CodeForbidden, "Only signed-in users can mark notifications read.")
	}
	if len(ids) == 0 {
		return errs.New(errs.CodeValidationFailed, "ids is required")
	}
	if len(ids) > 200 {
		return errs.New(errs.CodeValidationFailed, "at most 200 ids")
	}
	return s.Repo.MarkRead(ctx, c.UserID, c.OrganizationID, ids, s.now())
}

// MarkAllRead marks all visible as read.
func (s *Service) MarkAllRead(ctx context.Context) error {
	if err := tenancy.RequireRead(ctx, "notifications"); err != nil {
		return err
	}
	c, prefs, err := s.callerPrefs(ctx)
	if err != nil {
		return err
	}
	if c.Kind != tenancy.CallerUser || c.UserID == "" {
		return errs.New(errs.CodeForbidden, "Only signed-in users can mark notifications read.")
	}
	return s.Repo.MarkAllRead(ctx, c.UserID, c.OrganizationID, c.InstanceOwner, prefs.MutedCategories, s.now())
}

// Resolve manually resolves a notification.
// warning/critical need settings.write; info needs write on the category (webhooks/accounts/outbound/…).
func (s *Service) Resolve(ctx context.Context, id string) (*Notification, error) {
	if !store.IsID(store.PrefixNotification, id) {
		return nil, errs.New(errs.CodeNotFound, "The notification does not exist.")
	}
	c, _, err := s.callerPrefs(ctx)
	if err != nil {
		return nil, err
	}
	n, err := s.Repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if n.Scope == ScopeInstance {
		if !c.InstanceOwner {
			return nil, errs.New(errs.CodeNotFound, "The notification does not exist.")
		}
	} else if n.OrganizationID != c.OrganizationID {
		return nil, errs.New(errs.CodeNotFound, "The notification does not exist.")
	}
	if err := requireResolvePerm(ctx, n); err != nil {
		return nil, err
	}
	out, err := s.Engine.ResolveSync(ctx, n.Scope, n.Key)
	if err != nil {
		return nil, err
	}
	if s.Audit != nil {
		_ = s.Audit.Record(ctx, "notification.resolve", audit.Target{Type: "notification", ID: id}, nil)
	}
	if out == nil {
		out = n
	}
	return out, nil
}

func requireResolvePerm(ctx context.Context, n *Notification) error {
	if n.Severity == SeverityWarning || n.Severity == SeverityCritical {
		return tenancy.Require(ctx, tenancy.PermSettingsWrite)
	}
	// info: any member with write on the category
	switch n.Category {
	case CatWebhooks:
		return tenancy.Require(ctx, tenancy.PermWebhooksWrite)
	case CatAccounts, CatOAuth:
		return tenancy.Require(ctx, tenancy.PermAccountsWrite)
	case CatSending, CatQueues:
		return tenancy.Require(ctx, tenancy.PermSend)
	case CatSecurity, CatLimits, CatPlatform, CatInternal, CatStorage, CatIntegrations, CatPEC:
		return tenancy.Require(ctx, tenancy.PermSettingsWrite)
	default:
		return tenancy.Require(ctx, tenancy.PermSettingsWrite)
	}
}
