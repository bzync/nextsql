package executor

import (
	"github.com/bzync/nextsql/internal/catalog"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/sql/types"
	"github.com/bzync/nextsql/internal/storage/btree"
	"github.com/bzync/nextsql/internal/txn"
	"github.com/bzync/nextsql/internal/wal"
)

// InsertRows writes pre-evaluated rows in table column order. Same WAL, MVCC,
// encryption, and index maintenance as INSERT. Caller must be in a transaction.
func (s *Session) InsertRows(table string, rows [][]types.Value) (int64, error) {
	if s == nil || s.x == nil {
		return 0, nerr.New(nerr.InvalidArgument, "executor.InsertRows", "no active transaction")
	}
	tab, ok := s.lookup(table)
	if !ok {
		return 0, nerr.New(nerr.NotFound, "executor.InsertRows", "unknown table")
	}
	heap, err := s.heapOf(tab)
	if err != nil {
		return 0, err
	}
	htx := s.x.use(heap)
	useBatch := len(tab.Indexes) == 0 && !tab.HasVector() && !s.hasTriggers(tab.ID)
	keys := make([][]byte, 0, len(rows))
	vals := make([][]byte, 0, len(rows))
	changeRows := make([][]types.Value, 0, len(rows))
	var n int64
	for _, src := range rows {
		if len(src) != len(tab.Columns) {
			return n, nerr.New(nerr.InvalidArgument, "executor.InsertRows", "column count")
		}
		row := make([]types.Value, len(tab.Columns))
		for i := range src {
			v, err := types.Coerce(src[i], tab.Columns[i].Type)
			if err != nil {
				return n, err
			}
			row[i] = v
		}
		for i := range row {
			nv, err := s.applyDefault(tab, i, row[i])
			if err != nil {
				return n, err
			}
			if nv.Null && tab.Columns[i].NotNull {
				return n, nerr.New(nerr.InvalidArgument, "executor.InsertRows", "NULL in NOT NULL column")
			}
			row[i] = nv
		}
		if err := s.checkLegacyTenantRow(tab, row); err != nil {
			return n, err
		}
		if useBatch {
			if err := s.checkOutboundFKs(tab, row); err != nil {
				return n, err
			}
			pk, err := types.EncodeKey(tab.PKValues(row))
			if err != nil {
				return n, err
			}
			payload, err := types.EncodeRow(row)
			if err != nil {
				return n, err
			}
			keys = append(keys, pk)
			vals = append(vals, payload)
			changeRows = append(changeRows, row)
			n++
			continue
		}
		if err := s.writeRow(tab, htx, row, true); err != nil {
			return n, err
		}
		n++
	}
	if useBatch {
		if err := htx.InsertBatch(keys, vals); err != nil {
			return 0, err
		}
		for _, row := range changeRows {
			if err := s.stageRowChange(tab, wal.ChangeInsert, nil, row); err != nil {
				return 0, err
			}
		}
	}
	return n, nil
}

// InsertEncoded writes pre-encoded primary keys and row payloads.
func (s *Session) InsertEncoded(table string, keys, payloads [][]byte) (int64, error) {
	if s == nil || s.x == nil {
		return 0, nerr.New(nerr.InvalidArgument, "executor.InsertEncoded", "no active transaction")
	}
	if len(keys) != len(payloads) {
		return 0, nerr.New(nerr.InvalidArgument, "executor.InsertEncoded", "keys/payloads length")
	}
	tab, ok := s.lookup(table)
	if !ok {
		return 0, nerr.New(nerr.NotFound, "executor.InsertEncoded", "unknown table")
	}
	decoded := make([][]types.Value, len(payloads))
	for i, payload := range payloads {
		row, err := types.DecodeRow(payload, tab.Types())
		if err != nil {
			return 0, err
		}
		if err := s.checkLegacyTenantRow(tab, row); err != nil {
			return 0, err
		}
		decoded[i] = row
	}
	heap, err := s.heapOf(tab)
	if err != nil {
		return 0, err
	}
	if err := s.x.use(heap).InsertBatch(keys, payloads); err != nil {
		return 0, err
	}
	for i := range decoded {
		if err := s.stageEncodedChange(tab, wal.ChangeInsert, nil, keys[i], nil, payloads[i], "", ""); err != nil {
			return 0, err
		}
	}
	return int64(len(keys)), nil
}

const bulkCommitEvery = 8192
const maxHeapSwapCDCChanges = 131072

// BulkSetDecimal sets col to a decimal literal on every row of table,
// committing every 8192 rows so dirty pages stay under the buffer pool.
func (s *Session) BulkSetDecimal(table, col, lit string) (int64, error) {
	if s == nil {
		return 0, nerr.New(nerr.InvalidArgument, "executor.BulkSetDecimal", "nil session")
	}
	tab, ok := s.lookup(table)
	if !ok {
		return 0, nerr.New(nerr.NotFound, "executor.BulkSetDecimal", "unknown table")
	}
	ci, ok := tab.ColIndex(col)
	if !ok {
		return 0, nerr.New(nerr.NotFound, "executor.BulkSetDecimal", "unknown column")
	}
	d, err := types.ParseDecimal(lit)
	if err != nil {
		return 0, err
	}
	val := types.DecimalValue(d, tab.Columns[ci].Type)
	val, err = types.Coerce(val, tab.Columns[ci].Type)
	if err != nil {
		return 0, err
	}
	if len(tab.Indexes) == 0 && !tab.HasVector() && !s.hasTriggers(tab.ID) && s.soleSnapshot() {
		return s.patchDecimal(tab, ci, val)
	}
	var (
		after []types.Value
		total int64
	)
	for {
		if _, err := s.begin(txn.SnapshotIsolation); err != nil {
			return total, err
		}
		batch, err := s.nextDMLBatch(tab, nil, after, bulkCommitEvery)
		if err != nil {
			_ = s.abort()
			return total, err
		}
		if len(batch) == 0 {
			_, err := s.commit()
			return total, err
		}
		heap, err := s.heapOf(tab)
		if err != nil {
			_ = s.abort()
			return total, err
		}
		htx := s.x.use(heap)
		for _, row := range batch {
			neu := append([]types.Value(nil), row...)
			neu[ci] = val
			if err := s.replaceRow(tab, htx, row, neu); err != nil {
				_ = s.abort()
				return total, err
			}
			total++
		}
		after = tab.PKValues(batch[len(batch)-1])
		if _, err := s.commit(); err != nil {
			return total, err
		}
	}
}

// BulkDeleteAll deletes every row of table, committing every 8192 rows.
// When this session is the only snapshot and the table has no secondary
// indexes, the heap is replaced with an empty tree (same WAL/catalog txn).
func (s *Session) BulkDeleteAll(table string) (int64, error) {
	if s == nil {
		return 0, nerr.New(nerr.InvalidArgument, "executor.BulkDeleteAll", "nil session")
	}
	tab, ok := s.lookup(table)
	if !ok {
		return 0, nerr.New(nerr.NotFound, "executor.BulkDeleteAll", "unknown table")
	}
	if s.canTruncate(tab) {
		return s.truncateHeap(tab)
	}
	return s.bulkDeleteRows(tab)
}

func (s *Session) bulkDeleteRows(tab *catalog.Table) (int64, error) {
	var (
		after []types.Value
		total int64
	)
	for {
		if _, err := s.begin(txn.SnapshotIsolation); err != nil {
			return total, err
		}
		batch, err := s.nextDMLBatch(tab, nil, after, bulkCommitEvery)
		if err != nil {
			_ = s.abort()
			return total, err
		}
		if len(batch) == 0 {
			_, err := s.commit()
			return total, err
		}
		heap, err := s.heapOf(tab)
		if err != nil {
			_ = s.abort()
			return total, err
		}
		htx := s.x.use(heap)
		for _, row := range batch {
			if err := s.removeRow(tab, htx, row); err != nil {
				_ = s.abort()
				return total, err
			}
			total++
		}
		after = tab.PKValues(batch[len(batch)-1])
		if _, err := s.commit(); err != nil {
			return total, err
		}
	}
}

func (s *Session) soleSnapshot() bool {
	if s == nil || s.db == nil || s.db.Eng == nil || s.db.Eng.TM == nil {
		return true
	}
	return s.db.Eng.TM.LiveSnapshots() <= 1
}

func (s *Session) canTruncate(tab *catalog.Table) bool {
	if tab == nil || len(tab.Indexes) > 0 || tab.HasVector() || s.hasTriggers(tab.ID) {
		return false
	}
	if len(s.inboundFKs(tab)) > 0 {
		return false
	}
	return s.soleSnapshot()
}

func (s *Session) truncateHeap(tab *catalog.Table) (int64, error) {
	if s.x != nil {
		return 0, nerr.New(nerr.InvalidArgument, "executor.truncateHeap", "bulk heap replacement cannot run inside a transaction")
	}
	owned := false
	if s.x == nil {
		if _, err := s.begin(txn.SnapshotIsolation); err != nil {
			return 0, err
		}
		owned = true
	}
	n, err := s.countHeap(tab, nil, nil, true, true)
	if err != nil {
		if owned {
			_ = s.abort()
		}
		return 0, err
	}
	if n > maxHeapSwapCDCChanges {
		if !owned {
			return 0, nerr.New(nerr.Exhausted, "executor.truncateHeap", "heap-swap CDC identity batch exceeds transaction limit")
		}
		if err := s.abort(); err != nil {
			return 0, err
		}
		return s.bulkDeleteRows(tab)
	}
	var (
		after  []types.Value
		staged int64
	)
	for staged < n {
		rows, err := s.nextDMLBatch(tab, nil, after, bulkCommitEvery)
		if err != nil {
			if owned {
				_ = s.abort()
			}
			return 0, err
		}
		if len(rows) == 0 {
			if owned {
				_ = s.abort()
			}
			return 0, nerr.New(nerr.Conflict, "executor.truncateHeap", "heap changed while preparing CDC delete identities")
		}
		for _, row := range rows {
			if err := s.stageRowChange(tab, wal.ChangeDelete, row, nil); err != nil {
				if owned {
					_ = s.abort()
				}
				return 0, err
			}
			staged++
		}
		after = append(after[:0], tab.PKValues(rows[len(rows)-1])...)
	}
	if staged != n {
		if owned {
			_ = s.abort()
		}
		return 0, nerr.New(nerr.Conflict, "executor.truncateHeap", "heap changed while preparing CDC delete identities")
	}
	s.db.Eng.Enter(s.x.owner.Storage())
	heap, err := btree.CreateDetached(s.db.Eng)
	s.db.Eng.Leave(s.x.owner.Storage())
	if err != nil {
		if owned {
			_ = s.abort()
		}
		return 0, err
	}
	neu := tab.Clone()
	neu.HeapMeta = heap.Meta()
	raw, err := catalog.EncodeTable(neu)
	if err != nil {
		if owned {
			_ = s.abort()
		}
		return 0, err
	}
	if err := s.x.use(s.db.CatTree).Update(catalog.TableKey(neu.Name), raw); err != nil {
		if owned {
			_ = s.abort()
		}
		return 0, err
	}
	if s.overlay != nil {
		s.overlay[neu.Name] = neu
	}
	if s.pending != nil {
		if s.pending.heaps == nil {
			s.pending.heaps = make(map[string]*btree.Tree)
		}
		s.pending.heaps[neu.Name] = heap
	}
	if owned {
		if _, err := s.commit(); err != nil {
			return 0, err
		}
	}
	return n, nil
}

func (s *Session) patchDecimal(tab *catalog.Table, ci int, val types.Value) (int64, error) {
	cols := tab.Types()
	var (
		after []byte
		total int64
	)
	for {
		if _, err := s.begin(txn.SnapshotIsolation); err != nil {
			return total, err
		}
		heap, err := s.heapOf(tab)
		if err != nil {
			_ = s.abort()
			return total, err
		}
		htx := s.x.use(heap)
		var rowBuf []byte
		last, n, err := htx.PatchVisible(after, bulkCommitEvery, func(key []byte, payload []byte) ([]byte, error) {
			var err error
			rowBuf, err = types.ReplaceRowColumnInto(rowBuf, payload, cols, ci, val)
			if err != nil {
				return nil, err
			}
			if err := s.stageEncodedChange(tab, wal.ChangeUpdate, key, key, payload, rowBuf, "", ""); err != nil {
				return nil, err
			}
			return rowBuf, nil
		})
		if err != nil {
			_ = s.abort()
			return total, err
		}
		total += int64(n)
		if n == 0 {
			_, err := s.commit()
			return total, err
		}
		after = last
		if _, err := s.commit(); err != nil {
			return total, err
		}
	}
}
