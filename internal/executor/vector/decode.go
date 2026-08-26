package vector

import (
	"github.com/bzync/nextsql/internal/scheduler"
	"github.com/bzync/nextsql/internal/sql/types"
)

// DecodeRows fills dst with decoded payloads. dst is Reset first.
// Returns the number of remaining payloads when dst fills.
func DecodeRows(dst *Batch, payloads [][]byte, cols []types.Type) (int, error) {
	if dst == nil {
		dst = New(cols, scheduler.DefaultBatch)
	}
	dst.Reset()
	for i, raw := range payloads {
		row, err := types.DecodeRow(raw, cols)
		if err != nil {
			return 0, err
		}
		if !dst.AppendRow(row) {
			return len(payloads) - i, nil
		}
	}
	return 0, nil
}

// AppendEncoded decodes one row payload into dst. False if dst is full.
func AppendEncoded(dst *Batch, raw []byte, cols []types.Type) (bool, error) {
	if dst.Full() {
		return false, nil
	}
	row, err := types.DecodeRow(raw, cols)
	if err != nil {
		return false, err
	}
	return dst.AppendRow(row), nil
}
