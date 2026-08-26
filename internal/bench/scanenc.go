package bench

import (
	"fmt"

	"github.com/bzync/nextsql/internal/encoding"
	"github.com/bzync/nextsql/internal/sql/types"
)

// encodeBenchScan writes the official SLO scan row (STRING PK, STRING group,
// DECIMAL n) into reusable buffers. The encoding matches EncodeKey/EncodeRow.
func encodeBenchScan(abs int, keyBuf, valBuf []byte) (key, val []byte) {
	var id [20]byte
	id[0] = 's'
	const digits = 12
	for i := 1; i <= digits; i++ {
		id[i] = '0'
	}
	var dec [20]byte
	n := itoa10(dec[:], abs)
	copy(id[1+digits-n:1+digits], dec[:n])
	idLen := 1 + digits
	grp := byte('a' + abs%10)

	needK := 1 + idLen + 2
	if cap(keyBuf) < needK {
		keyBuf = make([]byte, needK)
	} else {
		keyBuf = keyBuf[:needK]
	}
	keyBuf[0] = 1
	copy(keyBuf[1:], id[:idLen])
	keyBuf[1+idLen] = 0
	keyBuf[2+idLen] = 0

	var coef [8]byte
	clen := putU64BE(coef[:], uint64(abs))
	decInner := 4 + clen
	decOuter := 4 + decInner
	needV := 8 + 1 + 4 + idLen + 5 + decOuter
	if cap(valBuf) < needV {
		valBuf = make([]byte, needV)
	} else {
		valBuf = valBuf[:needV]
	}
	copy(valBuf[0:4], "NSRW")
	valBuf[4] = 1
	valBuf[5] = 0
	encoding.PutU16(valBuf, 6, 3)
	valBuf[8] = 0
	off := 9
	encoding.PutU32(valBuf, off, uint32(idLen))
	off += 4
	copy(valBuf[off:], id[:idLen])
	off += idLen
	encoding.PutU32(valBuf, off, 1)
	off += 4
	valBuf[off] = grp
	off++
	encoding.PutU32(valBuf, off, uint32(decInner))
	off += 4
	valBuf[off] = 0
	valBuf[off+1] = 0
	encoding.PutU16(valBuf, off+2, 0)
	copy(valBuf[off+4:], coef[:clen])
	return keyBuf, valBuf
}

func itoa10(dst []byte, n int) int {
	var tmp [20]byte
	i := len(tmp)
	if n == 0 {
		i--
		tmp[i] = '0'
	} else {
		for n > 0 {
			i--
			tmp[i] = byte('0' + n%10)
			n /= 10
		}
	}
	return copy(dst, tmp[i:])
}

// putU64BE writes n in big-endian without leading zeros, matching big.Int.Bytes.
// n == 0 writes nothing and returns 0.
func putU64BE(dst []byte, n uint64) int {
	if n == 0 {
		return 0
	}
	var tmp [8]byte
	for i := 7; i >= 0; i-- {
		tmp[i] = byte(n)
		n >>= 8
	}
	i := 0
	for i < 8 && tmp[i] == 0 {
		i++
	}
	return copy(dst, tmp[i:])
}

func encodeBenchScanRef(abs int) (key, val []byte, err error) {
	decTy := types.Type{Kind: types.KindDecimal, Precision: 10, Scale: 0}
	d := types.DecimalFromInt64(int64(abs))
	row := []types.Value{
		types.StringValue(fmt.Sprintf("s%012d", abs)),
		types.StringValue(string(rune('a' + abs%10))),
		types.DecimalValue(d, decTy),
	}
	key, err = types.EncodeKey(row[:1])
	if err != nil {
		return nil, nil, err
	}
	val, err = types.EncodeRow(row)
	return key, val, err
}

func itoaAlloc(n int) string {
	if n == 0 {
		return "0"
	}
	var tmp [20]byte
	i := len(tmp)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		tmp[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		tmp[i] = '-'
	}
	return string(tmp[i:])
}
