package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/bzync/nextsql/internal/backup"
	"github.com/bzync/nextsql/internal/executor"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/storage"
	"github.com/bzync/nextsql/internal/storage/format"
)

// engineBackupAdapter lets backup.CreateFromEngine checkpoint and read the
// snapshot coordinates from the server's already-open engine — no second
// storage.Open, so no second recovery pass that could truncate a WAL tail
// the live engine is mid-write on.
type engineBackupAdapter struct{ e *storage.Engine }

func (a engineBackupAdapter) Identity() format.Identity { return a.e.Identity() }
func (a engineBackupAdapter) Checkpoint() error         { return a.e.Checkpoint() }
func (a engineBackupAdapter) CheckpointLSN() format.LSN { return a.e.WAL.CheckpointLSN() }
func (a engineBackupAdapter) RedoLSN() format.LSN       { return a.e.WAL.RedoLSN() }
func (a engineBackupAdapter) DurableLSN() format.LSN    { return a.e.WAL.DurableLSN() }

func backupNameOK(name string) bool {
	if name == "" || len(name) > 128 || name == "." || name == ".." {
		return false
	}
	return !strings.ContainsAny(name, `/\`+"\x00") && !strings.Contains(name, "..")
}

// wireBackupOps installs the M5 backup callbacks on db, all operating on
// backupDir. Only called when a backup_dir is configured and db is the
// legacy/non-hosted database.
func wireBackupOps(db *executor.DB, backupDir string) {
	eng := db.Eng
	if eng == nil {
		return
	}
	dataDir := filepath.Dir(eng.Path())

	create := func() (executor.BackupCreateResult, error) {
		if err := os.MkdirAll(backupDir, 0o700); err != nil {
			return executor.BackupCreateResult{}, nerr.Wrap(nerr.IO, "nextsqld.backup", "mkdir backup_dir", err)
		}
		name := "backup-" + time.Now().UTC().Format("20060102T150405Z")
		dest := filepath.Join(backupDir, name)
		if _, err := os.Stat(dest); err == nil {
			name += "-" + strconv.FormatInt(time.Now().UnixNano()%1e9, 10)
			dest = filepath.Join(backupDir, name)
		}
		res, err := backup.CreateFromEngine(engineBackupAdapter{eng}, dataDir, dest, eng.Keys(), backup.Options{})
		if err != nil {
			return executor.BackupCreateResult{}, err
		}
		return executor.BackupCreateResult{
			Name:          name,
			CheckpointLSN: uint64(res.Header.Checkpoint),
			DurableLSN:    uint64(res.Header.DurableLSN),
			Members:       res.Members,
		}, nil
	}

	list := func() ([]executor.BackupListEntry, bool) {
		infos, err := backup.ListBackups(backupDir)
		if err != nil {
			return []executor.BackupListEntry{}, true
		}
		out := make([]executor.BackupListEntry, 0, len(infos))
		for _, in := range infos {
			out = append(out, executor.BackupListEntry{
				Name:          filepath.Base(in.Path),
				CreatedUnix:   in.Header.CreatedNano / 1e9,
				DatabaseID:    in.Header.Identity.DatabaseString(),
				CheckpointLSN: uint64(in.Header.Checkpoint),
				DurableLSN:    uint64(in.Header.DurableLSN),
			})
		}
		return out, true
	}

	verify := func(name string) (executor.BackupVerifyResult, error) {
		if !backupNameOK(name) {
			return executor.BackupVerifyResult{}, nerr.New(nerr.InvalidArgument, "nextsqld.backup", "invalid backup name")
		}
		src := filepath.Join(backupDir, name)
		if _, err := os.Stat(src); err != nil {
			return executor.BackupVerifyResult{}, nerr.New(nerr.NotFound, "nextsqld.backup", "no such backup")
		}
		if err := backup.Verify(src, eng.Keys(), true); err != nil {
			return executor.BackupVerifyResult{Name: name, OK: false, Problem: err.Error()}, nil
		}
		return executor.BackupVerifyResult{Name: name, OK: true}, nil
	}

	db.SetBackupOps(create, list, verify)
}
