package checksum

import (
	"hash/crc32"

	"github.com/bzync/nextsql/internal/encoding"
	"github.com/bzync/nextsql/internal/nerr"
)

var table = crc32.MakeTable(crc32.Castagnoli)

func CRC32C(data []byte) uint32 {
	return crc32.Checksum(data, table)
}

// Write stores CRC32C(data[:off] || data[off+4:]) at data[off:off+4].
func Write(data []byte, off int) {
	sum := crc32.Update(0, table, data[:off])
	sum = crc32.Update(sum, table, data[off+4:])
	encoding.PutU32(data, off, sum)
}

func Verify(data []byte, off int) error {
	if off < 0 || off+4 > len(data) {
		return nerr.New(nerr.InvalidFormat, "checksum.Verify", "checksum field out of range")
	}
	got := encoding.U32(data, off)
	sum := crc32.Update(0, table, data[:off])
	sum = crc32.Update(sum, table, data[off+4:])
	if got != sum {
		return nerr.New(nerr.Corruption, "checksum.Verify", "checksum mismatch")
	}
	return nil
}
