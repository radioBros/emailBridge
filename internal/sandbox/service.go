package sandbox

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

// Service is the sandbox API (list / get / raw / delete) and retention sweep.
type Service struct {
	Repo    *Repo
	Objects Objects
	Log     *slog.Logger
	Now     func() time.Time
	// RetentionEvery is how often the scheduler duty sweeps expired captures (default 1h).
	RetentionEvery time.Duration
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s *Service) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

// List returns a page of captured messages for the caller's organisation.
func (s *Service) List(ctx context.Context, f ListFilter) (*List, error) {
	if err := tenancy.RequireRead(ctx, "sandbox"); err != nil {
		return nil, err
	}
	if c, ok := tenancy.CallerFrom(ctx); ok && c.Kind == tenancy.CallerAPIKey && c.AccountID != "" {
		if f.AccountID != "" && f.AccountID != c.AccountID {
			return nil, errs.New(errs.CodeForbidden, "The API key is bound to a different account.")
		}
		f.AccountID = c.AccountID
	}
	return s.Repo.list(ctx, f)
}

// Get returns one captured message.
func (s *Service) Get(ctx context.Context, id string) (*Captured, error) {
	if err := tenancy.RequireRead(ctx, "sandbox"); err != nil {
		return nil, err
	}
	if !store.IsID(store.PrefixCapture, id) {
		return nil, errs.New(errs.CodeNotFound, "The captured message does not exist.")
	}
	c, err := s.Repo.get(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := accountBoundOK(ctx, c.AccountID); err != nil {
		return nil, err
	}
	return c, nil
}

// Raw opens the RFC 822 body of a capture.
func (s *Service) Raw(ctx context.Context, id string) (*Captured, io.ReadCloser, error) {
	c, err := s.Get(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	body, err := s.openRaw(ctx, c)
	if err != nil {
		return nil, nil, err
	}
	return c, body, nil
}

// Delete removes a capture and its S3 object (operator+, S10 §4).
func (s *Service) Delete(ctx context.Context, id string) error {
	if err := requireSandboxWrite(ctx); err != nil {
		return err
	}
	if !store.IsID(store.PrefixCapture, id) {
		return errs.New(errs.CodeNotFound, "The captured message does not exist.")
	}
	c, err := s.Repo.get(ctx, id)
	if err != nil {
		return err
	}
	if err := accountBoundOK(ctx, c.AccountID); err != nil {
		return err
	}
	deleted, err := s.Repo.delete(ctx, id)
	if err != nil {
		return err
	}
	if deleted.RawRef != "" && s.Objects != nil {
		if err := s.Objects.Delete(ctx, deleted.RawRef); err != nil {
			s.log().WarnContext(ctx, "sandbox S3 delete after Mongo delete failed", "key", deleted.RawRef, "error", err)
		}
	}
	return nil
}

func accountBoundOK(ctx context.Context, accountID string) error {
	c, ok := tenancy.CallerFrom(ctx)
	if !ok {
		return errs.New(errs.CodeUnauthenticated, "Authentication is required.")
	}
	if c.Kind == tenancy.CallerAPIKey && c.AccountID != "" && c.AccountID != accountID {
		return errs.New(errs.CodeForbidden, "The API key is bound to a different account.")
	}
	return nil
}

func requireSandboxWrite(ctx context.Context) error {
	c, ok := tenancy.CallerFrom(ctx)
	if !ok {
		return errs.New(errs.CodeUnauthenticated, "Authentication is required.")
	}
	if c.Kind == tenancy.CallerAPIKey {
		if hasScope(c.Scopes, "sandbox:write") || hasScope(c.Scopes, "outbound:write") || hasScope(c.Scopes, "admin") {
			return nil
		}
		return errs.New(errs.CodeScopeMissing, "The API key lacks the scope this operation needs.").WithSafe("permission", "sandbox:write")
	}
	return tenancy.Require(ctx, tenancy.PermSend)
}

func hasScope(scopes []string, want string) bool {
	res, mode, _ := strings.Cut(want, ":")
	for _, s := range scopes {
		if s == "admin" || s == want {
			return true
		}
		if mode == "read" && s == res+":write" {
			return true
		}
	}
	return false
}
