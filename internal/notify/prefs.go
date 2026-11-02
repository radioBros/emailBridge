package notify

import (
	"context"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/radiobros/emailbridge/internal/config"
	"github.com/radiobros/emailbridge/internal/features"
	"github.com/radiobros/emailbridge/internal/settings"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

const switchEmail = "notifications.email"

// UserPrefs are resolved notification preferences for one user.
type UserPrefs struct {
	// EmailEnabled is the saved notifications.email.enabled; meaningful only when EmailSet. Use EmailOnFor.
	EmailEnabled    bool
	EmailSet        bool   // the user saved notifications.email.enabled explicitly
	Digest          string // hourly | daily | immediate
	MinSeverity     string
	Categories      map[string]bool // email category toggles
	MutedCategories []string        // in-app muted categories
	QuietStart      string
	QuietEnd        string
	QuietTZ         string
	Locale          string
	Email           string
	UserID          string
	Active          bool // the user account is active (disabled users get no email)
}

// Prefs loads user settings + feature switch for email.
type Prefs struct {
	DB       *store.DB
	Registry *features.Registry
	Env      string
}

func (p *Prefs) registry() *features.Registry {
	if p.Registry != nil {
		return p.Registry
	}
	return features.DefaultRegistry()
}

// EmailSwitchOn reports whether notifications.email is on for the org (and global).
func (p *Prefs) EmailSwitchOn(ctx context.Context, org string) bool {
	sw, ok := p.registry().Get(switchEmail)
	if !ok {
		return true
	}
	vals := features.Values{}
	env := p.Env
	if env == "" {
		env = "production"
	}
	var gdoc, odoc settings.Doc
	_ = p.DB.Collection(settings.SettingsCollection).FindOne(ctx, bson.M{"_id": settings.DocIDGlobal}).Decode(&gdoc)
	if gdoc.Switches != nil {
		if sv, ok := gdoc.Switches[switchEmail]; ok {
			if b, ok := sv.Value.(bool); ok {
				vals.Global = &features.Setting{Value: features.BoolValue(b), Locked: sv.Locked}
			}
		}
	}
	if org != "" {
		_ = p.DB.Collection(settings.SettingsCollection).FindOne(ctx, bson.M{"_id": settings.OrgDocID(org)}).Decode(&odoc)
		if odoc.Switches != nil {
			if sv, ok := odoc.Switches[switchEmail]; ok {
				if b, ok := sv.Value.(bool); ok {
					vals.Organization = &features.Setting{Value: features.BoolValue(b), Locked: sv.Locked}
				}
			}
		}
	}
	v, _ := features.Resolve(sw, env, vals)
	if v.Bool != nil {
		return *v.Bool
	}
	return true
}

// LoadUserPrefs reads userSettings + users for email/locale.
// Org defaults (notifications.defaults.*) apply when the user has no saved value; role defaults underneath.
func (p *Prefs) LoadUserPrefs(ctx context.Context, userID, orgID string) UserPrefs {
	out := UserPrefs{
		Digest: "hourly", MinSeverity: SeverityWarning,
		Categories: map[string]bool{}, UserID: userID,
	}
	for c := range knownCategories {
		out.Categories[c] = true
	}
	p.applyOrgDefaults(ctx, orgID, &out)
	var us struct {
		Settings bson.M `bson:"settings"`
	}
	_ = p.DB.Collection("userSettings").FindOne(ctx, bson.M{"_id": userID}).Decode(&us)
	if us.Settings != nil {
		if ui := asMap(us.Settings["ui"]); ui != nil {
			if loc, ok := ui["locale"].(string); ok {
				out.Locale = loc
			}
		}
		if n := asMap(us.Settings["notifications"]); n != nil {
			out.MutedCategories = append(out.MutedCategories, asStringSlice(n["mutedCategories"])...)
			if email := asMap(n["email"]); email != nil {
				if b, ok := email["enabled"].(bool); ok {
					out.EmailEnabled, out.EmailSet = b, true
				}
				if d, ok := email["digest"].(string); ok && d != "" {
					out.Digest = d
				}
				if m, ok := email["minSeverity"].(string); ok && m != "" {
					out.MinSeverity = m
				}
				if cats := asMap(email["categories"]); cats != nil {
					for k, v := range cats {
						if b, ok := v.(bool); ok {
							out.Categories[k] = b
						}
					}
				}
			}
			if qh := asMap(n["quietHours"]); qh != nil {
				out.QuietStart, _ = qh["start"].(string)
				out.QuietEnd, _ = qh["end"].(string)
				out.QuietTZ, _ = qh["tz"].(string)
			}
		}
	}
	var u struct {
		Email  string `bson:"email"`
		Locale string `bson:"locale"`
		Status string `bson:"status"`
	}
	if p.DB.Collection("users").FindOne(ctx, bson.M{"_id": userID}).Decode(&u) == nil {
		out.Active = u.Status != "disabled"
	}
	out.Email = u.Email
	if out.Locale == "" {
		out.Locale = u.Locale
	}
	return out
}

func (p *Prefs) applyOrgDefaults(ctx context.Context, orgID string, out *UserPrefs) {
	if p == nil || p.DB == nil || orgID == "" {
		return
	}
	var doc settings.Doc
	if err := p.DB.Collection(settings.SettingsCollection).FindOne(ctx, bson.M{"_id": settings.OrgDocID(orgID)}).Decode(&doc); err != nil {
		return
	}
	vals := doc.Values
	if vals == nil {
		return
	}
	if m, ok := vals["notifications.defaults.minSeverity"].(string); ok && knownSeverities[m] {
		out.MinSeverity = m
	}
	if d, ok := vals["notifications.defaults.digest"].(string); ok && d != "" {
		out.Digest = d
	}
	if b, ok := vals["notifications.defaults.emailEnabled"].(bool); ok && !out.EmailSet {
		out.EmailEnabled, out.EmailSet = b, true
	}
}

func asMap(v any) map[string]any {
	switch x := v.(type) {
	case map[string]any:
		return x
	case bson.M:
		return map[string]any(x)
	case bson.D:
		out := make(map[string]any, len(x))
		for _, e := range x {
			out[e.Key] = e.Value
		}
		return out
	default:
		return nil
	}
}

func asStringSlice(v any) []string {
	switch x := v.(type) {
	case []string:
		return x
	case []any:
		out := make([]string, 0, len(x))
		for _, e := range x {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case bson.A:
		out := make([]string, 0, len(x))
		for _, e := range x {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

// Muted reports whether category is muted in-app.
func (p UserPrefs) Muted(cat string) bool {
	for _, m := range p.MutedCategories {
		if m == cat {
			return true
		}
	}
	return false
}

// EmailDefaultOn is the default of notifications.email.enabled for a recipient role (W75 §6.2, owner
// decision 2026-10-27): owners and admins (and instance owners for instance problems) are emailed unless they
// opt out; operators and viewers only when they opt in.
func EmailDefaultOn(role tenancy.Role) bool {
	return role == tenancy.RoleOwner || role == tenancy.RoleAdmin
}

// EmailOnFor resolves the user's email toggle for a problem reaching them with role: an explicitly saved
// value always wins over the role default.
func (p UserPrefs) EmailOnFor(role tenancy.Role) bool {
	if p.EmailSet {
		return p.EmailEnabled
	}
	return EmailDefaultOn(role)
}

// WantsEmail reports whether this user, reached with role, should get email for n (prefs only; switch
// checked separately).
func (p UserPrefs) WantsEmail(n *Notification, role tenancy.Role) bool {
	if n == nil || !p.EmailOnFor(role) {
		return false
	}
	if severityRank[n.Severity] < severityRank[p.MinSeverity] {
		return false
	}
	if on, ok := p.Categories[n.Category]; ok && !on {
		return false
	}
	return true
}

// InQuietHours reports whether now is inside quiet hours.
func (p UserPrefs) InQuietHours(now time.Time) bool {
	if p.QuietStart == "" || p.QuietEnd == "" {
		return false
	}
	loc := time.UTC
	if p.QuietTZ != "" {
		if l, err := time.LoadLocation(p.QuietTZ); err == nil {
			loc = l
		}
	}
	local := now.In(loc)
	start, err1 := parseHHMM(p.QuietStart)
	end, err2 := parseHHMM(p.QuietEnd)
	if err1 != nil || err2 != nil {
		return false
	}
	mins := local.Hour()*60 + local.Minute()
	if start <= end {
		return mins >= start && mins < end
	}
	return mins >= start || mins < end
}

func parseHHMM(s string) (int, error) {
	parts := strings.Split(s, ":")
	if len(parts) != 2 {
		return 0, errBadTime
	}
	var h, m int
	for i, p := range parts {
		for _, c := range p {
			if c < '0' || c > '9' {
				return 0, errBadTime
			}
		}
		v := 0
		for _, c := range p {
			v = v*10 + int(c-'0')
		}
		if i == 0 {
			h = v
		} else {
			m = v
		}
	}
	if h > 23 || m > 59 {
		return 0, errBadTime
	}
	return h*60 + m, nil
}

var errBadTime = errString("bad time")

type errString string

func (e errString) Error() string { return string(e) }

// SettingInt reads an int setting from global/org docs with default.
func SettingInt(ctx context.Context, db *store.DB, org, key string, def int) int {
	v := settingRaw(ctx, db, org, key)
	switch x := v.(type) {
	case int32:
		return int(x)
	case int64:
		return int(x)
	case int:
		return x
	case float64:
		return int(x)
	}
	return def
}

// SettingDuration reads a duration setting.
func SettingDuration(ctx context.Context, db *store.DB, org, key string, def time.Duration) time.Duration {
	v := settingRaw(ctx, db, org, key)
	switch x := v.(type) {
	case string:
		if d, err := config.ParseDuration(x); err == nil {
			return d
		}
	case int64:
		return time.Duration(x)
	}
	return def
}

func settingRaw(ctx context.Context, db *store.DB, org, key string) any {
	var odoc, gdoc settings.Doc
	if org != "" {
		_ = db.Collection(settings.SettingsCollection).FindOne(ctx, bson.M{"_id": settings.OrgDocID(org)}).Decode(&odoc)
		if odoc.Values != nil {
			if v, ok := odoc.Values[key]; ok {
				return v
			}
		}
	}
	_ = db.Collection(settings.SettingsCollection).FindOne(ctx, bson.M{"_id": settings.DocIDGlobal}).Decode(&gdoc)
	if gdoc.Values != nil {
		if v, ok := gdoc.Values[key]; ok {
			return v
		}
	}
	return nil
}

// Recipient is a user who may be emailed about a problem, with the role that problem reaches them in.
type Recipient struct {
	UserID string
	Role   tenancy.Role
}

// OrgMembers returns the accepted members of org with their roles (pending invitations have no user).
func OrgMembers(ctx context.Context, db *store.DB, org string) ([]Recipient, error) {
	cur, err := db.Collection("memberships").Find(ctx, bson.M{"organizationId": org, "userId": bson.M{"$nin": bson.A{nil, ""}}})
	if err != nil {
		return nil, err
	}
	defer func() { _ = cur.Close(ctx) }()
	seen := map[string]bool{}
	var out []Recipient
	for cur.Next(ctx) {
		var m struct {
			UserID string       `bson:"userId"`
			Role   tenancy.Role `bson:"role"`
		}
		if cur.Decode(&m) == nil && m.UserID != "" && !seen[m.UserID] {
			seen[m.UserID] = true
			out = append(out, Recipient{UserID: m.UserID, Role: m.Role})
		}
	}
	return out, cur.Err()
}
