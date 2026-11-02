package webhook

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/radiobros/emailbridge/internal/audit"
	"github.com/radiobros/emailbridge/internal/bus"
	"github.com/radiobros/emailbridge/internal/crypto"
	"github.com/radiobros/emailbridge/internal/egress"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/events"
	"github.com/radiobros/emailbridge/internal/jobs"
	"github.com/radiobros/emailbridge/internal/rules"
	"github.com/radiobros/emailbridge/internal/settings"
	"github.com/radiobros/emailbridge/internal/sinks"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

// Publisher is the bus surface used by routing, retries and replay.
type Publisher interface {
	Publish(ctx context.Context, subject string, data []byte, msgID string, headers map[string]string) (bool, error)
}

// SecretResolver may replace endpoint secrets with an organisation-wide secret when
// webhook.secret.perEndpoint is disabled.
type SecretResolver func(ctx context.Context, endpoint *Endpoint) ([]string, error)

type httpClientKey struct {
	dialer  *egress.Dialer
	timeout time.Duration
}

// Service implements webhook domain operations.
type Service struct {
	Repo               *Repo
	Cipher             *crypto.Cipher
	Egress             *egress.Dialer
	Events             *events.Writer
	Rules              *rules.Repo
	Engine             *rules.Engine
	Publisher          Publisher
	Audit              *audit.Writer
	Log                *slog.Logger
	SecurityLog        *slog.Logger
	Version            string
	Concurrency        int
	DeliveryPartitions int
	ResponseExcerpt    int
	Retention          time.Duration
	RotationOverlap    time.Duration
	DefaultMaxAttempts int
	DefaultBackoffBase time.Duration
	DefaultJitter      float64
	DefaultTimeout     time.Duration
	DefaultAutoDisable time.Duration
	Jobs               *jobs.Service
	Settings           *settings.Service
	SecretResolver     SecretResolver
	RulesEnabled       func(context.Context, string) bool
	NotifyDisabled     func(context.Context, *Endpoint)
	Now                func() time.Time
	RandFloat          func() float64
	// QuotaEndpoints is EB_QUOTA_ENDPOINTS (0 = unlimited). Combined with org quotas.endpoints.
	QuotaEndpoints int
	// Metrics, when set, records dead letters (eb_deadletters_total).
	Metrics DeadLetterMetrics
	// IntegrationAction runs CRM/storage connector actions for kind=integration (W52).
	// orgID is the endpoint's organisation: the action must refuse an instance of another organisation.
	IntegrationAction func(ctx context.Context, orgID, instanceID string, msg sinks.Message) error
	// AIEnricher optionally adds data.ai on message.received (fail-open, W52).
	AIEnricher func(ctx context.Context, org, accountID string, data map[string]any) map[string]any

	breakerMu     sync.Map
	httpClients   sync.Map
	mtlsClients   *sync.Map
	sinkCache     *sinks.Cache
	sinkCacheOnce sync.Once
	globalOnce    sync.Once
	globalSem     chan struct{}
	schedulerOnce sync.Once
	scheduler     *scheduler
}

func (s *Service) sinksCache() *sinks.Cache {
	s.sinkCacheOnce.Do(func() {
		s.sinkCache = sinks.NewCache(sinks.Deps{
			Dial:                    s.Egress,
			AllowAmbientCredentials: false, // refreshed per build from settings
		})
	})
	return s.sinkCache
}

// DeadLetterMetrics records deliveries that exhausted retries (S12 §9).
type DeadLetterMetrics interface {
	DeadLetter(queue string)
}

// QueueDelivery is the dead-letter queue label for webhook deliveries.
const QueueDelivery = "delivery"

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s *Service) random() float64 {
	if s.RandFloat != nil {
		return s.RandFloat()
	}
	//nolint:gosec // Retry jitter is not security-sensitive.
	return rand.Float64()
}

func (s *Service) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

func (s *Service) securityLog() *slog.Logger {
	if s.SecurityLog != nil {
		return s.SecurityLog
	}
	return s.log()
}

func (s *Service) httpClient(timeout time.Duration) *http.Client {
	key := httpClientKey{dialer: s.Egress, timeout: timeout}
	if client, ok := s.httpClients.Load(key); ok {
		return client.(*http.Client)
	}
	client, _ := s.httpClients.LoadOrStore(key, s.Egress.HTTPClient(timeout))
	return client.(*http.Client)
}

func (s *Service) retention() time.Duration {
	if s.Retention > 0 {
		return s.Retention
	}
	return 30 * 24 * time.Hour
}

func (s *Service) partitions() int {
	if s.DeliveryPartitions > 0 {
		return s.DeliveryPartitions
	}
	return 256
}

// CreateEndpoint validates, encrypts and stores a webhook endpoint.
func (s *Service) CreateEndpoint(ctx context.Context, in EndpointCreate) (*Endpoint, error) {
	if err := tenancy.Require(ctx, tenancy.PermWebhooksWrite); err != nil {
		return nil, err
	}
	caller, _ := tenancy.CallerFrom(ctx)
	if err := s.checkEndpointQuota(ctx, caller.OrganizationID); err != nil {
		return nil, err
	}
	kind := in.Kind
	if kind == "" {
		kind = KindHTTP
	}
	endpoint := &Endpoint{
		ID:             store.NewID(store.PrefixEndpoint),
		OrganizationID: caller.OrganizationID,
		Name:           strings.TrimSpace(in.Name),
		Kind:           kind,
		Enabled:        true,
		URL:            strings.TrimSpace(in.URL),
		SinkConfig:     in.SinkConfig,
		Events:         append([]string(nil), in.Events...),
		Filter:         in.Filter,
		Payload:        in.Payload,
		Security:       SecurityOptions{SignatureMode: in.Security.SignatureMode},
		Retry:          in.Retry,
		Switches:       in.Switches,
		CreatedAt:      s.now(),
		UpdatedAt:      s.now(),
		Version:        1,
	}
	if in.Rule != nil {
		rule, err := s.normalizeEndpointRule(ctx, in.Rule)
		if err != nil {
			return nil, err
		}
		if rule.HasCEL() {
			rule.CurrentVersion = 1 // server-owned: a client-supplied currentVersion is ignored
			endpoint.Rule = rule
		}
	}
	if in.Delivery != nil {
		applyDeliveryUpdate(&endpoint.Delivery, *in.Delivery)
	}
	if in.Enabled != nil {
		endpoint.Enabled = *in.Enabled
	}
	if kind != KindHTTP {
		enc, err := s.encryptSinkConfig(ctx, endpoint, in.SinkConfig, nil)
		if err != nil {
			return nil, err
		}
		endpoint.SinkConfig = enc
	}
	if in.Security.TLS != nil {
		if !s.mtlsEnabled(ctx, caller.OrganizationID) {
			return nil, errs.New(errs.CodeFeatureDisabled, "webhook.mtls is disabled.")
		}
		tlsCfg, err := s.encryptTLS(ctx, endpoint, in.Security.TLS, nil)
		if err != nil {
			return nil, err
		}
		endpoint.Security.TLS = tlsCfg
	}
	s.applyDefaults(ctx, endpoint)
	if err := s.validateEndpoint(ctx, endpoint); err != nil {
		return nil, err
	}
	headers, err := s.encryptHeaders(ctx, endpoint, in.Headers, nil)
	if err != nil {
		return nil, err
	}
	endpoint.Headers = headers
	plain := in.Secret
	if plain == "" {
		plain, err = NewSecret()
		if err != nil {
			return nil, err
		}
	}
	secret, err := s.encryptSecret(ctx, endpoint, plain, s.now(), nil)
	if err != nil {
		return nil, err
	}
	endpoint.Secrets = []StoredSecret{secret}
	if err := s.Repo.insertEndpoint(ctx, endpoint); err != nil {
		return nil, err
	}
	if endpoint.Rule.HasCEL() {
		s.saveEndpointRuleVersion(ctx, endpoint)
	}
	s.audit(ctx, "webhookEndpoint.create", endpoint.ID, audit.Changes{
		"name": {After: endpoint.Name}, "url": {After: endpoint.URL}, "secret": {After: audit.Secret("set")},
	})
	return endpoint, nil
}

// GetEndpoint returns one endpoint within the caller's organisation.
func (s *Service) GetEndpoint(ctx context.Context, id string) (*Endpoint, error) {
	if err := tenancy.RequireReadOn(ctx, "webhooks", id); err != nil {
		return nil, err
	}
	return s.Repo.endpoint(ctx, id)
}

// ListEndpoints returns a tenant-scoped endpoint page.
func (s *Service) ListEndpoints(ctx context.Context, filter EndpointListFilter) (*EndpointList, error) {
	if err := tenancy.RequireRead(ctx, "webhooks"); err != nil {
		return nil, err
	}
	if ids, limited := tenancy.ScopedIDs(ctx, "webhooks"); limited {
		filter.IDs = ids
	}
	rows, next, total, capped, err := s.Repo.listEndpoints(ctx, filter)
	if err != nil {
		return nil, err
	}
	out := &EndpointList{Data: make([]EndpointView, len(rows)), NextCursor: next, Total: total, TotalCapped: capped}
	ids := make([]string, len(rows))
	orgID := ""
	for i := range rows {
		out.Data[i] = endpointView(&rows[i])
		ids[i] = rows[i].ID
		orgID = rows[i].OrganizationID
	}
	if len(ids) > 0 {
		last, err := s.Repo.lastDeliveries(ctx, orgID, ids)
		if err != nil {
			return nil, err
		}
		for i := range out.Data {
			if ld, ok := last[out.Data[i].ID]; ok {
				cp := ld
				out.Data[i].LastDelivery = &cp
			}
		}
	}
	return out, nil
}

// ViewEndpoint builds the API view, including lastDelivery when known.
func (s *Service) ViewEndpoint(ctx context.Context, endpoint *Endpoint) EndpointView {
	view := endpointView(endpoint)
	if endpoint == nil || s.Repo == nil {
		return view
	}
	last, err := s.Repo.lastDeliveries(ctx, endpoint.OrganizationID, []string{endpoint.ID})
	if err != nil {
		return view
	}
	if ld, ok := last[endpoint.ID]; ok {
		cp := ld
		view.LastDelivery = &cp
	}
	return view
}

// UpdateEndpoint applies the endpoint merge patch with optimistic concurrency.
func (s *Service) UpdateEndpoint(ctx context.Context, id string, in EndpointUpdate, ifMatch string) (*Endpoint, error) {
	if err := tenancy.RequireOn(ctx, tenancy.PermWebhooksWrite, id); err != nil {
		return nil, err
	}
	endpoint, err := s.Repo.endpoint(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := checkETag(ifMatch, endpoint.Version); err != nil {
		return nil, err
	}
	before := *endpoint
	oldVersion := endpoint.Version
	if in.Name != nil {
		endpoint.Name = strings.TrimSpace(*in.Name)
	}
	if in.Enabled != nil {
		endpoint.Enabled = *in.Enabled
		if *in.Enabled {
			endpoint.DisabledReason = nil
			endpoint.FailureStartedAt = nil
		}
	}
	if in.URL != nil {
		endpoint.URL = strings.TrimSpace(*in.URL)
	}
	prevSink := endpoint.SinkConfig
	if in.SinkConfig != nil {
		endpoint.SinkConfig = *in.SinkConfig
	}
	if in.Events != nil {
		endpoint.Events = append([]string(nil), (*in.Events)...)
	}
	if in.Filter != nil {
		endpoint.Filter = *in.Filter
	}
	if in.Headers != nil {
		endpoint.Headers, err = s.encryptHeaders(ctx, endpoint, *in.Headers, endpoint.Headers)
		if err != nil {
			return nil, err
		}
	}
	if in.Payload != nil {
		endpoint.Payload = *in.Payload
	}
	if in.Security != nil {
		if in.Security.SignatureMode != "" {
			endpoint.Security.SignatureMode = in.Security.SignatureMode
		}
		if in.Security.TLS != nil {
			if !s.mtlsEnabled(ctx, endpoint.OrganizationID) {
				return nil, errs.New(errs.CodeFeatureDisabled, "webhook.mtls is disabled.")
			}
			tlsCfg, err := s.encryptTLS(ctx, endpoint, in.Security.TLS, endpoint.Security.TLS)
			if err != nil {
				return nil, err
			}
			endpoint.Security.TLS = tlsCfg
		}
	}
	if in.Retry != nil {
		endpoint.Retry = *in.Retry
	}
	if in.Delivery != nil {
		applyDeliveryUpdate(&endpoint.Delivery, *in.Delivery)
	}
	if in.Switches != nil {
		endpoint.Switches = *in.Switches
	}
	ruleChanged := false
	if in.Rule != nil {
		rule, rerr := s.normalizeEndpointRule(ctx, in.Rule)
		if rerr != nil {
			return nil, rerr
		}
		prev := 0
		if endpoint.Rule != nil {
			prev = endpoint.Rule.CurrentVersion
		}
		// currentVersion is server-owned: it moves only when filter/map/builder change.
		if endpoint.Rule == nil || endpoint.Rule.Filter != rule.Filter || endpoint.Rule.Map != rule.Map ||
			!sameBuilder(endpoint.Rule.Builder, rule.Builder) {
			ruleChanged = true
			rule.CurrentVersion = prev + 1
		} else {
			rule.CurrentVersion = prev
		}
		if !rule.HasCEL() {
			// Cleared: drop the rule (versions stay for restore).
			rule = nil
		}
		endpoint.Rule = rule
	}
	if in.Secret != nil {
		if *in.Secret == "" {
			return nil, errs.New(errs.CodeValidationFailed, "A signing secret cannot be empty.")
		}
		secret, err := s.encryptSecret(ctx, endpoint, *in.Secret, s.now(), nil)
		if err != nil {
			return nil, err
		}
		endpoint.Secrets = []StoredSecret{secret}
	}
	if in.SinkConfig != nil && endpoint.Kind != KindHTTP {
		enc, err := s.encryptSinkConfig(ctx, endpoint, *in.SinkConfig, prevSink)
		if err != nil {
			return nil, err
		}
		endpoint.SinkConfig = enc
	}
	s.applyDefaults(ctx, endpoint)
	if err := s.validateEndpoint(ctx, endpoint); err != nil {
		return nil, err
	}
	endpoint.Version++
	endpoint.UpdatedAt = s.now()
	if err := s.Repo.replaceEndpoint(ctx, endpoint, oldVersion); err != nil {
		return nil, err
	}
	if ruleChanged {
		if s.Engine != nil {
			s.Engine.InvalidateRoute("endpoint:" + endpoint.ID)
		}
		after := EndpointRule{}
		if endpoint.Rule != nil {
			s.saveEndpointRuleVersion(ctx, endpoint)
			after = *endpoint.Rule
		}
		beforeRule := EndpointRule{}
		if before.Rule != nil {
			beforeRule = *before.Rule
		}
		s.audit(ctx, "endpoint.rule.update", endpoint.ID, audit.Changes{
			"filter": {Before: beforeRule.Filter, After: after.Filter}, "map": {Before: beforeRule.Map, After: after.Map},
			"version": {Before: beforeRule.CurrentVersion, After: after.CurrentVersion},
		})
	}
	if s.sinkCache != nil {
		s.sinkCache.Evict(endpoint.ID, oldVersion)
	}
	s.audit(ctx, "webhookEndpoint.update", endpoint.ID, audit.Diff(before, *endpoint, "headers", "secrets", "sinkConfig"))
	return endpoint, nil
}

const endpointRuleVersionsCollection = "endpointRuleVersions"

func (s *Service) normalizeEndpointRule(ctx context.Context, in *EndpointRule) (*EndpointRule, error) {
	if in == nil {
		return nil, nil
	}
	out := &EndpointRule{Filter: strings.TrimSpace(in.Filter), Map: strings.TrimSpace(in.Map)}
	if in.Builder != nil {
		out.Builder, _ = plainBSON(in.Builder).(map[string]any) // a restored version holds bson.D values
	}
	if out.Builder != nil && out.Filter == "" {
		b, err := rules.BuilderFromMap(out.Builder)
		if err != nil {
			return nil, err
		}
		cel, err := rules.BuilderToCEL(b)
		if err != nil {
			return nil, err
		}
		out.Filter = cel
	}
	if out.Filter == "" && out.Map == "" && out.Builder == nil {
		return out, nil
	}
	if out.Filter == "" {
		out.Filter = "true"
	}
	if s.Engine == nil {
		return out, nil
	}
	if _, err := s.Engine.Compile(out.Filter, out.Map); err != nil {
		return nil, err
	}
	return out, nil
}

// sameBuilder compares two builder states by content. A builder read back from MongoDB holds nested
// bson.D / bson.A values (key order as stored), so both sides are normalised to plain maps first.
func sameBuilder(a, b map[string]any) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	ja, errA := json.Marshal(plainBSON(a))
	jb, errB := json.Marshal(plainBSON(b))
	return errA == nil && errB == nil && string(ja) == string(jb)
}

// plainBSON converts bson.D / bson.M / bson.A trees to map[string]any / []any (json.Marshal sorts map keys).
func plainBSON(v any) any {
	switch x := v.(type) {
	case bson.D:
		m := make(map[string]any, len(x))
		for _, e := range x {
			m[e.Key] = plainBSON(e.Value)
		}
		return m
	case bson.M:
		return plainBSON(map[string]any(x))
	case map[string]any:
		m := make(map[string]any, len(x))
		for k, e := range x {
			m[k] = plainBSON(e)
		}
		return m
	case bson.A:
		return plainBSON([]any(x))
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = plainBSON(e)
		}
		return out
	}
	return v
}

// saveEndpointRuleVersion appends the endpoint's current rule to endpointRuleVersions. Best effort: the
// endpoint is already saved, so a failure is logged (restore then lacks that row) rather than returned.
func (s *Service) saveEndpointRuleVersion(ctx context.Context, ep *Endpoint) {
	if s.Repo == nil || s.Repo.DB == nil || ep.Rule == nil {
		return
	}
	caller, _ := tenancy.CallerFrom(ctx)
	doc := bson.M{
		"_id":            store.NewID(store.PrefixEndpointRuleVersion),
		"organizationId": ep.OrganizationID, "endpointId": ep.ID,
		"version": ep.Rule.CurrentVersion, "filter": ep.Rule.Filter, "map": ep.Rule.Map,
		"builder": ep.Rule.Builder, "createdBy": caller.Actor(), "createdAt": s.now(),
	}
	if _, err := s.Repo.DB.Collection(endpointRuleVersionsCollection).InsertOne(ctx, doc); err != nil {
		s.log().ErrorContext(ctx, "endpoint rule version insert failed", "endpointId", ep.ID,
			"version", ep.Rule.CurrentVersion, "err", err)
	}
}

// TestEndpointRule evaluates the endpoint CEL rule (W73).
func (s *Service) TestEndpointRule(ctx context.Context, id string, req rules.RouteTestRequest) (*rules.RouteTestResult, error) {
	if err := tenancy.RequireOn(ctx, tenancy.PermWebhooksWrite, id); err != nil {
		return nil, err
	}
	ep, err := s.Repo.endpoint(ctx, id)
	if err != nil {
		return nil, err
	}
	// W73b: a draft (filter / map / builder in the body) is evaluated without a stored rule.
	if !req.IsDraft() && (ep.Rule == nil || !ep.Rule.HasCEL()) {
		return nil, errs.New(errs.CodeValidationFailed, "endpoint has no CEL rule")
	}
	if s.Rules == nil {
		return nil, errs.New(errs.CodeInternalError, "rules engine unavailable")
	}
	if req.IsDraft() {
		return s.Rules.TestProgram(ctx, ep.OrganizationID, "endpoint:"+ep.ID, 0, "", "", req)
	}
	filter := ep.Rule.Filter
	if strings.TrimSpace(filter) == "" {
		filter = "true"
	}
	ver := ep.Rule.CurrentVersion
	if ver <= 0 {
		ver = 1
	}
	return s.Rules.TestProgram(ctx, ep.OrganizationID, "endpoint:"+ep.ID, ver, filter, ep.Rule.Map, req)
}

// EndpointRuleStats returns counters for the endpoint rule.
func (s *Service) EndpointRuleStats(ctx context.Context, id string) (*rules.RuleStats, error) {
	if err := tenancy.RequireReadOn(ctx, "webhooks", id); err != nil {
		return nil, err
	}
	if _, err := s.Repo.endpoint(ctx, id); err != nil {
		return nil, err
	}
	if s.Rules == nil {
		return &rules.RuleStats{}, nil
	}
	return s.Rules.GetStats(ctx, "endpoint", id)
}

// EndpointRuleDecisions returns recent decisions for the endpoint rule.
func (s *Service) EndpointRuleDecisions(ctx context.Context, id string, limit int) (*rules.RuleDecisionList, error) {
	if err := tenancy.RequireReadOn(ctx, "webhooks", id); err != nil {
		return nil, err
	}
	if _, err := s.Repo.endpoint(ctx, id); err != nil {
		return nil, err
	}
	if s.Rules == nil {
		return &rules.RuleDecisionList{}, nil
	}
	return s.Rules.ListDecisions(ctx, "endpoint", id, limit)
}

// DeleteEndpoint deletes an endpoint that no route targets.
func (s *Service) DeleteEndpoint(ctx context.Context, id, ifMatch string) error {
	if err := tenancy.RequireOn(ctx, tenancy.PermWebhooksWrite, id); err != nil {
		return err
	}
	endpoint, err := s.Repo.endpoint(ctx, id)
	if err != nil {
		return err
	}
	if err := checkETag(ifMatch, endpoint.Version); err != nil {
		return err
	}
	if err := s.Repo.deleteEndpoint(ctx, endpoint); err != nil {
		return err
	}
	s.audit(ctx, "webhookEndpoint.delete", endpoint.ID, audit.Changes{"name": {Before: endpoint.Name}})
	return nil
}

// RotateSecret prepends a new secret and expires the previous one after the configured overlap.
func (s *Service) RotateSecret(ctx context.Context, id string) (*RotateSecretResult, error) {
	if err := tenancy.RequireOn(ctx, tenancy.PermWebhooksWrite, id); err != nil {
		return nil, err
	}
	endpoint, err := s.Repo.endpoint(ctx, id)
	if err != nil {
		return nil, err
	}
	plain, err := NewSecret()
	if err != nil {
		return nil, err
	}
	now := s.now()
	overlap := s.rotationOverlap(endpoint)
	var expires *time.Time
	active := make([]StoredSecret, 0, 2)
	if len(endpoint.Secrets) > 0 && overlap > 0 {
		exp := now.Add(overlap)
		expires = &exp
		previous := endpoint.Secrets[0]
		previous.ExpiresAt = expires
		active = append(active, previous)
	}
	secret, err := s.encryptSecret(ctx, endpoint, plain, now, nil)
	if err != nil {
		return nil, err
	}
	endpoint.Secrets = append([]StoredSecret{secret}, active...)
	endpoint.UpdatedAt = now
	oldVersion := endpoint.Version
	endpoint.Version++
	if err := s.Repo.replaceEndpoint(ctx, endpoint, oldVersion); err != nil {
		return nil, err
	}
	s.audit(ctx, "webhookEndpoint.rotateSecret", endpoint.ID, audit.Changes{
		"secret": {Before: audit.Secret("set"), After: audit.Secret("set")},
	})
	return &RotateSecretResult{Secret: plain, SecretPreview: secret.Preview, ExpiresAt: expires, ID: secret.ID}, nil
}

func (s *Service) applyDefaults(ctx context.Context, endpoint *Endpoint) {
	if endpoint.Kind == "" {
		endpoint.Kind = KindHTTP
	}
	if endpoint.Payload.Text == "" {
		endpoint.Payload.Text = "none"
	}
	if endpoint.Payload.TextMax <= 0 {
		endpoint.Payload.TextMax = 64 << 10
	}
	if endpoint.Security.SignatureMode == "" {
		endpoint.Security.SignatureMode = SignatureStandard
	}
	retry := s.retryDefaults(ctx, endpoint.OrganizationID)
	if endpoint.Retry.MaxAttempts <= 0 {
		endpoint.Retry.MaxAttempts = retry.MaxAttempts
	}
	if endpoint.Retry.BackoffBase == "" {
		endpoint.Retry.BackoffBase = retry.BackoffBase
	}
	if endpoint.Retry.BackoffJitter == 0 {
		endpoint.Retry.BackoffJitter = retry.BackoffJitter
	}
	if endpoint.Retry.Timeout == "" {
		endpoint.Retry.Timeout = retry.Timeout
	}
	if endpoint.Delivery.Ordering == "" {
		endpoint.Delivery.Ordering = OrderingStrict
	}
	if endpoint.Delivery.MaxConcurrency <= 0 {
		endpoint.Delivery.MaxConcurrency = 8
		if s.Concurrency > 0 && endpoint.Delivery.MaxConcurrency > s.Concurrency {
			endpoint.Delivery.MaxConcurrency = s.Concurrency
		}
	}
	if endpoint.Delivery.ContentType == "" {
		endpoint.Delivery.ContentType = "json"
	}
	if endpoint.Delivery.AutoDisableAfter == "" {
		value := s.DefaultAutoDisable
		if value <= 0 {
			value = 5 * 24 * time.Hour
		}
		endpoint.Delivery.AutoDisableAfter = value.String()
	}
	if endpoint.Breaker.State == "" {
		endpoint.Breaker.State = "closed"
	}
}

const settingRetryDefaults = "webhook.retryDefaults"

// retryDefaults resolves organisation/global webhook.retryDefaults, then env/module defaults.
func (s *Service) retryDefaults(ctx context.Context, orgID string) RetryOptions {
	out := RetryOptions{
		MaxAttempts: s.DefaultMaxAttempts, BackoffJitter: s.DefaultJitter,
	}
	if out.MaxAttempts <= 0 {
		out.MaxAttempts = 10
	}
	base := s.DefaultBackoffBase
	if base <= 0 {
		base = 5 * time.Second
	}
	out.BackoffBase = base.String()
	if out.BackoffJitter == 0 {
		out.BackoffJitter = 0.2
	}
	timeout := s.DefaultTimeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	out.Timeout = timeout.String()
	if s.Settings == nil || orgID == "" {
		return out
	}
	body, err := s.Settings.Effective(ctx, orgID)
	if err != nil {
		return out
	}
	sv, ok := body.Values[settingRetryDefaults]
	if !ok || sv.Value == nil {
		return out
	}
	parsed, err := settings.ParseValue(settings.KeyMeta{Name: settingRetryDefaults, Kind: settings.KindAny}, sv.Value)
	if err != nil {
		return out
	}
	m, ok := parsed.(map[string]any)
	if !ok {
		return out
	}
	if n, ok := anyInt(m["maxAttempts"]); ok && n > 0 {
		out.MaxAttempts = n
	}
	if s, ok := m["backoffBase"].(string); ok && s != "" {
		out.BackoffBase = s
	}
	if j, ok := anyFloat(m["backoffJitter"]); ok {
		out.BackoffJitter = j
	}
	if t, ok := m["timeout"].(string); ok && t != "" {
		out.Timeout = t
	}
	return out
}

func anyInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int32:
		return int(n), true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	default:
		return 0, false
	}
}

func anyFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	default:
		return 0, false
	}
}

func (s *Service) validateEndpoint(ctx context.Context, endpoint *Endpoint) error {
	if endpoint.Name == "" || len(endpoint.Name) > 128 {
		return errs.New(errs.CodeValidationFailed, "Endpoint name is required and must not exceed 128 characters.")
	}
	switch endpoint.Kind {
	case KindHTTP:
		if len(endpoint.SinkConfig) > 0 {
			return errs.New(errs.CodeValidationFailed, "HTTP endpoints must not set sinkConfig.")
		}
		parsed, err := url.Parse(endpoint.URL)
		if err != nil || parsed.Hostname() == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") ||
			parsed.User != nil || parsed.Fragment != "" {
			return errs.New(errs.CodeValidationFailed, "Endpoint URL must be an absolute HTTP or HTTPS URL without credentials or a fragment.")
		}
		if s.Egress == nil {
			return errors.New("webhook: egress dialer not configured")
		}
		if err := s.Egress.CheckURLHost(ctx, parsed.Hostname(), egress.HTTP); err != nil {
			if errors.Is(err, egress.ErrBlocked) {
				s.securityLog().WarnContext(ctx, "webhook endpoint URL blocked",
					"endpointId", endpoint.ID,
					"host", parsed.Hostname(),
					"code", errs.CodeEndpointURLBlocked,
				)
				return errs.New(errs.CodeEndpointURLBlocked, "The endpoint URL is blocked by the egress policy.")
			}
			return errs.Wrap(err, errs.CodeWebhookHTTPError, "webhook.validate_url")
		}
	case sinks.KindNATS, sinks.KindKafka, sinks.KindSQS, sinks.KindSNS, sinks.KindPubSub:
		if strings.TrimSpace(endpoint.URL) != "" {
			return errs.New(errs.CodeValidationFailed, "Sink endpoints must not set url.")
		}
		if err := s.validateSinkConfig(ctx, endpoint); err != nil {
			return err
		}
	case KindIntegration:
		if strings.TrimSpace(endpoint.URL) != "" {
			return errs.New(errs.CodeValidationFailed, "Integration endpoints must not set url.")
		}
		if endpoint.SinkConfig == nil {
			return errs.New(errs.CodeValidationFailed, "sinkConfig.instanceId is required for integration endpoints.")
		}
		id, _ := endpoint.SinkConfig["instanceId"].(string)
		if !store.IsID(store.PrefixIntegration, strings.TrimSpace(id)) {
			return errs.New(errs.CodeValidationFailed, "sinkConfig.instanceId must be an integration instance id.")
		}
	default:
		return errs.New(errs.CodeValidationFailed, "kind must be http, nats, kafka, sqs, sns, pubsub or integration.")
	}
	if endpoint.Security.SignatureMode != SignatureStandard && endpoint.Security.SignatureMode != SignatureBodyOnly {
		return errs.New(errs.CodeValidationFailed, "signatureMode must be standard or bodyOnly.")
	}
	if endpoint.Security.TLS != nil && endpoint.Kind == KindHTTP && !s.mtlsEnabled(ctx, endpoint.OrganizationID) {
		return errs.New(errs.CodeFeatureDisabled, "webhook.mtls is disabled.")
	}
	if !slices.Contains([]string{"none", "plain", "html", "both"}, endpoint.Payload.Text) ||
		(slices.Contains(endpoint.Payload.Headers, "*") && len(endpoint.Payload.Headers) != 1) {
		return errs.New(errs.CodeValidationFailed, "Webhook payload settings are invalid.")
	}
	if endpoint.Retry.MaxAttempts < 1 || endpoint.Retry.MaxAttempts > 100 ||
		endpoint.Retry.BackoffJitter < 0 || endpoint.Retry.BackoffJitter > 1 ||
		parseDuration(endpoint.Retry.Timeout, 0) <= 0 || parseDuration(endpoint.Retry.BackoffBase, 0) <= 0 {
		return errs.New(errs.CodeValidationFailed, "Webhook retry settings are invalid.")
	}
	if err := s.validateDelivery(endpoint.Delivery); err != nil {
		return err
	}
	if err := validateEvents(endpoint.Events); err != nil {
		return err
	}
	for _, accountID := range endpoint.Filter.AccountIDs {
		if !store.IsID(store.PrefixAccount, accountID) {
			return errs.New(errs.CodeValidationFailed, "filter.accountIds contains an invalid account ID.")
		}
	}
	return nil
}

func validateEvents(types []string) error {
	for _, typ := range types {
		if typ == "*" {
			continue
		}
		entry, ok := events.Lookup(typ)
		if !ok || entry.Synthetic {
			return errs.New(errs.CodeValidationFailed, "Endpoint events contains an unknown event type.").WithSafe("eventType", typ)
		}
	}
	return nil
}

func (s *Service) encryptHeaders(ctx context.Context, endpoint *Endpoint, input []WebhookHeader, previous []StoredHeader) ([]StoredHeader, error) {
	type oldHeader struct {
		value StoredHeader
		index int
	}
	old := map[string]oldHeader{}
	for i, header := range previous {
		old[strings.ToLower(header.Name)] = oldHeader{value: header, index: i}
	}
	out := make([]StoredHeader, 0, len(input))
	seen := map[string]bool{}
	for i, header := range input {
		name := http.CanonicalHeaderKey(strings.TrimSpace(header.Name))
		lower := strings.ToLower(name)
		if name == "" || reservedHeaders[lower] || seen[lower] {
			return nil, errs.New(errs.CodeValidationFailed, "Custom header names must be unique and cannot override delivery headers.")
		}
		seen[lower] = true
		if strings.ContainsAny(header.Value, "\r\n") {
			return nil, errs.New(errs.CodeValidationFailed, "Custom header values cannot contain newlines.")
		}
		if header.Value == "" {
			if stored, ok := old[lower]; ok {
				plain, err := s.Cipher.DecryptString(ctx, stored.value.Value, crypto.AAD{
					Collection: EndpointsCollection,
					Field:      "headers." + strconv.Itoa(stored.index) + ".value",
					DocID:      endpoint.ID,
				})
				if err != nil {
					return nil, err
				}
				value, err := s.Cipher.EncryptString(ctx, endpoint.OrganizationID, plain, crypto.AAD{
					Collection: EndpointsCollection,
					Field:      "headers." + strconv.Itoa(i) + ".value",
					DocID:      endpoint.ID,
				})
				if err != nil {
					return nil, err
				}
				out = append(out, StoredHeader{Name: name, Value: value, Secret: header.Secret})
				continue
			}
			return nil, errs.New(errs.CodeValidationFailed, "A value is required for each custom header.")
		}
		value, err := s.Cipher.EncryptString(ctx, endpoint.OrganizationID, header.Value, crypto.AAD{
			Collection: EndpointsCollection, Field: "headers." + strconv.Itoa(i) + ".value", DocID: endpoint.ID,
		})
		if err != nil {
			return nil, err
		}
		out = append(out, StoredHeader{Name: name, Value: value, Secret: header.Secret})
	}
	return out, nil
}

func (s *Service) encryptSecret(
	ctx context.Context,
	endpoint *Endpoint,
	plain string,
	created time.Time,
	expires *time.Time,
) (StoredSecret, error) {
	if err := validateSecret(plain); err != nil {
		return StoredSecret{}, errs.New(errs.CodeValidationFailed, err.Error())
	}
	id := secretID(plain)
	value, err := s.Cipher.EncryptString(ctx, endpoint.OrganizationID, plain, crypto.AAD{
		Collection: EndpointsCollection, Field: "secrets." + id + ".value", DocID: endpoint.ID,
	})
	if err != nil {
		return StoredSecret{}, err
	}
	return StoredSecret{ID: id, Value: value, Preview: secretPreview(plain), CreatedAt: created, ExpiresAt: expires}, nil
}

func (s *Service) decryptSecrets(ctx context.Context, endpoint *Endpoint) ([]string, error) {
	if s.SecretResolver != nil {
		return s.SecretResolver(ctx, endpoint)
	}
	now := s.now()
	out := make([]string, 0, len(endpoint.Secrets))
	for _, secret := range endpoint.Secrets {
		if secret.ExpiresAt != nil && !now.Before(*secret.ExpiresAt) {
			continue
		}
		plain, err := s.Cipher.DecryptString(ctx, secret.Value, crypto.AAD{
			Collection: EndpointsCollection, Field: "secrets." + secret.ID + ".value", DocID: endpoint.ID,
		})
		if err != nil {
			return nil, err
		}
		out = append(out, plain)
	}
	if len(out) == 0 {
		return nil, errors.New("webhook: endpoint has no active signing secret")
	}
	return out, nil
}

func (s *Service) decryptHeaders(ctx context.Context, endpoint *Endpoint) (map[string]string, error) {
	out := make(map[string]string, len(endpoint.Headers))
	for i, header := range endpoint.Headers {
		plain, err := s.Cipher.DecryptString(ctx, header.Value, crypto.AAD{
			Collection: EndpointsCollection, Field: "headers." + strconv.Itoa(i) + ".value", DocID: endpoint.ID,
		})
		if err != nil {
			return nil, err
		}
		out[header.Name] = plain
	}
	return out, nil
}

func (s *Service) rotationOverlap(endpoint *Endpoint) time.Duration {
	if raw, ok := endpoint.Switches["webhook.secret.rotationOverlap"]; ok {
		switch value := raw.(type) {
		case bool:
			if !value {
				return 0
			}
		case string:
			return parseDuration(value, 24*time.Hour)
		}
	}
	if s.RotationOverlap > 0 {
		return s.RotationOverlap
	}
	return 24 * time.Hour
}

func applyDeliveryUpdate(dst *DeliveryOptions, patch DeliveryUpdate) {
	if patch.Ordering != nil {
		dst.Ordering = *patch.Ordering
	}
	if patch.MaxConcurrency != nil {
		dst.MaxConcurrency = *patch.MaxConcurrency
	}
	if patch.ContentType != nil {
		dst.ContentType = *patch.ContentType
	}
	if patch.RetryOn4xx != nil {
		dst.RetryOn4xx = *patch.RetryOn4xx
	}
	if patch.AutoDisableAfter != nil {
		dst.AutoDisableAfter = *patch.AutoDisableAfter
	}
}

func (s *Service) validateDelivery(d DeliveryOptions) error {
	if d.Ordering != OrderingStrict && d.Ordering != OrderingBestEffort {
		return errs.New(errs.CodeValidationFailed, "ordering must be strict or bestEffort.")
	}
	if d.ContentType != "json" && d.ContentType != "cloudevents" {
		return errs.New(errs.CodeValidationFailed, "contentType must be json or cloudevents.")
	}
	if d.MaxConcurrency < 1 || d.MaxConcurrency > 64 {
		return errs.New(errs.CodeValidationFailed, "maxConcurrency must be between 1 and 64.")
	}
	if s.Concurrency > 0 && d.MaxConcurrency > s.Concurrency {
		return errs.New(errs.CodeValidationFailed, "maxConcurrency must not exceed the node webhook concurrency.")
	}
	auto := parseDuration(d.AutoDisableAfter, 0)
	if auto < time.Hour || auto > 30*24*time.Hour {
		return errs.New(errs.CodeValidationFailed, "autoDisableAfter must be between 1h and 30d.")
	}
	return nil
}

func endpointView(endpoint *Endpoint) EndpointView {
	headers := make([]WebhookHeader, len(endpoint.Headers))
	for i, header := range endpoint.Headers {
		headers[i] = WebhookHeader{Name: header.Name, Secret: header.Secret, HasValue: !header.Value.IsZero()}
	}
	preview := ""
	if len(endpoint.Secrets) > 0 {
		preview = endpoint.Secrets[0].Preview
	}
	sec := SecurityView{SignatureMode: endpoint.Security.SignatureMode, TLS: tlsView(endpoint.Security.TLS)}
	return EndpointView{
		ID: endpoint.ID, OrganizationID: endpoint.OrganizationID, Name: endpoint.Name, Kind: endpoint.Kind,
		Enabled: endpoint.Enabled, URL: endpoint.URL, SinkConfig: sinkConfigView(endpoint.Kind, endpoint.SinkConfig),
		Events: append([]string(nil), endpoint.Events...), Filter: endpoint.Filter, Headers: headers,
		Payload: endpoint.Payload, SecretPreview: preview, Security: sec, Retry: endpoint.Retry,
		Delivery: endpoint.Delivery, DisabledReason: endpoint.DisabledReason,
		Breaker: endpoint.Breaker, Switches: endpoint.Switches, Rule: endpoint.Rule,
		CreatedAt: endpoint.CreatedAt, UpdatedAt: endpoint.UpdatedAt, Version: endpoint.Version,
	}
}

func (s *Service) audit(ctx context.Context, action, id string, changes audit.Changes) {
	if s.Audit == nil {
		return
	}
	if err := s.Audit.Record(ctx, action, audit.Target{Type: "webhookEndpoint", ID: id}, changes); err != nil {
		s.log().WarnContext(ctx, "webhook audit write failed", "action", action, "endpointId", id, "err", err)
	}
}

func checkETag(value string, version int64) error {
	if value == "" {
		return nil
	}
	value = strings.Trim(value, `"`)
	value = strings.TrimPrefix(value, "v")
	got, err := strconv.ParseInt(value, 10, 64)
	if err != nil || got != version {
		return errs.New(errs.CodeVersionMismatch, "The resource version does not match If-Match.")
	}
	return nil
}

func formatETag(version int64) string { return fmt.Sprintf(`"v%d"`, version) }

// checkEndpointQuota enforces EB_QUOTA_ENDPOINTS and the organisation's endpoints quota.
func (s *Service) checkEndpointQuota(ctx context.Context, org string) error {
	oq, err := tenancy.LoadOrgQuotas(ctx, s.Repo.DB, org)
	if err != nil {
		return err
	}
	limit, fromOrg := tenancy.EffectiveLimit(s.QuotaEndpoints, oq.Endpoints)
	if limit <= 0 {
		return nil
	}
	n, err := s.Repo.DB.Collection(EndpointsCollection).CountDocuments(ctx, bson.M{"organizationId": org})
	if err != nil {
		return errs.Wrap(err, errs.CodeInternalError, "webhook.quota")
	}
	if int(n) < limit {
		return nil
	}
	if fromOrg {
		return tenancy.OrgQuotaExceeded(tenancy.QuotaEndpoints, limit).WithoutRetryAfter()
	}
	return errs.New(errs.CodeQuotaEndpointsExceeded, "The organisation webhook endpoint quota is reached.").
		WithSafe("limit", limit).WithoutRetryAfter()
}

// EndpointExists is used by internal/rules without a webhook→rules import cycle in wiring.
func (s *Service) EndpointExists(ctx context.Context, orgID, id string) (bool, error) {
	n, err := s.Repo.DB.Collection(EndpointsCollection).CountDocuments(ctx, map[string]any{
		"_id": id, "organizationId": orgID,
	})
	return n > 0, err
}

func isFinal(state string) bool { return slices.Contains(finalStates, state) }

var _ Publisher = (*bus.Bus)(nil)
