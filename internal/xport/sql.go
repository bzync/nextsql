package xport

import (
	"strconv"
	"strings"

	"github.com/bzync/nextsql/internal/catalog"
	"github.com/bzync/nextsql/internal/catalog/ddl"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/sql/types"
)

// The canonical DDL renderers (CREATE TABLE / CREATE INDEX / identifier
// quoting / literal rendering) live in internal/catalog/ddl so packages that
// cannot import xport can reuse them. xport keeps only the parameterized
// INSERT template it uses for row export, plus thin aliases for the
// renderers its other files and tests already call by short name.

func quoteIdent(s string) string { return ddl.QuoteIdent(s) }

func sqlType(t types.Type) string { return ddl.SQLType(t) }

func createTableSQL(t *catalog.Table) (string, error) { return ddl.CreateTableSQL(t) }

func createTableSQLWithParents(t *catalog.Table, parents map[string]*catalog.Table) (string, error) {
	return ddl.CreateTableSQLWithParents(t, parents)
}

func createIndexSQL(t *catalog.Table, idx catalog.Index) (string, error) {
	return ddl.CreateIndexSQL(t, idx)
}

func insertSQL(t *catalog.Table) (string, error) {
	if t == nil || t.Name == "" || len(t.Columns) == 0 {
		return "", nerr.New(nerr.InvalidFormat, "xport.insertSQL", "invalid table")
	}
	var b strings.Builder
	b.WriteString("INSERT INTO ")
	b.WriteString(quoteIdent(t.Name))
	b.WriteString(" (")
	for i, c := range t.Columns {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(quoteIdent(c.Name))
	}
	b.WriteString(") VALUES (")
	for i := range t.Columns {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteByte('$')
		b.WriteString(strconv.Itoa(i + 1))
	}
	b.WriteByte(')')
	return b.String(), nil
}
