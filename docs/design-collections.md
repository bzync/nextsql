# Collections — design & decomposition

> Status: **C1 `STRUCT`, C2 `ARRAY`, C3 `MAP` — all LANDED 2026-09-04**
> (`TODO.md` log #107). Core engine (recursive `types.Type`, nested `NSRW`
> row + sortable-key encoding, `NSCT` v11→v12 catalog descriptor, recursive
> wire-protocol descriptor, lexer/parser/binder/executor, vectorized batch,
> `xport`, FK exclusion), all 7 official drivers, decoder fuzzing, and live
> verification through a real `nextsqld` all complete. Split out of the
> Datatype expansion track's D9 item (`docs/design-datatypes.md`) on
> 2026-09-04 at the user's request; implemented end-to-end under the user's
> "implement Collections and Spatial ... must be full". This track gates no
> release phase.
>
> The open questions each C-item's §3 stub used to defer to `AskUserQuestion`
> were resolved in code and recorded in each item's "Landed design record"
> below (the `docs/design-datatypes.md` §2 decision-record shape: on-disk
> layout, index-key ordering, CAST/coercion, `ENCRYPTED CLIENT` eligibility).

## 1. Why this is its own track, not a Datatype-expansion item

Every item in the Datatype expansion track (`docs/design-datatypes.md`) is a
new scalar `Kind` — a leaf value with a fixed or simply-parameterized wire
shape (`u16` ordinal, `int64` nanoseconds, a length-prefixed byte string).
`ARRAY<T>` / `MAP<K,V>` / `STRUCT<...>` are not scalars: they are recursive
type constructors whose element type can itself be any other type,
including another collection. That recursion touches:

- **the type system**: `types.Type` needs a recursive descriptor (an
  `ARRAY<T>` carries a nested `Type` for `T`; a `STRUCT` carries an ordered,
  named list of `(name, Type)` fields; a `MAP<K,V>` carries two nested
  `Type`s) — not a new `Kind` constant alone, the way every D-track item
  managed with at most a `Precision`/`EnumLabels`-style parameter;
- **the row format**: `internal/sql/types/row.go`'s heap-row encoding
  (`NSRW`, per `docs/storage-format.md`) currently encodes one flat scalar
  per column slot; a collection value is itself a nested sequence of
  scalars/collections and needs its own self-describing sub-encoding,
  closer in shape to `internal/json`'s `NSJB` binary format than to any
  existing scalar `encodeScalar` case;
- **index-key ordering**: `docs/design-datatypes.md`'s own decision record
  (§2) requires every orderable type to define a canonical total order for
  sortable index keys. An array or struct needs a defined element-wise
  comparison rule (lexicographic? length-first? undefined/non-orderable?)
  before it can appear in a `PRIMARY KEY`, `ORDER BY`, or index — this is a
  real design question, not implementation detail;
- **the catalog**: a column's declared type needs to persist the full
  recursive descriptor, which is a `NSCT` format concern comparable to
  D11's `EnumLabels` addition, but per-field/per-element rather than a flat
  label list;
- **binder/executor/vectorized batch**: `internal/executor/vector/batch.go`
  currently gives every column one flat `Vector` with per-`Kind` typed
  slices (`Str`, `Int`, `Flt`, ...); a columnar representation for nested
  collections (e.g. an `ARRAY<INT64>` column) is an open question in its
  own right — flatten to a `[][]int64`-shaped slice? Something closer to
  Arrow's list-array layout (offsets + a flat child array)? This decision
  has real performance consequences for vectorized execution and deserves
  its own analysis, not a default.

This is comparable in size to the original JSON (P9) or Vector (P11/P23)
phases — a phase-shaped body of work, not a bullet alongside scalar types.

## 2. Relationship to existing NextSQL features

Two things already in the engine do *some* of what collections would do,
and any design here needs to explain why it isn't just reusing them:

- **`JSON`** (`docs/json.md`, `NSJB` binary format) already stores
  arbitrarily nested arrays/objects with path indexing. A `STRUCT`/`ARRAY`
  design that ends up being "JSON with a declared schema" should say so
  explicitly and explain what a schema buys over path-indexed `JSON` (type
  checking at write time; more compact storage without per-value type
  tags; direct column projection without a path expression) rather than
  quietly duplicating `NSJB`.
- **`VECTOR<F32,N>`** is already a fixed-length homogeneous array of
  floats with its own storage/index machinery (`docs/vector.md`). A generic
  `ARRAY<FLOAT32>` should not become a second, slower way to store what
  `VECTOR` already covers — the design should state where the boundary is
  (e.g. `VECTOR` for fixed-dimension float arrays feeding ANN search;
  `ARRAY<T>` for variable-length or non-float element sequences).

## 2a. Landed design record (all three C-items, 2026-09-04)

Shared decisions, made once and recorded here; per-item specifics follow in §3.

- **Recursive `types.Type`**: new `Fields []Field` (STRUCT), `Elem []Type`
  (ARRAY element / MAP value), `Key []Type` (MAP key) — slices not `*Type`,
  so `Type` stays copyable with no aliasing; `Equals`/`String`/`Comparable`
  recurse. Constructors enforce `MaxNestDepth` = 8, `MaxStructFields` = 128,
  `MaxCollectionLen` = 2²⁰.
- **Heap-row encoding** (`row.go`): self-describing nested form — `u32` body
  length (O(1) `skipScalar` at any depth), `u32` member count, null bitmap,
  then each non-null member via `encodeScalar` recursively (MAP members
  interleaved key,value,…). NSJB-shaped, as §1 predicted.
- **Index-key ordering — all three ARE orderable** (resolving §1's open
  question toward lexicographic tuple order): each member framed with a
  marker byte `0x00` end / `0x01` NULL / `0x02` present, so a shorter
  prefix sorts first and NULLs sort before present values; decode is
  type-directed so payloads need no escaping. `Value.Cmp` matches
  (`cmpMember`).
- **Vectorized batch**: one boxed `[]types.Value` per row (`Vector.Coll`,
  `+ CollKeys` for MAP) — the deliberately simple representation; a nested
  columnar layout is a later optimization, not a correctness need.
- **Catalog**: `NSCT` v11→**v12**, one recursive per-column descriptor
  (`appendTypeRec`/`takeTypeRec`, depth-bounded, re-validated on decode);
  `internal/upgrade/compat` `FamilyCatalog` window → 12.
- **Wire protocol**: recursive `appendTypeFull`/`readTypeBody` after the
  fixed 5-byte meta — **no protocol version bump** (scalar header
  byte-identical; same precedent as ENUM's variable Type metadata).
- **`ENCRYPTED CLIENT`: not eligible** for any collection kind
  (structurally — client columns must be `KindString`); the server must
  inspect structure for indexing/projection/coercion.
- **FK eligibility**: collections cannot be FK columns (`catalog/fk.go`
  block-list, with `VECTOR`/`JSON`).
- **Aggregates**: `MIN`/`MAX` via generic `Value.Cmp`; `SUM`/`AVG` error.
- **Coercion**: isolated except same-Kind collections (member-by-member) and
  text (`Value.String()` → `STRING`/`TEXT`). MAP re-sorts to canonical key
  order + rejects duplicate keys (`CanonicalizeMap`). Implicit `JSON` ⇄
  collection coercion is **deliberately deferred** (clean follow-up: needs
  an `NSJB` ⇄ typed-collection bridge).
- **Grammar**: types `STRUCT<name TYPE, …>` / `ARRAY<TYPE>` /
  `MAP<KEYTYPE, VALTYPE>` (`<…>`, BigQuery-style). Constructors are
  function-call-shaped (no new lexer token): `ARRAY(e1, …)`,
  `STRUCT(e1 AS f1, …)`, `MAP(k1, v1, …)` — server re-coerces member types
  against the destination column. STRUCT field access is `col.field[.field…]`
  (extends the `Path` node; a `Path` whose head column is a `STRUCT` is
  field access, else JSON path extract). Accessors: `ELEMENT_AT` (1-based
  for arrays), `CARDINALITY`/`ARRAY_LENGTH`/`MAP_SIZE`, `ARRAY_CONTAINS`,
  `MAP_CONTAINS_KEY`, `MAP_KEYS`, `MAP_VALUES`.
- **Drivers, all 7**: Go needs no change (shares `internal/protocol`);
  JS/Bun/Deno (shared `protocol.mjs`), Node, PHP, Python, Ruby each gained
  `Kind` 32/33/34, a recursive type-descriptor codec, a param path
  (a plain list → ARRAY, plus `struct(...)` / `MapValue` / `StructValue`
  wrappers), and RowDesc/value decode. Each driver's own suite gained a
  collection round-trip test incl. a NULL member and a nested collection.

## 3. Sequenced plan — all landed 2026-09-04

Per-item specifics; the shared decisions are in §2a. Ordered as originally
planned by value-to-cost ratio, though all three shipped in one increment.

### C1 — `STRUCT<name T, ...>` (fixed, named, heterogeneous fields) — LANDED 2026-09-04
Plausibly the cheapest of the three: a `STRUCT` column has a fixed,
schema-known field list, so (unlike `ARRAY`/`MAP`) its size and layout are
static per declared type, closer in spirit to a nested `CREATE TABLE`
column list than to a variable-length collection. Candidate for the actual
first increment if this track is picked up, but **not decided**.

**Open questions — all RESOLVED in code (see §2a and below):**
- Nested `STRUCT`/`ARRAY`/`MAP` inside a `STRUCT` **allowed**, bounded at
  `MaxNestDepth` = 8.
- A `STRUCT` field is individually nullable (per-field null bit in the
  nested encoding); the whole value is also nullable as a unit. Per-field
  `NOT NULL`/`DEFAULT` constraints are **out of scope** (matches Postgres
  composite types) — deferred, not blocking.
- Construction is explicit: `STRUCT(expr AS name, ...)`. Implicit `JSON`
  object → `STRUCT` coercion is **deferred** (see §2a).
- Index-key ordering: **field-order lexicographic**, orderable (§2a).

### C2 — `ARRAY<T>` (variable-length, homogeneous) — LANDED 2026-09-04
**Open questions — all RESOLVED in code (see §2a and below):**
- `T` may be any storable type including another collection (arbitrary
  nesting up to `MaxNestDepth` = 8). `VECTOR`/`JSON` as an element are
  allowed by the type system (not specially blocked).
- A single engine-wide constant `MaxCollectionLen` = 2²⁰, enforced at
  decode and coerce time (not a per-declaration `ARRAY<T>(n)`).
- Index-key ordering: **lexicographic element-by-element**, orderable (§2a).
- Vectorized-batch representation: **one boxed `[]types.Value` per row**
  (§2a); a nested columnar layout is a later, benchmark-informed
  optimization.

### C3 — `MAP<K,V>` (variable-length, homogeneous key/value pairs) — LANDED 2026-09-04
Likely the most expensive of the three: needs a defined key-uniqueness
rule, a key ordering/comparison rule (for a canonical on-disk
representation — two maps with the same entries in different insertion
order must encode identically or comparison/equality breaks), and
key-type restrictions (can `K` be a `JSON` or `ARRAY`, or only orderable
scalars?).

**Open questions — all RESOLVED in code (see §2a and below):**
- Key type: **orderable scalars only** (`MapKeyComparable` — no
  collection/`JSON`/`VECTOR`/geo keys).
- Canonical key ordering: **sorted by `K`'s own `Value.Cmp` order**
  (`CanonicalizeMap`), so two MAPs with the same entries encode and
  compare identically regardless of construction order.
- Duplicate keys on construction: **rejected** (`CanonicalizeMap` errors).

## 4. Deliberately out of scope (deferred, not blocking)

- **Implicit `JSON` ⇄ collection coercion** — a `JSON` object → `STRUCT`,
  `JSON` array → `ARRAY<T>`, etc. Clean, separable follow-up: needs an
  `NSJB` builder from a typed collection value and the inverse walk. A
  collection → `STRING`/`TEXT` (via `Value.String()`) *is* supported.
- **Per-field `NOT NULL` / `DEFAULT`** on a `STRUCT` field (matches
  Postgres composite types, which don't enforce these on subfields).
- **`ARRAY<T>(n)` per-declaration length bound** — a single engine-wide
  `MaxCollectionLen` is used instead.
- **Nested columnar (Arrow-style) vectorized-batch layout** — the boxed
  `[]types.Value` representation is correct; this is a perf optimization
  gated on a benchmark showing it matters.
- **`[...]` / `[i]` subscript sugar** in the grammar — accessor functions
  (`ELEMENT_AT`, `ARRAY_LENGTH`, …) cover the same ground with no new
  lexer token; subscript sugar could be added later.
- **`ARRAY_AGG` / `MAP_AGG` aggregates**, `UNNEST` in `FROM`, array/map
  `slice`/`concat`/`sort`/`distinct` helpers — a natural next increment.

## 5. Source of truth

Mirrored in `TODO.md` under "Cross-cutting track — Collections." Gates no
phase (P0-P27 closed; P28-P30 next). See `docs/design-datatypes.md` D9 for
the original one-paragraph flag that prompted this split.
