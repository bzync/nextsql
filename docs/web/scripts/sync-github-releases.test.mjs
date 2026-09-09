import assert from "node:assert/strict";
import test from "node:test";
import { classifyArtifact, mergeCatalog, parseChecksums } from "./sync-github-releases.mjs";

const digest = "a".repeat(64);
const filename = "nextsql-1.2.3-linux-amd64.tar.gz";
const remoteRelease = {
  tag_name: "v1.2.3",
  name: "Remote title",
  body: "Remote summary.",
  draft: false,
  prerelease: false,
  published_at: "2026-09-09T01:02:03Z",
  assets: [
    {
      name: filename,
      size: 1234,
      state: "uploaded",
      digest: `sha256:${digest}`,
      browser_download_url: `https://github.com/bzync/nextsql/releases/download/v1.2.3/${filename}`,
    },
  ],
};

test("classifies versioned installer filenames", () => {
  assert.deepEqual(classifyArtifact(filename, "1.2.3"), {
    kind: "archive",
    platform: "linux-amd64",
  });
  assert.deepEqual(classifyArtifact("nextsql_1.2.3_arm64.deb", "1.2.3"), {
    kind: "deb",
    platform: "linux-arm64",
  });
  assert.deepEqual(classifyArtifact("nextsql-1.2.3_alpha_beta-1.x86_64.rpm", "1.2.3-alpha-beta"), {
    kind: "rpm",
    platform: "linux-amd64",
  });
  assert.equal(classifyArtifact("unrelated.txt", "1.2.3"), undefined);
});

test("parses and rejects ambiguous checksum manifests", () => {
  assert.equal(parseChecksums(`${digest}  ${filename}\n`).get(filename), digest);
  assert.throws(() => parseChecksums(`not-a-checksum  ${filename}\n`), /invalid SHA256SUMS line/);
  assert.throws(
    () => parseChecksums(`${digest}  ${filename}\n${digest}  ${filename}\n`),
    /duplicate SHA256SUMS entry/,
  );
});

test("uses GitHub for release facts and the local catalog for editorial copy", () => {
  const curated = {
    releases: [
      {
        version: "1.2.3",
        title: "Curated title",
        summary: "Curated summary.",
        highlights: ["One"],
        changes: [{ id: "c1", kind: "added", area: "Engine", text: "Added." }],
      },
    ],
  };
  const merged = mergeCatalog(
    curated,
    [remoteRelease],
    new Map([["v1.2.3", `${digest}  ${filename}\n`]]),
  );
  assert.equal(merged.releases[0].title, "Curated title");
  assert.equal(merged.releases[0].releasedAt, remoteRelease.published_at);
  assert.equal(merged.releases[0].channel, "stable");
  assert.equal(merged.releases[0].latest, true);
  assert.deepEqual(merged.releases[0].artifacts[0], {
    id: filename,
    kind: "archive",
    platform: "linux-amd64",
    filename,
    size: 1234,
    sha256: digest,
    url: remoteRelease.assets[0].browser_download_url,
  });
});

test("fails closed when checksums do not match GitHub's digest", () => {
  assert.throws(
    () => mergeCatalog({ releases: [] }, [remoteRelease], new Map([["v1.2.3", `${"b".repeat(64)}  ${filename}\n`]])),
    /does not match GitHub's digest/,
  );
});

test("fails closed when an installer does not match a supported filename", () => {
  const unknown = {
    ...remoteRelease,
    assets: [
      {
        ...remoteRelease.assets[0],
        name: "nextsql-1.2.3-plan9-amd64.tar.gz",
      },
    ],
  };
  assert.throws(
    () => mergeCatalog({ releases: [] }, [unknown], new Map([["v1.2.3", `${digest}  ignored\n`]])),
    /unrecognized installer filename/,
  );
});

test("fails closed when GitHub's URL does not name the same asset", () => {
  const redirected = {
    ...remoteRelease,
    assets: [
      {
        ...remoteRelease.assets[0],
        browser_download_url: "https://github.com/bzync/nextsql/releases/download/v1.2.3/different.tar.gz",
      },
    ],
  };
  assert.throws(
    () => mergeCatalog({ releases: [] }, [redirected], new Map([["v1.2.3", `${digest}  ${filename}\n`]])),
    /outside the expected GitHub release/,
  );
});
