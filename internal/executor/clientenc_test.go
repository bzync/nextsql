package executor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bzync/nextsql/internal/clientenc"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/sql/types"
)

type executorFieldKeys struct{ key clientenc.Key }

func (p executorFieldKeys) CurrentFieldKey(context.Context, string, string, string) (clientenc.Key, error) {
	return p.key, nil
}
func (p executorFieldKeys) FieldKey(_ context.Context, _, _, _, id string) (clientenc.Key, error) {
	if id != p.key.ID {
		return clientenc.Key{}, nerr.New(nerr.NotFound, "test", "field key missing")
	}
	return p.key, nil
}

func TestEncryptedClientOpaqueWriteRestartAndSQLGuards(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keys := testKeys(t)
	db, err := Create(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	s := db.Session()
	execOK(t, s, `CREATE TABLE accounts (id STRING PRIMARY KEY, secret TEXT ENCRYPTED CLIENT NOT NULL)`)
	fieldKey := clientenc.Key{ID: "customer-v1"}
	for i := range fieldKey.Material {
		fieldKey.Material[i] = 9
	}
	provider := executorFieldKeys{key: fieldKey}
	const plaintext = "never-on-the-server-4d2c1e"
	ciphertext, err := clientenc.Encrypt(context.Background(), provider, "app", "accounts", "secret", types.TextValue(plaintext))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ExecContext(context.Background(), `INSERT INTO accounts (id, secret) VALUES ('1', $1)`, []Param{{Value: types.StringValue(plaintext)}}); err == nil {
		t.Fatal("server accepted plaintext for ENCRYPTED CLIENT")
	}
	if _, err := s.ExecContext(context.Background(), `INSERT INTO accounts (id, secret) VALUES ('1', $1)`, []Param{{Value: types.StringValue(ciphertext)}}); err != nil {
		t.Fatal(err)
	}
	for _, sql := range []string{
		`SELECT secret FROM accounts WHERE secret = $1`,
		`SELECT LENGTH(secret) FROM accounts`,
		`SELECT secret FROM accounts ORDER BY secret`,
	} {
		if _, err := s.Exec(sql); err == nil {
			t.Fatalf("server accepted plaintext operation: %s", sql)
		}
	}
	res := execOK(t, s, `SELECT secret FROM accounts WHERE id = '1'`)
	if len(res.Rows) != 1 || res.Rows[0][0].Str != ciphertext || strings.Contains(res.Rows[0][0].Str, plaintext) {
		t.Fatalf("server result is not opaque ciphertext: %+v", res.Rows)
	}
	meta := execOK(t, s, `SELECT type FROM system.columns WHERE table_name = 'accounts' AND column_name = 'secret'`)
	if len(meta.Rows) != 1 || meta.Rows[0][0].Str != "TEXT ENCRYPTED CLIENT" {
		t.Fatalf("system.columns metadata: %+v", meta.Rows)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	got := execOK(t, reopened.Session(), `SELECT secret FROM accounts WHERE id = '1'`)
	value, err := clientenc.Decrypt(context.Background(), provider, "app", "accounts", "secret", got.Rows[0][0].Str)
	if err != nil || value.Str != plaintext || value.Typ.Kind != types.KindText {
		t.Fatalf("decrypt after restart: value=%+v err=%v", value, err)
	}

	// Page/WAL/UNDO encryption was already mandatory; this additionally pins
	// that the client plaintext never entered any server persistence path.
	_ = reopened.Close()
	if err := filepath.Walk(dir, func(name string, info os.FileInfo, walkErr error) error {
		if walkErr != nil || info == nil || info.IsDir() {
			return walkErr
		}
		raw, readErr := os.ReadFile(name)
		if readErr != nil {
			return readErr
		}
		if strings.Contains(string(raw), plaintext) {
			t.Fatalf("plaintext present in server file %s", name)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
