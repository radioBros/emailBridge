package gmail

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/mailproto/httpsend"
)

const defaultAPIRoot = "https://gmail.googleapis.com"

// Client talks to the Gmail REST API (or simulator). BaseURL overrides the host for tests.
type Client struct {
	HTTP     *http.Client
	BaseURL  string // empty → production Google API
	TokenURL string // empty → https://oauth2.googleapis.com/token (SA)
	User     string // when set, paths use /users/{email}/… instead of /users/me/…
}

// ForUser returns a copy of c whose requests address email's mailbox (/users/{email}/…). The module's
// client is shared; never set User on it from concurrent runners.
func (c *Client) ForUser(email string) *Client {
	if c == nil {
		return nil
	}
	cp := *c
	cp.User = email
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
		return strings.TrimRight(c.BaseURL, "/")
	}
	return defaultAPIRoot
}

func (c *Client) userPath() string {
	if c != nil && c.User != "" {
		return "/gmail/v1/users/" + url.PathEscape(c.User)
	}
	return "/gmail/v1/users/me"
}

func (c *Client) tokenURL() string {
	if c.TokenURL != "" {
		return c.TokenURL
	}
	return "https://oauth2.googleapis.com/token"
}

// Profile is users.getProfile.
type Profile struct {
	EmailAddress string
	HistoryID    string
}

// GetProfile calls users.getProfile.
func (c *Client) GetProfile(ctx context.Context, token string) (*Profile, error) {
	var out struct {
		EmailAddress string `json:"emailAddress"`
		HistoryID    string `json:"historyId"`
	}
	if err := c.get(ctx, token, c.userPath()+"/profile", nil, &out); err != nil {
		return nil, err
	}
	return &Profile{EmailAddress: out.EmailAddress, HistoryID: out.HistoryID}, nil
}

// Label is a Gmail label.
type Label struct {
	ID, Name, Type string
}

// ListLabels calls users.labels.list.
func (c *Client) ListLabels(ctx context.Context, token string) ([]Label, error) {
	var out struct {
		Labels []struct {
			ID, Name, Type string
		} `json:"labels"`
	}
	if err := c.get(ctx, token, c.userPath()+"/labels", nil, &out); err != nil {
		return nil, err
	}
	res := make([]Label, 0, len(out.Labels))
	for _, l := range out.Labels {
		res = append(res, Label{ID: l.ID, Name: l.Name, Type: l.Type})
	}
	return res, nil
}

// Message is a fetched Gmail message.
type Message struct {
	ID           string
	ThreadID     string
	LabelIDs     []string
	InternalDate time.Time
	Size         int64
	Raw          []byte
	Headers      map[string][]string // lower-case
}

// GetMessage fetches a message; format is metadata|raw|full|minimal.
func (c *Client) GetMessage(ctx context.Context, token, id, format string) (*Message, error) {
	if format == "" {
		format = "metadata"
	}
	q := url.Values{"format": {format}}
	var out struct {
		ID       string   `json:"id"`
		ThreadID string   `json:"threadId"`
		LabelIDs []string `json:"labelIds"`
		Internal string   `json:"internalDate"`
		Size     int64    `json:"sizeEstimate"`
		Raw      string   `json:"raw"`
		Payload  *struct {
			Headers []struct{ Name, Value string }
		} `json:"payload"`
	}
	if err := c.get(ctx, token, c.userPath()+"/messages/"+url.PathEscape(id), q, &out); err != nil {
		return nil, err
	}
	m := &Message{ID: out.ID, ThreadID: out.ThreadID, LabelIDs: out.LabelIDs, Size: out.Size, Headers: map[string][]string{}}
	if ms, err := strconv.ParseInt(out.Internal, 10, 64); err == nil {
		m.InternalDate = time.UnixMilli(ms).UTC()
	}
	if out.Raw != "" {
		raw, err := base64.RawURLEncoding.DecodeString(out.Raw)
		if err != nil {
			raw, err = base64.URLEncoding.DecodeString(out.Raw)
		}
		if err != nil {
			return nil, errs.Wrap(err, errs.CodeInternalError, "gmail.raw")
		}
		m.Raw = raw
	}
	if out.Payload != nil {
		for _, h := range out.Payload.Headers {
			k := strings.ToLower(h.Name)
			m.Headers[k] = append(m.Headers[k], h.Value)
		}
	}
	return m, nil
}

// HistoryRecord is one users.history.list entry.
type HistoryRecord struct {
	ID              string
	MessagesAdded   []string
	MessagesDeleted []string
	LabelsAdded     []LabelChange
	LabelsRemoved   []LabelChange
}

// LabelChange is a label change on a message.
type LabelChange struct {
	MessageID string
	LabelIDs  []string
}

// ErrHistoryExpired means startHistoryId is no longer valid (HTTP 404).
var ErrHistoryExpired = errs.New(errs.CodeNotFound, "Gmail history id expired.")

// ListHistory calls users.history.list.
func (c *Client) ListHistory(ctx context.Context, token, startHistoryID string) (records []HistoryRecord, newHistoryID string, err error) {
	q := url.Values{"startHistoryId": {startHistoryID}}
	var out struct {
		History []struct {
			ID            string `json:"id"`
			MessagesAdded []struct {
				Message struct {
					ID string `json:"id"`
				} `json:"message"`
			} `json:"messagesAdded"`
			MessagesDeleted []struct {
				Message struct {
					ID string `json:"id"`
				} `json:"message"`
			} `json:"messagesDeleted"`
			LabelsAdded []struct {
				Message struct {
					ID string `json:"id"`
				}
				LabelIDs []string `json:"labelIds"`
			} `json:"labelsAdded"`
			LabelsRemoved []struct {
				Message struct {
					ID string `json:"id"`
				}
				LabelIDs []string `json:"labelIds"`
			} `json:"labelsRemoved"`
		} `json:"history"`
		HistoryID string `json:"historyId"`
	}
	if err := c.get(ctx, token, c.userPath()+"/history", q, &out); err != nil {
		return nil, "", err
	}
	for _, h := range out.History {
		rec := HistoryRecord{ID: h.ID}
		for _, a := range h.MessagesAdded {
			rec.MessagesAdded = append(rec.MessagesAdded, a.Message.ID)
		}
		for _, d := range h.MessagesDeleted {
			rec.MessagesDeleted = append(rec.MessagesDeleted, d.Message.ID)
		}
		for _, a := range h.LabelsAdded {
			rec.LabelsAdded = append(rec.LabelsAdded, LabelChange{MessageID: a.Message.ID, LabelIDs: a.LabelIDs})
		}
		for _, a := range h.LabelsRemoved {
			rec.LabelsRemoved = append(rec.LabelsRemoved, LabelChange{MessageID: a.Message.ID, LabelIDs: a.LabelIDs})
		}
		records = append(records, rec)
	}
	return records, out.HistoryID, nil
}

// ListedMessage is a messages.list row (id + threadId).
type ListedMessage struct {
	ID, ThreadID string
}

// ListMessages lists message id/threadId pairs, optionally filtered by label and/or raw query.
func (c *Client) ListMessages(ctx context.Context, token, labelID string) ([]ListedMessage, error) {
	return c.SearchMessages(ctx, token, labelID, "")
}

// SearchMessages lists messages with optional labelIds and Gmail q= raw query.
func (c *Client) SearchMessages(ctx context.Context, token, labelID, rawQuery string) ([]ListedMessage, error) {
	q := url.Values{}
	if labelID != "" {
		q.Set("labelIds", labelID)
	}
	if rawQuery != "" {
		q.Set("q", rawQuery)
	}
	var out struct {
		Messages []struct {
			ID       string `json:"id"`
			ThreadID string `json:"threadId"`
		} `json:"messages"`
	}
	if err := c.get(ctx, token, c.userPath()+"/messages", q, &out); err != nil {
		return nil, err
	}
	ids := make([]ListedMessage, 0, len(out.Messages))
	for _, m := range out.Messages {
		ids = append(ids, ListedMessage{ID: m.ID, ThreadID: m.ThreadID})
	}
	return ids, nil
}

// ListMessageIDs lists message ids, optionally filtered by label.
func (c *Client) ListMessageIDs(ctx context.Context, token, labelID string) ([]string, error) {
	list, err := c.ListMessages(ctx, token, labelID)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(list))
	for _, m := range list {
		ids = append(ids, m.ID)
	}
	return ids, nil
}

// Modify adds/removes labels on a message.
func (c *Client) Modify(ctx context.Context, token, id string, add, remove []string) error {
	body := map[string]any{"addLabelIds": add, "removeLabelIds": remove}
	return c.post(ctx, token, c.userPath()+"/messages/"+url.PathEscape(id)+"/modify", body, nil)
}

// Insert stores a raw RFC822 under the given label IDs (users.messages.insert).
func (c *Client) Insert(ctx context.Context, token string, rawRFC822 []byte, labelIDs []string, internalDate time.Time) (messageID, threadID string, err error) {
	body := map[string]any{"raw": base64.RawURLEncoding.EncodeToString(rawRFC822)}
	if len(labelIDs) > 0 {
		body["labelIds"] = labelIDs
	}
	if !internalDate.IsZero() {
		body["internalDate"] = strconv.FormatInt(internalDate.UTC().UnixMilli(), 10)
	}
	var out struct {
		ID       string `json:"id"`
		ThreadID string `json:"threadId"`
	}
	if err := c.post(ctx, token, c.userPath()+"/messages", body, &out); err != nil {
		return "", "", err
	}
	return out.ID, out.ThreadID, nil
}

// Trash moves a message to TRASH.
func (c *Client) Trash(ctx context.Context, token, id string) error {
	return c.post(ctx, token, c.userPath()+"/messages/"+url.PathEscape(id)+"/trash", map[string]any{}, nil)
}

// Delete permanently deletes a message.
func (c *Client) Delete(ctx context.Context, token, id string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.apiRoot()+c.userPath()+"/messages/"+url.PathEscape(id), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	return c.do(req, nil)
}

// CreateLabel creates a user label.
func (c *Client) CreateLabel(ctx context.Context, token, name string) (*Label, error) {
	var out struct{ ID, Name, Type string }
	if err := c.post(ctx, token, c.userPath()+"/labels", map[string]any{"name": name, "labelListVisibility": "labelShow", "messageListVisibility": "show"}, &out); err != nil {
		return nil, err
	}
	return &Label{ID: out.ID, Name: out.Name, Type: out.Type}, nil
}

// DeleteLabel deletes a user label.
func (c *Client) DeleteLabel(ctx context.Context, token, id string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.apiRoot()+c.userPath()+"/labels/"+url.PathEscape(id), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	return c.do(req, nil)
}

// Send posts users.messages.send with raw base64url RFC822. Implements Sender.
func (c *Client) Send(ctx context.Context, token string, rawRFC822 []byte) (messageID, threadID string, err error) {
	body := map[string]any{"raw": base64.RawURLEncoding.EncodeToString(rawRFC822)}
	var out struct {
		ID       string `json:"id"`
		ThreadID string `json:"threadId"`
	}
	if err := c.post(ctx, token, c.userPath()+"/messages", body, &out); err != nil {
		return "", "", err
	}
	return out.ID, out.ThreadID, nil
}

// WatchResponse is users.watch.
type WatchResponse struct {
	HistoryID  string
	Expiration time.Time
}

// Watch calls users.watch.
func (c *Client) Watch(ctx context.Context, token, topicName string) (*WatchResponse, error) {
	var out struct {
		HistoryID  string `json:"historyId"`
		Expiration string `json:"expiration"`
	}
	if err := c.post(ctx, token, c.userPath()+"/watch", map[string]any{"topicName": topicName}, &out); err != nil {
		return nil, err
	}
	wr := &WatchResponse{HistoryID: out.HistoryID}
	if ms, err := strconv.ParseInt(out.Expiration, 10, 64); err == nil {
		wr.Expiration = time.UnixMilli(ms).UTC()
	}
	return wr, nil
}

func (c *Client) get(ctx context.Context, token, path string, q url.Values, dest any) error {
	u := c.apiRoot() + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	return c.do(req, dest)
}

func (c *Client) post(ctx context.Context, token, path string, body, dest any) error {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.apiRoot()+path, r)
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
		return errs.Wrap(err, errs.CodeServiceUnavailable, "gmail.http")
	}
	defer func() { _ = res.Body.Close() }()
	b, err := io.ReadAll(io.LimitReader(res.Body, 32<<20))
	if err != nil {
		return errs.Wrap(err, errs.CodeServiceUnavailable, "gmail.read")
	}
	if res.StatusCode == http.StatusNotFound && strings.Contains(req.URL.Path, "/history") {
		return ErrHistoryExpired
	}
	if res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden {
		return errs.New(errs.CodeOauthRevoked, "Gmail API rejected the access token.")
	}
	if res.StatusCode >= 400 {
		return errs.Wrap(&httpsend.StatusError{Provider: "Gmail", Status: res.StatusCode, Body: truncate(string(b), 200)}, errs.CodeServiceUnavailable, "gmail.status")
	}
	if dest == nil || len(b) == 0 {
		return nil
	}
	if err := json.Unmarshal(b, dest); err != nil {
		return errs.Wrap(err, errs.CodeInternalError, "gmail.json")
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
