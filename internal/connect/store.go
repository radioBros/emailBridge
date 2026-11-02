package connect

import (
	"context"
	"time"
)

// Store is the persistence used by Service (Mongo Repo or test fake).
type Store interface {
	InsertLink(ctx context.Context, l *Link) error
	GetLink(ctx context.Context, id string) (*Link, error)
	MarkLinkUsed(ctx context.Context, id string, at time.Time) error
	GetOrg(ctx context.Context, id string) (*orgDoc, error)
	SetOrgBranding(ctx context.Context, id string, branding map[string]any, version int64) (*orgDoc, error)
	ListOAuthApps(ctx context.Context, org string) ([]OAuthAppBrief, error)
	GetAccount(ctx context.Context, org, id string) (*accountDoc, error)
	SetIdentity(ctx context.Context, org, id string, pin Identity, version int64) error
}

// Adapt Repo to Store.
func (r *Repo) InsertLink(ctx context.Context, l *Link) error { return r.insertLink(ctx, l) }
func (r *Repo) GetLink(ctx context.Context, id string) (*Link, error) {
	return r.getLink(ctx, id)
}
func (r *Repo) MarkLinkUsed(ctx context.Context, id string, at time.Time) error {
	return r.markLinkUsed(ctx, id, at)
}
func (r *Repo) GetOrg(ctx context.Context, id string) (*orgDoc, error) { return r.getOrg(ctx, id) }
func (r *Repo) SetOrgBranding(ctx context.Context, id string, branding map[string]any, version int64) (*orgDoc, error) {
	return r.setOrgBranding(ctx, id, branding, version)
}
func (r *Repo) ListOAuthApps(ctx context.Context, org string) ([]OAuthAppBrief, error) {
	return r.listOAuthApps(ctx, org)
}
func (r *Repo) GetAccount(ctx context.Context, org, id string) (*accountDoc, error) {
	return r.getAccount(ctx, org, id)
}
func (r *Repo) SetIdentity(ctx context.Context, org, id string, pin Identity, version int64) error {
	return r.setIdentity(ctx, org, id, pin, version)
}
