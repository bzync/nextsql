package cdc

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/bzync/nextsql/internal/crypto"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/storage"
	"github.com/bzync/nextsql/internal/storage/format"
	"github.com/bzync/nextsql/internal/wal"
)

func changeRecord(t *testing.T, txn format.TxnID, prev, lsn format.LSN, c wal.Change) wal.Record {
	t.Helper()
	rec, err := wal.ChangeRec(txn, prev, c)
	if err != nil {
		t.Fatal(err)
	}
	rec.LSN = lsn
	return rec
}

func TestDecoderEmitsCommittedOnlyInOrder(t *testing.T) {
	d := NewDecoder(Limits{})
	aborted := changeRecord(t, 1, 1, 2, wal.Change{Operation: wal.ChangeInsert, TableID: 7, Table: "orders", Key: []byte("a")})
	if tx, _, err := d.Feed(aborted); err != nil || tx != nil {
		t.Fatalf("stage aborted: tx=%v err=%v", tx, err)
	}
	if tx, _, err := d.Feed(wal.Record{Type: wal.RecAbort, TxnID: 1, PrevLSN: 2, LSN: 3}); err != nil || tx != nil {
		t.Fatalf("abort: tx=%v err=%v", tx, err)
	}

	r1 := changeRecord(t, 2, 4, 5, wal.Change{Operation: wal.ChangeInsert, TableID: 7, Table: "orders", Tenant: "a", Key: []byte("1")})
	r2 := changeRecord(t, 2, 5, 6, wal.Change{Operation: wal.ChangeUpdate, TableID: 7, Table: "orders", Tenant: "a", OldTenant: "a", Key: []byte("2"), OldKey: []byte("1")})
	for _, rec := range []wal.Record{r1, r2} {
		if tx, _, err := d.Feed(rec); err != nil || tx != nil {
			t.Fatalf("change: tx=%v err=%v", tx, err)
		}
	}
	tx, committed, err := d.Feed(wal.Record{Type: wal.RecCommit, TxnID: 2, PrevLSN: 6, LSN: 7})
	if err != nil || !committed || tx == nil {
		t.Fatalf("commit: tx=%v committed=%v err=%v", tx, committed, err)
	}
	if tx.Token != 7 || len(tx.Events) != 2 || tx.Events[0].ChangeLSN != 5 || tx.Events[1].ChangeLSN != 6 {
		t.Fatalf("transaction order: %+v", tx)
	}
}

func TestDecoderDropsCrashStrandedBatch(t *testing.T) {
	d := NewDecoder(Limits{})
	stranded := changeRecord(t, 1, 1, 2, wal.Change{Operation: wal.ChangeDelete, TableID: 1, Table: "t", Key: []byte("old")})
	if _, _, err := d.Feed(stranded); err != nil {
		t.Fatal(err)
	}
	if _, _, err := d.Feed(wal.Record{Type: wal.RecBegin, TxnID: 2, LSN: 3}); err != nil {
		t.Fatal(err)
	}
	if len(d.pending) != 0 || d.pendingBytes != 0 {
		t.Fatalf("stranded state retained: txns=%d bytes=%d", len(d.pending), d.pendingBytes)
	}
}

func TestDecoderLimitsAndTenantFilter(t *testing.T) {
	d := NewDecoder(Limits{MaxChangesPerTxn: 1, MaxPendingBytes: 1024})
	r1 := changeRecord(t, 1, 1, 2, wal.Change{Operation: wal.ChangeInsert, TableID: 1, Table: "t", Tenant: "a", Key: []byte("1")})
	r2 := changeRecord(t, 1, 2, 3, wal.Change{Operation: wal.ChangeInsert, TableID: 1, Table: "t", Tenant: "b", Key: []byte("2")})
	if _, _, err := d.Feed(r1); err != nil {
		t.Fatal(err)
	}
	if _, _, err := d.Feed(r2); !nerr.HasCode(err, nerr.Exhausted) {
		t.Fatalf("limit: %v", err)
	}
	tx := &Transaction{Events: []Event{{Operation: wal.ChangeInsert, TableID: 1, Table: "t", Tenant: "a"}, {Operation: wal.ChangeInsert, TableID: 1, Table: "t", Tenant: "b"}}}
	got := filterTransaction(tx, Filter{Tenant: "a", TableIDs: map[uint32]struct{}{1: {}}, Operations: map[wal.ChangeOperation]struct{}{wal.ChangeUpdate: {}}})
	if got != nil {
		t.Fatalf("operation filter accepted insert: %+v", got)
	}
	got = filterTransaction(tx, Filter{Tenant: "a", TableIDs: map[uint32]struct{}{1: {}}, Operations: map[wal.ChangeOperation]struct{}{wal.ChangeInsert: {}}})
	if got == nil || len(got.Events) != 1 || got.Events[0].Tenant != "a" {
		t.Fatalf("filter: %+v", got)
	}
}

func TestSubscriptionResumeAcrossRestart(t *testing.T) {
	dek, err := crypto.GenerateDEK(1)
	if err != nil {
		t.Fatal(err)
	}
	keys, err := crypto.NewMemoryKeyProvider(dek)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "cdc.db")
	eng, err := storage.Create(path, keys, 8)
	if err != nil {
		t.Fatal(err)
	}
	commit := func(key string) {
		t.Helper()
		tx, err := eng.StartTxn()
		if err != nil {
			t.Fatal(err)
		}
		if err := tx.StageChange(wal.Change{Operation: wal.ChangeInsert, TableID: 9, Table: "events", Tenant: "tenant-a", Key: []byte(key)}); err != nil {
			t.Fatal(err)
		}
		if err := eng.CommitTxn(tx); err != nil {
			t.Fatal(err)
		}
	}
	commit("one")
	sub, err := Subscribe(eng.WAL, 0, Filter{Tenant: "tenant-a"}, Limits{PollInterval: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	first, err := sub.Next(ctx)
	cancel()
	if err != nil || len(first.Events) != 1 || string(first.Events[0].Key) != "one" {
		t.Fatalf("first: tx=%+v err=%v", first, err)
	}
	token := first.Token
	sub.Close()
	if err := eng.Close(); err != nil {
		t.Fatal(err)
	}
	eng, err = storage.Open(path, keys, 8)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	commit("two")
	resumed, err := Subscribe(eng.WAL, token, Filter{Tenant: "tenant-a"}, Limits{PollInterval: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer resumed.Close()
	ctx, cancel = context.WithTimeout(context.Background(), time.Second)
	second, err := resumed.Next(ctx)
	cancel()
	if err != nil || len(second.Events) != 1 || string(second.Events[0].Key) != "two" || second.Token <= token {
		t.Fatalf("second: tx=%+v err=%v", second, err)
	}
}
