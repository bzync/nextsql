package nextsql

import (
	"crypto/tls"
	"testing"

	"github.com/bzync/nextsql/internal/nerr"
)

func TestOpenRejectsURLWithKey(t *testing.T) {
	_, err := Open(Config{
		Address:  "nextsql://app:secret@db.example.com:7210/prod?key=deadbeef",
		User:     "app",
		Password: "x",
		TLS:      &tls.Config{MinVersion: tls.VersionTLS13},
	})
	if !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("got %v", err)
	}
}

func TestClusterRoutingClassifiers(t *testing.T) {
	reads := []string{
		"SELECT 1",
		"  select n from t",
		"\n-- comment\nSELECT n FROM t",
		"(SELECT n FROM t) UNION (SELECT n FROM u)",
		"SHOW TASKS",
		"WITH c AS (SELECT n FROM t) SELECT n FROM c",
	}
	for _, s := range reads {
		if !isReadOnlySQL(s) {
			t.Fatalf("expected read-only: %q", s)
		}
	}
	writes := []string{
		"INSERT INTO t (n) VALUES ('x')",
		"UPDATE t SET n = 'x'",
		"DELETE FROM t",
		"UPSERT INTO t (id) VALUES ('x')",
		"CREATE TABLE t (id STRING PRIMARY KEY)",
		"EXPLAIN ANALYZE SELECT n FROM t",
		"WITH c AS (SELECT 1) INSERT INTO t SELECT * FROM c",
		"BEGIN",
	}
	for _, s := range writes {
		if isReadOnlySQL(s) {
			t.Fatalf("expected not read-only: %q", s)
		}
	}
	for _, tc := range []struct {
		sql        string
		begin, end bool
	}{
		{"BEGIN", true, false},
		{"  begin transaction ", true, false},
		{"START TRANSACTION", true, false},
		{"COMMIT", false, true},
		{"rollback", false, true},
		{"SELECT 1", false, false},
	} {
		b, e := txnControl(tc.sql)
		if b != tc.begin || e != tc.end {
			t.Fatalf("txnControl(%q) = %v,%v want %v,%v", tc.sql, b, e, tc.begin, tc.end)
		}
	}
}

func TestOpenClusterNeedsAnAddress(t *testing.T) {
	if _, err := OpenCluster(nil, Config{User: "app"}); !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("got %v", err)
	}
}

func TestOpenRequiresTLSOffLoopback(t *testing.T) {
	_, err := Open(Config{
		Address:       "db.example.com:7210",
		User:          "app",
		Password:      "x",
		InsecureNoTLS: true,
	})
	if !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("got %v", err)
	}
}
