package tenancy

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/store"
)

// Job kind queued by DeleteOrganization (S11 §5.2).
const OrganizationErasureKind = "organization.erasure"

// eraseByOrganizationID is derived from CollectionRegistry (W34). Special cases
// (settings, jobs, organizations, userSettings orphans, audit scrub) stay in handle.
var eraseByOrganizationID = OrgEraseCollections()

// ErasureHandler is the cycle-free job-handler shape RegisterErasure expects. Claude adapts
// jobs.Service.Handle in cmd/ so tenancy never imports internal/jobs.
type ErasureHandler func(
	ctx context.Context,
	jobID string,
	payload json.RawMessage,
	progress func(done, total int64),
	cancelled func() bool,
) (any, error)

// RegisterFunc registers a job kind (adapter around jobs.Service.Handle).
type RegisterFunc func(kind string, h ErasureHandler)

// ErasureDeps holds collaborators for organisation erasure. Interfaces are funcs so tenancy
// imports none of accounts, apikeys or jobs.
type ErasureDeps struct {
	DB    *store.DB
	Audit Auditor

	// ListAccountIDs returns every account of the organisation (including state deleting).
	ListAccountIDs func(ctx context.Context, orgID string) ([]string, error)
	// EraseAccount queues account erasure (S11 §5.1) and waits until it finishes.
	EraseAccount func(ctx context.Context, accountID, orgID string, cancelled func() bool) error
	// RevokeAPIKeys revokes every API key of the organisation (Limen + cache + metadata).
	RevokeAPIKeys func(ctx context.Context, orgID string) (revoked int64, err error)

	// OrgObjectCleanups run after account erasure and API-key revoke, before Mongo org
	// collections are wiped (W68 branding S3, …). Errors fail the job so it can retry.
	OrgObjectCleanups []func(ctx context.Context, orgID string) error

	// AfterStep, when set, is called after each major step (tests inject a crash).
	AfterStep func(step string) error
}

type orgErasurePayload struct {
	OrganizationID string `json:"organizationId"`
}

// RegisterErasure wires the organization.erasure handler without importing internal/jobs.
func RegisterErasure(register RegisterFunc, deps ErasureDeps) {
	if register == nil {
		return
	}
	register(OrganizationErasureKind, deps.handle)
}

func (d ErasureDeps) handle(
	ctx context.Context,
	jobID string,
	payload json.RawMessage,
	progress func(done, total int64),
	cancelled func() bool,
) (any, error) {
	if d.DB == nil {
		return nil, errs.New(errs.CodeInternalError, "Organisation erasure requires a database.")
	}
	if cancelled == nil {
		cancelled = func() bool { return false }
	}
	if progress == nil {
		progress = func(int64, int64) {}
	}

	var p orgErasurePayload
	if err := json.Unmarshal(payload, &p); err != nil || p.OrganizationID == "" {
		return nil, errs.New(errs.CodeValidationFailed, "organization.erasure payload requires organizationId.")
	}
	orgID := p.OrganizationID
	ctx = store.WithOrganization(ctx, orgID)

	accountIDs := []string{}
	if d.ListAccountIDs != nil {
		ids, err := d.ListAccountIDs(ctx, orgID)
		if err != nil {
			return nil, errs.Wrap(err, errs.CodeInternalError, "tenancy.erase.listAccounts")
		}
		accountIDs = ids
	}

	// accounts + revoke keys + object cleanups + each org collection + jobs + settings + userSettings orphans + audit scrub + org doc
	total := int64(len(accountIDs) + 1 + len(d.OrgObjectCleanups) + len(eraseByOrganizationID) + 5)
	var done int64
	report := func() { progress(done, total) }
	report()

	counts := map[string]int64{}
	step := func(name string) error {
		if cancelled() {
			return errs.New(errs.CodeConflict, "Organisation erasure cancelled.")
		}
		if d.AfterStep != nil {
			if err := d.AfterStep(name); err != nil {
				return err
			}
		}
		return nil
	}

	memberUserIDs, err := d.listMemberUserIDs(ctx, orgID)
	if err != nil {
		return nil, err
	}

	for _, accID := range accountIDs {
		if err := step("account:" + accID); err != nil {
			return nil, err
		}
		if d.EraseAccount != nil {
			if err := d.EraseAccount(ctx, accID, orgID, cancelled); err != nil {
				return nil, err
			}
		}
		counts["accounts"]++
		done++
		report()
	}

	if err := step("apiKeys.revoke"); err != nil {
		return nil, err
	}
	if d.RevokeAPIKeys != nil {
		n, err := d.RevokeAPIKeys(ctx, orgID)
		if err != nil {
			return nil, errs.Wrap(err, errs.CodeInternalError, "tenancy.erase.revokeAPIKeys")
		}
		counts["apiKeysRevoked"] = n
	}
	done++
	report()

	for i, clean := range d.OrgObjectCleanups {
		name := "objects:" + strconv.Itoa(i)
		if err := step(name); err != nil {
			return nil, err
		}
		if clean != nil {
			if err := clean(ctx, orgID); err != nil {
				return nil, errs.Wrap(err, errs.CodeInternalError, "tenancy.erase.objects")
			}
		}
		counts["objectCleanups"]++
		done++
		report()
	}

	for _, coll := range eraseByOrganizationID {
		if err := step("coll:" + coll); err != nil {
			return nil, err
		}
		n, err := d.deleteByOrg(ctx, coll, orgID)
		if err != nil {
			return nil, err
		}
		counts[coll] = n
		done++
		report()
	}

	if err := step("jobs"); err != nil {
		return nil, err
	}
	n, err := d.deleteOrgJobs(ctx, orgID, jobID)
	if err != nil {
		return nil, err
	}
	counts["jobs"] = n
	done++
	report()

	if err := step("settings"); err != nil {
		return nil, err
	}
	n, err = d.deleteOrgSettings(ctx, orgID)
	if err != nil {
		return nil, err
	}
	counts["settings"] = n
	done++
	report()

	if err := step("userSettings.orphans"); err != nil {
		return nil, err
	}
	n, err = d.deleteOrphanUserSettings(ctx, memberUserIDs)
	if err != nil {
		return nil, err
	}
	counts["userSettings"] = n
	done++
	report()

	if err := step("audit.scrub"); err != nil {
		return nil, err
	}
	n, err = d.reduceAuditPersonal(ctx, orgID)
	if err != nil {
		return nil, err
	}
	counts["auditScrubbed"] = n
	done++
	report()

	if err := step("organization"); err != nil {
		return nil, err
	}
	n, err = d.deleteOrganizationDoc(ctx, orgID)
	if err != nil {
		return nil, err
	}
	counts["organization"] = n
	done++
	report()

	if d.Audit != nil {
		_ = d.Audit.Record(ctx, "organization.erased", "organization", orgID, map[string]FieldDiff{
			"counts": {After: counts},
		})
	}
	return counts, nil
}

func (d ErasureDeps) deleteByOrg(ctx context.Context, coll, orgID string) (int64, error) {
	res, err := d.DB.Collection(coll).DeleteMany(ctx, bson.M{"organizationId": orgID})
	if err != nil {
		// Collection may not exist yet — treat as empty.
		return 0, nil
	}
	return res.DeletedCount, nil
}

func (d ErasureDeps) deleteOrgJobs(ctx context.Context, orgID, keepJobID string) (int64, error) {
	filter := bson.M{"organizationId": orgID}
	if keepJobID != "" {
		filter["_id"] = bson.M{"$ne": keepJobID}
	}
	res, err := d.DB.Collection("jobs").DeleteMany(ctx, filter)
	if err != nil {
		return 0, nil
	}
	return res.DeletedCount, nil
}

func (d ErasureDeps) deleteOrgSettings(ctx context.Context, orgID string) (int64, error) {
	res, err := d.DB.Collection("settings").DeleteOne(ctx, bson.M{"_id": "org:" + orgID})
	if err != nil {
		return 0, nil
	}
	return res.DeletedCount, nil
}

func (d ErasureDeps) listMemberUserIDs(ctx context.Context, orgID string) ([]string, error) {
	if d.DB == nil || d.DB.DB == nil {
		return nil, nil
	}
	cur, err := d.DB.Collection(MembershipsCollection).Find(ctx, bson.M{
		"organizationId": orgID, "userId": bson.M{"$type": "string"},
	})
	if err != nil {
		return nil, nil
	}
	defer func() { _ = cur.Close(ctx) }()
	seen := map[string]struct{}{}
	var ids []string
	for cur.Next(ctx) {
		var row bson.M
		if err := cur.Decode(&row); err != nil {
			continue
		}
		uid, _ := row["userId"].(string)
		if uid == "" {
			continue
		}
		if _, ok := seen[uid]; ok {
			continue
		}
		seen[uid] = struct{}{}
		ids = append(ids, uid)
	}
	return ids, nil
}

// deleteOrphanUserSettings removes userSettings for former members who have no memberships left.
func (d ErasureDeps) deleteOrphanUserSettings(ctx context.Context, userIDs []string) (int64, error) {
	if d.DB == nil || d.DB.DB == nil || len(userIDs) == 0 {
		return 0, nil
	}
	var n int64
	for _, uid := range userIDs {
		left, err := d.DB.Collection(MembershipsCollection).CountDocuments(ctx, bson.M{"userId": uid})
		if err != nil || left > 0 {
			continue
		}
		res, err := d.DB.Collection("userSettings").DeleteOne(ctx, bson.M{"_id": uid})
		if err != nil {
			continue
		}
		n += res.DeletedCount
	}
	return n, nil
}

func (d ErasureDeps) deleteOrganizationDoc(ctx context.Context, orgID string) (int64, error) {
	res, err := d.DB.Collection(OrganizationsCollection).DeleteOne(ctx, bson.M{"_id": orgID})
	if err != nil {
		return 0, errs.Wrap(err, errs.CodeInternalError, "tenancy.erase.organization")
	}
	return res.DeletedCount, nil
}

// reduceAuditPersonal keeps auditLog rows for EB_RETENTION_AUDIT but strips personal fields down to IDs (S11 §5.2).
func (d ErasureDeps) reduceAuditPersonal(ctx context.Context, orgID string) (int64, error) {
	coll := d.DB.Collection("auditLog")
	cur, err := coll.Find(ctx, bson.M{"organizationId": orgID})
	if err != nil {
		return 0, nil
	}
	defer func() { _ = cur.Close(ctx) }()

	var n int64
	for cur.Next(ctx) {
		var raw bson.M
		if err := cur.Decode(&raw); err != nil {
			return n, errs.Wrap(err, errs.CodeInternalError, "tenancy.erase.audit.decode")
		}
		id, _ := raw["_id"].(string)
		if id == "" {
			continue
		}
		set := bson.M{}
		unset := bson.M{"actor.ip": "", "actor.userAgent": ""}
		if changes := asMap(raw["changes"]); changes != nil {
			set["changes"] = scrubChanges(changes)
		}
		update := bson.M{"$unset": unset}
		if len(set) > 0 {
			update["$set"] = set
		}
		if _, err := coll.UpdateOne(ctx, bson.M{"_id": id}, update); err != nil {
			return n, errs.Wrap(err, errs.CodeInternalError, "tenancy.erase.audit.scrub")
		}
		n++
	}
	if err := cur.Err(); err != nil {
		return n, errs.Wrap(err, errs.CodeInternalError, "tenancy.erase.audit.cursor")
	}
	return n, nil
}

func asMap(v any) bson.M {
	switch x := v.(type) {
	case bson.M:
		return x
	case map[string]any:
		return bson.M(x)
	case bson.D:
		m := make(bson.M, len(x))
		for _, e := range x {
			m[e.Key] = e.Value
		}
		return m
	default:
		return nil
	}
}

func scrubChanges(changes bson.M) bson.M {
	out := bson.M{}
	for k, v := range changes {
		fc := asMap(v)
		if fc == nil {
			out[k] = v
			continue
		}
		scrubbed := bson.M{}
		if b, ok := fc["before"]; ok {
			scrubbed["before"] = scrubValue(b)
		}
		if a, ok := fc["after"]; ok {
			scrubbed["after"] = scrubValue(a)
		}
		out[k] = scrubbed
	}
	return out
}

func scrubValue(v any) any {
	switch x := v.(type) {
	case string:
		if looksPersonal(x) {
			return idOnly(x)
		}
		return x
	case bson.M, map[string]any, bson.D:
		m := asMap(x)
		out := bson.M{}
		for k, vv := range m {
			out[k] = scrubValue(vv)
		}
		return out
	case bson.A:
		a := make(bson.A, len(x))
		for i, vv := range x {
			a[i] = scrubValue(vv)
		}
		return a
	case []any:
		a := make([]any, len(x))
		for i, vv := range x {
			a[i] = scrubValue(vv)
		}
		return a
	default:
		return v
	}
}

func looksPersonal(s string) bool {
	if strings.Contains(s, "@") {
		return true
	}
	// bare display names / free text longer than an ID prefix form are not kept
	if len(s) > 0 && !store.IsID(store.PrefixUser, s) && !store.IsID(store.PrefixOrganization, s) &&
		!store.IsID(store.PrefixMembership, s) && !store.IsID(store.PrefixAccount, s) &&
		!store.IsID(store.PrefixAPIKey, s) && !store.IsID(store.PrefixJob, s) &&
		!strings.HasPrefix(s, "org:") && s != "active" && s != "deleting" && s != "suspended" {
		// keep short enums / known non-personal tokens; drop longer free text
		if len(s) > 48 || strings.Contains(s, " ") {
			return true
		}
	}
	return false
}

func idOnly(s string) string {
	for _, p := range []store.Prefix{
		store.PrefixUser, store.PrefixOrganization, store.PrefixMembership,
		store.PrefixAccount, store.PrefixAPIKey, store.PrefixJob,
	} {
		if store.IsID(p, s) {
			return s
		}
	}
	return ""
}
