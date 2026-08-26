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
- [ ] upgrade/downgrade notes updated.

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

Build supported packages/artifacts.

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

- verify artifact checksums;
- verify installation;
- verify first startup;
- run smoke query;
- verify backup/restore;
- verify docs link to the correct version.

---

## 10. Rollback

If release validation fails after publish:

- stop further rollout;
- document affected versions;
- publish remediation;
- restore from known-good backup where required;
- do not advise opening newer-format data with older binaries unless explicitly supported.
