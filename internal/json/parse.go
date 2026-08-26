package json

import (
	"bytes"
	"sort"
	"strconv"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/bzync/nextsql/internal/encoding"
)

// FromText parses RFC 8259 JSON text into an NSJB document.
func FromText(src []byte) ([]byte, error) {
	if len(src) > MaxBytes {
		return nil, argErr("json.FromText", "JSON exceeds size limit")
	}
	p := parser{src: src}
	p.dst = append(p.dst, Magic...)
	p.dst = append(p.dst, Version)
	p.skipWS()
	if p.i >= len(p.src) {
		return nil, formatErr("json.FromText", "empty JSON")
	}
	if err := p.value(); err != nil {
		return nil, err
	}
	p.skipWS()
	if p.i != len(p.src) {
		return nil, formatErr("json.FromText", "trailing JSON text")
	}
	if len(p.dst) > MaxBytes {
		return nil, argErr("json.FromText", "JSON exceeds size limit")
	}
	return p.dst, nil
}

type parser struct {
	src   []byte
	i     int
	depth int
	dst   []byte
}

func (p *parser) skipWS() {
	for p.i < len(p.src) {
		switch p.src[p.i] {
		case ' ', '\t', '\n', '\r':
			p.i++
		default:
			return
		}
	}
}

func (p *parser) peek() byte {
	if p.i >= len(p.src) {
		return 0
	}
	return p.src[p.i]
}

func (p *parser) value() error {
	if p.i >= len(p.src) {
		return formatErr("json.FromText", "truncated JSON")
	}
	switch p.src[p.i] {
	case 'n':
		return p.lit("null", TagNull)
	case 't':
		return p.lit("true", TagTrue)
	case 'f':
		return p.lit("false", TagFalse)
	case '"':
		s, err := p.string()
		if err != nil {
			return err
		}
		p.appendTagged(TagString, s)
		return nil
	case '[':
		return p.array()
	case '{':
		return p.object()
	case '-', '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
		return p.number()
	default:
		return formatErr("json.FromText", "invalid JSON value")
	}
}

func (p *parser) lit(want string, tag byte) error {
	if p.i+len(want) > len(p.src) || string(p.src[p.i:p.i+len(want)]) != want {
		return formatErr("json.FromText", "invalid JSON literal")
	}
	p.i += len(want)
	p.dst = append(p.dst, tag)
	return nil
}

func (p *parser) array() error {
	p.depth++
	if p.depth > MaxDepth {
		return argErr("json.FromText", "JSON exceeds depth limit")
	}
	p.i++
	start := len(p.dst)
	p.dst = append(p.dst, TagArray, 0, 0, 0, 0)
	countOff := len(p.dst)
	p.dst = append(p.dst, 0, 0, 0, 0)
	p.skipWS()
	var n uint32
	if p.peek() != ']' {
		for {
			if err := p.value(); err != nil {
				return err
			}
			n++
			if n > MaxElems {
				return argErr("json.FromText", "JSON array exceeds limit")
			}
			p.skipWS()
			if p.peek() == ',' {
				p.i++
				p.skipWS()
				continue
			}
			break
		}
	}
	if p.peek() != ']' {
		return formatErr("json.FromText", "expected ]")
	}
	p.i++
	p.depth--
	encoding.PutU32(p.dst, start+1, uint32(len(p.dst)-(start+5)))
	encoding.PutU32(p.dst, countOff, n)
	return nil
}

type objEnt struct {
	key   []byte
	value []byte
}

func (p *parser) object() error {
	p.depth++
	if p.depth > MaxDepth {
		return argErr("json.FromText", "JSON exceeds depth limit")
	}
	p.i++
	p.skipWS()
	var ents []objEnt
	if p.peek() != '}' {
		for {
			if p.peek() != '"' {
				return formatErr("json.FromText", "expected object key")
			}
			key, err := p.string()
			if err != nil {
				return err
			}
			p.skipWS()
			if p.peek() != ':' {
				return formatErr("json.FromText", "expected :")
			}
			p.i++
			p.skipWS()
			valStart := len(p.dst)
			if err := p.value(); err != nil {
				return err
			}
			val := append([]byte(nil), p.dst[valStart:]...)
			p.dst = p.dst[:valStart]
			ents = append(ents, objEnt{key: key, value: val})
			if len(ents) > MaxElems {
				return argErr("json.FromText", "JSON object exceeds limit")
			}
			p.skipWS()
			if p.peek() == ',' {
				p.i++
				p.skipWS()
				continue
			}
			break
		}
	}
	if p.peek() != '}' {
		return formatErr("json.FromText", "expected }")
	}
	p.i++
	p.depth--

	// Last key wins, then sort for a deterministic stored form.
	ents = collapseKeys(ents)
	sort.Slice(ents, func(i, j int) bool {
		return bytes.Compare(ents[i].key, ents[j].key) < 0
	})

	start := len(p.dst)
	p.dst = append(p.dst, TagObject, 0, 0, 0, 0)
	if len(ents) > 0xFFFF {
		return argErr("json.FromText", "JSON object exceeds limit")
	}
	var nbuf [2]byte
	encoding.PutU16(nbuf[:], 0, uint16(len(ents)))
	p.dst = append(p.dst, nbuf[:]...)
	for _, e := range ents {
		if len(e.key) > 0xFFFF {
			return argErr("json.FromText", "JSON key exceeds limit")
		}
		encoding.PutU16(nbuf[:], 0, uint16(len(e.key)))
		p.dst = append(p.dst, nbuf[:]...)
		p.dst = append(p.dst, e.key...)
		p.dst = append(p.dst, e.value...)
	}
	encoding.PutU32(p.dst, start+1, uint32(len(p.dst)-(start+5)))
	return nil
}

func collapseKeys(ents []objEnt) []objEnt {
	if len(ents) < 2 {
		return ents
	}
	last := make(map[string]int, len(ents))
	for i, e := range ents {
		last[string(e.key)] = i
	}
	if len(last) == len(ents) {
		return ents
	}
	out := make([]objEnt, 0, len(last))
	seen := make(map[string]struct{}, len(last))
	for i, e := range ents {
		k := string(e.key)
		if last[k] != i {
			continue
		}
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, e)
	}
	return out
}

func (p *parser) appendTagged(tag byte, payload []byte) {
	var nbuf [4]byte
	encoding.PutU32(nbuf[:], 0, uint32(len(payload)))
	p.dst = append(p.dst, tag)
	p.dst = append(p.dst, nbuf[:]...)
	p.dst = append(p.dst, payload...)
}

func (p *parser) number() error {
	start := p.i
	if p.peek() == '-' {
		p.i++
	}
	if p.i >= len(p.src) {
		return formatErr("json.FromText", "truncated number")
	}
	if p.src[p.i] == '0' {
		p.i++
		if p.i < len(p.src) && p.src[p.i] >= '0' && p.src[p.i] <= '9' {
			return formatErr("json.FromText", "invalid number")
		}
	} else if p.src[p.i] >= '1' && p.src[p.i] <= '9' {
		for p.i < len(p.src) && p.src[p.i] >= '0' && p.src[p.i] <= '9' {
			p.i++
		}
	} else {
		return formatErr("json.FromText", "invalid number")
	}
	frac := false
	if p.peek() == '.' {
		frac = true
		p.i++
		if p.i >= len(p.src) || p.src[p.i] < '0' || p.src[p.i] > '9' {
			return formatErr("json.FromText", "invalid number")
		}
		for p.i < len(p.src) && p.src[p.i] >= '0' && p.src[p.i] <= '9' {
			p.i++
		}
	}
	exp := false
	if p.peek() == 'e' || p.peek() == 'E' {
		exp = true
		p.i++
		if p.peek() == '+' || p.peek() == '-' {
			p.i++
		}
		if p.i >= len(p.src) || p.src[p.i] < '0' || p.src[p.i] > '9' {
			return formatErr("json.FromText", "invalid number")
		}
		for p.i < len(p.src) && p.src[p.i] >= '0' && p.src[p.i] <= '9' {
			p.i++
		}
	}
	tok := p.src[start:p.i]
	if !frac && !exp {
		if n, err := strconv.ParseInt(string(tok), 10, 64); err == nil {
			var buf [8]byte
			encoding.PutU64(buf[:], 0, uint64(n))
			p.dst = append(p.dst, TagI64)
			p.dst = append(p.dst, buf[:]...)
			return nil
		}
	}
	p.appendTagged(TagNumber, tok)
	return nil
}

func (p *parser) string() ([]byte, error) {
	if p.peek() != '"' {
		return nil, formatErr("json.FromText", "expected string")
	}
	p.i++
	var out []byte
	for p.i < len(p.src) {
		c := p.src[p.i]
		if c == '"' {
			p.i++
			if len(out) > MaxString {
				return nil, argErr("json.FromText", "JSON string exceeds limit")
			}
			if !utf8.Valid(out) {
				return nil, formatErr("json.FromText", "JSON string is not UTF-8")
			}
			return out, nil
		}
		if c == '\\' {
			p.i++
			if p.i >= len(p.src) {
				return nil, formatErr("json.FromText", "truncated escape")
			}
			esc := p.src[p.i]
			p.i++
			switch esc {
			case '"', '\\', '/':
				out = append(out, esc)
			case 'b':
				out = append(out, '\b')
			case 'f':
				out = append(out, '\f')
			case 'n':
				out = append(out, '\n')
			case 'r':
				out = append(out, '\r')
			case 't':
				out = append(out, '\t')
			case 'u':
				r, err := p.u4()
				if err != nil {
					return nil, err
				}
				if utf16.IsSurrogate(r) {
					if p.i+2 <= len(p.src) && p.src[p.i] == '\\' && p.src[p.i+1] == 'u' {
						p.i += 2
						r2, err := p.u4()
						if err != nil {
							return nil, err
						}
						r = utf16.DecodeRune(r, r2)
					} else {
						r = utf8.RuneError
					}
				}
				var buf [utf8.UTFMax]byte
				n := utf8.EncodeRune(buf[:], r)
				out = append(out, buf[:n]...)
			default:
				return nil, formatErr("json.FromText", "invalid escape")
			}
			continue
		}
		if c < 0x20 {
			return nil, formatErr("json.FromText", "unescaped control in string")
		}
		if c < utf8.RuneSelf {
			out = append(out, c)
			p.i++
			continue
		}
		r, size := utf8.DecodeRune(p.src[p.i:])
		if r == utf8.RuneError && size == 1 {
			return nil, formatErr("json.FromText", "invalid UTF-8")
		}
		out = append(out, p.src[p.i:p.i+size]...)
		p.i += size
	}
	return nil, formatErr("json.FromText", "unterminated string")
}

func (p *parser) u4() (rune, error) {
	if p.i+4 > len(p.src) {
		return 0, formatErr("json.FromText", "truncated \\u escape")
	}
	var r rune
	for i := 0; i < 4; i++ {
		c := p.src[p.i+i]
		var v rune
		switch {
		case c >= '0' && c <= '9':
			v = rune(c - '0')
		case c >= 'a' && c <= 'f':
			v = rune(c-'a') + 10
		case c >= 'A' && c <= 'F':
			v = rune(c-'A') + 10
		default:
			return 0, formatErr("json.FromText", "invalid \\u escape")
		}
		r = r<<4 | v
	}
	p.i += 4
	return r, nil
}
