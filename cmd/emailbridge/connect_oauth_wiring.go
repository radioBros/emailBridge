package main

import (
	"context"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/radiobros/emailbridge/internal/accounts"
	"github.com/radiobros/emailbridge/internal/autoconfig"
	"github.com/radiobros/emailbridge/internal/connect"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/jobs"
	"github.com/radiobros/emailbridge/internal/oauth"
	"github.com/radiobros/emailbridge/internal/store"
)

// connectAccounts is the accounts surface the hosted OAuth flow needs (*accounts.Service).
type connectAccounts interface {
	Create(ctx context.Context, in accounts.CreateInput) (*accounts.Account, error)
	Delete(ctx context.Context, id, ifMatch string) (*jobs.Job, error)
}

// linkOAuthStarter starts OAuth for a connect link (*oauth.Service).
type linkOAuthStarter interface {
	StartForLink(ctx context.Context, accountID, appID, linkID, returnURL string) (*oauth.StartResult, error)
}

// hostedOAuth implements connect.OAuthStartFn (P6.4 hosted page → OAuth).
//
//   - oauth-mode link (accountID set): connect that account — never create one, never re-pin its identity
//     from the form email (anyone holding the link could otherwise re-pin someone else's account).
//   - hosted link (no account): create the account once and attach it to the link (pendingAccountId,
//     compare-and-set); a retry with the same address reuses it, another address is a conflict.
//
// The link is marked used by the OAuth callback after the tokens are stored, and the browser then goes to
// the link's returnURL (oauth.StartForLink).
type hostedOAuth struct {
	db       *store.DB
	accounts connectAccounts
	oauth    linkOAuthStarter
	discover func(ctx context.Context, email string) (imap, smtp *autoconfig.Server)
	now      func() time.Time
}

func (h *hostedOAuth) start(ctx context.Context, org, email, appID, linkID, accountID, returnURL string) (string, error) {
	if h.oauth == nil {
		return "", errs.New(errs.CodeInternalError, "OAuth is not configured.")
	}
	sys := systemOrgCtx(org)
	if accountID == "" {
		id, err := h.hostedAccount(ctx, sys, org, email, linkID)
		if err != nil {
			return "", err
		}
		accountID = id
	}
	res, err := h.oauth.StartForLink(sys, accountID, appID, linkID, returnURL)
	if err != nil {
		return "", err
	}
	return res.AuthorizeURL, nil
}

func (h *hostedOAuth) hostedAccount(ctx, sys context.Context, org, email, linkID string) (string, error) {
	links := h.db.Collection(connect.CollectionLinks)
	var l struct {
		Pending string `bson:"pendingAccountId"`
	}
	if err := links.FindOne(ctx, bson.M{"_id": linkID, "organizationId": org}).Decode(&l); err != nil {
		return "", errs.New(errs.CodeConnectLinkInvalid, "The connect link is invalid or expired.")
	}
	if l.Pending != "" {
		return h.reusePending(ctx, org, l.Pending, email)
	}
	created, err := h.createAccount(sys, org, email)
	if err != nil {
		return "", err
	}
	res, err := links.UpdateOne(ctx,
		bson.M{"_id": linkID, "organizationId": org, "pendingAccountId": bson.M{"$exists": false}},
		bson.M{"$set": bson.M{"pendingAccountId": created}},
	)
	if err != nil {
		return "", errs.Wrap(err, errs.CodeInternalError, "connect.link.attach")
	}
	if res.MatchedCount == 0 {
		// A concurrent start attached its account first: drop ours, continue with the winner's.
		_, _ = h.accounts.Delete(sys, created, "")
		if err := links.FindOne(ctx, bson.M{"_id": linkID, "organizationId": org}).Decode(&l); err != nil || l.Pending == "" {
			return "", errs.New(errs.CodeConnectLinkInvalid, "The connect link is invalid or expired.")
		}
		return h.reusePending(ctx, org, l.Pending, email)
	}
	return created, nil
}

func (h *hostedOAuth) reusePending(ctx context.Context, org, accountID, email string) (string, error) {
	var a struct {
		Email string `bson:"email"`
	}
	if err := h.db.Collection(accounts.Collection).FindOne(ctx,
		bson.M{"_id": accountID, "organizationId": org}).Decode(&a); err != nil {
		return "", errs.New(errs.CodeConflict, "The account started from this connect link no longer exists; ask for a new link.")
	}
	if !strings.EqualFold(strings.TrimSpace(a.Email), strings.TrimSpace(email)) {
		return "", errs.New(errs.CodeConflict, "This connect link is already in use for another address.")
	}
	return accountID, nil
}

func (h *hostedOAuth) createAccount(sys context.Context, org, email string) (string, error) {
	var imap, smtp *autoconfig.Server
	if h.discover != nil {
		imap, smtp = h.discover(sys, email)
	}
	if imap == nil {
		imap = &autoconfig.Server{Host: "imap.gmail.com", Port: 993, TLS: "implicit", User: "%EMAIL%"}
	}
	if smtp == nil {
		smtp = &autoconfig.Server{Host: "smtp.gmail.com", Port: 465, TLS: "implicit", User: "%EMAIL%"}
	}
	imapIn, smtpIn := mailInputs(email, "", imap, smtp)
	created, err := h.accounts.Create(sys, accounts.CreateInput{
		Name: email, Email: email, IMAP: imapIn, SMTP: smtpIn, SkipVerify: true,
	})
	if err != nil {
		return "", err
	}
	now := time.Now
	if h.now != nil {
		now = h.now
	}
	pin := connect.Pin(connect.Observed{Email: email, IMAPUser: expandMailUser(imapIn.Auth.User, email)}, now().UTC())
	_, _ = h.db.Collection(accounts.Collection).UpdateOne(sys,
		bson.M{"_id": created.ID, "organizationId": org},
		bson.M{"$set": bson.M{"identity": accounts.IdentityPin{
			Email: pin.Email, IMAPUser: pin.IMAPUser, PinnedAt: pin.PinnedAt,
		}}},
	)
	return created.ID, nil
}
