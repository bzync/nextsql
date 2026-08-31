package executor

import (
	"github.com/bzync/nextsql/internal/catalog"
	"github.com/bzync/nextsql/internal/executor/join"
	"github.com/bzync/nextsql/internal/sql/ast"
	"github.com/bzync/nextsql/internal/sql/optimizer"
	"github.com/bzync/nextsql/internal/sql/planner"
	"github.com/bzync/nextsql/internal/sql/types"
)

// partitionScanLeaf is a join input that is a plain scan of a partitioned
// table, optionally wrapped in a residual Filter the optimizer could not turn
// into partition pruning.
type partitionScanLeaf struct {
	scan   planner.SeqScan
	filter ast.Expr
}

func peelPartitionScanLeaf(p planner.Logical) (partitionScanLeaf, bool) {
	switch n := p.(type) {
	case planner.SeqScan:
		if n.Table == nil || n.Table.Partitioning == nil || len(n.Segments) > 0 {
			return partitionScanLeaf{}, false
		}
		return partitionScanLeaf{scan: n}, true
	case planner.Filter:
		inner, ok := n.Input.(planner.SeqScan)
		if !ok || inner.Table == nil || inner.Table.Partitioning == nil || len(inner.Segments) > 0 {
			return partitionScanLeaf{}, false
		}
		return partitionScanLeaf{scan: inner, filter: n.Pred}, true
	}
	return partitionScanLeaf{}, false
}

func partitionListHas(ids []uint32, id uint32) bool {
	if ids == nil {
		return true
	}
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}

// exprHasSubquery reports whether e contains a subquery node. Evaluating a
// subquery mutates per-session caches, so a join predicate that contains one
// cannot have its residual check run from parallel workers.
func exprHasSubquery(e ast.Expr) bool {
	switch x := e.(type) {
	case nil:
		return false
	case ast.Literal, ast.Ident, ast.Path, ast.Param, ast.VectorLit:
		return false
	case ast.ScalarSubquery, ast.InSubquery, ast.ExistsSubquery:
		return true
	case ast.Unary:
		return exprHasSubquery(x.Right)
	case ast.Binary:
		return exprHasSubquery(x.Left) || exprHasSubquery(x.Right)
	case ast.Between:
		return exprHasSubquery(x.Expr) || exprHasSubquery(x.Low) || exprHasSubquery(x.High)
	case ast.IsNull:
		return exprHasSubquery(x.Expr)
	case ast.Call:
		for _, a := range x.Args {
			if exprHasSubquery(a) {
				return true
			}
		}
		return false
	case ast.Case:
		if exprHasSubquery(x.Operand) || exprHasSubquery(x.Else) {
			return true
		}
		for _, w := range x.Whens {
			if exprHasSubquery(w.When) || exprHasSubquery(w.Then) {
				return true
			}
		}
		return false
	default:
		// Unknown node: assume the worst and force serial execution.
		return true
	}
}

func partitionWiseJoinTraceOp(n planner.Join) string {
	switch {
	case n.Kind == ast.JoinSemi:
		return "HashSemiJoin"
	case n.Kind == ast.JoinAnti:
		return "HashAntiJoin"
	case n.Kind == ast.JoinFull:
		return "FullJoin"
	case n.Kind == ast.JoinLeft:
		return "LeftJoin"
	default:
		return "HashJoin"
	}
}

// tryPartitionWiseJoin executes an equi-join between two identically
// partitioned tables as one bounded hash join per aligned partition pair, run
// in parallel across pairs. Because an aligned join can only match rows within
// the same partition pair, the union of the per-pair results is identical to a
// single join over the whole relations, but no hash table ever spans more than
// one partition and pruned pairs are skipped entirely. It returns ok=false when
// the join is not partition-aligned and the generic path must run instead.
func (s *Session) tryPartitionWiseJoin(n planner.Join) ([][]types.Value, error, bool) {
	switch n.Kind {
	case ast.JoinInner, ast.JoinLeft, ast.JoinFull, ast.JoinSemi, ast.JoinAnti:
	default:
		return nil, nil, false
	}
	if n.Cross || n.Method == "merge" || len(n.LeftKeys) == 0 || len(n.LeftKeys) != len(n.RightKeys) {
		return nil, nil, false
	}
	ls, ok := peelPartitionScanLeaf(n.Left)
	if !ok {
		return nil, nil, false
	}
	rs, ok := peelPartitionScanLeaf(n.Right)
	if !ok {
		return nil, nil, false
	}
	pairs, ok := catalog.AlignedPartitionJoin(ls.scan.Table, rs.scan.Table, n.LeftKeys, n.RightKeys)
	if !ok {
		return nil, nil, false
	}

	lTab, rTab := ls.scan.Table, rs.scan.Table
	lTypes, rTypes := lTab.Types(), rTab.Types()
	schema := n.Schema
	if schema == nil {
		schema = tableOf(n)
	}
	var pred join.Pred
	if n.Pred != nil {
		pred = func(l, r []types.Value) (bool, error) {
			row := make([]types.Value, 0, len(l)+len(r))
			row = append(row, l...)
			row = append(row, r...)
			return s.match(n.Pred, schema, row)
		}
	}

	type pairScan struct {
		lRows, rRows [][]types.Value
	}

	// Scan every kept partition pair serially: the session's pending map and
	// row hydration are not safe for concurrent scanners. Only the CPU-bound
	// join runs in parallel below.
	var scans []pairScan
	for _, pr := range pairs {
		lOK := partitionListHas(ls.scan.Partitions, pr.Left)
		rOK := partitionListHas(rs.scan.Partitions, pr.Right)
		keep := false
		switch n.Kind {
		case ast.JoinInner, ast.JoinSemi:
			keep = lOK && rOK
		case ast.JoinFull:
			keep = lOK || rOK
		default: // JoinLeft, JoinAnti
			keep = lOK
		}
		if !keep {
			continue
		}
		var ps pairScan
		if lOK {
			lr, err := s.scanHeapBatchPartitions(lTab, []uint32{pr.Left}, nil, nil, true, true, "SeqScan")
			if err != nil {
				return nil, err, true
			}
			if ls.filter != nil {
				if lr, err = s.applyFilter(lr, lTab, ls.filter, "Filter"); err != nil {
					return nil, err, true
				}
			}
			ps.lRows = lr
		}
		if rOK {
			rr, err := s.scanHeapBatchPartitions(rTab, []uint32{pr.Right}, nil, nil, true, true, "SeqScan")
			if err != nil {
				return nil, err, true
			}
			if rs.filter != nil {
				if rr, err = s.applyFilter(rr, rTab, rs.filter, "Filter"); err != nil {
					return nil, err, true
				}
			}
			ps.rRows = rr
		}
		scans = append(scans, ps)
	}

	outs := make([][][]types.Value, len(scans))
	runPair := func(i int) error {
		l, r := scans[i].lRows, scans[i].rRows
		var (
			part [][]types.Value
			err  error
		)
		switch n.Kind {
		case ast.JoinSemi:
			part, err = join.HashSemiJoin(l, r, n.LeftKeys, n.RightKeys, pred, s.budget())
		case ast.JoinAnti:
			part, err = join.HashAntiJoin(l, r, n.LeftKeys, n.RightKeys, pred, s.budget())
		default:
			part, err = join.HashJoin(l, r, n.LeftKeys, n.RightKeys, n.Kind, lTypes, rTypes, pred, s.budget())
		}
		if err != nil {
			return err
		}
		outs[i] = part
		return nil
	}

	w := s.workers()
	if len(scans) > 1 && w > 1 && !exprHasSubquery(n.Pred) {
		tasks := make([]func() error, len(scans))
		for i := range scans {
			i := i
			tasks[i] = func() error { return runPair(i) }
		}
		if err := s.pool().Run(s.budget().Context(), w, tasks); err != nil {
			return nil, err, true
		}
	} else {
		for i := range scans {
			if err := runPair(i); err != nil {
				return nil, err, true
			}
		}
	}

	var out [][]types.Value
	for _, part := range outs {
		out = append(out, part...)
	}
	if s.trace != nil {
		if node := optimizer.Find(s.trace, partitionWiseJoinTraceOp(n)); node != nil {
			node.ActRows = int64(len(out))
			pw := w
			if pw > len(scans) {
				pw = len(scans)
			}
			if pw < 1 {
				pw = 1
			}
			node.Workers = pw
		}
	}
	return out, nil, true
}
