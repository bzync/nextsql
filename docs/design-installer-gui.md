# NextSQL GUI Installer — design

> Status: **M1 in progress (2026-09-04).** Architecture decision made via
> `AskUserQuestion`; the serving backbone + first working wizard flow
> (welcome → data directory/key file → resource preset → administrator
> account → summary → install → completion) is being built as
> `cmd/nextsql-install` / `internal/installgui`.

This is the last open piece of Phase 28's exit gate (`TODO.md` "Installer
UX"). `PROJECT.md` is the authority for intended scope; `TODO.md` Phase 28
"Installer UX" for status. It is a separate product from NextSQL Manager
(`docs/design-manager.md`) — the Installer's job ends once a database exists
and is verified healthy; Manager takes over from there.

```text
NextSQL Installer → install / upgrade / repair / uninstall   (this doc)
NextSQL Manager   → server / cluster / security / backup / operations
NextSQL Studio    → database development / SQL / data / schema / RAG
```

## 1. Architecture decision — local web app driving `nextsql setup`

Chosen 2026-09-04 via `AskUserQuestion`, over a native Go GUI toolkit
(Wails/Fyne), three independent per-OS native installers (NSIS custom pages /
macOS Installer.app / Linux CLI-only), and a terminal-UI-first wizard.

**The GUI installer is a small Go binary (`nextsql-install`) that opens a
loopback HTTP server with an embedded wizard UI and launches the operator's
system browser to it.** This is the same architecture already chosen for
NextSQL Manager (`docs/design-manager.md` §1), for the same reasons —
smallest attack surface (no bundled runtime, no native IPC bridge), one
static Go binary per OS, real-browser accessibility for free, and no new
per-OS webview/toolkit dependency. `packaging/README.md` already documented
this intent before any code existed: *"The OS installers and, later, the GUI
installer drive that same command \[`nextsql setup`\]; every option they
expose is available to a script."*

### The installer never touches the engine directly — it drives the CLI

Unlike Manager (which talks to a *running* `nextsqld` over the NSQL wire
protocol), the installer's job is to create the very first database, before
any server is running. Two ways to do that were considered:

1. **Extract `nextsql setup`'s internals into a shared package** both the CLI
   and the GUI import directly (in-process).
2. **Shell out to the already-built, already-tested `nextsql` binary** —
   `nextsql setup --dry-run --json …` for live validation, `nextsql setup
   --json …` to commit — and treat its JSON stdout as the API response.

**Chosen: (2), subprocess.** `cmd/nextsql/setup.go`'s orchestration
(transactional rollback, health verification, config round-tripping) is
already implemented, fuzz/race/integration-tested, and reused unchanged; a
refactor to share it in-process would touch code that generates root keys
and bootstraps the catalog for two more call sites (CLI *and* GUI) for a
marginal saving, which is the wrong trade against `SKILLS.md`'s bias toward
the smallest change that does not touch already-verified crypto/bootstrap
paths without new dedicated coverage. Subprocess isolation is also a real
security property: `internal/installgui` **never links against
`internal/crypto`, `internal/storage`, `internal/setup`, or `internal/
sysinfo`** (enforced by `imports_test.go`, mirroring Manager's own
engine-import test) — a bug in the HTTP/JS layer cannot reach key material
in-process, because that process never holds any. The root password crosses
one boundary only: the browser posts it over loopback HTTP to
`nextsql-install`, which writes it to a mode-0600 temp file for the single
subprocess invocation and removes the file immediately after — the same
`--password-file` contract every other NextSQL surface already uses, never a
URL or an argv (`ps` would leak an argv value; a temp file does not).

### Single-operator token auth, not a login screen

The installer runs once, locally, before any user/password/RBAC system
exists — there is nothing to log in *as*. Modeled on the same trust boundary
Jupyter's local notebook server uses: `nextsql-install` generates a random
256-bit token at startup, prints/embeds it in the URL it opens
(`http://127.0.0.1:PORT/?token=…`), and requires it (via cookie, set on the
first page load, plus an `X-Installer-Token` header the bundled JS attaches
to every `/api/*` call) for everything else. A stray localhost process
without that token gets `403`. This is deliberately simpler than Manager's
session store (no concurrent multi-operator use case exists here) but keeps
the same CSP / security-header posture (`default-src 'self'`, no inline
script, `X-Frame-Options: DENY`).

### Consequences

- `nextsql-install` requires the `nextsql` binary alongside it (same
  directory as its own executable, overridable with `--nextsql-bin`, else
  `PATH`) — packaging always ships them together already.
- The installer holds no durable state and is safe to kill at any point
  before the final "Install" step is confirmed: every screen up to Summary
  only ever calls `nextsql setup --dry-run`, which writes nothing.
- Any GUI installer bug is at most as dangerous as running `nextsql setup`
  by hand with the same flags — no new mutation path is introduced.

## 2. Milestone decomposition

| Slice | Scope | Status |
|---|---|---|
| M1 | Serving backbone + token auth + one working flow: Welcome → data dir/key file (live capacity/permission feedback via dry-run) → resource preset → administrator account → summary → install → completion | **in progress** |
| M2 | Encryption setup wizard detail: generate-vs-import root key choice, recovery-key export/verification UX, "never upload root key" messaging surfaced explicitly (not just enforced by design) | open |
| M3 | Advanced/component selection (skip-init config-only mode, TLS certificate assistant for a remote listen address, custom buffer-pages) | open |
| M4 | Packaging integration: bundle `nextsql-install` into the `.tar.gz`/`.run`/`.deb`/`.msi`/`.pkg` artifacts as the default interactive entry point (`scripts/build-*-installer.sh`), auto-exit-on-completion instead of requiring Ctrl+C | open |
| M5 | Accessibility pass (keyboard-only walkthrough, screen-reader labels audit, prefers-reduced-motion, high-contrast) + light/dark/system theming | open |

Each slice is its own scoped increment, logged in `TODO.md` under Phase 28
"Installer UX", same discipline as the Manager MVP's M1–M9.

## 3. API surface (M1)

All under the single-operator token described above. Every request/response
body is JSON.

| Method | Path | Effect |
|---|---|---|
| `GET` | `/api/v1/hello` | Version/phase, OS-appropriate default paths, whether running elevated (root/Administrator) — no side effects, no subprocess |
| `POST` | `/api/v1/plan` | Runs `nextsql setup --dry-run --json` with the operator's current form values; returns hardware detection, resource-preset recommendation, resolved paths, and warnings. Writes nothing. |
| `POST` | `/api/v1/install` | Runs `nextsql setup --json` (no `--dry-run`) with the same values — the one mutating call, gated behind the Summary screen's explicit confirmation. |

`internal/installgui.Params` is the one shared shape between `/plan` and
`/install` (same fields, same validation, same argv-building) so the
Summary screen is guaranteed to describe exactly what Install will do.

## 4. Non-goals for M1

- No packaging integration yet (M4) — `nextsql-install` is built and tested
  standalone; wiring it into the OS installer artifacts is a separate,
  reversible follow-up once the flow is proven.
- No recovery-key export UI yet (M2) — `nextsql setup` does not generate a
  separate recovery key today (single root unlock key), so this waits on
  that capability existing.
- No TLS certificate assistant (M3) — the installer defaults to the
  loopback, TLS-optional path `nextsql setup` already supports; a remote,
  TLS-required listen address is left to the CLI/config file for now.
