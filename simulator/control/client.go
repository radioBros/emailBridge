// Package control is the HTTP client for the provider simulator control API.
package control

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/radiobros/emailbridge/simulator"
)

// Client talks to the simulator control API.
type Client struct {
	Base string
	HTTP *http.Client
}

// NewClient creates a client for baseURL (host:port or full URL).
func NewClient(base string) *Client {
	if !strings.HasPrefix(base, "http://") && !strings.HasPrefix(base, "https://") {
		base = "http://" + base
	}
	return &Client{Base: strings.TrimRight(base, "/"), HTTP: &http.Client{Timeout: 30 * time.Second}}
}

func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.Base+path, body)
	if err != nil {
		return err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	b, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if err != nil {
		return err
	}
	if res.StatusCode >= 300 {
		return fmt.Errorf("control %s %s: %s: %s", method, path, res.Status, bytes.TrimSpace(b))
	}
	if out == nil || len(b) == 0 || string(b) == "null\n" {
		return nil
	}
	return json.Unmarshal(b, out)
}

func userPath(user string) string {
	return "/users/" + url.PathEscape(user)
}

// Reset clears simulator state.
func (c *Client) Reset(ctx context.Context) error {
	return c.do(ctx, http.MethodPost, "/reset", nil, nil)
}

// CreateUser creates a user with default folders.
func (c *Client) CreateUser(ctx context.Context, username, password string) error {
	return c.do(ctx, http.MethodPost, "/users", map[string]string{"username": username, "password": password}, nil)
}

// DeleteUser removes a user.
func (c *Client) DeleteUser(ctx context.Context, username string) error {
	return c.do(ctx, http.MethodDelete, userPath(username), nil, nil)
}

// CreateMailbox creates a mailbox; specialUse is optional (\Sent, …).
func (c *Client) CreateMailbox(ctx context.Context, user, name string, specialUse ...string) error {
	return c.do(ctx, http.MethodPost, userPath(user)+"/mailboxes",
		map[string]any{"name": name, "specialUse": specialUse}, nil)
}

// DeleteMailbox deletes a mailbox.
func (c *Client) DeleteMailbox(ctx context.Context, user, name string) error {
	return c.do(ctx, http.MethodPost, userPath(user)+"/mailbox/delete", map[string]string{"name": name}, nil)
}

// InjectResult is returned by InjectMessage.
type InjectResult struct {
	UID         uint32 `json:"uid"`
	UIDValidity uint32 `json:"uidValidity"`
}

// InjectMessage injects raw RFC 822 into a mailbox.
func (c *Client) InjectMessage(ctx context.Context, user, mailbox string, raw []byte, flags []string, internalDate time.Time) (*InjectResult, error) {
	in := map[string]any{"mailbox": mailbox, "raw": string(raw), "flags": flags}
	if !internalDate.IsZero() {
		in["internalDate"] = internalDate
	}
	var out InjectResult
	if err := c.do(ctx, http.MethodPost, userPath(user)+"/mailbox/inject", in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// SetFlags updates message flags (op: add|remove|set).
func (c *Client) SetFlags(ctx context.Context, user, mailbox string, uids []uint32, flags []string, op string) error {
	return c.do(ctx, http.MethodPost, userPath(user)+"/mailbox/flags",
		map[string]any{"mailbox": mailbox, "uids": uids, "flags": flags, "op": op}, nil)
}

// Expunge expunges \Deleted messages (optionally limited to uids).
func (c *Client) Expunge(ctx context.Context, user, mailbox string, uids []uint32) error {
	return c.do(ctx, http.MethodPost, userPath(user)+"/mailbox/expunge",
		map[string]any{"mailbox": mailbox, "uids": uids}, nil)
}

// RenameMailbox renames a folder.
func (c *Client) RenameMailbox(ctx context.Context, user, oldName, newName string) error {
	return c.do(ctx, http.MethodPost, userPath(user)+"/mailbox/rename",
		map[string]string{"name": oldName, "newName": newName}, nil)
}

// RecreateMailbox empties a folder with a new UIDVALIDITY.
func (c *Client) RecreateMailbox(ctx context.Context, user, name string) (uidValidity uint32, err error) {
	var out struct {
		UIDValidity uint32 `json:"uidValidity"`
	}
	if err := c.do(ctx, http.MethodPost, userPath(user)+"/mailbox/recreate", map[string]string{"name": name}, &out); err != nil {
		return 0, err
	}
	return out.UIDValidity, nil
}

// SetCaps toggles capabilities for a user.
func (c *Client) SetCaps(ctx context.Context, user string, caps simulator.Caps) error {
	return c.do(ctx, http.MethodPut, userPath(user)+"/caps", caps, nil)
}

// GetCaps returns capability toggles.
func (c *Client) GetCaps(ctx context.Context, user string) (simulator.Caps, error) {
	var out simulator.Caps
	err := c.do(ctx, http.MethodGet, userPath(user)+"/caps", nil, &out)
	return out, err
}

// SessionStats returns the user's IMAP session counters (logins, open now, peak open).
func (c *Client) SessionStats(ctx context.Context, user string) (simulator.SessionStats, error) {
	var out simulator.SessionStats
	err := c.do(ctx, http.MethodGet, userPath(user)+"/sessions", nil, &out)
	return out, err
}

// SetIMAPFault arms an IMAP fault.
func (c *Client) SetIMAPFault(ctx context.Context, user string, f simulator.IMAPFault) error {
	return c.do(ctx, http.MethodPut, userPath(user)+"/faults/imap", f, nil)
}

// ClearIMAPFault clears IMAP faults.
func (c *Client) ClearIMAPFault(ctx context.Context, user string) error {
	return c.do(ctx, http.MethodDelete, userPath(user)+"/faults/imap", nil, nil)
}

// SetSMTPScript arms SMTP stage scripts.
func (c *Client) SetSMTPScript(ctx context.Context, user string, script simulator.SMTPScript) error {
	return c.do(ctx, http.MethodPut, userPath(user)+"/faults/smtp", script, nil)
}

// ClearSMTPScript clears SMTP scripts.
func (c *Client) ClearSMTPScript(ctx context.Context, user string) error {
	return c.do(ctx, http.MethodDelete, userPath(user)+"/faults/smtp", nil, nil)
}

// ListSMTPMessages returns accepted SMTP messages.
func (c *Client) ListSMTPMessages(ctx context.Context) ([]simulator.ReceivedSMTP, error) {
	var out []simulator.ReceivedSMTP
	err := c.do(ctx, http.MethodGet, "/smtp/messages", nil, &out)
	return out, err
}

// ClearSMTPMessages clears the SMTP log.
func (c *Client) ClearSMTPMessages(ctx context.Context) error {
	return c.do(ctx, http.MethodDelete, "/smtp/messages", nil, nil)
}

// StartFleet starts fleet mode.
func (c *Client) StartFleet(ctx context.Context, opt simulator.FleetOptions) error {
	return c.do(ctx, http.MethodPost, "/fleet", opt, nil)
}

// StopFleet stops fleet mode.
func (c *Client) StopFleet(ctx context.Context) error {
	return c.do(ctx, http.MethodDelete, "/fleet", nil, nil)
}

// Addrs returns bound listen addresses.
func (c *Client) Addrs(ctx context.Context) (simulator.Addresses, error) {
	var out simulator.Addresses
	err := c.do(ctx, http.MethodGet, "/addrs", nil, &out)
	return out, err
}

// PECCA fetches the simulator PEC CA certificate PEM.
func (c *Client) PECCA(ctx context.Context) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.Base+"/pec/ca.pem", nil)
	if err != nil {
		return nil, err
	}
	res, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = res.Body.Close() }()
	b, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if res.StatusCode >= 300 {
		return nil, fmt.Errorf("control GET /pec/ca.pem: %s: %s", res.Status, bytes.TrimSpace(b))
	}
	return b, nil
}

// PECReceipt is one generated PEC system message (control API).
type PECReceipt struct {
	ID           string    `json:"id"`
	Type         string    `json:"type"`
	RefMessageID string    `json:"refMessageId"`
	TransportID  string    `json:"identificativo"` // PEC daticert field (S17)
	At           time.Time `json:"at"`
	User         string    `json:"user"`
}

// ListPECReceipts lists generated PEC receipts (optional user filter).
func (c *Client) ListPECReceipts(ctx context.Context, user string) ([]PECReceipt, error) {
	path := "/pec/receipts"
	if user != "" {
		path += "?user=" + url.QueryEscape(user)
	}
	var out []PECReceipt
	err := c.do(ctx, http.MethodGet, path, nil, &out)
	return out, err
}

// SetPECDelay sets a per-mailbox delivery delay in milliseconds.
func (c *Client) SetPECDelay(ctx context.Context, user string, delayMs int64) error {
	return c.do(ctx, http.MethodPut, "/pec/users/"+url.PathEscape(user)+"/delay",
		map[string]any{"delayMs": delayMs}, nil)
}

// ForcePECReceipt forces the next delivery-side receipt type for sends from user.
func (c *Client) ForcePECReceipt(ctx context.Context, user, typ string) error {
	return c.do(ctx, http.MethodPut, "/pec/users/"+url.PathEscape(user)+"/force-receipt",
		map[string]string{"type": typ}, nil)
}

// ClearPECForce clears a forced receipt type.
func (c *Client) ClearPECForce(ctx context.Context, user string) error {
	return c.do(ctx, http.MethodDelete, "/pec/users/"+url.PathEscape(user)+"/force-receipt", nil, nil)
}
