package catalog

import (
	"crypto/sha256"
	"strings"
	"testing"

	"github.com/bzync/nextsql/internal/sql/ast"
	"github.com/bzync/nextsql/internal/sql/types"
)

func TestTableEncodeRoundTrip(t *testing.T) {
	stmt := ast.CreateTable{
		Name: "products",
		Columns: []ast.ColumnDef{
			{Name: "id", Type: types.UUID(), Primary: true, NotNull: true, Default: ast.Call{Name: "uuid"}},
			{Name: "name", Type: types.String(), NotNull: true},
			{Name: "price", Type: mustDec(t), Default: ast.Literal{Value: types.DecimalValue(mustParseDec(t, "0"), mustDec(t))}},
		},
		PK: []string{"id"},
	}
	tab, err := TableFromAST(1, stmt)
	if err != nil {
		t.Fatal(err)
	}
	tab.HeapMeta = 9
	tab.CDCImages = CDCImagesFull
	tab.Indexes = []Index{
		{Name: "ix_loc", Spatial: true, Columns: []int{1}, Meta: 12},
		{Name: "ix_cat", Columns: []int{1}, Path: []string{"category"}, Meta: 13},
		{Name: "ix_body", Fulltext: true, Columns: []int{1}, Meta: 14},
		{Name: "ix_emb", Vector: true, Columns: []int{1}, Meta: 15},
	}
	raw, err := EncodeTable(tab)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeTable(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "products" || got.ID != 1 || got.HeapMeta != 9 || len(got.Columns) != 3 || len(got.PK) != 1 {
		t.Fatalf("%+v", got)
	}
	if got.CDCImages != CDCImagesFull {
		t.Fatalf("CDC images=%v", got.CDCImages)
	}
	if got.Columns[0].Default.Kind != DefUUID || got.Columns[2].Default.Kind != DefLiteral {
		t.Fatalf("defaults %+v", got.Columns)
	}
	if len(got.Indexes) != 4 || !got.Indexes[0].Spatial || got.Indexes[0].Name != "ix_loc" {
		t.Fatalf("spatial index %+v", got.Indexes)
	}
	if got.Indexes[1].Name != "ix_cat" || len(got.Indexes[1].Path) != 1 || got.Indexes[1].Path[0] != "category" {
		t.Fatalf("path index %+v", got.Indexes[1])
	}
	if !got.Indexes[2].Fulltext || got.Indexes[2].Name != "ix_body" {
		t.Fatalf("fulltext index %+v", got.Indexes[2])
	}
	if got.Indexes[2].FTAnalyzer != 0 || got.Indexes[2].FTVersion != 0 {
		t.Fatalf("default analyzer %+v", got.Indexes[2])
	}
	if !got.Indexes[3].Vector || got.Indexes[3].Name != "ix_emb" {
		t.Fatalf("vector index %+v", got.Indexes[3])
	}
	// v6 appends one traversal-quantisation byte per index after the
	// partitioning descriptor, v7 a further 9 bytes per index (method + IVF
	// list/probe counts), v8 a further 4 bytes per index (IVF-PQ subspace
	// count), and v9 a further 3 bytes per index (full-text analyzer id +
	// revision); strip all of that plus the partitioning byte to synthesise a
	// legacy v3 body, and one more for v2.
	trailer := len(got.Indexes)*17 + 1 + len(got.Columns) // v10 adds one zero flag per unencrypted column
	v3 := append([]byte(nil), raw[:len(raw)-trailer]...)
	v3[4], v3[5] = byte(tableVersionV3), 0
	legacyV3, err := DecodeTable(v3)
	if err != nil {
		t.Fatal(err)
	}
	if legacyV3.CDCImages != CDCImagesFull || legacyV3.Partitioning != nil {
		t.Fatalf("v3 decode=%+v", legacyV3)
	}
	v2 := append([]byte(nil), raw[:len(raw)-trailer-1]...)
	v2[4], v2[5] = byte(tableVersionV2), 0
	legacy, err := DecodeTable(v2)
	if err != nil {
		t.Fatal(err)
	}
	if legacy.CDCImages != CDCImagesKeys {
		t.Fatalf("v2 image default=%v", legacy.CDCImages)
	}
}

func TestTableEncodeRoundTripBlobColumn(t *testing.T) {
	stmt := ast.CreateTable{
		Name: "files",
		Columns: []ast.ColumnDef{
			{Name: "id", Type: types.UUID(), Primary: true, NotNull: true, Default: ast.Call{Name: "uuid"}},
			{Name: "payload", Type: types.Blob(), NotNull: true},
		},
		PK: []string{"id"},
	}
	tab, err := TableFromAST(1, stmt)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := EncodeTable(tab)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeTable(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Columns) != 2 || got.Columns[1].Type.Kind != types.KindBlob || !got.Columns[1].NotNull {
		t.Fatalf("blob column round trip: %+v", got.Columns)
	}
}

func TestColumnFromASTRejectsBlobPrimaryKeyElsewhereUnaffected(t *testing.T) {
	// BLOB is allowed as a PRIMARY KEY (byte-lexicographic order is total),
	// unlike VECTOR. Confirms TableFromAST does not reject it.
	stmt := ast.CreateTable{
		Name:    "blobpk",
		Columns: []ast.ColumnDef{{Name: "k", Type: types.Blob(), Primary: true, NotNull: true}},
		PK:      []string{"k"},
	}
	if _, err := TableFromAST(1, stmt); err != nil {
		t.Fatalf("BLOB primary key should be allowed: %v", err)
	}
}

func TestColumnFromASTEncryptedClientBlob(t *testing.T) {
	stmt := ast.CreateTable{
		Name: "secrets",
		Columns: []ast.ColumnDef{
			{Name: "id", Type: types.UUID(), Primary: true, NotNull: true, Default: ast.Call{Name: "uuid"}},
			{Name: "secret", Type: types.Blob(), EncryptedClient: true},
		},
		PK: []string{"id"},
	}
	tab, err := TableFromAST(1, stmt)
	if err != nil {
		t.Fatal(err)
	}
	col := tab.Columns[1]
	if !col.ClientEncrypted() || col.ClientType.Kind != types.KindBlob || col.Type.Kind != types.KindString {
		t.Fatalf("ENCRYPTED CLIENT BLOB column: %+v", col)
	}
}

func TestTableEncodeFulltextAnalyzerV9(t *testing.T) {
	tab := &Table{
		ID: 1, Name: "articles",
		Columns: []Column{
			{Name: "id", Type: types.UUID(), NotNull: true, Primary: true},
			{Name: "body", Type: types.Text()},
		},
		PK: []int{0},
		Indexes: []Index{{
			Name: "ix_body", Fulltext: true, Columns: []int{1}, Meta: 14,
			FTAnalyzer: FTAnalyzerEnglish, FTVersion: FTAnalyzerEnglishV1,
		}},
	}
	raw, err := EncodeTable(tab)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeTable(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.Indexes[0].FTAnalyzer != FTAnalyzerEnglish || got.Indexes[0].FTVersion != FTAnalyzerEnglishV1 {
		t.Fatalf("analyzer %+v", got.Indexes[0])
	}
	tab.Indexes[0].FTVersion = FTAnalyzerEnglishV2
	raw2, err := EncodeTable(tab)
	if err != nil {
		t.Fatal(err)
	}
	got2, err := DecodeTable(raw2)
	if err != nil {
		t.Fatal(err)
	}
	if got2.Indexes[0].FTAnalyzer != FTAnalyzerEnglish || got2.Indexes[0].FTVersion != FTAnalyzerEnglishV2 {
		t.Fatalf("v2 analyzer %+v", got2.Indexes[0])
	}
	tab.Indexes[0].FTVersion = FTAnalyzerEnglishV3
	raw3, err := EncodeTable(tab)
	if err != nil {
		t.Fatal(err)
	}
	got3, err := DecodeTable(raw3)
	if err != nil {
		t.Fatal(err)
	}
	if got3.Indexes[0].FTAnalyzer != FTAnalyzerEnglish || got3.Indexes[0].FTVersion != FTAnalyzerEnglishV3 {
		t.Fatalf("v3 analyzer %+v", got3.Indexes[0])
	}
	for _, pair := range []struct {
		id  uint8
		ver uint16
	}{
		{FTAnalyzerFrench, FTAnalyzerFrenchV1},
		{FTAnalyzerGerman, FTAnalyzerGermanV1},
		{FTAnalyzerSpanish, FTAnalyzerSpanishV1},
	} {
		tab.Indexes[0].FTAnalyzer = pair.id
		tab.Indexes[0].FTVersion = pair.ver
		rawL, err := EncodeTable(tab)
		if err != nil {
			t.Fatalf("encode lang %d: %v", pair.id, err)
		}
		gotL, err := DecodeTable(rawL)
		if err != nil {
			t.Fatalf("decode lang %d: %v", pair.id, err)
		}
		if gotL.Indexes[0].FTAnalyzer != pair.id || gotL.Indexes[0].FTVersion != pair.ver {
			t.Fatalf("lang analyzer %+v", gotL.Indexes[0])
		}
	}
	// v8 ended after the IVF-PQ subspace counts; strip the 3-byte analyzer
	// trailer per index and the descriptor still decodes as simple.
	v8 := append([]byte(nil), raw[:len(raw)-3-len(tab.Columns)]...)
	v8[4], v8[5] = byte(tableVersionV8), 0
	legacy, err := DecodeTable(v8)
	if err != nil {
		t.Fatal(err)
	}
	if legacy.Indexes[0].FTAnalyzer != 0 || legacy.Indexes[0].FTVersion != 0 {
		t.Fatalf("v8 analyzer %+v", legacy.Indexes[0])
	}
	bad := append([]byte(nil), raw...)
	bad[len(bad)-len(tab.Columns)-3] = 99
	if _, err := DecodeTable(bad); err == nil {
		t.Fatal("expected unknown analyzer")
	}
	tab.Indexes[0].FTAnalyzer = FTAnalyzerEnglish
	tab.Indexes[0].FTVersion = 4
	if _, err := EncodeTable(tab); err == nil {
		t.Fatal("expected unknown english version")
	}
	tab.Indexes[0].FTAnalyzer = 0
	tab.Indexes[0].FTVersion = 2
	if _, err := EncodeTable(tab); err == nil {
		t.Fatal("expected invalid simple version")
	}
	tab.Indexes[0].Fulltext = false
	tab.Indexes[0].FTAnalyzer = FTAnalyzerEnglish
	tab.Indexes[0].FTVersion = FTAnalyzerEnglishV1
	if _, err := EncodeTable(tab); err == nil {
		t.Fatal("expected analyzer on non-fulltext index")
	}
}

func TestAIDefaultRoundTrip(t *testing.T) {
	dt, err := types.DecimalType(18, 0)
	if err != nil {
		t.Fatal(err)
	}
	stmt := ast.CreateTable{
		Name: "t",
		Columns: []ast.ColumnDef{
			{Name: "id", Type: dt, Primary: true, NotNull: true, Default: ast.Call{Name: "ai"}},
			{Name: "n", Type: types.String(), NotNull: true},
		},
		PK: []string{"id"},
	}
	tab, err := TableFromAST(1, stmt)
	if err != nil {
		t.Fatal(err)
	}
	if tab.Columns[0].Default.Kind != DefAI {
		t.Fatalf("kind %d", tab.Columns[0].Default.Kind)
	}
	raw, err := EncodeTable(tab)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeTable(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.Columns[0].Default.Kind != DefAI {
		t.Fatalf("decoded %+v", got.Columns[0].Default)
	}
}

func TestStoreGetPut(t *testing.T) {
	s := New()
	if s.PeekNext() != 1 {
		t.Fatal(s.PeekNext())
	}
	s.Put(&Table{ID: 3, Name: "t", Columns: []Column{{Name: "id", Type: types.UUID()}}, PK: []int{0}})
	got, ok := s.Get("t")
	if !ok || got.ID != 3 {
		t.Fatalf("%v %+v", ok, got)
	}
	if s.PeekNext() != 4 {
		t.Fatal(s.PeekNext())
	}
}

func TestPrimaryKeyRequired(t *testing.T) {
	_, err := TableFromAST(1, ast.CreateTable{Name: "t", Columns: []ast.ColumnDef{{Name: "n", Type: types.String()}}})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestReservedPrefixRejected(t *testing.T) {
	_, err := TableFromAST(1, ast.CreateTable{
		Name:    "nsql_lock",
		Columns: []ast.ColumnDef{{Name: "id", Type: types.String(), Primary: true}},
		PK:      []string{"id"},
	})
	if err == nil {
		t.Fatal("expected reserved prefix error")
	}
}

func TestHistoryTableNameAllowedInAST(t *testing.T) {
	tab, err := TableFromAST(1, ast.CreateTable{
		Name: HistoryTable,
		Columns: []ast.ColumnDef{
			{Name: "version", Type: types.String(), Primary: true},
			{Name: "name", Type: types.String(), NotNull: true},
		},
		PK: []string{"version"},
	})
	if err != nil || tab.Name != HistoryTable {
		t.Fatalf("%+v %v", tab, err)
	}
}

func TestMatchHistoryDDL(t *testing.T) {
	if !MatchHistoryDDL(HistoryDDL) {
		t.Fatal("canonical DDL must match itself")
	}
	spaced := "create   table   NSQL_SCHEMA_MIGRATIONS(\nversion STRING PRIMARY KEY,name STRING NOT NULL,applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),checksum STRING NOT NULL,execution_ms DECIMAL(12,0) NOT NULL,dirty DECIMAL(1,0) NOT NULL,direction STRING NOT NULL);"
	if !MatchHistoryDDL(spaced) {
		t.Fatal("whitespace and case must match after normalize")
	}
	wrong := `CREATE TABLE nsql_schema_migrations (version STRING PRIMARY KEY, name STRING NOT NULL)`
	if MatchHistoryDDL(wrong) {
		t.Fatal("wrong column list must not match")
	}
}

func TestDecodeTableV1EmptyForeignKeys(t *testing.T) {
	var buf []byte
	buf = append(buf, tableMagic...)
	buf = appendU16(buf, tableVersionV1)
	buf = appendU32(buf, 7)
	buf = appendString(buf, "legacy")
	buf = appendU64(buf, 11)
	buf = appendU16(buf, 1)
	buf = appendU16(buf, 0)
	buf = appendU16(buf, 1)
	var err error
	buf, err = appendColumn(buf, Column{Name: "id", Type: types.UUID(), NotNull: true, Primary: true})
	if err != nil {
		t.Fatal(err)
	}
	buf = appendU16(buf, 0)
	buf = appendU64(buf, 0)
	got, err := DecodeTable(buf)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "legacy" || got.ID != 7 || got.HeapMeta != 11 || len(got.ForeignKeys) != 0 {
		t.Fatalf("%+v", got)
	}
}

func TestDecodeTableUnknownVersion(t *testing.T) {
	var buf []byte
	buf = append(buf, tableMagic...)
	buf = appendU16(buf, 3)
	if _, err := DecodeTable(buf); err == nil {
		t.Fatal("expected unsupported version")
	}
}

func TestEncodeTableV2ForeignKeys(t *testing.T) {
	tab := &Table{
		ID: 2, Name: "orders",
		Columns: []Column{
			{Name: "id", Type: types.UUID(), NotNull: true, Primary: true},
			{Name: "customer_id", Type: types.UUID(), NotNull: true},
		},
		PK: []int{0},
		ForeignKeys: []ForeignKey{{
			Name: "fk_orders_customer_id", Columns: []int{1},
			RefTable: "customers", RefTableID: 1, RefColumns: []int{0},
			OnDelete: FKCascade, OnUpdate: FKRestrict,
		}},
	}
	raw, err := EncodeTable(tab)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeTable(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.ForeignKeys) != 1 {
		t.Fatalf("%+v", got.ForeignKeys)
	}
	fk := got.ForeignKeys[0]
	if fk.Name != "fk_orders_customer_id" || fk.RefTable != "customers" || fk.RefTableID != 1 {
		t.Fatalf("%+v", fk)
	}
	if len(fk.Columns) != 1 || fk.Columns[0] != 1 || len(fk.RefColumns) != 1 || fk.RefColumns[0] != 0 {
		t.Fatalf("%+v", fk)
	}
	if fk.OnDelete != FKCascade || fk.OnUpdate != FKRestrict {
		t.Fatalf("actions %+v", fk)
	}
	leftover := append(append([]byte(nil), raw...), 0)
	if _, err := DecodeTable(leftover); err == nil {
		t.Fatal("expected trailing bytes error")
	}
}

func TestCloneDeepCopiesForeignKeys(t *testing.T) {
	tab := &Table{
		Name:    "t",
		Columns: []Column{{Name: "id", Type: types.UUID()}},
		PK:      []int{0},
		ForeignKeys: []ForeignKey{{
			Name: "fk_t_id", Columns: []int{0}, RefTable: "p", RefColumns: []int{0},
		}},
	}
	c := tab.Clone()
	c.ForeignKeys[0].Columns[0] = 9
	c.ForeignKeys[0].RefColumns[0] = 8
	c.ForeignKeys[0].Name = "other"
	if tab.ForeignKeys[0].Columns[0] != 0 || tab.ForeignKeys[0].RefColumns[0] != 0 || tab.ForeignKeys[0].Name != "fk_t_id" {
		t.Fatalf("clone aliased: %+v", tab.ForeignKeys)
	}
}

func TestValidateForeignKeys(t *testing.T) {
	parent, err := TableFromAST(1, ast.CreateTable{
		Name: "customers",
		Columns: []ast.ColumnDef{
			{Name: "tenant_id", Type: types.UUID(), NotNull: true},
			{Name: "id", Type: types.UUID(), Primary: true, NotNull: true},
			{Name: "email", Type: types.String(), NotNull: true},
		},
		PK: []string{"tenant_id", "id"},
	})
	if err != nil {
		t.Fatal(err)
	}
	parent.Indexes = []Index{{Name: "ux_email", Unique: true, Columns: []int{0, 2}}}
	lookup := func(name string) (*Table, bool) {
		if name == "customers" {
			return parent, true
		}
		return nil, false
	}
	childStmt := ast.CreateTable{
		Name: "orders",
		Columns: []ast.ColumnDef{
			{Name: "tenant_id", Type: types.UUID(), NotNull: true},
			{Name: "id", Type: types.UUID(), Primary: true, NotNull: true},
			{Name: "customer_id", Type: types.UUID(), NotNull: true},
		},
		PK: []string{"tenant_id", "id"},
		FKs: []ast.ForeignKeyDef{{
			Name: "fk_orders_customer", Columns: []string{"tenant_id", "customer_id"},
			RefTable: "customers", RefCols: []string{"tenant_id", "id"},
		}},
	}
	child, err := TableFromAST(2, childStmt)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateForeignKeys(child, lookup); err != nil {
		t.Fatal(err)
	}
	if child.ForeignKeys[0].RefTableID != 1 || !sameOrds(child.ForeignKeys[0].RefColumns, []int{0, 1}) {
		t.Fatalf("%+v", child.ForeignKeys[0])
	}

	missing := childStmt
	missing.FKs[0].RefTable = "nope"
	bad, err := TableFromAST(3, missing)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateForeignKeys(bad, lookup); err == nil {
		t.Fatal("expected missing parent")
	}

	super := childStmt
	super.FKs[0].RefCols = []string{"tenant_id", "id"}
	// parent PK is (tenant_id, id); referencing a superkey is already exact PK. Use email-only.
	super.FKs = []ast.ForeignKeyDef{{
		Name: "fk_bad", Columns: []string{"customer_id"},
		RefTable: "customers", RefCols: []string{"email"},
	}}
	// also drop tenant so tenant rule does not fire first
	super.Columns = []ast.ColumnDef{
		{Name: "id", Type: types.UUID(), Primary: true, NotNull: true},
		{Name: "customer_id", Type: types.UUID(), NotNull: true},
	}
	super.PK = []string{"id"}
	bad, err = TableFromAST(4, super)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateForeignKeys(bad, lookup); err == nil {
		t.Fatal("expected non-unique target")
	}

	uniqStmt := ast.CreateTable{
		Name: "orders2",
		Columns: []ast.ColumnDef{
			{Name: "tenant_id", Type: types.UUID(), NotNull: true},
			{Name: "id", Type: types.UUID(), Primary: true, NotNull: true},
			{Name: "email", Type: types.String(), NotNull: true},
		},
		PK: []string{"id"},
		FKs: []ast.ForeignKeyDef{{
			Columns:  []string{"tenant_id", "email"},
			RefTable: "customers",
			RefCols:  []string{"tenant_id", "email"},
		}},
	}
	// child is not tenant-keyed? it has tenant_id. both keyed, FK includes tenant_id.
	uniq, err := TableFromAST(5, uniqStmt)
	if err != nil {
		t.Fatal(err)
	}
	if uniq.ForeignKeys[0].Name != "fk_orders2_tenant_id_email" {
		t.Fatalf("generated name %q", uniq.ForeignKeys[0].Name)
	}
	if err := ValidateForeignKeys(uniq, lookup); err != nil {
		t.Fatal(err)
	}

	revStmt := ast.CreateTable{
		Name: "orders3",
		Columns: []ast.ColumnDef{
			{Name: "tenant_id", Type: types.UUID(), NotNull: true},
			{Name: "id", Type: types.UUID(), Primary: true, NotNull: true},
			{Name: "customer_id", Type: types.UUID(), NotNull: true},
		},
		PK: []string{"tenant_id", "id"},
		FKs: []ast.ForeignKeyDef{{
			Name:     "fk_rev",
			Columns:  []string{"customer_id", "tenant_id"},
			RefTable: "customers",
			RefCols:  []string{"id", "tenant_id"},
		}},
	}
	rev, err := TableFromAST(6, revStmt)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateForeignKeys(rev, lookup); err != nil {
		t.Fatal(err)
	}

	mispair := revStmt
	mispair.Name = "orders4"
	mispair.FKs = []ast.ForeignKeyDef{{
		Name:     "fk_mis",
		Columns:  []string{"customer_id", "tenant_id"},
		RefTable: "customers",
		RefCols:  []string{"tenant_id", "id"},
	}}
	mp, err := TableFromAST(7, mispair)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateForeignKeys(mp, lookup); err != nil {
		t.Fatalf("tenant_id is no longer a special foreign-key column: %v", err)
	}
}

func TestFKDecimalPrecisionMustMatch(t *testing.T) {
	d10, err := types.DecimalType(10, 2)
	if err != nil {
		t.Fatal(err)
	}
	d5, err := types.DecimalType(5, 0)
	if err != nil {
		t.Fatal(err)
	}
	parent, err := TableFromAST(1, ast.CreateTable{
		Name: "codes",
		Columns: []ast.ColumnDef{
			{Name: "id", Type: d10, Primary: true, NotNull: true},
		},
		PK: []string{"id"},
	})
	if err != nil {
		t.Fatal(err)
	}
	child, err := TableFromAST(2, ast.CreateTable{
		Name: "uses",
		Columns: []ast.ColumnDef{
			{Name: "id", Type: types.UUID(), Primary: true, NotNull: true},
			{Name: "code", Type: d5, NotNull: true},
		},
		PK: []string{"id"},
		FKs: []ast.ForeignKeyDef{{
			Columns: []string{"code"}, RefTable: "codes", RefCols: []string{"id"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateForeignKeys(child, func(name string) (*Table, bool) {
		if name == "codes" {
			return parent, true
		}
		return nil, false
	}); err == nil {
		t.Fatal("expected decimal precision mismatch")
	}
}

func TestDecodeTableUnknownFKAction(t *testing.T) {
	tab := &Table{
		ID: 1, Name: "t",
		Columns:     []Column{{Name: "id", Type: types.UUID(), NotNull: true, Primary: true}},
		PK:          []int{0},
		ForeignKeys: []ForeignKey{{Name: "fk", Columns: []int{0}, RefTable: "p", RefTableID: 2, RefColumns: []int{0}}},
	}
	raw, err := EncodeTable(tab)
	if err != nil {
		t.Fatal(err)
	}
	raw[len(raw)-2] = 99
	if _, err := DecodeTable(raw); err == nil {
		t.Fatal("expected unknown action")
	}
}

func TestEncodeIndexCoveringPartialExpr(t *testing.T) {
	tab := &Table{
		ID: 4, Name: "items",
		Columns: []Column{
			{Name: "id", Type: types.UUID(), NotNull: true, Primary: true},
			{Name: "name", Type: types.String(), NotNull: true},
			{Name: "status", Type: types.String(), NotNull: true},
			{Name: "note", Type: types.Text()},
		},
		PK: []int{0},
		Indexes: []Index{{
			Name:    "ix_active_name",
			Columns: []int{1},
			Meta:    21,
			Include: []int{3},
			Predicate: ast.Binary{
				Op:    "=",
				Left:  ast.Ident{Name: "status"},
				Right: ast.Literal{Value: types.StringValue("active")},
			},
			Exprs:     []ast.Expr{ast.Call{Name: "lower", Args: []ast.Expr{ast.Ident{Name: "name"}}}},
			ExprTypes: []types.Type{types.String()},
		}},
	}
	raw, err := EncodeTable(tab)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeTable(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Indexes) != 1 {
		t.Fatalf("indexes %+v", got.Indexes)
	}
	idx := got.Indexes[0]
	if idx.Name != "ix_active_name" || len(idx.Include) != 1 || idx.Include[0] != 3 {
		t.Fatalf("include %+v", idx)
	}
	if !ExprEqual(idx.Predicate, tab.Indexes[0].Predicate) {
		t.Fatalf("predicate %+v", idx.Predicate)
	}
	if !idx.HasExpr() || !ExprEqual(idx.Exprs[0], tab.Indexes[0].Exprs[0]) {
		t.Fatalf("expr %+v", idx.Exprs)
	}
	if idx.ExprTypes[0].Kind != types.KindString {
		t.Fatalf("expr type %+v", idx.ExprTypes)
	}
	plain := &Table{
		ID: 5, Name: "t",
		Columns: []Column{{Name: "id", Type: types.UUID(), NotNull: true, Primary: true}},
		PK:      []int{0},
		Indexes: []Index{{Name: "ix_id", Columns: []int{0}, Meta: 3}},
	}
	raw, err = EncodeTable(plain)
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := DecodeTable(raw)
	if err != nil {
		t.Fatal(err)
	}
	if legacy.Indexes[0].HasExpr() || legacy.Indexes[0].Predicate != nil || len(legacy.Indexes[0].Include) != 0 {
		t.Fatalf("plain index grew extra fields: %+v", legacy.Indexes[0])
	}
}

func mustDec(t *testing.T) types.Type {
	t.Helper()
	d, err := types.DecimalType(12, 2)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestStatsEncodeRoundTrip(t *testing.T) {
	d, err := types.ParseDecimal("10")
	if err != nil {
		t.Fatal(err)
	}
	st := &TableStats{
		Table:   "t",
		TableID: 3,
		Rows:    100,
		Columns: []ColumnStats{{
			Ord: 0, Nulls: 2, NDV: 80, HasMinMax: true,
			Min: types.StringValue("a"), Max: types.StringValue("z"),
			Histogram:   []HistBucket{{Lower: types.StringValue("a"), Upper: types.StringValue("m"), Count: 50, NDV: 40}},
			MCV:         []MCV{{Value: types.StringValue("x"), Freq: 10}},
			Correlation: 0.5,
		}},
		Indexes:    []IndexStats{{Name: "ix", Selectivity: 0.1, NDV: 80, Unique: false}},
		Segments:   []SegmentStats{{ID: 1, Rows: 50, HasBounds: true, LowPK: []types.Value{types.DecimalValue(d, types.Type{Kind: types.KindDecimal})}, HighPK: []types.Value{types.DecimalValue(d, types.Type{Kind: types.KindDecimal})}}},
		Partitions: []PartitionStats{{ID: 1, Rows: 25}, {ID: 7, Rows: 75}},
	}
	raw, err := EncodeStats(st)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeStats(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.Table != "t" || got.Rows != 100 || len(got.Columns) != 1 || got.Columns[0].NDV != 80 || got.Columns[0].MCV[0].Value.Str != "x" {
		t.Fatalf("%+v", got)
	}
	if got.Indexes[0].Name != "ix" || got.Segments[0].ID != 1 {
		t.Fatalf("%+v", got)
	}
	if len(got.Partitions) != 2 || got.Partitions[0].Rows != 25 || got.Partitions[1].ID != 7 {
		t.Fatalf("partition stats: %+v", got.Partitions)
	}
	if _, err := DecodeStats(append(append([]byte(nil), raw...), 0)); err == nil {
		t.Fatal("stats decoder accepted trailing bytes")
	}
}

func TestStatsRejectInvalidPartitionCounts(t *testing.T) {
	for _, parts := range [][]PartitionStats{
		{{ID: 0, Rows: 1}},
		{{ID: 1, Rows: 1}, {ID: 1, Rows: 2}},
	} {
		if _, err := EncodeStats(&TableStats{Table: "t", TableID: 1, Partitions: parts}); err == nil {
			t.Fatalf("accepted invalid partition stats: %+v", parts)
		}
	}
}

func TestPartitionStatsEncodeRoundTrip(t *testing.T) {
	part := PartitionStats{
		ID:   7,
		Rows: 12,
		Columns: []ColumnStats{{
			Ord: 1, Nulls: 2, NDV: 4, HasMinMax: true,
			Min: types.StringValue("a"), Max: types.StringValue("z"), Correlation: 0.25,
		}},
		Indexes: []IndexStats{{Name: "ix_name", Selectivity: 0.25, NDV: 4}},
		Vectors: []VectorStats{{Ord: 2, Count: 10, Dim: 4}},
	}
	snapshot := sha256.Sum256([]byte("global NSST"))
	raw, err := EncodePartitionStats(3, snapshot, part)
	if err != nil {
		t.Fatal(err)
	}
	tableID, gotSnapshot, got, err := DecodePartitionStats(raw)
	if err != nil {
		t.Fatal(err)
	}
	if tableID != 3 || gotSnapshot != snapshot || got.ID != 7 || got.Rows != 12 || len(got.Columns) != 1 || got.Columns[0].Min.Str != "a" ||
		len(got.Indexes) != 1 || got.Indexes[0].Name != "ix_name" || len(got.Vectors) != 1 || got.Vectors[0].Count != 10 {
		t.Fatalf("table=%d partition=%+v", tableID, got)
	}
	if _, _, _, err := DecodePartitionStats(append(append([]byte(nil), raw...), 0)); err == nil {
		t.Fatal("partition stats decoder accepted trailing bytes")
	}
	if _, err := EncodePartitionStats(3, snapshot, PartitionStats{ID: 7, Columns: []ColumnStats{{Ord: 1, Histogram: []HistBucket{{}}}}}); err == nil {
		t.Fatal("partition stats encoder accepted a non-compact column sketch")
	}
	start, end := PartitionStatsRange(3)
	key := PartitionStatsKey(3, 7)
	if string(key[:5]) != string(start) || string(key) >= string(end) {
		t.Fatalf("key %x outside range [%x,%x)", key, start, end)
	}
	wide := PartitionStats{ID: 7}
	for ord := 0; ord < MaxPartitionSketchColumns; ord++ {
		wide.Columns = append(wide.Columns, ColumnStats{
			Ord: ord, HasMinMax: true,
			Min: types.StringValue(strings.Repeat("a", 256)),
			Max: types.StringValue(strings.Repeat("z", 256)),
		})
	}
	if _, err := EncodePartitionStats(3, snapshot, wide); err == nil {
		t.Fatal("partition stats encoder accepted an oversized record")
	}
	if _, _, _, err := DecodePartitionStats(make([]byte, MaxPartitionStatsBytes+1)); err == nil {
		t.Fatal("partition stats decoder accepted an oversized record")
	}
}

func FuzzDecodePartitionStats(f *testing.F) {
	snapshot := sha256.Sum256([]byte("fuzz snapshot"))
	seed, err := EncodePartitionStats(2, snapshot, PartitionStats{
		ID:      9,
		Rows:    1,
		Columns: []ColumnStats{{Ord: 0, NDV: 1, HasMinMax: true, Min: types.StringValue("x"), Max: types.StringValue("x")}},
	})
	if err != nil {
		f.Fatal(err)
	}
	f.Add(seed)
	f.Add([]byte("NSPS"))
	f.Fuzz(func(t *testing.T, raw []byte) {
		tableID, snapshot, part, err := DecodePartitionStats(raw)
		if err != nil {
			return
		}
		roundTrip, err := EncodePartitionStats(tableID, snapshot, part)
		if err != nil {
			t.Fatalf("decoded partition stats did not re-encode: %v", err)
		}
		if _, _, _, err := DecodePartitionStats(roundTrip); err != nil {
			t.Fatalf("re-encoded partition stats did not decode: %v", err)
		}
	})
}

func FuzzDecodeStats(f *testing.F) {
	seed, err := EncodeStats(&TableStats{
		Table:      "events",
		TableID:    7,
		Rows:       3,
		Partitions: []PartitionStats{{ID: 1, Rows: 1}, {ID: 4, Rows: 2}},
	})
	if err != nil {
		f.Fatal(err)
	}
	f.Add(seed)
	f.Add([]byte("NSST"))
	f.Fuzz(func(t *testing.T, raw []byte) {
		st, err := DecodeStats(raw)
		if err != nil {
			return
		}
		roundTrip, err := EncodeStats(st)
		if err != nil {
			t.Fatalf("decoded stats did not re-encode: %v", err)
		}
		if _, err := DecodeStats(roundTrip); err != nil {
			t.Fatalf("re-encoded stats did not decode: %v", err)
		}
	})
}

func TestVectorStatsEncodeRoundTrip(t *testing.T) {
	st := &TableStats{
		Table:   "docs",
		TableID: 4,
		Rows:    50,
		Vectors: []VectorStats{{
			Ord: 2, Count: 48, Dim: 1536, IndexName: "ix_emb", M: 16, EfConstruct: 64,
		}},
	}
	raw, err := EncodeStats(st)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeStats(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Vectors) != 1 || got.Vectors[0].Dim != 1536 || got.Vectors[0].IndexName != "ix_emb" || got.Vectors[0].M != 16 {
		t.Fatalf("%+v", got.Vectors)
	}
	var v1 []byte
	v1 = append(v1, []byte(statsMagic)...)
	v1 = appendU16(v1, 1)
	v1 = appendString(v1, "t")
	v1 = appendU32(v1, 1)
	v1 = appendU64(v1, 0)
	v1 = appendU16(v1, 0)
	v1 = appendU16(v1, 0)
	v1 = appendU16(v1, 0)
	old, err := DecodeStats(v1)
	if err != nil {
		t.Fatal(err)
	}
	if len(old.Vectors) != 0 {
		t.Fatalf("v1 should have no vectors: %+v", old.Vectors)
	}
	v2 := append([]byte(nil), v1...)
	copy(v2[4:6], appendU16(nil, 2))
	v2 = appendU16(v2, 0)
	old, err = DecodeStats(v2)
	if err != nil {
		t.Fatal(err)
	}
	if len(old.Partitions) != 0 {
		t.Fatalf("v2 should have no partition stats: %+v", old.Partitions)
	}
}

func mustParseDec(t *testing.T, s string) types.Decimal {
	t.Helper()
	d, err := types.ParseDecimal(s)
	if err != nil {
		t.Fatal(err)
	}
	return d
}
