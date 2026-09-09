# NextSQL Release Process

## 1. Release Principle

A version is released only when its applicable correctness, durability, security, integrity, and availability gates are green.

Do not release based only on compilation or happy-path tests.

---

## 2. Pre-Release Checklist

- [ ] target `TODO.md` phase gates are green;
- [ ] no critical unresolved correctness issue;
- [ ] no known critical unresolved security issue;
- [ ] `go test ./...` passes;
- [ ] race tests pass where required;
- [ ] crash/recovery tests pass;
- [ ] HA tests pass where applicable;
- [ ] backup/restore/PITR tests pass;
- [ ] tenant/RBAC tests pass;
- [ ] protocol/driver tests pass;
- [ ] fuzzers for new decoders/parsers are green;
- [ ] benchmark gates are green;
- [ ] `README.md` updated;
- [ ] `USAGE.md` updated;
- [ ] `CHANGELOG.md` updated;
- [ ] compatibility notes updated;
- [ ] upgrade/downgrade notes updated;
- [ ] `make test-upgrade` passes against every retained release fixture.
- [ ] the `Production evidence gate` GitHub Actions run for the release commit
      is green and its retained log records a non-RAM `test scratch:` filesystem.

### CI evidence retention

`.github/workflows/production-gate.yml` runs `make test-pr` on pull requests,
the full `make test-production` profile on `master` and version tags, and the
fuzz-inclusive `make test-nightly` profile daily. It sets
`NEXTSQL_REQUIRE_DURABLE_FS=1` and retains the complete profile log for 30 days.
Release approval must inspect that artifact's `test scratch:` line; a missing
artifact or a RAM-backed filesystem is not durability evidence.

---

## 3. Versioning

Use explicit version identifiers.

Development builds may use:

```text
0.1.0-dev
```

Release candidates may use a documented pre-release scheme.

Never reuse an already published version for different bits.

---

## 4. Compatibility Review

Before release, review:

- storage format;
- WAL format;
- catalog format;
- backup format;
- NSQL protocol;
- drivers;
- SQL semantics;
- CLI/config behavior.

See `COMPATIBILITY.md`.

### Retained release fixtures

`make test-upgrade` replays real data directories written by previously shipped
binaries (`tests/upgrade/testdata/`). It is the only evidence that a format,
catalog, or recovery change has not stranded data an operator already has, so
it is a release gate, not an optional check.

**After tagging a release, cut its fixture pair and commit it**, on the release
commit, using that release's own binaries:

```bash
git worktree add /tmp/rel <tag>
(cd /tmp/rel && go build -o /tmp/relbin/nextsql ./cmd/nextsql \
                && go build -o /tmp/relbin/nextsqld ./cmd/nextsqld)
scripts/make-upgrade-fixture.sh --label <tag> --bindir /tmp/relbin
git add tests/upgrade/testdata/<tag>-clean.tar.gz tests/upgrade/testdata/<tag>-dirty.tar.gz
```

Never edit or regenerate an already-committed archive with a newer binary: an
archive rewritten by current code proves nothing. To also exercise the rollback
direction with the real binaries:

```bash
NEXTSQL_UPGRADE_OLD_BINDIR=/tmp/relbin go test ./tests/upgrade
```

---

## 5. Benchmark Gate

Run labeled SLO/benchmark suites in production-like mode.

Keep enabled:

- encryption;
- WAL;
- fsync;
- auth;
- MVCC.

Record hardware/context.

ANN results must include recall.

---

## 6. Security Gate

Confirm:

- no secrets committed;
- no keys in URLs;
- auth/RBAC/tenant tests green;
- malformed protocol tests green;
- relevant vulnerability fixes included;
- security documentation reflects reality.

---

## 7. Build Artifacts

Build supported packages/artifacts locally when validating a release candidate:

Examples:

```bash
./scripts/build-installers.sh
```

Expected artifact types may include:

- Linux `.deb`;
- Linux `.tar.gz`;
- Linux `.run`;
- Windows `.zip`;
- Windows installer.

Only mark platforms supported if their release validation passes.

`installers/` is gitignored disposable output. Never commit release binaries.
Once every pre-release gate is green, create and push a `vX.Y.Z` tag whose
version exactly matches `internal/version.String`. The
`Publish release installers` workflow then:

1. checks out and validates that exact tag;
2. refuses to overwrite an existing GitHub Release;
3. builds and verifies the Linux amd64 and Windows amd64 artifacts in
   runner-temporary storage;
4. publishes those artifacts and `SHA256SUMS` to GitHub Releases; and
5. invokes the Pages workflow to rebuild the Downloads catalog from the
   published GitHub metadata.

Use the manual workflow dispatch only to package an existing tag that does not
already have a release. Re-cutting released bits requires a new version and tag.

---

## 8. Release Notes

Release notes should include:

- new features;
- fixes;
- security changes;
- performance changes;
- breaking changes;
- migration requirements;
- compatibility notes;
- known issues.

Do not duplicate the entire changelog.

---

## 9. Post-Release

After publishing:

- verify the GitHub Release contains only the expected artifacts and
  `SHA256SUMS`;
- verify artifact checksums against both the manifest and GitHub's published
  SHA-256 digests;
- verify installation;
- verify first startup;
- run smoke query;
- verify backup/restore;
- verify the deployed Downloads page links to the correct immutable release
  assets and displays their GitHub-reported sizes and digests.

---

## 10. Rollback

If release validation fails after publish:

- stop further rollout;
- document affected versions;
- publish remediation;
- restore from known-good backup where required;
- do not advise opening newer-format data with older binaries unless explicitly supported.
