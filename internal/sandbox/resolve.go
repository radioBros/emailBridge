package sandbox

import (
	"context"
	"errors"
	"net/mail"
	"strings"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/features"
	"github.com/radiobros/emailbridge/internal/settings"
)

const (
	switchEnabled  = "sandbox.enabled"
	switchPolicy   = "sandbox.recipientPolicy"
	settingRewrite = "sandbox.rewriteTo"
)

// decision is the resolved sandbox action for one outbound message.
type decision struct {
	Enabled   bool
	Policy    string
	RewriteTo string
}

// resolve reads sandbox.enabled / recipientPolicy / rewriteTo through features + settings docs +
// account switches (S9 §4, S6 §7). Env drives the catalogue DefaultFor (on + capture when not production).
func (g *Gate) resolve(ctx context.Context, orgID, accountID string) (decision, error) {
	reg := g.registry()
	env := g.env()

	valsEnabled := features.Values{}
	valsPolicy := features.Values{}
	g.fillSwitch(ctx, settings.DocIDGlobal, switchEnabled, true, &valsEnabled.Global)
	g.fillSwitch(ctx, settings.OrgDocID(orgID), switchEnabled, true, &valsEnabled.Organization)
	g.fillSwitch(ctx, settings.DocIDGlobal, switchPolicy, false, &valsPolicy.Global)
	g.fillSwitch(ctx, settings.OrgDocID(orgID), switchPolicy, false, &valsPolicy.Organization)

	accSwitches, err := g.accountSwitches(ctx, accountID)
	if err != nil {
		return decision{}, err
	}
	if v, ok := accSwitches[switchEnabled].(bool); ok {
		valsEnabled.Lower = &features.Setting{Value: features.BoolValue(v)}
	}

	swEn, ok := reg.Get(switchEnabled)
	if !ok {
		return decision{Enabled: false, Policy: PolicyAllow}, nil
	}
	swPol, _ := reg.Get(switchPolicy)
	enVal, _ := features.Resolve(swEn, env, valsEnabled)
	polVal, _ := features.Resolve(swPol, env, valsPolicy)

	d := decision{Policy: PolicyAllow}
	if enVal.Bool != nil {
		d.Enabled = *enVal.Bool
	}
	if polVal.String != nil && *polVal.String != "" {
		d.Policy = *polVal.String
	}
	d.RewriteTo, err = g.rewriteTo(ctx, orgID)
	if err != nil {
		return decision{}, err
	}
	return d, nil
}

func (g *Gate) fillSwitch(ctx context.Context, docID, name string, asBool bool, dest **features.Setting) {
	var doc settings.Doc
	err := g.DB.Collection(settings.SettingsCollection).FindOne(ctx, bson.M{"_id": docID}).Decode(&doc)
	if err != nil || doc.Switches == nil {
		return
	}
	sv, ok := doc.Switches[name]
	if !ok {
		return
	}
	if asBool {
		b, ok := asBoolValue(sv.Value)
		if !ok {
			return
		}
		*dest = &features.Setting{Value: features.BoolValue(b), Locked: sv.Locked}
		return
	}
	s, ok := sv.Value.(string)
	if !ok || s == "" {
		return
	}
	*dest = &features.Setting{Value: features.StringValue(s), Locked: sv.Locked}
}

func asBoolValue(v any) (bool, bool) {
	switch t := v.(type) {
	case bool:
		return t, true
	default:
		return false, false
	}
}

func (g *Gate) accountSwitches(ctx context.Context, accountID string) (map[string]any, error) {
	var row struct {
		Switches map[string]any `bson:"switches"`
	}
	err := g.DB.Collection("accounts").FindOne(ctx, bson.M{"_id": accountID}).Decode(&row)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, nil
		}
		return nil, errs.Wrap(err, errs.CodeInternalError, "sandbox.account")
	}
	return row.Switches, nil
}

func (g *Gate) rewriteTo(ctx context.Context, orgID string) (string, error) {
	if g.Settings != nil {
		body, err := g.Settings.Effective(ctx, orgID)
		if err != nil {
			return "", err
		}
		if v, ok := body.Values[settingRewrite]; ok {
			if s, ok := v.Value.(string); ok {
				return strings.TrimSpace(s), nil
			}
		}
	}
	for _, id := range []string{settings.OrgDocID(orgID), settings.DocIDGlobal} {
		var doc settings.Doc
		err := g.DB.Collection(settings.SettingsCollection).FindOne(ctx, bson.M{"_id": id}).Decode(&doc)
		if err != nil || doc.Values == nil {
			continue
		}
		if v, ok := doc.Values[settingRewrite]; ok {
			if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
				return strings.TrimSpace(s), nil
			}
		}
	}
	return "", nil
}

func validateRewriteTo(addr string) (string, error) {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return "", errs.New(errs.CodeValidationFailed, "sandbox.rewriteTo must be set for the rewrite policy.")
	}
	parsed, err := mail.ParseAddress(addr)
	if err != nil || parsed.Address == "" || !strings.Contains(parsed.Address, "@") {
		return "", errs.New(errs.CodeInvalidAddress, "sandbox.rewriteTo is not a valid address.").WithSafe("address", addr)
	}
	return strings.ToLower(parsed.Address), nil
}
