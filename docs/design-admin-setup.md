# NextSQL Admin — Setup mode design (formerly "NextSQL GUI Installer")

> **2026-09-05 (log #142): NextSQL Installer, NextSQL Manager, and NextSQL Studio were
> merged into one product, NextSQL Admin — see `docs/design-admin.md` for the umbrella
> architecture (one binary `nextsql-admin`, mode detection, the unified frontend).** This
> document is the still-authoritative implementation history and design record for what
> is now **Setup mode**: `nextsql-install`/`internal/installgui` became
> `nextsql-admin`/`internal/admin/setup`; behavior, routes, and every milestone decision
> below are unchanged, only the binary/package names moved, and the "separate product
> from Manager" framing below is superseded (see `docs/design-admin.md`). Historical text
> below still says "the Installer"/"GUI installer" — read that as "Setup mode".

> Status: **M1, M3, M5, and M6 complete; M4 (packaging integration) landed for
> the tarball/.run/.deb Linux artifacts (2026-09-05).** Developer and
> production deployment profiles plus the production security preflight
> landed 2026-09-06 (log #208): the Resources step defaults to Production,
> requires an administrator, and disables skip-init on that profile.
> Architecture decision
> made via `AskUserQuestion`; the serving backbone + working wizard flow
> (welcome → data directory/key file → resource preset [+ remote listen/TLS,
> M3 + start-at-boot, M6] → administrator account → summary → install →
> completion) ships as `cmd/nextsql-install` / `internal/installgui`. The
> frontend was rebuilt on React + `@bzync/rui` (2026-09-05, matching
> Manager's own stack — see §5). M2 is partial (generate-vs-import
> disclosure landed; recovery-key export/verification open). **M5 is
> complete (2026-09-05, log #141)**: Installer and Manager now share the
> installer's branded shell language and explicit system/light/dark control;
> deterministic real-Chrome keyboard + screen-reader-semantics/axe audits,
> including increased-contrast and reduced-motion emulation, are green.

This is the last open piece of Phase 28's exit gate (`TODO.md` "Installer
UX"). `PROJECT.md` is the authority for intended scope; `TODO.md` Phase 28
"Installer UX" for status. Setup mode's job ends once a database exists and
is verified healthy; Operations mode (`docs/design-admin-operations.md`)
takes over from there within the same `nextsql-admin` process/shell — see
`docs/design-admin.md` for how mode selection works.

```text
Setup mode       → install / upgrade / repair / uninstall            (this doc)
Operations mode  → server / cluster / security / backup / operations (docs/design-admin-operations.md)
Studio mode      → database development / SQL / data / schema        (Phase 29)
```

## 1. Architecture decision — local web app driving `nextsql setup`

Chosen 2026-09-04 via `AskUserQuestion`, over a native Go GUI toolkit
(Wails/Fyne), three independent per-OS native installers (NSIS custom pages /
macOS Installer.app / Linux CLI-only), and a terminal-UI-first wizard.

**Setup mode is a small Go binary (`nextsql-admin`, formerly `nextsql-install`)
that opens a loopback HTTP server with an embedded wizard UI and launches the
operator's system browser to it.** This is the same architecture already
chosen for Operations mode (`docs/design-admin-operations.md` §1), for the
same reasons —
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
| M1 | Serving backbone + token auth + one working flow: Welcome → data dir/key file (live capacity/permission feedback via dry-run) → resource preset → administrator account → summary → install → completion | **complete (2026-09-04; targeted tests and live API-to-database verification green)** |
| M2 | Encryption setup wizard detail: generate-vs-import root key choice, recovery-key export/verification UX, "never upload root key" messaging surfaced explicitly (not just enforced by design) | **generate-vs-import disclosure landed (2026-09-04)**; recovery-key export/verification still open |
| M3 | Advanced/component selection (skip-init config-only mode, TLS certificate assistant for a remote listen address, custom buffer-pages) | **complete (2026-09-04)** — all three pieces landed: skip-init (M1, log #136), custom buffer-pages (M1, log #135), remote listen + TLS (log #138) |
| M4 | Packaging integration: bundle `nextsql-admin` into the `.tar.gz`/`.run`/`.deb`/`.rpm`/`.msi`/`.pkg` artifacts as the default interactive entry point (`scripts/build-*-installer.sh`), auto-exit-on-completion instead of requiring Ctrl+C | **Linux (`.tar.gz`/`.run`/`.deb`/`.rpm`) complete (2026-09-05, `.rpm` added log #143)** — see below; Windows/macOS packaging out of scope (no build host in this environment) |
| M5 | Accessibility pass (keyboard-only walkthrough, screen-reader labels audit, prefers-reduced-motion, high-contrast) + light/dark/system theming | **complete (2026-09-05, log #141)** — deterministic real-Chrome Installer + Manager keyboard flows and axe WCAG 2.2 A/AA audits green; explicit three-state theme selection, transition focus/live announcements, semantic progress, associated errors, reduced motion, and increased/forced contrast landed |
| M6 | Service registration (`PROJECT.md` §46): an optional "start automatically at boot" step that enables an *already-installed, already-matching* systemd unit | **complete (2026-09-04)**, scoped to enabling an existing unit — see below; authoring/writing a unit file stays the packaged OS installer's job |

Each slice is its own scoped increment, logged in `TODO.md` under Phase 28
"Installer UX", same discipline as the Manager MVP's M1–M9.

### M2 progress: generate-vs-import disclosure (2026-09-04)

`nextsql init` already behaved correctly — a `--key-file` path that exists is
imported and reused, one that doesn't is generated fresh — but nothing said
so before the operator committed. Closed with no new engine surface, pure
disclosure: `nextsql setup`/`nextsql setup --dry-run` now `os.Stat`s
`--key-file` and (its default-derived) `--instance-key-file` before doing
anything else, and reports the answer as `key_file_exists`/
`instance_key_exists` booleans plus a matching advisory string in `--json`
`warnings` (and the text renderer's `key-file` line). `internal/installgui`
needed zero Go changes — it already forwards `nextsql setup`'s JSON
verbatim — only the Location step (an explicit "existing key will be
imported" / "new key will be generated" banner, refreshed on every `Check`)
and the Review step (the same disclosure next to the key path, right before
the operator confirms) in `web/app.js`. This is advisory text only: it does
not add a toggle that could fight the existing, already-safe stat-based
behavior, and a missing plan (operator hasn't pressed Check yet) reads as
"unknown" rather than guessing. Recovery-key export/verification (the rest
of M2) is unaffected and still open — see the non-goals below.

### M3 complete: remote listen address + TLS (2026-09-04)

The Resources step gained a second, independent "Advanced" disclosure (next
to the existing skip-init one): "configure a remote listen address", which
reveals a listen-address field plus TLS certificate/private-key path fields.
`internal/installgui.Params` gained `ListenAddr`/`TLSCert`/`TLSKey` — plain
strings passed straight through to `nextsql setup --listen/--tls-cert/--tls-key`,
exactly like `KeyFile`: **paths** the operator already placed on this
machine, never file content, never uploaded. `Params.Validate` only enforces
that `TLSCert`/`TLSKey` travel together (the same non-authoritative,
"catches a typo before spending a subprocess call" role `Validate` already
plays elsewhere) — installgui still never imports `internal/setup`
(`imports_test.go`), so it cannot and does not duplicate that package's
loopback-address parsing; `nextsql setup --dry-run`, which already refuses a
non-loopback address without both TLS flags (`setup.ErrInsecureRemote`, exit
6, writes nothing), stays the one authoritative check, surfaced through the
existing plan-error banner. The UI adds a client-side *hint only* (a
regex-based "does this look non-loopback" guess) so an operator sees the TLS
requirement before pressing Continue instead of only after a failed dry-run
— a wrong guess there costs nothing since the server call still catches it.
The Review step and completion screen both read the *resolved* `listen_addr`/
`tls` back from `nextsql setup`'s own JSON response rather than echoing the
form fields, so what's displayed always matches what was actually
configured. **Tests**: `Params.Validate`/`toArgs` cases for the TLS pairing
rule and the new flags. **Live-verified end to end** against real binaries
(job scratch dir, cleaned up after): `POST /api/v1/plan` with a non-loopback
address and no TLS files → the real `ErrInsecureRemote` rejection, nothing
written; the same address with a real self-signed P-256 cert/key → accepted;
a real (non-dry-run) `POST /api/v1/install` with `127.0.0.1:17443` + that
cert produced a working config and an initialized database; started the
real `nextsqld` against it with `--tls-cert`/`--tls-key` (log confirms
`"tls":true`); connected with the real `nextsql exec` client over that TLS
listener as the admin user the wizard created and got a real query result
back. Closes the "TLS certificate assistant and validation" and "Component
selection" `TODO.md` checklist lines (M3's three-item scope is exactly what
those two lines meant); "Optional explicit firewall-rule creation" is a
separate, still-open line — this is TLS configuration, not OS firewall
manipulation.

### M6 complete: service registration / start at boot (2026-09-04)

`PROJECT.md` §46 lists "service registration" as intended installer scope.
Two designs were considered: (a) have the wizard *author* a systemd unit
file itself, or (b) have it only offer to *enable* a unit some packaged
installer (`.deb`/`.tar.gz`/`.run`) already placed on disk. **Chosen: (b).**
Authoring unit files (root-vs-user paths, `sed`-templating `ExecStart`/
`ConditionPathExists` to match wherever the operator chose to put the
binaries) is exactly what `packaging/linux/{nextsql.service,
nextsql.user.service}` and `tarball/install.sh` already do, correctly, today
— duplicating that logic in the GUI installer risks the two definitions
drifting apart, and a from-source `nextsql-install` run (no packaging
involved at all) has nowhere authoritative to copy a unit's `ExecStart`
paths from. Offering only "enable what's already there and already
matches" needs no new authority and cannot itself misconfigure a service.

New `internal/installgui/service.go` — the one place besides `nextsql
setup` this package shells out from, documented as such in the package doc
comment (`config.go`). `DetectService` is read-only (`systemctl cat` /
`is-enabled` / `is-active` for a "nextsql" unit, scoped `--user` unless
running as root — the same split `detectDefaults` already uses) and never
errors on "not found" or "no systemd here", both expected, unremarkable
states. New `GET /api/v1/service` exposes it to the wizard. New
`Params.EnableService`; `maybeEnableService` (server.go) only ever calls the
one mutating function, `EnableService` (`systemctl enable --now nextsql`),
after independently re-verifying: the database this run just initialized is
health-verified OK, a "nextsql" unit exists in this operator's own scope,
and that unit's own `ExecStart --config` (parsed from `systemctl cat`)
already equals the config path `nextsql setup` just wrote — never a unit it
has to guess about. Any refusal is a clear, non-fatal reason; it never flips
the (already-succeeded) database install to a failure.

**A real bug found and fixed during live verification, not assumed away**:
`systemctl enable --now` exits `0` once a start has been *issued*, even for
a `Type=simple` unit whose process exits immediately after — confirmed live
by installing without a bootstrap admin user, which made a genuinely
started `nextsqld` immediately refuse to run and exit, while `enable --now`
itself still reported success. The initial implementation only checked that
exit code and would have told the operator "started" when it hadn't. Fixed
with a separate `Active` field: after `EnableService` returns successfully,
`WaitActive` polls `systemctl is-active` up to 4 times over ~1.5s (bounded —
never an unbounded wait, but enough grace for WAL recovery/catalog decode on
a real start) before concluding it did not stay running. The wizard's
completion screen and `TODO.md`/`CHANGELOG.md` distinguish "enabled to start
at boot" from "actually running now" accordingly. **Tests**: `service_test.go`
(`execStartConfigRe` parsing cases, `systemctlArgs` scope handling, a
real-host `DetectService`/`EnableService` pair that only ever probes
read-only or fails cleanly against a unit name that doesn't exist);
`server_test.go` (`GET /api/v1/service` shape, install with
`enableService:true` on a host with no matching unit → `OK:true` for the
database install regardless, a clear non-fatal `service.error`; install
without `enableService` → no `service` field in the response at all).
**Live-verified end to end** against a real *user-scope* systemd unit
registered for this exact purpose (`systemctl --user`, no root, fully
disabled/removed/`daemon-reload`d afterward — confirmed gone): `GET
/api/v1/service` correctly found it and parsed its `--config` path; a real
(non-dry-run) install without an admin user + `enableService:true` produced
`{"enabled":true,"active":false}` with the accurate "did not stay running"
reason (the real, live-reproduced bug above); a second real install *with*
an admin user produced `{"enabled":true,"active":true}`, confirmed
independently via `systemctl --user is-active`/`is-enabled`, and a real
`nextsql exec … SELECT 1` succeeded against the systemd-managed `nextsqld`.
`go build`/`go vet` clean; `internal/installgui` full suite green under
`-race`.

### Frontend rebuilt on React + @bzync/rui (2026-09-05)

M1 shipped a hand-written vanilla-JS wizard (a small `h()` hyperscript
helper, no framework) — a reasonable M1 shortcut, but one that was never
revisited, unlike Manager where the frontend stack was its own explicit
`AskUserQuestion` (log #121). Asked explicitly (`AskUserQuestion`) whether to
keep it or rebuild on the same React + `@bzync/rui` stack Manager already
uses: **rebuild**, both for visual/interaction consistency across
Installer/Manager/Studio and because rui's already-accessible components gave
M5's subsequent keyboard/screen-reader work a sound base rather than requiring
a second hand-rolled component layer.

New `internal/installgui/frontend/` — a standalone npm package (esbuild
bundle), identical shape to `internal/manager/frontend/`: same dependency
versions (`@bzync/rui` 0.0.9, `react`/`react-dom` 19.2.8), same
`build.mjs`/`tsconfig.json` pattern, output committed to
`internal/installgui/web/` so `go build ./...` still needs no Node
toolchain. `Server`/`assets.go`/`serveShell` needed **zero** Go changes — the
embed layout (`web/index.html` + `/assets/app.js` + `/assets/app.css`) was
already exactly what Manager's own build produces, so the frontend swap is
pure replacement of the `web/` output, same as any other rebuild-and-commit
cycle. Six step components (`Welcome`, `Location`, `Resources`,
`Administrator`, `Summary`, `Completion`) plus a `Stepper`-driven progress
bar, ported field-for-field from the vanilla version's `state` object and
API calls (`src/api.ts` mirrors Manager's `api.ts` request-wrapper pattern,
substituting the single-run `X-Installer-Token` header for Manager's CSRF
token).

**Two real, user-facing bugs found and fixed during the rewrite, not
introduced by it** — both surfaced by re-reading the ported logic against
its actual behavior rather than transcribing it unexamined:

1. **The "start at boot" checkbox's client-side config-path guess was
   wrong**, and had been since M6 (log #139): `viewServiceOption`'s
   `expectedConfig` computed `dataDir + "/nextsql.conf"` — `nextsql setup`'s
   own default when `--config-out` is omitted — instead of the path the
   wizard's *own* `nextsql setup --config-out` call actually resolves to.
   Every packaged installer (tarball/.deb/.run) keeps config out of the data
   directory (`/etc/nextsql` vs `/var/lib/nextsql`, or per-user
   `$XDG_CONFIG_HOME` vs `$XDG_DATA_HOME`), and any systemd unit it installs
   points `--config` at that separate path — so for *every* packaged
   install, this check silently found a "mismatch" and permanently disabled
   the checkbox (`box(disabled=true)` forces `enableService=false`, so it
   could never even be requested), even when the unit and the about-to-be-
   written config would in fact agree. Root-caused, not just patched at the
   symptom: `internal/installgui/defaults.go`'s `Defaults`/`detectDefaults`
   gained a `ConfigOut` field computed with the *same* /etc-or-per-user split
   as `DataDir`/`KeyFile` already use; `Params` gained a matching
   `ConfigOut` field wired to `nextsql setup --config-out`; the wizard
   prefills it from `/api/v1/hello`'s `defaults.configOut` exactly like
   `dataDir`/`keyFile`, transparently (no new form field — shown read-only
   on the Summary step for transparency, matching the key-file disclosure
   M2 already established). `viewServiceOption`'s (React: `ServiceOption`)
   `expectedConfig` now reads `params.configOut` instead of guessing. New
   `TestDetectDefaultsConfigOutSeparateFromDataDir` guards against a future
   regression back to the DataDir-based default, which would silently
   reintroduce the same bug. **Live-verified against a real registered
   `--user` systemd unit** (`~/.config/systemd/user/nextsql.service`,
   fully removed after): before the fix, `/api/v1/service` correctly found
   the unit but the checkbox stayed disabled; after, a real
   `POST /api/v1/install` with `enableService:true` produced
   `{"enabled":true,"active":true}`, confirmed independently via
   `systemctl --user is-active`/`is-enabled` and a real query against the
   systemd-managed `nextsqld`.
2. **The Administrator step's Continue button could disable itself with no
   visible reason.** The vanilla version's disabled expression
   (`(pw||confirm) && (mismatch || pw.length<8)`) only ever showed an error
   banner for a *mismatch*; a too-short password disabled Continue with
   nothing beyond a small muted hint next to the password field — easy to
   miss, and reported live by the user testing this exact screen mid-session
   ("why disclose it as optional? but the button to continue is disabled?").
   The rewrite (`Administrator.tsx`) replaces the boolean with one
   `disabledReason(params, confirm): string | null` that is always rendered
   as an explicit `Alert` when non-null, covering three cases in order: an
   asymmetric username/password (a rule `Params.Validate` already enforced
   server-side but the client never surfaced), a too-short password, and a
   mismatch. Leaving both fields blank is unconditionally allowed (`null`) —
   the "optional" disclosure in the copy above the fields is actually true
   now for every state a blank-blank operator can reach.

**Tests**: `npm run typecheck` clean; `internal/installgui`'s existing Go
suite (`TestDetectDefaultsNonEmpty` extended, new
`TestDetectDefaultsConfigOutSeparateFromDataDir`, new
`TestParamsToArgsConfigOut`) green under `-race`; no Go API surface changed
beyond the two new fields, so `server_test.go`/`runner_test.go` needed no
changes. **Live-verified end to end through a real Chrome browser**, not
just curl against the API: navigated the actual wizard URL
`nextsql-install` printed, clicked through Welcome → Location → Resources →
Administrator (reproducing bug 2 above, confirming the fix) → Summary →
Install → Completion → Finish, then independently queried the resulting
database — both directly (`nextsql exec`) and, in the systemd case above,
through the unit `enableService:true` started.

### M4 landed for Linux (2026-09-05)

`scripts/build-linux-installer.sh` now stages `nextsql-install` into every
Linux artifact alongside `nextsql`/`nextsqld`/`nextsql-bench` (one
`stage_bins` change covers `.tar.gz`, `.run`, and `.deb`/`.rpm`, since all
four share it). What each artifact does with it differs deliberately by
trust boundary, not uniformly:

- **`.tar.gz`/`.run` (`packaging/linux/tarball/install.sh`)**: these are run
  interactively by a human at a shell — the wizard is auto-launched as the
  default entry point when stdin is a TTY, no config already exists here,
  `--no-gui` wasn't passed, and (**deliberately scoped to `--user` mode
  only**) the install isn't `--system`/root. `install.sh` is not `exec`'d
  into the wizard — it regains control once the wizard process exits
  (Finish button, log #136, or Ctrl+C) so it can print a closing note
  either way. The placeholder config template `install.sh` used to write
  unconditionally is now skipped whenever the wizard is about to launch —
  writing it first would have made `nextsql setup --config-out` (called by
  the wizard) refuse with "config file already exists" the moment the
  operator picked a resource preset the template didn't already match
  exactly, found by tracing the actual `nextsql setup` clobber-check
  (`cmd/nextsql/setup.go`) before assuming the two writers were compatible.
- **`--system` (root) mode and the `.deb`'s `postinst`**: never
  auto-launched, only mentioned as an alternative in the printed
  instructions. **Deliberate, not an oversight**: the wizard subprocess
  would inherit install.sh's/postinst's root privileges and create the data
  directory and key file as root, while the "nextsql" system service account
  is what actually needs to read/write them afterward — a correct handoff
  needs either running the wizard as that unprivileged account or
  restricting which path it can write to, neither implemented here. Getting
  this wrong would be a real permission/availability bug (a service that
  can't start), not a convenience trade worth making without that follow-up
  design. A `.deb` `postinst` additionally must stay non-interactive
  regardless (it commonly runs under `apt`/cloud-init with no real TTY at
  all) — the same reasoning M6 already applied to "the installer never
  authors a unit file itself."
- `uninstall.sh` removes the `nextsql-install` binary alongside the other
  three on cleanup.

**Live-verified end to end** (job scratch dir, cleaned up after), the
non-interactive default path first (regression check: identical output to
before this change, `nextsql-install` shipped and hinted, nothing
auto-launched), then the interactive path under a real pty
(`python3 -c` `pty.openpty`, since a piped/backgrounded shell has no TTY of
its own): `install.sh --user` correctly auto-launched the wizard, printed
the URL/token, and — driven first via `curl` against the API directly, then
a second full pass through a real Chrome browser (see above) — completed a
real `nextsql setup` install whose `config_path` landed at exactly the
`$XDG_CONFIG_HOME/nextsql/nextsql.conf` path `install.sh` itself computed
for the systemd unit; `install.sh` regained control and printed its closing
note after Finish; the resulting `nextsqld`, started against that exact
config, served a real query. `.rpm`/Windows/macOS packaging remain out of
scope, same as log #134's platform-testing note (no `rpmbuild`/Wine/macOS
host in this environment).

**Update (2026-09-05, log #143)**: `.rpm` is no longer out of scope. Using a
disposable `fedora:40` Docker container (with `rpm-build`+`systemd-rpm-macros`
installed) rather than waiting for a host with `rpmbuild` preinstalled, the
`.rpm` was built and live-installed via `dnf install`, provisioned, started,
and queried successfully. Two real bugs in `packaging/linux/nextsql.spec.in`
were found and fixed along the way — relative `%doc`/`%license` paths (only
valid when a spec has a `%prep`/`%build` populating `%_builddir`, which this
one deliberately doesn't) and a `%files` list missing `nextsql-admin` (the
renamed binary)/`USAGE.md.gz`/`VERSION` — both invisible to `.deb` since
`dpkg` has no equivalent "installed but unpackaged files" check. Windows/macOS
packaging remain genuinely out of scope (no build host in this environment).

### M5 accessibility + shared Installer/Manager UI baseline (2026-09-05)

M5 closes the accessibility pass with behavior that can be repeated in CI,
not a one-time visual claim. `internal/webtest/cdp.mjs` drives a real local
Chrome/Chromium instance through the DevTools protocol, while each frontend
owns a bounded fixture server and `axe-core` audit (`npm run test:a11y`). The
Installer test uses keyboard activation through Welcome → Location →
Resources → Administrator → Review → Installing → Ready → Finished, exercises
the path combobox with Arrow/Escape, proves a non-empty password cannot
continue without matching confirmation, and runs the WCAG 2.2 A/AA-tagged axe
rules on every view. It also emulates `prefers-reduced-motion: reduce` and
`prefers-contrast: more`, asserts that those styles took effect, and audits
the stable result. The Manager test covers login, the authenticated shell,
and keyboard tab navigation under the same checks. This is a
screen-reader-oriented semantics/relationship audit; it does not claim a
manual NVDA/VoiceOver certification.

The concrete fixes are part of the product rather than test-only allowances:

- the visible RUI stepper is paired with semantic ordered progress and one
  `aria-current="step"`; page-like step and install-result transitions move
  focus to a stable heading and use an atomic live announcement;
- every custom path field is a labelled ARIA combobox with associated hint
  and error text, valid listbox children, active-descendant keyboard
  navigation, and deterministic Escape behavior;
- the resource radio group is named, advanced network disclosure exposes
  `aria-expanded`/`aria-controls`, and field errors use the input component's
  real `aria-invalid`/`aria-describedby` wiring;
- Administrator now requires matching confirmation whenever a password is
  present (blank confirmation previously left Continue enabled), and both
  Installer and Manager have skip links, explicit system/light/dark controls,
  reduced-motion CSS, stronger contrast modes, and AA-safe overrides for two
  fixed-color RUI 0.0.9 text styles found by axe;
- the browser fixtures apply the production CSP and assert that the bundled
  Inter face actually reaches `loaded`; this caught the Installer policy's
  missing `data:` allowance for inlined fonts, now aligned with Manager and
  protected by a Go response-header test.

Per the user's direction, Manager now adopts the Installer's presentation:
the same canonical wordmark, flat auth backdrop, self-hosted Inter/JetBrains
Mono fonts, elevated content card, 200px branded rail, spacing, theme control,
and responsive rail/navigation behavior. Manager retains a wider maximum
content canvas because operational result tables require it; product roles
and security boundaries remain separate.

### Actionable error messages (2026-09-07)

`nextsql setup` and the shared `Params` validator are the one authority on
what a plan (`/api/v1/plan`) or install (`/api/v1/install`) rejects, and the
wizard still surfaces their exact text — but no longer as the *only* thing a
first-run operator sees. `explainSetupError` (`src/setup/util.ts`, a pure
function, unit-tested via `npm run test:setup`) matches the raw message
against the known failure classes and returns a plain-language `title` +
`action`, plus the verbatim `detail` and a `detailOpen` hint. `SetupErrorAlert`
(`src/setup/components/`) renders it as the existing `Alert variant="error"`
with the headline as its title, the next step as its body, and the raw
message inside a `<details>` "Technical details" disclosure — collapsed for a
recognized error, expanded automatically when the text is unrecognized so
nothing is ever hidden. The three places a raw setup error reached the UI
(the Location and Resources steps' plan-check banner, the Install step's
failure card) all render through it.

Recognized classes: non-loopback listen address without TLS; an existing
config file or an already-initialized data directory; the production profile
missing an administrator, an unlock key, or a resource-policy setting
(`*_timeout_ms`, disk watermark); an unlock key left on the data volume; the
production profile with "write config only"; an unwritable target path
(`permission denied` / `operation not permitted`); a full disk; a failed
post-install health check; and a missing/unrunnable `nextsql` binary.
Anything else falls back to a generic "Setup couldn't finish" with the raw
message shown expanded. This is presentation only — it never changes an
outcome, retries, or suppresses an error, and it adds no API surface.

## 3. API surface (M1)

All under the single-operator token described above. Every request/response
body is JSON.

| Method | Path | Effect |
|---|---|---|
| `GET` | `/api/v1/hello` | Version/phase, OS-appropriate default paths, whether running elevated (root/Administrator) — no side effects, no subprocess |
| `GET` | `/api/v1/service` | Read-only `systemctl` probe for an existing "nextsql" unit (M6) — no `enable`/`start` |
| `POST` | `/api/v1/plan` | Runs `nextsql setup --dry-run --json` with the operator's current form values; returns hardware detection, resource-preset recommendation, resolved paths, and warnings. Writes nothing. |
| `POST` | `/api/v1/install` | Runs `nextsql setup --json` (no `--dry-run`) with the same values — the one mutating call, gated behind the Summary screen's explicit confirmation. When `enableService` was requested, also (best-effort, non-fatal) runs `systemctl enable --now` on a matching pre-existing unit (M6) and reports the outcome under `service`. |
| `POST` | `/api/v1/finish` | Closes `Server.Done()` so the process can exit on its own once the operator is done (log #136) |

`internal/installgui.Params` is the one shared shape between `/plan` and
`/install` (same fields, same validation, same argv-building) so the
Summary screen is guaranteed to describe exactly what Install will do.

## 4. Non-goals for M1

- No packaging integration yet (M4) — `nextsql-install` is built and tested
  standalone; wiring it into the OS installer artifacts is a separate,
  reversible follow-up once the flow is proven. **Landed for Linux
  (`.tar.gz`/`.run`/`.deb`), 2026-09-05** — see above; Windows/macOS
  packaging remain out of scope (no build host in this environment).
- No recovery-key export UI yet (M2) — `nextsql setup` does not generate a
  separate recovery key today (single root unlock key), so this waits on
  that capability existing.
- No TLS certificate assistant in M1 — the installer defaulted to the
  loopback, TLS-optional path `nextsql setup` already supports; a remote,
  TLS-required listen address was left to the CLI/config file. **Landed as
  M3, 2026-09-04** — see above.
