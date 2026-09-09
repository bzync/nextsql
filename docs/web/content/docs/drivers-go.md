# Go driver

```bash
go get github.com/bzync/nextsql/drivers/go
```

Import path `github.com/bzync/nextsql/drivers/go` (package `nextsql`). Source: [`drivers/go`](https://github.com/bzync/nextsql/tree/master/drivers/go). Versioned independently of the engine.

```go
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	nextsql "github.com/bzync/nextsql/drivers/go"
	"github.com/bzync/nextsql/internal/sql/types"
)

func main() {
	conn, err := nextsql.Open(nextsql.Config{
		Address:       "127.0.0.1:7210",
		Database:      "default",
		User:          "app",
		Password:      os.Getenv("NEXTSQL_DATABASE_PASS"),
		InsecureNoTLS: true, // loopback only
	})
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()

	dec, err := types.ParseDecimal("50.00")
	if err != nil {
		log.Fatal(err)
	}
	res, err := conn.Exec(context.Background(),
		`SELECT name FROM items WHERE price < $1`,
		types.DecimalValue(dec, types.Type{Kind: types.KindDecimal, Precision: 12, Scale: 2}),
	)
	if err != nil {
		log.Fatal(err)
	}
	for _, row := range res.Rows {
		fmt.Println(row[0].String())
	}

	// Safe to retry after a timeout: the mutation and replay result commit
	// atomically under this database-user-scoped key.
	_, err = conn.ExecIdempotent(context.Background(), "order-20260826-42",
		`INSERT INTO orders (id, status) VALUES ($1, $2)`,
		types.StringValue("42"), types.StringValue("created"),
	)
	if err != nil {
		log.Fatal(err)
	}

	stmt, err := conn.Prepare(context.Background(),
		`SELECT sku FROM items WHERE sku = $1`)
	if err != nil {
		log.Fatal(err)
	}
	defer stmt.Close()
	_, err = stmt.Exec(context.Background(), types.StringValue("A-1"))
	if err != nil {
		log.Fatal(err)
	}
}
```

`Query` returns `*Rows` (`Next`, `Values`, `Columns`, `Close`). Canceling the `Query` context opens a side connection and cancels the in-flight statement.

Continuous change streams use the same API and cancellation path, including
prepared statements:

```go
ctx, cancel := context.WithCancel(context.Background())
rows, err := conn.Query(ctx, `SUBSCRIBE TO orders AFTER 1842`)
if err != nil { log.Fatal(err) }
defer func() { cancel(); _ = rows.Close() }()
for rows.Next() {
    event := rows.Values()
    fmt.Println(event[0].String(), event[11].String()) // operation, resume_token
}
```

Persist `resume_token` only after processing every row in its transaction. See
[Change streams](/docs/cdc).

For `--require-client-key`, set `Config.KeyProvider` (never a URL). Remote connections need a `tls.Config` with TLS 1.3 and a CA.

To authenticate with a signed short-lived credential, put the `NSSC1.` value in
`Config.Password` — no other change. See [security](/docs/security).

`ENCRYPTED CLIENT` columns use the separate `Config.FieldKeys`
provider. Call `EncryptField(ctx, table, column, logicalValue)` before binding
and `DecryptField(ctx, table, column, logicalType, resultValue)` after a bare
projection. `MemoryFieldKeyring` is bounded convenience storage for keys already
loaded from a secret manager; `FileFieldKeyring` is the durable rotation/
revocation path. The field key is never sent to `nextsqld`. Equivalent helpers
ship in Node.js/TypeScript, Bun, and PHP (not Python or Ruby). PITR and
HA/failover are tested. For an explicit `ENCRYPTED CLIENT DETERMINISTIC`
column, use `EncryptFieldDeterministic` / `DecryptFieldDeterministic`; only bind
the resulting `NSCE2` value to equality/inequality predicates. This mode leaks
equality and frequency. General searchable encryption is not supported.

Follower-read routing uses `nextsql.OpenCluster` (`STRONG` / `BOUNDED` /
`STALE`). See [High availability](/docs/ha).

Multi-statement transactions are a session of `BEGIN` / statements / `COMMIT` on the same connection:

```go
conn.Exec(ctx, `BEGIN SNAPSHOT`)
conn.Exec(ctx, `UPDATE products SET price = price * 1.1 WHERE name = $1`, types.StringValue("Aero 2"))
conn.Exec(ctx, `COMMIT`)
```
