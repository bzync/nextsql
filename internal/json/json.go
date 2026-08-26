// Package json is NextSQL compact binary JSON (NSJB).
// The stored form is never UTF-8 JSON text.
package json

import (
	"bytes"
	"strconv"
	"unicode/utf8"

	"github.com/bzync/nextsql/internal/encoding"
	"github.com/bzync/nextsql/internal/nerr"
)

const (
	Magic   = "NSJB"
	Version = 1

	MaxDepth  = 32
	MaxBytes  = 1 << 20
	MaxString = 1 << 20
	MaxElems  = 1 << 20
)

const (
	TagNull   byte = 0x00
	TagFalse  byte = 0x01
	TagTrue   byte = 0x02
	TagI64    byte = 0x03
	TagString byte = 0x04
	TagNumber byte = 0x05
	TagArray  byte = 0x06
	TagObject byte = 0x07
)

// Kind is the runtime type of an extracted node.
type Kind uint8

const (
	KindMissing Kind = iota
	KindNull
	KindBool
	KindInt
	KindNumber
	KindString
	KindArray
	KindObject
)

// Result is one extracted JSON node. Containers keep a standalone NSJB document in Raw.
type Result struct {
	Kind Kind
	Bool bool
	Int  int64
	Str  string // string value or number token
	Raw  []byte
}

// ArrayLength returns the number of elements at path. Missing and JSON null
// report found=false; a present non-array is an invalid argument.
func ArrayLength(doc []byte, path []string) (n int, found bool, err error) {
	r, err := Extract(doc, path)
	if err != nil {
		return 0, false, err
	}
	if r.Kind == KindMissing || r.Kind == KindNull {
		return 0, false, nil
	}
	if r.Kind != KindArray {
		return 0, false, argErr("json.ArrayLength", "value is not an array")
	}
	off, err := rootOff(r.Raw)
	if err != nil {
		return 0, false, err
	}
	count, err := encoding.ReadU32(r.Raw, off+5)
	if err != nil {
		return 0, false, formatErr("json.ArrayLength", "truncated array count")
	}
	return int(count), true, nil
}

func formatErr(op, msg string) error {
	return nerr.New(nerr.InvalidFormat, op, msg)
}

func argErr(op, msg string) error {
	return nerr.New(nerr.InvalidArgument, op, msg)
}

// IsDoc reports whether b starts with a versioned NSJB header.
func IsDoc(b []byte) bool {
	return len(b) >= 5 && bytes.Equal(b[:4], []byte(Magic)) && b[4] == Version
}

func rootOff(doc []byte) (int, error) {
	if !IsDoc(doc) {
		if len(doc) >= 5 && bytes.Equal(doc[:4], []byte(Magic)) {
			return 0, formatErr("json", "unsupported JSON version")
		}
		return 0, formatErr("json", "not binary JSON")
	}
	return 5, nil
}

// Validate walks a document and rejects truncated, over-deep, or trailing input.
func Validate(doc []byte) error {
	if len(doc) > MaxBytes {
		return argErr("json.Validate", "JSON exceeds size limit")
	}
	off, err := rootOff(doc)
	if err != nil {
		return err
	}
	next, err := validateValue(doc, off, 0)
	if err != nil {
		return err
	}
	if next != len(doc) {
		return formatErr("json.Validate", "trailing JSON bytes")
	}
	return nil
}

func validateValue(b []byte, off, depth int) (int, error) {
	if depth > MaxDepth {
		return 0, argErr("json.Validate", "JSON exceeds depth limit")
	}
	if off >= len(b) {
		return 0, formatErr("json.Validate", "truncated JSON")
	}
	switch b[off] {
	case TagNull, TagFalse, TagTrue:
		return off + 1, nil
	case TagI64:
		if off+9 > len(b) {
			return 0, formatErr("json.Validate", "truncated i64")
		}
		return off + 9, nil
	case TagString, TagNumber:
		n, err := encoding.ReadU32(b, off+1)
		if err != nil {
			return 0, formatErr("json.Validate", "truncated length")
		}
		if int(n) > MaxString {
			return 0, argErr("json.Validate", "JSON string exceeds limit")
		}
		end := off + 5 + int(n)
		if end > len(b) {
			return 0, formatErr("json.Validate", "truncated payload")
		}
		if b[off] == TagString && !utf8.Valid(b[off+5:end]) {
			return 0, formatErr("json.Validate", "JSON string is not UTF-8")
		}
		return end, nil
	case TagArray:
		return validateArray(b, off, depth)
	case TagObject:
		return validateObject(b, off, depth)
	default:
		return 0, formatErr("json.Validate", "unknown JSON tag")
	}
}

func validateArray(b []byte, off, depth int) (int, error) {
	size, err := encoding.ReadU32(b, off+1)
	if err != nil {
		return 0, formatErr("json.Validate", "truncated array")
	}
	body := off + 5
	end := body + int(size)
	if end > len(b) || end < body {
		return 0, formatErr("json.Validate", "truncated array body")
	}
	n, err := encoding.ReadU32(b, body)
	if err != nil {
		return 0, formatErr("json.Validate", "truncated array count")
	}
	if int(n) > MaxElems {
		return 0, argErr("json.Validate", "JSON array exceeds limit")
	}
	cur := body + 4
	for i := uint32(0); i < n; i++ {
		cur, err = validateValue(b, cur, depth+1)
		if err != nil {
			return 0, err
		}
	}
	if cur != end {
		return 0, formatErr("json.Validate", "array size mismatch")
	}
	return end, nil
}

func validateObject(b []byte, off, depth int) (int, error) {
	size, err := encoding.ReadU32(b, off+1)
	if err != nil {
		return 0, formatErr("json.Validate", "truncated object")
	}
	body := off + 5
	end := body + int(size)
	if end > len(b) || end < body {
		return 0, formatErr("json.Validate", "truncated object body")
	}
	n, err := encoding.ReadU16(b, body)
	if err != nil {
		return 0, formatErr("json.Validate", "truncated object count")
	}
	if int(n) > MaxElems {
		return 0, argErr("json.Validate", "JSON object exceeds limit")
	}
	cur := body + 2
	for i := uint16(0); i < n; i++ {
		klen, err := encoding.ReadU16(b, cur)
		if err != nil {
			return 0, formatErr("json.Validate", "truncated object key")
		}
		cur += 2
		if cur+int(klen) > end {
			return 0, formatErr("json.Validate", "truncated object key")
		}
		if !utf8.Valid(b[cur : cur+int(klen)]) {
			return 0, formatErr("json.Validate", "JSON key is not UTF-8")
		}
		cur += int(klen)
		cur, err = validateValue(b, cur, depth+1)
		if err != nil {
			return 0, err
		}
	}
	if cur != end {
		return 0, formatErr("json.Validate", "object size mismatch")
	}
	return end, nil
}

// Skip returns the offset after the value at off.
func Skip(b []byte, off int) (int, error) {
	if off >= len(b) {
		return 0, formatErr("json.Skip", "truncated JSON")
	}
	switch b[off] {
	case TagNull, TagFalse, TagTrue:
		return off + 1, nil
	case TagI64:
		if off+9 > len(b) {
			return 0, formatErr("json.Skip", "truncated i64")
		}
		return off + 9, nil
	case TagString, TagNumber:
		n, err := encoding.ReadU32(b, off+1)
		if err != nil {
			return 0, formatErr("json.Skip", "truncated length")
		}
		end := off + 5 + int(n)
		if end > len(b) || end < off {
			return 0, formatErr("json.Skip", "truncated payload")
		}
		return end, nil
	case TagArray, TagObject:
		n, err := encoding.ReadU32(b, off+1)
		if err != nil {
			return 0, formatErr("json.Skip", "truncated container")
		}
		end := off + 5 + int(n)
		if end > len(b) || end < off {
			return 0, formatErr("json.Skip", "truncated container")
		}
		return end, nil
	default:
		return 0, formatErr("json.Skip", "unknown JSON tag")
	}
}

func wrap(value []byte) []byte {
	out := make([]byte, 5+len(value))
	copy(out, Magic)
	out[4] = Version
	copy(out[5:], value)
	return out
}

// Extract walks path without decoding unused siblings.
// A missing path returns KindMissing and a nil error.
func Extract(doc []byte, path []string) (Result, error) {
	if err := Validate(doc); err != nil {
		return Result{}, err
	}
	off, err := rootOff(doc)
	if err != nil {
		return Result{}, err
	}
	for _, part := range path {
		if off >= len(doc) {
			return Result{Kind: KindMissing}, nil
		}
		switch doc[off] {
		case TagObject:
			next, ok, err := findKey(doc, off, part)
			if err != nil {
				return Result{}, err
			}
			if !ok {
				return Result{Kind: KindMissing}, nil
			}
			off = next
		case TagArray:
			idx, ok := parseIndex(part)
			if !ok {
				return Result{Kind: KindMissing}, nil
			}
			next, found, err := findIndex(doc, off, idx)
			if err != nil {
				return Result{}, err
			}
			if !found {
				return Result{Kind: KindMissing}, nil
			}
			off = next
		default:
			return Result{Kind: KindMissing}, nil
		}
	}
	return decodeResult(doc, off)
}

func parseIndex(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	n := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int(c-'0')
		if n > MaxElems {
			return 0, false
		}
	}
	return n, true
}

func findKey(b []byte, off int, key string) (int, bool, error) {
	size, err := encoding.ReadU32(b, off+1)
	if err != nil {
		return 0, false, formatErr("json.Extract", "truncated object")
	}
	body := off + 5
	end := body + int(size)
	n, err := encoding.ReadU16(b, body)
	if err != nil {
		return 0, false, formatErr("json.Extract", "truncated object count")
	}
	cur := body + 2
	kb := []byte(key)
	for i := uint16(0); i < n; i++ {
		klen, err := encoding.ReadU16(b, cur)
		if err != nil {
			return 0, false, formatErr("json.Extract", "truncated object key")
		}
		cur += 2
		if cur+int(klen) > end {
			return 0, false, formatErr("json.Extract", "truncated object key")
		}
		match := int(klen) == len(kb) && bytes.Equal(b[cur:cur+int(klen)], kb)
		cur += int(klen)
		if match {
			return cur, true, nil
		}
		cur, err = Skip(b, cur)
		if err != nil {
			return 0, false, err
		}
	}
	return 0, false, nil
}

func findIndex(b []byte, off int, idx int) (int, bool, error) {
	size, err := encoding.ReadU32(b, off+1)
	if err != nil {
		return 0, false, formatErr("json.Extract", "truncated array")
	}
	body := off + 5
	end := body + int(size)
	_ = end
	n, err := encoding.ReadU32(b, body)
	if err != nil {
		return 0, false, formatErr("json.Extract", "truncated array count")
	}
	if idx < 0 || idx >= int(n) {
		return 0, false, nil
	}
	cur := body + 4
	for i := 0; i < idx; i++ {
		cur, err = Skip(b, cur)
		if err != nil {
			return 0, false, err
		}
	}
	return cur, true, nil
}

func decodeResult(b []byte, off int) (Result, error) {
	if off >= len(b) {
		return Result{}, formatErr("json.Extract", "truncated JSON")
	}
	switch b[off] {
	case TagNull:
		return Result{Kind: KindNull}, nil
	case TagFalse:
		return Result{Kind: KindBool, Bool: false}, nil
	case TagTrue:
		return Result{Kind: KindBool, Bool: true}, nil
	case TagI64:
		if off+9 > len(b) {
			return Result{}, formatErr("json.Extract", "truncated i64")
		}
		u, err := encoding.ReadU64(b, off+1)
		if err != nil {
			return Result{}, formatErr("json.Extract", "truncated i64")
		}
		return Result{Kind: KindInt, Int: int64(u)}, nil
	case TagString:
		n, err := encoding.ReadU32(b, off+1)
		if err != nil {
			return Result{}, formatErr("json.Extract", "truncated string")
		}
		end := off + 5 + int(n)
		if end > len(b) {
			return Result{}, formatErr("json.Extract", "truncated string")
		}
		return Result{Kind: KindString, Str: string(b[off+5 : end])}, nil
	case TagNumber:
		n, err := encoding.ReadU32(b, off+1)
		if err != nil {
			return Result{}, formatErr("json.Extract", "truncated number")
		}
		end := off + 5 + int(n)
		if end > len(b) {
			return Result{}, formatErr("json.Extract", "truncated number")
		}
		return Result{Kind: KindNumber, Str: string(b[off+5 : end])}, nil
	case TagArray:
		end, err := Skip(b, off)
		if err != nil {
			return Result{}, err
		}
		return Result{Kind: KindArray, Raw: wrap(b[off:end])}, nil
	case TagObject:
		end, err := Skip(b, off)
		if err != nil {
			return Result{}, err
		}
		return Result{Kind: KindObject, Raw: wrap(b[off:end])}, nil
	default:
		return Result{}, formatErr("json.Extract", "unknown JSON tag")
	}
}

// ToText writes compact UTF-8 JSON. Used for display, not storage.
func ToText(doc []byte) ([]byte, error) {
	off, err := rootOff(doc)
	if err != nil {
		return nil, err
	}
	out, next, err := writeText(nil, doc, off)
	if err != nil {
		return nil, err
	}
	if next != len(doc) {
		return nil, formatErr("json.ToText", "trailing JSON bytes")
	}
	return out, nil
}

func writeText(dst, b []byte, off int) ([]byte, int, error) {
	if off >= len(b) {
		return nil, 0, formatErr("json.ToText", "truncated JSON")
	}
	switch b[off] {
	case TagNull:
		return append(dst, "null"...), off + 1, nil
	case TagFalse:
		return append(dst, "false"...), off + 1, nil
	case TagTrue:
		return append(dst, "true"...), off + 1, nil
	case TagI64:
		if off+9 > len(b) {
			return nil, 0, formatErr("json.ToText", "truncated i64")
		}
		u := encoding.U64(b, off+1)
		return strconv.AppendInt(dst, int64(u), 10), off + 9, nil
	case TagString:
		n, err := encoding.ReadU32(b, off+1)
		if err != nil {
			return nil, 0, formatErr("json.ToText", "truncated string")
		}
		end := off + 5 + int(n)
		if end > len(b) {
			return nil, 0, formatErr("json.ToText", "truncated string")
		}
		dst = appendJSONString(dst, b[off+5:end])
		return dst, end, nil
	case TagNumber:
		n, err := encoding.ReadU32(b, off+1)
		if err != nil {
			return nil, 0, formatErr("json.ToText", "truncated number")
		}
		end := off + 5 + int(n)
		if end > len(b) {
			return nil, 0, formatErr("json.ToText", "truncated number")
		}
		return append(dst, b[off+5:end]...), end, nil
	case TagArray:
		return writeArray(dst, b, off)
	case TagObject:
		return writeObject(dst, b, off)
	default:
		return nil, 0, formatErr("json.ToText", "unknown JSON tag")
	}
}

func writeArray(dst, b []byte, off int) ([]byte, int, error) {
	size, err := encoding.ReadU32(b, off+1)
	if err != nil {
		return nil, 0, formatErr("json.ToText", "truncated array")
	}
	body := off + 5
	end := body + int(size)
	n, err := encoding.ReadU32(b, body)
	if err != nil {
		return nil, 0, formatErr("json.ToText", "truncated array count")
	}
	dst = append(dst, '[')
	cur := body + 4
	for i := uint32(0); i < n; i++ {
		if i > 0 {
			dst = append(dst, ',')
		}
		dst, cur, err = writeText(dst, b, cur)
		if err != nil {
			return nil, 0, err
		}
	}
	if cur != end {
		return nil, 0, formatErr("json.ToText", "array size mismatch")
	}
	return append(dst, ']'), end, nil
}

func writeObject(dst, b []byte, off int) ([]byte, int, error) {
	size, err := encoding.ReadU32(b, off+1)
	if err != nil {
		return nil, 0, formatErr("json.ToText", "truncated object")
	}
	body := off + 5
	end := body + int(size)
	n, err := encoding.ReadU16(b, body)
	if err != nil {
		return nil, 0, formatErr("json.ToText", "truncated object count")
	}
	dst = append(dst, '{')
	cur := body + 2
	for i := uint16(0); i < n; i++ {
		if i > 0 {
			dst = append(dst, ',')
		}
		klen, err := encoding.ReadU16(b, cur)
		if err != nil {
			return nil, 0, formatErr("json.ToText", "truncated object key")
		}
		cur += 2
		if cur+int(klen) > end {
			return nil, 0, formatErr("json.ToText", "truncated object key")
		}
		dst = appendJSONString(dst, b[cur:cur+int(klen)])
		cur += int(klen)
		dst = append(dst, ':')
		dst, cur, err = writeText(dst, b, cur)
		if err != nil {
			return nil, 0, err
		}
	}
	if cur != end {
		return nil, 0, formatErr("json.ToText", "object size mismatch")
	}
	return append(dst, '}'), end, nil
}

func appendJSONString(dst, s []byte) []byte {
	dst = append(dst, '"')
	for i := 0; i < len(s); {
		c := s[i]
		if c >= 0x20 && c != '"' && c != '\\' {
			if c < utf8.RuneSelf {
				dst = append(dst, c)
				i++
				continue
			}
			r, size := utf8.DecodeRune(s[i:])
			if r == utf8.RuneError && size == 1 {
				dst = append(dst, `\uFFFD`...)
				i++
				continue
			}
			dst = append(dst, s[i:i+size]...)
			i += size
			continue
		}
		switch c {
		case '"', '\\':
			dst = append(dst, '\\', c)
		case '\b':
			dst = append(dst, `\b`...)
		case '\f':
			dst = append(dst, `\f`...)
		case '\n':
			dst = append(dst, `\n`...)
		case '\r':
			dst = append(dst, `\r`...)
		case '\t':
			dst = append(dst, `\t`...)
		default:
			dst = append(dst, `\u00`...)
			const hex = "0123456789abcdef"
			dst = append(dst, hex[c>>4], hex[c&0x0F])
		}
		i++
	}
	return append(dst, '"')
}
