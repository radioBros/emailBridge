//go:build integration || e2e

// Package containers starts real dependencies for integration tests (S14 §2).
// Helpers skip (they do not fail) when no container runtime is available.
package containers

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/mongodb"
	"github.com/testcontainers/testcontainers-go/modules/nats"
	"github.com/testcontainers/testcontainers-go/wait"
)

const (
	envMongoURI   = "EB_TEST_MONGO_URI"
	envNATSURL    = "EB_TEST_NATS_URL"
	envS3Endpoint = "EB_TEST_S3_ENDPOINT"
	envS3Access   = "EB_TEST_S3_ACCESS_KEY"
	envS3Secret   = "EB_TEST_S3_SECRET_KEY" //nolint:gosec // G101: env var name, not a credential value
)

func skipIfNoRuntime(t *testing.T) {
	t.Helper()
	testcontainers.SkipIfProviderIsNotHealthy(t)
}

var (
	mongoOnce sync.Once
	mongoURI  string
	mongoErr  error
)

// MongoShared starts one MongoDB 8 replica set for the calling test package (sync.Once).
// Prefer this from TestMain or shared helpers; use Mongo for a dedicated container per call.
func MongoShared(t *testing.T) string {
	t.Helper()
	if v := os.Getenv(envMongoURI); v != "" {
		return v
	}
	skipIfNoRuntime(t)
	mongoOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		ctr, err := mongodb.Run(ctx, ImageMongo, mongodb.WithReplicaSet("rs0"))
		if err != nil {
			mongoErr = err
			return
		}
		uri, err := directURI(ctr.ConnectionString(ctx))
		if err != nil {
			mongoErr = err
			return
		}
		mongoURI = uri
	})
	if mongoErr != nil {
		t.Fatalf("mongodb container: %v", mongoErr)
	}
	return mongoURI
}

// Mongo returns a MongoDB replica-set URI usable with transactions.
func Mongo(t *testing.T) string {
	t.Helper()
	if v := os.Getenv(envMongoURI); v != "" {
		return v
	}
	skipIfNoRuntime(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	t.Cleanup(cancel)
	ctr, err := mongodb.Run(ctx, ImageMongo, mongodb.WithReplicaSet("rs0"))
	if err != nil {
		t.Fatalf("mongodb container: %v", err)
	}
	t.Cleanup(func() {
		_ = ctr.Terminate(context.Background())
	})
	uri, err := ctr.ConnectionString(ctx)
	if err != nil {
		t.Fatalf("mongodb uri: %v", err)
	}
	return uri
}

// Database returns a fresh database name for the current test.
func Database(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("ebtest_%d", time.Now().UnixNano())
}

// NATS returns a NATS URL with JetStream enabled.
func NATS(t *testing.T) string {
	t.Helper()
	if v := os.Getenv(envNATSURL); v != "" {
		return v
	}
	skipIfNoRuntime(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	t.Cleanup(cancel)
	ctr, err := nats.Run(ctx, ImageNATS)
	if err != nil {
		t.Fatalf("nats container: %v", err)
	}
	t.Cleanup(func() {
		_ = ctr.Terminate(context.Background())
	})
	url, err := ctr.ConnectionString(ctx)
	if err != nil {
		t.Fatalf("nats url: %v", err)
	}
	return url
}

// S3 returns an S3-compatible endpoint (host:port), access key and secret key.
// Escape hatch: EB_TEST_S3_ENDPOINT (with or without scheme) plus
// EB_TEST_S3_ACCESS_KEY / EB_TEST_S3_SECRET_KEY.
func S3(t *testing.T) (endpoint, accessKey, secretKey string) {
	t.Helper()
	if v := os.Getenv(envS3Endpoint); v != "" {
		ak := os.Getenv(envS3Access)
		sk := os.Getenv(envS3Secret)
		if ak == "" || sk == "" {
			t.Fatal("EB_TEST_S3_ACCESS_KEY and EB_TEST_S3_SECRET_KEY required with EB_TEST_S3_ENDPOINT")
		}
		return stripScheme(v), ak, sk
	}
	if devS3Up() {
		return DevS3Endpoint, DevS3AccessKey, DevS3SecretKey
	}
	skipIfNoRuntime(t)
	accessKey = randomHex(16)
	secretKey = randomHex(16)
	cfg := fmt.Sprintf(
		`{"identities":[{"name":"emailbridge","credentials":[{"accessKey":%q,"secretKey":%q}],"actions":["Admin","Read","Write","List","Tagging"]}]}`,
		accessKey, secretKey,
	)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	t.Cleanup(cancel)
	ctr, err := testcontainers.Run(ctx, ImageS3,
		testcontainers.WithExposedPorts("8333/tcp"),
		testcontainers.WithFiles(testcontainers.ContainerFile{
			Reader:            strings.NewReader(cfg),
			ContainerFilePath: "/etc/s3.json",
			FileMode:          0o644,
		}),
		testcontainers.WithEntrypoint("weed"),
		testcontainers.WithCmd(
			"server", "-dir=/data", "-ip.bind=0.0.0.0",
			"-s3", "-s3.port=8333", "-s3.config=/etc/s3.json",
		),
		testcontainers.WithWaitStrategy(
			wait.ForHTTP("/").
				WithPort("8333/tcp").
				WithStatusCodeMatcher(func(c int) bool { return c > 0 }).
				WithStartupTimeout(2*time.Minute),
		),
	)
	if err != nil {
		t.Fatalf("s3 container: %v", err)
	}
	t.Cleanup(func() {
		_ = ctr.Terminate(context.Background())
	})
	host, err := ctr.Host(ctx)
	if err != nil {
		t.Fatalf("s3 host: %v", err)
	}
	port, err := ctr.MappedPort(ctx, "8333/tcp")
	if err != nil {
		t.Fatalf("s3 port: %v", err)
	}
	return host + ":" + port.Port(), accessKey, secretKey
}

func stripScheme(v string) string {
	v = strings.TrimSpace(v)
	if i := strings.Index(v, "://"); i >= 0 {
		return v[i+3:]
	}
	return v
}

func randomHex(nBytes int) string {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// DovecotAddrs are the mapped addresses of a Dovecot test container.
type DovecotAddrs struct {
	IMAPS    string // implicit TLS
	IMAP     string // plaintext + STARTTLS
	Password string // any username logs in with it
}

// composeDovecotIMAPS / STARTTLS match deploy/compose/devdeps.yml published ports.
const (
	composeDovecotIMAPS = "127.0.0.1:10993"
	composeDovecotIMAP  = "127.0.0.1:10143"
	composeDovecotPass  = "devpass"
)

// DovecotCompose reports the compose-stack Dovecot when IMAPS answers a TLS handshake on the published port.
// A bare TCP accept is not enough: Docker publishes the host port even when the process inside listens
// elsewhere (the 2.4.5 image uses 31993/31143).
func DovecotCompose() (DovecotAddrs, bool) {
	d := &net.Dialer{Timeout: 400 * time.Millisecond}
	c, err := tls.DialWithDialer(d, "tcp", composeDovecotIMAPS, &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12}) //nolint:gosec // test Dovecot uses a self-signed certificate
	if err != nil {
		return DovecotAddrs{}, false
	}
	_ = c.SetDeadline(time.Now().Add(400 * time.Millisecond))
	buf := make([]byte, 64)
	n, err := c.Read(buf)
	_ = c.Close()
	if err != nil || n == 0 || !strings.HasPrefix(string(buf[:n]), "* OK") {
		return DovecotAddrs{}, false
	}
	pass := composeDovecotPass
	if p := os.Getenv("EB_TEST_IMAP_PASSWORD"); p != "" {
		pass = p
	} else if p := os.Getenv("USER_PASSWORD"); p != "" {
		pass = p
	}
	return DovecotAddrs{IMAPS: composeDovecotIMAPS, IMAP: composeDovecotIMAP, Password: pass}, true
}

// Dovecot starts a Dovecot image and returns the mapped IMAPS address and the password
// (any username logs in with that password).
func Dovecot(t *testing.T) (imapAddr, password string) {
	t.Helper()
	a := DovecotServer(t)
	return a.IMAPS, a.Password
}

// DovecotServer starts a Dovecot container owned by this test (terminated when the test ends) and returns
// both mapped ports.
func DovecotServer(t *testing.T) DovecotAddrs {
	t.Helper()
	skipIfNoRuntime(t)
	a, terminate, err := startDovecot()
	if err != nil {
		t.Fatalf("dovecot container: %v", err)
	}
	t.Cleanup(terminate)
	return a
}

var (
	dovecotOnce   sync.Once
	dovecotShared DovecotAddrs
	dovecotErr    error
)

// DovecotShared returns one Dovecot container per test process, shared by every test that asks for it. Like
// MongoShared it is never tied to one test's cleanup (the first caller's end would kill it for the rest);
// testcontainers' reaper removes it when the process exits.
func DovecotShared(t *testing.T) DovecotAddrs {
	t.Helper()
	skipIfNoRuntime(t)
	dovecotOnce.Do(func() { dovecotShared, _, dovecotErr = startDovecot() })
	if dovecotErr != nil {
		t.Fatalf("dovecot container: %v", dovecotErr)
	}
	return dovecotShared
}

func startDovecot() (DovecotAddrs, func(), error) {
	password := "testpass"
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	// Ryuk's reaper handshake races under parallel suites on Docker Desktop and has deleted the
	// just-started Dovecot mid-wait ("No such container"); the shared helper terminates explicitly.
	if os.Getenv("TESTCONTAINERS_RYUK_DISABLED") == "" {
		_ = os.Setenv("TESTCONTAINERS_RYUK_DISABLED", "true")
	}
	ctr, err := testcontainers.Run(ctx, ImageDovecot,
		testcontainers.WithExposedPorts("31993/tcp", "31143/tcp"),
		testcontainers.WithEnv(map[string]string{"USER_PASSWORD": password}),
		// Both listeners: the STARTTLS port can come up a moment after IMAPS.
		testcontainers.WithWaitStrategy(wait.ForAll(
			wait.ForListeningPort("31993/tcp"), wait.ForListeningPort("31143/tcp"),
		).WithDeadline(2*time.Minute)),
	)
	if err != nil {
		return DovecotAddrs{}, nil, err
	}
	terminate := func() { _ = ctr.Terminate(context.Background()) }
	host, err := ctr.Host(ctx)
	if err != nil {
		terminate()
		return DovecotAddrs{}, nil, err
	}
	imaps, err := ctr.MappedPort(ctx, "31993/tcp")
	if err != nil {
		terminate()
		return DovecotAddrs{}, nil, err
	}
	plain, err := ctr.MappedPort(ctx, "31143/tcp")
	if err != nil {
		terminate()
		return DovecotAddrs{}, nil, err
	}
	return DovecotAddrs{IMAPS: host + ":" + imaps.Port(), IMAP: host + ":" + plain.Port(), Password: password}, terminate, nil
}

// ClamAV starts a clamd container and returns a tcp host:port for EB_CLAMAV_ADDR.
// Skips (with reason) when the image cannot be pulled or started.
func ClamAV(t *testing.T) string {
	t.Helper()
	skipIfNoRuntime(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	t.Cleanup(cancel)
	ctr, err := testcontainers.Run(ctx, ImageClamAV,
		testcontainers.WithExposedPorts("3310/tcp"),
		testcontainers.WithWaitStrategy(
			wait.ForListeningPort("3310/tcp").WithStartupTimeout(4*time.Minute),
		),
	)
	if err != nil {
		t.Skipf("clamav container unavailable: %v", err)
	}
	t.Cleanup(func() { _ = ctr.Terminate(context.Background()) })
	host, err := ctr.Host(ctx)
	if err != nil {
		t.Skipf("clamav host: %v", err)
	}
	port, err := ctr.MappedPort(ctx, "3310/tcp")
	if err != nil {
		t.Skipf("clamav port: %v", err)
	}
	return host + ":" + port.Port()
}

// directURI adds directConnection=true: the single-node replica set advertises its container address, which
// the host (Windows, Podman/WSL) cannot reach, so the driver must not follow the topology.
func directURI(uri string, err error) (string, error) {
	if err != nil || strings.Contains(uri, "directConnection=") {
		return uri, err
	}
	sep := "?"
	if strings.Contains(uri, "?") {
		sep = "&"
	}
	return uri + sep + "directConnection=true", nil
}

// The emailbridge-dev stack's S3 (deploy/compose/devdeps.yml). When it runs, tests use it instead of starting a
// container per run; the credentials are dev-only and match the compose defaults.
const (
	DevS3Endpoint  = "127.0.0.1:18333"
	DevS3AccessKey = "ebdevaccess"
	DevS3SecretKey = "ebdevsecret0123456789"
)

func devS3Up() bool {
	c, err := net.DialTimeout("tcp", DevS3Endpoint, 300*time.Millisecond)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}
