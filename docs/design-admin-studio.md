# NextSQL Admin — Studio mode design

> Status: **P29 in progress — M1 authenticated query workspace, M2
> streaming/virtualized results, M3 bounded result tools/native inspectors, a
> confirm-before-run safety check, execute-selection, query history, a
> graphical EXPLAIN/EXPLAIN ANALYZE tree with bounded per-tab plan comparison
> and an ANALYZE-only profiler breakdown, and multi-tab editing implemented
> alongside all five originally scoped dedicated native explorers — JSON,
> Full-text, Vector, Hybrid, and Geo — a Users & roles privilege explorer,
> read-only live Transaction/Lock and verified Audit explorers,
> catalog-aware table/column IntelliSense, table/index statistics
> inspection, a lazy-loaded schema tree, per-table foreign-key
> inspection (outbound constraints + inbound "Referenced by"), a
> table-inspector Dependencies panel (inbound FKs + triggers on the table
> from `system.triggers`), a
> schema-relationship diagram and a global object search in the database
> explorer, crash recovery for unsaved editor tabs, saved queries with
> tag folders, a
> read-only Workflows, trigger/schedule relationships, tasks &
> change-streams explorer with a bounded accessible workflow diagram,
> indexed-JSON-path completion and vector-aware NEAREST/USING completion
> in the editor,
> a production environment tag + read-only safety mode, switch-realm/database
> connection re-targeting, a per-session read-consistency mode, a
> recent-connections quick-switch, a global command palette, git-friendly file
> export/import of the saved-query set, positional prepared parameters
> ($1..$N), a canonical-DDL view (`system.table_ddl` + an inspector DDL
> section), a unified table-inspector Constraints panel, a read-only schema-
> migration history explorer, a visible realm-wide administration warning
> for user/role creation and removal, and a deterministic development data
> generator (`INSERT`-script builder over authorized `system.columns`), a
> CSV / JSON / NDJSON import (a bounded, type-checked `INSERT`-script builder
> that maps a pasted or loaded document onto the target table's columns), a
> vector dataset import (the same, for an embedding field onto a `VECTOR` /
> `BITVECTOR` / `SPARSEVECTOR` column, validated against declared dimensions),
> and parameterized `INSERT` / `UPDATE` / `DELETE` generation (a `$1..$N` statement
> template built from a table's authorized columns into the editor),
> browser/CSS high-DPI verification at DPR 2, layout persistence without
> credentials (explorer/inspector visibility and pane widths plus the last
> authorized table name in per-connection `localStorage`), vector-aware
> NEAREST/USING completion, and a table / index designer (`CREATE TABLE` /
> `CREATE INDEX` form with live native DDL preview into the editor), and a
> client-side **SQL formatter** (a Format button / Shift+Alt+F that reflows
> the editor buffer through a comment-preserving tokenizer guarded by a
> re-tokenize equivalence check, never executing), a vector dataset
> importer, **live parser diagnostics** (a debounced connection-free
> `POST /api/v1/studio/query/diagnostics` locating each statement in the
> buffer that fails to parse, with a Go-to-error control — grammar errors
> only, the binder half stays open), and an **editable data grid** with
> staged-change review and atomic transactional commit/discard (in-place
> cell editing, row deletion/insertion staging, a review modal, and a
> `BEGIN; ... COMMIT;` script executed over the authenticated session
> connection with automatic rollback on failure)
> implemented; the
> RBAC boundary is integration-test-covered (`TestAdminStudioEnforcesRBAC`),
> closing the MVP exit-gate RBAC, "Prepared parameters", and
> "Table inspector/data editor" lines
> (2026-09-08).**
> This document distinguishes the implemented slices from the
> larger Studio MVP in `TODO.md`; it does not close the P29 exit gate.

`PROJECT.md` §§48–67 defines the intended product. `TODO.md` Phase 29 is the
implementation-status authority. `docs/design-admin.md` defines the shared
NextSQL Admin process, shell, mode detection, and authentication boundaries.

## 1. Implemented increments

### M1 — authenticated query workspace

M1 turns the reserved Studio route in Operations mode into a useful,
authenticated database-development workspace:

- a responsive table explorer backed by the connected server's authorized
  `system.tables` rows;
- lazy table overview, column, and index inspection through `system.tables`,
  `system.columns`, and `system.indexes`;
- an editor with line numbers, bracket pairing, indentation, and
  Ctrl/Cmd+Enter execution;
- explicit cancellation for the one active Studio query in the browser
  session;
- typed, NULL-aware, bounded result preview; and
- visible connection/database/realm context plus server capability discovery.

This is deliberately not a miniature generic SQL client. The first read is
`system.capabilities`, catalog data comes only from NextSQL's native `system`
schema, and every statement goes through the native NSQL protocol.

### M2 — streaming and virtualized results

M2 replaces the browser's materialized query-response path with a bounded
NDJSON stream. Admin emits result metadata, batches of at most 128 rows / a
256 KiB raw-text target, then one completion frame. A single row is capped at
1 MiB raw text, and the whole preview remains capped at 5,000 rows / 8 MiB / 25
seconds. Reaching any bound cancels and drains the NSQL stream and ends with
`truncated=true`; it is never presented as a complete result.

The browser parses frames incrementally and independently re-enforces the
same row, row-width, raw-row, aggregate-byte, frame-order, and frame-size
bounds. Its grid virtualizes the DOM at a fixed row height: it retains only the
already-bounded preview and mounts the visible row window plus a small
overscan, rather than mounting thousands of table rows. Column headers remain
sticky, NULL remains distinct, and the scroll region is keyboard focusable.

The one-line result status under the editor (`queryResultSummary`, pure and
unit-tested) reads the completion frame rather than just counting rows: a
statement with result columns reports `N rows · M columns · T ms` (with the
truncation note when a bound was hit); a **column-less** result — a write or
a DDL — reports `N rows affected · T ms`, or `Statement completed · T ms`
when the affected count is zero, instead of a misleading `0 rows`. A
selection run is prefixed `Selection · `. This is the "Result / plan /
messages / statistics" panel's statistics line; the plan tree, the messages
(errors / truncation alerts), and the grid itself are the other three
facets, already present.

### M3 — bounded result tools and native cell inspection

M3 adds row selection plus result actions to the virtualized query grid. An
active cell can be copied exactly; selected rows (or all loaded rows when no
selection exists) can be copied as TSV or downloaded as CSV/JSON. CSV and TSV
neutralize spreadsheet formula prefixes and spell SQL NULL as `\\N`; the JSON
envelope preserves ordered/duplicate column names, column types, raw strings,
and NULL without object-key loss. Exports are generated locally only from the
already bounded preview and have an independent 64 MiB encoded ceiling.

The native inspector recognizes server-reported types rather than guessing
from cell text: JSON gets a bounded tree plus unchanged raw view; dense/bit/
sparse vectors get dimension, non-zero, L2-norm, and bounded value views;
fixed `POINT`/`BOX`/`LINESTRING`/`POLYGON` values get a normalized SVG
coordinate preview; and `TIMESTAMPTZ` gets canonical server, UTC,
browser-local, and epoch representations.

General `GEOMETRY`/`GEOGRAPHY` `MULTIPOINT`/`MULTILINESTRING`/`MULTIPOLYGON`/
`GEOMETRYCOLLECTION` values also get the SVG coordinate preview: the frontend
parser mirrors the server's own WKT grammar
(`internal/sql/types/spatial.go`'s `parseWKTGeom`/`writeWKT`), recursively
flattening a `GEOMETRYCOLLECTION`'s members — including nested collections,
bounded to the same 8-level nesting depth the server enforces
(`types.MaxSpatialDepth`) — into simple `POINT`/`LINESTRING`/`POLYGON` leaf
parts for rendering, under an independent preview point/part budget. Unlike
the fixed native shapes (always WGS84 lon/lat degrees), `GEOMETRY` is planar
and `GEOGRAPHY` is geodetic in an arbitrary per-column SRID, so the inspector
never range-checks their coordinates against longitude/latitude and labels
the coordinate order from the declared column type rather than assuming
lon/lat. An unparseable or over-budget value still fails closed to the
raw-WKT `CodeBlock` fallback, matching every other inspector's behavior on
invalid input.

### High-DPI browser rendering

The shared real-Chrome accessibility fixture applies a 720-CSS-pixel viewport
at device-pixel ratio 2 to Setup, Operations, and the authenticated Studio
workspace. For Studio it proves the compact single-column workspace layout
activates, the document does not overflow horizontally, every rendered raster
image has enough source pixels for its physical size, bundled scalable fonts
are loaded, and the DPR-2 view remains axe WCAG 2.2 AA clean. Device metrics
are reset before the interaction suite continues, so later checks cannot
accidentally inherit the high-density viewport.

This closes the Studio shell's browser/CSS-level high-DPI checklist item. It
does not convert the still-unexecuted Windows/macOS packages into supported or
tested platform claims; those remain tracked under Phase 28 packaging.

### Layout persistence without credentials

Studio's three-pane shell now has a resizable layout worth persisting. Column
splitters (`role="separator"`, 24px hit target, pointer drag plus Arrow/Home/End)
set explorer and inspector pixel widths; Hide/Show controls unmount a pane;
**Reset layout** in the command palette restores the default widths and shows
both panes. Those preferences, plus the last selected table *name*, are
mirrored to per-connection `localStorage` (`nextsql-studio-layout:…`) by a
bounded codec (`serializeStudioLayout` / `parseStudioLayout`). Malformed
storage is the default layout. SQL buffers already have their own crash-recovery
codec; results, query history, parameters, and any credential are never written
here. On load, a stored table is re-opened only if it still appears in the
authorized bootstrap list — a renamed or unauthorized table is dropped rather
than guessed. Compact breakpoints still collapse the grid and hide the
splitters, so the DPR-2 single-column assertion is unchanged.

### Dedicated JSON Explorer

The bounded JSON cell inspector is now the first dedicated native explorer.
Its tree nodes are selectable, including array elements, and every selection
shows the exact NextSQL-native path rooted at the result column. Generated
paths quote every identifier segment and leave array positions numeric, for
example `"metadata"."tags".0."sku"`; the exact quoted-array shape is pinned
against `internal/sql/parser`, including the array-index parser fix in
`TODO.md` log #158. The raw JSON tab remains byte-for-byte unchanged.

When the operator has selected the source table in Database explorer and the
result column is one of that table's authorized `JSON` columns, the explorer
compares the selected path with the already-loaded `system.indexes` result.
It names every matching valid index and reports non-valid index state rather
than flattening it into a success. `system.indexes.columns` currently exposes
paths as unquoted dotted text without segment-boundary metadata, so a path
containing a quoted/special object key is deliberately reported as ambiguous
instead of guessed. This is a fail-closed UI limitation, not a catalog claim.

The selected native path can be copied or inserted into the active editor as
`SELECT <path> FROM <table> LIMIT 100`. Insert only edits the current tab; it
does not execute or analyze the statement. Any later run follows the existing
authenticated NSQL path, confirmation logic, query budgets, and server-side
RBAC. There is no new Admin HTTP route, direct catalog read, or hidden query.

### Dedicated Full-text Explorer

The Basic Full-text Explorer is the second dedicated native explorer. It is
enabled only when the connected server's authoritative
`system.capabilities` row reports `fulltext` support. Its table selector uses
the bootstrap's bounded, authorized `system.tables` rows; selecting a table
loads that same existing Studio detail endpoint and derives searchable
`STRING`/`TEXT` columns, primary-key context, and full-text indexes from the
RBAC-filtered `system.columns` / `system.indexes` results. No private catalog
read or new Admin route exists.

Selecting a valid full-text index locks the generated `SEARCH` field list to
that index's exact catalog order. This is not an index hint: NextSQL has no
full-text hint syntax, so the UI calls it a candidate and tells the operator
to use `EXPLAIN` to verify the optimizer's actual access path. Automatic mode
allows one to eight visible `STRING`/`TEXT` fields and truthfully notes that
the optimizer may choose an exact matching index or the shipped sequential
fallback. Because `system.indexes.columns` is currently an unescaped
comma-delimited string, a catalog index whose field boundaries cannot be
matched exactly against the visible columns is surfaced but disabled rather
than guessed.

The builder accepts a bounded search phrase, output mode (rows,
`HIGHLIGHT`, or `SNIPPET`), and a 1–100 result limit. Every identifier is
quoted and string-literal apostrophes are doubled. `HIGHLIGHT`/`SNIPPET`
queries select primary-key context plus one aliased marked field per search
column because the current grammar does not mix `SELECT *` with extra
select-list expressions. Native phrase quotes, prefix `*`, fuzzy `~`, and
typo-tolerance input pass through unchanged for the server to validate.

Copy and Insert never execute. Run search is an explicit action using the
existing bounded streaming query path, cancellation, history, authenticated
driver connection, and server-side RBAC. Results add an ordinal “BM25 rank”
column derived only from the server's documented score-descending result
order; `#1` means the first/highest-ranked returned row. The UI explicitly
states that the current result schema exposes no numeric BM25 score and never
invents one. Server `HIGHLIGHT`/`SNIPPET` markers remain literal result text,
not interpreted HTML.

### Dedicated Vector Explorer

The Vector Explorer is the third dedicated native explorer, enabled only when
the connected server's `system.capabilities` row reports `vector` support.
It reads the same Studio table-detail endpoint as the JSON and Full-text
Explorers: `system.columns` for `VECTOR<...>`/`BITVECTOR<...>`/
`SPARSEVECTOR<...>` columns and their declared dimensions, and
`system.indexes` for a `kind = "vector"` row naming a candidate index. A
vector index always covers exactly one column (`CREATE VECTOR INDEX`
requires it), so an empty or comma-containing catalog column field is
unconditionally unrepresentable and marked unusable rather than guessed —
the same fail-closed posture as the Full-text Explorer's comma-delimited
field list, just with no valid multi-column case to parse. `system.indexes`
reports every vector index algorithm (HNSW, IVF, IVFPQ, SPARSE) under one
undifferentiated `"vector"` kind with no algorithm/quantization detail, so
the explorer only ever states "a vector index exists" and its status, and
points to `EXPLAIN` for the actual access path and algorithm.

The metric selector offers exactly the column-kind-valid subset: `COSINE`,
`L2`, and `INNER_PRODUCT` for a dense `VECTOR` column; `HAMMING` only for
`BITVECTOR`; `COSINE` and `INNER_PRODUCT` for `SPARSEVECTOR`. This was
verified against a real running `nextsqld`, not just the docs: each of the
three metric/column-kind mismatches the explorer prevents client-side
(`HAMMING` on dense, `L2` on sparse, `COSINE` on a bit vector) is rejected
server-side with `invalid_argument`, confirming the two layers agree. The
query-vector textarea accepts `[...]`, `(...)`, or a bare comma list and
normalizes to the one native literal grammar all three column kinds share —
a parenthesized tuple (`NEAREST col TO (v1, v2, ...)`; a `SPARSEVECTOR`
column accepts the same dense form and drops zeros server-side) — never a
friendlier shape the parser doesn't actually accept. A live "Vector
inspector" panel parses the same input the query builder will use and shows
value count, L2 norm, and non-zero count, surfacing a declared-dimension or
`BITVECTOR` 0/1-domain mismatch before Run/Insert are reachable.

NSQL has no per-query HNSW/IVF/IVFPQ search-time tuning setting: `NEAREST col
TO <expr> [USING metric]` is the entire query-time grammar (verified by
reading `internal/sql/parser`'s `nearestClause`); `WITH (QUANTIZATION = ...)`
and `WITH (LISTS = ..., PROBES = ...)` are `CREATE VECTOR INDEX` build-time
options only. The explorer says so explicitly instead of offering a control
for a setting that does not exist anywhere in the engine.

Copy and Insert never execute. Run search reuses the exact same bounded
streaming query path, cancellation, history, authenticated driver
connection, and server-side RBAC as every other Studio action — with no
special result context, since a `NEAREST` result carries no analogous rank
value the way a `SEARCH` result's BM25 order does.

### Dedicated Hybrid Explorer

The Hybrid Explorer is the fourth dedicated native explorer, enabled only
when the connected server reports both `fulltext` and `vector` support. It
does not read any new catalog surface: `hybridCatalog(detail)` returns the
Full-text and Vector Explorers' own `fullTextCatalog(detail)` and
`vectorCatalog(detail)` results unchanged, plus a filterable-column list
derived from `system.columns` for the structured-filter side. A table
eligible for hybrid search is exactly a table eligible for both standalone
explorers.

The structured filter is a single optional `column operator value`
condition — `=`, `<>`, `<`, `<=`, `>`, `>=`, `IS NULL`, `IS NOT NULL`.
NextSQL has no `LIKE`/regex operator at all (checked directly against
`internal/sql/lexer`'s keyword table), so none is offered; pattern matching
over text is SEARCH's job. Only columns whose type coerces cleanly from a
bare literal are filterable: exact/unsigned integers and `DECIMAL` take a
raw numeric literal, and `STRING`/`TEXT`/`UUID`/`TIMESTAMPTZ` take a quoted
string literal — both directions verified against a real running `nextsqld`
(including confirming NextSQL has no `BOOL`/`BOOLEAN` scalar type at all, so
no boolean branch exists to filter on). `BLOB`, `JSON`, every vector column,
every geo type, and every nested collection column are excluded rather than
guessed at a coercion that might not hold. Leaving the filter column
unselected is a complete hybrid query (SEARCH+NEAREST only), not an
incomplete one.

The full-text and vector sides reuse the exact same field/index/phrase and
metric/vector validation the two standalone explorers already apply — not a
re-derivation of it — then compose one statement in the exact clause order
`docs/vector.md`/`docs/optimizer.md` document ("SEARCH + NEAREST, with or
without WHERE, is planned as one hybrid problem"), verified against a real
running `nextsqld` before the builder was written: `[WHERE <filter>] SEARCH
<fields> FOR '<phrase>' NEAREST <col> TO (<vector>) USING <metric> LIMIT
<n>`. A hybrid query missing either its full-text or its vector input is
rejected outright — that degraded single-retriever form is already better
served by opening the dedicated Full-text or Vector Explorer instead.

"Execution plan" and "Candidate counts where available" needed no new
rendering code. A live hybrid `EXPLAIN ANALYZE` produces the exact same
operator/estimates/actuals/time/cpu/memory/disk/cache/spill/workers/index
column shape the existing graphical EXPLAIN tree (log #153) already parses,
and a `Candidates` node's own `rows=N` estimate/actual figure **is** the
candidate count — there is no separate number to extract. The explorer's
"Explain" action simply runs `EXPLAIN ANALYZE <generated SQL>` through the
same bounded stream as Run, and `ResultGrid` renders the tree automatically
because the result columns match its existing detection, the same as typing
`EXPLAIN ANALYZE` in the editor directly would.

Copy and Insert never execute. Run search and Explain both reuse the exact
same bounded streaming query path, cancellation, history, authenticated
driver connection, and server-side RBAC as every other Studio action. A
`Run search` result shows a truthful "Hybrid rank" (not "BM25 rank") ordinal
column, stating that NextSQL's reciprocal-rank-fusion score is not exposed
as a single number by the current result schema — `ResultGrid`'s previous
full-text-only `fullTextContext`/`showBM25Rank` were generalized into a
`rankContext`/`showRank` pair driven by a `FullTextResultContext |
HybridResultContext` union, rather than duplicating the rank-column
rendering for a second context shape.

### Dedicated Geo Explorer

The Geo Explorer is the fifth and final of the originally scoped native
explorers, enabled only when the connected server reports `geo` support. It
is the only one to require its own interactive drawing surface — a fixed
equirectangular world-extent SVG canvas (lon -180..180 across the width, lat
90..-90 down the height; not a zoomable/pannable map, no tile fetches, no
CDN dependency) that the user clicks to place a point or add a polygon
vertex. That canvas is `aria-hidden` and excluded from the accessibility
tree: every coordinate it can set is also reachable through ordinary
labeled, keyboard-operable Longitude/Latitude/Radius number fields and
Add/Remove-vertex buttons rendered alongside it, so nothing is lost by
excluding a click-drag surface that has no complete keyboard equivalent of
its own. `geoCatalog(detail)` mirrors the Vector Explorer's catalog shape:
`system.columns` is the authority for eligible columns, restricted to
`POINT` (`LOCATION` is a storage-level alias the catalog always reports back
as `POINT`, per `internal/sql/types/types.go`'s `Type.String`), and
`system.indexes` is the authority for a candidate `spatial`-kind index,
unusable when its catalog column field is empty or ambiguous — the same
single-column-only reasoning the Vector Explorer already applies, since
`docs/geo.md`'s `CREATE SPATIAL INDEX` also requires exactly one column.

Only `POINT` columns are offered as the searched column, deliberately
narrower than every geometry pair `DWITHIN` itself accepts. This is not an
arbitrary restriction: `WITHIN`'s first argument must be a `POINT` (verified
against `EvalGeo` in `internal/sql/types/geo.go`), so restricting the
explorer's one offered column kind to `POINT` keeps both of its two query
shapes valid for every table it lists, and keeps every generated query
spatial-index-eligible.

Two query shapes, chosen by a plain `Select` (no custom segmented control
needed):

- **Point + radius** generates `WHERE DWITHIN(<col>, POINT(<lon>, <lat>),
  <meters>)`. The draw canvas overlays an approximate radius circle
  (meters-to-degrees via a local equirectangular approximation, explicitly
  disclaimed as schematic — never sent to the server, which evaluates
  DWITHIN's own true haversine/geodesic distance).
- **Polygon** generates `WHERE WITHIN(<col>, POLYGON(<wkt>))` over an
  auto-closed ring (the first vertex is repeated to close it, matching
  `docs/geo.md`: "each ring is closed"), bounded to 64 drawn vertices — well
  under the server's 256-total-vertex storage cap once closed.

`SELECT *` cannot be mixed with an extra computed column in this grammar
(checked against `internal/sql/parser`'s `sel()`, which only ever parses a
bare `*` or a full item list, never both), so no computed distance column is
added alongside the star projection.

The `POINT(lon, lat)` and `POLYGON(wkt)` SQL function-call literals this
explorer generates were checked line-for-line against
`internal/sql/types/geo.go`'s `EvalGeo`/`ParseWKT` before being written, not
assumed from `docs/geo.md` alone — this session's practice since log #158
found a case where a documented native-JSON syntax had in fact never worked.
Here the doc and the parser agreed exactly, including the WKT ring-list
string shape `POLYGON('((lon lat, ...))')` (`LINESTRING`/`POLYGON` take one
quoted WKT string argument, not bare numeric arguments like `POINT`/`BOX`
do).

Once a point or a ≥3-vertex polygon is valid, the exact same
`internal/admin/frontend/src/studio/CellInspector.tsx` `GeoView` renderer
used for an inspected result cell is reused unchanged for a zoomed
confirmation preview (its own bounds-fit projection, distinct from the draw
canvas's fixed world extent) — fed the equivalent WKT text
(`POINT(lon lat)`/`POLYGON((lon lat, ...))`), never the comma-separated SQL
literal, so the reused parser (`parseGeo`) sees exactly the shape it already
expects. Reusing `GeoView` here surfaced a real, pre-existing accessibility
defect: its raw-value `CodeBlock` had no focusable scroll wrapper, so a wide
literal (a multi-vertex polygon, easily wider than a short inspected
`POINT`) failed axe's `scrollable-region-focusable` check. Fixed at the
source in `CellInspector.tsx`/`studio.css` for all four of its `CodeBlock`
call sites (JSON raw view, geo fallback/normal views, generic text
fallback) — the same focusable/labeled-wrapper pattern
`.nss-vector-sql-preview`/`.nss-fulltext-sql-preview` already used, not a
Geo-Explorer-local workaround.

Copy, Insert, and Run follow the exact same contract as every other
explorer: Copy/Insert never execute; Run reuses the ordinary bounded
streaming query path, cancellation, history, and server-side RBAC.

### Confirm-before-run safety check

Before an editor statement runs (button click or Ctrl/Cmd+Enter), the browser
calls `POST /api/v1/studio/query/analyze` with the SQL text. Admin parses it
with `internal/sql/parser` — the same grammar `nextsqld`'s own executor
binds — and classifies it with `internal/admin/studio.Analyze`/`classify`; it
never opens a database connection. A statement is flagged destructive when it
is `UPDATE`/`DELETE` with no `WHERE` clause, `DROP TABLE`/`DROP INDEX`/`DROP
USER`/`DROP ROLE`/`DROP WORKFLOW`/`DROP TRIGGER`/`DROP SCHEDULE`/`DROP
RESOURCE GROUP`, or `ALTER TABLE ... DROP COLUMN`/`DROP PARTITION`. A flagged
statement shows a confirmation naming the reason (e.g. the table it would
empty) before `handleStudioQueryStream` is ever called; canceling runs
nothing. Analysis is advisory only: a parse failure or any other analyze
error is not surfaced as a warning-dialog block — the statement still runs
and the real executor remains the sole authority on validity/authorization/
effect, exactly as before this check existed.

The same parsed-AST analysis now returns `realm_scoped=true` for `CREATE USER`,
`DROP USER`, `CREATE ROLE`, and `DROP ROLE`. NextSQL's accepted hosting model
defines users and roles once per realm while grants carry database scope
(`docs/design-multidatabase-dbaas.md` §19), so those four statements can affect
access across every database in the connected realm even though Studio is
pointed at one database. Their confirmation therefore names both the connected
realm and database and explains the wider reach. `GRANT`/`REVOKE` are excluded:
their database/object scope is explicit, and the existing builder shows it.
There are no `ALTER USER`/`ALTER ROLE` statements in the current AST. This flag
is advisory like `destructive` and `write`; nextsqld's RBAC remains the sole
authority. A consolidated script warning includes realm-scoped statements too,
and a destructive realm operation retains both reasons rather than one hiding
the other.

### Switch realm / database (connection re-targeting)

The first true connection-manager capability: a Studio session can move its
NSQL connection to a different **realm and/or database** on the same
`nextsqld`, without signing out and back in. NextSQL binds the realm and
database at handshake time only (`drivers/go`'s `Config.Realm`/`Database`
are read once, in `handshake`), so a switch is a fresh authenticated
connection, not an in-band command — hence the password field. Studio holds
no credential of its own, so the password is supplied each time, used once
to open the new connection, and never stored anywhere.

* **Route** — `POST /api/v1/studio/reconnect`, CSRF-protected like every
  other state-changing Studio route. Body `{realm, database, password}`;
  `studio.ReconnectRequest.Validate` bounds each name to
  `MaxConnNameBytes` (128) and, when non-empty, to the bare-identifier
  shape NextSQL's Hello accepts (an empty name selects the deployment
  default). The handler rebuilds the driver config from the admin process's
  fixed host/TLS settings, sets the same NSQL user, opens the new
  connection with a 15 s timeout, and only on success calls
  `session.reconnect`.
* **Atomic, fail-safe swap** — `session.reconnect` takes the connection
  lock with `TryLock`: a switch cannot race an in-flight editor query
  (`nerr.Conflict` / HTTP 409 if one is running). It installs the new
  `*nextsql.Conn`, updates the session's `realm`/`database` under
  `stateMu`, then closes the old connection. A failed open (wrong password
  → 401, unknown realm, suspended database) never reaches `session.reconnect`,
  so the session stays exactly as it was — verified end to end by
  `TestAdminStudioWorkspaceOverNSQL` (a wrong-password reconnect returns
  401 and a follow-up `SELECT 1` on the same session still succeeds).
* **Live reachability** — Studio's toolbar **Connected** badge is not
  decorative. Ops and Studio share `GET /api/v1/connection`, which pings
  the session's official-driver socket (`SELECT 1`, 3 s timeout, skip if a
  query already holds the connection). The admin cookie can outlive a dead
  nextsqld process; a failed probe returns HTTP 200 `{connected:false}` so
  the operator is not signed out, the badge flips to **Disconnected**, and
  Run is disabled until the probe succeeds again (5 s poll while the tab is
  visible, plus Retry). `GET /api/v1/session` still does not touch
  nextsqld — CSRF recovery must not depend on the database being up.
* **Client** — the toolbar shows the target `nextsqld` address (new
  `Bootstrap.server_addr`, display only) next to the user/realm/database
  label, and a **Switch connection…** button opens `SwitchConnection.tsx`
  (a `Modal` form: realm, database, password). On success the new
  realm/database flow up through `onConnectionChanged` → `ops/App.tsx`'s
  `who`, which re-keys the per-connection `localStorage` scopes (editor
  drafts, saved queries, environment tag) and, via the Studio remount key,
  reloads the workspace against the new connection. Unsaved editor buffers
  for the previous connection remain in that connection's own crash-recovery
  `localStorage` slot.
* **Recent connections** — a successful switch records the `{realm,
  database}` pair (never a credential) to `localStorage`, keyed per
  `nextsqld` host + NSQL user (`recentConnectionStorageKey`). The
  Switch-connection modal shows the most-recent pairs (bounded at
  `MAX_RECENT_CONNECTIONS` = 10) as quick-fill buttons — clicking one
  populates the realm/database fields and focuses the password, which is
  still required. `parseRecentConnections` / `recordRecentConnection` /
  `serializeRecentConnections` are pure and bounded (malformed → empty,
  the all-default pair is not recorded, over-long names dropped), and the
  write is synchronous in the switch handler because a successful switch
  immediately remounts the workspace.
* **Still out of scope** (needs the multi-target model proper): named
  connection *profiles*, a different host/port, TLS/mTLS field entry,
  OS-keychain credential storage, and a full recent-connections *home
  screen*. `studio.Bootstrap` still carries only the one process-managed
  host; this slice re-targets *within* it.

### Read-consistency mode

A Studio session can choose how its **reads** observe replicated state —
`STRONG` (default; every acknowledged write, leader-served behind a Raft
read barrier), `BOUNDED` (any member within a staleness bound), or `STALE`
(local applied state, no bound). Unlike the realm/database switch this is
**not** a reconnect: the official driver's `Conn.SetReadConsistency` is a
live session-control frame on the existing connection, so
`POST /api/v1/studio/read-consistency` (`{mode, max_staleness_ms}`,
CSRF-protected) just forwards it. `session.setReadConsistency` takes the
connection lock with `TryLock` (a mode change cannot race an in-flight
query → `409`) and records the mode only after the wire call succeeds.
`studio.SetReadConsistencyRequest.Validate` accepts the three modes and a
`0…MaxReadStalenessMS` (1 hour) bound; a non-`BOUNDED` mode drops the bound.

The mode is **per session, reads only** — writes always go to the leader,
and nextsqld remains the authority over whether a routed read is actually
served from a follower (a single-node deployment simply records the mode).
A fresh connection is `STRONG`, so `session.reconnect` resets it and the
bootstrap read reports the current `read_consistency` / `max_staleness_ms`.
The toolbar shows a `Select` (Strong / Bounded / Stale), a seconds input
that appears for Bounded, and — whenever the mode is not Strong — a
`warning` badge (`"bounded reads"` / `"stale reads"`) so a stale result is
never silently presented as authoritative.

### Production environment tag and read-only safety mode

Studio has no multi-target connection *profile* model yet — it always runs
on the single `nextsqld` the Operations-mode login reached (the realm and
database within it are switchable — see the section above). What it can do,
as another connection-manager slice, is let the operator **label** that
connection's environment. `resultTools.ts`'s `environmentStorageKey(realm,
database, user)` picks a `localStorage` slot; the tag is one of
`development` / `test` / `staging` / `production`, a per-viewer browser
preference — never sent anywhere, and not a credential (the design's
"no credential storage" boundary is intact). Every storage access is
try/caught: a private window or blocked site data just means "no tag",
which is the safe default.

A `production` tag renders a standing `role="alert"` banner and turns on
**read-only mode** (the "optional read-only production session default" —
the user can toggle it off for the session). To make read-only mode
possible, `Analyze` now also returns a `write` flag: `studio.go`'s
`isWriteStmt` mirrors `nextsqld`'s own `executor.isMutating` at the AST
level (DML, DDL, workflow/trigger/schedule/resource-group and index DDL,
`BEGIN`/`COMMIT`/`ROLLBACK`) plus the operational writes gated elsewhere
(`SET CONFIG`, `BACKUP DATABASE`, `MAINTAIN`, cluster control). `EXPLAIN`,
`ANALYZE`, `VERIFY BACKUP`, `SHOW`, `SUBSCRIBE`, and `SET/RESET RESOURCE
GROUP` are reads. While read-only mode is on, the pre-run confirm gate
widens from "destructive" to "destructive **or** a write" — every write
asks for confirmation first, individually overridable with "Run anyway",
and a script's read-only-blocked writes fold into the same one
consolidated pre-run warning. This is advisory UX, exactly like the
destructive check: server-side RBAC (test-covered per §2) is the real
protection.

### Execute selection

The editor's Run action (button or Ctrl/Cmd+Enter) runs the browser's current
`<textarea>` selection instead of the whole buffer whenever the selection is
non-empty after trimming; `selectionStart`/`selectionEnd` are read directly
from the DOM element rather than from reactive state, so the exact text
highlighted at the moment Run is invoked is what gets analyzed and executed,
even though clicking Run itself moves focus away from the editor. The
confirm-before-run check above runs against the same selected substring, and
a destructive selection's classification is captured alongside the substring
so confirming later runs precisely what was analyzed regardless of any
buffer edits made while the confirmation is open. This lets a user draft
several candidate statements in one editor buffer and run exactly one without
the single-statement-per-request server contract changing at all.

### Query history with privacy controls

Every statement run through the editor (whole buffer or selection) is
recorded client-side once it settles: success (with row count/elapsed read
from the stream's own completion frame), cancellation, or error. A canceled
confirm-before-run dialog records nothing, since no query ever ran. The list
lives only in React state — never `localStorage`/`sessionStorage`/disk,
never sent anywhere — so it disappears on reload or navigating away from
Studio; an explicit "Clear" action empties it outright, and it is bounded at
50 entries / 2 MiB of combined text regardless, evicting oldest first. A
"History" popover trigger next to Run/Cancel opens the list (most recent
first); clicking an entry loads its SQL back into the editor via `setSQL`
without re-running it, so a past destructive statement still has to pass the
confirm-before-run check again on its own merits.

### Crash recovery for unsaved editors

The editor tab buffers — **only** each tab's title and SQL text, never its
results, errors, plan baselines or history — are mirrored to `localStorage`
(debounced 500 ms) under a per-connection key
(`editorDraftStorageKey(realm, database, user)`, the same sanitized scoping
as the environment tag). On load, `parseEditorDrafts` rehydrates them into
the initial tab state so a browser crash, an accidental close, or a reload
does not lose unsaved work. The codec is pure and bounded: at most 8 tabs,
200,000 chars per buffer, oversized content truncated rather than rejected,
malformed JSON treated as "no draft". Every storage access is wrapped in
try/catch — a private window, blocked site data, or a quota error simply
means no crash recovery, never a broken editor.

This is a deliberate, narrow exception to the "query history never touches
disk" rule above: history is an automatic log of *everything* run (the
sensitive surface), whereas crash recovery persists only the small working
buffer whose loss is the exact harm the feature exists to prevent — the same
reasoning that will apply to explicitly *saved* queries. A `role="status"`
notice on rehydration says the tabs were restored from this browser and
offers **Start fresh**, which clears the stored draft and resets to one
default tab. `editorDraftsWorthRestoring` suppresses the notice when the
stored state is just the pristine default buffer, so a fresh session never
sees it.

### Saved queries (folders via tags)

A "Saved" popover next to History keeps named, tag-grouped SQL snippets the
operator explicitly chooses to keep — mirrored to `localStorage`
(`savedQueryStorageKey(realm, database, user)`, the same per-connection
scoping and "your browser only, never sent anywhere" property as crash
recovery). A **folder is just a tag**: the panel has a tag `Select` that
filters the list, and a free-text filter over name and SQL body. Each entry
can be loaded into the active tab (via `setSQL`, without running — a
destructive one still faces confirm-before-run), have its stored SQL
overwritten with the current buffer (**Update**), renamed inline, or deleted.

`serializeSavedQueries`/`parseSavedQueries`/`upsertSavedQuery`/
`removeSavedQuery`/`filterSavedQueries`/`savedQueryTags` are pure and bounded:
at most 200 entries, 200,000 chars of SQL and 200 chars of name per entry, 10
tags per entry (comma-split, trimmed, deduped, sorted), malformed storage
treated as an empty list. `upsertSavedQuery` keys on entry id and re-sorts
most-recently-updated first. Every storage access is wrapped in try/catch.

**Git-friendly file export / import.** The panel footer has **Export…**
and **Import…**. Export writes the set through `exportSavedQueries` —
`{ format: "nextsql-studio-saved-queries-v1", queries: [...] }`,
entries ordered by name then id, two-space indent, trailing newline — so
the *output is byte-identical regardless of save order* and a set checked
into a repo produces a readable diff. It downloads as
`nextsql-studio-saved-queries-<date>.json` via an object-URL anchor (the
same mechanism as result export). Import reads a chosen file (`FileReader`,
rejected above `MAX_SAVED_QUERY_IMPORT_BYTES` = 48 MiB) and runs
`parseSavedQueriesExport` (accepts the export wrapper *or* a bare array;
same per-entry bounds and id-dedupe as `parseSavedQueries`; anything
unparseable or missing `id`+`sql` is dropped) then `mergeSavedQueries`:
a **merge by id** — a new id is added, a matching id is replaced only when
the incoming entry is at least as new *and* its content actually differs,
and an older-or-identical duplicate is left alone. So re-importing an
older copy never clobbers a local edit. The result is recency-ordered and
capped at 200 like the live list, and a `role="status"` notice reports the
added / updated / unchanged counts. All four helpers are pure; the hidden
`<input type="file">` lives in the workspace, not the popover, so the OS
file dialog closing the popover cannot interrupt the read.

### Graphical EXPLAIN / EXPLAIN ANALYZE

`EXPLAIN`'s existing structured result (`internal/sql/optimizer.ExplainColumns`:
`operator, estimates, actuals, time, cpu, memory, disk, cache, spill, workers,
index`) is already the whole plan tree, preorder-encoded as one row per
operator with the operator cell's own leading whitespace as its indentation
(2 spaces per depth). The browser detects this exact 11-column shape (never
inferred from column count alone), reconstructs the tree from that
indentation, and renders it as nested cards instead of a flat grid — a
`Plan`/`Table` toggle switches back to the ordinary grid, which still has
every M1–M3 copy/export/inspect tool. Every node always shows its estimated
rows; a statement run as plain `EXPLAIN` shows one explicit "Estimates
only… nothing below is a measurement" line and nothing else, while
`EXPLAIN ANALYZE` additionally shows actual rows and CPU/memory/disk/cache/
spill/workers/index — measured fields render only when the statement was
actually analyzed, never guessed from the estimate. A node whose actual row
count is an order of magnitude off its estimate (≥10x either direction) is
flagged as an error, ≥3x as a warning.

### Plan comparison

An EXPLAIN result can be pinned as one baseline on its owning editor tab and
compared with a later EXPLAIN or EXPLAIN ANALYZE result on that same tab. The
baseline is deep-copied rather than retaining aliases to the rendered tree,
survives reruns and tab switches, and disappears when it is explicitly cleared
or its tab is closed. It is never inherited by a newly created tab.

Comparison aligns operators only by their deterministic structural path in
the preorder tree (`1`, `1.1`, `1.1.1`, and so on). A label or index change at
the same path is reported as an operator change; changed server-reported
estimate/resource values are reported as a metric change; unmatched paths are
added or removed. It deliberately does not guess semantic operator identity
across reordered or reshaped trees. The view retains both raw server values,
and actual/resource fields are called measured only for a snapshot captured
from `EXPLAIN ANALYZE`; a mixed analyzed/estimate-only comparison says so.

Each baseline and current comparison capture is capped at 512 operators. With
the existing eight-tab cap, the browser can retain at most eight baselines,
and none is persisted, sent to the server, or added to the PWA cache.

### Query profiler breakdown

An `EXPLAIN ANALYZE` result also offers a `Profile` view. A pure browser model
walks at most 512 operators in structural-path order and preserves the raw
server fields for every node. It adds only derivations supported by those
fields: the existing 3×/10× estimated-versus-actual row severity, the symmetric
error factor and direction, root-reported time, longest non-root reported time,
peak reported memory/spill, and maximum reported workers. The per-operator
table keeps time, CPU, memory, disk, cache, spill, workers, and index visible.
A plain `EXPLAIN` cannot enter this view because it has no measured actual-row
signal.

This is intentionally not a sampled timeline or an additive flame graph. The
executor currently times some operators around their whole input subtree, can
copy elapsed time into the CPU field, and can put peak query memory/spill on the
root; disk/cache stay zero wherever the executor does not attribute them to an
operator. Consequently Studio calls these values server-reported, not
exclusive, never sums them, never calculates percentages, and does not treat a
zero disk/cache counter as proof that no I/O occurred. The exact duration parser
accepts only the optimizer's integer `ns`/`µs`/`ms`/`s` format for selecting
reported maxima; the original strings remain the displayed authority.

### Multi-tab editing

Each tab is an independent SQL buffer plus its own last result/error, bounded
at 8 tabs; a single mounted editor/result pair is rebound to whichever tab is
active rather than mounting one editor per tab. Running state stays
session-wide, not per-tab, because the server allows only one active Studio
query per session — a second attempt fails fast with `409`. A tab other than
the one actually running shows a disabled "Busy…" Run action and an inline
notice naming which tab is running, so that real constraint is surfaced
honestly instead of ever producing a `409` the user wouldn't understand; a
running tab cannot be closed. `executeQuery`/`requestRun`/`confirmPendingRun`
all carry an explicit tab id captured at the moment Run was invoked or a
destructive statement confirmed, so results land back on the tab that
actually ran even if the user switches tabs while it (or its confirmation
dialog) is still pending. The tab strip is a small hand-rolled widget (plain
buttons with `aria-pressed`, matching this file's existing
`nss-object-button`/`nss-history-item` pattern) rather than `@bzync/rui`'s
`Tabs`, whose keyboard-navigation logic expects only `role="tab"` children
and renders its own `<button>` — incompatible with nesting a second `<button>`
for a close control.

### Execute script

`POST /api/v1/studio/query/split` tokenizes an editor buffer into individually
runnable statements using a real lexer pass over the same tokenizer the
parser itself runs on — a `;` inside a string, quoted identifier, or comment
never cuts a statement early, and comment-only/empty fragments are dropped.
`internal/admin/studio.SplitScript` deliberately re-implements
`internal/migrate.Split`'s three-line splitting loop rather than importing
that package: `internal/migrate` transitively pulls in `internal/catalog`,
`internal/security`, and `internal/cli`, none of which belong in Studio's
official-interfaces-only dependency footprint (enforced by
`imports_test.go`). The split endpoint never opens a driver connection or
touches the session's query slot — same trust level as `.../query/analyze`.
A script is capped at `MaxScriptStatements` (200) statements and the existing
`MaxSQLBytes` (1 MiB) buffer limit.

The browser analyzes every split statement (reusing the same
confirm-before-run classifier) before running anything, and shows one
consolidated confirmation naming how many statements are flagged as
destructive, realm-wide, or (when enabled) writes under read-only mode, plus a
bounded list of reasons — not a dialog interrupting the script partway
through. A statement that belongs to more than one category retains every
applicable reason. Once confirmed (or immediately, if nothing is flagged),
statements run one at a time through the existing bounded non-streaming
`POST /api/v1/studio/query` path (each one's result is small enough to keep
whole; the streaming grid exists for one large result, not many small ones),
stopping at the first failed or canceled statement rather than continuing
past one that didn't do what the script expected. Each statement's outcome
(`pending`/`running`/`success`/`error`/`canceled`/`skipped`) is tracked
per-tab alongside its own result, selectable from a script-results list;
selecting a row shows that statement's own `ResultGrid`. Cancellation reuses
the same per-session `query_id`/cancel plumbing as a single statement, plus a
client-side abort flag that stops the loop from starting the next statement.
Every statement result and outcome is also recorded into query history, the
same as single-statement execution.

**Real bug found and fixed while implementing this**: the non-streaming
`POST /api/v1/studio/query` JSON response reports a DDL/DML statement's
column metadata as JSON `null` (no result set), not an empty array — visible
directly from a live `nextsqld`/`nextsql-admin` instance via `curl`. The
streaming transport (used by every prior single-statement Run) already
coalesces this (`frame.columns ?? []` in `acceptLine`), but this endpoint had
never been wired into the frontend before Execute Script, so nothing had hit
the gap. `ResultGrid`'s `result.columns.length === 0` check crashed the whole
Studio workspace (a blank page, confirmed via the browser console) the first
time a script contained a non-`SELECT` statement. Fixed by normalizing
`columns`/`column_types`/`rows` from `null` to `[]` in the `api.studioQuery`
client wrapper (`normalizeStudioResult`), so every caller of that function
only ever sees a null-safe `StudioResultSet`. Covered by a
`test-accessibility.mjs` fixture that deliberately returns `columns: null`
for a non-`SELECT` script statement and asserts the workspace stays mounted
after selecting that statement's result row; reverting the fix reproduces
the exact failure (verified before restoring it).

### Prepared parameters

NextSQL binds **positional** parameters (`$1..$N`). When the active buffer
references any, `extractQueryParams` (a plain regex scan, same heuristic
level as the FROM/JOIN table scan — a `$n` adjacent to an identifier
character is ignored, one inside a string literal is not) surfaces a
**Parameters** panel below the editor: one text `Input` per distinct
referenced number, each with a **NULL** checkbox that disables its input.
Bind values are per-tab, ephemeral React state — they are never written to
`localStorage` (the editor drafts persist SQL text only) and never sent
anywhere except with an explicit Run.

On Run (single-statement path only — not native explorers, whose generated
SQL has no placeholders, and not Execute Script), the editor builds a
**dense** positional array up to the highest referenced number: a
referenced-but-untouched slot sends `""`, a `$2`-with-no-`$1` gap still
sends a slot for `$1`. Each slot is `{ "value": string }` or, for a ticked
NULL, `{ "value": null }`. The request travels on the existing
`params` field of `POST /api/v1/studio/query{,/stream}`.

Server-side, `studio.ValidateParams` bounds the set (`MaxQueryParams` = 32,
`MaxQueryParamBytes` = 64 KiB per present value; `413` past either), then
`studio.ParamValues` converts each slot to a driver `types.Value`: a
present value becomes `types.StringValue` and is **coerced against the
statement's expected parameter type by nextsqld's own binder** — the same
untyped-string binding path `internal/executor`'s own tests exercise (a
`$1` string binding an `INT64` key, a `BLOB` column, etc.); a null slot
becomes `types.Null(types.NullType())`, a typed SQL NULL. Studio never
attempts client-side type inference — there is no safe way to tell an
integer literal from a string that looks like one without the plan, and
the server already does this correctly. The confirm-before-run dialog
carries the captured params through unchanged, so a destructive
parameterized statement still runs with exactly the values shown when it
was analyzed.

### Find/replace

A "Find" toolbar button (and Ctrl/Cmd+F while focus is inside the editor)
opens a panel with Find, Match case, Previous/Next, Replace, and Replace all
— operating only on the active tab's own SQL buffer, never the database, so
it needs no confirm-before-run check of its own. Matching is an ordinary
literal substring search (`internal/admin/frontend/src/studio/resultTools.ts`'s
`findAllMatches`), never a regex, so a find query containing regex
metacharacters is never misinterpreted as a pattern.

Navigation (`nextMatchIndex`/`previousMatchIndex`) always wraps: Next past
the last match returns to the first, Previous past the first returns to the
last. A match is shown by moving the real textarea's native selection to it
(`textarea.setSelectionRange`) — no separate highlight overlay is drawn, so
this hits none of the "no highlight-overlay primitive" blockers that ruled
out syntax highlighting (log #154) and formatting; the browser's own
selection rendering is enough. Replace acts on the current selection only
when it exactly matches one of the live matches (mirroring common editor
"Replace" behavior); otherwise it just finds the next occurrence first,
requiring a second click to actually replace, rather than guessing which
occurrence the user meant. A single Replace edits the textarea through the
same native-setter-plus-`"input"`-event path the `CodeEditor`'s own
`onChange` contract expects (the same technique `test-accessibility.mjs`
already uses to script input), so `StudioTab.sql` stays in sync exactly as
if the user had typed the replacement; Replace all instead computes the
whole substitution in one pass (`replaceAllMatches`, iterating the
precomputed match list rather than re-scanning the mutated string, so a
replacement that itself contains the search text can never cause runaway
re-matching) and applies it through the ordinary `setSQL` path.

### GRANT/REVOKE builder

A form-driven generator for `GRANT`/`REVOKE` statements (Developer operations
scope), reachable from **More → Grant / Revoke…** (the editor toolbar's
three-dot overflow, next to Run/Run script/History). It is entirely
client-side and adds **no new server route**:
it only generates SQL text and inserts it into the active editor tab via the
same `setSQL` path `insertTableQuery` already uses — it never executes or
analyzes anything itself, so the existing confirm-before-run check and
server-side RBAC apply to the inserted statement exactly as they would to
hand-typed SQL.

`internal/admin/frontend/src/studio/resultTools.ts` gained the pure
generation logic (`buildGrantSQL`, `quoteIdentifier`, `GRANT_PRIVILEGES`,
`GRANT_SCOPES`, `namesFromResult`), unit-tested in `test-studio-results.mjs`;
`GrantBuilder.tsx` is the modal UI. Both statement shapes
`internal/sql/parser.grantRevoke` accepts are supported: role membership
(`GRANT|REVOKE <role> TO|FROM <grantee>`, no `ON` clause at all) and privilege
grants (`GRANT|REVOKE <priv[, priv...]|ALL PRIVILEGES> ON <scope>[ <object>]
TO|FROM <grantee>`) across every scope `scope()` accepts (`CLUSTER`,
`DATABASE [name]`, `SCHEMA name`, `TABLE name`, `COLUMN [table.]column`,
`FUNCTION name`, `RESOURCE GROUP name`, `BACKUP`, `REPLICATION`,
`ADMINISTRATION`). Every generated identifier is unconditionally rendered as
a double-quoted identifier (`""` doubles an embedded quote) rather than
validated against a "looks like a bare identifier" pattern, so a name with
whitespace, punctuation, or a reserved-word collision is exactly as safe to
interpolate as a plain one — matching how the parser's own `p.ident()`
accepts either token shape identically. Every generated shape was confirmed
directly against `internal/sql/parser` (a throwaway probe test invoking
`parser.Parse`), not assumed from reading the grammar table alone.

**Real grammar gap found while grounding this, deliberately not worked
around**: `GRANT_PRIVILEGES` omits `"alter"`. `security.PrivAlter` and
`ParsePrivilege("alter")` both exist, but the GRANT/REVOKE privilege-list
parser (`privOrIdent` in `internal/sql/parser`) only accepts a closed set of
privilege keyword tokens plus a bare-identifier fallback, and `"alter"` lexes
as its own reserved keyword (used by `ALTER TABLE`) that is not in that
accepted set — so `GRANT ALTER ON TABLE x TO y` fails to parse today even
though the privilege itself exists at the engine level. Fixing the parser is
a separate, larger, higher-blast-radius grammar change (touching
`internal/sql/parser`, not just this builder), so the builder simply never
offers "alter" as a choice — confirmed and pinned by both a parser probe and
a unit test asserting `GRANT_PRIVILEGES` excludes it — rather than generating
SQL the engine cannot parse. Every other `docs/security.md`-documented
privilege spelling (`usage`, `restore`, `cdc`, and the `all`/`admin` alias
via `ALL PRIVILEGES`) is not a reserved keyword and parses correctly through
the same fallback path; all are offered.

Grantee/role suggestions come from **the same admin-only
`system.users`/`system.roles` read Operations mode's Security view already
exposes** (`GET /api/v1/security`, reused as-is — no new Studio endpoint):
both tables return zero rows, never an error, for a non-admin caller
(`docs/system-catalog.md`), so a degraded fetch here just means no
suggestions, never a blocking error — every field stays plain free text
either way, using the browser's native `<datalist>` rather than Studio's own
result-fetching plumbing. Table-name suggestions reuse the bootstrap
explorer's already-loaded authorized table list; column names are typed
directly (no live per-table column lookup) — enriching that further belongs
with the already-deferred "Context-aware IntelliSense from live catalog"
item, not this builder.

### Users & roles privilege explorer

A read-only view (Developer operations scope), reachable from **More →
Users & roles…** (the same three-dot overflow as Grant / Revoke). Like the
GRANT/REVOKE builder, it
adds **no new server route**: it reads the same admin-only `GET
/api/v1/security` (`internal/admin/ops.handleSecurity`) that both Operations
mode's own Security view and the GRANT/REVOKE builder's grantee suggestions
already use, showing only the `users`/`roles`/`grants` sections of that
response (TLS/key-rotation/audit-log stay Operations-mode-only, out of this
explorer's scope). `system.users`, `system.roles`, and `system.grants` all
return zero rows, never an error, for a non-admin caller
(`docs/system-catalog.md`), so a non-admin connection sees empty sections
here, matching Operations mode's own convention exactly rather than a
Studio-specific one.

Each `system.grants` row's "Revoke…" action calls the new
`grantStateFromRow(grantee, privilege, scope, object)` (`resultTools.ts`) —
the exact inverse of `buildGrantSQL` — to produce a REVOKE-prefilled
`GrantBuilderState`, then opens the existing `GrantBuilder` with it via a new
optional `initial` prop (re-applied every time the modal transitions to
open, so a stale prior fill never leaks into a differently-prefilled or
unprefilled reopen). This still never executes or analyzes anything: the
builder's own "Insert into editor" is the only path that touches the active
tab, exactly as it already was for a hand-built grant.

Two facts about the persisted grant shape were confirmed directly against
`internal/security/rbac.go` and `internal/executor/security.go` before
writing the reverse mapping, not assumed from `docs/system-catalog.md`
alone:

- **`system.grants` never carries a role-membership row.** `ACL.Grant`
  dispatches `GRANT <role> TO <grantee>` (no `ON` clause) to a wholly
  separate `GrantRole`/`RevokeRole` path that lives in `system.roles`'
  `members` column instead — so every `system.grants` row is unconditionally
  mode `"privilege"` for this reverse mapping; there is no ambiguity to
  resolve.
- **`ALL PRIVILEGES` persists as the single privilege `"admin"`, not a
  synthetic flag.** `applyGrant`/`applyRevoke` expand `ALL PRIVILEGES` to
  `privs = []string{"admin"}` before ever touching the ACL, so a row with
  `privilege = "admin"` round-trips through `grantStateFromRow` as
  `privileges: ["admin"]` (already one of `GRANT_PRIVILEGES`), rendering as
  `REVOKE ADMIN ON ... FROM ...` — a real, valid, narrower statement — rather
  than reconstructing an `allPrivileges: true` flag that was never actually
  stored.

A `COLUMN`-scope object is `table.column` when the grant named a table, or a
bare column name when it was granted scope-wide — the exact shape
`ACL.allowedForLocked` itself splits on `"."` for enforcement — reversed the
same way here (`object.indexOf(".")`). An unrecognized scope spelling
(should the persisted set ever diverge from `GRANT_SCOPES`) falls back to
`"table"` rather than a builder state with no scope selected, so the form
stays usable and previews an explicit statement instead of silently
rendering nothing.

### Transaction console and Lock explorer

A read-only live view (Developer operations scope), reachable from a
"Transactions & locks…" button beside the other Studio tools. It adds **no
new server route**: it calls the same `api.activity()` / `GET /api/v1/activity`
read Operations mode's Activity page already uses. The existing 15-second
read model issues `SELECT *` against `system.sessions`,
`system.active_queries`, `system.transactions`, and `system.locks` through
the logged-in operator's official NSQL connection. The system catalog's own
RBAC filtering therefore remains the sole authority for admin-wide versus
own-session/transaction visibility; Studio does not reinterpret it.

The panel shows counts and the four result tables, surfaces partial-read
warnings returned by the bundle, and refetches on every open plus an explicit
Refresh. A load guard permits at most one activity request at a time and a
retry clears its prior error before showing the new loading state. Unlike the
security catalog, this live state is never treated as a load-once cache.

The panel deliberately has no kill, terminate, or rollback button. NextSQL
currently has no authoritative server operation for canceling or rolling back
another session's transaction; only the owning connection (or credential
revocation when appropriate) can end it. Inventing a Studio-local action
would imply authority and behavior the engine does not provide.

The realistic active-query row in the browser fixture exposed an existing
accessibility defect in the shared Operations `ResultTable`: RUI's inner
horizontal scroller could overflow without being keyboard-focusable. The
shared component now gives a labeled, focusable outer region ownership of
horizontal scrolling and disables the redundant inner overflow. Distinct
labels identify the Sessions, Active queries, Open transactions, and Held
locks tables in both Operations and Studio.

### Audit viewer

A read-only verified-tail view (Developer operations scope), reachable from an
"Audit…" toolbar button. It adds **no new server route**: it calls the same
`api.security()` / `GET /api/v1/security` bundle Operations Security already
uses and displays only its `system.audit_verify` and `system.audit_log`
results. Both tables remain admin-only at the system-catalog layer; a
non-admin receives zero rows rather than a Studio-specific authorization
interpretation.

The audit-chain card is not a copied implementation. Operations Security's
existing `AuditVerifyCard` now lives in `ops/AuditVerifyCard.tsx` and both
modes import it, keeping the chain/signature badges, line counts, first bad
line, and problem rendering identical. The recent-record table uses the same
shared focusable `ResultTable`; it does not hide entries when chain
verification fails, matching `security.TailEvents` and `system.audit_log`'s
investigation-oriented contract.

Audit refetches on every open and explicit Refresh, unlike the Users/Roles
load-once catalog view. The shared security loader now guards the builder,
Users/Roles explorer, and Audit viewer with one in-flight request maximum,
clears old errors on retry, and still surfaces the bundle's partial-read
warnings. There is deliberately no polling: although the returned tail is
hard-capped at 200 records, verifying the complete chain remains sequential
in the audit file's size. There is also no delete, clear, or repair action;
the append-only durable log and server verification remain authoritative.

### Workflows, relationships, tasks & change streams explorer

A read-only view (Workflow/CDC scope), reachable from a "Workflows & CDC…"
toolbar button. Unlike the Users & roles / Transaction console / Audit
viewers — which reuse an Operations-mode bundle — Operations mode has no
workflow view, so this adds one small dedicated route,
`GET /api/v1/studio/workflows`, built exactly like `GET /api/v1/studio/table`:
it runs `SELECT * FROM system.workflows ORDER BY name` (required),
bounded reads of `system.triggers` and `system.schedules`,
`SELECT * FROM system.tasks ORDER BY id` and
`SELECT * FROM system.change_streams ORDER BY table_name, lsn` (all four
definition/activity reads are not required — list-shaped and legitimately
empty) through the logged-in operator's own NSQL connection. Every one of the
five results is independently capped at 500 rows before it reaches the
browser. Every row carries the system catalog's own RBAC filtering: workflows and schedules use workflow
visibility; triggers use `canSeeTable` on the firing table; tasks show a
non-admin only its own tasks; and change streams use `canSeeTable` on the
subscribed table (`docs/system-catalog.md`). A non-admin therefore sees fewer
rows, never a client-side reinterpretation of that boundary.

The panel shows workflow / trigger / schedule / task / change-stream counts,
all five native result tables, and a deterministic relationship view:
`TABLE → TRIGGER → WORKFLOW` plus `SCHEDULE → WORKFLOW`. A small complete graph
is drawn as inline SVG; its concise `role=img` label and an always-visible
relationship list preserve the same information without sight. The graph is
omitted rather than drawn partially when either catalog read is truncated or
when it exceeds 60 nodes / 80 links; the combined text alternative is itself
capped at 500 relationships. The client-side "Filter by workflow" select
narrows the task table over the already-loaded result. Task and subscription
state is live, so the whole bundle refetches on every open and explicit
Refresh. A change-stream row's `lsn` is its consumer's last-observed commit
position — its resume cursor.

It is **read-only by design**, matching the other Developer-operations
explorers: workflow bodies and trigger/schedule definitions are authored
through the editor's own `CREATE`/`ALTER WORKFLOW`|`TRIGGER`|`SCHEDULE`
statements (which pass the same server RBAC), there is no cancel/retry-task
button (`CANCEL TASK` is a deliberate follow-on), and a CDC subscription is
paused/resumed only by its own consuming client — Studio is not that client.
The graph shows definition topology, not workflow statement bodies: the
current `system.workflows` contract intentionally exposes counts rather than
source text.

### Schema migration history explorer

A read-only view (Developer-operations scope), reachable from a "Migrations…"
toolbar button and the command palette. It is the first slice of the migration
workspace, and — like the Workflows explorer — Operations mode has no matching
bundle, so it adds one small dedicated route, `GET /api/v1/studio/migrations`.
The route runs a single **non-`required`** read,
`SELECT version, name, applied_at, execution_ms, dirty, direction, checksum
FROM nsql_schema_migrations ORDER BY version LIMIT 2001`, through the logged-in
operator's own NSQL connection.

`nsql_schema_migrations` is a *reserved user table* (`internal/catalog`
`ReservedPrefix`, created from `catalog.HistoryDDL` the first time the official
migration system runs), not a `system.*` view. The authority for reading it is
therefore the caller's own SELECT privilege on that table — stated plainly
rather than dressed up as system-catalog RBAC. The table does not exist on a
database the migration system has never touched, so an absent or invisible
table is a warning, not an error: the response carries `present=false` (with
the reason in `warnings`), and the panel explains that the migration system
has not run (pointing at `nextsql migrate up`). Row 2001 sets `truncated`.

The panel computes a bounded summary purely from the returned rows — applied
count, current version (the last row), last direction, and whether any row is
dirty — and raises a `role="alert"` banner naming any dirty version, pointing
at `nextsql migrate repair` / `force`.

It is **read-only by design**, matching the other Developer-operations
explorers. Creating, validating, dry-running, applying and reverting
migrations genuinely cannot move into Studio: they need the local migration
*files*, and `nextsql-admin` is a pure `nextsqld` protocol client that never
holds a data directory or a migration folder. This explorer reports only what
the `nextsql migrate` CLI has already recorded.

### Data generator for development

A "Generate data…" toolbar button and command-palette entry
(Developer-operations scope) that builds `INSERT` statements of synthetic rows
from the authorized `system.columns` metadata already in the table-detail
bundle, then loads them into the active editor tab **for review** — exactly the
GRANT/REVOKE-builder and native-explorer boundary: it never executes anything,
adds no server route (it reuses `GET /api/v1/studio/table`), and the generated
text faces confirm-before-run and server-side RBAC like any hand-typed
statement.

`dataGenColumns` / `buildDataGeneratorSQL` (`resultTools.ts`) are pure and
bounded — at most `MAX_DATAGEN_ROWS` = 1,000 rows, batched into
`DATAGEN_ROWS_PER_STATEMENT` = 100-row `INSERT` statements, rejecting a result
over `MAX_DATAGEN_SQL_BYTES` = 512 KiB. Every table and column name is rendered
through `quoteIdentifier`, every string value through the same `''`-doubling
`quoteSQLString` the full-text/vector builders use.

Value generation is **deterministic given `(state, columns)`**: a seeded
mulberry32 PRNG is advanced once per generated cell in column order, so the
same seed always produces byte-identical SQL (unit-tested). A `NOW()` cell is
emitted as the literal function call, so the generated *text* stays
deterministic even though the value it produces at run time does not; the
"random timestamp" strategy anchors on a fixed 2025 UTC window for the same
reason.

Per-column fill strategies are offered only for the scalar types the generator
can produce safely — `INT8..64` / `UINT8..64` (sequence or random), `DECIMAL`
(precision/scale-aware random), `STRING` / `TEXT` (lorem words, `name-N` label,
or UUID text), `UUID`, `BOOL`, `TIMESTAMPTZ`, and `JSON` (empty object). Every
other declared type — `VECTOR` / `BITVECTOR` / `SPARSEVECTOR`, the geo types,
`STRUCT` / `ARRAY` / `MAP`, `BLOB` — is badged "type not generatable" and left
out of the column list. A column left out (whether skipped or not generatable)
that is **NOT NULL with no `DEFAULT`** is a blocking error naming the column,
not a silently-broken `INSERT`.

### CSV / JSON import for development

An "Import data…" toolbar button and command-palette entry
(Developer-operations scope). Like the data generator, it produces an
`INSERT` script into the active editor tab **for review** — it never
executes, adds no server route (it reuses `GET /api/v1/studio/table` for
the target table's authorized `system.columns`), and the generated text
faces confirm-before-run and server-side RBAC like any hand-typed
statement. This is the deliberate boundary for the checklist's
"CSV/JSON/NDJSON import": a protocol-only client streaming bulk rows into
the server directly would need its own admission/backpressure design; a
reviewable script does not.

`parseImportText` / `buildImportInsertSQL` (`resultTools.ts`) are pure and
bounded. `parseImportText` accepts five `ImportFormat`s — comma / semicolon
/ tab-delimited (a small RFC 4180 state machine: quoted fields, `""`
escapes, embedded newlines, a leading BOM stripped, a `"` only special at
field start), a JSON array of objects, and NDJSON. The delimited header row
names the fields; JSON/NDJSON fields are the first-seen union of object
keys. Input over `MAX_IMPORT_INPUT_BYTES` = 8 MiB and rows past
`MAX_IMPORT_ROWS` = 2,000 are rejected / truncated (with a visible notice).

`buildImportInsertSQL` maps each document field to a target column
(auto-paired by exact case-insensitive name via `autoImportMapping`, with a
per-field override `Select`), then emits `INSERT` statements batched at
`IMPORT_ROWS_PER_STATEMENT` = 100 rows, rejecting a result over
`MAX_IMPORT_SQL_BYTES` = 1 MiB. Every identifier goes through
`quoteIdentifier` and every string value through the same `''`-doubling
`quoteSQLString` the other builders use. **Integer and boolean cells are
validated against the target column kind** (reusing `dataGenFieldKind`) —
a non-integer for an `INT` column or an unrecognized boolean token is a
named `Row N: …` error, never a quoted guess; a `JSON` column's value must
`JSON.parse`. Column output order follows catalog ordinal, not document
order. An empty or missing value becomes `NULL` when "treat empty as NULL"
is on (this cannot distinguish a JSON `null` from a JSON `""` — an accepted
limitation); an empty value for a **NOT NULL column with no `DEFAULT`** is a
blocking error, as is such a column left unmapped. `VECTOR` / geo /
collection / `BLOB` columns are named as non-importable and are not offered
as mapping targets — a `VECTOR` / `BITVECTOR` / `SPARSEVECTOR` column has its
own dedicated path (below).

### Vector dataset import

An "Import vector dataset…" toolbar button and command-palette entry
(Developer-operations scope). The generic CSV/JSON importer above refuses
`VECTOR` / `BITVECTOR` / `SPARSEVECTOR` columns because a generic text cell
is not a vector; this dedicated path fills that gap for a development
embedding dataset, under the same never-execute boundary and with no server
route (it reuses `GET /api/v1/studio/table`).

`buildVectorImportSQL` (`resultTools.ts`, pure) maps **exactly one** document
field onto the table's vector column — its cell parsed by the same
`parseVectorLiteralInput` grammar the Vector Explorer uses (a bracketed
`[…]`, a parenthesized `(…)`, or a bare comma list; a JSON array field
serialises to `[…]` naturally) and validated against the column's **declared
dimensions** and, for a `BITVECTOR`, its 0/1 domain — and optionally maps
other fields onto the table's scalar columns, reusing the generic importer's
per-kind cell validation (`importValue`). `autoVectorImportField` resolves
the embedding field (exact case-insensitive name match → first unclaimed
field → first field) into an always-editable `Select`. It emits `INSERT`
statements with the exact native parenthesized vector literal
(`docs/vector.md`: `INSERT INTO docs (sig) VALUES ((1, 0, …));`), column
order following catalog ordinal, every identifier `quoteIdentifier`-quoted,
batched at `IMPORT_ROWS_PER_STATEMENT` = 100. Bounds: the shared
`MAX_IMPORT_INPUT_BYTES` = 8 MiB input and `MAX_IMPORT_SQL_BYTES` = 1 MiB
output ceilings, plus a vector-specific `MAX_VECTOR_IMPORT_ROWS` = 1,000 row
cap and a `MAX_VECTOR_IMPORT_VALUES` = 262,144 rows×dimensions ceiling that
fails with an actionable "import fewer rows" message before the SQL ceiling
would. A `SPARSEVECTOR` column takes the same dense parenthesized form — the
server coerces the zeros away. A wrong-length or non-finite vector is a
named `Row N, column "…": …` error, never silently padded.

### Parameterized INSERT / UPDATE / DELETE generation

A "Parameterized DML…" control in the Studio SQL workspace
(Developer-operations scope, alongside the data generator and CSV/JSON
import). Like those, it produces reviewable SQL text into the active editor
tab and **never executes** — no server route (it reuses
`GET /api/v1/studio/table` for the target table's authorized
`system.columns`), and the emitted text faces confirm-before-run and
server-side RBAC like any hand-typed statement. Unlike the value builders it
emits **positional placeholders**, not literals: the `$1..$N` it writes are
bound in the editor's existing Parameters panel (`extractQueryParams`) before
the operator runs the statement, so no row value is produced here and every
column type — including `VECTOR` / geo / collection — is a valid target.

`buildParameterizedDML` (`resultTools.ts`) is pure. It reuses `dataGenColumns`
to project the table-detail bundle's authorized `system.columns`, then:

- **`INSERT`** — `INSERT INTO "t" (…) VALUES ($1, …, $N);` over the chosen
  columns (default: every column), one placeholder per column, column list in
  catalog ordinal order. A **NOT NULL column with no `DEFAULT`** left out is a
  blocking error naming the column.
- **`UPDATE`** — `UPDATE "t" SET "c1" = $1, … WHERE "k1" = $M AND …;`. `SET`
  columns default to the non-primary-key columns; `WHERE` key columns default
  to the primary key. Placeholders number the `SET` list first, then the
  `WHERE` list. At least one `WHERE` key column is required; a column may not
  appear in both `SET` and `WHERE`.
- **`DELETE`** — `DELETE FROM "t" WHERE "k1" = $1 AND …;`, keyed on the chosen
  columns (default: the primary key), at least one required.

Every table and column name is rendered through `quoteIdentifier`. The
placeholder count is capped at `MAX_QUERY_PARAMS` (32) — the same bound the
editor's Parameters panel and the server's `studio.MaxQueryParams` enforce —
with a blocking error past it. A table with no primary key simply starts with
an empty `WHERE` selection the operator must fill in.

The Studio query-tab strip switched from wrapping to multiple rows to a single
horizontally scrolling row at the same time, so a crowded tab bar no longer
lands wrapped rows behind the find/replace popover.

### Table / index designer

A "Design schema…" control in the Studio SQL workspace (database-explorer
scope, reachable also as command-palette **Design table…** / **Design
index…**). Like the data generator, CSV/JSON import, and parameterized DML
builder, it produces reviewable SQL text into the active editor tab and
**never executes** — no server route (index mode reuses
`GET /api/v1/studio/table` for the target table's authorized
`system.columns`), and the emitted text faces confirm-before-run and
server-side RBAC like any hand-typed statement. The form's preview is the
live DDL the operator will run, which is the remaining half of "Generated
native DDL preview" after the inspector's read-only canonical DDL (log
#188).

`buildCreateTableSQL` / `buildCreateIndexSQL` (`resultTools.ts`) are pure.

**Table.** Columns come from a closed NextSQL type-kind list assembled here
(CHAR/VARCHAR length, DECIMAL precision/scale, VECTOR/BITVECTOR/SPARSEVECTOR
dimension) — never interpolating free-text type SQL. `PRIMARY KEY` is
required (`catalog.TableFromAST`); a single key column is inline, a
composite key is a table-level clause. Table names using the reserved
`nsql_` prefix are rejected. `DEFAULT` is `UUID()` / `NOW()` / `AI()` (AI()
only on DECIMAL) or a bounded literal. VECTOR/BITVECTOR/SPARSEVECTOR cannot
be keys. One optional `FOREIGN KEY` names local columns plus a referenced
table/columns and `ON DELETE` / `ON UPDATE`. Collections, ENUM, and
GEOMETRY subtypes stay out of the form — they need nested-type UI this
designer does not pretend to have.

**Index.** Kind is `btree` / `unique` / `fulltext` / `vector` / `spatial`,
restricted to what the authorized columns can actually support. B+Tree
indexes may add a JSON path on a single JSON key column and INCLUDE
columns; FULLTEXT is 1–8 STRING/TEXT/CHAR/VARCHAR columns with an optional
analyzer; VECTOR method/quantization/IVF options are restricted to the
column kind (SPARSE only on SPARSEVECTOR, IVF/IVFPQ only on dense VECTOR);
SPATIAL is one geo column.

Caps: 64 table columns, 16 btree keys, 8 fulltext fields, 128-character
identifiers, CHAR 65535, DECIMAL 38, VECTOR dim 8192. Every name is
rendered through `quoteIdentifier`.

### Catalog-aware IntelliSense

A bounded, keyboard-operable suggestion list (SQL-editor scope), reachable
from a "Suggest" toolbar button next to Find or Ctrl+Space inside the
editor. It offers **catalog identifiers only — table and column names —
and deliberately no NextSQL keyword completion**: the only ground truth for
the keyword set is `internal/sql/lexer`'s unexported `keywords` map, and
hand-copying it into the frontend would silently drift the first time a
keyword is added there without the frontend being updated in lockstep, the
kind of unverifiable duplication this design already rejects elsewhere
(e.g. `GRANT_PRIVILEGES`'s comment on `internal/sql/parser`'s privilege
list). Table names are free — already loaded in the bootstrap's
`system.tables` read. Column names for a table are offered only once that
table's columns have been fetched, and are fetched lazily and **only while
the suggestion panel is open**, through the exact same `api.studioTable()`
route every native explorer already uses — no new server route, and no
speculative/eager catalog reads for tables the buffer doesn't reference.

**Which tables get column suggestions**: `extractReferencedTables` scans
the buffer for `FROM <ident>` / `JOIN <ident>` (optionally schema-qualified,
e.g. `system.capabilities`), bounded at `MAX_REFERENCED_TABLES` = 8 distinct
tables. This is not a heuristic approximation of NextSQL's grammar — it is
the grammar: confirmed directly against `internal/sql/parser`'s `selectStmt`
(around its `KwFrom` handling), a `SELECT`'s FROM clause takes exactly one
table reference (or a parenthesized derived-table subquery) followed by a
chain of `JOIN`s; there is no comma-separated implicit-join list to miss. A
**quoted** (`"..."`) FROM/JOIN target is not recognized — only a bare
identifier — so a table whose name needs quoting gets no column
suggestions from this slice; it can still be typed and queried by hand.

**Ranking**: `rankSQLSuggestions` is pure and synchronous — it never
fetches anything, only ranks what the caller already has. Table names
matching the current prefix always come first (alphabetical), then column
names of already-resolved referenced tables (alphabetical, labeled
`column — table`); a table whose columns are still `"loading"` or
`"error"` contributes no column suggestions rather than a guess. An empty
prefix matches everything, bounded by `MAX_SQL_SUGGESTIONS` = 50 either way.
The per-table column cache is bounded at `MAX_INTELLISENSE_TABLE_CACHE` =
16 distinct tables per tab session, evicting the oldest-inserted entry via
the pure `withCachedTableLoading` reducer once a session has referenced
that many distinct tables — the same simple insertion-order bound
`MAX_STUDIO_TABS` already uses elsewhere, not an LRU.

**Accessible without a caret-anchored popup**: `@bzync/rui`'s `CodeEditor`
is a plain controlled-textarea wrapper with no ref forwarding, no
cursor-pixel-position callback, and no completion/overlay primitive at
all — exactly why the design doc flags NextSQL-native syntax
highlighting as blocked for a frontend-only slice (SQL formatting, which
only rewrites the buffer text and needs no overlay, is implemented — see
§7). Rather than trying to
float a popup at the caret, the suggestion list reuses the existing
`Popover`/`PopoverContent` primitive the Find/Replace panel already uses
(a toolbar-anchored, non-modal `role="dialog"`), and the underlying
textarea is given the standard ARIA 1.2 combobox-with-listbox-popup role
imperatively (`role="combobox"`, `aria-autocomplete="list"`,
`aria-expanded`, `aria-controls`, `aria-activedescendant`) — the same
direct-DOM technique the editor already uses to set `aria-label="SQL
editor"` on RUI's internal textarea. Real DOM focus never leaves the
textarea while suggesting: arrow keys/Enter/Escape are handled in the
existing `onEditorKeyDown` capture-phase handler, and `aria-activedescendant`
names the highlighted option instead. Clicking an option also works and
refocuses the textarea afterward. Accepting a suggestion replaces exactly
the identifier word touching the cursor (`currentWordRange`, extending in
both directions over `[A-Za-z0-9_]`) via the same `setSQL` update path
`insertTableQuery` already uses — not a native-setter/dispatch trick, since
nothing here needs synchronous same-tick DOM-selection chaining the way
Find/Replace's `replaceCurrent` does.

Ctrl+Space is a known-imperfect binding (some Linux input methods intercept
it); the toolbar button is the primary, always-discoverable trigger.

**JSON-path completion where metadata is known**: NextSQL persists no JSON
schema, so the only JSON structure it exposes any metadata for is a
**JSON-path index** — a `system.indexes` row whose `columns` value is a
dotted native path (`metadata.tags.0`, `metadata.category`; the same shape
`internal/xport`'s `createIndexSQL` writes and `reportedJSONPathIndexes`
already parses for the JSON Explorer). When `currentJSONPathRange` detects
the caret sitting inside a dotted path in the buffer, the suggestion list
switches from table/column names to `rankJSONPathSuggestions`: the indexed
JSON paths — across every FROM/JOIN-referenced table whose index metadata
has resolved — that begin with what has been typed. These paths come from
`jsonPathIndexPaths`, parsed from the `indexes` result the IntelliSense
`api.studioTable()` fetch **already returns** (a parallel
`tableJSONPathCache` filled from the same response — no new request, no new
route). It is never an inferred or free-typed path, only one that is
actually indexed — the same ambiguous-rather-than-guess discipline the
misspelled-table suggestions below use. Accepting one replaces the whole
dotted-path range. Non-indexed JSON structure is deliberately not offered:
there is no JSON schema to complete against without guessing.

**Vector-aware completion**: the catalog *does* expose per-column vector
metadata — the column's declared `VECTOR` / `BITVECTOR` / `SPARSEVECTOR`
type and the metrics that kind accepts (the same `vectorCatalog` /
`VECTOR_METRICS_BY_KIND` the Vector Explorer already uses). When
`currentNearestContext` detects the caret in a `NEAREST` column slot or a
`USING` metric slot of the current statement, the suggestion list switches
to `rankNearestColumnSuggestions` or `rankNearestMetricSuggestions`.
Columns come from `vectorColumnsFromResult` on the same `api.studioTable()`
response IntelliSense already fetches (`tableVectorCache`, no new route).
Metrics are the exact SQL spellings (`COSINE` / `L2` / `INNER_PRODUCT` /
`HAMMING`) that column kind accepts; a `BITVECTOR` column never offers a
real-valued metric. Inside `TO (...)` there is still no per-element catalog,
so that slot is not a completion context — the same refuse-to-guess rule as
non-indexed JSON. `CREATE INDEX … USING` is not a NEAREST slot.

Inline **parser diagnostics** are implemented; **binder** diagnostics
remain open. The parser half was done without touching the ~100 individual
`nerr.New(nerr.Syntax, "sql.parser", …)` sites: `parser.ParseDiag` wraps
`Parse` and, on any failure, attaches `p.tok.Pos` — the byte offset of the
token the parser stopped on (`internal/sql/lexer.Token.Pos`), which is where
the parser leaves `p.tok` because it returns up the stack without advancing
past the first error — plus the underlying message with its `nextsql
syntax:` prefix stripped. `studio.Diagnostics` splits the editor buffer with
the same `';'`-in-string/comment-safe lexer pass `SplitScript` uses,
`ParseDiag`s each statement, and maps each statement-local offset back to a
whole-buffer UTF-16 offset + 1-based line/column (`POST
/api/v1/studio/query/diagnostics`, parser-only, no connection, no query
slot). The editor runs it debounced as you type and renders one warning per
broken statement under the editor with a **Go to error** control — RUI's
`CodeEditor` still has no text-overlay primitive, so this is a strip, not an
inline squiggle. It is advisory: nextsqld re-parses, binds, and authorizes
on Run.

The **binder** half — "unknown column" / "unknown table" with a source
position — stays open for two reasons: the binder's name-resolution errors
discard both the identifier and its position (see the misspelled-table
section), *and* `nextsql-admin` has no catalog of its own to resolve names
against (the RBAC boundary — it is a pure protocol client). Surfacing those
needs either the binder to carry positions and nextsqld to return them in
its NSQL error frame, or a catalog-introspection surface in the Admin layer
— both larger than a frontend slice.

### Deterministic misspelled table-name suggestions

The remaining SQL-editor checklist item "deterministic suggestions for
misspelled identifiers where safe" is implemented for **bare FROM/JOIN
table names only** — the smallest slice of that item that is fully
deterministic against ground truth already loaded client-side, and the one
place NextSQL's grammar makes what an identifier *must* be unambiguous (a
`FROM`/`JOIN` target is always a table reference — see the previous
section's grounding against `internal/sql/parser`'s `selectStmt`). Column
names, aliases, function names, and parameter names are all deliberately
**not** covered: a bare identifier in a `SELECT` list or `WHERE` clause can
be a column, a function call, a cast, an alias, or a JSON path segment, and
telling those apart correctly needs the real parser's AST, not a regex —
misclassifying one of them as a wrong column and offering a "fix" would be
actively harmful, not merely unhelpful, so this slice does not attempt it.

**Why not react to a real server error instead of guessing client-side**:
investigated first, and ruled out on the actual source, not assumed.
Every "unknown table"/"unknown column" error NextSQL's binder and executor
raise (`internal/sql/binder/binder.go`, `internal/executor/eval.go`,
`exec.go`, `exec_ddl.go`, `dml_bulk.go`, ...) is a flat literal string —
`nerr.New(nerr.NotFound, "sql.binder", "unknown column")` — that never
includes the offending identifier's own name anywhere in the error text or
in any structured field. A reactive "did you mean X" fed from that text
is therefore infeasible without changing the server's error surface, which
is out of scope for a Studio-only frontend slice (and a wire/error-format
change carries its own compatibility weight this increment doesn't need to
take on). The client-side FROM/JOIN check below needs no server change at
all.

**Detection**: `suggestTableNameFixes` (`resultTools.ts`) reuses the exact
same `REFERENCED_TABLE_RE` extraction the IntelliSense section above
already grounded against the parser grammar. A bare (unqualified — no `.`)
FROM/JOIN target that doesn't exactly match a name in the already-loaded
`catalogTableNames` is a candidate; a **schema-qualified** target (e.g.
`system.capabilities`) is never flagged, because Studio's bootstrap only
lists `system.tables`' own persisted-user-table rows, never the separate
virtual `system.*` catalog views — there is no ground-truth list to check
a qualified name against without guessing. The whole check is disabled
outright whenever `bootstrap.tables_truncated` is true: a partial
1,000-row catalog read cannot prove a name doesn't exist beyond the cutoff.

**Matching**: a bounded single-row Levenshtein edit-distance DP
(`levenshteinDistance`) compares each candidate bad name against every
loaded table name (skipping any whose length differs by more than the
allowed distance, both for performance and because a match that far apart
in length is never meaningful). The allowed distance is 1 for names of 4
characters or fewer, 2 above that — deliberately not scaled any higher, so
this stays a "misspelling" correction rather than a guess. **A tie between
two or more equally-close real table names is never resolved by guessing**
— no fix is offered for it, the same ambiguous-rather-than-guessed
convention the JSON Explorer's indexed-path indicator and the Vector/Hybrid
Explorers' catalog matching already established. Findings are capped at
`MAX_TABLE_NAME_FIXES` = 5 per buffer.

**Fixing**: each finding renders as a live `aria-live="polite"` notice
below the editor (`"<name>" doesn't match any table you can see.` plus a
`Use "<real name>" instead` button) — `aria-live="polite"`, not
`role="alert"`, on purpose: it recomputes on every keystroke, unlike the
explicit-action confirm-before-run/query-error alerts elsewhere in this
document, so it must never interrupt the way an assertive alert would.
`applyTableNameFix` rewrites every recorded occurrence of that one bad
name (working from the last span backward so earlier offsets stay valid)
and touches nothing else in the buffer; the fix is never applied
automatically. Nothing is fetched or executed to compute or apply a fix —
purely a pure/synchronous rewrite of already-loaded state.

### Live parser diagnostics

The editor reports **where a statement fails to parse** as you type, using
the same grammar `nextsqld` binds and runs. It is advisory — the server
re-parses, binds, and authorizes on Run — and covers **grammar** errors
only; an unresolved table or column name is reported by the server when the
statement actually executes (the binder half; see §7 option 2 and the
parser/binder-diagnostics note above).

Server side, `parser.ParseDiag(src) (ast.Stmt, *SyntaxDiag, error)` is `Parse`
plus a location. It changed **none** of the ~100 individual
`nerr.New(nerr.Syntax, "sql.parser", …)` sites: at the top of `Parse`, on
every failure path, it captures `p.tok.Pos` — the byte offset of the token
the parser stopped on. That works because the parser returns straight up the
call stack on its first error without advancing `p.tok`, so the current
token *is* the offending one by the time `ParseDiag` reads it. The message
is the underlying `*nerr.Error`'s `.Message` (no `nextsql syntax:` prefix).
`err != nil` and `diag != nil` are always set together; the offset can equal
`len(src)` for a statement that ends prematurely.

`studio.Diagnostics(sql)` turns that into buffer coordinates. It splits the
editor buffer with `splitStatementSpans` — the identical
`';'`-in-string/comment-safe lexer pass `SplitScript` uses, keeping each
trimmed fragment's byte span — `ParseDiag`s each statement, and maps each
statement-local offset back to a whole-buffer UTF-16 offset plus a 1-based
line and (UTF-16) column (`locateInBuffer` / `utf16Len`, so an astral-plane
rune earlier in the buffer does not shift the marker). One `Diagnostic` per
statement that fails to parse (the parser stops at its first error), in
buffer order; a valid buffer returns an empty slice. Empty/oversized stay
request errors like `Analyze`; a buffer that will not tokenize at all is
reported as one diagnostic at the lexer's stopping point rather than an
error, so the editor can still point at it.

`POST /api/v1/studio/query/diagnostics` (`handleStudioDiagnostics`, authed +
CSRF, bounded by `ValidateSQL` and `MaxScriptStatements`) is parser-only: it
opens no driver connection and never touches the session's query slot, the
same trust level as `/query/analyze` and `/query/split`.

The editor runs it debounced (400 ms) on the active buffer, race-guarded by
a sequence ref, and clears the strip on an empty buffer, a 401, or any
network error — it must never block a run. Results render as an
`aria-live="polite"` strip of `Alert variant="warning"` under the editor
(next to the misspelled-table strip, and `polite` for the same reason — it
recomputes as you type), each "Line L, column C: message." with a **Go to
error** button that focuses the textarea and selects the token at `offset`.
There is no inline squiggle: `@bzync/rui`'s `CodeEditor` has no
text-overlay primitive at all — the strip is the surface. Capped at 20
entries.

### Table statistics inspection

The database-explorer checklist item "Statistics" is implemented by extending
the existing lazy table-detail bundle, not by adding a route. `GET
/api/v1/studio/table` already runs one authorized `SELECT` each against
`system.tables`, `system.columns`, and `system.indexes` for the selected
table; it now also reads `system.table_stats` (`row_count`, `updated_at`) and
`system.index_stats` (per-index `row_count`), both filtered by `table_name`
and both sharing `system.tables`' exact table-visibility filter — a user who
can see the table in the explorer can see its recorded statistics, and no
one else. The two new reads are **not** `required` in the bundle: they are
list-shaped views that legitimately return zero rows for a table that has
never been analyzed (and zero rows on a legacy/embedded deployment with no
process statistics), the same convention Operations mode's Maintenance view
already uses for the identical pair of tables.

The inspector renders a new **Statistics** section under Indexes: the
`system.table_stats` row and the `system.index_stats` rows as two bounded
result grids, or an explicit "No statistics recorded yet. Run ANALYZE on
this table…" note when both are empty. These are **estimates written by
`ANALYZE`** (or the engine's automatic in-transaction refresh once a table
accumulates ≥1,000 changed rows — see `docs/sql.md`), never a live
`COUNT(*)`, and the section does not imply otherwise. Studio issues no
`ANALYZE` itself; it only surfaces what the catalog already holds.

### Lazy-loaded schema tree

The database-explorer checklist item "Lazy-loaded database/schema/table/
index/workflow tree" is implemented entirely in the browser (`SchemaTree.tsx`)
against reads Studio already has — it adds **no route and no server surface**.
The flat, filterable table list the Database explorer showed before is now a
two-branch disclosure tree:

* **Tables** — one node per authorized table, from the `system.tables` list
  the bootstrap read already returns. Selecting a table name still loads the
  full right-hand inspector exactly as before; expanding a table node fetches
  that table's `GET /api/v1/studio/table` bundle **once** (cached for the
  session) and renders three sub-branches, **Columns** (`column_name · type`,
  primary-key columns tagged `PK`), **Indexes** (`index_name`, with
  `kind` / `unique` / a non-`valid` status shown inline), and **Foreign keys**
  (one leaf per constraint: name, then `(child cols) → ref_table (ref cols)`
  and a non-`RESTRICT` `ON DELETE` action inline). A table's detail is
  only ever fetched when its node is first expanded or its name is first
  selected, never for the whole list.
* **Workflows** — collapsed by default; the first time it is opened it runs
  the existing `GET /api/v1/studio/workflows` bundle and lists each visible
  workflow (`name`, owner). Read-only, like the Workflows explorer it shares
  the read with — no run/cancel/edit action.

Every fetch goes through the same authorized official-driver session as the
rest of Studio, so the system catalog's RBAC filtering stays the sole
authority over which tables, columns, indexes, foreign keys, and workflows
appear. The filter box narrows the Tables branch only. The ER diagram is the
"Schema diagram…" explorer; a full dependency view (views/workflows/triggers
referencing a table) is still blocked on catalog surfaces that do not exist.
Canonical DDL is available in the inspector's **DDL** section (see below):
`system.table_ddl` (added log #187) renders each visible table's and
index's `CREATE` statement with the same `internal/catalog/ddl` code that
backs backup/restore SQL export.

### DDL panel

The database-explorer "DDL view" checklist item is the table inspector's
**DDL** section (under Statistics). `GET /api/v1/studio/table` runs one more
**non-`required`** `SELECT object_type, object_name, ddl FROM
system.table_ddl WHERE table_name = <name> ORDER BY object_type DESC,
object_name` — `TABLE` row first, then each `INDEX` row. Non-required so a
`nextsqld` predating `system.table_ddl` (log #187) makes this a warning and
an empty panel, not a failed detail load. `system.table_ddl` is
`canSeeTable`-filtered exactly like `system.columns`, so a user only ever
sees DDL for tables they can already inspect.

`tableDDLScript` joins the `ddl` cells into one runnable script (each
statement `;`-terminated). It renders in a **focusable** `<pre
tabindex=0>` — deliberately not `@bzync/rui`'s `CodeBlock`, whose internal
`overflow-x-auto` wrapper fails axe's `scrollable-region-focusable` rule on
a long single-line `CREATE` (caught by `test-accessibility.mjs`); a plain
`<pre>` with `overflow:auto` is keyboard-scrollable and passes. **Copy
DDL** copies the script; **Open in editor** drops it into the active tab
via `setSQL` without running it, so re-creating from it still faces
confirm-before-run.

### Unified Constraints panel

The Database-explorer "Constraints" checklist item is a derived, read-only
table-inspector section over metadata the selected table's existing detail
bundle already returned. NextSQL has no `CHECK` constraint and no separate
`system.constraints` view: primary-key and NOT-NULL declarations are fields in
`system.columns`, UNIQUE is represented by `system.indexes`, and referential
constraints are rows in `system.foreign_keys`. `tableConstraintsResult`
combines those sources without a new Admin route or server query.

The grid contains one composite PRIMARY KEY row in catalog ordinal order, one
row for each UNIQUE index (kept explicitly labelled **UNIQUE INDEX**, including
its INCLUDE/predicate/status details), one row for every NOT NULL column, and
one grouped FOREIGN KEY row with its ordered child/referenced columns and
`ON DELETE`/`ON UPDATE` actions. A `PRIMARY` backing-index row returned by an
older server is suppressed rather than duplicating the primary-key declaration.
Invalid or absent expected metadata is omitted rather than guessed. Derived
output is capped at 2,048 rows and the inspector discloses truncation; an empty
set receives an explicit no-constraints state.

All inputs remain `canSeeTable`-filtered server results from the logged-in
Studio session. This is presentation only: no DDL is executed and no client
constraint model becomes authoritative over nextsqld.

### Foreign-key inspection

The database-explorer checklist items "Primary/foreign keys" and the
foreign-key part of "Dependencies" are implemented by extending the same lazy
table-detail bundle (no new route): `GET /api/v1/studio/table` runs two more
authorized `SELECT`s against `system.foreign_keys`, both sharing
`system.tables`' exact table-visibility filter and, like the statistics reads,
**not** `required` —

* `foreign_keys` — `WHERE table_name = <name>` ordered by `constraint_name,
  ordinal`: this table's *outbound* FK constraints (the ones it declares);
* `referencing_keys` — `WHERE ref_table = <name>` ordered by `table_name,
  constraint_name, ordinal`: the *inbound* references — FK constraints on
  other tables that point at this one. Because `system.foreign_keys` filters
  each row by `SELECT` on its **child** table, a user only ever learns about
  inbound references from tables they can already see.

The inspector renders a **Foreign keys** section between Indexes and
Statistics showing the outbound `system.foreign_keys` rows as a bounded grid
(or "This table has no foreign keys." when empty). Primary-key columns are
already shown (tagged `PK`) in the Columns view and tree branch, from
`system.columns.is_primary`. The schema tree adds the per-table
**Foreign keys** sub-branch described above (outbound only — the tree
describes each table's own structure).

A separate **Dependencies** section follows, rendered only when the table has
inbound references or triggers. It composes two already-authorized reads:

* **Referenced by** — the inbound `referencing_keys` grid (FK constraints on
  other visible tables that point at this one);
* **Triggers** — row triggers defined on this table, from a third
  non-`required` table-detail query, `SELECT * FROM system.triggers WHERE
  table_name = <name> ORDER BY name`. `system.triggers` (P29, log #190)
  already filters each row by `SELECT` on the table the trigger fires on — the
  same visibility rule as `system.foreign_keys` — so this adds no new
  disclosure and no new route. The schema tree gains a matching per-table
  **Triggers** sub-branch (shown only when non-empty).

Views and workflows that reference a table remain outside this view — NextSQL
has no views, and there is no workflow-body / trigger-body source catalog
surface to resolve free table references against.

### Schema-relationship diagram

The database-explorer checklist item "ER diagram from actual FKs" is a
modal explorer (**Schema diagram…** in the toolbar) over one authorized
read — `GET /api/v1/studio/schema-graph` runs a single
`SELECT * FROM system.foreign_keys ORDER BY ref_table, table_name,
constraint_name, ordinal LIMIT 4001`, capped at 4,000 rows (a schema at the
1,000-table bootstrap limit with the per-table maximum of 16 foreign keys is
far below that) and carrying the system catalog's own RBAC filter — a foreign
key is in the result only when the caller can `SELECT` its child table.

The browser collapses the flat one-row-per-column result into one edge per
constraint (`buildSchemaGraph`), then lays the tables out in dependency
layers (`layoutSchemaGraph`): a table that references nothing is leftmost,
and a table is placed one layer to the right of the furthest table it
references, with foreign-key cycles broken deterministically. The diagram is
an inline `<svg role="img">` — table boxes and cubic-bezier edges with an
arrowhead pointing from child to parent — with an `aria-label` stating its
size. Below it, **always shown**, is a **Relationships** list grouped by
referenced table; this is the diagram's text alternative and the fallback
when the layout is skipped. Layout is skipped (list only) for a schema with
more than 40 related tables or 80 foreign keys, or when the 4,000-row read
was truncated. All layout math is pure and unit-tested; no layout library is
added.

### Global object search

The database-explorer checklist item "Global object search" is a keyboard
finder (**Search objects…** in the Database explorer header) over the two
object namespaces Studio can enumerate *completely* without a fetch per
object: table names (from the bootstrap read) and workflow names (one
authorized `system.workflows` read, loaded the first time the finder opens).
Columns and indexes are **not** searched — that would need a
`GET /api/v1/studio/table` per table — and the finder says so.

`rankObjectMatches` (pure, unit-tested) scores each name by how it matches
the query: exact, then prefix, then substring, then in-order subsequence;
anything that is not at least a subsequence is dropped, never surfaced as a
guess. The finder is a `Modal` with a `role="combobox"` input and a
`role="listbox"` of results (Arrow keys move the active option, Enter
activates, Escape closes). Activating a table opens it in the inspector;
activating a workflow opens the read-only Workflows explorer. No route, no
server surface.

### Global command palette

The "Main shell / UX" checklist item "Global command palette" is a
keyboard launcher over Studio's *own actions*, opened with **Ctrl/Cmd+K**
anywhere in the workspace (a `window` `keydown` listener) or via the
**Commands** button in the Database-explorer header. It adds no behavior:
every entry maps to an existing handler — New query tab, Run query / Run
script / Cancel (each disabled exactly when its toolbar button is),
Suggest, Saved queries…, Search objects…, Switch connection…, Schema
diagram…, the GRANT/REVOKE builder, each dedicated explorer
(Full-text / Vector / Hybrid / Geo / Users & roles / Transactions & locks
/ Audit / Workflows), Hide/Show explorer, Hide/Show inspector, and Reset
layout. The dedicated explorers and builders are also listed in the
editor toolbar **More** (three-dot) overflow so the primary Run/Find/History
row stays scannable.

`rankCommandMatches` (pure, unit-tested) is the same exact→prefix→
substring→subsequence scoring as `rankObjectMatches`, but it also searches
per-command hidden `keywords`, keeps the caller's curated order for an
empty query rather than sorting alphabetically, and filters a disabled
command out entirely (there is nothing to run). The palette is a `Modal`
with a `role="combobox"` input and a `role="listbox"` of commands (Arrow
keys, Enter, Escape) — the same shape as the object finder. No route, no
server surface, no new state beyond an open flag.

### Editable data grid, staged-change review & transactional commit

The Studio MVP exit-gate line "Table inspector/data editor" needed a write
path, not just the M1 read-only inspector. `detectEditableTable(sql,
catalogTables)` restricts editing to a single-table `SELECT` with no
join/union and no `system.*` table; `isResultEditable(result, pkColumns)`
additionally requires the table to have primary-key columns and every one
of them to be present in the result set — a query that projects away the
key cannot be edited, since there is nothing safe to key an `UPDATE`/
`DELETE` on. `extractRowPK`/`makeRowKey` turn a result row into a
serialized primary-key tuple used as the staging map key, so composite
keys and re-edits of the same row coalesce correctly.

Edits are staged, not applied immediately: `stageCellUpdate` records a
per-cell change (reverting to no-op if the value is restored to the
original), `stageRowDelete`/`stageRowInsert` mark row deletion and new-row
records, and `stagedChangesCount`/`stagedChangesSummary` track counts for
the UI. `formatCellSQLLiteral` renders a typed value as a native SQL
literal (quoting strings/JSON, `NULL`, unquoted numeric/boolean). None of
this touches the connection — it is pure state in `StudioWorkspace`, the
same "reviewable text, not a live write" boundary as the data generator,
importers, and DML/DDL builders, except the generated script is the thing
that actually runs.

`buildStagedChangeSQL` compiles all staged updates/deletes/inserts into one
`BEGIN; ...; COMMIT;` script, ordered updates-then-deletes-then-inserts,
bounded by `MAX_STAGED_CHANGES` (500) and `MAX_STAGED_SQL_BYTES` (512 KiB)
so an unbounded edit session cannot build an unbounded transaction.
`EditCellModal` (NULL toggle, type badge, original-vs-modified diff,
multiline JSON) and `AddRowModal` (per-column type badges/NULL flags) collect
the staged values; `ResultGrid` shows modified cells, deleted rows, and
inserted rows with distinct highlighting and a staged-change count bar;
`StagedChangesReviewModal` lists every staged change with per-item removal,
discard-all, and a copyable/insert-into-editor preview of the compiled
script before anything runs.

Commit itself (`commitStagedChanges` in `StudioWorkspace.tsx`) executes the
compiled statements sequentially over the operator's own authenticated
session connection — the same official-driver connection every other
Studio query uses, so RBAC applies exactly as it would to hand-typed SQL.
Any statement failure issues `ROLLBACK;` so no dirty transaction state is
left open, and a successful commit re-runs the active tab's query to
refresh the grid from the now-committed data. There is no new server
route: this is client-composed SQL executed through the existing query
path.

## 2. Architecture and trust boundary

The existing Operations-mode login remains M1's connection prompt. It opens
one official Go-driver connection as the operator's own NextSQL user and keeps
the password only for the duration of that connection setup; no browser or
server-side credential file is created. Studio reuses that authenticated
session for its first workspace rather than prematurely inventing the full
saved multi-environment profile model.

```text
browser Studio route
  -> same-origin Admin HTTP API (session cookie + CSRF on every POST)
    -> bounded Studio adapter (`internal/admin/studio`, `internal/admin/ops`)
      -> official Go driver (`drivers/go`)
        -> NSQL
          -> parser / binder / planner / executor / RBAC in nextsqld
```

Studio does not import or read storage, WAL, undo, recovery, catalog, crypto,
transaction, or executor internals. `internal/admin/studio/imports_test.go`
pins that boundary. It has no root-key, data-directory, ACL-file, or auth-file
access. The server remains authoritative for SQL semantics, authorization,
realm/database visibility, transactions, durability, replication, and audit.

This "the server's RBAC is the only authority" property is directly
test-covered: `TestAdminStudioEnforcesRBAC` (`tests/integration/admin_test.go`)
wires a real `security.ACL` with a limited user and proves — through
`/studio/bootstrap`, `/studio/table`, `/studio/query`, and
`/studio/workflows` — that the user cannot list, open, read, or `CREATE`
anything its grants disallow, and that admin-only `system.*` views return
zero rows rather than data or an error. Realm/database isolation is
inherited unchanged from the protocol layer (covered by
`multirealm_auth_test.go` / `multirealm_routing_test.go`); Studio adds no
realm/database routing code of its own.

The implemented Studio slices add no persistent format, catalog format, WAL
record, Raft state, NSQL wire frame, public driver API, or migration. Their
only new wire surface is the same-origin Admin HTTP adapter below; the result
tools, native explorers, and Developer operations reads add no server route.

## 3. HTTP contracts

All routes require the existing Operations-mode session. POST routes also
require its per-session `X-NSM-CSRF` token.

| Route | Purpose | Bound |
|---|---|---|
| `GET /api/v1/connection` | Live nextsqld reachability for this admin session (`connected` true/false). Shared by Ops and Studio. Does not sign the operator out when the database is down. | 3 s ping; skipped while a query holds the connection |
| `GET /api/v1/studio/bootstrap` | `system.capabilities`, the authorized table list, and the target `nextsqld` address (`server_addr`, display only) | 1,000 tables; detail omitted |
| `POST /api/v1/studio/reconnect` | Re-target the session's connection to a different realm/database on the same `nextsqld` (fresh authenticated connection; password used once, never stored) | 128-byte bare-identifier names; 15 s open; `409` if a query is in flight |
| `POST /api/v1/studio/read-consistency` | Set the session's read-consistency mode (`strong`/`bounded`/`stale`) — a live session-control frame on the current connection, reads only | mode enum; `0…1 h` staleness bound; `409` if a query is in flight |
| `GET /api/v1/studio/table?name=…` | Lazy `system.tables`/`columns`/`indexes`/`foreign_keys` (outbound)/`referencing_keys` (inbound)/`table_stats`/`index_stats`/`table_ddl` detail | one validated bare identifier |
| `GET /api/v1/studio/workflows` | Read-only `system.workflows` + `system.triggers` + `system.schedules` + `system.tasks` + `system.change_streams` for the Workflows & CDC explorer | RBAC-filtered; every result independently capped at 500 rows |
| `GET /api/v1/studio/schema-graph` | Read-only whole-catalog `system.foreign_keys` for the schema-relationship diagram | 4,000 rows; RBAC-filtered by the system catalog |
| `GET /api/v1/studio/migrations` | Read-only `nsql_schema_migrations` history for the migration explorer (`present`, `history`, `truncated`) | 2,000 rows; not `required` — an absent/invisible table yields `present=false`, not an error; authority is the caller's own SELECT privilege on the reserved table |
| `POST /api/v1/studio/query/analyze` | Parse-only confirm-before-run classification (`kind`, `destructive`, `write`, `realm_scoped`) | 1 MiB SQL; no driver connection |
| `POST /api/v1/studio/query` | Execute one editor request through the official driver | 1 MiB SQL; ≤32 positional params × ≤64 KiB each; 25 seconds; one active query per session |
| `POST /api/v1/studio/query/stream` | Primary browser query path: NDJSON metadata/row-batch/completion frames | 5,000 rows; 8 MiB aggregate; 1 MiB/row; 128 rows/batch; same param bound |
| `POST /api/v1/studio/query/cancel` | Cancel the matching active query | query id scoped to the current session |

Request decoding accepts exactly one JSON object, rejects unknown fields, and
uses `http.MaxBytesReader`; an oversized request is `413`. Query ids contain
only 1–128 ASCII letters, digits, underscores, or hyphens. Table metadata SQL
accepts only the exact bare-identifier shape recognized by the NextSQL lexer
before interpolation, so quotes, whitespace, separators, and comment syntax
cannot enter the statement.

The original M1 query response contains `columns`, `column_types`, `rows`,
optional `affected`, `truncated`, and `elapsed_ms`. The M2 browser path sends
the same information as ordered `meta` → zero or more `rows` → `complete`
NDJSON frames; a failure after metadata is an explicit terminal `error` frame.
Cells are strings or JSON `null`; the UI never coerces a returned value into a
different SQL type.

## 4. Cancellation correctness

The browser chooses a query id before execution. The Admin session registers
that id before driver I/O, and a cancellation request can therefore reach the
query even while the execution request is blocked. Only the exact id in the
same cookie session can be canceled. A second Studio execution fails fast
with `409` while the connection is busy instead of forming an unbounded queue.

M1 testing exposed two older cancellation gaps below Studio itself:

1. the Go driver armed its context callback only after receiving the first
   response frame, so a query blocked before `RowDesc`/`CommandComplete` could
   not be canceled; and
2. the transaction lock manager waited without the executing query's context,
   so even a server-side cancel could not release a row-lock wait.

The driver now arms cancellation before that first read and transfers the
callback to streaming `Rows` only when needed. The executor attaches its
per-query budget context to the live transaction handle, and contended key and
range waits remove themselves from both the waiter queue and wait-for graph on
cancel. A live TLS/NSQL integration test holds a real row lock, observes the
waiting query through `system.active_queries`, cancels it, and proves the
connection remains reusable. The Studio HTTP integration performs the same
lock-wait cancellation through `/query/cancel` and then rolls back on the same
Admin session.

## 5. Resource and failure behavior

- SQL is capped at 1 MiB before protocol encoding.
- A Studio query has a 25-second context, leaving five seconds inside Admin's
  30-second HTTP write deadline to cancel, drain, and return a structured
  error.
- The M2 HTTP adapter never retains the whole result. It emits at most 128 rows
  per batch, targets 256 KiB raw cell text per batch, rejects a row over 1 MiB,
  and stops at 5,000 rows or 8 MiB aggregate cell text. At the first excess it
  cancels and closes/drains the driver stream, preserving the connection frame
  boundary, and reports `truncated=true`.
- The browser independently enforces the same bounds and mounts only the
  visible result rows plus eight rows of overscan on each side. Scrolling does
  not grow the live DOM with result cardinality.
- Copy/export actions are disabled while a stream is running or after a query
  error. They operate only on the retained bounded preview; encoded output is
  capped at 64 MiB and never enters server storage or the PWA cache.
- JSON tree work stops at 1,024 nodes / 24 levels; vector parsing stops at
  65,536 values and renders at most 256; fixed-geo preview stops at 2,048
  coordinate points. An unsupported/invalid native value fails closed to an
  error or raw-text fallback rather than a guessed visualization.
- JSON path selection retains at most one bounded path per mounted tree node
  (24 segments under the same 1,024-node limit). Index indication reads only
  the selected table's already-bounded authorized metadata and returns
  “ambiguous” when the catalog's dotted text cannot prove segment boundaries.
- Full-text Explorer input is capped at 4,096 browser characters and 100
  result rows; field selection inherits the engine's eight-field limit.
  Table metadata remains the existing bounded per-table read, and Run search
  inherits M2's 5,000-row / 8 MiB / 25-second stream ceiling even though its
  generated `LIMIT` is stricter.
- The explorer bootstrap returns at most 1,000 tables; columns and indexes are
  fetched only for the selected table.
- The Workflows explorer reads at most 500 rows from each of its five
  definition/activity catalogs. Its always-visible relationship list retains at most 500
  combined paths; the SVG is omitted above 60 nodes / 80 links or whenever a
  source was truncated, so a partial graph is never presented as complete.
- Each Admin session has at most one active Studio query and uses the already
  bounded Operations session store. Navigation/unmount requests cancellation.
- The Transaction/Lock explorer reuses Operations mode's existing 15-second
  activity bundle, permits at most one browser request in flight, refetches on
  every open/Refresh, and retains no unbounded history or polling loop.
- The Audit viewer reuses the existing 15-second security bundle, permits at
  most one shared security read in flight, displays at most the server's
  200-record audit tail, and never polls the whole-chain verification path.
- Plan-comparison captures stop at 512 operators each. There is at most one
  baseline per tab under the existing eight-tab cap; captures are browser-only
  and disappear when their owning tab is closed or the page reloads.
- Query-profile construction stops at 512 operators and exists only for the
  currently rendered ANALYZE result; it creates no history, polling loop, or
  server-side state.
- SQL/server errors are surfaced as errors; Studio never substitutes guessed
  data or a fake success state.

Truncation is a preview behavior, not silent query success: the UI explicitly
states that Studio canceled the remaining result stream. M2 removed the
materialized M1 browser path while retaining hard client/server budgets; the
non-streaming JSON endpoint (`POST /api/v1/studio/query`) went back into use
as Execute Script's per-statement execution path (each statement's own
result is small enough to keep whole), not just internal compatibility
surface.

## 6. Implemented/tested/gated audit

| Surface | Designed | Implemented | Tested | Production-gated |
|---|---:|---:|---:|---:|
| Official NSQL-only boundary | yes | yes | import audit + live NSQL | no |
| Capability-aware bootstrap | yes | yes | HTTP integration + browser fixture | no |
| Authorized table/column/index explorer | M1 scope | yes | injection unit + live catalog + browser | no |
| Table/index statistics inspection | Database explorer scope | yes | live ANALYZE→system.table_stats/index_stats integration + real-browser section render + axe | no |
| Unified table Constraints panel (PK/UNIQUE-index/NOT-NULL/FK) | Database explorer scope | yes | pure grouping/order/empty/bound tests + real-browser all-four-kinds render/axe | no |
| Single editor execution | M1 scope | yes | live DDL/DML/SELECT + browser | no |
| Session-scoped cancellation | yes | yes | unit + real lock-wait driver/Admin integrations | no |
| Bounded typed result preview | M1 scope | yes | row/byte/NULL/type unit + live result | no |
| Streaming NDJSON result transport | yes | yes | ordering/batch/row/byte/failure unit + live NSQL | no |
| Virtualized accessible result grid | yes | yes | 250-row real-Chrome DOM-bound + axe audit | no |
| Result status line (rows/columns/affected/elapsed) | yes | yes | pure `queryResultSummary` unit (read / singular / truncated / write-affected / DDL-completed / selection) + real-browser column-less `UPDATE` shows "N rows affected" | no |
| Cell/row selection and copy | yes | yes | pure serializer + real-browser interaction | no |
| Selected/all loaded CSV + JSON export | current bounded-preview scope | yes | pure format/security/bounds tests + browser actions | no |
| JSON tree/raw inspector | yes | yes | node/depth bound unit + real-browser modal/axe | no |
| Dense/bit/sparse vector inspector | yes | yes | parser/dimension/order tests + real-browser modal | no |
| Fixed POINT/BOX/LINESTRING/POLYGON preview | partial geo scope | yes | coordinate-order/parser tests + real-browser SVG | no |
| General GEOMETRY/GEOGRAPHY multi-shape preview | yes | yes | recursive/nested/bound-limit parser unit + real-browser SVG | no |
| TIMESTAMPTZ UTC/local inspector | yes | yes | offset/UTC unit + real-browser modal | no |
| Studio accessibility baseline | M1 states | yes | real Chrome + axe WCAG 2.2 A/AA tags | no |
| High-DPI browser rendering | Main shell / UX scope | yes | real Chrome at DPR 2: compact Setup/Operations/Studio layouts, no page overflow, raster-source density, scalable-font readiness, axe WCAG 2.2 AA, metric reset | no |
| Layout persistence without credentials | Main shell / UX scope | yes | pure `serializeStudioLayout`/`parseStudioLayout` unit (round-trip/garbage/clamp/truncate/no-credential) + real-Chrome hide-explorer→localStorage→reload-stays-hidden/restores-selected-table/axe/show-splitter/hide-inspector | no |
| RBAC boundary (limited user confined to grants across every route) | design invariant | yes | `TestAdminStudioEnforcesRBAC` — real ACL, negative-path over bootstrap/table/query/workflows + zero-row admin views | closes the MVP exit-gate RBAC line |
| Confirm-before-run destructive-statement warning | partial connection-manager scope | yes | classification unit + live NSQL parse + real-browser confirm/cancel/axe | no |
| Visible cross-database administration warning (realm-wide users/roles) | Developer operations / connection-manager scope | yes | parsed-AST positive/negative unit cases + live HTTP/NSQL analysis + pure realm/database label tests + real-browser single/script confirm/cancel/both-reasons/axe | no |
| Production environment tag + read-only safety mode | connection-manager scope (partial) | yes | write-classification unit (`isMutating` mirror) + live analyze `write` flag + real-browser tag/banner/read-only-confirm/toggle/axe | no |
| Switch realm/database (connection re-targeting) | connection-manager scope (partial) | yes | `ReconnectRequest.Validate` + `session.reconnect` swap/busy-guard Go unit + `TestAdminStudioWorkspaceOverNSQL` (CSRF / bad-name / wrong-password-then-still-usable / valid-swap-then-whoami over NSQL) + real-browser Switch-connection modal open/axe/real-switch/switch-back | no |
| Recent connections (realm/database quick-switch) | connection-manager scope (partial) | yes | `parseRecentConnections` / `recordRecentConnection` / `serializeRecentConnections` / `recentConnectionLabel` pure unit (garbage/dedupe/move-to-front/cap/all-default-drop/round-trip) + real-browser switch→reopen→recent-entry-offered | no |
| Read-consistency mode (strong/bounded/stale) | connection-manager scope (partial) | yes | `SetReadConsistencyRequest.Validate` + `session.setReadConsistency` busy/no-conn guard Go unit + `TestAdminStudioWorkspaceOverNSQL` (CSRF / unknown-mode / set-bounded-then-bootstrap-reflects / SELECT-under-bounded / non-bounded-drops-bound over NSQL) + real-browser Select→Bounded staleness-input/badge/axe→Strong | no |
| Execute selection | SQL editor scope | yes | real-browser scripted-selection run + fixture-captured request bodies + axe | no |
| Query history with privacy controls | SQL editor scope | yes | real-browser record/reload/clear + axe | no |
| Crash recovery for unsaved editors | Main shell scope | yes | pure `serializeEditorDrafts`/`parseEditorDrafts`/`editorDraftsWorthRestoring` unit tests (round-trip/garbage/caps/clamp/worth-restoring) + real-browser type→mirror→reload→restore→Start-fresh + axe | no |
| Saved queries (folders via tags) | SQL editor scope | yes | pure `serializeSavedQueries`/`parseSavedQueries`/`upsertSavedQuery`/`removeSavedQuery`/`filterSavedQueries`/`savedQueryTags` unit tests (round-trip/garbage/caps/upsert-by-id/tag+text filter) + real-browser save→mirror→load→delete + axe | no |
| Saved-query set file export / import | SQL editor scope | yes | pure `exportSavedQueries` (stable order-independent doc) / `parseSavedQueriesExport` (wrapper or bare array, bounds, drop invalid) / `mergeSavedQueries` (add / update-if-newer / unchanged, cap, recency order) / `savedQueriesExportFilename` unit tests + real-browser Export/Import controls present + axe | no |
| Graphical EXPLAIN / EXPLAIN ANALYZE tree | EXPLAIN/profiler scope (partial) | yes | live-captured-shape parser unit + real-browser toggle/highlight + axe | no |
| Plan comparison | EXPLAIN/profiler scope | yes | capture/compare/bound pure unit + real-browser replace/compare/tab-isolation/clear + axe | no |
| Multi-tab editing | SQL editor scope | yes | real-browser cross-tab isolation/busy-notice/bound + axe | no |
| Execute script | SQL editor scope | yes | real-lexer split unit + live NSQL split/query + real-browser confirm/sequence/null-result-regression/axe | no |
| Find/replace | SQL editor scope | yes | literal-match/wrap-around/replace unit + real-browser Ctrl+F/navigate/replace/replace-all/axe | no |
| Prepared parameters (positional $1..$N) | SQL editor scope | yes | `extractQueryParams` pure unit + `studio.ValidateParams`/`ParamValues` Go unit + `TestAdminStudioWorkspaceOverNSQL` string→INT64 bind / typed-NULL bind / 413-overflow over NSQL + real-browser panel/NULL-toggle/params-in-request/axe | closes the MVP exit-gate "Prepared parameters" line |
| Dedicated JSON Explorer | yes | yes | path/tree/query/index pure unit + quoted-array parser regression + real-browser select/index/insert/axe | no |
| Basic Full-text Explorer | yes | yes | catalog/query/bound pure unit + parser/executor generated-SQL regression + real-browser insert/run/rank/marker/axe | no |
| Saved profile/OS credential manager | yes | no | no | no |
| Catalog-aware IntelliSense (table/column completion) | SQL editor scope | yes | extraction/ranking/word-range/cache-eviction pure unit + real-browser click/keyboard/cache-reuse/axe | no |
| Indexed-JSON-path completion | SQL editor scope | yes | path-extraction/range-detection/prefix-rank pure unit + real-browser dotted-path mode-switch/accept/axe | no |
| Vector-aware completion (NEAREST column + USING metric) | SQL editor scope | yes | `currentNearestContext`/`vectorColumnsFromResult`/`rankNearestColumnSuggestions`/`rankNearestMetricSuggestions` pure unit (kind-restricted metrics, CREATE INDEX USING ignored, unresolved table empty) + real-browser NEAREST/USING mode-switch/accept/axe, HAMMING not offered for VECTOR<F32,N> | no |
| Deterministic misspelled FROM/JOIN table-name suggestions | SQL editor scope (partial) | yes | Levenshtein/detection/tie/bound/apply pure unit + real-browser live-notice/fix/disappear/no-false-positive/axe | no |
| Parser diagnostics (live, grammar errors) | yes | yes | `parser.ParseDiag` offset unit + `FuzzParse` invariant + `studio.Diagnostics` line/column/multi-statement unit + `ops` handler auth/CSRF/parse-failure + `TestAdminStudioWorkspaceOverNSQL` clean/broken buffer (compiles; not run — concurrent session holds the Admin port) | no |
| Binder diagnostics (unresolved table/column + position) | yes | no | no | no |
| Query profiler breakdown | EXPLAIN/profiler scope | yes | duration/profile/bound pure unit + real-browser metrics/caveat/table/axe | no |
| GRANT/REVOKE builder | Developer operations scope | yes | pure SQL-generation unit (every scope shape, both grant/revoke, quoting) + real-browser fill/preview/insert/cancel/axe | no |
| Dedicated Vector Explorer | yes | yes | catalog/parse/metric/build pure unit + live-server metric-enforcement regression + real-browser select/inspector/insert/run/axe | no |
| Dedicated Hybrid Explorer | yes | yes | catalog-composition/filter/build pure unit + live-server clause-order/plan-shape regression + real-browser select/insert/explain/run/axe | no |
| Native Geo Explorer | yes | yes | catalog/literal/build pure unit + parser/EvalGeo-verified literal-shape regression + real-browser draw/insert/run/axe (point+radius and polygon) | no |
| Users & roles privilege explorer | Developer operations scope | yes | reverse-mapping pure unit (round-tripped through buildGrantSQL) + real-browser view/revoke-prefill/reopen-blank/axe | no |
| Transaction console + Lock explorer | Developer operations scope | yes | real-browser live-row render/refresh/reopen/no-kill/axe + existing Activity route integration | no |
| Audit viewer | Developer operations scope | yes | shared status renderer + real-browser verified→tampered refresh/suspect-row retention/reopen/axe; audit-failure text has an asserted AA contrast override for whichever theme is live (7.23:1 dark, 5.72:1 light; the assertion was pinned to the dark value and never ran until log #261 unblocked the gate) + existing Security route integration | no |
| Workflows, trigger/schedule relationships, tasks & change-streams explorer | Workflow/CDC scope (read-only) | yes | trigger/schedule shape + separated RBAC tests; live CREATE WORKFLOW/TRIGGER/SCHEDULE→bundle integration; pure graph ordering/bounds; real-browser SVG/text-alternative/refetch/filter/stream-lsn/no-mutation-control/axe | no |
| Lazy-loaded schema tree (tables→columns/indexes/foreign-keys, workflows) | Database explorer scope | yes | reuses `studio/table` + `studio/workflows` reads (no new route) + real-browser expand-Columns/expand-ForeignKeys/expand-Workflows/one-authorized-read/axe | no |
| Per-table foreign-key inspection — outbound (tree sub-branch + inspector section) and inbound "Referenced by" (inspector) | Database explorer scope | yes | `system.foreign_keys` (`table_name` + `ref_table` filters) added to the `studio/table` bundle (not `required`) + live CREATE TABLE…REFERENCES→parent/child detail integration + real-browser inspector/tree/Referenced-by render/axe | no |
| Table-inspector Dependencies panel — inbound FKs + triggers on the table | Database explorer scope | yes | `system.triggers` (`table_name` filter) added to the `studio/table` bundle (not `required`) + `TestAdminStudioWorkspaceOverNSQL` (CREATE TRIGGER → child detail carries the trigger row; unrelated table does not) + real-browser Dependencies-section render / tree Triggers sub-branch expand / axe | no |
| Schema-relationship diagram (ER from actual FKs) | Database explorer scope | yes | new `GET /api/v1/studio/schema-graph` (whole-catalog `system.foreign_keys`, 4,000-row cap, RBAC-filtered) + pure `buildSchemaGraph`/`layoutSchemaGraph` unit tests (edges/layers/cycles/oversize) + live integration edge assertion + real-browser SVG-`role=img`/aria-label/relationship-list/axe | no |
| Global object search | Database explorer scope | yes | pure `rankObjectMatches` unit tests (exact/prefix/substring/subsequence/cap) + real-browser open/type/`role=option`/Enter-opens-inspector/axe; no route (table list + one `system.workflows` read) | no |
| Global command palette (Ctrl/Cmd+K) | Main shell / UX scope | yes | pure `rankCommandMatches` unit tests (curated-order/limit/keyword/disabled-filtered/subsequence/no-match) + real-browser Ctrl+K-open/combobox/axe/filter/Enter-runs-command/Escape-closes; no route | no |
| Canonical DDL view (`system.table_ddl` + inspector DDL section) | Database explorer scope | yes | shared `internal/catalog/ddl` renderer (`ddl_test.go`) + `TestSystemTableDDL` (TABLE/INDEX rows, FK clause, **DROP + re-CREATE round-trip**) + `TestSystemTableDDLRBAC` + `ddl` line added to the `studio/table` bundle (not `required`) + live NSQL FK-clause integration + real-browser DDL-section/`Copy DDL`/axe | no |
| Schema migration history explorer (read-only, `nsql_schema_migrations`) | Developer-operations migration-workspace scope (read side) | yes | new `GET /api/v1/studio/migrations` (non-`required` read; `present=false` on absent/invisible table) + `TestAdminStudioWorkspaceOverNSQL` (present=false on fresh DB → CREATE reserved table + row → present=true, row surfaces) + no-session 401 list + real-browser open/applied-row/dirty-alert/no-apply-or-repair-button/axe/Refresh-re-reads | no |
| Data generator for development | Developer-operations scope | yes | pure `dataGenColumns` / `dataGenFieldKind` / `buildDataGeneratorSQL` unit tests (ordinal order, unsupported-type detection, deterministic-per-seed, NOT-NULL-no-default block, 100-row batching, identifier/string quoting, bounds) + real-browser open/preview/not-generatable-badge/Insert-without-execution/axe; no route (reuses `studio/table`) | no |
| CSV / JSON / NDJSON import for development | Developer-operations scope | yes | pure `parseImportText` / `autoImportMapping` / `buildImportInsertSQL` unit tests (RFC 4180 quoting/escapes/embedded delimiter, JSON key-union, NDJSON, exact case-insensitive auto-map, ordinal output order, type-checked int/bool/JSON cells with named row errors, empty→NULL / NOT-NULL block, double-map rejection, 100-row batching, identifier/string quoting, malformed-document errors, MAX_IMPORT_ROWS truncation) + real-browser paste-CSV/preview/non-importable-columns-named/Insert-without-execution/axe; no route (reuses `studio/table`) | no |
| Vector dataset import for development | Developer-operations scope | yes | pure `buildVectorImportSQL` / `autoVectorImportField` unit tests (NDJSON array embedding + scalar column, ordinal column order, exact-name field resolution, wrong-dimension named row error, embedding-field-required, NOT-NULL scalar block, quoted CSV vector cell, BITVECTOR 0/1 domain, rows×dimensions ceiling, identifier quoting, no-vector-column message) — reuses the generic importer's `parseImportText` / `importValue` and the Vector Explorer's `parseVectorLiteralInput`; no route (reuses `studio/table`) | no |
| Parameterized INSERT / UPDATE / DELETE generation | Developer-operations / data-editing scope | yes | pure `buildParameterizedDML` / `dmlDefaultColumns` unit tests (per-kind defaults, INSERT placeholder-per-column + NOT-NULL-no-default block + defaulted-column omission, UPDATE SET-then-WHERE param order + SET/WHERE-overlap + WHERE-required, DELETE composite key, unknown/duplicate column names, `MAX_QUERY_PARAMS` ceiling, identifier quoting) + real-browser open/INSERT-template/switch-to-DELETE/Insert-without-execution/axe; no route (reuses `studio/table`) | no |
| Table designer | Database explorer scope | yes | pure `buildCreateTableSQL` unit (default UUID PK, reserved `nsql_` prefix, missing/composite PK, VECTOR-PK rejection, AI() only on DECIMAL, CHAR/VECTOR/DECIMAL type SQL, quoting, FK clause) + real-browser Design-schema open/preview/axe; no route | no |
| Index designer | Database explorer scope | yes | pure `buildCreateIndexSQL` unit (btree INCLUDE, unique, JSON path, fulltext analyzer + non-text rejection, HNSW F16, IVFPQ, SPARSE-kind restriction, spatial, unknown/include-overlap, quoting) + real-browser switch-to-index/name+key/Insert-without-execution/axe; no route (reuses `studio/table`) | no |
| Generated native DDL preview (live, as you edit) | Database explorer scope | yes | designer preview is the same quoted native DDL Insert loads into the editor; covered by the table/index designer unit + real-browser preview assertions | no |
| Editable data grid, staged-change review & transactional commit | Data-editing scope / MVP exit-gate "Table inspector/data editor" | yes | pure `detectEditableTable`/`isResultEditable`/`extractRowPK`/`makeRowKey`/`stageCellUpdate`/`stageRowDelete`/`stageRowInsert`/`buildStagedChangeSQL` unit tests (updates, deletions, insertions, composite keys, revert-to-original coalescing, bounds) + `npm run build`/`go build ./cmd/nextsql-admin` | closes the MVP exit-gate "Table inspector/data editor" line |

“Production-gated” remains **no** for the implemented rows because the Studio
MVP exit gate includes the open connection manager (profiles, OS credential
storage, TLS/mTLS fields, production labeling/safety mode), parser/binder
diagnostics, migration, and other surfaces. M1–M3 plus
the focused editor/profiler/JSON/full-text/vector/hybrid/geo slices are
implemented and verified, not a phase-completion claim.

## 7. Next increments

All five originally scoped native explorers (JSON, Full-text, Vector,
Hybrid, Geo), the Users & roles privilege explorer, the read-only
Transaction/Lock and Audit explorers, catalog-aware IntelliSense
(table/column completion), deterministic misspelled FROM/JOIN
table-name suggestions, table/index statistics inspection, the
lazy-loaded schema tree, per-table foreign-key inspection (outbound
constraints + an inbound "Referenced by" grid), a schema-relationship
diagram and a global object search in the database explorer, the read-only
Workflows, trigger/schedule relationships, tasks & change-streams explorer,
indexed-JSON-path completion, vector-aware NEAREST/USING completion,
the production environment tag + read-only safety mode, switch-realm/database
connection re-targeting, a per-session read-consistency mode
(strong/bounded/stale), a recent-connections quick-switch, a global
command palette, the visible cross-database administration warning for
realm-wide user/role DDL, git-friendly
file export/import of the saved-query set, positional prepared
parameters ($1..$N), a canonical-DDL view (`system.table_ddl` + an
inspector DDL section), the unified table Constraints panel, and a
read-only schema-migration history explorer (`nsql_schema_migrations`
via `GET /api/v1/studio/migrations`), a development data generator
(`INSERT`-script builder over authorized `system.columns`), a
CSV / JSON / NDJSON import (a type-checked `INSERT`-script builder that maps
a pasted or loaded document onto the target table's columns), a vector
dataset import (its vector-typed counterpart — one embedding field onto a
`VECTOR` / `BITVECTOR` / `SPARSEVECTOR` column, validated against declared
dimensions), a
table-inspector **Dependencies** panel (inbound FKs + triggers defined on
the table, from `system.triggers`), and **parameterized
`INSERT` / `UPDATE` / `DELETE` generation** (a `$1..$N` statement template
built from a table's authorized columns into the editor, bound in the existing
Parameters panel), **layout persistence without credentials** (resizable
explorer/inspector panes, hide/show, last authorized table name, per-connection
`localStorage`, never a secret), a **table / index designer** (a form that
emits native `CREATE TABLE` / `CREATE INDEX` into the editor with live DDL
preview, never executing), and **live parser diagnostics** (a debounced
`POST /api/v1/studio/query/diagnostics` — parser-only, no connection —
locating each statement in the buffer that fails to parse, shown as a strip
with a Go-to-error control; grammar errors only, the binder half stays open)
are now
implemented; the RBAC boundary is integration-test-covered.
The next coherent Studio work should preserve this boundary and choose one
of:

1. the remaining connection-manager scope: multi-environment *profiles*
   with OS credential storage and TLS/mTLS fields. The confirm-before-run
   destructive-statement warning, the visible realm-wide administration
   warning, the production environment tag + read-only safety mode, the
   **switch realm/database** re-targeting, and the per-session
   **read-consistency** mode above are the connection-manager checklist slices
   already implemented — the first three work against whichever connection
   Studio currently holds; the fourth
   moves that connection to a different realm/database on the same
   `nextsqld` (a fresh authenticated connection swapped in atomically;
   `POST /api/v1/studio/reconnect`; password supplied each time, never
   stored); the fifth sets STRONG/BOUNDED/STALE live on the current
   connection (`POST /api/v1/studio/read-consistency`, reads only). A
   bounded `localStorage` **recent connections** quick-switch (realm/
   database pairs, never a credential) already prefills the switch form.
   What remains is architecturally larger than a single slice:
   `internal/admin/studio.Bootstrap` still carries only the one
   process-managed host, so *named profiles* pointing at **different**
   `nextsqld` hosts — with their own TLS/mTLS material and an OS-keychain
   credential store — plus a full recent-connections *home screen* first
   need a real multi-target connection model, not just new form fields;
2. the remaining SQL-editor scope: source-position **binder**
   diagnostics
   (execute-selection, query history, multi-tab editing, execute script,
   find/replace, catalog-aware IntelliSense, deterministic misspelled
   table-name suggestions, indexed-JSON-path completion, vector-aware
   NEAREST/USING completion, live parser diagnostics, crash recovery
   for unsaved buffers, saved queries with tag folders, git-friendly
   file export/import of the saved-query set, positional prepared
   parameters, a **SQL formatter**, and the global command palette above are
   the editor/shell slices already implemented; column-level
   misspelling suggestions are deliberately not among them — see that
   section's rationale on why only FROM/JOIN table names are safely
   deterministic without real AST access. **SQL formatting is now
   implemented** (a **Format** button / command-palette entry / Shift+Alt+F
   over pure `formatSQL` in `resultTools.ts`): the earlier "blocked" note
   assumed reusing `internal/sql/lexer` (which discards comments and folds
   case); the shipped formatter is instead a self-contained,
   comment-preserving tokenizer plus a clause-level reflow, guarded by a
   strict re-tokenize equivalence check (keywords case-insensitive, every
   other token and every comment byte-for-byte and in order) that returns
   the buffer unchanged on any mismatch, any unterminated
   string/comment/quoted-identifier, or a > 1 MiB buffer — so it can only
   ever change whitespace and keyword case, never meaning. It never
   executes and adds no route; parenthesized groups (column-def lists,
   VALUES tuples, subqueries) are kept on one line by design.
   NextSQL-native syntax highlighting stays blocked on `@bzync/rui`'s
   `CodeEditor` having no highlight-overlay primitive at all — materially
   larger than this frontend can make alone in one slice; **live parser
   diagnostics are now implemented** without touching the ~100 individual
   parser error sites — `parser.ParseDiag` attaches the stopped-on token's
   byte offset at the top of `Parse`, `studio.Diagnostics` maps it per
   statement back to the buffer, and a debounced strip under the editor
   shows it with a Go-to-error control (see the parser/binder-diagnostics
   subsection above); the **binder** half — unresolved table/column names
   with a position — is what stays a core-decoder change, and additionally
   needs a name-resolution surface the Admin client does not have;
   vector-aware completion is implemented for the two slots the catalog
   actually describes — the NEAREST column and the USING metric — and
   still refuses to guess inside TO (...)); or
3. the remaining Developer operations migration workspace. The **read side**
   is now implemented: a read-only migration history explorer over the
   reserved `nsql_schema_migrations` table (`GET /api/v1/studio/migrations`,
   a non-`required` read that reports `present=false` when the migration
   system has never run or the caller cannot see the table), showing the
   applied history with a current-version / dirty-state summary and a
   `role="alert"` for any dirty version. The read-only Users & roles,
   Transaction/Lock, and Audit slices established the reused-catalog boundary
   this follows. What remains is the mutating half — create / validate /
   dry-run / apply / down / repair — and it genuinely cannot move into
   Studio: those verbs operate on the local migration *files*, and
   `nextsql-admin` is a pure `nextsqld` protocol client with no data
   directory or migration folder. The **development data generator** — a
   deterministic, seed-driven `INSERT`-script builder over authorized
   `system.columns`, loaded into the editor for review, no route — is also
   implemented here, as is **CSV / JSON / NDJSON import**: `parseImportText` /
   `buildImportInsertSQL` map a pasted or loaded document onto the target
   table's authorized columns and emit a bounded, type-checked `INSERT`
   script into the editor — the same never-execute boundary, no route. A
   protocol-only client cannot instead *stream* bulk rows into the server
   without its own admission/backpressure design; a reviewable script needs
   none. **Vector dataset import** is the vector-typed counterpart of that
   importer (`buildVectorImportSQL`): it maps one embedding field onto a
   `VECTOR` / `BITVECTOR` / `SPARSEVECTOR` column (validated against declared
   dimensions and the 0/1 domain by the Vector Explorer's own
   `parseVectorLiteralInput`), any other fields onto scalar columns, and
   emits the native parenthesized vector literal — same boundary, no route,
   with a rows×dimensions ceiling on top of the shared byte bounds.
   **Parameterized `INSERT` / `UPDATE` / `DELETE` generation** is also
   implemented here — `buildParameterizedDML` emits a `$1..$N` statement
   template for a table from its authorized columns into the editor (bound in
   the existing Parameters panel), the same never-execute boundary, no route;
   `UPDATE`/`DELETE` require an explicit WHERE key. **The data-editing
   bucket is also now implemented**: an editable result grid
   (`detectEditableTable`/`isResultEditable`/`ResultGrid`) with
   in-place cell editing, row deletion/insertion staging
   (`stageCellUpdate`/`stageRowDelete`/`stageRowInsert`), a staged-change
   review modal (`StagedChangesReviewModal`), and atomic transactional
   commit/discard (`buildStagedChangeSQL` compiled into a bounded
   `BEGIN; ... COMMIT;` script, executed over the session connection with
   automatic `ROLLBACK;` on failure) — see the "Editable data grid" section
   above. Unlike every other bucket item this one *does* write, but only
   through the operator's own authenticated connection, so RBAC applies
   exactly as it would to hand-typed SQL. What remains in this
   Developer-operations bucket is *streaming* bulk
   import and a `nextsql-bench` result viewer (needs bench result artifacts a
   protocol-only client never holds). The detailed EXPLAIN/profiler checklist
   is complete with the tree, plan comparison, and bounded ANALYZE-only
   profile above; or
4. the remaining database-explorer schema tooling. Table/index statistics
   inspection, the lazy-loaded schema tree (tables→columns/indexes/foreign-keys
   plus a read-only workflows branch), per-table foreign-key inspection —
   outbound FKs (schema-tree sub-branch + inspector section) and an inbound
   **Referenced by** grid — and the **schema-relationship diagram** (a modal
   inline-SVG ER view laid out from `GET /api/v1/studio/schema-graph`, with a
   grouped relationship list as its text alternative and large-schema
   fallback), all over the read-only `system.foreign_keys` view
   (`table_name, constraint_name, ordinal, column_name, ref_table, ref_column,
   on_delete, on_update`, table-visibility-filtered like `system.columns`),
   above already extend the lazy `system.*` reads as far as they go — as does
   the **global object search** (a keyboard finder over table + workflow
   names, `rankObjectMatches`, no route). Canonical DDL is done end to end —
   `system.table_ddl` (`table_name, object_type, object_name, ddl`,
   table-visibility-filtered; log #187) renders each visible table's and
   index's `CREATE` statement via the shared `internal/catalog/ddl` renderer
   (extracted from `internal/xport` so `internal/executor` can depend on it),
   correct for FK ordinals, expression/JSON-path indexes, vector
   method/quantization, full-text analyzer, ENUM/CHAR/VARCHAR types, and
   `DEFAULT` literals — and the inspector's **DDL** section (log #188)
   consumes it: a focusable copyable script plus Open-in-editor, from a
   non-required line in the existing table-detail bundle.
   The table inspector's **Constraints** section now also composes the four
   already-exposed native shapes — primary key and NOT NULL from
   `system.columns`, UNIQUE indexes from `system.indexes`, and grouped foreign
   keys from `system.foreign_keys` — into one bounded grid without inventing a
   redundant server view. The inspector's **Dependencies** section composes the
   inbound "Referenced by" grid with the triggers defined on the table, read
   from `system.triggers` (`table_name` filter, a third non-`required`
   table-detail query; the schema tree gains a matching per-table **Triggers**
   sub-branch). The table/index *designer* is implemented: a form that emits
   native `CREATE TABLE` / `CREATE INDEX` into the editor for review, with live
   DDL preview as you edit, never executing (closed type list, required
   PRIMARY KEY, kind-restricted index options). What remains blocked here is
   the rest of a full dependency view — workflow / trigger *bodies* that
   free-reference a table need a source-text catalog contract that does not
   exist, and NextSQL has no views. NextSQL has no
   `CHECK` constraint or `system.constraints` view — "constraints" in this
   engine are PK (`system.tables.pk`), `UNIQUE` (a unique `system.indexes`
   row), `NOT NULL` (`system.columns.not_null`), and FK
   (`system.foreign_keys`); that existing union is now the implemented
   Constraints panel; or
5. the remaining Workflow/CDC scope. The read-only Workflows, tasks &
   change-streams explorer above establishes the reused-catalog boundary
   and covers the read side of the workflow explorer, task monitor, and
   CDC/change-stream explorer. The `system.triggers` / `system.schedules`
   surfaces and their bounded accessible definition-topology diagram now
   close the trigger/schedule relationship and workflow-diagram checklist
   items. What remains here is workflow-body display (which would require a
   new source-text catalog contract) plus the mutation actions `CANCEL TASK`
   and client-stream pause/resume (deliberately deferred — Studio is not the
   CDC consumer).

No later increment may weaken server-side RBAC, make a root key available to
Studio, cache live `/api/*` responses in the PWA service worker, or use a
private engine shortcut.
