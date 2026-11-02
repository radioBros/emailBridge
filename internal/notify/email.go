package notify

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/radiobros/emailbridge/internal/sysmail"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

// Email channel (W75 §6.2). A user is emailed about a problem only when BOTH the resolved feature switch
// `notifications.email` (global → organisation; instance-scope problems use the global value) AND the user's
// own `notifications.email.enabled` are on (unset = the role default: on for owners and admins, and for
// instance owners on instance problems; off for operators and viewers), and the problem passes the user's minimum severity and email
// categories. Critical problems (and every problem for users with digest "immediate") are emailed when they
// are raised; the rest wait for the user's hourly / daily digest. Each user gets at most one email per
// problem until its severity rises, or it is resolved and re-opens after the throttle window.

const digestScanLimit = 1000

// digestPeriod is the minimum time between two digests of a user.
func digestPeriod(digest string) time.Duration {
	switch digest {
	case "immediate":
		return 0
	case "daily":
		return 24 * time.Hour
	default:
		return time.Hour
	}
}

// recipients returns the users that may receive email about n with their role: the organisation's members,
// or the instance owners (role owner) for an instance-scope problem.
func (c *Channels) recipients(ctx context.Context, n *Notification) []Recipient {
	if n.Scope == ScopeInstance {
		ids, err := c.Repo.InstanceOwnerIDs(ctx)
		if err != nil {
			c.log().Warn("notify email recipients", "err", err.Error(), "notificationId", n.ID)
			return nil
		}
		return ownerRecipients(ids)
	}
	members, err := OrgMembers(ctx, c.Repo.DB, n.OrganizationID)
	if err != nil {
		c.log().Warn("notify email recipients", "err", err.Error(), "notificationId", n.ID)
		return nil
	}
	return members
}

func ownerRecipients(ids []string) []Recipient {
	ids = unique(ids)
	out := make([]Recipient, len(ids))
	for i, id := range ids {
		out[i] = Recipient{UserID: id, Role: tenancy.RoleOwner}
	}
	return out
}

// digestItem is a problem reaching a user with the role of the membership (or instance ownership) it came
// through.
type digestItem struct {
	n    *Notification
	role tenancy.Role
}

// emailAllowed reports whether prefs' user may be emailed about n now (switch, user toggle, severity,
// category, address) and has not been emailed about it already (rec, the previous email, may be nil).
func (c *Channels) emailAllowed(ctx context.Context, n *Notification, prefs UserPrefs, role tenancy.Role, rec *EmailRecord) bool {
	if !prefs.Active || prefs.Email == "" || !prefs.WantsEmail(n, role) {
		return false
	}
	if !c.Prefs.EmailSwitchOn(ctx, n.OrganizationID) {
		return false
	}
	if rec == nil {
		return true
	}
	if severityRank[n.Severity] > severityRank[rec.Severity] {
		return true // severity rose since the last email
	}
	throttle := SettingDuration(ctx, c.Repo.DB, n.OrganizationID, "notifications.email.throttle", 6*time.Hour)
	return n.OpenedAt.After(rec.EmailedAt) && c.now().Sub(rec.EmailedAt) >= throttle
}

// emailNow sends the immediate email for a new, re-opened or escalated problem.
func (c *Channels) emailNow(ctx context.Context, n *Notification) {
	if c.Mail == nil || c.Prefs == nil || c.Repo == nil {
		return
	}
	now := c.now()
	for _, r := range c.recipients(ctx, n) {
		uid := r.UserID
		prefs := c.Prefs.LoadUserPrefs(ctx, uid, n.OrganizationID)
		critical := n.Severity == SeverityCritical
		if !critical && (prefs.Digest != "immediate" || prefs.InQuietHours(now)) {
			continue // the digest picks it up
		}
		recs, err := c.Repo.EmailRecords(ctx, uid, []string{n.ID})
		if err != nil {
			c.log().Warn("notify email records", "err", err.Error())
			continue
		}
		var rec *EmailRecord
		if r, ok := recs[n.ID]; ok {
			rec = &r
		}
		if !c.emailAllowed(ctx, n, prefs, r.Role, rec) {
			continue
		}
		if c.send(ctx, prefs, renderTitle(n), c.alertBody(n), c.absLink(n.Link)) {
			if err := c.Repo.RecordEmails(ctx, uid, []*Notification{n}, false, now); err != nil {
				c.log().Warn("notify email record", "err", err.Error())
			}
		}
	}
}

// FlushDigests sends every user whose digest is due one email listing the open problems they have not been
// emailed about yet. Runs on the scheduler leader.
func (c *Channels) FlushDigests(ctx context.Context) {
	if c == nil || c.Mail == nil || c.Prefs == nil || c.Repo == nil {
		return
	}
	open, err := c.Repo.OpenForDigest(ctx, digestScanLimit)
	if err != nil {
		c.log().Warn("notify digest scan", "err", err.Error())
		return
	}
	if len(open) == 0 {
		return
	}
	byOrg := map[string][]*Notification{}
	var instance []*Notification
	for _, n := range open {
		if n.Scope == ScopeInstance {
			instance = append(instance, n)
		} else if n.OrganizationID != "" {
			byOrg[n.OrganizationID] = append(byOrg[n.OrganizationID], n)
		}
	}
	perUser := map[string][]digestItem{}
	add := func(rs []Recipient, ns []*Notification) {
		for _, r := range rs {
			for _, n := range ns {
				perUser[r.UserID] = append(perUser[r.UserID], digestItem{n: n, role: r.Role})
			}
		}
	}
	for org, ns := range byOrg {
		members, err := OrgMembers(ctx, c.Repo.DB, org)
		if err != nil {
			c.log().Warn("notify digest members", "err", err.Error(), "organizationId", org)
			continue
		}
		add(members, ns)
	}
	if len(instance) > 0 {
		owners, err := c.Repo.InstanceOwnerIDs(ctx)
		if err != nil {
			c.log().Warn("notify digest owners", "err", err.Error())
		}
		add(ownerRecipients(owners), instance)
	}
	users := make([]string, 0, len(perUser))
	for uid := range perUser {
		users = append(users, uid)
	}
	sort.Strings(users)
	for _, uid := range users {
		if ctx.Err() != nil {
			return
		}
		c.digestFor(ctx, uid, perUser[uid])
	}
}

func (c *Channels) digestFor(ctx context.Context, uid string, candidates []digestItem) {
	now := c.now()
	org := ""
	if len(candidates) > 0 {
		org = candidates[0].n.OrganizationID
	}
	prefs := c.Prefs.LoadUserPrefs(ctx, uid, org)
	if !prefs.Active || (prefs.EmailSet && !prefs.EmailEnabled) || prefs.Email == "" {
		return
	}
	last, err := c.Repo.LastDigestAt(ctx, uid)
	if err != nil {
		c.log().Warn("notify last digest", "err", err.Error())
		return
	}
	if !last.IsZero() && now.Sub(last) < digestPeriod(prefs.Digest) {
		return
	}
	ids := make([]string, len(candidates))
	for i, it := range candidates {
		ids[i] = it.n.ID
	}
	recs, err := c.Repo.EmailRecords(ctx, uid, ids)
	if err != nil {
		c.log().Warn("notify email records", "err", err.Error())
		return
	}
	quiet := prefs.InQuietHours(now)
	var items []*Notification
	seen := map[string]bool{}
	for _, it := range candidates {
		n := it.n
		if seen[n.ID] || (quiet && n.Severity != SeverityCritical) {
			continue
		}
		var rec *EmailRecord
		if r, ok := recs[n.ID]; ok {
			rec = &r
		}
		if c.emailAllowed(ctx, n, prefs, it.role, rec) {
			seen[n.ID] = true
			items = append(items, n)
		}
	}
	if len(items) == 0 {
		return
	}
	sort.SliceStable(items, func(i, j int) bool {
		if severityRank[items[i].Severity] != severityRank[items[j].Severity] {
			return severityRank[items[i].Severity] > severityRank[items[j].Severity]
		}
		return items[i].LastSeen.After(items[j].LastSeen)
	})
	title, body, link := c.renderDigest(items)
	if c.send(ctx, prefs, title, body, link) {
		if err := c.Repo.RecordEmails(ctx, uid, items, true, now); err != nil {
			c.log().Warn("notify digest record", "err", err.Error())
		}
	}
}

func (c *Channels) renderDigest(items []*Notification) (title, body, link string) {
	if len(items) == 1 {
		n := items[0]
		return renderTitle(n), c.alertBody(n), c.absLink(n.Link)
	}
	var b strings.Builder
	for _, n := range items {
		fmt.Fprintf(&b, "- [%s] %s", n.Severity, renderTitle(n))
		if l := c.absLink(n.Link); l != "" {
			b.WriteString(" — " + l)
		}
		b.WriteString("\n")
	}
	return fmt.Sprintf("%d open problems need attention", len(items)), strings.TrimRight(b.String(), "\n"), c.absLink("/")
}

func (c *Channels) send(ctx context.Context, prefs UserPrefs, title, body, link string) bool {
	err := c.Mail.Enqueue(ctx, sysmail.Mail{
		Kind: sysmail.KindAlert, To: prefs.Email, Locale: prefs.Locale,
		Data: map[string]string{"Title": title, "Body": body, "Link": link},
	})
	if err != nil {
		c.log().Warn("notify email enqueue", "err", err.Error(), "userId", prefs.UserID)
		return false
	}
	return true
}

// alertBody is the detail line plus the absolute link to the admin page that fixes the problem.
func (c *Channels) alertBody(n *Notification) string {
	base := humanizeKey(n.DetailKey) + paramsSuffix(n.DetailParams)
	if l := c.absLink(n.Link); l != "" {
		return base + "\n\n" + l
	}
	return base
}

// absLink turns a relative admin route into an absolute URL on the public URL ("" stays "").
func (c *Channels) absLink(link string) string {
	if link == "" || c.PublicURL == "" {
		return link
	}
	return strings.TrimRight(c.PublicURL, "/") + link
}
