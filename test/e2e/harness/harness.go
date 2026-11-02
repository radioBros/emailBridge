//go:build e2e

// Package harness is the shared EmailBridge end-to-end test harness (build tag e2e).
package harness

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/descope/virtualwebauthn"
	"github.com/pquerna/otp/totp"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/test/containers"
	"github.com/radiobros/emailbridge/test/tmpsweep"
)

const (
	OwnerEmail      = "owner@e2e.test"
	OwnerPassword   = "correct horse battery staple"
	SessionCookie   = "eb_session"
	ChallengeCookie = "session_2fa"
)

// Node is one real emailbridge serve subprocess with Mongo, S3 and embedded NATS.
type Node struct {
	T        *testing.T
	Base     string // http://localhost:<port>
	Addr     string // host:port
	MongoURI string
	Database string
	DataDir  string
	cmd      *exec.Cmd
	cancel   context.CancelFunc
	stderr   *bytes.Buffer
	exited   chan struct{}     // closed when the serve process has exited
	env      map[string]string // kept so Restart boots the same node (same keys, database and data dir)
}

var (
	buildOnce sync.Once
	buildPath string
	buildErr  error
)

// BinaryPath returns a freshly built emailbridge binary for the tree under test
// (or EB_E2E_BIN). Always rebuilds once per process unless EB_E2E_BIN is set.
func BinaryPath(t *testing.T) string {
	t.Helper()
	buildOnce.Do(func() {
		if p := os.Getenv("EB_E2E_BIN"); p != "" {
			buildPath = p
			return
		}
		tmpsweep.Sweep("eb-e2e-bin-", 6*time.Hour)
		dir, err := os.MkdirTemp("", "eb-e2e-bin-*")
		if err != nil {
			buildErr = err
			return
		}
		name := "emailbridge"
		if strings.EqualFold(os.Getenv("GOOS"), "windows") ||
			(os.Getenv("GOOS") == "" && strings.Contains(strings.ToLower(os.Getenv("OS")), "windows")) {
			name += ".exe"
		}
		buildPath = filepath.Join(dir, name)
		root := FindRepoRoot()
		args := []string{"build", "-o", buildPath}
		if _, err := os.Stat(filepath.Join(root, "internal", "webui", "dist", "index.html")); err == nil {
			args = append(args, "-tags", "embedweb")
		}
		cmd := exec.Command("go", append(args, "./cmd/emailbridge")...) //nolint:gosec // G204: fixed args, path under TempDir
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
		var out bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &out
		buildErr = cmd.Run()
		if buildErr != nil {
			buildErr = fmt.Errorf("go build ./cmd/emailbridge: %w\n%s\n(set EB_E2E_BIN to a working binary, or fix the import cycle)", buildErr, out.String())
			buildPath = ""
		}
	})
	if buildErr != nil {
		t.Fatal(buildErr)
	}
	if buildPath == "" {
		t.Fatal("no emailbridge binary")
	}
	return buildPath
}

// FindRepoRoot walks up from the working directory to go.mod.
func FindRepoRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		panic(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			panic("go.mod not found")
		}
		dir = parent
	}
}

func FreeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	a := l.Addr().String()
	_ = l.Close()
	return a
}

// HostMongoURI forces directConnection so the driver does not chase replica-set member
// addresses advertised as Docker-internal IPs that the host cannot reach.
func HostMongoURI(uri string) string {
	u, err := url.Parse(uri)
	if err != nil {
		if strings.Contains(uri, "?") {
			return uri + "&directConnection=true"
		}
		return uri + "?directConnection=true"
	}
	q := u.Query()
	q.Set("directConnection", "true")
	u.RawQuery = q.Encode()
	return u.String()
}

func b64(n int) string {
	k := make([]byte, n)
	_, _ = rand.Read(k)
	return base64.StdEncoding.EncodeToString(k)
}

// StartNode boots Mongo + S3, builds the binary if needed, and starts `emailbridge serve`.
func StartNode(t *testing.T, extraEnv map[string]string) *Node {
	t.Helper()
	if os.Getenv("DOCKER_CONFIG") == "" {
		cfg := t.TempDir()
		_ = os.WriteFile(filepath.Join(cfg, "config.json"), []byte(`{}`), 0o600)
		t.Setenv("DOCKER_CONFIG", cfg)
	}
	mongoURI := HostMongoURI(containers.MongoShared(t))
	dbName := containers.Database(t)
	ep, ak, sk := containers.S3(t)
	httpAddr := FreeAddr(t)
	_, port, err := net.SplitHostPort(httpAddr)
	if err != nil {
		t.Fatal(err)
	}
	base := "http://localhost:" + port
	dataDir := t.TempDir()

	env := map[string]string{
		"EB_ENV":                      "development",
		"EB_ROLES":                    "api",
		"EB_HTTP_ADDR":                httpAddr,
		"EB_METRICS_ADDR":             FreeAddr(t),
		"EB_PUBLIC_URL":               base,
		"EB_MONGO_URI":                mongoURI,
		"EB_MONGO_DATABASE":           dbName,
		"EB_S3_ENDPOINT":              "http://" + ep,
		"EB_S3_ACCESS_KEY":            ak,
		"EB_S3_SECRET_KEY":            sk,
		"EB_S3_PATH_STYLE":            "true",
		"EB_AUTH_SECRET":              b64(32),
		"EB_KEYS_LOCAL":               "k1:" + b64(32),
		"EB_DATA_DIR":                 dataDir,
		"EB_NATS_MODE":                "embedded",
		"EB_AUTH_COOKIE_SECURE":       "false",
		"EB_AUTH_MFA_REQUIRED":        "true",
		"EB_AUTH_LOCKOUT_THRESHOLD":   "5",
		"EB_AUTH_LOCKOUT_DURATION":    "15m",
		"EB_AUTH_WEBAUTHN_RP_ID":      "localhost",
		"EB_BOOTSTRAP_OWNER_EMAIL":    OwnerEmail,
		"EB_BOOTSTRAP_OWNER_PASSWORD": OwnerPassword,
		"EB_BOOTSTRAP_ORG_NAME":       "E2E Org A",
		"EB_SYSMAIL_MODE":             "off",
		"EB_HTTP_TRUSTED_PROXIES":     "127.0.0.1/32",
		"EB_RATE_LOGIN":               "1000/min",
		"EB_SHUTDOWN_GRACE":           "2s",
		"EB_LOG_STDOUT":               "off",
		"EB_NODE_ID":                  "",
	}
	if v := os.Getenv("EB_E2E_LOG_STDOUT"); v != "" { // debugging: mirror the node's logs (json|text) into Stderr()
		env["EB_LOG_STDOUT"] = v
		env["EB_LOG_LEVEL"] = "debug"
	}
	for k, v := range extraEnv {
		env[k] = v
	}

	n := &Node{
		T: t, Base: base, Addr: httpAddr, MongoURI: mongoURI, Database: dbName,
		DataDir: dataDir, env: env,
	}
	n.launch()
	t.Cleanup(func() {
		n.Stop()
	})
	n.dumpOnFailure()
	n.waitReady(90 * time.Second)
	return n
}

// launch starts the serve process with n.env.
func (n *Node) launch() {
	n.T.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, BinaryPath(n.T), "serve") //nolint:gosec // G204: bin from go build / EB_E2E_BIN, args fixed
	cmd.Env = filterEnv(os.Environ(), "EB_")
	for k, v := range n.env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	var stderr bytes.Buffer
	cmd.Stdout = &stderr
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		cancel()
		n.T.Fatalf("start serve: %v", err)
	}
	exited := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(exited)
	}()
	n.cmd, n.cancel, n.stderr, n.exited = cmd, cancel, &stderr, exited
}

// dumpOnFailure prints the node's log when the test fails and EB_E2E_LOG_STDOUT is set (debugging);
// EB_E2E_LOG_DUMP=always prints it on success too (load runs that pass but behave oddly).
func (n *Node) dumpOnFailure() {
	t := n.T
	t.Cleanup(func() {
		if (t.Failed() || os.Getenv("EB_E2E_LOG_DUMP") == "always") && os.Getenv("EB_E2E_LOG_STDOUT") != "" {
			t.Logf("=== node %s (%s) log ===\n%s", n.env["EB_NODE_ID"], n.env["EB_ROLES"], n.Stderr())
		}
	})
}

// Kill ends the serve process at once, with no graceful shutdown (chaos: a crashed node).
func (n *Node) Kill() {
	n.T.Helper()
	if n.cancel == nil {
		return
	}
	_ = n.cmd.Process.Kill()
	<-n.exited
	n.cancel()
	n.cancel = nil
}

// Restart boots a stopped or killed node again with the same settings and waits until it is ready.
func (n *Node) Restart() {
	n.T.Helper()
	if n.cancel != nil {
		n.T.Fatal("Restart: the node is still running")
	}
	n.launch()
	n.waitReady(90 * time.Second)
}

func filterEnv(in []string, dropPrefix string) []string {
	out := make([]string, 0, len(in))
	for _, e := range in {
		k, _, _ := strings.Cut(e, "=")
		if strings.HasPrefix(k, dropPrefix) {
			continue
		}
		out = append(out, e)
	}
	return out
}

// waitReady waits for /readyz. A node that exits because one of its listen ports was taken in the meantime
// (FreeAddr closes its probe listener, so another process can grab the port before the node binds it) is
// relaunched on fresh ports, up to three times.
func (n *Node) waitReady(d time.Duration) {
	n.T.Helper()
	for attempt := 1; ; attempt++ {
		if n.waitReadyOnce(d) {
			return
		}
		log := n.stderr.String()
		if attempt >= 3 || !isBindError(log) {
			n.T.Fatalf("serve exited early:\n%s", log)
		}
		n.T.Logf("node %s: a listen port was taken, relaunching on fresh ports", n.env["EB_NODE_ID"])
		n.cancel()
		n.cancel = nil
		n.rerollPorts()
		n.launch()
	}
}

func isBindError(log string) bool {
	return strings.Contains(log, "bind: Only one usage of each socket address") ||
		strings.Contains(log, "bind: address already in use")
}

// rerollPorts gives the node new loopback ports for its own listen addresses (HTTP, metrics/health and an
// embedded NATS listen address — peers are started only after this node is ready, so none depends on it yet)
// and keeps Base, Addr and the public URL in step with the new HTTP port.
func (n *Node) rerollPorts() {
	for k, v := range n.env {
		if !strings.HasSuffix(k, "_ADDR") && !strings.HasSuffix(k, "_LISTEN") {
			continue
		}
		if host, _, err := net.SplitHostPort(v); err != nil || host != "127.0.0.1" {
			continue
		}
		n.env[k] = FreeAddr(n.T)
	}
	n.Addr = n.env["EB_HTTP_ADDR"]
	_, port, _ := net.SplitHostPort(n.Addr)
	n.Base = "http://localhost:" + port
	if _, ok := n.env["EB_PUBLIC_URL"]; ok {
		n.env["EB_PUBLIC_URL"] = n.Base
	}
}

// waitReadyOnce reports true once /readyz answers 200 and false when the process exits first.
func (n *Node) waitReadyOnce(d time.Duration) bool {
	n.T.Helper()
	deadline := time.Now().Add(d)
	for {
		resp, err := http.Get(n.Base + "/readyz") //nolint:noctx // test poll
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return true
			}
		}
		select {
		case <-n.exited:
			return false
		default:
		}
		if time.Now().After(deadline) {
			n.T.Fatalf("node never became ready:\n%s", n.stderr.String())
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// Stop cancels the serve process.
func (n *Node) Stop() {
	if n.cancel == nil {
		return
	}
	n.cancel()
	n.cancel = nil
	select {
	case <-n.exited:
	case <-time.After(30 * time.Second):
		_ = n.cmd.Process.Kill()
	}
}

// Stderr returns the serve process log buffer (for debugging failed scenarios).
func (n *Node) Stderr() string { return n.stderr.String() }

// Client is an HTTP client with cookie jar and EmailBridge CSRF headers.
type Client struct {
	N   *Node
	C   *http.Client
	IP  string
	Hdr map[string]string
}

// NewClient returns a jar-backed client bound to n.
func (n *Node) NewClient() *Client {
	jar, _ := cookiejar.New(nil)
	return &Client{N: n, C: &http.Client{
		Jar: jar, Timeout: 60 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

// Resp is one HTTP response decoded as JSON when possible.
type Resp struct {
	Status int
	Header http.Header
	Body   []byte
	JSON   map[string]any
}

// Str returns a top-level string field from the JSON body.
func (r Resp) Str(k string) string {
	s, _ := r.JSON[k].(string)
	return s
}

// Do performs an HTTP request against the node.
func (c *Client) Do(method, path string, body any) Resp {
	c.N.T.Helper()
	var rd io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		rd = strings.NewReader(b)
	case []byte:
		rd = bytes.NewReader(b)
	default:
		raw, _ := json.Marshal(b)
		rd = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, c.N.Base+path, rd)
	if err != nil {
		c.N.T.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Origin", c.N.Base)
	req.Header.Set("X-Requested-With", "EmailBridge")
	if c.IP != "" {
		req.Header.Set("X-Forwarded-For", c.IP)
	}
	for k, v := range c.Hdr {
		req.Header.Set(k, v)
	}
	res, err := c.C.Do(req)
	if err != nil {
		c.N.T.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	raw, _ := io.ReadAll(res.Body)
	out := Resp{Status: res.StatusCode, Header: res.Header, Body: raw}
	_ = json.Unmarshal(raw, &out.JSON)
	return out
}

// Cookie returns a named cookie from the jar, or nil.
func (c *Client) Cookie(name string) *http.Cookie {
	u, _ := url.Parse(c.N.Base)
	for _, ck := range c.C.Jar.Cookies(u) {
		if ck.Name == name {
			return ck
		}
	}
	return nil
}

// SignIn posts password credentials.
func (c *Client) SignIn(email, password string) Resp {
	c.N.T.Helper()
	return c.Do(http.MethodPost, "/v1/auth/signin/credential", map[string]any{
		"credential": email, "password": password,
	})
}

// EnrolTOTP completes TOTP enrolment for the current session.
func (c *Client) EnrolTOTP(password string) (secret, usedCode string) {
	c.N.T.Helper()
	if password == "" {
		password = OwnerPassword
	}
	r := c.Do(http.MethodPost, "/v1/auth/two-factor/initiate-setup", map[string]any{"password": password})
	if r.Status != http.StatusOK {
		c.N.T.Fatalf("initiate-setup = %d %s", r.Status, r.Body)
	}
	u, err := url.Parse(r.Str("uri"))
	if err != nil {
		c.N.T.Fatal(err)
	}
	secret = u.Query().Get("secret")
	usedCode, _ = totp.GenerateCode(secret, time.Now())
	r = c.Do(http.MethodPost, "/v1/auth/two-factor/finalize-setup", map[string]any{"code": usedCode})
	if r.Status != http.StatusOK {
		c.N.T.Fatalf("finalize-setup = %d %s", r.Status, r.Body)
	}
	return secret, usedCode
}

// FreshTOTP returns a TOTP code that is not in used.
func FreshTOTP(t *testing.T, secret string, used ...string) string {
	t.Helper()
	seen := map[string]bool{}
	for _, c := range used {
		seen[c] = true
	}
	deadline := time.Now().Add(45 * time.Second)
	for {
		code, err := totp.GenerateCode(secret, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		if !seen[code] {
			return code
		}
		if time.Now().After(deadline) {
			t.Fatal("TOTP step did not advance")
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// ExpectProblem asserts an RFC 9457 problem response.
func ExpectProblem(t *testing.T, r Resp, status int, code string) {
	t.Helper()
	if r.Status != status || r.Str("code") != code || !strings.HasPrefix(r.Header.Get("Content-Type"), "application/problem+json") {
		t.Fatalf("want %d %s problem, got %d %s %s", status, code, r.Status, r.Header.Get("Content-Type"), r.Body)
	}
}

// ExpectStatus asserts an HTTP status code.
func ExpectStatus(t *testing.T, r Resp, status int) {
	t.Helper()
	if r.Status != status {
		t.Fatalf("want %d, got %d %s", status, r.Status, r.Body)
	}
}

// OwnerSession signs in the bootstrap owner and completes TOTP enrolment when needed.
func (n *Node) OwnerSession(t *testing.T) (*Client, string) {
	t.Helper()
	c := n.NewClient()
	r := c.SignIn(OwnerEmail, OwnerPassword)
	if r.Status != http.StatusOK {
		t.Fatalf("owner sign-in = %d %s", r.Status, r.Body)
	}
	me := c.Do(http.MethodGet, "/v1/me", nil)
	if me.Status != http.StatusOK {
		t.Fatalf("me = %d %s", me.Status, me.Body)
	}
	mfa, _ := me.JSON["mfa"].(bool)
	var secret string
	if !mfa {
		var used string
		secret, used = c.EnrolTOTP(OwnerPassword)
		_ = used
		me = c.Do(http.MethodGet, "/v1/me", nil)
		if me.Status != http.StatusOK || me.JSON["mfa"] != true {
			t.Fatalf("me after enrol = %d %s", me.Status, me.Body)
		}
	} else if r.JSON["two_factor_required"] == true {
		t.Fatal("owner already has MFA but sign-in returned challenge; store secret across tests")
	}
	return c, secret
}

// SignInWithTOTP does password + TOTP for a user that already enrolled.
func (n *Node) SignInWithTOTP(t *testing.T, email, password, secret string, used ...string) *Client {
	t.Helper()
	c := n.NewClient()
	r := c.SignIn(email, password)
	if r.Status != http.StatusOK || r.JSON["two_factor_required"] != true {
		t.Fatalf("2fa sign-in = %d %s", r.Status, r.Body)
	}
	code := FreshTOTP(t, secret, used...)
	r = c.Do(http.MethodPost, "/v1/auth/two-factor/verify", map[string]any{"code": code, "method": "totp"})
	if r.Status != http.StatusOK {
		t.Fatalf("verify = %d %s", r.Status, r.Body)
	}
	return c
}

// PasskeyDevice is a virtual WebAuthn authenticator for e2e tests.
type PasskeyDevice struct {
	RP   virtualwebauthn.RelyingParty
	Auth virtualwebauthn.Authenticator
	Cred virtualwebauthn.Credential
}

// NewDevice builds a virtual passkey for base URL origin.
func NewDevice(base string) *PasskeyDevice {
	return &PasskeyDevice{
		RP:   virtualwebauthn.RelyingParty{Name: "EmailBridge", ID: "localhost", Origin: base},
		Auth: virtualwebauthn.NewAuthenticator(),
		Cred: virtualwebauthn.NewCredential(virtualwebauthn.KeyTypeEC2),
	}
}

func unwrap(raw []byte) string {
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) == nil {
		if d, ok := m["data"]; ok {
			return string(d)
		}
	}
	return string(raw)
}

// RegisterPasskey completes passkey registration for the current session.
func (c *Client) RegisterPasskey(d *PasskeyDevice) Resp {
	c.N.T.Helper()
	r := c.Do(http.MethodGet, "/v1/auth/passkeys/begin-registration", nil)
	if r.Status != http.StatusOK {
		c.N.T.Fatalf("begin-registration = %d %s", r.Status, r.Body)
	}
	opts, err := virtualwebauthn.ParseAttestationOptions(unwrap(r.Body))
	if err != nil {
		c.N.T.Fatal(err)
	}
	d.Auth.Options.UserHandle = []byte(opts.UserID)
	att := virtualwebauthn.CreateAttestationResponse(d.RP, d.Auth, d.Cred, *opts)
	d.Auth.AddCredential(d.Cred)
	return c.Do(http.MethodPost, "/v1/auth/passkeys/finish-registration", att)
}

// PasskeySignIn completes passkey authentication.
func (c *Client) PasskeySignIn(d *PasskeyDevice) Resp {
	c.N.T.Helper()
	r := c.Do(http.MethodGet, "/v1/auth/passkeys/begin-authentication", nil)
	if r.Status != http.StatusOK {
		c.N.T.Fatalf("begin-authentication = %d %s", r.Status, r.Body)
	}
	opts, err := virtualwebauthn.ParseAssertionOptions(unwrap(r.Body))
	if err != nil {
		c.N.T.Fatal(err)
	}
	return c.Do(http.MethodPost, "/v1/auth/passkeys/finish-authentication",
		virtualwebauthn.CreateAssertionResponse(d.RP, d.Auth, d.Cred, *opts))
}

// SeedInvite inserts a pending membership invite and returns the plain token.
func (n *Node) SeedInvite(t *testing.T, orgID, email, role string) string {
	t.Helper()
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	sum := sha256.Sum256([]byte(token))
	hash := hex.EncodeToString(sum[:])
	now := time.Now().UTC()
	memID := store.NewID(store.PrefixMembership)
	doc := bson.M{
		"_id": memID, "organizationId": orgID, "email": strings.ToLower(email), "role": role,
		"invite":    bson.M{"email": strings.ToLower(email), "tokenHash": hash, "expiresAt": now.Add(72 * time.Hour)},
		"createdAt": now, "updatedAt": now, "version": int64(1),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	client, err := mongo.Connect(options.Client().ApplyURI(n.MongoURI))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Disconnect(context.Background()) })
	_, err = client.Database(n.Database).Collection("memberships").InsertOne(ctx, doc)
	if err != nil {
		t.Fatal(err)
	}
	return token
}
