# Studio SQL completion and feature notes

Studio automatically opens SQL suggestions after two identifier characters or
a qualifier dot. Type to filter, use Up/Down to navigate, Enter/Tab to insert,
and Escape to dismiss. **Suggest** and Ctrl+Space open the list explicitly.
Pasting a script or restoring a draft does not automatically open the list.
The popup stays within the visible workspace; navigating suggestions scrolls
the list without moving the editor sideways.

The reference covers all documented statement families, native column types,
and built-in names from this Admin build. Statement heads such as `CREATE SC`
complete to `CREATE SCHEDULE` as one replacement. `CREATE TABLE`, `ADD COLUMN`,
and `CAST` type slots offer native types; vector-index `USING` offers HNSW,
IVF, IVFPQ, and SPARSE. Existing table/column, alias, primary/foreign-key,
relational JOIN, indexed JSON-path, and vector-column/metric completion remains
available from the authenticated catalog.

Function signatures provide hints where curated. Other native built-ins show
their name with an argument placeholder rather than a guessed signature.
Window functions carry an `OVER (...)` note; `AI()` is labeled default-only.
`LIKE` remains an infix predicate and is not offered as `LIKE()`.

## Availability and warnings

Studio uses the connected server's `system.capabilities` for feature status:

| Status | Editing behavior |
| --- | --- |
| Supported / production | Feature completions remain available. |
| Experimental | Selectable, with an experimental note. |
| Deprecated | Selectable, with a deprecation note. |
| Unsupported / disabled / unavailable | Labeled unavailable; accepting the suggestion does not insert it. |
| Missing, conflicting, or unknown metadata | Selectable, explicitly labeled unverified. Missing metadata does not prove lack of support. |

Feature notes appear below the editor for recognized unavailable commands,
feature-dependent constructs, and unknown built-in calls. For example:

- `CREATE DATABASE`: explains the one-database-per-deployment model.
- `CREATE PROCEDURE` / `CALL`: points to `CREATE WORKFLOW` / `RUN WORKFLOW`.
- `VACUUM`: points to `MAINTAIN`.
- `RESTORE`: points to offline CLI restore/PITR.
- `SELECT YEAR(NOW())`: warns that `YEAR()` is absent from this Admin build's
  built-ins; the native date-part function is `EXTRACT('year', ts)`.

Comments, literals, quoted identifiers, column declarations, and CTE column
lists are excluded from unknown-function detection. Notes include a location
and a jump control. They are advisory and never execute SQL or block Run.

**Version and authorization boundary:** the language reference and live parser
diagnostics belong to the Admin build; capabilities belong to the connected
server. Capabilities describe feature families, not every function signature,
index option, deployment setting, or user permission. They cannot establish
that a newer Admin function exists on an older server. Run remains the
authoritative server parse/bind/authorization check. Studio does not invent
user-defined functions, change server capabilities, or bypass RBAC.

## Bounded implementation

- Automatic opening is debounced by 120 ms; feature notes by 250 ms.
- Automatic lexical assistance stops above 131,072 UTF-16 code units and
  displays a large-buffer notice. Execution retains its existing limits.
- Suggestions stay capped at 50 and notes at 20. Catalog references retain
  the existing 8-table reference and 16-table cache bounds.
- No new request is needed for static SQL suggestions or feature notes.
  Catalog metadata still loads lazily over the authenticated Studio API.
- Credentials and results are not added to completion state or storage.

## Source and verification audit

Owning phase: P29. No persistent, wire/API, catalog, transaction/WAL, recovery,
Raft, permission, or driver contract changes are introduced.

| Surface | Implemented source | Verification |
| --- | --- | --- |
| Commands | `sqlCommands.json`, with complete example statements | Every example parses; every documented top-level statement has a completion. |
| Built-ins and system tables | `generate-sql-reference.mjs` derives `sqlReference.generated.json` from the native binder, geometry/spatial registries and system schema | Staleness check; parser/binder recognition; exact native system-table registry comparison. |
| Column types | `NEXTSQL_TYPE_SUGGESTIONS` | Every suggestion parses as a declared type and every documented native type is covered. |
| Availability and notes | `sqlCompletion.ts` | Unit checks for supported/unsupported/experimental/deprecated/unknown and conflicting metadata, source ranges, quoting/comments, CTEs and resource bounds. |
| Editor integration | `StudioWorkspace.tsx` | Chrome tests for automatic popup/focus, whole-command insertion, unavailable algorithm handling, warning display and literal exclusions; existing Studio interaction and accessibility suite. |

`npm run build` refreshes the generated inventory. `npm run test:studio` rejects
a stale inventory and tests malformed Go case-label lists in a timeout-bounded
subprocess. Label separators are mandatory commas, preventing ambiguous regex
backtracking in the build-time extractor. Run `make test-admin-ui` and `go test ./tests/docs` for the
frontend gate and native-reference parity checks. These extend the completed
P29 gate; they introduce no claim about newly supported engine syntax.
