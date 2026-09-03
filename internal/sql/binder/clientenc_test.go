package binder

import (
	"testing"

	"github.com/bzync/nextsql/internal/catalog"
	"github.com/bzync/nextsql/internal/sql/parser"
)

func TestBindClientEncryptedOpaqueOnly(t *testing.T) {
	create, err := parser.Parse(`CREATE TABLE accounts (id STRING PRIMARY KEY, secret STRING ENCRYPTED CLIENT, secret2 STRING ENCRYPTED CLIENT)`)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Bind(create, func(string) (*catalog.Table, bool) { return nil, false }, 1)
	if err != nil {
		t.Fatal(err)
	}
	tab := b.(CreateTable).Table
	lookup := func(name string) (*catalog.Table, bool) { return tab, name == "accounts" }
	for _, sql := range []string{
		`INSERT INTO accounts (id, secret) VALUES ('1', $1)`,
		`SELECT secret FROM accounts`,
		`SELECT * FROM accounts`,
		`UPDATE accounts SET secret = $1 WHERE id = '1'`,
		`UPDATE accounts SET secret = secret WHERE id = '1'`,
	} {
		stmt, err := parser.Parse(sql)
		if err != nil {
			t.Fatalf("parse %q: %v", sql, err)
		}
		if _, err := Bind(stmt, lookup, 2); err != nil {
			t.Fatalf("bind %q: %v", sql, err)
		}
	}
	for _, sql := range []string{
		`INSERT INTO accounts (id, secret) VALUES ('1', 'plaintext')`,
		`SELECT secret FROM accounts WHERE secret = $1`,
		`SELECT LENGTH(secret) FROM accounts`,
		`SELECT DISTINCT secret FROM accounts`,
		`SELECT secret FROM accounts ORDER BY secret`,
		`CREATE INDEX ix_secret ON accounts (secret)`,
		`WITH c AS (SELECT secret FROM accounts) SELECT LENGTH(secret) FROM c`,
		`SELECT id FROM accounts WHERE id IN (SELECT secret FROM accounts)`,
		`SELECT LENGTH((SELECT secret FROM accounts)) FROM accounts`,
		`UPDATE accounts SET id = (SELECT secret FROM accounts)`,
		`UPDATE accounts SET secret = secret2`,
		`SELECT secret FROM accounts UNION ALL SELECT secret FROM accounts`,
	} {
		stmt, err := parser.Parse(sql)
		if err != nil {
			t.Fatalf("parse %q: %v", sql, err)
		}
		if _, err := Bind(stmt, lookup, 2); err == nil {
			t.Fatalf("bound forbidden SQL %q", sql)
		}
	}
}
