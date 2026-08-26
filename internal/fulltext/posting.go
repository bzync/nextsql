package fulltext

import (
	"bytes"

	"github.com/bzync/nextsql/internal/encoding"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/sql/types"
)

const (
	kindStats  byte = 0x00
	kindPost   byte = 0x01
	kindDocLen byte = 0x02
	valVersion byte = 1
)

// StatsKey is the single corpus-stats record in a full-text tree.
func StatsKey() []byte { return []byte{kindStats} }

// PostingKey is term + primary key. Term must not contain NUL.
func PostingKey(term string, pk []byte) []byte {
	out := make([]byte, 0, 2+len(term)+len(pk))
	out = append(out, kindPost)
	out = append(out, term...)
	out = append(out, 0x00)
	out = append(out, pk...)
	return out
}

// PostingBounds is the exclusive range covering every posting for term.
func PostingBounds(term string) (start, end []byte) {
	start = PostingKey(term, nil)
	end = types.PrefixEnd(start)
	return start, end
}

// SplitPostingKey extracts term and primary-key bytes.
func SplitPostingKey(k []byte) (term string, pk []byte, err error) {
	if len(k) < 2 || k[0] != kindPost {
		return "", nil, nerr.New(nerr.InvalidFormat, "fulltext.SplitPostingKey", "not a posting key")
	}
	nul := bytes.IndexByte(k[1:], 0)
	if nul < 0 {
		return "", nil, nerr.New(nerr.InvalidFormat, "fulltext.SplitPostingKey", "truncated term")
	}
	term = string(k[1 : 1+nul])
	pk = append([]byte(nil), k[2+nul:]...)
	return term, pk, nil
}

// IsPostingKey reports a posting record.
func IsPostingKey(k []byte) bool { return len(k) > 0 && k[0] == kindPost }

// DocLenKey stores the token count for one document.
func DocLenKey(pk []byte) []byte {
	out := make([]byte, 1+len(pk))
	out[0] = kindDocLen
	copy(out[1:], pk)
	return out
}

// IsDocLenKey reports a document-length record.
func IsDocLenKey(k []byte) bool { return len(k) > 0 && k[0] == kindDocLen }

// SplitDocLenKey returns the primary-key suffix.
func SplitDocLenKey(k []byte) ([]byte, error) {
	if !IsDocLenKey(k) {
		return nil, nerr.New(nerr.InvalidFormat, "fulltext.SplitDocLenKey", "not a doclen key")
	}
	return append([]byte(nil), k[1:]...), nil
}

// EncodePosting writes tf and positions.
func EncodePosting(tf uint32, pos []uint32) []byte {
	buf := make([]byte, 1+8+4*len(pos))
	buf[0] = valVersion
	encoding.PutU32(buf, 1, tf)
	encoding.PutU32(buf, 5, uint32(len(pos)))
	for i, p := range pos {
		encoding.PutU32(buf, 9+4*i, p)
	}
	return buf
}

// DecodePosting reads a posting value. Malformed input fails closed.
func DecodePosting(raw []byte) (tf uint32, pos []uint32, err error) {
	if len(raw) < 9 || raw[0] != valVersion {
		return 0, nil, nerr.New(nerr.InvalidFormat, "fulltext.DecodePosting", "bad posting")
	}
	tf = encoding.U32(raw, 1)
	n := encoding.U32(raw, 5)
	need := 9 + 4*int(n)
	if n > MaxDocTokens || need != len(raw) {
		return 0, nil, nerr.New(nerr.InvalidFormat, "fulltext.DecodePosting", "bad posting length")
	}
	if n == 0 {
		return tf, nil, nil
	}
	pos = make([]uint32, n)
	for i := uint32(0); i < n; i++ {
		pos[i] = encoding.U32(raw, 9+4*int(i))
	}
	return tf, pos, nil
}

// EncodeDocLen writes a document token count.
func EncodeDocLen(n uint32) []byte {
	buf := make([]byte, 5)
	buf[0] = valVersion
	encoding.PutU32(buf, 1, n)
	return buf
}

// DecodeDocLen reads a document token count.
func DecodeDocLen(raw []byte) (uint32, error) {
	if len(raw) != 5 || raw[0] != valVersion {
		return 0, nerr.New(nerr.InvalidFormat, "fulltext.DecodeDocLen", "bad doclen")
	}
	return encoding.U32(raw, 1), nil
}

// Stats is corpus-wide BM25 parameters stored in the index tree.
type Stats struct {
	Docs   uint64
	Tokens uint64
}

// EncodeStats writes corpus stats.
func EncodeStats(st Stats) []byte {
	buf := make([]byte, 17)
	buf[0] = valVersion
	encoding.PutU64(buf, 1, st.Docs)
	encoding.PutU64(buf, 9, st.Tokens)
	return buf
}

// DecodeStats reads corpus stats.
func DecodeStats(raw []byte) (Stats, error) {
	if len(raw) != 17 || raw[0] != valVersion {
		return Stats{}, nerr.New(nerr.InvalidFormat, "fulltext.DecodeStats", "bad stats")
	}
	return Stats{Docs: encoding.U64(raw, 1), Tokens: encoding.U64(raw, 9)}, nil
}

// Pair is one inverted-index key/value for a document (postings + doclen).
type Pair struct {
	K, V []byte
}

// EncodeDocPairs writes postings and the document-length record. Not stats.
func EncodeDocPairs(pk []byte, doc Doc) []Pair {
	if doc.Len == 0 || len(pk) == 0 {
		return nil
	}
	out := make([]Pair, 0, len(doc.Terms)+1)
	for _, t := range doc.Terms {
		out = append(out, Pair{
			K: PostingKey(t.Term, pk),
			V: EncodePosting(t.TF, t.Pos),
		})
	}
	out = append(out, Pair{K: DocLenKey(pk), V: EncodeDocLen(doc.Len)})
	return out
}
