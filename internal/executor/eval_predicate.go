package executor

import (
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/sql/types"
)

// evalPredicateFn evaluates the two predicate/conversion builtins the grammar
// produces for `x [NOT] LIKE p [ESCAPE c]` and `CAST(x AS t)`. Both are
// ordinary calls so that every walker in the engine already understands them.
func evalPredicateFn(name string, args []types.Value) (types.Value, bool, error) {
	switch name {
	case "like":
		v, err := evalLike(args)
		return v, true, err
	case "cast":
		v, err := evalCast(args)
		return v, true, err
	}
	return types.Value{}, false, nil
}

// evalLike implements SQL LIKE: `_` matches exactly one character, `%` matches
// any sequence including the empty one, and every other character matches
// itself. Matching is over Unicode code points, like LENGTH and SUBSTRING.
// A NULL value, pattern or escape yields NULL, not false.
func evalLike(args []types.Value) (types.Value, error) {
	if len(args) != 2 && len(args) != 3 {
		return types.Value{}, nerr.New(nerr.InvalidArgument, "executor.eval", "LIKE takes a value, a pattern, and an optional ESCAPE character")
	}
	for _, a := range args {
		if a.Null {
			return types.Null(types.Bool()), nil
		}
	}
	val, err := likeText(args[0], "LIKE value")
	if err != nil {
		return types.Value{}, err
	}
	pat, err := likeText(args[1], "LIKE pattern")
	if err != nil {
		return types.Value{}, err
	}
	escape := rune(-1)
	if len(args) == 3 {
		escText, err := likeText(args[2], "LIKE ESCAPE")
		if err != nil {
			return types.Value{}, err
		}
		esc := []rune(escText)
		if len(esc) != 1 {
			return types.Value{}, nerr.New(nerr.InvalidArgument, "executor.eval", "LIKE ESCAPE takes exactly one character")
		}
		escape = esc[0]
	}
	toks, err := compileLike([]rune(pat), escape)
	if err != nil {
		return types.Value{}, err
	}
	return types.BoolValue(matchLike([]rune(val), toks)), nil
}

// likeText accepts the string family the string builtins accept. CHAR's
// padding is not significant content, matching the other string functions.
func likeText(v types.Value, what string) (string, error) {
	switch v.Typ.Kind {
	case types.KindString, types.KindText, types.KindVarchar:
		return v.Str, nil
	case types.KindChar:
		return trimCharPad(v.Str), nil
	}
	return "", nerr.New(nerr.InvalidArgument, "executor.eval", what+" requires STRING or TEXT")
}

func trimCharPad(s string) string {
	i := len(s)
	for i > 0 && s[i-1] == ' ' {
		i--
	}
	return s[:i]
}

type likeTokKind uint8

const (
	likeLiteral likeTokKind = iota
	likeAnySingle
	likeAnyMany
)

type likeTok struct {
	kind likeTokKind
	r    rune
}

// compileLike turns a pattern into tokens, resolving ESCAPE. An escape
// character must introduce `%`, `_` or itself; anything else is an invalid
// escape sequence and is refused rather than guessed at.
func compileLike(pat []rune, escape rune) ([]likeTok, error) {
	toks := make([]likeTok, 0, len(pat))
	for i := 0; i < len(pat); i++ {
		c := pat[i]
		if escape >= 0 && c == escape {
			i++
			if i >= len(pat) {
				return nil, nerr.New(nerr.InvalidArgument, "executor.eval", "LIKE pattern ends with a dangling escape character")
			}
			n := pat[i]
			if n != '%' && n != '_' && n != escape {
				return nil, nerr.New(nerr.InvalidArgument, "executor.eval", "LIKE escape character must precede '%', '_', or itself")
			}
			toks = append(toks, likeTok{kind: likeLiteral, r: n})
			continue
		}
		switch c {
		case '%':
			// Collapse runs of `%`: they match the same thing and collapsing
			// keeps the matcher's backtracking bounded.
			if len(toks) > 0 && toks[len(toks)-1].kind == likeAnyMany {
				continue
			}
			toks = append(toks, likeTok{kind: likeAnyMany})
		case '_':
			toks = append(toks, likeTok{kind: likeAnySingle})
		default:
			toks = append(toks, likeTok{kind: likeLiteral, r: c})
		}
	}
	return toks, nil
}

// matchLike is the classic linear-scan wildcard matcher: it remembers the last
// `%` and resumes there on a mismatch. It never backtracks more than once per
// input position, so a hostile pattern cannot make it run exponentially the
// way a regular-expression engine can.
func matchLike(s []rune, pat []likeTok) bool {
	var si, pi int
	star, mark := -1, 0
	for si < len(s) {
		switch {
		case pi < len(pat) && pat[pi].kind == likeAnySingle:
			si++
			pi++
		case pi < len(pat) && pat[pi].kind == likeLiteral && pat[pi].r == s[si]:
			si++
			pi++
		case pi < len(pat) && pat[pi].kind == likeAnyMany:
			star = pi
			mark = si
			pi++
		case star >= 0:
			pi = star + 1
			mark++
			si = mark
		default:
			return false
		}
	}
	for pi < len(pat) && pat[pi].kind == likeAnyMany {
		pi++
	}
	return pi == len(pat)
}

// evalCast converts a value to the declared target type. The target arrives as
// a typed NULL literal built by the parser from the written type, so precision,
// scale, length, ENUM labels and vector element type are exact. Conversion
// itself is types.Coerce — the same matrix the engine applies everywhere else,
// so CAST can never reach a conversion the engine does not otherwise accept.
func evalCast(args []types.Value) (types.Value, error) {
	if len(args) != 2 {
		return types.Value{}, nerr.New(nerr.InvalidArgument, "executor.eval", "CAST takes a value and a target type")
	}
	target := args[1].Typ
	if target.Kind == types.KindInvalid {
		return types.Value{}, nerr.New(nerr.InvalidArgument, "executor.eval", "CAST requires a declared target type")
	}
	if args[0].Null {
		return types.Null(target), nil
	}
	out, err := types.Coerce(args[0], target)
	if err != nil {
		return types.Value{}, nerr.Wrap(nerr.InvalidArgument, "executor.eval", "CAST failed", err)
	}
	return out, nil
}
