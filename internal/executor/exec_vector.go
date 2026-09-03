package executor

import (
	"strconv"

	"github.com/bzync/nextsql/internal/catalog"
	"github.com/bzync/nextsql/internal/executor/aggregate"
	"github.com/bzync/nextsql/internal/executor/join"
	"github.com/bzync/nextsql/internal/executor/sort"
	"github.com/bzync/nextsql/internal/executor/vector"
	"github.com/bzync/nextsql/internal/executor/window"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/sql/ast"
	"github.com/bzync/nextsql/internal/sql/optimizer"
	"github.com/bzync/nextsql/internal/sql/planner"
	"github.com/bzync/nextsql/internal/sql/types"
	"github.com/bzync/nextsql/internal/storage/btree"
)

func (s *Session) execSelect(plan planner.Logical) (*Result, error) {
	var (
		proj   *planner.Project
		agg    *planner.Aggregate
		facet  *planner.Facet
		srt    *planner.Sort
		limit  int64 = -1
		offset int64
		cur    = plan
	)
	peeledProject := false
	for {
		switch n := cur.(type) {
		case planner.Limit:
			// Derived-table Limit/Sort/Aggregate stay nested under the
			// already-peeled outer Project so collectPlan can apply them.
			if peeledProject || agg != nil {
				goto done
			}
			offset += n.Offset
			if n.N >= 0 {
				if limit < 0 {
					limit = n.N
				} else {
					remain := limit - n.Offset
					if remain < 0 {
						remain = 0
					}
					if n.N < remain {
						limit = n.N
					} else {
						limit = remain
					}
				}
			} else if limit >= 0 {
				remain := limit - n.Offset
				if remain < 0 {
					remain = 0
				}
				limit = remain
			}
			cur = n.Input
		case planner.Sort:
			if peeledProject || agg != nil {
				goto done
			}
			s := n
			srt = &s
			cur = n.Input
		case planner.Project:
			// Keep inner Projects (RIGHT→LEFT column reorder, derived tables)
			// for collectPlan.
			if peeledProject {
				goto done
			}
			peeledProject = true
			p := n
			proj = &p
			cur = n.Input
		case planner.Aggregate:
			if peeledProject {
				goto done
			}
			a := n
			agg = &a
			cur = n.Input
		case planner.Facet:
			if peeledProject || agg != nil {
				goto done
			}
			f := n
			facet = &f
			cur = n.Input
		case planner.Empty:
			names := n.Names
			if proj != nil {
				names = proj.Names
			}
			if agg != nil {
				names = agg.Names
			}
			if facet != nil {
				names = []string{"facet", "value", "count"}
			}
			return &Result{Columns: append([]string(nil), names...)}, nil
		default:
			goto done
		}
	}
done:
	tab := tableOf(cur)
	if tab == nil {
		tab = tableOf(plan)
	}
	if agg != nil && agg.Schema != nil {
		tab = agg.Schema
	}
	var names []string
	if facet != nil {
		names = []string{"facet", "value", "count"}
	} else if agg != nil {
		names = agg.Names
	} else if proj != nil {
		names = proj.Names
	} else {
		return nil, nerr.New(nerr.Internal, "executor.execSelect", "select without project")
	}
	res := &Result{Columns: append([]string(nil), names...)}

	var hlPop func()
	var hlErr error
	if proj != nil {
		hlPop, hlErr = s.maybePushHighlight(plan, proj.Exprs)
	} else if agg != nil {
		hlPop, hlErr = s.maybePushHighlight(plan, agg.Exprs)
	}
	if hlErr != nil {
		return nil, hlErr
	}
	if hlPop != nil {
		defer hlPop()
	}

	var (
		rows [][]types.Value
		err  error
	)
	if facet != nil {
		rows, err = s.execFacet(facet, cur)
		if err != nil {
			return nil, err
		}
		if s.trace != nil {
			if n := optimizer.Find(s.trace, "Facet"); n != nil {
				n.ActRows = int64(len(rows))
				n.Workers = 1
			}
		}
	} else if agg != nil {
		rows, err = s.execAggregate(agg, cur)
		if err != nil {
			return nil, err
		}
		if agg.Having != nil {
			rows, err = s.applyFilter(rows, aggregateResultTable(agg), agg.Having, "Having")
			if err != nil {
				return nil, err
			}
		}
		if s.trace != nil {
			if n := optimizer.Find(s.trace, "Aggregate"); n != nil {
				n.ActRows = int64(len(rows))
				n.Workers = s.workers()
			}
		}
	} else {
		rows, err = s.collectPlan(cur)
		if err != nil {
			return nil, err
		}
		out := make([][]types.Value, 0, len(rows))
		for _, row := range rows {
			if err := s.budget().Check(); err != nil {
				return nil, err
			}
			dst := make([]types.Value, len(proj.Exprs))
			for i, ex := range proj.Exprs {
				v, err := s.eval(ex, tab, row)
				if err != nil {
					return nil, err
				}
				dst[i] = v
			}
			out = append(out, dst)
			if s.trace != nil {
				if n := optimizer.Find(s.trace, "Project"); n != nil {
					n.ActRows++
				}
			}
		}
		rows = out
	}
	if (proj != nil && proj.Distinct) || (agg != nil && agg.Distinct) {
		rows, err = s.hashDistinct(rows)
		if err != nil {
			return nil, err
		}
		if s.trace != nil {
			if n := optimizer.Find(s.trace, "HashDistinct"); n != nil {
				n.ActRows = int64(len(rows))
			}
		}
	}

	if srt != nil {
		keys := make([]sort.Key, len(srt.Keys))
		for i, k := range srt.Keys {
			keys[i] = sort.Key{Col: k.Col, Desc: k.Desc}
		}
		if srt.TopN > 0 {
			rows, err = sort.TopRows(rows, keys, srt.TopN)
			if err != nil {
				return nil, err
			}
		} else {
			if err := sort.Rows(rows, keys); err != nil {
				return nil, err
			}
		}
		if srt.OrderedDistinct {
			rows, err = orderedDistinct(rows, keys)
			if err != nil {
				return nil, err
			}
			if s.trace != nil {
				if n := optimizer.Find(s.trace, "OrderedDistinct"); n != nil {
					n.ActRows = int64(len(rows))
				}
			}
		}
		if s.trace != nil {
			if n := optimizer.Find(s.trace, "Sort"); n != nil {
				n.ActRows = int64(len(rows))
			}
		}
		if srt.Hidden > 0 {
			if srt.Hidden > len(names) {
				return nil, nerr.New(nerr.Internal, "executor.execSelect", "ORDER BY hidden columns exceed output")
			}
			names = names[:len(names)-srt.Hidden]
			res.Columns = append([]string(nil), names...)
			for i, row := range rows {
				if srt.Hidden > len(row) {
					return nil, nerr.New(nerr.Internal, "executor.execSelect", "ORDER BY hidden columns exceed row")
				}
				rows[i] = row[:len(row)-srt.Hidden]
			}
		}
	}

	rows = applyLimitOffset(rows, limit, offset)
	if max := s.budget().ResultRows(); len(rows) > max {
		return nil, nerr.New(nerr.Exhausted, "executor.execSelect", "result exceeds row limit")
	}
	var mem int64
	for _, r := range rows {
		rowBytes := int64(len(r) * 16)
		for _, v := range r {
			rowBytes += int64(len(v.Str) + len(v.JSON) + 4*len(v.Vec))
		}
		mem += rowBytes
		if mem > s.budget().ResultBytes() {
			return nil, nerr.New(nerr.Exhausted, "executor.execSelect", "result exceeds size budget")
		}
		if err := s.budget().ChargeMem(rowBytes); err != nil {
			return nil, err
		}
	}
	res.Rows = rows
	if s.trace != nil {
		if n := optimizer.Find(s.trace, "Limit"); n != nil {
			n.ActRows = int64(len(res.Rows))
		}
		optimizer.FillDefaults(s.trace)
		s.trace.Memory = s.budget().PeakMemory()
		if s.trace.Memory == 0 {
			s.trace.Memory = mem
		}
		s.trace.Spill = s.budget().Disk()
		if s.trace.Workers == 0 {
			s.trace.Workers = s.workers()
		}
	}
	return res, nil
}

func aggregateResultTable(a *planner.Aggregate) *catalog.Table {
	_, outTypes := aggSpecs(a)
	tab := &catalog.Table{Name: "aggregate", Columns: make([]catalog.Column, len(a.Names))}
	for i, name := range a.Names {
		tab.Columns[i] = catalog.Column{Name: name, Type: outTypes[i]}
	}
	return tab
}

func orderedDistinct(rows [][]types.Value, keys []sort.Key) ([][]types.Value, error) {
	if len(rows) < 2 {
		return rows, nil
	}
	out := rows[:1]
	for _, row := range rows[1:] {
		cmp, err := sort.Compare(out[len(out)-1], row, keys)
		if err != nil {
			return nil, err
		}
		if cmp != 0 {
			out = append(out, row)
		}
	}
	return out, nil
}

func (s *Session) hashDistinct(rows [][]types.Value) ([][]types.Value, error) {
	seen := make(map[string]struct{}, len(rows))
	out := make([][]types.Value, 0, len(rows))
	for _, row := range rows {
		key, err := types.EncodeRow(row)
		if err != nil {
			return nil, err
		}
		if _, ok := seen[string(key)]; ok {
			continue
		}
		if err := s.budget().ChargeMem(int64(len(key) + 16)); err != nil {
			return nil, err
		}
		seen[string(key)] = struct{}{}
		out = append(out, row)
	}
	return out, nil
}

func (s *Session) execAggregate(a *planner.Aggregate, input planner.Logical) ([][]types.Value, error) {
	if countStarOnly(a) {
		if n, ok, err := s.tryCountScan(input); err != nil {
			return nil, err
		} else if ok {
			return countStarResult(a, n), nil
		}
		if n, ok, err := s.tryCountJoin(input); err != nil {
			return nil, err
		} else if ok {
			return countStarResult(a, n), nil
		}
	}
	if rows, err, ok := s.tryStreamHeapAggregate(a, input); ok {
		return rows, err
	}
	if canStream(input) {
		return s.streamAggregate(a, input)
	}
	rows, err := s.collectPlan(input)
	if err != nil {
		return nil, err
	}
	return s.runAggregate(a, rows)
}

func canStream(p planner.Logical) bool {
	switch p.(type) {
	case planner.Empty, planner.Limit, planner.Filter, planner.Scan, planner.SeqScan, planner.IndexScan, planner.Search, planner.Nearest, planner.Candidates, planner.Rerank, planner.Project:
		return true
	default:
		return false
	}
}

func countStarOnly(a *planner.Aggregate) bool {
	if a == nil || len(a.Groups) > 0 || len(a.Specs) == 0 {
		return false
	}
	for _, sp := range a.Specs {
		if !sp.Star || sp.Fun != "count" {
			return false
		}
	}
	return true
}

func countStarResult(a *planner.Aggregate, n int64) [][]types.Value {
	d, _ := types.ParseDecimal(strconv.FormatInt(n, 10))
	v := types.DecimalValue(d, types.Type{Kind: types.KindDecimal})
	raw := make([]types.Value, len(a.Specs))
	for i := range raw {
		raw[i] = v
	}
	return orderAgg([][]types.Value{raw}, a)
}

func (s *Session) tryCountScan(input planner.Logical) (int64, bool, error) {
	switch n := input.(type) {
	case planner.Scan:
		c, err := s.countHeap(n.Table, nil, nil, true, true)
		return c, true, err
	case planner.SeqScan:
		if len(n.Segments) == 0 {
			c, err := s.countHeapPartitions(n.Table, n.Partitions, nil, nil, true, true)
			return c, true, err
		}
		var total int64
		for _, seg := range n.Segments {
			c, err := s.countHeapPartitions(n.Table, n.Partitions, seg.Low, seg.High, seg.LowIncl, seg.HighIncl)
			if err != nil {
				return 0, true, err
			}
			total += c
		}
		return total, true, nil
	default:
		return 0, false, nil
	}
}

const maxJoinBuild = 100_000

func (s *Session) tryCountJoin(input planner.Logical) (int64, bool, error) {
	j, ok := input.(planner.Join)
	if !ok || j.Cross || j.Kind == ast.JoinLeft || j.Kind == ast.JoinFull || j.Kind == ast.JoinCross || len(j.LeftKeys) == 0 || len(j.RightKeys) == 0 {
		return 0, false, nil
	}
	right, err := s.collectPlan(j.Right)
	if err != nil {
		return 0, true, err
	}
	if len(right) <= maxJoinBuild {
		ht, err := join.Build(right, j.RightKeys, s.budget())
		if err != nil {
			return 0, true, err
		}
		n, err := s.streamProbeCount(j.Left, ht, j.LeftKeys)
		return n, true, err
	}
	left, err := s.collectPlan(j.Left)
	if err != nil {
		return 0, true, err
	}
	if len(left) > maxJoinBuild {
		return 0, false, nil
	}
	ht, err := join.Build(left, j.LeftKeys, s.budget())
	if err != nil {
		return 0, true, err
	}
	n, err := s.streamProbeCount(j.Right, ht, j.RightKeys)
	return n, true, err
}

func (s *Session) streamProbeCount(p planner.Logical, ht *join.Table, keyCols []int) (int64, error) {
	var tab *catalog.Table
	switch n := p.(type) {
	case planner.Scan:
		tab = n.Table
	case planner.SeqScan:
		if len(n.Segments) != 0 {
			break
		}
		tab = n.Table
	}
	if tab == nil {
		rows, err := s.collectPlan(p)
		if err != nil {
			return 0, err
		}
		var n int64
		for _, row := range rows {
			c, err := ht.ProbeCount(row, keyCols)
			if err != nil {
				return n, err
			}
			n += int64(c)
		}
		return n, nil
	}
	heap, err := s.heapOf(tab)
	if err != nil {
		return 0, err
	}
	htx := s.x.use(heap)
	cols := tab.Types()
	w := s.workers()
	if w > 1 {
		splits, err := htx.SplitKeys(w)
		if err == nil && len(splits) > 0 {
			return s.parallelProbeCount(htx, tab, cols, ht, keyCols, splits)
		}
	}
	return s.rangeProbeCount(htx, cols, ht, keyCols, nil, nil)
}

func (s *Session) parallelProbeCount(htx *btree.Txn, tab *catalog.Table, cols []types.Type, ht *join.Table, keyCols []int, splits [][]byte) (int64, error) {
	ranges := splitByteRanges(nil, nil, splits)
	counts := make([]int64, len(ranges))
	tasks := make([]func() error, len(ranges))
	for i := range ranges {
		i := i
		tasks[i] = func() error {
			n, err := s.rangeProbeCount(htx, cols, ht, keyCols, ranges[i][0], ranges[i][1])
			counts[i] = n
			return err
		}
	}
	if err := s.pool().Run(s.budget().Context(), s.workers(), tasks); err != nil {
		return 0, err
	}
	var total int64
	for _, c := range counts {
		total += c
	}
	return total, nil
}

func (s *Session) rangeProbeCount(htx *btree.Txn, cols []types.Type, ht *join.Table, keyCols []int, start, end []byte) (int64, error) {
	row := make([]types.Value, len(cols))
	var (
		total int64
		n     int
	)
	err := htx.RangeVisible(start, end, func(_, val []byte) error {
		n++
		if n&255 == 0 {
			if err := s.budget().Check(); err != nil {
				return err
			}
		}
		for _, c := range keyCols {
			if c < 0 || c >= len(cols) {
				return nerr.New(nerr.Internal, "executor.rangeProbeCount", "join key out of range")
			}
			v, err := types.DecodeRowColumn(val, cols, c)
			if err != nil {
				return err
			}
			row[c] = v
		}
		c, err := ht.ProbeCount(row, keyCols)
		if err != nil {
			return err
		}
		total += int64(c)
		return nil
	})
	return total, err
}

func (s *Session) countHeap(tab *catalog.Table, low, high []types.Value, lowIncl, highIncl bool) (int64, error) {
	return s.countHeapPartitions(tab, nil, low, high, lowIncl, highIncl)
}

func (s *Session) countHeapPartitions(tab *catalog.Table, partitions []uint32, low, high []types.Value, lowIncl, highIncl bool) (int64, error) {
	if tab != nil && tab.Partitioning != nil {
		start, end, err := encodeBounds(low, high, lowIncl, highIncl)
		if err != nil {
			return 0, err
		}
		var total int64
		for _, part := range partitionSelection(tab, partitions) {
			heap, err := s.partitionHeap(tab, part.ID)
			if err != nil {
				return 0, err
			}
			n, err := s.countRange(s.x.use(heap), start, end, false)
			if err != nil {
				return 0, err
			}
			total += n
		}
		return total, nil
	}
	heap, err := s.heapOf(tab)
	if err != nil {
		return 0, err
	}
	start, end, err := encodeBounds(low, high, lowIncl, highIncl)
	if err != nil {
		return 0, err
	}
	htx := s.x.use(heap)
	if start == nil && end == nil && s.soleSnapshot() {
		if n, ok := htx.CachedLive(); ok {
			if s.trace != nil {
				if node := optimizer.Find(s.trace, "SeqScan"); node != nil {
					node.ActRows = n
					node.Workers = 1
				}
			}
			return n, nil
		}
	}
	w := s.workers()
	if w > 1 && start == nil && end == nil {
		splits, err := htx.SplitKeys(w)
		if err == nil && len(splits) > 0 {
			n, err := s.parallelCount(htx, splits)
			if err != nil {
				return 0, err
			}
			if s.trace != nil {
				if node := optimizer.Find(s.trace, "SeqScan"); node != nil {
					node.ActRows = n
					node.Workers = w
				}
			}
			return n, nil
		}
	}
	n, err := s.countRange(htx, start, end, false)
	if err != nil {
		return 0, err
	}
	if s.trace != nil {
		if node := optimizer.Find(s.trace, "SeqScan"); node != nil {
			node.ActRows = n
			node.Workers = 1
		}
	}
	return n, nil
}

func (s *Session) countRange(htx *btree.Txn, start, end []byte, visible bool) (int64, error) {
	if err := s.budget().Check(); err != nil {
		return 0, err
	}
	var (
		n   int64
		err error
	)
	if visible {
		n, err = htx.CountVisible(start, end)
	} else {
		n, err = htx.Count(start, end)
	}
	if err != nil {
		return 0, err
	}
	if err := s.budget().ChargeIO(n * 32); err != nil {
		return 0, err
	}
	return n, nil
}

func (s *Session) parallelCount(htx *btree.Txn, splits [][]byte) (int64, error) {
	ranges := splitByteRanges(nil, nil, splits)
	counts := make([]int64, len(ranges))
	tasks := make([]func() error, len(ranges))
	for i := range ranges {
		i := i
		tasks[i] = func() error {
			n, err := s.countRange(htx, ranges[i][0], ranges[i][1], true)
			counts[i] = n
			return err
		}
	}
	if err := s.pool().Run(s.budget().Context(), s.workers(), tasks); err != nil {
		return 0, err
	}
	var total int64
	for _, c := range counts {
		total += c
	}
	return total, nil
}

func splitByteRanges(start, end []byte, splits [][]byte) [][2][]byte {
	ranges := make([][2][]byte, 0, len(splits)+1)
	prev := start
	for _, k := range splits {
		if start != nil && compareBytes(k, start) <= 0 {
			continue
		}
		if end != nil && compareBytes(k, end) >= 0 {
			break
		}
		ranges = append(ranges, [2][]byte{prev, k})
		prev = k
	}
	return append(ranges, [2][]byte{prev, end})
}

func (s *Session) tryStreamHeapAggregate(a *planner.Aggregate, input planner.Logical) ([][]types.Value, error, bool) {
	var tab *catalog.Table
	switch n := input.(type) {
	case planner.Scan:
		tab = n.Table
	case planner.SeqScan:
		if n.Table != nil && n.Table.Partitioning != nil {
			// Partition-wise aggregation: aggregate each surviving partition
			// heap independently and in parallel, then merge the partial
			// hash tables. This removes the sequential per-partition scan
			// penalty of the generic streaming path.
			rows, err := s.partitionWiseHeapAggregate(a, n)
			return rows, err, true
		}
		if len(n.Segments) > 0 {
			return nil, nil, false
		}
		tab = n.Table
	default:
		return nil, nil, false
	}
	if tab == nil {
		return nil, nil, false
	}
	rows, err := s.streamHeapAggregate(a, tab)
	return rows, err, true
}

func countStarGrouped(specs []aggregate.Spec) bool {
	if len(specs) == 0 {
		return false
	}
	for _, sp := range specs {
		if sp.Fun != "count" || sp.Col >= 0 {
			return false
		}
	}
	return true
}

func (s *Session) feedHeapAggregate(h *aggregate.Hash, a *planner.Aggregate, tab *catalog.Table, specs []aggregate.Spec, rng func(func(key, val []byte) error) error) (int64, error) {
	cols := tab.Types()
	project := countStarGrouped(specs) && len(a.Groups) > 0
	for _, g := range a.Groups {
		if g < 0 || g >= len(cols) {
			project = false
			break
		}
	}
	peekStr := project && len(a.Groups) == 1 &&
		(cols[a.Groups[0]].Kind == types.KindString || cols[a.Groups[0]].Kind == types.KindText)
	var (
		act   int64
		ioAcc int64
		n     int
		gvals []types.Value
	)
	if project && !peekStr {
		gvals = make([]types.Value, len(a.Groups))
	}
	err := rng(func(_, val []byte) error {
		n++
		ioAcc += int64(len(val))
		if n&255 == 0 {
			if err := s.budget().Check(); err != nil {
				return err
			}
			if err := s.budget().ChargeIO(ioAcc); err != nil {
				return err
			}
			ioAcc = 0
		}
		act++
		if peekStr {
			b, null, err := types.PeekRowColumn(val, cols, a.Groups[0])
			if err != nil {
				return err
			}
			return h.AddCountStarBytes(b, null, cols[a.Groups[0]])
		}
		if project {
			for i, g := range a.Groups {
				v, err := types.DecodeRowColumn(val, cols, g)
				if err != nil {
					return err
				}
				gvals[i] = v
			}
			return h.AddCountStar(gvals)
		}
		row, err := s.decodeHeapRow(tab, val)
		if err != nil {
			return err
		}
		return h.Add(row)
	})
	if err != nil {
		return act, err
	}
	if ioAcc > 0 {
		if err := s.budget().ChargeIO(ioAcc); err != nil {
			return act, err
		}
	}
	return act, nil
}

func (s *Session) streamHeapAggregate(a *planner.Aggregate, tab *catalog.Table) ([][]types.Value, error) {
	heap, err := s.heapOf(tab)
	if err != nil {
		return nil, err
	}
	htx := s.x.use(heap)
	specs, outTy := aggSpecs(a)
	w := s.workers()
	if w > 1 {
		splits, err := htx.SplitKeys(w)
		if err == nil && len(splits) > 0 {
			rows, err := s.parallelHeapAggregate(a, tab, htx, specs, outTy, splits)
			if err != nil {
				return nil, err
			}
			return rows, nil
		}
	}
	h := aggregate.New(a.Groups, specs, outTy, s.budget())
	defer h.Close()
	act, err := s.feedHeapAggregate(h, a, tab, specs, func(fn func(key, val []byte) error) error {
		if s.soleSnapshot() {
			return htx.RangeLive(fn)
		}
		return htx.Range(nil, nil, fn)
	})
	if err != nil {
		return nil, err
	}
	if s.trace != nil {
		if n := optimizer.Find(s.trace, "SeqScan"); n != nil {
			n.ActRows = act
			n.Workers = 1
		}
	}
	raw, err := h.Finish()
	if err != nil {
		return nil, err
	}
	return orderAgg(raw, a), nil
}

func (s *Session) parallelHeapAggregate(a *planner.Aggregate, tab *catalog.Table, htx *btree.Txn, specs []aggregate.Spec, outTy []types.Type, splits [][]byte) ([][]types.Value, error) {
	ranges := splitByteRanges(nil, nil, splits)
	parts := make([]*aggregate.Hash, len(ranges))
	acts := make([]int64, len(ranges))
	tasks := make([]func() error, len(ranges))
	for i := range ranges {
		i := i
		tasks[i] = func() error {
			h := aggregate.New(a.Groups, specs, outTy, s.budget())
			parts[i] = h
			n, err := s.feedHeapAggregate(h, a, tab, specs, func(fn func(key, val []byte) error) error {
				if s.soleSnapshot() {
					return htx.RangeLiveRange(ranges[i][0], ranges[i][1], fn)
				}
				return htx.RangeVisible(ranges[i][0], ranges[i][1], fn)
			})
			acts[i] = n
			return err
		}
	}
	if err := s.pool().Run(s.budget().Context(), s.workers(), tasks); err != nil {
		for _, h := range parts {
			if h != nil {
				h.Close()
			}
		}
		return nil, err
	}
	merged := aggregate.New(a.Groups, specs, outTy, s.budget())
	defer merged.Close()
	var act int64
	for i, h := range parts {
		act += acts[i]
		if h == nil {
			continue
		}
		if err := merged.Merge(h); err != nil {
			h.Close()
			return nil, err
		}
		h.Close()
	}
	if s.trace != nil {
		if n := optimizer.Find(s.trace, "SeqScan"); n != nil {
			n.ActRows = act
			n.Workers = s.workers()
		}
	}
	raw, err := merged.Finish()
	if err != nil {
		return nil, err
	}
	return orderAgg(raw, a), nil
}

// partitionWiseHeapAggregate aggregates a partitioned table by running one
// partial hash aggregation per surviving partition heap, in parallel through
// the query scheduler, and merging the partials. Groups that span partitions
// are folded during the merge, so the result is identical to a single
// aggregation over the union of the partitions.
func (s *Session) partitionWiseHeapAggregate(a *planner.Aggregate, n planner.SeqScan) ([][]types.Value, error) {
	tab := n.Table
	sel := partitionSelection(tab, n.Partitions)
	specs, outTy := aggSpecs(a)

	// Segment bounds (PK ranges left by the optimizer) are encoded once and
	// applied inside every partition heap.
	type span struct{ start, end []byte }
	var spans []span
	if len(n.Segments) == 0 {
		spans = []span{{nil, nil}}
	} else {
		for _, seg := range n.Segments {
			start, end, err := encodeBounds(seg.Low, seg.High, seg.LowIncl, seg.HighIncl)
			if err != nil {
				return nil, err
			}
			spans = append(spans, span{start, end})
		}
	}

	// Partition heap transactions are resolved serially: the session's pending
	// map is not safe for concurrent writers. The btree.Txn values themselves
	// support concurrent range reads.
	htxs := make([]*btree.Txn, len(sel))
	for i, part := range sel {
		heap, err := s.partitionHeap(tab, part.ID)
		if err != nil {
			return nil, err
		}
		htxs[i] = s.x.use(heap)
	}

	sole := s.soleSnapshot()
	parts := make([]*aggregate.Hash, len(sel))
	acts := make([]int64, len(sel))
	tasks := make([]func() error, len(sel))
	for i := range sel {
		i := i
		htx := htxs[i]
		tasks[i] = func() error {
			h := aggregate.New(a.Groups, specs, outTy, s.budget())
			parts[i] = h
			var act int64
			for _, sp := range spans {
				sp := sp
				got, err := s.feedHeapAggregate(h, a, tab, specs, func(fn func(key, val []byte) error) error {
					if sole && sp.start == nil && sp.end == nil {
						return htx.RangeLive(fn)
					}
					return htx.Range(sp.start, sp.end, fn)
				})
				act += got
				if err != nil {
					return err
				}
			}
			acts[i] = act
			return nil
		}
	}
	w := s.workers()
	if w < 1 {
		w = 1
	}
	if err := s.pool().Run(s.budget().Context(), w, tasks); err != nil {
		for _, h := range parts {
			if h != nil {
				h.Close()
			}
		}
		return nil, err
	}

	merged := aggregate.New(a.Groups, specs, outTy, s.budget())
	defer merged.Close()
	var act int64
	for i, h := range parts {
		act += acts[i]
		if h == nil {
			continue
		}
		if err := merged.Merge(h); err != nil {
			h.Close()
			return nil, err
		}
		h.Close()
	}
	if s.trace != nil {
		if node := optimizer.Find(s.trace, "SeqScan"); node != nil {
			node.ActRows = act
			node.Workers = min(w, len(sel))
		}
	}
	raw, err := merged.Finish()
	if err != nil {
		return nil, err
	}
	return orderAgg(raw, a), nil
}

func (s *Session) streamAggregate(a *planner.Aggregate, input planner.Logical) ([][]types.Value, error) {
	specs, outTy := aggSpecs(a)
	h := aggregate.New(a.Groups, specs, outTy, s.budget())
	defer h.Close()
	if err := s.forEachRow(input, func(row []types.Value) error {
		return h.Add(row)
	}); err != nil {
		return nil, err
	}
	raw, err := h.Finish()
	if err != nil {
		return nil, err
	}
	return orderAgg(raw, a), nil
}

func aggSpecs(a *planner.Aggregate) ([]aggregate.Spec, []types.Type) {
	specs := make([]aggregate.Spec, len(a.Specs))
	for i, sp := range a.Specs {
		col := sp.Col
		if sp.Star {
			col = -1
		}
		specs[i] = aggregate.Spec{Fun: sp.Fun, Col: col}
	}
	outTy := make([]types.Type, len(a.Names))
	for i := range outTy {
		outTy[i] = types.Type{Kind: types.KindDecimal}
	}
	if a.Schema != nil {
		gi := 0
		for i, ex := range a.Exprs {
			if id, ok := ex.(ast.Ident); ok {
				if ord, found := a.Schema.ColIndex(id.Name); found && i < len(outTy) {
					outTy[i] = a.Schema.Columns[ord].Type
					if gi < len(a.Groups) {
						gi++
					}
				}
			}
		}
	}
	return specs, outTy
}

func (s *Session) runAggregate(a *planner.Aggregate, rows [][]types.Value) ([][]types.Value, error) {
	specs, outTy := aggSpecs(a)
	var (
		raw [][]types.Value
		err error
	)
	w := s.workers()
	if w > 1 && len(rows) >= 64 {
		raw, err = aggregate.Parallel(s.pool(), s.budget(), a.Groups, specs, outTy, splitRows(rows, w))
	} else {
		h := aggregate.New(a.Groups, specs, outTy, s.budget())
		defer h.Close()
		for _, row := range rows {
			if err := h.Add(row); err != nil {
				return nil, err
			}
		}
		raw, err = h.Finish()
	}
	if err != nil {
		return nil, err
	}
	return orderAgg(raw, a), nil
}

func orderAgg(raw [][]types.Value, a *planner.Aggregate) [][]types.Value {
	if len(a.Exprs) == 0 || len(raw) == 0 {
		return raw
	}
	out := make([][]types.Value, len(raw))
	for i, row := range raw {
		dst := make([]types.Value, len(a.Exprs))
		gi, ai := 0, 0
		for j, ex := range a.Exprs {
			if _, ok := ex.(ast.Call); ok {
				idx := len(a.Groups) + ai
				if idx < len(row) {
					dst[j] = row[idx]
				}
				ai++
			} else {
				if gi < len(row) {
					dst[j] = row[gi]
				}
				gi++
			}
		}
		out[i] = dst
	}
	return out
}

func splitRows(rows [][]types.Value, n int) [][][]types.Value {
	if n < 1 {
		n = 1
	}
	out := make([][][]types.Value, n)
	for i, r := range rows {
		out[i%n] = append(out[i%n], r)
	}
	return out
}

func (s *Session) collectPlan(p planner.Logical) ([][]types.Value, error) {
	if p == nil {
		return nil, nil
	}
	if err := s.budget().Check(); err != nil {
		return nil, err
	}
	switch n := p.(type) {
	case planner.Empty:
		return nil, nil
	case planner.With:
		res, err := s.execWith(n)
		if err != nil {
			return nil, err
		}
		if res == nil {
			return nil, nil
		}
		return res.Rows, nil
	case planner.CTEScan:
		return s.scanCTE(n)
	case planner.SetOperation:
		res, err := s.execSetOperation(n)
		if err != nil {
			return nil, err
		}
		if res == nil {
			return nil, nil
		}
		return res.Rows, nil
	case planner.Sort:
		rows, err := s.collectPlan(n.Input)
		if err != nil {
			return nil, err
		}
		keys := make([]sort.Key, len(n.Keys))
		for i, k := range n.Keys {
			keys[i] = sort.Key{Col: k.Col, Desc: k.Desc}
		}
		if n.TopN > 0 {
			rows, err = sort.TopRows(rows, keys, n.TopN)
			if err != nil {
				return nil, err
			}
		} else {
			if err := sort.Rows(rows, keys); err != nil {
				return nil, err
			}
		}
		if n.OrderedDistinct {
			rows, err = orderedDistinct(rows, keys)
			if err != nil {
				return nil, err
			}
		}
		if n.Hidden > 0 {
			for i, row := range rows {
				if n.Hidden > len(row) {
					return nil, nerr.New(nerr.Internal, "executor.collectPlan", "ORDER BY hidden columns exceed row")
				}
				rows[i] = row[:len(row)-n.Hidden]
			}
		}
		return rows, nil
	case planner.Limit:
		rows, err := s.collectPlan(n.Input)
		if err != nil {
			return nil, err
		}
		return applyLimitOffset(rows, n.N, n.Offset), nil
	case planner.Project:
		rows, err := s.collectPlan(n.Input)
		if err != nil {
			return nil, err
		}
		tab := tableOf(n.Input)
		out := make([][]types.Value, 0, len(rows))
		for _, row := range rows {
			if err := s.budget().Check(); err != nil {
				return nil, err
			}
			dst := make([]types.Value, len(n.Exprs))
			for i, ex := range n.Exprs {
				v, err := s.eval(ex, tab, row)
				if err != nil {
					return nil, err
				}
				dst[i] = v
			}
			out = append(out, dst)
		}
		if n.Distinct {
			return s.hashDistinct(out)
		}
		return out, nil
	case planner.Filter:
		rows, err := s.collectPlan(n.Input)
		if err != nil {
			return nil, err
		}
		tab := tableOf(n.Input)
		return s.applyFilter(rows, tab, n.Pred, "Filter")
	case planner.Scan:
		return s.scanHeapBatch(n.Table, nil, nil, true, true, "SeqScan")
	case planner.SeqScan:
		if len(n.Segments) == 0 {
			return s.scanHeapBatchPartitions(n.Table, n.Partitions, nil, nil, true, true, "SeqScan")
		}
		var all [][]types.Value
		for _, seg := range n.Segments {
			part, err := s.scanHeapBatchPartitions(n.Table, n.Partitions, seg.Low, seg.High, seg.LowIncl, seg.HighIncl, "SeqScan")
			if err != nil {
				return nil, err
			}
			all = append(all, part...)
		}
		return all, nil
	case planner.IndexScan:
		return s.scanIndexBatch(n)
	case planner.Search:
		return s.searchFulltext(n)
	case planner.Facet:
		rows, err := s.collectPlan(n.Input)
		if err != nil {
			return nil, err
		}
		out, err := s.facetRows(&n, rows)
		if err != nil {
			return nil, err
		}
		if s.trace != nil {
			if node := optimizer.Find(s.trace, "Facet"); node != nil {
				node.ActRows = int64(len(out))
				node.Workers = 1
			}
		}
		return out, nil
	case planner.Nearest:
		return s.searchNearest(n)
	case planner.Candidates:
		return s.execCandidates(n)
	case planner.Rerank:
		return s.execRerank(n)
	case planner.Join:
		return s.execJoin(n)
	case planner.Aggregate:
		var (
			rows [][]types.Value
			err  error
		)
		if sc, ok := n.Input.(planner.SeqScan); ok && sc.Table != nil && sc.Table.Partitioning != nil {
			rows, err = s.partitionWiseHeapAggregate(&n, sc)
		} else {
			var in [][]types.Value
			in, err = s.collectPlan(n.Input)
			if err == nil {
				rows, err = s.runAggregate(&n, in)
			}
		}
		if err != nil {
			return nil, err
		}
		if n.Having != nil {
			rows, err = s.applyFilter(rows, aggregateResultTable(&n), n.Having, "Having")
			if err != nil {
				return nil, err
			}
		}
		if n.Distinct {
			return s.hashDistinct(rows)
		}
		return rows, nil
	case planner.Window:
		rows, err := s.collectPlan(n.Input)
		if err != nil {
			return nil, err
		}
		tab := tableOf(n.Input)
		out, err := window.Apply(rows, tab, n.Specs, s.eval, s.budget())
		if err != nil {
			return nil, err
		}
		if s.trace != nil {
			if node := optimizer.Find(s.trace, "Window"); node != nil {
				node.ActRows = int64(len(out))
				node.Workers = 1
			}
		}
		return out, nil
	default:
		return nil, nerr.New(nerr.Internal, "executor.collectPlan", "unexpected plan node")
	}
}

func (s *Session) applyFilter(rows [][]types.Value, tab *catalog.Table, pred ast.Expr, op string) ([][]types.Value, error) {
	if pred == nil {
		return rows, nil
	}
	if tab == nil || len(rows) == 0 {
		var out [][]types.Value
		for _, row := range rows {
			ok, err := s.match(pred, tab, row)
			if err != nil {
				return nil, err
			}
			if ok {
				out = append(out, row)
			}
		}
		return out, nil
	}
	b := vector.New(tab.Types(), s.budget().BatchSize())
	var out [][]types.Value
	flush := func() error {
		if b.Count == 0 {
			return nil
		}
		if err := vector.Filter(b, tab, pred, s.eval); err != nil {
			return err
		}
		for i := 0; i < b.Count; i++ {
			out = append(out, b.Row(i))
			if s.trace != nil {
				if n := optimizer.Find(s.trace, op); n != nil {
					n.ActRows++
				}
			}
		}
		b.Reset()
		return nil
	}
	for _, row := range rows {
		if err := s.budget().Check(); err != nil {
			return nil, err
		}
		if !b.AppendRow(row) {
			if err := flush(); err != nil {
				return nil, err
			}
			b.AppendRow(row)
		}
	}
	if err := flush(); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Session) execJoin(n planner.Join) ([][]types.Value, error) {
	if rows, err, ok := s.tryPartitionWiseJoin(n); ok {
		return rows, err
	}
	left, err := s.collectPlan(n.Left)
	if err != nil {
		return nil, err
	}
	right, err := s.collectPlan(n.Right)
	if err != nil {
		return nil, err
	}
	schema := n.Schema
	if schema == nil {
		schema = tableOf(n)
	}
	if n.Kind == ast.JoinSemi || n.Kind == ast.JoinAnti {
		return s.execMarkJoin(n, left, right, schema)
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
	lTypes := joinLeftTypes(n, left)
	rTypes := joinRightTypes(n, right)
	var out [][]types.Value
	if n.Kind == ast.JoinFull {
		// v1 FULL is hash-only and memory-capped (no spill, no merge).
		if s.workers() > 1 && len(left)+len(right) >= 64 {
			out, err = join.ParallelHash(s.pool(), s.budget(), left, right, n.LeftKeys, n.RightKeys, n.Kind, lTypes, rTypes, pred)
		} else {
			out, err = join.HashJoin(left, right, n.LeftKeys, n.RightKeys, n.Kind, lTypes, rTypes, pred, s.budget())
		}
	} else if n.Method == "merge" && join.Sorted(left, n.LeftKeys) && join.Sorted(right, n.RightKeys) {
		out, err = join.MergeJoin(left, right, n.LeftKeys, n.RightKeys, n.Kind, rTypes, pred, s.budget())
	} else if !rankPreserving(n.Left) && s.workers() > 1 && len(left)+len(right) >= 64 {
		out, err = join.ParallelHash(s.pool(), s.budget(), left, right, n.LeftKeys, n.RightKeys, n.Kind, lTypes, rTypes, pred)
	} else {
		out, err = join.HashJoin(left, right, n.LeftKeys, n.RightKeys, n.Kind, lTypes, rTypes, pred, s.budget())
	}
	if err != nil {
		return nil, err
	}
	if s.trace != nil {
		op := "HashJoin"
		if n.Kind == ast.JoinFull {
			op = "FullJoin"
		} else if n.Kind == ast.JoinLeft {
			op = "LeftJoin"
		} else if n.Method == "merge" {
			op = "MergeJoin"
		} else if n.Cross || n.Kind == ast.JoinCross {
			op = "CrossJoin"
		}
		if node := optimizer.Find(s.trace, op); node != nil {
			node.ActRows = int64(len(out))
			node.Workers = s.workers()
		}
	}
	return out, nil
}

func (s *Session) execMarkJoin(n planner.Join, left, right [][]types.Value, schema *catalog.Table) ([][]types.Value, error) {
	var pred join.Pred
	if n.Pred != nil {
		pred = func(l, r []types.Value) (bool, error) {
			row := make([]types.Value, 0, len(l)+len(r))
			row = append(row, l...)
			row = append(row, r...)
			return s.match(n.Pred, schema, row)
		}
	}
	var out [][]types.Value
	var err error
	if n.Kind == ast.JoinAnti {
		out, err = join.HashAntiJoin(left, right, n.LeftKeys, n.RightKeys, pred, s.budget())
	} else {
		out, err = join.HashSemiJoin(left, right, n.LeftKeys, n.RightKeys, pred, s.budget())
	}
	if err != nil {
		return nil, err
	}
	if s.trace != nil {
		op := "HashSemiJoin"
		if n.Kind == ast.JoinAnti {
			op = "HashAntiJoin"
		}
		if node := optimizer.Find(s.trace, op); node != nil {
			node.ActRows = int64(len(out))
			node.Workers = s.workers()
		}
	}
	return out, nil
}

func joinLeftTypes(n planner.Join, left [][]types.Value) []types.Type {
	if tab := tableOf(n.Left); tab != nil {
		return tab.Types()
	}
	if n.Schema != nil {
		rightN := 0
		if rt := tableOf(n.Right); rt != nil {
			rightN = len(rt.Columns)
		}
		leftN := len(n.Schema.Columns) - rightN
		if leftN > 0 && leftN <= len(n.Schema.Columns) {
			out := make([]types.Type, leftN)
			for i := range out {
				out[i] = n.Schema.Columns[i].Type
			}
			return out
		}
	}
	if len(left) > 0 {
		out := make([]types.Type, len(left[0]))
		for i, v := range left[0] {
			out[i] = v.Typ
		}
		return out
	}
	return nil
}

func joinRightTypes(n planner.Join, right [][]types.Value) []types.Type {
	if tab := tableOf(n.Right); tab != nil {
		return tab.Types()
	}
	if n.Schema != nil {
		leftN := 0
		if lt := tableOf(n.Left); lt != nil {
			leftN = len(lt.Columns)
		}
		if leftN < len(n.Schema.Columns) {
			out := make([]types.Type, len(n.Schema.Columns)-leftN)
			for i := range out {
				out[i] = n.Schema.Columns[leftN+i].Type
			}
			return out
		}
	}
	if len(right) > 0 {
		out := make([]types.Type, len(right[0]))
		for i, v := range right[0] {
			out[i] = v.Typ
		}
		return out
	}
	return nil
}

func rankPreserving(p planner.Logical) bool {
	switch n := p.(type) {
	case planner.Search, planner.Nearest, planner.Rerank, planner.Candidates:
		return true
	case planner.Filter:
		return rankPreserving(n.Input)
	case planner.Limit:
		return rankPreserving(n.Input)
	case planner.Join:
		return rankPreserving(n.Left)
	default:
		return false
	}
}

func (s *Session) scanHeapBatch(tab *catalog.Table, low, high []types.Value, lowIncl, highIncl bool, op string) ([][]types.Value, error) {
	return s.scanHeapBatchPartitions(tab, nil, low, high, lowIncl, highIncl, op)
}

func (s *Session) scanHeapBatchPartitions(tab *catalog.Table, partitions []uint32, low, high []types.Value, lowIncl, highIncl bool, op string) ([][]types.Value, error) {
	start, end, err := encodeBounds(low, high, lowIncl, highIncl)
	if err != nil {
		return nil, err
	}
	if tab != nil && tab.Partitioning != nil {
		var rows [][]types.Value
		for _, part := range partitionSelection(tab, partitions) {
			heap, err := s.partitionHeap(tab, part.ID)
			if err != nil {
				return nil, err
			}
			partRows, err := s.oneRange(s.x.use(heap), tab, start, end, op)
			if err != nil {
				return nil, err
			}
			rows = append(rows, partRows...)
		}
		if err := s.hydrateRows(tab, rows); err != nil {
			return nil, err
		}
		return rows, nil
	}
	heap, err := s.heapOf(tab)
	if err != nil {
		return nil, err
	}
	htx := s.x.use(heap)
	w := s.workers()
	var rows [][]types.Value
	if w > 1 {
		splits, err := htx.SplitKeys(w)
		if err == nil && len(splits) > 0 {
			rows, err = s.parallelRanges(htx, tab, start, end, splits, op)
			if err != nil {
				return nil, err
			}
			if err := s.hydrateRows(tab, rows); err != nil {
				return nil, err
			}
			return rows, nil
		}
	}
	rows, err = s.oneRange(htx, tab, start, end, op)
	if err != nil {
		return nil, err
	}
	if err := s.hydrateRows(tab, rows); err != nil {
		return nil, err
	}
	return rows, nil
}

func (s *Session) oneRange(htx *btree.Txn, tab *catalog.Table, start, end []byte, op string) ([][]types.Value, error) {
	b := vector.New(tab.Types(), s.budget().BatchSize())
	var out [][]types.Value
	flush := func() {
		out = append(out, b.Rows()...)
		if s.trace != nil {
			if n := optimizer.Find(s.trace, op); n != nil {
				n.ActRows += int64(b.Count)
			}
		}
		b.Reset()
	}
	err := htx.Range(start, end, func(_, val []byte) error {
		if err := s.budget().Check(); err != nil {
			return err
		}
		if err := s.budget().ChargeIO(int64(len(val))); err != nil {
			return err
		}
		ok, err := vector.AppendEncoded(b, val, tab.Types())
		if err != nil {
			return err
		}
		if !ok {
			flush()
			ok, err = vector.AppendEncoded(b, val, tab.Types())
			if err != nil {
				return err
			}
			if !ok {
				return nerr.New(nerr.Internal, "executor.scanHeapBatch", "empty batch")
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if b.Count > 0 {
		flush()
	}
	return out, nil
}

func (s *Session) parallelRanges(htx *btree.Txn, tab *catalog.Table, start, end []byte, splits [][]byte, op string) ([][]types.Value, error) {
	ranges := make([][2][]byte, 0, len(splits)+1)
	prev := start
	for _, k := range splits {
		if start != nil && compareBytes(k, start) <= 0 {
			continue
		}
		if end != nil && compareBytes(k, end) >= 0 {
			break
		}
		ranges = append(ranges, [2][]byte{prev, k})
		prev = k
	}
	ranges = append(ranges, [2][]byte{prev, end})
	parts := make([][][]types.Value, len(ranges))
	tasks := make([]func() error, len(ranges))
	for i := range ranges {
		i := i
		tasks[i] = func() error {
			var got [][]types.Value
			err := htx.RangeVisible(ranges[i][0], ranges[i][1], func(_, val []byte) error {
				if err := s.budget().Check(); err != nil {
					return err
				}
				if err := s.budget().ChargeIO(int64(len(val))); err != nil {
					return err
				}
				row, err := s.decodeHeapRow(tab, val)
				if err != nil {
					return err
				}
				got = append(got, row)
				return nil
			})
			parts[i] = got
			return err
		}
	}
	if err := s.pool().Run(s.budget().Context(), s.workers(), tasks); err != nil {
		return nil, err
	}
	var all [][]types.Value
	for _, p := range parts {
		all = append(all, p...)
	}
	if s.trace != nil {
		if n := optimizer.Find(s.trace, op); n != nil {
			n.ActRows += int64(len(all))
			n.Workers = s.workers()
		}
	}
	return all, nil
}

func (s *Session) scanIndexBatch(n planner.IndexScan) ([][]types.Value, error) {
	var rows [][]types.Value
	err := s.scanIndex(n, func(row []types.Value) error {
		if err := s.budget().Check(); err != nil {
			return err
		}
		rows = append(rows, row)
		return nil
	})
	return rows, err
}

func compareBytes(a, b []byte) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] < b[i] {
			return -1
		}
		if a[i] > b[i] {
			return 1
		}
	}
	switch {
	case len(a) < len(b):
		return -1
	case len(a) > len(b):
		return 1
	default:
		return 0
	}
}
