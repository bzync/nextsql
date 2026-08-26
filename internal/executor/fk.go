package executor

import (
	"github.com/bzync/nextsql/internal/catalog"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/security"
	"github.com/bzync/nextsql/internal/sql/types"
	"github.com/bzync/nextsql/internal/storage/btree"
	"github.com/bzync/nextsql/internal/txn"
)

type fkChildWork struct {
	child *catalog.Table
	fk    catalog.ForeignKey
	row   []types.Value
	act   catalog.FKAction
}

type inboundFK struct {
	child *catalog.Table
	fk    catalog.ForeignKey
}

func fkErr() error {
	return nerr.New(nerr.ForeignKey, "executor.fk", "foreign key violation")
}

func fkReferences(fk catalog.ForeignKey, parent *catalog.Table) bool {
	if parent == nil {
		return false
	}
	if parent.ID != 0 && fk.RefTableID == parent.ID {
		return true
	}
	if fk.RefTable != parent.Name {
		return false
	}
	// Overlay-only parent: IDs may not have landed in the catalog yet.
	return parent.ID == 0 || fk.RefTableID == 0 || fk.RefTableID == parent.ID
}

func (s *Session) fkTM() (*txn.Handle, *txn.Manager, error) {
	if s == nil || s.x == nil || s.x.owner == nil || s.db == nil || s.db.Eng == nil || s.db.Eng.TM == nil {
		return nil, nil, nerr.New(nerr.Internal, "executor.fk", "transaction manager missing")
	}
	h := s.x.owner.Handle()
	if h == nil {
		return nil, nil, nerr.New(nerr.Internal, "executor.fk", "transaction handle missing")
	}
	return h, s.db.Eng.TM, nil
}

func (s *Session) fkNote(ok bool) {
	if s == nil || s.db == nil || s.db.metrics == nil {
		return
	}
	s.db.metrics.AddFKCheck()
	if !ok {
		s.db.metrics.AddFKViolation()
	}
}

func (s *Session) resetFKStmt() {
	if s == nil {
		return
	}
	s.fkProbes = 0
	s.fkDepth = 0
	s.fkTouched = 0
	s.fkVisited = nil
}

func (s *Session) fkVisitKey(tab *catalog.Table, row []types.Value) (string, error) {
	if tab == nil {
		return "", nerr.New(nerr.Internal, "executor.fk", "missing table")
	}
	pk, err := types.EncodeKey(tab.PKValues(row))
	if err != nil {
		return "", err
	}
	return tab.Name + "\x00" + string(pk), nil
}

func (s *Session) fkMarkVisit(tab *catalog.Table, row []types.Value) (bool, error) {
	key, err := s.fkVisitKey(tab, row)
	if err != nil {
		return false, err
	}
	if s.fkVisited == nil {
		s.fkVisited = make(map[string]struct{})
	}
	if _, seen := s.fkVisited[key]; seen {
		return true, nil
	}
	s.fkVisited[key] = struct{}{}
	return false, nil
}

func (s *Session) fkSeen(tab *catalog.Table, row []types.Value) bool {
	if s == nil || s.fkVisited == nil {
		return false
	}
	key, err := s.fkVisitKey(tab, row)
	if err != nil {
		return false
	}
	_, ok := s.fkVisited[key]
	return ok
}

func (s *Session) fkTouchedCap() int {
	if s != nil && s.fkMaxTouched > 0 {
		return s.fkMaxTouched
	}
	return security.MaxFKTouchedRows
}

func (s *Session) fkCapErr(msg string) error {
	if s != nil {
		s.fkBroken = true
		if s.db != nil && s.db.metrics != nil {
			s.db.metrics.AddFKCascadeReject()
		}
	}
	return nerr.New(nerr.Exhausted, "executor.fk", msg)
}

func (s *Session) chargeFKTouched() error {
	if s.fkTouched >= s.fkTouchedCap() {
		return s.fkCapErr("foreign key cascade exceeded row limit")
	}
	s.fkTouched++
	if s.db != nil && s.db.metrics != nil {
		s.db.metrics.AddFKCascadeRows(1)
	}
	return nil
}

func (s *Session) touchFKCascade() error {
	if s.fkDepth >= security.MaxFKDepth {
		return s.fkCapErr("foreign key cascade exceeded depth limit")
	}
	return nil
}

func (s *Session) fkWriteSnap() (txn.Snapshot, bool, error) {
	if s == nil || (s.fkDepth == 0 && !s.conflictWrite) {
		return txn.Snapshot{}, false, nil
	}
	h, tm, err := s.fkTM()
	if err != nil {
		return txn.Snapshot{}, false, err
	}
	return tm.Capture(h.ID), true, nil
}

func (s *Session) treeDelete(tx *btree.Txn, key []byte) error {
	snap, ok, err := s.fkWriteSnap()
	if err != nil {
		return err
	}
	if ok {
		return tx.DeleteAt(key, snap)
	}
	return tx.Delete(key)
}

func (s *Session) treeInsert(tx *btree.Txn, key, val []byte) error {
	snap, ok, err := s.fkWriteSnap()
	if err != nil {
		return err
	}
	if ok {
		return tx.InsertAt(key, val, snap)
	}
	return tx.Insert(key, val)
}

func (s *Session) treeUpdate(tx *btree.Txn, key, val []byte) error {
	snap, ok, err := s.fkWriteSnap()
	if err != nil {
		return err
	}
	if ok {
		return tx.UpdateAt(key, val, snap)
	}
	return tx.Update(key, val)
}

func (s *Session) treeLookup(tx *btree.Txn, key []byte) ([]byte, error) {
	snap, ok, err := s.fkWriteSnap()
	if err != nil {
		return nil, err
	}
	if ok {
		return tx.LookupAt(key, snap)
	}
	return tx.Lookup(key)
}

func inboundAction(fk catalog.ForeignKey, deleting bool) catalog.FKAction {
	if deleting {
		return fk.OnDelete
	}
	return fk.OnUpdate
}

func (s *Session) heapDelete(htx *btree.Txn, pk []byte) error {
	return s.treeDelete(htx, pk)
}

func (s *Session) heapUpdate(htx *btree.Txn, pk, payload []byte) error {
	return s.treeUpdate(htx, pk, payload)
}

func sameEncodedPK(tab *catalog.Table, a, b []types.Value) bool {
	if tab == nil {
		return false
	}
	ka, err := types.EncodeKey(tab.PKValues(a))
	if err != nil {
		return false
	}
	kb, err := types.EncodeKey(tab.PKValues(b))
	if err != nil {
		return false
	}
	return string(ka) == string(kb)
}

func fkAnyNull(row []types.Value, cols []int) bool {
	for _, i := range cols {
		if i < 0 || i >= len(row) || row[i].Null {
			return true
		}
	}
	return false
}

func sameIntSet(a, b []int) bool {
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

func uniqueIndexOn(tab *catalog.Table, cols []int) (catalog.Index, bool) {
	if tab == nil {
		return catalog.Index{}, false
	}
	for _, idx := range tab.Indexes {
		if !idx.Unique || idx.Fulltext || idx.Vector || idx.Spatial || len(idx.Path) > 0 || idx.HasExpr() || idx.Predicate != nil {
			continue
		}
		if sameIntSet(idx.Columns, cols) {
			return idx, true
		}
	}
	return catalog.Index{}, false
}

func btreeIndexOn(tab *catalog.Table, cols []int) (catalog.Index, bool) {
	if tab == nil {
		return catalog.Index{}, false
	}
	for _, idx := range tab.Indexes {
		if idx.Fulltext || idx.Vector || idx.Spatial || len(idx.Path) > 0 || idx.HasExpr() {
			continue
		}
		if sameIntSet(idx.Columns, cols) {
			return idx, true
		}
	}
	return catalog.Index{}, false
}

func refsPK(tab *catalog.Table, cols []int) bool {
	return tab != nil && sameIntSet(tab.PK, cols)
}

func childValsForRef(row []types.Value, fk catalog.ForeignKey, dest []int) []types.Value {
	pos := make(map[int]int, len(fk.RefColumns))
	for i, o := range fk.RefColumns {
		pos[o] = i
	}
	out := make([]types.Value, len(dest))
	for i, o := range dest {
		p, ok := pos[o]
		if !ok || p >= len(fk.Columns) {
			continue
		}
		ci := fk.Columns[p]
		if ci >= 0 && ci < len(row) {
			out[i] = row[ci]
		}
	}
	return out
}

func parentValsForChild(row []types.Value, fk catalog.ForeignKey, dest []int) []types.Value {
	pos := make(map[int]int, len(fk.Columns))
	for i, o := range fk.Columns {
		pos[o] = i
	}
	out := make([]types.Value, len(dest))
	for i, o := range dest {
		p, ok := pos[o]
		if !ok || p >= len(fk.RefColumns) {
			continue
		}
		ri := fk.RefColumns[p]
		if ri >= 0 && ri < len(row) {
			out[i] = row[ri]
		}
	}
	return out
}

func (s *Session) childRefKey(parent *catalog.Table, fk catalog.ForeignKey, childRow []types.Value) ([]byte, error) {
	if refsPK(parent, fk.RefColumns) {
		return types.EncodeKey(childValsForRef(childRow, fk, parent.PK))
	}
	idx, ok := uniqueIndexOn(parent, fk.RefColumns)
	if !ok {
		return nil, nerr.New(nerr.Internal, "executor.fk", "referenced key is not PRIMARY KEY or UNIQUE")
	}
	return types.EncodeKey(childValsForRef(childRow, fk, idx.Columns))
}

func (s *Session) parentRefKey(parent *catalog.Table, fk catalog.ForeignKey, parentRow []types.Value) ([]byte, error) {
	if refsPK(parent, fk.RefColumns) {
		return types.EncodeKey(parent.PKValues(parentRow))
	}
	idx, ok := uniqueIndexOn(parent, fk.RefColumns)
	if !ok {
		return nil, nerr.New(nerr.Internal, "executor.fk", "referenced key is not PRIMARY KEY or UNIQUE")
	}
	k, _, err := s.indexKV(parent, idx, parentRow)
	return k, err
}

func refColsChanged(fk catalog.ForeignKey, old, neu []types.Value) bool {
	if neu == nil {
		return true
	}
	for _, i := range fk.RefColumns {
		if i < 0 || i >= len(old) || i >= len(neu) {
			return true
		}
		if old[i].Null != neu[i].Null {
			return true
		}
		if old[i].Null {
			continue
		}
		cmp, err := old[i].Cmp(neu[i])
		if err != nil || cmp != 0 {
			return true
		}
	}
	return false
}

func (s *Session) checkOutboundFKs(tab *catalog.Table, row []types.Value) error {
	if tab == nil || len(tab.ForeignKeys) == 0 {
		return nil
	}
	s.fkProbes = 0
	for _, fk := range tab.ForeignKeys {
		if err := s.checkOutboundFK(tab, fk, row); err != nil {
			return err
		}
	}
	return nil
}

func (s *Session) checkOutboundFK(child *catalog.Table, fk catalog.ForeignKey, row []types.Value) error {
	if fkAnyNull(row, fk.Columns) {
		return nil
	}
	parent, ok := s.lookup(fk.RefTable)
	if !ok {
		s.fkNote(false)
		return fkErr()
	}
	refKey, err := s.childRefKey(parent, fk, row)
	if err != nil {
		return err
	}
	h, tm, err := s.fkTM()
	if err != nil {
		return err
	}
	if err := tm.LockKey(h, refKey, txn.Shared); err != nil {
		return err
	}
	probe := tm.Capture(h.ID)
	found, parentRow, err := s.lookupParentAt(parent, fk, refKey, probe)
	if err != nil {
		return err
	}
	if !found || !s.tenantVisible(parent, parentRow) {
		s.fkNote(false)
		return fkErr()
	}
	s.fkNote(true)
	return nil
}

func (s *Session) lookupParentAt(parent *catalog.Table, fk catalog.ForeignKey, refKey []byte, probe txn.Snapshot) (bool, []types.Value, error) {
	heap, err := s.heapOf(parent)
	if err != nil {
		return false, nil, err
	}
	htx := s.x.use(heap)
	if refsPK(parent, fk.RefColumns) {
		raw, err := htx.LookupAt(refKey, probe)
		if err != nil {
			if nerr.HasCode(err, nerr.NotFound) {
				return false, nil, nil
			}
			return false, nil, err
		}
		row, err := s.decodeHeapRow(parent, raw)
		if err != nil {
			return false, nil, err
		}
		return true, row, nil
	}
	idx, ok := uniqueIndexOn(parent, fk.RefColumns)
	if !ok {
		return false, nil, nerr.New(nerr.Internal, "executor.fk", "referenced key is not PRIMARY KEY or UNIQUE")
	}
	ix, err := s.indexOf(parent, idx)
	if err != nil {
		return false, nil, err
	}
	itx := s.x.use(ix)
	pkRaw, err := itx.LookupAt(refKey, probe)
	if err != nil {
		if nerr.HasCode(err, nerr.NotFound) {
			return false, nil, nil
		}
		return false, nil, err
	}
	raw, err := htx.LookupAt(pkRaw, probe)
	if err != nil {
		if nerr.HasCode(err, nerr.NotFound) {
			return false, nil, nil
		}
		return false, nil, err
	}
	row, err := s.decodeHeapRow(parent, raw)
	if err != nil {
		return false, nil, err
	}
	return true, row, nil
}

func (s *Session) checkInboundFKs(tab *catalog.Table, old, neu []types.Value, deleting bool) error {
	work, err := s.collectInbound(tab, old, neu, deleting)
	if err != nil {
		return err
	}
	return s.applyInboundWork(tab, work, old, neu, deleting)
}

func (s *Session) collectInbound(parent *catalog.Table, old, neu []types.Value, deleting bool) ([]fkChildWork, error) {
	inbounds := s.inboundFKs(parent)
	if len(inbounds) == 0 {
		return nil, nil
	}
	if err := s.checkTenantRow(parent, old); err != nil {
		return nil, err
	}
	s.fkProbes = 0
	var work []fkChildWork
	for _, in := range inbounds {
		if !deleting && !refColsChanged(in.fk, old, neu) {
			continue
		}
		if fkAnyNull(old, in.fk.RefColumns) {
			continue
		}
		refKey, err := s.parentRefKey(parent, in.fk, old)
		if err != nil {
			return nil, err
		}
		h, tm, err := s.fkTM()
		if err != nil {
			return nil, err
		}
		if err := tm.LockKey(h, refKey, txn.Exclusive); err != nil {
			return nil, err
		}
		probe := tm.Capture(h.ID)
		act := inboundAction(in.fk, deleting)
		if act == catalog.FKRestrict {
			exists, err := s.childExistsAt(in.child, in.fk, old, probe)
			if err != nil {
				return nil, err
			}
			if exists {
				s.fkNote(false)
				return nil, fkErr()
			}
			s.fkNote(true)
			continue
		}
		rows, err := s.collectChildrenAt(in.child, in.fk, old, probe)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			work = append(work, fkChildWork{child: in.child, fk: in.fk, row: row, act: act})
		}
		s.fkNote(true)
	}
	return work, nil
}

func (s *Session) applyInboundWork(parent *catalog.Table, work []fkChildWork, old, neu []types.Value, deleting bool) error {
	for _, w := range work {
		if deleting && w.act == catalog.FKCascade && s.fkSeen(w.child, w.row) {
			continue
		}
		src := w.row
		// Self-ref ON UPDATE: parent heap already moved; rewrite the live new row.
		if !deleting && parent != nil && w.child != nil && w.child.Name == parent.Name && sameEncodedPK(w.child, w.row, old) {
			src = cloneRow(neu)
		}
		if err := s.checkTenantRow(w.child, src); err != nil {
			return err
		}
		if err := s.touchFKCascade(); err != nil {
			return err
		}
		s.fkDepth++
		err := s.applyOneChild(w, src, old, neu, deleting)
		s.fkDepth--
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Session) applyOneChild(w fkChildWork, src, oldParent, newParent []types.Value, deleting bool) error {
	heap, err := s.heapOf(w.child)
	if err != nil {
		return err
	}
	htx := s.x.use(heap)
	switch w.act {
	case catalog.FKCascade:
		if deleting {
			return s.removeRow(w.child, htx, src)
		}
		return s.replaceRow(w.child, htx, src, rewriteChildFK(src, w.fk, newParent))
	case catalog.FKSetNull:
		return s.replaceRow(w.child, htx, src, nullChildFK(w.child, src, w.fk))
	case catalog.FKSetDefault:
		neu, err := defaultChildFK(s, w.child, src, w.fk)
		if err != nil {
			s.fkNote(false)
			return err
		}
		// ApplyDefault(i, liveValue) would keep the old key; we evaluated
		// from Null. If the default still names this parent, fail closed.
		still, err := fkRowMatches(neu, w.fk, oldParent)
		if err != nil {
			return err
		}
		if still {
			s.fkNote(false)
			return fkErr()
		}
		return s.replaceRow(w.child, htx, src, neu)
	default:
		s.fkNote(false)
		return fkErr()
	}
}

func rewriteChildFK(row []types.Value, fk catalog.ForeignKey, newParent []types.Value) []types.Value {
	neu := cloneRow(row)
	for i, c := range fk.Columns {
		if c < 0 || c >= len(neu) {
			continue
		}
		r := fk.RefColumns[i]
		if r < 0 || r >= len(newParent) {
			continue
		}
		neu[c] = newParent[r].Clone()
	}
	return neu
}

func nullChildFK(child *catalog.Table, row []types.Value, fk catalog.ForeignKey) []types.Value {
	neu := cloneRow(row)
	for _, c := range fk.Columns {
		if c < 0 || c >= len(neu) || c >= len(child.Columns) {
			continue
		}
		neu[c] = types.Null(child.Columns[c].Type)
	}
	return neu
}

func defaultChildFK(s *Session, child *catalog.Table, row []types.Value, fk catalog.ForeignKey) ([]types.Value, error) {
	neu := cloneRow(row)
	for _, c := range fk.Columns {
		if c < 0 || c >= len(neu) || c >= len(child.Columns) {
			continue
		}
		nv, err := s.applyDefault(child, c, types.Null(child.Columns[c].Type))
		if err != nil {
			return nil, err
		}
		if nv.Null && child.Columns[c].NotNull {
			return nil, fkErr()
		}
		neu[c] = nv
	}
	return neu, nil
}

func (s *Session) childExistsAt(child *catalog.Table, fk catalog.ForeignKey, parentRow []types.Value, probe txn.Snapshot) (bool, error) {
	if idx, ok := btreeIndexOn(child, fk.Columns); ok {
		found, err := s.probeChildIndex(child, idx, fk, parentRow, probe)
		if err != nil || found {
			return found, err
		}
		return false, nil
	}
	return s.probeChildHeap(child, fk, parentRow, probe)
}

func (s *Session) collectChildrenAt(child *catalog.Table, fk catalog.ForeignKey, parentRow []types.Value, probe txn.Snapshot) ([][]types.Value, error) {
	if idx, ok := btreeIndexOn(child, fk.Columns); ok {
		return s.collectChildIndex(child, idx, fk, parentRow, probe)
	}
	return s.collectChildHeap(child, fk, parentRow, probe)
}

func (s *Session) collectChildIndex(child *catalog.Table, idx catalog.Index, fk catalog.ForeignKey, parentRow []types.Value, probe txn.Snapshot) ([][]types.Value, error) {
	ix, err := s.indexOf(child, idx)
	if err != nil {
		return nil, err
	}
	itx := s.x.use(ix)
	vals := parentValsForChild(parentRow, fk, idx.Columns)
	prefix, err := types.EncodeKey(vals)
	if err != nil {
		return nil, err
	}
	var out [][]types.Value
	if idx.Unique {
		if err := s.chargeFKProbe(); err != nil {
			return nil, err
		}
		pkRaw, err := itx.LookupAt(prefix, probe)
		if err != nil {
			if nerr.HasCode(err, nerr.NotFound) {
				return nil, nil
			}
			return nil, err
		}
		row, err := s.childRowAt(child, pkRaw, probe)
		if err != nil || row == nil {
			return nil, err
		}
		ok, err := fkRowMatches(row, fk, parentRow)
		if err != nil || !ok {
			return nil, err
		}
		if err := s.chargeFKTouched(); err != nil {
			return nil, err
		}
		return [][]types.Value{row}, nil
	}
	end := types.PrefixEnd(prefix)
	err = itx.RangeAt(prefix, end, probe, func(_, pkRaw []byte) error {
		if err := s.chargeFKProbe(); err != nil {
			return err
		}
		row, err := s.childRowAt(child, pkRaw, probe)
		if err != nil {
			return err
		}
		if row == nil {
			return nil
		}
		ok, err := fkRowMatches(row, fk, parentRow)
		if err != nil || !ok {
			return err
		}
		if err := s.chargeFKTouched(); err != nil {
			return err
		}
		out = append(out, row)
		return nil
	})
	return out, err
}

func (s *Session) collectChildHeap(child *catalog.Table, fk catalog.ForeignKey, parentRow []types.Value, probe txn.Snapshot) ([][]types.Value, error) {
	heap, err := s.heapOf(child)
	if err != nil {
		return nil, err
	}
	htx := s.x.use(heap)
	var out [][]types.Value
	err = htx.RangeAt(nil, nil, probe, func(_, raw []byte) error {
		if err := s.chargeFKProbe(); err != nil {
			return err
		}
		row, err := s.decodeHeapRow(child, raw)
		if err != nil {
			return err
		}
		ok, err := fkRowMatches(row, fk, parentRow)
		if err != nil || !ok {
			return err
		}
		if err := s.chargeFKTouched(); err != nil {
			return err
		}
		out = append(out, cloneRow(row))
		return nil
	})
	return out, err
}

func (s *Session) childRowAt(child *catalog.Table, pkRaw []byte, probe txn.Snapshot) ([]types.Value, error) {
	heap, err := s.heapOf(child)
	if err != nil {
		return nil, err
	}
	htx := s.x.use(heap)
	raw, err := htx.LookupAt(pkRaw, probe)
	if err != nil {
		if nerr.HasCode(err, nerr.NotFound) {
			return nil, nil
		}
		return nil, err
	}
	row, err := s.decodeHeapRow(child, raw)
	if err != nil {
		return nil, err
	}
	return cloneRow(row), nil
}

func (s *Session) probeChildIndex(child *catalog.Table, idx catalog.Index, fk catalog.ForeignKey, parentRow []types.Value, probe txn.Snapshot) (bool, error) {
	ix, err := s.indexOf(child, idx)
	if err != nil {
		return false, err
	}
	itx := s.x.use(ix)
	vals := parentValsForChild(parentRow, fk, idx.Columns)
	prefix, err := types.EncodeKey(vals)
	if err != nil {
		return false, err
	}
	if idx.Unique {
		if err := s.chargeFKProbe(); err != nil {
			return false, err
		}
		pkRaw, err := itx.LookupAt(prefix, probe)
		if err != nil {
			if nerr.HasCode(err, nerr.NotFound) {
				return false, nil
			}
			return false, err
		}
		return s.childRowExists(child, pkRaw, probe)
	}
	end := types.PrefixEnd(prefix)
	var found bool
	err = itx.RangeAt(prefix, end, probe, func(_, pkRaw []byte) error {
		if err := s.chargeFKProbe(); err != nil {
			return err
		}
		ok, err := s.childRowExists(child, pkRaw, probe)
		if err != nil || ok {
			found = ok
			if err == nil && ok {
				return errStop
			}
			return err
		}
		return nil
	})
	if err == errStop {
		err = nil
	}
	return found, err
}

func (s *Session) probeChildHeap(child *catalog.Table, fk catalog.ForeignKey, parentRow []types.Value, probe txn.Snapshot) (bool, error) {
	heap, err := s.heapOf(child)
	if err != nil {
		return false, err
	}
	htx := s.x.use(heap)
	var found bool
	err = htx.RangeAt(nil, nil, probe, func(_, raw []byte) error {
		if err := s.chargeFKProbe(); err != nil {
			return err
		}
		row, err := s.decodeHeapRow(child, raw)
		if err != nil {
			return err
		}
		_ = s.tenantVisible(child, row)
		ok, err := fkRowMatches(row, fk, parentRow)
		if err != nil || !ok {
			return err
		}
		// RESTRICT counts a matching child even when the session tenant
		// cannot see it (global parent referenced by another tenant).
		found = true
		return errStop
	})
	if err == errStop {
		err = nil
	}
	return found, err
}

func (s *Session) childRowExists(child *catalog.Table, pkRaw []byte, probe txn.Snapshot) (bool, error) {
	heap, err := s.heapOf(child)
	if err != nil {
		return false, err
	}
	htx := s.x.use(heap)
	raw, err := htx.LookupAt(pkRaw, probe)
	if err != nil {
		if nerr.HasCode(err, nerr.NotFound) {
			return false, nil
		}
		return false, err
	}
	row, err := s.decodeHeapRow(child, raw)
	if err != nil {
		return false, err
	}
	_ = s.tenantVisible(child, row)
	return true, nil
}

func fkRowMatches(childRow []types.Value, fk catalog.ForeignKey, parentRow []types.Value) (bool, error) {
	for i, c := range fk.Columns {
		if c < 0 || c >= len(childRow) {
			return false, nil
		}
		r := fk.RefColumns[i]
		if r < 0 || r >= len(parentRow) {
			return false, nil
		}
		cv, pv := childRow[c], parentRow[r]
		if cv.Null || pv.Null {
			return false, nil
		}
		if cv.Typ.Kind != pv.Typ.Kind {
			got, err := types.Coerce(cv, pv.Typ)
			if err != nil {
				return false, err
			}
			cv = got
		}
		cmp, err := cv.Cmp(pv)
		if err != nil || cmp != 0 {
			return false, err
		}
	}
	return true, nil
}

func (s *Session) chargeFKProbe() error {
	b := s.budget()
	if err := b.Check(); err != nil {
		return err
	}
	s.fkProbes++
	if lim := b.ResultRows(); lim > 0 && s.fkProbes > lim {
		return nerr.New(nerr.Exhausted, "executor.fk", "foreign key probe exceeded row budget")
	}
	return b.ChargeMem(64)
}
