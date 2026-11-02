//go:build e2e

package harness

import (
	"net"
	"testing"
	"time"
)

// StartPeer starts another node of first's cluster: the same database, S3, keys and secrets, its own HTTP
// port, data directory and node ID, the given roles, and NATS through first's embedded server (first must have
// been started with EB_NATS_EMBEDDED_LISTEN, see ClusterEnv).
func StartPeer(t *testing.T, first *Node, nodeID, roles string, extra map[string]string) *Node {
	t.Helper()
	listen := first.env["EB_NATS_EMBEDDED_LISTEN"]
	if listen == "" {
		t.Fatal("StartPeer: the first node has no EB_NATS_EMBEDDED_LISTEN")
	}
	env := make(map[string]string, len(first.env))
	for k, v := range first.env {
		env[k] = v
	}
	httpAddr := FreeAddr(t)
	_, port, err := net.SplitHostPort(httpAddr)
	if err != nil {
		t.Fatal(err)
	}
	delete(env, "EB_NATS_EMBEDDED_LISTEN")
	env["EB_NATS_MODE"] = "external"
	env["EB_NATS_URL"] = "nats://" + listen
	env["EB_NODE_ID"] = nodeID
	env["EB_ROLES"] = roles
	env["EB_HTTP_ADDR"] = httpAddr
	env["EB_METRICS_ADDR"] = FreeAddr(t)
	env["EB_DATA_DIR"] = t.TempDir()
	for k, v := range extra {
		env[k] = v
	}
	n := &Node{T: t, Base: "http://localhost:" + port, Addr: httpAddr, MongoURI: first.MongoURI,
		Database: first.Database, DataDir: env["EB_DATA_DIR"], env: env}
	n.launch()
	t.Cleanup(n.Stop)
	n.dumpOnFailure()
	n.waitReady(90 * time.Second)
	return n
}

// ClusterEnv is the extra environment of the first node of a multi-process cluster: an embedded NATS
// server other nodes connect to, an explicit node ID, the given roles, and short leases so failover tests
// do not wait for the 30 s production TTL. Placement tick and hand-off timeout are shortened so rolling
// upgrades rebalance within the test wait (production defaults are 5 s / 30 s).
func ClusterEnv(t *testing.T, nodeID, roles string) map[string]string {
	t.Helper()
	return map[string]string{
		"EB_NATS_EMBEDDED_LISTEN": FreeAddr(t),
		"EB_NODE_ID":              nodeID,
		"EB_ROLES":                roles,
		"EB_LEASE_TTL":            "6s",
		"EB_LEADER_TTL":           "6s",
		"EB_NODE_HEARTBEAT":       "1s",
		"EB_SYNC_TICK":            "500ms",
		"EB_SYNC_HANDOFF_TIMEOUT": "5s",
	}
}

// Env returns one of the node's environment values (diagnostics in cluster tests).
func (n *Node) Env(key string) string { return n.env[key] }
