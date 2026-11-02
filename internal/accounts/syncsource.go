package accounts

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/radiobros/emailbridge/internal/errs"
	eventtypes "github.com/radiobros/emailbridge/internal/events/types"
	"github.com/radiobros/emailbridge/internal/features"
	"github.com/radiobros/emailbridge/internal/index"
	"github.com/radiobros/emailbridge/internal/ippool"
	"github.com/radiobros/emailbridge/internal/mailproto/imapc"
	"github.com/radiobros/emailbridge/internal/settings"
	"github.com/radiobros/emailbridge/internal/store"
	ebsync "github.com/radiobros/emailbridge/internal/sync"
)

// SyncSource adapts Service to ebsync.Accounts for the sync engine (P1.07).
type SyncSource struct {
	Svc *Service
}

// NewSyncSource returns an ebsync.Accounts backed by svc.
func NewSyncSource(svc *Service) *SyncSource {
	return &SyncSource{Svc: svc}
}

// Placeable lists account IDs that should be connected (not disabled / deleting). System caller — no tenant.
func (ss *SyncSource) Placeable(ctx context.Context) ([]string, error) {
	cur, err := ss.Svc.coll().Find(ctx, bson.M{
		"state": bson.M{"$nin": []string{StateDisabled, StateDeleting}},
	}, options.Find().SetProjection(bson.M{"_id": 1}))
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "accounts.placeable")
	}
	defer func() { _ = cur.Close(ctx) }()
	var ids []string
	for cur.Next(ctx) {
		var doc struct {
			ID string `bson:"_id"`
		}
		if err := cur.Decode(&doc); err != nil {
			return nil, errs.Wrap(err, errs.CodeInternalError, "accounts.placeable")
		}
		ids = append(ids, doc.ID)
	}
	if ids == nil {
		ids = []string{}
	}
	return ids, cur.Err()
}

// Load returns the account with IMAP password decrypted.
func (ss *SyncSource) Load(ctx context.Context, accountID string) (*ebsync.Account, error) {
	var a Account
	if err := ss.Svc.coll().FindOne(ctx, bson.M{"_id": accountID}).Decode(&a); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, errs.New(errs.CodeNotFound, "The account does not exist.").WithSafe("accountId", accountID)
		}
		return nil, errs.Wrap(err, errs.CodeInternalError, "accounts.load")
	}
	imapUser := a.IMAP.Auth.User
	pass := ""
	if a.UsesAuthServer() {
		if ss.Svc.Credentials == nil {
			return nil, errs.New(errs.CodeAuthServerMisconfigured, "Credential server is not wired.")
		}
		u, p, _, err := ss.Svc.Credentials(ctx, a.ID, "imap")
		if err != nil {
			return nil, err
		}
		if u != "" {
			imapUser = u
		}
		pass = p
	} else if !a.IMAP.Auth.Pass.IsZero() {
		p, err := ss.Svc.decryptPass(ctx, a.ID, "imap.auth.pass", a.IMAP.Auth.Pass)
		if err != nil {
			return nil, errs.Wrap(err, errs.CodeInternalError, "accounts.decrypt")
		}
		pass = p
	}
	if upn := a.DelegatedUPN(); upn != "" {
		imapUser = upn
	}
	notifyFrom := a.CreatedAt
	if a.Sync.NotifyFrom != nil {
		notifyFrom = *a.Sync.NotifyFrom
	}
	paths := a.Sync.Paths
	if len(paths) == 0 {
		paths = []string{"*"}
	}
	idle := a.Sync.IdlePaths
	if len(idle) == 0 {
		idle = []string{"INBOX"}
	}
	var resync, maxIdle time.Duration
	if a.Sync.ResyncDelay != "" {
		resync, _ = time.ParseDuration(a.Sync.ResyncDelay)
	}
	if a.Sync.MaxIdle != "" {
		maxIdle, _ = time.ParseDuration(a.Sync.MaxIdle)
	}
	ignoreCert := false
	protocolLogs := false
	if a.Switches != nil {
		if v, ok := a.Switches["accounts.ignoreCertErrors"].(bool); ok {
			ignoreCert = v
		}
		if v, ok := a.Switches["protocolLogs.enabled"].(bool); ok {
			protocolLogs = v
		}
	}
	var rawUntil *time.Time
	if a.RawDebugUntil != nil && time.Now().UTC().Before(*a.RawDebugUntil) {
		t := a.RawDebugUntil.UTC()
		rawUntil = &t
	}
	var token func(context.Context) (string, string, error)
	if a.UsesOAuth() && ss.Svc.OAuthToken != nil {
		srcID := a.DelegatedTokenSource()
		fn := ss.Svc.OAuthToken
		upn := a.DelegatedUPN()
		token = func(ctx context.Context) (string, string, error) {
			tok, user, err := fn(ctx, srcID)
			if err != nil {
				return "", "", err
			}
			if upn != "" {
				return tok, upn, nil
			}
			return tok, user, nil
		}
	} else if a.UsesAuthServer() && ss.Svc.Credentials != nil {
		id := a.ID
		fn := ss.Svc.Credentials
		token = func(ctx context.Context) (string, string, error) {
			u, _, tok, err := fn(ctx, id, "oauth")
			if err != nil {
				return "", "", err
			}
			if tok == "" {
				return "", "", errs.New(errs.CodeAuthServerFailed, "Credential server returned no OAuth token.")
			}
			if u == "" {
				u = imapUser
			}
			return tok, u, nil
		}
	}
	out := &ebsync.Account{Token: token, Provider: a.Provider,
		ID: a.ID, OrganizationID: a.OrganizationID, ExternalID: a.ExternalID,
		Email: a.Email, State: a.State,
		IMAPAddr: net.JoinHostPort(a.IMAP.Host, strconv.Itoa(a.IMAP.Port)),
		IMAPTLS:  imapc.TLSMode(a.IMAP.TLS), IMAPUser: imapUser, IMAPPass: pass, DelegatedUser: a.DelegatedUPN(),
		IgnoreCertErrors: ignoreCert,
		IMAPID:           ebsync.AccountIMAPID(a.IMAP.IDName, a.IMAP.IDVersion, a.IMAP.IDVendor, a.IMAP.IDSupportURL),
		DisableRev2:      a.IMAP.DisableRev2,
		Paths:            paths, IdlePaths: idle, NotifyFrom: notifyFrom,
		ResyncDelay: resync, MaxIdle: maxIdle, AuthFailingSince: a.AuthFailingSince,
		ProtocolLogs: protocolLogs, RawDebugUntil: rawUntil,
		ArchiveRaw:         ss.resolveSwitch(ctx, &a, "inbound.archiveRaw"),
		QResync:            ss.resolveSwitch(ctx, &a, "sync.qresync"),
		BounceDetection:    ss.resolveSwitch(ctx, &a, "inbound.bounceDetection"),
		SpamScoring:        ss.resolveSwitch(ctx, &a, "integrations.rspamd"),
		PEC:                ss.resolveSwitch(ctx, &a, "pec.enabled"),
		PECEvidence:        ss.resolveSwitch(ctx, &a, "pec.evidenceArchive"),
		AttachmentsS3:      ss.resolveSwitch(ctx, &a, "attachments.s3"),
		AttachmentsAV:      ss.resolveSwitch(ctx, &a, "attachments.antivirus"),
		DenyWebSafeHTML:    !ss.resolveSwitch(ctx, &a, "mailbox.webSafeHtml"),
		DenyCalendarEvents: !ss.resolveSwitch(ctx, &a, "mailbox.calendarEvents"),
		DenyReplyText:      !ss.resolveSwitch(ctx, &a, "inbound.replyText"),
		SpecialUse:         a.Sync.SpecialUse,
	}
	ss.applyNetwork(ctx, &a, out)
	if a.Sync.SyncFrom != nil {
		out.SyncFrom = a.Sync.SyncFrom.UTC()
	}
	if ss.resolveSwitch(ctx, &a, "inbound.authResults") {
		out.AuthResults = ss.orgAuthMode(ctx, a.OrganizationID)
		out.TrustedAuth = ss.orgTrustedAuth(ctx, a.OrganizationID)
	}
	return out, nil
}

// applyNetwork copies account.network and merges organisation IP-pool settings (W57).
func (ss *SyncSource) applyNetwork(ctx context.Context, a *Account, out *ebsync.Account) {
	poolAddrs, smtpStrat, imapStrat := ss.orgIPPool(ctx, a.OrganizationID)
	if a.Network != nil {
		out.Proxy = a.Network.Proxy
		out.LocalAddress = a.Network.LocalAddress
		if len(a.Network.LocalAddresses) > 0 {
			poolAddrs = a.Network.LocalAddresses
		}
		if a.Network.IMAPStrategy != "" {
			imapStrat = a.Network.IMAPStrategy
		}
		_ = smtpStrat // SMTP path uses SMTPSettings
	}
	out.LocalAddresses = poolAddrs
	out.IMAPStrategy = imapStrat
}

func (ss *SyncSource) orgIPPool(ctx context.Context, orgID string) (addrs []string, smtpStrat, imapStrat string) {
	smtpStrat, imapStrat = ippool.DefaultStrategy, ippool.DefaultStrategy
	read := func(docID string) {
		var doc settings.Doc
		if err := ss.Svc.DB.Collection(settings.SettingsCollection).FindOne(ctx, bson.M{"_id": docID}).Decode(&doc); err != nil {
			return
		}
		if v, ok := stringList(doc.Values["ipPool.localAddresses"]); ok && len(v) > 0 {
			addrs = v
		}
		if v, _ := doc.Values["ipPool.smtpStrategy"].(string); v != "" {
			smtpStrat = v
		}
		if v, _ := doc.Values["ipPool.imapStrategy"].(string); v != "" {
			imapStrat = v
		}
	}
	read(settings.DocIDGlobal)
	read(settings.OrgDocID(orgID))
	return addrs, smtpStrat, imapStrat
}

func stringList(v any) ([]string, bool) {
	switch t := v.(type) {
	case []string:
		return t, true
	case []any:
		out := make([]string, 0, len(t))
		for _, x := range t {
			if s, ok := x.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out, true
	case bson.A:
		out := make([]string, 0, len(t))
		for _, x := range t {
			if s, ok := x.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out, true
	default:
		return nil, false
	}
}

// orgAuthMode reads authResults.mode from the organisation settings (default header).
func (ss *SyncSource) orgAuthMode(ctx context.Context, orgID string) string {
	var doc settings.Doc
	if err := ss.Svc.DB.Collection(settings.SettingsCollection).FindOne(ctx, bson.M{"_id": settings.OrgDocID(orgID)}).Decode(&doc); err != nil {
		return "header"
	}
	if v, _ := doc.Values["authResults.mode"].(string); v == "verify" || v == "header" {
		return v
	}
	return "header"
}

// orgTrustedAuth reads authResults.trustedAuthservIds from the organisation settings.
func (ss *SyncSource) orgTrustedAuth(ctx context.Context, orgID string) []string {
	var doc settings.Doc
	if err := ss.Svc.DB.Collection(settings.SettingsCollection).FindOne(ctx, bson.M{"_id": settings.OrgDocID(orgID)}).Decode(&doc); err != nil {
		return nil
	}
	switch v := doc.Values["authResults.trustedAuthservIds"].(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, x := range v {
			if s, ok := x.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	case bson.A:
		out := make([]string, 0, len(v))
		for _, x := range v {
			if s, ok := x.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

// resolveSwitch resolves a boolean feature switch for an account (global → organisation → account, with
// locks) via features; unknown switches are off.
func (ss *SyncSource) resolveSwitch(ctx context.Context, a *Account, name string) bool {
	sw, ok := ss.Svc.registry().Get(name)
	if !ok {
		return false
	}
	vals := features.Values{}
	if a.Switches != nil {
		if v, ok := a.Switches[name].(bool); ok {
			vals.Lower = &features.Setting{Value: features.BoolValue(v)}
		}
	}
	read := func(docID string) *features.Setting {
		var doc settings.Doc
		err := ss.Svc.DB.Collection(settings.SettingsCollection).FindOne(ctx, bson.M{"_id": docID}).Decode(&doc)
		if err != nil || doc.Switches == nil {
			return nil
		}
		if sv, ok := doc.Switches[name]; ok {
			if b, ok := sv.Value.(bool); ok {
				return &features.Setting{Value: features.BoolValue(b), Locked: sv.Locked}
			}
		}
		return nil
	}
	if features.Allows(sw, features.ScopeGlobal) {
		vals.Global = read(settings.DocIDGlobal)
	}
	vals.Organization = read(settings.OrgDocID(a.OrganizationID))
	v, _ := features.Resolve(sw, "production", vals)
	return v.Bool != nil && *v.Bool
}

// Claimed sets accounts.owner after a lease claim and returns (then clears) pendingCommands.
func (ss *SyncSource) Claimed(ctx context.Context, accountID, nodeID string, epoch uint64) ([]ebsync.Command, error) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	var before Account
	err := ss.Svc.coll().FindOneAndUpdate(ctx,
		bson.M{"_id": accountID},
		bson.M{"$set": bson.M{
			"owner":           Owner{NodeID: nodeID, Epoch: epoch, Since: now},
			"pendingCommands": []PendingCommand{},
			"updatedAt":       now,
		}},
		options.FindOneAndUpdate().SetReturnDocument(options.Before),
	).Decode(&before)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, errs.New(errs.CodeNotFound, "The account does not exist.").WithSafe("accountId", accountID)
		}
		return nil, errs.Wrap(err, errs.CodeInternalError, "accounts.claimed")
	}
	out := make([]ebsync.Command, 0, len(before.PendingCommands))
	for _, c := range before.PendingCommands {
		out = append(out, ebsync.Command{Cmd: c.Cmd, Args: c.Arg, At: c.At})
	}
	return out, nil
}

// Transition moves the account state (fenced by epoch). Retries version conflicts; epoch mismatch → ErrFenced.
func (ss *SyncSource) Transition(ctx context.Context, accountID string, epoch uint64, to string, reason *eventtypes.Reason) error {
	var last error
	for attempt := 0; attempt < 3; attempt++ {
		var doc Account
		if err := ss.Svc.coll().FindOne(ctx, bson.M{"_id": accountID}).Decode(&doc); err != nil {
			if errors.Is(err, mongo.ErrNoDocuments) {
				return errs.New(errs.CodeNotFound, "The account does not exist.").WithSafe("accountId", accountID)
			}
			return errs.Wrap(err, errs.CodeInternalError, "accounts.transition")
		}
		if doc.Owner == nil || doc.Owner.Epoch != epoch {
			return fmt.Errorf("accounts: transition: %w", index.ErrFenced)
		}
		tctx := store.WithOrganization(ctx, doc.OrganizationID)
		var sr *StateReason
		if reason != nil {
			sr = &StateReason{Kind: reason.Kind, Code: reason.Code, Message: reason.Message}
		}
		_, err := ss.Svc.Transition(tctx, accountID, epoch, to, sr)
		if err == nil {
			return nil
		}
		if errs.CodeOf(err) != errs.CodeConflict {
			return err
		}
		// Conflict: fencing or concurrent patch. Reload and decide.
		var again Account
		if err2 := ss.Svc.coll().FindOne(ctx, bson.M{"_id": accountID}).Decode(&again); err2 != nil {
			return errs.Wrap(err2, errs.CodeInternalError, "accounts.transition")
		}
		if again.Owner == nil || again.Owner.Epoch != epoch {
			return fmt.Errorf("accounts: transition: %w", index.ErrFenced)
		}
		last = err
	}
	return last
}
