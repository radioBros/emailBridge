package accounts

import (
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/radiobros/emailbridge/internal/audit"
	"github.com/radiobros/emailbridge/internal/bus"
	"github.com/radiobros/emailbridge/internal/crypto"
	"github.com/radiobros/emailbridge/internal/egress"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/events"
	"github.com/radiobros/emailbridge/internal/features"
	"github.com/radiobros/emailbridge/internal/jobs"
	"github.com/radiobros/emailbridge/internal/logging/query"
	"github.com/radiobros/emailbridge/internal/objstore"
	"github.com/radiobros/emailbridge/internal/store"
	ebsync "github.com/radiobros/emailbridge/internal/sync"
)

// CorePublisher is the subset of *bus.Bus used to deliver sync commands (S4 §6).
type CorePublisher interface {
	PublishCore(subject string, data []byte) error
}

// Service is the accounts domain API.
type Service struct {
	// OAuthToken returns a fresh access token (and the login name) for an OAuth account (P6.1); set by
	// cmd/ wiring from internal/oauth. Nil: OAuth accounts cannot connect.
	OAuthToken func(ctx context.Context, accountID string) (token, username string, err error)
	// BindOAuthApp links an account to a service-account / app-only OAuth app without user consent (W43).
	// Wired to oauth.Service.LinkAppOnly. Nil: create/patch with oauth.appId fails.
	BindOAuthApp func(ctx context.Context, accountID, appID string, skipVerify bool) error
	// Credentials resolves IMAP/SMTP secrets from the external credential server when useAuthServer (W54).
	// Wired to authserver.Resolver. Nil: auth-server accounts cannot connect.
	Credentials func(ctx context.Context, accountID, protocol string) (user, pass, oauthToken string, err error)
	DB          *store.DB
	Cipher      *crypto.Cipher
	Events      *events.Writer
	Jobs        *jobs.Service
	Bus         CorePublisher
	Audit       *audit.Writer
	Egress      *egress.Dialer
	Objects     *objstore.Store
	Logs        *query.Service
	Registry    *features.Registry
	// IMAPID is the global EB_IMAP_ID_* map used by verify (account fields overlay it, W57).
	IMAPID map[string]string
	// QuotaAccounts is EB_QUOTA_ACCOUNTS (0 = unlimited).
	QuotaAccounts int
	// RawDebugTTL is protocolLogs.rawDebugTtl (default 1h, capped at 24h). Wire from effective settings.
	RawDebugTTL time.Duration
	Log         *slog.Logger

	verifyMu sync.Mutex
	verifyRL map[string]*tokenBucket // per organisation
}

const (
	defaultRawDebugTTL = time.Hour
	maxRawDebugTTL     = 24 * time.Hour
)

func (s *Service) rawDebugTTL() time.Duration {
	ttl := s.RawDebugTTL
	if ttl <= 0 {
		ttl = defaultRawDebugTTL
	}
	if ttl > maxRawDebugTTL {
		return maxRawDebugTTL
	}
	return ttl
}

type tokenBucket struct {
	tokens float64
	last   time.Time
}

func (s *Service) log() *slog.Logger {
	if s.Log == nil {
		return slog.Default()
	}
	return s.Log
}

func (s *Service) registry() *features.Registry {
	if s.Registry != nil {
		return s.Registry
	}
	return features.DefaultRegistry()
}

func (s *Service) dialMail(ctx context.Context, network, addr string) (net.Conn, error) {
	if s.Egress != nil {
		return s.Egress.DialContext(ctx, network, addr, egress.Mail)
	}
	var d net.Dialer
	return d.DialContext(ctx, network, addr)
}

func (s *Service) encryptPass(ctx context.Context, orgID, accountID, field, pass string) (crypto.EncryptedString, error) {
	return s.Cipher.EncryptString(ctx, orgID, pass, crypto.AAD{
		Collection: Collection, Field: field, DocID: accountID,
	})
}

func (s *Service) decryptPass(ctx context.Context, accountID, field string, es crypto.EncryptedString) (string, error) {
	return s.Cipher.DecryptString(ctx, es, crypto.AAD{
		Collection: Collection, Field: field, DocID: accountID,
	})
}

func (s *Service) audit(ctx context.Context, action, id string, changes audit.Changes) {
	if s.Audit == nil {
		return
	}
	_ = s.Audit.Record(ctx, action, audit.Target{Type: "account", ID: id}, changes)
}

func (s *Service) emit(ctx context.Context, d events.Draft) error {
	if s.Events == nil {
		return nil
	}
	_, err := s.Events.Emit(ctx, d)
	return err
}

func extPtr(s string) *string {
	if s == "" {
		return nil
	}
	v := s
	return &v
}

// publishSyncCmd notifies the owner node (best-effort; command is also in pendingCommands).
func (s *Service) publishSyncCmd(a *Account, cmd string, arg map[string]any) {
	if s.Bus == nil || a.Owner == nil || a.Owner.NodeID == "" {
		return
	}
	at := time.Now().UTC().Truncate(time.Millisecond)
	body, _ := json.Marshal(ebsync.CommandMessage{
		AccountID: a.ID,
		Command:   ebsync.Command{Cmd: cmd, Args: arg, At: at},
	})
	_ = s.Bus.PublishCore(bus.NodeSyncSubject(a.Owner.NodeID), body)
}

// NudgeSync asks the account's sync owner for a fast resync (provider push notifications, P6.2/P6.3). No
// tenancy: callers are verified webhooks. An account without an owner is picked up by placement anyway.
func (s *Service) NudgeSync(ctx context.Context, accountID string) {
	var a Account
	if err := s.coll().FindOne(ctx, bson.M{"_id": accountID}).Decode(&a); err != nil {
		return
	}
	s.publishSyncCmd(&a, CmdResync, map[string]any{"mode": "fast"})
}

// appendCommand builds a pending command, publishes to the sync owner, and returns it for a
// Mongo $push (never rewrite the whole pendingCommands array — that would clobber the engine).
func (s *Service) appendCommand(a *Account, cmd, by string, arg map[string]any) PendingCommand {
	at := time.Now().UTC().Truncate(time.Millisecond)
	pc := PendingCommand{Cmd: cmd, At: at, By: by, Arg: arg}
	a.PendingCommands = append(a.PendingCommands, pc)
	s.publishSyncCmd(a, cmd, arg)
	return pc
}

// updateAPI applies a targeted UpdateOne for API-owned fields only (+ optional pendingCommands $push).
// Filter is {_id, version} (plus org scope). Does not touch owner / sync-engine fields.
func (s *Service) updateAPI(ctx context.Context, id string, expectVersion int64, set, unset bson.M, cmd *PendingCommand) error {
	now := time.Now().UTC().Truncate(time.Millisecond)
	if set == nil {
		set = bson.M{}
	}
	set["updatedAt"] = now
	update := bson.M{"$set": set, "$inc": bson.M{"version": 1}}
	if len(unset) > 0 {
		update["$unset"] = unset
	}
	if cmd != nil {
		update["$push"] = bson.M{"pendingCommands": bson.M{"$each": []PendingCommand{*cmd}, "$slice": -50}}
	}
	filter, err := store.Scoped(ctx, bson.M{"_id": id, "version": expectVersion})
	if err != nil {
		return errs.Wrap(err, errs.CodeInternalError, "accounts.scoped")
	}
	res, err := s.coll().UpdateOne(ctx, filter, update)
	if err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return errs.New(errs.CodeExternalIDTaken, "That externalId is already used in this organisation.")
		}
		return errs.Wrap(err, errs.CodeInternalError, "accounts.update")
	}
	if res.MatchedCount == 0 {
		return errs.New(errs.CodeVersionMismatch, "The resource version does not match If-Match.")
	}
	return nil
}

// formatETag renders the version as `"v<n>"`, the format every other resource uses (the web client sends it back).
func formatETag(v int64) string { return `"v` + itoa(v) + `"` }

func itoa(v int64) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var b [20]byte
	i := len(b)
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// parseETag accepts `"v<n>"` and, for clients that kept the older form, a bare `"<n>"`.
func parseETag(s string) int64 {
	s = strings.TrimPrefix(trimETag(s), "v")
	var n int64
	for _, c := range s {
		if c < '0' || c > '9' {
			return -1
		}
		n = n*10 + int64(c-'0')
	}
	return n
}

func trimETag(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1]
	}
	if len(s) >= 4 && s[:3] == `W/"` && s[len(s)-1] == '"' {
		return s[3 : len(s)-1]
	}
	return s
}
