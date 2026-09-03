package executor

import (
	"github.com/bzync/nextsql/internal/catalog"
	"github.com/bzync/nextsql/internal/clientenc"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/sql/types"
)

// validateClientEncryptedRow is the last leader-side gate before persistence.
// It validates only public envelope structure and logical type; nextsqld never
// has a field key and therefore cannot authenticate or decrypt the payload.
func validateClientEncryptedRow(tab *catalog.Table, row []types.Value) error {
	if tab == nil || len(row) != len(tab.Columns) {
		return nerr.New(nerr.InvalidArgument, "executor.clientenc", "row shape does not match table")
	}
	for i, col := range tab.Columns {
		if !col.ClientEncrypted() || row[i].Null {
			continue
		}
		if row[i].Typ.Kind != types.KindString && row[i].Typ.Kind != types.KindText {
			return nerr.New(nerr.InvalidArgument, "executor.clientenc", "ENCRYPTED CLIENT value must be an opaque string")
		}
		if err := clientenc.ValidateForColumn(row[i].Str, col.ClientType); err != nil {
			return nerr.Wrap(nerr.InvalidArgument, "executor.clientenc", "invalid ENCRYPTED CLIENT value", err)
		}
	}
	return nil
}
