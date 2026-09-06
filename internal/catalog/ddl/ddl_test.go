package ddl

import (
	"strings"
	"testing"

	"github.com/bzync/nextsql/internal/catalog"
	"github.com/bzync/nextsql/internal/sql/types"
)

func TestQuoteIdentAndSQLType(t *testing.T) {
	if got := QuoteIdent(`a"b`); got != `"a""b"` {
		t.Fatalf("QuoteIdent = %q", got)
	}
	if got := SQLType(types.Type{Kind: types.KindDecimal, Precision: 10, Scale: 2}); got != "DECIMAL(10,2)" {
		t.Fatalf("SQLType(DECIMAL) = %q", got)
	}
}

func TestCreateTableSQLDefaultsAndPK(t *testing.T) {
	tab := &catalog.Table{
		Name: "widgets",
		Columns: []catalog.Column{
			{Name: "id", Type: types.String(), NotNull: true, Primary: true},
			{Name: "created", Type: types.TimestampTZ(), Default: catalog.Default{Kind: catalog.DefNow}},
			{Name: "label", Type: types.String(), Default: catalog.Default{Kind: catalog.DefLiteral, Literal: types.StringValue("n/a")}},
		},
		PK: []int{0},
	}
	got, err := CreateTableSQL(tab)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`CREATE TABLE "widgets" (`,
		`"id" STRING PRIMARY KEY`,
		`"created" TIMESTAMPTZ DEFAULT NOW()`,
		`"label" STRING DEFAULT 'n/a'`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("CreateTableSQL missing %q in:\n%s", want, got)
		}
	}
}

func TestCreateTableSQLForeignKeyMissingParentFailsClosed(t *testing.T) {
	child := &catalog.Table{
		Name:    "orders",
		Columns: []catalog.Column{{Name: "id", Type: types.String(), NotNull: true, Primary: true}, {Name: "cust", Type: types.String(), NotNull: true}},
		PK:      []int{0},
		ForeignKeys: []catalog.ForeignKey{{
			Name: "fk", Columns: []int{1}, RefTable: "customers", RefColumns: []int{0},
			OnDelete: catalog.FKCascade, OnUpdate: catalog.FKRestrict,
		}},
	}
	if _, err := CreateTableSQL(child); err == nil {
		t.Fatal("want error when the referenced table is not supplied")
	}
}

func TestCreateIndexSQLShapes(t *testing.T) {
	tab := &catalog.Table{
		Name:    "docs",
		Columns: []catalog.Column{{Name: "id", Type: types.String(), NotNull: true, Primary: true}, {Name: "title", Type: types.String()}, {Name: "body", Type: types.String()}},
		PK:      []int{0},
	}
	plain, err := CreateIndexSQL(tab, catalog.Index{Name: "docs_title", Columns: []int{1}})
	if err != nil {
		t.Fatal(err)
	}
	if plain != `CREATE INDEX "docs_title" ON "docs" ("title")` {
		t.Fatalf("plain index = %q", plain)
	}
	uniq, err := CreateIndexSQL(tab, catalog.Index{Name: "docs_u", Columns: []int{1}, Unique: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(uniq, `CREATE UNIQUE INDEX `) {
		t.Fatalf("unique index = %q", uniq)
	}
	cover, err := CreateIndexSQL(tab, catalog.Index{Name: "docs_c", Columns: []int{1}, Include: []int{2}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cover, ` INCLUDE ("body")`) {
		t.Fatalf("covering index = %q", cover)
	}
}

func TestFKActionSQL(t *testing.T) {
	for a, want := range map[catalog.FKAction]string{
		catalog.FKRestrict: "RESTRICT", catalog.FKCascade: "CASCADE",
		catalog.FKSetNull: "SET NULL", catalog.FKSetDefault: "SET DEFAULT",
	} {
		if got, err := FKActionSQL(a); err != nil || got != want {
			t.Fatalf("FKActionSQL(%v) = %q, %v", a, got, err)
		}
	}
}
