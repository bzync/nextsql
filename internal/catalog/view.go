package catalog

import (
	"bytes"

	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/security"
)

const (
	viewMagic        = "NSVW"
	viewVersion      = 1
	KeyView     byte = 'V'

	MaxViewNameBytes  = 128
	MaxViewColumns    = 1024
	MaxViewDescriptor = security.MaxSQLBytes
	// MaxViewDepth bounds how far view references may nest. A view over a
	// view is ordinary; an unbounded chain (or a cycle introduced by CREATE OR
	// REPLACE) would be unbounded work at bind time.
	MaxViewDepth = 8
)

// View is the durable, versioned view descriptor.
//
// The defining query is stored as SQL text rather than as an encoded plan: a
// view has to survive changes to the shape of the tables under it, and text is
// the only form that re-resolves against the catalog as it is at query time.
// It is re-parsed when the view is used, and the parse is bounded by the same
// statement limits any other statement has.
//
// Columns, when present, are the operator-declared output names from
// `CREATE VIEW v (a, b) AS ...`. Empty means the query's own output names are
// used.
type View struct {
	ID      uint32
	Name    string
	Owner   string
	Columns []string
	Query   string
}

func (v *View) Clone() *View {
	if v == nil {
		return nil
	}
	out := *v
	out.Columns = append([]string(nil), v.Columns...)
	return &out
}

func ViewKey(name string) []byte {
	k := make([]byte, 1+len(name))
	k[0] = KeyView
	copy(k[1:], name)
	return k
}

func validateView(v *View) error {
	if v == nil {
		return nerr.New(nerr.InvalidArgument, "catalog.EncodeView", "nil view")
	}
	if v.Name == "" || len(v.Name) > MaxViewNameBytes {
		return nerr.New(nerr.InvalidArgument, "catalog.EncodeView", "invalid view name")
	}
	if ReservedName(v.Name) {
		return nerr.New(nerr.InvalidArgument, "catalog.EncodeView", "view name prefix nsql_ is reserved")
	}
	if v.Query == "" || len(v.Query) > MaxViewDescriptor {
		return nerr.New(nerr.InvalidArgument, "catalog.EncodeView", "invalid view query")
	}
	if len(v.Columns) > MaxViewColumns {
		return nerr.New(nerr.InvalidArgument, "catalog.EncodeView", "too many view columns")
	}
	seen := make(map[string]struct{}, len(v.Columns))
	for _, c := range v.Columns {
		if c == "" {
			return nerr.New(nerr.InvalidArgument, "catalog.EncodeView", "empty view column name")
		}
		if _, dup := seen[c]; dup {
			return nerr.New(nerr.InvalidArgument, "catalog.EncodeView", "duplicate view column name")
		}
		seen[c] = struct{}{}
	}
	return nil
}

func EncodeView(v *View) ([]byte, error) {
	if err := validateView(v); err != nil {
		return nil, err
	}
	buf := append([]byte(nil), viewMagic...)
	buf = appendU16(buf, viewVersion)
	buf = appendU32(buf, v.ID)
	buf = appendString(buf, v.Name)
	buf = appendString(buf, v.Owner)
	buf = appendU16(buf, uint16(len(v.Columns)))
	for _, c := range v.Columns {
		buf = appendString(buf, c)
	}
	buf = appendString(buf, v.Query)
	if len(buf) > MaxViewDescriptor {
		return nil, nerr.New(nerr.InvalidArgument, "catalog.EncodeView", "view descriptor exceeds size limit")
	}
	return buf, nil
}

func DecodeView(raw []byte) (*View, error) {
	if len(raw) > MaxViewDescriptor {
		return nil, nerr.New(nerr.InvalidFormat, "catalog.DecodeView", "view descriptor exceeds size limit")
	}
	if len(raw) < len(viewMagic) || !bytes.Equal(raw[:len(viewMagic)], []byte(viewMagic)) {
		return nil, nerr.New(nerr.InvalidFormat, "catalog.DecodeView", "bad view magic")
	}
	off := len(viewMagic)
	ver, off, err := takeU16(raw, off)
	if err != nil {
		return nil, err
	}
	if ver != viewVersion {
		return nil, nerr.New(nerr.InvalidFormat, "catalog.DecodeView", "unsupported view version")
	}
	v := &View{}
	v.ID, off, err = takeU32(raw, off)
	if err != nil {
		return nil, err
	}
	v.Name, off, err = takeString(raw, off)
	if err != nil {
		return nil, err
	}
	v.Owner, off, err = takeString(raw, off)
	if err != nil {
		return nil, err
	}
	n, off, err := takeU16(raw, off)
	if err != nil {
		return nil, err
	}
	if int(n) > MaxViewColumns {
		return nil, nerr.New(nerr.InvalidFormat, "catalog.DecodeView", "too many view columns")
	}
	for i := 0; i < int(n); i++ {
		var c string
		c, off, err = takeString(raw, off)
		if err != nil {
			return nil, err
		}
		v.Columns = append(v.Columns, c)
	}
	v.Query, off, err = takeString(raw, off)
	if err != nil {
		return nil, err
	}
	if off != len(raw) {
		return nil, nerr.New(nerr.InvalidFormat, "catalog.DecodeView", "trailing view bytes")
	}
	if err := validateView(v); err != nil {
		return nil, nerr.Wrap(nerr.InvalidFormat, "catalog.DecodeView", "invalid view descriptor", err)
	}
	return v, nil
}
