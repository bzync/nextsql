# NextSQL Admin UI design system

Owning phases: P28 (Setup and Operations) and P29 (Studio). The design system
extends `@bzync/rui` rather than creating another component library. Its source
is `internal/admin/frontend/src/shared/design-system.css`; mode styles own
layout, pane sizes, and overflow. Fonts are bundled for offline use.

## Visual contract

- Use neutral backgrounds, white or charcoal work surfaces, and blue for
  actions, focus, and selection. Reserve status colors for actual states.
- Use Inter for interface text and catalog tables. Use JetBrains Mono for SQL,
  identifiers, paths, and values that benefit from character alignment.
- Use the shared 4/8/12/16/24/32px spacing scale. Standard controls are 36px;
  compact actions are 32px. Keep the editor's deliberately smaller tools and
  the operator's table-density preference.
- Use 6px control corners and 8px panel corners. Separate persistent surfaces
  with borders; reserve elevation for floating menus and dialogs.
- Use one page heading and a short description. The topbar provides location,
  server/environment context, global search, and theme controls. The desktop
  account control lives in the navigation footer; mobile uses the topbar.
- Use the same underlined tab treatment in views and dialogs. Selection has
  an explicit rule and font weight, with keyboard focus shown separately.

## Component conventions

| Surface | Implementation | Convention |
| --- | --- | --- |
| Buttons and fields | RUI `Button`, `Input`, `Select`, `Textarea` | Use component size/variant props. Primary for the main action, secondary for routine actions, ghost for quiet tools, destructive for destructive actions. |
| Page headings | RUI `PageHeader` with `nsa-page-header` | Single heading, description, compact actions; avoid repeated breadcrumbs and mode kickers. |
| Setup headings | `StepHeader` with `nsa-step-header` | Same typography and title/description rhythm on every step. |
| Navigation | RUI `NavigationLink` in the shared shell | Text and icon, persistent active marker, collapsible desktop rail, horizontal mobile navigation. |
| View tabs | RUI `Tabs` family | Shared semantic selectors style view and dialog tabs; retain arrow-key navigation and scroll access. |
| Cards and sections | RUI `Card`; Operations `Section` | Flat bordered surfaces; mode CSS owns layout. |
| Summary metrics | RUI `Stat` in `StatGrid` | One divided surface, tabular figures, factual labels and descriptions. No invented trends. |
| Catalog results | Operations `ResultTable` | Shared title/filter/export toolbar, scroll region, footer and density preferences. |
| Query workspace | Studio panes and editor controls | Same tokens and panel headers; container queries stack panes according to available workspace width. Preserve resizing, virtualization and bounded streaming. |
| Dialogs | RUI modal primitives | Shared type, corners, fields and tabs; retain focus trapping and restoration. |

RUI 0.0.9's unlayered `font: inherit` reset overrides its own layered control
size utilities. The shared stylesheet uses `revert-layer` for font size,
weight, and line height to restore component sizing. It also gives tables an
explicit application font, avoiding browser-default table typography.

## Scope and verification

This is a presentation change. There are no changes to persistent formats,
wire/API contracts, catalogs, transactions, WAL/recovery, Raft, RBAC, tenant
boundaries, query execution, or driver behavior. Existing session and
production-environment indicators remain in the shell. No network fonts,
new runtime dependencies, or background work are introduced.

| Area | Designed | Implemented | Verification |
| --- | --- | --- | --- |
| Shared tokens and primitive styling | Yes | Shared stylesheet in all three modes and portaled dialogs | Frontend build/typecheck; Chrome/axe audit |
| Operations hierarchy and navigation | Yes | One search/account control per viewport, compact header and divided metrics | Browser assertions, light/dark axe, 360px mobile and DPR 2 checks |
| Setup and Studio consistency | Yes | Shared typography, palette, corners and panel headers | Existing Setup wizard and Studio interaction/accessibility suite |
| Engine and security gates | Existing contracts | Unchanged | This change adds no engine behavior or phase-gate claims |

Run the frontend gate with `make test-admin-ui`. To save review screenshots
of deterministic fixture views, run from `internal/admin/frontend`:

```bash
NEXTSQL_ADMIN_SCREENSHOTS=/tmp/nextsql-admin-design npm run test:a11y
```

The screenshots contain fixture data. They demonstrate presentation; they
are not evidence of a live server's health or performance.
