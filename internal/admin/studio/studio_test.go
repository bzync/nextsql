package studio

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/sql/types"
)

type fakeRows struct {
	cols     []string
	types    []types.Type
	rows     [][]types.Value
	i        int
	affected int64
	err      error
	closed   bool
}

func (r *fakeRows) Columns() []string         { return append([]string{}, r.cols...) }
func (r *fakeRows) ColumnTypes() []types.Type { return append([]types.Type{}, r.types...) }
func (r *fakeRows) Affected() int64           { return r.affected }
func (r *fakeRows) Err() error                { return r.err }
func (r *fakeRows) Close() error              { r.closed = true; return r.err }
func (r *fakeRows) Values() []types.Value     { return r.rows[r.i-1] }
func (r *fakeRows) Next() bool                { r.i++; return r.i <= len(r.rows) }

func TestValidateQueryRequest(t *testing.T) {
	for _, id := range []string{"q1", "query-123_abc", strings.Repeat("a", 128)} {
		if err := ValidateQueryID(id); err != nil {
			t.Errorf("ValidateQueryID(%q): %v", id, err)
		}
	}
	for _, id := range []string{"", "contains space", "a/b", strings.Repeat("a", 129)} {
		if err := ValidateQueryID(id); !nerr.HasCode(err, nerr.InvalidArgument) {
			t.Errorf("ValidateQueryID(%q): want invalid_argument, got %v", id, err)
		}
	}
	if err := ValidateSQL(" SELECT 1 "); err != nil {
		t.Fatalf("ValidateSQL(valid): %v", err)
	}
	if err := ValidateSQL(" \n\t "); !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("blank SQL: want invalid_argument, got %v", err)
	}
	if err := ValidateSQL(strings.Repeat("x", MaxSQLBytes+1)); !nerr.HasCode(err, nerr.Exhausted) {
		t.Fatalf("oversized SQL: want exhausted, got %v", err)
	}
}

func TestValidateAndConvertQueryParams(t *testing.T) {
	if err := ValidateParams(nil); err != nil {
		t.Fatalf("ValidateParams(nil): %v", err)
	}
	small := "x"
	if err := ValidateParams([]QueryParam{{Value: &small}, {Value: nil}}); err != nil {
		t.Fatalf("ValidateParams(ok): %v", err)
	}
	tooMany := make([]QueryParam, MaxQueryParams+1)
	if err := ValidateParams(tooMany); !nerr.HasCode(err, nerr.Exhausted) {
		t.Fatalf("too many params: want exhausted, got %v", err)
	}
	big := strings.Repeat("v", MaxQueryParamBytes+1)
	if err := ValidateParams([]QueryParam{{Value: &big}}); !nerr.HasCode(err, nerr.Exhausted) {
		t.Fatalf("oversized param value: want exhausted, got %v", err)
	}

	if ParamValues(nil) != nil {
		t.Fatal("ParamValues(nil) should be nil")
	}
	val := "42"
	got := ParamValues([]QueryParam{{Value: &val}, {Value: nil}})
	if len(got) != 2 {
		t.Fatalf("ParamValues len = %d, want 2", len(got))
	}
	if got[0].Null || got[0].Str != "42" {
		t.Fatalf("present value = %+v, want string 42", got[0])
	}
	if !got[1].Null || got[1].Typ.Kind != types.KindNull {
		t.Fatalf("nil value = %+v, want typed NULL", got[1])
	}
}

func TestValidateReconnectRequest(t *testing.T) {
	ok := []ReconnectRequest{
		{Password: "pw"},
		{Realm: "acme", Database: "prod", Password: "pw"},
		{Realm: "r-1_2", Database: "d-1_2", Password: "pw"},
	}
	for _, r := range ok {
		if err := r.Validate(); err != nil {
			t.Errorf("Validate(%+v): unexpected %v", r, err)
		}
	}
	bad := []ReconnectRequest{
		{Realm: "acme", Database: "prod"},                                   // no password
		{Realm: "bad name", Password: "pw"},                                 // space
		{Database: "bad/name", Password: "pw"},                              // slash
		{Realm: strings.Repeat("a", MaxConnNameBytes+1), Password: "pw"},    // over cap
		{Database: strings.Repeat("a", MaxConnNameBytes+1), Password: "pw"}, // over cap
	}
	for _, r := range bad {
		if err := r.Validate(); !nerr.HasCode(err, nerr.InvalidArgument) {
			t.Errorf("Validate(%+v): want invalid_argument, got %v", r, err)
		}
	}
}

func TestValidateSetReadConsistencyRequest(t *testing.T) {
	for _, r := range []SetReadConsistencyRequest{
		{Mode: ReadStrong},
		{Mode: ReadStale},
		{Mode: ReadBounded, MaxStalenessMS: 0},
		{Mode: ReadBounded, MaxStalenessMS: 5000},
		{Mode: ReadBounded, MaxStalenessMS: MaxReadStalenessMS},
	} {
		if err := r.Validate(); err != nil {
			t.Errorf("Validate(%+v): unexpected %v", r, err)
		}
	}
	for _, r := range []SetReadConsistencyRequest{
		{Mode: ""},
		{Mode: "eventual"},
		{Mode: ReadBounded, MaxStalenessMS: -1},
		{Mode: ReadBounded, MaxStalenessMS: MaxReadStalenessMS + 1},
	} {
		if err := r.Validate(); !nerr.HasCode(err, nerr.InvalidArgument) {
			t.Errorf("Validate(%+v): want invalid_argument, got %v", r, err)
		}
	}
}

func TestCollectTypedRowsAndNull(t *testing.T) {
	rows := &fakeRows{
		cols:  []string{"id", "name"},
		types: []types.Type{types.Int64(), types.String()},
		rows: [][]types.Value{
			{types.Int64Value(7), types.StringValue("seven")},
			{types.Int64Value(8), types.Null(types.String())},
		},
		affected: 2,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	got, err := Collect(ctx, rows, cancel)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if !rows.closed {
		t.Fatal("row stream was not closed")
	}
	if got.Truncated || got.Affected != 2 || len(got.Rows) != 2 {
		t.Fatalf("unexpected result: %+v", got)
	}
	if len(got.ColumnTypes) != 2 || got.ColumnTypes[0] != "INT64" || got.ColumnTypes[1] != "STRING" {
		t.Fatalf("column types = %v", got.ColumnTypes)
	}
	if got.Rows[1][1] != nil {
		t.Fatalf("NULL cell = %v, want nil", got.Rows[1][1])
	}
}

func TestCollectCancelsAtRowLimit(t *testing.T) {
	vals := make([][]types.Value, MaxResultRows+1)
	for i := range vals {
		vals[i] = []types.Value{types.Int64Value(int64(i))}
	}
	rows := &fakeRows{cols: []string{"n"}, types: []types.Type{types.Int64()}, rows: vals}
	ctx, cancel := context.WithCancel(context.Background())
	got, err := Collect(ctx, rows, cancel)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if !got.Truncated || len(got.Rows) != MaxResultRows {
		t.Fatalf("truncated=%v rows=%d", got.Truncated, len(got.Rows))
	}
	if !errors.Is(ctx.Err(), context.Canceled) || !rows.closed {
		t.Fatalf("cancel/close not propagated: ctx=%v closed=%v", ctx.Err(), rows.closed)
	}
}

func TestCollectCancelsBeforeOversizedCellIsRetained(t *testing.T) {
	rows := &fakeRows{
		cols:  []string{"payload"},
		types: []types.Type{types.String()},
		rows:  [][]types.Value{{types.StringValue(strings.Repeat("x", MaxResultBytes+1))}},
	}
	ctx, cancel := context.WithCancel(context.Background())
	got, err := Collect(ctx, rows, cancel)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if !got.Truncated || len(got.Rows) != 0 {
		t.Fatalf("oversized result retained: truncated=%v rows=%d", got.Truncated, len(got.Rows))
	}
	if !errors.Is(ctx.Err(), context.Canceled) || !rows.closed {
		t.Fatalf("cancel/close not propagated: ctx=%v closed=%v", ctx.Err(), rows.closed)
	}
}

func TestStreamEmitsBoundedTypedBatches(t *testing.T) {
	vals := make([][]types.Value, StreamBatchRows+1)
	for i := range vals {
		vals[i] = []types.Value{types.Int64Value(int64(i)), types.StringValue(fmt.Sprintf("row-%d", i))}
	}
	vals[1][1] = types.Null(types.String())
	rows := &fakeRows{
		cols:     []string{"id", "name"},
		types:    []types.Type{types.Int64(), types.String()},
		rows:     vals,
		affected: int64(len(vals)),
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var frames []StreamFrame
	err := Stream(ctx, rows, cancel, func(frame StreamFrame) error {
		frames = append(frames, frame)
		return nil
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if !rows.closed {
		t.Fatal("row stream was not closed")
	}
	if len(frames) != 4 || frames[0].Type != "meta" || frames[1].Type != "rows" || frames[2].Type != "rows" || frames[3].Type != "complete" {
		t.Fatalf("frame sequence = %#v", frames)
	}
	if got := frames[0].ColumnTypes; len(got) != 2 || got[0] != "INT64" || got[1] != "STRING" {
		t.Fatalf("column types = %v", got)
	}
	if got := len(frames[1].Rows); got != StreamBatchRows {
		t.Fatalf("first batch rows = %d, want %d", got, StreamBatchRows)
	}
	if frames[1].Rows[1][1] != nil {
		t.Fatalf("NULL cell = %v, want nil", frames[1].Rows[1][1])
	}
	complete := frames[3]
	if complete.Truncated || complete.RowCount != len(vals) || complete.Affected != int64(len(vals)) {
		t.Fatalf("completion = %+v", complete)
	}
}

func TestStreamCancelsAtRowLimitAfterFlushingRetainedRows(t *testing.T) {
	vals := make([][]types.Value, MaxResultRows+1)
	for i := range vals {
		vals[i] = []types.Value{types.Int64Value(int64(i))}
	}
	rows := &fakeRows{cols: []string{"n"}, types: []types.Type{types.Int64()}, rows: vals}
	ctx, cancel := context.WithCancel(context.Background())
	retained := 0
	var complete StreamFrame
	err := Stream(ctx, rows, cancel, func(frame StreamFrame) error {
		if frame.Type == "rows" {
			retained += len(frame.Rows)
		}
		if frame.Type == "complete" {
			complete = frame
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if retained != MaxResultRows || !complete.Truncated || complete.RowCount != MaxResultRows {
		t.Fatalf("retained=%d completion=%+v", retained, complete)
	}
	if !errors.Is(ctx.Err(), context.Canceled) || !rows.closed {
		t.Fatalf("cancel/close not propagated: ctx=%v closed=%v", ctx.Err(), rows.closed)
	}
}

func TestStreamCancelsBeforeOversizedCellIsEmitted(t *testing.T) {
	rows := &fakeRows{
		cols:  []string{"payload"},
		types: []types.Type{types.String()},
		rows:  [][]types.Value{{types.StringValue(strings.Repeat("x", StreamRowBytes+1))}},
	}
	ctx, cancel := context.WithCancel(context.Background())
	rowFrames := 0
	var complete StreamFrame
	err := Stream(ctx, rows, cancel, func(frame StreamFrame) error {
		if frame.Type == "rows" {
			rowFrames++
		}
		if frame.Type == "complete" {
			complete = frame
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if rowFrames != 0 || !complete.Truncated || complete.RowCount != 0 {
		t.Fatalf("row frames=%d completion=%+v", rowFrames, complete)
	}
	if !errors.Is(ctx.Err(), context.Canceled) || !rows.closed {
		t.Fatalf("cancel/close not propagated: ctx=%v closed=%v", ctx.Err(), rows.closed)
	}
}

func TestStreamEmitterFailureCancelsAndCloses(t *testing.T) {
	rows := &fakeRows{
		cols:  []string{"n"},
		types: []types.Type{types.Int64()},
		rows:  [][]types.Value{{types.Int64Value(1)}},
	}
	ctx, cancel := context.WithCancel(context.Background())
	want := errors.New("client disconnected")
	err := Stream(ctx, rows, cancel, func(frame StreamFrame) error {
		if frame.Type == "rows" {
			return want
		}
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), want.Error()) {
		t.Fatalf("Stream error = %v, want emitter failure", err)
	}
	if !errors.Is(ctx.Err(), context.Canceled) || !rows.closed {
		t.Fatalf("cancel/close not propagated: ctx=%v closed=%v", ctx.Err(), rows.closed)
	}
}

func TestStreamRejectsInconsistentRowWidth(t *testing.T) {
	rows := &fakeRows{
		cols:  []string{"a", "b"},
		types: []types.Type{types.Int64(), types.Int64()},
		rows:  [][]types.Value{{types.Int64Value(1)}},
	}
	ctx, cancel := context.WithCancel(context.Background())
	err := Stream(ctx, rows, cancel, func(StreamFrame) error { return nil })
	if !nerr.HasCode(err, nerr.InvalidFormat) {
		t.Fatalf("Stream error = %v, want invalid_format", err)
	}
	if !errors.Is(ctx.Err(), context.Canceled) || !rows.closed {
		t.Fatalf("cancel/close not propagated: ctx=%v closed=%v", ctx.Err(), rows.closed)
	}
}

func TestAnalyzeFlagsUpdateDeleteWithoutWhere(t *testing.T) {
	for _, tc := range []struct {
		sql  string
		kind string
	}{
		{"UPDATE accounts SET balance = 0", "UPDATE"},
		{"DELETE FROM accounts", "DELETE"},
	} {
		a, err := Analyze(tc.sql)
		if err != nil {
			t.Fatalf("Analyze(%q): %v", tc.sql, err)
		}
		if a.Kind != tc.kind || !a.Destructive || len(a.Reasons) == 0 {
			t.Fatalf("Analyze(%q) = %+v, want destructive %s with a reason", tc.sql, a, tc.kind)
		}
		if !strings.Contains(a.Reasons[0], "accounts") {
			t.Fatalf("Analyze(%q) reason %q does not name the table", tc.sql, a.Reasons[0])
		}
	}
}

func TestAnalyzeAllowsUpdateDeleteWithWhere(t *testing.T) {
	for _, sql := range []string{
		"UPDATE accounts SET balance = 0 WHERE id = 1",
		"DELETE FROM accounts WHERE id = 1",
	} {
		a, err := Analyze(sql)
		if err != nil {
			t.Fatalf("Analyze(%q): %v", sql, err)
		}
		if a.Destructive || len(a.Reasons) != 0 {
			t.Fatalf("Analyze(%q) = %+v, want not destructive", sql, a)
		}
	}
}

func TestAnalyzeFlagsDestructiveDDL(t *testing.T) {
	for _, tc := range []struct {
		sql, kind, name string
	}{
		{"DROP TABLE accounts", "DROP TABLE", "accounts"},
		{"DROP INDEX idx_accounts_name", "DROP INDEX", "idx_accounts_name"},
		{"DROP USER alice", "DROP USER", "alice"},
		{"DROP ROLE analyst", "DROP ROLE", "analyst"},
		{"DROP WORKFLOW nightly", "DROP WORKFLOW", "nightly"},
		{"DROP TRIGGER audit_trg", "DROP TRIGGER", "audit_trg"},
		{"DROP SCHEDULE nightly_run", "DROP SCHEDULE", "nightly_run"},
		{"DROP RESOURCE GROUP batch", "DROP RESOURCE GROUP", "batch"},
		{"ALTER TABLE accounts DROP COLUMN notes", "ALTER TABLE", "notes"},
	} {
		a, err := Analyze(tc.sql)
		if err != nil {
			t.Fatalf("Analyze(%q): %v", tc.sql, err)
		}
		if a.Kind != tc.kind || !a.Destructive || len(a.Reasons) == 0 {
			t.Fatalf("Analyze(%q) = %+v, want destructive %s with a reason", tc.sql, a, tc.kind)
		}
		if !strings.Contains(a.Reasons[0], tc.name) {
			t.Fatalf("Analyze(%q) reason %q does not name %q", tc.sql, a.Reasons[0], tc.name)
		}
	}
}

func TestAnalyzeAllowsSafeStatements(t *testing.T) {
	for _, sql := range []string{
		"SELECT * FROM accounts",
		"INSERT INTO accounts (id) VALUES (1)",
		"CREATE TABLE t (id INT64 PRIMARY KEY)",
		"ALTER TABLE accounts ADD COLUMN notes TEXT",
		"ALTER TABLE accounts RENAME COLUMN notes TO note",
	} {
		a, err := Analyze(sql)
		if err != nil {
			t.Fatalf("Analyze(%q): %v", sql, err)
		}
		if a.Destructive || len(a.Reasons) != 0 {
			t.Fatalf("Analyze(%q) = %+v, want not destructive", sql, a)
		}
	}
}

func TestAnalyzeClassifiesWrites(t *testing.T) {
	writes := []string{
		"INSERT INTO accounts (id) VALUES (1)",
		"UPDATE accounts SET name = 'x' WHERE id = 1",
		"DELETE FROM accounts WHERE id = 1",
		"UPSERT INTO accounts (id) VALUES (1)",
		"CREATE TABLE t (id INT64 PRIMARY KEY)",
		"DROP TABLE accounts",
		"CREATE INDEX ix ON accounts (name)",
		"ALTER TABLE accounts ADD COLUMN notes TEXT",
		"CREATE WORKFLOW w(n INT64) AS BEGIN UPDATE accounts SET name = 'x' WHERE id = $n; END",
		"RUN WORKFLOW w(1)",
		"GRANT SELECT ON TABLE accounts TO alice",
		"REVOKE SELECT ON TABLE accounts FROM alice",
		"CREATE USER alice IDENTIFIED BY 'pw'",
		"BEGIN",
		"COMMIT",
		"ROLLBACK",
		"BACKUP DATABASE",
		"SET CONFIG max_connections = '100'",
	}
	for _, sql := range writes {
		a, err := Analyze(sql)
		if err != nil {
			t.Fatalf("Analyze(%q): %v", sql, err)
		}
		if !a.Write {
			t.Fatalf("Analyze(%q).Write = false, want true", sql)
		}
	}

	reads := []string{
		"SELECT * FROM accounts",
		"SELECT 1",
		"EXPLAIN SELECT * FROM accounts",
		"ANALYZE accounts",
		"SHOW TASKS",
		"VERIFY BACKUP 'nightly'",
	}
	for _, sql := range reads {
		a, err := Analyze(sql)
		if err != nil {
			t.Fatalf("Analyze(%q): %v", sql, err)
		}
		if a.Write {
			t.Fatalf("Analyze(%q).Write = true, want false", sql)
		}
	}
}

func TestAnalyzeFlagsRealmScopedPrincipalStatements(t *testing.T) {
	realmScoped := []string{
		"CREATE USER alice IDENTIFIED BY 'pw'",
		"DROP USER alice",
		"CREATE ROLE analysts",
		"DROP ROLE analysts",
	}
	for _, sql := range realmScoped {
		a, err := Analyze(sql)
		if err != nil {
			t.Fatalf("Analyze(%q): %v", sql, err)
		}
		if !a.RealmScoped {
			t.Fatalf("Analyze(%q).RealmScoped = false, want true", sql)
		}
	}

	notRealmScoped := []string{
		"GRANT SELECT ON TABLE accounts TO alice",
		"REVOKE SELECT ON TABLE accounts FROM alice",
		"CREATE TABLE t (id INT64 PRIMARY KEY)",
		"DROP TABLE accounts",
		"SELECT * FROM accounts",
	}
	for _, sql := range notRealmScoped {
		a, err := Analyze(sql)
		if err != nil {
			t.Fatalf("Analyze(%q): %v", sql, err)
		}
		if a.RealmScoped {
			t.Fatalf("Analyze(%q).RealmScoped = true, want false", sql)
		}
	}
}

func TestAnalyzeRejectsEmptyOrOversizedSQL(t *testing.T) {
	if _, err := Analyze("   "); !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("Analyze(empty) error = %v, want invalid_argument", err)
	}
	if _, err := Analyze(strings.Repeat("a", MaxSQLBytes+1)); !nerr.HasCode(err, nerr.Exhausted) {
		t.Fatalf("Analyze(oversized) error = %v, want exhausted", err)
	}
}

func TestAnalyzeReportsSyntaxErrors(t *testing.T) {
	if _, err := Analyze("SELECT FROM WHERE"); !nerr.HasCode(err, nerr.Syntax) {
		t.Fatalf("Analyze(invalid) error = %v, want syntax", err)
	}
}

func TestSplitScriptOneAndMany(t *testing.T) {
	stmts, err := SplitScript("CREATE TABLE t (id UUID);")
	if err != nil || len(stmts) != 1 || !strings.Contains(stmts[0], "CREATE TABLE") {
		t.Fatalf("SplitScript(one) = %v, %v", stmts, err)
	}
	stmts, err = SplitScript(`
CREATE TABLE customers (id UUID PRIMARY KEY);
CREATE INDEX ix_customers_id ON customers (id);
`)
	if err != nil || len(stmts) != 2 {
		t.Fatalf("SplitScript(many) = %v, %v", stmts, err)
	}
}

func TestSplitScriptIgnoresSemiInStringIdentComment(t *testing.T) {
	src := `
INSERT INTO t (name) VALUES ('a;b'); -- also ; here
INSERT INTO t (name) VALUES ("x;y");
/* block ; comment */
ANALYZE
`
	stmts, err := SplitScript(src)
	if err != nil {
		t.Fatal(err)
	}
	if len(stmts) != 3 {
		t.Fatalf("%d %#v", len(stmts), stmts)
	}
	if !strings.Contains(stmts[0], "'a;b'") || !strings.Contains(stmts[1], `"x;y"`) {
		t.Fatalf("%#v", stmts)
	}
}

func TestSplitScriptEscapedQuotes(t *testing.T) {
	src := `INSERT INTO t (s) VALUES ('it''s;fine'); INSERT INTO t (s) VALUES ("a""b;c");`
	stmts, err := SplitScript(src)
	if err != nil || len(stmts) != 2 {
		t.Fatalf("%v %v", stmts, err)
	}
}

func TestSplitScriptRejectsCommentOnlyOrEmpty(t *testing.T) {
	if _, err := SplitScript("-- only comments\n/* and a block */\n;\n"); !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("SplitScript(blank) error = %v, want invalid_argument", err)
	}
	if _, err := SplitScript("   "); !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("SplitScript(empty) error = %v, want invalid_argument", err)
	}
}

func TestSplitScriptUnterminated(t *testing.T) {
	if _, err := SplitScript("INSERT INTO t (s) VALUES ('oops"); !nerr.HasCode(err, nerr.Syntax) {
		t.Fatal("expected syntax error for unterminated string")
	}
	if _, err := SplitScript("ANALYZE /*"); !nerr.HasCode(err, nerr.Syntax) {
		t.Fatal("expected syntax error for unterminated comment")
	}
}

func TestSplitScriptRejectsOversizedSQL(t *testing.T) {
	if _, err := SplitScript(strings.Repeat("a", MaxSQLBytes+1)); !nerr.HasCode(err, nerr.Exhausted) {
		t.Fatalf("SplitScript(oversized) error = %v, want exhausted", err)
	}
}

func TestSplitScriptRejectsTooManyStatements(t *testing.T) {
	var b strings.Builder
	for i := 0; i <= MaxScriptStatements; i++ {
		b.WriteString("ANALYZE;")
	}
	if _, err := SplitScript(b.String()); !nerr.HasCode(err, nerr.Exhausted) {
		t.Fatalf("SplitScript(too many) error = %v, want exhausted", err)
	}
}
