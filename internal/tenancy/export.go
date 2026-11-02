package tenancy

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/store"
)

// MeExportAuditLimit is the hard cap on audit rows in GET /v1/me/export (W34 / S11 §6).
const MeExportAuditLimit = 200

// ExportSession is one active session in the me export (no token).
type ExportSession struct {
	ID           string    `json:"id" doc:"Session ID (UUID)." example:"3f6c2a1e-8b4d-4e7a-9c5f-1d2e3b4a5c6d"`
	Current      bool      `json:"current" doc:"True for the session that requested the export." example:"true"`
	CreatedAt    time.Time `json:"createdAt" doc:"Sign-in time." example:"2026-10-26T08:15:42Z"`
	LastActiveAt time.Time `json:"lastActiveAt" doc:"Last request seen on the session." example:"2026-10-27T10:02:11Z"`
	ExpiresAt    time.Time `json:"expiresAt" doc:"Expiry unless used again." example:"2026-11-02T08:15:42Z"`
	UserAgent    string    `json:"userAgent,omitempty" doc:"Browser user agent at sign-in." example:"Mozilla/5.0 (Macintosh; Intel Mac OS X 14_6) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Safari/605.1.15"`
	IP           string    `json:"ip,omitempty" doc:"Client IP at sign-in." example:"203.0.113.24"`
	Method       string    `json:"method" doc:"Sign-in method (password, password+totp, passkey, oidc …)." example:"passkey"`
}

// ExportPasskey is a passkey name in the me export (no credential material).
type ExportPasskey struct {
	ID        string     `json:"id" doc:"Passkey ID." example:"7d1e9b2c-4a6f-4c3e-8b5d-2f1a0e9c8b7a"`
	Name      string     `json:"name" doc:"Name the user gave the passkey." example:"MacBook Touch ID"`
	CreatedAt *time.Time `json:"createdAt,omitempty" doc:"Registration time." example:"2026-10-21T11:20:00Z"`
}

// ExportAuditRecord is a redacted audit row for the export payload.
type ExportAuditRecord struct {
	ID             string         `json:"id" bson:"_id" doc:"Audit record ID (aud_…)." example:"aud_01m5d44p70hwn3w76n3fzr2gaa"`
	OrganizationID *string        `json:"organizationId" bson:"organizationId" doc:"Organisation of the action; null for instance-level actions." example:"org_01m5caqpk0kast63yc13wpy2d9"`
	At             time.Time      `json:"at" bson:"at" doc:"When the action happened." example:"2026-10-24T14:32:10Z"`
	Actor          map[string]any `json:"actor" bson:"actor" doc:"Who acted (type and ID)." example:"{\"type\":\"user\",\"id\":\"usr_01m5ccvej0dqefkn0xhkcq2q6z\"}"`
	Action         string         `json:"action" bson:"action" doc:"Audit action name." example:"user.update"`
	Target         map[string]any `json:"target" bson:"target" doc:"What was acted on (type and ID)." example:"{\"type\":\"user\",\"id\":\"usr_01m5ccvej0dqefkn0xhkcq2q6z\"}"`
	Changes        map[string]any `json:"changes,omitempty" bson:"changes,omitempty" doc:"Changed fields with before/after values (secrets redacted)." example:"{\"locale\":{\"before\":\"en\",\"after\":\"it\"}}"`
	RequestID      string         `json:"requestId,omitempty" bson:"requestId,omitempty" doc:"Request that caused the action." example:"req_01m5d44p70hwn3w76n3fzr2gaa"`
}

// MeExportBody is the JSON body of GET /v1/me/export.
type MeExportBody struct {
	Profile     *User           `json:"profile" doc:"The user's profile."`
	Memberships []MeMembership  `json:"memberships" doc:"Organisations the user belongs to, with roles."`
	Settings    map[string]any  `json:"settings" doc:"Per-user UI settings (as stored by PATCH /v1/me/settings)." example:"{\"tables\":{\"accounts\":{\"visibility\":[\"email\",\"state\",\"lastSyncAt\"],\"order\":[\"email\",\"state\",\"lastSyncAt\"]}},\"ui\":{\"theme\":\"dark\"}}"`
	Sessions    []ExportSession `json:"sessions" doc:"Active sessions (no tokens)."`
	Passkeys    []ExportPasskey `json:"passkeys" doc:"Registered passkeys (names only, no key material)."`
	Audit       struct {
		Data       []ExportAuditRecord `json:"data" doc:"Audit records where the user is the actor, newest first (at most 200 per page)."`
		Limit      int                 `json:"limit" doc:"Maximum number of audit records per page (200)." example:"200"`
		NextCursor *string             `json:"nextCursor" doc:"Pass as auditCursor to get the next page of the audit trail; null on the last page." example:"eyJhdCI6IjIwMjYtMTAtMjRUMTQ6MzI6MTBaIiwiaWQiOiJhdWRfMDFtNWQ0NHA3MGh3bjN3NzZuM2Z6cjJnYWEifQ"`
	} `json:"audit" doc:"The user's audit trail, one page of at most 200 records."`
}

// MeExportOutput wraps MeExportBody for huma.
type MeExportOutput struct {
	Body MeExportBody
}

// SessionExporter lists the caller's sessions without tokens (wired to auth in cmd).
type SessionExporter func(ctx context.Context, authUserID, currentSessionID string) ([]ExportSession, error)

// PasskeyExporter lists passkey id+name (wired to auth in cmd).
type PasskeyExporter func(ctx context.Context, authUserID string) ([]ExportPasskey, error)

// ExportRateCheck returns a rate-limit error when the caller should wait.
type ExportRateCheck func(ctx context.Context) error

// MeExportDeps are collaborators for GET /v1/me/export. Tenancy never imports internal/auth.
type MeExportDeps struct {
	DB        *store.DB
	Repo      *Repo
	Audit     Auditor
	Sessions  SessionExporter
	Passkeys  PasskeyExporter
	Rate      ExportRateCheck
	CursorKey []byte // HMAC key for audit cursors (derived; HOTSPOTS: wire from auth secret)
}

// RegisterMeExport mounts GET /v1/me/export. Claude wires deps from cmd/emailbridge (HOTSPOTS).
func RegisterMeExport(srv *httpapi.Server, deps MeExportDeps) {
	if srv == nil {
		return
	}
	ext := httpapi.WithScope(httpapi.Phase("P3"), "me:read")
	httpapi.Register(srv, huma.Operation{
		OperationID: "exportMe", Method: http.MethodGet, Path: "/v1/me/export",
		Summary: "Export the caller's personal data", Tags: []string{"Me"},
		Description: "GDPR data portability (Art. 20): returns everything EmailBridge stores about the signed-in user — " +
			"profile, memberships, UI settings, active sessions (no tokens), passkey names (no key material) and " +
			"their own audit records (200 per page, newest first) — as one JSON document. Follow `audit.nextCursor` " +
			"with `?auditCursor=` for the rest of the audit trail; those follow-up pages do not count against the " +
			"limit of 5 exports per user per hour (`429`). Cursors are HMAC-signed and bound to the user and export " +
			"start (`422` when tampered). Signed-in users only (API keys get `403`). The first page writes a " +
			"`me.export` audit record.",
		Metadata: httpapi.Problems(errs.CodeUnauthenticated, errs.CodeForbidden, errs.CodeRateLimited, errs.CodeNotFound,
			errs.CodeValidationFailed),
		Extensions: ext,
	}, deps.handle)
}

type meExportIn struct {
	AuditCursor string `query:"auditCursor" doc:"audit.nextCursor of the previous page; omit for the first page." example:"eyJhdCI6IjIwMjYtMTAtMjRUMTQ6MzI6MTBaIiwiaWQiOiJhdWRfMDFtNWQ0NHA3MGh3bjN3NzZuM2Z6cjJnYWEifQ"`
}

func (d MeExportDeps) handle(ctx context.Context, in *meExportIn) (*MeExportOutput, error) {
	cursor := ""
	if in != nil {
		cursor = in.AuditCursor
	}
	body, err := d.ExportPage(ctx, cursor)
	if err != nil {
		return nil, err
	}
	return &MeExportOutput{Body: *body}, nil
}

// Export builds the first page of the GDPR portability payload for the signed-in user.
func (d MeExportDeps) Export(ctx context.Context) (*MeExportBody, error) {
	return d.ExportPage(ctx, "")
}

// ExportPage builds the payload with the audit page after auditCursor ("" = the first page). Only the first
// page counts against the export rate limit and writes the me.export audit record.
func (d MeExportDeps) ExportPage(ctx context.Context, auditCursor string) (*MeExportBody, error) {
	c, ok := CallerFrom(ctx)
	if !ok {
		return nil, errs.New(errs.CodeUnauthenticated, "Authentication is required.")
	}
	if c.Kind != CallerUser || c.UserID == "" {
		return nil, errs.New(errs.CodeForbidden, "Personal data export requires a signed-in user.")
	}
	var after *exportCursor
	exportStart := time.Now().UTC().Truncate(time.Second)
	if auditCursor != "" {
		cur, err := decodeExportCursor(d.CursorKey, c.UserID, auditCursor)
		if err != nil {
			return nil, errs.New(errs.CodeValidationFailed, "The audit cursor is invalid.").WithSafe("param", "auditCursor")
		}
		after = &cur
		exportStart = cur.Start
	}
	if d.Rate != nil && after == nil {
		if err := d.Rate(ctx); err != nil {
			return nil, err
		}
	}
	if d.Repo == nil || d.DB == nil || d.DB.DB == nil {
		return nil, errs.New(errs.CodeInternalError, "Personal data export is not configured.")
	}

	u, err := d.Repo.UserByID(ctx, c.UserID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, errs.New(errs.CodeNotFound, "No such user.")
		}
		return nil, err
	}

	ms, err := d.Repo.MembershipsOf(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(ms))
	for _, m := range ms {
		ids = append(ids, m.OrganizationID)
	}
	orgs, err := d.Repo.Organizations(ctx, ids)
	if err != nil {
		return nil, err
	}
	memberships := make([]MeMembership, 0, len(ms))
	for _, m := range ms {
		memberships = append(memberships, MeMembership{
			OrganizationID: m.OrganizationID, OrganizationName: orgs[m.OrganizationID].Name, Role: m.Role,
		})
	}

	settings := map[string]any{}
	var usDoc struct {
		Settings map[string]any `bson:"settings"`
	}
	if err := d.DB.Collection("userSettings").FindOne(ctx, bson.M{"_id": u.ID}).Decode(&usDoc); err == nil && usDoc.Settings != nil {
		settings = usDoc.Settings
	}

	sessions := []ExportSession{}
	if d.Sessions != nil && u.AuthUserID != "" {
		// currentSessionID is empty here; the wired auth adapter marks Current from its own context.
		ss, err := d.Sessions(ctx, u.AuthUserID, "")
		if err != nil {
			return nil, errs.Wrap(err, errs.CodeInternalError, "tenancy.export.sessions")
		}
		if ss != nil {
			sessions = ss
		}
	}

	passkeys := []ExportPasskey{}
	if d.Passkeys != nil && u.AuthUserID != "" {
		pk, err := d.Passkeys(ctx, u.AuthUserID)
		if err != nil {
			return nil, errs.Wrap(err, errs.CodeInternalError, "tenancy.export.passkeys")
		}
		if pk != nil {
			passkeys = pk
		}
	}

	auditRows, next, err := d.loadAudit(ctx, u.ID, after, exportStart)
	if err != nil {
		return nil, err
	}

	out := &MeExportBody{
		Profile:     u,
		Memberships: memberships,
		Settings:    settings,
		Sessions:    sessions,
		Passkeys:    passkeys,
	}
	out.Audit.Data = auditRows
	out.Audit.Limit = MeExportAuditLimit
	out.Audit.NextCursor = next

	if d.Audit != nil && after == nil {
		_ = d.Audit.Record(ctx, "me.export", "user", u.ID, nil)
	}
	return out, nil
}

// exportCursor is the (at, _id) keyset position of the last audit row of a page, bound to the export.
type exportCursor struct {
	At    time.Time `json:"at"`
	ID    string    `json:"id"`
	User  string    `json:"u"`
	Start time.Time `json:"s"`
}

func deriveExportCursorKey(master []byte, userID string, start time.Time) []byte {
	mac := hmac.New(sha256.New, master)
	_, _ = mac.Write([]byte(userID + "|" + start.UTC().Format(time.RFC3339)))
	return mac.Sum(nil)
}

func encodeExportCursor(master []byte, c exportCursor) string {
	payload, _ := json.Marshal(struct {
		At    time.Time `json:"at"`
		ID    string    `json:"id"`
		User  string    `json:"u"`
		Start time.Time `json:"s"`
	}{At: c.At, ID: c.ID, User: c.User, Start: c.Start})
	key := deriveExportCursorKey(master, c.User, c.Start)
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(payload)
	return base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func decodeExportCursor(master []byte, userID, s string) (exportCursor, error) {
	var c exportCursor
	if len(master) == 0 {
		return c, errors.New("unsigned")
	}
	dot := strings.IndexByte(s, '.')
	if dot <= 0 || dot == len(s)-1 {
		return c, errors.New("bad format")
	}
	payload, err := base64.RawURLEncoding.DecodeString(s[:dot])
	if err != nil {
		return c, err
	}
	sig, err := base64.RawURLEncoding.DecodeString(s[dot+1:])
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(payload, &c); err != nil {
		return c, err
	}
	if c.ID == "" || c.At.IsZero() || c.User == "" || c.Start.IsZero() {
		return c, errors.New("incomplete cursor")
	}
	if c.User != userID {
		return c, fmt.Errorf("user mismatch")
	}
	key := deriveExportCursorKey(master, c.User, c.Start)
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(payload)
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return c, errors.New("bad signature")
	}
	return c, nil
}

// loadAudit returns one page of the user's own audit records, newest first, and the cursor of the next page
// (nil on the last one). A database error fails the export instead of returning an empty trail.
func (d MeExportDeps) loadAudit(ctx context.Context, userID string, after *exportCursor, exportStart time.Time) ([]ExportAuditRecord, *string, error) {
	filter := bson.M{"actor.type": "user", "actor.id": userID}
	if after != nil {
		filter["$or"] = []bson.M{
			{"at": bson.M{"$lt": after.At}},
			{"at": after.At, "_id": bson.M{"$lt": after.ID}},
		}
	}
	opts := options.Find().
		SetSort(bson.D{{Key: "at", Value: -1}, {Key: "_id", Value: -1}}).
		SetLimit(int64(MeExportAuditLimit + 1))
	cur, err := d.DB.Collection("auditLog").Find(ctx, filter, opts)
	if err != nil {
		return nil, nil, errs.Wrap(err, errs.CodeInternalError, "tenancy.export.audit")
	}
	defer func() { _ = cur.Close(ctx) }()
	var rows []ExportAuditRecord
	if err := cur.All(ctx, &rows); err != nil {
		return nil, nil, errs.Wrap(err, errs.CodeInternalError, "tenancy.export.audit")
	}
	if rows == nil {
		rows = []ExportAuditRecord{}
	}
	var next *string
	if len(rows) > MeExportAuditLimit {
		rows = rows[:MeExportAuditLimit]
		last := rows[len(rows)-1]
		c := encodeExportCursor(d.CursorKey, exportCursor{At: last.At, ID: last.ID, User: userID, Start: exportStart})
		next = &c
	}
	for i := range rows {
		// Strip anything that could look like a secret in changes (defensive).
		rows[i].Changes = scrubExportChanges(rows[i].Changes)
		if rows[i].Actor != nil {
			delete(rows[i].Actor, "ip")
			delete(rows[i].Actor, "userAgent")
		}
	}
	return rows, next, nil
}

func scrubExportChanges(changes map[string]any) map[string]any {
	if changes == nil {
		return nil
	}
	out := map[string]any{}
	secretKeys := map[string]struct{}{
		"password": {}, "passwordHash": {}, "totp": {}, "totpSecret": {}, "backupCodes": {},
		"token": {}, "secret": {}, "clientSecret": {}, "refreshToken": {}, "accessToken": {},
	}
	for k, v := range changes {
		if _, bad := secretKeys[k]; bad {
			out[k] = map[string]any{"before": "[redacted]", "after": "[redacted]"}
			continue
		}
		out[k] = v
	}
	return out
}
