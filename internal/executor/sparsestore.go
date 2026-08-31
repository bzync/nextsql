package executor

import (
	"bytes"

	"github.com/bzync/nextsql/internal/catalog"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/sql/planner"
	"github.com/bzync/nextsql/internal/sql/types"
	"github.com/bzync/nextsql/internal/storage/btree"
	"github.com/bzync/nextsql/internal/txn"
	nsvec "github.com/bzync/nextsql/internal/vector"
)

// sqlSparse binds one SPARSE inverted index to its encrypted storage: the
// detached index tree holds the NSSM header and one NSSP posting list per
// dimension; the shared vector store holds the full-precision NSSV payloads
// used for COSINE re-rank and for discovering a row's coordinates on delete.
// It implements nsvec.SparseStore so build, row maintenance, and search all
// run against the same WAL/backup/PITR/Raft-durable trees as every other index.
//
// This slice has no process-local cached copy: a NEAREST reloads posting lists
// from the index tree per query.
type sqlSparse struct {
	itx     *btree.Txn
	vtx     *btree.Txn
	col     uint16
	snap    txn.Snapshot
	useSnap bool
}

func (v *sqlSparse) lookup(tx *btree.Txn, key []byte) ([]byte, error) {
	if v != nil && v.useSnap {
		return tx.LookupAt(key, v.snap)
	}
	return tx.Lookup(key)
}

func (v *sqlSparse) put(tx *btree.Txn, k, val []byte) error {
	if v != nil && v.useSnap {
		_, err := tx.LookupAt(k, v.snap)
		if err != nil {
			if nerr.HasCode(err, nerr.NotFound) {
				return tx.InsertAt(k, val, v.snap)
			}
			return err
		}
		return tx.UpdateAt(k, val, v.snap)
	}
	return upsert(tx, k, val)
}

func (v *sqlSparse) del(tx *btree.Txn, k []byte) error {
	err := tx.Delete(k)
	if err != nil && nerr.HasCode(err, nerr.NotFound) {
		return nil
	}
	return err
}

func (v *sqlSparse) LoadSparseMeta() (nsvec.SparseMeta, error) {
	raw, err := v.lookup(v.itx, nsvec.SparseMetaKey())
	if err != nil {
		return nsvec.SparseMeta{}, err
	}
	return nsvec.DecodeSparseMeta(raw)
}

func (v *sqlSparse) SaveSparseMeta(m nsvec.SparseMeta) error {
	raw, err := nsvec.EncodeSparseMeta(m)
	if err != nil {
		return err
	}
	return v.put(v.itx, nsvec.SparseMetaKey(), raw)
}

func (v *sqlSparse) ListPostings(dim uint32) ([]nsvec.SparsePosting, error) {
	raw, err := v.lookup(v.itx, nsvec.SparsePostingKey(dim))
	if err != nil {
		if nerr.HasCode(err, nerr.NotFound) {
			return nil, nil
		}
		return nil, err
	}
	return nsvec.DecodeSparseList(raw)
}

func (v *sqlSparse) AddPosting(dim uint32, p nsvec.SparsePosting) error {
	entries, err := v.ListPostings(dim)
	if err != nil {
		return err
	}
	replaced := false
	for i, e := range entries {
		if bytes.Equal(e.PK, p.PK) {
			entries[i] = p
			replaced = true
			break
		}
	}
	if !replaced {
		entries = append(entries, p)
	}
	return v.writeList(dim, entries)
}

func (v *sqlSparse) RemovePosting(dim uint32, pk []byte) (bool, error) {
	entries, err := v.ListPostings(dim)
	if err != nil {
		return false, err
	}
	out := entries[:0]
	removed := false
	for _, e := range entries {
		if bytes.Equal(e.PK, pk) {
			removed = true
			continue
		}
		out = append(out, e)
	}
	if !removed {
		return false, nil
	}
	return true, v.writeList(dim, out)
}

func (v *sqlSparse) writeList(dim uint32, entries []nsvec.SparsePosting) error {
	key := nsvec.SparsePostingKey(dim)
	if len(entries) == 0 {
		return v.del(v.itx, key)
	}
	raw, err := nsvec.EncodeSparseList(entries)
	if err != nil {
		return err
	}
	return v.put(v.itx, key, raw)
}

func (v *sqlSparse) LoadSparse(pk []byte) (nsvec.SparseVec, error) {
	raw, err := v.lookup(v.vtx, nsvec.PayloadKey(v.col, pk))
	if err != nil {
		return nsvec.SparseVec{}, err
	}
	return nsvec.DecodeSparse(raw)
}

func (s *Session) sparseStoreOf(tab *catalog.Table, idx catalog.Index) (*sqlSparse, error) {
	if len(idx.Columns) != 1 {
		return nil, nerr.New(nerr.Internal, "executor.sparseStoreOf", "VECTOR INDEX column count")
	}
	ix, err := s.indexOf(tab, idx)
	if err != nil {
		return nil, err
	}
	vs, err := s.vecOf(tab)
	if err != nil {
		return nil, err
	}
	st := &sqlSparse{itx: s.x.use(ix), vtx: s.x.use(vs), col: uint16(idx.Columns[0])}
	if snap, ok, err := s.fkWriteSnap(); err != nil {
		return nil, err
	} else if ok {
		st.snap = snap
		st.useSnap = true
	}
	return st, nil
}

func valueSparse(v types.Value, dim uint32) (nsvec.SparseVec, error) {
	if dim == 0 {
		dim = uint32(v.Typ.Precision)
	}
	if v.Typ.VecElem == types.VecSparse || len(v.SparseIdx) > 0 {
		return nsvec.NewSparseVec(dim, v.SparseIdx, v.SparseVal)
	}
	idx, val, err := types.DenseToSparse(v.Vec)
	if err != nil {
		return nsvec.SparseVec{}, err
	}
	if dim == 0 {
		dim = uint32(len(v.Vec))
	}
	return nsvec.NewSparseVec(dim, idx, val)
}

// buildSparseIndex writes the inverted index for a SPARSEVECTOR column into the
// fresh detached index tree. Shared by CREATE VECTOR INDEX … USING SPARSE and
// REBUILD INDEX.
func (s *Session) buildSparseIndex(tab *catalog.Table, idx catalog.Index, htx *btree.Txn, progress *rebuildProgress) error {
	if len(idx.Columns) != 1 {
		return nerr.New(nerr.InvalidArgument, "executor.buildSparseIndex", "VECTOR INDEX column count")
	}
	col := idx.Columns[0]
	dim := uint32(tab.Columns[col].Type.Precision)
	metric := nsvec.MetricCosine

	st, err := s.sparseStoreOf(tab, idx)
	if err != nil {
		return err
	}
	meta := nsvec.DefaultSparseMeta(dim, metric)
	if err := st.SaveSparseMeta(meta); err != nil {
		return err
	}
	if err := htx.Range(nil, nil, func(_, val []byte) error {
		if err := s.budget().Check(); err != nil {
			return err
		}
		r, err := s.decodeHeapRow(tab, val)
		if err != nil {
			return err
		}
		v := r[col]
		if v.Null {
			progress.add(1, 0)
			return nil
		}
		pk, err := types.EncodeKey(tab.PKValues(r))
		if err != nil {
			return err
		}
		sv, err := valueSparse(v, dim)
		if err != nil {
			return err
		}
		if err := nsvec.AddSparse(st, pk, sv); err != nil {
			return err
		}
		progress.add(1, 1)
		return nil
	}); err != nil {
		return err
	}
	return nil
}

// maintainSparseIndex applies one row change to a non-partitioned SPARSE index.
// DELETE/UPDATE run after deleteVectors, so this uses the in-memory old/new
// values rather than LoadSparse against the payload store.
func (s *Session) maintainSparseIndex(tab *catalog.Table, idx catalog.Index, old, neu []types.Value) error {
	st, err := s.sparseStoreOf(tab, idx)
	if err != nil {
		return err
	}
	col := idx.Columns[0]
	dim := uint32(tab.Columns[col].Type.Precision)
	if old != nil && col < len(old) && !old[col].Null {
		pk, err := types.EncodeKey(tab.PKValues(old))
		if err != nil {
			return err
		}
		sv, err := valueSparse(old[col], dim)
		if err != nil {
			return err
		}
		if err := removeSparseCoords(st, pk, sv); err != nil {
			return err
		}
	}
	if neu != nil && col < len(neu) && !neu[col].Null {
		pk, err := types.EncodeKey(tab.PKValues(neu))
		if err != nil {
			return err
		}
		sv, err := valueSparse(neu[col], dim)
		if err != nil {
			return err
		}
		return addSparseCoords(st, pk, sv)
	}
	return nil
}

func removeSparseCoords(st nsvec.SparseStore, pk []byte, sv nsvec.SparseVec) error {
	meta, err := st.LoadSparseMeta()
	if err != nil {
		return err
	}
	found := false
	for _, idx := range sv.Indices {
		ok, err := st.RemovePosting(idx, pk)
		if err != nil {
			return err
		}
		found = found || ok
	}
	if found && meta.Count > 0 {
		meta.Count--
		return st.SaveSparseMeta(meta)
	}
	return nil
}

func addSparseCoords(st nsvec.SparseStore, pk []byte, sv nsvec.SparseVec) error {
	if err := nsvec.CheckSparse(sv); err != nil {
		return err
	}
	meta, err := st.LoadSparseMeta()
	if err != nil {
		return err
	}
	for i, idx := range sv.Indices {
		if err := st.AddPosting(idx, nsvec.SparsePosting{PK: pk, Value: sv.Values[i]}); err != nil {
			return err
		}
	}
	meta.Count++
	return st.SaveSparseMeta(meta)
}

func (s *Session) nearestSparseIndex(n planner.Nearest, q nsvec.SparseVec, metric nsvec.Metric, tab *catalog.Table, idx catalog.Index) ([][]types.Value, error) {
	st, err := s.sparseStoreOf(tab, idx)
	if err != nil {
		return nil, err
	}
	meta, err := st.LoadSparseMeta()
	if err != nil {
		if nerr.HasCode(err, nerr.NotFound) {
			return nil, nil
		}
		return nil, err
	}
	if n.Metric != "" && meta.Metric != metric {
		return s.nearestSparseFlat(n, q, metric)
	}
	if meta.Count == 0 {
		return nil, nil
	}
	k := int(n.K)
	if k < 1 {
		k = int(meta.Count)
	}
	if k < 1 {
		return nil, nil
	}
	if n.Residual != nil && uint64(k) < meta.Count {
		over := k * 4
		if over < k {
			over = k
		}
		if uint64(over) > meta.Count {
			over = int(meta.Count)
		}
		k = over
	}
	hits, err := nsvec.SearchSparse(st, q, k, 0, s.workers())
	if err != nil {
		return nil, err
	}
	heap, err := s.heapOf(tab)
	if err != nil {
		return nil, err
	}
	htx := s.x.use(heap)
	var out [][]types.Value
	for _, h := range hits {
		if err := s.budget().Check(); err != nil {
			return nil, err
		}
		rowv, err := s.fetchPKRow(htx, tab, h.PK)
		if err != nil {
			return nil, err
		}
		if rowv == nil {
			continue
		}
		ok, err := s.match(n.Residual, tab, rowv)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		out = append(out, rowv)
		if n.K > 0 && int64(len(out)) >= n.K {
			break
		}
	}
	return out, nil
}

func (s *Session) nearestSparseQuery(n planner.Nearest) (nsvec.SparseVec, error) {
	v, err := s.eval(n.Query, n.Table, nil)
	if err != nil {
		return nsvec.SparseVec{}, err
	}
	if v.Null {
		return nsvec.SparseVec{}, nil
	}
	want := uint32(0)
	if n.Table != nil && n.Column >= 0 && n.Column < len(n.Table.Columns) {
		want = uint32(n.Table.Columns[n.Column].Type.Precision)
	}
	if v.Typ.Kind != types.KindVector {
		return nsvec.SparseVec{}, nerr.New(nerr.InvalidArgument, "executor.nearestSparseQuery", "NEAREST query must be a VECTOR")
	}
	return valueSparse(v, want)
}

func (s *Session) nearestSparseFlat(n planner.Nearest, q nsvec.SparseVec, metric nsvec.Metric) ([][]types.Value, error) {
	tab := n.Table
	if tab == nil || n.Column < 0 || n.Column >= len(tab.Columns) {
		return nil, nerr.New(nerr.Internal, "executor.nearestSparseFlat", "missing NEAREST column")
	}
	var (
		cands []nsvec.SparseCand
		rows  [][]types.Value
	)
	if n.Input != nil {
		got, err := s.collectPlan(n.Input)
		if err != nil {
			return nil, err
		}
		for _, row := range got {
			v := row[n.Column]
			if v.Null {
				continue
			}
			sv, err := valueSparse(v, q.Dim)
			if err != nil {
				return nil, err
			}
			pk, err := types.EncodeKey(tab.PKValues(row))
			if err != nil {
				return nil, err
			}
			cands = append(cands, nsvec.SparseCand{PK: pk, Vec: sv})
			rows = append(rows, row)
		}
	} else {
		start, end := nsvec.PayloadBounds(uint16(n.Column))
		scan := func(vs *btree.Tree) error {
			return s.x.use(vs).Range(start, end, func(key, val []byte) error {
				if err := s.budget().Check(); err != nil {
					return err
				}
				_, pk, err := nsvec.SplitPayloadKey(key)
				if err != nil {
					return err
				}
				sv, err := nsvec.DecodeSparse(val)
				if err != nil {
					return err
				}
				cands = append(cands, nsvec.SparseCand{PK: pk, Vec: sv})
				return nil
			})
		}
		if tab.VecMeta == 0 {
			return nil, nil
		}
		vs, err := s.vecOf(tab)
		if err != nil {
			return nil, err
		}
		if err := scan(vs); err != nil {
			return nil, err
		}
	}
	k := int(n.K)
	if k < 1 {
		k = len(cands)
	}
	hits, err := nsvec.SparseFlat(q, metric, cands, k)
	if err != nil {
		return nil, err
	}
	if n.Input != nil {
		byPK := make(map[string][]types.Value, len(rows))
		for i, row := range rows {
			byPK[string(cands[i].PK)] = row
		}
		var out [][]types.Value
		for _, h := range hits {
			if row, ok := byPK[string(h.PK)]; ok {
				out = append(out, row)
			}
		}
		return out, nil
	}
	heap, err := s.heapOf(tab)
	if err != nil {
		return nil, err
	}
	htx := s.x.use(heap)
	var out [][]types.Value
	for _, h := range hits {
		if err := s.budget().Check(); err != nil {
			return nil, err
		}
		rowv, err := s.fetchPKRow(htx, tab, h.PK)
		if err != nil {
			return nil, err
		}
		if rowv == nil {
			continue
		}
		ok, err := s.match(n.Residual, tab, rowv)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		out = append(out, rowv)
		if n.K > 0 && int64(len(out)) >= n.K {
			break
		}
	}
	return out, nil
}
