package tenancy

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/store"
)

var slugPattern = regexp.MustCompile(`^[a-z0-9-]{2,48}$`)

var orgSortFields = map[string]string{
	"name": "name", "createdAt": "createdAt", "id": "_id",
}

func (m *Module) registerOrgs(srv *httpapi.Server) {
	ext := httpapi.Phase("P0")
	httpapi.Register(srv, huma.Operation{
		OperationID: "listOrganizations", Method: http.MethodGet, Path: "/v1/organizations",
		Summary: "List organisations", Tags: []string{"organizations"},
		Description: "Lists every organisation on the instance. Instance owners only (`instance.admin`); other callers get " +
			"`403 forbidden`. Pagination: `limit` (default 50, max 200) with `cursor` from `nextCursor`, or `page` " +
			"(1-based) with `includeTotal=true` for a total capped at 10 000. Sort by `name`, `createdAt` or `id` " +
			"(prefix `-` for descending; default `-id`). Optional `q` filters by name or slug (case-insensitive " +
			"substring). Each row includes `memberCount` and `accountCount` (one aggregation per page). Read-only.",
		Metadata:   httpapi.Problems(errs.CodeUnauthenticated, errs.CodeForbidden, errs.CodeSortNotAllowed, errs.CodeValidationFailed),
		Extensions: httpapi.WithScope(ext, "organizations:read"),
	}, m.listOrganizations)
	httpapi.Register(srv, huma.Operation{
		OperationID: "createOrganization", Method: http.MethodPost, Path: "/v1/organizations",
		DefaultStatus: http.StatusCreated,
		Summary:       "Create organisation", Tags: []string{"organizations"},
		Description: "Creates an empty organisation (no members yet; invite the first owner with `POST /v1/members` " +
			"under `EB-Organization`). Instance owners only. The slug is lower-cased and must be unique on the " +
			"instance (`409 conflict` otherwise). Writes an `organization.created` audit record. Supports " +
			"`Idempotency-Key`; returns `Location` and `ETag`.",
		Metadata:   httpapi.Problems(errs.CodeUnauthenticated, errs.CodeForbidden, errs.CodeValidationFailed, errs.CodeConflict),
		Extensions: httpapi.WithScope(httpapi.Phase("P0"), "organizations:write"),
	}, m.createOrganization)
	httpapi.Register(srv, huma.Operation{
		OperationID: "getOrganization", Method: http.MethodGet, Path: "/v1/organizations/{organizationId}",
		Summary: "Get organisation", Tags: []string{"organizations"},
		Description: "Returns one organisation with its `ETag`. Members may read their own organisation (`read` " +
			"permission; API keys need a read scope); instance owners may read any. Another organisation's ID gives " +
			"`403 organization_mismatch`.",
		Metadata: httpapi.Problems(errs.CodeUnauthenticated, errs.CodeForbidden, errs.CodeOrganizationMismatch,
			errs.CodeScopeMissing, errs.CodeNotFound),
		Extensions: httpapi.WithScope(httpapi.Phase("P0"), "organizations:read"),
	}, m.getOrganization)
	httpapi.Register(srv, huma.Operation{
		OperationID: "patchOrganization", Method: http.MethodPatch, Path: "/v1/organizations/{organizationId}",
		Summary: "Update organisation", Tags: []string{"organizations"},
		Description: "Changes the name, branding, quotas or status (`active` / `suspended`) of an organisation; omitted " +
			"fields stay unchanged. Requires `settings.write` in that organisation (owner, admin) or the instance " +
			"owner; `quotas` can be changed by the instance owner only (`403`)." +
			" Send the last `ETag` as `If-Match` to avoid lost updates (`412 version_mismatch`). An " +
			"organisation being deleted cannot be changed (`409 conflict`). Writes an `organization.updated` audit record.",
		Metadata: httpapi.Problems(errs.CodeUnauthenticated, errs.CodeForbidden, errs.CodeOrganizationMismatch, errs.CodeNotFound,
			errs.CodeVersionMismatch, errs.CodeConflict, errs.CodeValidationFailed),
		Extensions: httpapi.WithScope(httpapi.Phase("P0"), "organizations:write"),
	}, m.patchOrganization)
	httpapi.Register(srv, huma.Operation{
		OperationID: "deleteOrganization", Method: http.MethodDelete, Path: "/v1/organizations/{organizationId}",
		DefaultStatus: http.StatusAccepted,
		Summary:       "Delete organisation", Tags: []string{"organizations"},
		Description: "Marks the organisation `deleting` and queues an `organization.erasure` job that erases its " +
			"accounts, messages, endpoints, keys and members, keeping only the audit log (reduced to IDs) for its " +
			"retention period. Instance owners or organisation owners (`organization.delete`). Answers `202` with " +
			"the job and `Location: /v1/jobs/{jobId}`; poll the job for progress. Irreversible. Writes an " +
			"`organization.deleted` audit record. `If-Match` is honoured.",
		Metadata: httpapi.Problems(errs.CodeUnauthenticated, errs.CodeForbidden, errs.CodeNotFound, errs.CodeConflict,
			errs.CodeVersionMismatch, errs.CodeFeatureDisabled),
		Extensions: httpapi.WithScope(httpapi.Phase("P0"), "organizations:write"),
	}, m.deleteOrganization)
}

type orgListIn struct {
	Limit        int    `query:"limit" doc:"Page size (default 50, at most 200)." example:"50"`
	Cursor       string `query:"cursor" doc:"Opaque cursor from the previous page's nextCursor." example:"eyJpZCI6Im9yZ18wMW01Y2FxcGswa2FzdDYzeWMxM3dweTJkOSIsImZpZWxkIjoiX2lkIn0"`
	Page         int    `query:"page" doc:"1-based page number (offset paging; use instead of cursor)." example:"1"`
	IncludeTotal bool   `query:"includeTotal" doc:"With page: also return total (capped at 10 000)." example:"true"`
	Sort         string `query:"sort" doc:"Sort field name, createdAt or id; prefix - for descending. Default -id." example:"name"`
	Q            string `query:"q" doc:"Case-insensitive substring match on name or slug." example:"acme"`
}

type orgListOut struct {
	Body OrganizationList
}

// OrganizationList is the list response (S2 §2).
type OrganizationList struct {
	Data        []Organization `json:"data" doc:"The organisations of this page."`
	NextCursor  *string        `json:"nextCursor" doc:"Cursor of the next page; null on the last page." example:"eyJpZCI6Im9yZ18wMW01Y2FxcGswa2FzdDYzeWMxM3dweTJkOSIsImZpZWxkIjoiX2lkIn0"`
	Total       *int64         `json:"total,omitempty" doc:"Total number of organisations (only with page and includeTotal)." example:"2"`
	TotalCapped *bool          `json:"totalCapped,omitempty" doc:"True when total stopped counting at 10 000." example:"false"`
}

type orgCreateIn struct {
	Body struct {
		Name     string         `json:"name" doc:"Display name (required, not blank)." example:"Acme Legal"`
		Slug     string         `json:"slug" doc:"Unique short name, [a-z0-9-]{2,48} (lower-cased)." example:"acme-legal"`
		Branding map[string]any `json:"branding,omitempty" doc:"Optional connect-page branding (see /v1/connect/branding)." example:"{\"brandName\":\"Acme Mail\"}"`
		Quotas   map[string]any `json:"quotas,omitempty" doc:"Organisation quotas, enforced in addition to the instance-wide EB_QUOTA_* limits (the stricter wins): accounts, sendsPerDay (rolling 24 h, whole organisation), sendsPerAccountMinute (rolling minute, per account). Whole numbers; omitted, null or 0 = no organisation limit. Unknown keys fail with 422." example:"{\"accounts\":50,\"sendsPerDay\":20000,\"sendsPerAccountMinute\":30}"`
	}
}

type orgCreatedOut struct {
	Status   int
	Location string `header:"Location" doc:"URL of the new organisation." example:"/v1/organizations/org_01m5caqpk0kast63yc13wpy2d9"`
	ETag     string `header:"ETag" doc:"Version of the new organisation." example:"\"v1\""`
	Body     Organization
}

type orgIDIn struct {
	OrganizationID string `path:"organizationId" doc:"Organisation ID (org_…)." example:"org_01m5caqpk0kast63yc13wpy2d9"`
}

type orgGetOut struct {
	ETag string `header:"ETag" doc:"Current version; send as If-Match on PATCH / DELETE." example:"\"v3\""`
	Body Organization
}

type orgPatchIn struct {
	OrganizationID string `path:"organizationId" doc:"Organisation ID (org_…)." example:"org_01m5caqpk0kast63yc13wpy2d9"`
	IfMatch        string `header:"If-Match" doc:"ETag from the last read; a stale value fails with 412 version_mismatch." example:"\"v3\""`
	Body           struct {
		Name     *string        `json:"name,omitempty" doc:"New display name (not blank)." example:"Acme Legal S.r.l."`
		Branding map[string]any `json:"branding,omitempty" doc:"Replaces the branding object." example:"{\"brandName\":\"Acme Mail\"}"`
		Quotas   map[string]any `json:"quotas,omitempty" doc:"Replaces the quota values (instance owners only; Organisation quotas, enforced in addition to the instance-wide EB_QUOTA_* limits (the stricter wins): accounts, sendsPerDay (rolling 24 h, whole organisation), sendsPerAccountMinute (rolling minute, per account). Whole numbers; omitted, null or 0 = no organisation limit. Unknown keys fail with 422. Send {} to clear them all)." example:"{\"accounts\":100}"`
		Status   *string        `json:"status,omitempty" doc:"active or suspended." example:"suspended"`
	}
}

type orgDeleteIn struct {
	OrganizationID string `path:"organizationId" doc:"Organisation ID (org_…)." example:"org_01m5caqpk0kast63yc13wpy2d9"`
	IfMatch        string `header:"If-Match" doc:"ETag from the last read; a stale value fails with 412 version_mismatch." example:"\"v3\""`
}

// AcceptedJob is the JobAccepted payload subset returned on 202 (OpenAPI Job).
type AcceptedJob struct {
	ID             string    `json:"id" doc:"Job ID (job_…); poll GET /v1/jobs/{jobId}." example:"job_01m5cnaee0w96fc46z350nc6q0"`
	OrganizationID string    `json:"organizationId,omitempty" doc:"Organisation the job works on." example:"org_01m5caqpk0kast63yc13wpy2d9"`
	Kind           string    `json:"kind" doc:"Job kind." example:"organization.erasure"`
	State          string    `json:"state" doc:"Job state at creation (queued)." example:"queued"`
	CreatedAt      time.Time `json:"createdAt" doc:"When the job was queued." example:"2026-10-24T15:00:00Z"`
}

type orgDeleteOut struct {
	Status   int
	Location string `header:"Location" doc:"URL of the erasure job." example:"/v1/jobs/job_01m5cnaee0w96fc46z350nc6q0"`
	Body     struct {
		Job AcceptedJob `json:"job" doc:"The queued erasure job."`
	}
}

func (m *Module) listOrganizations(ctx context.Context, in *orgListIn) (*orgListOut, error) {
	list, err := m.service.ListOrganizations(ctx, ListFilter{
		Limit: in.Limit, Cursor: in.Cursor, Page: in.Page, IncludeTotal: in.IncludeTotal, Sort: in.Sort, Q: in.Q,
	})
	if err != nil {
		return nil, err
	}
	return &orgListOut{Body: *list}, nil
}

func (m *Module) createOrganization(ctx context.Context, in *orgCreateIn) (*orgCreatedOut, error) {
	o, err := m.service.CreateOrganization(ctx, in.Body.Name, in.Body.Slug, in.Body.Branding, in.Body.Quotas)
	if err != nil {
		return nil, err
	}
	return &orgCreatedOut{Status: http.StatusCreated, Location: "/v1/organizations/" + o.ID, ETag: formatETag(o.Version), Body: *o}, nil
}

func (m *Module) getOrganization(ctx context.Context, in *orgIDIn) (*orgGetOut, error) {
	o, err := m.service.GetOrganization(ctx, in.OrganizationID)
	if err != nil {
		return nil, err
	}
	return &orgGetOut{ETag: formatETag(o.Version), Body: *o}, nil
}

func (m *Module) patchOrganization(ctx context.Context, in *orgPatchIn) (*orgGetOut, error) {
	o, err := m.service.PatchOrganization(ctx, in.OrganizationID, OrgPatch{
		Name: in.Body.Name, Branding: in.Body.Branding, Quotas: in.Body.Quotas, Status: in.Body.Status,
	}, in.IfMatch)
	if err != nil {
		return nil, err
	}
	return &orgGetOut{ETag: formatETag(o.Version), Body: *o}, nil
}

func (m *Module) deleteOrganization(ctx context.Context, in *orgDeleteIn) (*orgDeleteOut, error) {
	job, err := m.service.DeleteOrganization(ctx, in.OrganizationID, in.IfMatch)
	if err != nil {
		return nil, err
	}
	out := &orgDeleteOut{Status: http.StatusAccepted, Location: "/v1/jobs/" + job.ID}
	out.Body.Job = *job
	return out, nil
}

// ListFilter holds shared list query parameters.
type ListFilter struct {
	Limit        int
	Cursor       string
	Page         int
	IncludeTotal bool
	Sort         string
	Q            string // members: invite email or user email/name
}

// OrgPatch is PATCH /v1/organizations/{id}.
type OrgPatch struct {
	Name     *string
	Branding map[string]any
	Quotas   map[string]any
	Status   *string
}

// ListOrganizations is GET /v1/organizations (instance owners only).
func (s *Service) ListOrganizations(ctx context.Context, f ListFilter) (*OrganizationList, error) {
	if err := Require(ctx, PermInstanceAdmin); err != nil {
		return nil, err
	}
	sortField, sortDir, err := parseListSort(f.Sort, orgSortFields)
	if err != nil {
		return nil, err
	}
	limit := clampLimit(f.Limit)
	sortDoc := bson.D{{Key: sortField, Value: sortDir}}
	if sortField != "_id" {
		sortDoc = append(sortDoc, bson.E{Key: "_id", Value: sortDir})
	}
	base := bson.M{}
	if err := applyOrgSearch(base, f.Q); err != nil {
		return nil, err
	}
	filter := cloneFilter(base)
	var skip int64
	if f.Page > 0 {
		skip = int64(f.Page-1) * int64(limit)
	} else if f.Cursor != "" {
		cur, err := decodeCursor(f.Cursor)
		if err != nil {
			return nil, errs.New(errs.CodeValidationFailed, "The cursor is invalid.")
		}
		keyFilter, err := orgCursorFilter(cur, sortField, sortDir)
		if err != nil {
			return nil, err
		}
		filter = bson.M{"$and": []bson.M{base, keyFilter}}
	}
	rows, err := s.Repo.ListOrganizations(ctx, filter, sortDoc, int64(limit+1), skip)
	if err != nil {
		return nil, err
	}
	out := &OrganizationList{Data: rows}
	if out.Data == nil {
		out.Data = []Organization{}
	}
	if len(rows) > limit {
		rows = rows[:limit]
		out.Data = rows
		last := rows[len(rows)-1]
		nc := encodeCursor(cursorPayload{ID: last.ID, Field: sortField, Desc: sortDir < 0, Key: orgSortKey(last, sortField)})
		out.NextCursor = &nc
	}
	if err := s.attachOrgCounts(ctx, out.Data); err != nil {
		return nil, err
	}
	if f.IncludeTotal && f.Page > 0 {
		n, err := s.Repo.CountOrganizations(ctx, base, totalCap+1)
		if err != nil {
			return nil, err
		}
		capped := n > totalCap
		if capped {
			n = totalCap
		}
		out.Total, out.TotalCapped = &n, &capped
	}
	return out, nil
}

// applyOrgSearch adds a case-insensitive name/slug substring filter onto base.
func applyOrgSearch(base bson.M, raw string) error {
	q := strings.TrimSpace(raw)
	if q == "" {
		return nil
	}
	if len(q) > 200 {
		return errs.New(errs.CodeValidationFailed, "q must be at most 200 characters.")
	}
	rx := bson.M{"$regex": regexp.QuoteMeta(q), "$options": "i"}
	base["$or"] = []bson.M{{"name": rx}, {"slug": rx}}
	return nil
}

func cloneFilter(in bson.M) bson.M {
	out := make(bson.M, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// attachOrgCounts fills memberCount and accountCount for the organisations on this page (two aggregations).
func (s *Service) attachOrgCounts(ctx context.Context, rows []Organization) error {
	if len(rows) == 0 || s.Repo == nil || s.Repo.DB == nil {
		return nil
	}
	ids := make([]string, len(rows))
	for i := range rows {
		ids[i] = rows[i].ID
	}
	members, err := s.Repo.CountByOrganization(ctx, MembershipsCollection, ids, nil)
	if err != nil {
		return err
	}
	// Same rule as the account quota: an account being deleted (state "deleting") no longer counts.
	accounts, err := s.Repo.CountByOrganization(ctx, "accounts", ids, bson.M{"state": bson.M{"$ne": "deleting"}})
	if err != nil {
		return err
	}
	for i := range rows {
		rows[i].MemberCount = members[rows[i].ID]
		rows[i].AccountCount = accounts[rows[i].ID]
	}
	return nil
}

func orgSortKey(o Organization, field string) any {
	switch field {
	case "name":
		return o.Name
	case "createdAt":
		return timeKey(o.CreatedAt)
	default:
		return o.ID
	}
}

func orgCursorFilter(p cursorPayload, sortField string, sortDir int) (bson.M, error) {
	cmp := "$lt"
	if sortDir > 0 {
		cmp = "$gt"
	}
	if sortField == "_id" {
		return bson.M{"_id": bson.M{cmp: p.ID}}, nil
	}
	key := p.Key
	if sortField == "createdAt" {
		t, err := parseTimeKey(key)
		if err != nil {
			return nil, errs.New(errs.CodeValidationFailed, "The cursor is invalid.")
		}
		key = t
	}
	return bson.M{"$or": []bson.M{
		{sortField: bson.M{cmp: key}},
		{sortField: key, "_id": bson.M{cmp: p.ID}},
	}}, nil
}

// CreateOrganization is POST /v1/organizations (instance owners only).
func (s *Service) CreateOrganization(ctx context.Context, name, slug string, branding, quotas map[string]any) (*Organization, error) {
	if err := Require(ctx, PermInstanceAdmin); err != nil {
		return nil, err
	}
	name = strings.TrimSpace(name)
	slug = strings.TrimSpace(strings.ToLower(slug))
	if name == "" {
		return nil, errs.New(errs.CodeValidationFailed, "name is required.")
	}
	if !slugPattern.MatchString(slug) {
		return nil, errs.New(errs.CodeValidationFailed, "slug must match [a-z0-9-]{2,48}.")
	}
	if _, err := s.Repo.OrganizationBySlug(ctx, slug); err == nil {
		return nil, errs.New(errs.CodeConflict, "An organisation with this slug already exists.").WithSafe("slug", slug)
	} else if !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	now := time.Now().UTC()
	quotas, err := NormalizeQuotas(quotas)
	if err != nil {
		return nil, err
	}
	o := &Organization{
		ID: store.NewID(store.PrefixOrganization), Slug: slug, Name: name, Status: "active",
		Branding: branding, Quotas: quotas, CreatedAt: now, UpdatedAt: now, Version: 1,
	}
	if err := s.Repo.InsertOrganization(ctx, o); err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return nil, errs.New(errs.CodeConflict, "An organisation with this slug already exists.").WithSafe("slug", slug)
		}
		return nil, fmt.Errorf("tenancy: create organization: %w", err)
	}
	s.audit(ctx, "organization.created", "organization", o.ID, map[string]FieldDiff{
		"name": {After: o.Name}, "slug": {After: o.Slug},
	})
	return o, nil
}

// GetOrganization is GET /v1/organizations/{id}.
func (s *Service) GetOrganization(ctx context.Context, id string) (*Organization, error) {
	if err := s.canAccessOrg(ctx, id, PermRead); err != nil {
		return nil, err
	}
	if !store.IsID(store.PrefixOrganization, id) {
		return nil, errs.New(errs.CodeNotFound, "Organisation not found.").WithSafe("organizationId", id)
	}
	o, err := s.Repo.Organization(ctx, id)
	if errors.Is(err, ErrNotFound) {
		return nil, errs.New(errs.CodeNotFound, "Organisation not found.").WithSafe("organizationId", id)
	}
	return o, err
}

// PatchOrganization is PATCH /v1/organizations/{id}.
func (s *Service) PatchOrganization(ctx context.Context, id string, in OrgPatch, ifMatch string) (*Organization, error) {
	if err := s.canAccessOrg(ctx, id, PermSettingsWrite); err != nil {
		return nil, err
	}
	cur, err := s.GetOrganization(ctx, id)
	if err != nil {
		return nil, err
	}
	if cur.Status == "deleting" {
		return nil, errs.New(errs.CodeConflict, "Organisation is being deleted.")
	}
	if err := checkIfMatch(ifMatch, cur.Version); err != nil {
		return nil, err
	}
	set := bson.M{}
	changes := map[string]FieldDiff{}
	if in.Name != nil {
		name := strings.TrimSpace(*in.Name)
		if name == "" {
			return nil, errs.New(errs.CodeValidationFailed, "name must not be empty.")
		}
		changes["name"] = FieldDiff{Before: cur.Name, After: name}
		set["name"] = name
	}
	if in.Branding != nil {
		changes["branding"] = FieldDiff{Before: cur.Branding, After: in.Branding}
		set["branding"] = in.Branding
	}
	if in.Quotas != nil {
		// Quotas limit the organisation, so only the instance owner may change them (not its own admins).
		if err := Require(ctx, PermInstanceAdmin); err != nil {
			return nil, errs.New(errs.CodeForbidden, "Only the instance owner can change organisation quotas.")
		}
		q, err := NormalizeQuotas(in.Quotas)
		if err != nil {
			return nil, err
		}
		changes["quotas"] = FieldDiff{Before: cur.Quotas, After: q}
		set["quotas"] = q
	}
	if in.Status != nil {
		st := *in.Status
		if st != "active" && st != "suspended" {
			return nil, errs.New(errs.CodeValidationFailed, "status must be active or suspended.")
		}
		changes["status"] = FieldDiff{Before: cur.Status, After: st}
		set["status"] = st
	}
	if len(set) == 0 {
		return cur, nil
	}
	o, err := s.Repo.UpdateOrganization(ctx, id, cur.Version, set)
	if errors.Is(err, ErrNotFound) {
		return nil, errs.New(errs.CodeVersionMismatch, "The resource version does not match If-Match.")
	}
	if err != nil {
		return nil, err
	}
	s.audit(ctx, "organization.updated", "organization", o.ID, changes)
	return o, nil
}

// DeleteOrganization marks the organisation deleting and enqueues an erasure job (S11 §5.2).
// Cascade is out of scope for P0 — the P1 erasure job must: erase every account as in S11 §5.1;
// then delete endpoints, routes, versions, templates, gateways, suppression lists, API keys,
// memberships, settings and integration instances; retain auditLog for EB_RETENTION_AUDIT with
// personal fields reduced to IDs; finally remove the organizations document.
func (s *Service) DeleteOrganization(ctx context.Context, id string, ifMatch string) (*AcceptedJob, error) {
	c, ok := CallerFrom(ctx)
	if !ok {
		return nil, errs.New(errs.CodeUnauthenticated, "Authentication is required.")
	}
	// S2/OpenAPI: instance owners; S10: org owners — allow either.
	if !c.InstanceOwner && !c.Allows(PermOrgDelete) {
		return nil, errs.New(errs.CodeForbidden, "Your role does not allow this operation.").WithSafe("permission", string(PermOrgDelete))
	}
	if s.Jobs == nil {
		return nil, errs.New(errs.CodeFeatureDisabled, "Organisation deletion requires the jobs service.")
	}
	if !store.IsID(store.PrefixOrganization, id) {
		return nil, errs.New(errs.CodeNotFound, "Organisation not found.").WithSafe("organizationId", id)
	}
	cur, err := s.Repo.Organization(ctx, id)
	if errors.Is(err, ErrNotFound) {
		return nil, errs.New(errs.CodeNotFound, "Organisation not found.").WithSafe("organizationId", id)
	}
	if err != nil {
		return nil, err
	}
	if cur.Status == "deleting" {
		return nil, errs.New(errs.CodeConflict, "Organisation is already being deleted.")
	}
	if err := checkIfMatch(ifMatch, cur.Version); err != nil {
		return nil, err
	}
	var jobID string
	now := time.Now().UTC()
	err = s.Repo.DB.WithTx(ctx, func(txCtx context.Context) error {
		if _, err := s.Repo.UpdateOrganization(txCtx, id, cur.Version, bson.M{"status": "deleting"}); err != nil {
			return err
		}
		txCtx = store.WithOrganization(txCtx, id)
		jid, err := s.Jobs(txCtx, "organization.erasure", map[string]string{"organizationId": id})
		if err != nil {
			return err
		}
		jobID = jid
		return nil
	})
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, errs.New(errs.CodeVersionMismatch, "The resource version does not match If-Match.")
		}
		return nil, err
	}
	job := &AcceptedJob{
		ID: jobID, OrganizationID: id, Kind: "organization.erasure", State: "queued", CreatedAt: now,
	}
	s.audit(ctx, "organization.deleted", "organization", id, map[string]FieldDiff{
		"status": {Before: cur.Status, After: "deleting"},
		"jobId":  {After: jobID},
	})
	return job, nil
}

func (s *Service) canAccessOrg(ctx context.Context, orgID string, p Permission) error {
	c, ok := CallerFrom(ctx)
	if !ok {
		return errs.New(errs.CodeUnauthenticated, "Authentication is required.")
	}
	if c.InstanceOwner {
		return nil
	}
	if c.OrganizationID != "" && c.OrganizationID != orgID {
		return errs.New(errs.CodeOrganizationMismatch, "You are not a member of that organisation.")
	}
	if c.OrganizationID == "" {
		return errs.New(errs.CodeForbidden, "Organisation context is required.")
	}
	if p == PermRead {
		return RequireRead(ctx, "organizations")
	}
	return Require(ctx, p)
}
