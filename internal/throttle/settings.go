package throttle

import (
	"context"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/radiobros/emailbridge/internal/features"
	"github.com/radiobros/emailbridge/internal/settings"
	"github.com/radiobros/emailbridge/internal/store"
)

const (
	keyLimits  = "sending.throttle.limits"
	keyWindow  = "sending.sendWindows.config"
	keyPreSend = "sending.preSendHook.cel"
)

// accountsCollection is accounts.Collection (not imported: accounts depends on the send path).
const accountsCollection = "accounts"

// SettingsConfig loads gate Config from the settings collection: the switches resolve global → organisation →
// account (locks honoured), the limits, window and pre-send hook come from the organisation document.
func SettingsConfig(db *store.DB, reg *features.Registry, env string) ConfigFunc {
	if reg == nil {
		reg = features.DefaultRegistry()
	}
	return func(ctx context.Context, orgID, accountID string) (Config, error) {
		if db == nil || orgID == "" {
			cfg := Config{Limits: DefaultLimits()}
			cfg.ThrottleOn = SwitchOn(reg, switchThrottle, env, nil)
			return cfg, nil
		}
		var global, doc settings.Doc
		_ = db.Collection(settings.SettingsCollection).FindOne(ctx, bson.M{"_id": settings.DocIDGlobal}).Decode(&global)
		_ = db.Collection(settings.SettingsCollection).FindOne(ctx, bson.M{"_id": settings.OrgDocID(orgID)}).Decode(&doc)
		var acc struct {
			Switches map[string]any `bson:"switches"`
		}
		if accountID != "" {
			_ = db.Collection(accountsCollection).FindOne(ctx,
				bson.M{"_id": accountID, "organizationId": orgID},
				options.FindOne().SetProjection(bson.M{"switches": 1}),
			).Decode(&acc)
		}
		return configFromDocs(global, doc, acc.Switches, reg, env), nil
	}
}

func configFromDoc(doc settings.Doc, reg *features.Registry, env string) Config {
	return configFromDocs(settings.Doc{}, doc, nil, reg, env)
}

func configFromDocs(global, doc settings.Doc, account map[string]any, reg *features.Registry, env string) Config {
	cfg := Config{Limits: DefaultLimits()}
	cfg.ThrottleOn = resolveSwitch(reg, switchThrottle, env, global, doc, account)
	cfg.WindowsOn = resolveSwitch(reg, switchWindows, env, global, doc, account)
	cfg.PreSendOn = resolveSwitch(reg, switchPreSend, env, global, doc, account)
	if doc.Values != nil {
		if v, ok := doc.Values[keyLimits]; ok {
			cfg.Limits = ParseLimits(v)
		}
		if v, ok := doc.Values[keyWindow]; ok {
			w, err := ParseWindow(v)
			if err == nil {
				cfg.Window = w
			}
		}
		if v, ok := doc.Values[keyPreSend]; ok {
			if s, ok := v.(string); ok {
				cfg.CEL = s
			}
		}
	}
	return cfg
}

// resolveSwitch resolves name over the global and organisation settings documents and the account's own
// switches, each level only where the switch's scopes allow it; unknown switches stay on (fail safe).
func resolveSwitch(reg *features.Registry, name, env string, global, org settings.Doc, account map[string]any) bool {
	sw, ok := reg.Get(name)
	if !ok {
		return true
	}
	fromDoc := func(d settings.Doc) *features.Setting {
		if sv, ok := d.Switches[name]; ok {
			if b, ok := sv.Value.(bool); ok {
				return &features.Setting{Value: features.BoolValue(b), Locked: sv.Locked}
			}
		}
		return nil
	}
	vals := features.Values{Organization: fromDoc(org)}
	if features.Allows(sw, features.ScopeGlobal) {
		vals.Global = fromDoc(global)
	}
	if features.Allows(sw, features.ScopeAccount) {
		if b, ok := account[name].(bool); ok {
			vals.Lower = &features.Setting{Value: features.BoolValue(b)}
		}
	}
	if env == "" {
		env = "production"
	}
	v, _ := features.Resolve(sw, env, vals)
	if v.Bool != nil {
		return *v.Bool
	}
	return true
}
