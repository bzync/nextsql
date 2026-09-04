package config

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestLoadAndValidate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.conf")
	body := "data_dir=/var/lib/nextsql\nkey_file=/etc/nextsql/master.key\ninstance_key_file=/etc/nextsql/instance.key\nbuffer_pages=64\nlog_level=debug\n# comment\nlisten_addr=127.0.0.1:9000\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DataDir != "/var/lib/nextsql" || cfg.KeyFile != "/etc/nextsql/master.key" {
		t.Fatalf("paths: %+v", cfg)
	}
	if cfg.InstanceRootFile() != "/etc/nextsql/instance.key" {
		t.Fatalf("instance key: %+v", cfg)
	}
	if cfg.BufferPages != 64 || cfg.ListenAddr != "127.0.0.1:9000" {
		t.Fatalf("fields: %+v", cfg)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if cfg.DataFile() != "/var/lib/nextsql/nextsql.db" {
		t.Fatalf("data file %q", cfg.DataFile())
	}
}

func TestDefaultInstanceRootFile(t *testing.T) {
	cfg := Default()
	cfg.KeyFile = "/etc/nextsql/database.key"
	if got := cfg.InstanceRootFile(); got != "/etc/nextsql/database.key.instance" {
		t.Fatalf("InstanceRootFile()=%q", got)
	}
	cfg.KeyFile = ""
	if got := cfg.InstanceRootFile(); got != "" {
		t.Fatalf("client-key mode default=%q", got)
	}
}

func TestLoadRejectsUnknownKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.conf")
	if err := os.WriteFile(path, []byte("password=secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected unknown key error")
	}
}

func TestLoadAdmissionKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ops.conf")
	body := "max_inflight_queries=4\nmax_query_queue=8\nquery_queue_wait_ms=250\nmax_result_rows=100\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MaxInflight != 4 || cfg.MaxQueryQueue != 8 || cfg.QueueWaitMS != 250 || cfg.MaxResultRows != 100 {
		t.Fatalf("%+v", cfg)
	}
}

func TestLoadMaxOpenDatabases(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dbmgr.conf")
	if err := os.WriteFile(path, []byte("max_open_databases=3\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MaxOpenDatabases != 3 {
		t.Fatalf("%+v", cfg)
	}
}

func TestDefaultMaxOpenDatabases(t *testing.T) {
	if cfg := Default(); cfg.MaxOpenDatabases != DefaultMaxOpenDatabases {
		t.Fatalf("Default().MaxOpenDatabases = %d, want %d", cfg.MaxOpenDatabases, DefaultMaxOpenDatabases)
	}
}

func TestLoadMaxOpenDatabasesRejectsNegative(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dbmgr.conf")
	if err := os.WriteFile(path, []byte("max_open_databases=-1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected error")
	}
}

func TestLoadMaxTotalBufferPages(t *testing.T) {
	path := filepath.Join(t.TempDir(), "budget.conf")
	if err := os.WriteFile(path, []byte("buffer_pages=64\nmax_total_buffer_pages=256\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MaxTotalBufferPages != 256 {
		t.Fatalf("%+v", cfg)
	}
}

func TestLoadMaxTotalBufferPagesRejectsNegative(t *testing.T) {
	path := filepath.Join(t.TempDir(), "budget.conf")
	if err := os.WriteFile(path, []byte("max_total_buffer_pages=-1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected error")
	}
}

func TestValidateMaxTotalBufferPagesBelowBufferPagesRejected(t *testing.T) {
	cfg := Default()
	cfg.BufferPages = 100
	cfg.MaxTotalBufferPages = 50
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error")
	}
}

func TestValidateMaxTotalBufferPagesZeroUnbounded(t *testing.T) {
	cfg := Default()
	cfg.MaxTotalBufferPages = 0
	if err := cfg.Validate(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadConnectionLimits(t *testing.T) {
	path := filepath.Join(t.TempDir(), "conn.conf")
	body := "max_connections=64\nmax_connections_per_user=4\nidle_timeout_ms=30000\nshutdown_drain_ms=5000\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MaxConnections != 64 || cfg.MaxConnectionsPerUser != 4 || cfg.IdleTimeoutMS != 30000 || cfg.DrainTimeoutMS != 5000 {
		t.Fatalf("%+v", cfg)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestDefaultEnablesGracefulDrain(t *testing.T) {
	if got := Default().DrainTimeoutMS; got != DefaultDrainTimeoutMS {
		t.Fatalf("DrainTimeoutMS = %d, want %d", got, DefaultDrainTimeoutMS)
	}
}

func TestLoadShutdownDrainMSZeroDisablesDraining(t *testing.T) {
	path := filepath.Join(t.TempDir(), "conn.conf")
	if err := os.WriteFile(path, []byte("shutdown_drain_ms=0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DrainTimeoutMS != 0 {
		t.Fatalf("DrainTimeoutMS = %d, want 0", cfg.DrainTimeoutMS)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestLoadConnectionLimitsRejectsInvalid(t *testing.T) {
	cases := []string{
		"max_connections=0\n",
		"max_connections=-1\n",
		"max_connections_per_user=-1\n",
		"idle_timeout_ms=0\n",
		"idle_timeout_ms=-1\n",
		"shutdown_drain_ms=-1\n",
	}
	for _, body := range cases {
		path := filepath.Join(t.TempDir(), "conn.conf")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(path); err == nil {
			t.Fatalf("Load(%q): expected error", body)
		}
	}
}

func TestLoadStatementTransactionLockTimeouts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "timeouts.conf")
	body := "statement_timeout_ms=10000\ntransaction_timeout_ms=60000\nlock_timeout_ms=5000\nidle_transaction_timeout_ms=15000\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.StatementTimeoutMS != 10000 || cfg.TransactionTimeoutMS != 60000 || cfg.LockTimeoutMS != 5000 || cfg.IdleTransactionTimeoutMS != 15000 {
		t.Fatalf("%+v", cfg)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestLoadTransactionAndLockTimeoutZeroDisables(t *testing.T) {
	path := filepath.Join(t.TempDir(), "timeouts.conf")
	if err := os.WriteFile(path, []byte("transaction_timeout_ms=0\nlock_timeout_ms=0\nidle_transaction_timeout_ms=0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TransactionTimeoutMS != 0 || cfg.LockTimeoutMS != 0 || cfg.IdleTransactionTimeoutMS != 0 {
		t.Fatalf("%+v", cfg)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestLoadTimeoutsRejectsInvalid(t *testing.T) {
	cases := []string{
		"statement_timeout_ms=0\n",
		"statement_timeout_ms=-1\n",
		"transaction_timeout_ms=-1\n",
		"lock_timeout_ms=-1\n",
		"idle_transaction_timeout_ms=-1\n",
	}
	for _, body := range cases {
		path := filepath.Join(t.TempDir(), "timeouts.conf")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(path); err == nil {
			t.Fatalf("Load(%q): expected error", body)
		}
	}
}

func TestLoadWALRetentionMS(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wal.conf")
	if err := os.WriteFile(path, []byte("wal_archive=/data/archive\nwal_retention_ms=604800000\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.WalArchive != "/data/archive" || cfg.WalRetentionMS != 604800000 {
		t.Fatalf("%+v", cfg)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestLoadWALRetentionMSZeroDisables(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wal.conf")
	if err := os.WriteFile(path, []byte("wal_retention_ms=0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.WalRetentionMS != 0 {
		t.Fatalf("%+v", cfg)
	}
}

func TestLoadWALRetentionMSRejectsNegative(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wal.conf")
	if err := os.WriteFile(path, []byte("wal_retention_ms=-1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected error")
	}
}

func TestLoadDiskWatermark(t *testing.T) {
	path := filepath.Join(t.TempDir(), "disk.conf")
	body := "disk_watermark_check_ms=60000\n" +
		"disk_watermark_warn_percent=80\n" +
		"disk_watermark_reject_percent=90\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DiskWatermarkCheckMS != 60000 || cfg.DiskWatermarkWarnPercent != 80 || cfg.DiskWatermarkRejectPercent != 90 {
		t.Fatalf("%+v", cfg)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if warn, reject := cfg.DiskWatermarkThresholds(); warn != 80 || reject != 90 {
		t.Fatalf("DiskWatermarkThresholds() = %v, %v", warn, reject)
	}
}

func TestLoadDiskWatermarkZeroDisablesAndUsesDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "disk.conf")
	if err := os.WriteFile(path, []byte("disk_watermark_check_ms=0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DiskWatermarkCheckMS != 0 {
		t.Fatalf("%+v", cfg)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if warn, reject := cfg.DiskWatermarkThresholds(); warn != DefaultDiskWatermarkWarnPercent || reject != DefaultDiskWatermarkRejectPercent {
		t.Fatalf("DiskWatermarkThresholds() = %v, %v, want defaults %v, %v", warn, reject, DefaultDiskWatermarkWarnPercent, DefaultDiskWatermarkRejectPercent)
	}
}

func TestLoadDiskWatermarkRejectsInvalid(t *testing.T) {
	cases := []string{
		"disk_watermark_check_ms=-1\n",
		"disk_watermark_warn_percent=-1\n",
		"disk_watermark_warn_percent=101\n",
		"disk_watermark_reject_percent=-1\n",
		"disk_watermark_reject_percent=101\n",
	}
	for _, body := range cases {
		path := filepath.Join(t.TempDir(), "disk.conf")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(path); err == nil {
			t.Fatalf("expected error loading %q", body)
		}
	}
}

func TestValidateDiskWatermarkWarnMustBeBelowReject(t *testing.T) {
	cfg := Default()
	cfg.DiskWatermarkWarnPercent = 95
	cfg.DiskWatermarkRejectPercent = 85
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error when warn_percent >= reject_percent")
	}

	cfg.DiskWatermarkWarnPercent = 90
	cfg.DiskWatermarkRejectPercent = 90
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error when warn_percent == reject_percent")
	}
}

func TestLoadReplicaLag(t *testing.T) {
	path := filepath.Join(t.TempDir(), "replicalag.conf")
	body := "replica_lag_check_ms=30000\n" +
		"replica_lag_warn_entries=500\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ReplicaLagCheckMS != 30000 || cfg.ReplicaLagWarnEntries != 500 {
		t.Fatalf("%+v", cfg)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if got := cfg.ReplicaLagWarnThreshold(); got != 500 {
		t.Fatalf("ReplicaLagWarnThreshold() = %v, want 500", got)
	}
}

func TestLoadReplicaLagZeroDisablesAndUsesDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "replicalag.conf")
	if err := os.WriteFile(path, []byte("replica_lag_check_ms=0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ReplicaLagCheckMS != 0 {
		t.Fatalf("%+v", cfg)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if got := cfg.ReplicaLagWarnThreshold(); got != DefaultReplicaLagWarnEntries {
		t.Fatalf("ReplicaLagWarnThreshold() = %v, want default %v", got, DefaultReplicaLagWarnEntries)
	}
}

func TestLoadReplicaLagRejectsInvalid(t *testing.T) {
	cases := []string{
		"replica_lag_check_ms=-1\n",
		"replica_lag_warn_entries=-1\n",
	}
	for _, body := range cases {
		path := filepath.Join(t.TempDir(), "replicalag.conf")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(path); err == nil {
			t.Fatalf("expected error loading %q", body)
		}
	}
}

func TestLoadAuditSigningKeyset(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.conf")
	body := "audit_file=/var/log/nextsql/audit.jsonl\n" +
		"audit_signing_keyset=/etc/nextsql/audit.keys\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AuditFile != "/var/log/nextsql/audit.jsonl" || cfg.AuditSigningKeyset != "/etc/nextsql/audit.keys" {
		t.Fatalf("audit config: %+v", cfg)
	}
}

func TestValidateLogLevel(t *testing.T) {
	cfg := Default()
	cfg.LogLevel = "trace"
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected invalid log level")
	}
}

func TestLoadClusterKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ha.conf")
	body := "node_id=n1\nraft_bind=127.0.0.1:7211\nraft_join=n1=127.0.0.1:7211,n2=127.0.0.1:7212,n3=127.0.0.1:7213\nraft_bootstrap=true\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.NodeID != "n1" || cfg.RaftBind != "127.0.0.1:7211" || !cfg.RaftBootstrap {
		t.Fatalf("%+v", cfg)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestLoadAndValidateMTLS(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mtls.conf")
	body := "tls_cert=/etc/nextsql/server.crt\ntls_key=/etc/nextsql/server.key\ntls_client_ca=/etc/nextsql/client-ca.pem\ntls_client_crl=/etc/nextsql/client-crl.pem\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TLSClientCA != "/etc/nextsql/client-ca.pem" || cfg.TLSClientCRL != "/etc/nextsql/client-crl.pem" {
		t.Fatalf("%+v", cfg)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	cfg.TLSCert = ""
	cfg.TLSKey = ""
	if err := cfg.Validate(); err == nil {
		t.Fatal("client CA accepted without server key pair")
	}
	cfg = Default()
	cfg.TLSClientCRL = "/etc/nextsql/client-crl.pem"
	if err := cfg.Validate(); err == nil {
		t.Fatal("client CRL accepted without client CA")
	}
}

func TestLoadAndValidateShortLivedCredentials(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token.conf")
	body := "token_verify_keyset=/etc/nextsql/token.keyset\ntoken_revocations=/etc/nextsql/token.revocations\ntoken_audience=prod-eu\ntoken_identity_source_hint=7:oidc, 9:OIDC\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TokenKeyset != "/etc/nextsql/token.keyset" || cfg.TokenRevocations != "/etc/nextsql/token.revocations" || cfg.TokenAudience != "prod-eu" ||
		cfg.TokenIdentitySourceHints[7] != "oidc" || cfg.TokenIdentitySourceHints[9] != "oidc" {
		t.Fatalf("%+v", cfg)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	cfg = Default()
	cfg.TokenAudience = "prod-eu"
	if err := cfg.Validate(); err == nil {
		t.Fatal("token audience accepted without a verify keyset")
	}
	cfg = Default()
	cfg.TokenRevocations = "/etc/nextsql/token.revocations"
	if err := cfg.Validate(); err == nil {
		t.Fatal("token revocations accepted without a verify keyset")
	}
	cfg = Default()
	cfg.TokenIdentitySourceHints = map[uint32]string{7: "oidc"}
	if err := cfg.Validate(); err == nil {
		t.Fatal("token identity source hint accepted without a verify keyset")
	}
}

func TestLoadRejectsBadTokenIdentitySourceHints(t *testing.T) {
	tooMany := make([]string, 65)
	for i := range tooMany {
		tooMany[i] = strconv.Itoa(i+1) + ":oidc"
	}
	for _, value := range []string{"0:oidc", "x:oidc", "1:token", "1:oidc,1:oidc", "1=oidc", strings.Join(tooMany, ",")} {
		path := filepath.Join(t.TempDir(), "token.conf")
		body := "token_verify_keyset=/etc/nextsql/token.keyset\ntoken_identity_source_hint=" + value + "\n"
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(path); err == nil {
			t.Fatalf("accepted token_identity_source_hint=%q", value)
		}
	}
	emptyPath := filepath.Join(t.TempDir(), "token-empty.conf")
	if err := os.WriteFile(emptyPath, []byte("token_identity_source_hint=\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(emptyPath)
	if err != nil || len(cfg.TokenIdentitySourceHints) != 0 {
		t.Fatalf("empty optional hint should disable mapping: cfg=%+v err=%v", cfg, err)
	}
}

func TestLoadAndValidateEmbeddedAuthBroker(t *testing.T) {
	path := filepath.Join(t.TempDir(), "embedded.conf")
	body := "data_dir=/var/lib/nextsql\n" +
		"token_verify_keyset=/etc/nextsql/token.keyset.pub\n" +
		"auth_broker_config=/etc/nextsql/auth-broker.conf\n" +
		"auth_broker_listen=127.0.0.1:8645\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.EmbeddedAuthBrokerEnabled() || cfg.EmbeddedAuthBrokerConfigPath() != "/etc/nextsql/auth-broker.conf" || cfg.AuthBrokerListen != "127.0.0.1:8645" {
		t.Fatalf("embedded broker config: %+v", cfg)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}

	cfg.AuthBrokerConfig = ""
	if got := cfg.EmbeddedAuthBrokerConfigPath(); got != "/var/lib/nextsql/"+AuthBrokerFileName {
		t.Fatalf("default embedded broker config path = %q", got)
	}
	cfg.TokenKeyset = ""
	if err := cfg.Validate(); err == nil {
		t.Fatal("embedded broker accepted without token_verify_keyset")
	}
	cfg.TokenKeyset = "/etc/nextsql/token.keyset.pub"
	cfg.RaftBind = "127.0.0.1:7211"
	if err := cfg.Validate(); err == nil {
		t.Fatal("embedded broker accepted with HA enabled")
	}
}

// TestSafeEntries covers the system.config read-model source (Manager
// Configuration view, M8): every network-address-shaped key is redacted,
// every other non-default setting passes through unredacted, a bare
// Default() produces only its own core fields (no stray entries for
// never-set fields), and the key set is exactly what Marshal would have
// emitted — proving SafeEntries can never name a key Marshal doesn't.
func TestSafeEntries(t *testing.T) {
	cfg := Default()
	cfg.DataDir = "/var/lib/nextsql"
	cfg.KeyFile = "/etc/nextsql/master.key"
	cfg.ListenAddr = "10.0.0.5:7210"
	cfg.RaftBind = "10.0.0.5:7211"
	cfg.RaftJoin = "1=10.0.0.5:7211,2=10.0.0.6:7211"
	cfg.AuthBrokerListen = "127.0.0.1:8645"
	cfg.MaxConnections = 256

	entries := cfg.SafeEntries()
	byKey := make(map[string]string, len(entries))
	for _, e := range entries {
		if _, dup := byKey[e.Key]; dup {
			t.Fatalf("duplicate key %q in SafeEntries", e.Key)
		}
		byKey[e.Key] = e.Value
	}

	for _, redacted := range []string{"listen_addr", "raft_bind", "raft_join", "auth_broker_listen"} {
		v, ok := byKey[redacted]
		if !ok {
			t.Fatalf("SafeEntries missing %q entirely", redacted)
		}
		if v != "[redacted]" {
			t.Fatalf("SafeEntries[%q] = %q, want [redacted]", redacted, v)
		}
		if strings.Contains(v, "10.0.0") || strings.Contains(v, "127.0.0.1") || strings.Contains(v, ":") {
			t.Fatalf("SafeEntries[%q] leaked address material: %q", redacted, v)
		}
	}
	if byKey["data_dir"] != "/var/lib/nextsql" || byKey["key_file"] != "/etc/nextsql/master.key" {
		t.Fatalf("non-address fields must pass through unredacted: %+v", byKey)
	}
	if byKey["max_connections"] != "256" {
		t.Fatalf("numeric field mismatch: %+v", byKey)
	}

	// Same key set Marshal emits — split on "=" the same way SafeEntries
	// does internally, so this test would catch either side drifting.
	wantKeys := map[string]bool{}
	for _, line := range strings.Split(strings.TrimRight(string(cfg.Marshal()), "\n"), "\n") {
		if line == "" {
			continue
		}
		k, _, _ := strings.Cut(line, "=")
		wantKeys[k] = true
	}
	if len(wantKeys) != len(byKey) {
		t.Fatalf("SafeEntries key count %d != Marshal key count %d", len(byKey), len(wantKeys))
	}
	for k := range wantKeys {
		if _, ok := byKey[k]; !ok {
			t.Fatalf("SafeEntries missing Marshal key %q", k)
		}
	}

	// A bare Default() must not fabricate entries for fields nothing set.
	bare := Default().SafeEntries()
	for _, e := range bare {
		if e.Key == "raft_bind" || e.Key == "raft_join" || e.Key == "max_connections" {
			t.Fatalf("bare Default() must not emit unset field %q", e.Key)
		}
	}
}

func TestWithSetting(t *testing.T) {
	base := Default()
	base.DataDir = "/var/lib/nextsql"
	base.BufferPages = 1024

	// A numeric setting: parsed and range-checked like Load.
	next, err := base.WithSetting("buffer_pages", "4096", false)
	if err != nil {
		t.Fatal(err)
	}
	if next.BufferPages != 4096 {
		t.Fatalf("buffer_pages = %d, want 4096", next.BufferPages)
	}
	if base.BufferPages != 1024 {
		t.Fatalf("WithSetting mutated the receiver: %d", base.BufferPages)
	}
	if next.DataDir != "/var/lib/nextsql" {
		t.Fatalf("WithSetting dropped an unrelated key: %q", next.DataDir)
	}

	// A bool.
	next, err = base.WithSetting("require_client_key", "true", false)
	if err != nil || !next.RequireClientKey {
		t.Fatalf("require_client_key: %v / %v", next.RequireClientKey, err)
	}

	// DEFAULT resets to the built-in default.
	base.MaxConnections = 999
	next, err = base.WithSetting("max_connections", "", true)
	if err != nil {
		t.Fatal(err)
	}
	if next.Setting("max_connections") != "" {
		t.Fatalf("max_connections not reset: %q", next.Setting("max_connections"))
	}

	// Rejections: unknown key, unparseable value, out-of-range value,
	// empty value without reset, a value that fails Validate.
	for _, c := range []struct{ key, val string }{
		{"nonsense_key", "1"},
		{"buffer_pages", "not-a-number"},
		{"buffer_pages", "0"},
		{"buffer_pages", ""},
		{"disk_watermark_warn_percent", "150"},
	} {
		if _, err := base.WithSetting(c.key, c.val, false); err == nil {
			t.Fatalf("WithSetting(%q,%q) accepted, want error", c.key, c.val)
		}
	}

	// Every settable key round-trips: set it to a plausible value, get it back.
	for _, k := range SettableKeys() {
		if !settableKeys[k] {
			t.Fatalf("SettableKeys returned %q not in settableKeys", k)
		}
	}
}

func TestDiffState(t *testing.T) {
	running := Default()
	running.DataDir = "/data"
	running.BufferPages = 2048 // came from a startup flag
	running.MaxConnections = 100

	file := Default()
	file.DataDir = "/data"
	file.BufferPages = 1024 // nextsql.conf still says the old value
	file.MaxConnections = 100

	states := DiffState(running, file)
	byKey := map[string]EntryState{}
	for _, s := range states {
		byKey[s.Key] = s
	}
	if s := byKey["buffer_pages"]; s.Value != "2048" || s.FileValue != "1024" || !s.RestartRequired {
		t.Fatalf("buffer_pages state = %+v", s)
	}
	if s := byKey["data_dir"]; s.RestartRequired {
		t.Fatalf("data_dir matches; restart_required must be false: %+v", s)
	}
	if s := byKey["max_connections"]; s.RestartRequired {
		t.Fatalf("max_connections matches: %+v", s)
	}

	// Same config for both -> nothing needs a restart.
	for _, s := range DiffState(running, running) {
		if s.RestartRequired {
			t.Fatalf("identical configs but %q reports restart_required", s.Key)
		}
	}

	// An address key: values differ but both display "[redacted]"; the
	// RestartRequired flag must still be computed from the real values.
	r2 := Default()
	r2.ListenAddr = "0.0.0.0:7210"
	f2 := Default()
	f2.ListenAddr = "127.0.0.1:7210"
	for _, s := range DiffState(r2, f2) {
		if s.Key == "listen_addr" {
			if s.Value != "[redacted]" || s.FileValue != "[redacted]" {
				t.Fatalf("listen_addr not redacted: %+v", s)
			}
			if !s.RestartRequired {
				t.Fatalf("listen_addr differs but RestartRequired=false: %+v", s)
			}
		}
	}
}
