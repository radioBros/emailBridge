//go:build e2e

package p0

import (
	"net/http"
	"testing"

	"github.com/radiobros/emailbridge/test/e2e/harness"
)

const (
	ownerEmail      = harness.OwnerEmail
	ownerPassword   = harness.OwnerPassword
	sessionCookie   = harness.SessionCookie
	challengeCookie = harness.ChallengeCookie
)

// Node wraps harness.Node with the p0-local Client helpers.
type Node struct{ *harness.Node }

// StartNode boots Mongo + S3 and starts `emailbridge serve` (api role).
func StartNode(t *testing.T, extraEnv map[string]string) *Node {
	t.Helper()
	return &Node{harness.StartNode(t, extraEnv)}
}

func findRepoRoot() string { return harness.FindRepoRoot() }

// Client is a cookie-jar HTTP client; ip/hdr match the pre-extract harness field names.
type Client struct {
	inner *harness.Client
	ip    string
	hdr   map[string]string
}

type resp = harness.Resp

func (n *Node) Client() *Client {
	return &Client{inner: n.NewClient()}
}

func (c *Client) sync() {
	c.inner.IP = c.ip
	c.inner.Hdr = c.hdr
}

func (c *Client) do(method, path string, body any) resp {
	c.sync()
	return c.inner.Do(method, path, body)
}

func (c *Client) cookie(name string) *http.Cookie {
	return c.inner.Cookie(name)
}

func (c *Client) signIn(email, password string) resp {
	c.sync()
	return c.inner.SignIn(email, password)
}

func (c *Client) enrolTOTP(password string) (secret, usedCode string) {
	c.sync()
	return c.inner.EnrolTOTP(password)
}

func freshTOTP(t *testing.T, secret string, used ...string) string {
	return harness.FreshTOTP(t, secret, used...)
}

func expectProblem(t *testing.T, r resp, status int, code string) {
	harness.ExpectProblem(t, r, status, code)
}

func expectStatus(t *testing.T, r resp, status int) {
	harness.ExpectStatus(t, r, status)
}

func (n *Node) OwnerSession(t *testing.T) (*Client, string) {
	t.Helper()
	c, secret := n.Node.OwnerSession(t)
	return &Client{inner: c}, secret
}

func (n *Node) SignInWithTOTP(t *testing.T, email, password, secret string, used ...string) *Client {
	t.Helper()
	return &Client{inner: n.Node.SignInWithTOTP(t, email, password, secret, used...)}
}

type passkeyDevice = harness.PasskeyDevice

func newDevice(base string) *passkeyDevice { return harness.NewDevice(base) }

func (c *Client) registerPasskey(d *passkeyDevice) resp {
	c.sync()
	return c.inner.RegisterPasskey(d)
}

func (c *Client) passkeySignIn(d *passkeyDevice) resp {
	c.sync()
	return c.inner.PasskeySignIn(d)
}

func (n *Node) seedInvite(t *testing.T, orgID, email, role string) string {
	return n.SeedInvite(t, orgID, email, role)
}
