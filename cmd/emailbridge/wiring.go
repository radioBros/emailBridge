package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	gosync "sync"
	"time"

	"github.com/radiobros/emailbridge/internal/evidence"

	eventtypes "github.com/radiobros/emailbridge/internal/events/types"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/integrations"
	"github.com/radiobros/emailbridge/internal/integrations/connectors"
	"github.com/radiobros/emailbridge/internal/pecreceipts"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/radiobros/emailbridge/internal/accounts"
	"github.com/radiobros/emailbridge/internal/apikeys"
	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/archive"
	"github.com/radiobros/emailbridge/internal/attachments"
	"github.com/radiobros/emailbridge/internal/audit"
	"github.com/radiobros/emailbridge/internal/auth"
	"github.com/radiobros/emailbridge/internal/authserver"
	"github.com/radiobros/emailbridge/internal/bus"
	"github.com/radiobros/emailbridge/internal/config"
	"github.com/radiobros/emailbridge/internal/connect"
	"github.com/radiobros/emailbridge/internal/crypto"
	"github.com/radiobros/emailbridge/internal/egress"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/events"
	"github.com/radiobros/emailbridge/internal/gateways"
	"github.com/radiobros/emailbridge/internal/health"
	"github.com/radiobros/emailbridge/internal/imapproxy"
	"github.com/radiobros/emailbridge/internal/ippool"
	"github.com/radiobros/emailbridge/internal/jobs"
	"github.com/radiobros/emailbridge/internal/logging/query"
	"github.com/radiobros/emailbridge/internal/mailbox"
	"github.com/radiobros/emailbridge/internal/mailproto/imapc"
	"github.com/radiobros/emailbridge/internal/messages"
	"github.com/radiobros/emailbridge/internal/msa"
	"github.com/radiobros/emailbridge/internal/oauth"
	"github.com/radiobros/emailbridge/internal/objstore"
	"github.com/radiobros/emailbridge/internal/observability"
	"github.com/radiobros/emailbridge/internal/protolog"
	"github.com/radiobros/emailbridge/internal/providers/gmail"
	"github.com/radiobros/emailbridge/internal/providers/graph"
	"github.com/radiobros/emailbridge/internal/rules"
	"github.com/radiobros/emailbridge/internal/sandbox"
	"github.com/radiobros/emailbridge/internal/settings"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/submit"
	"github.com/radiobros/emailbridge/internal/suppression"
	syncengine "github.com/radiobros/emailbridge/internal/sync"
	"github.com/radiobros/emailbridge/internal/sysmail"
	"github.com/radiobros/emailbridge/internal/templates"
	"github.com/radiobros/emailbridge/internal/tenancy"
	"github.com/radiobros/emailbridge/internal/throttle"
	"github.com/radiobros/emailbridge/internal/tracking"
	"github.com/radiobros/emailbridge/internal/webhook"
)

// hook is a module that only connects services of modules started before it to modules started after it
// (their options are read at their own Start). Keeping these links in cmd/ means domain packages never
// import each other just to be wired.
type hook struct {
	name  string
	roles []app.Role
	start func(ctx context.Context, a *app.App) error
	stop  func(ctx context.Context) error // optional
}

func (h *hook) Name() string                                { return h.name }
func (h *hook) Roles() []app.Role                           { return h.roles }
func (h *hook) Start(ctx context.Context, a *app.App) error { return h.start(ctx, a) }
func (h *hook) Stop(ctx context.Context) error {
	if h.stop == nil {
		return nil
	}
	return h.stop(ctx)
}

// provideEgress builds the SSRF-safe dialer every outbound connection uses (IMAP/SMTP with egress.Mail,
// webhooks and other HTTP with egress.HTTP; S10 §8) and provides it to later modules.
func provideEgress(cfg *config.Config, security *slog.Logger) app.Module {
	return &hook{name: "egress", start: func(_ context.Context, a *app.App) error {
		prefixes, hosts, err := egress.ParseAllow(cfg.Webhook.EgressAllow)
		if err != nil {
			return fmt.Errorf("EB_EGRESS_ALLOW: %w", err)
		}
		d := egress.NewDialer(egress.Policy{
			BlockLinkLocal: cfg.Webhook.EgressBlockLinkLocal,
			BlockPrivate:   cfg.Webhook.EgressBlockPrivate,
			Allow:          prefixes,
			AllowHosts:     hosts,
		})
		d.Blocked = func(host string, addr netip.Addr, reason string) {
			security.Warn("outbound connection refused by egress policy", "host", host, "addr", addr.String(), "reason", reason)
		}
		app.Provide(a, d)
		return nil
	}}
}

// accountsService returns the accounts service of this node when the api role runs it, else a minimal
// one with what the sync and submit adapters need (sync-only or submit-only nodes).
func accountsService(a *app.App) (*accounts.Service, error) {
	if svc, ok := app.Get[*accounts.Service](a); ok {
		return svc, nil
	}
	db, err := app.MustGet[*store.DB](a)
	if err != nil {
		return nil, err
	}
	cipher, err := app.MustGet[*crypto.Cipher](a)
	if err != nil {
		return nil, err
	}
	ev, err := app.MustGet[*events.Writer](a)
	if err != nil {
		return nil, err
	}
	b, _ := app.Get[*bus.Bus](a)
	return &accounts.Service{DB: db, Cipher: cipher, Events: ev, Bus: b, Log: a.Log}, nil
}

// syncNeeds adapts the webhook payload-needs resolver to the sync engine.
type syncNeeds struct{ r *webhook.NeedsResolver }

func (s syncNeeds) Needs(ctx context.Context, org string) (syncengine.Needs, error) {
	n, err := s.r.Needs(ctx, org)
	if err != nil {
		return syncengine.Needs{}, err
	}
	return syncengine.Needs{Headers: n.Headers, Text: n.Text, TextMax: int64(n.TextMax), Attachments: n.Attachments,
		WebSafeHTML: n.WebSafeHTML, CalendarEvents: n.CalendarEvents, ReplyText: n.ReplyText}, nil
}

// connectSync gives the sync engine its account source and the raw archive (inbound.archiveRaw).
func connectSync(sm *syncengine.Module, gm *graph.Module, im *integrations.Module, em *evidence.Module, pecAnchors string) app.Module {
	return &hook{name: "wire.sync", roles: []app.Role{app.RoleSync}, start: func(_ context.Context, a *app.App) error {
		svc, err := accountsService(a)
		if err != nil {
			return err
		}
		sm.Accounts = accounts.NewSyncSource(svc)
		if db, ok := app.Get[*store.DB](a); ok {
			repo := &submit.Repo{DB: db}
			sm.Outbound = repo.OutboundFor // bounce / complaint correlation (P5.4)
			// Hard bounces and complaints go straight onto the suppression lists (P5.2); the service only needs
			// the database, so it works on sync nodes where the suppression API module does not run.
			sm.Suppress = &suppression.Service{Repo: &suppression.Repo{DB: db}}
			// Provider backends (P6): accounts whose provider is not IMAP sync through these.
			sm.Backends = map[string]syncengine.Backend{}
			if gb, ok := app.Get[*gmail.Backend](a); ok {
				sm.Backends[syncengine.ProviderGmail] = gb
			}
			if gm != nil && gm.Client() != nil {
				sm.Backends[syncengine.ProviderGraph] = gm.NewBackend()
			}
		}
		sm.Needs = syncNeeds{&webhook.NeedsResolver{DB: svc.DB}}
		// Attachment rows / S3 / ClamAV (P4.3, W48): the attachments module starts after sync, so resolve it per call.
		sm.Attachments = syncengine.LazyAttachments{Get: func() syncengine.AttachmentsAPI {
			svc, ok := app.Get[*attachments.Service](a)
			if !ok || svc == nil {
				return nil
			}
			return attachments.SyncAPI{Svc: svc}
		}}
		if im != nil {
			sm.Scorer = rspamdScorer{im: im} // P8.3, accounts with integrations.rspamd on
		}
		if db, ok := app.Get[*store.DB](a); ok {
			anchors, err := loadPEMPool(pecAnchors)
			if err != nil {
				return fmt.Errorf("EB_PEC_TRUST_ANCHORS: %w", err)
			}
			// P9: PEC envelopes and receipts of accounts with pec.enabled on (S17 §4).
			sm.PEC = &syncengine.PEC{Receipts: &pecreceipts.Repo{DB: db}, Anchors: anchors,
				Evidence: evidenceStore{em}, EvidencePolicy: evidencePolicy(db)}
			if js, ok := app.Get[*jobs.Service](a); ok {
				sm.PEC.TSARetry = func(ctx context.Context, ref evidence.Ref, item evidence.Item) error {
					_, err := js.Create(store.WithOrganization(ctx, item.Organization), evidence.JobKindTSARetry,
						evidence.NewTSARetryPayload(ref, item, time.Now()))
					return err
				}
			}
		}
		if st, ok := app.Get[*archive.Store](a); ok {
			sm.Archive = st
		}
		return nil
	}}
}

// connectArchive gives the raw-source API its account loader (on-demand IMAP connections).
func connectArchive(am *archive.Module) app.Module {
	return &hook{name: "wire.archive", start: func(_ context.Context, a *app.App) error {
		svc, err := accountsService(a)
		if err != nil {
			return err
		}
		am.Accounts = accounts.NewSyncSource(svc)
		return nil
	}}
}

// smtpSource adapts the accounts service to submit.AccountSMTP.
type smtpSource struct{ svc *accounts.Service }

func (s smtpSource) SMTP(ctx context.Context, accountID string) (*submit.SMTPAccount, error) {
	st, err := s.svc.SMTPSettings(ctx, accountID)
	if err != nil {
		return nil, err
	}
	var token func(context.Context) (string, string, error)
	if st.OAuth && s.svc.OAuthToken != nil {
		id, fn := st.ID, s.svc.OAuthToken
		token = func(ctx context.Context) (string, string, error) { return fn(ctx, id) }
	}
	return &submit.SMTPAccount{Token: token, ID: st.ID, OrganizationID: st.OrganizationID, ExternalID: st.ExternalID, State: st.State,
		Email: st.Email, Name: st.Name, Host: st.Host, Port: st.Port, TLS: st.TLS, User: st.User, Password: st.Password,
		IgnoreCertErrors: st.IgnoreCertErrors, Policy: st.Policy, MaxAttempts: st.MaxAttempts, BackoffBase: st.BackoffBase,
		CopyToSent: st.CopyToSent, ProtocolLogs: st.ProtocolLogs, ProtocolLogsRawAll: st.RawLog,
		GatewayID: st.GatewayID, EHLO: st.EHLO, Proxy: st.Proxy, LocalAddress: st.LocalAddress,
		LocalAddresses: st.LocalAddresses, SMTPStrategy: st.SMTPStrategy, // IP pool (W57)
		Provider: st.Provider, // gmail / graph send through their APIs (G8); without it the worker dialled SMTP
	}, nil
}

// sentCopy adapts the sync engine's copier to submit.SentCopier (S6 §5).
type sentCopy struct{ c *syncengine.SentCopier }

func (s sentCopy) CopyToSent(ctx context.Context, accountID, _ string, raw io.Reader, date time.Time) error {
	b, err := io.ReadAll(io.LimitReader(raw, 64<<20))
	if err != nil {
		return err
	}
	_, _, err = s.c.CopyToSent(ctx, accountID, "", b, date)
	return err
}

// connectSubmit gives the send pipeline its account source, the copy-to-Sent service and the sandbox gate.
func connectSubmit(sub *submit.Module, sbx *sandbox.Module, imapID map[string]string) app.Module {
	return &hook{name: "wire.submit", roles: []app.Role{app.RoleAPI, app.RoleSubmit}, start: func(_ context.Context, a *app.App) error {
		svc, err := accountsService(a)
		if err != nil {
			return err
		}
		sub.Accounts = smtpSource{svc}
		copier := &syncengine.SentCopier{Accounts: accounts.NewSyncSource(svc), IMAPID: imapID}
		if d, ok := app.Get[*egress.Dialer](a); ok {
			copier.Egress = d // per-account proxy / local bind (W57)
			copier.Dial = func(ctx context.Context, network, addr string) (net.Conn, error) {
				return d.DialContext(ctx, network, addr, egress.Mail)
			}
		}
		copier.IPPool, _ = app.Get[*ippool.Selector](a)
		sub.Sent = sentCopy{copier}
		if g := sbx.Gate(); g != nil {
			sub.Sandbox = g
		}
		return nil
	}}
}

// sysmailAccount sends system mail through a connected account (EB_SYSMAIL_MODE=account, P1.17) as the
// system caller of the account's organisation, via the normal send pipeline.
type sysmailAccount struct {
	submit   *submit.Service
	accounts *accounts.Service
}

func (s sysmailAccount) system(ctx context.Context, accountID string) (context.Context, *accounts.SMTPSettings, error) {
	st, err := s.accounts.SMTPSettings(ctx, accountID)
	if err != nil {
		return nil, nil, err
	}
	ctx = tenancy.WithCaller(store.WithOrganization(ctx, st.OrganizationID), tenancy.Caller{Kind: tenancy.CallerSystem, OrganizationID: st.OrganizationID})
	return ctx, st, nil
}

func (s sysmailAccount) SendSystem(ctx context.Context, accountID, _, _ string, raw []byte) (string, error) {
	sctx, _, err := s.system(ctx, accountID)
	if err != nil {
		return "", err
	}
	res, err := s.submit.Submit(sctx, accountID, submit.SubmitRequest{Raw: base64.StdEncoding.EncodeToString(raw)}, "", false)
	if err != nil {
		return "", err
	}
	if res.Accepted == nil {
		return "", errs.New(errs.CodeInternalError, "system mail was not accepted by the send pipeline")
	}
	return res.Accepted.ID, nil
}

func (s sysmailAccount) Check(ctx context.Context, accountID string) error {
	_, st, err := s.system(ctx, accountID)
	if err != nil {
		return err
	}
	switch st.State {
	case "disabled":
		return errs.New(errs.CodeAccountDisabled, "The system-mail account is disabled.")
	case "deleting":
		return errs.New(errs.CodeAccountDeleting, "The system-mail account is being deleted.")
	}
	return nil
}

// connectSysmailAccount binds the account sender once submit runs on this node (api/submit roles).
func connectSysmailAccount(subm *submit.Module) app.Module {
	return &hook{name: "wire.sysmail-account", start: func(ctx context.Context, a *app.App) error {
		sm, ok := app.Get[*sysmail.Module](a)
		if !ok || subm.Service() == nil {
			return nil
		}
		svc, err := accountsService(a)
		if err != nil {
			return err
		}
		sender := sysmailAccount{submit: subm.Service(), accounts: svc}
		sm.BindAccountSender(ctx, sender, sender)
		return nil
	}}
}

// connectProtolog runs after accounts: the protocol-logs API checks accounts and audits downloads
// through the accounts service (protolog cannot import accounts: accounts → sync → protolog).
// connectStreamMetrics feeds the JetStream work-stream depths into the observability collector
// (eb_stream_pending / eb_stream_ack_pending, S12 §9).
func connectStreamMetrics() app.Module {
	return &hook{name: "wire.stream-metrics", start: func(_ context.Context, a *app.App) error {
		m, ok := app.Get[*observability.Metrics](a)
		b, okb := app.Get[*bus.Bus](a)
		if !ok || !okb {
			return nil
		}
		m.ObserveStreams(func(ctx context.Context) ([]observability.StreamStat, error) {
			var out []observability.StreamStat
			for _, name := range []string{bus.StreamEvents, bus.StreamDelivery, bus.StreamSubmit, bus.StreamSysmail} {
				q, err := b.QueueStats(ctx, name)
				if err != nil {
					return out, err
				}
				// Redeliveries are not exposed by bus.QueueStats yet; the series reports 0 until they are.
				out = append(out, observability.StreamStat{Stream: name, Pending: float64(q.Pending), AckPending: float64(q.AckPending)})
			}
			return out, nil
		})
		return nil
	}}
}

func connectProtolog(pm *protolog.Module) app.Module {
	return &hook{name: "wire.protolog", roles: []app.Role{app.RoleAPI}, start: func(_ context.Context, a *app.App) error {
		svc, err := app.MustGet[*accounts.Service](a)
		if err != nil {
			return err
		}
		pm.Accounts, pm.Audit = svc, svc
		return nil
	}}
}

// connectErasure registers the organisation erasure job (S11 §5.2) with its collaborators: account
// erasure runs inline through jobs.RunNow (no waiting on runner slots), API keys are revoked through
// internal/apikeys as the system caller.
func connectErasure() app.Module {
	return &hook{name: "wire.org-erasure", roles: []app.Role{app.RoleAPI}, start: func(_ context.Context, a *app.App) error {
		js, err := app.MustGet[*jobs.Service](a)
		if err != nil {
			return err
		}
		db, err := app.MustGet[*store.DB](a)
		if err != nil {
			return err
		}
		keys, err := app.MustGet[*apikeys.Service](a)
		if err != nil {
			return err
		}
		aud, _ := app.Get[tenancy.Auditor](a)
		system := func(ctx context.Context, org string) context.Context {
			return tenancy.WithCaller(store.WithOrganization(ctx, org), tenancy.Caller{Kind: tenancy.CallerSystem, OrganizationID: org})
		}
		ids := func(ctx context.Context, coll, org string) ([]string, error) {
			cur, err := db.Collection(coll).Find(ctx, bson.M{"organizationId": org}, options.Find().SetProjection(bson.M{"_id": 1}))
			if err != nil {
				return nil, err
			}
			var rows []struct {
				ID string `bson:"_id"`
			}
			if err := cur.All(ctx, &rows); err != nil {
				return nil, err
			}
			out := make([]string, len(rows))
			for i, r := range rows {
				out[i] = r.ID
			}
			return out, nil
		}
		connectSvc, _ := app.Get[*connect.Service](a)
		var objectCleanups []func(context.Context, string) error
		if connectSvc != nil {
			objectCleanups = append(objectCleanups, connectSvc.EraseOrgObjects)
		}
		tenancy.RegisterErasure(func(kind string, h tenancy.ErasureHandler) {
			js.Handle(kind, func(ctx context.Context, j *jobs.Job, r jobs.Reporter) (any, error) {
				return h(ctx, j.ID, j.Payload, func(done, total int64) { r.Progress(ctx, done, total) }, r.Cancelled)
			})
		}, tenancy.ErasureDeps{
			DB:             db,
			Audit:          aud,
			ListAccountIDs: func(ctx context.Context, org string) ([]string, error) { return ids(ctx, accounts.Collection, org) },
			EraseAccount: func(ctx context.Context, accountID, org string, cancelled func() bool) error {
				_, err := js.RunNow(system(ctx, org), "erasure", org, map[string]string{"accountId": accountID, "organizationId": org}, cancelled)
				return err
			},
			RevokeAPIKeys: func(ctx context.Context, org string) (int64, error) {
				list, err := ids(ctx, apikeys.Collection, org)
				if err != nil {
					return 0, err
				}
				var n int64
				for _, id := range list {
					if err := keys.Revoke(system(ctx, org), id, ""); err != nil && errs.CodeOf(err) != errs.CodeNotFound {
						return n, err
					}
					n++
				}
				return n, nil
			},
			OrgObjectCleanups: objectCleanups,
		})
		return nil
	}}
}

// perTickDur converts a rate into claims per placement tick (EB_SYNC_TICK).
func perTickDur(r config.Rate, tick time.Duration) int {
	if r.Count <= 0 || r.Period <= 0 {
		return 0
	}
	if tick <= 0 {
		tick = 5 * time.Second
	}
	return max(1, int(int64(r.Count)*int64(tick)/int64(r.Period)))
}

// logAuthorizer scopes the log API through RBAC: logs.read in the active organisation; instance owners
// see every organisation and instance-level records (S12 §8).
func logAuthorizer() query.Authorizer {
	return query.AuthorizerFunc(func(ctx context.Context) (string, bool, error) {
		c, ok := tenancy.CallerFrom(ctx)
		if !ok {
			return "", false, errs.New(errs.CodeUnauthenticated, "Authentication is required.")
		}
		if c.InstanceOwner && c.Kind == tenancy.CallerUser {
			return c.OrganizationID, true, nil
		}
		if err := tenancy.Require(ctx, tenancy.PermLogsRead); err != nil {
			return "", false, err
		}
		return c.OrganizationID, false, nil
	})
}

// authMailer adapts auth's mail requests to system-mail jobs (template data keys, reset link).
type authMailer struct {
	svc       *sysmail.Service
	publicURL string
}

func (m authMailer) Enqueue(ctx context.Context, am auth.Mail) error {
	data := map[string]string{}
	switch am.Kind {
	case auth.MailMFACode:
		data["Code"] = am.Data["code"]
	case auth.MailPasswordReset:
		data["ResetURL"] = strings.TrimRight(m.publicURL, "/") + "/reset-password?token=" + url.QueryEscape(am.Data["token"])
	default:
		for k, v := range am.Data {
			data[k] = v
		}
	}
	return m.svc.Enqueue(ctx, sysmail.Mail{Kind: am.Kind, To: am.To, Locale: am.Locale, Data: data})
}

// connectAuthMailer runs after sysmail and before auth: OTP and reset mail go through the queue.
func connectAuthMailer(am *auth.Module, publicURL string) app.Module {
	return &hook{name: "wire.auth-mailer", roles: []app.Role{app.RoleAPI}, start: func(_ context.Context, a *app.App) error {
		svc, err := app.MustGet[*sysmail.Service](a)
		if err != nil {
			return err
		}
		am.Options.Mailer = authMailer{svc: svc, publicURL: publicURL}
		return nil
	}}
}

// connectTenancy runs after auth and apikeys, before tenancy: session identities and server-side user
// creation (bootstrap owner, invitations) come from Limen, bearer tokens from internal/apikeys.
func connectTenancy(tm *tenancy.Module) app.Module {
	return &hook{name: "wire.tenancy", roles: []app.Role{app.RoleAPI}, start: func(_ context.Context, a *app.App) error {
		svc, err := app.MustGet[*auth.Service](a)
		if err != nil {
			return err
		}
		tm.Identity = func(ctx context.Context) (tenancy.Identity, bool) {
			id, ok := auth.IdentityFrom(ctx)
			if !ok {
				return tenancy.Identity{}, false
			}
			return tenancy.Identity{AuthUserID: id.AuthUserID, Email: id.Email, MFA: id.MFA}, true
		}
		tm.Creator = svc.CreateUser
		// OIDC SSO (P3.4): invited addresses may sign in with SSO; provider group mappings create memberships.
		svc.InviteChecker = func(ctx context.Context, email string) (bool, error) {
			ts, err := app.MustGet[*tenancy.Service](a)
			if err != nil {
				return false, err
			}
			return ts.HasPendingInvite(ctx, email)
		}
		svc.ProvisionHook = func(ctx context.Context, authUserID, email, providerID string, groupRoles map[string]string, groups []string) error {
			ts, err := app.MustGet[*tenancy.Service](a)
			if err != nil {
				return err
			}
			return ts.ProvisionFromGroups(ctx, authUserID, email, providerID, groupRoles, groups)
		}
		// users.lastLoginAt: the tenancy service exists once tm has started, so look it up per sign-in.
		svc.OnLogin = func(ctx context.Context, authUserID string) error {
			ts, err := app.MustGet[*tenancy.Service](a)
			if err != nil {
				return err
			}
			return ts.TouchLogin(ctx, authUserID)
		}
		keys, err := app.MustGet[tenancy.KeyResolver](a)
		if err != nil {
			return err
		}
		tm.Keys = keys
		js, err := app.MustGet[*jobs.Service](a)
		if err != nil {
			return err
		}
		tm.Jobs = func(ctx context.Context, kind string, payload any) (string, error) {
			j, err := js.Create(ctx, kind, payload)
			if err != nil {
				return "", err
			}
			return j.ID, nil
		}
		return nil
	}}
}

type settingsChange struct {
	Scope string   `json:"scope"`
	Keys  []string `json:"keys"`
}

// settingsNotifier publishes runtime-settings changes so every node drops its cached copy.
func settingsNotifier(bp **bus.Bus, log *slog.Logger) settings.Notifier {
	return func(ctx context.Context, scope string, keys []string) {
		b := *bp
		if b == nil {
			return
		}
		data, err := json.Marshal(settingsChange{Scope: scope, Keys: keys})
		if err == nil {
			err = b.PublishCore(bus.SettingsChangedSubject, data)
		}
		if err != nil {
			log.WarnContext(ctx, "settings change not announced; other nodes refresh on their cache TTL", "scope", scope, "err", err)
		}
	}
}

// connectSettings runs after bus and settings: it resolves the bus for the notifier and subscribes this
// node to changes made on other nodes.
func connectSettings(bp **bus.Bus, log *slog.Logger) app.Module {
	var unsub func() error
	return &hookStop{hook: hook{name: "wire.settings", roles: []app.Role{app.RoleAPI}, start: func(_ context.Context, a *app.App) error {
		b, err := app.MustGet[*bus.Bus](a)
		if err != nil {
			return err
		}
		svc, err := app.MustGet[*settings.Service](a)
		if err != nil {
			return err
		}
		*bp = b
		unsub, err = b.SubscribeCore(bus.SettingsChangedSubject, func(_ string, data []byte) {
			var ch settingsChange
			if err := json.Unmarshal(data, &ch); err != nil {
				log.Warn("malformed settings change notice", "err", err)
				return
			}
			svc.Invalidate(ch.Scope)
		})
		if err != nil {
			return fmt.Errorf("subscribe settings changes: %w", err)
		}
		return nil
	}}, stop: func() error {
		if unsub != nil {
			return unsub()
		}
		return nil
	}}
}

type hookStop struct {
	hook
	stop func() error
}

func (h *hookStop) Stop(context.Context) error { return h.stop() }

// connectSubmitExtras runs after the submit module started: it gives the submit service the template renderer
// (P5.1), the reply/forward message source (P5.1) and the SMTP gateway resolver (P5.3).
func connectSubmitExtras(sub *submit.Module) app.Module {
	return &hook{name: "wire.submit.extras", roles: []app.Role{app.RoleAPI, app.RoleSubmit}, start: func(_ context.Context, a *app.App) error {
		svc := sub.Service()
		if svc == nil {
			return nil
		}
		if tpl, ok := app.Get[*templates.Service](a); ok {
			svc.Templates = tpl
		}
		db, err := app.MustGet[*store.DB](a)
		if err != nil {
			return err
		}
		objects, err := app.MustGet[*objstore.Store](a)
		if err != nil {
			return err
		}
		svc.Messages = &submit.IndexArchiveSource{DB: db, Objects: objects}
		if g, ok := app.Get[*gateways.Service](a); ok {
			svc.Gateways = gatewayResolver{g}
		}
		if aw, ok := app.Get[*audit.Writer](a); ok {
			svc.Audit = aw
		}
		if sp, ok := app.Get[*suppression.Service](a); ok {
			svc.Suppression = sp
		}
		oa, _ := app.Get[*oauth.Service](a) // the senders need the account's OAuth token per send (G8)
		if gs, ok := app.Get[*graph.Sender](a); ok {
			if oa != nil {
				gs.SetTokens(oa)
			}
			svc.Graph = gs // provider=graph accounts send through Microsoft Graph (P6.3)
		}
		if gs, ok := app.Get[*gmail.Sender](a); ok {
			if oa != nil {
				gs.SetTokens(oa)
			}
			svc.Gmail = gs // provider=gmail accounts send through the Gmail API (P6.2)
		}
		if g, ok := app.Get[*throttle.Gate](a); ok {
			svc.Throttle = g // per-domain / per-provider buckets, send windows, pre-send policy (P5.5)
		}
		if tr, ok := app.Get[*tracking.Service](a); ok {
			svc.Tracking = tr
			if sp, ok := app.Get[*suppression.Service](a); ok {
				tr.Unsub = sp.AddUnsubscribe
			}
		}
		return nil
	}}
}

// gatewayResolver adapts the gateways service to the submit worker (P5.3).
type gatewayResolver struct{ g *gateways.Service }

func (r gatewayResolver) ResolveSMTP(ctx context.Context, orgID, gatewayID string) (*submit.SMTPAccount, error) {
	d, err := r.g.DialSettings(ctx, orgID, gatewayID)
	if errors.Is(err, gateways.ErrAPIGateway) {
		return r.resolveAPI(ctx, orgID, gatewayID)
	}
	if err != nil {
		return nil, err
	}
	return &submit.SMTPAccount{
		ID: d.ID, OrganizationID: d.OrganizationID, GatewayID: d.ID,
		Host: d.Host, Port: d.Port, TLS: d.TLS, User: d.User, Password: d.Password,
		EHLO: d.EHLOName, LocalAddress: d.LocalAddress, Proxy: d.Proxy, Policy: d.Policy,
		MaxAttempts: d.MaxAttempts, BackoffBase: d.BackoffBase, IgnoreCertErrors: d.IgnoreCertErrors,
	}, nil
}

// resolveAPI turns an API gateway (P8.3) into a submit account whose APISend calls the provider.
func (r gatewayResolver) resolveAPI(ctx context.Context, orgID, gatewayID string) (*submit.SMTPAccount, error) {
	res, err := r.g.ResolveAPI(ctx, orgID, gatewayID)
	if err != nil {
		return nil, err
	}
	g := r.g
	return &submit.SMTPAccount{
		ID: res.GatewayID, OrganizationID: orgID, GatewayID: res.GatewayID, Policy: res.Policy,
		MaxAttempts: res.MaxAttempts, BackoffBase: res.BackoffBase,
		APISend: func(ctx context.Context, m submit.APIMail) (string, error) {
			out, err := g.Send(ctx, res, gateways.OutboundMail{
				From: m.From, To: m.To, Cc: m.Cc, Bcc: m.Bcc, RawMIME: m.Raw,
				MessageIDHeader: m.MessageID, OutboundID: m.OutboundID,
			})
			if err != nil {
				return "", err
			}
			return out.ProviderMessageID, nil
		},
	}, nil
}

// connectMessages gives the messages API (P4.1) the account source with decrypted credentials.
func connectMessages(m *messages.Module, gm *gmail.Module, grm *graph.Module) app.Module {
	return &hook{name: "wire.messages", roles: []app.Role{app.RoleAPI}, start: func(_ context.Context, a *app.App) error {
		svc, err := accountsService(a)
		if err != nil {
			return err
		}
		m.Accounts = accounts.NewSyncSource(svc)
		// Mailbox API for API-backed accounts (G9): Gmail API / Microsoft Graph instead of IMAP.
		m.Providers = func(_ context.Context, acc *syncengine.Account) (mailbox.Ops, error) {
			o, ok := app.Get[*oauth.Service](a)
			if !ok || o == nil {
				return nil, mailbox.Unsupported("OAuth is not available on this node")
			}
			switch acc.Provider {
			case syncengine.ProviderGmail:
				if gm == nil || gm.Client() == nil {
					return nil, mailbox.Unsupported("the Gmail provider is not running")
				}
				return &gmail.Ops{Client: gm.Client(), Tokens: o, AccountID: acc.ID, User: acc.Email}, nil
			case syncengine.ProviderGraph:
				if grm == nil || grm.Client() == nil {
					return nil, mailbox.Unsupported("the Graph provider is not running")
				}
				return &graph.Ops{Client: grm.Client(), Tokens: o, AccountID: acc.ID, UPN: firstNonEmpty(acc.DelegatedUser, acc.Email)}, nil
			default:
				return nil, mailbox.Unsupported("unknown provider")
			}
		}
		return nil
	}}
}

// derivedKey derives a 32-byte purpose-bound key from the instance auth secret (HMAC-SHA256 with a label), so
// signed URLs (attachments, tracking) never share a key with sessions or with each other.
func derivedKey(secret, purpose string) []byte {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte("emailbridge/" + purpose))
	return mac.Sum(nil)
}

// connectAttachments gives the attachments API (P4.3) its two sources: the raw archive through the index
// (inbound.archiveRaw) and, when the message is not archived, the on-demand IMAP part fetch of the messages API.
func connectAttachments(m *attachments.Module) app.Module {
	// API (downloads) and webhook nodes (storage connector uploads, W52) read attachment content.
	return &hook{name: "wire.attachments", roles: []app.Role{app.RoleAPI, app.RoleWebhook}, start: func(_ context.Context, a *app.App) error {
		db, err := app.MustGet[*store.DB](a)
		if err != nil {
			return err
		}
		objects, err := app.MustGet[*objstore.Store](a)
		if err != nil {
			return err
		}
		m.Raw = archiveRaw{&submit.IndexArchiveSource{DB: db, Objects: objects}}
		if svc, ok := app.Get[*messages.Service](a); ok {
			m.Fetcher = imapParts{svc}
		}
		return nil
	}}
}

type archiveRaw struct{ src *submit.IndexArchiveSource }

func (r archiveRaw) OpenRaw(ctx context.Context, accountID, messageID string) (io.ReadCloser, error) {
	raw, err := r.src.Raw(ctx, accountID, messageID)
	if err != nil {
		return nil, err
	}
	return io.NopCloser(bytes.NewReader(raw)), nil
}

type imapParts struct{ svc *messages.Service }

func (f imapParts) FetchPart(ctx context.Context, accountID, messageID, partID string) (io.ReadCloser, attachments.PartInfo, error) {
	body, ctype, err := f.svc.GetAttachment(ctx, accountID, messageID, partID, 0)
	if err != nil {
		return nil, attachments.PartInfo{}, err
	}
	return io.NopCloser(bytes.NewReader(body)), attachments.PartInfo{ContentType: ctype, Size: int64(len(body))}, nil
}

// connectOAuth lets the accounts service hand out OAuth access tokens (P6.1) to every IMAP and SMTP connection
// of OAuth accounts; the oauth module refreshes them under a cluster lease.
func connectOAuth() app.Module {
	return &hook{name: "wire.oauth", roles: []app.Role{app.RoleAPI, app.RoleSync, app.RoleSubmit}, start: func(_ context.Context, a *app.App) error {
		svc, err := accountsService(a)
		if err != nil {
			return err
		}
		if o, ok := app.Get[*oauth.Service](a); ok {
			svc.OAuthToken = o.AccessToken
			svc.BindOAuthApp = o.LinkAppOnly // service-account / app-only mailboxes (W43)
		}
		if r, ok := app.Get[*authserver.Resolver](a); ok && r != nil {
			svc.Credentials = r.Resolve // accounts with useAuthServer: credentials fetched at connect time (W54)
		}
		return nil
	}}
}

// connectProtocolServers gives the SMTP submission server and the IMAP proxy (P7.1) their system lookups.
func connectProtocolServers(ms *msa.Module, px *imapproxy.Module) app.Module {
	return &hook{name: "wire.protocol", roles: []app.Role{app.RoleAPI}, start: func(_ context.Context, a *app.App) error {
		db, err := app.MustGet[*store.DB](a)
		if err != nil {
			return err
		}
		look := accountLookup{db}
		ms.Accounts = msaAccounts{look}
		px.Accounts = proxyAccounts{look}
		if keys, ok := app.Get[*apikeys.Service](a); ok {
			ms.APIKeys = apiKeySecrets{keys}
		}
		svc, err := accountsService(a)
		if err != nil {
			return err
		}
		px.Source = proxySource{accounts.NewSyncSource(svc)}
		if d, ok := app.Get[*egress.Dialer](a); ok {
			px.Dial = func(ctx context.Context, network, addr string) (net.Conn, error) {
				return d.DialContext(ctx, network, addr, egress.Mail)
			}
		}
		return nil
	}}
}

// accountLookup finds an account without an organisation context (protocol logins). An email address that
// several organisations connected is ambiguous: the client must log in with the account ID instead.
type accountLookup struct{ db *store.DB }

type lookedUp struct {
	ID             string `bson:"_id"`
	OrganizationID string `bson:"organizationId"`
	Email          string `bson:"email"`
	State          string `bson:"state"`
}

func (l accountLookup) byID(ctx context.Context, id string) (*lookedUp, error) {
	var a lookedUp
	if err := l.db.Collection(accounts.Collection).FindOne(ctx, bson.M{"_id": id}).Decode(&a); err != nil {
		return nil, errs.New(errs.CodeNotFound, "The account does not exist.")
	}
	return &a, nil
}

func (l accountLookup) byEmail(ctx context.Context, email string) (*lookedUp, error) {
	cur, err := l.db.Collection(accounts.Collection).Find(ctx, bson.M{"email": strings.ToLower(strings.TrimSpace(email))},
		options.Find().SetLimit(2))
	if err != nil {
		return nil, err
	}
	var rows []lookedUp
	if err := cur.All(ctx, &rows); err != nil {
		return nil, err
	}
	switch len(rows) {
	case 0:
		return nil, errs.New(errs.CodeNotFound, "The account does not exist.")
	case 1:
		return &rows[0], nil
	default:
		return nil, errs.New(errs.CodeConflict, "Several accounts use this address: log in with the account ID (acc_…).")
	}
}

type msaAccounts struct{ l accountLookup }

func (m msaAccounts) ByID(ctx context.Context, id string) (*msa.AccountRef, error) {
	a, err := m.l.byID(ctx, id)
	if err != nil {
		return nil, err
	}
	return &msa.AccountRef{ID: a.ID, OrganizationID: a.OrganizationID, Email: a.Email, State: a.State}, nil
}

func (m msaAccounts) ByEmail(ctx context.Context, email string) (*msa.AccountRef, error) {
	a, err := m.l.byEmail(ctx, email)
	if err != nil {
		return nil, err
	}
	return &msa.AccountRef{ID: a.ID, OrganizationID: a.OrganizationID, Email: a.Email, State: a.State}, nil
}

type proxyAccounts struct{ l accountLookup }

func (p proxyAccounts) ByID(ctx context.Context, id string) (*imapproxy.AccountRef, error) {
	a, err := p.l.byID(ctx, id)
	if err != nil {
		return nil, err
	}
	return &imapproxy.AccountRef{ID: a.ID, OrganizationID: a.OrganizationID, Email: a.Email, State: a.State}, nil
}

func (p proxyAccounts) ByEmail(ctx context.Context, email string) (*imapproxy.AccountRef, error) {
	a, err := p.l.byEmail(ctx, email)
	if err != nil {
		return nil, err
	}
	return &imapproxy.AccountRef{ID: a.ID, OrganizationID: a.OrganizationID, Email: a.Email, State: a.State}, nil
}

// apiKeySecrets verifies an API key used as an SMTP password (same checks as the HTTP API; the client IP of
// an SMTP session is not available, so IP-restricted keys are refused there).
type apiKeySecrets struct{ keys *apikeys.Service }

func (k apiKeySecrets) VerifySecret(ctx context.Context, secret string) (*tenancy.Caller, error) {
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, "/smtp-auth", nil)
	if err != nil {
		return nil, err
	}
	r.Header.Set("Authorization", "Bearer "+secret)
	c, err := k.keys.Resolve(r)
	if err != nil {
		return nil, err
	}
	if c == nil {
		return nil, errs.New(errs.CodeAPIKeyInvalid, "The API key is invalid.")
	}
	return c, nil
}

// proxySource loads the real IMAP server of an account for the proxy, with the OAuth token when it has one.
type proxySource struct{ ss *accounts.SyncSource }

func (p proxySource) LoadIMAP(ctx context.Context, accountID string) (*imapproxy.IMAPTarget, error) {
	acc, err := p.ss.Load(ctx, accountID)
	if err != nil {
		return nil, err
	}
	var o imapc.Options
	if err := syncengine.AuthOptions(ctx, acc, &o); err != nil {
		return nil, err
	}
	return &imapproxy.IMAPTarget{Addr: acc.IMAPAddr, TLS: acc.IMAPTLS, InsecureSkipVerify: acc.IgnoreCertErrors,
		Username: o.Username, Password: o.Password, AccessToken: o.AccessToken}, nil
}

// connectGmail gives the Gmail push endpoint (P6.2) its account lookup and the resync nudge.
func connectGmail(m *gmail.Module) app.Module {
	return &hook{name: "wire.gmail", roles: []app.Role{app.RoleAPI, app.RoleSync, app.RoleSubmit}, start: func(_ context.Context, a *app.App) error {
		svc, err := accountsService(a)
		if err != nil {
			return err
		}
		db, err := app.MustGet[*store.DB](a)
		if err != nil {
			return err
		}
		m.Lookup = func(ctx context.Context, email string) (string, string, error) {
			var acc struct {
				ID             string `bson:"_id"`
				OrganizationID string `bson:"organizationId"`
			}
			err := db.Collection(accounts.Collection).FindOne(ctx, bson.M{"email": strings.ToLower(strings.TrimSpace(email)),
				"provider": syncengine.ProviderGmail}).Decode(&acc)
			return acc.ID, acc.OrganizationID, err
		}
		m.Nudge = svc.NudgeSync
		return nil
	}}
}

// connectGraph gives the Graph change-notification endpoint (P6.3) the resync nudge; the backend and sender are
// picked up by connectSync / connectSubmitExtras once the module has started.
func connectGraph(m *graph.Module) app.Module {
	return &hook{name: "wire.graph", roles: []app.Role{app.RoleAPI, app.RoleSync, app.RoleSubmit}, start: func(_ context.Context, a *app.App) error {
		svc, err := accountsService(a)
		if err != nil {
			return err
		}
		m.Nudge = graphNudger{svc}
		return nil
	}}
}

type graphNudger struct{ svc *accounts.Service }

func (g graphNudger) NudgeGraph(ctx context.Context, accountID string) error {
	g.svc.NudgeSync(ctx, accountID)
	return nil
}

// connectHealth gives account health (P6.5) the account source and the on-demand IMAP pool for live checks.
func connectHealth(m *health.Module) app.Module {
	return &hook{name: "wire.health", roles: []app.Role{app.RoleAPI}, start: func(_ context.Context, a *app.App) error {
		svc, err := accountsService(a)
		if err != nil {
			return err
		}
		m.Accounts = accounts.NewSyncSource(svc)
		if msg, ok := app.Get[*messages.Service](a); ok {
			m.Pool = msg.Pool
		}
		return nil
	}}
}

// rspamdScorer scores inbound mail with the organisation's Rspamd connector instance (P8.3). The integrations
// module starts after the sync wiring, so its service is looked up when a message is scored.
type rspamdScorer struct{ im *integrations.Module }

func (r rspamdScorer) Score(ctx context.Context, orgID string, raw []byte) (*eventtypes.SpamInfo, error) {
	svc := r.im.Service()
	if svc == nil {
		return nil, nil
	}
	opts, err := svc.RspamdTarget(ctx, orgID)
	if err != nil || opts == nil {
		return nil, err
	}
	res, err := connectors.RspamdCheck(ctx, svc.HTTPClient(), *opts, raw)
	if err != nil {
		return nil, err
	}
	return &eventtypes.SpamInfo{Score: res.Score, Action: res.Action, Symbols: res.Symbols}, nil
}

// connectMeExport mounts GET /v1/me/export (S11 §6) once the tenancy service exists: profile, memberships, user
// settings, sessions (no tokens) and passkey names from auth, and the user's own audit records.
// cursorKey signs /v1/me/export audit cursors (W76), derived from the auth secret.
func connectMeExport(tm *tenancy.Module, cursorKey []byte) app.Module {
	return &hook{name: "wire.meExport", roles: []app.Role{app.RoleAPI}, start: func(_ context.Context, a *app.App) error {
		srv, err := app.MustGet[*httpapi.Server](a)
		if err != nil {
			return err
		}
		db, err := app.MustGet[*store.DB](a)
		if err != nil {
			return err
		}
		ts, err := app.MustGet[*tenancy.Service](a)
		if err != nil {
			return err
		}
		authSvc, err := app.MustGet[*auth.Service](a)
		if err != nil {
			return err
		}
		deps := tenancy.MeExportDeps{
			DB: db, Repo: ts.Repo, Rate: tm.CheckMeExportRate, CursorKey: cursorKey,
			Sessions: func(ctx context.Context, authUserID, _ string) ([]tenancy.ExportSession, error) {
				current := ""
				if id, ok := auth.IdentityFrom(ctx); ok {
					current = id.SessionID
				}
				return authSvc.ExportSessions(ctx, authUserID, current)
			},
			Passkeys: authSvc.ExportPasskeys,
		}
		deps.Audit = ts.Audit
		tenancy.RegisterMeExport(srv, deps)
		return nil
	}}
}

// sentryRefresh is how often every node re-reads the Sentry connector (W37): an install, edit or uninstall on
// one node reaches the error reporters of all nodes within this delay, and a restart picks it up at start.
const sentryRefresh = time.Minute

// connectSentryConnector lets an installed Sentry connector configure instance error reporting when
// EB_SENTRY_DSN is empty (EB_SENTRY_DSN always wins). The node that handles the change applies it at once.
func connectSentryConnector(im *integrations.Module) app.Module {
	done := make(chan struct{})
	var once gosync.Once
	return &hook{name: "wire.sentryConnector", start: func(_ context.Context, a *app.App) error {
		rep, ok := app.Get[*observability.Reporter](a)
		svc := im.Service()
		if !ok || rep == nil || svc == nil || rep.EnvLocked() {
			return nil
		}
		apply := func(enabled bool, dsn, env string, rate float64) {
			if !enabled || dsn == "" {
				rep.ClearConnector()
				return
			}
			if err := rep.ApplyConnector(observability.ReporterOptions{DSN: dsn, Environment: env, SampleRate: rate}); err != nil {
				a.Log.Warn("sentry connector: invalid configuration, error reporting stays off", "err", err)
			}
		}
		svc.OnSentryChange = func(_ context.Context, enabled bool, dsn, env string, rate float64) { apply(enabled, dsn, env, rate) }
		var last connectors.SentryRuntime
		refresh := func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			rt, err := svc.ActiveSentry(ctx)
			if err != nil {
				a.Log.Debug("sentry connector refresh failed", "err", err)
				return
			}
			if rt == last {
				return
			}
			last = rt
			apply(rt.Enabled, rt.DSN, rt.Environment, rt.SampleRate)
		}
		refresh()
		go func() {
			t := time.NewTicker(sentryRefresh)
			defer t.Stop()
			for {
				select {
				case <-done:
					return
				case <-t.C:
					refresh()
				}
			}
		}()
		return nil
	}, stop: func(context.Context) error {
		once.Do(func() { close(done) })
		return nil
	}}
}

// loadPEMPool reads a PEM bundle of certificates; an empty path means no anchors (trust unknown).
func loadPEMPool(path string) (*x509.CertPool, error) {
	if strings.TrimSpace(path) == "" {
		return nil, nil
	}
	b, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(b) {
		return nil, errors.New("no PEM certificates in the file")
	}
	return pool, nil
}

// connectPECReceipts gives the PEC receipts API (S17 §8) the raw source of a receipt: the indexed message through
// the same path as GET …/messages/{id}/raw; the evidence archive comes first once P9.2 is wired.
func connectPECReceipts(m *pecreceipts.Module, am *archive.Module, em *evidence.Module) app.Module {
	return &hook{name: "wire.pecReceipts", roles: []app.Role{app.RoleAPI}, start: func(_ context.Context, _ *app.App) error {
		m.Raw = pecreceipts.CompositeRaw{Message: archiveOpener{am}, Evidence: evidenceStore{em}}
		m.Evidence = evidenceStore{em}
		return nil
	}}
}

// connectEvidence gives the evidence archive (P9.2) the egress HTTP client for RFC 3161 and the TSA anchors.
func connectEvidence(m *evidence.Module, tsaAnchors string) app.Module {
	return &hook{name: "wire.evidence", start: func(_ context.Context, a *app.App) error {
		if d, ok := app.Get[*egress.Dialer](a); ok && d != nil {
			m.Options.HTTPClient = d.HTTPClient(30 * time.Second)
		}
		if strings.TrimSpace(tsaAnchors) != "" {
			anchors, err := evidence.LoadAnchorsFile(tsaAnchors)
			if err != nil {
				return fmt.Errorf("EB_PEC_TSA_ANCHORS: %w", err)
			}
			m.Options.TSAAnchors = anchors
		}
		return nil
	}}
}

// evidenceStore resolves the evidence store when used (the module starts after this wiring is built); with no
// evidence bucket configured it is never ready and refuses everything.
type evidenceStore struct{ m *evidence.Module }

func (e evidenceStore) Ready() bool { return e.m != nil && e.m.Store() != nil && e.m.Store().Ready() }

func (e evidenceStore) Archive(ctx context.Context, item evidence.Item) (evidence.Ref, error) {
	if !e.Ready() {
		return evidence.Ref{}, errs.New(errs.CodeEvidenceLockUnavailable, "The evidence archive is not available.")
	}
	return e.m.Store().Archive(ctx, item)
}

func (e evidenceStore) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	if e.m == nil || e.m.Store() == nil {
		return nil, errs.New(errs.CodeServiceUnavailable, "The evidence archive is not configured.")
	}
	return e.m.Store().Open(ctx, key)
}

func (e evidenceStore) Verify(ctx context.Context, ref evidence.Ref, sha string) (evidence.ArchiveCheck, error) {
	if e.m == nil || e.m.Store() == nil {
		return evidence.ArchiveCheck{}, errs.New(errs.CodeServiceUnavailable, "The evidence archive is not configured.")
	}
	return e.m.Store().Verify(ctx, ref, sha)
}

// evidencePolicy reads the organisation's pec.tsaUrl and pec.evidenceRetention (S17 §9) from its settings document.
func evidencePolicy(db *store.DB) func(ctx context.Context, org string) (string, time.Duration) {
	return func(ctx context.Context, org string) (string, time.Duration) {
		var doc settings.Doc
		if err := db.Collection(settings.SettingsCollection).FindOne(ctx, bson.M{"_id": settings.OrgDocID(org)}).Decode(&doc); err != nil {
			return "", 0
		}
		tsa, _ := doc.Values["pec.tsaUrl"].(string)
		var retention time.Duration
		if v, _ := doc.Values["pec.evidenceRetention"].(string); v != "" {
			retention = parseDays(v)
		}
		return strings.TrimSpace(tsa), retention
	}
}

// parseDays reads a duration that may use a "d" (days) suffix, as the settings defaults do ("3650d").
func parseDays(v string) time.Duration {
	v = strings.TrimSpace(v)
	if n, ok := strings.CutSuffix(v, "d"); ok {
		var days int
		if _, err := fmt.Sscanf(n, "%d", &days); err == nil && days > 0 {
			return time.Duration(days) * 24 * time.Hour
		}
		return 0
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return 0
	}
	return d
}

// archiveOpener resolves the archive service at call time (it exists once the archive module started).
type archiveOpener struct{ am *archive.Module }

func (o archiveOpener) Open(ctx context.Context, accountID, messageID string) (io.ReadCloser, error) {
	svc := o.am.Service()
	if svc == nil {
		return nil, errs.New(errs.CodeServiceUnavailable, "The raw message source is not available on this node.")
	}
	return svc.OpenRaw(ctx, accountID, messageID)
}

// connectRules gives CEL its `account` and `env` variables (S8 §2): both loaders were missing, so `account.*`
// and `env.*` were empty in routing and route tests (final pass, found by the P7 end-to-end suite). Secret env
// values are decrypted here and masked in any output.
func connectRules(m *rules.Module) app.Module {
	return &hook{name: "wire.rules", start: func(_ context.Context, a *app.App) error {
		db, err := app.MustGet[*store.DB](a)
		if err != nil {
			return err
		}
		m.EnvLoader = func(ctx context.Context, orgID string) (map[string]string, []string, error) {
			svc, ok := app.Get[*settings.Service](a)
			if !ok || svc == nil {
				return map[string]string{}, nil, nil
			}
			return svc.RuleEnv(ctx, orgID)
		}
		m.AccountLoader = func(ctx context.Context, orgID, accountID string) (map[string]any, error) {
			return accounts.RuleAccount(ctx, db, orgID, accountID) // id, externalId, email, name, metadata (G4)
		}
		return nil
	}}
}
