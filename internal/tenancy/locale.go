package tenancy

import (
	"context"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/radiobros/emailbridge/internal/i18n"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/sysmail"
)

// userSettingsColl mirrors settings.UserSettingsCollection (avoid importing settings → cycle).
const userSettingsColl = "userSettings"

func localeForEmail(ctx context.Context, db *store.DB, repo *Repo, email string) string {
	if repo == nil {
		return "en"
	}
	u, err := repo.UserByEmail(ctx, email)
	if err != nil || u == nil {
		return "en"
	}
	if loc := uiLocaleOfUser(ctx, db, u.ID); loc != "" {
		return loc
	}
	return sysmail.NormalizeLocale(u.Locale)
}

func uiLocaleOfUser(ctx context.Context, db *store.DB, userID string) string {
	if db == nil || userID == "" {
		return ""
	}
	var doc struct {
		Settings bson.M `bson:"settings"`
	}
	err := db.Collection(userSettingsColl).FindOne(ctx, bson.M{"_id": userID}).Decode(&doc)
	if err != nil || doc.Settings == nil {
		return ""
	}
	ui, _ := doc.Settings["ui"].(bson.M)
	if ui == nil {
		if m, ok := doc.Settings["ui"].(map[string]any); ok {
			raw, _ := m["locale"].(string)
			if i18n.IsSupported(raw) {
				return i18n.Normalize(raw)
			}
			return ""
		}
		return ""
	}
	raw, _ := ui["locale"].(string)
	if !i18n.IsSupported(raw) {
		return ""
	}
	return i18n.Normalize(raw)
}
