package binder

import (
	"strconv"

	"github.com/bzync/nextsql/internal/catalog"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/sql/ast"
)

// placeholderOutName is the output name the select-list binder gives an
// expression that was not aliased. It is not a legal column name, so a CTAS
// that would produce one is refused and the operator asked for an alias.
const placeholderOutName = "?"

// bindCreateTableAs binds `CREATE TABLE name PRIMARY KEY (cols) AS <query>`.
//
// The query is bound by the ordinary query binder, so the source is the same
// relation it would be on its own -- joins, aggregates, set operations, CTEs
// and views all behave identically, and its tables are authorized as reads in
// the usual way.
//
// What cannot be settled here is the new table's column types. A NextSQL query
// has no static output type: a result column's type is carried by its values
// (types.Value.Typ), and there is no expression typer that could predict
// `a + b` or `UPPER(s)` without evaluating it. So the schema is derived in the
// executor from the rows the source actually produced, and this function's job
// is everything that can be decided before paying for the query: the name is
// free, the output columns can legally become columns, and the named key is
// among them.
func bindCreateTableAs(s ast.CreateTable, lookup Lookup, nextID uint32, ctes map[string]*CTE) (Bound, error) {
	if catalog.ReservedName(s.Name) {
		return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "table name prefix nsql_ is reserved")
	}
	if _, ok := lookup(s.Name); ok {
		return nil, nerr.New(nerr.AlreadyExists, "sql.binder", "table already exists: "+s.Name)
	}
	// A FROM-less SELECT is served by its own restricted evaluator outside the
	// query pipeline, so it cannot be bound as a source relation. Refuse by
	// name rather than through a confusing "unknown table"; a table of
	// constants is written with CREATE TABLE and INSERT ... VALUES.
	if sel, ok := s.Query.(ast.Select); ok && sel.NoFrom {
		return nil, nerr.New(nerr.InvalidArgument, "sql.binder",
			"CREATE TABLE ... AS requires a query that reads a relation; write CREATE TABLE and INSERT instead")
	}
	q, err := bind(s.Query, lookup, nextID, ctes)
	if err != nil {
		return nil, err
	}
	names, ok := boundNames(q)
	if !ok {
		return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "CREATE TABLE ... AS source must be a query")
	}
	if len(names) == 0 {
		return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "CREATE TABLE ... AS source has no output columns")
	}
	// Every output column becomes a real column, so each needs a name a column
	// can have. An unaliased expression has none -- it reports as "?" -- and a
	// repeated name would declare the same column twice. Both are refused here,
	// naming the column, because the fix is for the operator to write the name
	// the new table's column should have.
	seen := make(map[string]struct{}, len(names))
	for i, n := range names {
		if n == placeholderOutName {
			return nil, nerr.New(nerr.InvalidArgument, "sql.binder",
				"CREATE TABLE ... AS output column "+strconv.Itoa(i+1)+" is an unnamed expression; give it an alias")
		}
		if _, dup := seen[n]; dup {
			return nil, nerr.New(nerr.InvalidArgument, "sql.binder",
				"CREATE TABLE ... AS output column name is repeated: "+n)
		}
		seen[n] = struct{}{}
	}
	if len(s.PK) == 0 {
		return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "CREATE TABLE ... AS requires PRIMARY KEY")
	}
	inPK := make(map[string]struct{}, len(s.PK))
	for _, k := range s.PK {
		if _, dup := inPK[k]; dup {
			return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "PRIMARY KEY column is repeated: "+k)
		}
		inPK[k] = struct{}{}
		if _, ok := seen[k]; !ok {
			return nil, nerr.New(nerr.InvalidArgument, "sql.binder",
				"PRIMARY KEY column is not an output column of the query: "+k)
		}
	}
	return CreateTableAs{Name: s.Name, PK: s.PK, Columns: names, Query: q}, nil
}
