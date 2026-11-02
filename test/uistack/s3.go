package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Dev stack S3 (deploy/compose/devdeps.yml) — same values as test/containers.
const (
	devS3Endpoint  = "127.0.0.1:18333"
	devS3AccessKey = "ebdevaccess"
	devS3SecretKey = "ebdevsecret0123456789"
)

// startS3 returns host:port, accessKey, secretKey.
// Order: EB_TEST_S3_* env → emailbridge-dev stack S3 on :18333 → docker seaweedfs.
func startS3() (endpoint, accessKey, secretKey string, cleanup func(), err error) {
	if v := os.Getenv("EB_TEST_S3_ENDPOINT"); v != "" {
		ak := os.Getenv("EB_TEST_S3_ACCESS_KEY")
		sk := os.Getenv("EB_TEST_S3_SECRET_KEY")
		if ak == "" || sk == "" {
			return "", "", "", nil, fmt.Errorf("EB_TEST_S3_ACCESS_KEY and EB_TEST_S3_SECRET_KEY required with EB_TEST_S3_ENDPOINT")
		}
		return stripScheme(v), ak, sk, func() {}, nil
	}
	if devS3Up() {
		return devS3Endpoint, devS3AccessKey, devS3SecretKey, func() {}, nil
	}
	accessKey = randomHex(16)
	secretKey = randomHex(16)
	name := fmt.Sprintf("eb-uistack-s3-%d", time.Now().UnixNano())
	cfg := fmt.Sprintf(
		`{"identities":[{"name":"emailbridge","credentials":[{"accessKey":%q,"secretKey":%q}],"actions":["Admin","Read","Write","List","Tagging"]}]}`,
		accessKey, secretKey,
	)
	tmp, err := os.CreateTemp("", "eb-s3-*.json")
	if err != nil {
		return "", "", "", nil, err
	}
	cfgPath := tmp.Name()
	if _, err := tmp.WriteString(cfg); err != nil {
		_ = tmp.Close()
		return "", "", "", nil, err
	}
	_ = tmp.Close()

	dockerBin := dockerPath()
	cmd := exec.Command(dockerBin, "run", "-d", "--rm", //nolint:gosec // G204: dockerBin from LookPath/DOCKER_BIN; args built here
		"--name", name,
		"-p", "127.0.0.1::8333",
		"-v", cfgPath+":/etc/s3.json:ro",
		"chrislusf/seaweedfs:4.50",
		"server", "-dir=/data", "-ip.bind=0.0.0.0",
		"-s3", "-s3.port=8333", "-s3.config=/etc/s3.json",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		_ = os.Remove(cfgPath)
		return "", "", "", nil, fmt.Errorf("docker run seaweedfs: %w\n%s", err, out)
	}
	cleanup = func() {
		_ = exec.Command(dockerBin, "rm", "-f", name).Run() //nolint:gosec // G204: local docker cleanup of container we started
		_ = os.Remove(cfgPath)
	}
	portOut, err := exec.Command(dockerBin, "port", name, "8333/tcp").CombinedOutput() //nolint:gosec // G204: query port of container we started
	if err != nil {
		cleanup()
		return "", "", "", nil, fmt.Errorf("docker port: %w\n%s", err, portOut)
	}
	line := strings.TrimSpace(string(portOut))
	endpoint = strings.TrimSpace(strings.Split(line, "\n")[0])
	if endpoint == "" {
		cleanup()
		return "", "", "", nil, fmt.Errorf("empty s3 port mapping: %q", portOut)
	}
	deadline := time.Now().Add(90 * time.Second)
	for {
		if time.Now().After(deadline) {
			cleanup()
			return "", "", "", nil, fmt.Errorf("s3 never became ready at %s", endpoint)
		}
		resp, err := http.Get("http://" + endpoint + "/") //nolint:noctx // local bootstrap poll
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode > 0 {
				break
			}
		}
		time.Sleep(400 * time.Millisecond)
	}
	return endpoint, accessKey, secretKey, cleanup, nil
}

func devS3Up() bool {
	c, err := net.DialTimeout("tcp", devS3Endpoint, 300*time.Millisecond)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

func dockerPath() string {
	if p := os.Getenv("DOCKER_BIN"); p != "" {
		return p
	}
	if p, err := exec.LookPath("docker"); err == nil {
		return p
	}
	candidates := []string{
		`C:\Program Files\Docker\Docker\resources\bin\docker.exe`,
		`C:\Program Files\Docker\Docker\resources\bin\docker`,
		"/usr/bin/docker",
		"/usr/local/bin/docker",
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return "docker"
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
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
