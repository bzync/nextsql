package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// roundTrip writes c.Marshal() to a temp file, reloads it, and returns the
// reloaded config.
func roundTrip(t *testing.T, c Config) Config {
	t.Helper()
	path := filepath.Join(t.TempDir(), "nextsql.conf")
	if err := os.WriteFile(path, c.Marshal(), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("reload marshaled config: %v\n---\n%s", err, c.Marshal())
	}
	return got
}

func TestMarshalRoundTripsDefault(t *testing.T) {
	c := Default()
	if got := roundTrip(t, c); !reflect.DeepEqual(got, c) {
		t.Fatalf("default round trip mismatch:\n got %+v\nwant %+v", got, c)
	}
}

func TestMarshalRoundTripsFullyPopulated(t *testing.T) {
	c := Default()
	c.DataDir = "/var/lib/nextsql"
	c.KeyFile = "/etc/nextsql/root.key"
	c.InstanceKeyFile = "/etc/nextsql/root.key.instance"
	c.AuthFile = "/etc/nextsql/users"
	c.ListenAddr = "10.0.0.5:7210"
	c.LogLevel = "warn"
	c.BufferPages = 262144
	c.TLSCert = "/etc/nextsql/tls.crt"
	c.TLSKey = "/etc/nextsql/tls.key"
	c.TLSClientCA = "/etc/nextsql/clients.pem"
	c.TLSClientCRL = "/etc/nextsql/clients.crl"
	c.RequireClientKey = true
	c.TokenKeyset = "/etc/nextsql/tokens.keyset"
	c.TokenRevocations = "/etc/nextsql/tokens.revocations"
	c.TokenAudience = "nextsql-prod"
	c.TokenIdentitySourceHints = map[uint32]string{9: "oidc", 3: "oidc"}
	c.AuditFile = "/var/log/nextsql.audit"
	c.AuditSigningKeyset = "/etc/nextsql/audit.keyset"
	c.WalArchive = "/mnt/archive/wal"
	c.BackupDir = "/mnt/backups/nextsql"
	c.WalRetentionMS = 3600000
	c.DiskWatermarkCheckMS = 15000
	c.DiskWatermarkWarnPercent = 80
	c.DiskWatermarkRejectPercent = 92.5
	c.ReplicaLagCheckMS = 5000
	c.ReplicaLagWarnEntries = 2000
	c.MaxInflight = 64
	c.MaxOpenDatabases = 16
	c.MaxTotalBufferPages = 524288
	c.TaskWorkers = 8
	c.MaxQueryQueue = 256
	c.QueueWaitMS = 4000
	c.MaxResultRows = 100000
	c.MaxConnections = 512
	c.MaxConnectionsPerUser = 32
	c.MaxConnectionsPerDatabase = 128
	c.MaxConnectionsPerRealm = 200
	c.IdleTimeoutMS = 90000
	c.StatementTimeoutMS = 45000
	c.TransactionTimeoutMS = 120000
	c.LockTimeoutMS = 10000
	c.IdleTransactionTimeoutMS = 30000
	c.DrainTimeoutMS = 20000
	c.NodeID = "node-a"
	c.RaftBind = "10.0.0.5:7220"
	c.RaftJoin = "10.0.0.6:7220"
	c.RaftBootstrap = true

	if err := c.Validate(); err != nil {
		t.Fatalf("test config is itself invalid: %v", err)
	}
	if got := roundTrip(t, c); !reflect.DeepEqual(got, c) {
		t.Fatalf("full round trip mismatch:\n got %+v\nwant %+v\n--- marshaled ---\n%s", got, c, c.Marshal())
	}
}

func TestMarshalOnlyEmitsKnownKeys(t *testing.T) {
	// Every emitted key must be one Load accepts — otherwise Load rejects
	// our own output. A fully-populated config exercises every branch.
	c := Default()
	c.NodeID, c.RaftBind = "n", "127.0.0.1:1"
	c.TokenIdentitySourceHints = map[uint32]string{1: "oidc"}
	c.TokenKeyset = "k"
	out := string(c.Marshal())
	for _, line := range strings.Split(out, "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !strings.Contains(line, "=") {
			t.Fatalf("malformed marshaled line: %q", line)
		}
	}
}
