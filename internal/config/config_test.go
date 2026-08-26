package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadAndValidate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.conf")
	body := "data_dir=/var/lib/nextsql\nkey_file=/etc/nextsql/master.key\nbuffer_pages=64\nlog_level=debug\n# comment\nlisten_addr=127.0.0.1:9000\n"
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
