package catalog

import (
	"sync"

	"github.com/bzync/nextsql/internal/clientenc"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/sql/ast"
	"github.com/bzync/nextsql/internal/sql/types"
	"github.com/bzync/nextsql/internal/storage/format"
)

const (
	DefNone uint8 = iota
	DefUUID
	DefNow
	DefLiteral
	DefAI
)

type Default struct {
	Kind    uint8
	Literal types.Value
}

type Column struct {
	Name string
	Type types.Type
	// ClientType is the logical plaintext type for ENCRYPTED CLIENT. Type is
	// STRING because nextsqld stores and returns only an NSCE1 ciphertext.
	// KindInvalid means the column is not client-encrypted.
	ClientType types.Type
	NotNull    bool
	Primary    bool
	Default    Default
}

func (c Column) ClientEncrypted() bool { return c.ClientType.Kind != types.KindInvalid }

func (c Column) LogicalType() types.Type {
	if c.ClientEncrypted() {
		return c.ClientType
	}
	return c.Type
}

type Index struct {
	Name      string
	Unique    bool
	Spatial   bool
	Fulltext  bool
	Vector    bool
	Columns   []int
	Path      []string // JSON path after Columns[0]; empty means a column index
	Meta      format.PageID
	Include   []int      // INCLUDE columns stored in the leaf payload, not the key
	Predicate ast.Expr   // partial-index WHERE; nil indexes every row
	Exprs     []ast.Expr // parallel to Columns; non-nil entries are expression keys
	ExprTypes []types.Type
	// VecQuant is the HNSW traversal-quantisation encoding for a vector index
	// (0 none, types.VecF16, types.VecI8). The graph keeps a compact quantised
	// copy of each vector for search and re-ranks against the column payloads.
	VecQuant uint8
	// VecMethod selects the ANN structure for a Vector index: VecMethodHNSW
	// (graph, default), VecMethodIVF (inverted file / coarse quantiser),
	// VecMethodIVFPQ (inverted file with product-quantised residual codes),
	// or VecMethodSPARSE (inverted index over a SPARSEVECTOR column).
	// IVFLists is the coarse-quantiser cell count; IVFProbes is the default
	// number of cells a search scans (0 = ~10% of IVFLists, at least 1).
	// IVFSubspaces is the product-quantisation subspace count M (IVFPQ only;
	// divides the vector dimension).
	VecMethod    uint8
	IVFLists     uint32
	IVFProbes    uint32
	IVFSubspaces uint32
	// FTAnalyzer / FTVersion are the versioned full-text analyzer stored
	// with a FULLTEXT index (0/0 = simple v1, matching pre-v9 catalogs;
	// english v1 = stem only, english v2 = stem + stop-word dictionary v1,
	// english v3 = v2 + synonym dictionary v1 (query-time OR expansion);
	// french/german/spanish v1 = Snowball stemmer + stop-word dictionary v1).
	// Non-fulltext indexes must leave both zero.
	FTAnalyzer uint8
	FTVersion  uint16
}

const (
	// FTAnalyzerSimple is Unicode-lowercase tokenization with no stemmer
	// and no stop-word list.
	FTAnalyzerSimple uint8 = 0
	// FTAnalyzerEnglish is simple tokenization plus Snowball English (Porter2).
	FTAnalyzerEnglish uint8 = 1
	// FTAnalyzerFrench is Snowball French plus stop-word dictionary v1.
	FTAnalyzerFrench uint8 = 2
	// FTAnalyzerGerman is Snowball German plus stop-word dictionary v1.
	FTAnalyzerGerman uint8 = 3
	// FTAnalyzerSpanish is Snowball Spanish plus stop-word dictionary v1.
	FTAnalyzerSpanish uint8 = 4
	// FTAnalyzerEnglishV1 is English stemming without stop-word filtering.
	FTAnalyzerEnglishV1 uint16 = 1
	// FTAnalyzerEnglishV2 is English stemming plus stop-word dictionary v1.
	FTAnalyzerEnglishV2 uint16 = 2
	// FTAnalyzerEnglishV3 is English v2 plus synonym dictionary v1.
	// CREATE FULLTEXT INDEX … ANALYZER = 'english' writes this revision.
	FTAnalyzerEnglishV3 uint16 = 3
	// FTAnalyzerFrenchV1 is the first shipped French analyzer revision.
	FTAnalyzerFrenchV1 uint16 = 1
	// FTAnalyzerGermanV1 is the first shipped German analyzer revision.
	FTAnalyzerGermanV1 uint16 = 1
	// FTAnalyzerSpanishV1 is the first shipped Spanish analyzer revision.
	FTAnalyzerSpanishV1 uint16 = 1
)

const (
	// VecMethodHNSW is the default navigable-small-world graph vector index.
	VecMethodHNSW uint8 = 0
	// VecMethodIVF is the inverted-file (coarse-quantiser) vector index.
	VecMethodIVF uint8 = 1
	// VecMethodIVFPQ is the inverted-file index with product-quantised residual
	// codes (IVF-PQ).
	VecMethodIVFPQ uint8 = 2
	// VecMethodSPARSE is the inverted-index over a SPARSEVECTOR column.
	VecMethodSPARSE uint8 = 3
	// MaxVectorIndexLists bounds USING IVF WITH (LISTS = n) (abuse limit;
	// matches vector.MaxIVFLists).
	MaxVectorIndexLists = 1 << 16
	// MaxVectorIndexSubspaces bounds USING IVFPQ WITH (SUBSPACES = M) (abuse
	// limit; matches vector.MaxIVFPQSubspaces).
	MaxVectorIndexSubspaces = 128
)

type Table struct {
	ID           uint32
	Name         string
	Columns      []Column
	Indexes      []Index
	PK           []int
	HeapMeta     format.PageID
	VecMeta      format.PageID // detached vector store; 0 if the table has no VECTOR columns
	ForeignKeys  []ForeignKey
	CDCImages    CDCImageMode
	Partitioning *Partitioning
}

// PartitionKind identifies the native physical routing rule stored in the
// table descriptor. PartitionNone is the zero value for an unpartitioned
// table. The catalog format reserves all routing details before any SQL
// surface is enabled so recovery never has to infer physical ownership.
type PartitionKind uint8

const (
	PartitionNone PartitionKind = iota
	PartitionRange
	PartitionHash
	PartitionList
	// PartitionLegacyTenant is decoder/runtime compatibility for databases
	// created before shared row tenancy was removed. SQL cannot create it.
	PartitionLegacyTenant
)

const (
	MaxPartitions          = 1024
	MaxPartitionColumns    = 8
	MaxPartitionValues     = 4096
	MaxPartitionNameLength = 63
)

// Partitioning is the durable routing descriptor for one table. Columns are
// catalog ordinals. Physical trees remain detached and are named here by
// authenticated page metadata; the catalog tree supplies WAL, backup, PITR,
// and Raft durability for the descriptor itself.
type Partitioning struct {
	Kind       PartitionKind
	NextID     uint32 // durable high-water allocator; partition identities are never reused
	Columns    []int
	Partitions []Partition
}

// Partition is one physical member of a partitioned table. Values are typed
// tuples over Partitioning.Columns. RANGE stores exactly two tuples (nil is an
// unbounded edge), LIST stores its admitted tuples, HASH stores modulus and
// remainder, and TENANT stores one tenant tuple. Indexes maps logical index
// names to partition-local physical trees.
type Partition struct {
	ID             uint32
	Name           string
	HeapMeta       format.PageID
	VecMeta        format.PageID
	LowerInclusive bool
	UpperInclusive bool
	Modulus        uint32
	Remainder      uint32
	Values         [][]types.Value
	Indexes        []PartitionIndex
}

type PartitionIndex struct {
	Name string
	Meta format.PageID
}

// CDCImageMode controls opt-in logical WAL row images. The zero value is the
// low-amplification key-only policy used by existing tables.
type CDCImageMode uint8

const (
	CDCImagesKeys CDCImageMode = iota
	CDCImagesFull
)

// DDL caps for FOREIGN KEY clauses.
const (
	MaxForeignKeysPerTable = 16
	MaxFKColumns           = 8
	MaxIncludeColumns      = 16
	maxFKNameLen           = 63
)

// IntsEqual reports whether a and b have the same length and values in order.
func IntsEqual(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// FKAction is a referential action. NO ACTION is stored as FKRestrict.
type FKAction uint8

const (
	FKRestrict FKAction = iota // also NO ACTION
	FKCascade
	FKSetNull
	FKSetDefault
)

type ForeignKey struct {
	Name       string
	Columns    []int // child ordinals
	RefTable   string
	RefTableID uint32
	RefColumns []int // parent ordinals (PK or unique index)
	OnDelete   FKAction
	OnUpdate   FKAction
	refNames   []string // referenced column names until ValidateForeignKeys
}

// LegacyTenantColumn marks a table that used the removed shared-tenancy model.
// It is retained only so upgraded databases can fail closed during migration.
const LegacyTenantColumn = "tenant_id"

// LegacyTenantCol returns the removed shared-tenancy marker column when its
// historical type is UUID, STRING, or TEXT.
func (t *Table) LegacyTenantCol() (int, bool) {
	if t == nil {
		return -1, false
	}
	i, ok := t.ColIndex(LegacyTenantColumn)
	if !ok {
		return -1, false
	}
	switch t.Columns[i].Type.Kind {
	case types.KindUUID, types.KindString, types.KindText:
		return i, true
	default:
		return -1, false
	}
}

func (t *Table) ColIndex(name string) (int, bool) {
	for i, c := range t.Columns {
		if c.Name == name {
			return i, true
		}
	}
	return -1, false
}

func (t *Table) Types() []types.Type {
	out := make([]types.Type, len(t.Columns))
	for i, c := range t.Columns {
		out[i] = c.Type
	}
	return out
}

func (t *Table) PKValues(row []types.Value) []types.Value {
	out := make([]types.Value, len(t.PK))
	for i, ord := range t.PK {
		out[i] = row[ord]
	}
	return out
}

func (t *Table) IndexValues(idx Index, row []types.Value) []types.Value {
	out := make([]types.Value, 0, len(idx.Columns)+len(t.PK)+len(idx.Include))
	for i, ord := range idx.Columns {
		if idx.KeyIsExpr(i) {
			out = append(out, types.Null(idx.KeyType(t, i)))
			continue
		}
		v := row[ord]
		if i == 0 && len(idx.Path) > 0 && !v.Null {
			extracted, err := types.ExtractJSON(v.JSON, idx.Path)
			if err != nil {
				out = append(out, types.Null(types.JSON()))
			} else {
				out = append(out, extracted)
			}
		} else {
			out = append(out, v)
		}
	}
	out = append(out, t.PKValues(row)...)
	for _, ord := range idx.Include {
		if ord >= 0 && ord < len(row) {
			out = append(out, row[ord])
		}
	}
	return out
}

func (idx Index) HasExpr() bool {
	for _, e := range idx.Exprs {
		if e != nil {
			return true
		}
	}
	return false
}

func (idx Index) KeyIsExpr(i int) bool {
	return i >= 0 && i < len(idx.Exprs) && idx.Exprs[i] != nil
}

func (idx Index) KeyType(tab *Table, i int) types.Type {
	if idx.KeyIsExpr(i) && i < len(idx.ExprTypes) && idx.ExprTypes[i].Kind != 0 {
		return idx.ExprTypes[i]
	}
	if tab != nil && i >= 0 && i < len(idx.Columns) {
		ord := idx.Columns[i]
		if ord >= 0 && ord < len(tab.Columns) {
			if i == 0 && len(idx.Path) > 0 {
				return types.JSON()
			}
			return tab.Columns[ord].Type
		}
	}
	return types.Type{}
}

func (idx Index) Covers(needed []int, tab *Table) bool {
	if tab == nil {
		return false
	}
	if needed == nil {
		needed = make([]int, len(tab.Columns))
		for i := range tab.Columns {
			needed[i] = i
		}
	}
	if len(needed) == 0 {
		return true
	}
	have := make(map[int]struct{}, len(tab.PK)+len(idx.Columns)+len(idx.Include))
	for _, ord := range tab.PK {
		have[ord] = struct{}{}
	}
	for _, ord := range idx.Include {
		have[ord] = struct{}{}
	}
	if !idx.Spatial && !idx.Fulltext && !idx.Vector {
		for i, ord := range idx.Columns {
			if idx.KeyIsExpr(i) {
				continue
			}
			if i == 0 && len(idx.Path) > 0 {
				continue
			}
			have[ord] = struct{}{}
		}
	}
	for _, ord := range needed {
		if _, ok := have[ord]; !ok {
			return false
		}
	}
	return true
}

func (idx Index) UsesColumn(ord int, tab *Table) bool {
	for _, c := range idx.Columns {
		if c == ord {
			return true
		}
	}
	for _, c := range idx.Include {
		if c == ord {
			return true
		}
	}
	if tab == nil || ord < 0 || ord >= len(tab.Columns) {
		return false
	}
	name := tab.Columns[ord].Name
	if ExprUsesIdent(idx.Predicate, name) {
		return true
	}
	for _, e := range idx.Exprs {
		if ExprUsesIdent(e, name) {
			return true
		}
	}
	return false
}

func (idx Index) RenameColumn(old, neu string) Index {
	if old == "" || old == neu {
		return idx
	}
	idx.Predicate = RewriteIdent(idx.Predicate, old, neu)
	if len(idx.Exprs) > 0 {
		exprs := make([]ast.Expr, len(idx.Exprs))
		for i, e := range idx.Exprs {
			exprs[i] = RewriteIdent(e, old, neu)
		}
		idx.Exprs = exprs
	}
	return idx
}

func (t *Table) Clone() *Table {
	if t == nil {
		return nil
	}
	c := *t
	c.Columns = append([]Column(nil), t.Columns...)
	c.Indexes = append([]Index(nil), t.Indexes...)
	for i := range c.Indexes {
		c.Indexes[i].Columns = append([]int(nil), t.Indexes[i].Columns...)
		if len(t.Indexes[i].Path) > 0 {
			c.Indexes[i].Path = append([]string(nil), t.Indexes[i].Path...)
		}
		if len(t.Indexes[i].Include) > 0 {
			c.Indexes[i].Include = append([]int(nil), t.Indexes[i].Include...)
		}
		if len(t.Indexes[i].Exprs) > 0 {
			c.Indexes[i].Exprs = append([]ast.Expr(nil), t.Indexes[i].Exprs...)
		}
		if len(t.Indexes[i].ExprTypes) > 0 {
			c.Indexes[i].ExprTypes = append([]types.Type(nil), t.Indexes[i].ExprTypes...)
		}
	}
	c.PK = append([]int(nil), t.PK...)
	if len(t.ForeignKeys) > 0 {
		c.ForeignKeys = append([]ForeignKey(nil), t.ForeignKeys...)
		for i := range c.ForeignKeys {
			c.ForeignKeys[i].Columns = append([]int(nil), t.ForeignKeys[i].Columns...)
			c.ForeignKeys[i].RefColumns = append([]int(nil), t.ForeignKeys[i].RefColumns...)
			if len(t.ForeignKeys[i].refNames) > 0 {
				c.ForeignKeys[i].refNames = append([]string(nil), t.ForeignKeys[i].refNames...)
			}
		}
	}
	if t.Partitioning != nil {
		p := *t.Partitioning
		p.Columns = append([]int(nil), t.Partitioning.Columns...)
		p.Partitions = append([]Partition(nil), t.Partitioning.Partitions...)
		for i := range p.Partitions {
			p.Partitions[i].Indexes = append([]PartitionIndex(nil), t.Partitioning.Partitions[i].Indexes...)
			if len(t.Partitioning.Partitions[i].Values) > 0 {
				p.Partitions[i].Values = make([][]types.Value, len(t.Partitioning.Partitions[i].Values))
				for j := range p.Partitions[i].Values {
					if t.Partitioning.Partitions[i].Values[j] == nil {
						continue
					}
					p.Partitions[i].Values[j] = make([]types.Value, len(t.Partitioning.Partitions[i].Values[j]))
					for k := range p.Partitions[i].Values[j] {
						p.Partitions[i].Values[j][k] = t.Partitioning.Partitions[i].Values[j][k].Clone()
					}
				}
			}
		}
		c.Partitioning = &p
	}
	return &c
}

// Store is the in-memory catalog cache. Durable rows live in the primary tree.
type Store struct {
	mu     sync.RWMutex
	tables map[string]*Table
	stats  map[string]*TableStats
	nextID uint32
	gen    uint64
}

func New() *Store {
	return &Store{tables: make(map[string]*Table), stats: make(map[string]*TableStats), nextID: 1, gen: 1}
}

func (s *Store) PeekNext() uint32 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.nextID
}

func (s *Store) NextID() uint32 {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := s.nextID
	s.nextID++
	return id
}

func (s *Store) SetNextID(id uint32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id > s.nextID {
		s.nextID = id
	}
}

func (s *Store) Get(name string) (*Table, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.tables[name]
	if !ok {
		return nil, false
	}
	return t.Clone(), true
}

func (s *Store) Put(t *Table) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tables[t.Name] = t.Clone()
	if t.ID >= s.nextID {
		s.nextID = t.ID + 1
	}
	s.gen++
}

func (s *Store) Remove(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.tables, name)
	delete(s.stats, name)
	s.gen++
}

func (s *Store) List() []*Table {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Table, 0, len(s.tables))
	for _, t := range s.tables {
		out = append(out, t.Clone())
	}
	return out
}

func (s *Store) Replace(tables []*Table) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tables = make(map[string]*Table, len(tables))
	s.stats = make(map[string]*TableStats)
	s.nextID = 1
	s.gen++
	for _, t := range tables {
		s.tables[t.Name] = t.Clone()
		if t.ID >= s.nextID {
			s.nextID = t.ID + 1
		}
	}
}

func (s *Store) Generation() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.gen
}

func (s *Store) Stats(name string) (*TableStats, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	st, ok := s.stats[name]
	if !ok {
		return nil, false
	}
	return st.Clone(), true
}

func (s *Store) SetStats(st *TableStats) {
	if st == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stats == nil {
		s.stats = make(map[string]*TableStats)
	}
	s.stats[st.Table] = st.Clone()
	s.gen++
}

func (s *Store) AllStats() []*TableStats {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*TableStats, 0, len(s.stats))
	for _, st := range s.stats {
		out = append(out, st.Clone())
	}
	return out
}

func TableFromAST(id uint32, stmt ast.CreateTable) (*Table, error) {
	if stmt.Name == "" {
		return nil, nerr.New(nerr.InvalidArgument, "catalog.TableFromAST", "empty table name")
	}
	if ReservedName(stmt.Name) && !IsHistoryTable(stmt.Name) {
		return nil, nerr.New(nerr.InvalidArgument, "catalog.TableFromAST", "table name prefix nsql_ is reserved")
	}
	if len(stmt.Columns) == 0 {
		return nil, nerr.New(nerr.InvalidArgument, "catalog.TableFromAST", "table has no columns")
	}
	t := &Table{ID: id, Name: stmt.Name, Columns: make([]Column, 0, len(stmt.Columns))}
	seen := make(map[string]int, len(stmt.Columns))
	for i, c := range stmt.Columns {
		if c.Name == "" {
			return nil, nerr.New(nerr.InvalidArgument, "catalog.TableFromAST", "empty column name")
		}
		if _, ok := seen[c.Name]; ok {
			return nil, nerr.New(nerr.AlreadyExists, "catalog.TableFromAST", "duplicate column")
		}
		seen[c.Name] = i
		col, err := ColumnFromAST(c)
		if err != nil {
			return nil, err
		}
		t.Columns = append(t.Columns, col)
	}
	if len(stmt.PK) == 0 {
		return nil, nerr.New(nerr.InvalidArgument, "catalog.TableFromAST", "PRIMARY KEY is required")
	}
	for _, name := range stmt.PK {
		i, ok := seen[name]
		if !ok {
			return nil, nerr.New(nerr.NotFound, "catalog.TableFromAST", "PRIMARY KEY column missing")
		}
		t.PK = append(t.PK, i)
		if t.Columns[i].ClientEncrypted() {
			return nil, nerr.New(nerr.InvalidArgument, "catalog.TableFromAST", "ENCRYPTED CLIENT column cannot be a primary key")
		}
		t.Columns[i].Primary = true
		t.Columns[i].NotNull = true
		if t.Columns[i].Type.Kind == types.KindVector {
			return nil, nerr.New(nerr.InvalidArgument, "catalog.TableFromAST", "PRIMARY KEY cannot be VECTOR")
		}
	}
	if err := attachForeignKeys(t, stmt); err != nil {
		return nil, err
	}
	return t, nil
}

func (t *Table) HasVector() bool {
	if t == nil {
		return false
	}
	for _, c := range t.Columns {
		if c.Type.Kind == types.KindVector {
			return true
		}
	}
	return false
}

// ColumnFromAST builds a catalog column from a parsed definition.
func ColumnFromAST(c ast.ColumnDef) (Column, error) {
	if c.Name == "" {
		return Column{}, nerr.New(nerr.InvalidArgument, "catalog.ColumnFromAST", "empty column name")
	}
	def, err := defaultFromAST(c)
	if err != nil {
		return Column{}, err
	}
	col := Column{
		Name:    c.Name,
		Type:    c.Type,
		NotNull: c.NotNull || c.Primary,
		Primary: c.Primary,
		Default: def,
	}
	if !c.EncryptedClient {
		return col, nil
	}
	if !clientenc.SupportedType(c.Type) {
		return Column{}, nerr.New(nerr.InvalidArgument, "catalog.ColumnFromAST", "ENCRYPTED CLIENT supports scalar UUID, STRING, TEXT, BLOB, INT8, INT16, INT32, INT64, DECIMAL, TIMESTAMPTZ, JSON, and BOOL values")
	}
	if c.Primary {
		return Column{}, nerr.New(nerr.InvalidArgument, "catalog.ColumnFromAST", "ENCRYPTED CLIENT column cannot be a primary key")
	}
	if c.Default != nil {
		return Column{}, nerr.New(nerr.InvalidArgument, "catalog.ColumnFromAST", "ENCRYPTED CLIENT column cannot have a server-side default")
	}
	if c.References != nil {
		return Column{}, nerr.New(nerr.InvalidArgument, "catalog.ColumnFromAST", "ENCRYPTED CLIENT column cannot have a foreign key")
	}
	col.ClientType = c.Type
	col.Type = types.String()
	return col, nil
}

func defaultFromAST(c ast.ColumnDef) (Default, error) {
	if c.Default == nil {
		return Default{Kind: DefNone}, nil
	}
	switch e := c.Default.(type) {
	case ast.Call:
		switch e.Name {
		case "uuid":
			if c.Type.Kind != types.KindUUID {
				return Default{}, nerr.New(nerr.InvalidArgument, "catalog.defaultFromAST", "UUID() default requires UUID")
			}
			if len(e.Args) != 0 {
				return Default{}, nerr.New(nerr.InvalidArgument, "catalog.defaultFromAST", "UUID() takes no arguments")
			}
			return Default{Kind: DefUUID}, nil
		case "now":
			if c.Type.Kind != types.KindTimestampTZ {
				return Default{}, nerr.New(nerr.InvalidArgument, "catalog.defaultFromAST", "NOW() default requires TIMESTAMPTZ")
			}
			if len(e.Args) != 0 {
				return Default{}, nerr.New(nerr.InvalidArgument, "catalog.defaultFromAST", "NOW() takes no arguments")
			}
			return Default{Kind: DefNow}, nil
		case "ai":
			if c.Type.Kind != types.KindDecimal || c.Type.Scale != 0 {
				return Default{}, nerr.New(nerr.InvalidArgument, "catalog.defaultFromAST", "AI() default requires DECIMAL(p,0)")
			}
			if len(e.Args) != 0 {
				return Default{}, nerr.New(nerr.InvalidArgument, "catalog.defaultFromAST", "AI() takes no arguments")
			}
			return Default{Kind: DefAI}, nil
		default:
			return Default{}, nerr.New(nerr.InvalidArgument, "catalog.defaultFromAST", "unsupported default function")
		}
	case ast.Literal:
		v, err := types.Coerce(e.Value, c.Type)
		if err != nil {
			return Default{}, err
		}
		return Default{Kind: DefLiteral, Literal: v}, nil
	default:
		return Default{}, nerr.New(nerr.InvalidArgument, "catalog.defaultFromAST", "unsupported default")
	}
}

func (t *Table) ApplyDefault(i int, v types.Value) (types.Value, error) {
	if !v.Null || t.Columns[i].Default.Kind == DefNone {
		return v, nil
	}
	switch t.Columns[i].Default.Kind {
	case DefUUID:
		return types.NewUUID()
	case DefNow:
		return types.Now(), nil
	case DefLiteral:
		return t.Columns[i].Default.Literal, nil
	case DefAI:
		return v, nerr.New(nerr.InvalidArgument, "catalog.ApplyDefault", "AI() requires the executor")
	default:
		return v, nil
	}
}
