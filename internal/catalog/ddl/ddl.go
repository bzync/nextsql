// Package ddl renders catalog objects back to canonical NextSQL DDL.
//
// It was extracted from internal/xport so consumers that cannot depend on
// xport (which imports internal/executor) can still reconstruct a table's
// CREATE statement — notably internal/executor's system.table_ddl view. The
// rendering is the single source of truth for "what SQL recreates this
// object": FK ordinals, expression/JSON-path indexes, vector method and
// quantization, full-text analyzer, ENUM/CHAR/VARCHAR types, nested
// STRUCT/ARRAY/MAP literal defaults. It depends only on leaf packages
// (catalog, fulltext, json, types, nerr).
package ddl

import (
	"encoding/hex"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/bzync/nextsql/internal/catalog"
	"github.com/bzync/nextsql/internal/fulltext"
	nsjson "github.com/bzync/nextsql/internal/json"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/sql/types"
)

// QuoteIdent wraps an identifier in double quotes, doubling any embedded
// quote — the canonical NextSQL identifier quoting.
func QuoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

// SQLType renders a column's logical type as it appears in DDL.
func SQLType(t types.Type) string {
	switch t.Kind {
	case types.KindDecimal:
		return fmt.Sprintf("DECIMAL(%d,%d)", t.Precision, t.Scale)
	default:
		return t.String()
	}
}

// CreateTableSQL renders CREATE TABLE for t with no cross-table context, so
// a foreign key referencing another table fails closed.
func CreateTableSQL(t *catalog.Table) (string, error) {
	return CreateTableSQLWithParents(t, nil)
}

// CreateTableSQLWithParents renders CREATE TABLE for t. parents supplies the
// referenced tables so a foreign key's referenced column names can be
// resolved; a self-referencing key needs no entry.
func CreateTableSQLWithParents(t *catalog.Table, parents map[string]*catalog.Table) (string, error) {
	if t == nil || t.Name == "" || len(t.Columns) == 0 {
		return "", nerr.New(nerr.InvalidFormat, "catalog.ddl.CreateTableSQL", "invalid table")
	}
	var b strings.Builder
	b.WriteString("CREATE TABLE ")
	b.WriteString(QuoteIdent(t.Name))
	b.WriteString(" (")
	for i, c := range t.Columns {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(QuoteIdent(c.Name))
		b.WriteByte(' ')
		b.WriteString(SQLType(c.LogicalType()))
		if c.ClientEncrypted() {
			b.WriteString(" ENCRYPTED CLIENT")
		}
		singlePK := len(t.PK) == 1 && t.PK[0] == i
		if singlePK {
			b.WriteString(" PRIMARY KEY")
		} else if c.NotNull {
			b.WriteString(" NOT NULL")
		}
		switch c.Default.Kind {
		case catalog.DefUUID:
			b.WriteString(" DEFAULT UUID()")
		case catalog.DefNow:
			b.WriteString(" DEFAULT NOW()")
		case catalog.DefAI:
			b.WriteString(" DEFAULT AI()")
		case catalog.DefLiteral:
			lit, err := SQLLiteral(c.Default.Literal)
			if err != nil {
				return "", err
			}
			b.WriteString(" DEFAULT ")
			b.WriteString(lit)
		}
	}
	if len(t.PK) > 1 {
		b.WriteString(", PRIMARY KEY (")
		for i, ord := range t.PK {
			if i > 0 {
				b.WriteString(", ")
			}
			if ord < 0 || ord >= len(t.Columns) {
				return "", nerr.New(nerr.InvalidFormat, "catalog.ddl.CreateTableSQL", "invalid primary key ordinal")
			}
			b.WriteString(QuoteIdent(t.Columns[ord].Name))
		}
		b.WriteByte(')')
	}
	for _, fk := range t.ForeignKeys {
		clause, err := foreignKeySQL(t, fk, parents)
		if err != nil {
			return "", err
		}
		b.WriteString(", ")
		b.WriteString(clause)
	}
	b.WriteByte(')')
	return b.String(), nil
}

func foreignKeySQL(child *catalog.Table, fk catalog.ForeignKey, parents map[string]*catalog.Table) (string, error) {
	parent := child
	if fk.RefTable != "" && fk.RefTable != child.Name {
		if parents != nil {
			parent = parents[fk.RefTable]
		} else {
			parent = nil
		}
	}
	if parent == nil {
		return "", nerr.New(nerr.InvalidFormat, "catalog.ddl.CreateTableSQL", "referenced table missing")
	}
	var b strings.Builder
	if fk.Name != "" {
		b.WriteString("CONSTRAINT ")
		b.WriteString(QuoteIdent(fk.Name))
		b.WriteByte(' ')
	}
	b.WriteString("FOREIGN KEY (")
	for i, ord := range fk.Columns {
		if i > 0 {
			b.WriteString(", ")
		}
		if ord < 0 || ord >= len(child.Columns) {
			return "", nerr.New(nerr.InvalidFormat, "catalog.ddl.CreateTableSQL", "invalid foreign key ordinal")
		}
		b.WriteString(QuoteIdent(child.Columns[ord].Name))
	}
	b.WriteString(") REFERENCES ")
	b.WriteString(QuoteIdent(fk.RefTable))
	b.WriteString(" (")
	for i, ord := range fk.RefColumns {
		if i > 0 {
			b.WriteString(", ")
		}
		if ord < 0 || ord >= len(parent.Columns) {
			return "", nerr.New(nerr.InvalidFormat, "catalog.ddl.CreateTableSQL", "invalid referenced column ordinal")
		}
		b.WriteString(QuoteIdent(parent.Columns[ord].Name))
	}
	b.WriteByte(')')
	del, err := FKActionSQL(fk.OnDelete)
	if err != nil {
		return "", err
	}
	up, err := FKActionSQL(fk.OnUpdate)
	if err != nil {
		return "", err
	}
	b.WriteString(" ON DELETE ")
	b.WriteString(del)
	b.WriteString(" ON UPDATE ")
	b.WriteString(up)
	return b.String(), nil
}

// FKActionSQL renders a foreign-key referential action.
func FKActionSQL(a catalog.FKAction) (string, error) {
	switch a {
	case catalog.FKRestrict:
		return "RESTRICT", nil
	case catalog.FKCascade:
		return "CASCADE", nil
	case catalog.FKSetNull:
		return "SET NULL", nil
	case catalog.FKSetDefault:
		return "SET DEFAULT", nil
	default:
		return "", nerr.New(nerr.InvalidFormat, "catalog.ddl.CreateTableSQL", "unknown foreign key action")
	}
}

// CreateIndexSQL renders CREATE [kind] INDEX for idx on t, including
// expression keys, JSON paths, INCLUDE columns, a partial predicate, and
// full-text / vector method options.
func CreateIndexSQL(t *catalog.Table, idx catalog.Index) (string, error) {
	if t == nil || idx.Name == "" || len(idx.Columns) == 0 {
		return "", nerr.New(nerr.InvalidFormat, "catalog.ddl.CreateIndexSQL", "invalid index")
	}
	var b strings.Builder
	switch {
	case idx.Vector:
		b.WriteString("CREATE VECTOR INDEX ")
	case idx.Fulltext:
		b.WriteString("CREATE FULLTEXT INDEX ")
	case idx.Spatial:
		b.WriteString("CREATE SPATIAL INDEX ")
	case idx.Unique:
		b.WriteString("CREATE UNIQUE INDEX ")
	default:
		b.WriteString("CREATE INDEX ")
	}
	b.WriteString(QuoteIdent(idx.Name))
	b.WriteString(" ON ")
	b.WriteString(QuoteIdent(t.Name))
	b.WriteString(" (")
	for i, ord := range idx.Columns {
		if i > 0 {
			b.WriteString(", ")
		}
		if idx.KeyIsExpr(i) {
			b.WriteByte('(')
			b.WriteString(catalog.FormatExpr(idx.Exprs[i]))
			b.WriteByte(')')
			continue
		}
		if ord < 0 || ord >= len(t.Columns) {
			return "", nerr.New(nerr.InvalidFormat, "catalog.ddl.CreateIndexSQL", "invalid index column")
		}
		b.WriteString(QuoteIdent(t.Columns[ord].Name))
		if i == 0 && len(idx.Path) > 0 {
			for _, p := range idx.Path {
				b.WriteByte('.')
				b.WriteString(QuoteIdent(p))
			}
		}
	}
	b.WriteByte(')')
	if len(idx.Include) > 0 {
		b.WriteString(" INCLUDE (")
		for i, ord := range idx.Include {
			if i > 0 {
				b.WriteString(", ")
			}
			if ord < 0 || ord >= len(t.Columns) {
				return "", nerr.New(nerr.InvalidFormat, "catalog.ddl.CreateIndexSQL", "invalid INCLUDE column")
			}
			b.WriteString(QuoteIdent(t.Columns[ord].Name))
		}
		b.WriteByte(')')
	}
	if idx.Predicate != nil {
		b.WriteString(" WHERE ")
		b.WriteString(catalog.FormatExpr(idx.Predicate))
	}
	if idx.Fulltext {
		if name := (fulltext.Analyzer{ID: idx.FTAnalyzer, Version: idx.FTVersion}).Name(); name != "" && name != "simple" {
			fmt.Fprintf(&b, " WITH (ANALYZER = '%s')", name)
		}
	}
	if idx.Vector {
		if idx.VecMethod == catalog.VecMethodIVF {
			fmt.Fprintf(&b, " USING IVF WITH (LISTS = %d", idx.IVFLists)
			if idx.IVFProbes > 0 {
				fmt.Fprintf(&b, ", PROBES = %d", idx.IVFProbes)
			}
			b.WriteByte(')')
		} else if idx.VecMethod == catalog.VecMethodIVFPQ {
			fmt.Fprintf(&b, " USING IVFPQ WITH (LISTS = %d", idx.IVFLists)
			if idx.IVFProbes > 0 {
				fmt.Fprintf(&b, ", PROBES = %d", idx.IVFProbes)
			}
			fmt.Fprintf(&b, ", SUBSPACES = %d)", idx.IVFSubspaces)
		} else if idx.VecMethod == catalog.VecMethodSPARSE {
			b.WriteString(" USING SPARSE")
		} else {
			b.WriteString(" USING HNSW")
			switch idx.VecQuant {
			case types.VecF16:
				b.WriteString(" WITH (QUANTIZATION = 'F16')")
			case types.VecI8:
				b.WriteString(" WITH (QUANTIZATION = 'I8')")
			}
		}
	}
	return b.String(), nil
}

// SQLLiteral renders a value as a NextSQL literal, used for column DEFAULT
// clauses and (from xport) row data.
func SQLLiteral(v types.Value) (string, error) {
	if v.Null {
		return "NULL", nil
	}
	switch v.Typ.Kind {
	case types.KindUUID:
		return "'" + types.FormatUUID(v.UUID) + "'", nil
	case types.KindString, types.KindText, types.KindChar, types.KindVarchar, types.KindEnum:
		return "'" + strings.ReplaceAll(v.Str, "'", "''") + "'", nil
	case types.KindBlob:
		return "X'" + hex.EncodeToString([]byte(v.Str)) + "'", nil
	case types.KindDecimal:
		return v.Dec.String(), nil
	case types.KindInt8, types.KindInt16, types.KindInt32, types.KindInt64:
		return strconv.FormatInt(v.Int, 10), nil
	case types.KindUint8, types.KindUint16, types.KindUint32, types.KindUint64:
		return strconv.FormatUint(v.Uint, 10), nil
	case types.KindFloat32, types.KindFloat64:
		if math.IsNaN(v.Flt) || math.IsInf(v.Flt, 0) {
			return "'" + v.String() + "'", nil
		}
		return v.String(), nil
	case types.KindTimestampTZ, types.KindTimestamp, types.KindDate, types.KindTime, types.KindInterval:
		return "'" + v.String() + "'", nil
	case types.KindBool:
		if v.Bool {
			return "TRUE", nil
		}
		return "FALSE", nil
	case types.KindJSON:
		txt, err := nsjson.ToText(v.JSON)
		if err != nil {
			return "", err
		}
		return "'" + strings.ReplaceAll(string(txt), "'", "''") + "'", nil
	case types.KindVector:
		if v.Typ.VecElem == types.VecSparse || len(v.SparseIdx) > 0 {
			dim := int(v.Typ.Precision)
			dense := make([]float32, dim)
			for i, idx := range v.SparseIdx {
				if int(idx) < dim && i < len(v.SparseVal) {
					dense[idx] = v.SparseVal[i]
				}
			}
			v.Vec = dense
		}
		var b strings.Builder
		b.WriteByte('(')
		for i, f := range v.Vec {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(strconv.FormatFloat(float64(f), 'g', -1, 32))
		}
		b.WriteByte(')')
		return b.String(), nil
	case types.KindPoint:
		return fmt.Sprintf("POINT(%g, %g)", v.Lon, v.Lat), nil
	case types.KindBox:
		return fmt.Sprintf("BOX(%g, %g, %g, %g)", v.Box[0], v.Box[1], v.Box[2], v.Box[3]), nil
	case types.KindLine, types.KindPolygon:
		return "'" + strings.ReplaceAll(v.String(), "'", "''") + "'", nil
	case types.KindArray:
		var b strings.Builder
		b.WriteString("ARRAY(")
		for i, e := range v.Coll {
			if i > 0 {
				b.WriteString(", ")
			}
			lit, err := SQLLiteral(e)
			if err != nil {
				return "", err
			}
			b.WriteString(lit)
		}
		b.WriteByte(')')
		return b.String(), nil
	case types.KindStruct:
		var b strings.Builder
		b.WriteString("STRUCT(")
		for i, e := range v.Coll {
			if i > 0 {
				b.WriteString(", ")
			}
			lit, err := SQLLiteral(e)
			if err != nil {
				return "", err
			}
			b.WriteString(lit)
			b.WriteString(" AS ")
			if i < len(v.Typ.Fields) {
				b.WriteString(QuoteIdent(v.Typ.Fields[i].Name))
			}
		}
		b.WriteByte(')')
		return b.String(), nil
	case types.KindMap:
		var b strings.Builder
		b.WriteString("MAP(")
		for i := range v.Coll {
			if i > 0 {
				b.WriteString(", ")
			}
			kl, err := SQLLiteral(v.CollKeys[i])
			if err != nil {
				return "", err
			}
			vl, err := SQLLiteral(v.Coll[i])
			if err != nil {
				return "", err
			}
			b.WriteString(kl)
			b.WriteString(", ")
			b.WriteString(vl)
		}
		b.WriteByte(')')
		return b.String(), nil
	case types.KindGeometry, types.KindGeography:
		if v.Geom == nil {
			return "NULL", nil
		}
		fn := "ST_GeomFromEWKT"
		if v.Typ.Kind == types.KindGeography {
			fn = "ST_GeogFromText"
			return fn + "('" + strings.ReplaceAll(types.FormatGeomWKT(v.Geom), "'", "''") + "')", nil
		}
		return fn + "('" + strings.ReplaceAll(types.FormatGeomEWKT(v.Geom), "'", "''") + "')", nil
	default:
		return "", nerr.New(nerr.InvalidArgument, "catalog.ddl.SQLLiteral", "unsupported default type")
	}
}
