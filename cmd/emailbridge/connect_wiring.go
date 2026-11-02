package main

import (
	"context"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/radiobros/emailbridge/internal/accounts"
	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/autoconfig"
	"github.com/radiobros/emailbridge/internal/connect"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/features"
	"github.com/radiobros/emailbridge/internal/oauth"
	"github.com/radiobros/emailbridge/internal/settings"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

// connectHostedConnect injects account-create, OAuth-start and feature-switch adapters onto the
// connect module before it starts (P6.4). Place this hook after accounts + oauth and before
// connect.Module in the modules list.
func connectHostedConnect(c *connect.Module) app.Module {
	return &hook{name: "wire.connect", roles: []app.Role{app.RoleAPI}, start: func(_ context.Context, a *app.App) error {
		acc, err := accountsService(a)
		if err != nil {
			return err
		}
		oauthSvc, _ := app.Get[*oauth.Service](a)
		db, err := app.MustGet[*store.DB](a)
		if err != nil {
			return err
		}
		env := c.Env
		if env == "" {
			env = "production"
		}
		reg := c.Registry
		if reg == nil {
			reg = features.DefaultRegistry()
		}

		c.Enabled = func(ctx context.Context, org string) bool {
			return hostedPageOn(ctx, db, reg, env, org)
		}
		c.AVEnabled = func(ctx context.Context, org string) bool {
			return orgSwitchOn(ctx, db, reg, env, org, "attachments.antivirus")
		}

		c.Create = func(ctx context.Context, org, name, email, password string, imap, smtp *autoconfig.Server) (string, error) {
			sys := systemOrgCtx(org)
			imapIn, smtpIn := mailInputs(email, password, imap, smtp)
			vr, err := acc.Verify(sys, accounts.VerifyRequest{IMAP: &imapIn, SMTP: &smtpIn})
			if err != nil {
				return "", err
			}
			if vr.IMAP != nil && !vr.IMAP.OK {
				return "", errs.New(errs.Code(vr.IMAP.Code), "IMAP verification failed.").WithSafe("detail", vr.IMAP.Detail)
			}
			if vr.SMTP != nil && !vr.SMTP.OK {
				return "", errs.New(errs.Code(vr.SMTP.Code), "SMTP verification failed.").WithSafe("detail", vr.SMTP.Detail)
			}
			in := accounts.CreateInput{Name: name, Email: email, IMAP: imapIn, SMTP: smtpIn, SkipVerify: true}
			imapHost := ""
			if imap != nil {
				imapHost = imap.Host
			}
			if autoconfig.IsPEC(domainOf(email), imapHost) {
				// S17 §10: PEC module on, INBOX only, and never resend a certified message whose outcome is
				// unknown — the hosted page asks nothing, so it applies the suggested policy.
				in.Switches = map[string]any{"pec.enabled": true}
				in.Sync = &accounts.SyncConfig{Paths: []string{"INBOX"}, IdlePaths: []string{"INBOX"}}
				in.SMTP.Policy = "atMostOnce"
			}
			created, err := acc.Create(sys, in)
			if err != nil {
				return "", err
			}
			pin := connect.Pin(connect.Observed{
				Email: email, IMAPUser: expandMailUser(imapIn.Auth.User, email),
			}, time.Now().UTC())
			_, _ = db.Collection(accounts.Collection).UpdateOne(sys,
				bson.M{"_id": created.ID, "organizationId": org},
				bson.M{"$set": bson.M{
					"identity": accounts.IdentityPin{
						Email: pin.Email, IMAPUser: pin.IMAPUser, OAuthSubject: pin.OAuthSubject, PinnedAt: pin.PinnedAt,
					},
				}},
			)
			return created.ID, nil
		}

		ho := &hostedOAuth{db: db, accounts: acc, discover: func(ctx context.Context, email string) (*autoconfig.Server, *autoconfig.Server) {
			if disco, ok := app.Get[*autoconfig.Service](a); ok && disco != nil {
				if r, err := disco.Discover(ctx, email); err == nil && r != nil {
					return r.IMAP, r.SMTP
				}
			}
			return nil, nil
		}}
		if oauthSvc != nil {
			ho.oauth = oauthSvc
		}
		c.OAuthStart = ho.start
		return nil
	}}
}

func systemOrgCtx(org string) context.Context {
	return tenancy.WithCaller(context.Background(), tenancy.Caller{
		Kind: tenancy.CallerSystem, OrganizationID: org,
	})
}

func hostedPageOn(ctx context.Context, db *store.DB, reg *features.Registry, env, org string) bool {
	return orgSwitchOn(ctx, db, reg, env, org, "connect.hostedPage")
}

// orgSwitchOn resolves a boolean feature switch for one organisation (settings doc + registry).
func orgSwitchOn(ctx context.Context, db *store.DB, reg *features.Registry, env, org, name string) bool {
	sw, ok := reg.Get(name)
	if !ok {
		return false
	}
	vals := features.Values{}
	var doc settings.Doc
	_ = db.Collection(settings.SettingsCollection).FindOne(ctx, bson.M{"_id": settings.OrgDocID(org)}).Decode(&doc)
	if doc.Switches != nil {
		if sv, ok := doc.Switches[name]; ok {
			if b, ok := sv.Value.(bool); ok {
				vals.Organization = &features.Setting{Value: features.BoolValue(b), Locked: sv.Locked}
			}
		}
	}
	v, _ := features.Resolve(sw, env, vals)
	return v.Bool != nil && *v.Bool
}

func mailInputs(email, password string, imap, smtp *autoconfig.Server) (accounts.IMAPConfigInput, accounts.SMTPConfigInput) {
	imapIn := accounts.IMAPConfigInput{
		Host: imap.Host, Port: imap.Port, TLS: imap.TLS,
		Auth: accounts.MailAuthInput{User: expandMailUser(imap.User, email), Pass: password},
	}
	smtpIn := accounts.SMTPConfigInput{
		Host: smtp.Host, Port: smtp.Port, TLS: smtp.TLS,
		Auth: accounts.MailAuthInput{User: expandMailUser(smtp.User, email), Pass: password},
	}
	return imapIn, smtpIn
}

func expandMailUser(tmpl, email string) string {
	switch strings.TrimSpace(tmpl) {
	case "", "%EMAIL%", "%EMAILADDRESS%":
		return email
	case "%EMAILLOCALPART%":
		at := strings.IndexByte(email, '@')
		if at > 0 {
			return email[:at]
		}
		return email
	default:
		return tmpl
	}
}

func domainOf(email string) string {
	if i := strings.LastIndex(email, "@"); i >= 0 {
		return strings.ToLower(email[i+1:])
	}
	return ""
}
