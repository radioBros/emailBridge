package health

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/radiobros/emailbridge/internal/accounts"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/index"
	"github.com/radiobros/emailbridge/internal/mailbox/ondemand"
	"github.com/radiobros/emailbridge/internal/store"
	ebsync "github.com/radiobros/emailbridge/internal/sync"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

const (
	defaultLimit = 50
	maxLimit     = 200
	checkRate    = 10.0 // per minute per organisation
	checkTimeout = 30 * time.Second
)

// AccountLoader loads a decrypted sync account for on-demand dial (accounts.SyncSource).
type AccountLoader interface {
	Load(ctx context.Context, accountID string) (*ebsync.Account, error)
}

// Service diagnoses account health.
type Service struct {
	DB       *store.DB
	Accounts AccountLoader
	Pool     *ondemand.Pool
	Log      *slog.Logger
	Now      func() time.Time

	checkMu sync.Mutex
	checkRL map[string]*tokenBucket

	listMu    sync.Mutex
	listCache map[string]*cachedRows // organisation → diagnosed rows (List)
}

type tokenBucket struct {
	tokens float64
	last   time.Time
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s *Service) log() *slog.Logger {
	if s.Log == nil {
		return slog.Default()
	}
	return s.Log
}

func (s *Service) accountsColl() *mongo.Collection {
	return s.DB.Collection(index.Accounts)
}

// Get returns a diagnosis for one account.
func (s *Service) Get(ctx context.Context, accountID string) (*AccountHealth, error) {
	if err := tenancy.RequireReadAccount(ctx, "accounts", accountID); err != nil {
		return nil, err
	}
	a, err := s.loadAccount(ctx, accountID)
	if err != nil {
		return nil, err
	}
	folders, err := s.folderCount(ctx, accountID)
	if err != nil {
		return nil, err
	}
	h := Diagnose(DiagnoseInput{Account: a, FolderCount: folders, Now: s.now()})
	return &h, nil
}

// Check runs a live probe then returns an updated diagnosis.
func (s *Service) Check(ctx context.Context, accountID string) (*AccountHealth, error) {
	if err := tenancy.RequireAccount(ctx, tenancy.PermAccountsOperate, accountID); err != nil {
		return nil, err
	}
	if org, ok := store.OrganizationFrom(ctx); ok {
		if err := s.takeCheckToken(ctx, org); err != nil {
			return nil, err
		}
	}
	a, err := s.loadAccount(ctx, accountID)
	if err != nil {
		return nil, err
	}
	folders, err := s.folderCount(ctx, accountID)
	if err != nil {
		return nil, err
	}
	live := s.liveCheck(ctx, a)
	s.invalidateList(a.OrganizationID)
	h := Diagnose(DiagnoseInput{Account: a, FolderCount: folders, Now: s.now(), Live: live})
	return &h, nil
}

func (s *Service) loadAccount(ctx context.Context, id string) (*accounts.Account, error) {
	filter, err := store.Scoped(ctx, bson.M{"_id": id})
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "health.scoped")
	}
	var a accounts.Account
	err = s.accountsColl().FindOne(ctx, filter).Decode(&a)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, errs.New(errs.CodeNotFound, "The account does not exist.").WithSafe("accountId", id)
		}
		return nil, errs.Wrap(err, errs.CodeInternalError, "health.load")
	}
	return &a, nil
}

func (s *Service) folderCount(ctx context.Context, accountID string) (int, error) {
	n, err := s.DB.Collection(index.Mailboxes).CountDocuments(ctx, bson.M{
		"accountId": accountID, "status": "active",
	})
	if err != nil {
		return 0, errs.Wrap(err, errs.CodeInternalError, "health.folders")
	}
	return int(n), nil
}

func (s *Service) takeCheckToken(ctx context.Context, org string) error {
	s.checkMu.Lock()
	defer s.checkMu.Unlock()
	if s.checkRL == nil {
		s.checkRL = map[string]*tokenBucket{}
	}
	b := s.checkRL[org]
	now := s.now()
	if b == nil {
		b = &tokenBucket{tokens: checkRate, last: now}
		s.checkRL[org] = b
	}
	elapsed := now.Sub(b.last).Minutes()
	b.tokens += elapsed * checkRate
	if b.tokens > checkRate {
		b.tokens = checkRate
	}
	b.last = now
	if b.tokens < 1 {
		info := errs.TokenBucketInfo(checkRate, checkRate, b.tokens)
		httpapi.NoteRateLimit(ctx, info)
		return errs.New(errs.CodeRateLimited, "Account health check is limited to 10 requests per minute per organisation.").WithRate(info)
	}
	b.tokens--
	httpapi.NoteRateLimit(ctx, errs.TokenBucketInfo(checkRate, checkRate, b.tokens))
	return nil
}

// List returns org-wide counts and a page of rows. The diagnosis of every account of the organisation is
// computed once and cached for listCacheTTL (W70-d4: a large organisation is not re-diagnosed on every page,
// filter or poll); API-key scoping, q, the severity filter, sorting and paging apply to the cached rows, so the
// counts always cover exactly the accounts the caller may see. GET/POST /v1/accounts/{id}/health stay live.
func (s *Service) List(ctx context.Context, f ListFilter) (*List, error) {
	if err := tenancy.RequireRead(ctx, "accounts"); err != nil {
		return nil, err
	}
	org, ok := store.OrganizationFrom(ctx)
	if !ok || org == "" {
		return nil, errs.Wrap(store.ErrNoTenant, errs.CodeInternalError, "health.scoped")
	}
	all, err := s.orgRows(ctx, org)
	if err != nil {
		return nil, err
	}
	allowed := s.visibleAccounts(ctx)
	q := strings.ToLower(strings.TrimSpace(f.Q))
	var rows []Row
	counts := Counts{}
	for _, row := range all {
		if allowed != nil && !allowed[row.AccountID] {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(row.Name), q) && !strings.Contains(strings.ToLower(row.Email), q) {
			continue
		}
		counts.Total++
		switch row.Severity {
		case SeverityCritical:
			counts.Critical++
		case SeverityWarning:
			counts.Warning++
		case SeverityInfo:
			counts.Info++
		default:
			counts.OK++
		}
		if f.Severity != "" && row.Severity != f.Severity {
			continue
		}
		rows = append(rows, row)
	}
	sortRows(rows, f.Sort)
	limit := f.Limit
	if limit <= 0 {
		limit = defaultLimit
	}
	if limit > maxLimit {
		limit = maxLimit
	}
	page := f.Page
	if page <= 0 {
		page = 1
	}
	start := (page - 1) * limit
	if start > len(rows) {
		start = len(rows)
	}
	end := start + limit
	if end > len(rows) {
		end = len(rows)
	}
	pageRows := rows[start:end]
	if pageRows == nil {
		pageRows = []Row{}
	}
	out := &List{Counts: counts, Data: pageRows, Page: page, Limit: limit}
	if f.IncludeTotal {
		n := len(rows)
		out.Total = &n
	}
	return out, nil
}

// listCacheTTL bounds how stale GET /v1/health/accounts may be.
const listCacheTTL = 15 * time.Second

type cachedRows struct {
	rows []Row
	at   time.Time
}

// listProjection skips the account fields the diagnosis never reads (SMTP, sync state, identity …).
var listProjection = bson.M{"smtp": 0, "sync": 0, "identity": 0, "network": 0, "pendingCommands": 0, "delegated": 0}

// orgRows returns the diagnosed rows of every account of org, from the cache when fresh.
func (s *Service) orgRows(ctx context.Context, org string) ([]Row, error) {
	now := s.now()
	s.listMu.Lock()
	if c, ok := s.listCache[org]; ok && now.Sub(c.at) < listCacheTTL && !now.Before(c.at) {
		rows := c.rows
		s.listMu.Unlock()
		return rows, nil
	}
	s.listMu.Unlock()

	cur, err := s.accountsColl().Find(ctx, bson.M{"organizationId": org}, options.Find().SetProjection(listProjection))
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "health.list")
	}
	defer func() { _ = cur.Close(ctx) }()
	var rows []Row
	for cur.Next(ctx) {
		var a accounts.Account
		if err := cur.Decode(&a); err != nil {
			return nil, errs.Wrap(err, errs.CodeInternalError, "health.decode")
		}
		rows = append(rows, ToRow(Diagnose(DiagnoseInput{Account: &a, FolderCount: 0, Now: now})))
	}
	if err := cur.Err(); err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "health.list")
	}
	s.listMu.Lock()
	if s.listCache == nil {
		s.listCache = map[string]*cachedRows{}
	}
	if len(s.listCache) >= maxListCacheOrgs {
		for k, c := range s.listCache {
			if now.Sub(c.at) >= listCacheTTL {
				delete(s.listCache, k)
			}
		}
		if len(s.listCache) >= maxListCacheOrgs {
			s.listCache = map[string]*cachedRows{}
		}
	}
	s.listCache[org] = &cachedRows{rows: rows, at: now}
	s.listMu.Unlock()
	return rows, nil
}

// maxListCacheOrgs bounds the list cache's memory.
const maxListCacheOrgs = 1024

// invalidateList drops the cached rows of org (after a live check changed what we know).
func (s *Service) invalidateList(org string) {
	s.listMu.Lock()
	delete(s.listCache, org)
	s.listMu.Unlock()
}

// visibleAccounts is the set of account IDs a resource-limited API key may see (nil = all of the org).
func (s *Service) visibleAccounts(ctx context.Context) map[string]bool {
	var ids []string
	limited := false
	if c, ok := tenancy.CallerFrom(ctx); ok && c.Kind == tenancy.CallerAPIKey && c.AccountID != "" {
		ids, limited = []string{c.AccountID}, true
	}
	if scoped, ok := tenancy.ScopedIDs(ctx, "accounts"); ok {
		ids, limited = scoped, true
	}
	if !limited {
		return nil
	}
	out := make(map[string]bool, len(ids))
	for _, id := range ids {
		out[id] = true
	}
	return out
}

func sortRows(rows []Row, sort string) {
	field, desc := "severity", true
	if sort != "" {
		desc = strings.HasPrefix(sort, "-")
		field = strings.TrimPrefix(sort, "-")
	}
	less := func(i, j int) bool {
		a, b := rows[i], rows[j]
		var cmp int
		switch field {
		case "name":
			cmp = strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
		case "state":
			cmp = strings.Compare(a.State, b.State)
		case "lastSyncAt":
			cmp = compareTimePtr(a.LastSyncAt, b.LastSyncAt)
		default: // severity
			cmp = a.Severity.Rank() - b.Severity.Rank()
			if cmp == 0 {
				cmp = strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
			}
		}
		if desc {
			cmp = -cmp
		}
		if cmp == 0 {
			return a.AccountID < b.AccountID
		}
		return cmp < 0
	}
	// insertion sort — lists are organisation-sized
	for i := 1; i < len(rows); i++ {
		for j := i; j > 0 && less(j, j-1); j-- {
			rows[j], rows[j-1] = rows[j-1], rows[j]
		}
	}
}

func compareTimePtr(a, b *time.Time) int {
	if a == nil && b == nil {
		return 0
	}
	if a == nil {
		return -1
	}
	if b == nil {
		return 1
	}
	return a.Compare(*b)
}
