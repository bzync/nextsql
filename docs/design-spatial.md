# Spatial — `GEOMETRY` / `GEOGRAPHY` — design & decomposition

> Status: **design recorded, implementation in progress.** This is D10 from
> the Datatype-expansion track (`docs/design-datatypes.md`), whose recorded
> 2026-09-04 scoping decision was "a new, more general PostGIS-style
> subsystem **alongside** the existing 4 WGS84 shapes — not a generalization
> of them with SRID support." Written under the user's "implement
> Collections and Spatial ... must be full", after the Collections track
> (`docs/design-collections.md`) landed. Phase-sized, like P9 (JSON) or
> P11/P23 (Vector). Gates no release phase. Mirrored in `TODO.md` under
> "Cross-cutting track — Spatial."
>
> **Progress**: **S1–S6 all landed 2026-09-04** (`TODO.md` log #113).
> `GEOMETRY`/`GEOGRAPHY` are usable end to end: declare, store, query
> (measurement + predicates), index, drive (all 7 official drivers), and the
> S5 overlay operators (bounded — see §8) plus GeoJSON. See §7 for the
> per-increment writeup and what each one actually covers.

## 1. What already ships, and why this sits beside it

`docs/geo.md` ships four fixed scalar types — `POINT`/`LOCATION`, `BOX`,
`LINESTRING`, `POLYGON` — each its own `Kind` with a fixed little-endian
`float64` layout, hard-coded WGS84 lon/lat degrees, no SRID concept, a
256-vertex cap so a geometry fits one 16 KiB page, and deliberately
*native* (not OGC) predicate semantics. Its own closing line lists
"geography-vs-geometry dual types" as **not implemented**.

This track adds that. The four existing types **stay exactly as they are**
— no SRID retrofit, no shared type hierarchy, no deprecation. They remain
the fast path for "a WGS84 point/box/line/polygon column." `GEOMETRY` and
`GEOGRAPHY` are a second, general family alongside them, for: arbitrary OGC
geometry (incl. `Multi*` and `GeometryCollection`), an explicit spatial
reference system (SRID), and the planar-vs-geodetic distinction.

**Product-scope note**: adding a PostGIS-style `GEOMETRY`/`GEOGRAPHY` split
is a real departure from `docs/geo.md`'s stated "this is not PostGIS"
identity. `PROJECT.md` gets a one-line scope note in the S1 increment
("native spatial types cover the OGC Simple Features common subset;
`GEOMETRY`/`GEOGRAPHY` with SRID sit alongside the four fixed WGS84
shapes"). This is a deliberate expansion the user directed, not scope creep.

## 2. Decision record (made now, before code — `docs/design-datatypes.md` §2 shape)

### 2.1 Two types, not one

`GEOMETRY` (planar / Cartesian arithmetic) and `GEOGRAPHY` (geodetic /
great-circle arithmetic) are **genuinely separate types** with separate
operation semantics, matching PostGIS's own split — not one type with a
mode flag. `KindGeometry` and `KindGeography` are two new `Kind` tags. A
measurement or predicate resolves its algorithm from the operand Kind:
`ST_Distance` over `GEOMETRY` is planar Euclidean in SRID units; over
`GEOGRAPHY` it is metres on the ellipsoid (Vincenty, haversine fallback,
reusing the existing `internal/sql/types/geo.go` primitives). Mixing a
`GEOMETRY` and a `GEOGRAPHY` operand in one call is a type error — the
coercion bridge (§2.7) or an `ST_Transform`-style function must convert
one first — same isolation discipline as every D-item.

### 2.2 SRID is a per-column declaration

`GEOMETRY(subtype, srid)` / `GEOGRAPHY(subtype, srid)`, e.g.
`GEOMETRY(Point, 3857)`, `GEOGRAPHY(Polygon, 4326)`. Both the subtype and
the SRID are optional: `GEOMETRY` alone = any subtype, SRID 0;
`GEOMETRY(Point)` = points, SRID 0; `GEOGRAPHY` alone = any subtype, SRID
4326 (WGS84 is the only geodetic frame this engine models). Chosen over a
per-value SRID tag: simpler storage (no 4 bytes/value), simpler index (one
CRS per index), and a compile-time check that every value in a column
shares a frame — the per-value flexibility PostGIS allows is rarely used
and costs a compatibility check on every binary operation.

Stored with **no catalog or protocol version bump**: the SRID goes in the
existing `Type.Precision` (`uint16` — SRID codes in scope all fit), the
subtype in `Type.Scale` (`0` any, `1` Point, `2` LineString, `3` Polygon,
`4` MultiPoint, `5` MultiLineString, `6` MultiPolygon, `7`
GeometryCollection). New `Kind` tags are a plain appended byte, exactly
like every scalar D-item that needed no bump.

### 2.3 SRID registry — closed set, no PROJ dependency

Recognised SRIDs: **0** (unknown / local Cartesian, `GEOMETRY` only),
**4326** (WGS84 geographic, lon/lat degrees), **3857** (WGS84 / Web
Mercator, metres). `ST_Transform(geom, srid)` supports only the
`4326 ↔ 3857` pair, via closed-form spherical Mercator math (no external
`PROJ`/`proj4` — that would contradict `docs/geo.md`'s "not a GIS
sidecar"). Any other target SRID, or a transform between two unregistered
codes, errors with a clear message. A column may still *declare* an
arbitrary SRID integer (it is opaque metadata); only `ST_Transform` and
the geodetic-math path care about the specific value.

### 2.4 On-disk layout — EWKB

The heap-row payload is **EWKB** (PostGIS's extended WKB): 1 byte
endianness (always little, `01`), `uint32` geometry type with the
`0x20000000` SRID-present flag when an SRID is carried, optional `uint32`
SRID, then type-specific coordinate data (`Point` = 2 × `float64`;
`LineString` = `uint32` count + points; `Polygon` = `uint32` ring count +
each ring a `uint32` count + points; `Multi*` = `uint32` part count + each
part a full sub-geometry incl. its own 5-byte header;
`GeometryCollection` likewise). Self-delimiting, so `skipScalar` reads the
whole thing without a separate length prefix — but a `uint32` **total byte
length** is still prepended (like the collection encoding) so `skipScalar`
is O(1) rather than a full structural walk. Resource caps: total vertices
≤ 65536 per value, nesting (collection-in-collection) ≤ 8, part count ≤
4096 — enforced at decode.

Coordinates are `(x, y)` = `(longitude, latitude)` for geographic frames,
matching `docs/geo.md`. 3D (`Z`) and measured (`M`) coordinates, and
curve/surface geometries (`CircularString`, etc.), are **out of scope**
(deferred, §8).

### 2.5 Index-key ordering — canonical EWKB byte order

The sortable key is the canonical EWKB bytes (always little-endian,
SRID-normalised, ring orientation normalised — exterior CCW, holes CW —
and closed) run through the same zero-escaped order-preserving byte
encoding `BLOB`/`STRING`/`JSON` already use. This is a **deterministic
total order**, so `GEOMETRY`/`GEOGRAPHY` are valid `PRIMARY KEY` /
`ORDER BY` / `GROUP BY` / plain-btree-index columns and `=` works as exact
geometric equality on the canonical form. It is **not geometrically
meaningful** (two nearby points can sort far apart) — documented, and the
same pragmatic choice PostGIS makes for `ORDER BY geom`. Spatial locality
is served by the dedicated spatial index (§2.6), not the btree order.

### 2.6 Spatial index — generalise `CREATE SPATIAL INDEX`

`CREATE SPATIAL INDEX ix ON t (geom)` accepts a `GEOMETRY`/`GEOGRAPHY`
column (today: `POINT` only). The key is the geometry's **bounding-box**
mapped to a 64-bit Morton (Z-order) code of the box centre plus a coarse
box-extent bucket, then the primary key — reusing the existing geohash
machinery, now keyed on a bbox rather than a point. Sargable predicates:
`ST_DWithin(col, const, r)`, `ST_Intersects(col, const)`,
`ST_Contains(const, col)` / `ST_Within(col, const)` against a constant
geometry — the optimiser covers the query with a Z-order prefix range over
the constant's bbox (expanded by `r` for `DWithin`) and keeps the exact
predicate as a residual. A proper R-tree is **deferred** (§8) — the
Z-order-of-bbox approach is correct (residual re-checks) and reuses proven
code.

### 2.7 Coercion bridge to the four fixed types

Required by `docs/design-datatypes.md` §2. NextSQL has no `CAST(x AS type)`
SQL syntax — coercion is implicit (column assignment on `INSERT`/`UPDATE`,
function arguments), driven by `types.Coerce`. So the "bridge" is: a
`POINT` value assigned to a `GEOGRAPHY(Point, 4326)` column, or vice versa,
is converted automatically. The rules:

| From | To | Rule |
|---|---|---|
| `POINT` / `LINESTRING` / `POLYGON` | `GEOGRAPHY(<same>, 4326)` | wrap, SRID 4326 (they are WGS84 by definition) |
| `POINT` / `LINESTRING` / `POLYGON` | `GEOMETRY(<same>, 4326)` | same, planar-tagged |
| `BOX` | `GEOMETRY(Polygon, 4326)` / `GEOGRAPHY(Polygon, 4326)` | expand to a 5-point ring (antimeridian-wrapping box → error, same as `docs/geo.md`) |
| `GEOGRAPHY(Point, 4326)` | `POINT` | unwrap when subtype is Point and SRID 4326; other subtype/SRID errors |
| `GEOMETRY`/`GEOGRAPHY` | `STRING`/`TEXT` | `ST_AsText` (WKT) |
| `STRING`/`TEXT` | `GEOMETRY`/`GEOGRAPHY` | `ST_GeomFromText` / `ST_GeogFromText`; WKT or EWKT (`SRID=4326;POINT(...)`) |

### 2.8 Other properties

- **`ENCRYPTED CLIENT`: not eligible** — the server evaluates predicates,
  measurements and spatial-index keys; an opaque blob defeats all of that.
- **FK eligibility: not eligible** — block-list, with `VECTOR`/`JSON`/
  collections and the existing geo types.
- **Aggregates**: `MIN`/`MAX` via the canonical-EWKB `Value.Cmp` order;
  `SUM`/`AVG` error. `ST_Collect` / `ST_Extent` / `ST_Union` aggregate
  forms are deferred (§8).
- **Determinism / replication**: every operation is a pure function of its
  inputs (no wall-clock, no RNG); constructors capture-once like every
  other value. Nothing new for Raft.

## 3. Relationship to `internal/sql/types/geo.go`

The existing 2000-line `geo.go` holds validated WGS84 primitives:
haversine, Vincento inverse, point-in-polygon ray casting, segment
distance, spherical ring area, the `EvalGeo` function dispatcher, WKT
parsing for the four shapes. `GEOGRAPHY`'s geodetic math **reuses these
primitives directly**. `GEOMETRY`'s planar math is new but simple
(Euclidean distance, shoelace area, planar point-in-polygon). The EWKB
codec and the general OGC predicate suite (`ST_Relate`-family) are new.

## 4. Grammar

- **Types**: `GEOMETRY`, `GEOMETRY(Point)`, `GEOMETRY(Point, 3857)`, and
  the same for `GEOGRAPHY`. Subtype names: `Point`, `LineString`,
  `Polygon`, `MultiPoint`, `MultiLineString`, `MultiPolygon`,
  `GeometryCollection` (case-insensitive).
- **Constructors** (functions, no new literal syntax — same precedent as
  D5/D7): `ST_GeomFromText('POINT(1 2)'[, srid])`,
  `ST_GeogFromText('POINT(1 2)')`, `ST_MakePoint(x, y)` (→ `GEOMETRY`),
  `ST_Point(x, y, srid)`, `ST_GeomFromGeoJSON('{...}')`. A plain quoted
  WKT/EWKT string also coerces into a `GEOMETRY`/`GEOGRAPHY` column on
  `INSERT`/`UPDATE` (isolated text-coercion path, like the four fixed
  types).
- **Functions**: `ST_`-prefixed, the OGC Simple Features common subset —
  see §7 for which land in which increment. `docs/geo.md`'s existing
  non-`ST_` spellings (`DISTANCE`, `DWITHIN`, `WITHIN`, …) stay bound to
  the four fixed types only; the `GEOMETRY`/`GEOGRAPHY` family is
  `ST_`-only to keep the two families visibly distinct.

## 5. Catalog / protocol / drivers

- **Catalog**: new `Kind` tags + SRID in `Precision` + subtype in `Scale`
  → **no `NSCT` version bump** (v12 stays current).
- **Wire protocol**: same — SRID/subtype ride in the existing 5-byte meta;
  new `Kind` tags. **No NSQL version bump.**
- **Drivers, all 7**: each gains `Kind` values, decodes EWKB result values
  to a native representation (`{ type, srid, coordinates }` GeoJSON-ish
  object, or a WKT string — TBD per driver in the S4 increment), and
  accepts a WKT string / GeoJSON object / `{ kind: 'geometry', wkt, srid }`
  wrapper as a param. Go needs no change (shares `internal/protocol`).

## 6. Testing obligations (per `SKILLS.md` §21)

Every increment: unit + `internal/executor` integration + restart/reopen
durability. New untrusted decoders (EWKB, WKT, GeoJSON) get `go test
-fuzz`. The spatial index increment adds crash-injection + rebuild +
concurrent-write coverage (same bar as every other index type). Predicate
correctness is cross-checked against a table of known PostGIS results
committed as test fixtures (values, not a live PostGIS dependency).

## 7. Sequenced plan

| # | Scope | Status |
|---|---|---|
| **S1** | `KindGeometry`/`KindGeography` (Kinds 35/36), `Type` (subtype in `Scale`, SRID in `Precision`), EWKB codec (`internal/sql/types/spatial.go` — encode/decode/skip/sortable), WKT/EWKT parse+format, **no** catalog/protocol version bump, lexer/parser (`GEOMETRY(sub, srid)` grammar + `ST_GeomFromText`/`ST_GeogFromText`/`ST_GeomFromEWKT`/`ST_Point`), binder (`IsSpatialFunc`), executor `eval_spatial.go`, vectorized batch (`Vector.Geom`), accessors (`ST_X`/`ST_Y`/`ST_SRID`/`ST_SetSRID`/`ST_GeometryType`/`ST_NPoints`/`ST_NumGeometries`/`ST_GeometryN`/`ST_AsText`/`ST_AsEWKT`/`ST_AsBinary`/`ST_Dimension`/`ST_IsEmpty`), the coercion bridge (§2.7 — implicit, matching `Coerce`; NextSQL has no `CAST(x AS type)` syntax), `ENCRYPTED CLIENT` (structural) + FK exclusion, `xport`, EWKB+WKT fuzz (2 targets), `PROJECT.md` §29 rewrite. | ☑ **landed (log #113)** |
| **S2** | Measurement (`ST_Distance`/`ST_Length`/`ST_Perimeter`/`ST_Area` — planar for `GEOMETRY`, geodetic for `GEOGRAPHY`, reusing `geo.go`'s haversine/spherical-ring-area), `ST_DWithin`, the predicate suite (`ST_Equals`/`ST_Intersects`/`ST_Disjoint`/`ST_Contains`/`ST_Within`/`ST_Covers`/`ST_CoveredBy`/`ST_Crosses`/`ST_Overlaps`/`ST_Touches` — native semantics, §8), derived geometry (`ST_Envelope`/`ST_Centroid`/`ST_Boundary`/`ST_PointN`/`ST_StartPoint`/`ST_EndPoint`/`ST_ExteriorRing`/`ST_InteriorRingN`/`ST_NumInteriorRings`/`ST_Reverse`), `ST_Transform` (4326↔3857). | ☑ **landed (log #113)** |
| **S3** | Generalised `CREATE SPATIAL INDEX` to `GEOMETRY`/`GEOGRAPHY` (bbox-centre Z-order key, `internal/executor/exec.go` `indexKV`), optimiser sargability for `ST_Intersects`/`ST_Contains`/`ST_Within`/`ST_Covers`/`ST_CoveredBy`/`ST_DWithin` against a constant geometry (new `geoConstGeom`/`geoGeneralColConst` in `internal/sql/optimizer/physical.go`, selectivity in `cost.go`), a real distance-unit bug found and fixed (a `GEOMETRY` column's `ST_DWithin` radius is in raw coordinate units, not metres — `ExpandBBox` assumed metres; new `spatialMatch.planar` flag), `EXPLAIN` shows `IndexScan … spatial`, restart durability. | ☑ **landed (log #113)** |
| **S4** | All 7 drivers (js/bun/deno share `protocol.mjs`; node/python/php/ruby own impls) — `Kind` 35/36, an EWKB decoder (→ a GeoJSON-ish `{type, srid, coordinates}` object), a WKT/EWKT param path (`{kind:'geometry'\|'geography', wkt, srid}`, or a plain string the server coerces), per-driver round-trip test. | ☑ **landed (log #113)** |
| **S5** | `ST_ConvexHull` (exact, Andrew's monotone chain), `ST_Simplify` (exact, Douglas–Peucker), `ST_Segmentize` (exact), `ST_Buffer` (exact circle for a Point; convex-hull-of-offset-points for anything else — a documented convex over-approximation, §8), `ST_Intersection`/`ST_Union`/`ST_Difference`/`ST_SymDifference` — exact for the cases each provably solves (Sutherland–Hodgman when at least one operand is convex; disjoint/containment fast paths for union/difference) and a clear error otherwise rather than a silently wrong boundary (§8) — correctness over feature completeness (`CLAUDE.md` priority order). A real sign error in the Sutherland–Hodgman line-intersection formula was found and fixed (caught by a known-area test, not a fuzz target — see log #113). | ☑ **landed (log #113)** |
| **S6** | `ST_AsGeoJSON` / `ST_GeomFromGeoJSON` (RFC 7946, `encoding/json`) + fuzz (found and fixed two real crashes: an empty-ring index panic in `canonicalizeGeom` before validation ran, and a short-position/ring-count mismatch — both now fail closed with a clean error), docs pass (this file, `docs/geo.md` cross-ref, `docs/sql.md`, `PROJECT.md` §29), `CHANGELOG.md`, `TODO.md` track close. **`ST_Collect`/`ST_Extent` aggregates deferred** (§8) — NextSQL's aggregate framework (`COUNT`/`SUM`/`AVG`/`MIN`/`MAX`) is a hardcoded 5-name list across the binder, `GROUP BY` planning, and the hash-aggregate executor; adding a new aggregate function class is a separable cross-cutting change, not a scalar-function addition, and wasn't started speculatively. | ☑ **landed (log #113)** |

S1–S4 make `GEOMETRY`/`GEOGRAPHY` genuinely usable (declare, store, query,
index, drive). S5–S6 complete the OGC surface. Each is its own `TODO.md`
log entry.

## 8. Deliberately out of scope (deferred)

- **3D (`Z`) / measured (`M`) coordinates**, curve geometries
  (`CircularString`, `CompoundCurve`, `CurvePolygon`), `TIN`/`PolyhedralSurface`.
- **A real R-tree** spatial index (S3 uses bbox-Z-order + residual, which
  is correct; an R-tree is a later performance increment).
- **`PROJ`/`proj4`** and arbitrary datum transforms — only `4326↔3857`.
- **Raster** (`docs` has no raster type and none is planned).
- **`ST_Relate` with an explicit DE-9IM matrix argument** — the named
  predicates (`ST_Contains` etc.) are in; the raw matrix form is deferred.
- **Topology** (`ST_Node`, `ST_Polygonize`, coverage), **linear
  referencing** (`ST_LineLocatePoint`, `ST_LineSubstring`), **clustering**
  (`ST_ClusterDBSCAN`).
- **Deprecating or SRID-retrofitting the four fixed WGS84 types** — they
  stay as-is, per the 2026-09-04 scoping decision.
- **`ST_Collect` / `ST_Extent` aggregates** — NextSQL's aggregate framework
  is a hardcoded `COUNT`/`SUM`/`AVG`/`MIN`/`MAX` list spanning the binder,
  `GROUP BY` planning, and the hash-aggregate executor; a new aggregate
  function class is a separable cross-cutting change (comparable in shape
  to the Collections track's own `ARRAY_AGG` deferral), not a scalar
  addition.
- **`ST_Buffer` on non-Point input is a convex over-approximation**
  (convex hull of every vertex's own offset-circle points), not a true
  Minkowski-sum buffer — exact only when the input is itself convex; never
  an under-approximation. **`ST_Intersection`/`ST_Union`/`ST_Difference`/
  `ST_SymDifference` are bounded, not general polygon booleans**: exact for
  the cases each provably solves (Sutherland–Hodgman clipping when at
  least one polygon operand is convex; disjoint/containment fast paths for
  union/difference) and a clear error for two general overlapping
  non-convex polygons, rather than a silently wrong boundary. A full
  general-polygon-boolean algorithm (Vatti/Greiner–Hormann, with hole and
  self-intersection handling) is a real, separate, and substantial
  computational-geometry subproject — deferred rather than attempted
  partially and passed off as complete.
- **`ST_MakeValid`, `ST_Force2D`** — the binder accepts the names
  (`IsSpatialFunc`) but no executor case implements them yet; calling
  either fails closed with an "unknown function" error at evaluation time,
  not silently. Flagged here so the gap is explicit rather than discovered
  by a user hitting it.

## 9. Source of truth

Mirrored in `TODO.md` under "Cross-cutting track — Spatial." See
`docs/design-datatypes.md` D10 for the original scoping note, `docs/geo.md`
for the four fixed WGS84 types this family sits beside.
