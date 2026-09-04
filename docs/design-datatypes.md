# Datatype expansion — design & decomposition

> Status: **D1-D5, D7, D8, and D11 — every scalar D-item except D6 — are all
> LANDED** — core engine, all 7 official drivers, docs, and live
> verification complete for all eight (`TODO.md` logs #90-#99; see each
> item's own section below for its specific log references). **D6
> (`INTERVAL`) landed 2026-09-04** (`TODO.md` log #100) — Postgres-style
> 3-field storage (months/days/nanos), a new `INTERVAL 'text'` literal
> grammar production (the one D-item that needed dedicated literal syntax,
> unlike D1-D5/D7/D8/D11's plain-string precedent — arithmetic dispatch
> needs the operand's actual wire `Kind`), full `DATE`/`TIME`/`TIMESTAMP`/
> `TIMESTAMPTZ` arithmetic with Postgres's day-of-month clamping, and all 7
> drivers — plus three real, previously-undetected bugs found and fixed
> along the way (a completely missing `TIMESTAMPTZ` string-literal coercion
> path predating this entire track, a `FormatInterval`/`ParseInterval`
> round-trip mismatch, and a Node-driver negative-`BigInt` encoding bug
> that silently affected pre-1970 timestamps too). D4/D7/D8 were found
> core-engine-complete but completely undocumented and driver-absent on
> 2026-09-03 (log #96); D5 was *documented* as driver-complete on
> 2026-09-03 but that claim was false (log #94 never actually checked the
> other 6 drivers — see D5's own section for the correction); all four
> plus D11's remaining driver gap closed together on 2026-09-04 (log #99),
> after the user's explicit "must all datatypes must be supported end to
> end" — D6 followed immediately after in the same session (log #100).
> D4/D11 are deliberately **not** `ENCRYPTED CLIENT`-eligible. **D9
> (collections, `docs/design-collections.md`) and D10 (spatial,
> `docs/design-spatial.md`) both split into their own cross-cutting tracks
> once every scalar D-item landed, and both are now fully LANDED too**
> (D9: `TODO.md` log #109; D10: log #113) — every D-track item, scalar and
> non-scalar alike, is complete as of 2026-09-04. This document replaces the original flat taxonomy
> sketch with a sequenced, gated plan, following the same "smallest coherent increment"
> discipline used by the Multi-database hosting track
> (`docs/design-multidatabase-dbaas.md`). Mirrored in `TODO.md` under
> "Cross-cutting track — Datatype expansion." This track gates no release
> phase.

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
appended byte tag, `KindDate`=23, `KindTime`=24).

**Driver claim correction (2026-09-04, `TODO.md` log #99)**: this entry
originally said "All 7 official drivers updated" on 2026-09-03 — that was
**false**. Grep-confirmed the same day D4/D7/D8's driver gap was found
(log #96): zero of `Kind.Date`/`Kind.Time`/`KIND_DATE`/`KIND_TIME` existed
in any of the 6 non-Go drivers. Only the Go core-engine work (this section)
and live verification via the `nextsql` CLI (which shares `internal/protocol`
directly, same as a Go-driver client) had actually happened; the
"all 7 drivers" line was written without checking the other 6. All 7
drivers actually landed 2026-09-04 alongside D4/D7/D8's own driver work —
see D4/D7/D8's "Drivers, all 7" sections below, which cover DATE/TIME too
(implemented in the same pass, same files, same commits). While implementing D5 originally, also
found and fixed a **pre-existing latent bug**, unrelated to D5: the
vectorized-execution `Batch.Compact`/`clonePrefix` paths
(`internal/executor/vector/batch.go`) were missing `INT8..64`/`UINT8..64`
cases entirely (present since D2/D3), silently leaving stale/zero values in
those columns after a vectorized filter or `Project`. Fixed alongside adding
the new `DATE`/`TIME` cases in the same switches. See `TODO.md` log #94 for
the full file list, test coverage, and live-verification writeup.

### D4 — `CHAR(n)` / `VARCHAR(n)` — LANDED 2026-09-04
**Semantics decision, made in code but never recorded here until now**:
true SQL-standard `CHAR(n)` — fixed-width, space-padded storage and
comparison — not a bare length ceiling. **On-disk layout**: both `Kind`s
reuse `STRING`/`TEXT`'s exact encoding (`u32` byte-length prefix + UTF-8
bytes, sharing every `KindString`/`KindText`/`KindBlob` case in
`row.go`); `n` is carried in `Type.Precision`, the same field `DECIMAL`
uses for a parameter. **`n` counts runes, not bytes** — a deliberate
deviation from this doc's earlier VARCHAR-only proposal (which assumed
byte counting for simplicity); multi-byte UTF-8 content (`'héllo'` = 5
runes) is measured the way a human reading `CHAR(5)` would expect, at the
cost of an O(n) scan on write instead of a length-prefix comparison.
**Coercion into `CHAR(n)`**: shorter input is right-padded with `U+0020`
to exactly `n` runes; input over `n` whose excess is entirely trailing
spaces is silently trimmed back to `n` (the one ISO-standard silent-data
loss exception, deliberately kept — see below); input over `n` with any
non-space excess errors. **Coercion into `VARCHAR(n)`**: plain length
ceiling on the existing `STRING` behavior, no padding, over-length always
errors — never truncates. **Index-key ordering**: plain byte-lexicographic
on the stored (already-padded, for `CHAR`) representation — since padding
always brings a `CHAR(n)` value to exactly `n` runes regardless of how
many explicit trailing spaces the input had, two values that the standard
considers equal end up byte-identical on disk, so no separate
trim-then-compare comparator was needed; this is the same emergent
property `BLOB`'s plain byte-lexicographic order relies on. **Deliberate
exception to this track's own "never silently truncate" precedent
(D2/D3)**: `CHAR(n)`'s trailing-space truncation is the one place this
track knowingly reintroduces ISO `CHAR`'s classic silent-data-loss
behavior, because "true SQL-standard" was the explicit decision — anyone
who wants zero silent truncation should use `VARCHAR(n)`, which never
truncates. **`ENCRYPTED CLIENT`: explicitly NOT supported** for either
`Kind` (no entry in `internal/clientenc/clientenc.go`) — a deliberate
deviation from every prior D-item, because padding/truncation would
corrupt an opaque ciphertext blob that the server cannot inspect.
**FK eligibility**: ordinary FK-eligible scalars. No `NSCT` catalog
version bump (`Kind` is a plain appended byte tag).

**What's actually done**: `internal/sql/types/types.go` (`KindChar`/
`KindVarchar`, `CharType(n)`/`VarcharType(n)` constructors), `value.go`
(pad/trim/error `Coerce` logic, `Cmp`), `row.go` (encode/decode, sharing
`STRING`/`TEXT`/`BLOB`'s cases throughout), lexer/parser (`CHAR(n)`/
`VARCHAR(n)` grammar), binder (`facetable`), executor (`eval.go`,
`idempotency.go`), `internal/executor/vector/batch.go`, `internal/xport/
sql.go`, `internal/catalog/workflow.go`. Dedicated tests:
`internal/sql/types/char_test.go` (`TestCharCoercePadAndTrim`,
`TestVarcharCoerceCeiling`, `TestCharVarcharCrossCoerce`,
`TestCharRowAndKeyRoundTrip`, `TestCharCmp`, `TestCharTypeBounds`),
`internal/executor/char_test.go` (`TestCharVarcharInsertSelectRoundTrip`,
`TestCharOrderBy`, `TestCharStringFunctions`, `TestCharForeignKey`,
`TestCharVarcharNotEncryptedClient`, `TestCharParamRoundTrip`) — all
green (`go test ./internal/sql/types/... ./internal/executor/...`).

**Drivers, all 7** (landed 2026-09-04, `TODO.md` log #99 — closing the gap
`TODO.md` log #96 found): Go needed no change (shares `internal/protocol`
directly). Every non-Go driver decodes `CHAR`/`VARCHAR` result values and
`RowDesc` column headers as plain strings, sharing the exact same code path
already used for `STRING`/`TEXT` (the wire encoding is identical — a
`u32`-length-prefixed UTF-8 payload — so no new decode logic was needed
beyond adding the two `Kind` values to each existing `STRING`/`TEXT`/`BLOB`
case group). **Deliberately no client-side encode wrapper** for either
type: a plain string parameter already round-trips correctly as the write
path, since the server coerces `STRING -> CHAR`/`VARCHAR` (padding/
length-checking) against the destination column exactly as it does for a
SQL string literal — unlike `INT8`/`UINT8`/`FLOAT32`/`ENUM`, there is no
width or ordinal choice for a client to make explicit, so a wrapper would
add API surface with no behavior it enables. Live-verified against a real
`nextsqld` through the Python, Node, and PHP drivers (`CREATE TABLE`,
`CHAR(5)` padding, `VARCHAR(10)`, restart durability implied by the shared
core-engine test). See `TODO.md` log #99 for the full per-driver file list.

### D6 — `INTERVAL` — LANDED 2026-09-04
**Storage decision made by the user 2026-09-04**: Postgres-style 3-field
storage (chosen over a single fixed-duration-only field or a from-scratch
design) — `months` (`int32`, calendar) + `days` (`int32`, calendar) +
`nanos` (`int64`, time-of-day component), 16 bytes. This engine uses
nanosecond precision throughout (not Postgres's microseconds), so the
justified-comparison multiplier below is 1000x larger than Postgres's own —
directly relevant to why comparison uses `int64`-with-explicit-overflow-
checking rather than Postgres's own internal arithmetic, see below.

**On-disk layout**: `Value.IntervalMonths`/`Value.IntervalDays` (new `int32`
fields); the third component reuses `Value.Time`, the same field
`DATE`/`TIME`/`TIMESTAMP`/`TIMESTAMPTZ` already share, disambiguated by
`Value.Typ.Kind`. Wire/heap-row encoding (`encodeScalar`) is the exact
16 bytes, little-endian, no sign-bit flip (that only applies to the
sortable-key path) — reconstructs the *exact* original value every time,
unlike the sortable key below.

**Comparison — Postgres's own "justified" heuristic** (1 month = 30 days =
24h): `justifiedNanos(months, days, nanos) = (months*30 + days) * 86400e9 +
nanos`, giving intervals a total order despite months/days/nanos being
fundamentally different units. This is not an approximation of Postgres's
behavior — it is Postgres's actual, documented `interval_cmp` rule: **two
intervals unequal in their raw fields can compare equal** (`1 month` = `30
days`, verified live: `SELECT dur FROM t WHERE dur = INTERVAL '423 days'`
matches a stored `1 year 2 months 3 days`, since `14*30+3 = 423`).
Implemented with explicit `int64` overflow checking (erroring, never
wrapping) rather than Postgres's own wider internal arithmetic, since this
engine's nanosecond precision makes the multiplier 1000x larger than
Postgres's microseconds — international-scale `INTERVAL` magnitudes
(centuries) remain safely comparable; only genuinely extreme month/day
counts error rather than silently overflow.

**Index-key ordering**: the sortable key is the justified total (8 bytes,
sign-bit-flipped `int64`) — **not** a field-by-field encoding of (months,
days, nanos), which would not match `Cmp`'s order for boundary cases (e.g.
`40 days` vs `1 month 10 days` would sort differently under naive
field-priority encoding than under the justified rule, breaking index-scan
vs. heap-scan consistency). Consequence, found and accepted as a deliberate
design tradeoff before it became a bug: `decodeSortable` cannot recover the
exact original `(months, days)` split for an index-only-scan
reconstruction — it returns a canonical `(0 months, N days, remainder
nanos)` value with the *same justified total* instead. A plain heap scan
(the 16-byte exact encoding above) always returns the original value
unchanged; only the sortable-key path (index-only scans, `PRIMARY KEY`
reconstruction) canonicalizes — the same class of deliberate,
documented canonicalization as `FLOAT`'s `-0.0` → `+0.0`
(docs/design-datatypes.md D8), not a new kind of imprecision.

**Literal syntax — deliberately NOT following D1-D5/D7/D8's "no dedicated
literal syntax" precedent**: `INTERVAL 'text'` typed-literal syntax (a new
grammar production, `KwInterval` + a required string) was added, unlike
`DATE`/`TIME`/`TIMESTAMP`'s bare-quoted-string approach. Found necessary,
not assumed: a plain string literal in an arithmetic expression like
`d + '1 month'` cannot work, because the executor's arithmetic dispatch
routes on the *actual wire `Kind`* of both operands before evaluation —
unlike column-assignment coercion (`INSERT`/`UPDATE`), which has a known
destination type to `Coerce` against, `d + <param>` has no such destination
until *after* the arithmetic already needs to know both operand Kinds. A
plain string still works for column assignment via the same isolated
text-coercion precedent as D1-D5/D7/D8 (`ParseInterval`, accepting
`"<quantity> <unit> ..."` pairs — `year(s)`, `month(s)`, `day(s)`,
`hour(s)`, `minute(s)`, `second(s)`, `millisecond(s)`, `microsecond(s)`,
common abbreviations). year/month/day quantities must be whole numbers
(fractional calendar units are ambiguous — how many days is "1.5 months"?
— and explicitly out of scope for this increment); hour and smaller units
accept a decimal quantity, converted to nanoseconds via exact `big.Int`
arithmetic (never float), erroring on sub-nanosecond precision rather than
rounding.

**Arithmetic** (the entire point of choosing D6 over leaving D5's `DATE`/
`TIME` un-composable): `INTERVAL +/- INTERVAL` (field-wise, `int64`/`int32`
overflow-checked); `<temporal> +/- INTERVAL` for all of `DATE`/`TIME`/
`TIMESTAMP`/`TIMESTAMPTZ`, commutative for `+`; `<temporal> - <same
temporal>` yields `INTERVAL` as the exact elapsed duration (carried
entirely in the nanosecond field — this increment does not attempt to
break an arbitrary elapsed duration back into "N months, N days", which is
inherently ambiguous without an anchor date); unary negation. **`DATE`
arithmetic always promotes to `TIMESTAMP`** (matches Postgres): a `DATE`
has no time-of-day, so an interval carrying any time component doesn't fit
back into `DATE`. Calendar-month addition **clamps the day-of-month to the
target month's last day** (`Jan 31 + 1 month = Feb 28/29`, live-verified
both in a leap year and a non-leap year), the same rule Postgres uses.
**`TIME` discards an interval's months/days components entirely** (also
matching Postgres) and wraps modulo 24h. **`TIMESTAMPTZ` calendar math
operates on UTC civil fields directly** — this engine has no
session-timezone concept, unlike Postgres, whose `TIMESTAMPTZ + INTERVAL`
uses the session's timezone GUC for calendar-correct results; a deliberate,
documented simplification, not an oversight. `INTERVAL * scalar` (Postgres
supports scaling, e.g. `2 * INTERVAL '1 month'`) is explicitly **out of
scope for this increment** — fractional-quantity distribution across
calendar units introduces its own ambiguity (how does `0.5 * (1 month)`
distribute into days?) comparable to D6's own literal-quantity restriction
above, deferred rather than guessed at.

**Coercion**: isolated from every family but text, same D1-D8 precedent —
no implicit numeric reinterpretation, even though `INTERVAL` is internally
"a number of nanoseconds plus two integers" to the storage layer.
**Aggregates**: `MIN`/`MAX` work for free via the generic `Value.Cmp`
dispatch (justified order); `SUM`/`AVG` correctly error (no `INTERVAL`
source case in the `DECIMAL`-promotion `Coerce` path) — live-verified.
**FK eligibility**: ordinary FK-eligible scalar (block-list, not
allow-list). **`ENCRYPTED CLIENT`**: included (generic opaque-scalar
reasoning, same as D1-D3/D5/D7/D8 — nothing about `INTERVAL`'s fixed
16-byte shape prevents opaque encryption, unlike `CHAR`'s padding or
`ENUM`'s declaration-order semantics). No `NSCT` catalog version bump
(`Kind` is a plain appended byte tag, `KindInterval` = 31, right after
`KindEnum` = 30).

**Three real bugs found and fixed while implementing this**, none actually
new to D6 — all pre-existing, D6 just happened to be the first thing that
exercised the affected paths:
1. **`internal/sql/types/value.go`'s `Coerce` had no `STRING`/`TEXT` ->
   `TIMESTAMPTZ` case at all.** `ParseTimestamp` was defined but never
   called from anywhere in the non-test codebase — every existing test
   populates `TIMESTAMPTZ` via `DEFAULT NOW()` or a driver-native datetime
   object sent directly as `Kind.TimestampTZ` over the wire (bypassing text
   coercion entirely), so a plain SQL string literal `INSERT` into a
   `TIMESTAMPTZ` column, e.g. `VALUES ('2024-01-01T00:00:00Z')`, had never
   worked in this codebase, at any point, predating the entire
   Datatype-expansion track. Found only because D6's own tests needed a
   `TIMESTAMPTZ` column populated by literal text (to combine with
   `INTERVAL` in an arithmetic test). Fixed by adding the missing case,
   mirroring `DATE`/`TIME`/`TIMESTAMP`'s existing isolated-text-coercion
   pattern exactly.
2. **`FormatInterval` and `ParseInterval` were not actually
   round-trippable**, despite `FormatInterval`'s own doc comment claiming
   they were: `FormatInterval` originally emitted the time component as
   `HH:MM:SS[.frac]` (a colon-separated literal), but `ParseInterval`'s
   grammar only understands `"<quantity> <unit>"` pairs and has no
   colon-time production at all. Caught by `TestIntervalParseFormat`'s own
   format/reparse round-trip assertion before it ever shipped. Fixed by
   rewriting `FormatInterval`'s time-component output to the same
   `"<quantity> hours <quantity> minutes <quantity[.frac]> seconds"` shape
   `ParseInterval` already accepts.
3. **The Node driver's `putU64` threw a `RangeError` on any negative
   `BigInt`** instead of wrapping it to the unsigned 64-bit two's-complement
   bit pattern, because `Buffer.writeBigUInt64LE` requires its input in
   `[0, 2^64)` and does not wrap automatically — unlike the shared
   JS/Bun/Deno driver's `DataView.setBigUint64`, which wraps per the
   ECMAScript `ToBigUint64` abstract operation. This silently affected
   every pre-1970 `TIMESTAMPTZ`/`TIMESTAMP` encoded through the Node driver
   too (same helper), not just `INTERVAL`'s legitimately-negative
   nanosecond component (e.g. `"-1 hour"`) — nothing in the existing test
   suite exercised a negative value through `putU64` specifically until
   this D6 driver test (the pre-existing pre-1970 `DATE` test case uses a
   completely different `int32` encode path that never touches `putU64`).
   Fixed with `BigInt.asUintN(64, ...)` before the `Buffer` write.

**Live-verified** against a real `nextsqld`, in Go (CLI) and through the
Python/Node/PHP drivers: `DATE`/`TIMESTAMP`/`TIMESTAMPTZ`/`TIME` arithmetic
incl. the leap-year and non-leap-year `Jan 31 + 1 month` clamp both via SQL
literals and via bound parameters, `TIME` wraparound past midnight, the
justified comparison, restart durability, and negative-nanos round trips
through all three driver implementations.

**Drivers, all 7**: Go needed no change (shares `internal/protocol`
directly — `INTERVAL`, like `DATE`/`TIME`/`TIMESTAMP`/`FLOAT32`/`FLOAT64`,
needs no extra `Type` wire metadata beyond the fixed header, confirmed live
before writing any driver code). JS/Bun/Deno and Node each gained
`Kind.Interval = 31` and an `encodeInterval`/decode-case pair — an explicit
`{kind:'interval', months, days, nanos}` wrapper is required (unlike
`CHAR`/`VARCHAR`), for the same reason the SQL literal syntax is required:
arithmetic dispatch needs the real wire `Kind`, and a plain string only
satisfies column-assignment coercion, not expression evaluation. PHP, Python
(`Interval` dataclass — `datetime.timedelta` was considered and rejected as
the natural mapping, since `timedelta` cannot represent a calendar month
without silently converting it to a fixed day count, which would misrepresent
D6's whole reason for choosing Postgres-style storage), and Ruby (`Interval`
struct, keyword-init mirroring `EnumValue`/`NaiveTimestamp`) each gained the
same. Every driver's own test suite gained an `INTERVAL` round-trip test
including a negative-nanos case (a deliberate regression test for bug #3
above): Node 24/24, Bun 22/22, Deno 22/22, PHP `unit.php` `ok`, Python
42/42, Ruby 40/40.

### D7 — Plain `TIMESTAMP` (no timezone) — LANDED 2026-09-04
**Product decision, made in code but never recorded here until now**: a
real use case existed (import compatibility with sources that carry naive
local time) and was accepted. **On-disk layout**: `int64` nanoseconds
since `1970-01-01T00:00:00`, the civil value read literally with **no**
offset applied — reuses `Value.Time`, the same field `TIMESTAMPTZ`/`TIME`
use, disambiguated everywhere by `Value.Typ.Kind`. **Deliberately isolated
from `TIMESTAMPTZ`**: converting between them would require an assumed
timezone the engine has no basis for, so coercion is text-only (`STRING`/
`TEXT`, same ISO-8601-family precedent as `DATE`/`TIME`), same isolation
precedent as D1-D3/D5. **Index-key ordering**: shares `TIMESTAMPTZ`'s
existing plain-integer ordering (a naive nanosecond count is already a
correct total order — no sign-bit-flip subtlety, unlike `DATE`).
**`ENCRYPTED CLIENT`**: included (`internal/clientenc/clientenc.go`,
same generic opaque-scalar reasoning as D1-D3/D5). No `NSCT` catalog
version bump.

**What's actually done**: `internal/sql/types/types.go` (`KindTimestamp`,
`Timestamp()` constructor), `value.go`, `row.go`, lexer/parser (`KwTimestamp`
grammar — note `timestamp` and `timestamptz` are two separate keywords),
binder, `internal/executor/eval.go`/`idempotency.go`,
`internal/executor/vector/batch.go`, `internal/xport/sql.go`,
`internal/catalog/workflow.go`, `internal/clientenc/clientenc.go`.
Dedicated tests: `internal/sql/types/timestamp_test.go`
(`TestNaiveTimestampParseFormat`, `TestNaiveTimestampRowAndKeyRoundTrip`,
`TestNaiveTimestampIsolatedFromTZ`), `internal/executor/timestamp_test.go`
(`TestScheduleAtCanonicalTimestamp`, `TestPlainTimestampInsertSelectRoundTrip`,
`TestPlainTimestampForeignKeyAndMinMax`) — all green.

**Drivers, all 7** (landed 2026-09-04, `TODO.md` log #99): Go needed no
change. Every driver gained a `Kind`/`KIND_TIMESTAMP` value and decodes it
with the exact same 8-byte-nanoseconds logic as `TIMESTAMPTZ` — the wire
shape is identical; only the semantic label differs. **A bare native
datetime object still defaults to `TIMESTAMPTZ`** in every driver (existing
behavior, unchanged) — an explicit wrapper is required to select the naive
`TIMESTAMP` Kind instead: `{kind:'timestamp', value}` (JS/Node/PHP),
`NaiveTimestamp(value)` (Python — a naive `datetime.datetime`, tzinfo
`None`, already meant "assume UTC, encode as TIMESTAMPTZ" before this
change, so repurposing that default would have silently changed existing
behavior), `NaiveTimestamp.new(value:)` (Ruby). Decoding returns: a `Date`
tagged UTC in JS/Node (the only representation available; its UTC-read
fields are the intended civil value); a naive `datetime.datetime` in Python
(the natural, unambiguous mapping); a `DateTimeImmutable` in PHP; a
UTC-tagged `Time` in Ruby (same reasoning as JS — no native naive-time
type). **Two real, pre-existing bugs found and fixed in the PHP driver**
while implementing this, both affecting `TIMESTAMPTZ` too, not just the new
`TIMESTAMP` type: (1) encoding any negative epoch-nanosecond value (any
pre-1970 timestamp) via the existing `u64fromDec()` decimal-string helper
silently produced wrong bytes, because `decToBytes()` treats a leading `-`
as the digit `0`; (2) decoding a negative-nanosecond value combined
`intdiv()`'s truncation-toward-zero with PHP's dividend-signed `%`,
producing a negative microseconds component that made
`DateTimeImmutable::createFromFormat('U.u', ...)` silently return `false`
instead of raising. Both fixed with a signed-safe `i64le()` encode helper
and a floor-based `splitNanos()` decode helper — see `TODO.md` log #99 for
the full root-cause writeup and reproduction. Live-verified against a real
`nextsqld` through the Python, Node, and PHP drivers, including a
pre-1970 round trip.

### D8 — `FLOAT32` / `FLOAT64` — LANDED 2026-09-04
**Approval decision, made in code but never recorded here until now**:
approved, with a stated reason (interop with external numeric data) and
the required NaN/-0/ordering-canonicalization spec. **Canonical total
order for index keys**: `-Inf < negative reals < 0 < positive reals <
+Inf < NaN`; `-0.0` is canonicalized to `+0.0` on write, and every NaN
payload/sign-bit pattern collapses to one canonical value — both
canonicalizations happen once, at `Coerce`/construction time, so `Cmp`
and the sortable-key encoder never need special-case float logic beyond
ordinary IEEE-754-with-canonical-inputs comparison. Stored in `Value.Flt`
(a new field; distinct from `DECIMAL`'s exact big-integer-plus-scale
representation — `FLOAT32`/`FLOAT64` are `DECIMAL`'s first inexact
sibling in this engine, a deliberate, approved reopening of the
float-rounding bug class `DECIMAL`-only arithmetic had avoided until now).
**`ENCRYPTED CLIENT`**: included (`internal/clientenc/clientenc.go`).
No `NSCT` catalog version bump.

**What's actually done**: `internal/sql/types/types.go` (`KindFloat32`/
`KindFloat64`, constructors), `value.go` (canonicalizing `Coerce`, `Cmp`),
`row.go`, lexer/parser (`FLOAT32`/`FLOAT64` grammar — no bare `FLOAT`
keyword, width is explicit), binder, `internal/executor/eval.go`/
`idempotency.go`, `internal/executor/vector/batch.go`,
`internal/xport/sql.go`, `internal/catalog/workflow.go`,
`internal/clientenc/clientenc.go`. Dedicated tests:
`internal/sql/types/float_test.go` (`TestFloatCanonicalization`,
`TestFloatTotalOrder`, `TestFloatRowAndKeyRoundTrip`, `TestFloatCoerce`),
`internal/executor/float_test.go` (`TestFloatInsertSelectArith`,
`TestFloatOrderByTotalOrder`, `TestFloatAggregatesAndPK`) — all green.
**Arithmetic breaks the D2/D3 DECIMAL-promotion rule on purpose**: if
either operand is a float, the whole expression evaluates in `float64`
and yields `FLOAT64` (`internal/executor/eval.go`'s `evalArith`, with an
inline comment pointing back at this doc) — `DECIMAL` is exact and cannot
represent an arbitrary float result, so promoting into it the way
`INT`/`UINT` do would be lossy rather than safe. Assigning the result back
into a `FLOAT32` column re-rounds via `Coerce`.

**Drivers, all 7** (landed 2026-09-04, `TODO.md` log #99): Go needed no
change. A bare native float/number already means `DECIMAL` in every driver
(existing behavior, unchanged and required to stay that way), so every
driver gained an explicit wrapper: `{kind:'float32'|'float64', value}`
(JS/Node/PHP), `Float32(value)`/`Float64(value)` dataclasses (Python),
`Float32.new(value)`/`Float64.new(value)` structs (Ruby). **NaN and
`+-Infinity` are valid `FLOAT32`/`FLOAT64` values and every driver's
wrapper accepts them** (unlike the bare-number-to-`DECIMAL` path, which
still requires finite, unchanged) — verified round-trip in every driver's
test suite. Decoding uses each language's native IEEE-754 unpack (already
present in every driver for `VECTOR`/geospatial float fields — no new
binary-math code needed, only new `Kind` cases routing to it). Live-verified
against a real `nextsqld` through the Python, Node, and PHP drivers.

### D9 — Collections: `ARRAY<T>` / `MAP<K,V>` / `STRUCT<...>` — SPLIT OUT 2026-09-04, then LANDED 2026-09-04
**Too large for this track** (recursive type descriptors, per-element
encoding, index-key ordering for composites, row-format `NSRW` changes —
comparable to JSON/Vector phases), so split into its own
`docs/design-collections.md` and a `TODO.md` "Cross-cutting track —
Collections" on 2026-09-04. **All three sub-items (C1 `STRUCT`, C2
`ARRAY`, C3 `MAP`) then landed end-to-end the same day** (`TODO.md` log
#109) under the user's "must be full" — core engine (`NSCT` v11→v12,
recursive wire descriptor, orderable lexicographic tuple keys), all 7
drivers, fuzzing, docs. The full design record is
`docs/design-collections.md` §2a.

### D10 — Spatial: `GEOMETRY` / `GEOGRAPHY` — SPLIT OUT to its own track 2026-09-04
**Scoping decision (2026-09-04)**: a second, more general PostGIS-style
subsystem alongside the existing 4 WGS84 shapes (`docs/geo.md`) — not a
generalization of them. The four fixed types stay exactly as they are.

**Now has its own design document and `TODO.md` track**, written
2026-09-04 under the user's "implement Collections and Spatial ... must be
full": **`docs/design-spatial.md`** (§2 decision record — two separate
Kinds `GEOMETRY`/`GEOGRAPHY`, per-column SRID+subtype declaration with no
`NSCT`/protocol bump, closed SRID registry {0, 4326, 3857} with no `PROJ`
dependency, EWKB on-disk with a u32 length prefix, canonical-EWKB
index-key order, `CREATE SPATIAL INDEX` generalized to a bbox Z-order key,
explicit CAST bridge to the four fixed types, `ST_`-only function surface)
and a sequenced S1–S6 plan. Mirrored in `TODO.md` under "Cross-cutting
track — Spatial." **S1 through S6 all landed 2026-09-04** (`TODO.md` log
#113) — see `docs/design-spatial.md` §7 for the full per-increment
writeup.

### D11 — `ENUM(label, ...)` — LANDED 2026-09-04
Was missing from the original taxonomy entirely — added 2026-09-03, found
half-wired and unsafe the same day (`TODO.md` log #96), core-engine wiring
completed and live-verified 2026-09-04 (`TODO.md` log #97), all 7 official
drivers landed and live-verified the same day (`TODO.md` log #98) — the
first D-track item to go from "found undocumented and unsafe" to fully
landed by this track's own bar in two days.

**On-disk layout**: `u16` ordinal index into the column's declared label
list (`Type.EnumLabels`, mirrors Postgres/MySQL), same shape as `row.go`'s
existing `KindString`/`KindText`/`KindBlob`/`KindChar`/`KindVarchar`
grouping for the heap-row string payload (the label itself, for display)
plus a dedicated 2-byte ordinal field. **Index-key ordering**: plain
unsigned big-endian ordinal, no sign-bit flip (an ordinal is always ≥ 0,
mirroring `UINT16`) — **declaration order, not lexicographic**, the entire
point of `ENUM` (e.g. `small < medium < large` even though that is the
reverse of alphabetic order). **Coercion**: isolated from every family but
text (D1-D8 precedent) — `CAST` from `STRING`/`TEXT` validates label-set
membership and errors on a non-member value; two `ENUM` types with
different declared label lists are different types, and coercing between
them re-resolves the label against the destination's list. **Arithmetic**:
none — `ENUM` is not a numeric family. **Aggregates**: `MIN`/`MAX` work via
the existing generic `Value.Cmp` dispatch (declaration order); `SUM`/`AVG`
correctly error. **`ENCRYPTED CLIENT`: deliberately NOT supported**
(`internal/clientenc/clientenc.go`'s `SupportedType` has no `KindEnum`
case, mirroring `CHAR`/`VARCHAR`'s exclusion) — declared labels aren't a
stated sensitivity need; revisit only if a concrete use case appears.
**FK eligibility**: ordinary FK-eligible scalars (the FK check is a
block-list — `VECTOR`/`JSON` — not an allow-list, so `ENUM` was already
covered with no code change). **Catalog**: `EnumLabels` persists per
column; the `NSCT` table format took a real version bump, v10→v11, the
only D-item that needed one (a new `Kind` tag alone was enough for every
other item).

**What's done**: `internal/sql/types/{types,value,row}.go` (Kind, label
list, `EnumType`/`EnumValue`/`EnumValueByOrdinal` constructors, `Coerce`,
`Cmp`, row/key encode-decode), lexer/parser grammar (`ENUM('a','b',...)`
with a quoted label list), `internal/catalog/encode.go` (persistence + the
v11 bump), `internal/sql/binder/binder.go` (`facetable` — `ENUM` is a
natural `SEARCH ... FACET` column, low-cardinality by construction),
`internal/executor/idempotency.go`, `internal/catalog/workflow.go`,
`internal/xport/sql.go` (`sqlLiteral`, quoted/escaped like `STRING`;
DDL emission needed no separate change — `sqlType` already falls through
to `Type.String()`, which already rendered `ENUM('a', 'b')` correctly),
and `internal/executor/vector/batch.go` (shares the `Int` slice with
`INT8..64`/`DATE`, since the ordinal is the vectorized representation).
Tests: `internal/sql/types/enum_test.go`, `internal/executor/enum_test.go`
— INSERT/SELECT round trip + catalog persist/reopen, declaration-order
`ORDER BY`/`MIN`/`MAX`, CAST membership validation, FK, `ENCRYPTED CLIENT`
rejection, `FACET`, vectorized-batch `Compact`/`Project` — all green.

**Two real bugs found and fixed while wiring this** (full detail in
`TODO.md` log #97): (1) the wire protocol (`internal/protocol/value.go`)
never carried `Type.EnumLabels` — every `ENUM` value failed to decode the
moment it crossed the network (`ENUM ordinal out of range`), invisible to
every in-process `go test` in this entire track since none of them
serialize a `types.Value`/`types.Type` at all; caught only by live
verification against a running `nextsqld`, not by any unit test. Fixed by
giving `ENUM`'s `Type` bounded variable-length wire metadata. (2) five
`internal/catalog` tests were already failing on `HEAD`, unrelated to
`ENUM` itself — their hand-computed "strip N trailing bytes to synthesize
a legacy catalog format" arithmetic was never updated for v11's own
2-bytes-per-column trailer when D11's catalog change shipped. Both fixed;
see the log for the full root-cause writeup.

**Drivers, all 7** (`TODO.md` log #98): Go needed no change (shares
`internal/protocol` directly, same as every prior D-item). `drivers/js/
protocol.mjs` (shared by Bun/Deno) and `drivers/node/nextsql.js` (its own
independent implementation, per existing precedent) each gained `Kind.Enum
= 30`, `appendEnumLabels`/`readEnumLabels` helpers mirroring the Go fix
exactly, an explicit `{kind:'enum', value, labels}` param wrapper
(`encodeEnum`), and label-list-aware `decodeValue`/`decodeRowDesc` —
a plain JS string also still works as an `ENUM` param, coerced
server-side, same as a SQL string literal. `drivers/php/src/{Client,
Protocol}.php` — `KIND_ENUM`, `appendEnumLabels`/`readEnumLabels`,
`encodeEnum` (`['kind'=>'enum','value'=>...,'labels'=>...]` wrapper),
`decodeValue`/`decodeRowDesc` updated. `drivers/python/nextsql/
protocol.py` — `KIND_ENUM`, an `EnumValue` wrapper dataclass (named to
avoid colliding with the stdlib's `enum.Enum`, unlike every other
wrapper's plain type-name precedent), `Column.labels`, same label-list
plumbing. `drivers/ruby/lib/nextsql/protocol.rb` — `KIND_ENUM`, an
`EnumValue` struct (keyword-arg `value:`/`labels:`, mirroring `Vector`'s
existing keyword-init precedent), `Column` gained a third `labels` field,
same plumbing. Every driver's own test suite gained an ENUM round-trip
test (param encode/decode, non-member rejection, `RowDesc`/`Column`
label-list framing where applicable) — Node 22/22, Bun 20/20, Deno 20/20,
PHP `unit.php` `ok`, Python 37/37, Ruby 35/35, all green. **Live-verified**
against a real `nextsqld` through three independent real network-client
implementations (Python, Node, PHP) — `CREATE TABLE ... ENUM(...)`,
declaration-order `ORDER BY`, an explicit ENUM-wrapper bound parameter, a
plain-string bound parameter (server-side coercion), and non-member
rejection all correct end-to-end; Go's path was already proven by the
core-engine wiring's own live verification (log #97), and Bun/Deno share
`protocol.mjs` with the live-verified Node implementation.

## 4. What this track does NOT include

Per `PROJECT.md`/`TODO.md` convention, this track gates no phase (P0-P27
are closed; P28-P30 are next). Nothing here should be started opportunistically
inside other phase/track work — each `D`-item gets its own increment log
entry when picked up, same as M2's sub-items.
