package catalog

import (
	"testing"

	"github.com/bzync/nextsql/internal/sql/ast"
	"github.com/bzync/nextsql/internal/sql/types"
)

func TestClientEncryptedColumnCatalogV10(t *testing.T) {
	tab, err := TableFromAST(7, ast.CreateTable{
		Name: "accounts",
		Columns: []ast.ColumnDef{
			{Name: "id", Type: types.UUID(), Primary: true},
			{Name: "ssn", Type: types.Text(), EncryptedClient: true, NotNull: true},
		},
		PK: []string{"id"},
	})
	if err != nil {
		t.Fatal(err)
	}
	col := tab.Columns[1]
	if col.Type.Kind != types.KindString || col.ClientType.Kind != types.KindText || !col.ClientEncrypted() {
		t.Fatalf("column = %+v", col)
	}
	raw, err := EncodeTable(tab)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeTable(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Columns[1].ClientEncrypted() || got.Columns[1].ClientType.Kind != types.KindText {
		t.Fatalf("round trip = %+v", got.Columns[1])
	}
	bad := append([]byte(nil), raw...)
	// The second column's ENCRYPTED CLIENT flag is thirteen bytes from the
	// end: flag + type(6), then v11's 2-byte-per-column ENUM label count,
	// v12's collection flag, and v13's encryption-mode byte for each column.
	bad[len(bad)-15] = 2
	if _, err := DecodeTable(bad); err == nil {
		t.Fatal("accepted unknown ENCRYPTED CLIENT flag")
	}
	badType := append([]byte(nil), raw...)
	badType[len(badType)-13] = 9 // VecElem in the six-byte catalog type.
	if _, err := DecodeTable(badType); err == nil {
		t.Fatal("accepted non-canonical ENCRYPTED CLIENT logical type")
	}

	// A real v9 descriptor has no v10 flags/types (or v11 ENUM label counts).
	// Synthesise that ending and prove it remains readable as an ordinary
	// physical STRING column.
	v9 := append([]byte(nil), raw[:len(raw)-8-4-2-2]...) // v10 flags/types, v11 labels, v12 collections, v13 modes
	v9[4], v9[5] = byte(tableVersionV9), 0
	legacy, err := DecodeTable(v9)
	if err != nil {
		t.Fatal(err)
	}
	if legacy.Columns[1].ClientEncrypted() || legacy.Columns[1].Type.Kind != types.KindString {
		t.Fatalf("v9 compatibility column = %+v", legacy.Columns[1])
	}
}

func TestClientEncryptionModeCatalogV13(t *testing.T) {
	tab, err := TableFromAST(8, ast.CreateTable{Name: "accounts", Columns: []ast.ColumnDef{
		{Name: "id", Type: types.UUID(), Primary: true},
		{Name: "email", Type: types.String(), EncryptedClient: true},
	}, PK: []string{"id"}})
	if err != nil {
		t.Fatal(err)
	}
	tab.Columns[1].ClientEncryptionMode = ClientEncryptionDeterministic
	raw, err := EncodeTable(tab)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeTable(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.Columns[1].ClientEncryptionMode != ClientEncryptionDeterministic {
		t.Fatalf("mode = %d", got.Columns[1].ClientEncryptionMode)
	}

	v12 := append([]byte(nil), raw[:len(raw)-len(tab.Columns)]...)
	v12[4], v12[5] = byte(tableVersionV12), 0
	legacy, err := DecodeTable(v12)
	if err != nil {
		t.Fatal(err)
	}
	if legacy.Columns[1].ClientEncryptionMode != ClientEncryptionRandomized {
		t.Fatalf("v12 mode = %d, want randomized compatibility default", legacy.Columns[1].ClientEncryptionMode)
	}

	bad := append([]byte(nil), raw...)
	bad[len(bad)-1] = 2
	if _, err := DecodeTable(bad); err == nil {
		t.Fatal("accepted unknown client-encryption mode")
	}
	bad = append([]byte(nil), raw...)
	bad[len(bad)-2] = ClientEncryptionDeterministic
	if _, err := DecodeTable(bad); err == nil {
		t.Fatal("accepted encryption mode on plaintext column")
	}
}

func TestClientEncryptedColumnRejectsServerFeatures(t *testing.T) {
	base := ast.CreateTable{Name: "t", Columns: []ast.ColumnDef{
		{Name: "id", Type: types.UUID(), Primary: true},
		{Name: "secret", Type: types.String(), EncryptedClient: true},
	}, PK: []string{"id"}}
	if _, err := TableFromAST(1, base); err != nil {
		t.Fatal(err)
	}
	base.PK = []string{"secret"}
	if _, err := TableFromAST(1, base); err == nil {
		t.Fatal("accepted encrypted primary key")
	}
	base.PK = []string{"id"}
	base.Columns[1].Default = ast.Literal{Value: types.StringValue("plaintext")}
	if _, err := TableFromAST(1, base); err == nil {
		t.Fatal("accepted encrypted server default")
	}
}
