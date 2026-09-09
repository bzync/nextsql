package executor

import (
	"strings"

	"github.com/bzync/nextsql/internal/catalog"
	"github.com/bzync/nextsql/internal/clientenc"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/sql/ast"
	"github.com/bzync/nextsql/internal/sql/binder"
	"github.com/bzync/nextsql/internal/sql/types"
)

// validateClientEncryptedRow is the last leader-side gate before persistence.
// It validates only public envelope structure and logical type; nextsqld never
// has a field key and therefore cannot authenticate or decrypt the payload.
func validateClientEncryptedRow(tab *catalog.Table, row []types.Value) error {
	if tab == nil || len(row) != len(tab.Columns) {
		return nerr.New(nerr.InvalidArgument, "executor.clientenc", "row shape does not match table")
	}
	for i, col := range tab.Columns {
		if !col.ClientEncrypted() || row[i].Null {
			continue
		}
		if row[i].Typ.Kind != types.KindString && row[i].Typ.Kind != types.KindText {
			return nerr.New(nerr.InvalidArgument, "executor.clientenc", "ENCRYPTED CLIENT value must be an opaque string")
		}
		mode := clientenc.ModeRandomized
		if col.ClientEncryptionMode == catalog.ClientEncryptionDeterministic {
			mode = clientenc.ModeDeterministic
		}
		if err := clientenc.ValidateForColumnMode(row[i].Str, col.ClientType, mode); err != nil {
			return nerr.Wrap(nerr.InvalidArgument, "executor.clientenc", "invalid ENCRYPTED CLIENT value", err)
		}
	}
	return nil
}

// validateClientEncryptedPredicateParams is the execution-time half of the
// binder's deterministic-equality rule. The binder proves the expression
// shape; this gate proves that each runtime parameter is a bounded NSCE2
// envelope with the column's declared logical type before a scan/index lookup.
func (s *Session) validateClientEncryptedPredicateParams(bound binder.Bound) error {
	switch x := bound.(type) {
	case binder.Select:
		if err := s.validateClientEncryptedWhereParams(x.Where, x.Schema); err != nil {
			return err
		}
		return s.validateClientEncryptedPredicateParams(x.Input)
	case binder.Update:
		return s.validateClientEncryptedWhereParams(x.Where, x.Table)
	case binder.Delete:
		return s.validateClientEncryptedWhereParams(x.Where, x.Table)
	case binder.With:
		for _, cte := range x.CTEs {
			if err := s.validateClientEncryptedPredicateParams(cte.Query); err != nil {
				return err
			}
		}
		return s.validateClientEncryptedPredicateParams(x.Query)
	case binder.SetOperation:
		if err := s.validateClientEncryptedPredicateParams(x.Left); err != nil {
			return err
		}
		return s.validateClientEncryptedPredicateParams(x.Right)
	case binder.Explain:
		return s.validateClientEncryptedPredicateParams(x.Stmt)
	default:
		return nil
	}
}

func (s *Session) validateClientEncryptedWhereParams(expr ast.Expr, tab *catalog.Table) error {
	if expr == nil || tab == nil {
		return nil
	}
	switch x := expr.(type) {
	case ast.Binary:
		if x.Op == "AND" || x.Op == "OR" {
			if err := s.validateClientEncryptedWhereParams(x.Left, tab); err != nil {
				return err
			}
			return s.validateClientEncryptedWhereParams(x.Right, tab)
		}
		if x.Op == "=" || x.Op == "<>" || x.Op == "!=" {
			if col, ok := deterministicClientColumn(x.Left, tab); ok {
				if param, ok := x.Right.(ast.Param); ok {
					return s.validateDeterministicParam(param, col)
				}
			}
			if col, ok := deterministicClientColumn(x.Right, tab); ok {
				if param, ok := x.Left.(ast.Param); ok {
					return s.validateDeterministicParam(param, col)
				}
			}
		}
	}
	return nil
}

func deterministicClientColumn(expr ast.Expr, tab *catalog.Table) (catalog.Column, bool) {
	var names []string
	switch x := expr.(type) {
	case ast.Ident:
		names = append(names, x.Name)
	case ast.Path:
		if len(x.Parts) == 2 {
			names = append(names, strings.Join(x.Parts, "."), x.Parts[1])
		}
		if len(x.Parts) > 0 {
			names = append(names, x.Parts[0])
		}
	default:
		return catalog.Column{}, false
	}
	for _, name := range names {
		if ord, ok := tab.ColIndex(name); ok {
			col := tab.Columns[ord]
			if col.ClientEncrypted() && col.ClientEncryptionMode == catalog.ClientEncryptionDeterministic {
				return col, true
			}
		}
	}
	return catalog.Column{}, false
}

func (s *Session) validateDeterministicParam(param ast.Param, col catalog.Column) error {
	value, err := s.lookupParam(param.Name)
	if err != nil {
		return err
	}
	if value.Null {
		return nil
	}
	if value.Typ.Kind != types.KindString && value.Typ.Kind != types.KindText {
		return nerr.New(nerr.InvalidArgument, "executor.clientenc", "DETERMINISTIC ENCRYPTED CLIENT comparison requires an NSCE2 string parameter")
	}
	if err := clientenc.ValidateForColumnMode(value.Str, col.ClientType, clientenc.ModeDeterministic); err != nil {
		return nerr.Wrap(nerr.InvalidArgument, "executor.clientenc", "invalid deterministic ENCRYPTED CLIENT parameter", err)
	}
	return nil
}
