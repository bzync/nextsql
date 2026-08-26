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
