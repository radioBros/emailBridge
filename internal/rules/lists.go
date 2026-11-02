package rules

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/radiobros/emailbridge/internal/audit"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

const (
	RuleListsCollection = "ruleLists"
	maxListsPerOrg      = 50
	maxEntriesPerList   = 5000
	maxEntryBytes       = 256
)

var reListName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// RuleList is an organisation named list for inList().
type RuleList struct {
	ID             string    `bson:"_id" json:"id" doc:"Named list ID (rls_…)" example:"rls_01m5dn2nz0q7h3ncd1j49hxtnb"`
	OrganizationID string    `bson:"organizationId" json:"organizationId" doc:"Organisation ID (org_…)" example:"org_01m5caqpk0kast63yc13wpy2d9"`
	Name           string    `bson:"name" json:"name" doc:"List name used in inList(\"name\", …): lower case, digits, - and _, at most 64 characters" example:"vip-customers"`
	Kind           string    `bson:"kind" json:"kind" doc:"addresses, domains or keywords" example:"addresses"` // addresses | domains | keywords
	Entries        []string  `bson:"entries" json:"entries,omitempty" doc:"The entries (omitted in list responses)"`
	EntryCount     int       `bson:"entryCount" json:"entryCount" doc:"Number of entries" example:"3"`
	CreatedAt      time.Time `bson:"createdAt" json:"createdAt" doc:"When the list was created" example:"2026-10-22T10:00:00Z"`
	UpdatedAt      time.Time `bson:"updatedAt" json:"updatedAt" doc:"When it last changed" example:"2026-10-26T16:45:12Z"`
	Version        int64     `bson:"version" json:"version" doc:"Document version; the ETag" example:"2"`
}

// RuleListCreate is POST /v1/rule-lists.
type RuleListCreate struct {
	Name    string   `json:"name" doc:"List name: lower case, digits, - and _, at most 64 characters" example:"vip-customers"`
	Kind    string   `json:"kind" doc:"addresses, domains or keywords" example:"addresses"`
	Entries []string `json:"entries,omitempty" doc:"Entries (at most 5000; lower-cased and de-duplicated)"`
}

// RuleListUpdate is PATCH /v1/rule-lists/{id}.
type RuleListUpdate struct {
	Name    *string   `json:"name,omitempty" doc:"New name" example:"key-customers"`
	Entries *[]string `json:"entries,omitempty" doc:"Replaces all entries"`
}

// RuleListPage is a list response.
type RuleListPage struct {
	Data       []RuleList `json:"data" doc:"The lists of this page."`
	NextCursor *string    `json:"nextCursor" doc:"Always null: the lists are paged with page (no cursor)"`
	Total      *int64     `json:"total,omitempty" doc:"Total number of matching lists (only with includeTotal)." example:"2"`
}

// ListListsFilter is GET /v1/rule-lists (W73b: server-side search, sort and pages for the admin table).
type ListListsFilter struct {
	Limit        int
	Page         int
	Q            string // case-insensitive substring of the name
	Kind         string
	Sort         string // name | updatedAt | entryCount, "-" prefix = descending
	IncludeTotal bool
}

var listSortFields = map[string]string{"name": "name", "updatedAt": "updatedAt", "entryCount": "entryCount"}

// ListCache is a short TTL snapshot of org lists for inList().
type ListCache struct {
	DB  *store.DB
	TTL time.Duration

	mu    sync.Mutex
	cache map[string]cachedLists
}

type cachedLists struct {
	m  map[string]map[string]struct{}
	at time.Time
}

// Snapshot returns name → lowercased membership set for the org.
func (c *ListCache) Snapshot(ctx context.Context, orgID string) (map[string]map[string]struct{}, error) {
	if c == nil || c.DB == nil {
		return map[string]map[string]struct{}{}, nil
	}
	ttl := c.TTL
	if ttl <= 0 {
		ttl = 5 * time.Second
	}
	c.mu.Lock()
	if x, ok := c.cache[orgID]; ok && time.Since(x.at) < ttl {
		c.mu.Unlock()
		return x.m, nil
	}
	c.mu.Unlock()
	cur, err := c.DB.Collection(RuleListsCollection).Find(ctx, bson.M{"organizationId": orgID},
		options.Find().SetProjection(bson.M{"name": 1, "entries": 1}))
	if err != nil {
		return nil, err
	}
	var rows []struct {
		Name    string   `bson:"name"`
		Entries []string `bson:"entries"`
	}
	if err := cur.All(ctx, &rows); err != nil {
		return nil, err
	}
	m := make(map[string]map[string]struct{}, len(rows))
	for _, r := range rows {
		set := make(map[string]struct{}, len(r.Entries))
		for _, e := range r.Entries {
			set[strings.ToLower(strings.TrimSpace(e))] = struct{}{}
		}
		m[strings.ToLower(r.Name)] = set
		m[r.Name] = set
	}
	c.mu.Lock()
	if c.cache == nil {
		c.cache = map[string]cachedLists{}
	}
	c.cache[orgID] = cachedLists{m: m, at: time.Now()}
	c.mu.Unlock()
	return m, nil
}

// Invalidate drops the org snapshot.
func (c *ListCache) Invalidate(orgID string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	delete(c.cache, orgID)
	c.mu.Unlock()
}

func normalizeEntries(in []string) ([]string, error) {
	if len(in) > maxEntriesPerList {
		return nil, errs.New(errs.CodeValidationFailed, "list exceeds 5000 entries")
	}
	out := make([]string, 0, len(in))
	seen := map[string]struct{}{}
	for _, e := range in {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		if len(e) > maxEntryBytes {
			return nil, errs.New(errs.CodeValidationFailed, "list entry exceeds 256 bytes")
		}
		key := strings.ToLower(e)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, e)
	}
	return out, nil
}

func validateListKind(kind string) error {
	switch kind {
	case "addresses", "domains", "keywords":
		return nil
	default:
		return errs.New(errs.CodeValidationFailed, "kind must be addresses, domains or keywords")
	}
}

// CreateList inserts a named list.
func (r *Repo) CreateList(ctx context.Context, in RuleListCreate) (*RuleList, error) {
	if err := tenancy.Require(ctx, tenancy.PermWebhooksWrite); err != nil {
		return nil, err
	}
	c, _ := tenancy.CallerFrom(ctx)
	name := strings.TrimSpace(in.Name)
	if !reListName.MatchString(name) {
		return nil, errs.New(errs.CodeValidationFailed, "invalid list name")
	}
	if err := validateListKind(in.Kind); err != nil {
		return nil, err
	}
	entries, err := normalizeEntries(in.Entries)
	if err != nil {
		return nil, err
	}
	n, err := r.DB.Collection(RuleListsCollection).CountDocuments(ctx, bson.M{"organizationId": c.OrganizationID})
	if err != nil {
		return nil, err
	}
	if n >= maxListsPerOrg {
		return nil, errs.New(errs.CodeValidationFailed, "organisation list quota exceeded (50)")
	}
	now := time.Now().UTC()
	list := &RuleList{
		ID: store.NewID(store.PrefixRuleList), OrganizationID: c.OrganizationID,
		Name: name, Kind: in.Kind, Entries: entries, EntryCount: len(entries),
		CreatedAt: now, UpdatedAt: now, Version: 1,
	}
	if _, err := r.DB.Collection(RuleListsCollection).InsertOne(ctx, list); err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return nil, errs.New(errs.CodeRuleListNameTaken, "A rule list with this name already exists.")
		}
		return nil, err
	}
	if r.Lists != nil {
		r.Lists.Invalidate(c.OrganizationID)
	}
	if r.Audit != nil {
		_ = r.Audit.Record(ctx, "rule_list.create", audit.Target{Type: "ruleList", ID: list.ID}, audit.Changes{
			"name": {After: name}, "kind": {After: in.Kind},
		})
	}
	return list, nil
}

// GetList returns one list.
func (r *Repo) GetList(ctx context.Context, id string) (*RuleList, error) {
	if err := tenancy.RequireReadOn(ctx, "routes", id); err != nil {
		return nil, err
	}
	filter, err := store.Scoped(ctx, bson.M{"_id": id})
	if err != nil {
		return nil, err
	}
	var list RuleList
	err = r.DB.Collection(RuleListsCollection).FindOne(ctx, filter).Decode(&list)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, errs.New(errs.CodeNotFound, "Rule list not found.")
	}
	return &list, err
}

// ListLists returns org lists.
func (r *Repo) ListLists(ctx context.Context, limit int) (*RuleListPage, error) {
	return r.ListListsPage(ctx, ListListsFilter{Limit: limit})
}

// ListListsPage returns one page of org lists; the list view leaves out the entries (W73b) — GET one list for
// them.
func (r *Repo) ListListsPage(ctx context.Context, f ListListsFilter) (*RuleListPage, error) {
	if err := tenancy.RequireRead(ctx, "routes"); err != nil {
		return nil, err
	}
	c, _ := tenancy.CallerFrom(ctx)
	if f.Limit <= 0 || f.Limit > 100 {
		f.Limit = 50
	}
	f.Page = max(f.Page, 1)
	q := bson.M{"organizationId": c.OrganizationID}
	if s := strings.TrimSpace(f.Q); s != "" {
		q["name"] = bson.M{"$regex": regexp.QuoteMeta(strings.ToLower(s))} // names are lower-case
	}
	if f.Kind != "" {
		q["kind"] = f.Kind
	}
	dir, key := 1, strings.TrimSpace(f.Sort)
	if strings.HasPrefix(key, "-") {
		dir, key = -1, key[1:]
	}
	field, ok := listSortFields[key]
	if !ok {
		field, dir = "name", 1
	}
	sort := bson.D{{Key: field, Value: dir}}
	if field != "name" {
		sort = append(sort, bson.E{Key: "name", Value: 1})
	}
	cur, err := r.DB.Collection(RuleListsCollection).Find(ctx, q, options.Find().
		SetSort(sort).SetSkip(int64(f.Page-1)*int64(f.Limit)).SetLimit(int64(f.Limit)).
		SetProjection(bson.M{"entries": 0}))
	if err != nil {
		return nil, err
	}
	rows := []RuleList{}
	if err := cur.All(ctx, &rows); err != nil {
		return nil, err
	}
	out := &RuleListPage{Data: rows}
	if f.IncludeTotal {
		n, err := r.DB.Collection(RuleListsCollection).CountDocuments(ctx, q)
		if err != nil {
			return nil, err
		}
		out.Total = &n
	}
	return out, nil
}

// UpdateList patches a list.
func (r *Repo) UpdateList(ctx context.Context, id string, in RuleListUpdate, ifMatch string) (*RuleList, error) {
	if err := tenancy.RequireOn(ctx, tenancy.PermWebhooksWrite, id); err != nil {
		return nil, err
	}
	cur, err := r.GetList(ctx, id)
	if err != nil {
		return nil, err
	}
	if ifMatch != "" && parseETag(ifMatch) != cur.Version {
		return nil, errs.New(errs.CodeVersionMismatch, "The resource version does not match If-Match.")
	}
	if in.Name != nil {
		name := strings.TrimSpace(*in.Name)
		if !reListName.MatchString(name) {
			return nil, errs.New(errs.CodeValidationFailed, "invalid list name")
		}
		cur.Name = name
	}
	if in.Entries != nil {
		entries, err := normalizeEntries(*in.Entries)
		if err != nil {
			return nil, err
		}
		cur.Entries = entries
		cur.EntryCount = len(entries)
	}
	cur.UpdatedAt = time.Now().UTC()
	old := cur.Version
	cur.Version++
	filter, err := store.Scoped(ctx, bson.M{"_id": id, "version": old})
	if err != nil {
		return nil, err
	}
	res, err := r.DB.Collection(RuleListsCollection).ReplaceOne(ctx, filter, cur)
	if mongo.IsDuplicateKeyError(err) {
		return nil, errs.New(errs.CodeRuleListNameTaken, "A rule list with this name already exists.")
	}
	if err != nil {
		return nil, err
	}
	if res.MatchedCount == 0 {
		return nil, errs.New(errs.CodeVersionMismatch, "The resource version does not match If-Match.")
	}
	if r.Lists != nil {
		r.Lists.Invalidate(cur.OrganizationID)
	}
	if r.Audit != nil {
		_ = r.Audit.Record(ctx, "rule_list.update", audit.Target{Type: "ruleList", ID: cur.ID}, nil)
	}
	return cur, nil
}

// DeleteList removes a list.
func (r *Repo) DeleteList(ctx context.Context, id, ifMatch string) error {
	if err := tenancy.RequireOn(ctx, tenancy.PermWebhooksWrite, id); err != nil {
		return err
	}
	cur, err := r.GetList(ctx, id)
	if err != nil {
		return err
	}
	if ifMatch != "" && parseETag(ifMatch) != cur.Version {
		return errs.New(errs.CodeVersionMismatch, "The resource version does not match If-Match.")
	}
	filter, err := store.Scoped(ctx, bson.M{"_id": id, "version": cur.Version})
	if err != nil {
		return err
	}
	res, err := r.DB.Collection(RuleListsCollection).DeleteOne(ctx, filter)
	if err != nil {
		return err
	}
	if res.DeletedCount == 0 {
		return errs.New(errs.CodeVersionMismatch, "The resource version does not match If-Match.")
	}
	if r.Lists != nil {
		r.Lists.Invalidate(cur.OrganizationID)
	}
	if r.Audit != nil {
		_ = r.Audit.Record(ctx, "rule_list.delete", audit.Target{Type: "ruleList", ID: id}, nil)
	}
	return nil
}

// EnsureListIndexes creates ruleLists indexes (migration 45).
func EnsureListIndexes(ctx context.Context, db *store.DB) error {
	return db.EnsureIndexes(ctx, RuleListsCollection,
		mongo.IndexModel{
			Keys:    bson.D{{Key: "organizationId", Value: 1}, {Key: "name", Value: 1}},
			Options: options.Index().SetUnique(true),
		},
		mongo.IndexModel{Keys: bson.D{{Key: "organizationId", Value: 1}, {Key: "_id", Value: 1}}},
	)
}
