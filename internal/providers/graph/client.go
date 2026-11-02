package graph

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/mailproto/httpsend"
)

const defaultAPIRoot = "https://graph.microsoft.com/v1.0"

// Client talks to the Microsoft Graph REST API (or simulator). BaseURL overrides the host for tests.
type Client struct {
	HTTP     *http.Client
	BaseURL  string // empty → production Graph
	TokenURL string // empty → login.microsoftonline.com (client_credentials)
	UPN      string // when set, paths use /users/{upn}/… instead of /me/…
}

// ForUser returns a copy of c whose requests address upn's mailbox (/users/{upn}/…). The module's client is
// shared by every account, so a runner must never set UPN on it: concurrent accounts would read each
// other's mailboxes (with a tenant-wide app-only token, account A could sync mailbox B).
func (c *Client) ForUser(upn string) *Client {
	if c == nil {
		return nil
	}
	cp := *c
	cp.UPN = upn
	return &cp
}

func (c *Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}

func (c *Client) apiRoot() string {
	if c.BaseURL != "" {
		return strings.TrimRight(c.BaseURL, "/") + "/v1.0"
	}
	return defaultAPIRoot
}

func (c *Client) userPrefix() string {
	if c.UPN != "" {
		return "/users/" + url.PathEscape(c.UPN)
	}
	return "/me"
}

// Folder is a Graph mailFolder.
type Folder struct {
	ID, DisplayName, WellKnownName string
}

// Message is a Graph message resource (subset).
type Message struct {
	ID                string
	ConversationID    string
	Subject           string
	InternetMessageID string
	IsRead, IsDraft   bool
	Flagged           bool
	Received          time.Time
	Size              int64
	ParentFolderID    string
	From              string
	To                []string
	Cc                []string
	Removed           bool
	Raw               []byte
}

// DeltaPage is one messages/delta response.
type DeltaPage struct {
	Messages  []Message
	NextLink  string
	DeltaLink string
}

// ErrSyncStateNotFound means the delta token is invalid (HTTP 410 / syncStateNotFound).
var ErrSyncStateNotFound = errs.New(errs.CodeNotFound, "Graph delta sync state not found.")

type graphMsgJSON struct {
	ID                string `json:"id"`
	ConversationID    string `json:"conversationId"`
	Subject           string `json:"subject"`
	InternetMessageID string `json:"internetMessageId"`
	IsRead            bool   `json:"isRead"`
	IsDraft           bool   `json:"isDraft"`
	Flag              *struct {
		FlagStatus string `json:"flagStatus"`
	} `json:"flag"`
	Received       string `json:"receivedDateTime"`
	Size           int64  `json:"size"`
	ParentFolderID string `json:"parentFolderId"`
	From           *struct {
		EmailAddress struct {
			Address string `json:"address"`
		} `json:"emailAddress"`
	} `json:"from"`
	ToRecipients []struct {
		EmailAddress struct {
			Address string `json:"address"`
		} `json:"emailAddress"`
	} `json:"toRecipients"`
	CcRecipients []struct {
		EmailAddress struct {
			Address string `json:"address"`
		} `json:"emailAddress"`
	} `json:"ccRecipients"`
	Removed *struct {
		Reason string `json:"reason"`
	} `json:"@removed"`
}

func parseMessage(v graphMsgJSON) Message {
	m := Message{
		ID: v.ID, ConversationID: v.ConversationID, Subject: v.Subject,
		InternetMessageID: v.InternetMessageID, IsRead: v.IsRead, IsDraft: v.IsDraft,
		ParentFolderID: v.ParentFolderID, Size: v.Size, Removed: v.Removed != nil,
	}
	if v.Flag != nil {
		m.Flagged = strings.EqualFold(v.Flag.FlagStatus, "flagged")
	}
	if t, err := time.Parse(time.RFC3339, v.Received); err == nil {
		m.Received = t.UTC()
	}
	if v.From != nil {
		m.From = v.From.EmailAddress.Address
	}
	for _, a := range v.ToRecipients {
		m.To = append(m.To, a.EmailAddress.Address)
	}
	for _, a := range v.CcRecipients {
		m.Cc = append(m.Cc, a.EmailAddress.Address)
	}
	return m
}

// ListFolders calls GET …/mailFolders.
func (c *Client) ListFolders(ctx context.Context, token string) ([]Folder, error) {
	var out struct {
		Value []struct {
			ID, DisplayName, WellKnownName string
		} `json:"value"`
	}
	if err := c.get(ctx, token, c.userPrefix()+"/mailFolders", nil, &out); err != nil {
		return nil, err
	}
	res := make([]Folder, 0, len(out.Value))
	for _, f := range out.Value {
		res = append(res, Folder{ID: f.ID, DisplayName: f.DisplayName, WellKnownName: f.WellKnownName})
	}
	return res, nil
}

// DeltaMessages runs messages/delta for a folder. deltaLink empty → initial sync.
func (c *Client) DeltaMessages(ctx context.Context, token, folderID, deltaLink string) (*DeltaPage, error) {
	var out struct {
		Value     []graphMsgJSON `json:"value"`
		NextLink  string         `json:"@odata.nextLink"`
		DeltaLink string         `json:"@odata.deltaLink"`
	}
	var err error
	if deltaLink != "" {
		err = c.getURL(ctx, token, deltaLink, &out)
	} else {
		path := c.userPrefix() + "/mailFolders/" + url.PathEscape(folderID) + "/messages/delta"
		err = c.get(ctx, token, path, nil, &out)
	}
	if err != nil {
		return nil, err
	}
	page := &DeltaPage{NextLink: out.NextLink, DeltaLink: out.DeltaLink}
	for _, v := range out.Value {
		page.Messages = append(page.Messages, parseMessage(v))
	}
	return page, nil
}

// GetMessage fetches a single message.
func (c *Client) GetMessage(ctx context.Context, token, id string) (*Message, error) {
	var v graphMsgJSON
	if err := c.get(ctx, token, c.userPrefix()+"/messages/"+url.PathEscape(id), nil, &v); err != nil {
		return nil, err
	}
	m := parseMessage(v)
	return &m, nil
}

// GetMIME fetches GET …/messages/{id}/$value.
func (c *Client) GetMIME(ctx context.Context, token, id string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.apiRoot()+c.userPrefix()+"/messages/"+url.PathEscape(id)+"/$value", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := c.http().Do(req)
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeServiceUnavailable, "graph.http")
	}
	defer func() { _ = res.Body.Close() }()
	b, err := io.ReadAll(io.LimitReader(res.Body, 32<<20))
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeServiceUnavailable, "graph.read")
	}
	if res.StatusCode >= 400 {
		return nil, mapStatus(res.StatusCode, b)
	}
	return b, nil
}

// Patch updates isRead / flag on a message.
func (c *Client) Patch(ctx context.Context, token, id string, body map[string]any) error {
	return c.write(ctx, token, http.MethodPatch, c.userPrefix()+"/messages/"+url.PathEscape(id), body, nil)
}

// Move moves a message to destinationId.
func (c *Client) Move(ctx context.Context, token, id, destinationID string) error {
	return c.write(ctx, token, http.MethodPost, c.userPrefix()+"/messages/"+url.PathEscape(id)+"/move",
		map[string]any{"destinationId": destinationID}, nil)
}

// Delete deletes a message.
func (c *Client) Delete(ctx context.Context, token, id string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete,
		c.apiRoot()+c.userPrefix()+"/messages/"+url.PathEscape(id), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	return c.do(req, nil)
}

// CreateFolder posts a mailFolder.
func (c *Client) CreateFolder(ctx context.Context, token, displayName string) (*Folder, error) {
	var out struct {
		ID, DisplayName, WellKnownName string
	}
	if err := c.write(ctx, token, http.MethodPost, c.userPrefix()+"/mailFolders",
		map[string]any{"displayName": displayName}, &out); err != nil {
		return nil, err
	}
	return &Folder{ID: out.ID, DisplayName: out.DisplayName, WellKnownName: out.WellKnownName}, nil
}

// DeleteFolder deletes a mailFolder.
func (c *Client) DeleteFolder(ctx context.Context, token, folderID string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete,
		c.apiRoot()+c.userPrefix()+"/mailFolders/"+url.PathEscape(folderID), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	return c.do(req, nil)
}

// RenameFolder patches displayName.
func (c *Client) RenameFolder(ctx context.Context, token, folderID, displayName string) error {
	return c.write(ctx, token, http.MethodPatch, c.userPrefix()+"/mailFolders/"+url.PathEscape(folderID),
		map[string]any{"displayName": displayName}, nil)
}

// CreateMIME uploads an RFC822 into a folder (POST …/mailFolders/{id}/messages with MIME).
func (c *Client) CreateMIME(ctx context.Context, token, folderID string, rawRFC822 []byte) (messageID string, err error) {
	payload := base64.StdEncoding.EncodeToString(rawRFC822)
	path := c.userPrefix() + "/mailFolders/" + url.PathEscape(folderID) + "/messages"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.apiRoot()+path, strings.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "text/plain")
	res, err := c.http().Do(req)
	if err != nil {
		return "", errs.Wrap(err, errs.CodeServiceUnavailable, "graph.http")
	}
	defer func() { _ = res.Body.Close() }()
	b, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return "", errs.Wrap(err, errs.CodeServiceUnavailable, "graph.read")
	}
	if res.StatusCode >= 400 {
		return "", mapStatus(res.StatusCode, b)
	}
	var out struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(b, &out)
	return out.ID, nil
}

// SearchMessages lists messages; when useSearch is true, uses $search=text, else $filter where possible.
func (c *Client) SearchMessages(ctx context.Context, token, folderID, text string, useSearch bool) ([]Message, error) {
	q := url.Values{}
	path := c.userPrefix() + "/messages"
	if folderID != "" {
		path = c.userPrefix() + "/mailFolders/" + url.PathEscape(folderID) + "/messages"
	}
	if useSearch && text != "" {
		q.Set("$search", `"`+text+`"`)
	} else if text != "" {
		q.Set("$filter", "contains(subject,'"+strings.ReplaceAll(text, "'", "''")+"')")
	}
	var out struct {
		Value []graphMsgJSON `json:"value"`
	}
	if err := c.get(ctx, token, path, q, &out); err != nil {
		return nil, err
	}
	res := make([]Message, 0, len(out.Value))
	for _, v := range out.Value {
		res = append(res, parseMessage(v))
	}
	return res, nil
}

// Send posts sendMail with Graph MIME upload: Content-Type text/plain, body = base64(RFC822).
func (c *Client) Send(ctx context.Context, token string, rawRFC822 []byte) (messageID, conversationID string, err error) {
	payload := base64.StdEncoding.EncodeToString(rawRFC822)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.apiRoot()+c.userPrefix()+"/sendMail",
		strings.NewReader(payload))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "text/plain")
	res, err := c.http().Do(req)
	if err != nil {
		return "", "", errs.Wrap(err, errs.CodeServiceUnavailable, "graph.http")
	}
	defer func() { _ = res.Body.Close() }()
	b, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return "", "", errs.Wrap(err, errs.CodeServiceUnavailable, "graph.read")
	}
	if res.StatusCode >= 400 {
		return "", "", mapStatus(res.StatusCode, b)
	}
	// Graph returns 202 with empty body; the simulator may echo ids for tests.
	var out struct {
		ID             string `json:"id"`
		ConversationID string `json:"conversationId"`
	}
	if len(b) > 0 {
		_ = json.Unmarshal(b, &out)
	}
	return out.ID, out.ConversationID, nil
}

// Subscription is a Graph change-notification subscription.
type Subscription struct {
	ID, Resource, ClientState, NotificationURL, ChangeType string
	Expiration                                             time.Time
}

// CreateSubscription posts /subscriptions.
func (c *Client) CreateSubscription(ctx context.Context, token string, sub Subscription) (*Subscription, error) {
	changeType := sub.ChangeType
	if changeType == "" {
		changeType = "created,updated,deleted"
	}
	body := map[string]any{
		"changeType":         changeType,
		"notificationUrl":    sub.NotificationURL,
		"resource":           sub.Resource,
		"expirationDateTime": sub.Expiration.UTC().Format(time.RFC3339),
		"clientState":        sub.ClientState,
	}
	var out struct {
		ID, Resource, ClientState, NotificationURL, ChangeType string
		ExpirationDateTime                                     string `json:"expirationDateTime"`
	}
	if err := c.write(ctx, token, http.MethodPost, "/subscriptions", body, &out); err != nil {
		return nil, err
	}
	exp, _ := time.Parse(time.RFC3339, out.ExpirationDateTime)
	return &Subscription{
		ID: out.ID, Resource: out.Resource, ClientState: out.ClientState,
		NotificationURL: out.NotificationURL, ChangeType: out.ChangeType, Expiration: exp.UTC(),
	}, nil
}

// RenewSubscription patches expirationDateTime.
func (c *Client) RenewSubscription(ctx context.Context, token, id string, exp time.Time) (*Subscription, error) {
	var out struct {
		ID                 string `json:"id"`
		ExpirationDateTime string `json:"expirationDateTime"`
		ClientState        string `json:"clientState"`
		Resource           string `json:"resource"`
	}
	if err := c.write(ctx, token, http.MethodPatch, "/subscriptions/"+url.PathEscape(id),
		map[string]any{"expirationDateTime": exp.UTC().Format(time.RFC3339)}, &out); err != nil {
		return nil, err
	}
	e, _ := time.Parse(time.RFC3339, out.ExpirationDateTime)
	return &Subscription{ID: out.ID, Expiration: e.UTC(), ClientState: out.ClientState, Resource: out.Resource}, nil
}

func (c *Client) get(ctx context.Context, token, path string, q url.Values, dest any) error {
	u := c.apiRoot() + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	return c.getURL(ctx, token, u, dest)
}

func (c *Client) getURL(ctx context.Context, token, u string, dest any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	return c.do(req, dest)
}

func (c *Client) write(ctx context.Context, token, method, path string, body, dest any) error {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.apiRoot()+path, r)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	return c.do(req, dest)
}

func (c *Client) do(req *http.Request, dest any) error {
	res, err := c.http().Do(req)
	if err != nil {
		return errs.Wrap(err, errs.CodeServiceUnavailable, "graph.http")
	}
	defer func() { _ = res.Body.Close() }()
	b, err := io.ReadAll(io.LimitReader(res.Body, 32<<20))
	if err != nil {
		return errs.Wrap(err, errs.CodeServiceUnavailable, "graph.read")
	}
	if res.StatusCode == http.StatusNoContent || res.StatusCode == http.StatusAccepted {
		if dest != nil && len(b) > 0 {
			if err := json.Unmarshal(b, dest); err != nil {
				return errs.Wrap(err, errs.CodeInternalError, "graph.json")
			}
		}
		return nil
	}
	if res.StatusCode == http.StatusCreated {
		if dest != nil && len(b) > 0 {
			if err := json.Unmarshal(b, dest); err != nil {
				return errs.Wrap(err, errs.CodeInternalError, "graph.json")
			}
		}
		return nil
	}
	if res.StatusCode >= 400 {
		return mapStatus(res.StatusCode, b)
	}
	if dest == nil || len(b) == 0 {
		return nil
	}
	if err := json.Unmarshal(b, dest); err != nil {
		return errs.Wrap(err, errs.CodeInternalError, "graph.json")
	}
	return nil
}

func mapStatus(code int, body []byte) error {
	s := string(body)
	if code == http.StatusGone || strings.Contains(s, "syncStateNotFound") {
		return ErrSyncStateNotFound
	}
	if code == http.StatusUnauthorized || code == http.StatusForbidden {
		return errs.New(errs.CodeOauthRevoked, "Graph API rejected the access token.")
	}
	if code == http.StatusNotFound {
		return errs.New(errs.CodeNotFound, "Graph resource not found.")
	}
	return errs.Wrap(&httpsend.StatusError{Provider: "Graph", Status: code, Body: truncate(s, 200)}, errs.CodeServiceUnavailable, "graph.status")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
