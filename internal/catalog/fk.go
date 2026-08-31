package catalog

import (
	"strconv"

	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/sql/ast"
	"github.com/bzync/nextsql/internal/sql/types"
)

func attachForeignKeys(t *Table, stmt ast.CreateTable) error {
	defs := make([]ast.ForeignKeyDef, 0, len(stmt.FKs)+len(stmt.Columns))
	defs = append(defs, stmt.FKs...)
	for _, c := range stmt.Columns {
		if c.References == nil {
			continue
		}
		fk := *c.References
		if len(fk.Columns) == 0 {
			fk.Columns = []string{c.Name}
		}
		defs = append(defs, fk)
	}
	if len(defs) == 0 {
		return nil
	}
	if len(defs) > MaxForeignKeysPerTable {
		return nerr.New(nerr.InvalidArgument, "catalog.TableFromAST", "too many foreign keys")
	}
	used := make(map[string]struct{}, len(defs))
	for _, d := range defs {
		if len(d.Columns) == 0 {
			return nerr.New(nerr.InvalidArgument, "catalog.TableFromAST", "foreign key has no columns")
		}
		if len(d.Columns) > MaxFKColumns {
			return nerr.New(nerr.InvalidArgument, "catalog.TableFromAST", "too many foreign key columns")
		}
		if len(d.RefCols) != len(d.Columns) {
			return nerr.New(nerr.InvalidArgument, "catalog.TableFromAST", "foreign key column count mismatch")
		}
		cols := make([]int, 0, len(d.Columns))
		seen := make(map[int]struct{}, len(d.Columns))
		for _, name := range d.Columns {
			i, ok := t.ColIndex(name)
			if !ok {
				return nerr.New(nerr.NotFound, "catalog.TableFromAST", "foreign key column missing")
			}
			if _, dup := seen[i]; dup {
				return nerr.New(nerr.InvalidArgument, "catalog.TableFromAST", "duplicate foreign key column")
			}
			seen[i] = struct{}{}
			cols = append(cols, i)
		}
		name := d.Name
		if name == "" {
			name = uniqueFKName(defaultFKName(t.Name, d.Columns), used)
		} else {
			if len(name) > maxFKNameLen {
				return nerr.New(nerr.InvalidArgument, "catalog.TableFromAST", "constraint name too long")
			}
			if _, ok := used[name]; ok {
				return nerr.New(nerr.AlreadyExists, "catalog.TableFromAST", "duplicate constraint name")
			}
			used[name] = struct{}{}
		}
		t.ForeignKeys = append(t.ForeignKeys, ForeignKey{
			Name:     name,
			Columns:  cols,
			RefTable: d.RefTable,
			OnDelete: FKAction(d.OnDelete),
			OnUpdate: FKAction(d.OnUpdate),
			refNames: append([]string(nil), d.RefCols...),
		})
	}
	return nil
}

// AddForeignKey attaches one FOREIGN KEY to an existing table descriptor.
func AddForeignKey(t *Table, def ast.ForeignKeyDef) error {
	if t == nil {
		return nerr.New(nerr.InvalidArgument, "catalog.AddForeignKey", "nil table")
	}
	if len(t.ForeignKeys) >= MaxForeignKeysPerTable {
		return nerr.New(nerr.InvalidArgument, "catalog.AddForeignKey", "too many foreign keys")
	}
	used := make(map[string]struct{}, len(t.ForeignKeys)+1)
	for _, fk := range t.ForeignKeys {
		used[fk.Name] = struct{}{}
	}
	tmp := &Table{Name: t.Name, Columns: t.Columns}
	if err := attachForeignKeys(tmp, ast.CreateTable{FKs: []ast.ForeignKeyDef{def}}); err != nil {
		return err
	}
	if len(tmp.ForeignKeys) != 1 {
		return nerr.New(nerr.InvalidArgument, "catalog.AddForeignKey", "expected one foreign key")
	}
	fk := tmp.ForeignKeys[0]
	if def.Name != "" {
		if _, ok := used[fk.Name]; ok {
			return nerr.New(nerr.AlreadyExists, "catalog.AddForeignKey", "duplicate constraint name")
		}
	} else {
		fk.Name = uniqueFKName(fk.Name, used)
	}
	t.ForeignKeys = append(t.ForeignKeys, fk)
	return nil
}

// ValidateForeignKeys checks CREATE TABLE foreign keys against lookup
// (session overlay ∪ catalog).
func ValidateForeignKeys(child *Table, lookup func(string) (*Table, bool)) error {
	if child == nil || len(child.ForeignKeys) == 0 {
		return nil
	}
	if len(child.ForeignKeys) > MaxForeignKeysPerTable {
		return nerr.New(nerr.InvalidArgument, "catalog.ValidateForeignKeys", "too many foreign keys")
	}
	names := make(map[string]struct{}, len(child.ForeignKeys))
	for i := range child.ForeignKeys {
		fk := &child.ForeignKeys[i]
		if fk.Name == "" {
			return nerr.New(nerr.InvalidArgument, "catalog.ValidateForeignKeys", "empty constraint name")
		}
		if _, ok := names[fk.Name]; ok {
			return nerr.New(nerr.AlreadyExists, "catalog.ValidateForeignKeys", "duplicate constraint name")
		}
		names[fk.Name] = struct{}{}
		if err := validateOneFK(child, fk, lookup); err != nil {
			return err
		}
	}
	return nil
}

func validateOneFK(child *Table, fk *ForeignKey, lookup func(string) (*Table, bool)) error {
	if len(fk.Columns) == 0 || len(fk.Columns) > MaxFKColumns {
		return nerr.New(nerr.InvalidArgument, "catalog.ValidateForeignKeys", "invalid foreign key column count")
	}
	parent, err := resolveParent(child, fk.RefTable, lookup)
	if err != nil {
		return err
	}
	fk.RefTable = parent.Name
	fk.RefTableID = parent.ID
	if len(fk.refNames) > 0 {
		if len(fk.refNames) != len(fk.Columns) {
			return nerr.New(nerr.InvalidArgument, "catalog.ValidateForeignKeys", "foreign key column count mismatch")
		}
		ords := make([]int, 0, len(fk.refNames))
		seen := make(map[int]struct{}, len(fk.refNames))
		for _, name := range fk.refNames {
			i, ok := parent.ColIndex(name)
			if !ok {
				return nerr.New(nerr.NotFound, "catalog.ValidateForeignKeys", "referenced column missing")
			}
			if _, dup := seen[i]; dup {
				return nerr.New(nerr.InvalidArgument, "catalog.ValidateForeignKeys", "duplicate referenced column")
			}
			seen[i] = struct{}{}
			ords = append(ords, i)
		}
		fk.RefColumns = ords
	}
	if len(fk.RefColumns) != len(fk.Columns) {
		return nerr.New(nerr.InvalidArgument, "catalog.ValidateForeignKeys", "foreign key column count mismatch")
	}
	if !validFKAction(fk.OnDelete) || !validFKAction(fk.OnUpdate) {
		return nerr.New(nerr.InvalidArgument, "catalog.ValidateForeignKeys", "unknown foreign key action")
	}
	for i, c := range fk.Columns {
		if c < 0 || c >= len(child.Columns) {
			return nerr.New(nerr.InvalidArgument, "catalog.ValidateForeignKeys", "foreign key column missing")
		}
		r := fk.RefColumns[i]
		if r < 0 || r >= len(parent.Columns) {
			return nerr.New(nerr.NotFound, "catalog.ValidateForeignKeys", "referenced column missing")
		}
		ct := child.Columns[c].Type
		pt := parent.Columns[r].Type
		if ct.Kind == types.KindVector || ct.Kind == types.KindJSON || pt.Kind == types.KindVector || pt.Kind == types.KindJSON {
			return nerr.New(nerr.InvalidArgument, "catalog.ValidateForeignKeys", "VECTOR and JSON cannot be foreign key columns")
		}
		if !fkTypesCompatible(ct, pt) {
			return nerr.New(nerr.InvalidArgument, "catalog.ValidateForeignKeys", "foreign key type mismatch")
		}
	}
	if !isExactUniqueKey(parent, fk.RefColumns) {
		return nerr.New(nerr.InvalidArgument, "catalog.ValidateForeignKeys", "referenced columns must be PRIMARY KEY or UNIQUE")
	}
	if fk.OnDelete == FKSetNull || fk.OnUpdate == FKSetNull {
		for _, c := range fk.Columns {
			if child.Columns[c].NotNull {
				return nerr.New(nerr.InvalidArgument, "catalog.ValidateForeignKeys", "SET NULL requires nullable foreign key columns")
			}
		}
	}
	if fk.OnDelete == FKSetDefault || fk.OnUpdate == FKSetDefault {
		for _, c := range fk.Columns {
			if child.Columns[c].Default.Kind == DefNone {
				return nerr.New(nerr.InvalidArgument, "catalog.ValidateForeignKeys", "SET DEFAULT requires a column default")
			}
		}
	}
	if (fk.OnDelete == FKCascade || fk.OnUpdate == FKCascade) && parent.Name != child.Name {
		if cascadeReaches(parent.Name, child.Name, child, lookup) {
			return nerr.New(nerr.InvalidArgument, "catalog.ValidateForeignKeys", "cyclic CASCADE is not allowed")
		}
	}
	return nil
}

func resolveParent(child *Table, name string, lookup func(string) (*Table, bool)) (*Table, error) {
	if name == "" {
		return nil, nerr.New(nerr.InvalidArgument, "catalog.ValidateForeignKeys", "empty referenced table")
	}
	if child != nil && name == child.Name {
		return child, nil
	}
	if lookup == nil {
		return nil, nerr.New(nerr.NotFound, "catalog.ValidateForeignKeys", "referenced table missing")
	}
	p, ok := lookup(name)
	if !ok {
		return nil, nerr.New(nerr.NotFound, "catalog.ValidateForeignKeys", "referenced table missing")
	}
	return p, nil
}

func cascadeReaches(start, target string, child *Table, lookup func(string) (*Table, bool)) bool {
	seen := make(map[string]struct{})
	var walk func(name string) bool
	walk = func(name string) bool {
		if name == target {
			return true
		}
		if _, ok := seen[name]; ok {
			return false
		}
		seen[name] = struct{}{}
		t, err := resolveParent(child, name, lookup)
		if err != nil {
			return false
		}
		for _, fk := range t.ForeignKeys {
			if fk.OnDelete != FKCascade && fk.OnUpdate != FKCascade {
				continue
			}
			if walk(fk.RefTable) {
				return true
			}
		}
		return false
	}
	return walk(start)
}

func isExactUniqueKey(parent *Table, cols []int) bool {
	if sameOrdSet(parent.PK, cols) {
		return true
	}
	for _, idx := range parent.Indexes {
		if !idx.Unique || idx.Fulltext || idx.Vector || idx.Spatial || len(idx.Path) > 0 {
			continue
		}
		if sameOrdSet(idx.Columns, cols) {
			return true
		}
	}
	return false
}

func validFKAction(a FKAction) bool {
	return a <= FKSetDefault
}

func fkTypesCompatible(a, b types.Type) bool {
	if a.Kind == types.KindDecimal || b.Kind == types.KindDecimal {
		return a.Equals(b)
	}
	if a.Equals(b) {
		return true
	}
	if (a.Kind == types.KindString || a.Kind == types.KindText) && (b.Kind == types.KindString || b.Kind == types.KindText) {
		return true
	}
	va := fkProbeValue(a)
	vb := fkProbeValue(b)
	if va.Null || vb.Null {
		return false
	}
	if _, err := types.Coerce(va, b); err != nil {
		return false
	}
	if _, err := types.Coerce(vb, a); err != nil {
		return false
	}
	return true
}

func fkProbeValue(t types.Type) types.Value {
	switch t.Kind {
	case types.KindUUID:
		v, err := types.NewUUID()
		if err != nil {
			return types.Null(t)
		}
		return v
	case types.KindString:
		return types.StringValue("a")
	case types.KindText:
		return types.TextValue("a")
	case types.KindDecimal:
		d, err := types.ParseDecimal("0")
		if err != nil {
			return types.Null(t)
		}
		d, err = d.Rescale(int(t.Precision), int(t.Scale))
		if err != nil {
			return types.Null(t)
		}
		return types.DecimalValue(d, t)
	case types.KindTimestampTZ:
		return types.Now()
	case types.KindBool:
		return types.BoolValue(true)
	default:
		return types.Null(t)
	}
}

func sameOrds(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func sameOrdSet(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	seen := make(map[int]int, len(a))
	for _, o := range a {
		seen[o]++
	}
	for _, o := range b {
		n := seen[o]
		if n == 0 {
			return false
		}
		seen[o] = n - 1
	}
	return true
}

func defaultFKName(child string, cols []string) string {
	n := len("fk_") + len(child)
	for _, c := range cols {
		n += 1 + len(c)
	}
	b := make([]byte, 0, n)
	b = append(b, "fk_"...)
	b = append(b, child...)
	for _, c := range cols {
		b = append(b, '_')
		b = append(b, c...)
	}
	if len(b) > maxFKNameLen {
		b = b[:maxFKNameLen]
	}
	return string(b)
}

func uniqueFKName(base string, used map[string]struct{}) string {
	if _, ok := used[base]; !ok {
		used[base] = struct{}{}
		return base
	}
	for n := 2; ; n++ {
		suf := "_" + strconv.Itoa(n)
		name := base
		if len(name)+len(suf) > maxFKNameLen {
			name = name[:maxFKNameLen-len(suf)]
		}
		name += suf
		if _, ok := used[name]; !ok {
			used[name] = struct{}{}
			return name
		}
	}
}
