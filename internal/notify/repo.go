package notify

import (
	"context"
	"errors"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/store"
)

// Repo persists notifications and read marks.
type Repo struct {
	DB *store.DB
}

// EnsureIndexes creates the W75 indexes (notifications, per-user read marks and email records).
func EnsureIndexes(ctx context.Context, db *store.DB) error {
	_, err := db.Collection(Collection).Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "scope", Value: 1}, {Key: "key", Value: 1}}, Options: options.Index().SetUnique(true).SetName("scope_key")},
		{Keys: bson.D{{Key: "scope", Value: 1}, {Key: "state", Value: 1}, {Key: "lastSeen", Value: -1}}, Options: options.Index().SetName("scope_state_lastSeen")},
		{Keys: bson.D{{Key: "organizationId", Value: 1}, {Key: "state", Value: 1}, {Key: "lastSeen", Value: -1}}, Options: options.Index().SetName("org_state_lastSeen")},
		{Keys: bson.D{{Key: "resolvedAt", Value: 1}}, Options: options.Index().SetName("resolved_ttl").
			SetExpireAfterSeconds(90 * 24 * 3600).SetPartialFilterExpression(bson.M{"state": StateResolved})},
	})
	if err != nil {
		return err
	}
	_, err = db.Collection(ReadsCollection).Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "userId", Value: 1}, {Key: "notificationId", Value: 1}}, Options: options.Index().SetUnique(true).SetName("user_notification")},
		{Keys: bson.D{{Key: "userId", Value: 1}}, Options: options.Index().SetName("user")},
		{Keys: bson.D{{Key: "organizationId", Value: 1}}, Options: options.Index().SetName("org")},
	})
	if err != nil {
		return err
	}
	_, err = db.Collection(EmailsCollection).Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "userId", Value: 1}, {Key: "notificationId", Value: 1}}, Options: options.Index().SetUnique(true).SetName("user_notification")},
		{Keys: bson.D{{Key: "userId", Value: 1}, {Key: "digest", Value: 1}, {Key: "emailedAt", Value: -1}}, Options: options.Index().SetName("user_digest")},
		{Keys: bson.D{{Key: "organizationId", Value: 1}}, Options: options.Index().SetName("org")},
		{Keys: bson.D{{Key: "emailedAt", Value: 1}}, Options: options.Index().SetName("emailed_ttl").SetExpireAfterSeconds(90 * 24 * 3600)},
	})
	return err
}

// Migration returns store migration number n (W75).
func Migration(number int) store.Migration {
	return store.Migration{Number: number, Name: "notifications indexes", Up: EnsureIndexes}
}

// UpsertRaise inserts or bumps an open problem; re-opens a resolved key.
func (r *Repo) UpsertRaise(ctx context.Context, p Problem, now time.Time) (n *Notification, created, severityRose bool, err error) {
	org := ""
	if p.Scope != ScopeInstance {
		org = p.Scope
	}
	filter := bson.M{"scope": p.Scope, "key": p.Key}
	var cur Notification
	err = r.DB.Collection(Collection).FindOne(ctx, filter).Decode(&cur)
	if err != nil && !errors.Is(err, mongo.ErrNoDocuments) {
		return nil, false, false, errs.Wrap(err, errs.CodeInternalError, "notify.find")
	}
	if errors.Is(err, mongo.ErrNoDocuments) {
		n = &Notification{
			ID: store.NewID(store.PrefixNotification), Scope: p.Scope, OrganizationID: org,
			Key: p.Key, Category: p.Category, Severity: p.Severity, State: StateOpen,
			TitleKey: p.TitleKey, DetailKey: p.DetailKey, TitleParams: p.TitleParams, DetailParams: p.DetailParams,
			Link: sanitizeLink(p.Link), Data: p.Data, Occurrences: 1, FirstSeen: now, OpenedAt: now, LastSeen: now,
			Fingerprint: p.Fingerprint,
		}
		doc := notificationDoc(n)
		if _, err := r.DB.Collection(Collection).InsertOne(ctx, doc); err != nil {
			if mongo.IsDuplicateKeyError(err) {
				return r.UpsertRaise(ctx, p, now)
			}
			return nil, false, false, errs.Wrap(err, errs.CodeInternalError, "notify.insert")
		}
		return n, true, false, nil
	}
	severityRose = severityRank[p.Severity] > severityRank[cur.Severity]
	set := bson.M{
		"lastSeen": now,
		"titleKey": p.TitleKey, "detailKey": p.DetailKey,
		"titleParams": p.TitleParams, "detailParams": p.DetailParams,
		"link": sanitizeLink(p.Link), "category": p.Category,
	}
	if p.Data != nil {
		set["data"] = p.Data
	}
	if severityRose || cur.State == StateResolved {
		set["severity"] = p.Severity
		set["severityRank"] = severityRank[p.Severity]
	}
	update := bson.M{"$set": set, "$inc": bson.M{"occurrences": 1}}
	if cur.State == StateResolved {
		set["state"] = StateOpen
		set["openedAt"] = now
		update["$unset"] = bson.M{"resolvedAt": ""}
		created = true // treat re-open as channel-worthy
	}
	_, err = r.DB.Collection(Collection).UpdateOne(ctx, bson.M{"_id": cur.ID}, update)
	if err != nil {
		return nil, false, false, errs.Wrap(err, errs.CodeInternalError, "notify.update")
	}
	_ = r.DB.Collection(Collection).FindOne(ctx, bson.M{"_id": cur.ID}).Decode(&cur)
	return &cur, created, severityRose, nil
}

// ResolveKey marks an open problem resolved; nil,nil if missing.
func (r *Repo) ResolveKey(ctx context.Context, scope, key string, now time.Time) (*Notification, error) {
	var cur Notification
	err := r.DB.Collection(Collection).FindOne(ctx, bson.M{"scope": scope, "key": key}).Decode(&cur)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, nil
	}
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "notify.find")
	}
	if cur.State == StateResolved {
		return &cur, nil
	}
	resAt := now
	_, err = r.DB.Collection(Collection).UpdateOne(ctx, bson.M{"_id": cur.ID}, bson.M{
		"$set": bson.M{"state": StateResolved, "resolvedAt": resAt, "lastSeen": now},
	})
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "notify.resolve")
	}
	cur.State = StateResolved
	cur.ResolvedAt = &resAt
	cur.LastSeen = now
	return &cur, nil
}

// GetByID loads one notification.
func (r *Repo) GetByID(ctx context.Context, id string) (*Notification, error) {
	var n Notification
	err := r.DB.Collection(Collection).FindOne(ctx, bson.M{"_id": id}).Decode(&n)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, errs.New(errs.CodeNotFound, "The notification does not exist.")
	}
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "notify.get")
	}
	return &n, nil
}

// List returns a page visible to org (+ optional instance).
func (r *Repo) List(ctx context.Context, org string, includeInstance bool, f ListFilter, userID string, muted []string) (*Page, error) {
	q := visibilityFilter(org, includeInstance)
	if f.State != "" {
		q["state"] = f.State
	} else {
		q["state"] = StateOpen
	}
	if f.Category != "" {
		q["category"] = f.Category
	}
	if f.Severity != "" {
		q["severity"] = f.Severity
	}
	if f.From != nil {
		q["lastSeen"] = bson.M{"$gte": f.From.UTC()}
	}
	if f.To != nil {
		clause, _ := q["lastSeen"].(bson.M)
		if clause == nil {
			clause = bson.M{}
		}
		clause["$lte"] = f.To.UTC()
		q["lastSeen"] = clause
	}
	if len(muted) > 0 {
		q["category"] = bson.M{"$nin": muted}
		if f.Category != "" {
			for _, m := range muted {
				if m == f.Category {
					return &Page{Data: []Notification{}, Page: f.Page, Limit: f.Limit}, nil
				}
			}
			q["category"] = f.Category
		}
	}
	reads, _ := r.readSet(ctx, userID)
	if f.Unread != nil && userID != "" {
		ids := make([]string, 0, len(reads))
		for id := range reads {
			ids = append(ids, id)
		}
		if *f.Unread {
			if len(ids) > 0 {
				q["_id"] = bson.M{"$nin": ids}
			}
		} else if len(ids) == 0 {
			return &Page{Data: []Notification{}, Page: max(f.Page, 1), Limit: clampLimit(f.Limit)}, nil
		} else {
			q["_id"] = bson.M{"$in": ids}
		}
	}
	limit := clampLimit(f.Limit)
	page := f.Page
	if page <= 0 {
		page = 1
	}
	sort := sortSpec(f.Sort)
	opts := options.Find().SetSort(sort).SetSkip(int64((page - 1) * limit)).SetLimit(int64(limit))
	cur, err := r.DB.Collection(Collection).Find(ctx, q, opts)
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "notify.list")
	}
	defer func() { _ = cur.Close(ctx) }()
	var rows []Notification
	if err := cur.All(ctx, &rows); err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "notify.list")
	}
	if rows == nil {
		rows = []Notification{}
	}
	for i := range rows {
		_, ok := reads[rows[i].ID]
		rows[i].Unread = !ok
	}
	out := &Page{Data: rows, Page: page, Limit: limit}
	if f.IncludeTotal {
		n, err := r.DB.Collection(Collection).CountDocuments(ctx, q)
		if err != nil {
			return nil, errs.Wrap(err, errs.CodeInternalError, "notify.count")
		}
		out.Total = &n
	}
	return out, nil
}

func clampLimit(limit int) int {
	if limit <= 0 || limit > 200 {
		return 50
	}
	return limit
}

// notificationDoc is the BSON form with severityRank for sort.
func notificationDoc(n *Notification) bson.M {
	doc := bson.M{
		"_id": n.ID, "scope": n.Scope, "key": n.Key, "category": n.Category,
		"severity": n.Severity, "severityRank": severityRank[n.Severity], "state": n.State,
		"titleKey": n.TitleKey, "detailKey": n.DetailKey, "occurrences": n.Occurrences,
		"firstSeen": n.FirstSeen, "openedAt": n.OpenedAt, "lastSeen": n.LastSeen,
	}
	if n.OrganizationID != "" {
		doc["organizationId"] = n.OrganizationID
	}
	if n.TitleParams != nil {
		doc["titleParams"] = n.TitleParams
	}
	if n.DetailParams != nil {
		doc["detailParams"] = n.DetailParams
	}
	if n.Link != "" {
		doc["link"] = n.Link
	}
	if n.Data != nil {
		doc["data"] = n.Data
	}
	if n.Fingerprint != "" {
		doc["fingerprint"] = n.Fingerprint
	}
	return doc
}

func visibilityFilter(org string, includeInstance bool) bson.M {
	if includeInstance {
		return bson.M{"$or": []bson.M{
			{"organizationId": org},
			{"scope": ScopeInstance},
		}}
	}
	return bson.M{"organizationId": org}
}

func sortSpec(sort string) bson.D {
	switch sort {
	case "lastSeen":
		return bson.D{{Key: "lastSeen", Value: 1}}
	case "firstSeen":
		return bson.D{{Key: "firstSeen", Value: 1}}
	case "-firstSeen":
		return bson.D{{Key: "firstSeen", Value: -1}}
	case "severity":
		// rank: info(1) < warning(2) < critical(3) — stored as severityRank on write
		return bson.D{{Key: "severityRank", Value: 1}, {Key: "lastSeen", Value: -1}}
	case "-severity":
		return bson.D{{Key: "severityRank", Value: -1}, {Key: "lastSeen", Value: -1}}
	default:
		return bson.D{{Key: "lastSeen", Value: -1}}
	}
}

// SummaryCounts returns unread/open totals for the bell.
func (r *Repo) SummaryCounts(ctx context.Context, org, userID string, includeInstance bool, muted []string) (*Summary, error) {
	q := visibilityFilter(org, includeInstance)
	q["state"] = StateOpen
	if len(muted) > 0 {
		q["category"] = bson.M{"$nin": muted}
	}
	cur, err := r.DB.Collection(Collection).Find(ctx, q, options.Find().SetProjection(bson.M{"_id": 1, "severity": 1}))
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "notify.summary")
	}
	defer func() { _ = cur.Close(ctx) }()
	reads, _ := r.readSet(ctx, userID)
	sum := &Summary{BySeverity: map[string]int{SeverityInfo: 0, SeverityWarning: 0, SeverityCritical: 0}}
	for cur.Next(ctx) {
		var row struct {
			ID       string `bson:"_id"`
			Severity string `bson:"severity"`
		}
		if err := cur.Decode(&row); err != nil {
			continue
		}
		sum.Open++
		sum.BySeverity[row.Severity]++
		if _, ok := reads[row.ID]; !ok {
			sum.Unread++
		}
	}
	return sum, cur.Err()
}

func (r *Repo) readSet(ctx context.Context, userID string) (map[string]struct{}, error) {
	out := map[string]struct{}{}
	if userID == "" {
		return out, nil
	}
	cur, err := r.DB.Collection(ReadsCollection).Find(ctx, bson.M{"userId": userID},
		options.Find().SetProjection(bson.M{"notificationId": 1}))
	if err != nil {
		return out, err
	}
	defer func() { _ = cur.Close(ctx) }()
	for cur.Next(ctx) {
		var row struct {
			NID string `bson:"notificationId"`
		}
		if cur.Decode(&row) == nil {
			out[row.NID] = struct{}{}
		}
	}
	return out, nil
}

// MarkRead records read marks for the user.
func (r *Repo) MarkRead(ctx context.Context, userID, org string, ids []string, now time.Time) error {
	for _, id := range ids {
		if !store.IsID(store.PrefixNotification, id) {
			continue
		}
		doc := ReadMark{
			ID: userID + ":" + id, UserID: userID, NotificationID: id,
			OrganizationID: org, ReadAt: now,
		}
		_, err := r.DB.Collection(ReadsCollection).UpdateOne(ctx,
			bson.M{"userId": userID, "notificationId": id},
			bson.M{"$set": doc},
			options.UpdateOne().SetUpsert(true),
		)
		if err != nil {
			return errs.Wrap(err, errs.CodeInternalError, "notify.markRead")
		}
	}
	return nil
}

// MarkAllRead marks every visible open notification as read.
func (r *Repo) MarkAllRead(ctx context.Context, userID, org string, includeInstance bool, muted []string, now time.Time) error {
	q := visibilityFilter(org, includeInstance)
	q["state"] = StateOpen
	if len(muted) > 0 {
		q["category"] = bson.M{"$nin": muted}
	}
	cur, err := r.DB.Collection(Collection).Find(ctx, q, options.Find().SetProjection(bson.M{"_id": 1}))
	if err != nil {
		return errs.Wrap(err, errs.CodeInternalError, "notify.markAll")
	}
	defer func() { _ = cur.Close(ctx) }()
	var ids []string
	for cur.Next(ctx) {
		var row struct {
			ID string `bson:"_id"`
		}
		if cur.Decode(&row) == nil {
			ids = append(ids, row.ID)
		}
	}
	return r.MarkRead(ctx, userID, org, ids, now)
}

// OpenKeys returns open keys for a scope prefix (watchers).
func (r *Repo) OpenKeys(ctx context.Context, category string) (map[string]string, error) {
	// key → scope
	out := map[string]string{}
	q := bson.M{"state": StateOpen}
	if category != "" {
		q["category"] = category
	}
	cur, err := r.DB.Collection(Collection).Find(ctx, q, options.Find().SetProjection(bson.M{"key": 1, "scope": 1}))
	if err != nil {
		return nil, err
	}
	defer func() { _ = cur.Close(ctx) }()
	for cur.Next(ctx) {
		var row struct {
			Key, Scope string
		}
		if cur.Decode(&row) == nil {
			out[row.Scope+"\x00"+row.Key] = row.Scope
		}
	}
	return out, nil
}

// InstanceOwnerOrgs returns organisation ids that have an instance-owner member.
func (r *Repo) InstanceOwnerOrgs(ctx context.Context) ([]string, error) {
	cur, err := r.DB.Collection("users").Find(ctx, bson.M{"instanceRole": "owner"},
		options.Find().SetProjection(bson.M{"_id": 1}))
	if err != nil {
		return nil, err
	}
	defer func() { _ = cur.Close(ctx) }()
	var userIDs []string
	for cur.Next(ctx) {
		var u struct {
			ID string `bson:"_id"`
		}
		if cur.Decode(&u) == nil {
			userIDs = append(userIDs, u.ID)
		}
	}
	if len(userIDs) == 0 {
		return nil, nil
	}
	mcur, err := r.DB.Collection("memberships").Find(ctx, bson.M{"userId": bson.M{"$in": userIDs}},
		options.Find().SetProjection(bson.M{"organizationId": 1}))
	if err != nil {
		return nil, err
	}
	defer func() { _ = mcur.Close(ctx) }()
	seen := map[string]bool{}
	var orgs []string
	for mcur.Next(ctx) {
		var m struct {
			Org string `bson:"organizationId"`
		}
		if mcur.Decode(&m) == nil && m.Org != "" && !seen[m.Org] {
			seen[m.Org] = true
			orgs = append(orgs, m.Org)
		}
	}
	return orgs, nil
}

func sanitizeLink(link string) string {
	link = strings.TrimSpace(link)
	if link == "" {
		return ""
	}
	if !strings.HasPrefix(link, "/") || strings.HasPrefix(link, "//") {
		return ""
	}
	// Rewrite legacy admin paths to routes that exist today.
	switch {
	case link == "/organization/api-keys" || strings.HasPrefix(link, "/organization/api-keys/"):
		return "/api-keys"
	case link == "/webhooks" || strings.HasPrefix(link, "/webhooks/"):
		return "/webhooks" // admin has a list page only (no /webhooks/:id)
	}
	return link
}

// OpenGauge counts open problems by category/severity for metrics.
func (r *Repo) OpenGauge(ctx context.Context) (map[[2]string]int, error) {
	cur, err := r.DB.Collection(Collection).Aggregate(ctx, mongo.Pipeline{
		{{Key: "$match", Value: bson.M{"state": StateOpen}}},
		{{Key: "$group", Value: bson.M{
			"_id": bson.M{"category": "$category", "severity": "$severity"},
			"n":   bson.M{"$sum": 1},
		}}},
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = cur.Close(ctx) }()
	out := map[[2]string]int{}
	for cur.Next(ctx) {
		var row struct {
			ID struct {
				Category, Severity string
			} `bson:"_id"`
			N int
		}
		if cur.Decode(&row) == nil {
			out[[2]string{row.ID.Category, row.ID.Severity}] = row.N
		}
	}
	return out, cur.Err()
}
