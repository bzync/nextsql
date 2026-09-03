# Datatype expansion — design & decomposition

> Status: **D1 (`BLOB`), D2 (fixed-width signed integers), D3
> (fixed-width unsigned integers), and D5 (`DATE`/`TIME`) landed 2026-09-03**
> (see `TODO.md` log #90, log #91, log #92, and log #94); D4, D6-D10 remain
> planning-only, no implementation started. This document replaces the
> original flat taxonomy sketch with a sequenced, gated plan, following the
> same "smallest coherent increment" discipline used by the Multi-database
> hosting track (`docs/design-multidatabase-dbaas.md`). Mirrored in `TODO.md`
> under "Cross-cutting track — Datatype expansion." This track gates no
> release phase.

## 1. Already shipped (not part of this track)

These exist today in `internal/sql/types/types.go` / `docs/sql.md`. Listed
here only so the taxonomy below isn't mistaken for new work.

| Type | Storage | Notes |
|---|---|---|
| `UUID` | 16 bytes | `DEFAULT UUID()` |
| `STRING` / `TEXT` | `u32` length + UTF-8 | same encoding, two spellings |
| `DECIMAL(p,s)` | unscaled integer + scale, `1<=p<=38` | only exact-numeric type today |
| `TIMESTAMPTZ` | `int64` UTC nanos | `DEFAULT NOW()` |
| `JSON` | binary `NSJB` | path extract/index, see `docs/json.md` |
| `VECTOR<F32\|F16\|I8,N>`, `BITVECTOR<N>`, `SPARSEVECTOR<N>` | see `docs/vector.md` | Vector Engine 2.0, production-gated (P23) |
| `POINT` / `BOX` / `LINESTRING` / `POLYGON` | see `docs/geo.md` | WGS84 fixed shapes |
| `BOOL`, `NULL` | 1 byte / tag | — |

## 2. Decision record

Every new type below must state, before code starts: on-disk layout,
index-key ordering (this is a clustered B+Tree engine — every orderable type
needs a canonical total order, e.g. NaN/-0 handling for floats), CAST/coercion
rules, and `ENCRYPTED CLIENT` eligibility (today an explicit allow-list:
UUID/STRING/TEXT/DECIMAL/TIMESTAMPTZ/JSON/BOOL). Every new type is also 7x
driver surface (Go/JS/Node/Bun/Deno/PHP/Python/Ruby).

## 3. Sequenced plan

Ordered by value-to-cost ratio and dependency, not by the original
taxonomy's grouping.

### D1 — `BLOB` (variable-length raw bytes) — LANDED 2026-09-03
One type (`BLOB`), `u32` length prefix, same shape as `STRING`/`TEXT`'s
encoding minus UTF-8 validation — not a 3-way `BINARY(n)`/`VARBINARY(n)`/
`BLOB` split (nothing distinguishes fixed-width-padded from variable-length
for `STRING` either; revisit only if a concrete padding need surfaces).
Literal syntax `X'<hex>'` (`X''` = empty). Canonical order is plain
byte-lexicographic comparison (same zero-escaped sortable-bytes encoding
`STRING`/`TEXT`/`JSON` already use), so `BLOB` is a valid `PRIMARY KEY`/
`ORDER BY`/`GROUP BY` column. Deliberately isolated from `STRING`/`TEXT`:
`Coerce` either direction requires hex text, never an implicit
byte-for-byte reinterpretation. `ENCRYPTED CLIENT` is supported (the
opaque-ciphertext path is fully generic over scalar encode/decode). No
`JSON` interaction (explicit non-goal — no auto base64/hex coercion).
No `NSCT` catalog version bump (`Kind` is a plain appended byte tag). All
7 official drivers updated; Go needed no code change (shares
`internal/sql/types`/`internal/protocol` directly). See `TODO.md` log #90
for the full file list, test coverage, and live-verification writeup.

### D2 — Fixed-width signed integers (`INT8`/`INT16`/`INT32`/`INT64`) — LANDED 2026-09-03
Exact (not floating), so no rounding-surprise class introduced. **Index-key
ordering**: sign-bit-flip before big-endian unsigned storage (same trick
`TIMESTAMPTZ` already used for its `int64`) — naive two's-complement byte
order would otherwise sort every negative value after every positive one.
**Arithmetic**: `+ - * /` and unary `-` always promote both operands to
`DECIMAL` (arbitrary precision, matching the pre-D2 behavior where
`DECIMAL` was the only arithmetic type) — the operation itself can never
overflow; only an explicit assignment/coercion of the result back into a
fixed-width column re-checks range and errors on overflow (never wraps).
**Coercion**: `Int<->Int` (narrowing range-checks), `Int<->Decimal` (via
`Decimal.Rescale(0,0)`, erroring on any fractional remainder), `Int<->
String/Text` (via decimal-text parse, generically through the existing
`Value.String()`/`Coerce` string path) — deliberately isolated from
`BLOB`/`UUID`/`BOOL`/`JSON`/geo, mirroring D1's isolation precedent.
**Aggregates**: `SUM`/`AVG` reuse the existing DECIMAL-promotion
accumulator (`types.Coerce(v, Type{Kind:KindDecimal})` inside
`aggregate/hash.go`'s `acc()`, unchanged) so accumulation cannot overflow
even summing many near-max values; `MIN`/`MAX` stay in the column's own
int kind via the existing generic `Value.Cmp`. **Literal typing**: bare
integer literals still parse as untyped `DECIMAL` (unchanged) and coerce
into whichever int column/parameter they target — no new literal syntax.
**`ENCRYPTED CLIENT`**: included (same generic opaque-scalar reasoning as
D1). **FK eligibility**: ordinary FK-eligible scalars, unlike `BLOB`/
`VECTOR`/`JSON`. No `NSCT` catalog version bump. All 7 official drivers
updated; Go needed no code change. Found and fixed, while implementing
this increment, a latent float-overflow bug in the PHP driver's 64-bit
decoder (`Protocol::i64`) at/above magnitude 2^63 — see `TODO.md` log #91
for the full file list, test coverage, and live-verification writeup.

### D3 — Fixed-width unsigned integers (`UINT8`/`UINT16`/`UINT32`/`UINT64`) — LANDED 2026-09-03
Mirrors D2's shape exactly, plus one extension: **index-key ordering** is
plain unsigned big-endian bytes, no sign-bit flip needed (unlike D2). Same
**arithmetic-promotes-to-DECIMAL** and **narrowing/negative-assignment
errors rather than wraps** decisions, reused rather than relitigated.
**Coercion extension beyond D2's precedent**: `INT8..64` and `UINT8..64` are
treated as one coercible "exact integer" group — direct `Int<->Uint`
coercion is range/sign checked either way (negative `Int` → `Uint` errors; a
`Uint` magnitude above the signed kind's max errors), rather than isolating
the two families from each other the way D1/D2 isolated `BLOB`/ints from
unrelated families (`BOOL`/`UUID`/`JSON`/geo) — the two integer families are
close enough in kind that forcing a detour through `DECIMAL` would be pure
friction with no safety benefit, since both are already exact. `SUM`/`AVG`
reuse the same DECIMAL-promotion accumulator; `MIN`/`MAX` stay in the
column's own uint kind. Ordinary FK-eligible scalars. `ENCRYPTED CLIENT`
supported (same generic opaque-scalar reasoning as D1/D2). No `NSCT` catalog
version bump. All 7 official drivers updated; Go needed no code change.
PHP's `UINT64` needed its own design note: PHP's native `int` is a signed
64-bit type with no unsigned counterpart, so a value at or above 2^63
decodes as a decimal digit string instead (mirroring how `DECIMAL` is
already represented in that driver) rather than silently going negative;
encode accepts either a non-negative native `int` or such a string. Also
found and fixed, while implementing this increment, a pre-existing gap
(not introduced by D3): the Node driver's `ENCRYPTED CLIENT` (NSCE1) path
had never picked up D2's `INT8..64` support at all — it now supports the
full `INT8..64`/`UINT8..64` set, same as every other driver. See `TODO.md`
log #92 for the full file list, test coverage, and live-verification
writeup.

### D5 — `DATE` / `TIME` — LANDED 2026-09-03
**On-disk layout**: `DATE` is a fixed-width signed `int32` day count since the
Unix epoch (1970-01-01 = 0), 4 bytes; `TIME` is nanoseconds-since-midnight,
range `[0, 86399999999999]` (< 1 day of nanos), stored as 8 bytes. Both reuse
existing `Value` fields rather than adding new ones: `DATE` reuses
`Value.Int` (the same int64 the fixed-width signed integers use, sign-extended
from `int32`); `TIME` reuses `Value.Time` (the same field `TIMESTAMPTZ` uses
for UTC-epoch nanoseconds) — disambiguated by `Value.Typ.Kind` at every read
site, the same way `Value.Int` already serves four integer widths. The Go
constructor for `TIME` is named `TimeOfDay()`/`TimeOfDayValue()`, not
`Time()`/`TimeValue()`, to stay distinct from the pre-existing
`TimeValue`/`Value.Time` (TIMESTAMPTZ's constructor). **Index-key ordering**:
`DATE` sign-bit-flips its 4 bytes before big-endian storage, the same trick
`INT32`/`TIMESTAMPTZ` already use (a day count can be negative for pre-1970
dates); `TIME` stores plain unsigned big-endian bytes with no flip — nanoseconds-
since-midnight is always non-negative, mirroring `UINT64`'s reasoning.
**Coercion**: isolated from every family but text, same as D1-D3's isolation
precedent — `DATE`/`TIME` accept only `STRING`/`TEXT` (ISO 8601:
`YYYY-MM-DD` / `HH:MM:SS[.fraction]`, nanosecond precision, parsed via Go's
`time.Parse`) and format back the same way via `Value.String()`. Deliberately
**no dedicated literal syntax** (no `DATE '...'`/`TIME '...'` prefix, unlike
`BLOB`'s `X'...'`) — a plain quoted string already has an unambiguous textual
representation for a calendar date or time-of-day, so a column/parameter
typed `DATE`/`TIME` just coerces a string literal like `INT`/`UINT` coerce a
decimal-text literal (D2 precedent: "bare literals... no new literal syntax").
**No arithmetic**: `+`/`-`/`*`/`/` over `DATE`/`TIME` operands are rejected
(`isNumericKind` doesn't include either Kind) — calendar arithmetic (months
are not fixed-duration) is D6 `INTERVAL`'s job, explicitly deferred; this
increment is comparison/ordering/CAST only. **Aggregates**: `MIN`/`MAX` work
for free via the existing generic `Value.Cmp` dispatch (no per-kind allowlist
there, same as `BLOB`); `SUM`/`AVG` correctly error (DECIMAL-promotion
`Coerce` has no `DATE`/`TIME` source case). **FK eligibility**: ordinary
FK-eligible scalars (the FK check is a block-list — `VECTOR`/`JSON` — not an
allow-list). **`ENCRYPTED CLIENT`**: included (same generic opaque-scalar
reasoning as D1-D3). No `NSCT` catalog version bump (`Kind` is a plain
appended byte tag, `KindDate`=23, `KindTime`=24). All 7 official drivers
updated; Go needed no code change beyond the shared `internal/sql/types`/
`internal/protocol` packages (same precedent as D1-D3). While implementing,
found and fixed a **pre-existing latent bug**, unrelated to D5: the
vectorized-execution `Batch.Compact`/`clonePrefix` paths
(`internal/executor/vector/batch.go`) were missing `INT8..64`/`UINT8..64`
cases entirely (present since D2/D3), silently leaving stale/zero values in
those columns after a vectorized filter or `Project`. Fixed alongside adding
the new `DATE`/`TIME` cases in the same switches. See `TODO.md` log #94 for
the full file list, test coverage, and live-verification writeup.

### D4 — `CHAR(n)` / `VARCHAR(n)`
**Blocked on a semantics decision, not effort**: does `CHAR(n)` mean
fixed-width space-padded storage/comparison (SQL-standard/Postgres
behavior), or just a length ceiling on the existing `STRING` encoding? If
the latter, `VARCHAR(n)` is `STRING` plus a length check and `CHAR(n)` may
not be worth a distinct type at all. Needs an explicit decision (recommend
`AskUserQuestion`) before any code.

### D5 — `DATE` / `TIME`
Ready to scope once D1-D3 conventions are settled. Both have a trivial
total order (day count / nanos-since-midnight), so lower risk than D6/D7.

### D6 — `INTERVAL`
**Blocked on its own design writeup**, not just storage: calendar
arithmetic (months are not fixed-duration) must be specified against
`DATE`/`TIMESTAMP` before an encoding is chosen. Do not start alongside D5.

### D7 — Plain `TIMESTAMP` (no timezone)
**Blocked on a product decision**: `TIMESTAMPTZ` already exists and covers
the common case. The doc's original inclusion of naive local time wasn't
justified — confirm there's a real use case (e.g. import compatibility)
before adding a second temporal type that differs only in tz handling.

### D8 — `FLOAT32` / `FLOAT64`
**Blocked on explicit approval.** Today `DECIMAL` is the only numeric type
and is exact by design — no float-rounding bug class exists in NextSQL
today. Adding IEEE-754 floats reopens that class deliberately. Needs
`AskUserQuestion` (or equivalent explicit sign-off) with a stated reason
(e.g. interop with external numeric data) before scoping starts, and if
approved, an explicit NaN/-0/ordering-canonicalization spec for index keys.

### D9 — Collections: `ARRAY<T>` / `MAP<K,V>` / `STRUCT<...>`
**Too large for this track.** Recursive type descriptors, per-element
encoding, index-key ordering for composites, and row-format (`NSRW`)
changes make this comparable in size to the original JSON (P9) or Vector
(P11/P23) phases, not a bullet alongside scalar types. Action for this
track: split into a dedicated `docs/design-collections.md` and its own
TODO.md track once someone is ready to scope it — no scalar-type work
here should block on it, and it should not be started opportunistically
alongside D1-D8.

### D10 — Spatial: `GEOMETRY` / `GEOGRAPHY`
**Blocked on a scoping decision**: does this mean generalizing the 4
existing WGS84 shapes with SRID support, or building a second, more
general PostGIS-style subsystem alongside the first? These are very
different sizes of work. Needs `AskUserQuestion` before any TODO.md item
is created.

### D11 — `ENUM(label, ...)`
Was missing from the original taxonomy entirely — added 2026-09-03.
**Blocked on a scoping decision, not effort**: unlike every other item so
far, this needs new catalog-level metadata (an ordered, named label list
per column), not just a new `Kind` tag — so it needs an explicit decision
on whether the catalog format (`NSCT`, currently v10) takes a version bump,
and how label lifecycle works (`ALTER TABLE ... ADD VALUE`, rename,
remove-with-existing-data-referencing-it) before implementation starts.
Design intent to record once scoped: storage as a fixed-width integer index
into the column's declared label list (mirrors Postgres/MySQL); order is
**declaration order, not lexicographic** — the entire point of `ENUM` is
intentional non-alphabetic ordering (e.g. `small < medium < large`); `CAST`
to/from `STRING`/`TEXT` validates label-set membership; FK-eligible;
`ENCRYPTED CLIENT` not a stated need (declared labels aren't typically
sensitive) — leave out unless a use case appears. Needs `AskUserQuestion` on
the catalog-format question before scoping starts.

## 4. Explicitly cut from the original taxonomy

Not carried forward as engine column types. Reasoning below; revisit only
if a concrete, stated use case appears (not "completeness").

- **Identity/Network** (`INET`, `CIDR`, `MACADDR`): niche, no stated use
  case. `VARBINARY`/`STRING` plus an app-level `CHECK`-style validator
  covers this without new engine types.
- **Structured Documents beyond JSON** (`YAML`, `XML`, `TOML`, `INI`,
  `ENV`): JSON is a first-class type because of a real binary format
  (`NSJB`) with path indexing behind it. These would each need the same
  investment repeated five times, or else they're just `TEXT` with a
  naming convention — in which case they add nothing as engine types.
  Store as `TEXT`/`BLOB`; a format label is an application concern.
- **DevOps/Infrastructure** (`DOCKERFILE`, `COMPOSE`, `K8S_MANIFEST`,
  `HCL`), **Development Content** (`CODE`, `MARKDOWN`), **Generic Files**
  (`FILE`): not database types by any definition used elsewhere in this
  doc — no type-specific engine behavior (validation, indexing) was ever
  specified for them. Conflicts with `PROJECT.md`'s explicit "not a
  compatibility/generic storage layer" identity. If a real generic-file
  need surfaces later, it's a single `FILE` type with an optional
  MIME/format tag — a separate, future, explicitly-scoped item, not seven
  new types here.

## 5. What this track does NOT include

Per `PROJECT.md`/`TODO.md` convention, this track gates no phase (P0-P27
are closed; P28-P30 are next). Nothing here should be started opportunistically
inside other phase/track work — each `D`-item gets its own increment log
entry when picked up, same as M2's sub-items.
