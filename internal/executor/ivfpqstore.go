package executor

import (
	"bytes"
	"encoding/binary"

	"github.com/bzync/nextsql/internal/catalog"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/sql/planner"
	"github.com/bzync/nextsql/internal/sql/types"
	"github.com/bzync/nextsql/internal/storage/btree"
	"github.com/bzync/nextsql/internal/txn"
	nsvec "github.com/bzync/nextsql/internal/vector"
)

// sqlIVFPQ binds one IVF-PQ (inverted-file + product-quantisation) vector index
// to its encrypted storage. The detached index tree holds the coarse-quantiser
// header, centroids (grouped like plain IVF), the product-quantisation codebook
// (chunked, since it never fits one B+Tree record), and the front-coded posting
// lists of M-byte residual codes; the shared vector store holds the
// full-precision column payloads used for the exact re-rank. It implements
// nsvec.IVFPQStore so training, row maintenance, and search all run against the
// same WAL/backup/PITR/Raft-durable trees as every other index.
//
// This slice has no process-local cached copy: a NEAREST reloads and decrypts
// the quantiser from the index tree per query (a documented follow-on, matching
// plain IVF's first increment).
type sqlIVFPQ struct {
	itx     *btree.Txn // detached IVF-PQ index tree
	vtx     *btree.Txn // shared vector payload store
	col     uint16     // catalog ordinal of the vector column
	snap    txn.Snapshot
	useSnap bool
}

func (v *sqlIVFPQ) lookup(tx *btree.Txn, key []byte) ([]byte, error) {
	if v != nil && v.useSnap {
		return tx.LookupAt(key, v.snap)
	}
	return tx.Lookup(key)
}

func (v *sqlIVFPQ) put(tx *btree.Txn, k, val []byte) error {
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

func (v *sqlIVFPQ) del(tx *btree.Txn, k []byte) error {
	err := tx.Delete(k)
	if err != nil && nerr.HasCode(err, nerr.NotFound) {
		return nil
	}
	return err
}

func (v *sqlIVFPQ) LoadIVFPQMeta() (nsvec.IVFPQMeta, error) {
	raw, err := v.lookup(v.itx, nsvec.IVFMetaKey())
	if err != nil {
		return nsvec.IVFPQMeta{}, err
	}
	return nsvec.DecodeIVFPQMeta(raw)
}

func (v *sqlIVFPQ) SaveIVFPQMeta(m nsvec.IVFPQMeta) error {
	raw, err := nsvec.EncodeIVFPQMeta(m)
	if err != nil {
		return err
	}
	return v.put(v.itx, nsvec.IVFMetaKey(), raw)
}

// Centroid grouping is identical to plain IVF: the bare IVFCentroidsKey record
// holds an "IVFCG" group header and each group of centroids lives at
// IVFCentroidChunkKey(i).
func (v *sqlIVFPQ) LoadCentroids() ([][]float32, error) {
	raw, err := v.lookup(v.itx, nsvec.IVFCentroidsKey())
	if err != nil {
		return nil, err
	}
	if !bytes.HasPrefix(raw, []byte(ivfCentGroupMagic)) {
		return nsvec.DecodeCentroids(raw)
	}
	if len(raw) != len(ivfCentGroupMagic)+1+4 || raw[len(ivfCentGroupMagic)] != ivfCentGroupVersion {
		return nil, nerr.New(nerr.InvalidFormat, "executor.sqlIVFPQ.LoadCentroids", "bad IVF centroid group header")
	}
	n := int(binary.BigEndian.Uint32(raw[len(ivfCentGroupMagic)+1:]))
	if n <= 0 || n > nsvec.MaxIVFLists {
		return nil, nerr.New(nerr.InvalidFormat, "executor.sqlIVFPQ.LoadCentroids", "bad IVF centroid group count")
	}
	var out [][]float32
	for i := 0; i < n; i++ {
		craw, err := v.lookup(v.itx, nsvec.IVFCentroidChunkKey(uint32(i)))
		if err != nil {
			return nil, err
		}
		group, err := nsvec.DecodeCentroids(craw)
		if err != nil {
			return nil, err
		}
		out = append(out, group...)
	}
	return out, nil
}

func (v *sqlIVFPQ) SaveCentroids(c [][]float32) error {
	if len(c) == 0 || len(c[0]) == 0 {
		return nerr.New(nerr.InvalidArgument, "executor.sqlIVFPQ.SaveCentroids", "empty IVF centroids")
	}
	dim := len(c[0])
	perGroup := ivfCentGroupBudget / (4 * dim)
	if perGroup < 1 {
		perGroup = 1
	}
	var groups int
	for start := 0; start < len(c); start += perGroup {
		end := start + perGroup
		if end > len(c) {
			end = len(c)
		}
		raw, err := nsvec.EncodeCentroids(c[start:end], dim)
		if err != nil {
			return err
		}
		if err := v.put(v.itx, nsvec.IVFCentroidChunkKey(uint32(groups)), raw); err != nil {
			return err
		}
		groups++
	}
	hdr := make([]byte, len(ivfCentGroupMagic)+1+4)
	copy(hdr, ivfCentGroupMagic)
	hdr[len(ivfCentGroupMagic)] = ivfCentGroupVersion
	binary.BigEndian.PutUint32(hdr[len(ivfCentGroupMagic)+1:], uint32(groups))
	return v.put(v.itx, nsvec.IVFCentroidsKey(), hdr)
}

// ivfpqCbGroupMagic marks the bare codebook record as a chunk index: an encoded
// product-quantisation codebook (~4·Ksub·dim bytes) never fits in one ~8 KiB
// B+Tree record, so LoadCodebook / SaveCodebook split the encoded block into
// fixed-size chunks at IVFPQCodebookChunkKey(i) and keep the count + total
// length here.
const (
	ivfpqCbGroupMagic   = "IVPCG"
	ivfpqCbGroupVersion = 1
	ivfpqCbChunkBytes   = 7000
	// ivfpqCbMaxBytes bounds a reassembled codebook block before decode:
	// 4·Ksub·dim with Ksub ≤ 256 and dim ≤ vector.MaxDim (8192).
	ivfpqCbMaxBytes = 4 * 256 * 8192
)

func (v *sqlIVFPQ) LoadCodebook() (*nsvec.PQCodebook, error) {
	raw, err := v.lookup(v.itx, nsvec.IVFPQCodebookKey())
	if err != nil {
		return nil, err
	}
	if len(raw) != len(ivfpqCbGroupMagic)+1+4+4 || !bytes.HasPrefix(raw, []byte(ivfpqCbGroupMagic)) || raw[len(ivfpqCbGroupMagic)] != ivfpqCbGroupVersion {
		return nil, nerr.New(nerr.InvalidFormat, "executor.sqlIVFPQ.LoadCodebook", "bad IVF-PQ codebook header")
	}
	off := len(ivfpqCbGroupMagic) + 1
	chunks := int(binary.BigEndian.Uint32(raw[off:]))
	total := int(binary.BigEndian.Uint32(raw[off+4:]))
	if chunks <= 0 || total <= 0 || total > ivfpqCbMaxBytes || chunks > total {
		return nil, nerr.New(nerr.InvalidFormat, "executor.sqlIVFPQ.LoadCodebook", "bad IVF-PQ codebook chunk index")
	}
	buf := make([]byte, 0, total)
	for i := 0; i < chunks; i++ {
		craw, err := v.lookup(v.itx, nsvec.IVFPQCodebookChunkKey(uint32(i)))
		if err != nil {
			return nil, err
		}
		buf = append(buf, craw...)
		if len(buf) > total {
			return nil, nerr.New(nerr.InvalidFormat, "executor.sqlIVFPQ.LoadCodebook", "IVF-PQ codebook chunk overrun")
		}
	}
	if len(buf) != total {
		return nil, nerr.New(nerr.InvalidFormat, "executor.sqlIVFPQ.LoadCodebook", "IVF-PQ codebook length mismatch")
	}
	return nsvec.DecodePQCodebook(buf)
}

func (v *sqlIVFPQ) SaveCodebook(cb *nsvec.PQCodebook) error {
	raw, err := nsvec.EncodePQCodebook(cb)
	if err != nil {
		return err
	}
	var chunks int
	for start := 0; start < len(raw); start += ivfpqCbChunkBytes {
		end := start + ivfpqCbChunkBytes
		if end > len(raw) {
			end = len(raw)
		}
		if err := v.put(v.itx, nsvec.IVFPQCodebookChunkKey(uint32(chunks)), raw[start:end]); err != nil {
			return err
		}
		chunks++
	}
	hdr := make([]byte, len(ivfpqCbGroupMagic)+1+4+4)
	copy(hdr, ivfpqCbGroupMagic)
	hdr[len(ivfpqCbGroupMagic)] = ivfpqCbGroupVersion
	binary.BigEndian.PutUint32(hdr[len(ivfpqCbGroupMagic)+1:], uint32(chunks))
	binary.BigEndian.PutUint32(hdr[len(ivfpqCbGroupMagic)+5:], uint32(len(raw)))
	return v.put(v.itx, nsvec.IVFPQCodebookKey(), hdr)
}

func (v *sqlIVFPQ) ListEntries(list int) ([]nsvec.PQEntry, error) {
	if list < 0 || list > int(^uint32(0)) {
		return nil, nerr.New(nerr.InvalidArgument, "executor.sqlIVFPQ.ListEntries", "IVF-PQ list out of range")
	}
	raw, err := v.lookup(v.itx, nsvec.IVFPostingKey(uint32(list)))
	if err != nil {
		if nerr.HasCode(err, nerr.NotFound) {
			return nil, nil
		}
		return nil, err
	}
	return nsvec.DecodePQList(raw)
}

func (v *sqlIVFPQ) AddEntry(list int, e nsvec.PQEntry) error {
	entries, err := v.ListEntries(list)
	if err != nil {
		return err
	}
	replaced := false
	for i, ex := range entries {
		if bytes.Equal(ex.PK, e.PK) {
			entries[i] = e
			replaced = true
			break
		}
	}
	if !replaced {
		entries = append(entries, e)
	}
	return v.writeList(list, entries)
}

func (v *sqlIVFPQ) RemoveEntry(list int, pk []byte) (bool, error) {
	entries, err := v.ListEntries(list)
	if err != nil {
		return false, err
	}
	out := entries[:0]
	removed := false
	for _, ex := range entries {
		if bytes.Equal(ex.PK, pk) {
			removed = true
			continue
		}
		out = append(out, ex)
	}
	if !removed {
		return false, nil
	}
	return true, v.writeList(list, out)
}

func (v *sqlIVFPQ) writeList(list int, entries []nsvec.PQEntry) error {
	if list < 0 || list > int(^uint32(0)) {
		return nerr.New(nerr.InvalidArgument, "executor.sqlIVFPQ.writeList", "IVF-PQ list out of range")
	}
	meta, err := v.LoadIVFPQMeta()
	if err != nil {
		return err
	}
	key := nsvec.IVFPostingKey(uint32(list))
	if len(entries) == 0 {
		return v.del(v.itx, key)
	}
	raw, err := nsvec.EncodePQList(entries, int(meta.M))
	if err != nil {
		return err
	}
	return v.put(v.itx, key, raw)
}

func (v *sqlIVFPQ) LoadVec(pk []byte) ([]float32, error) {
	raw, err := v.lookup(v.vtx, nsvec.PayloadKey(v.col, pk))
	if err != nil {
		return nil, err
	}
	return nsvec.DecodePayload(raw)
}

// ivfpqStoreOf binds the IVF-PQ store for a non-partitioned table's vector index.
func (s *Session) ivfpqStoreOf(tab *catalog.Table, idx catalog.Index) (*sqlIVFPQ, error) {
	if len(idx.Columns) != 1 {
		return nil, nerr.New(nerr.Internal, "executor.ivfpqStoreOf", "VECTOR INDEX column count")
	}
	ix, err := s.indexOf(tab, idx)
	if err != nil {
		return nil, err
	}
	vs, err := s.vecOf(tab)
	if err != nil {
		return nil, err
	}
	st := &sqlIVFPQ{itx: s.x.use(ix), vtx: s.x.use(vs), col: uint16(idx.Columns[0])}
	if snap, ok, err := s.fkWriteSnap(); err != nil {
		return nil, err
	} else if ok {
		st.snap = snap
		st.useSnap = true
	}
	return st, nil
}

// emptyPQCodebook is a valid all-zero placeholder codebook for an IVF-PQ index
// built on a table with no vectors yet: one sub-centroid per subspace so
// AddIVFPQ can encode inserts (every residual maps to code 0) until a REBUILD
// trains a real codebook.
func emptyPQCodebook(dim, m int) *nsvec.PQCodebook {
	subDim := dim / m
	cb := &nsvec.PQCodebook{M: m, SubDim: subDim, Ksub: 1, Sub: make([][][]float32, m)}
	for i := 0; i < m; i++ {
		cb.Sub[i] = [][]float32{make([]float32, subDim)}
	}
	return cb
}

// buildIVFPQIndex trains a coarse quantiser and a product-quantisation codebook
// over the table heap and writes the centroids, codebook, posting lists, and
// header into the fresh detached index tree. Shared by CREATE VECTOR INDEX ...
// USING IVFPQ and REBUILD INDEX.
func (s *Session) buildIVFPQIndex(tab *catalog.Table, idx catalog.Index, htx *btree.Txn, progress *rebuildProgress) error {
	if len(idx.Columns) != 1 {
		return nerr.New(nerr.InvalidArgument, "executor.buildIVFPQIndex", "VECTOR INDEX column count")
	}
	col := idx.Columns[0]
	dim := int(tab.Columns[col].Type.Precision)
	metric := graphMetric(tab.Columns[col].Type)
	m := int(idx.IVFSubspaces)
	if m < 1 || dim%m != 0 {
		return nerr.New(nerr.InvalidArgument, "executor.buildIVFPQIndex", "IVFPQ SUBSPACES must divide the vector dimension")
	}

	type row struct {
		pk  []byte
		vec []float32
	}
	var rows []row
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
		rows = append(rows, row{pk: pk, vec: append([]float32(nil), v.Vec...)})
		progress.add(1, 1)
		return nil
	}); err != nil {
		return err
	}

	st, err := s.ivfpqStoreOf(tab, idx)
	if err != nil {
		return err
	}

	meta := nsvec.DefaultIVFPQMeta(uint16(dim), metric, idx.IVFLists, uint16(m))
	if idx.IVFProbes > 0 {
		meta.NProbe = idx.IVFProbes
		if meta.NProbe > meta.NList {
			meta.NProbe = meta.NList
		}
	}

	if len(rows) == 0 {
		meta.NList = 1
		meta.NProbe = 1
		meta.Trained = true
		meta.Count = 0
		if err := st.SaveCentroids([][]float32{make([]float32, dim)}); err != nil {
			return err
		}
		if err := st.SaveCodebook(emptyPQCodebook(dim, m)); err != nil {
			return err
		}
		return st.SaveIVFPQMeta(meta)
	}

	// Train on a deterministic sample so very large tables do not hold every
	// vector twice; TrainIVFPQ clamps NList to the sample size.
	const maxTrainSample = 50000
	var sample [][]float32
	if len(rows) <= maxTrainSample {
		sample = make([][]float32, len(rows))
		for i, r := range rows {
			sample[i] = r.vec
		}
	} else {
		stride := len(rows) / maxTrainSample
		if stride < 1 {
			stride = 1
		}
		for i := 0; i < len(rows) && len(sample) < maxTrainSample; i += stride {
			sample = append(sample, rows[i].vec)
		}
	}
	mem, err := nsvec.TrainIVFPQ(meta, sample)
	if err != nil {
		return err
	}
	// Persist the trained centroids + codebook + header first so AddIVFPQ (which
	// reloads them through st) sees a consistent index, then add every row.
	if err := st.SaveCentroids(mem.Centroids); err != nil {
		return err
	}
	if err := st.SaveCodebook(mem.Codebook); err != nil {
		return err
	}
	if err := st.SaveIVFPQMeta(mem.Meta); err != nil {
		return err
	}
	for _, r := range rows {
		if err := s.budget().Check(); err != nil {
			return err
		}
		if err := nsvec.AddIVFPQ(st, r.pk, r.vec); err != nil {
			return err
		}
	}
	return nil
}

// maintainIVFPQIndex applies one row change to a non-partitioned IVF-PQ index.
// The payload has already been written to the vector store by putVectors.
func (s *Session) maintainIVFPQIndex(tab *catalog.Table, idx catalog.Index, old, neu []types.Value) error {
	st, err := s.ivfpqStoreOf(tab, idx)
	if err != nil {
		return err
	}
	if old != nil {
		pk, err := types.EncodeKey(tab.PKValues(old))
		if err != nil {
			return err
		}
		if _, err := nsvec.RemoveIVFPQ(st, pk); err != nil {
			return err
		}
	}
	if neu != nil {
		col := idx.Columns[0]
		if col >= len(neu) || neu[col].Null {
			return nil
		}
		pk, err := types.EncodeKey(tab.PKValues(neu))
		if err != nil {
			return err
		}
		return nsvec.AddIVFPQ(st, pk, neu[col].Vec)
	}
	return nil
}

// nearestIVFPQIndex answers a NEAREST query through an IVF-PQ index: rank the
// centroids, ADC-score the probed posting lists, and re-rank the top candidates
// exactly against the full-precision payloads. A USING metric that differs from
// the trained one falls back to exact flat.
func (s *Session) nearestIVFPQIndex(n planner.Nearest, q []float32, metric nsvec.Metric, tab *catalog.Table, idx catalog.Index) ([][]types.Value, error) {
	st, err := s.ivfpqStoreOf(tab, idx)
	if err != nil {
		return nil, err
	}
	meta, err := st.LoadIVFPQMeta()
	if err != nil {
		if nerr.HasCode(err, nerr.NotFound) {
			return nil, nil
		}
		return nil, err
	}
	if n.Metric != "" && meta.Metric != metric {
		return s.nearestFlat(n, q, metric)
	}
	if !meta.Trained || meta.Count == 0 {
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
	hits, err := nsvec.SearchIVFPQ(st, q, k, int(idx.IVFProbes), 0, s.workers())
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
