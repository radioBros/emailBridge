package ai

import (
	"context"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/radiobros/emailbridge/internal/features"
	"github.com/radiobros/emailbridge/internal/settings"
)

// AccountSwitches loads per-account feature switch overlays (W52 G7).
type AccountSwitches interface {
	SwitchBool(ctx context.Context, orgID, accountID, name string) (value bool, set bool, err error)
}

func (s *Service) aiEnabled(ctx context.Context, org, accountID string) bool {
	if s.Enabled != nil {
		return s.Enabled(ctx, org, accountID)
	}
	sw, ok := s.registry().Get(switchAI)
	if !ok {
		return false
	}
	vals := features.Values{}
	vals.Global = s.readSwitchDoc(ctx, settings.DocIDGlobal, switchAI)
	if org != "" {
		vals.Organization = s.readSwitchDoc(ctx, settings.OrgDocID(org), switchAI)
	}
	if accountID != "" && s.Accounts != nil {
		if b, set, err := s.Accounts.SwitchBool(ctx, org, accountID, switchAI); err == nil && set {
			vals.Lower = &features.Setting{Value: features.BoolValue(b)}
		}
	}
	env := s.Env
	if env == "" {
		env = "production"
	}
	v, _ := features.Resolve(sw, env, vals)
	if v.Bool != nil {
		return *v.Bool
	}
	return false
}

func (s *Service) readSwitchDoc(ctx context.Context, docID, name string) *features.Setting {
	var coll *mongo.Collection
	switch {
	case s.DB != nil:
		coll = s.DB.Collection(settings.SettingsCollection)
	default:
		if r, ok := s.Repo.(*Repo); ok && r != nil && r.DB != nil {
			coll = r.DB.Collection(settings.SettingsCollection)
		}
	}
	if coll == nil {
		return nil
	}
	var doc settings.Doc
	err := coll.FindOne(ctx, bson.M{"_id": docID}).Decode(&doc)
	if err != nil || doc.Switches == nil {
		return nil
	}
	sv, ok := doc.Switches[name]
	if !ok {
		return nil
	}
	b, ok := sv.Value.(bool)
	if !ok {
		return nil
	}
	return &features.Setting{Value: features.BoolValue(b), Locked: sv.Locked}
}
