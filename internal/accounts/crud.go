package accounts

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/radiobros/emailbridge/internal/audit"
	"github.com/radiobros/emailbridge/internal/connect"
	"github.com/radiobros/emailbridge/internal/crypto"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/events"
	eventtypes "github.com/radiobros/emailbridge/internal/events/types"
	"github.com/radiobros/emailbridge/internal/jobs"
	"github.com/radiobros/emailbridge/internal/logging"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

func (s *Service) coll() *mongo.Collection { return s.DB.Collection(Collection) }

func (s *Service) requireOrg(ctx context.Context) (string, error) {
	org, ok := store.OrganizationFrom(ctx)
	if !ok {
		return "", errs.New(errs.CodeForbidden, "An organisation context is required.")
	}
	return org, nil
}

func (s *Service) load(ctx context.Context, id string) (*Account, error) {
	if !store.IsID(store.PrefixAccount, id) {
		return nil, errs.New(errs.CodeNotFound, "The account does not exist.").WithSafe("accountId", id)
	}
	filter, err := store.Scoped(ctx, bson.M{"_id": id})
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "accounts.scoped")
	}
	var a Account
	if err := s.coll().FindOne(ctx, filter).Decode(&a); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, errs.New(errs.CodeNotFound, "The account does not exist.").WithSafe("accountId", id)
		}
		return nil, errs.Wrap(err, errs.CodeInternalError, "accounts.get")
	}
	return &a, nil
}

// Get returns one account (redacted view is the caller's responsibility via View()).
func (s *Service) Get(ctx context.Context, id string) (*Account, error) {
	if err := tenancy.RequireReadAccount(ctx, "accounts", id); err != nil {
		return nil, err
	}
	return s.load(ctx, id)
}

// Create registers an account (optionally verifies IMAP/SMTP first).
func (s *Service) Create(ctx context.Context, in CreateInput) (*Account, error) {
	if err := tenancy.Require(ctx, tenancy.PermAccountsWrite); err != nil {
		return nil, err
	}
	c, _ := tenancy.CallerFrom(ctx)
	if c.Kind == tenancy.CallerAPIKey && c.AccountID != "" {
		return nil, errs.New(errs.CodeForbidden, "An account-bound API key cannot create accounts.")
	}
	org, err := s.requireOrg(ctx)
	if err != nil {
		return nil, err
	}
	if err := validateCreate(in, s.registry()); err != nil {
		return nil, err
	}
	metadata, err := normalizeMetadata(in.Metadata)
	if err != nil {
		return nil, err
	}
	if err := s.validateDelegatedRef(ctx, org, "", in.Delegated); err != nil {
		return nil, err
	}
	provider := normalizeProvider(in.Provider)
	useAuthServer := in.Auth != nil && in.Auth.UseAuthServer
	if err := s.checkAccountQuota(ctx, org); err != nil {
		return nil, err
	}
	// API providers (gmail/graph) skip IMAP/SMTP verify; they authenticate via OAuth / service account.
	if !in.SkipVerify && !providerAPI(provider) {
		ignoreCert := false
		if v, ok := in.Switches["accounts.ignoreCertErrors"].(bool); ok {
			ignoreCert = v
		}
		vr, err := s.Verify(ctx, VerifyRequest{IMAP: &in.IMAP, SMTP: &in.SMTP, IgnoreCertErrors: ignoreCert})
		if err != nil {
			return nil, err
		}
		if vr.IMAP != nil && !vr.IMAP.OK {
			code := errs.Code(vr.IMAP.Code)
			if code == "" {
				code = errs.CodeMailConnectFailed
			}
			return nil, errs.New(code, "IMAP verification failed.").WithSafe("detail", vr.IMAP.Detail)
		}
		if vr.SMTP != nil && !vr.SMTP.OK {
			code := errs.Code(vr.SMTP.Code)
			if code == "" {
				code = errs.CodeMailConnectFailed
			}
			return nil, errs.New(code, "SMTP verification failed.").WithSafe("detail", vr.SMTP.Detail)
		}
	}

	id := store.NewID(store.PrefixAccount)
	now := time.Now().UTC().Truncate(time.Millisecond)
	var imapPass, smtpPass crypto.EncryptedString
	if !useAuthServer && in.IMAP.Auth.Pass != "" {
		p, encErr := s.encryptPass(ctx, org, id, "imap.auth.pass", in.IMAP.Auth.Pass)
		if encErr != nil {
			return nil, errs.Wrap(encErr, errs.CodeInternalError, "accounts.encrypt")
		}
		imapPass = p
	}
	if !useAuthServer && in.SMTP.Auth.Pass != "" {
		p, encErr := s.encryptPass(ctx, org, id, "smtp.auth.pass", in.SMTP.Auth.Pass)
		if encErr != nil {
			return nil, errs.Wrap(encErr, errs.CodeInternalError, "accounts.encrypt")
		}
		smtpPass = p
	}
	syncCfg := defaultSync()
	if in.Sync != nil {
		syncCfg = *in.Sync
		if len(syncCfg.Paths) == 0 {
			syncCfg.Paths = []string{"*"}
		}
		if len(syncCfg.IdlePaths) == 0 {
			syncCfg.IdlePaths = []string{"INBOX"}
		}
	}
	policy := in.SMTP.Policy
	if policy == "" {
		policy = PolicyStandard
	}
	a := &Account{
		ID: id, OrganizationID: org, ExternalID: extPtr(strings.TrimSpace(in.ExternalID)),
		Name: strings.TrimSpace(in.Name), Email: strings.TrimSpace(in.Email),
		State: StateNew, Provider: provider,
		IMAP: IMAPConfig{
			Host: strings.TrimSpace(in.IMAP.Host), Port: in.IMAP.Port, TLS: in.IMAP.TLS,
			Auth:   MailAuth{User: strings.TrimSpace(in.IMAP.Auth.User), Pass: imapPass},
			IDName: in.IMAP.IDName, IDVersion: in.IMAP.IDVersion, IDVendor: in.IMAP.IDVendor,
			IDSupportURL: in.IMAP.IDSupportURL, DisableRev2: in.IMAP.DisableRev2,
		},
		SMTP: SMTPConfig{
			Host: strings.TrimSpace(in.SMTP.Host), Port: in.SMTP.Port, TLS: in.SMTP.TLS,
			Auth:   MailAuth{User: strings.TrimSpace(in.SMTP.Auth.User), Pass: smtpPass},
			Policy: policy, MaxAttempts: in.SMTP.MaxAttempts, BackoffBase: in.SMTP.BackoffBase,
			GatewayID: strings.TrimSpace(in.SMTP.GatewayID),
		},
		Sync: syncCfg, TZ: in.TZ, Locale: strings.TrimSpace(in.Locale), Network: in.Network,
		UseAuthServer: useAuthServer, Delegated: in.Delegated, Metadata: metadata,
		CopyToSent: CopyToSentAuto, CreatedAt: now, UpdatedAt: now, Version: 1, CreatedBy: c.Actor(),
	}
	if _, err := s.applyProtocolSwitches(ctx, a, in.Switches, now); err != nil {
		return nil, err
	}
	a.Switches = in.Switches

	err = s.DB.WithTx(ctx, func(txCtx context.Context) error {
		if _, err := s.coll().InsertOne(txCtx, a); err != nil {
			if mongo.IsDuplicateKeyError(err) {
				return errs.New(errs.CodeExternalIDTaken, "That externalId is already used in this organisation.")
			}
			return errs.Wrap(err, errs.CodeInternalError, "accounts.insert")
		}
		ext := a.ExternalID
		return s.emit(txCtx, events.Draft{
			Type: events.AccountAdded, OrganizationID: org, AccountID: a.ID, Subject: a.ID,
			Data: &eventtypes.AccountAdded{AccountRef: eventtypes.AccountRef{AccountID: a.ID, ExternalID: ext}, Email: a.Email, Name: a.Name},
		})
	})
	if err != nil {
		return nil, err
	}
	createChanges := audit.Changes{
		"name":  {After: a.Name},
		"email": {After: a.Email},
		"imap":  {After: map[string]any{"host": a.IMAP.Host, "port": a.IMAP.Port, "user": a.IMAP.Auth.User, "pass": audit.Secret("x")}},
		"smtp":  {After: map[string]any{"host": a.SMTP.Host, "port": a.SMTP.Port, "user": a.SMTP.Auth.User, "pass": audit.Secret("x")}},
	}
	if len(a.Metadata) > 0 {
		createChanges["metadata"] = audit.FieldChange{After: copyMetadata(a.Metadata)}
	}
	s.audit(ctx, "account.create", a.ID, createChanges)
	s.log().Info("account created", "accountId", a.ID, "email", a.Email, "imapPass", logging.Secret(in.IMAP.Auth.Pass))
	if in.OAuth != nil && strings.TrimSpace(in.OAuth.AppID) != "" {
		if err := s.bindOAuth(ctx, a.ID, in.OAuth.AppID, in.SkipVerify); err != nil {
			_, _ = s.coll().DeleteOne(ctx, bson.M{"_id": a.ID, "organizationId": org})
			return nil, err
		}
		a, err = s.load(ctx, a.ID)
		if err != nil {
			return nil, err
		}
	}
	return a, nil
}

func (s *Service) bindOAuth(ctx context.Context, accountID, appID string, skipVerify bool) error {
	if s.BindOAuthApp == nil {
		return errs.New(errs.CodeInternalError, "OAuth binding is not configured.")
	}
	return s.BindOAuthApp(ctx, accountID, strings.TrimSpace(appID), skipVerify)
}

// Patch updates an account. Changing credentials/servers queues configChanged; disabled=true disables.
func (s *Service) Patch(ctx context.Context, id string, in PatchInput, ifMatch string) (*Account, error) {
	if err := tenancy.RequireAccount(ctx, tenancy.PermAccountsWrite, id); err != nil {
		return nil, err
	}
	cur, err := s.load(ctx, id)
	if err != nil {
		return nil, err
	}
	if cur.State == StateDeleting {
		return nil, errs.New(errs.CodeAccountDeleting, "The account is being deleted.")
	}
	if ifMatch != "" && parseETag(ifMatch) != cur.Version {
		return nil, errs.New(errs.CodeVersionMismatch, "The resource version does not match If-Match.")
	}
	c, _ := tenancy.CallerFrom(ctx)
	changes := audit.Changes{}
	changedFields := []string{}
	configChanged := false
	needDisable := in.Disabled != nil && *in.Disabled
	fromState := cur.State
	stateChanged := false
	set := bson.M{}
	unset := bson.M{}
	expectVersion := cur.Version

	if in.Provider != nil {
		return nil, errs.New(errs.CodeValidationFailed, "provider cannot be changed after create.")
	}
	if in.Name != nil {
		name := strings.TrimSpace(*in.Name)
		if name == "" {
			return nil, errs.New(errs.CodeValidationFailed, "name must not be empty.")
		}
		changes["name"] = audit.FieldChange{Before: cur.Name, After: name}
		cur.Name = name
		set["name"] = name
		changedFields = append(changedFields, "name")
	}
	if in.Email != nil {
		if err := validateEmail(*in.Email); err != nil {
			return nil, err
		}
		email := strings.TrimSpace(*in.Email)
		changes["email"] = audit.FieldChange{Before: cur.Email, After: email}
		cur.Email = email
		set["email"] = email
		changedFields = append(changedFields, "email")
	}
	if in.ClearExt {
		changes["externalId"] = audit.FieldChange{Before: cur.ExternalID, After: nil}
		cur.ExternalID = nil
		unset["externalId"] = ""
		changedFields = append(changedFields, "externalId")
	} else if in.ExternalID != nil {
		if err := validateExternalID(*in.ExternalID); err != nil {
			return nil, err
		}
		ext := strings.TrimSpace(*in.ExternalID)
		changes["externalId"] = audit.FieldChange{Before: cur.ExternalID, After: ext}
		cur.ExternalID = extPtr(ext)
		set["externalId"] = ext
		changedFields = append(changedFields, "externalId")
	}
	if in.ClearDelegated {
		changes["delegated"] = audit.FieldChange{Before: cur.Delegated, After: nil}
		cur.Delegated = nil
		unset["delegated"] = ""
		changedFields = append(changedFields, "delegated")
		configChanged = true
	} else if in.Delegated != nil {
		if err := s.validateDelegatedRef(ctx, cur.OrganizationID, cur.ID, in.Delegated); err != nil {
			return nil, err
		}
		changes["delegated"] = audit.FieldChange{Before: cur.Delegated, After: in.Delegated}
		cur.Delegated = in.Delegated
		set["delegated"] = in.Delegated
		changedFields = append(changedFields, "delegated")
		configChanged = true
	}
	if in.Auth != nil {
		cur.UseAuthServer = in.Auth.UseAuthServer
		set["useAuthServer"] = in.Auth.UseAuthServer
		changes["useAuthServer"] = audit.FieldChange{After: in.Auth.UseAuthServer}
		changedFields = append(changedFields, "useAuthServer")
		if in.Auth.UseAuthServer {
			cur.IMAP.Auth.Pass = crypto.EncryptedString{}
			cur.SMTP.Auth.Pass = crypto.EncryptedString{}
			set["imap"] = cur.IMAP
			set["smtp"] = cur.SMTP
			configChanged = true
		}
	}
	credChanged := false
	if in.IMAP != nil {
		requirePass := !cur.UseAuthServer && cur.Delegated == nil && in.IMAP.Auth.Pass != ""
		if err := validateIMAPInput(*in.IMAP, requirePass); err != nil {
			return nil, err
		}
		if in.IMAP.Host != "" {
			cur.IMAP.Host = strings.TrimSpace(in.IMAP.Host)
		}
		if in.IMAP.Port != 0 {
			cur.IMAP.Port = in.IMAP.Port
		}
		if in.IMAP.TLS != "" {
			cur.IMAP.TLS = in.IMAP.TLS
		}
		if in.IMAP.Auth.User != "" {
			cur.IMAP.Auth.User = strings.TrimSpace(in.IMAP.Auth.User)
			credChanged = true
		}
		if in.IMAP.IDName != "" {
			cur.IMAP.IDName = in.IMAP.IDName
		}
		if in.IMAP.IDVersion != "" {
			cur.IMAP.IDVersion = in.IMAP.IDVersion
		}
		if in.IMAP.IDVendor != "" {
			cur.IMAP.IDVendor = in.IMAP.IDVendor
		}
		if in.IMAP.IDSupportURL != "" {
			cur.IMAP.IDSupportURL = in.IMAP.IDSupportURL
		}
		cur.IMAP.DisableRev2 = in.IMAP.DisableRev2
		if in.IMAP.Auth.Pass != "" && !cur.UseAuthServer {
			es, err := s.encryptPass(ctx, cur.OrganizationID, cur.ID, "imap.auth.pass", in.IMAP.Auth.Pass)
			if err != nil {
				return nil, errs.Wrap(err, errs.CodeInternalError, "accounts.encrypt")
			}
			cur.IMAP.Auth.Pass = es
			changes["imap.auth.pass"] = audit.FieldChange{Before: audit.Secret("x"), After: audit.Secret("y")}
			credChanged = true
		}
		set["imap"] = cur.IMAP
		changes["imap"] = audit.FieldChange{After: map[string]any{"host": cur.IMAP.Host, "port": cur.IMAP.Port, "user": cur.IMAP.Auth.User}}
		changedFields = append(changedFields, "imap")
		configChanged = true
	}
	if in.SMTP != nil {
		requirePass := !cur.UseAuthServer && cur.Delegated == nil && in.SMTP.Auth.Pass != ""
		if err := validateSMTPInput(*in.SMTP, requirePass); err != nil {
			return nil, err
		}
		if in.SMTP.Host != "" {
			cur.SMTP.Host = strings.TrimSpace(in.SMTP.Host)
		}
		if in.SMTP.Port != 0 {
			cur.SMTP.Port = in.SMTP.Port
		}
		if in.SMTP.TLS != "" {
			cur.SMTP.TLS = in.SMTP.TLS
		}
		if in.SMTP.Auth.User != "" {
			cur.SMTP.Auth.User = strings.TrimSpace(in.SMTP.Auth.User)
			credChanged = true
		}
		if in.SMTP.Policy != "" {
			cur.SMTP.Policy = in.SMTP.Policy
		}
		if in.SMTP.MaxAttempts != nil {
			cur.SMTP.MaxAttempts = in.SMTP.MaxAttempts
		}
		if in.SMTP.BackoffBase != "" {
			cur.SMTP.BackoffBase = in.SMTP.BackoffBase
		}
		cur.SMTP.GatewayID = strings.TrimSpace(in.SMTP.GatewayID)
		if in.SMTP.Auth.Pass != "" && !cur.UseAuthServer {
			es, err := s.encryptPass(ctx, cur.OrganizationID, cur.ID, "smtp.auth.pass", in.SMTP.Auth.Pass)
			if err != nil {
				return nil, errs.Wrap(err, errs.CodeInternalError, "accounts.encrypt")
			}
			cur.SMTP.Auth.Pass = es
			changes["smtp.auth.pass"] = audit.FieldChange{Before: audit.Secret("x"), After: audit.Secret("y")}
			credChanged = true
		}
		set["smtp"] = cur.SMTP
		changes["smtp"] = audit.FieldChange{After: map[string]any{"host": cur.SMTP.Host, "port": cur.SMTP.Port, "user": cur.SMTP.Auth.User}}
		changedFields = append(changedFields, "smtp")
		configChanged = true
	}
	if credChanged || in.Email != nil {
		var pin *connect.Identity
		if cur.Identity != nil {
			pin = &connect.Identity{
				Email: cur.Identity.Email, IMAPUser: cur.Identity.IMAPUser,
				OAuthSubject: cur.Identity.OAuthSubject, PinnedAt: cur.Identity.PinnedAt,
			}
		}
		next, err := connect.GuardCredentialChange(pin, connect.Observed{
			Email: cur.Email, IMAPUser: cur.IMAP.Auth.User,
		}, in.RePin)
		if err != nil {
			return nil, err
		}
		if next != nil {
			cur.Identity = &IdentityPin{
				Email: next.Email, IMAPUser: next.IMAPUser,
				OAuthSubject: next.OAuthSubject, PinnedAt: next.PinnedAt,
			}
			set["identity"] = cur.Identity
			changedFields = append(changedFields, "identity")
		}
	}
	if in.Network != nil {
		if err := validateNetwork(in.Network); err != nil {
			return nil, err
		}
		cur.Network = in.Network
		set["network"] = cur.Network
		changedFields = append(changedFields, "network")
		configChanged = true
	}
	if in.Sync != nil {
		if err := validateSync(in.Sync); err != nil {
			return nil, err
		}
		cur.Sync = *in.Sync
		set["sync"] = cur.Sync
		changedFields = append(changedFields, "sync")
	}
	if in.TZ != nil {
		cur.TZ = *in.TZ
		set["tz"] = cur.TZ
		changedFields = append(changedFields, "tz")
	}
	if in.Locale != nil {
		if err := validateLocale(*in.Locale); err != nil {
			return nil, err
		}
		cur.Locale = strings.TrimSpace(*in.Locale)
		set["locale"] = cur.Locale
		changedFields = append(changedFields, "locale")
	}
	if in.Switches != nil {
		if err := validateSwitches(in.Switches, s.registry()); err != nil {
			return nil, err
		}
		nowSw := time.Now().UTC().Truncate(time.Millisecond)
		protoChanged, err := s.applyProtocolSwitches(ctx, cur, in.Switches, nowSw)
		if err != nil {
			return nil, err
		}
		changes["switches"] = audit.FieldChange{Before: cur.Switches, After: in.Switches}
		cur.Switches = in.Switches
		set["switches"] = in.Switches
		if cur.RawDebugUntil == nil {
			unset["rawDebugUntil"] = ""
		} else {
			set["rawDebugUntil"] = *cur.RawDebugUntil
		}
		changedFields = append(changedFields, "switches")
		if protoChanged {
			configChanged = true
		}
	}
	if in.ClearMetadata || in.Metadata != nil {
		before := copyMetadata(cur.Metadata)
		var next map[string]any
		if !in.ClearMetadata {
			merged, err := mergeMetadata(cur.Metadata, in.Metadata)
			if err != nil {
				return nil, err
			}
			next = merged
		}
		if !metadataEqual(before, next) {
			changes["metadata"] = audit.FieldChange{Before: before, After: copyMetadata(next)}
			cur.Metadata = next
			if len(next) == 0 {
				unset["metadata"] = ""
			} else {
				set["metadata"] = next
			}
			changedFields = append(changedFields, "metadata")
		}
	}
	if needDisable {
		to, ok := Apply(cur.State, EventDisable)
		if !ok {
			return nil, errs.New(errs.CodeConflict, "The account cannot be disabled from its current state.").WithSafe("state", cur.State)
		}
		cur.State = to
		now := time.Now().UTC().Truncate(time.Millisecond)
		cur.StateReason = &StateReason{Kind: "config", Code: "disabled", Message: "Disabled by API", At: now}
		set["state"] = cur.State
		set["stateReason"] = cur.StateReason
		changedFields = append(changedFields, "state")
		changes["state"] = audit.FieldChange{Before: fromState, After: StateDisabled}
		stateChanged = true
	}
	wantOAuth := in.OAuth != nil && strings.TrimSpace(in.OAuth.AppID) != ""
	if len(changedFields) == 0 && !wantOAuth {
		return cur, nil
	}
	var cmd *PendingCommand
	if configChanged {
		pc := s.appendCommand(cur, CmdConfigChanged, c.Actor(), nil)
		cmd = &pc
		if to, ok := Apply(cur.State, EventCredentialsChanged); ok {
			cur.State = to
			set["state"] = to
			changedFields = append(changedFields, "state")
			stateChanged = true
		}
	}

	if len(changedFields) > 0 {
		err = s.DB.WithTx(ctx, func(txCtx context.Context) error {
			if err := s.updateAPI(txCtx, id, expectVersion, set, unset, cmd); err != nil {
				return err
			}
			ext := cur.ExternalID
			if err := s.emit(txCtx, events.Draft{
				Type: events.AccountUpdated, OrganizationID: cur.OrganizationID, AccountID: cur.ID, Subject: cur.ID,
				Data: &eventtypes.AccountUpdated{AccountRef: eventtypes.AccountRef{AccountID: cur.ID, ExternalID: ext}, Changed: changedFields},
			}); err != nil {
				return err
			}
			if stateChanged {
				var reason *eventtypes.Reason
				if cur.StateReason != nil {
					reason = &eventtypes.Reason{Kind: cur.StateReason.Kind, Code: cur.StateReason.Code, Message: cur.StateReason.Message}
				}
				return s.emit(txCtx, events.Draft{
					Type: events.AccountStateChanged, OrganizationID: cur.OrganizationID, AccountID: cur.ID, Subject: cur.ID,
					Data: &eventtypes.AccountStateChanged{
						AccountRef: eventtypes.AccountRef{AccountID: cur.ID, ExternalID: ext},
						From:       fromState, To: cur.State, Reason: reason,
					},
				})
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		cur.Version = expectVersion + 1
		cur.UpdatedAt = time.Now().UTC().Truncate(time.Millisecond)
		s.audit(ctx, "account.update", cur.ID, changes)
	}
	if wantOAuth {
		if err := s.bindOAuth(ctx, cur.ID, in.OAuth.AppID, false); err != nil {
			return nil, err
		}
		cur, err = s.load(ctx, cur.ID)
		if err != nil {
			return nil, err
		}
	}
	return cur, nil
}

// Delete marks the account deleting and queues an erasure job (202).
func (s *Service) Delete(ctx context.Context, id, ifMatch string) (*jobs.Job, error) {
	if err := tenancy.RequireAccount(ctx, tenancy.PermAccountsWrite, id); err != nil {
		return nil, err
	}
	cur, err := s.load(ctx, id)
	if err != nil {
		return nil, err
	}
	if cur.State == StateDeleting {
		return nil, errs.New(errs.CodeAccountDeleting, "The account is already being deleted.")
	}
	if err := s.refuseDeleteIfDelegatedTarget(ctx, cur.OrganizationID, cur.ID); err != nil {
		return nil, err
	}
	if ifMatch != "" && parseETag(ifMatch) != cur.Version {
		return nil, errs.New(errs.CodeVersionMismatch, "The resource version does not match If-Match.")
	}
	to, ok := Apply(cur.State, EventDelete)
	if !ok {
		return nil, errs.New(errs.CodeConflict, "The account cannot be deleted from its current state.")
	}
	from := cur.State
	now := time.Now().UTC().Truncate(time.Millisecond)
	expectVersion := cur.Version
	cur.State = to
	cur.DeletedAt = &now
	cur.StateReason = &StateReason{Kind: "config", Code: "deleting", Message: "Erasure requested", At: now}
	c, _ := tenancy.CallerFrom(ctx)
	pc := s.appendCommand(cur, CmdReconnect, c.Actor(), map[string]any{"reason": "deleting"}) // nudge owner to release

	var job *jobs.Job
	err = s.DB.WithTx(ctx, func(txCtx context.Context) error {
		if err := s.updateAPI(txCtx, id, expectVersion, bson.M{
			"state": cur.State, "stateReason": cur.StateReason, "deletedAt": now,
		}, nil, &pc); err != nil {
			return err
		}
		ext := cur.ExternalID
		if err := s.emit(txCtx, events.Draft{
			Type: events.AccountStateChanged, OrganizationID: cur.OrganizationID, AccountID: cur.ID, Subject: cur.ID,
			Data: &eventtypes.AccountStateChanged{
				AccountRef: eventtypes.AccountRef{AccountID: cur.ID, ExternalID: ext},
				From:       from, To: StateDeleting,
				Reason: &eventtypes.Reason{Kind: "config", Code: "deleting", Message: "Erasure requested"},
			},
		}); err != nil {
			return err
		}
		j, err := s.Jobs.Create(txCtx, "erasure", map[string]string{
			"accountId": cur.ID, "organizationId": cur.OrganizationID,
		})
		if err != nil {
			return err
		}
		job = j
		return nil
	})
	if err != nil {
		return nil, err
	}
	s.audit(ctx, "account.delete", cur.ID, audit.Changes{"state": {Before: from, After: StateDeleting}})
	return job, nil
}

// Transition is used by the sync engine (P1.07). It checks the fencing epoch and emits account.state_changed
// in the same transaction as the state write.
func (s *Service) Transition(ctx context.Context, accountID string, epoch uint64, to string, reason *StateReason) (*Account, error) {
	cur, err := s.load(ctx, accountID)
	if err != nil {
		return nil, err
	}
	if cur.Owner == nil || cur.Owner.Epoch != epoch {
		return nil, errs.New(errs.CodeConflict, "Fencing epoch mismatch.").WithSafe("epoch", fmt.Sprintf("%d", epoch))
	}
	from := cur.State
	if from == to {
		return cur, nil
	}
	// Find an event that yields to from from; prefer exact Apply match across AllEvents.
	ok := false
	for _, ev := range AllEvents {
		if next, yes := Apply(from, ev); yes && next == to {
			ok = true
			break
		}
	}
	if !ok {
		return nil, errs.New(errs.CodeConflict, "Illegal state transition.").WithSafe("from", from).WithSafe("to", to)
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	expectVersion := cur.Version
	cur.State = to
	set := bson.M{"state": to, "updatedAt": now}
	if reason != nil {
		reason.At = now
		cur.StateReason = reason
		set["stateReason"] = reason
	}
	filter, err := store.Scoped(ctx, bson.M{"_id": accountID, "owner.epoch": epoch, "version": expectVersion})
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "accounts.scoped")
	}
	err = s.DB.WithTx(ctx, func(txCtx context.Context) error {
		res, err := s.coll().UpdateOne(txCtx, filter, bson.M{"$set": set, "$inc": bson.M{"version": 1}})
		if err != nil {
			return errs.Wrap(err, errs.CodeInternalError, "accounts.transition")
		}
		if res.MatchedCount == 0 {
			return errs.New(errs.CodeConflict, "Fencing epoch mismatch or concurrent update.")
		}
		ext := cur.ExternalID
		var er *eventtypes.Reason
		if reason != nil {
			er = &eventtypes.Reason{Kind: reason.Kind, Code: reason.Code, Message: reason.Message}
		}
		return s.emit(txCtx, events.Draft{
			Type: events.AccountStateChanged, OrganizationID: cur.OrganizationID, AccountID: cur.ID, Subject: cur.ID,
			Data: &eventtypes.AccountStateChanged{
				AccountRef: eventtypes.AccountRef{AccountID: cur.ID, ExternalID: ext},
				From:       from, To: to, Reason: er,
			},
		})
	})
	if err != nil {
		return nil, err
	}
	cur.Version = expectVersion + 1
	cur.UpdatedAt = now
	return cur, nil
}

// Resume re-enables a disabled account (200).
func (s *Service) Resume(ctx context.Context, id string) (*Account, error) {
	if err := tenancy.RequireAccount(ctx, tenancy.PermAccountsOperate, id); err != nil {
		return nil, err
	}
	cur, err := s.load(ctx, id)
	if err != nil {
		return nil, err
	}
	to, ok := Apply(cur.State, EventResume)
	if !ok {
		return nil, errs.New(errs.CodeAccountNotDisabled, "The account is not disabled.")
	}
	from := cur.State
	now := time.Now().UTC().Truncate(time.Millisecond)
	expectVersion := cur.Version
	cur.State = to
	cur.StateReason = &StateReason{Kind: "config", Code: "resume", Message: "Resumed by API", At: now}
	cur.AuthFailingSince = nil
	// Resume is a placement signal (disabled → connecting); no sync command (S4 §6 / brief 17b).
	// Clear authFailingSince so the disable timer does not immediately re-fire after resume.

	err = s.DB.WithTx(ctx, func(txCtx context.Context) error {
		if err := s.updateAPI(txCtx, id, expectVersion, bson.M{
			"state": cur.State, "stateReason": cur.StateReason,
		}, bson.M{"authFailingSince": ""}, nil); err != nil {
			if errs.CodeOf(err) == errs.CodeVersionMismatch {
				return errs.New(errs.CodeVersionMismatch, "The resource was modified concurrently.")
			}
			return err
		}
		ext := cur.ExternalID
		return s.emit(txCtx, events.Draft{
			Type: events.AccountStateChanged, OrganizationID: cur.OrganizationID, AccountID: cur.ID, Subject: cur.ID,
			Data: &eventtypes.AccountStateChanged{
				AccountRef: eventtypes.AccountRef{AccountID: cur.ID, ExternalID: ext},
				From:       from, To: to,
				Reason: &eventtypes.Reason{Kind: "config", Code: "resume", Message: "Resumed by API"},
			},
		})
	})
	if err != nil {
		return nil, err
	}
	cur.Version = expectVersion + 1
	cur.UpdatedAt = now
	s.audit(ctx, "account.resume", cur.ID, audit.Changes{"state": {Before: from, After: to}})
	return cur, nil
}

// Sync queues a resync command and returns a tracking job (202).
func (s *Service) Sync(ctx context.Context, id string, in SyncInput) (*jobs.Job, error) {
	if err := tenancy.RequireAccount(ctx, tenancy.PermAccountsOperate, id); err != nil {
		return nil, err
	}
	if in.Mode == "" {
		in.Mode = "fast"
	}
	if in.Mode != "fast" && in.Mode != "full" {
		return nil, errs.New(errs.CodeValidationFailed, "mode must be fast or full.").WithSafe("mode", in.Mode)
	}
	cur, err := s.load(ctx, id)
	if err != nil {
		return nil, err
	}
	if cur.State == StateDeleting {
		return nil, errs.New(errs.CodeAccountDeleting, "The account is being deleted.")
	}
	c, _ := tenancy.CallerFrom(ctx)
	arg := map[string]any{"mode": in.Mode}
	if len(in.Paths) > 0 {
		arg["paths"] = in.Paths
	}
	pc := s.appendCommand(cur, CmdResync, c.Actor(), arg)
	expectVersion := cur.Version
	var job *jobs.Job
	err = s.DB.WithTx(ctx, func(txCtx context.Context) error {
		if err := s.updateAPI(txCtx, id, expectVersion, bson.M{}, nil, &pc); err != nil {
			return err
		}
		j, err := s.Jobs.Create(txCtx, "account.resync", map[string]any{"accountId": id, "mode": in.Mode, "paths": in.Paths})
		if err != nil {
			return err
		}
		job = j
		return nil
	})
	return job, err
}

// Reconnect queues a reconnect command and returns a tracking job (202).
// Retries a few times when a concurrent sync transition or OAuth token write bumps version between
// load and update (same race the e2e XOAUTH2 path hits after completeOAuth).
func (s *Service) Reconnect(ctx context.Context, id string) (*jobs.Job, error) {
	if err := tenancy.RequireAccount(ctx, tenancy.PermAccountsOperate, id); err != nil {
		return nil, err
	}
	c, _ := tenancy.CallerFrom(ctx)
	var job *jobs.Job
	var last error
	for attempt := 0; attempt < 5; attempt++ {
		cur, err := s.load(ctx, id)
		if err != nil {
			return nil, err
		}
		if cur.State == StateDeleting {
			return nil, errs.New(errs.CodeAccountDeleting, "The account is being deleted.")
		}
		pc := s.appendCommand(cur, CmdReconnect, c.Actor(), nil)
		expectVersion := cur.Version
		last = s.DB.WithTx(ctx, func(txCtx context.Context) error {
			if err := s.updateAPI(txCtx, id, expectVersion, bson.M{}, nil, &pc); err != nil {
				return err
			}
			j, err := s.Jobs.Create(txCtx, "account.reconnect", map[string]string{"accountId": id})
			if err != nil {
				return err
			}
			job = j
			return nil
		})
		if last == nil || errs.CodeOf(last) != errs.CodeVersionMismatch {
			return job, last
		}
	}
	return nil, last
}

// Flush queues a flush command: drop local sync index and silent-resync (W48 / G15).
func (s *Service) Flush(ctx context.Context, id string) (*jobs.Job, error) {
	if err := tenancy.RequireAccount(ctx, tenancy.PermAccountsOperate, id); err != nil {
		return nil, err
	}
	cur, err := s.load(ctx, id)
	if err != nil {
		return nil, err
	}
	if cur.State == StateDeleting {
		return nil, errs.New(errs.CodeAccountDeleting, "The account is being deleted.")
	}
	c, _ := tenancy.CallerFrom(ctx)
	pc := s.appendCommand(cur, CmdFlush, c.Actor(), nil)
	expectVersion := cur.Version
	var job *jobs.Job
	err = s.DB.WithTx(ctx, func(txCtx context.Context) error {
		if err := s.updateAPI(txCtx, id, expectVersion, bson.M{}, nil, &pc); err != nil {
			return err
		}
		j, err := s.Jobs.Create(txCtx, "account.flush", map[string]string{"accountId": id})
		if err != nil {
			return err
		}
		job = j
		return nil
	})
	if err == nil {
		s.audit(ctx, "account.flush", id, audit.Changes{})
	}
	return job, err
}

// checkAccountQuota enforces EB_QUOTA_ACCOUNTS and the organisation's own `accounts` quota (S9 §2.12): the
// stricter wins; accounts being deleted do not count. Waiting does not free a slot, so no Retry-After.
func (s *Service) checkAccountQuota(ctx context.Context, org string) error {
	oq, err := tenancy.LoadOrgQuotas(ctx, s.DB, org)
	if err != nil {
		return err
	}
	limit, fromOrg := tenancy.EffectiveLimit(s.QuotaAccounts, oq.Accounts)
	if limit <= 0 {
		return nil
	}
	n, err := s.coll().CountDocuments(ctx, bson.M{"organizationId": org, "state": bson.M{"$ne": StateDeleting}},
		options.Count().SetLimit(int64(limit)))
	if err != nil {
		return errs.Wrap(err, errs.CodeInternalError, "accounts.quota")
	}
	if int(n) < limit {
		return nil
	}
	if fromOrg {
		return tenancy.OrgQuotaExceeded(tenancy.QuotaAccounts, limit).WithoutRetryAfter()
	}
	return errs.New(errs.CodeQuotaAccountsExceeded, "The organisation account quota is reached.").
		WithSafe("limit", limit).WithoutRetryAfter()
}
