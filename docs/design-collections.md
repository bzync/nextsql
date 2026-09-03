# Collections — design & decomposition

> Status: **scoped, not implemented.** Split out of the Datatype expansion
> track's D9 item (`docs/design-datatypes.md`) on 2026-09-04 at the user's
> request, following the same "smallest coherent increment" discipline as
> that track and Multi-database hosting (`docs/design-multidatabase-dbaas.md`).
> This document identifies the open questions and a sequenced plan; it does
> not make the implementation-level decisions itself — several explicitly
> need `AskUserQuestion` before any code, called out inline. Mirrored in
> `TODO.md` under "Cross-cutting track — Collections." This track gates no
> release phase and should not be started opportunistically inside other
> phase/track work.

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

## 3. Sequenced plan (draft — needs scoping, not yet decided)

Following the Datatype-expansion track's convention of ordering by
value-to-cost ratio and dependency:

### C1 — `STRUCT<field: T, ...>` (fixed, named, heterogeneous fields)
Plausibly the cheapest of the three: a `STRUCT` column has a fixed,
schema-known field list, so (unlike `ARRAY`/`MAP`) its size and layout are
static per declared type, closer in spirit to a nested `CREATE TABLE`
column list than to a variable-length collection. Candidate for the actual
first increment if this track is picked up, but **not decided**.

**Open questions needing `AskUserQuestion` before any code:**
- Are nested `STRUCT`s inside a `STRUCT` allowed (recursion depth), and is
  there a bound?
- Does a `STRUCT` field support `NOT NULL`/`DEFAULT` the way a top-level
  column does, or is the whole `STRUCT` value nullable only as a unit?
- `CAST`/coercion: does a `JSON` object with matching keys implicitly
  coerce to a `STRUCT`, or is construction explicit-only
  (`STRUCT(field: expr, ...)` syntax)?
- Index-key ordering: field-order lexicographic comparison, or
  non-orderable (rejected in `PRIMARY KEY`/`ORDER BY`/index contexts)?

### C2 — `ARRAY<T>` (variable-length, homogeneous)
**Open questions needing `AskUserQuestion` before any code:**
- Element type restrictions: can `T` be `VECTOR`/`JSON`/another `ARRAY`
  (arbitrary nesting), or is `T` restricted to non-collection scalars for
  the first increment (deferring nested arrays to a later item, the way
  D1-D11 deferred collections entirely)?
- A max length bound (mirroring `MaxEnumLabels`'s 4096, `MaxVectorDim`,
  etc.) — resource-safety requires *some* bound per `AGENTS.md` §10.
  What's the right ceiling for an array column, and should it be
  per-declaration (`ARRAY<T>(max_len)`) or a single engine-wide constant?
- Index-key ordering: lexicographic element-by-element (like a tuple), or
  non-orderable?
- Vectorized-batch representation (§1 above) — offsets + flat child array,
  vs. one Go slice-of-slices per row, vs. something else; this has real
  performance implications and deserves a benchmark-informed decision, not
  a default picked for expedience.

### C3 — `MAP<K,V>` (variable-length, homogeneous key/value pairs)
Likely the most expensive of the three: needs a defined key-uniqueness
rule, a key ordering/comparison rule (for a canonical on-disk
representation — two maps with the same entries in different insertion
order must encode identically or comparison/equality breaks), and
key-type restrictions (can `K` be a `JSON` or `ARRAY`, or only orderable
scalars?).

**Open questions needing `AskUserQuestion` before any code:**
- Key type restriction (orderable scalars only, most likely, but this is a
  decision, not an assumption).
- Canonical key ordering for on-disk encoding (sorted by `K`'s own
  canonical order, most likely, for deterministic equality/comparison —
  but explicit sign-off needed, matching every other D-track ordering
  decision).
- Duplicate-key behavior on construction: last-write-wins, or reject?

## 4. What this document deliberately does not do

Per the user's 2026-09-04 direction (split D9 into its own track, scope
only, do not implement): this document identifies the shape of the problem
and the specific decisions blocking each sub-item. It does not:

- pick a storage format for any of C1/C2/C3 (each needs the
  `docs/design-datatypes.md` §2-style decision record — on-disk layout,
  index-key ordering, `CAST`/coercion rules, `ENCRYPTED CLIENT`
  eligibility — filled in only after the open questions above are
  answered);
- write any code, `Kind` constant, or catalog format change;
- commit this track to a specific next increment (C1 `STRUCT` is the
  *candidate* smallest first step, not a decision).

## 5. Source of truth

Mirrored in `TODO.md` under "Cross-cutting track — Collections." Gates no
phase (P0-P27 closed; P28-P30 next). See `docs/design-datatypes.md` D9 for
the original one-paragraph flag that prompted this split.
