package catalog

import (
	"strings"

	"github.com/bzync/nextsql/internal/sql/lexer"
)

// HistoryTable is the reserved schema-history table (design C.2).
const HistoryTable = "nsql_schema_migrations"

// ReservedPrefix is rejected on CREATE TABLE except the exact history DDL.
const ReservedPrefix = "nsql_"

// HistoryDDL is the only legal CREATE TABLE for HistoryTable when it is absent.
const HistoryDDL = `CREATE TABLE nsql_schema_migrations (
    version      STRING PRIMARY KEY,
    name         STRING NOT NULL,
    applied_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    checksum     STRING NOT NULL,
    execution_ms DECIMAL(12,0) NOT NULL,
    dirty        DECIMAL(1,0) NOT NULL,
    direction    STRING NOT NULL
)`

// ReservedName reports whether name uses the reserved nsql_ prefix (case-folded).
func ReservedName(name string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(name)), ReservedPrefix)
}

// IsHistoryTable reports whether name is the reserved history table (case-folded).
func IsHistoryTable(name string) bool {
	return strings.EqualFold(strings.TrimSpace(name), HistoryTable)
}

// NormalizeSQL collapses space/newline runs, folds unquoted idents, and drops
// comments and trailing semicolons. Used to match HistoryDDL.
func NormalizeSQL(src string) string {
	lx := lexer.New(src)
	var parts []string
	for {
		tok := lx.Next()
		if err := lx.Err(); err != nil {
			return collapseWS(src)
		}
		if tok.Kind == lexer.EOF {
			break
		}
		if tok.Kind == lexer.Semi {
			continue
		}
		if tok.Lit != "" {
			parts = append(parts, tok.Lit)
		}
	}
	return strings.Join(parts, " ")
}

// MatchHistoryDDL reports whether sql is the C.2 history DDL after NormalizeSQL.
func MatchHistoryDDL(sql string) bool {
	return NormalizeSQL(sql) == NormalizeSQL(HistoryDDL)
}

func collapseWS(src string) string {
	var b strings.Builder
	prevSpace := false
	for _, r := range src {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			if !prevSpace && b.Len() > 0 {
				b.WriteByte(' ')
				prevSpace = true
			}
			continue
		}
		if r == ';' {
			continue
		}
		b.WriteRune(r)
		prevSpace = false
	}
	return strings.TrimSpace(b.String())
}
