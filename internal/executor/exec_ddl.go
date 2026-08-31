package executor

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bzync/nextsql/internal/catalog"
	"github.com/bzync/nextsql/internal/crypto"
	"github.com/bzync/nextsql/internal/fulltext"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/sql/ast"
	"github.com/bzync/nextsql/internal/sql/planner"
	"github.com/bzync/nextsql/internal/sql/types"
	"github.com/bzync/nextsql/internal/storage/btree"
	"github.com/bzync/nextsql/internal/wal"
)

func (s *Session) execCreateDatabase(p planner.CreateDatabase) (*Result, error) {
	if s == nil || s.db == nil {
		return nil, nerr.New(nerr.Internal, "executor.CreateDatabase", "nil database")
	}
	if err := validateDBName(p.Name); err != nil {
		return nil, err
	}
	dir := filepath.Dir(s.db.path)
	if s.db.path == "" && s.db.Eng != nil {
		dir = filepath.Dir(s.db.Eng.Path())
	}
	if dir == "" {
		return nil, nerr.New(nerr.Unavailable, "executor.CreateDatabase", "database directory is unknown")
	}
	path := filepath.Join(dir, p.Name)
	if _, err := os.Stat(path); err == nil {
		if p.IfNotExists {
			return &Result{}, nil
		}
		return nil, nerr.New(nerr.AlreadyExists, "executor.CreateDatabase", "database already exists")
	} else if !os.IsNotExist(err) {
		return nil, nerr.Wrap(nerr.IO, "executor.CreateDatabase", "stat", err)
	}
	keys := s.db.keys
	if keys == nil && s.db.Eng != nil {
		keys = s.db.Eng.Keys()
	}
	if keys == nil {
		return nil, nerr.New(nerr.Unavailable, "executor.CreateDatabase", "key provider is not available")
	}
	// An Envelope is bound to the source file identity. Snapshot the
	// current DEK so the new file can be created without that binding.
	if _, ok := keys.(*crypto.Envelope); ok {
		dek, err := keys.Current()
		if err != nil {
			return nil, err
		}
		keys, err = crypto.NewMemoryKeyProvider(dek)
		if err != nil {
			return nil, err
		}
	}
	pages := s.db.bufferPages
	if pages < 1 {
		pages = 32
	}
	created, err := Create(path, keys, pages)
	if err != nil {
		return nil, err
	}
	_ = created.Close()
	return &Result{}, nil
}

func validateDBName(name string) error {
	if name == "" || name == "." || name == ".." {
		return nerr.New(nerr.InvalidArgument, "executor.CreateDatabase", "invalid database name")
	}
	if strings.ContainsAny(name, `/\:`) || strings.Contains(name, "..") {
		return nerr.New(nerr.InvalidArgument, "executor.CreateDatabase", "invalid database name")
	}
	if catalog.ReservedName(name) {
		return nerr.New(nerr.InvalidArgument, "executor.CreateDatabase", "database name prefix nsql_ is reserved")
	}
	return nil
}

func (s *Session) execDropTable(p planner.DropTable) (*Result, error) {
	if p.Table == nil {
		if p.IfExists {
			return &Result{}, nil
		}
		return nil, nerr.New(nerr.NotFound, "executor.DropTable", "unknown table")
	}
	name := p.Table.Name
	if catalog.ReservedName(name) && !catalog.IsHistoryTable(name) {
		return nil, nerr.New(nerr.InvalidArgument, "executor.DropTable", "table name prefix nsql_ is reserved")
	}
	if len(s.inboundFKs(p.Table)) > 0 {
		return nil, nerr.New(nerr.ForeignKey, "executor.DropTable", "table is referenced by a foreign key")
	}
	heap, err := s.heapOf(p.Table)
	if err != nil {
		return nil, err
	}
	if err := s.queueTreeReclaim(heap); err != nil {
		return nil, err
	}
	if p.Table.VecMeta != 0 {
		vec, err := s.vecOf(p.Table)
		if err != nil {
			return nil, err
		}
		if err := s.queueTreeReclaim(vec); err != nil {
			return nil, err
		}
	}
	if p.Table.Partitioning == nil {
		for _, idx := range p.Table.Indexes {
			tr, err := s.indexOf(p.Table, idx)
			if err != nil {
				return nil, err
			}
			if err := s.queueTreeReclaim(tr); err != nil {
				return nil, err
			}
		}
	}
	if p.Table.Partitioning != nil {
		for _, part := range p.Table.Partitioning.Partitions {
			ph, err := s.partitionHeap(p.Table, part.ID)
			if err != nil {
				return nil, err
			}
			if err := s.queueTreeReclaim(ph); err != nil {
				return nil, err
			}
			if part.VecMeta != 0 {
				pv, err := s.partitionVec(p.Table, part.ID)
				if err != nil {
					return nil, err
				}
				if err := s.queueTreeReclaim(pv); err != nil {
					return nil, err
				}
			}
			for _, pi := range part.Indexes {
				// Partition-local index metas are stored per partition; reclaim them.
				tr, err := s.partitionIndex(p.Table, part.ID, catalog.Index{Name: pi.Name, Meta: pi.Meta})
				if err == nil {
					if err := s.queueTreeReclaim(tr); err != nil {
						return nil, err
					}
				}
			}
		}
	}
	ctx := s.x.use(s.db.CatTree)
	if err := ctx.Delete(catalog.TableKey(name)); err != nil && !nerr.HasCode(err, nerr.NotFound) {
		return nil, err
	}
	if err := ctx.Delete(catalog.StatsKey(name)); err != nil && !nerr.HasCode(err, nerr.NotFound) {
		return nil, err
	}
	if err := s.deletePartitionStats(p.Table.ID); err != nil {
		return nil, err
	}
	if err := s.dropAIKeys(p.Table); err != nil {
		return nil, err
	}
	if s.overlay == nil {
		s.overlay = make(map[string]*catalog.Table)
	}
	s.overlay[name] = nil
	if s.pending != nil {
		if s.pending.dropped == nil {
			s.pending.dropped = make(map[string]struct{})
		}
		s.pending.dropped[name] = struct{}{}
		if s.pending.stats != nil {
			delete(s.pending.stats, name)
		}
	}
	return &Result{}, nil
}

func (s *Session) resolveDropIndex(drop ast.DropIndex) (ast.Stmt, error) {
	table, err := s.resolveIndexTable(drop.Name)
	if err != nil {
		return nil, err
	}
	drop.Table = table
	if drop.Table == "" && !drop.IfExists {
		return nil, nerr.New(nerr.NotFound, "executor.DropIndex", "unknown index")
	}
	return drop, nil
}

func (s *Session) resolveRebuildIndex(rebuild ast.RebuildIndex) (ast.Stmt, error) {
	table, err := s.resolveIndexTable(rebuild.Name)
	if err != nil {
		return nil, err
	}
	if table == "" {
		return nil, nerr.New(nerr.NotFound, "executor.RebuildIndex", "unknown index")
	}
	rebuild.Table = table
	return rebuild, nil
}

func (s *Session) resolveMaintainIndex(st ast.Maintain) (ast.Maintain, error) {
	table, err := s.resolveIndexTable(st.Index)
	if err != nil {
		return ast.Maintain{}, err
	}
	if table == "" {
		return ast.Maintain{}, nerr.New(nerr.NotFound, "executor.MaintainIndex", "unknown index")
	}
	st.Table = table
	return st, nil
}

func (s *Session) resolveIndexTable(indexName string) (string, error) {
	var table string
	names := make(map[string]struct{})
	if s.db != nil && s.db.Cat != nil {
		for _, tab := range s.db.Cat.List() {
			names[tab.Name] = struct{}{}
		}
	}
	for name, tab := range s.overlay {
		if tab == nil {
			delete(names, name)
		} else {
			names[name] = struct{}{}
		}
	}
	for name := range names {
		tab, ok := s.lookup(name)
		if !ok {
			continue
		}
		for _, idx := range tab.Indexes {
			if idx.Name != indexName {
				continue
			}
			if table != "" && table != tab.Name {
				return "", nerr.New(nerr.InvalidArgument, "executor.Index", "index name is ambiguous")
			}
			table = tab.Name
		}
	}
	return table, nil
}

func (s *Session) execDropIndex(p planner.DropIndex) (*Result, error) {
	if p.Table == nil {
		if p.IfExists {
			return &Result{}, nil
		}
		return nil, nerr.New(nerr.NotFound, "executor.DropIndex", "unknown index")
	}
	tab, ok := s.lookup(p.Table.Name)
	if !ok {
		return nil, nerr.New(nerr.NotFound, "executor.DropIndex", "unknown table")
	}
	pos := -1
	for i, idx := range tab.Indexes {
		if idx.Name == p.Name {
			pos = i
			p.Index = idx
			break
		}
	}
	if pos < 0 {
		if p.IfExists {
			return &Result{}, nil
		}
		return nil, nerr.New(nerr.NotFound, "executor.DropIndex", "unknown index")
	}
	if p.Index.Unique && s.uniqueIndexRequiredByFK(tab, pos) {
		return nil, nerr.New(nerr.ForeignKey, "executor.DropIndex", "unique index is required by a foreign key")
	}
	if tab.Partitioning != nil {
		if p.Index.Vector {
			s.dirtyHNSW = true
		}
		neu := tab.Clone()
		for i, part := range tab.Partitioning.Partitions {
			local, err := s.partitionIndex(tab, part.ID, p.Index)
			if err != nil {
				return nil, err
			}
			if err := s.queueTreeReclaim(local); err != nil {
				return nil, err
			}
			s.pending.indexDrops = append(s.pending.indexDrops, indexMapDrop{key: partitionIndexKey(tab.Name, part.ID, p.Index.Name), tree: local, partition: true})
			kept := neu.Partitioning.Partitions[i].Indexes[:0]
			for _, physical := range neu.Partitioning.Partitions[i].Indexes {
				if physical.Name != p.Index.Name {
					kept = append(kept, physical)
				}
			}
			neu.Partitioning.Partitions[i].Indexes = kept
		}
		neu.Indexes = append(neu.Indexes[:pos], neu.Indexes[pos+1:]...)
		if err := s.putCatalog(neu, tab.Name); err != nil {
			return nil, err
		}
		return &Result{}, nil
	}
	old, err := s.indexOf(tab, p.Index)
	if err != nil {
		return nil, err
	}
	if err := s.queueTreeReclaim(old); err != nil {
		return nil, err
	}
	s.pending.indexDrops = append(s.pending.indexDrops, indexMapDrop{key: idxKey(tab.Name, p.Index.Name), tree: old})
	neu := tab.Clone()
	neu.Indexes = append(neu.Indexes[:pos], neu.Indexes[pos+1:]...)
	if err := s.putCatalog(neu, tab.Name); err != nil {
		return nil, err
	}
	return &Result{}, nil
}

func (s *Session) execRebuildIndex(p planner.RebuildIndex) (res *Result, err error) {
	if p.Table == nil {
		return nil, nerr.New(nerr.NotFound, "executor.RebuildIndex", "unknown index")
	}
	tab, ok := s.lookup(p.Table.Name)
	if !ok {
		return nil, nerr.New(nerr.NotFound, "executor.RebuildIndex", "unknown table")
	}
	pos := -1
	for i, idx := range tab.Indexes {
		if idx.Name == p.Index.Name {
			pos = i
			p.Index = idx
			break
		}
	}
	if pos < 0 {
		return nil, nerr.New(nerr.NotFound, "executor.RebuildIndex", "unknown index")
	}
	if tab.Partitioning != nil {
		return s.rebuildPartitionedIndex(tab, pos, p.Index)
	}
	old, err := s.indexOf(tab, p.Index)
	if err != nil {
		return nil, err
	}
	if err := s.queueTreeReclaim(old); err != nil {
		return nil, err
	}
	s.pending.indexDrops = append(s.pending.indexDrops, indexMapDrop{key: idxKey(tab.Name, p.Index.Name), tree: old})
	progress := s.db.beginIndexRebuild(tab.Name, p.Index.Name)
	started := time.Now()
	defer func() {
		s.db.finishIndexRebuild(progress)
		if s.db.metrics != nil {
			s.db.metrics.ObserveIndexRebuild(progress.rows.Load(), progress.entries.Load(), time.Since(started), err)
		}
	}()
	built, err := s.buildIndex(tab, p.Index, progress)
	if err != nil {
		return nil, err
	}
	neu := tab.Clone()
	neu.Indexes[pos] = built
	progress.phase.Store("committing")
	if err := s.putCatalog(neu, tab.Name); err != nil {
		return nil, err
	}
	return &Result{}, nil
}

func (s *Session) rebuildPartitionedIndex(tab *catalog.Table, pos int, idx catalog.Index) (res *Result, err error) {
	progress := s.db.beginIndexRebuild(tab.Name, idx.Name)
	started := time.Now()
	defer func() {
		s.db.finishIndexRebuild(progress)
		if s.db.metrics != nil {
			s.db.metrics.ObserveIndexRebuild(progress.rows.Load(), progress.entries.Load(), time.Since(started), err)
		}
	}()
	if err := s.db.Eng.CrashAt(wal.PointDuringIndexBuild); err != nil {
		return nil, err
	}
	neu := tab.Clone()
	for i, part := range tab.Partitioning.Partitions {
		old, err := s.partitionIndex(tab, part.ID, idx)
		if err != nil {
			return nil, err
		}
		if err := s.queueTreeReclaim(old); err != nil {
			return nil, err
		}
		s.pending.indexDrops = append(s.pending.indexDrops, indexMapDrop{key: partitionIndexKey(tab.Name, part.ID, idx.Name), tree: old, partition: true})
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
		s.pending.partIdxs[partitionIndexKey(tab.Name, part.ID, idx.Name)] = local
		if idx.Vector {
			s.dirtyHNSW = true
			if err := s.buildPartitionVectorIndex(tab, idx, part, s.x.use(heap), progress); err != nil {
				return nil, err
			}
		} else if err := s.populatePartitionIndex(tab, idx, s.x.use(heap), s.x.use(local), progress); err != nil {
			return nil, err
		}
		found := false
		for j := range neu.Partitioning.Partitions[i].Indexes {
			if neu.Partitioning.Partitions[i].Indexes[j].Name == idx.Name {
				neu.Partitioning.Partitions[i].Indexes[j].Meta = local.Meta()
				found = true
				break
			}
		}
		if !found {
			return nil, nerr.New(nerr.Corruption, "executor.RebuildIndex", "partition-local index metadata missing")
		}
	}
	if idx.Unique && !idx.Vector {
		if err := s.verifyCrossPartitionUnique(tab, idx); err != nil {
			return nil, err
		}
	}
	neu.Indexes[pos].Meta = 0
	progress.phase.Store("committing")
	if err := s.putCatalog(neu, tab.Name); err != nil {
		return nil, err
	}
	return &Result{}, nil
}

func (s *Session) queueTreeReclaim(tree *btree.Tree) error {
	if s == nil || s.pending == nil || tree == nil {
		return nerr.New(nerr.Internal, "executor.reclaim", "missing transaction or tree")
	}
	pages, err := tree.OwnedPages()
	if err != nil {
		return err
	}
	s.pending.reclaims = append(s.pending.reclaims, pages...)
	return nil
}

func (s *Session) uniqueIndexRequiredByFK(parent *catalog.Table, drop int) bool {
	for _, in := range s.inboundFKs(parent) {
		if !sameOrdinals(in.fk.RefColumns, parent.Indexes[drop].Columns) {
			continue
		}
		if sameOrdinals(in.fk.RefColumns, parent.PK) {
			continue
		}
		hasReplacement := false
		for i, idx := range parent.Indexes {
			if i != drop && idx.Unique && sameOrdinals(in.fk.RefColumns, idx.Columns) {
				hasReplacement = true
				break
			}
		}
		if !hasReplacement {
			return true
		}
	}
	return false
}

func sameOrdinals(a, b []int) bool {
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

func (s *Session) execAlterTable(p planner.AlterTable) (*Result, error) {
	if p.Table == nil || p.Result == nil {
		return nil, nerr.New(nerr.Internal, "executor.AlterTable", "missing table")
	}
	old := p.Table
	neu := p.Result.Clone()
	switch cmd := p.Kind.(type) {
	case ast.AlterAddColumn:
		if err := s.alterAddColumn(old, neu, cmd); err != nil {
			return nil, err
		}
	case ast.AlterDropColumn:
		if err := s.alterDropColumn(old, neu, cmd); err != nil {
			return nil, err
		}
	case ast.AlterRenameColumn:
		if err := s.renameAIKey(old, cmd.Old, cmd.New); err != nil {
			return nil, err
		}
		if err := s.putCatalog(neu, old.Name); err != nil {
			return nil, err
		}
	case ast.AlterRenameTable:
		if err := s.alterRenameTable(old, neu); err != nil {
			return nil, err
		}
	case ast.AlterAddConstraint:
		if err := s.alterAddConstraint(old, neu); err != nil {
			return nil, err
		}
	case ast.AlterDropConstraint:
		if err := s.putCatalog(neu, old.Name); err != nil {
			return nil, err
		}
	case ast.AlterSetCDCImages:
		if err := s.putCatalog(neu, old.Name); err != nil {
			return nil, err
		}
	case ast.AlterAddPartition:
		if err := s.alterAddPartition(old, neu, cmd); err != nil {
			return nil, err
		}
	case ast.AlterDropPartition:
		if err := s.alterDropPartition(old, neu, cmd); err != nil {
			return nil, err
		}
	case ast.AlterAttachPartition:
		if err := s.alterAttachPartition(old, neu, p.Transfer, cmd); err != nil {
			return nil, err
		}
	case ast.AlterDetachPartition:
		if err := s.alterDetachPartition(old, neu, p.Transfer, cmd); err != nil {
			return nil, err
		}
	default:
		return nil, nerr.New(nerr.InvalidArgument, "executor.AlterTable", "unsupported ALTER TABLE command")
	}
	return &Result{}, nil
}

func (s *Session) alterAttachPartition(old, neu, source *catalog.Table, cmd ast.AlterAttachPartition) error {
	if old == nil || neu == nil || source == nil || old.Partitioning == nil || neu.Partitioning == nil ||
		len(neu.Partitioning.Partitions) != len(old.Partitioning.Partitions)+1 {
		return nerr.New(nerr.Internal, "executor.AlterTable", "invalid partition attachment")
	}
	if len(s.inboundFKs(source)) != 0 || len(source.ForeignKeys) != 0 {
		return nerr.New(nerr.ForeignKey, "executor.AlterTable", "attached table has foreign-key dependencies")
	}
	if err := s.rejectTableAutomationDependencies(source); err != nil {
		return err
	}
	part := &neu.Partitioning.Partitions[len(neu.Partitioning.Partitions)-1]
	if part.Name != cmd.Partition.Name || part.HeapMeta != source.HeapMeta || part.VecMeta != source.VecMeta {
		return nerr.New(nerr.Internal, "executor.AlterTable", "partition attachment mismatch")
	}
	heap, err := s.heapOf(source)
	if err != nil {
		return err
	}
	if err := s.validateAttachedRows(neu, source, part.ID, heap); err != nil {
		return err
	}
	s.pending.partHeaps[partitionHeapKey(old.Name, part.ID)] = heap
	if source.VecMeta != 0 {
		vec, err := s.vecOf(source)
		if err != nil {
			return err
		}
		s.pending.partVecs[partitionHeapKey(old.Name, part.ID)] = vec
	}
	for i, idx := range source.Indexes {
		if i >= len(part.Indexes) || part.Indexes[i].Name != idx.Name || part.Indexes[i].Meta != idx.Meta {
			return nerr.New(nerr.Corruption, "executor.AlterTable", "attached table index ownership mismatch")
		}
		tree, err := s.indexOf(source, idx)
		if err != nil {
			return err
		}
		s.pending.partIdxs[partitionIndexKey(old.Name, part.ID, idx.Name)] = tree
		if idx.Vector {
			// The source's process-local HNSW mem copy is keyed by the source
			// table name; force search to reload under the partition key.
			s.dirtyHNSW = true
		}
	}
	ctx := s.x.use(s.db.CatTree)
	if err := ctx.Delete(catalog.TableKey(source.Name)); err != nil {
		return err
	}
	if err := ctx.Delete(catalog.StatsKey(source.Name)); err != nil && !nerr.HasCode(err, nerr.NotFound) {
		return err
	}
	if err := s.dropAIKeys(source); err != nil {
		return err
	}
	if s.overlay == nil {
		s.overlay = make(map[string]*catalog.Table)
	}
	s.overlay[source.Name] = nil
	return s.putCatalog(neu, old.Name)
}

func (s *Session) alterDetachPartition(old, neu, detached *catalog.Table, cmd ast.AlterDetachPartition) error {
	if old == nil || neu == nil || detached == nil || old.Partitioning == nil || neu.Partitioning == nil ||
		len(neu.Partitioning.Partitions)+1 != len(old.Partitioning.Partitions) {
		return nerr.New(nerr.Internal, "executor.AlterTable", "invalid partition detachment")
	}
	var part *catalog.Partition
	for i := range old.Partitioning.Partitions {
		if old.Partitioning.Partitions[i].Name == cmd.Name {
			part = &old.Partitioning.Partitions[i]
			break
		}
	}
	if part == nil || detached.Name != part.Name || detached.HeapMeta != part.HeapMeta || detached.VecMeta != part.VecMeta {
		return nerr.New(nerr.Internal, "executor.AlterTable", "partition detachment mismatch")
	}
	heap, err := s.partitionHeap(old, part.ID)
	if err != nil {
		return err
	}
	if err := s.validateDetachedRows(detached, heap); err != nil {
		return err
	}
	s.pending.heaps[detached.Name] = heap
	if part.VecMeta != 0 {
		vec, err := s.partitionVec(old, part.ID)
		if err != nil {
			return err
		}
		s.pending.vecs[detached.Name] = vec
	}
	for i, idx := range detached.Indexes {
		if i >= len(part.Indexes) || part.Indexes[i].Name != idx.Name || part.Indexes[i].Meta != idx.Meta {
			return nerr.New(nerr.Corruption, "executor.AlterTable", "detached table index ownership mismatch")
		}
		tree, err := s.partitionIndex(old, part.ID, idx)
		if err != nil {
			return err
		}
		s.pending.idxs[idxKey(detached.Name, idx.Name)] = tree
		if idx.Vector {
			s.dirtyHNSW = true
		}
	}
	raw, err := catalog.EncodeTable(detached)
	if err != nil {
		return err
	}
	if err := s.x.use(s.db.CatTree).Insert(catalog.TableKey(detached.Name), raw); err != nil {
		return err
	}
	if s.overlay == nil {
		s.overlay = make(map[string]*catalog.Table)
	}
	s.overlay[detached.Name] = detached.Clone()
	s.pending.partitionDrops = append(s.pending.partitionDrops, partitionHeapKey(old.Name, part.ID))
	if err := s.deleteOnePartitionStats(old.ID, part.ID); err != nil {
		return err
	}
	return s.putCatalog(neu, old.Name)
}

func (s *Session) validateAttachedRows(parent, source *catalog.Table, partitionID uint32, heap *btree.Tree) error {
	uniques := crossPartitionUniqueIndexes(parent)
	var lockTx *btree.Txn
	if len(uniques) > 0 {
		for _, p := range parent.Partitioning.Partitions {
			if p.ID == partitionID {
				continue
			}
			ix, err := s.partitionIndex(parent, p.ID, uniques[0])
			if err != nil {
				return err
			}
			lockTx = s.x.use(ix)
			break
		}
	}
	return s.x.use(heap).Range(nil, nil, func(_, raw []byte) error {
		if err := s.budget().Check(); err != nil {
			return err
		}
		row, err := s.decodeHeapRow(source, raw)
		if err != nil {
			return err
		}
		part, err := parent.PartitionForRow(row)
		if err != nil || part == nil || part.ID != partitionID {
			return nerr.New(nerr.InvalidArgument, "executor.AlterTable", "attached table contains a row outside the partition rule")
		}
		for i := range parent.Columns {
			if parent.Columns[i].Default.Kind == catalog.DefAI {
				if err := s.bumpAI(parent, i, row[i]); err != nil {
					return err
				}
			}
		}
		for _, idx := range uniques {
			ok, err := s.indexRowMatches(parent, idx, row)
			if err != nil {
				return err
			}
			if !ok {
				continue
			}
			k, _, err := s.indexKV(parent, idx, row)
			if err != nil {
				return err
			}
			if lockTx != nil {
				if err := lockTx.LockExclusive(k); err != nil {
					return err
				}
			}
			dup, err := s.crossPartitionUniqueConflict(parent, idx, partitionID, k)
			if err != nil {
				return err
			}
			if dup {
				return nerr.New(nerr.AlreadyExists, "executor.AlterTable", "attached partition duplicates a UNIQUE key in an existing partition")
			}
		}
		return nil
	})
}

func (s *Session) validateDetachedRows(detached *catalog.Table, heap *btree.Tree) error {
	return s.x.use(heap).Range(nil, nil, func(_, raw []byte) error {
		if err := s.budget().Check(); err != nil {
			return err
		}
		row, err := s.decodeHeapRow(detached, raw)
		if err != nil {
			return err
		}
		for i := range detached.Columns {
			if detached.Columns[i].Default.Kind == catalog.DefAI {
				if err := s.bumpAI(detached, i, row[i]); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func (s *Session) rejectTableAutomationDependencies(tab *catalog.Table) error {
	for _, trigger := range s.listTriggers() {
		if trigger.TableID == tab.ID {
			return nerr.New(nerr.InvalidArgument, "executor.AlterTable", "attached table has trigger dependencies")
		}
	}
	for _, workflow := range s.listWorkflows() {
		for _, dep := range workflow.Dependencies {
			if dep.Kind == catalog.WorkflowDependencyTable && dep.ID == tab.ID {
				return nerr.New(nerr.InvalidArgument, "executor.AlterTable", "attached table has workflow dependencies")
			}
		}
	}
	return nil
}

func (s *Session) alterAddPartition(old, neu *catalog.Table, cmd ast.AlterAddPartition) error {
	if old.Partitioning == nil || neu.Partitioning == nil || len(neu.Partitioning.Partitions) != len(old.Partitioning.Partitions)+1 {
		return nerr.New(nerr.Internal, "executor.AlterTable", "invalid partition addition")
	}
	part := &neu.Partitioning.Partitions[len(neu.Partitioning.Partitions)-1]
	if part.Name != cmd.Partition.Name {
		return nerr.New(nerr.Internal, "executor.AlterTable", "partition addition mismatch")
	}
	s.db.Eng.Enter(s.x.owner.Storage())
	heap, err := btree.CreateDetached(s.db.Eng)
	s.db.Eng.Leave(s.x.owner.Storage())
	if err != nil {
		return err
	}
	part.HeapMeta = heap.Meta()
	s.pending.partHeaps[partitionHeapKey(old.Name, part.ID)] = heap
	if neu.HasVector() {
		s.db.Eng.Enter(s.x.owner.Storage())
		vec, err := btree.CreateDetached(s.db.Eng)
		s.db.Eng.Leave(s.x.owner.Storage())
		if err != nil {
			return err
		}
		part.VecMeta = vec.Meta()
		s.pending.partVecs[partitionHeapKey(old.Name, part.ID)] = vec
	}
	for i := range part.Indexes {
		idx := indexByName(neu, part.Indexes[i].Name)
		s.db.Eng.Enter(s.x.owner.Storage())
		local, err := btree.CreateDetached(s.db.Eng)
		s.db.Eng.Leave(s.x.owner.Storage())
		if err != nil {
			return err
		}
		if idx.Fulltext {
			if err := s.x.use(local).Insert(fulltext.StatsKey(), fulltext.EncodeStats(fulltext.Stats{})); err != nil {
				return err
			}
		}
		part.Indexes[i].Meta = local.Meta()
		s.pending.partIdxs[partitionIndexKey(old.Name, part.ID, idx.Name)] = local
		if idx.Vector {
			if err := s.initPartitionVectorIndex(neu, idx, *part); err != nil {
				return err
			}
		}
	}
	if err := catalog.ValidatePartitioning(neu); err != nil {
		return err
	}
	return s.putCatalog(neu, old.Name)
}

func (s *Session) alterDropPartition(old, neu *catalog.Table, cmd ast.AlterDropPartition) error {
	if old.Partitioning == nil || neu.Partitioning == nil || len(neu.Partitioning.Partitions)+1 != len(old.Partitioning.Partitions) {
		return nerr.New(nerr.Internal, "executor.AlterTable", "invalid partition removal")
	}
	var dropped *catalog.Partition
	for i := range old.Partitioning.Partitions {
		if old.Partitioning.Partitions[i].Name == cmd.Name {
			dropped = &old.Partitioning.Partitions[i]
			break
		}
	}
	if dropped == nil {
		return nerr.New(nerr.NotFound, "executor.AlterTable", "unknown partition")
	}
	rows, err := s.countHeapPartitions(old, []uint32{dropped.ID}, nil, nil, true, true)
	if err != nil {
		return err
	}
	if rows != 0 {
		return nerr.New(nerr.InvalidArgument, "executor.AlterTable", "cannot drop a non-empty partition")
	}
	heap, err := s.partitionHeap(old, dropped.ID)
	if err != nil {
		return err
	}
	if err := s.queueTreeReclaim(heap); err != nil {
		return err
	}
	if dropped.VecMeta != 0 {
		vec, err := s.partitionVec(old, dropped.ID)
		if err != nil {
			return err
		}
		if err := s.queueTreeReclaim(vec); err != nil {
			return err
		}
	}
	for _, idx := range dropped.Indexes {
		tree, err := s.partitionIndex(old, dropped.ID, catalog.Index{Name: idx.Name, Meta: idx.Meta})
		if err != nil {
			return err
		}
		if err := s.queueTreeReclaim(tree); err != nil {
			return err
		}
		if indexByName(old, idx.Name).Vector {
			s.dirtyHNSW = true
		}
	}
	s.pending.partitionDrops = append(s.pending.partitionDrops, partitionHeapKey(old.Name, dropped.ID))
	if err := s.deleteOnePartitionStats(old.ID, dropped.ID); err != nil {
		return err
	}
	return s.putCatalog(neu, old.Name)
}

func (s *Session) alterAddColumn(old, neu *catalog.Table, cmd ast.AlterAddColumn) error {
	n, err := s.countHeap(old, nil, nil, true, true)
	if err != nil {
		return err
	}
	added := neu.Columns[len(neu.Columns)-1]
	if added.NotNull && added.Default.Kind == catalog.DefNone && n > 0 {
		return nerr.New(nerr.InvalidArgument, "executor.AlterTable", "cannot add a NOT NULL column without a default to a non-empty table")
	}
	if neu.HasVector() && neu.VecMeta == 0 {
		if _, err := s.ensureVec(neu); err != nil {
			return err
		}
	}
	if err := s.rewriteHeapRows(old, neu, func(row []types.Value) ([]types.Value, error) {
		out := make([]types.Value, len(neu.Columns))
		copy(out, row)
		for i := len(row); i < len(neu.Columns); i++ {
			v := types.Null(neu.Columns[i].Type)
			nv, err := s.applyDefault(neu, i, v)
			if err != nil {
				return nil, err
			}
			if nv.Null && neu.Columns[i].NotNull {
				return nil, nerr.New(nerr.InvalidArgument, "executor.AlterTable", "NULL in NOT NULL column")
			}
			out[i] = nv
		}
		return out, nil
	}); err != nil {
		return err
	}
	if cmd.Column.References != nil {
		if err := s.validateExistingFKs(neu); err != nil {
			return err
		}
	}
	return s.putCatalog(neu, old.Name)
}

func (s *Session) alterDropColumn(old, neu *catalog.Table, cmd ast.AlterDropColumn) error {
	idx, ok := old.ColIndex(cmd.Name)
	if !ok {
		return nerr.New(nerr.NotFound, "executor.AlterTable", "unknown column")
	}
	for _, in := range s.inboundFKs(old) {
		for _, c := range in.fk.RefColumns {
			if c == idx {
				return nerr.New(nerr.ForeignKey, "executor.AlterTable", "column is referenced by a foreign key")
			}
		}
	}
	if err := s.rewriteHeapRows(old, neu, func(row []types.Value) ([]types.Value, error) {
		out := make([]types.Value, 0, len(row)-1)
		for i, v := range row {
			if i == idx {
				continue
			}
			out = append(out, v)
		}
		return out, nil
	}); err != nil {
		return err
	}
	if old.Columns[idx].Default.Kind == catalog.DefAI {
		if err := s.deleteAIKey(old.ID, old.Columns[idx].Name); err != nil {
			return err
		}
	}
	return s.putCatalog(neu, old.Name)
}

func (s *Session) alterRenameTable(old, neu *catalog.Table) error {
	if old.Name == neu.Name {
		return s.putCatalog(neu, old.Name)
	}
	if _, ok := s.lookup(neu.Name); ok {
		return nerr.New(nerr.AlreadyExists, "executor.AlterTable", "table already exists")
	}
	raw, err := catalog.EncodeTable(neu)
	if err != nil {
		return err
	}
	ctx := s.x.use(s.db.CatTree)
	if err := ctx.Delete(catalog.TableKey(old.Name)); err != nil {
		return err
	}
	if err := ctx.Insert(catalog.TableKey(neu.Name), raw); err != nil {
		return err
	}
	if st, ok := s.lookupStats(old.Name); ok {
		st.Table = neu.Name
		body, err := catalog.EncodeStats(st)
		if err != nil {
			return err
		}
		if err := ctx.Delete(catalog.StatsKey(old.Name)); err != nil && !nerr.HasCode(err, nerr.NotFound) {
			return err
		}
		if err := ctx.Insert(catalog.StatsKey(neu.Name), body); err != nil {
			return err
		}
		if s.pending != nil {
			if s.pending.stats == nil {
				s.pending.stats = make(map[string]*catalog.TableStats)
			}
			delete(s.pending.stats, old.Name)
			s.pending.stats[neu.Name] = st
		}
	}
	if err := s.rewriteInboundRefTable(old.Name, neu.Name); err != nil {
		return err
	}
	if s.overlay == nil {
		s.overlay = make(map[string]*catalog.Table)
	}
	s.overlay[old.Name] = nil
	s.overlay[neu.Name] = neu
	if s.pending != nil {
		if s.pending.renames == nil {
			s.pending.renames = make(map[string]string)
		}
		s.pending.renames[old.Name] = neu.Name
		if tr, ok := s.pending.heaps[old.Name]; ok {
			s.pending.heaps[neu.Name] = tr
			delete(s.pending.heaps, old.Name)
		}
		if tr, ok := s.pending.vecs[old.Name]; ok {
			s.pending.vecs[neu.Name] = tr
			delete(s.pending.vecs, old.Name)
		}
		moved := make(map[string]*btree.Tree)
		prefix := old.Name + "/"
		for k, tr := range s.pending.idxs {
			if strings.HasPrefix(k, prefix) {
				moved[neu.Name+"/"+k[len(prefix):]] = tr
				delete(s.pending.idxs, k)
			}
		}
		for k, tr := range moved {
			s.pending.idxs[k] = tr
		}
	}
	return nil
}

func (s *Session) rewriteInboundRefTable(oldName, newName string) error {
	names := make(map[string]struct{})
	if s.overlay != nil {
		for name, t := range s.overlay {
			if t != nil {
				names[name] = struct{}{}
			}
		}
	}
	if s.db != nil && s.db.Cat != nil {
		for _, t := range s.db.Cat.List() {
			names[t.Name] = struct{}{}
		}
	}
	for name := range names {
		if name == oldName {
			continue
		}
		t, ok := s.lookup(name)
		if !ok {
			continue
		}
		changed := false
		for i := range t.ForeignKeys {
			if t.ForeignKeys[i].RefTable == oldName {
				t.ForeignKeys[i].RefTable = newName
				changed = true
			}
		}
		if !changed {
			continue
		}
		if err := s.putCatalog(t, t.Name); err != nil {
			return err
		}
	}
	return nil
}

func (s *Session) alterAddConstraint(old, neu *catalog.Table) error {
	if err := s.validateExistingFKs(neu); err != nil {
		return err
	}
	return s.putCatalog(neu, old.Name)
}

func (s *Session) validateExistingFKs(tab *catalog.Table) error {
	if tab == nil || len(tab.ForeignKeys) == 0 {
		return nil
	}
	heap, err := s.heapOf(tab)
	if err != nil {
		return err
	}
	htx := s.x.use(heap)
	return htx.Range(nil, nil, func(_, val []byte) error {
		row, err := s.decodeHeapRow(tab, val)
		if err != nil {
			// During ADD CONSTRAINT the row still matches the current catalog
			// column count; decode with the pre-constraint descriptor if needed.
			return err
		}
		return s.checkOutboundFKs(tab, row)
	})
}

func (s *Session) putCatalog(tab *catalog.Table, keyName string) error {
	raw, err := catalog.EncodeTable(tab)
	if err != nil {
		return err
	}
	ctx := s.x.use(s.db.CatTree)
	if err := ctx.Update(catalog.TableKey(keyName), raw); err != nil {
		return err
	}
	if s.overlay == nil {
		s.overlay = make(map[string]*catalog.Table)
	}
	s.overlay[tab.Name] = tab
	return nil
}

func (s *Session) rewriteHeapRows(old, neu *catalog.Table, mapRow func([]types.Value) ([]types.Value, error)) error {
	heap, err := s.heapOf(old)
	if err != nil {
		return err
	}
	htx := s.x.use(heap)
	type pair struct{ k, v []byte }
	var pairs []pair
	err = htx.Range(nil, nil, func(k, val []byte) error {
		if err := s.budget().Check(); err != nil {
			return err
		}
		row, err := s.decodeHeapRow(old, val)
		if err != nil {
			return err
		}
		mapped, err := mapRow(row)
		if err != nil {
			return err
		}
		if err := s.deleteVectors(old, k, row); err != nil {
			return err
		}
		if err := s.putVectors(neu, k, mapped); err != nil {
			return err
		}
		payload, err := types.EncodeRow(detachVectors(neu, mapped))
		if err != nil {
			return err
		}
		pairs = append(pairs, pair{append([]byte(nil), k...), payload})
		return nil
	})
	if err != nil {
		return err
	}
	for _, p := range pairs {
		if err := htx.Update(p.k, p.v); err != nil {
			return err
		}
	}
	return nil
}
