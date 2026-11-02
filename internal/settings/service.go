package settings

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/radiobros/emailbridge/internal/config"
	"github.com/radiobros/emailbridge/internal/crypto"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/features"
	"github.com/radiobros/emailbridge/internal/i18n"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

// Notifier is called after a settings change (wired to NATS eb.settings.changed by the binary).
// scope is the same string Invalidate understands: "global" for instance-wide changes, or
// "org:<orgId>" for an organisation document (never the bare word "organization").
type Notifier func(ctx context.Context, scope string, keys []string)

// Service reads and patches runtime settings, feature switches and user settings.
type Service struct {
	DB       *store.DB
	Cipher   *crypto.Cipher
	Registry *features.Registry
	Boot     *config.Config
	// EnvSources maps EB_* keys to config.Source (from Load report); optional.
	EnvSources map[string]config.Source
	Notify     Notifier
	EnvName    string // EB_ENV for switch defaults

	catalog map[string]KeyMeta

	mu    sync.RWMutex
	cache map[string]*cachedEffective // key: orgID or ""
}

type cachedEffective struct {
	body SettingsBody
	at   time.Time
}

// NewService builds a Service.
func NewService(db *store.DB, cipher *crypto.Cipher, reg *features.Registry, boot *config.Config) *Service {
	if reg == nil {
		reg = features.DefaultRegistry()
	}
	env := "production"
	if boot != nil {
		env = boot.Process.Env
	}
	return &Service{
		DB: db, Cipher: cipher, Registry: reg, Boot: boot,
		EnvName: env, catalog: Catalog(), cache: map[string]*cachedEffective{},
	}
}

// Invalidate drops cached effective settings for scope ("global" or "org:<id>" or org id).
func (s *Service) Invalidate(scope string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case scope == "" || scope == DocIDGlobal || scope == "global":
		delete(s.cache, "")
		// global affects every org
		for k := range s.cache {
			delete(s.cache, k)
		}
	case len(scope) > 4 && scope[:4] == "org:":
		delete(s.cache, scope[4:])
	default:
		delete(s.cache, scope)
	}
}

// Effective returns resolved runtime settings for orgID ("" = global-only view).
func (s *Service) Effective(ctx context.Context, orgID string) (SettingsBody, error) {
	s.mu.RLock()
	if c, ok := s.cache[orgID]; ok && time.Since(c.at) < 30*time.Second {
		body := c.body
		s.mu.RUnlock()
		return body, nil
	}
	s.mu.RUnlock()

	global, err := s.loadDoc(ctx, DocIDGlobal)
	if err != nil {
		return SettingsBody{}, err
	}
	var org *Doc
	if orgID != "" {
		org, err = s.loadDoc(ctx, OrgDocID(orgID))
		if err != nil {
			return SettingsBody{}, err
		}
	}
	body := s.resolve(global, org)
	s.mu.Lock()
	s.cache[orgID] = &cachedEffective{body: body, at: time.Now()}
	s.mu.Unlock()
	return body, nil
}

func (s *Service) loadDoc(ctx context.Context, id string) (*Doc, error) {
	var d Doc
	err := s.DB.Collection(SettingsCollection).FindOne(ctx, bson.M{"_id": id}).Decode(&d)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return &Doc{ID: id, Values: map[string]any{}, Switches: map[string]SwitchValue{}, Env: map[string]any{}, Version: 0}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("settings: load %s: %w", id, err)
	}
	if d.Values == nil {
		d.Values = map[string]any{}
	} else {
		d.Values = normalizeMap(d.Values)
	}
	if d.Switches == nil {
		d.Switches = map[string]SwitchValue{}
	}
	if d.Env == nil {
		d.Env = map[string]any{}
	} else {
		d.Env = normalizeMap(d.Env)
	}
	return &d, nil
}

// effectiveVersion is the version (and ETag) of the effective settings of an organisation view: the sum of
// the global and organisation document versions. Both only grow, so any write to either document changes it
// and a stale If-Match can never match again (max() could not see a write to the lower document).
func effectiveVersion(global, org *Doc) int64 {
	v := global.Version
	if org != nil {
		v += org.Version
	}
	return v
}

func (s *Service) resolve(global, org *Doc) SettingsBody {
	body := SettingsBody{
		Values:   map[string]SettingValue{},
		Switches: map[string]SettingValue{},
		Version:  effectiveVersion(global, org),
	}
	for name, meta := range s.catalog {
		sv := SettingValue{Scope: changeScope(meta)}
		// precedence: organization → global → env → default
		if org != nil {
			if v, ok := org.Values[name]; ok {
				sv.Value, sv.Source = s.displayValue(meta, v), "organization"
				body.Values[name] = sv
				continue
			}
		}
		if v, ok := global.Values[name]; ok {
			sv.Value, sv.Source = s.displayValue(meta, v), "global"
			body.Values[name] = sv
			continue
		}
		if meta.EnvKey != "" && s.Boot != nil {
			src := "default"
			if s.EnvSources != nil {
				if es := s.EnvSources[meta.EnvKey]; es == config.SourceEnv || es == config.SourceFile {
					src = "env"
				}
			}
			sv.Value, sv.Source = DisplayBootValue(meta, s.Boot), src
			body.Values[name] = sv
			continue
		}
		if meta.Default != "" {
			v, _ := ParseValue(meta, meta.Default)
			sv.Value, sv.Source = v, "default"
		} else {
			sv.Value, sv.Source = nil, "default"
		}
		body.Values[name] = sv
	}

	for _, sw := range s.Registry.All() {
		vals := features.Values{}
		if g, ok := global.Switches[sw.Name]; ok {
			vals.Global = &features.Setting{Value: toFeatureValue(sw, g.Value), Locked: g.Locked}
		}
		if org != nil {
			if o, ok := org.Switches[sw.Name]; ok {
				vals.Organization = &features.Setting{Value: toFeatureValue(sw, o.Value), Locked: o.Locked}
			}
		}
		eff, src := features.Resolve(sw, s.EnvName, vals)
		locked := (vals.Global != nil && vals.Global.Locked) ||
			(vals.Organization != nil && vals.Organization.Locked)
		body.Switches[sw.Name] = SettingValue{
			Value:  featureDisplay(eff),
			Source: featureSource(src),
			Scope:  switchChangeScope(sw),
			Locked: locked,
		}
	}
	return body
}

func changeScope(m KeyMeta) string {
	switch m.Scope {
	case "global":
		return "global"
	case "organization":
		return "organization"
	default:
		return "organization"
	}
}

func switchChangeScope(sw features.Switch) string {
	for _, sc := range sw.Scopes {
		if sc == features.ScopeOrganization {
			return "organization"
		}
	}
	return "global"
}

func toFeatureValue(sw features.Switch, raw any) features.Value {
	if sw.Default.IsString() {
		s, _ := raw.(string)
		if s == "" {
			if fs, ok := raw.(float64); ok {
				_ = fs
			}
		}
		switch v := raw.(type) {
		case string:
			return features.StringValue(v)
		default:
			return features.StringValue(fmt.Sprint(raw))
		}
	}
	switch v := raw.(type) {
	case bool:
		return features.BoolValue(v)
	default:
		return features.BoolValue(false)
	}
}

func featureDisplay(v features.Value) any {
	if v.Bool != nil {
		return *v.Bool
	}
	if v.String != nil {
		return *v.String
	}
	return nil
}

func featureSource(s features.Source) string {
	switch s {
	case features.SourceGlobal, features.SourceLockedGlobal:
		return "global"
	case features.SourceOrganization, features.SourceLockedOrganization:
		return "organization"
	case features.SourceLower:
		return "account"
	default:
		return "default"
	}
}

func (s *Service) displayValue(meta KeyMeta, raw any) any {
	if meta.Secret {
		if ss, ok := asSecretStored(raw); ok {
			return "••••" + ss.Last4
		}
		if str, ok := raw.(string); ok {
			return maskSecret(str)
		}
	}
	return raw
}

func asSecretStored(raw any) (secretStored, bool) {
	switch v := raw.(type) {
	case secretStored:
		return v, v.Secret
	}
	m, ok := asObject(raw)
	if !ok {
		return secretStored{}, false
	}
	if secret, _ := m["_secret"].(bool); !secret {
		return secretStored{}, false
	}
	ss := secretStored{Secret: true}
	if l4, ok := m["last4"].(string); ok {
		ss.Last4 = l4
	}
	return ss, true
}

const maxPatchRetries = 3

func (s *Service) allowSettingsDottedKey(key string) bool {
	if _, ok := s.catalog[key]; ok {
		return true
	}
	if _, ok := s.Registry.Get(key); ok {
		return true
	}
	if strings.HasPrefix(key, "switches.") {
		_, ok := s.Registry.Get(strings.TrimPrefix(key, "switches."))
		return ok
	}
	return false
}

// Patch applies a JSON Merge Patch at scope ("global" or "organization").
func (s *Service) Patch(ctx context.Context, scope string, patch map[string]any, ifMatch string) (SettingsBody, error) {
	c, ok := tenancy.CallerFrom(ctx)
	if !ok {
		return SettingsBody{}, errs.New(errs.CodeUnauthenticated, "Authentication is required.")
	}
	var docID string
	switch scope {
	case "global":
		if err := tenancy.Require(ctx, tenancy.PermInstanceAdmin); err != nil {
			return SettingsBody{}, err
		}
		docID, scope = DocIDGlobal, "global"
	default:
		if err := tenancy.Require(ctx, tenancy.PermSettingsWrite); err != nil {
			return SettingsBody{}, err
		}
		if c.OrganizationID == "" {
			return SettingsBody{}, errs.New(errs.CodeForbidden, "An organisation context is required.")
		}
		docID, scope = OrgDocID(c.OrganizationID), "organization"
	}
	if err := rejectClientKeys(patch, "", s.allowSettingsDottedKey); err != nil {
		return SettingsBody{}, err
	}

	var changed []string
	var lastErr error
	for attempt := 0; attempt < maxPatchRetries; attempt++ {
		doc, err := s.loadDoc(ctx, docID)
		if err != nil {
			return SettingsBody{}, err
		}
		loadedVersion := doc.Version
		if ifMatch != "" {
			// If-Match carries the ETag of GET /v1/settings: the effective version of the caller's view.
			cur, err := s.viewVersion(ctx, c.OrganizationID, docID, doc)
			if err != nil {
				return SettingsBody{}, err
			}
			if parseETag(ifMatch) != cur {
				return SettingsBody{}, errs.New(errs.CodeVersionMismatch, "The settings version does not match If-Match.")
			}
		}
		work := cloneMap(patch)
		changed, err = s.applyPatchToDoc(ctx, doc, scope, work)
		if err != nil {
			return SettingsBody{}, err
		}
		doc.Version = loadedVersion + 1
		doc.UpdatedAt = time.Now().UTC()
		okWrite, err := s.replaceSettingsDoc(ctx, docID, loadedVersion, doc)
		if err != nil {
			return SettingsBody{}, err
		}
		if !okWrite {
			if ifMatch != "" {
				return SettingsBody{}, errs.New(errs.CodeVersionMismatch, "The settings version does not match If-Match.")
			}
			lastErr = errs.New(errs.CodeVersionMismatch, "The settings were updated concurrently.")
			continue
		}
		notifyScope := "global"
		if scope == "organization" {
			notifyScope = OrgDocID(c.OrganizationID)
		}
		s.Invalidate(notifyScope)
		if s.Notify != nil && len(changed) > 0 {
			s.Notify(ctx, notifyScope, changed)
		}
		// Answer the caller's view (as GET does) so the returned ETag works as the next If-Match.
		return s.Effective(ctx, c.OrganizationID)
	}
	if lastErr != nil {
		return SettingsBody{}, errs.New(errs.CodeConflict, "The settings could not be updated after concurrent modifications.")
	}
	return SettingsBody{}, errs.New(errs.CodeVersionMismatch, "The settings were updated concurrently.")
}

// viewVersion is the effective version of the caller's view (global + their organisation), using the
// already-loaded target document for its own part.
func (s *Service) viewVersion(ctx context.Context, orgID, loadedID string, loaded *Doc) (int64, error) {
	pick := func(id string) (*Doc, error) {
		if id == loadedID {
			return loaded, nil
		}
		return s.loadDoc(ctx, id)
	}
	global, err := pick(DocIDGlobal)
	if err != nil {
		return 0, err
	}
	var org *Doc
	if orgID != "" {
		if org, err = pick(OrgDocID(orgID)); err != nil {
			return 0, err
		}
	}
	return effectiveVersion(global, org), nil
}

func (s *Service) replaceSettingsDoc(ctx context.Context, docID string, loadedVersion int64, doc *Doc) (bool, error) {
	coll := s.DB.Collection(SettingsCollection)
	if loadedVersion == 0 {
		filter := bson.M{"_id": docID, "$or": []bson.M{
			{"version": int64(0)},
			{"version": bson.M{"$exists": false}},
		}}
		res, err := coll.ReplaceOne(ctx, filter, doc, options.Replace().SetUpsert(true))
		if err != nil {
			if mongo.IsDuplicateKeyError(err) {
				return false, nil
			}
			return false, fmt.Errorf("settings: save: %w", err)
		}
		return res.MatchedCount > 0 || res.UpsertedCount > 0, nil
	}
	res, err := coll.ReplaceOne(ctx, bson.M{"_id": docID, "version": loadedVersion}, doc)
	if err != nil {
		return false, fmt.Errorf("settings: save: %w", err)
	}
	return res.MatchedCount > 0, nil
}

func (s *Service) applyPatchToDoc(ctx context.Context, doc *Doc, scope string, patch map[string]any) ([]string, error) {
	changed := []string{}
	if v, has := patch["values"]; has {
		keys, err := s.applyValuesPatch(ctx, doc, scope, v)
		if err != nil {
			return nil, err
		}
		changed = append(changed, keys...)
		delete(patch, "values")
	}
	if v, has := patch["switches"]; has {
		keys, err := s.applySwitchesPatch(ctx, doc, scope, v)
		if err != nil {
			return nil, err
		}
		changed = append(changed, keys...)
		delete(patch, "switches")
	}
	if v, has := patch["env"]; has {
		if err := s.applyEnvPatch(ctx, doc, v); err != nil {
			return nil, err
		}
		changed = append(changed, "env")
		delete(patch, "env")
	}
	for k, v := range patch {
		if strings.HasPrefix(k, "switches.") {
			name := k[len("switches."):]
			keys, err := s.applySwitchesPatch(ctx, doc, scope, map[string]any{name: v})
			if err != nil {
				return nil, err
			}
			changed = append(changed, keys...)
			continue
		}
		if _, ok := s.catalog[k]; !ok {
			if _, ok := s.Registry.Get(k); ok {
				keys, err := s.applySwitchesPatch(ctx, doc, scope, map[string]any{k: v})
				if err != nil {
					return nil, err
				}
				changed = append(changed, keys...)
				continue
			}
			return nil, errs.New(errs.CodeSettingNotRuntime, "This setting cannot be changed at runtime.").WithSafe("key", k)
		}
		keys, err := s.applyValuesPatch(ctx, doc, scope, map[string]any{k: v})
		if err != nil {
			return nil, err
		}
		changed = append(changed, keys...)
	}
	return changed, nil
}

func parseETag(s string) int64 {
	// "v12" or v12 or "12"
	s = trimETag(s)
	var n int64
	_, _ = fmt.Sscanf(s, "v%d", &n)
	if n == 0 {
		_, _ = fmt.Sscanf(s, "%d", &n)
	}
	return n
}

func trimETag(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1]
	}
	return s
}

func formatETag(v int64) string { return fmt.Sprintf(`"v%d"`, v) }

func (s *Service) applyValuesPatch(ctx context.Context, doc *Doc, scope string, raw any) ([]string, error) {
	pm, ok := asObject(raw)
	if !ok {
		return nil, errs.New(errs.CodeValidationFailed, "values must be an object")
	}
	var keys []string
	orgID := ""
	if doc.ID != DocIDGlobal {
		orgID = doc.ID[len("org:"):]
	}
	for k, v := range pm {
		meta, ok := s.catalog[k]
		if !ok {
			return nil, errs.New(errs.CodeSettingNotRuntime, "This setting cannot be changed at runtime.").WithSafe("key", k)
		}
		if meta.Scope == "global" && scope != "global" {
			return nil, errs.New(errs.CodeForbidden, "This setting can only be changed at global scope.").WithSafe("key", k)
		}
		if meta.Scope == "organization" && scope != "organization" {
			return nil, errs.New(errs.CodeForbidden, "This setting can only be changed at organisation scope.").WithSafe("key", k)
		}
		if v == nil {
			delete(doc.Values, k)
			keys = append(keys, k)
			continue
		}
		parsed, err := ParseValue(meta, v)
		if err != nil {
			return nil, errs.New(errs.CodeValidationFailed, "Invalid setting value.").WithSafe("key", k).WithSafe("error", err.Error())
		}
		if meta.Secret {
			str, _ := parsed.(string)
			if str == "" {
				if s2, ok := v.(string); ok {
					str = s2
				}
			}
			stored, err := s.encryptSecret(ctx, orgID, k, doc.ID, str)
			if err != nil {
				return nil, err
			}
			doc.Values[k] = stored
		} else {
			doc.Values[k] = parsed
		}
		keys = append(keys, k)
	}
	return keys, nil
}

func (s *Service) encryptSecret(ctx context.Context, orgID, field, docID, plaintext string) (bson.M, error) {
	if s.Cipher == nil {
		return nil, fmt.Errorf("settings: cipher not configured")
	}
	last4 := plaintext
	if len(last4) > 4 {
		last4 = last4[len(last4)-4:]
	}
	es, err := s.Cipher.EncryptString(ctx, orgID, plaintext, crypto.AAD{
		Collection: SettingsCollection, Field: field, DocID: docID,
	})
	if err != nil {
		return nil, err
	}
	return bson.M{
		"_secret": true,
		"last4":   last4,
		"kid":     es.KID,
		"dek":     es.DEK,
		"nonce":   es.Nonce,
		"ct":      es.CT,
	}, nil
}

func (s *Service) applySwitchesPatch(ctx context.Context, doc *Doc, scope string, raw any) ([]string, error) {
	_ = ctx
	pm, ok := asObject(raw)
	if !ok {
		return nil, errs.New(errs.CodeValidationFailed, "switches must be an object")
	}
	var keys []string
	for name, v := range pm {
		sw, ok := s.Registry.Get(name)
		if !ok {
			return nil, errs.New(errs.CodeSwitchUnknown, "Unknown feature switch.").WithSafe("name", name)
		}
		wantScope := features.ScopeOrganization
		if scope == "global" {
			wantScope = features.ScopeGlobal
		}
		if !features.Allows(sw, wantScope) {
			return nil, errs.New(errs.CodeForbidden, "This switch cannot be set at this scope.").WithSafe("name", name)
		}
		// Higher-scope locks block lower-scope writes (W51).
		if scope != "global" {
			g, _ := s.loadDoc(ctx, DocIDGlobal)
			if g != nil {
				if gv, ok := g.Switches[name]; ok && gv.Locked {
					return nil, errs.New(errs.CodeSettingLocked, "This switch is locked at global scope.").WithSafe("name", name)
				}
			}
		}
		if v == nil {
			delete(doc.Switches, name)
			keys = append(keys, "switches."+name)
			continue
		}
		sv := SwitchValue{}
		if om, ok := asObject(v); ok {
			if _, has := om["value"]; has {
				sv.Value = om["value"]
			} else {
				sv.Value = v
			}
			if lk, ok := om["locked"].(bool); ok {
				canLock := sw.Lockable && ((scope == "global" && features.Allows(sw, features.ScopeGlobal)) ||
					(scope == "organization" && features.Allows(sw, features.ScopeOrganization)))
				if !canLock {
					return nil, errs.New(errs.CodeForbidden, "Only lockable switches may be locked at an allowed scope.")
				}
				sv.Locked = lk
			}
		} else {
			sv.Value = v
		}
		// type-check against switch default
		fv := toFeatureValue(sw, sv.Value)
		if sw.Default.IsBool() && fv.Bool == nil {
			return nil, errs.New(errs.CodeValidationFailed, "Switch expects a boolean.").WithSafe("name", name)
		}
		if sw.Default.IsString() && fv.String == nil {
			return nil, errs.New(errs.CodeValidationFailed, "Switch expects a string.").WithSafe("name", name)
		}
		sv.Value = featureDisplay(fv)
		doc.Switches[name] = sv
		keys = append(keys, "switches."+name)
	}
	return keys, nil
}

// envKeyRe is the shape of a CEL env key (env.<key>).
var envKeyRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)

const (
	maxEnvKeys     = 100
	maxEnvValueLen = 4096
)

// applyEnvPatch merges settings.env (CEL env.*, S8 §2). A value is a string, or {"value": "…", "secret": true}
// for a secret, which is stored encrypted like secret settings values (S1, S11 D1) and masked in any output.
func (s *Service) applyEnvPatch(ctx context.Context, doc *Doc, raw any) error {
	if raw == nil {
		doc.Env = map[string]any{}
		return nil
	}
	pm, ok := asObject(raw)
	if !ok {
		return errs.New(errs.CodeValidationFailed, "env must be an object")
	}
	if err := rejectPollution(pm, "env"); err != nil {
		return err
	}
	if doc.Env == nil {
		doc.Env = map[string]any{}
	}
	orgID := ""
	if doc.ID != DocIDGlobal {
		orgID = strings.TrimPrefix(doc.ID, "org:")
	}
	for k, v := range pm {
		if !envKeyRe.MatchString(k) {
			return errs.New(errs.CodeValidationFailed, "env keys are letters, digits and _ (max 64), starting with a letter or _.").WithSafe("key", k)
		}
		if v == nil {
			delete(doc.Env, k)
			continue
		}
		value, secret := "", false
		switch t := v.(type) {
		case string:
			value = t
		default:
			obj, ok := asObject(v)
			if !ok {
				return errs.New(errs.CodeValidationFailed, "An env value is a string or {value, secret}.").WithSafe("key", k)
			}
			str, ok := obj["value"].(string)
			if !ok {
				return errs.New(errs.CodeValidationFailed, "An env value object needs a string value.").WithSafe("key", k)
			}
			value = str
			secret, _ = obj["secret"].(bool)
		}
		if len(value) > maxEnvValueLen {
			return errs.New(errs.CodeValidationFailed, "An env value is at most 4096 bytes.").WithSafe("key", k)
		}
		if !secret {
			doc.Env[k] = value
			continue
		}
		stored, err := s.encryptSecret(ctx, orgID, "env."+k, doc.ID, value)
		if err != nil {
			return errs.Wrap(err, errs.CodeInternalError, "settings.env.encrypt")
		}
		doc.Env[k] = stored
	}
	if len(doc.Env) > maxEnvKeys {
		return errs.New(errs.CodeValidationFailed, "At most 100 env keys per organisation.")
	}
	return nil
}

// RuleEnv returns the organisation's CEL env values (secrets decrypted) and which keys are secret, for rule
// evaluation and output masking (S8 §2, §5). It is the rules EnvLoader.
func (s *Service) RuleEnv(ctx context.Context, orgID string) (map[string]string, []string, error) {
	env := map[string]string{}
	if orgID == "" {
		return env, nil, nil
	}
	doc, err := s.loadDoc(ctx, OrgDocID(orgID))
	if err != nil {
		return nil, nil, err
	}
	var secretKeys []string
	for k, raw := range doc.Env {
		if str, ok := raw.(string); ok {
			env[k] = str
			continue
		}
		// Round-trip through BSON so the stored binary fields decode into EncryptedString whatever their
		// in-memory type after loading.
		b, err := bson.Marshal(bson.M{"v": raw})
		if err != nil {
			continue
		}
		var wrap struct {
			V struct {
				Secret bool                   `bson:"_secret"`
				ES     crypto.EncryptedString `bson:",inline"`
			} `bson:"v"`
		}
		if bson.Unmarshal(b, &wrap) != nil || !wrap.V.Secret {
			continue
		}
		if s.Cipher == nil {
			return nil, nil, errs.New(errs.CodeInternalError, "settings: cipher not configured")
		}
		plain, err := s.Cipher.DecryptString(ctx, wrap.V.ES, crypto.AAD{Collection: SettingsCollection, Field: "env." + k, DocID: doc.ID})
		if err != nil {
			return nil, nil, errs.Wrap(err, errs.CodeInternalError, "settings.env.decrypt")
		}
		env[k] = plain
		secretKeys = append(secretKeys, k)
	}
	return env, secretKeys, nil
}

// ListFeatureSwitches returns the registry with effective values for the caller's org.
func (s *Service) ListFeatureSwitches(ctx context.Context) ([]FeatureSwitchBody, error) {
	if err := tenancy.RequireRead(ctx, "settings"); err != nil {
		return nil, err
	}
	c, _ := tenancy.CallerFrom(ctx)
	eff, err := s.Effective(ctx, c.OrganizationID)
	if err != nil {
		return nil, err
	}
	out := make([]FeatureSwitchBody, 0, len(s.Registry.All()))
	for _, sw := range s.Registry.All() {
		scopes := make([]string, len(sw.Scopes))
		for i, sc := range sw.Scopes {
			scopes[i] = string(sc)
		}
		item := FeatureSwitchBody{
			Name: sw.Name, Description: sw.Description, Scopes: scopes,
			Default: featureDisplay(sw.EffectiveDefault(s.EnvName)),
			Phase:   sw.Phase, Lockable: sw.Lockable,
		}
		if sv, ok := eff.Switches[sw.Name]; ok {
			item.Effective, item.Source, item.Locked = sv.Value, sv.Source, sv.Locked
		}
		out = append(out, item)
	}
	return out, nil
}

// UILocale returns the saved settings.ui.locale for userID, normalised to a
// supported code, or "" when unset / missing.
func (s *Service) UILocale(ctx context.Context, userID string) string {
	if s == nil || s.DB == nil || strings.TrimSpace(userID) == "" {
		return ""
	}
	var d UserDoc
	err := s.DB.Collection(UserSettingsCollection).FindOne(ctx, bson.M{"_id": userID}).Decode(&d)
	if err != nil || d.Settings == nil {
		return ""
	}
	ui, ok := asObject(d.Settings["ui"])
	if !ok || ui == nil {
		return ""
	}
	raw, _ := ui["locale"].(string)
	if !i18n.IsSupported(raw) {
		return ""
	}
	return i18n.Normalize(raw)
}

// GetUserSettings returns the caller's userSettings (users only).
func (s *Service) GetUserSettings(ctx context.Context) (UserSettingsBody, error) {
	c, ok := tenancy.CallerFrom(ctx)
	if !ok {
		return UserSettingsBody{}, errs.New(errs.CodeUnauthenticated, "Authentication is required.")
	}
	if c.Kind != tenancy.CallerUser || c.UserID == "" {
		return UserSettingsBody{}, errs.New(errs.CodeForbidden, "User settings require a signed-in user.")
	}
	var d UserDoc
	err := s.DB.Collection(UserSettingsCollection).FindOne(ctx, bson.M{"_id": c.UserID}).Decode(&d)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return UserSettingsBody{Settings: map[string]any{}, UpdatedAt: time.Time{}, Version: 0}, nil
	}
	if err != nil {
		return UserSettingsBody{}, err
	}
	if d.Settings == nil {
		d.Settings = map[string]any{}
	} else {
		d.Settings = normalizeMap(d.Settings)
	}
	return UserSettingsBody{Settings: d.Settings, UpdatedAt: d.UpdatedAt, Version: d.Version}, nil
}

// PatchUserSettings deep-merges a patch into the caller's userSettings.
func (s *Service) PatchUserSettings(ctx context.Context, patch map[string]any, ifMatch string) (UserSettingsBody, error) {
	c, ok := tenancy.CallerFrom(ctx)
	if !ok {
		return UserSettingsBody{}, errs.New(errs.CodeUnauthenticated, "Authentication is required.")
	}
	if c.Kind != tenancy.CallerUser || c.UserID == "" {
		return UserSettingsBody{}, errs.New(errs.CodeForbidden, "User settings require a signed-in user.")
	}
	if err := rejectPollution(patch, ""); err != nil {
		return UserSettingsBody{}, err
	}
	if err := validateDashboardPatch(patch); err != nil {
		return UserSettingsBody{}, err
	}
	var lastErr error
	for attempt := 0; attempt < maxPatchRetries; attempt++ {
		cur, err := s.GetUserSettings(ctx)
		if err != nil {
			return UserSettingsBody{}, err
		}
		loadedVersion := cur.Version
		if ifMatch != "" && parseETag(ifMatch) != loadedVersion {
			return UserSettingsBody{}, errs.New(errs.CodeVersionMismatch, "The settings version does not match If-Match.")
		}
		merged, err := DeepMerge(cur.Settings, patch)
		if err != nil {
			return UserSettingsBody{}, err
		}
		if err := ValidateUserSettings(merged); err != nil {
			return UserSettingsBody{}, err
		}
		now := time.Now().UTC()
		doc := UserDoc{ID: c.UserID, Settings: merged, UpdatedAt: now, Version: loadedVersion + 1}
		okWrite, err := s.replaceUserSettingsDoc(ctx, c.UserID, loadedVersion, doc)
		if err != nil {
			return UserSettingsBody{}, err
		}
		if !okWrite {
			if ifMatch != "" {
				return UserSettingsBody{}, errs.New(errs.CodeVersionMismatch, "The settings version does not match If-Match.")
			}
			lastErr = errs.New(errs.CodeVersionMismatch, "The settings were updated concurrently.")
			continue
		}
		return UserSettingsBody{Settings: merged, UpdatedAt: now, Version: doc.Version}, nil
	}
	if lastErr != nil {
		return UserSettingsBody{}, errs.New(errs.CodeConflict, "The settings could not be updated after concurrent modifications.")
	}
	return UserSettingsBody{}, errs.New(errs.CodeVersionMismatch, "The settings were updated concurrently.")
}

func (s *Service) replaceUserSettingsDoc(ctx context.Context, userID string, loadedVersion int64, doc UserDoc) (bool, error) {
	coll := s.DB.Collection(UserSettingsCollection)
	if loadedVersion == 0 {
		filter := bson.M{"_id": userID, "$or": []bson.M{
			{"version": int64(0)},
			{"version": bson.M{"$exists": false}},
		}}
		res, err := coll.ReplaceOne(ctx, filter, doc, options.Replace().SetUpsert(true))
		if err != nil {
			if mongo.IsDuplicateKeyError(err) {
				return false, nil
			}
			return false, err
		}
		return res.MatchedCount > 0 || res.UpsertedCount > 0, nil
	}
	res, err := coll.ReplaceOne(ctx, bson.M{"_id": userID, "version": loadedVersion}, doc)
	if err != nil {
		return false, err
	}
	return res.MatchedCount > 0, nil
}
