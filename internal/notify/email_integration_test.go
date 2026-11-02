//go:build integration

package notify

import (
	"context"
	"fmt"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/radiobros/emailbridge/internal/features"
	"github.com/radiobros/emailbridge/internal/settings"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

// emailFixture is one organisation with one member, settings written through the real settings service.
type emailFixture struct {
	t        *testing.T
	db       *store.DB
	eng      *Engine
	svc      *Service
	mail     *memMail
	settings *settings.Service
	org      string
	user     string
	email    string
	owner    bool         // instance owner
	role     tenancy.Role // organisation role
}

func newEmailFixture(t *testing.T, db *store.DB, eng *Engine, svc *Service, mail *memMail, instanceOwner bool) *emailFixture {
	t.Helper()
	return newEmailFixtureRole(t, db, eng, svc, mail, instanceOwner, tenancy.RoleOwner)
}

// newEmailFixtureRole is newEmailFixture with the member's organisation role.
func newEmailFixtureRole(t *testing.T, db *store.DB, eng *Engine, svc *Service, mail *memMail, instanceOwner bool, orgRole tenancy.Role) *emailFixture {
	t.Helper()
	f := &emailFixture{
		t: t, db: db, eng: eng, svc: svc, mail: mail,
		settings: settings.NewService(db, nil, features.DefaultRegistry(), nil),
		org:      store.NewID(store.PrefixOrganization), user: store.NewID(store.PrefixUser), owner: instanceOwner,
		role: orgRole,
	}
	f.email = f.user + "@notify.test"
	role := "none"
	if instanceOwner {
		role = "owner"
	}
	ctx := context.Background()
	if _, err := db.Collection("users").InsertOne(ctx, bson.M{
		"_id": f.user, "email": f.email, "instanceRole": role, "status": "active", "locale": "it",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Collection("memberships").InsertOne(ctx, bson.M{
		"_id": store.NewID(store.PrefixMembership), "userId": f.user, "organizationId": f.org, "role": string(orgRole),
	}); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *emailFixture) ctx() context.Context {
	return ctxUser(f.org, f.user, f.role, f.owner)
}

// setSwitch writes notifications.email at scope ("global" | "organization"); value nil clears it.
func (f *emailFixture) setSwitch(scope string, value *bool, locked bool) {
	f.t.Helper()
	var v any
	if value != nil {
		v = map[string]any{"value": *value, "locked": locked}
	}
	ctx := ctxUser(f.org, f.user, tenancy.RoleOwner, true)
	if _, err := f.settings.Patch(ctx, scope, map[string]any{"switches": map[string]any{switchEmail: v}}, ""); err != nil {
		f.t.Fatalf("patch %s switch: %v", scope, err)
	}
}

// setUser writes the user's notifications.email object through PATCH /v1/me/settings semantics.
func (f *emailFixture) setUser(email map[string]any) {
	f.t.Helper()
	if _, err := f.settings.PatchUserSettings(f.ctx(), map[string]any{"notifications": map[string]any{"email": email}}, ""); err != nil {
		f.t.Fatalf("patch user settings: %v", err)
	}
}

func (f *emailFixture) mailsTo() int {
	f.mail.mu.Lock()
	defer f.mail.mu.Unlock()
	n := 0
	for _, m := range f.mail.sent {
		if m.To == f.email {
			n++
		}
	}
	return n
}

func (f *emailFixture) raise(key, severity string) *Notification {
	f.t.Helper()
	n, err := f.eng.RaiseSync(context.Background(), Problem{
		Scope: f.org, Key: key, Category: CatWebhooks, Severity: severity,
		TitleKey: "notify.webhook.failing.title", DetailKey: "notify.webhook.failing.detail", Link: "/webhooks/x",
	})
	if err != nil {
		f.t.Fatal(err)
	}
	return n
}

// inAppVisible asserts the problem is in the user's list and bell summary, whatever the email settings.
func (f *emailFixture) inAppVisible(n *Notification) {
	f.t.Helper()
	page, err := f.svc.List(f.ctx(), ListFilter{})
	if err != nil {
		f.t.Fatal(err)
	}
	found := false
	for _, row := range page.Data {
		found = found || row.ID == n.ID
	}
	sum, err := f.svc.Summary(f.ctx())
	if err != nil {
		f.t.Fatal(err)
	}
	if !found || sum.Open < 1 || sum.Unread < 1 {
		f.t.Fatalf("in-app must always show the problem: found=%v summary=%+v", found, sum)
	}
}

func boolp(b bool) *bool { return &b }

// TestIntegrationEmailToggleMatrix: email goes out only when the resolved feature switch notifications.email
// (global → organisation, global lock wins) AND the user's notifications.email.enabled are both on — unset
// means the role default (owner/admin on, operator/viewer off; owner decision 2026-10-27); in-app always
// shows the problem. Every combination of global {unset,on,off,off-locked,on-locked} × organisation
// {unset,on,off} × user {unset,true,false} × role {owner,viewer} (all four roles on the global=unset rows,
// which keeps the run short; the role default itself is a pure function covered by TestEmailRoleDefaults).
func TestIntegrationEmailToggleMatrix(t *testing.T) {
	db, eng, svc, mail, _, _ := harness(t)
	type sw struct {
		name   string
		value  *bool
		locked bool
	}
	globals := []sw{{"unset", nil, false}, {"on", boolp(true), false}, {"off", boolp(false), false},
		{"off-locked", boolp(false), true}, {"on-locked", boolp(true), true}}
	orgs := []sw{{"unset", nil, false}, {"on", boolp(true), false}, {"off", boolp(false), false}}
	users := []struct {
		name  string
		value *bool
	}{{"unset", nil}, {"true", boolp(true)}, {"false", boolp(false)}}
	allRoles := []tenancy.Role{tenancy.RoleOwner, tenancy.RoleAdmin, tenancy.RoleOperator, tenancy.RoleViewer}
	someRoles := []tenancy.Role{tenancy.RoleOwner, tenancy.RoleViewer}

	for _, g := range globals {
		for _, o := range orgs {
			for _, u := range users {
				roles := someRoles
				if g.value == nil {
					roles = allRoles
				}
				for _, role := range roles {
					name := fmt.Sprintf("global=%s/org=%s/user=%s/role=%s", g.name, o.name, u.name, role)
					t.Run(name, func(t *testing.T) {
						f := newEmailFixtureRole(t, db, eng, svc, mail, false, role)
						f.setSwitch("global", nil, false) // clear the lock first so the org write is allowed
						if o.value != nil {
							f.setSwitch("organization", o.value, false)
						}
						f.setSwitch("global", g.value, g.locked)
						if u.value != nil {
							f.setUser(map[string]any{"enabled": *u.value})
						}
						// Expected: the resolved switch (default ON; org overrides global unless global is locked).
						switchOn := true
						if g.value != nil {
							switchOn = *g.value
						}
						if o.value != nil && !g.locked {
							switchOn = *o.value
						}
						userOn := role == tenancy.RoleOwner || role == tenancy.RoleAdmin
						if u.value != nil {
							userOn = *u.value
						}
						want := 0
						if switchOn && userOn {
							want = 1
						}
						if got := f.eng.Channels.Prefs.EmailSwitchOn(context.Background(), f.org); got != switchOn {
							t.Fatalf("resolved switch = %v, want %v", got, switchOn)
						}
						n := f.raise("matrix:"+name, SeverityCritical) // critical → immediate path
						if got := f.mailsTo(); got != want {
							t.Fatalf("immediate emails = %d, want %d", got, want)
						}
						f.inAppVisible(n)
						// The digest never sends what the immediate path withheld, nor repeats what it sent.
						f.raise("matrix-digest:"+name, SeverityWarning)
						f.eng.Channels.FlushDigests(context.Background())
						if got := f.mailsTo(); got != 2*want {
							t.Fatalf("after digest emails = %d, want %d", got, 2*want)
						}
					})
				}
			}
		}
	}
	// leave the global switch cleared for other tests sharing nothing but hygiene
	f := newEmailFixture(t, db, eng, svc, mail, false)
	f.setSwitch("global", nil, false)
}

// TestIntegrationEmailInstanceScopeUsesGlobalSwitch: instance-scope problems email instance owners only and
// follow the global switch (an organisation switch does not silence them).
func TestIntegrationEmailInstanceScopeUsesGlobalSwitch(t *testing.T) {
	db, eng, svc, mail, _, _ := harness(t)
	owner := newEmailFixture(t, db, eng, svc, mail, true)
	member := newEmailFixture(t, db, eng, svc, mail, false)
	owner.setSwitch("organization", boolp(false), false)
	if _, err := eng.RaiseSync(context.Background(), Problem{
		Scope: ScopeInstance, Key: "platform.node:n1", Category: CatPlatform, Severity: SeverityCritical,
		TitleKey: "notify.platform.node.title", DetailKey: "notify.platform.node.detail", Link: "/cluster",
	}); err != nil {
		t.Fatal(err)
	}
	if owner.mailsTo() != 1 || member.mailsTo() != 0 {
		t.Fatalf("instance problem: owner=%d member=%d", owner.mailsTo(), member.mailsTo())
	}
	owner.setSwitch("global", boolp(false), false)
	if _, err := eng.RaiseSync(context.Background(), Problem{
		Scope: ScopeInstance, Key: "platform.node:n2", Category: CatPlatform, Severity: SeverityCritical,
		TitleKey: "notify.platform.node.title", DetailKey: "notify.platform.node.detail",
	}); err != nil {
		t.Fatal(err)
	}
	if owner.mailsTo() != 1 {
		t.Fatalf("global switch off must silence instance email: %d", owner.mailsTo())
	}
}

// TestIntegrationEmailRecipientsByRole: the same user is a viewer in org A and an owner in org B; with the
// toggle unset only B's problems are emailed (immediate and digest), A's stay in-app; an explicit opt-in
// brings A's in; an instance owner who is only a viewer of an organisation still gets instance problems.
func TestIntegrationEmailRecipientsByRole(t *testing.T) {
	db, eng, svc, mail, _, _ := harness(t)
	ctx := context.Background()
	a := newEmailFixtureRole(t, db, eng, svc, mail, true, tenancy.RoleViewer)
	orgB := store.NewID(store.PrefixOrganization)
	if _, err := db.Collection("memberships").InsertOne(ctx, bson.M{
		"_id": store.NewID(store.PrefixMembership), "userId": a.user, "organizationId": orgB, "role": "owner",
	}); err != nil {
		t.Fatal(err)
	}
	// A pending invitation (no user) and an operator of org A never get mail by default.
	op := newEmailFixtureRole(t, db, eng, svc, mail, false, tenancy.RoleOperator)
	if _, err := db.Collection("memberships").UpdateOne(ctx, bson.M{"userId": op.user}, bson.M{"$set": bson.M{"organizationId": a.org}}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Collection("memberships").InsertOne(ctx, bson.M{
		"_id": store.NewID(store.PrefixMembership), "email": "pending@notify.test", "organizationId": a.org, "role": "admin",
	}); err != nil {
		t.Fatal(err)
	}
	raise := func(scope, key, sev string) {
		t.Helper()
		if _, err := eng.RaiseSync(ctx, Problem{
			Scope: scope, Key: key, Category: CatWebhooks, Severity: sev,
			TitleKey: "notify.webhook.failing.title", DetailKey: "notify.webhook.failing.detail", Link: "/webhooks/x",
		}); err != nil {
			t.Fatal(err)
		}
	}
	raise(a.org, "role:a-crit", SeverityCritical)
	if a.mailsTo() != 0 || op.mailsTo() != 0 {
		t.Fatalf("viewer/operator emailed by default: viewer=%d operator=%d", a.mailsTo(), op.mailsTo())
	}
	raise(orgB, "role:b-crit", SeverityCritical)
	if a.mailsTo() != 1 {
		t.Fatalf("owner of B must be emailed: %d", a.mailsTo())
	}
	raise(a.org, "role:a-warn", SeverityWarning)
	raise(orgB, "role:b-warn", SeverityWarning)
	eng.Channels.FlushDigests(ctx)
	mail.mu.Lock()
	last := mail.sent[len(mail.sent)-1]
	mail.mu.Unlock()
	if a.mailsTo() != 2 || last.Data["Title"] == "2 open problems need attention" {
		t.Fatalf("digest must carry only org B's problem: mails=%d last=%+v", a.mailsTo(), last.Data)
	}
	if op.mailsTo() != 0 {
		t.Fatalf("operator digest: %d", op.mailsTo())
	}
	// In-app is unchanged for the viewer.
	page, err := svc.List(a.ctx(), ListFilter{})
	if err != nil || len(page.Data) == 0 {
		t.Fatalf("viewer in-app list: %v %+v", err, page)
	}
	// Instance problems reach the instance owner whatever their organisation role.
	raise(ScopeInstance, "role:instance", SeverityCritical)
	if a.mailsTo() != 3 || op.mailsTo() != 0 {
		t.Fatalf("instance problem: owner=%d operator=%d", a.mailsTo(), op.mailsTo())
	}
	// Explicit opt-in: the operator now gets org A's critical problems.
	op.setUser(map[string]any{"enabled": true})
	raise(a.org, "role:a-crit2", SeverityCritical)
	if op.mailsTo() != 1 {
		t.Fatalf("operator opt-in: %d", op.mailsTo())
	}
}

// TestIntegrationEmailThrottleAndDigest: one email per problem per user; severity rise re-emails; re-open
// inside the throttle does not; the digest is one email for several problems, at most once per period, and
// honours minimum severity, email categories and disabled users.
func TestIntegrationEmailThrottleAndDigest(t *testing.T) {
	db, eng, svc, mail, _, _ := harness(t)
	f := newEmailFixture(t, db, eng, svc, mail, false)
	ctx := context.Background()

	// warning → no immediate email (hourly digest), bump → nothing.
	f.raise("thr:a", SeverityWarning)
	f.raise("thr:a", SeverityWarning)
	if f.mailsTo() != 0 {
		t.Fatalf("warning must wait for the digest: %d", f.mailsTo())
	}
	f.raise("thr:b", SeverityWarning)
	f.raise("thr:info", SeverityInfo) // under the default minimum severity
	eng.Channels.FlushDigests(ctx)
	if f.mailsTo() != 1 {
		t.Fatalf("one digest for two problems: %d", f.mailsTo())
	}
	mail.mu.Lock()
	last := mail.sent[len(mail.sent)-1]
	mail.mu.Unlock()
	if last.Data["Title"] != "2 open problems need attention" || last.Locale != "it" {
		t.Fatalf("digest mail: %+v", last)
	}
	// Second flush inside the hour: nothing, even for a new problem.
	f.raise("thr:c", SeverityWarning)
	eng.Channels.FlushDigests(ctx)
	if f.mailsTo() != 1 {
		t.Fatalf("hourly digest sent twice: %d", f.mailsTo())
	}
	// An hour later: only the new problem.
	if _, err := db.Collection(EmailsCollection).UpdateMany(ctx, bson.M{"userId": f.user},
		bson.M{"$set": bson.M{"emailedAt": time.Now().Add(-61 * time.Minute)}}); err != nil {
		t.Fatal(err)
	}
	eng.Channels.FlushDigests(ctx)
	if f.mailsTo() != 2 {
		t.Fatalf("second digest: %d", f.mailsTo())
	}
	mail.mu.Lock()
	last = mail.sent[len(mail.sent)-1]
	mail.mu.Unlock()
	if last.Data["Link"] != "https://eb.test/webhooks" {
		t.Fatalf("single-item digest link: %+v", last.Data)
	}

	// Severity rises → immediate email; further critical bumps → none.
	f.raise("thr:a", SeverityCritical)
	f.raise("thr:a", SeverityCritical)
	if f.mailsTo() != 3 {
		t.Fatalf("severity rise: %d", f.mailsTo())
	}
	// Resolve + re-open inside the 6 h throttle → no email; after it → email.
	if _, err := eng.ResolveSync(ctx, f.org, "thr:a"); err != nil {
		t.Fatal(err)
	}
	f.raise("thr:a", SeverityCritical)
	if f.mailsTo() != 3 {
		t.Fatalf("re-open inside the throttle: %d", f.mailsTo())
	}
	if _, err := eng.ResolveSync(ctx, f.org, "thr:a"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Collection(EmailsCollection).UpdateMany(ctx, bson.M{"userId": f.user},
		bson.M{"$set": bson.M{"emailedAt": time.Now().Add(-7 * time.Hour)}}); err != nil {
		t.Fatal(err)
	}
	f.raise("thr:a", SeverityCritical)
	if f.mailsTo() != 4 {
		t.Fatalf("re-open after the throttle: %d", f.mailsTo())
	}

	// Category off, digest immediate, min severity info.
	f.setUser(map[string]any{"digest": "immediate", "minSeverity": "info", "categories": map[string]any{"webhooks": false}})
	f.raise("thr:cat", SeverityWarning)
	if f.mailsTo() != 4 {
		t.Fatalf("category off: %d", f.mailsTo())
	}
	f.setUser(map[string]any{"categories": map[string]any{"webhooks": true}})
	f.raise("thr:imm", SeverityInfo)
	if f.mailsTo() != 5 {
		t.Fatalf("immediate digest user, info severity: %d", f.mailsTo())
	}
	// Disabled users get nothing.
	if _, err := db.Collection("users").UpdateOne(ctx, bson.M{"_id": f.user}, bson.M{"$set": bson.M{"status": "disabled"}}); err != nil {
		t.Fatal(err)
	}
	f.raise("thr:disabled", SeverityCritical)
	eng.Channels.FlushDigests(ctx)
	if f.mailsTo() != 5 {
		t.Fatalf("disabled user emailed: %d", f.mailsTo())
	}
}

// TestIntegrationEmailMuteDoesNotSilenceEmail: muting a category hides it in-app only; email follows the
// email categories.
func TestIntegrationEmailMuteDoesNotSilenceEmail(t *testing.T) {
	db, eng, svc, mail, _, _ := harness(t)
	f := newEmailFixture(t, db, eng, svc, mail, false)
	if _, err := f.settings.PatchUserSettings(f.ctx(), map[string]any{"notifications": map[string]any{
		"mutedCategories": []any{CatWebhooks},
	}}, ""); err != nil {
		t.Fatal(err)
	}
	f.raise("mute:a", SeverityCritical)
	if f.mailsTo() != 1 {
		t.Fatalf("muted in-app category still emails: %d", f.mailsTo())
	}
	sum, err := f.svc.Summary(f.ctx())
	if err != nil || sum.Open != 0 {
		t.Fatalf("muted category hidden in-app: %+v %v", sum, err)
	}
}

// TestIntegrationSettingDurationReadsSettingsService: thresholds written through the settings service
// (day units included) are what the watchers read.
func TestIntegrationSettingDurationReadsSettingsService(t *testing.T) {
	db, eng, svc, mail, _, _ := harness(t)
	f := newEmailFixture(t, db, eng, svc, mail, true)
	ctx := ctxUser(f.org, f.user, tenancy.RoleOwner, true)
	if _, err := f.settings.Patch(ctx, "global", map[string]any{"values": map[string]any{
		"notifications.accounts.syncStall": "2d", "notifications.webhooks.failureThreshold": 3,
		"notifications.watchers.interval": "10s",
	}}, ""); err != nil {
		t.Fatal(err)
	}
	if d := SettingDuration(context.Background(), db, "", "notifications.accounts.syncStall", time.Hour); d != 48*time.Hour {
		t.Fatalf("syncStall = %v", d)
	}
	if n := SettingInt(context.Background(), db, "", "notifications.webhooks.failureThreshold", 5); n != 3 {
		t.Fatalf("failureThreshold = %d", n)
	}
	if d := watcherInterval(context.Background(), db); d != 10*time.Second {
		t.Fatalf("interval = %v", d)
	}
}
