package executor

import (
	"strconv"

	"github.com/bzync/nextsql/internal/catalog"
	"github.com/bzync/nextsql/internal/catalog/ddl"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/sql/ast"
	"github.com/bzync/nextsql/internal/sql/binder"
	"github.com/bzync/nextsql/internal/sql/parser"
	"github.com/bzync/nextsql/internal/sql/planner"
	"github.com/bzync/nextsql/internal/sql/types"
)

// execCreateTableAs runs `CREATE TABLE name PRIMARY KEY (cols) AS <query>`.
//
// The statement is a snapshot, not a view: it reads the source once and stores
// the rows. What it adds over `CREATE TABLE` plus `INSERT ... <query>` is that
// the column names and types are not written out. The names come from the
// source's output (checked in the binder); the types come from the values the
// source actually produced, because a NextSQL query has no static output type
// -- a result column's type travels with its values, and there is no
// expression typer that could predict `a + b` or `UPPER(s)` in advance.
//
// Deriving from values is what makes the empty and all-NULL cases refusals
// rather than guesses, and both are refused by name: there is no type to
// derive, and inventing one would silently create a table with a column shape
// the operator never asked for.
//
// The source is read to completion before the table exists, so it cannot read
// its own output -- the self-reference hazard execInsertQuery documents does
// not arise here at all. Running it through execPlan is also what bounds it:
// the query path caps a result by max_result_rows and max_result_bytes and
// charges it to the statement's memory budget, so an oversized source is the
// same explicit Exhausted rejection any query of that size would get.
func (s *Session) execCreateTableAs(p planner.CreateTableAs) (*Result, error) {
	if _, ok := s.lookup(p.Name); ok {
		return nil, nerr.New(nerr.AlreadyExists, "executor.CreateTableAs", "table already exists: "+p.Name)
	}
	res, err := s.execPlan(p.Input)
	if err != nil {
		return nil, err
	}
	if res == nil {
		return nil, nerr.New(nerr.Internal, "executor.CreateTableAs", "source produced no result")
	}
	if len(res.Columns) != len(p.Columns) {
		return nil, nerr.New(nerr.Internal, "executor.CreateTableAs", "source column count changed after binding")
	}
	defs, err := ctasColumns(p.Columns, p.PK, res.Rows)
	if err != nil {
		return nil, err
	}
	// Built through the ordinary catalog path, so every rule a written CREATE
	// TABLE obeys -- reserved names, duplicate columns, a PK that may not be a
	// VECTOR -- applies here unchanged instead of being restated. The identity
	// is reserved now rather than at plan time: the plan is cacheable and a
	// catalog identity may never be reused (see planner.CreateTableAs).
	stmt := ast.CreateTable{Name: p.Name, Columns: defs, PK: append([]string(nil), p.PK...)}
	tab, err := catalog.TableFromAST(s.db.Cat.NextID(), stmt)
	if err != nil {
		return nil, err
	}
	if err := checkExpressibleAsDDL(tab); err != nil {
		return nil, err
	}
	if _, err := s.execCreateTable(planner.CreateTable{Table: tab}); err != nil {
		return nil, err
	}
	// Re-read the table through the session so the rows are written against
	// the same overlay copy every other statement in this transaction sees.
	created, ok := s.lookup(p.Name)
	if !ok {
		return nil, nerr.New(nerr.Internal, "executor.CreateTableAs", "table missing after creation: "+p.Name)
	}
	heap, err := s.heapOf(created)
	if err != nil {
		return nil, err
	}
	htx := s.x.use(heap)
	// Every column of a derived table comes from the source, so each slot is
	// filled below. The prototype is still typed NULLs rather than zero
	// values: a zero types.Value carries no type and is what log #251 found
	// reaching output, so no row here is ever built from one.
	empty := make([]types.Value, len(created.Columns))
	for i := range empty {
		empty[i] = types.Null(created.Columns[i].Type)
	}
	var n int64
	for _, vals := range res.Rows {
		if len(vals) != len(defs) {
			return nil, nerr.New(nerr.Internal, "executor.CreateTableAs", "source row width changed after binding")
		}
		row := append([]types.Value(nil), empty...)
		for j := range vals {
			cv, err := types.Coerce(vals[j], created.Columns[j].Type)
			if err != nil {
				return nil, err
			}
			row[j] = cv
		}
		// The same choke point a plain INSERT ends at, so defaults, the NOT
		// NULL check and the write itself behave identically.
		// CREATE TABLE ... AS has no RETURNING, so the zero Returning and a
		// nil sink collect nothing.
		if err := s.finishInsertRow(created, htx, row, allNamed(len(row)), binder.Returning{}, nil); err != nil {
			return nil, err
		}
		n++
	}
	if err := s.maybeAutoAnalyze(created, n); err != nil {
		return nil, err
	}
	return &Result{Affected: n}, nil
}

// maxDeclarableDecimalPrecision is the largest DECIMAL precision the dialect
// accepts (types.DecimalType).
const maxDeclarableDecimalPrecision = 38

// ctasColumns derives the new table's column definitions from the source's
// output names and the values it produced.
//
// A column's type is the type its non-NULL values carry, and every non-NULL
// value in a column must carry the same type: a column holding two different
// types has no single type to declare, so it is refused rather than resolved
// by picking one or coercing to text.
//
// A NULL still carries a type -- a NULL read from a STRING column is a STRING
// NULL, and CAST(x AS t) produces a typed NULL of t -- so a column that is
// NULL in every row is usually still typeable, and is accepted. What cannot be
// typed is an *untyped* NULL, the bare NULL literal, whose kind is KindNull:
// there is nothing to derive and nothing to store. That is refused by name,
// and a CAST is the remedy because it supplies exactly the missing type.
//
// Nullability is deliberately not inferred from the data. Only the primary-key
// columns are NOT NULL -- TableFromAST marks those itself, since a key cannot
// be NULL. Declaring the others NOT NULL because this particular result
// happened to contain no NULL would let incidental data decide what the new
// table accepts from then on.
func ctasColumns(names, pk []string, rows [][]types.Value) ([]ast.ColumnDef, error) {
	if len(rows) == 0 {
		return nil, nerr.New(nerr.InvalidArgument, "executor.CreateTableAs",
			"the query returned no rows, so no column type can be derived; write CREATE TABLE and INSERT instead")
	}
	inPK := make(map[string]struct{}, len(pk))
	for _, k := range pk {
		inPK[k] = struct{}{}
	}
	defs := make([]ast.ColumnDef, len(names))
	for j, name := range names {
		var (
			typ      types.Type
			have     bool
			nullTyp  types.Type
			haveNull bool
			sawNull  bool
		)
		for _, r := range rows {
			if j >= len(r) {
				return nil, nerr.New(nerr.Internal, "executor.CreateTableAs", "short source row")
			}
			v := r[j]
			if v.Null {
				sawNull = true
				// Remember the first type a NULL carries, as the fallback for
				// a column that never holds a non-NULL value.
				if !haveNull && typeDerivable(v.Typ) {
					nullTyp, haveNull = v.Typ, true
				}
				continue
			}
			if !have {
				typ, have = v.Typ, true
				continue
			}
			if !typ.Equals(v.Typ) {
				return nil, nerr.New(nerr.InvalidArgument, "executor.CreateTableAs",
					"column "+name+" holds more than one type ("+typ.String()+" and "+v.Typ.String()+"); cast the query's output to one type")
			}
		}
		switch {
		case have:
			// Derived from real values.
		case haveNull:
			typ = nullTyp
		default:
			return nil, nerr.New(nerr.InvalidArgument, "executor.CreateTableAs",
				"column "+name+" is an untyped NULL in every row, so its type cannot be derived; CAST it to the type the column should have")
		}
		// An exact-decimal value carries its precision and scale in the value
		// (types.Decimal) rather than in the type, so arithmetic and COUNT/SUM
		// produce a DECIMAL whose type says (0,0) -- which DECIMAL has no way
		// to spell, since precision is mandatory and must be at least 1. For
		// that case, and only that case, the scale is read from the values the
		// way every other type here is read from the values. Precision is
		// widened to the dialect maximum instead of the minimum that fits this
		// batch: the scale decides whether a stored value keeps its digits, so
		// it must be derived, while a tight precision would only mean the new
		// table rejected a larger value later.
		if typ.Kind == types.KindDecimal && typ.Precision == 0 {
			scale := maxDecimalScale(rows, j)
			if scale > maxDeclarableDecimalPrecision {
				return nil, nerr.New(nerr.InvalidArgument, "executor.CreateTableAs",
					"column "+name+" holds a decimal with more than "+strconv.Itoa(maxDeclarableDecimalPrecision)+
						" decimal places; CAST it to the type the column should have")
			}
			dt, err := types.DecimalType(maxDeclarableDecimalPrecision, uint16(scale))
			if err != nil {
				return nil, err
			}
			typ = dt
		}
		if _, key := inPK[name]; key && sawNull {
			return nil, nerr.New(nerr.InvalidArgument, "executor.CreateTableAs",
				"PRIMARY KEY column "+name+" is NULL in "+strconv.Itoa(countNull(rows, j))+" row(s)")
		}
		defs[j] = ast.ColumnDef{Name: name, Type: typ}
	}
	return defs, nil
}

// typeDerivable reports whether a type is concrete enough to declare a column
// with. KindNull is the untyped NULL literal and KindInvalid is the zero
// value; neither names a storable type. Every other kind is left to
// catalog.ColumnFromAST, which owns what a column may be declared as.
func typeDerivable(t types.Type) bool {
	return t.Kind != types.KindNull && t.Kind != types.KindInvalid
}

// maxDecimalScale is the largest scale any decimal value in column j carries,
// which is the scale the column needs to store every one of them exactly.
func maxDecimalScale(rows [][]types.Value, j int) int {
	max := 0
	for _, r := range rows {
		if j >= len(r) || r[j].Null || r[j].Typ.Kind != types.KindDecimal {
			continue
		}
		if sc := r[j].Dec.Scale; sc > max {
			max = sc
		}
	}
	return max
}

func countNull(rows [][]types.Value, j int) int {
	n := 0
	for _, r := range rows {
		if j < len(r) && r[j].Null {
			n++
		}
	}
	return n
}

// checkExpressibleAsDDL refuses a derived table whose own canonical DDL would
// not re-parse to the same shape.
//
// Every other CREATE TABLE starts from DDL an operator wrote, so its shape is
// declarable by construction. A derived one does not: a value's type is not
// always a type the dialect can spell. Integer arithmetic, for instance,
// produces an exact decimal whose precision and scale are carried per value
// rather than in the type, and `DECIMAL` has no bare form -- so a column
// derived straight from `qty * 2` would be DECIMAL(0,0), which the type
// checker rejects. Such a table can be created and queried but cannot be
// rendered as DDL that works, which means it cannot be logically exported and
// re-imported. That is a durability property, so it is refused up front.
//
// The check is the round trip itself rather than a list of type kinds, so a
// type added later cannot quietly reintroduce the problem: render the table's
// canonical DDL with the same renderer system.table_ddl and logical export
// use, parse it back, and require every column type to match.
func checkExpressibleAsDDL(tab *catalog.Table) error {
	sql, err := ddl.CreateTableSQL(tab)
	if err != nil {
		return nerr.New(nerr.InvalidArgument, "executor.CreateTableAs",
			"the derived table cannot be expressed as DDL: "+err.Error())
	}
	parsed, err := parser.Parse(sql)
	if err != nil {
		return undeclarableColumn(tab, err)
	}
	ct, ok := parsed.(ast.CreateTable)
	if !ok || len(ct.Columns) != len(tab.Columns) {
		return nerr.New(nerr.Internal, "executor.CreateTableAs", "canonical DDL did not round-trip")
	}
	for i, c := range ct.Columns {
		if !c.Type.Equals(tab.Columns[i].Type) {
			return nerr.New(nerr.InvalidArgument, "executor.CreateTableAs",
				"column "+tab.Columns[i].Name+" has the derived type "+tab.Columns[i].Type.String()+
					", which does not round-trip through DDL; CAST it to the type the column should have")
		}
	}
	return nil
}

// undeclarableColumn turns a failed re-parse into an error that names the
// column responsible, by re-parsing each column's type on its own. The
// statement is already failing, so the extra parses cost nothing that matters.
func undeclarableColumn(tab *catalog.Table, cause error) error {
	for _, c := range tab.Columns {
		probe := "CREATE TABLE probe (" + ddl.QuoteIdent(c.Name) + " " + ddl.SQLType(c.Type) + ")"
		if _, err := parser.Parse(probe); err != nil {
			return nerr.New(nerr.InvalidArgument, "executor.CreateTableAs",
				"column "+c.Name+" has no declarable type (derived "+ddl.SQLType(c.Type)+
					"); CAST it to the type the column should have")
		}
	}
	return nerr.New(nerr.InvalidArgument, "executor.CreateTableAs",
		"the derived table cannot be expressed as DDL: "+cause.Error())
}

// allNamed marks every column as supplied by the statement.
func allNamed(n int) []bool {
	named := make([]bool, n)
	for i := range named {
		named[i] = true
	}
	return named
}
