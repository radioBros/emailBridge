package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/radiobros/emailbridge/simulator"
	"github.com/radiobros/emailbridge/simulator/control"
	simpec "github.com/radiobros/emailbridge/simulator/pec"
	"github.com/radiobros/emailbridge/test/tmpsweep"
)

const (
	ownerEmail     = "owner@ui.test"
	ownerPassword  = "correct horse battery staple"
	viewerEmail    = "viewer@ui.test"
	viewerPassword = "viewer horse battery staple"
	sessionCookie  = "eb_session"
)

// Stack is the running UI test environment.
type Stack struct {
	Base     string
	MongoURI string
	Database string
	DataDir  string
	Sim      *simulator.Simulator
	SimAddr  simulator.Addresses
	SimCtl   *control.Client
	cmd      *exec.Cmd
	cancel   func()
	stderr   *bytes.Buffer
	cleanups []func()
}

func findRepoRoot() string {
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

func freeAddr() string {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	a := l.Addr().String()
	_ = l.Close()
	return a
}

func b64(n int) string {
	k := make([]byte, n)
	_, _ = rand.Read(k)
	return base64.StdEncoding.EncodeToString(k)
}

func hostMongoURI(uri string) string {
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

func ensureEmbedWeb(root string) error {
	src := filepath.Join(root, "web", "dist", "index.html")
	dstDir := filepath.Join(root, "internal", "webui", "dist")
	dst := filepath.Join(dstDir, "index.html")
	if _, err := os.Stat(src); err != nil {
		return fmt.Errorf("web/dist missing — run pnpm build in web/: %w", err)
	}
	if st, err := os.Stat(dst); err == nil {
		srcSt, _ := os.Stat(src)
		if srcSt != nil && !srcSt.ModTime().After(st.ModTime()) {
			return nil
		}
	}
	_ = os.RemoveAll(dstDir)
	return copyDir(filepath.Join(root, "web", "dist"), dstDir)
}

func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o750)
		}
		data, err := os.ReadFile(path) //nolint:gosec // G304: path from filepath.Walk of a tree we own
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o600) //nolint:gosec // G703: target under dst from Walk of a tree we own
	})
}

func buildBinary(root string) (string, error) {
	if p := os.Getenv("EB_E2E_BIN"); p != "" {
		return p, nil
	}
	tmpsweep.Sweep("eb-uistack-bin-", 6*time.Hour)
	tmpsweep.Sweep("eb-uistack-data-", 6*time.Hour)
	dir, err := os.MkdirTemp("", "eb-uistack-bin-*")
	if err != nil {
		return "", err
	}
	name := "emailbridge"
	if strings.Contains(strings.ToLower(os.Getenv("OS")), "windows") {
		name += ".exe"
	}
	out := filepath.Join(dir, name)
	cmd := exec.Command("go", "build", "-tags", "embedweb", "-o", out, "./cmd/emailbridge") //nolint:gosec // G204: fixed go build of this repo
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOMAXPROCS=2", "GOFLAGS=-p=2")
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("go build: %w\n%s", err, buf.String())
	}
	return out, nil
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

func startStack() (*Stack, error) {
	root := findRepoRoot()
	if err := ensureEmbedWeb(root); err != nil {
		return nil, err
	}
	bin, err := buildBinary(root)
	if err != nil {
		return nil, err
	}

	mongoURI := os.Getenv("EB_TEST_MONGO_URI")
	if mongoURI == "" {
		mongoURI = "mongodb://127.0.0.1:27017/?replicaSet=rs0&directConnection=true"
	}
	mongoURI = hostMongoURI(mongoURI)
	dbName := fmt.Sprintf("ebui_%d", time.Now().UnixNano())

	s3ep, s3ak, s3sk, s3cleanup, err := startS3()
	if err != nil {
		return nil, err
	}

	sim, err := simulator.New(simulator.Options{PEC: &simpec.Config{
		Domains: []simpec.Domain{{
			Name: "pec.sim.test", Provider: "Gestore Simulato S.p.A.", // Italian provider name
			SystemAddress: "posta-certificata@pec.sim.test",
		}},
		SlowDelay: 50 * time.Millisecond,
	}})
	if err != nil {
		s3cleanup()
		return nil, err
	}
	if err := sim.Start(); err != nil {
		s3cleanup()
		return nil, err
	}
	addr := sim.Addr()
	ctl := control.NewClient(addr.Control)

	caPEM, err := ctl.PECCA(context.Background())
	if err != nil {
		_ = sim.Stop()
		s3cleanup()
		return nil, err
	}
	dataDir, err := os.MkdirTemp("", "eb-uistack-data-*")
	if err != nil {
		_ = sim.Stop()
		s3cleanup()
		return nil, err
	}
	caPath := filepath.Join(dataDir, "pec-ca.pem")
	if err := os.WriteFile(caPath, caPEM, 0o600); err != nil {
		_ = sim.Stop()
		s3cleanup()
		return nil, err
	}

	httpAddr := "127.0.0.1:18765"
	if p := os.Getenv("EB_UISTACK_ADDR"); p != "" {
		httpAddr = p
	} else if os.Getenv("EB_UISTACK_RANDOM_PORT") == "1" {
		httpAddr = freeAddr()
	}
	_, port, _ := net.SplitHostPort(httpAddr)
	// Public URL must use a hostname (WebAuthn RP ID cannot be an IP).
	base := "http://localhost:" + port

	env := map[string]string{
		"EB_ENV":                      "development",
		"EB_ROLES":                    "all",
		"EB_HTTP_ADDR":                httpAddr,
		"EB_METRICS_ADDR":             freeAddr(),
		"EB_PUBLIC_URL":               base,
		"EB_MONGO_URI":                mongoURI,
		"EB_MONGO_DATABASE":           dbName,
		"EB_S3_ENDPOINT":              "http://" + s3ep,
		"EB_S3_ACCESS_KEY":            s3ak,
		"EB_S3_SECRET_KEY":            s3sk,
		"EB_S3_PATH_STYLE":            "true",
		"EB_AUTH_SECRET":              b64(32),
		"EB_KEYS_LOCAL":               "k1:" + b64(32),
		"EB_DATA_DIR":                 dataDir,
		"EB_NATS_MODE":                "embedded",
		"EB_AUTH_COOKIE_SECURE":       "false",
		"EB_AUTH_MFA_REQUIRED":        "true",
		"EB_AUTH_LOCKOUT_THRESHOLD":   "1000",
		"EB_AUTH_LOCKOUT_DURATION":    "1m",
		"EB_AUTH_WEBAUTHN_RP_ID":      "localhost",
		"EB_BOOTSTRAP_OWNER_EMAIL":    ownerEmail,
		"EB_BOOTSTRAP_OWNER_PASSWORD": ownerPassword,
		"EB_BOOTSTRAP_ORG_NAME":       "UI Org A",
		"EB_SYSMAIL_MODE":             "off",
		"EB_HTTP_TRUSTED_PROXIES":     "127.0.0.1/32",
		"EB_RATE_LOGIN":               "10000/min",
		"EB_RATE_API":                 "10000/min",
		"EB_RATE_DEFAULT":             "10000/min",
		"EB_SHUTDOWN_GRACE":           "2s",
		"EB_LOG_STDOUT":               "off",
		"EB_EGRESS_ALLOW":             "127.0.0.1/32,::1/128",
		"EB_EGRESS_BLOCK_PRIVATE":     "false",
		"EB_PEC_TRUST_ANCHORS":        caPath,
	}

	cmd := exec.Command(bin, "serve") //nolint:gosec // local test binary
	cmd.Env = filterEnv(os.Environ(), "EB_")
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	var stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stderr, &stderr
	if err := cmd.Start(); err != nil {
		_ = sim.Stop()
		s3cleanup()
		return nil, err
	}
	stop := func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}
	st := &Stack{
		Base: base, MongoURI: mongoURI, Database: dbName, DataDir: dataDir,
		Sim: sim, SimAddr: addr, SimCtl: ctl, cmd: cmd, cancel: stop, stderr: &stderr,
		cleanups: []func(){s3cleanup, func() { _ = sim.Stop() }, stop},
	}
	if err := st.waitReady(90 * time.Second); err != nil {
		st.Close()
		return nil, err
	}
	return st, nil
}

func (s *Stack) waitReady(d time.Duration) error {
	deadline := time.Now().Add(d)
	for {
		resp, err := http.Get(s.Base + "/readyz") //nolint:noctx // bootstrap poll
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		if s.cmd.ProcessState != nil && s.cmd.ProcessState.Exited() {
			return fmt.Errorf("serve exited early:\n%s", s.stderr.String())
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("node never became ready:\n%s", s.stderr.String())
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// Close stops the stack.
func (s *Stack) Close() {
	for i := len(s.cleanups) - 1; i >= 0; i-- {
		s.cleanups[i]()
	}
}
