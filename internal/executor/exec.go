package executor

import (
	"strings"

	"github.com/bzync/nextsql/internal/catalog"
	"github.com/bzync/nextsql/internal/fulltext"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/security"
	"github.com/bzync/nextsql/internal/sql/ast"
	"github.com/bzync/nextsql/internal/sql/binder"
	"github.com/bzync/nextsql/internal/sql/planner"
	"github.com/bzync/nextsql/internal/sql/types"
	"github.com/bzync/nextsql/internal/storage/btree"
	"github.com/bzync/nextsql/internal/wal"
)

const dmlChunk = 4096

func (s *Session) execPlan(plan planner.Logical) (*Result, error) {
	switch p := plan.(type) {
	case planner.CreateTable:
		return s.execCreateTable(p)
	case planner.CreateWorkflow:
		return s.execCreateWorkflow(p)
	case planner.RunWorkflow:
		return s.execRunWorkflow(p)
	case planner.AlterWorkflow:
		return s.execAlterWorkflow(p)
	case planner.DropWorkflow:
		return s.execDropWorkflow(p)
	case planner.CreateTrigger:
		return s.execCreateTrigger(p)
	case planner.AlterTrigger:
		return s.execAlterTrigger(p)
	case planner.DropTrigger:
		return s.execDropTrigger(p)
	case planner.CreateSchedule:
		return s.execCreateSchedule(p)
	case planner.AlterSchedule:
		return s.execAlterSchedule(p)
	case planner.DropSchedule:
		return s.execDropSchedule(p)
	case planner.CreateResourceGroup:
		return s.execCreateResourceGroup(p)
	case planner.AlterResourceGroup:
		return s.execAlterResourceGroup(p)
	case planner.DropResourceGroup:
		return s.execDropResourceGroup(p)
	case planner.ShowTasks:
		return s.execShowTasks(p)
	case planner.CancelTask:
		return s.execCancelTask(p)
	case planner.CreateDatabase:
		return s.execCreateDatabase(p)
	case planner.DropTable:
		return s.execDropTable(p)
	case planner.DropIndex:
		return s.execDropIndex(p)
	case planner.RebuildIndex:
		return s.execRebuildIndex(p)
	case planner.AlterTable:
		return s.execAlterTable(p)
	case planner.CreateIndex:
		return s.execCreateIndex(p)
	case planner.Insert:
		return s.execInsert(p)
	case planner.Upsert:
		return s.execUpsert(p)
	case planner.Update:
		return s.execUpdate(p)
	case planner.Delete:
		return s.execDelete(p)
	case planner.Analyze:
		return s.execAnalyze(p)
	case planner.Maintain:
		return nil, nerr.New(nerr.Internal, "executor.execPlan", "MAINTAIN must execute outside a SQL transaction")
	case planner.Empty:
		return &Result{Columns: append([]string(nil), p.Names...)}, nil
	case planner.SetOperation:
		return s.execSetOperation(p)
	case planner.With:
		return s.execWith(p)
	case planner.CTEScan, planner.Limit, planner.Sort, planner.Project, planner.Filter, planner.Scan, planner.SeqScan, planner.IndexScan, planner.Join, planner.Aggregate, planner.Window, planner.Search, planner.Facet, planner.Nearest, planner.Candidates, planner.Rerank:
		return s.execSelect(plan)
	default:
		return nil, nerr.New(nerr.Internal, "executor.execPlan", "unsupported plan")
	}
}

func (s *Session) execSetOperation(p planner.SetOperation) (*Result, error) {
	left, err := s.execPlan(p.Left)
	if err != nil {
		return nil, err
	}
	right, err := s.execPlan(p.Right)
	if err != nil {
		return nil, err
	}
	if len(left.Columns) != len(right.Columns) || len(left.Columns) != len(p.Names) {
		return nil, nerr.New(nerr.Internal, "executor.setOperation", "set-operation column count changed after binding")
	}
	left.Rows, right.Rows, err = coerceSetRows(left.Rows, right.Rows, len(p.Names))
	if err != nil {
		return nil, err
	}
	rows := make([][]types.Value, 0, len(left.Rows)+len(right.Rows))
	switch p.Op {
	case "intersect":
		rows, err = s.intersectRows(left.Rows, right.Rows)
	case "except":
		rows, err = s.exceptRows(left.Rows, right.Rows)
	default:
		rows = append(rows, left.Rows...)
		rows = append(rows, right.Rows...)
		if !p.All {
			rows, err = s.hashDistinct(rows)
		}
	}
	if err != nil {
		return nil, err
	}
	if len(rows) > s.budget().ResultRows() {
		return nil, nerr.New(nerr.Exhausted, "executor.setOperation", "result exceeds row limit")
	}
	return &Result{Columns: append([]string(nil), p.Names...), Rows: rows}, nil
}

func coerceSetRows(left, right [][]types.Value, cols int) ([][]types.Value, [][]types.Value, error) {
	common := make([]types.Type, cols)
	for col := 0; col < cols; col++ {
		for _, rows := range [][][]types.Value{left, right} {
			for _, row := range rows {
				if col >= len(row) {
					return nil, nil, nerr.New(nerr.Internal, "executor.setOperation", "set-operation row width mismatch")
				}
				if row[col].Null {
					continue
				}
				var err error
				common[col], err = commonSetType(common[col], row[col].Typ)
				if err != nil {
					return nil, nil, err
				}
			}
		}
	}
	coerce := func(rows [][]types.Value) error {
		for _, row := range rows {
			for col := range row {
				if common[col].Kind == types.KindInvalid || common[col].Kind == types.KindNull {
					continue
				}
				v, err := types.Coerce(row[col], common[col])
				if err != nil {
					return nerr.Wrap(nerr.InvalidArgument, "executor.setOperation", "set-operation column types are incompatible", err)
				}
				row[col] = v
			}
		}
		return nil
	}
	if err := coerce(left); err != nil {
		return nil, nil, err
	}
	if err := coerce(right); err != nil {
		return nil, nil, err
	}
	return left, right, nil
}

func commonSetType(a, b types.Type) (types.Type, error) {
	if a.Kind == types.KindInvalid || a.Kind == types.KindNull {
		return b, nil
	}
	if a.Equals(b) {
		return a, nil
	}
	if (a.Kind == types.KindString || a.Kind == types.KindText) && (b.Kind == types.KindString || b.Kind == types.KindText) {
		return types.Text(), nil
	}
	if a.Kind == types.KindDecimal && b.Kind == types.KindDecimal {
		scale := max(a.Scale, b.Scale)
		precision := max(a.Precision-a.Scale, b.Precision-b.Scale) + scale
		if precision > 38 {
			return types.Type{}, nerr.New(nerr.InvalidArgument, "executor.setOperation", "set-operation DECIMAL precision exceeds 38")
		}
		return types.DecimalType(precision, scale)
	}
	if a.Kind == b.Kind {
		return types.Type{}, nerr.New(nerr.InvalidArgument, "executor.setOperation", "set-operation column type parameters differ")
	}
	return types.Type{}, nerr.New(nerr.InvalidArgument, "executor.setOperation", "set-operation column types are incompatible")
}

func (s *Session) intersectRows(left, right [][]types.Value) ([][]types.Value, error) {
	rightSet, err := s.encodedRowSet(right)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{})
	out := make([][]types.Value, 0)
	for _, row := range left {
		key, err := types.EncodeRow(row)
		if err != nil {
			return nil, err
		}
		ks := string(key)
		if _, ok := rightSet[ks]; !ok {
			continue
		}
		if _, dup := seen[ks]; dup {
			continue
		}
		if err := s.budget().ChargeMem(int64(len(key) + 16)); err != nil {
			return nil, err
		}
		seen[ks] = struct{}{}
		out = append(out, row)
	}
	return out, nil
}

func (s *Session) exceptRows(left, right [][]types.Value) ([][]types.Value, error) {
	rightSet, err := s.encodedRowSet(right)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{})
	out := make([][]types.Value, 0)
	for _, row := range left {
		key, err := types.EncodeRow(row)
		if err != nil {
			return nil, err
		}
		ks := string(key)
		if _, excluded := rightSet[ks]; excluded {
			continue
		}
		if _, dup := seen[ks]; dup {
			continue
		}
		if err := s.budget().ChargeMem(int64(len(key) + 16)); err != nil {
			return nil, err
		}
		seen[ks] = struct{}{}
		out = append(out, row)
	}
	return out, nil
}

func (s *Session) encodedRowSet(rows [][]types.Value) (map[string]struct{}, error) {
	out := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		key, err := types.EncodeRow(row)
		if err != nil {
			return nil, err
		}
		if _, exists := out[string(key)]; exists {
			continue
		}
		if err := s.budget().ChargeMem(int64(len(key) + 16)); err != nil {
			return nil, err
		}
		out[string(key)] = struct{}{}
	}
	return out, nil
}

func (s *Session) execCreateTable(p planner.CreateTable) (*Result, error) {
	name := p.Table.Name
	history := catalog.IsHistoryTable(name)
	if catalog.ReservedName(name) && !history {
		return nil, nerr.New(nerr.InvalidArgument, "executor.CreateTable", "table name prefix nsql_ is reserved")
	}
	if _, ok := s.lookup(name); ok {
		return nil, nerr.New(nerr.AlreadyExists, "executor.CreateTable", "table already exists")
	}
	if history && !catalog.MatchHistoryDDL(s.execSQL) {
		return nil, nerr.New(nerr.InvalidArgument, "executor.CreateTable", "nsql_schema_migrations must use the reserved history DDL")
	}
	s.db.Eng.Enter(s.x.owner.Storage())
	heap, err := btree.CreateDetached(s.db.Eng)
	s.db.Eng.Leave(s.x.owner.Storage())
	if err != nil {
		return nil, err
	}
	p.Table.HeapMeta = heap.Meta()
	if p.Table.HasVector() {
		s.db.Eng.Enter(s.x.owner.Storage())
		vs, err := btree.CreateDetached(s.db.Eng)
		s.db.Eng.Leave(s.x.owner.Storage())
		if err != nil {
			return nil, err
		}
		p.Table.VecMeta = vs.Meta()
		s.pending.vecs[p.Table.Name] = vs
	}
	// Allocate per-partition physical trees.
	if p.Table.Partitioning != nil {
		if s.pending.partHeaps == nil {
			s.pending.partHeaps = make(map[string]*btree.Tree)
		}
		if s.pending.partVecs == nil {
			s.pending.partVecs = make(map[string]*btree.Tree)
		}
		if s.pending.partIdxs == nil {
			s.pending.partIdxs = make(map[string]*btree.Tree)
		}
		for i := range p.Table.Partitioning.Partitions {
			s.db.Eng.Enter(s.x.owner.Storage())
			ph, err := btree.CreateDetached(s.db.Eng)
			s.db.Eng.Leave(s.x.owner.Storage())
			if err != nil {
				return nil, err
			}
			p.Table.Partitioning.Partitions[i].HeapMeta = ph.Meta()
			s.pending.partHeaps[partitionHeapKey(p.Table.Name, p.Table.Partitioning.Partitions[i].ID)] = ph
			if p.Table.HasVector() {
				s.db.Eng.Enter(s.x.owner.Storage())
				pv, err := btree.CreateDetached(s.db.Eng)
				s.db.Eng.Leave(s.x.owner.Storage())
				if err != nil {
					return nil, err
				}
				p.Table.Partitioning.Partitions[i].VecMeta = pv.Meta()
				s.pending.partVecs[partitionHeapKey(p.Table.Name, p.Table.Partitioning.Partitions[i].ID)] = pv
			}
			// Partition-local secondary indexes: none at creation time.
			// Their metas will be allocated on CREATE INDEX.
		}
		if err := catalog.ValidatePartitioning(p.Table); err != nil {
			return nil, err
		}
	}
	raw, err := catalog.EncodeTable(p.Table)
	if err != nil {
		return nil, err
	}
	if err := s.x.use(s.db.CatTree).Insert(catalog.TableKey(p.Table.Name), raw); err != nil {
		return nil, err
	}
	s.overlay[p.Table.Name] = p.Table.Clone()
	s.pending.heaps[p.Table.Name] = heap
	s.db.Cat.SetNextID(p.Table.ID + 1)
	if history {
		if err := s.grantHistoryDML(); err != nil {
			return nil, err
		}
	}
	return &Result{}, nil
}

func (s *Session) grantHistoryDML() error {
	if s.acl == nil || strings.TrimSpace(s.user) == "" {
		return nil
	}
	for _, p := range []security.Privilege{
		security.PrivSelect, security.PrivInsert, security.PrivUpdate, security.PrivDelete,
	} {
		if err := s.acl.GrantInRealm(s.realmID, s.user, p, security.ScopeTable, catalog.HistoryTable); err != nil {
			return err
		}
	}
	return nil
}

func (s *Session) execCreateIndex(p planner.CreateIndex) (*Result, error) {
	tab, ok := s.lookup(p.Table.Name)
	if !ok {
		return nil, nerr.New(nerr.NotFound, "executor.CreateIndex", "unknown table")
	}
	for _, idx := range tab.Indexes {
		if idx.Name == p.Index.Name {
			return nil, nerr.New(nerr.AlreadyExists, "executor.CreateIndex", "index already exists")
		}
	}
	if tab.Partitioning != nil {
		neu, err := s.buildPartitionedIndex(tab, p.Index, nil)
		if err != nil {
			return nil, err
		}
		if err := s.putCatalog(neu, tab.Name); err != nil {
			return nil, err
		}
		return &Result{}, nil
	}
	built, err := s.buildIndex(tab, p.Index, nil)
	if err != nil {
		return nil, err
	}
	tab.Indexes = append(tab.Indexes, built)
	if err := s.putCatalog(tab, tab.Name); err != nil {
		return nil, err
	}
	return &Result{}, nil
}

func (s *Session) buildPartitionedIndex(tab *catalog.Table, idx catalog.Index, progress *rebuildProgress) (*catalog.Table, error) {
	if tab == nil || tab.Partitioning == nil {
		return nil, nerr.New(nerr.Internal, "executor.CreateIndex", "missing partition descriptor")
	}
	neu := tab.Clone()
	idx.Meta = 0 // the logical index is backed only by partition-local roots
	neu.Indexes = append(neu.Indexes, idx)
	if err := s.db.Eng.CrashAt(wal.PointDuringIndexBuild); err != nil {
		return nil, err
	}
	for i := range neu.Partitioning.Partitions {
		part := &neu.Partitioning.Partitions[i]
		heap, err := s.partitionHeap(tab, part.ID)
		if err != nil {
			return nil, err
		}
		s.db.Eng.Enter(s.x.owner.Storage())
		local, err := btree.CreateDetached(s.db.Eng)
		s.db.Eng.Leave(s.x.owner.Storage())
		if err != nil {
			return nil, err
		}
		key := partitionIndexKey(tab.Name, part.ID, idx.Name)
		s.pending.partIdxs[key] = local
		if idx.Vector {
			if err := s.buildPartitionVectorIndex(tab, idx, *part, s.x.use(heap), progress); err != nil {
				return nil, err
			}
		} else if err := s.populatePartitionIndex(tab, idx, s.x.use(heap), s.x.use(local), progress); err != nil {
			return nil, err
		}
		part.Indexes = append(part.Indexes, catalog.PartitionIndex{Name: idx.Name, Meta: local.Meta()})
	}
	if idx.Unique && !idx.Vector {
		if err := s.verifyCrossPartitionUnique(tab, idx); err != nil {
			return nil, err
		}
	}
	if err := catalog.ValidatePartitioning(neu); err != nil {
		return nil, err
	}
	return neu, nil
}

// populatePartitionIndex streams one local heap into one local index. Keeping
// this path streaming avoids an input-sized result buffer during DDL.
func (s *Session) populatePartitionIndex(tab *catalog.Table, idx catalog.Index, htx, itx *btree.Txn, progress *rebuildProgress) error {
	var st fulltext.Stats
	if err := htx.Range(nil, nil, func(_, val []byte) error {
		if err := s.budget().Check(); err != nil {
			return err
		}
		row, err := s.decodeHeapRow(tab, val)
		if err != nil {
			return err
		}
		pairs, err := s.indexPairs(tab, idx, row)
		if err != nil {
			return err
		}
		for _, pair := range pairs {
			if err := itx.Insert(pair.k, pair.v); err != nil {
				return err
			}
			if idx.Fulltext && fulltext.IsDocLenKey(pair.k) {
				n, err := fulltext.DecodeDocLen(pair.v)
				if err != nil {
					return err
				}
				st.Docs++
				st.Tokens += uint64(n)
			}
		}
		progress.add(1, int64(len(pairs)))
		return nil
	}); err != nil {
		return err
	}
	if idx.Fulltext {
		return itx.Insert(fulltext.StatsKey(), fulltext.EncodeStats(st))
	}
	return nil
}

func (s *Session) buildIndex(tab *catalog.Table, idx catalog.Index, progress *rebuildProgress) (catalog.Index, error) {
	s.db.Eng.Enter(s.x.owner.Storage())
	ix, err := btree.CreateDetached(s.db.Eng)
	s.db.Eng.Leave(s.x.owner.Storage())
	if err != nil {
		return catalog.Index{}, err
	}
	idx.Meta = ix.Meta()
	if err := s.db.Eng.CrashAt(wal.PointDuringIndexBuild); err != nil {
		return catalog.Index{}, err
	}
	heap, err := s.heapOf(tab)
	if err != nil {
		return catalog.Index{}, err
	}
	itx := s.x.use(ix)
	htx := s.x.use(heap)
	s.pending.idxs[idxKey(tab.Name, idx.Name)] = ix
	var pairs []kv
	w := s.workers()
	if idx.Vector {
		if idx.VecMethod == catalog.VecMethodIVF {
			if err := s.buildIVFIndex(tab, idx, htx, progress); err != nil {
				return catalog.Index{}, err
			}
		} else if idx.VecMethod == catalog.VecMethodIVFPQ {
			if err := s.buildIVFPQIndex(tab, idx, htx, progress); err != nil {
				return catalog.Index{}, err
			}
		} else if idx.VecMethod == catalog.VecMethodSPARSE {
			if err := s.buildSparseIndex(tab, idx, htx, progress); err != nil {
				return catalog.Index{}, err
			}
		} else if err := s.buildVectorIndex(tab, idx, htx, progress); err != nil {
			return catalog.Index{}, err
		}
	} else {
		if w > 1 {
			splits, _ := htx.SplitKeys(w)
			if len(splits) > 0 {
				parts := make([][]kv, len(splits)+1)
				ranges := make([][2][]byte, 0, len(splits)+1)
				var prev []byte
				for _, k := range splits {
					ranges = append(ranges, [2][]byte{prev, k})
					prev = k
				}
				ranges = append(ranges, [2][]byte{prev, nil})
				tasks := make([]func() error, len(ranges))
				for i := range ranges {
					i := i
					tasks[i] = func() error {
						var got []kv
						err := htx.RangeVisible(ranges[i][0], ranges[i][1], func(_, val []byte) error {
							row, err := s.decodeHeapRow(tab, val)
							if err != nil {
								return err
							}
							pairs, err := s.indexPairs(tab, idx, row)
							if err != nil {
								return err
							}
							got = append(got, pairs...)
							progress.add(1, int64(len(pairs)))
							return nil
						})
						parts[i] = got
						return err
					}
				}
				if err := s.pool().Run(s.budget().Context(), w, tasks); err != nil {
					return catalog.Index{}, err
				}
				for _, part := range parts {
					pairs = append(pairs, part...)
				}
			}
		}
		if pairs == nil {
			err = htx.Range(nil, nil, func(_, val []byte) error {
				row, err := s.decodeHeapRow(tab, val)
				if err != nil {
					return err
				}
				got, err := s.indexPairs(tab, idx, row)
				if err != nil {
					return err
				}
				pairs = append(pairs, got...)
				progress.add(1, int64(len(got)))
				return nil
			})
			if err != nil {
				return catalog.Index{}, err
			}
		}
		for _, pkv := range pairs {
			if err := itx.Insert(pkv.k, pkv.v); err != nil {
				return catalog.Index{}, err
			}
		}
		if idx.Fulltext {
			if err := writeFulltextStats(itx, pairs); err != nil {
				return catalog.Index{}, err
			}
		}
	}
	return idx, nil
}

func (s *Session) execInsert(p planner.Insert) (*Result, error) {
	tab, ok := s.lookup(p.Table.Name)
	if !ok {
		return nil, nerr.New(nerr.NotFound, "executor.Insert", "unknown table")
	}
	heap, err := s.heapOf(tab)
	if err != nil {
		return nil, err
	}
	htx := s.x.use(heap)
	var n int64
	var out [][]types.Value
	empty := make([]types.Value, len(tab.Columns))
	for i := range empty {
		empty[i] = types.Null(tab.Columns[i].Type)
	}
	for _, exprs := range p.Rows {
		row := append([]types.Value(nil), empty...)
		for j, ex := range exprs {
			v, err := s.evalInsertValue(ex, tab, p.Columns[j], row)
			if err != nil {
				return nil, err
			}
			v, err = types.Coerce(v, tab.Columns[p.Columns[j]].Type)
			if err != nil {
				return nil, err
			}
			row[p.Columns[j]] = v
		}
		for i := range row {
			nv, err := s.applyDefault(tab, i, row[i])
			if err != nil {
				return nil, err
			}
			if nv.Null && tab.Columns[i].NotNull {
				return nil, nerr.New(nerr.InvalidArgument, "executor.Insert", "NULL in NOT NULL column")
			}
			row[i] = nv
		}
		if err := s.checkLegacyTenantRow(tab, row); err != nil {
			return nil, err
		}
		if err := s.writeRow(tab, htx, row, true); err != nil {
			return nil, err
		}
		if err := s.collectReturning(&out, p.Returning, tab, row, nil); err != nil {
			return nil, err
		}
		n++
	}
	if err := s.maybeAutoAnalyze(tab, n); err != nil {
		return nil, err
	}
	return returningResult(p.Returning, out, n), nil
}

func (s *Session) execUpdate(p planner.Update) (*Result, error) {
	tab, ok := s.lookup(p.Table.Name)
	if !ok {
		return nil, nerr.New(nerr.NotFound, "executor.Update", "unknown table")
	}
	heap, err := s.heapOf(tab)
	if err != nil {
		return nil, err
	}
	htx := s.x.use(heap)
	src, pred, chunked := dmlSource(p.Input)
	if !chunked || src == nil || updateTouchesPK(tab, p.Sets) {
		return s.execUpdateBuffered(p, tab, htx)
	}
	var (
		affected int64
		after    []types.Value
		out      [][]types.Value
	)
	for {
		need := dmlChunk
		if p.Limit > 0 {
			left := p.Limit - affected
			if left <= 0 {
				break
			}
			if int(left) < need {
				need = int(left)
			}
		}
		batch, err := s.nextDMLBatch(tab, pred, after, need)
		if err != nil {
			return nil, err
		}
		if len(batch) == 0 {
			break
		}
		for _, row := range batch {
			neu, err := s.applyUpdate(tab, p.Sets, row)
			if err != nil {
				return nil, err
			}
			if err := s.replaceRow(tab, htx, row, neu); err != nil {
				return nil, err
			}
			if err := s.collectReturning(&out, p.Returning, tab, neu, nil); err != nil {
				return nil, err
			}
			affected++
		}
		after = tab.PKValues(batch[len(batch)-1])
	}
	if err := s.maybeAutoAnalyze(tab, affected); err != nil {
		return nil, err
	}
	s.recordAutomaticMaintenance(tab, affected)
	return returningResult(p.Returning, out, affected), nil
}

func (s *Session) execUpdateBuffered(p planner.Update, tab *catalog.Table, htx *btree.Txn) (*Result, error) {
	var olds, news [][]types.Value
	err := s.forEachRow(p.Input, func(row []types.Value) error {
		neu, err := s.applyUpdate(tab, p.Sets, row)
		if err != nil {
			return err
		}
		olds = append(olds, row)
		news = append(news, neu)
		return nil
	})
	if err != nil {
		return nil, err
	}
	var out [][]types.Value
	for i := range olds {
		if err := s.replaceRow(tab, htx, olds[i], news[i]); err != nil {
			return nil, err
		}
		if err := s.collectReturning(&out, p.Returning, tab, news[i], nil); err != nil {
			return nil, err
		}
	}
	if err := s.maybeAutoAnalyze(tab, int64(len(olds))); err != nil {
		return nil, err
	}
	s.recordAutomaticMaintenance(tab, int64(len(olds)))
	return returningResult(p.Returning, out, int64(len(olds))), nil
}

func (s *Session) applyUpdate(tab *catalog.Table, sets []binder.Set, row []types.Value) ([]types.Value, error) {
	neu := append([]types.Value(nil), row...)
	for _, set := range sets {
		v, err := s.eval(set.Expr, tab, row)
		if err != nil {
			return nil, err
		}
		v, err = types.Coerce(v, tab.Columns[set.Col].Type)
		if err != nil {
			return nil, err
		}
		if v.Null && tab.Columns[set.Col].NotNull {
			return nil, nerr.New(nerr.InvalidArgument, "executor.Update", "NULL in NOT NULL column")
		}
		neu[set.Col] = v
	}
	if err := s.checkLegacyTenantRow(tab, row); err != nil {
		return nil, err
	}
	if err := s.checkLegacyTenantRow(tab, neu); err != nil {
		return nil, err
	}
	return neu, nil
}

func (s *Session) execDelete(p planner.Delete) (*Result, error) {
	tab, ok := s.lookup(p.Table.Name)
	if !ok {
		return nil, nerr.New(nerr.NotFound, "executor.Delete", "unknown table")
	}
	heap, err := s.heapOf(tab)
	if err != nil {
		return nil, err
	}
	htx := s.x.use(heap)
	src, pred, chunked := dmlSource(p.Input)
	if !chunked || src == nil {
		return s.execDeleteBuffered(p, tab, htx)
	}
	var (
		affected int64
		out      [][]types.Value
	)
	for {
		need := dmlChunk
		if p.Limit > 0 {
			left := p.Limit - affected
			if left <= 0 {
				break
			}
			if int(left) < need {
				need = int(left)
			}
		}
		batch, err := s.nextDMLBatch(tab, pred, nil, need)
		if err != nil {
			return nil, err
		}
		if len(batch) == 0 {
			break
		}
		for _, row := range batch {
			if err := s.removeRow(tab, htx, row); err != nil {
				return nil, err
			}
			if err := s.collectReturning(&out, p.Returning, tab, row, nil); err != nil {
				return nil, err
			}
			affected++
		}
	}
	if err := s.maybeAutoAnalyze(tab, affected); err != nil {
		return nil, err
	}
	s.recordAutomaticMaintenance(tab, affected)
	return returningResult(p.Returning, out, affected), nil
}

func (s *Session) execDeleteBuffered(p planner.Delete, tab *catalog.Table, htx *btree.Txn) (*Result, error) {
	var rows [][]types.Value
	err := s.forEachRow(p.Input, func(row []types.Value) error {
		if err := s.checkLegacyTenantRow(tab, row); err != nil {
			return err
		}
		rows = append(rows, row)
		return nil
	})
	if err != nil {
		return nil, err
	}
	var out [][]types.Value
	for _, row := range rows {
		if err := s.removeRow(tab, htx, row); err != nil {
			return nil, err
		}
		if err := s.collectReturning(&out, p.Returning, tab, row, nil); err != nil {
			return nil, err
		}
	}
	if err := s.maybeAutoAnalyze(tab, int64(len(rows))); err != nil {
		return nil, err
	}
	s.recordAutomaticMaintenance(tab, int64(len(rows)))
	return returningResult(p.Returning, out, int64(len(rows))), nil
}

func updateTouchesPK(tab *catalog.Table, sets []binder.Set) bool {
	if tab == nil {
		return false
	}
	for _, set := range sets {
		for _, pk := range tab.PK {
			if set.Col == pk {
				return true
			}
		}
	}
	return false
}

func dmlSource(input planner.Logical) (*catalog.Table, ast.Expr, bool) {
	switch n := input.(type) {
	case planner.Scan:
		return n.Table, nil, true
	case planner.SeqScan:
		return n.Table, nil, true
	case planner.Filter:
		tab, inner, ok := dmlSource(n.Input)
		if !ok || inner != nil {
			return nil, nil, false
		}
		return tab, n.Pred, true
	default:
		return nil, nil, false
	}
}

func (s *Session) nextDMLBatch(tab *catalog.Table, pred ast.Expr, after []types.Value, limit int) ([][]types.Value, error) {
	if limit < 1 {
		limit = dmlChunk
	}
	var out [][]types.Value
	lowIncl := after == nil
	err := s.scanHeap(tab, after, nil, lowIncl, true, func(row []types.Value) error {
		if pred != nil {
			ok, err := s.match(pred, tab, row)
			if err != nil || !ok {
				return err
			}
		}
		out = append(out, append([]types.Value(nil), row...))
		if len(out) >= limit {
			return errStop
		}
		return nil
	})
	if err == errStop {
		err = nil
	}
	return out, err
}

func (s *Session) writeRow(tab *catalog.Table, htx *btree.Txn, row []types.Value, insert bool) error {
	if err := validateClientEncryptedRow(tab, row); err != nil {
		return err
	}
	event := ast.TriggerInsert
	if !insert {
		event = ast.TriggerUpdate
	}
	if err := s.fireTriggers(tab, event, ast.TriggerBefore, nil, row); err != nil {
		return err
	}
	if err := s.checkOutboundFKs(tab, row); err != nil {
		return err
	}
	if !insert {
		if err := s.checkInboundFKs(tab, row, row, false); err != nil {
			return err
		}
	}
	pk, err := types.EncodeKey(tab.PKValues(row))
	if err != nil {
		return err
	}
	if tab.Partitioning != nil {
		part, err := s.partitionForRow(tab, row)
		if err != nil {
			return err
		}
		hheap, err := s.partitionHeap(tab, part.ID)
		if err != nil {
			return err
		}
		htx = s.x.use(hheap)
	}
	if err := s.putVectors(tab, pk, row); err != nil {
		return err
	}
	payload, err := types.EncodeRow(detachVectors(tab, row))
	if err != nil {
		return err
	}
	if insert {
		if err := htx.Insert(pk, payload); err != nil {
			return err
		}
	} else {
		if err := htx.Update(pk, payload); err != nil {
			return err
		}
	}
	if insert {
		if err := s.maintainIndexes(tab, nil, row); err != nil {
			return err
		}
		if err := s.stageRowChange(tab, wal.ChangeInsert, nil, row); err != nil {
			return err
		}
		return s.fireTriggers(tab, event, ast.TriggerAfter, nil, row)
	}
	if err := s.stageRowChange(tab, wal.ChangeUpdate, row, row); err != nil {
		return err
	}
	return s.fireTriggers(tab, event, ast.TriggerAfter, nil, row)
}

func (s *Session) replaceRow(tab *catalog.Table, htx *btree.Txn, old, neu []types.Value) error {
	if err := validateClientEncryptedRow(tab, neu); err != nil {
		return err
	}
	if err := s.fireTriggers(tab, ast.TriggerUpdate, ast.TriggerBefore, old, neu); err != nil {
		return err
	}
	if err := s.checkOutboundFKs(tab, neu); err != nil {
		return err
	}
	// Probe children before the parent key moves so CASCADE UPDATE can
	// rewrite them after the new parent key is visible to outbound checks.
	work, err := s.collectInbound(tab, old, neu, false)
	if err != nil {
		return err
	}
	oldPK, err := types.EncodeKey(tab.PKValues(old))
	if err != nil {
		return err
	}
	newPK, err := types.EncodeKey(tab.PKValues(neu))
	if err != nil {
		return err
	}
	// Partitioned move: if partition changes, move between heaps regardless of PK change.
	if tab.Partitioning != nil {
		oldPart, err := s.partitionForRow(tab, old)
		if err != nil {
			return err
		}
		newPart, err := s.partitionForRow(tab, neu)
		if err != nil {
			return err
		}
		if oldPart.ID != newPart.ID {
			payload, err := types.EncodeRow(detachVectors(tab, neu))
			if err != nil {
				return err
			}
			oldHeap, err := s.partitionHeap(tab, oldPart.ID)
			if err != nil {
				return err
			}
			newHeap, err := s.partitionHeap(tab, newPart.ID)
			if err != nil {
				return err
			}
			trueOldPayload, err := s.heapDeleteReturningOld(s.x.use(oldHeap), oldPK)
			if err != nil {
				return err
			}
			if err := s.x.use(newHeap).Insert(newPK, payload); err != nil {
				return err
			}
			// Decode/hydrate before deleteVectors: hydrate re-attaches vector
			// data by looking it up in the vector store, which deleteVectors
			// is about to remove.
			trueOld, err := s.decodeHeapRow(tab, trueOldPayload)
			if err != nil {
				return err
			}
			if err := s.deleteVectors(tab, oldPK, old); err != nil {
				return err
			}
			if err := s.putVectors(tab, newPK, neu); err != nil {
				return err
			}
			if err := s.maintainIndexes(tab, trueOld, neu); err != nil {
				return err
			}
			if err := s.applyInboundWork(tab, work, old, neu, false); err != nil {
				return err
			}
			if err := s.stageRowChange(tab, wal.ChangeUpdate, old, neu); err != nil {
				return err
			}
			return s.fireTriggers(tab, ast.TriggerUpdate, ast.TriggerAfter, old, neu)
		}
		// Same partition: use partition heap txn.
		partHeap, err := s.partitionHeap(tab, oldPart.ID)
		if err != nil {
			return err
		}
		htx = s.x.use(partHeap)
	}
	payload, err := types.EncodeRow(detachVectors(tab, neu))
	if err != nil {
		return err
	}
	var trueOldPayload []byte
	if string(oldPK) != string(newPK) {
		p, err := s.heapDeleteReturningOld(htx, oldPK)
		if err != nil {
			return err
		}
		trueOldPayload = p
		if err := htx.Insert(newPK, payload); err != nil {
			return err
		}
	} else {
		p, err := s.heapUpdateReturningOld(htx, oldPK, payload)
		if err != nil {
			return err
		}
		trueOldPayload = p
	}
	// Decode/hydrate before deleteVectors: hydrate re-attaches vector data by
	// looking it up in the vector store, which deleteVectors is about to
	// remove.
	trueOld, err := s.decodeHeapRow(tab, trueOldPayload)
	if err != nil {
		return err
	}
	if string(oldPK) != string(newPK) {
		if err := s.deleteVectors(tab, oldPK, old); err != nil {
			return err
		}
	}
	if err := s.putVectors(tab, newPK, neu); err != nil {
		return err
	}
	if err := s.maintainIndexes(tab, trueOld, neu); err != nil {
		return err
	}
	if err := s.applyInboundWork(tab, work, old, neu, false); err != nil {
		return err
	}
	if err := s.stageRowChange(tab, wal.ChangeUpdate, old, neu); err != nil {
		return err
	}
	return s.fireTriggers(tab, ast.TriggerUpdate, ast.TriggerAfter, old, neu)
}

func (s *Session) removeRow(tab *catalog.Table, htx *btree.Txn, row []types.Value) error {
	if err := s.fireTriggers(tab, ast.TriggerDelete, ast.TriggerBefore, row, nil); err != nil {
		return err
	}
	if seen, err := s.fkMarkVisit(tab, row); err != nil {
		return err
	} else if seen {
		return nil
	}
	if err := s.checkInboundFKs(tab, row, nil, true); err != nil {
		return err
	}
	pk, err := types.EncodeKey(tab.PKValues(row))
	if err != nil {
		return err
	}
	if tab.Partitioning != nil {
		part, err := s.partitionForRow(tab, row)
		if err != nil {
			return err
		}
		hheap, err := s.partitionHeap(tab, part.ID)
		if err != nil {
			return err
		}
		htx = s.x.use(hheap)
	}
	trueOldPayload, err := s.heapDeleteReturningOld(htx, pk)
	if err != nil {
		return err
	}
	// Decode/hydrate before deleteVectors: hydrate re-attaches vector data by
	// looking it up in the vector store, which deleteVectors is about to
	// remove.
	trueOld, err := s.decodeHeapRow(tab, trueOldPayload)
	if err != nil {
		return err
	}
	if err := s.deleteVectors(tab, pk, row); err != nil {
		return err
	}
	if err := s.maintainIndexes(tab, trueOld, nil); err != nil {
		return err
	}
	if err := s.stageRowChange(tab, wal.ChangeDelete, row, nil); err != nil {
		return err
	}
	return s.fireTriggers(tab, ast.TriggerDelete, ast.TriggerAfter, row, nil)
}

func (s *Session) maintainIndexes(tab *catalog.Table, old, neu []types.Value) error {
	var err error
	if tab.Partitioning != nil && old != nil && neu != nil {
		oldPart, err := s.partitionForRow(tab, old)
		if err != nil {
			return err
		}
		newPart, err := s.partitionForRow(tab, neu)
		if err != nil {
			return err
		}
		if oldPart.ID != newPart.ID {
			// Cross-partition move: delete from old, insert into new.
			for _, idx := range tab.Indexes {
				if idx.Vector {
					if err := s.maintainCrossPartitionVectorIndex(tab, idx, *oldPart, *newPart, old, neu); err != nil {
						return err
					}
					continue
				}
				oldIx, err := s.partitionIndex(tab, oldPart.ID, idx)
				if err != nil {
					return err
				}
				newIx, err := s.partitionIndex(tab, newPart.ID, idx)
				if err != nil {
					return err
				}
				oldItx := s.x.use(oldIx)
				newItx := s.x.use(newIx)
				if ok, err := s.indexRowMatches(tab, idx, old); err != nil {
					return err
				} else if ok {
					pairs, err := s.indexPairs(tab, idx, old)
					if err != nil {
						return err
					}
					for _, pkv := range pairs {
						if err := s.treeDelete(oldItx, pkv.k); err != nil && !nerr.HasCode(err, nerr.NotFound) {
							return err
						}
					}
				}
				if ok, err := s.indexRowMatches(tab, idx, neu); err != nil {
					return err
				} else if ok {
					if err := s.checkCrossPartitionUnique(tab, idx, newItx, neu); err != nil {
						return err
					}
					pairs, err := s.indexPairs(tab, idx, neu)
					if err != nil {
						return err
					}
					for _, pkv := range pairs {
						if err := s.treeInsert(newItx, pkv.k, pkv.v); err != nil {
							return err
						}
					}
				}
				if idx.Fulltext {
					oldDoc, err := fulltextDoc(tab, idx, old)
					if err != nil {
						return err
					}
					newDoc, err := fulltextDoc(tab, idx, neu)
					if err != nil {
						return err
					}
					if oldDoc.Len > 0 {
						if err := adjustFulltextStats(oldItx, -1, -int64(oldDoc.Len)); err != nil {
							return err
						}
					}
					if newDoc.Len > 0 {
						if err := adjustFulltextStats(newItx, 1, int64(newDoc.Len)); err != nil {
							return err
						}
					}
				}
			}
			return nil
		}
	}
	for _, idx := range tab.Indexes {
		if idx.Vector {
			if tab.Partitioning != nil {
				prow := neu
				if prow == nil {
					prow = old
				}
				part, err := s.partitionForRow(tab, prow)
				if err != nil {
					return err
				}
				if err := s.maintainPartitionVectorIndex(tab, idx, *part, old, neu); err != nil {
					return err
				}
			} else if idx.VecMethod == catalog.VecMethodIVF {
				if err := s.maintainIVFIndex(tab, idx, old, neu); err != nil {
					return err
				}
			} else if idx.VecMethod == catalog.VecMethodIVFPQ {
				if err := s.maintainIVFPQIndex(tab, idx, old, neu); err != nil {
					return err
				}
			} else if idx.VecMethod == catalog.VecMethodSPARSE {
				if err := s.maintainSparseIndex(tab, idx, old, neu); err != nil {
					return err
				}
			} else if err := s.maintainVectorIndex(tab, idx, old, neu); err != nil {
				return err
			}
			continue
		}
		var itx *btree.Txn
		var realIx *btree.Tree
		if tab.Partitioning != nil {
			// For partitioned tables, secondary indexes are per-partition.
			// old and neu should be in same partition for non-moving updates, but for insert/delete we have only one.
			var row []types.Value
			if neu != nil {
				row = neu
			} else {
				row = old
			}
			if row != nil {
				part, err := s.partitionForRow(tab, row)
				if err != nil {
					return err
				}
				ix, err := s.partitionIndex(tab, part.ID, idx)
				if err != nil {
					return err
				}
				itx = s.x.use(ix)
			} else {
				ix, err := s.indexOf(tab, idx)
				if err != nil {
					return err
				}
				itx = s.x.use(ix)
			}
		} else {
			ix, err := s.indexOf(tab, idx)
			if err != nil {
				return err
			}
			itx = s.x.use(ix)
			realIx = ix
		}
		// While REBUILD INDEX ... ONLINE is (or was very recently) in progress
		// for this index, realIx may be the freshly-backfilled tree; use a
		// fresh snapshot for its writes so this transaction's own (possibly
		// older) snapshot cannot silently miss an entry the backfill just
		// committed. See freshTreeSnap's doc comment.
		online := realIx != nil && s.db.onlineBuildActive(idxKey(tab.Name, idx.Name))
		if idx.Fulltext && old != nil && neu != nil && sameFulltextRow(tab, idx, old, neu) {
			continue
		}
		if old != nil {
			ok, err := s.indexRowMatches(tab, idx, old)
			if err != nil {
				return err
			}
			if ok {
				pairs, err := s.indexPairs(tab, idx, old)
				if err != nil {
					return err
				}
				for _, pkv := range pairs {
					if online {
						snap, serr := s.freshTreeSnap()
						if serr != nil {
							return serr
						}
						if err := itx.DeleteAt(pkv.k, snap); err != nil && !nerr.HasCode(err, nerr.NotFound) {
							return err
						}
						continue
					}
					if err := s.treeDelete(itx, pkv.k); err != nil && !nerr.HasCode(err, nerr.NotFound) {
						return err
					}
				}
			}
		}
		if neu != nil {
			ok, err := s.indexRowMatches(tab, idx, neu)
			if err != nil {
				return err
			}
			if ok {
				if err := s.checkCrossPartitionUnique(tab, idx, itx, neu); err != nil {
					return err
				}
				pairs, err := s.indexPairs(tab, idx, neu)
				if err != nil {
					return err
				}
				for _, pkv := range pairs {
					if online {
						snap, serr := s.freshTreeSnap()
						if serr != nil {
							return serr
						}
						if err := itx.InsertAt(pkv.k, pkv.v, snap); err != nil {
							return err
						}
						continue
					}
					if err := s.treeInsert(itx, pkv.k, pkv.v); err != nil {
						return err
					}
				}
			}
		}
		if idx.Fulltext {
			var oldDoc, newDoc fulltext.Doc
			if old != nil {
				oldDoc, err = fulltextDoc(tab, idx, old)
				if err != nil {
					return err
				}
			}
			if neu != nil {
				newDoc, err = fulltextDoc(tab, idx, neu)
				if err != nil {
					return err
				}
			}
			var dDocs, dToks int64
			if oldDoc.Len > 0 {
				dDocs--
				dToks -= int64(oldDoc.Len)
			}
			if newDoc.Len > 0 {
				dDocs++
				dToks += int64(newDoc.Len)
			}
			if err := adjustFulltextStats(itx, dDocs, dToks); err != nil {
				return err
			}
		}
		if realIx != nil {
			if sh := s.db.onlineShadow(idxKey(tab.Name, idx.Name)); sh != nil && sh != realIx {
				if err := s.mirrorOnlineIndex(tab, idx, sh, old, neu); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

type kv struct{ k, v []byte }

func (s *Session) indexRowMatches(tab *catalog.Table, idx catalog.Index, row []types.Value) (bool, error) {
	if idx.Predicate == nil {
		return true, nil
	}
	return s.match(idx.Predicate, tab, row)
}

func (s *Session) indexPairs(tab *catalog.Table, idx catalog.Index, row []types.Value) ([]kv, error) {
	if idx.Vector {
		return nil, nil
	}
	ok, err := s.indexRowMatches(tab, idx, row)
	if err != nil || !ok {
		return nil, err
	}
	if idx.Fulltext {
		doc, err := fulltextDoc(tab, idx, row)
		if err != nil {
			return nil, err
		}
		if doc.Len == 0 {
			return nil, nil
		}
		pk, err := types.EncodeKey(tab.PKValues(row))
		if err != nil {
			return nil, err
		}
		raw := fulltext.EncodeDocPairs(pk, doc)
		out := make([]kv, len(raw))
		for i, p := range raw {
			out[i] = kv{p.K, p.V}
		}
		return out, nil
	}
	k, v, err := s.indexKV(tab, idx, row)
	if err != nil {
		return nil, err
	}
	return []kv{{k, v}}, nil
}

func fulltextDoc(tab *catalog.Table, idx catalog.Index, row []types.Value) (fulltext.Doc, error) {
	if len(idx.Columns) == 0 || len(idx.Columns) > fulltext.MaxFields {
		return fulltext.Doc{}, nerr.New(nerr.InvalidArgument, "executor.fulltextDoc", "FULLTEXT INDEX column count")
	}
	return analyzeSearchRow(row, idx.Columns, fulltext.Analyzer{ID: idx.FTAnalyzer, Version: idx.FTVersion})
}

func analyzeSearchRow(row []types.Value, cols []int, a fulltext.Analyzer) (fulltext.Doc, error) {
	if len(cols) == 0 || len(cols) > fulltext.MaxFields {
		return fulltext.Doc{}, nerr.New(nerr.InvalidArgument, "executor.analyzeSearchRow", "SEARCH column count")
	}
	texts := make([]string, len(cols))
	for i, ord := range cols {
		if ord < 0 || ord >= len(row) {
			return fulltext.Doc{}, nerr.New(nerr.InvalidArgument, "executor.analyzeSearchRow", "SEARCH column out of range")
		}
		v := row[ord]
		if v.Null {
			continue
		}
		if v.Typ.Kind != types.KindString && v.Typ.Kind != types.KindText {
			return fulltext.Doc{}, nerr.New(nerr.InvalidArgument, "executor.analyzeSearchRow", "SEARCH requires text")
		}
		texts[i] = v.Str
	}
	return fulltext.AnalyzeFields(texts, a)
}

func sameFulltextRow(tab *catalog.Table, idx catalog.Index, old, neu []types.Value) bool {
	if len(idx.Columns) == 0 {
		return false
	}
	oldPK, err := types.EncodeKey(tab.PKValues(old))
	if err != nil {
		return false
	}
	newPK, err := types.EncodeKey(tab.PKValues(neu))
	if err != nil {
		return false
	}
	if string(oldPK) != string(newPK) {
		return false
	}
	for _, ord := range idx.Columns {
		if ord < 0 || ord >= len(old) || ord >= len(neu) {
			return false
		}
		o, n := old[ord], neu[ord]
		if o.Null && n.Null {
			continue
		}
		if o.Null || n.Null || o.Str != n.Str {
			return false
		}
	}
	return true
}

func writeFulltextStats(itx *btree.Txn, pairs []kv) error {
	var st fulltext.Stats
	for _, p := range pairs {
		if !fulltext.IsDocLenKey(p.k) {
			continue
		}
		n, err := fulltext.DecodeDocLen(p.v)
		if err != nil {
			return err
		}
		st.Docs++
		st.Tokens += uint64(n)
	}
	if st.Docs == 0 {
		return nil
	}
	return itx.Insert(fulltext.StatsKey(), fulltext.EncodeStats(st))
}

func adjustFulltextStats(itx *btree.Txn, dDocs, dToks int64) error {
	if dDocs == 0 && dToks == 0 {
		return nil
	}
	raw, err := itx.Lookup(fulltext.StatsKey())
	if err != nil && !nerr.HasCode(err, nerr.NotFound) {
		return err
	}
	var st fulltext.Stats
	existed := err == nil
	if existed {
		st, err = fulltext.DecodeStats(raw)
		if err != nil {
			return err
		}
	}
	if dDocs < 0 && uint64(-dDocs) > st.Docs {
		st.Docs = 0
	} else {
		st.Docs = uint64(int64(st.Docs) + dDocs)
	}
	if dToks < 0 && uint64(-dToks) > st.Tokens {
		st.Tokens = 0
	} else {
		st.Tokens = uint64(int64(st.Tokens) + dToks)
	}
	if st.Docs == 0 {
		st.Tokens = 0
		if !existed {
			return nil
		}
		if err := itx.Delete(fulltext.StatsKey()); err != nil && !nerr.HasCode(err, nerr.NotFound) {
			return err
		}
		return nil
	}
	body := fulltext.EncodeStats(st)
	if existed {
		return itx.Update(fulltext.StatsKey(), body)
	}
	return itx.Insert(fulltext.StatsKey(), body)
}

func (s *Session) indexKV(tab *catalog.Table, idx catalog.Index, row []types.Value) ([]byte, []byte, error) {
	if idx.Spatial {
		if len(idx.Columns) != 1 {
			return nil, nil, nerr.New(nerr.InvalidArgument, "executor.indexKV", "spatial index column count")
		}
		pt := row[idx.Columns[0]]
		if pt.Null || pt.Typ.Kind != types.KindPoint {
			return nil, nil, nerr.New(nerr.InvalidArgument, "executor.indexKV", "spatial index requires POINT")
		}
		k, err := types.EncodeGeoKey(types.GeoHash64(pt.Lon, pt.Lat), tab.PKValues(row))
		if err != nil {
			return nil, nil, err
		}
		v, err := encodeIndexPayload(tab, idx, row)
		return k, v, err
	}
	var cols []types.Value
	for i, ord := range idx.Columns {
		var v types.Value
		var err error
		if idx.KeyIsExpr(i) {
			v, err = s.eval(idx.Exprs[i], tab, row)
			if err != nil {
				return nil, nil, err
			}
			if typ := idx.KeyType(tab, i); typ.Kind != 0 {
				v, err = types.Coerce(v, typ)
				if err != nil {
					return nil, nil, err
				}
			}
		} else {
			v = row[ord]
			if i == 0 && len(idx.Path) > 0 {
				if v.Null {
					v = types.Null(types.JSON())
				} else {
					extracted, err := types.ExtractJSON(v.JSON, idx.Path)
					if err != nil {
						return nil, nil, err
					}
					v = extracted
				}
			}
		}
		cols = append(cols, v)
	}
	var k []byte
	var err error
	if idx.Unique {
		k, err = types.EncodeKey(cols)
	} else {
		k, err = types.EncodeKey(append(cols, tab.PKValues(row)...))
	}
	if err != nil {
		return nil, nil, err
	}
	v, err := encodeIndexPayload(tab, idx, row)
	return k, v, err
}

func encodeIndexPayload(tab *catalog.Table, idx catalog.Index, row []types.Value) ([]byte, error) {
	vals := tab.PKValues(row)
	for _, ord := range idx.Include {
		if ord < 0 || ord >= len(row) {
			return nil, nerr.New(nerr.Internal, "executor.indexKV", "INCLUDE column out of range")
		}
		vals = append(vals, row[ord])
	}
	return types.EncodeKey(vals)
}

func indexPayloadTypes(tab *catalog.Table, idx catalog.Index) []types.Type {
	out := pkTypeList(tab)
	for _, ord := range idx.Include {
		if ord >= 0 && ord < len(tab.Columns) {
			out = append(out, tab.Columns[ord].Type)
		}
	}
	return out
}

func indexPKKey(tab *catalog.Table, idxVal []byte) ([]byte, error) {
	pk, err := types.DecodeKey(idxVal, pkTypeList(tab))
	if err != nil {
		return nil, err
	}
	return types.EncodeKey(pk)
}

func projectTable(n planner.Project) *catalog.Table {
	if len(n.Names) == 0 {
		return tableOf(n.Input)
	}
	in := tableOf(n.Input)
	out := &catalog.Table{}
	if in != nil {
		out.Name = in.Name
	}
	for i, name := range n.Names {
		col := catalog.Column{Name: name}
		if in != nil && i < len(n.Cols) && n.Cols[i] >= 0 && n.Cols[i] < len(in.Columns) {
			src := in.Columns[n.Cols[i]]
			col = src
			col.Name = name
		} else if in != nil {
			if j, ok := in.ColIndex(name); ok {
				col = in.Columns[j]
				col.Name = name
			}
		}
		out.Columns = append(out.Columns, col)
	}
	return out
}

func tableOf(p planner.Logical) *catalog.Table {
	switch n := p.(type) {
	case planner.With:
		return tableOf(n.Query)
	case planner.CTEScan:
		return n.Schema
	case planner.SetOperation:
		if t := tableOf(n.Left); t != nil {
			return t
		}
		return tableOf(n.Right)
	case planner.Sort:
		return tableOf(n.Input)
	case planner.Scan:
		return n.Table
	case planner.SeqScan:
		return n.Table
	case planner.IndexScan:
		return n.Table
	case planner.Search:
		if n.Table != nil {
			return n.Table
		}
		return tableOf(n.Input)
	case planner.Facet:
		if n.Table != nil {
			return n.Table
		}
		return tableOf(n.Input)
	case planner.Nearest:
		if n.Table != nil {
			return n.Table
		}
		return tableOf(n.Input)
	case planner.Candidates:
		if n.Table != nil {
			return n.Table
		}
		return tableOf(n.Input)
	case planner.Rerank:
		if n.Table != nil {
			return n.Table
		}
		return tableOf(n.Input)
	case planner.Filter:
		return tableOf(n.Input)
	case planner.Project:
		return projectTable(n)
	case planner.Limit:
		return tableOf(n.Input)
	case planner.Update:
		return n.Table
	case planner.Delete:
		return n.Table
	case planner.Join:
		if n.Kind == ast.JoinSemi || n.Kind == ast.JoinAnti {
			return tableOf(n.Left)
		}
		if n.Schema != nil {
			return n.Schema
		}
		if t := tableOf(n.Left); t != nil {
			return t
		}
		return tableOf(n.Right)
	case planner.Aggregate:
		if n.Schema != nil {
			return n.Schema
		}
		return tableOf(n.Input)
	case planner.Window:
		if n.Schema != nil {
			return n.Schema
		}
		return tableOf(n.Input)
	default:
		return nil
	}
}

type stopError struct{}

func (stopError) Error() string { return "stop" }

var errStop = stopError{}
