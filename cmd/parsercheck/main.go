package main
import (
 "fmt"
 "github.com/bzync/nextsql/internal/sql/parser"
)
func main(){
 for _, sql := range []string{
  "SELECT * FROM system.storage",
  "SELECT * FROM system.raft",
  "SELECT * FROM system.replication",
  "SELECT * FROM system.capabilities",
  "SELECT name FROM system.capabilities WHERE status='supported' LIMIT 2",
 }{
  stmt, err := parser.Parse(sql)
  fmt.Printf("%q -> %T err %v\n", sql, stmt, err)
 }
}
