package catalog

import (
	"strconv"

	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/sql/ast"
)

// DDL caps for CHECK constraints. A check runs on every row a write touches,
// so the count is bounded like foreign keys are.
const (
	MaxChecksPerTable = 16
	maxCheckNameLen   = 63
)

// Check is one stored CHECK constraint. Expr is the parsed predicate; it is
// evaluated against the row being written, and the row is refused only when
// the predicate is FALSE. UNKNOWN passes, which is the SQL rule and the reason
// a check is not a substitute for NOT NULL.
type Check struct {
	Name string
	Expr ast.Expr
}

// defaultCheckName names an unnamed constraint after its table, in the same
// shape as an unnamed foreign key's name.
func defaultCheckName(table string, n int) string {
	name := "ck_" + table + "_" + strconv.Itoa(n)
	if len(name) > maxCheckNameLen {
		name = name[:maxCheckNameLen]
	}
	return name
}

func uniqueCheckName(base string, used map[string]struct{}) string {
	if _, ok := used[base]; !ok {
		used[base] = struct{}{}
		return base
	}
	for i := 2; ; i++ {
		suffix := "_" + strconv.Itoa(i)
		name := base
		if len(name)+len(suffix) > maxCheckNameLen {
			name = name[:maxCheckNameLen-len(suffix)]
		}
		name += suffix
		if _, ok := used[name]; !ok {
			used[name] = struct{}{}
			return name
		}
	}
}

// constraintNames returns every constraint name already in use on t. Foreign
// keys and checks share one namespace, so DROP CONSTRAINT is unambiguous.
func constraintNames(t *Table) map[string]struct{} {
	used := make(map[string]struct{}, len(t.ForeignKeys)+len(t.Checks))
	for _, fk := range t.ForeignKeys {
		used[fk.Name] = struct{}{}
	}
	for _, c := range t.Checks {
		used[c.Name] = struct{}{}
	}
	return used
}

// attachChecks resolves CREATE TABLE check constraints onto the descriptor.
// Column-level and table-level checks are the same thing once named.
func attachChecks(t *Table, stmt ast.CreateTable) error {
	if len(stmt.Checks) == 0 {
		return nil
	}
	if len(stmt.Checks) > MaxChecksPerTable {
		return nerr.New(nerr.InvalidArgument, "catalog.TableFromAST", "too many CHECK constraints")
	}
	used := constraintNames(t)
	for i, def := range stmt.Checks {
		if def.Expr == nil {
			return nerr.New(nerr.InvalidArgument, "catalog.TableFromAST", "CHECK requires a predicate")
		}
		name := def.Name
		if name == "" {
			name = uniqueCheckName(defaultCheckName(t.Name, i+1), used)
		} else {
			if len(name) > maxCheckNameLen {
				return nerr.New(nerr.InvalidArgument, "catalog.TableFromAST", "constraint name too long")
			}
			if _, ok := used[name]; ok {
				return nerr.New(nerr.AlreadyExists, "catalog.TableFromAST", "duplicate constraint name")
			}
			used[name] = struct{}{}
		}
		t.Checks = append(t.Checks, Check{Name: name, Expr: def.Expr})
	}
	return nil
}

// AddCheck attaches one CHECK to an existing table descriptor. The caller is
// responsible for validating the predicate against the table's columns and for
// validating existing rows.
func AddCheck(t *Table, def ast.CheckDef) (Check, error) {
	if t == nil {
		return Check{}, nerr.New(nerr.InvalidArgument, "catalog.AddCheck", "nil table")
	}
	if def.Expr == nil {
		return Check{}, nerr.New(nerr.InvalidArgument, "catalog.AddCheck", "CHECK requires a predicate")
	}
	if len(t.Checks) >= MaxChecksPerTable {
		return Check{}, nerr.New(nerr.InvalidArgument, "catalog.AddCheck", "too many CHECK constraints")
	}
	used := constraintNames(t)
	name := def.Name
	if name == "" {
		name = uniqueCheckName(defaultCheckName(t.Name, len(t.Checks)+1), used)
	} else {
		if len(name) > maxCheckNameLen {
			return Check{}, nerr.New(nerr.InvalidArgument, "catalog.AddCheck", "constraint name too long")
		}
		if _, ok := used[name]; ok {
			return Check{}, nerr.New(nerr.AlreadyExists, "catalog.AddCheck", "duplicate constraint name")
		}
	}
	chk := Check{Name: name, Expr: def.Expr}
	t.Checks = append(t.Checks, chk)
	return chk, nil
}

// DropCheck removes a stored CHECK by name, reporting whether it existed.
func DropCheck(t *Table, name string) bool {
	for i := range t.Checks {
		if t.Checks[i].Name == name {
			t.Checks = append(t.Checks[:i], t.Checks[i+1:]...)
			return true
		}
	}
	return false
}
