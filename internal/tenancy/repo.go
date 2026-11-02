package tenancy

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/radiobros/emailbridge/internal/store"
)

// Collection names (S1 §2).
const (
	OrganizationsCollection = "organizations"
	UsersCollection         = "users"
	MembershipsCollection   = "memberships"
)

// Organization is S1 `organizations`.
type Organization struct {
	ID        string         `bson:"_id" json:"id" doc:"Organisation ID (org_…)." example:"org_01m5caqpk0kast63yc13wpy2d9"`
	Slug      string         `bson:"slug" json:"slug" doc:"URL-safe short name, unique on the instance ([a-z0-9-]{2,48}), derived from the name." example:"acme-legal"`
	Name      string         `bson:"name" json:"name" doc:"Display name." example:"Acme Legal"`
	Status    string         `bson:"status" json:"status" doc:"active, suspended (set by the instance owner), or deleting while the erasure job runs." example:"active"`
	Quotas    map[string]any `bson:"quotas,omitempty" json:"quotas,omitempty" doc:"Organisation quotas set by the instance owner: accounts, sendsPerDay, sendsPerAccountMinute, endpoints; enforced in addition to the instance-wide EB_QUOTA_* limits (the stricter wins). Absent or 0 = no organisation limit." example:"{\"accounts\":50,\"sendsPerDay\":20000}"`
	Branding  map[string]any `bson:"branding,omitempty" json:"branding,omitempty" doc:"Hosted connect-page branding (brandName, logo asset IDs, palette); managed through /v1/connect/branding." example:"{\"brandName\":\"Acme Mail\",\"logoAssetId\":\"bas_01m5cez6h0hw36zejf1j2kxhgq\"}"`
	CreatedAt time.Time      `bson:"createdAt" json:"createdAt" doc:"Creation time (RFC 3339, UTC)." example:"2026-10-20T09:00:00Z"`
	UpdatedAt time.Time      `bson:"updatedAt" json:"updatedAt" doc:"Last change (RFC 3339, UTC)." example:"2026-10-24T14:32:10Z"`
	Version   int64          `bson:"version" json:"version" doc:"Optimistic-concurrency version, incremented on every change." example:"3"`
	// MemberCount and AccountCount are list-view fields (not stored); filled by ListOrganizations.
	MemberCount  int64 `bson:"-" json:"memberCount" doc:"Number of memberships (including pending invitations) in this organisation. List responses only." example:"4"`
	AccountCount int64 `bson:"-" json:"accountCount" doc:"Number of non-deleted mail accounts in this organisation. List responses only." example:"12"`
}

// User is the EmailBridge profile of a Limen user (S1 `users`).
type User struct {
	ID                 string     `bson:"_id" json:"id" doc:"User ID (usr_…)." example:"usr_01m5ccvej0dqefkn0xhkcq2q6z"`
	AuthUserID         string     `bson:"authUserId" json:"-"`
	Email              string     `bson:"email" json:"email" doc:"Sign-in email address." example:"maria.rossi@example.com"`
	Name               string     `bson:"name" json:"name" doc:"Display name." example:"Maria Rossi"`
	Locale             string     `bson:"locale" json:"locale" doc:"UI and notification language: en, it, es, fr, de, pt, ru, zh or ja." example:"it"`
	InstanceRole       string     `bson:"instanceRole" json:"instanceRole" doc:"owner for the instance owner (platform administrator), otherwise none." example:"none"` // owner | none
	Status             string     `bson:"status" json:"status" doc:"active or disabled." example:"active"`
	LastOrganizationID string     `bson:"lastOrganizationId,omitempty" json:"-"`
	LastLoginAt        *time.Time `bson:"lastLoginAt,omitempty" json:"lastLoginAt,omitempty" doc:"Last successful sign-in (RFC 3339, UTC)." example:"2026-10-26T08:15:42Z"`
	CreatedAt          time.Time  `bson:"createdAt" json:"createdAt" doc:"Creation time (RFC 3339, UTC)." example:"2026-10-20T09:00:00Z"`
	UpdatedAt          time.Time  `bson:"updatedAt" json:"updatedAt" doc:"Last change (RFC 3339, UTC)." example:"2026-10-24T14:32:10Z"`
	Version            int64      `bson:"version" json:"version" doc:"Optimistic-concurrency version." example:"2"`
}

// Invite is the pending-invitation payload on a membership (S1, S10 §2.4). Secrets stay out of JSON.
type Invite struct {
	Email     string    `bson:"email" json:"-"`
	TokenHash string    `bson:"tokenHash" json:"-"`
	ExpiresAt time.Time `bson:"expiresAt" json:"-"`
}

// Membership is S1 `memberships`.
type Membership struct {
	ID             string     `bson:"_id" json:"id" doc:"Membership ID (mem_…); also the member ID in /v1/members/{memberId}." example:"mem_01m5ch2yg0fs9wazx4v0ck93t7"`
	OrganizationID string     `bson:"organizationId" json:"organizationId" doc:"Organisation ID." example:"org_01m5caqpk0kast63yc13wpy2d9"`
	UserID         string     `bson:"userId,omitempty" json:"userId,omitempty" doc:"User ID; absent while the invitation is pending." example:"usr_01m5ccvej0dqefkn0xhkcq2q6z"`
	Email          string     `bson:"email,omitempty" json:"email,omitempty" doc:"Member (or invitee) email address." example:"maria.rossi@example.com"`
	Role           Role       `bson:"role" json:"role" doc:"Role in the organisation: owner, admin, operator or viewer." example:"operator"`
	InvitedBy      string     `bson:"invitedBy,omitempty" json:"invitedBy,omitempty" doc:"User ID of the member who sent the invitation." example:"usr_01m5ck6pf088s9k9jty8fqvx0g"`
	AcceptedAt     *time.Time `bson:"acceptedAt,omitempty" json:"acceptedAt,omitempty" doc:"When the invitation was accepted; absent while pending." example:"2026-10-21T10:04:00Z"`
	Invite         *Invite    `bson:"invite,omitempty" json:"-"`
	// Name and LastLoginAt come from the member's user profile when listed; not stored here.
	Name        string     `bson:"-" json:"name,omitempty" doc:"Display name from the member's profile (listings only)." example:"Maria Rossi"`
	LastLoginAt *time.Time `bson:"-" json:"lastLoginAt,omitempty" doc:"Member's last sign-in (listings only)." example:"2026-10-26T08:15:42Z"`
	CreatedAt   time.Time  `bson:"createdAt" json:"createdAt" doc:"Creation (invitation) time." example:"2026-10-20T09:30:00Z"`
	UpdatedAt   time.Time  `bson:"updatedAt" json:"updatedAt" doc:"Last change." example:"2026-10-21T10:04:00Z"`
	Version     int64      `bson:"version" json:"version" doc:"Optimistic-concurrency version." example:"2"`
}

// ErrNotFound is returned for missing documents.
var ErrNotFound = errors.New("tenancy: not found")

// Repo is the MongoDB access of this package.
type Repo struct{ DB *store.DB }

// Migration creates the tenancy indexes.
func Migration(number int) store.Migration {
	return store.Migration{Number: number, Name: "tenancy indexes", Up: func(ctx context.Context, db *store.DB) error {
		uniq := func(keys bson.D) mongoIndex { return mongoIndex{keys, true} }
		for coll, idx := range map[string][]mongoIndex{
			OrganizationsCollection: {uniq(bson.D{{Key: "slug", Value: 1}})},
			UsersCollection:         {uniq(bson.D{{Key: "email", Value: 1}}), uniq(bson.D{{Key: "authUserId", Value: 1}})},
			MembershipsCollection:   {{bson.D{{Key: "userId", Value: 1}}, false}, {bson.D{{Key: "organizationId", Value: 1}, {Key: "_id", Value: 1}}, false}},
		} {
			for _, ix := range idx {
				m := mongo.IndexModel{Keys: ix.keys}
				if ix.unique {
					m.Options = options.Index().SetUnique(true)
				}
				if err := db.EnsureIndexes(ctx, coll, m); err != nil {
					return err
				}
			}
		}
		// one membership per user and organisation (pending invitations have no userId yet)
		if err := db.EnsureIndexes(ctx, MembershipsCollection, mongo.IndexModel{
			Keys:    bson.D{{Key: "organizationId", Value: 1}, {Key: "userId", Value: 1}},
			Options: options.Index().SetUnique(true).SetPartialFilterExpression(bson.M{"userId": bson.M{"$type": "string"}}),
		}); err != nil {
			return err
		}
		// one pending invite per email per organisation; unique invite token hashes
		if err := db.EnsureIndexes(ctx, MembershipsCollection, mongo.IndexModel{
			Keys:    bson.D{{Key: "organizationId", Value: 1}, {Key: "invite.email", Value: 1}},
			Options: options.Index().SetUnique(true).SetPartialFilterExpression(bson.M{"invite.email": bson.M{"$type": "string"}}),
		}); err != nil {
			return err
		}
		return db.EnsureIndexes(ctx, MembershipsCollection, mongo.IndexModel{
			Keys:    bson.D{{Key: "invite.tokenHash", Value: 1}},
			Options: options.Index().SetUnique(true).SetPartialFilterExpression(bson.M{"invite.tokenHash": bson.M{"$type": "string"}}),
		})
	}}
}

type mongoIndex struct {
	keys   bson.D
	unique bool
}

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

// Slugify turns a name into an organisation slug (S1: [a-z0-9-]{2,48}).
func Slugify(name string) string {
	s := strings.Trim(slugRe.ReplaceAllString(strings.ToLower(name), "-"), "-")
	if len(s) > 48 {
		s = strings.Trim(s[:48], "-")
	}
	if len(s) < 2 {
		s = "org-" + s
	}
	return s
}

// CreateOrganization inserts an organisation.
func (r *Repo) CreateOrganization(ctx context.Context, name, slug string) (*Organization, error) {
	now := time.Now().UTC()
	o := &Organization{ID: store.NewID(store.PrefixOrganization), Slug: slug, Name: name, Status: "active", CreatedAt: now, UpdatedAt: now, Version: 1}
	if _, err := r.DB.Collection(OrganizationsCollection).InsertOne(ctx, o); err != nil {
		return nil, fmt.Errorf("tenancy: create organization: %w", err)
	}
	return o, nil
}

// Organization returns an organisation by ID.
func (r *Repo) Organization(ctx context.Context, id string) (*Organization, error) {
	var o Organization
	err := r.DB.Collection(OrganizationsCollection).FindOne(ctx, bson.M{"_id": id}).Decode(&o)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, ErrNotFound
	}
	return &o, err
}

// CreateUser inserts a user profile.
func (r *Repo) CreateUser(ctx context.Context, u *User) error {
	now := time.Now().UTC()
	if u.ID == "" {
		u.ID = store.NewID(store.PrefixUser)
	}
	u.Email = strings.ToLower(strings.TrimSpace(u.Email))
	if u.Status == "" {
		u.Status = "active"
	}
	if u.InstanceRole == "" {
		u.InstanceRole = "none"
	}
	if u.Locale == "" {
		u.Locale = "it"
	}
	u.CreatedAt, u.UpdatedAt, u.Version = now, now, 1
	_, err := r.DB.Collection(UsersCollection).InsertOne(ctx, u)
	return err
}

// UserByAuthID returns the profile linked to a Limen user.
func (r *Repo) UserByAuthID(ctx context.Context, authUserID string) (*User, error) {
	var u User
	err := r.DB.Collection(UsersCollection).FindOne(ctx, bson.M{"authUserId": authUserID}).Decode(&u)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, ErrNotFound
	}
	return &u, err
}

// CountUsers returns the number of profiles (bootstrap runs only on an empty instance).
func (r *Repo) CountUsers(ctx context.Context) (int64, error) {
	return r.DB.Collection(UsersCollection).CountDocuments(ctx, bson.M{})
}

// SetLastOrganization remembers the organisation a user last acted in.
func (r *Repo) SetLastOrganization(ctx context.Context, userID, orgID string) error {
	_, err := r.DB.Collection(UsersCollection).UpdateOne(ctx, bson.M{"_id": userID}, bson.M{"$set": bson.M{"lastOrganizationId": orgID}})
	return err
}

// CreateMembership inserts an accepted membership.
func (r *Repo) CreateMembership(ctx context.Context, orgID, userID string, role Role) (*Membership, error) {
	now := time.Now().UTC()
	email := ""
	if u, err := r.UserByID(ctx, userID); err == nil {
		email = u.Email
	}
	m := &Membership{ID: store.NewID(store.PrefixMembership), OrganizationID: orgID, UserID: userID, Email: email, Role: role, AcceptedAt: &now, CreatedAt: now, UpdatedAt: now, Version: 1}
	if _, err := r.DB.Collection(MembershipsCollection).InsertOne(ctx, m); err != nil {
		return nil, fmt.Errorf("tenancy: create membership: %w", err)
	}
	return m, nil
}

// UserByID returns a user profile by ID.
func (r *Repo) UserByID(ctx context.Context, id string) (*User, error) {
	var u User
	err := r.DB.Collection(UsersCollection).FindOne(ctx, bson.M{"_id": id}).Decode(&u)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, ErrNotFound
	}
	return &u, err
}

// UpdateUser applies a $set/$inc update when version matches; returns ErrNotFound on miss.
func (r *Repo) UpdateUser(ctx context.Context, id string, version int64, set bson.M) (*User, error) {
	set["updatedAt"] = time.Now().UTC()
	res := r.DB.Collection(UsersCollection).FindOneAndUpdate(ctx,
		bson.M{"_id": id, "version": version},
		bson.M{"$set": set, "$inc": bson.M{"version": 1}},
		options.FindOneAndUpdate().SetReturnDocument(options.After),
	)
	var u User
	if err := res.Decode(&u); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &u, nil
}

// UserByEmail returns a user profile by lowercased email.
// TouchLogin records a successful sign-in on the user's profile.
func (r *Repo) TouchLogin(ctx context.Context, authUserID string, at time.Time) error {
	_, err := r.DB.Collection(UsersCollection).UpdateOne(ctx, bson.M{"authUserId": authUserID},
		bson.M{"$set": bson.M{"lastLoginAt": at}})
	return err
}

// UsersByID returns the users with the given IDs, keyed by ID (missing ones are absent).
func (r *Repo) UsersByID(ctx context.Context, ids []string) (map[string]User, error) {
	out := make(map[string]User, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	cur, err := r.DB.Collection(UsersCollection).Find(ctx, bson.M{"_id": bson.M{"$in": ids}})
	if err != nil {
		return nil, err
	}
	var users []User
	if err := cur.All(ctx, &users); err != nil {
		return nil, err
	}
	for _, u := range users {
		out[u.ID] = u
	}
	return out, nil
}

func (r *Repo) UserByEmail(ctx context.Context, email string) (*User, error) {
	var u User
	err := r.DB.Collection(UsersCollection).FindOne(ctx, bson.M{"email": strings.ToLower(strings.TrimSpace(email))}).Decode(&u)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, ErrNotFound
	}
	return &u, err
}

// UserIDsMatching returns up to limit user IDs whose email or name matches q (literal substring, case-insensitive).
func (r *Repo) UserIDsMatching(ctx context.Context, q string, limit int64) ([]string, error) {
	if limit <= 0 {
		return nil, nil
	}
	rx := bson.M{"$regex": regexp.QuoteMeta(q), "$options": "i"}
	opts := options.Find().SetLimit(limit).SetProjection(bson.M{"_id": 1})
	cur, err := r.DB.Collection(UsersCollection).Find(ctx, bson.M{"$or": []bson.M{{"email": rx}, {"name": rx}}}, opts)
	if err != nil {
		return nil, err
	}
	defer func() { _ = cur.Close(ctx) }()
	var rows []struct {
		ID string `bson:"_id"`
	}
	if err := cur.All(ctx, &rows); err != nil {
		return nil, err
	}
	ids := make([]string, len(rows))
	for i, row := range rows {
		ids[i] = row.ID
	}
	return ids, nil
}

// ListOrganizations returns organisations matching filter, sorted, with an extra row for nextCursor.
func (r *Repo) ListOrganizations(ctx context.Context, filter bson.M, sort bson.D, limit int64, skip int64) ([]Organization, error) {
	opts := options.Find().SetSort(sort).SetLimit(limit)
	if skip > 0 {
		opts.SetSkip(skip)
	}
	cur, err := r.DB.Collection(OrganizationsCollection).Find(ctx, filter, opts)
	if err != nil {
		return nil, err
	}
	var out []Organization
	return out, cur.All(ctx, &out)
}

// CountOrganizations counts organisations matching filter (capped).
func (r *Repo) CountOrganizations(ctx context.Context, filter bson.M, cap int64) (int64, error) {
	return r.DB.Collection(OrganizationsCollection).CountDocuments(ctx, filter, options.Count().SetLimit(cap))
}

// CountByOrganization aggregates document counts per organizationId for the given IDs.
// extra is merged into the match (e.g. deletedAt $exists false for accounts).
func (r *Repo) CountByOrganization(ctx context.Context, collection string, orgIDs []string, extra bson.M) (map[string]int64, error) {
	out := make(map[string]int64, len(orgIDs))
	if len(orgIDs) == 0 {
		return out, nil
	}
	match := bson.M{"organizationId": bson.M{"$in": orgIDs}}
	for k, v := range extra {
		match[k] = v
	}
	pipe := mongo.Pipeline{
		{{Key: "$match", Value: match}},
		{{Key: "$group", Value: bson.M{"_id": "$organizationId", "n": bson.M{"$sum": 1}}}},
	}
	cur, err := r.DB.Collection(collection).Aggregate(ctx, pipe)
	if err != nil {
		return nil, err
	}
	defer func() { _ = cur.Close(ctx) }()
	var rows []struct {
		ID string `bson:"_id"`
		N  int64  `bson:"n"`
	}
	if err := cur.All(ctx, &rows); err != nil {
		return nil, err
	}
	for _, row := range rows {
		out[row.ID] = row.N
	}
	return out, nil
}

// OrganizationBySlug returns an organisation by slug.
func (r *Repo) OrganizationBySlug(ctx context.Context, slug string) (*Organization, error) {
	var o Organization
	err := r.DB.Collection(OrganizationsCollection).FindOne(ctx, bson.M{"slug": slug}).Decode(&o)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, ErrNotFound
	}
	return &o, err
}

// UpdateOrganization applies a $set/$inc update when version matches; returns ErrNotFound on miss.
func (r *Repo) UpdateOrganization(ctx context.Context, id string, version int64, set bson.M) (*Organization, error) {
	set["updatedAt"] = time.Now().UTC()
	res := r.DB.Collection(OrganizationsCollection).FindOneAndUpdate(ctx,
		bson.M{"_id": id, "version": version},
		bson.M{"$set": set, "$inc": bson.M{"version": 1}},
		options.FindOneAndUpdate().SetReturnDocument(options.After),
	)
	var o Organization
	if err := res.Decode(&o); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &o, nil
}

// InsertOrganization inserts a fully populated organisation document.
func (r *Repo) InsertOrganization(ctx context.Context, o *Organization) error {
	_, err := r.DB.Collection(OrganizationsCollection).InsertOne(ctx, o)
	return err
}

// Membership returns a membership by ID (any organisation).
func (r *Repo) Membership(ctx context.Context, id string) (*Membership, error) {
	var m Membership
	err := r.DB.Collection(MembershipsCollection).FindOne(ctx, bson.M{"_id": id}).Decode(&m)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, ErrNotFound
	}
	return &m, err
}

// MembershipInOrg returns a membership by ID scoped to an organisation.
func (r *Repo) MembershipInOrg(ctx context.Context, orgID, id string) (*Membership, error) {
	var m Membership
	err := r.DB.Collection(MembershipsCollection).FindOne(ctx, bson.M{"_id": id, "organizationId": orgID}).Decode(&m)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, ErrNotFound
	}
	return &m, err
}

// MembershipByInviteHash finds a pending invitation by token hash.
func (r *Repo) MembershipByInviteHash(ctx context.Context, hash string) (*Membership, error) {
	var m Membership
	err := r.DB.Collection(MembershipsCollection).FindOne(ctx, bson.M{"invite.tokenHash": hash}).Decode(&m)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, ErrNotFound
	}
	return &m, err
}

// PendingInviteByEmail finds a pending invite for email in an organisation.
func (r *Repo) PendingInviteByEmail(ctx context.Context, orgID, email string) (*Membership, error) {
	var m Membership
	err := r.DB.Collection(MembershipsCollection).FindOne(ctx, bson.M{
		"organizationId": orgID,
		"invite.email":   strings.ToLower(strings.TrimSpace(email)),
	}).Decode(&m)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, ErrNotFound
	}
	return &m, err
}

// InsertMembership inserts a membership row (pending or accepted).
func (r *Repo) InsertMembership(ctx context.Context, m *Membership) error {
	_, err := r.DB.Collection(MembershipsCollection).InsertOne(ctx, m)
	return err
}

// ListMemberships lists memberships for an organisation.
func (r *Repo) ListMemberships(ctx context.Context, filter bson.M, sort bson.D, limit, skip int64) ([]Membership, error) {
	opts := options.Find().SetSort(sort).SetLimit(limit)
	if skip > 0 {
		opts.SetSkip(skip)
	}
	cur, err := r.DB.Collection(MembershipsCollection).Find(ctx, filter, opts)
	if err != nil {
		return nil, err
	}
	var out []Membership
	return out, cur.All(ctx, &out)
}

// CountMemberships counts memberships matching filter (capped).
func (r *Repo) CountMemberships(ctx context.Context, filter bson.M, cap int64) (int64, error) {
	return r.DB.Collection(MembershipsCollection).CountDocuments(ctx, filter, options.Count().SetLimit(cap))
}

// CountOwners returns the number of accepted owners in an organisation.
func (r *Repo) CountOwners(ctx context.Context, orgID string) (int64, error) {
	return r.DB.Collection(MembershipsCollection).CountDocuments(ctx, bson.M{
		"organizationId": orgID, "role": RoleOwner, "acceptedAt": bson.M{"$ne": nil},
	})
}

// UpdateMembership applies a versioned $set/$inc update.
func (r *Repo) UpdateMembership(ctx context.Context, id string, version int64, set bson.M, unset bson.M) (*Membership, error) {
	set["updatedAt"] = time.Now().UTC()
	update := bson.M{"$set": set, "$inc": bson.M{"version": 1}}
	if len(unset) > 0 {
		update["$unset"] = unset
	}
	res := r.DB.Collection(MembershipsCollection).FindOneAndUpdate(ctx,
		bson.M{"_id": id, "version": version},
		update,
		options.FindOneAndUpdate().SetReturnDocument(options.After),
	)
	var m Membership
	if err := res.Decode(&m); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &m, nil
}

// DeleteMembership removes a membership by ID and organisation.
func (r *Repo) DeleteMembership(ctx context.Context, orgID, id string, version int64) error {
	filter := bson.M{"_id": id, "organizationId": orgID}
	if version > 0 {
		filter["version"] = version
	}
	res, err := r.DB.Collection(MembershipsCollection).DeleteOne(ctx, filter)
	if err != nil {
		return err
	}
	if res.DeletedCount == 0 {
		return ErrNotFound
	}
	return nil
}

// MembershipsOf lists a user's accepted memberships with their organisations.
func (r *Repo) MembershipsOf(ctx context.Context, userID string) ([]Membership, error) {
	cur, err := r.DB.Collection(MembershipsCollection).Find(ctx, bson.M{"userId": userID, "acceptedAt": bson.M{"$ne": nil}},
		options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}))
	if err != nil {
		return nil, err
	}
	var out []Membership
	return out, cur.All(ctx, &out)
}

// Organizations returns the organisations with the given IDs, keyed by ID.
func (r *Repo) Organizations(ctx context.Context, ids []string) (map[string]Organization, error) {
	cur, err := r.DB.Collection(OrganizationsCollection).Find(ctx, bson.M{"_id": bson.M{"$in": ids}})
	if err != nil {
		return nil, err
	}
	var list []Organization
	if err := cur.All(ctx, &list); err != nil {
		return nil, err
	}
	out := make(map[string]Organization, len(list))
	for _, o := range list {
		out[o.ID] = o
	}
	return out, nil
}
