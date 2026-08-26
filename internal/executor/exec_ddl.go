package executor

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bzync/nextsql/internal/catalog"
	"github.com/bzync/nextsql/internal/crypto"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/sql/ast"
	"github.com/bzync/nextsql/internal/sql/planner"
	"github.com/bzync/nextsql/internal/sql/types"
	"github.com/bzync/nextsql/internal/storage/btree"
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
	for _, idx := range p.Table.Indexes {
		tr, err := s.indexOf(p.Table, idx)
		if err != nil {
			return nil, err
		}
		if err := s.queueTreeReclaim(tr); err != nil {
			return nil, err
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
	old, err := s.indexOf(tab, p.Index)
	if err != nil {
		return nil, err
	}
	if err := s.queueTreeReclaim(old); err != nil {
		return nil, err
	}
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
	old, err := s.indexOf(tab, p.Index)
	if err != nil {
		return nil, err
	}
	if err := s.queueTreeReclaim(old); err != nil {
		return nil, err
	}
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
	default:
		return nil, nerr.New(nerr.InvalidArgument, "executor.AlterTable", "unsupported ALTER TABLE command")
	}
	return &Result{}, nil
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
		if err := s.deleteVectors(old, k); err != nil {
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
