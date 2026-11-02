package accounts

import (
	"context"
	"errors"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/radiobros/emailbridge/internal/errs"
)

// SMTPSettings are an account's sending settings with the password decrypted, for the submit pipeline
// (cmd/ adapts them to submit.SMTPAccount). A system read: no tenancy context.
type SMTPSettings struct {
	// OAuth: the account authenticates with OAuth tokens (P6.1); Password is then empty.
	OAuth              bool
	ID, OrganizationID string
	ExternalID         *string
	State, Email, Name string
	// Provider is accounts.provider (imap/gmail/graph); submit uses Gmail API when gmail.
	Provider             string
	Host                 string
	Port                 int
	TLS                  string
	User, Password       string
	IgnoreCertErrors     bool
	Policy               string
	MaxAttempts          int
	BackoffBase          time.Duration
	CopyToSent           string
	ProtocolLogs, RawLog bool
	GatewayID            string
	EHLO                 string
	Proxy                string
	LocalAddress         string
	LocalAddresses       []string // IP pool candidates (W57)
	SMTPStrategy         string
}

// SMTPSettings loads the account's SMTP settings (password decrypted).
func (s *Service) SMTPSettings(ctx context.Context, accountID string) (*SMTPSettings, error) {
	var a Account
	if err := s.coll().FindOne(ctx, bson.M{"_id": accountID}).Decode(&a); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, errs.New(errs.CodeNotFound, "The account does not exist.").WithSafe("accountId", accountID)
		}
		return nil, errs.Wrap(err, errs.CodeInternalError, "accounts.smtp")
	}
	user := a.SMTP.Auth.User
	pass := ""
	if a.UsesAuthServer() {
		if s.Credentials == nil {
			return nil, errs.New(errs.CodeAuthServerMisconfigured, "Credential server is not wired.")
		}
		u, p, _, err := s.Credentials(ctx, a.ID, "smtp")
		if err != nil {
			return nil, err
		}
		if u != "" {
			user = u
		}
		pass = p
	} else if !a.SMTP.Auth.Pass.IsZero() {
		p, err := s.decryptPass(ctx, a.ID, "smtp.auth.pass", a.SMTP.Auth.Pass)
		if err != nil {
			return nil, errs.Wrap(err, errs.CodeInternalError, "accounts.decrypt")
		}
		pass = p
	}
	if upn := a.DelegatedUPN(); upn != "" {
		user = upn
	}
	out := &SMTPSettings{ID: a.ID, OrganizationID: a.OrganizationID, ExternalID: a.ExternalID, State: a.State, Email: a.Email, Name: a.Name,
		OAuth: a.UsesOAuth(), Provider: normalizeProvider(a.Provider),
		Host: a.SMTP.Host, Port: a.SMTP.Port, TLS: a.SMTP.TLS, User: user, Password: pass,
		Policy: a.SMTP.Policy, CopyToSent: a.CopyToSent, RawLog: a.rawDebugActive(), GatewayID: a.SMTP.GatewayID}
	if a.SMTP.MaxAttempts != nil {
		out.MaxAttempts = *a.SMTP.MaxAttempts
	}
	if a.SMTP.BackoffBase != "" {
		out.BackoffBase, _ = time.ParseDuration(a.SMTP.BackoffBase)
	}
	poolAddrs, smtpStrat, _ := (&SyncSource{Svc: s}).orgIPPool(ctx, a.OrganizationID)
	if a.Network != nil {
		out.EHLO, out.Proxy, out.LocalAddress = a.Network.EHLOName, a.Network.Proxy, a.Network.LocalAddress
		if len(a.Network.LocalAddresses) > 0 {
			poolAddrs = a.Network.LocalAddresses
		}
		if a.Network.SMTPStrategy != "" {
			smtpStrat = a.Network.SMTPStrategy
		}
	}
	out.LocalAddresses, out.SMTPStrategy = poolAddrs, smtpStrat
	if v, ok := a.Switches["accounts.ignoreCertErrors"].(bool); ok {
		out.IgnoreCertErrors = v
	}
	if v, ok := a.Switches["protocolLogs.enabled"].(bool); ok {
		out.ProtocolLogs = v
	}
	return out, nil
}
