package notify

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/radiobros/emailbridge/internal/events"
	"github.com/radiobros/emailbridge/internal/events/types"
	"github.com/radiobros/emailbridge/internal/realtime"
	"github.com/radiobros/emailbridge/internal/sysmail"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

// RealtimeHub is the admin WebSocket surface used by notify.
type RealtimeHub interface {
	Publish(org, topic, typ string, data any, keys map[string]string) error
}

// EventEmitter stores CloudEvents (narrowed for tests).
type EventEmitter interface {
	Emit(ctx context.Context, drafts ...events.Draft) ([]events.Event, error)
}

// Mailer enqueues sysmail jobs.
type Mailer interface {
	Enqueue(ctx context.Context, m sysmail.Mail) error
}

// Channels fans out raise/resolve to realtime, events and email.
type Channels struct {
	Repo      *Repo
	Prefs     *Prefs
	Hub       RealtimeHub
	Events    EventEmitter
	Mail      Mailer
	Log       *slog.Logger
	PublicURL string
	Now       func() time.Time
}

func (c *Channels) now() time.Time {
	if c.Now != nil {
		return c.Now().UTC()
	}
	return time.Now().UTC()
}

// OnRaised is called after a successful UpsertRaise.
func (c *Channels) OnRaised(ctx context.Context, n *Notification, created, severityRose bool) {
	if c == nil || n == nil {
		return
	}
	c.publishRT(ctx, n, rtType(created))
	if created || severityRose {
		c.emitRaised(ctx, n)
	}
	if created || severityRose {
		c.emailNow(ctx, n)
	}
}

// OnResolved is called after ResolveKey.
func (c *Channels) OnResolved(ctx context.Context, n *Notification) {
	if c == nil || n == nil {
		return
	}
	c.publishRT(ctx, n, "notification.resolved")
	c.emitResolved(ctx, n)
}

// scopeKey is the realtime routing key: "instance" for instance-scope problems (instance owners only, see
// VisibleTo), "organization" otherwise.
func scopeKey(n *Notification) string {
	if n.Scope == ScopeInstance {
		return ScopeInstance
	}
	return "organization"
}

func rtType(created bool) string {
	if created {
		return "notification.raised"
	}
	return "notification.updated"
}

func (c *Channels) publishRT(ctx context.Context, n *Notification, typ string) {
	if c.Hub == nil {
		return
	}
	var orgs []string
	if n.Scope == ScopeInstance {
		o, err := c.Repo.InstanceOwnerOrgs(ctx)
		if err != nil {
			c.log().Warn("notify instance orgs", "err", err)
			return
		}
		orgs = o
	} else {
		orgs = []string{n.OrganizationID}
	}
	payload := map[string]any{
		"id": n.ID, "key": n.Key, "category": n.Category, "severity": n.Severity,
		"state": n.State, "titleKey": n.TitleKey, "detailKey": n.DetailKey,
		"link": n.Link, "occurrences": n.Occurrences, "scope": n.Scope,
	}
	for _, org := range orgs {
		if org == "" {
			continue
		}
		if err := c.Hub.Publish(org, "notifications", typ, payload, map[string]string{"scope": scopeKey(n)}); err != nil {
			c.log().Warn("notify realtime", "err", err, "org", org)
		}
	}
}

func (c *Channels) emitRaised(ctx context.Context, n *Notification) {
	if c.Events == nil {
		return
	}
	data := &types.SystemAlertRaised{
		NotificationID: n.ID, Key: n.Key, Category: n.Category, Severity: n.Severity,
		Scope: n.Scope, TitleKey: n.TitleKey, DetailKey: n.DetailKey, Link: n.Link,
		Occurrences: n.Occurrences, Params: n.TitleParams,
	}
	for _, org := range c.eventOrgs(ctx, n) {
		_, err := c.Events.Emit(ctx, events.Draft{
			Type: events.SystemAlertRaised, OrganizationID: org, Subject: n.ID, Data: data,
		})
		if err != nil {
			c.log().Warn("notify event raised", "err", err)
		}
	}
}

func (c *Channels) emitResolved(ctx context.Context, n *Notification) {
	if c.Events == nil || n.ResolvedAt == nil {
		return
	}
	data := &types.SystemAlertResolved{
		NotificationID: n.ID, Key: n.Key, Category: n.Category, Scope: n.Scope, ResolvedAt: *n.ResolvedAt,
	}
	for _, org := range c.eventOrgs(ctx, n) {
		_, err := c.Events.Emit(ctx, events.Draft{
			Type: events.SystemAlertResolved, OrganizationID: org, Subject: n.ID, Data: data,
		})
		if err != nil {
			c.log().Warn("notify event resolved", "err", err)
		}
	}
}

func (c *Channels) eventOrgs(ctx context.Context, n *Notification) []string {
	if n.Scope != ScopeInstance {
		if n.OrganizationID == "" {
			return nil
		}
		return []string{n.OrganizationID}
	}
	orgs, err := c.Repo.InstanceOwnerOrgs(ctx)
	if err != nil {
		return nil
	}
	return orgs
}

func (c *Channels) log() *slog.Logger {
	if c.Log != nil {
		return c.Log
	}
	return slog.Default()
}

func renderTitle(n *Notification) string {
	if n.TitleKey != "" {
		return humanizeKey(n.TitleKey) + paramsSuffix(n.TitleParams)
	}
	return n.Category + " alert"
}

func renderAlertBody(n *Notification) string {
	base := humanizeKey(n.DetailKey) + paramsSuffix(n.DetailParams)
	if n.Link != "" {
		return base + "\n\n" + n.Link
	}
	return base
}

func humanizeKey(k string) string {
	k = strings.TrimPrefix(k, "notify.")
	k = strings.ReplaceAll(k, ".", " ")
	k = strings.ReplaceAll(k, "_", " ")
	if k == "" {
		return "EmailBridge alert"
	}
	return strings.ToUpper(k[:1]) + k[1:]
}

func paramsSuffix(p map[string]string) string {
	if len(p) == 0 {
		return ""
	}
	parts := make([]string, 0, len(p))
	for k, v := range p {
		parts = append(parts, fmt.Sprintf("%s=%s", k, v))
	}
	return " (" + strings.Join(parts, ", ") + ")"
}

func unique(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// VisibleTo is the realtime visibility rule of the notifications topic: instance-scope problems reach
// instance owners only; organisation problems reach every member of the organisation (the hub already
// routes by organisation).
func VisibleTo(c tenancy.Caller, msg realtime.Message) bool {
	return msg.Keys["scope"] != ScopeInstance || c.InstanceOwner
}
