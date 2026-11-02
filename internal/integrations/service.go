package integrations

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/radiobros/emailbridge/internal/audit"
	"github.com/radiobros/emailbridge/internal/crypto"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/integrations/connectors"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

// SentryChangeFunc is called when a Sentry instance is installed, updated, or removed (W37).
// enabled=false means ClearConnector; otherwise ApplyConnector with the given DSN settings.
type SentryChangeFunc func(ctx context.Context, enabled bool, dsn, environment string, sampleRate float64)

// Service is the integrations façade.
type Service struct {
	Repo        *Repo
	Registry    *Registry
	Cipher      *crypto.Cipher
	Audit       *audit.Writer
	Endpoints   EndpointResources // optional
	Routes      RouteResources    // optional
	Settings    SettingsResources // optional
	Gateways    GatewayResources  // optional; sending providers (W52)
	Attachments AttachmentReader  // optional; storage uploads (W52)
	Submit      OutboundSubmitter // optional; FeatherDesk inbound
	HTTP        *http.Client
	DB          *store.DB // optional; chat digests read the message index
	// OnSentryChange is wired from cmd to observability.Reporter (W37 HOTSPOT).
	OnSentryChange SentryChangeFunc
	limiter        *callbackLimiter
	rspamd         rspamdCache
}

func (s *Service) reg() *Registry {
	if s.Registry != nil {
		return s.Registry
	}
	return DefaultRegistry()
}

func (s *Service) client() *http.Client {
	if s.HTTP != nil {
		return s.HTTP
	}
	return &http.Client{Timeout: 15 * time.Second}
}

// Catalogue returns connector definitions with install state.
func (s *Service) Catalogue(ctx context.Context, f CatalogueFilter) (*Catalogue, error) {
	if err := tenancy.RequireRead(ctx, "integrations"); err != nil {
		return nil, err
	}
	insts, err := s.Repo.listByType(ctx, "")
	if err != nil {
		return nil, err
	}
	byType := map[string][]string{}
	for i := range insts {
		byType[insts[i].Type] = append(byType[insts[i].Type], insts[i].ID)
	}
	q := strings.ToLower(strings.TrimSpace(f.Q))
	cat := strings.TrimSpace(f.Category)
	out := &Catalogue{Data: []CatalogueItem{}}
	for _, d := range s.reg().All() {
		if cat != "" && d.Category != cat {
			continue
		}
		if q != "" {
			hay := strings.ToLower(d.Name + " " + d.Description + " " + d.ID)
			if !strings.Contains(hay, q) {
				continue
			}
		}
		ids := byType[d.ID]
		item := CatalogueItem{
			ID: d.ID, Version: d.Version, Name: d.Name, Category: d.Category,
			Icon: d.Icon, Color: d.Color, Description: d.Description, DocsPath: d.DocsPath,
			ConfigSchema: d.ConfigSchema, SecretFields: append([]string(nil), d.SecretFields...),
			Capabilities:   append([]string(nil), d.Capabilities...),
			InstalledCount: len(ids), InstanceIDs: ids,
		}
		if d.Category == CategorySending {
			item.RuntimeHint = "Creates a sending gateway. Manage send policy under Sending → Gateways."
		}
		out.Data = append(out.Data, item)
	}
	return out, nil
}

// List pages instances.
func (s *Service) List(ctx context.Context, f ListFilter) (*List, error) {
	if err := tenancy.RequireRead(ctx, "integrations"); err != nil {
		return nil, err
	}
	res, err := s.Repo.list(ctx, f)
	if err != nil {
		return nil, err
	}
	out := &List{Data: []View{}, NextCursor: res.NextCursor, Total: res.Total, TotalCapped: res.TotalCapped}
	for i := range res.Rows {
		out.Data = append(out.Data, s.view(&res.Rows[i]))
	}
	return out, nil
}

// Get returns one instance.
func (s *Service) Get(ctx context.Context, id string) (*View, error) {
	if err := tenancy.RequireRead(ctx, "integrations"); err != nil {
		return nil, err
	}
	if !store.IsID(store.PrefixIntegration, id) {
		return nil, errs.New(errs.CodeNotFound, "The integration instance does not exist.")
	}
	inst, err := s.Repo.get(ctx, id)
	if err != nil {
		return nil, err
	}
	v := s.view(inst)
	return &v, nil
}

// Install creates an instance and its resources.
func (s *Service) Install(ctx context.Context, typ string, in InstallInput) (*View, *TestResult, error) {
	if err := tenancy.Require(ctx, tenancy.PermSettingsWrite); err != nil {
		return nil, nil, err
	}
	def, ok := s.reg().Get(typ)
	if !ok {
		return nil, nil, errs.New(errs.CodeNotFound, "Unknown integration type.").WithSafe("type", typ)
	}
	if def.InstanceOwnerOnly {
		c, cok := tenancy.CallerFrom(ctx)
		owner := cok && c.InstanceOwner
		if err := connectors.GuardInstall(connectors.Def{
			ID: def.ID, Name: def.Name, InstanceOwnerOnly: def.InstanceOwnerOnly,
		}, owner); err != nil {
			return nil, nil, err
		}
	}
	org, ok := store.OrganizationFrom(ctx)
	if !ok {
		return nil, nil, store.ErrNoTenant
	}
	cfg, secrets := splitConfigSecrets(def, cloneMap(in.Config), cloneStrMap(in.Secrets))
	applySchemaDefaults(def, cfg)
	if err := validateConfig(def.ConfigSchema, cfg, secrets, def.SecretFields); err != nil {
		return nil, nil, err
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = def.Name
	}
	if len(name) > 120 {
		return nil, nil, errs.New(errs.CodeValidationFailed, "name must be at most 120 characters.")
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	inst := &Instance{
		ID: store.NewID(store.PrefixIntegration), OrganizationID: org, Type: def.ID,
		DefinitionVersion: def.Version, Name: name, Config: cfg, Secrets: map[string]crypto.EncryptedString{},
		Enabled: true, CreatedAt: now, UpdatedAt: now, Version: 1,
	}
	if c, ok := tenancy.CallerFrom(ctx); ok {
		inst.CreatedBy = c.UserID
		if c.Kind == tenancy.CallerAPIKey {
			inst.CreatedBy = c.APIKeyID
		}
	}
	for _, f := range def.SecretFields {
		plain := strings.TrimSpace(secrets[f])
		if plain == "" {
			continue
		}
		es, err := s.Cipher.EncryptString(ctx, org, plain, crypto.AAD{
			Collection: Collection, Field: "secrets." + f, DocID: inst.ID,
		})
		if err != nil {
			return nil, nil, errs.Wrap(err, errs.CodeInternalError, "integrations.encrypt")
		}
		inst.Secrets[f] = es
	}
	created, err := s.createResources(ctx, def, inst.ID, cfg, secrets)
	if err != nil {
		return nil, nil, err
	}
	inst.CreatedResources = created
	if err := s.Repo.insert(ctx, inst); err != nil {
		_ = s.rollbackResources(ctx, created)
		return nil, nil, err
	}
	if s.Settings != nil {
		_ = s.Settings.SetSwitch(ctx, SwitchName(def.ID), true)
	}
	s.audit(ctx, "integration.installed", inst.ID, audit.Changes{
		"type": {After: def.ID}, "name": {After: name},
	})
	s.notifySentry(ctx, def.ID, cfg, secrets, inst.Enabled)
	var testRes *TestResult
	if in.RunTest {
		tr, err := s.runTest(ctx, def, cfg, secrets)
		if err != nil {
			return nil, nil, err
		}
		testRes = &tr
		s.audit(ctx, "integration.tested", inst.ID, audit.Changes{"ok": {After: tr.OK}})
	}
	v := s.view(inst)
	return &v, testRes, nil
}

// Patch updates an instance.
func (s *Service) Patch(ctx context.Context, id, ifMatch string, in PatchInput) (*View, error) {
	if err := tenancy.Require(ctx, tenancy.PermSettingsWrite); err != nil {
		return nil, err
	}
	inst, err := s.Repo.get(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := checkIfMatch(ifMatch, inst.Version); err != nil {
		return nil, err
	}
	def, ok := s.reg().Get(inst.Type)
	if !ok {
		return nil, errs.New(errs.CodeNotFound, "Unknown integration type.").WithSafe("type", inst.Type)
	}
	old := *inst
	if in.Name != nil {
		n := strings.TrimSpace(*in.Name)
		if n == "" || len(n) > 120 {
			return nil, errs.New(errs.CodeValidationFailed, "name is invalid.")
		}
		inst.Name = n
	}
	secretsPlain, err := s.decryptSecrets(ctx, inst, def)
	if err != nil {
		return nil, err
	}
	if in.Config != nil || in.Secrets != nil {
		cfg := cloneMap(inst.Config)
		for k, v := range in.Config {
			cfg[k] = v
		}
		sec := cloneStrMap(secretsPlain)
		for k, v := range in.Secrets {
			if strings.TrimSpace(v) != "" {
				sec[k] = v
			}
		}
		cfg, sec = splitConfigSecrets(def, cfg, sec)
		applySchemaDefaults(def, cfg)
		if err := validateConfig(def.ConfigSchema, cfg, sec, def.SecretFields); err != nil {
			return nil, err
		}
		inst.Config = cfg
		for _, f := range def.SecretFields {
			plain := strings.TrimSpace(sec[f])
			if plain == "" {
				continue
			}
			es, err := s.Cipher.EncryptString(ctx, inst.OrganizationID, plain, crypto.AAD{
				Collection: Collection, Field: "secrets." + f, DocID: inst.ID,
			})
			if err != nil {
				return nil, errs.Wrap(err, errs.CodeInternalError, "integrations.encrypt")
			}
			if inst.Secrets == nil {
				inst.Secrets = map[string]crypto.EncryptedString{}
			}
			inst.Secrets[f] = es
			secretsPlain[f] = plain
		}
		if err := s.reapplyOwned(ctx, def, inst, secretsPlain); err != nil {
			return nil, err
		}
	}
	if in.Enabled != nil && *in.Enabled != inst.Enabled {
		inst.Enabled = *in.Enabled
		if err := s.setResourcesEnabled(ctx, inst, inst.Enabled); err != nil {
			return nil, err
		}
		if s.Settings != nil {
			_ = s.Settings.SetSwitch(ctx, SwitchName(def.ID), inst.Enabled)
		}
	}
	inst.UpdatedAt = time.Now().UTC().Truncate(time.Millisecond)
	inst.Version++
	if err := s.Repo.replace(ctx, inst, old.Version); err != nil {
		return nil, err
	}
	s.audit(ctx, "integration.updated", inst.ID, audit.Changes{
		"name":    {Before: old.Name, After: inst.Name},
		"enabled": {Before: old.Enabled, After: inst.Enabled},
	})
	s.notifySentry(ctx, inst.Type, inst.Config, secretsPlain, inst.Enabled)
	v := s.view(inst)
	return &v, nil
}

// Uninstall deletes an instance and its owned resources.
func (s *Service) Uninstall(ctx context.Context, id, ifMatch string) error {
	if err := tenancy.Require(ctx, tenancy.PermSettingsWrite); err != nil {
		return err
	}
	inst, err := s.Repo.get(ctx, id)
	if err != nil {
		return err
	}
	if err := checkIfMatch(ifMatch, inst.Version); err != nil {
		return err
	}
	typ := inst.Type
	if err := s.deleteResources(ctx, inst.CreatedResources); err != nil {
		return err
	}
	if err := s.Repo.delete(ctx, inst); err != nil {
		return err
	}
	if s.Settings != nil {
		_ = s.Settings.SetSwitch(ctx, SwitchName(inst.Type), false)
	}
	s.audit(ctx, "integration.uninstalled", id, audit.Changes{
		"type": {Before: inst.Type}, "name": {Before: inst.Name},
	})
	s.notifySentry(ctx, typ, nil, nil, false)
	return nil
}

// Test runs the connector test hook.
func (s *Service) Test(ctx context.Context, id string) (*TestResult, error) {
	if err := tenancy.Require(ctx, tenancy.PermSettingsWrite); err != nil {
		return nil, err
	}
	inst, err := s.Repo.get(ctx, id)
	if err != nil {
		return nil, err
	}
	def, ok := s.reg().Get(inst.Type)
	if !ok {
		return nil, errs.New(errs.CodeNotFound, "Unknown integration type.").WithSafe("type", inst.Type)
	}
	secrets, err := s.decryptSecrets(ctx, inst, def)
	if err != nil {
		return nil, err
	}
	tr, err := s.runTest(ctx, def, inst.Config, secrets)
	if err != nil {
		return nil, err
	}
	s.audit(ctx, "integration.tested", inst.ID, audit.Changes{"ok": {After: tr.OK}})
	return &tr, nil
}

func (s *Service) runTest(ctx context.Context, def *Definition, cfg map[string]any, secrets map[string]string) (TestResult, error) {
	if def.Test == nil {
		return TestResult{OK: true, Detail: "no test hook"}, nil
	}
	return def.Test(ctx, TestInput{Config: cfg, Secrets: secrets, Client: s.client()})
}

func (s *Service) createResources(ctx context.Context, def *Definition, instanceID string, cfg map[string]any, secrets map[string]string) (CreatedResources, error) {
	out := CreatedResources{}
	cfgWithInst := cloneMap(cfg)
	cfgWithInst["__instanceId"] = instanceID
	var endpointIDs []string
	for _, et := range def.Resources.WebhookEndpoints {
		if s.Endpoints == nil {
			continue
		}
		name := resolveTemplate(et.Name, cfgWithInst, secrets)
		if strings.TrimSpace(name) == "" {
			name = def.Name
		}
		url := resolveTemplate(et.URL, cfgWithInst, secrets)
		sink := map[string]any{}
		for k, tmpl := range et.SinkConfig {
			v := resolveTemplate(tmpl, cfgWithInst, secrets)
			v = strings.ReplaceAll(v, "{{instance.id}}", instanceID)
			sink[k] = v
		}
		created, err := s.Endpoints.Create(ctx, EndpointSpec{
			Name: name, URL: url, Kind: et.Kind, Events: et.Events, SinkConfig: sink,
			PayloadText: et.PayloadText, PayloadAttachments: et.PayloadAttachments,
		})
		if err != nil {
			_ = s.rollbackResources(ctx, out)
			return CreatedResources{}, err
		}
		endpointIDs = append(endpointIDs, created.ID)
		out.Endpoints = append(out.Endpoints, created.ID)
	}
	for _, rt := range def.Resources.Routes {
		if s.Routes == nil {
			continue
		}
		if rt.EndpointIndex < 0 || rt.EndpointIndex >= len(endpointIDs) {
			_ = s.rollbackResources(ctx, out)
			return CreatedResources{}, errs.New(errs.CodeInternalError, "route endpoint index out of range")
		}
		filter := resolveTemplate(rt.Filter, cfgWithInst, secrets)
		if strings.TrimSpace(filter) == "" {
			filter = "true"
		}
		mapExpr := resolveTemplate(rt.Map, cfgWithInst, secrets)
		created, err := s.Routes.Create(ctx, RouteSpec{
			Name: resolveTemplate(rt.Name, cfgWithInst, secrets), EndpointID: endpointIDs[rt.EndpointIndex],
			Filter: filter, Map: mapExpr,
		})
		if err != nil {
			_ = s.rollbackResources(ctx, out)
			return CreatedResources{}, err
		}
		out.Routes = append(out.Routes, created.ID)
	}
	if len(def.Resources.Settings) > 0 && s.Settings != nil {
		vals := map[string]string{}
		for k, tmpl := range def.Resources.Settings {
			v := resolveTemplate(tmpl, cfgWithInst, secrets)
			if strings.TrimSpace(v) == "" && strings.Contains(tmpl, "{{config.timeout}}") {
				v = "5s"
			}
			vals[k] = v
			out.Settings = append(out.Settings, k)
		}
		if err := s.Settings.Apply(ctx, vals); err != nil {
			_ = s.rollbackResources(ctx, out)
			return CreatedResources{}, err
		}
	}
	if def.Category == CategorySending && s.Gateways != nil {
		gw, err := s.Gateways.Create(ctx, gatewaySpecFrom(def, cfg, secrets))
		if err != nil {
			_ = s.rollbackResources(ctx, out)
			return CreatedResources{}, err
		}
		out.Gateways = append(out.Gateways, gw.ID)
	}
	return out, nil
}

func gatewaySpecFrom(def *Definition, cfg map[string]any, secrets map[string]string) GatewaySpec {
	spec := GatewaySpec{
		Name: def.Name, Kind: def.ID,
		Region: strAny(cfg, "region"), Domain: strAny(cfg, "domain"),
		BaseURL: strAny(cfg, "baseUrl"), From: strAny(cfg, "from"),
		AccessKey: strAny(cfg, "accessKey"), SecretKey: secrets["secretKey"],
	}
	if secrets["serverToken"] != "" {
		spec.APIKey = secrets["serverToken"]
	}
	if secrets["apiKey"] != "" {
		spec.APIKey = secrets["apiKey"]
	}
	return spec
}

func strAny(cfg map[string]any, key string) string {
	if cfg == nil {
		return ""
	}
	v, _ := cfg[key].(string)
	return strings.TrimSpace(v)
}

func (s *Service) reapplyOwned(ctx context.Context, def *Definition, inst *Instance, secrets map[string]string) error {
	cfg := inst.Config
	if len(def.Resources.WebhookEndpoints) > 0 && s.Endpoints != nil && len(inst.CreatedResources.Endpoints) > 0 {
		url := resolveTemplate(def.Resources.WebhookEndpoints[0].URL, cfg, secrets)
		if strings.TrimSpace(url) != "" {
			if _, err := s.Endpoints.UpdateURL(ctx, inst.CreatedResources.Endpoints[0], url, 0); err != nil {
				return err
			}
		}
	}
	if len(def.Resources.Settings) > 0 && s.Settings != nil {
		vals := map[string]string{}
		for k, tmpl := range def.Resources.Settings {
			v := resolveTemplate(tmpl, cfg, secrets)
			if strings.TrimSpace(v) == "" && strings.Contains(tmpl, "timeout") {
				v = "5s"
			}
			vals[k] = v
		}
		if err := s.Settings.Apply(ctx, vals); err != nil {
			return err
		}
	}
	if def.Category == CategorySending && s.Gateways != nil && len(inst.CreatedResources.Gateways) > 0 {
		return s.Gateways.Update(ctx, inst.CreatedResources.Gateways[0], gatewaySpecFrom(def, cfg, secrets))
	}
	return nil
}

func (s *Service) setResourcesEnabled(ctx context.Context, inst *Instance, enabled bool) error {
	if s.Endpoints != nil {
		for _, id := range inst.CreatedResources.Endpoints {
			if _, err := s.Endpoints.SetEnabled(ctx, id, enabled, 0); err != nil {
				return err
			}
		}
	}
	if s.Routes != nil {
		for _, id := range inst.CreatedResources.Routes {
			if _, err := s.Routes.SetEnabled(ctx, id, enabled, 0); err != nil {
				return err
			}
		}
	}
	if s.Gateways != nil {
		for _, id := range inst.CreatedResources.Gateways {
			if err := s.Gateways.SetEnabled(ctx, id, enabled); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Service) deleteResources(ctx context.Context, cr CreatedResources) error {
	if s.Routes != nil {
		for _, id := range cr.Routes {
			_ = s.Routes.Delete(ctx, id, 0)
		}
	}
	if s.Endpoints != nil {
		for _, id := range cr.Endpoints {
			_ = s.Endpoints.Delete(ctx, id, 0)
		}
	}
	if s.Gateways != nil {
		for _, id := range cr.Gateways {
			_ = s.Gateways.Delete(ctx, id)
		}
	}
	if s.Settings != nil && len(cr.Settings) > 0 {
		_ = s.Settings.Clear(ctx, cr.Settings)
	}
	return nil
}

func (s *Service) rollbackResources(ctx context.Context, cr CreatedResources) error {
	return s.deleteResources(ctx, cr)
}

func (s *Service) decryptSecrets(ctx context.Context, inst *Instance, def *Definition) (map[string]string, error) {
	out := map[string]string{}
	for _, f := range def.SecretFields {
		es, ok := inst.Secrets[f]
		if !ok || es.IsZero() {
			continue
		}
		plain, err := s.Cipher.DecryptString(ctx, es, crypto.AAD{
			Collection: Collection, Field: "secrets." + f, DocID: inst.ID,
		})
		if err != nil {
			return nil, errs.Wrap(err, errs.CodeInternalError, "integrations.decrypt")
		}
		out[f] = plain
	}
	return out, nil
}

func (s *Service) view(inst *Instance) View {
	def, _ := s.reg().Get(inst.Type)
	var fields []string
	if def != nil {
		fields = def.SecretFields
	}
	return viewOf(inst, fields)
}

func (s *Service) audit(ctx context.Context, action, id string, changes audit.Changes) {
	if s.Audit == nil {
		return
	}
	_ = s.Audit.Record(ctx, action, audit.Target{Type: "integration", ID: id}, changes)
}

func checkIfMatch(ifMatch string, version int64) error {
	if ifMatch == "" {
		return nil
	}
	value := strings.TrimPrefix(strings.Trim(strings.TrimSpace(ifMatch), `"`), "v")
	got, err := strconv.ParseInt(value, 10, 64)
	if err != nil || got != version {
		return errs.New(errs.CodeVersionMismatch, "The resource version does not match If-Match.")
	}
	return nil
}

func formatETag(v int64) string { return fmt.Sprintf(`"v%d"`, v) }

func applySchemaDefaults(def *Definition, cfg map[string]any) {
	props, _ := def.ConfigSchema["properties"].(map[string]any)
	if props == nil {
		return
	}
	for name, raw := range props {
		ps, _ := raw.(map[string]any)
		if ps == nil {
			continue
		}
		if _, ok := cfg[name]; ok {
			continue
		}
		if defVal, ok := ps["default"]; ok {
			cfg[name] = defVal
		}
	}
}

func cloneMap(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func cloneStrMap(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
