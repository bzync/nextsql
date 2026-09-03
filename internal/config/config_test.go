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
