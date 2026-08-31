//go:build ignore

package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/bzync/nextsql/internal/catalog"
	"github.com/bzync/nextsql/internal/crypto"
	"github.com/bzync/nextsql/internal/executor"
	"github.com/bzync/nextsql/internal/sql/types"
)

func main() {
	tab := &catalog.Table{
		ID: 1, Name: "t",
		Columns: []catalog.Column{{Name: "id", Type: types.String()}, {Name: "account_id", Type: types.String()}, {Name: "n", Type: types.String()}},
		PK:      []int{0},
	}
	part := catalog.Partitioning{
		Kind:    catalog.PartitionList,
		Columns: []int{1},
		Partitions: []catalog.Partition{
			{ID: 1, Name: "p_a", HeapMeta: 1, Values: [][]types.Value{{types.StringValue("a")}}},
			{ID: 2, Name: "p_b", HeapMeta: 2, Values: [][]types.Value{{types.StringValue("b")}}},
		},
	}
	tab.Partitioning = &part
	rowA := []types.Value{types.StringValue("1"), types.StringValue("a"), types.StringValue("hello")}
	rowC := []types.Value{types.StringValue("3"), types.StringValue("c"), types.StringValue("bad")}
	for _, row := range [][]types.Value{rowA, rowC} {
		p, err := tab.PartitionForRow(row)
		if err != nil {
			fmt.Printf("row %v partition err: %v\n", row[1].Str, err)
		} else {
			fmt.Printf("row %v -> partition %s ID %d\n", row[1].Str, p.Name, p.ID)
		}
	}
	dir, _ := os.MkdirTemp("", "parttest")
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "nextsql.db")
	dek, _ := crypto.GenerateDEK(1)
	keys, _ := crypto.NewMemoryKeyProvider(dek)
	db, _ := executor.Create(path, keys, 32)
	defer db.Close()
	s2 := db.Session()
	_, err := s2.Exec(`CREATE TABLE t_list (id STRING PRIMARY KEY, account_id STRING NOT NULL, n STRING NOT NULL) PARTITION BY LIST (account_id) (PARTITION p_a VALUES IN ('a'), PARTITION p_b VALUES IN ('b'))`)
	if err != nil {
		fmt.Println("create err", err)
		os.Exit(1)
	}
	tbl, ok := db.Cat.Get("t_list")
	if !ok {
		fmt.Println("not found cat")
		os.Exit(1)
	}
	fmt.Printf("cat partitioning: kind %v cols %v\n", tbl.Partitioning.Kind, tbl.Partitioning.Columns)
	for _, p := range tbl.Partitioning.Partitions {
		fmt.Printf(" part %s ID %d HeapMeta %d Values %v\n", p.Name, p.ID, p.HeapMeta, p.Values)
	}
	_, err = s2.Exec(`INSERT INTO t_list (id, account_id, n) VALUES ('1', 'a', 'hello')`)
	if err != nil {
		fmt.Println("insert a err", err)
		os.Exit(1)
	}
	res, err := s2.Exec(`SELECT * FROM t_list`)
	if err != nil {
		fmt.Println("select err", err)
		os.Exit(1)
	}
	fmt.Printf("select all rows %d\n", len(res.Rows))
	for _, r := range res.Rows {
		fmt.Printf(" row %v\n", r)
	}
	res, err = s2.Exec(`SELECT * FROM t_list WHERE account_id = 'a'`)
	if err != nil {
		fmt.Println("select a err", err)
		os.Exit(1)
	}
	fmt.Printf("select a rows %d\n", len(res.Rows))
	res, _ = s2.Exec(`EXPLAIN SELECT * FROM t_list WHERE account_id = 'a'`)
	for _, r := range res.Rows {
		fmt.Printf("explain %v\n", r)
	}
}
