package xport

import (
	"crypto/sha256"
	"os"

	"github.com/bzync/nextsql/internal/crypto"
	"github.com/bzync/nextsql/internal/executor"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/sql/types"
)

// Verify checks the export inventory, decrypts the payload, and optionally
// import-tests into a temporary database. A successful write is not a
// valid export; this function is the gate.
func Verify(src string, keys crypto.KeyProvider, root *crypto.DEK, importTest bool) error {
	if src == "" {
		return nerr.New(nerr.InvalidArgument, "xport.Verify", "source is required")
	}
	if keys == nil && root == nil {
		return nerr.New(nerr.InvalidArgument, "xport.Verify", "nil key provider")
	}
	return verifyDir(src, keys, root, importTest)
}

func verifyDir(src string, keys crypto.KeyProvider, root *crypto.DEK, importTest bool) error {
	hdr, dumps, err := loadDump(src, keys, root)
	if err != nil {
		return err
	}
	_ = hdr
	if !importTest {
		return nil
	}
	tmp, err := os.MkdirTemp("", "nextsql-import-test-")
	if err != nil {
		return nerr.Wrap(nerr.IO, "xport.Verify", "temp", err)
	}
	defer os.RemoveAll(tmp)

	dek, err := crypto.GenerateDEK(1)
	if err != nil {
		return err
	}
	testKeys, err := crypto.NewMemoryKeyProvider(dek)
	if err != nil {
		return err
	}
	db, _, err := openOrCreateDest(tmp, testKeys, 64)
	if err != nil {
		return nerr.Wrap(nerr.Corruption, "xport.Verify", "import test failed to create dest", err)
	}
	defer db.Close()
	if _, err := applyDump(db, dumps); err != nil {
		return nerr.Wrap(nerr.Corruption, "xport.Verify", "import test failed", err)
	}
	// Confirm every table is queryable after commit.
	s := db.Session()
	for _, d := range dumps {
		if _, err := s.Exec("SELECT * FROM " + quoteIdent(d.Table.Name) + " LIMIT 1"); err != nil {
			return nerr.Wrap(nerr.Corruption, "xport.Verify", "import test query failed", err)
		}
	}
	return compareImported(s, dumps)
}

// compareImported requires every imported table to hold exactly the rows its
// dump holds, value for value. Loading without an error is not enough: an
// import that rewrote values would pass it, and one did -- INSERT used to
// replace a restored NULL in a defaulted column with the column default.
//
// Rows are compared as a multiset of digests of their canonical dump record,
// so the comparison does not depend on scan order, and the read-back streams
// through ForEachVisible, so a large table is neither buffered as a result nor
// bound by statement limits. The extra memory is one 32-byte digest per row,
// next to dumps that are already in memory.
func compareImported(s *executor.Session, dumps []tableDump) error {
	for _, d := range dumps {
		want := make(map[[32]byte]int, len(d.Rows))
		for _, row := range d.Rows {
			rec, err := encodeRowRec(d.Table.Name, row)
			if err != nil {
				return err
			}
			want[sha256.Sum256(rec)]++
		}
		var got int
		err := s.ForEachVisible(d.Table.Name, func(row []types.Value) error {
			rec, err := encodeRowRec(d.Table.Name, row)
			if err != nil {
				return err
			}
			sum := sha256.Sum256(rec)
			if want[sum] == 0 {
				return nerr.New(nerr.Corruption, "xport.Verify", "import test: table "+d.Table.Name+" holds a row that differs from the export")
			}
			want[sum]--
			got++
			return nil
		})
		if err != nil {
			if nerr.HasCode(err, nerr.Corruption) {
				return err
			}
			return nerr.Wrap(nerr.Corruption, "xport.Verify", "import test read-back failed", err)
		}
		if got != len(d.Rows) {
			return nerr.New(nerr.Corruption, "xport.Verify", "import test: table "+d.Table.Name+" row count differs from the export")
		}
	}
	return nil
}

func (p Point) String() string {
	switch p {
	case PointBeforeWrite:
		return "before_write"
	case PointDuringWrite:
		return "during_write"
	case PointBeforeVerify:
		return "before_verify"
	default:
		return "none"
	}
}
