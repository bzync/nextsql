package ast

import (
	"reflect"
	"sync"

	"github.com/bzync/nextsql/internal/sql/types"
)

// MaxNestingDepth bounds how deeply a parsed statement may nest expressions
// and statements — parentheses, operator chains, subqueries, set operations
// and workflow bodies all count. Every later stage (binder, optimizer,
// executor, catalog encoder, EXPLAIN) walks the tree recursively, so an
// unbounded tree is an unbounded goroutine stack: a 2 MiB `SELECT 1+1+…`
// or `SELECT ((((…))))` exceeded Go's 1 GB stack ceiling and killed the whole
// server with an unrecoverable fatal error. A statement deeper than this is
// rejected before any of those stages run.
//
// The value is a structural limit, not an operational knob: it has to stay
// well below the depth at which the deepest recursive stage exhausts its
// stack, and above what generated SQL legitimately produces (long OR chains
// from query builders, deeply parenthesised filters).
const MaxNestingDepth = 4096

// MaxStoredNestingDepth bounds a persisted expression tree when the catalog
// decodes it. It is deliberately far above MaxNestingDepth: expressions stored
// before the parser enforced a bound must stay readable, so this rejects only
// what no parser ever produced — corrupt or hostile bytes — and keeps the
// decoder's own recursion bounded.
const MaxStoredNestingDepth = 1 << 16

var (
	exprType  = reflect.TypeOf((*Expr)(nil)).Elem()
	stmtType  = reflect.TypeOf((*Stmt)(nil)).Elem()
	valueType = reflect.TypeOf(types.Value{})
	typeType  = reflect.TypeOf(types.Type{})

	// leafTypes are expression nodes with no Expr or Stmt beneath them.
	leafTypes = map[reflect.Type]bool{
		reflect.TypeOf(Literal{}):   true,
		reflect.TypeOf(Ident{}):     true,
		reflect.TypeOf(Param{}):     true,
		reflect.TypeOf(Path{}):      true,
		reflect.TypeOf(VectorLit{}): true,
	}

	holdsNode sync.Map // reflect.Type -> bool
)

// ExceedsDepth reports whether the tree rooted at node nests Expr and Stmt
// values more than limit levels deep. Depth counts every Expr or Stmt
// interface value on the path from the root, so `a + b` is depth 2 and a
// statement holding it is depth 3.
//
// The walk is iterative with an explicit work list, so checking an
// arbitrarily deep tree never recurses; it stops as soon as the limit is
// passed, and otherwise visits each node once. Typed values (literals, vector
// literals) and type descriptors are leaves: they are bounded by their own
// decoders, not by statement nesting.
func ExceedsDepth(node any, limit int) bool {
	if node == nil {
		return false
	}
	type item struct {
		v     reflect.Value
		depth int
	}
	work := []item{{v: reflect.ValueOf(node), depth: 0}}
	for len(work) > 0 {
		it := work[len(work)-1]
		work = work[:len(work)-1]
		v, depth := it.v, it.depth
		switch v.Kind() {
		case reflect.Interface:
			if v.IsNil() {
				continue
			}
			if v.Type() == exprType || v.Type() == stmtType {
				depth++
				if depth > limit {
					return true
				}
			}
			e := v.Elem()
			if isLeaf(e) {
				continue
			}
			work = append(work, item{v: e, depth: depth})
		case reflect.Pointer:
			if v.IsNil() {
				continue
			}
			work = append(work, item{v: v.Elem(), depth: depth})
		case reflect.Struct:
			if v.Type() == valueType || v.Type() == typeType {
				continue
			}
			for i := 0; i < v.NumField(); i++ {
				f := v.Field(i)
				if mayHoldNode(f.Type()) {
					work = append(work, item{v: f, depth: depth})
				}
			}
		case reflect.Slice, reflect.Array:
			if !mayHoldNode(v.Type().Elem()) {
				continue
			}
			for i := 0; i < v.Len(); i++ {
				e := v.Index(i)
				if e.Kind() == reflect.Interface && !e.IsNil() && isLeaf(e.Elem()) {
					// A leaf still occupies one level; only its depth matters.
					if (e.Type() == exprType || e.Type() == stmtType) && depth+1 > limit {
						return true
					}
					continue
				}
				work = append(work, item{v: e, depth: depth})
			}
		case reflect.Map:
			if !mayHoldNode(v.Type().Elem()) {
				continue
			}
			iter := v.MapRange()
			for iter.Next() {
				work = append(work, item{v: iter.Value(), depth: depth})
			}
		}
	}
	return false
}

// isLeaf reports whether v (the concrete value inside an interface) has no
// Expr or Stmt beneath it, so the walk need not descend into it.
func isLeaf(v reflect.Value) bool {
	return leafTypes[v.Type()]
}

// mayHoldNode reports whether a value of type t can contain an Expr or Stmt.
// Scalars, strings and byte slices cannot; skipping them keeps the walk
// proportional to the tree, not to the literal data it carries. Results are
// cached per type, and a type met again while it is still being analysed (a
// self-referential struct) is assumed to hold a node, which only ever makes
// the walk visit more, never less.
func mayHoldNode(t reflect.Type) bool {
	if v, ok := holdsNode.Load(t); ok {
		return v.(bool)
	}
	return analyseType(t, map[reflect.Type]bool{})
}

func analyseType(t reflect.Type, visiting map[reflect.Type]bool) bool {
	if v, ok := holdsNode.Load(t); ok {
		return v.(bool)
	}
	if visiting[t] {
		return true
	}
	visiting[t] = true
	holds := false
	switch t.Kind() {
	case reflect.Interface:
		holds = true
	case reflect.Struct:
		if t == valueType || t == typeType || leafTypes[t] {
			break
		}
		for i := 0; i < t.NumField(); i++ {
			if analyseType(t.Field(i).Type, visiting) {
				holds = true
				break
			}
		}
	case reflect.Pointer, reflect.Slice, reflect.Array, reflect.Map:
		holds = analyseType(t.Elem(), visiting)
	}
	delete(visiting, t)
	holdsNode.Store(t, holds)
	return holds
}
