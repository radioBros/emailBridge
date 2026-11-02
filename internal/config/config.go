// Package config loads and validates EmailBridge environment configuration (S9 §1–§3).
package config

import (
	"fmt"
	"net/netip"
	"time"
)

// Source identifies where a config value came from.
type Source string

const (
	// SourceDefault means the built-in default was used.
	SourceDefault Source = "default"
	// SourceEnv means the value came from the process environment.
	SourceEnv Source = "env"
	// SourceFile means the value came from a <KEY>_FILE.
	SourceFile Source = "file"
)

// Report describes how Load resolved keys (for startup logging and typo warnings).
type Report struct {
	// Warnings lists unknown EB_* keys (excluding EB_TEST_* and known <KEY>_FILE forms).
	Warnings []string
	// Sources maps every known env key to where its value came from.
	Sources map[string]Source
}

// Config is the fully typed EmailBridge configuration.
type Config struct {
	Process   ProcessConfig
	HTTP      HTTPConfig
	Stores    StoresConfig
	Keys      KeysConfig
	Auth      AuthConfig
	SysMail   SysMailConfig
	Log       LogConfig
	Sync      SyncConfig
	Lease     LeaseConfig
	Submit    SubmitConfig
	Webhook   WebhookConfig
	Rate      RateConfig
	Retention RetentionConfig
	Protocol  ProtocolConfig
	Gmail     GmailConfig
	PEC       PECConfig
	AI        AIConfig
}

// ProcessConfig is S9 §2.1.
type ProcessConfig struct {
	Roles         []string      `env:"EB_ROLES" default:"all"`
	NodeID        string        `env:"EB_NODE_ID"` // default: hostname (applied after load)
	Env           string        `env:"EB_ENV" default:"production" oneof:"development|staging|production"`
	DataDir       string        `env:"EB_DATA_DIR" default:"/data"`
	ShutdownGrace time.Duration `env:"EB_SHUTDOWN_GRACE" default:"60s"`
}

// HTTPConfig is S9 §2.2.
type HTTPConfig struct {
	Addr              string         `env:"EB_HTTP_ADDR" default:":8080"`
	PublicURL         string         `env:"EB_PUBLIC_URL"`
	CallbackURL       string         `env:"EB_CALLBACK_URL"` // default: EB_PUBLIC_URL
	TrustedProxies    []netip.Prefix `env:"EB_HTTP_TRUSTED_PROXIES"`
	CORSOrigins       []string       `env:"EB_HTTP_CORS_ORIGINS"`
	MaxBody           Size           `env:"EB_HTTP_MAX_BODY" default:"32MiB"`
	RequestTimeout    time.Duration  `env:"EB_HTTP_REQUEST_TIMEOUT" default:"30s"`
	RequestTimeoutMax time.Duration  `env:"EB_HTTP_REQUEST_TIMEOUT_MAX" default:"120s"`
	MetricsAddr       string         `env:"EB_METRICS_ADDR" default:":9090"`
}

// StoresConfig is S9 §2.3.
type StoresConfig struct {
	MongoURI           string `env:"EB_MONGO_URI" secret:"true" required:"true"`
	MongoDatabase      string `env:"EB_MONGO_DATABASE" default:"emailbridge"`
	NATSMode           string `env:"EB_NATS_MODE" default:"embedded" oneof:"embedded|external"`
	NATSURL            string `env:"EB_NATS_URL"`
	NATSCreds          string `env:"EB_NATS_CREDS" secret:"true"`
	NATSReplicas       int    `env:"EB_NATS_REPLICAS" default:"1"`
	NATSEmbeddedListen string `env:"EB_NATS_EMBEDDED_LISTEN"`
	S3Endpoint         string `env:"EB_S3_ENDPOINT" required:"true"`
	S3Region           string `env:"EB_S3_REGION" default:"us-east-1"`
	S3AccessKey        string `env:"EB_S3_ACCESS_KEY" secret:"true"`
	S3SecretKey        string `env:"EB_S3_SECRET_KEY" secret:"true"`
	S3Bucket           string `env:"EB_S3_BUCKET" default:"emailbridge"`
	S3PathStyle        bool   `env:"EB_S3_PATH_STYLE" default:"true"`
	S3EvidenceBucket   string `env:"EB_S3_EVIDENCE_BUCKET"`
	S3EvidenceLockMode string `env:"EB_S3_EVIDENCE_LOCK_MODE" default:"GOVERNANCE"` // or COMPLIANCE (S17 §9)
	ClamAVAddr         string `env:"EB_CLAMAV_ADDR"`                                // tcp://host:3310 or unix:///path; empty = scans skipped
}

// KeysConfig is S9 §2.4.
type KeysConfig struct {
	Provider        string  `env:"EB_KEYS_PROVIDER" default:"local" oneof:"local|vault|awskms"`
	Local           KeyRing `env:"EB_KEYS_LOCAL" secret:"true"`
	Active          string  `env:"EB_KEYS_ACTIVE"` // default: first kid
	VaultAddr       string  `env:"EB_VAULT_ADDR"`
	VaultToken      string  `env:"EB_VAULT_TOKEN" secret:"true"`
	VaultTransitKey string  `env:"EB_VAULT_TRANSIT_KEY"`
	VaultNamespace  string  `env:"EB_VAULT_NAMESPACE"`
	AWSKMSKeyID     string  `env:"EB_AWS_KMS_KEY_ID"`
	AWSRegion       string  `env:"EB_AWS_REGION"`
}

// AuthConfig is S9 §2.5.
type AuthConfig struct {
	Secret              string        `env:"EB_AUTH_SECRET" secret:"true" required:"true"`
	CookieDomain        string        `env:"EB_AUTH_COOKIE_DOMAIN"`
	CookieSecure        bool          `env:"EB_AUTH_COOKIE_SECURE" default:"true"`
	SessionTTL          time.Duration `env:"EB_AUTH_SESSION_TTL" default:"12h" runtime:"true"`
	SessionIdle         time.Duration `env:"EB_AUTH_SESSION_IDLE" default:"2h" runtime:"true"`
	MFARequired         bool          `env:"EB_AUTH_MFA_REQUIRED" default:"true" runtime:"true"`
	WebAuthnRPID        string        `env:"EB_AUTH_WEBAUTHN_RP_ID"`
	LockoutThreshold    int           `env:"EB_AUTH_LOCKOUT_THRESHOLD" default:"5" runtime:"true"`
	LockoutDuration     time.Duration `env:"EB_AUTH_LOCKOUT_DURATION" default:"15m" runtime:"true"`
	BootstrapOwnerEmail string        `env:"EB_BOOTSTRAP_OWNER_EMAIL"`
	BootstrapOwnerPass  string        `env:"EB_BOOTSTRAP_OWNER_PASSWORD" secret:"true"`
	BootstrapOrgName    string        `env:"EB_BOOTSTRAP_ORG_NAME" default:"Default"`
}

// SysMailConfig is S9 §2.6.
type SysMailConfig struct {
	Mode      string `env:"EB_SYSMAIL_MODE" default:"off" oneof:"off|smtp|account" runtime:"true"`
	From      string `env:"EB_SYSMAIL_FROM" runtime:"true"`
	SMTPHost  string `env:"EB_SYSMAIL_SMTP_HOST" runtime:"true"`
	SMTPPort  int    `env:"EB_SYSMAIL_SMTP_PORT" default:"465" runtime:"true"`
	SMTPTLS   string `env:"EB_SYSMAIL_SMTP_TLS" default:"implicit" oneof:"implicit|starttls|none" runtime:"true"`
	SMTPUser  string `env:"EB_SYSMAIL_SMTP_USER" runtime:"true"`
	SMTPPass  string `env:"EB_SYSMAIL_SMTP_PASS" secret:"true" runtime:"true"`
	AccountID string `env:"EB_SYSMAIL_ACCOUNT_ID" runtime:"true"`
	// AllowList: outside production, system mail goes only to these addresses (comma-separated).
	AllowList []string `env:"EB_SYSMAIL_ALLOW_LIST" runtime:"true"`
}

// LogConfig is S9 §2.7 (log + Sentry + OTel).
type LogConfig struct {
	Dir                    string        `env:"EB_LOG_DIR"` // default: $EB_DATA_DIR/logs
	Level                  string        `env:"EB_LOG_LEVEL" default:"info" runtime:"true"`
	DiskCap                Size          `env:"EB_LOG_DISK_CAP" default:"5GiB" runtime:"true"`
	Retention              time.Duration `env:"EB_LOG_RETENTION" default:"30d" runtime:"true"`
	Stdout                 string        `env:"EB_LOG_STDOUT" default:"off" oneof:"off|json|text" runtime:"true"`
	HashAddresses          bool          `env:"EB_LOG_HASH_ADDRESSES" default:"false" runtime:"true"`
	SentryDSN              string        `env:"EB_SENTRY_DSN" secret:"true"`
	SentryEnvironment      string        `env:"EB_SENTRY_ENVIRONMENT"`
	SentryRelease          string        `env:"EB_SENTRY_RELEASE"`
	SentrySampleRate       float64       `env:"EB_SENTRY_SAMPLE_RATE" default:"1.0" runtime:"true"`
	SentryTracesSampleRate float64       `env:"EB_SENTRY_TRACES_SAMPLE_RATE" default:"0" runtime:"true"`
	SentrySendPII          bool          `env:"EB_SENTRY_SEND_PII" default:"false" runtime:"true"`
	OTelEnabled            bool          `env:"EB_OTEL_ENABLED" default:"false"`
}

// SyncConfig is S9 §2.8.
type SyncConfig struct {
	MaxAccounts              int           `env:"EB_SYNC_MAX_ACCOUNTS" default:"2000"`
	ConnectRate              Rate          `env:"EB_SYNC_CONNECT_RATE" default:"20/s"`
	Tick                     time.Duration `env:"EB_SYNC_TICK" default:"5s"`
	HandoffTimeout           time.Duration `env:"EB_SYNC_HANDOFF_TIMEOUT" default:"30s"`
	ResyncDelay              time.Duration `env:"EB_SYNC_RESYNC_DELAY" default:"900s" runtime:"true"`
	MaxIdle                  time.Duration `env:"EB_SYNC_MAX_IDLE" default:"25m" runtime:"true"`
	ReconnectMin             time.Duration `env:"EB_SYNC_RECONNECT_MIN" default:"5s"`
	ReconnectMax             time.Duration `env:"EB_SYNC_RECONNECT_MAX" default:"10m"`
	AuthDisableAfter         time.Duration `env:"EB_SYNC_AUTH_DISABLE_AFTER" default:"72h" runtime:"true"`
	NoIdlePoll               time.Duration `env:"EB_SYNC_NOIDLE_POLL" default:"60s" runtime:"true"`
	FullResync               time.Duration `env:"EB_SYNC_FULL_RESYNC" default:"24h" runtime:"true"`
	CatchupMax               time.Duration `env:"EB_SYNC_CATCHUP_MAX" default:"7d" runtime:"true"`
	MaxConnectionsPerAccount int           `env:"EB_SYNC_MAX_CONNECTIONS_PER_ACCOUNT" default:"3" runtime:"true"`
	FetchBatch               int           `env:"EB_SYNC_FETCH_BATCH" default:"100"`
	TextMax                  Size          `env:"EB_SYNC_TEXT_MAX" default:"256KiB" runtime:"true"`
	IMAPIDName               string        `env:"EB_IMAP_ID_NAME" default:"EmailBridge" runtime:"true"`
	IMAPIDVersion            string        `env:"EB_IMAP_ID_VERSION" runtime:"true"`
	IMAPIDVendor             string        `env:"EB_IMAP_ID_VENDOR" default:"EmailBridge" runtime:"true"`
	IMAPIDSupportURL         string        `env:"EB_IMAP_ID_SUPPORT_URL" runtime:"true"`
}

// LeaseConfig is S9 §2.9.
type LeaseConfig struct {
	LeaseTTL           time.Duration `env:"EB_LEASE_TTL" default:"30s"`
	LeaseRenew         time.Duration `env:"EB_LEASE_RENEW"` // default: TTL/3
	LeaderTTL          time.Duration `env:"EB_LEADER_TTL" default:"15s"`
	NodeHeartbeat      time.Duration `env:"EB_NODE_HEARTBEAT" default:"5s"`
	EventPartitions    int           `env:"EB_EVENT_PARTITIONS" default:"64"`
	DeliveryPartitions int           `env:"EB_DELIVERY_PARTITIONS" default:"256"`
}

// SubmitConfig is S9 §2.10.
type SubmitConfig struct {
	Concurrency     int           `env:"EB_SUBMIT_CONCURRENCY" default:"32"`
	MaxMessageSize  Size          `env:"EB_SUBMIT_MAX_MESSAGE_SIZE" default:"25MiB" runtime:"true"`
	Policy          string        `env:"EB_SUBMIT_POLICY" default:"standard" oneof:"standard|atMostOnce" runtime:"true"`
	MaxAttempts     int           `env:"EB_SUBMIT_MAX_ATTEMPTS" default:"10" runtime:"true"`
	BackoffBase     time.Duration `env:"EB_SUBMIT_BACKOFF_BASE" default:"5s" runtime:"true"`
	BackoffJitter   float64       `env:"EB_SUBMIT_BACKOFF_JITTER" default:"0.2" runtime:"true"`
	SMTPPoolMaxMsgs int           `env:"EB_SMTP_POOL_MAX_MESSAGES" default:"100"`
	SMTPPoolIdle    time.Duration `env:"EB_SMTP_POOL_IDLE" default:"10m"`
	SMTPPoolPerAcct int           `env:"EB_SMTP_POOL_PER_ACCOUNT" default:"2"`
	IdempotencyTTL  time.Duration `env:"EB_IDEMPOTENCY_TTL" default:"24h"`
}

// WebhookConfig is S9 §2.11.
type WebhookConfig struct {
	Concurrency          int           `env:"EB_WEBHOOK_CONCURRENCY" default:"64"`
	Timeout              time.Duration `env:"EB_WEBHOOK_TIMEOUT" default:"30s" runtime:"true"`
	MaxAttempts          int           `env:"EB_WEBHOOK_MAX_ATTEMPTS" default:"10" runtime:"true"`
	BackoffBase          time.Duration `env:"EB_WEBHOOK_BACKOFF_BASE" default:"5s" runtime:"true"`
	BackoffJitter        float64       `env:"EB_WEBHOOK_BACKOFF_JITTER" default:"0.2" runtime:"true"`
	ResponseExcerpt      Size          `env:"EB_WEBHOOK_RESPONSE_EXCERPT" default:"4KiB"`
	EgressBlockLinkLocal bool          `env:"EB_EGRESS_BLOCK_LINK_LOCAL" default:"true" runtime:"true"`
	EgressBlockPrivate   bool          `env:"EB_EGRESS_BLOCK_PRIVATE" default:"false" runtime:"true"`
	EgressAllow          []string      `env:"EB_EGRESS_ALLOW" runtime:"true"`
	RulesCostLimit       int64         `env:"EB_RULES_COST_LIMIT" default:"50000" runtime:"true"`
	RulesTimeLimit       time.Duration `env:"EB_RULES_TIME_LIMIT" default:"50ms" runtime:"true"`
}

// RateConfig is S9 §2.12.
type RateConfig struct {
	APIKey                  Rate `env:"EB_RATE_API_KEY" default:"20/s,burst=40" runtime:"true"`
	APIOrg                  Rate `env:"EB_RATE_API_ORG" default:"100/s,burst=200" runtime:"true"`
	Login                   Rate `env:"EB_RATE_LOGIN" default:"10/min" runtime:"true"`
	QuotaAccounts           int  `env:"EB_QUOTA_ACCOUNTS" default:"0" runtime:"true"`
	QuotaSendsPerDay        int  `env:"EB_QUOTA_SENDS_PER_DAY" default:"0" runtime:"true"`
	QuotaSendsPerAccountMin int  `env:"EB_QUOTA_SENDS_PER_ACCOUNT_MINUTE" default:"60" runtime:"true"`
	QuotaEndpoints          int  `env:"EB_QUOTA_ENDPOINTS" default:"50" runtime:"true"`
}

// RetentionConfig is S9 §2.13.
type RetentionConfig struct {
	Events      time.Duration `env:"EB_RETENTION_EVENTS" default:"7d" runtime:"true"`
	Deliveries  time.Duration `env:"EB_RETENTION_DELIVERIES" default:"30d" runtime:"true"`
	Outbound    time.Duration `env:"EB_RETENTION_OUTBOUND" default:"90d" runtime:"true"`
	OutboundRaw time.Duration `env:"EB_RETENTION_OUTBOUND_RAW" default:"7d" runtime:"true"`
	Sandbox     time.Duration `env:"EB_RETENTION_SANDBOX" default:"14d" runtime:"true"`
	Jobs        time.Duration `env:"EB_RETENTION_JOBS" default:"30d" runtime:"true"`
	Audit       time.Duration `env:"EB_RETENTION_AUDIT" default:"365d" runtime:"true"`
	Tracking    time.Duration `env:"EB_RETENTION_TRACKING" default:"90d" runtime:"true"`
}

// fieldMeta is collected from struct tags for the reflection loader.
type fieldMeta struct {
	Key      string
	Default  string
	Secret   bool
	Runtime  bool
	Required bool
	OneOf    []string
	Field    any // pointer into Config for setting
	Kind     fieldKind
}

type fieldKind int

const (
	kindString fieldKind = iota
	kindInt
	kindInt64
	kindBool
	kindFloat64
	kindDuration
	kindSize
	kindStringSlice
	kindPrefixSlice
	kindRate
	kindKeyRing
)

// ValidationError collects all validation problems from Load / Validate.
type ValidationError struct {
	Problems []string
}

// Error implements error.
func (e *ValidationError) Error() string {
	if len(e.Problems) == 1 {
		return e.Problems[0]
	}
	out := fmt.Sprintf("%d config problems:", len(e.Problems))
	for _, p := range e.Problems {
		out += "\n  - " + p
	}
	return out
}

// ProtocolConfig is the SMTP submission server and the IMAP proxy (P7.1, docs/spec/P7.1-msa.md). Both are off by
// default and listen only on api nodes; the per-organisation switches protocol.msa / protocol.imapProxy apply too.
type ProtocolConfig struct {
	SMTPEnabled           bool   `env:"EB_SMTP_SERVER_ENABLED" default:"false"`
	SMTPAddr              string `env:"EB_SMTP_SERVER_ADDR" default:":1587"`
	SMTPAddrTLS           string `env:"EB_SMTP_SERVER_ADDR_TLS"`
	SMTPDomain            string `env:"EB_SMTP_SERVER_DOMAIN"`
	SMTPTLSCertFile       string `env:"EB_SMTP_SERVER_TLS_CERT_FILE"`
	SMTPTLSKeyFile        string `env:"EB_SMTP_SERVER_TLS_KEY_FILE"`
	SMTPMaxMessageSize    int64  `env:"EB_SMTP_SERVER_MAX_MESSAGE_SIZE" default:"26214400"`
	SMTPMaxRecipients     int    `env:"EB_SMTP_SERVER_MAX_RECIPIENTS" default:"100"`
	SMTPRatePerAccount    int    `env:"EB_SMTP_SERVER_RATE_PER_ACCOUNT" default:"60"`
	SMTPAuthRate          int    `env:"EB_SMTP_SERVER_AUTH_RATE" default:"10"`
	SMTPAllowInsecureAuth bool   `env:"EB_SMTP_SERVER_ALLOW_INSECURE_AUTH" default:"false"`

	IMAPProxyEnabled           bool   `env:"EB_IMAP_PROXY_ENABLED" default:"false"`
	IMAPProxyAddr              string `env:"EB_IMAP_PROXY_ADDR" default:":1143"`
	IMAPProxyAddrTLS           string `env:"EB_IMAP_PROXY_ADDR_TLS"`
	IMAPProxyTLSCertFile       string `env:"EB_IMAP_PROXY_TLS_CERT_FILE"`
	IMAPProxyTLSKeyFile        string `env:"EB_IMAP_PROXY_TLS_KEY_FILE"`
	IMAPProxyMaxConnPerAccount int    `env:"EB_IMAP_PROXY_MAX_CONNECTIONS_PER_ACCOUNT" default:"2"`
	IMAPProxyAuthRate          int    `env:"EB_IMAP_PROXY_AUTH_RATE" default:"10"`
	IMAPProxyAllowInsecureAuth bool   `env:"EB_IMAP_PROXY_ALLOW_INSECURE_AUTH" default:"false"`
}

// GmailConfig is the Gmail API backend (P6.2, docs/spec/P6.2-gmail.md).
type GmailConfig struct {
	// PushServiceAccount, when set, is the only service account whose Pub/Sub push tokens are accepted.
	PushServiceAccount string `env:"EB_GMAIL_PUSH_SERVICE_ACCOUNT"`
	// APIURL overrides the Gmail API host (https://gmail.googleapis.com); for the provider simulator and private
	// gateways only. GraphAPIURL overrides https://graph.microsoft.com the same way.
	APIURL      string `env:"EB_GMAIL_API_URL"`
	GraphAPIURL string `env:"EB_GRAPH_API_URL"`
}

// PECConfig is S17 §7.
type PECConfig struct {
	// TrustAnchors is a PEM bundle of PEC provider certificates (or their CAs): receipts whose signer chains to one
	// are reported trusted; without anchors trust is unknown.
	TrustAnchors string `env:"EB_PEC_TRUST_ANCHORS"`
	// TSAAnchors is a PEM bundle of time-stamping authority roots for RFC 3161 token checks (S17 §9).
	TSAAnchors string `env:"EB_PEC_TSA_ANCHORS"`
}

// AIConfig holds the node limits of the AI features (S9 §2.13c). Provider settings live per organisation
// in the AI settings, not here.
type AIConfig struct {
	// EmbeddingsMaxChunks caps the chunks one semantic search or ask compares (G1).
	EmbeddingsMaxChunks int `env:"EB_AI_EMBEDDINGS_MAX_CHUNKS" default:"50000"`
	// EmbeddingsRPM limits background embedding requests per organisation and node (G1).
	EmbeddingsRPM int `env:"EB_AI_EMBEDDINGS_RPM" default:"60"`
}
