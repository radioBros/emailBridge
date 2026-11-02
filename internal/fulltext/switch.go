package fulltext

import (
	"context"
	"errors"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/radiobros/emailbridge/internal/accounts"
	"github.com/radiobros/emailbridge/internal/features"
	"github.com/radiobros/emailbridge/internal/settings"
	"github.com/radiobros/emailbridge/internal/store"
)

// defaultSwitchTTL is how long a resolved switch is reused by the indexer and the API on one node.
const defaultSwitchTTL = 15 * time.Second

// Switches resolves `search.fullText`.
//
// The organisation value (with an organisation lock) is the master switch: search answers
// `feature_disabled` and nothing is indexed while it is off. With it on, every account is indexed unless the
// account sets the switch to false (an opt-out; an organisation lock forces the organisation value). An
// account that is missing or being deleted is never indexed.
type Switches struct {
	DB       *store.DB
	Registry *features.Registry
	Env      string
	// Name is the switch resolved (default SwitchName). Another feature with the same rule — organisation
	// master, account opt-out — reuses the resolver with its own name (G1 `ai.embeddings`).
	Name string
	// TTL caches answers per organisation / account (default 15 s; negative = no cache).
	TTL time.Duration
	Now func() time.Time

	mu    sync.Mutex
	cache map[string]cachedSwitch
}

type cachedSwitch struct {
	on  bool
	exp time.Time
}

// accountDoc is the part of an accounts document the switch needs.
type accountDoc struct {
	OrganizationID string         `bson:"organizationId"`
	State          string         `bson:"state"`
	Switches       map[string]any `bson:"switches"`
}

func (s *Switches) name() string {
	if s.Name != "" {
		return s.Name
	}
	return SwitchName
}

func (s *Switches) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Switches) ttl() time.Duration {
	if s.TTL == 0 {
		return defaultSwitchTTL
	}
	return s.TTL
}

func (s *Switches) registry() *features.Registry {
	if s.Registry != nil {
		return s.Registry
	}
	return features.DefaultRegistry()
}

func (s *Switches) env() string {
	if s.Env != "" {
		return s.Env
	}
	return "production"
}

func (s *Switches) cached(key string) (bool, bool) {
	if s.ttl() < 0 {
		return false, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.cache[key]
	if !ok || s.now().After(c.exp) {
		return false, false
	}
	return c.on, true
}

func (s *Switches) remember(key string, on bool) {
	if s.ttl() < 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cache == nil {
		s.cache = map[string]cachedSwitch{}
	}
	if len(s.cache) > 10000 { // bounded: drop everything rather than track ages
		s.cache = map[string]cachedSwitch{}
	}
	s.cache[key] = cachedSwitch{on: on, exp: s.now().Add(s.ttl())}
}

// Clear drops every cached answer (a settings change was announced).
func (s *Switches) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cache = nil
}

// Forget drops the cached answers of an account (after its switches changed).
func (s *Switches) Forget(accountID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.cache, "acc:"+accountID)
}

// OrgEnabled reports whether the switch is on for the organisation (the master switch).
func (s *Switches) OrgEnabled(ctx context.Context, orgID string) (bool, error) {
	if v, ok := s.cached("org:" + orgID); ok {
		return v, nil
	}
	on, _, _, err := s.resolveOrg(ctx, orgID)
	if err != nil {
		return false, err
	}
	s.remember("org:"+orgID, on)
	return on, nil
}

// AccountEnabled reports whether the account's messages are indexed: the organisation switch is on, the
// account exists in orgID and is not being deleted, and it did not opt out.
func (s *Switches) AccountEnabled(ctx context.Context, orgID, accountID string) (bool, error) {
	if v, ok := s.cached("acc:" + accountID); ok {
		return v, nil
	}
	on, err := s.resolveAccount(ctx, orgID, accountID)
	if err != nil {
		return false, err
	}
	s.remember("acc:"+accountID, on)
	return on, nil
}

// AccountEnabledFresh is AccountEnabled without the cache (purges decide on the current value).
func (s *Switches) AccountEnabledFresh(ctx context.Context, orgID, accountID string) (bool, error) {
	on, err := s.resolveAccount(ctx, orgID, accountID)
	if err != nil {
		return false, err
	}
	s.remember("acc:"+accountID, on)
	return on, nil
}

func (s *Switches) resolveAccount(ctx context.Context, orgID, accountID string) (bool, error) {
	orgOn, sw, vals, err := s.resolveOrg(ctx, orgID)
	if err != nil || !orgOn {
		return false, err
	}
	var acc accountDoc
	err = s.DB.Collection(accounts.Collection).FindOne(ctx,
		bson.M{"_id": accountID, "organizationId": orgID},
		options.FindOne().SetProjection(bson.M{"organizationId": 1, "state": 1, "switches": 1}),
	).Decode(&acc)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if acc.State == accounts.StateDeleting {
		return false, nil
	}
	if b, ok := acc.Switches[s.name()].(bool); ok {
		vals.Lower = &features.Setting{Value: features.BoolValue(b)}
	}
	v, _ := features.Resolve(sw, s.env(), vals)
	return v.Bool != nil && *v.Bool, nil
}

// resolveOrg resolves the organisation value and returns the layered values for an account resolution.
func (s *Switches) resolveOrg(ctx context.Context, orgID string) (bool, features.Switch, features.Values, error) {
	sw, ok := s.registry().Get(s.name())
	if !ok {
		return false, features.Switch{}, features.Values{}, nil
	}
	vals := features.Values{}
	var err error
	if features.Allows(sw, features.ScopeGlobal) {
		if vals.Global, err = s.readSetting(ctx, settings.DocIDGlobal); err != nil {
			return false, sw, vals, err
		}
	}
	if vals.Organization, err = s.readSetting(ctx, settings.OrgDocID(orgID)); err != nil {
		return false, sw, vals, err
	}
	v, _ := features.Resolve(sw, s.env(), vals)
	return v.Bool != nil && *v.Bool, sw, vals, nil
}

func (s *Switches) readSetting(ctx context.Context, docID string) (*features.Setting, error) {
	var doc settings.Doc
	// No projection: switch names contain dots, which a projection path would read as nesting.
	err := s.DB.Collection(settings.SettingsCollection).FindOne(ctx, bson.M{"_id": docID}).Decode(&doc)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	sv, ok := doc.Switches[s.name()]
	if !ok {
		return nil, nil
	}
	b, ok := sv.Value.(bool)
	if !ok {
		return nil, nil
	}
	return &features.Setting{Value: features.BoolValue(b), Locked: sv.Locked}, nil
}
