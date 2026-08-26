package xport

import (
	"os"

	"github.com/bzync/nextsql/internal/crypto"
	"github.com/bzync/nextsql/internal/nerr"
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
