package oauth

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/radiobros/emailbridge/internal/accounts"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

// ServerSignature is one provider signature entry.
type ServerSignature struct {
	Email       string `json:"email" doc:"Send-as address the signature belongs to." example:"sales@example.org"`
	DisplayName string `json:"displayName,omitempty" doc:"Display name configured for the address." example:"Acme Sales"`
	IsDefault   bool   `json:"isDefault,omitempty" doc:"The default send-as address." example:"true"`
	IsPrimary   bool   `json:"isPrimary,omitempty" doc:"The account's primary address." example:"true"`
	Signature   string `json:"signature,omitempty" doc:"Signature HTML as stored at the provider." example:"<div>Anna Verdi<br>Acme Sales · +39 02 1234 5678</div>"`
}

// ServerSignatures is GET …/server-signatures.
type ServerSignatures struct {
	Data   []ServerSignature `json:"data" doc:"Signatures found at the provider; empty when it offers none."`
	Reason string            `json:"reason,omitempty" doc:"Why data is empty: graph_signatures_unavailable (Microsoft Graph does not expose signatures)." example:"graph_signatures_unavailable"`
}

// ListServerSignatures returns Gmail sendAs signatures, or an empty Graph list with reason.
func (s *Service) ListServerSignatures(ctx context.Context, accountID string) (*ServerSignatures, error) {
	if err := tenancy.RequireReadAccount(ctx, "accounts", accountID); err != nil {
		return nil, err
	}
	if !store.IsID(store.PrefixAccount, accountID) {
		return nil, errs.New(errs.CodeNotFound, "The account does not exist.")
	}
	org, ok := store.OrganizationFrom(ctx)
	if !ok {
		return nil, errs.New(errs.CodeForbidden, "An organisation context is required.")
	}
	var doc struct {
		Provider string        `bson:"provider"`
		OAuth    *AccountOAuth `bson:"oauth"`
		Email    string        `bson:"email"`
	}
	err := s.Repo.DB.Collection(accounts.Collection).FindOne(ctx, bson.M{
		"_id": accountID, "organizationId": org,
	}).Decode(&doc)
	if err != nil {
		return nil, errs.New(errs.CodeNotFound, "The account does not exist.")
	}
	prov := strings.ToLower(strings.TrimSpace(doc.Provider))
	if prov == "" {
		prov = accounts.ProviderIMAP
	}
	switch prov {
	case accounts.ProviderGraph:
		return &ServerSignatures{Data: []ServerSignature{}, Reason: "graph_signatures_unavailable"}, nil
	case accounts.ProviderGmail:
		return s.gmailSignatures(ctx, accountID, doc.Email)
	case accounts.ProviderIMAP:
		if doc.OAuth == nil || doc.OAuth.AppID == "" {
			return nil, errs.New(errs.CodeUnsupportedForProvider, "Server signatures require a Gmail or Graph account.")
		}
		app, err := s.Repo.GetAppByID(ctx, org, doc.OAuth.AppID)
		if err != nil || app.Provider != ProviderGmail {
			return nil, errs.New(errs.CodeUnsupportedForProvider, "Server signatures require a Gmail OAuth link for IMAP accounts.")
		}
		return s.gmailSignatures(ctx, accountID, doc.Email)
	default:
		return nil, errs.New(errs.CodeUnsupportedForProvider, "Server signatures are not supported for this provider.")
	}
}

func (s *Service) gmailSignatures(ctx context.Context, accountID, email string) (*ServerSignatures, error) {
	tok, _, err := s.AccessToken(ctx, accountID)
	if err != nil {
		return nil, err
	}
	list, err := fetchGmailSendAs(ctx, s.httpClient(), s.gmailAPIRoot(), email, tok)
	if err != nil {
		return nil, err
	}
	return &ServerSignatures{Data: list}, nil
}

func (s *Service) gmailAPIRoot() string {
	if s.GmailAPIRoot != "" {
		return strings.TrimRight(s.GmailAPIRoot, "/")
	}
	return "https://gmail.googleapis.com"
}

func fetchGmailSendAs(ctx context.Context, cli *http.Client, apiRoot, email, token string) ([]ServerSignature, error) {
	user := "me"
	if strings.TrimSpace(email) != "" {
		user = url.PathEscape(email)
	}
	u := strings.TrimRight(apiRoot, "/") + "/gmail/v1/users/" + user + "/settings/sendAs"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if cli == nil {
		cli = http.DefaultClient
	}
	res, err := cli.Do(req)
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeServiceUnavailable, "gmail.sendAs")
	}
	defer func() { _ = res.Body.Close() }()
	b, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeServiceUnavailable, "gmail.sendAs.read")
	}
	if res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden {
		return nil, errs.New(errs.CodeOauthRevoked, "Gmail API rejected the access token.")
	}
	if res.StatusCode >= 400 {
		return nil, errs.New(errs.CodeServiceUnavailable, "Gmail sendAs list failed.").WithSafe("status", res.StatusCode)
	}
	var out struct {
		SendAs []struct {
			SendAsEmail string `json:"sendAsEmail"`
			DisplayName string `json:"displayName"`
			IsDefault   bool   `json:"isDefault"`
			IsPrimary   bool   `json:"isPrimary"`
			Signature   string `json:"signature"`
		} `json:"sendAs"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "gmail.sendAs.decode")
	}
	list := make([]ServerSignature, 0, len(out.SendAs))
	for _, e := range out.SendAs {
		list = append(list, ServerSignature{
			Email: e.SendAsEmail, DisplayName: e.DisplayName,
			IsDefault: e.IsDefault, IsPrimary: e.IsPrimary, Signature: e.Signature,
		})
	}
	if list == nil {
		list = []ServerSignature{}
	}
	return list, nil
}
