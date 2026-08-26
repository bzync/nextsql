package migrate

import (
	"context"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/security"
	"github.com/bzync/nextsql/internal/sql/types"
)

// Execer is a persistent session: one statement per Exec, transactions persist.
type Execer interface {
	Exec(ctx context.Context, sql string, params ...types.Value) (Result, error)
}

// Result is a subset of the driver / executor result used by the runner.
type Result struct {
	Columns  []string
	Rows     [][]types.Value
	Affected int64
}

// Report is migrate status output (B.2).
type Report struct {
	Version    string
	Dirty      bool
	DirtyVer   string
	Applied    int
	Pending    int
	Mismatches []string
	PendingVer []string
}

// UpOptions selects how many pending files to apply.
type UpOptions struct {
	Count  int    // 0 means all remaining
	To     string // inclusive version cap; empty means no cap
	DryRun bool
}

// DownOptions selects how many applied files to roll back (B.8).
type DownOptions struct {
	Count  int    // 0 means all applied
	To     string // inclusive lower bound; empty means no bound
	DryRun bool
}

// Status bootstraps history, compares files, and reports dirty / checksum / pending.
func Status(ctx context.Context, db Execer, dir string) (Report, error) {
	migs, byVer, err := loadFiles(dir)
	if err != nil {
		return Report{}, err
	}
	rows, err := loadHistory(ctx, db, true)
	if err != nil {
		return Report{}, err
	}
	return buildReport(migs, byVer, rows), reportFault(rows, byVer)
}

func buildReport(migs []Migration, byVer map[string]Migration, rows []HistoryRow) Report {
	pending := pendingOf(migs, historyByVersion(rows))
	vers := make([]string, len(pending))
	for i, m := range pending {
		vers[i] = m.Version
	}
	return Report{
		Version:    currentVersion(rows),
		Dirty:      dirtyVersion(rows) != "",
		DirtyVer:   dirtyVersion(rows),
		Applied:    len(rows),
		Pending:    len(pending),
		Mismatches: checksumMismatches(rows, byVer),
		PendingVer: vers,
	}
}

func reportFault(rows []HistoryRow, byVer map[string]Migration) error {
	if len(checksumMismatches(rows, byVer)) > 0 {
		return ErrChecksum
	}
	if dirtyVersion(rows) != "" {
		return ErrDirty
	}
	return nil
}

// CurrentVersion returns the latest applied version, or "" when history is empty.
// It does not create the history table.
func CurrentVersion(ctx context.Context, db Execer) (string, error) {
	rows, err := loadHistory(ctx, db, false)
	if err != nil {
		return "", err
	}
	return currentVersion(rows), nil
}

// Pending lists unapplied migrations in order. It does not create the history
// table and does not abort on dirty/checksum (B.2 / B.7: those stop up/down).
func Pending(ctx context.Context, db Execer, dir string) ([]Migration, error) {
	migs, _, err := loadFiles(dir)
	if err != nil {
		return nil, err
	}
	rows, err := loadHistory(ctx, db, false)
	if err != nil {
		return nil, err
	}
	return pendingOf(migs, historyByVersion(rows)), nil
}

// Up applies pending up files on a persistent session (B.3).
func Up(ctx context.Context, db Execer, dir string, opt UpOptions) ([]string, error) {
	if opt.Count < 0 {
		return nil, AsValidation(nerr.New(nerr.InvalidArgument, "migrate", "--count must be >= 0"))
	}
	if opt.To != "" && !validVersionArg(opt.To) {
		return nil, AsValidation(nerr.New(nerr.InvalidArgument, "migrate", "invalid --to version"))
	}
	migs, byVer, err := loadFiles(dir)
	if err != nil {
		return nil, err
	}
	rows, err := loadHistory(ctx, db, true)
	if err != nil {
		return nil, err
	}
	if err := reportFault(rows, byVer); err != nil {
		return nil, err
	}
	plan, err := planUp(migs, historyByVersion(rows), opt)
	if err != nil {
		return nil, err
	}
	if err := checkStatementBudget(plan); err != nil {
		return nil, err
	}
	applied := make([]string, 0, len(plan))
	for _, m := range plan {
		stmts, err := loadApplyStatements(m)
		if err != nil {
			return applied, err
		}
		applied = append(applied, m.Version)
		if opt.DryRun {
			continue
		}
		if err := applyUp(ctx, db, m, stmts); err != nil {
			return applied[:len(applied)-1], err
		}
	}
	return applied, nil
}

// Down rolls back applied files newest-first (B.8). Missing .down.sql is exit 6.
func Down(ctx context.Context, db Execer, dir string, opt DownOptions) ([]string, error) {
	if opt.Count < 0 {
		return nil, AsValidation(nerr.New(nerr.InvalidArgument, "migrate", "--count must be >= 0"))
	}
	if opt.To != "" && !validVersionArg(opt.To) {
		return nil, AsValidation(nerr.New(nerr.InvalidArgument, "migrate", "invalid --to version"))
	}
	migs, byVer, err := loadFiles(dir)
	if err != nil {
		return nil, err
	}
	rows, err := loadHistory(ctx, db, true)
	if err != nil {
		return nil, err
	}
	if err := reportFault(rows, byVer); err != nil {
		return nil, err
	}
	plan, err := planDown(migs, rows, opt)
	if err != nil {
		return nil, err
	}
	if err := checkDownStatementBudget(plan); err != nil {
		return nil, err
	}
	applied := make([]string, 0, len(plan))
	for _, m := range plan {
		stmts, err := loadDownStatements(m)
		if err != nil {
			return applied, err
		}
		applied = append(applied, m.Version)
		if opt.DryRun {
			continue
		}
		if err := applyDown(ctx, db, m, stmts); err != nil {
			return applied[:len(applied)-1], err
		}
	}
	return applied, nil
}

// Force sets history to version without running migration SQL (B.7).
func Force(ctx context.Context, db Execer, dir, version string) error {
	version = strings.TrimSpace(version)
	if version == "" {
		return AsValidation(nerr.New(nerr.InvalidArgument, "migrate", "VERSION is required"))
	}
	if _, err := loadHistory(ctx, db, true); err != nil {
		return err
	}
	if isNoneVersion(version) {
		return withTxn(ctx, db, func() error {
			_, err := db.Exec(ctx, deleteAllHistorySQL)
			return err
		})
	}
	if !validVersionArg(version) {
		return AsValidation(nerr.New(nerr.InvalidArgument, "migrate", "invalid VERSION"))
	}
	name, sum := versionFileMeta(dir, version)
	return withTxn(ctx, db, func() error {
		if _, err := db.Exec(ctx, deleteAfterSQL, types.StringValue(version)); err != nil {
			return err
		}
		if _, err := db.Exec(ctx, deleteVersionSQL, types.StringValue(version)); err != nil {
			return err
		}
		if _, err := db.Exec(ctx, insertCleanSQL,
			types.StringValue(version),
			types.StringValue(name),
			types.StringValue(sum),
		); err != nil {
			return err
		}
		_, err := db.Exec(ctx, clearDirtySQL)
		return err
	})
}

// Repair rewrites stored checksums of applied files to match the working tree (B.6).
func Repair(ctx context.Context, db Execer, dir string) (int, error) {
	_, byVer, err := loadFiles(dir)
	if err != nil {
		return 0, err
	}
	rows, err := loadHistory(ctx, db, true)
	if err != nil {
		return 0, err
	}
	n := 0
	err = withTxn(ctx, db, func() error {
		for _, r := range rows {
			m, ok := byVer[r.Version]
			if !ok {
				continue
			}
			if m.Up.Checksum == r.Checksum {
				continue
			}
			if _, err := db.Exec(ctx, updateChecksumSQL, types.StringValue(m.Up.Checksum), types.StringValue(r.Version)); err != nil {
				return err
			}
			n++
		}
		return nil
	})
	return n, err
}

func loadFiles(dir string) ([]Migration, map[string]Migration, error) {
	migs, err := Validate(dir)
	if err != nil {
		return nil, nil, err
	}
	byVer := make(map[string]Migration, len(migs))
	for _, m := range migs {
		byVer[m.Version] = m
	}
	return migs, byVer, nil
}

func pendingOf(migs []Migration, have map[string]HistoryRow) []Migration {
	var out []Migration
	for _, m := range migs {
		if _, ok := have[m.Version]; !ok {
			out = append(out, m)
		}
	}
	return out
}

func planUp(migs []Migration, have map[string]HistoryRow, opt UpOptions) ([]Migration, error) {
	pending := pendingOf(migs, have)
	if opt.To != "" {
		known := false
		for _, m := range migs {
			if m.Version == opt.To {
				known = true
				break
			}
		}
		if !known {
			if _, ok := have[opt.To]; !ok {
				return nil, AsValidation(nerr.New(nerr.InvalidArgument, "migrate", "unknown --to version "+opt.To))
			}
		}
		var cut []Migration
		for _, m := range pending {
			if m.Version > opt.To {
				break
			}
			cut = append(cut, m)
		}
		pending = cut
	}
	if opt.Count > 0 && len(pending) > opt.Count {
		pending = pending[:opt.Count]
	}
	return pending, nil
}

func planDown(migs []Migration, rows []HistoryRow, opt DownOptions) ([]Migration, error) {
	byVer := make(map[string]Migration, len(migs))
	for _, m := range migs {
		byVer[m.Version] = m
	}
	if opt.To != "" {
		if _, ok := byVer[opt.To]; !ok {
			if _, ok := historyByVersion(rows)[opt.To]; !ok {
				return nil, AsValidation(nerr.New(nerr.InvalidArgument, "migrate", "unknown --to version "+opt.To))
			}
		}
	}
	var plan []Migration
	for i := len(rows) - 1; i >= 0; i-- {
		ver := rows[i].Version
		if opt.To != "" && ver < opt.To {
			break
		}
		m, ok := byVer[ver]
		if !ok {
			m = Migration{Version: ver, Name: rows[i].Name}
		}
		plan = append(plan, m)
	}
	if opt.Count > 0 && len(plan) > opt.Count {
		plan = plan[:opt.Count]
	}
	return plan, nil
}

func checkStatementBudget(plan []Migration) error {
	total := 0
	for _, m := range plan {
		body, err := readBody(m.Up.Path)
		if err != nil {
			return err
		}
		stmts, err := Split(parseSource(body))
		if err != nil {
			return AsValidation(err)
		}
		total += len(stmts)
		if total > MaxStatementsPerUp {
			return AsValidation(nerr.New(nerr.InvalidArgument, "migrate", "more than 4096 statements in one up"))
		}
	}
	return nil
}

func loadApplyStatements(m Migration) ([]string, error) {
	body, err := readBody(m.Up.Path)
	if err != nil {
		return nil, err
	}
	if Checksum(body) != m.Up.Checksum {
		return nil, ErrChecksum
	}
	stmts, err := Split(parseSource(body))
	if err != nil {
		return nil, AsValidation(err)
	}
	if len(stmts) > MaxStatementsPerFile {
		return nil, AsValidation(nerr.New(nerr.InvalidArgument, "migrate", filepath.Base(m.Up.Path)+": more than 32 statements"))
	}
	for _, s := range stmts {
		if len(s) > security.MaxSQLBytes {
			return nil, AsValidation(nerr.New(nerr.InvalidArgument, "migrate", filepath.Base(m.Up.Path)+": statement exceeds 1 MiB"))
		}
		if err := checkStatement(s); err != nil {
			return nil, AsValidation(annotateFile(m.Up.Path, err))
		}
	}
	return stmts, nil
}

func checkDownStatementBudget(plan []Migration) error {
	total := 0
	for _, m := range plan {
		if m.Down == nil {
			continue
		}
		body, err := readBody(m.Down.Path)
		if err != nil {
			return err
		}
		stmts, err := Split(parseSource(body))
		if err != nil {
			return AsValidation(err)
		}
		total += len(stmts)
		if total > MaxStatementsPerUp {
			return AsValidation(nerr.New(nerr.InvalidArgument, "migrate", "more than 4096 statements in one down"))
		}
	}
	return nil
}

func loadDownStatements(m Migration) ([]string, error) {
	if m.Down == nil {
		return nil, AsValidation(nerr.New(nerr.InvalidArgument, "migrate", "no down file for "+m.Version))
	}
	body, err := readBody(m.Down.Path)
	if err != nil {
		return nil, err
	}
	if Checksum(body) != m.Down.Checksum {
		return nil, ErrChecksum
	}
	stmts, err := Split(parseSource(body))
	if err != nil {
		return nil, AsValidation(err)
	}
	if len(stmts) > MaxStatementsPerFile {
		return nil, AsValidation(nerr.New(nerr.InvalidArgument, "migrate", filepath.Base(m.Down.Path)+": more than 32 statements"))
	}
	for _, s := range stmts {
		if len(s) > security.MaxSQLBytes {
			return nil, AsValidation(nerr.New(nerr.InvalidArgument, "migrate", filepath.Base(m.Down.Path)+": statement exceeds 1 MiB"))
		}
		if err := checkStatement(s); err != nil {
			return nil, AsValidation(annotateFile(m.Down.Path, err))
		}
	}
	return stmts, nil
}

func applyUp(ctx context.Context, db Execer, m Migration, stmts []string) error {
	start := time.Now()
	if _, err := db.Exec(ctx, "BEGIN"); err != nil {
		return AsApply(err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = db.Exec(ctx, "ROLLBACK")
		}
	}()
	if _, err := db.Exec(ctx, insertDirtySQL,
		types.StringValue(m.Version),
		types.StringValue(m.Name),
		types.StringValue(m.Up.Checksum),
	); err != nil {
		return AsApply(err)
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(ctx, stmt); err != nil {
			return AsApply(err)
		}
	}
	ms := time.Since(start).Milliseconds()
	if _, err := db.Exec(ctx, finalizeSQL, decMS(ms), types.StringValue(m.Version)); err != nil {
		return AsApply(err)
	}
	if _, err := db.Exec(ctx, "COMMIT"); err != nil {
		return AsApply(err)
	}
	committed = true
	return nil
}

func applyDown(ctx context.Context, db Execer, m Migration, stmts []string) error {
	if _, err := db.Exec(ctx, "BEGIN"); err != nil {
		return AsApply(err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = db.Exec(ctx, "ROLLBACK")
		}
	}()
	res, err := db.Exec(ctx, markDirtyDownSQL, types.StringValue(m.Version))
	if err != nil {
		return AsApply(err)
	}
	if res.Affected != 1 {
		return AsApply(nerr.New(nerr.NotFound, "migrate", "history row missing for "+m.Version))
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(ctx, stmt); err != nil {
			return AsApply(err)
		}
	}
	if _, err := db.Exec(ctx, deleteVersionSQL, types.StringValue(m.Version)); err != nil {
		return AsApply(err)
	}
	if _, err := db.Exec(ctx, "COMMIT"); err != nil {
		return AsApply(err)
	}
	committed = true
	return nil
}

func withTxn(ctx context.Context, db Execer, fn func() error) error {
	if _, err := db.Exec(ctx, "BEGIN"); err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = db.Exec(ctx, "ROLLBACK")
		}
	}()
	if err := fn(); err != nil {
		return err
	}
	if _, err := db.Exec(ctx, "COMMIT"); err != nil {
		return err
	}
	committed = true
	return nil
}

func validVersionArg(v string) bool {
	if len(v) != 14 {
		return false
	}
	_, err := strconv.ParseUint(v, 10, 64)
	return err == nil
}

func isNoneVersion(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "0", "none":
		return true
	default:
		return false
	}
}
