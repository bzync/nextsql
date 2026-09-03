package parser

import (
	"testing"

	"github.com/bzync/nextsql/internal/sql/ast"
	"github.com/bzync/nextsql/internal/sql/types"
)

func TestParseEncryptedClientColumn(t *testing.T) {
	stmt, err := Parse(`CREATE TABLE accounts (id UUID PRIMARY KEY, ssn STRING ENCRYPTED CLIENT NOT NULL)`)
	if err != nil {
		t.Fatal(err)
	}
	ct := stmt.(ast.CreateTable)
	if len(ct.Columns) != 2 || !ct.Columns[1].EncryptedClient || !ct.Columns[1].NotNull || ct.Columns[1].Type.Kind != types.KindString {
		t.Fatalf("column = %+v", ct.Columns[1])
	}
	if _, err := Parse(`CREATE TABLE bad (id UUID PRIMARY KEY, ssn STRING ENCRYPTED SERVER)`); err == nil {
		t.Fatal("accepted ENCRYPTED without CLIENT")
	}
	if _, err := Parse(`CREATE TABLE bad (id UUID PRIMARY KEY, ssn STRING ENCRYPTED CLIENT ENCRYPTED CLIENT)`); err == nil {
		t.Fatal("accepted duplicate ENCRYPTED CLIENT")
	}
}
