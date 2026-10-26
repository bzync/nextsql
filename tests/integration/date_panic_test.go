package integration

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/bzync/nextsql/internal/executor"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/protocol"
)

func TestDateFunctionInvalidArityOverTLS(t *testing.T) {
	addr, tlsCfg := startTLSServer(t)
	conn := openApp(t, addr, tlsCfg)
	other := openApp(t, addr, tlsCfg)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	for _, sql := range []string{
		`SELECT DATE_ADD(NOW(), INTERVAL '-5 minutes')`,
		`SELECT DATE_ADD()`,
		`SELECT DATE_ADD(NOW())`,
		`SELECT DATE_ADD(NULL, NULL)`,
		`SELECT DATE_ADD(NOW(), -5, 'minute', NULL)`,
		`SELECT DATE_DIFF()`,
		`SELECT DATE_DIFF(NOW())`,
		`SELECT DATE_DIFF(NOW(), NOW())`,
		`SELECT DATE_DIFF(NOW(), NOW(), 'minute', NULL)`,
		`SELECT EXTRACT()`,
		`SELECT EXTRACT('minute')`,
		`SELECT EXTRACT('minute', NOW(), NULL)`,
		`SELECT DATE_TRUNC()`,
		`SELECT DATE_TRUNC('minute')`,
		`SELECT DATE_TRUNC('minute', NOW(), NULL)`,
	} {
		t.Run(sql, func(t *testing.T) {
			if _, err := conn.Exec(ctx, sql); !nerr.HasCode(err, nerr.InvalidArgument) {
				t.Fatalf("expected InvalidArgument, got %v", err)
			}
			stmt, err := conn.Prepare(ctx, sql)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			if _, err := stmt.Exec(ctx); !nerr.HasCode(err, nerr.InvalidArgument) {
				t.Fatalf("prepared: expected InvalidArgument, got %v", err)
			}
			if _, err := conn.Exec(ctx, `SELECT 1`); err != nil {
				t.Fatalf("affected session unusable after validation error: %v", err)
			}
			if _, err := other.Exec(ctx, `SELECT 1`); err != nil {
				t.Fatalf("other session unusable: %v", err)
			}
		})
	}
	if _, err := conn.Exec(ctx, `SELECT DATE_ADD(NOW(), -5, 'minute')`); err != nil {
		t.Fatal(err)
	}
	openApp(t, addr, tlsCfg) // the listener still accepts new sessions
}

type panicLogWriter chan string

func (w panicLogWriter) Write(p []byte) (int, error) {
	w <- string(p)
	return len(p), nil
}

func TestSessionPanicRollsBackAndKeepsServerAvailable(t *testing.T) {
	logs := make(panicLogWriter, 4)
	var srv *protocol.Server
	addr, tlsCfg := startTLSServer(t, func(s *protocol.Server) {
		srv = s
		s.Limits.MaxSessions = 2
		s.Limits.MaxSessionsPerUser = 2
		s.Limits.MaxSessionsPerDatabase = 2
		s.Limits.MaxSessionsPerRealm = 2
		s.Log = slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelError}))
		// Inject an execution panic through the existing operation seam;
		// no production test hook or broken storage state is needed.
		s.DB.SetBackupOps(nil, nil, func(string) (executor.BackupVerifyResult, error) {
			panic("sensitive query data")
		})
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	busy := openApp(t, addr, tlsCfg)
	other := openApp(t, addr, tlsCfg)
	for _, sql := range []string{
		`CREATE TABLE panic_txn (id STRING PRIMARY KEY)`,
		`BEGIN`,
		`INSERT INTO panic_txn (id) VALUES ('1')`,
	} {
		if _, err := busy.Exec(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := busy.Exec(ctx, `VERIFY BACKUP 'panic'`); err == nil {
		t.Fatal("panicking session reported success")
	}
	// Recovery logs only after all session and admission cleanup has run.
	select {
	case msg := <-logs:
		if !strings.Contains(msg, "session panic") || strings.Contains(msg, "sensitive") {
			t.Fatalf("missing or unsafe diagnostic: %s", msg)
		}
	case <-ctx.Done():
		t.Fatal("session panic was not recovered")
	}
	if got := len(srv.DatabaseHandle().LiveSessions()); got != 1 {
		t.Fatalf("panicking backend was not unregistered: live sessions=%d", got)
	}
	rows, err := other.Exec(ctx, `SELECT id FROM panic_txn`)
	if err != nil || len(rows.Rows) != 0 {
		t.Fatalf("uncommitted insert was not rolled back: rows=%v err=%v", rows, err)
	}
	// The same key can be inserted immediately: rollback released its lock.
	if _, err := other.Exec(ctx, `INSERT INTO panic_txn (id) VALUES ('1')`); err != nil {
		t.Fatalf("lock leaked by panicking session: %v", err)
	}
	replacement := openApp(t, addr, tlsCfg) // admission slots were released
	if _, err := replacement.Exec(ctx, `SELECT id FROM panic_txn`); err != nil {
		t.Fatalf("replacement session unusable: %v", err)
	}
}
