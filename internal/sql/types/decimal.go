package types

import (
	"math/big"
	"strings"
	"unicode"

	"github.com/bzync/nextsql/internal/encoding"
	"github.com/bzync/nextsql/internal/nerr"
)

// Decimal is an arbitrary-precision scaled integer (unscaled coef + scale).
type Decimal struct {
	Coef  *big.Int
	Scale int
}

func (d Decimal) IsZero() bool {
	return d.Coef == nil || d.Coef.Sign() == 0
}

func (d Decimal) clone() Decimal {
	if d.Coef == nil {
		return Decimal{Scale: d.Scale, Coef: new(big.Int)}
	}
	return Decimal{Scale: d.Scale, Coef: new(big.Int).Set(d.Coef)}
}

func (d Decimal) Clone() Decimal { return d.clone() }

func (d Decimal) Negate() Decimal {
	out := d.clone()
	if out.Coef != nil && out.Coef.Sign() != 0 {
		out.Coef.Neg(out.Coef)
	}
	return out
}

func alignScale(a, b Decimal) (Decimal, Decimal) {
	a, b = a.clone(), b.clone()
	if a.Coef == nil {
		a.Coef = new(big.Int)
	}
	if b.Coef == nil {
		b.Coef = new(big.Int)
	}
	if a.Scale < b.Scale {
		a.Coef.Mul(a.Coef, pow10(b.Scale-a.Scale))
		a.Scale = b.Scale
	} else if b.Scale < a.Scale {
		b.Coef.Mul(b.Coef, pow10(a.Scale-b.Scale))
		b.Scale = a.Scale
	}
	return a, b
}

func AddDec(a, b Decimal) Decimal {
	x, y := alignScale(a, b)
	x.Coef.Add(x.Coef, y.Coef)
	return x
}

func SubDec(a, b Decimal) Decimal {
	x, y := alignScale(a, b)
	x.Coef.Sub(x.Coef, y.Coef)
	return x
}

func MulDec(a, b Decimal) Decimal {
	x, y := a.clone(), b.clone()
	if x.Coef == nil {
		x.Coef = new(big.Int)
	}
	if y.Coef == nil {
		y.Coef = new(big.Int)
	}
	x.Coef.Mul(x.Coef, y.Coef)
	x.Scale += y.Scale
	return x
}

func QuoDec(a, b Decimal) (Decimal, error) {
	x, y := a.clone(), b.clone()
	if x.Coef == nil {
		x.Coef = new(big.Int)
	}
	if y.Coef == nil || y.Coef.Sign() == 0 {
		return Decimal{}, nerr.New(nerr.InvalidArgument, "types.QuoDec", "division by zero")
	}
	x.Coef.Mul(x.Coef, pow10(6))
	x.Coef.Quo(x.Coef, y.Coef)
	x.Scale = x.Scale + 6 - y.Scale
	return x, nil
}

// DecimalFromInt64 is a non-negative or negative integer with scale 0.
func DecimalFromInt64(n int64) Decimal {
	return Decimal{Coef: new(big.Int).SetInt64(n), Scale: 0}
}

func ParseDecimal(s string) (Decimal, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Decimal{}, nerr.New(nerr.InvalidArgument, "types.ParseDecimal", "empty decimal")
	}
	neg := false
	if s[0] == '+' {
		s = s[1:]
	} else if s[0] == '-' {
		neg = true
		s = s[1:]
	}
	if s == "" {
		return Decimal{}, nerr.New(nerr.InvalidArgument, "types.ParseDecimal", "invalid decimal")
	}
	intp, frac, ok := strings.Cut(s, ".")
	if !ok {
		intp, frac = s, ""
	}
	if intp == "" {
		intp = "0"
	}
	for _, r := range intp {
		if !unicode.IsDigit(r) {
			return Decimal{}, nerr.New(nerr.InvalidArgument, "types.ParseDecimal", "invalid decimal")
		}
	}
	for _, r := range frac {
		if !unicode.IsDigit(r) {
			return Decimal{}, nerr.New(nerr.InvalidArgument, "types.ParseDecimal", "invalid decimal")
		}
	}
	digits := strings.TrimLeft(intp+frac, "0")
	if digits == "" {
		digits = "0"
	}
	coef, ok := new(big.Int).SetString(digits, 10)
	if !ok {
		return Decimal{}, nerr.New(nerr.InvalidArgument, "types.ParseDecimal", "invalid decimal")
	}
	if neg && coef.Sign() != 0 {
		coef.Neg(coef)
	}
	return Decimal{Coef: coef, Scale: len(frac)}, nil
}

func (d Decimal) Rescale(prec, scale int) (Decimal, error) {
	out := d.clone()
	if out.Coef == nil {
		out.Coef = new(big.Int)
	}
	if scale > out.Scale {
		mul := pow10(scale - out.Scale)
		out.Coef.Mul(out.Coef, mul)
		out.Scale = scale
	} else if scale < out.Scale {
		div := pow10(out.Scale - scale)
		rem := new(big.Int)
		out.Coef.QuoRem(out.Coef, div, rem)
		if rem.Sign() != 0 {
			return Decimal{}, nerr.New(nerr.InvalidArgument, "types.Decimal.Rescale", "decimal would lose scale")
		}
		out.Scale = scale
	}
	if prec > 0 {
		abs := new(big.Int).Abs(out.Coef)
		if abs.Cmp(pow10(prec)) >= 0 {
			return Decimal{}, nerr.New(nerr.InvalidArgument, "types.Decimal.Rescale", "decimal exceeds precision")
		}
	}
	return out, nil
}

func (d Decimal) Cmp(o Decimal) int {
	a, b := d.clone(), o.clone()
	if a.Coef == nil {
		a.Coef = new(big.Int)
	}
	if b.Coef == nil {
		b.Coef = new(big.Int)
	}
	if a.Scale < b.Scale {
		a.Coef.Mul(a.Coef, pow10(b.Scale-a.Scale))
	} else if b.Scale < a.Scale {
		b.Coef.Mul(b.Coef, pow10(a.Scale-b.Scale))
	}
	return a.Coef.Cmp(b.Coef)
}

func (d Decimal) String() string {
	if d.Coef == nil || d.Coef.Sign() == 0 {
		if d.Scale <= 0 {
			return "0"
		}
		return "0." + strings.Repeat("0", d.Scale)
	}
	neg := d.Coef.Sign() < 0
	abs := new(big.Int).Abs(d.Coef).String()
	if d.Scale <= 0 {
		if neg {
			return "-" + abs
		}
		return abs
	}
	if len(abs) <= d.Scale {
		abs = strings.Repeat("0", d.Scale-len(abs)+1) + abs
	}
	ip := abs[:len(abs)-d.Scale]
	fp := abs[len(abs)-d.Scale:]
	if neg {
		return "-" + ip + "." + fp
	}
	return ip + "." + fp
}

func encodeDecimal(d Decimal) []byte {
	if d.Coef == nil {
		d.Coef = new(big.Int)
	}
	coef := d.Coef.Bytes()
	buf := make([]byte, 4+len(coef))
	flags := byte(0)
	if d.Coef.Sign() < 0 {
		flags = 1
	}
	buf[0] = flags
	buf[1] = 0
	encoding.PutU16(buf, 2, uint16(d.Scale))
	copy(buf[4:], coef)
	return buf
}

func decodeDecimal(b []byte) (Decimal, error) {
	if len(b) < 4 {
		return Decimal{}, nerr.New(nerr.InvalidFormat, "types.decodeDecimal", "truncated decimal")
	}
	d := Decimal{Scale: int(encoding.U16(b, 2)), Coef: new(big.Int).SetBytes(b[4:])}
	if b[0]&1 != 0 && d.Coef.Sign() != 0 {
		d.Coef.Neg(d.Coef)
	}
	return d, nil
}

func pow10(n int) *big.Int {
	return new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(n)), nil)
}
