package main

import (
	"context"
	"net/url"
	"path/filepath"
	"time"

	"github.com/radiobros/emailbridge/internal/evidence"

	"github.com/radiobros/emailbridge/internal/authserver"
	"github.com/radiobros/emailbridge/internal/autoconfig"
	"github.com/radiobros/emailbridge/internal/connect"
	"github.com/radiobros/emailbridge/internal/pecreceipts"

	"github.com/radiobros/emailbridge/internal/accounts"
	"github.com/radiobros/emailbridge/internal/ai"
	"github.com/radiobros/emailbridge/internal/apikeys"
	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/archive"
	"github.com/radiobros/emailbridge/internal/attachments"
	"github.com/radiobros/emailbridge/internal/audit"
	"github.com/radiobros/emailbridge/internal/auth"
	"github.com/radiobros/emailbridge/internal/bus"
	"github.com/radiobros/emailbridge/internal/cluster"
	"github.com/radiobros/emailbridge/internal/config"
	"github.com/radiobros/emailbridge/internal/configcode"
	"github.com/radiobros/emailbridge/internal/crypto"
	"github.com/radiobros/emailbridge/internal/deliverytest"
	"github.com/radiobros/emailbridge/internal/embeddings"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/events"
	"github.com/radiobros/emailbridge/internal/exports"
	"github.com/radiobros/emailbridge/internal/features"
	"github.com/radiobros/emailbridge/internal/fulltext"
	"github.com/radiobros/emailbridge/internal/gateways"
	"github.com/radiobros/emailbridge/internal/health"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/idempotency"
	"github.com/radiobros/emailbridge/internal/imapproxy"
	"github.com/radiobros/emailbridge/internal/index"
	"github.com/radiobros/emailbridge/internal/integrations"
	"github.com/radiobros/emailbridge/internal/ippool"
	"github.com/radiobros/emailbridge/internal/jobs"
	"github.com/radiobros/emailbridge/internal/logging"
	logquery "github.com/radiobros/emailbridge/internal/logging/query"
	logstore "github.com/radiobros/emailbridge/internal/logging/store"
	"github.com/radiobros/emailbridge/internal/mailproto/smtpc"
	"github.com/radiobros/emailbridge/internal/mcp"
	"github.com/radiobros/emailbridge/internal/messages"
	"github.com/radiobros/emailbridge/internal/msa"
	"github.com/radiobros/emailbridge/internal/notify"
	"github.com/radiobros/emailbridge/internal/oauth"
	"github.com/radiobros/emailbridge/internal/objstore"
	"github.com/radiobros/emailbridge/internal/observability"
	"github.com/radiobros/emailbridge/internal/protolog"
	"github.com/radiobros/emailbridge/internal/providers/gmail"
	"github.com/radiobros/emailbridge/internal/providers/graph"
	"github.com/radiobros/emailbridge/internal/queues"
	"github.com/radiobros/emailbridge/internal/realtime"
	"github.com/radiobros/emailbridge/internal/rules"
	"github.com/radiobros/emailbridge/internal/sandbox"
	"github.com/radiobros/emailbridge/internal/scheduler"
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
	"github.com/radiobros/emailbridge/internal/webui"
)

// tenancyModule resolves callers. The session identity source, the user creator and the API-key
// resolver are connected by connectTenancy once auth and apikeys have started.
func tenancyModule(cfg *config.Config) *tenancy.Module {
	m := &tenancy.Module{}
	m.Bootstrap.Email = cfg.Auth.BootstrapOwnerEmail
	m.Bootstrap.Password = cfg.Auth.BootstrapOwnerPass
	m.Bootstrap.OrgName = cfg.Auth.BootstrapOrgName
	m.PublicURL = cfg.HTTP.PublicURL
	m.InviteRate = cfg.Rate.Login
	return m
}

// migrations lists every MongoDB migration in order. Numbers are permanent; each domain adds its steps
// here when its work package lands (hot spot owned by P0.01, S0 §6).
func migrations() []store.Migration {
	return []store.Migration{
		crypto.Migration(1),
		tenancy.Migration(2),
		audit.Migration(3),
		settings.Migration(4),
		apikeys.Migration(5),
		rules.Migration(6),
		events.Migration(7),
		jobs.Migration(8),
		accounts.Migration(9),
		index.Migration(10),
		webhook.Migration(11),
		submit.Migration(12),
		archive.Migration(13),
		sandbox.Migration(14),
		webhook.LastDeliveryIndexMigration(15),
		rules.LastErrorIndexMigration(16),
		archive.TombstoneMigration(17),
		cluster.Migration(18),
		webhook.ReplayedAtIndexMigration(19),
		events.ListIndexesMigration(20),
		apikeys.GranularScopesMigration(21),
		messages.Migration(22),
		gateways.Migration(23),
		templates.Migration(24),
		auth.Migration(25),
		rules.P31Migration(26),
		attachments.Migration(27),
		suppression.Migration(28),
		tracking.Migration(29),
		ai.Migration(30),
		exports.Migration(31),
		oauth.Migration(32),
		index.ThreadIndexMigration(33),
		{Number: 34, Name: "msa and imap proxy credentials", Up: func(ctx context.Context, db *store.DB) error {
			if err := msa.EnsureIndexes(ctx, db); err != nil {
				return err
			}
			return imapproxy.EnsureIndexes(ctx, db)
		}},
		integrations.Migration(35),
		gmail.Migration(36),
		index.ProviderIDMigration(37),
		graph.Migration(38),
		connect.Migration(39),
		gateways.Migration(40), // kind index (W36)
		pecreceipts.Migration(41),
		suppression.MigrationMailingList(42), // list-scoped unsubscribes (W53)
		accounts.DelegatedIndexMigration(43), // delegated mailboxes (W54)
		connect.MigrationAssets(44),          // brandingAssets indexes (W68)
		rules.W73Migration(45),               // W73 named lists, rule stats, endpoint rule versions
		notify.Migration(46),                 // W75 system notifications
		cluster.KPIMigration(47),             // dashboard KPI indexes + apiCallStats TTL (W74)
		queues.Migration(48),                 // W72 queue item indexes + sysmailJobs records
		fulltext.Migration(49),               // G3 full-text store (messageText) indexes
		embeddings.Migration(50),             // G1 semantic search vectors (messageEmbeddings) indexes
	}
}

// modules lists every module of the binary in start order: platform modules first, then domain modules,
// and the HTTP listener last so all routes exist before the first request (and it stops first). Each
// work package adds its entry here when it lands. Keeping the list in cmd/ means internal/app never
// imports a domain package, so there are no import cycles.
func modules(_ context.Context, cfg *config.Config, report *config.Report, logs *logging.System) ([]app.Module, error) {
	creds, err := natsCredsFile(cfg)
	if err != nil {
		return nil, err
	}
	repRef := &reporterRef{} // bound after the observability module starts
	rulesMod := &rules.Module{
		EngineOptions: rules.EngineOptions{CostLimit: uint64(max(cfg.Webhook.RulesCostLimit, 1)), TimeLimit: cfg.Webhook.RulesTimeLimit},
		Log:           logs.For(logging.CatRules, "rules"),
	}
	ring := make([]crypto.LocalKey, len(cfg.Keys.Local))
	for i, k := range cfg.Keys.Local {
		ring[i] = crypto.LocalKey{ID: k.Kid, Key: k.Key}
	}
	authMod := &auth.Module{Options: auth.Options{
		Auth:        cfg.Auth,
		PublicURL:   cfg.HTTP.PublicURL,
		CORSOrigins: cfg.HTTP.CORSOrigins,
		Env:         cfg.Process.Env,
		LoginRate:   cfg.Rate.Login,
		EmailOTP:    cfg.SysMail.Mode != "off",
		Log:         logs.For(logging.CatAuth, "auth"),
		SecurityLog: logs.For(logging.CatSecurity, "auth"),
	}}
	tm := tenancyModule(cfg)
	subm := &submit.Module{
		Concurrency: cfg.Submit.Concurrency, MaxMessageSize: int64(cfg.Submit.MaxMessageSize), Policy: cfg.Submit.Policy,
		MaxAttempts: cfg.Submit.MaxAttempts, BackoffBase: cfg.Submit.BackoffBase, BackoffJitter: cfg.Submit.BackoffJitter,
		PoolMaxMsgs: cfg.Submit.SMTPPoolMaxMsgs, PoolIdle: cfg.Submit.SMTPPoolIdle, PoolPerAccount: cfg.Submit.SMTPPoolPerAcct,
		IdempotencyTTL: cfg.Submit.IdempotencyTTL, QuotaSendsPerDay: cfg.Rate.QuotaSendsPerDay,
		QuotaSendsPerMinute: cfg.Rate.QuotaSendsPerAccountMin, EHLO: hostOf(cfg.HTTP.PublicURL),
		Log: logs.For(logging.CatSend, "submit"),
	}
	sbx := &sandbox.Module{Env: cfg.Process.Env, Retention: cfg.Retention.Sandbox, Registry: features.DefaultRegistry(),
		Log: logs.For(logging.CatSend, "sandbox")}
	arm := &archive.Module{Log: logs.For(logging.CatSync, "archive")}
	pm := &protolog.Module{Log: logs.For(logging.CatAPI, "protolog")}
	msgMod := &messages.Module{Log: logs.For(logging.CatAPI, "messages")}
	healthMod := &health.Module{Log: logs.For(logging.CatAPI, "health")}
	graphMod := &graph.Module{CallbackURL: cfg.HTTP.CallbackURL, BaseURL: cfg.Gmail.GraphAPIURL, Log: logs.For(logging.CatSync, "graph")}
	gmailMod := &gmail.Module{CallbackURL: cfg.HTTP.CallbackURL, BaseURL: cfg.Gmail.APIURL, PushServiceAccount: cfg.Gmail.PushServiceAccount,
		Env: cfg.Process.Env, Log: logs.For(logging.CatSync, "gmail")}
	gwMod := &gateways.Module{Log: logs.For(logging.CatSend, "gateways"), EHLO: hostOf(cfg.HTTP.PublicURL),
		CallbackURL: cfg.HTTP.CallbackURL}
	trkMod := &tracking.Module{SignKey: derivedKey(cfg.Auth.Secret, "tracking/v1"), PublicURL: cfg.HTTP.PublicURL,
		TTL: cfg.Retention.Tracking, Registry: features.DefaultRegistry(), Env: cfg.Process.Env,
		Log: logs.For(logging.CatAPI, "tracking")}
	evMod := &evidence.Module{Options: evidence.OptionsFromEnv(cfg.Stores.S3Endpoint, cfg.Stores.S3Region,
		cfg.Stores.S3AccessKey, cfg.Stores.S3SecretKey, cfg.Stores.S3EvidenceBucket, cfg.Stores.S3PathStyle,
		cfg.Stores.S3EvidenceLockMode, 0, nil, nil), Log: logs.For(logging.CatSystem, "evidence")}
	pecMod := &pecreceipts.Module{Registry: features.DefaultRegistry(), Env: cfg.Process.Env,
		Log: logs.For(logging.CatAPI, "pecreceipts")}
	connectMod := &connect.Module{PublicURL: cfg.HTTP.PublicURL, Env: cfg.Process.Env, Registry: features.DefaultRegistry(),
		CSRFKey:    derivedKey(cfg.Auth.Secret, "connect/csrf/v1"),
		ClamAVAddr: cfg.Stores.ClamAVAddr,
		Log:        logs.For(logging.CatAPI, "connect")}
	intMod := &integrations.Module{Log: logs.For(logging.CatIntegration, "integrations")}
	notifyMod := &notify.Module{Env: cfg.Process.Env, PublicURL: cfg.HTTP.PublicURL, Registry: features.DefaultRegistry(),
		Log: logs.For(logging.CatSystem, "notify")}
	p := cfg.Protocol
	msaMod := &msa.Module{Env: cfg.Process.Env, Registry: features.DefaultRegistry(), Log: logs.For(logging.CatSend, "msa"),
		Options: msa.Options{Enabled: p.SMTPEnabled, Addr: p.SMTPAddr, AddrTLS: p.SMTPAddrTLS, Domain: p.SMTPDomain,
			TLSCertFile: p.SMTPTLSCertFile, TLSKeyFile: p.SMTPTLSKeyFile, MaxMessageBytes: p.SMTPMaxMessageSize,
			MaxRecipients: p.SMTPMaxRecipients, RatePerAccount: p.SMTPRatePerAccount, AuthRatePerIP: p.SMTPAuthRate,
			AllowInsecureAuth: p.SMTPAllowInsecureAuth}}
	proxyMod := &imapproxy.Module{Env: cfg.Process.Env, Registry: features.DefaultRegistry(), Log: logs.For(logging.CatSync, "imapproxy"),
		Options: imapproxy.Options{Enabled: p.IMAPProxyEnabled, Addr: p.IMAPProxyAddr, AddrTLS: p.IMAPProxyAddrTLS,
			TLSCertFile: p.IMAPProxyTLSCertFile, TLSKeyFile: p.IMAPProxyTLSKeyFile,
			MaxConnectionsPerAcct: p.IMAPProxyMaxConnPerAccount, AuthRatePerIP: p.IMAPProxyAuthRate,
			AllowInsecureAuth: p.IMAPProxyAllowInsecureAuth}}
	attMod := &attachments.Module{ClamAVAddr: cfg.Stores.ClamAVAddr, SignKey: derivedKey(cfg.Auth.Secret, "attachments/signed-url/v1"),
		PublicURL: cfg.HTTP.PublicURL, Log: logs.For(logging.CatAPI, "attachments")}
	imapID := map[string]string{"name": cfg.Sync.IMAPIDName, "version": firstNonEmpty(cfg.Sync.IMAPIDVersion, version),
		"vendor": cfg.Sync.IMAPIDVendor, "support-url": cfg.Sync.IMAPIDSupportURL}
	arm.IMAPID, msgMod.IMAPID = imapID, imapID // on-demand IMAP sends the same ID as sync (W57)
	sm := &syncengine.Module{
		Config: syncengine.Config{
			ReconnectMin: cfg.Sync.ReconnectMin, ReconnectMax: cfg.Sync.ReconnectMax, MaxIdle: cfg.Sync.MaxIdle,
			ResyncDelay: cfg.Sync.ResyncDelay, NoIdlePoll: cfg.Sync.NoIdlePoll, FullResync: cfg.Sync.FullResync,
			FetchBatch: cfg.Sync.FetchBatch, CatchupMax: cfg.Sync.CatchupMax, AuthDisableAfter: cfg.Sync.AuthDisableAfter,
			IMAPID: imapID,
		},
		LeaseTTL:       cfg.Lease.LeaseTTL,
		Tick:           cfg.Sync.Tick,
		HandoffTimeout: cfg.Sync.HandoffTimeout,
		MaxAccounts:    cfg.Sync.MaxAccounts,
		ConnectRate:    perTickDur(cfg.Sync.ConnectRate, cfg.Sync.Tick),
		Tap:            &protolog.Recorder{Log: logs.For(logging.CatProtocol, "imap")},
		Log:            logs.For(logging.CatSync, "sync"),
	}
	var settingsBus *bus.Bus
	return []app.Module{
		&store.Module{
			Options:    store.Options{URI: cfg.Stores.MongoURI, Database: cfg.Stores.MongoDatabase},
			Migrations: migrations(),
		},
		&bus.Module{Options: bus.Options{
			Mode:      bus.Mode(cfg.Stores.NATSMode),
			URL:       cfg.Stores.NATSURL,
			CredsFile: creds,
			DataDir:   cfg.Process.DataDir,
			Listen:    cfg.Stores.NATSEmbeddedListen,
			Replicas:  cfg.Stores.NATSReplicas,
			Log:       logs.For(logging.CatCluster, "bus"),
		}},
		&crypto.Module{Provider: crypto.ProviderOptions{
			Provider:        cfg.Keys.Provider,
			Local:           ring,
			Active:          cfg.Keys.Active,
			VaultAddr:       cfg.Keys.VaultAddr,
			VaultToken:      cfg.Keys.VaultToken,
			VaultTransitKey: cfg.Keys.VaultTransitKey,
			VaultNamespace:  cfg.Keys.VaultNamespace,
			AWSKMSKeyID:     cfg.Keys.AWSKMSKeyID,
			AWSRegion:       cfg.Keys.AWSRegion,
		}},
		&httpapi.Module{Options: httpapi.Options{
			Addr:              cfg.HTTP.Addr,
			PublicURL:         cfg.HTTP.PublicURL,
			TrustedProxies:    cfg.HTTP.TrustedProxies,
			CORSOrigins:       cfg.HTTP.CORSOrigins,
			MaxBody:           int64(cfg.HTTP.MaxBody),
			RequestTimeout:    cfg.HTTP.RequestTimeout,
			RequestTimeoutMax: cfg.HTTP.RequestTimeoutMax,
			Env:               cfg.Process.Env,
			AccessLog:         logs.For(logging.CatAPI, "http"),
			SecurityLog:       logs.For(logging.CatSecurity, "http"),
			OnPanic:           panicReporter(logs, repRef),
			OnReportable:      repRef.captureError,
			SPA:               webui.FS(),
		}},
		&objstore.Module{Options: objstore.Options{
			Endpoint:       cfg.Stores.S3Endpoint,
			Region:         cfg.Stores.S3Region,
			AccessKey:      cfg.Stores.S3AccessKey,
			SecretKey:      cfg.Stores.S3SecretKey,
			Bucket:         cfg.Stores.S3Bucket,
			PathStyle:      cfg.Stores.S3PathStyle,
			EvidenceBucket: cfg.Stores.S3EvidenceBucket,
		}},
		&observability.Module{Options: observability.ModuleOptions{
			MetricsAddr: cfg.HTTP.MetricsAddr,
			Tracing:     observability.TracingOptions{Enabled: cfg.Log.OTelEnabled, ServiceName: "emailbridge", Version: version, NodeID: cfg.Process.NodeID},
			Reporter: observability.ReporterOptions{
				DSN:              cfg.Log.SentryDSN,
				Environment:      firstNonEmpty(cfg.Log.SentryEnvironment, cfg.Process.Env),
				Release:          firstNonEmpty(cfg.Log.SentryRelease, version),
				SampleRate:       cfg.Log.SentrySampleRate,
				TracesSampleRate: cfg.Log.SentryTracesSampleRate,
				SendPII:          cfg.Log.SentrySendPII,
				NodeID:           cfg.Process.NodeID,
				Reportable:       errs.Reportable,
			},
			LogSystem: logs,
		}},
		bindReporter(repRef), // after observability: HTTP panics and internal errors reach the reporter
		&logstore.Module{System: logs, Options: logstore.Options{Dir: logDir(cfg)}},
		provideEgress(cfg, logs.For(logging.CatSecurity, "egress")),
		&ippool.Module{Log: logs.For(logging.CatSystem, "ippool")}, // before every module that dials mail (W57)
		connectEvidence(evMod, cfg.PEC.TSAAnchors),
		evMod,
		&scheduler.Module{TTL: cfg.Lease.LeaderTTL, Log: logs.For(logging.CatCluster, "scheduler")},
		&jobs.Module{Retention: func() time.Duration { return cfg.Retention.Jobs }, Log: logs.For(logging.CatJob, "jobs")},
		// Domain modules: each work package adds its entry here.
		&events.Module{
			Partitions: cfg.Lease.EventPartitions,
			Retention:  func() time.Duration { return cfg.Retention.Events },
			LeaseTTL:   cfg.Lease.LeaseTTL,
			Log:        logs.For(logging.CatEvent, "events"),
		},
		&sysmail.Module{Options: sysmail.Options{
			Mode:      cfg.SysMail.Mode,
			AccountID: cfg.SysMail.AccountID,
			From:      cfg.SysMail.From,
			Env:       cfg.Process.Env,
			AllowList: cfg.SysMail.AllowList,
			SMTP: smtpc.Options{
				Host: cfg.SysMail.SMTPHost, Port: cfg.SysMail.SMTPPort, TLS: smtpc.TLSMode(cfg.SysMail.SMTPTLS),
				User: cfg.SysMail.SMTPUser, Pass: cfg.SysMail.SMTPPass, EHLO: cfg.Process.NodeID,
			},
			Log: logs.For(logging.CatSystem, "sysmail"),
		}},
		&audit.Module{Log: logs.For(logging.CatAudit, "audit"), Retention: cfg.Retention.Audit},
		connectAuthMailer(authMod, cfg.HTTP.PublicURL),
		authMod,
		&apikeys.Module{Log: logs.For(logging.CatAuth, "apikeys")},
		connectTenancy(tm),
		tm,
		connectMeExport(tm, derivedKey(cfg.Auth.Secret, "me/export-cursor/v1")),
		// After tenancy: keys are scoped by the caller its middleware resolves (S2 §2).
		&idempotency.Module{TTL: cfg.Submit.IdempotencyTTL, Stale: 2 * cfg.HTTP.RequestTimeoutMax, Log: logs.For(logging.CatAPI, "idempotency")},
		&settings.Module{
			Boot:       cfg,
			EnvSources: report.Sources,
			Registry:   features.DefaultRegistry(),
			Notify:     settingsNotifier(&settingsBus, logs.For(logging.CatSystem, "settings")),
			Log:        logs.For(logging.CatSystem, "settings"),
		},
		connectSettings(&settingsBus, logs.For(logging.CatSystem, "settings")),
		&logquery.Module{System: logs, Authorizer: logAuthorizer()},
		&accounts.Module{QuotaAccounts: cfg.Rate.QuotaAccounts, IMAPID: imapID, Log: logs.For(logging.CatAPI, "accounts")},
		&oauth.Module{CallbackURL: cfg.HTTP.CallbackURL, PublicURL: cfg.HTTP.PublicURL, NodeID: cfg.Process.NodeID,
			GmailAPIRoot: cfg.Gmail.APIURL, GraphAPIRoot: cfg.Gmail.GraphAPIURL,
			Log: logs.For(logging.CatAPI, "oauth")},
		&authserver.Module{Log: logs.For(logging.CatSecurity, "authserver")}, // external credential server (W54)
		connectOAuth(),
		&autoconfig.Module{Log: logs.For(logging.CatAPI, "autoconfig")},
		connectHostedConnect(connectMod),
		connectMod,
		connectErasure(),
		connectArchive(arm),
		arm,
		connectPECReceipts(pecMod, arm, evMod),
		pecMod,
		connectGmail(gmailMod),
		gmailMod,
		connectGraph(graphMod),
		graphMod,
		connectSync(sm, graphMod, intMod, evMod, cfg.PEC.TrustAnchors),
		sm,
		sbx,
		&templates.Module{Registry: features.DefaultRegistry(), Env: cfg.Process.Env, Log: logs.For(logging.CatSend, "templates")},
		connectGatewaySink(gwMod),
		gwMod,
		&throttle.Module{Registry: features.DefaultRegistry(), Env: cfg.Process.Env, Log: logs.For(logging.CatSend, "throttle")},
		&suppression.Module{Registry: features.DefaultRegistry(), Env: cfg.Process.Env, Log: logs.For(logging.CatAPI, "suppression")},
		connectTrackingAccounts(trkMod),
		trkMod,
		connectSubmit(subm, sbx, sm.Config.IMAPID),
		subm,
		connectSubmitExtras(subm),
		&deliverytest.Module{Env: cfg.Process.Env, Log: logs.For(logging.CatSend, "deliverytest")},
		connectMessages(msgMod, gmailMod, graphMod),
		msgMod,
		// G3 full-text search: after messages (the indexer fetches bodies through messages.Service).
		&fulltext.Module{Env: cfg.Process.Env, Registry: features.DefaultRegistry(), EventPartitions: cfg.Lease.EventPartitions,
			LeaseTTL: cfg.Lease.LeaseTTL, Log: logs.For(logging.CatSync, "fulltext")},
		connectHealth(healthMod),
		healthMod,
		connectAttachments(attMod),
		attMod,
		connectSysmailAccount(subm),
		connectProtolog(pm),
		pm,
		connectRules(rulesMod),
		rulesMod,
		&webhook.Module{
			QuotaEndpoints:     cfg.Rate.QuotaEndpoints, // W76: instance endpoint quota
			EventPartitions:    cfg.Lease.EventPartitions,
			DeliveryPartitions: cfg.Lease.DeliveryPartitions,
			Concurrency:        cfg.Webhook.Concurrency,
			LeaseTTL:           cfg.Lease.LeaseTTL,
			ResponseExcerpt:    int(cfg.Webhook.ResponseExcerpt),
			Retention:          cfg.Retention.Deliveries,
			DefaultMaxAttempts: cfg.Webhook.MaxAttempts,
			DefaultBackoffBase: cfg.Webhook.BackoffBase,
			DefaultJitter:      cfg.Webhook.BackoffJitter,
			DefaultTimeout:     cfg.Webhook.Timeout,
			Log:                logs.For(logging.CatDelivery, "webhook"),
			SecurityLog:        logs.For(logging.CatSecurity, "webhook"),
		},
		&configcode.Module{Log: logs.For(logging.CatSystem, "configcode")},
		&exports.Module{SignKey: derivedKey(cfg.Auth.Secret, "exports/signed-url/v1"), PublicURL: cfg.HTTP.PublicURL,
			Env: cfg.Process.Env, Log: logs.For(logging.CatAPI, "exports")},
		&ai.Module{Env: cfg.Process.Env, Registry: features.DefaultRegistry(), Log: logs.For(logging.CatAPI, "ai")},
		// G1 semantic search: after fulltext (it observes the text store) and ai (embeddings and completions).
		&embeddings.Module{Env: cfg.Process.Env, Registry: features.DefaultRegistry(), Log: logs.For(logging.CatAPI, "embeddings"),
			MaxChunks: cfg.AI.EmbeddingsMaxChunks, RequestsPerMinute: float64(cfg.AI.EmbeddingsRPM)},
		&mcp.Module{Env: cfg.Process.Env, Registry: features.DefaultRegistry(), Log: logs.For(logging.CatAPI, "mcp")},
		connectIntegrations(intMod),
		intMod,
		connectSentryConnector(intMod),
		connectProtocolServers(msaMod, proxyMod),
		msaMod,
		proxyMod,
		&realtime.Module{},
		connectNotifySources(notifyMod), // before notify: watchers read JetStream queues and the node registry
		notifyMod,
		registerNotifyMetrics(notifyMod),
		// W72 queue page: after jobs, realtime and the domain modules; before cluster (GET /v1/queues counts).
		&queues.Module{Log: logs.For(logging.CatAPI, "queues")},
		// After tenancy: the API-call counter middleware needs the caller's organisation (W74).
		&cluster.Module{Log: logs.For(logging.CatCluster, "cluster"), Retention: func() cluster.Retention {
			return cluster.Retention{Events: cfg.Retention.Events, Deliveries: cfg.Retention.Deliveries, Outbound: cfg.Retention.Outbound}
		}},
		connectStreamMetrics(),
		activateMCPListener(), // after every module that registers routes: the dedicated MCP port opens now
		&httpapi.Listener{},
		&httpapi.HealthListener{Addr: cfg.HTTP.Addr}, // health only, on nodes without the api role
	}, nil
}

// hostOf returns the host of a URL ("" when unparsable).
func hostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

// logDir is EB_LOG_DIR, default $EB_DATA_DIR/logs.
func logDir(cfg *config.Config) string {
	if cfg.Log.Dir != "" {
		return cfg.Log.Dir
	}
	return filepath.Join(cfg.Process.DataDir, "logs")
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}

// activateMCPListener opens the dedicated MCP port (mcp.listen) once every route is registered, so the
// generated MCP tools never see a half-built API document.
func activateMCPListener() app.Module {
	return &hook{name: "wire.mcp.listener", roles: []app.Role{app.RoleAPI}, start: func(ctx context.Context, a *app.App) error {
		if ctl, ok := app.Get[*mcp.Control](a); ok && ctl != nil {
			ctl.Activate(ctx)
		}
		return nil
	}}
}
