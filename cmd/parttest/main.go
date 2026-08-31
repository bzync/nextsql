package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/bzync/nextsql/internal/crypto"
	"github.com/bzync/nextsql/internal/executor"
)

func main() {
	dir, _ := os.MkdirTemp("", "parttest")
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "nextsql.db")
	dek, _ := crypto.GenerateDEK(1)
	keys, _ := crypto.NewMemoryKeyProvider(dek)
	db, err := executor.Create(path, keys, 32)
	if err != nil {
		panic(err)
	}
	defer db.Close()
	s := db.Session()
	// Test LIST partitioning
	fmt.Println("=== LIST partition DDL ===")
	_, err = s.Exec(`CREATE TABLE t_list (id STRING PRIMARY KEY, account_id STRING NOT NULL, n STRING NOT NULL) PARTITION BY LIST (account_id) (PARTITION p_a VALUES IN ('a'), PARTITION p_b VALUES IN ('b'))`)
	if err != nil {
		fmt.Printf("create list err: %v\n", err)
		os.Exit(1)
	} else {
		fmt.Println("create list ok")
	}
	_, err = s.Exec(`INSERT INTO t_list (id, account_id, n) VALUES ('1', 'a', 'hello')`)
	if err != nil {
		fmt.Printf("insert a err: %v\n", err)
		os.Exit(1)
	} else {
		fmt.Println("insert a ok")
	}
	_, err = s.Exec(`INSERT INTO t_list (id, account_id, n) VALUES ('2', 'b', 'world')`)
	if err != nil {
		fmt.Printf("insert b err: %v\n", err)
		os.Exit(1)
	} else {
		fmt.Println("insert b ok")
	}
	// Insert outside partition should fail
	_, err = s.Exec(`INSERT INTO t_list (id, account_id, n) VALUES ('3', 'c', 'bad')`)
	if err == nil {
		fmt.Println("expected error for tenant c but got none")
		os.Exit(1)
	} else {
		fmt.Printf("insert c correctly failed: %v\n", err)
	}
	res, err := s.Exec(`SELECT * FROM t_list WHERE account_id = 'a'`)
	if err != nil {
		fmt.Printf("select a err: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("select a rows: %d\n", len(res.Rows))
	for _, r := range res.Rows {
		fmt.Printf(" row: %v\n", r)
	}
	// EXPLAIN
	res, err = s.Exec(`EXPLAIN SELECT * FROM t_list WHERE account_id = 'a'`)
	if err != nil {
		fmt.Printf("explain err: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("EXPLAIN columns: %v\n", res.Columns)
	for _, r := range res.Rows {
		fmt.Printf(" explain row: %v\n", r)
	}

	// Test RANGE partitioning
	fmt.Println("\n=== RANGE partition DDL ===")
	s2 := db.Session()
	_, err = s2.Exec(`CREATE TABLE t_range (id STRING PRIMARY KEY, k STRING, v STRING) PARTITION BY RANGE (k) (PARTITION p0 VALUES LESS THAN ('m'), PARTITION p1 VALUES LESS THAN MAXVALUE)`)
	if err != nil {
		fmt.Printf("create range err: %v\n", err)
		os.Exit(1)
	} else {
		fmt.Println("create range ok")
	}
	_, err = s2.Exec(`INSERT INTO t_range (id, k, v) VALUES ('1', 'a', 'early'), ('2', 'z', 'late')`)
	if err != nil {
		fmt.Printf("insert range err: %v\n", err)
		os.Exit(1)
	} else {
		fmt.Println("insert range ok")
	}
	res, err = s2.Exec(`SELECT * FROM t_range WHERE k = 'a'`)
	if err != nil {
		fmt.Printf("select range a err: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("select range a rows: %d\n", len(res.Rows))
	res, err = s2.Exec(`SELECT * FROM t_range WHERE k = 'z'`)
	if err != nil {
		fmt.Printf("select range z err: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("select range z rows: %d\n", len(res.Rows))
	res, err = s2.Exec(`EXPLAIN SELECT * FROM t_range WHERE k = 'a'`)
	if err != nil {
		fmt.Printf("explain range err: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("EXPLAIN range columns: %v\n", res.Columns)
	for _, r := range res.Rows {
		fmt.Printf(" explain row: %v\n", r)
	}

	// Test SELECT all
	res, err = s2.Exec(`SELECT * FROM t_range`)
	if err != nil {
		fmt.Printf("select all range err: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("select all range rows: %d\n", len(res.Rows))

	// WAL/recovery: close and reopen
	fmt.Println("\n=== WAL/recovery ===")
	db.Close()
	db2, err := executor.Open(path, keys, 32)
	if err != nil {
		fmt.Printf("reopen err: %v\n", err)
		os.Exit(1)
	}
	s3 := db2.Session()
	res, err = s3.Exec(`SELECT * FROM t_list`)
	if err != nil {
		fmt.Printf("select after reopen err: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("after reopen tenant rows: %d\n", len(res.Rows))
	res, err = s3.Exec(`SELECT * FROM t_range`)
	if err != nil {
		fmt.Printf("select range after reopen err: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("after reopen range rows: %d\n", len(res.Rows))
	db2.Close()
	fmt.Println("\nAll partition tests passed")
}
