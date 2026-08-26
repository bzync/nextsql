package migrate

import (
	"context"
	"sort"
	"strconv"

	"github.com/bzync/nextsql/internal/catalog"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/sql/types"
)

// History statements match design B.3 / B.5 exactly.
const (
	historySelectSQL = `SELECT version, name, applied_at, checksum, execution_ms, dirty, direction FROM nsql_schema_migrations`

	insertDirtySQL = `INSERT INTO nsql_schema_migrations
    (version, name, checksum, execution_ms, dirty, direction)
VALUES
    ($1, $2, $3, 0, 1, 'up')`

	finalizeSQL = `UPDATE nsql_schema_migrations
SET dirty = 0, applied_at = NOW(), execution_ms = $1
WHERE version = $2`

	// markDirtyDownSQL flips the existing up row so a crash mid-down is visible (B.3 / B.8).
	// The version PK already exists; a second INSERT would conflict.
	markDirtyDownSQL = `UPDATE nsql_schema_migrations
SET dirty = 1, direction = 'down', execution_ms = 0
WHERE version = $1`

	insertCleanSQL = `INSERT INTO nsql_schema_migrations
    (version, name, checksum, execution_ms, dirty, direction)
VALUES
    ($1, $2, $3, 0, 0, 'up')`

	updateChecksumSQL = `UPDATE nsql_schema_migrations SET checksum = $1 WHERE version = $2`

	deleteVersionSQL    = `DELETE FROM nsql_schema_migrations WHERE version = $1`
	deleteAfterSQL      = `DELETE FROM nsql_schema_migrations WHERE version > $1`
	deleteAllHistorySQL = `DELETE FROM nsql_schema_migrations`
	clearDirtySQL       = `UPDATE nsql_schema_migrations SET dirty = 0`
)

// HistoryRow is one nsql_schema_migrations record.
type HistoryRow struct {
	Version     string
	Name        string
	AppliedAt   types.Value
	Checksum    string
	ExecutionMS int64
	Dirty       bool
	Direction   string
}

func createHistory(ctx context.Context, db Execer) error {
	_, err := db.Exec(ctx, catalog.HistoryDDL)
	return err
}

func loadHistory(ctx context.Context, db Execer, bootstrap bool) ([]HistoryRow, error) {
	res, err := db.Exec(ctx, historySelectSQL)
	if err != nil && nerr.HasCode(err, nerr.NotFound) && bootstrap {
		if cerr := createHistory(ctx, db); cerr != nil && !nerr.HasCode(cerr, nerr.AlreadyExists) {
			return nil, cerr
		}
		res, err = db.Exec(ctx, historySelectSQL)
	}
	if err != nil {
		if nerr.HasCode(err, nerr.NotFound) {
			if bootstrap {
				return nil, AsValidation(nerr.New(nerr.InvalidArgument, "migrate", "nsql_schema_migrations has the wrong schema"))
			}
			return nil, nil
		}
		return nil, err
	}
	return parseHistory(res)
}

func parseHistory(res Result) ([]HistoryRow, error) {
	out := make([]HistoryRow, 0, len(res.Rows))
	for _, row := range res.Rows {
		if len(row) < 7 {
			return nil, AsValidation(nerr.New(nerr.InvalidArgument, "migrate", "nsql_schema_migrations has the wrong schema"))
		}
		h := HistoryRow{
			Version:     row[0].Str,
			Name:        row[1].Str,
			AppliedAt:   row[2],
			Checksum:    row[3].Str,
			ExecutionMS: decInt(row[4]),
			Dirty:       !row[5].Null && !row[5].Dec.IsZero(),
			Direction:   row[6].Str,
		}
		if h.Version == "" {
			return nil, AsValidation(nerr.New(nerr.InvalidArgument, "migrate", "nsql_schema_migrations has the wrong schema"))
		}
		out = append(out, h)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	return out, nil
}

func decInt(v types.Value) int64 {
	if v.Null {
		return 0
	}
	n, err := strconv.ParseInt(v.Dec.String(), 10, 64)
	if err != nil {
		return 0
	}
	return n
}

func historyByVersion(rows []HistoryRow) map[string]HistoryRow {
	m := make(map[string]HistoryRow, len(rows))
	for _, r := range rows {
		m[r.Version] = r
	}
	return m
}

func dirtyVersion(rows []HistoryRow) string {
	for _, r := range rows {
		if r.Dirty {
			return r.Version
		}
	}
	return ""
}

func currentVersion(rows []HistoryRow) string {
	if len(rows) == 0 {
		return ""
	}
	return rows[len(rows)-1].Version
}

func checksumMismatches(rows []HistoryRow, byVer map[string]Migration) []string {
	var out []string
	for _, r := range rows {
		m, ok := byVer[r.Version]
		if !ok {
			if r.Checksum == forcedChecksum {
				continue
			}
			out = append(out, r.Version)
			continue
		}
		if m.Up.Checksum != r.Checksum {
			out = append(out, r.Version)
		}
	}
	return out
}

func decMS(ms int64) types.Value {
	t, err := types.DecimalType(12, 0)
	if err != nil {
		t = types.Type{Kind: types.KindDecimal, Precision: 12}
	}
	d, err := types.ParseDecimal(strconv.FormatInt(ms, 10))
	if err != nil {
		return types.DecimalValue(types.Decimal{}, t)
	}
	return types.DecimalValue(d, t)
}
