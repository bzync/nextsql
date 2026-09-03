package lexer

import (
	"encoding/hex"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/bzync/nextsql/internal/nerr"
)

type Kind uint16

const (
	EOF Kind = iota
	Ident
	String
	Number
	Param
	HexLit // X'...' BLOB literal; Lit holds the decoded raw bytes
	// keywords
	KwCreate
	KwTable
	KwIndex
	KwUnique
	KwOn
	KwInsert
	KwInto
	KwValues
	KwSelect
	KwDistinct
	KwFrom
	KwWhere
	KwUpdate
	KwSet
	KwDelete
	KwBegin
	KwCommit
	KwRollback
	KwPrimary
	KwKey
	KwNot
	KwNull
	KwDefault
	KwAnd
	KwOr
	KwBetween
	KwIn
	KwIs
	KwLimit
	KwOffset
	KwAs
	KwTrue
	KwFalse
	KwTransaction
	KwRead
	KwCommitted
	KwSnapshot
	KwSerializable
	KwUuid
	KwString
	KwText
	KwBlob
	KwInt8
	KwInt16
	KwInt32
	KwInt64
	KwUint8
	KwUint16
	KwUint32
	KwUint64
	KwChar
	KwVarchar
	KwEnum
	KwFloat32
	KwFloat64
	KwDecimal
	KwTimestamptz
	KwTimestamp
	KwDate
	KwTime
	KwJson
	KwVector
	KwF32
	KwF16
	KwI8
	KwBitvector
	KwSparsevector
	KwExplain
	KwAnalyze
	KwMaintain
	KwPoint
	KwBox
	KwLocation
	KwLineString
	KwPolygon
	KwSpatial
	KwFulltext
	KwSearch
	KwFor
	KwNearest
	KwTo
	KwUsing
	KwHnsw
	KwCosine
	KwL2
	KwInnerProduct
	KwHamming
	KwJoin
	KwInner
	KwLeft
	KwRight
	KwFull
	KwCross
	KwOuter
	KwGroup
	KwHaving
	KwBy
	KwDrop
	KwUser
	KwRole
	KwGrant
	KwRevoke
	KwIdentified
	KwCluster
	KwDatabase
	KwSchema
	KwColumn
	KwFunction
	KwBackup
	KwReplication
	KwAdministration
	KwConnect
	KwExecute
	KwAll
	KwPrivileges
	KwAdmin
	KwReset
	KwForeign
	KwReferences
	KwConstraint
	KwCascade
	KwRestrict
	KwAction
	KwMatch
	KwAlter
	KwAdd
	KwRename
	KwRebuild
	KwOrder
	KwAsc
	KwDesc
	KwIf
	KwExists
	KwCase
	KwWhen
	KwThen
	KwElse
	KwEnd
	KwUnion
	KwIntersect
	KwExcept
	KwWith
	KwOver
	KwUpsert
	KwReturning
	KwWorkflow
	KwRun
	KwTrigger
	KwBefore
	KwAfter
	KwEach
	KwSchedule
	KwEvery
	KwAt
	KwCron
	KwShow
	KwTask
	KwTasks
	KwCancel
	KwSubscribe
	KwEncrypted
	KwClient
	KwTransfer
	KwLeader
	KwResource
	KwDrain
	KwMaintenance
	KwEnable
	KwDisable
	KwReconcile
	KwConfirm
	// symbols
	LParen
	RParen
	Comma
	Dot
	Star
	Eq
	Neq
	Lt
	Gt
	Lte
	Gte
	Plus
	Minus
	Slash
	Semi
)

func (k Kind) String() string {
	if s, ok := kindNames[k]; ok {
		return s
	}
	return "?"
}

var kindNames = map[Kind]string{
	EOF: "EOF", Ident: "ident", String: "string", Number: "number", Param: "param", HexLit: "hex literal",
	LParen: "(", RParen: ")", Comma: ",", Dot: ".", Star: "*", Eq: "=", Neq: "<>",
	Lt: "<", Gt: ">", Lte: "<=", Gte: ">=", Plus: "+", Minus: "-", Slash: "/", Semi: ";",
}

type Token struct {
	Kind Kind
	Lit  string
	Pos  int
}

type Lexer struct {
	src string
	i   int
	err error
}

func New(src string) *Lexer { return &Lexer{src: src} }

func (l *Lexer) Err() error { return l.err }

func (l *Lexer) Next() Token {
	l.skip()
	if l.err != nil {
		return Token{Kind: EOF, Pos: l.i}
	}
	if l.i >= len(l.src) {
		return Token{Kind: EOF, Pos: l.i}
	}
	pos := l.i
	c := l.src[l.i]
	switch c {
	case '(':
		l.i++
		return Token{Kind: LParen, Lit: "(", Pos: pos}
	case ')':
		l.i++
		return Token{Kind: RParen, Lit: ")", Pos: pos}
	case ',':
		l.i++
		return Token{Kind: Comma, Lit: ",", Pos: pos}
	case '.':
		if l.i+1 < len(l.src) && isDigit(l.src[l.i+1]) {
			return l.number(pos)
		}
		l.i++
		return Token{Kind: Dot, Lit: ".", Pos: pos}
	case '*':
		l.i++
		return Token{Kind: Star, Lit: "*", Pos: pos}
	case '+':
		l.i++
		return Token{Kind: Plus, Lit: "+", Pos: pos}
	case '-':
		l.i++
		return Token{Kind: Minus, Lit: "-", Pos: pos}
	case '/':
		l.i++
		return Token{Kind: Slash, Lit: "/", Pos: pos}
	case ';':
		l.i++
		return Token{Kind: Semi, Lit: ";", Pos: pos}
	case '=':
		l.i++
		return Token{Kind: Eq, Lit: "=", Pos: pos}
	case '<':
		l.i++
		if l.i < len(l.src) && l.src[l.i] == '>' {
			l.i++
			return Token{Kind: Neq, Lit: "<>", Pos: pos}
		}
		if l.i < len(l.src) && l.src[l.i] == '=' {
			l.i++
			return Token{Kind: Lte, Lit: "<=", Pos: pos}
		}
		return Token{Kind: Lt, Lit: "<", Pos: pos}
	case '>':
		l.i++
		if l.i < len(l.src) && l.src[l.i] == '=' {
			l.i++
			return Token{Kind: Gte, Lit: ">=", Pos: pos}
		}
		return Token{Kind: Gt, Lit: ">", Pos: pos}
	case '!':
		if l.i+1 < len(l.src) && l.src[l.i+1] == '=' {
			l.i += 2
			return Token{Kind: Neq, Lit: "!=", Pos: pos}
		}
		l.fail("unexpected character")
		return Token{Kind: EOF, Pos: pos}
	case '\'':
		return l.string(pos)
	case '"':
		return l.quotedIdent(pos)
	case '$':
		return l.param(pos)
	}
	if isDigit(c) {
		return l.number(pos)
	}
	if (c == 'x' || c == 'X') && l.i+1 < len(l.src) && l.src[l.i+1] == '\'' {
		return l.hexLiteral(pos)
	}
	if isIdentStart(c) {
		return l.ident(pos)
	}
	l.fail("unexpected character")
	return Token{Kind: EOF, Pos: pos}
}

func (l *Lexer) skip() {
	for l.i < len(l.src) {
		c := l.src[l.i]
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' {
			l.i++
			continue
		}
		if c == '-' && l.i+1 < len(l.src) && l.src[l.i+1] == '-' {
			l.i += 2
			for l.i < len(l.src) && l.src[l.i] != '\n' {
				l.i++
			}
			continue
		}
		if c == '/' && l.i+1 < len(l.src) && l.src[l.i+1] == '*' {
			l.i += 2
			for l.i+1 < len(l.src) && !(l.src[l.i] == '*' && l.src[l.i+1] == '/') {
				l.i++
			}
			if l.i+1 >= len(l.src) {
				l.fail("unterminated comment")
				return
			}
			l.i += 2
			continue
		}
		return
	}
}

func (l *Lexer) string(pos int) Token {
	l.i++ // opening '
	var b strings.Builder
	for l.i < len(l.src) {
		c := l.src[l.i]
		if c == '\'' {
			if l.i+1 < len(l.src) && l.src[l.i+1] == '\'' {
				b.WriteByte('\'')
				l.i += 2
				continue
			}
			l.i++
			return Token{Kind: String, Lit: b.String(), Pos: pos}
		}
		b.WriteByte(c)
		l.i++
	}
	l.fail("unterminated string")
	return Token{Kind: EOF, Pos: pos}
}

// hexLiteral scans an X'...' BLOB literal. The body must be an even number
// of hex digits (0-9, a-f, A-F); X” is the empty blob.
func (l *Lexer) hexLiteral(pos int) Token {
	l.i += 2 // 'x'/'X' and opening '
	start := l.i
	for l.i < len(l.src) && l.src[l.i] != '\'' {
		l.i++
	}
	if l.i >= len(l.src) {
		l.fail("unterminated hex literal")
		return Token{Kind: EOF, Pos: pos}
	}
	digits := l.src[start:l.i]
	l.i++ // closing '
	raw, err := hex.DecodeString(digits)
	if err != nil {
		l.fail("invalid hex literal")
		return Token{Kind: EOF, Pos: pos}
	}
	return Token{Kind: HexLit, Lit: string(raw), Pos: pos}
}

func (l *Lexer) quotedIdent(pos int) Token {
	l.i++
	var b strings.Builder
	for l.i < len(l.src) {
		c := l.src[l.i]
		if c == '"' {
			if l.i+1 < len(l.src) && l.src[l.i+1] == '"' {
				b.WriteByte('"')
				l.i += 2
				continue
			}
			l.i++
			if b.Len() == 0 {
				l.fail("empty quoted identifier")
				return Token{Kind: EOF, Pos: pos}
			}
			return Token{Kind: Ident, Lit: b.String(), Pos: pos}
		}
		b.WriteByte(c)
		l.i++
	}
	l.fail("unterminated identifier")
	return Token{Kind: EOF, Pos: pos}
}

func (l *Lexer) param(pos int) Token {
	l.i++
	start := l.i
	if l.i < len(l.src) && isDigit(l.src[l.i]) {
		for l.i < len(l.src) && isDigit(l.src[l.i]) {
			l.i++
		}
		return Token{Kind: Param, Lit: l.src[start:l.i], Pos: pos}
	}
	if l.i < len(l.src) && isIdentStart(l.src[l.i]) {
		for l.i < len(l.src) && isIdentPart(l.src[l.i]) {
			l.i++
		}
		return Token{Kind: Param, Lit: l.src[start:l.i], Pos: pos}
	}
	l.fail("invalid parameter")
	return Token{Kind: EOF, Pos: pos}
}

func (l *Lexer) number(pos int) Token {
	start := l.i
	for l.i < len(l.src) && isDigit(l.src[l.i]) {
		l.i++
	}
	if l.i < len(l.src) && l.src[l.i] == '.' {
		l.i++
		for l.i < len(l.src) && isDigit(l.src[l.i]) {
			l.i++
		}
	}
	return Token{Kind: Number, Lit: l.src[start:l.i], Pos: pos}
}

func (l *Lexer) ident(pos int) Token {
	start := l.i
	for l.i < len(l.src) {
		r, w := utf8.DecodeRuneInString(l.src[l.i:])
		if r == utf8.RuneError && w == 1 {
			break
		}
		if l.i == start {
			if !unicode.IsLetter(r) && r != '_' {
				break
			}
		} else if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' {
			break
		}
		l.i += w
	}
	lit := l.src[start:l.i]
	if k, ok := keywords[strings.ToLower(lit)]; ok {
		return Token{Kind: k, Lit: strings.ToLower(lit), Pos: pos}
	}
	return Token{Kind: Ident, Lit: strings.ToLower(lit), Pos: pos}
}

func (l *Lexer) fail(msg string) {
	if l.err == nil {
		l.err = nerr.New(nerr.Syntax, "sql.lexer", msg)
	}
}

func isDigit(c byte) bool      { return c >= '0' && c <= '9' }
func isIdentStart(c byte) bool { return c == '_' || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') }
func isIdentPart(c byte) bool  { return isIdentStart(c) || isDigit(c) }

var keywords = map[string]Kind{
	"create": KwCreate, "table": KwTable, "index": KwIndex, "unique": KwUnique, "on": KwOn,
	"insert": KwInsert, "into": KwInto, "values": KwValues, "select": KwSelect, "distinct": KwDistinct, "from": KwFrom,
	"where": KwWhere, "update": KwUpdate, "set": KwSet, "delete": KwDelete, "begin": KwBegin,
	"commit": KwCommit, "rollback": KwRollback, "primary": KwPrimary, "key": KwKey,
	"not": KwNot, "null": KwNull, "default": KwDefault, "and": KwAnd, "or": KwOr,
	"between": KwBetween, "in": KwIn, "is": KwIs, "limit": KwLimit, "offset": KwOffset, "as": KwAs, "true": KwTrue,
	"false": KwFalse, "transaction": KwTransaction, "read": KwRead, "committed": KwCommitted,
	"snapshot": KwSnapshot, "serializable": KwSerializable, "uuid": KwUuid, "string": KwString,
	"text": KwText, "blob": KwBlob, "int8": KwInt8, "int16": KwInt16, "int32": KwInt32, "int64": KwInt64,
	"uint8": KwUint8, "uint16": KwUint16, "uint32": KwUint32, "uint64": KwUint64,
	"char": KwChar, "varchar": KwVarchar, "enum": KwEnum, "float32": KwFloat32, "float64": KwFloat64,
	"decimal": KwDecimal, "timestamptz": KwTimestamptz, "timestamp": KwTimestamp, "date": KwDate, "time": KwTime, "json": KwJson,
	"vector": KwVector, "bitvector": KwBitvector, "sparsevector": KwSparsevector, "f32": KwF32, "f16": KwF16, "i8": KwI8, "explain": KwExplain, "analyze": KwAnalyze, "maintain": KwMaintain,
	"point": KwPoint, "box": KwBox, "location": KwLocation,
	"linestring": KwLineString, "polygon": KwPolygon, "spatial": KwSpatial,
	"fulltext": KwFulltext, "search": KwSearch, "for": KwFor,
	"nearest": KwNearest, "to": KwTo, "using": KwUsing, "hnsw": KwHnsw,
	"cosine": KwCosine, "l2": KwL2, "inner_product": KwInnerProduct, "hamming": KwHamming,
	"join": KwJoin, "inner": KwInner, "left": KwLeft, "right": KwRight, "full": KwFull, "cross": KwCross, "outer": KwOuter, "group": KwGroup, "having": KwHaving, "by": KwBy,
	"drop": KwDrop, "user": KwUser, "role": KwRole, "grant": KwGrant, "revoke": KwRevoke,
	"identified": KwIdentified, "cluster": KwCluster, "database": KwDatabase,
	"schema": KwSchema, "column": KwColumn, "function": KwFunction,
	"backup": KwBackup, "replication": KwReplication, "administration": KwAdministration,
	"connect": KwConnect, "execute": KwExecute, "all": KwAll, "privileges": KwPrivileges,
	"admin": KwAdmin, "reset": KwReset,
	"foreign": KwForeign, "references": KwReferences, "constraint": KwConstraint,
	"cascade": KwCascade, "restrict": KwRestrict, "action": KwAction, "match": KwMatch,
	"alter": KwAlter, "add": KwAdd, "rename": KwRename, "rebuild": KwRebuild,
	"order": KwOrder, "asc": KwAsc, "desc": KwDesc,
	"if": KwIf, "exists": KwExists,
	"case": KwCase, "when": KwWhen, "then": KwThen, "else": KwElse, "end": KwEnd,
	"union":     KwUnion,
	"intersect": KwIntersect, "except": KwExcept,
	"with": KwWith, "over": KwOver,
	"schedule": KwSchedule, "every": KwEvery, "at": KwAt, "cron": KwCron,
	"upsert": KwUpsert, "returning": KwReturning,
	"workflow": KwWorkflow, "run": KwRun, "trigger": KwTrigger,
	"before": KwBefore, "after": KwAfter, "each": KwEach,
	"show": KwShow, "task": KwTask, "tasks": KwTasks, "cancel": KwCancel, "subscribe": KwSubscribe,
	"encrypted": KwEncrypted, "client": KwClient,
	"transfer": KwTransfer, "leader": KwLeader,
	"resource":    KwResource,
	"drain":       KwDrain,
	"maintenance": KwMaintenance,
	"enable":      KwEnable,
	"disable":     KwDisable,
	"reconcile":   KwReconcile,
	"confirm":     KwConfirm,
}
