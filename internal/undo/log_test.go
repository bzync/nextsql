package undo

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bzync/nextsql/internal/crypto"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/storage/format"
	"github.com/bzync/nextsql/internal/storage/row"
)

func testKeys(t *testing.T) *crypto.MemoryKeyProvider {
	t.Helper()
	dek, err := crypto.GenerateDEK(1)
	if err != nil {
		t.Fatal(err)
	}
	keys, err := crypto.NewMemoryKeyProvider(dek)
	if err != nil {
		t.Fatal(err)
	}
	return keys
}

func TestUndoRoundTripAndWrongKey(t *testing.T) {
	id, err := format.NewIdentity()
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "db.undo")
	keys := testKeys(t)
	lg, err := Create(dir, keys, id)
	if err != nil {
		t.Fatal(err)
	}
	uid, err := lg.Append(Record{
		Txn:  3,
		Kind: KindUpdate,
		Key:  []byte("k"),
		Old:  row.Version{Xmin: 1, Payload: []byte("old")},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := lg.Get(uid)
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Key) != "k" || string(got.Old.Payload) != "old" || got.Txn != 3 {
		t.Fatalf("got %+v", got)
	}
	if err := lg.Close(); err != nil {
		t.Fatal(err)
	}

	lg, err = Open(dir, keys, id)
	if err != nil {
		t.Fatal(err)
	}
	defer lg.Close()
	got, err = lg.Get(uid)
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Old.Payload) != "old" {
		t.Fatalf("reopen %q", got.Old.Payload)
	}

	other, err := crypto.GenerateDEK(1)
	if err != nil {
		t.Fatal(err)
	}
	wrong, err := crypto.NewMemoryKeyProvider(other)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Open(dir, wrong, id); !nerr.HasCode(err, nerr.Crypto) {
		t.Fatalf("wrong key: %v", err)
	}
}

func TestUndoChain(t *testing.T) {
	id, err := format.NewIdentity()
	if err != nil {
		t.Fatal(err)
	}
	lg, err := Create(filepath.Join(t.TempDir(), "db.undo"), testKeys(t), id)
	if err != nil {
		t.Fatal(err)
	}
	defer lg.Close()
	a, err := lg.Append(Record{Txn: 1, Kind: KindInsert, Key: []byte("k")})
	if err != nil {
		t.Fatal(err)
	}
	b, err := lg.Append(Record{Txn: 1, Kind: KindUpdate, Key: []byte("k"), Old: row.Version{Payload: []byte("v")}})
	if err != nil {
		t.Fatal(err)
	}
	chain := lg.Chain(b)
	if len(chain) != 2 || chain[0].ID != b || chain[1].ID != a {
		t.Fatalf("chain %+v", chain)
	}
}

func TestVacuumDurablyRemovesForgottenTransactions(t *testing.T) {
	id, err := format.NewIdentity()
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "db.undo")
	keys := testKeys(t)
	lg, err := Create(dir, keys, id)
	if err != nil {
		t.Fatal(err)
	}
	a, err := lg.Append(Record{Txn: 10, Kind: KindInsert, Key: []byte("forgotten-a")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lg.Append(Record{Txn: 10, Kind: KindUpdate, Key: []byte("forgotten-b"), Old: row.Version{Payload: make([]byte, 1024)}}); err != nil {
		t.Fatal(err)
	}
	keep, err := lg.Append(Record{Txn: 11, Kind: KindInsert, Key: []byte("retained")})
	if err != nil {
		t.Fatal(err)
	}
	if err := lg.Flush(); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(filepath.Join(dir, logName))
	if err != nil {
		t.Fatal(err)
	}
	lg.ForgetTxn(10)
	if err := lg.Vacuum(); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(filepath.Join(dir, logName))
	if err != nil {
		t.Fatal(err)
	}
	if after.Size() >= before.Size() {
		t.Fatalf("vacuum did not shrink log: before=%d after=%d", before.Size(), after.Size())
	}
	if err := lg.Close(); err != nil {
		t.Fatal(err)
	}
	lg, err = Open(dir, keys, id)
	if err != nil {
		t.Fatal(err)
	}
	defer lg.Close()
	if _, err := lg.Get(a); !nerr.HasCode(err, nerr.NotFound) {
		t.Fatalf("forgotten record rehydrated: %v", err)
	}
	if _, err := lg.Get(keep); err != nil {
		t.Fatalf("retained record lost: %v", err)
	}
	if next, err := lg.Append(Record{Txn: 12, Kind: KindInsert, Key: []byte("new")}); err != nil || next <= keep {
		t.Fatalf("append after vacuum id=%d err=%v", next, err)
	}
}

func TestForgetBeforeHonorsHorizonPredicateAndSkipsStalePasses(t *testing.T) {
	id, err := format.NewIdentity()
	if err != nil {
		t.Fatal(err)
	}
	lg, err := Create(filepath.Join(t.TempDir(), "db.undo"), testKeys(t), id)
	if err != nil {
		t.Fatal(err)
	}
	defer lg.Close()
	for _, txn := range []format.TxnID{1, 2, 3, 5} {
		if _, err := lg.Append(Record{Txn: txn, Kind: KindInsert, Key: []byte{byte(txn)}}); err != nil {
			t.Fatal(err)
		}
	}
	notCommitted := map[format.TxnID]bool{2: true}
	canForget := func(txn format.TxnID) bool { return !notCommitted[txn] }

	// Horizon 4: txn 1 and 3 are forgotten, 2 (not committed) and 5 (at or
	// above the horizon) stay.
	lg.ForgetBefore(4, canForget)
	for txn, want := range map[format.TxnID]bool{1: false, 2: true, 3: false, 5: true} {
		if got := lg.Head(txn) != 0; got != want {
			t.Fatalf("txn %d retained=%v, want %v", txn, got, want)
		}
	}
	// A pass at a horizon that has not advanced is a no-op even if the
	// predicate would now allow more.
	delete(notCommitted, 2)
	lg.ForgetBefore(4, canForget)
	if lg.Head(2) == 0 {
		t.Fatal("pass at an unchanged horizon forgot txn 2")
	}
	// Once the horizon advances, everything below it is reconsidered.
	lg.ForgetBefore(6, canForget)
	if got := lg.RetainedTxns(); got != 0 {
		t.Fatalf("retained txns = %d, want 0", got)
	}
}
