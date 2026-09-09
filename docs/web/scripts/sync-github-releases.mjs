#!/usr/bin/env node

import { mkdir, readFile, writeFile } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

const SCRIPT_DIR = path.dirname(fileURLToPath(import.meta.url));
const SITE_DIR = path.resolve(SCRIPT_DIR, "..");
const DEFAULT_CATALOG = path.join(SITE_DIR, "data", "releases.json");
const DEFAULT_OUTPUT = path.join(SITE_DIR, "data", "releases.github.json");
const DEFAULT_REPOSITORY = "bzync/nextsql";
const API_VERSION = "2022-11-28";
const MAX_RELEASE_PAGES = 10;
const MAX_API_RESPONSE_BYTES = 8 * 1024 * 1024;
const MAX_CHECKSUM_BYTES = 1024 * 1024;

function escapeRegExp(value) {
  return value.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

export function classifyArtifact(filename, version) {
  const v = escapeRegExp(version);
  const rpmVersion = escapeRegExp(version.replaceAll("-", "_"));
  const patterns = [
    [new RegExp(`^nextsql-${v}-linux-(amd64|arm64)\\.run$`), "run", (arch) => `linux-${arch}`],
    [new RegExp(`^nextsql-${v}-linux-(amd64|arm64)\\.tar\\.gz$`), "archive", (arch) => `linux-${arch}`],
    [new RegExp(`^nextsql_${v}_(amd64|arm64)\\.deb$`), "deb", (arch) => `linux-${arch}`],
    [new RegExp(`^nextsql-${rpmVersion}-1\\.(x86_64|aarch64)\\.rpm$`), "rpm", (arch) =>
      arch === "x86_64" ? "linux-amd64" : "linux-arm64"],
    [new RegExp(`^nextsql-${v}-windows-(amd64)-setup\\.exe$`), "setup", (arch) => `windows-${arch}`],
    [new RegExp(`^nextsql-${v}-windows-(amd64)\\.zip$`), "archive", (arch) => `windows-${arch}`],
    [new RegExp(`^nextsql-${v}-darwin-(amd64|arm64)\\.tar\\.gz$`), "archive", (arch) => `darwin-${arch}`],
  ];

  for (const [pattern, kind, platformFor] of patterns) {
    const match = pattern.exec(filename);
    if (match) return { kind, platform: platformFor(match[1]) };
  }
  return undefined;
}

function looksLikeInstaller(filename) {
  return /^nextsql(?:[-_]).+\.(?:deb|rpm|run|zip|exe|tar\.gz)$/.test(filename);
}

export function parseChecksums(source) {
  const sums = new Map();
  for (const rawLine of source.split(/\r?\n/)) {
    const line = rawLine.trim();
    if (!line) continue;
    const match = /^([a-fA-F0-9]{64})\s+\*?(.+)$/.exec(line);
    if (!match) throw new Error(`invalid SHA256SUMS line: ${rawLine}`);
    if (sums.has(match[2])) throw new Error(`duplicate SHA256SUMS entry: ${match[2]}`);
    sums.set(match[2], match[1].toLowerCase());
  }
  return sums;
}

function compareVersions(a, b) {
  const parse = (version) => {
    const [core, prerelease = ""] = version.split("-", 2);
    return { numbers: core.split(".").map((part) => Number.parseInt(part, 10) || 0), prerelease };
  };
  const pa = parse(a);
  const pb = parse(b);
  for (let i = 0; i < 3; i += 1) {
    if (pa.numbers[i] !== pb.numbers[i]) return pa.numbers[i] - pb.numbers[i];
  }
  if (pa.prerelease && !pb.prerelease) return -1;
  if (!pa.prerelease && pb.prerelease) return 1;
  return pa.prerelease.localeCompare(pb.prerelease);
}

function versionFromTag(tag) {
  const version = tag.startsWith("v") ? tag.slice(1) : "";
  if (!/^\d+\.\d+\.\d+(?:-[A-Za-z0-9]+(?:[.-][A-Za-z0-9]+)*)?$/.test(version)) {
    throw new Error(`release tag is not v<semver>: ${tag}`);
  }
  return version;
}

function validateDownloadURL(repository, tag, asset) {
  let url;
  try {
    url = new URL(asset.browser_download_url);
  } catch {
    throw new Error(`${tag}/${asset.name}: invalid browser_download_url`);
  }
  const prefix = `/${repository}/releases/download/${tag}/`;
  let downloadedName = "";
  try {
    downloadedName = decodeURIComponent(url.pathname.slice(prefix.length));
  } catch {
    throw new Error(`${tag}/${asset.name}: download URL has invalid encoding`);
  }
  if (
    url.protocol !== "https:" ||
    url.hostname !== "github.com" ||
    !url.pathname.startsWith(prefix) ||
    downloadedName !== asset.name ||
    url.search ||
    url.hash
  ) {
    throw new Error(`${tag}/${asset.name}: download URL is outside the expected GitHub release`);
  }
  return url.toString();
}

export function mergeCatalog(curated, githubReleases, checksumSources, repository = DEFAULT_REPOSITORY) {
  if (!curated || !Array.isArray(curated.releases)) throw new Error("curated catalog has no releases array");
  const curatedByVersion = new Map(curated.releases.map((release) => [release.version, release]));
  const releases = [];

  for (const remote of githubReleases) {
    if (remote.draft) continue;
    const version = versionFromTag(remote.tag_name);
    if (!remote.published_at) throw new Error(`${remote.tag_name}: published release has no published_at`);
    if (!Array.isArray(remote.assets)) throw new Error(`${remote.tag_name}: assets is not an array`);
    const checksumsText = checksumSources.get(remote.tag_name);
    if (typeof checksumsText !== "string") throw new Error(`${remote.tag_name}: SHA256SUMS is missing`);
    const checksums = parseChecksums(checksumsText);
    const artifacts = [];

    for (const asset of remote.assets) {
      const classification = classifyArtifact(asset.name, version);
      if (!classification) {
        if (looksLikeInstaller(asset.name)) {
          throw new Error(`${remote.tag_name}/${asset.name}: unrecognized installer filename`);
        }
        continue;
      }
      if (asset.state !== "uploaded") throw new Error(`${remote.tag_name}/${asset.name}: asset is not uploaded`);
      if (!Number.isSafeInteger(asset.size) || asset.size <= 0) {
        throw new Error(`${remote.tag_name}/${asset.name}: invalid asset size`);
      }
      const digest = /^sha256:([a-fA-F0-9]{64})$/.exec(asset.digest ?? "");
      if (!digest) throw new Error(`${remote.tag_name}/${asset.name}: GitHub SHA-256 digest is missing`);
      const checksum = checksums.get(asset.name);
      if (!checksum) throw new Error(`${remote.tag_name}/${asset.name}: missing from SHA256SUMS`);
      if (checksum !== digest[1].toLowerCase()) {
        throw new Error(`${remote.tag_name}/${asset.name}: SHA256SUMS does not match GitHub's digest`);
      }
      artifacts.push({
        id: asset.name,
        ...classification,
        filename: asset.name,
        size: asset.size,
        sha256: checksum,
        url: validateDownloadURL(repository, remote.tag_name, asset),
      });
    }

    if (artifacts.length === 0) throw new Error(`${remote.tag_name}: no recognized installer assets`);
    const local = curatedByVersion.get(version);
    const remoteSummary = String(remote.body ?? "").split(/\n\s*\n/, 1)[0].trim();
    releases.push({
      version,
      title: local?.title || remote.name || `NextSQL ${version}`,
      status: "published",
      channel: remote.prerelease ? "preview" : "stable",
      latest: false,
      releasedAt: remote.published_at,
      summary: local?.summary || remoteSummary || `NextSQL ${version} release.`,
      highlights: local?.highlights ?? [],
      changes: local?.changes ?? [],
      artifacts,
    });
  }

  releases.sort((a, b) => compareVersions(b.version, a.version));
  if (releases[0]) releases[0].latest = true;
  return { releases };
}

async function readBounded(response, limit, label) {
  if (!response.ok) throw new Error(`${label}: GitHub returned HTTP ${response.status}`);
  const declared = Number(response.headers.get("content-length") ?? 0);
  if (declared > limit) throw new Error(`${label}: response exceeds ${limit} bytes`);
  if (!response.body) return "";
  const reader = response.body.getReader();
  const chunks = [];
  let size = 0;
  while (true) {
    const { done, value } = await reader.read();
    if (done) break;
    size += value.byteLength;
    if (size > limit) {
      await reader.cancel();
      throw new Error(`${label}: response exceeds ${limit} bytes`);
    }
    chunks.push(value);
  }
  const merged = new Uint8Array(size);
  let offset = 0;
  for (const chunk of chunks) {
    merged.set(chunk, offset);
    offset += chunk.byteLength;
  }
  return new TextDecoder().decode(merged);
}

function githubHeaders(token, accept = "application/vnd.github+json") {
  return {
    Accept: accept,
    "X-GitHub-Api-Version": API_VERSION,
    "User-Agent": "nextsql-docs-release-sync",
    ...(token ? { Authorization: `Bearer ${token}` } : {}),
  };
}

async function fetchGitHubReleases(repository, token) {
  if (!/^[A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+$/.test(repository)) {
    throw new Error(`invalid GitHub repository: ${repository}`);
  }
  const releases = [];
  for (let page = 1; page <= MAX_RELEASE_PAGES; page += 1) {
    const response = await fetch(`https://api.github.com/repos/${repository}/releases?per_page=100&page=${page}`, {
      headers: githubHeaders(token),
    });
    const source = await readBounded(response, MAX_API_RESPONSE_BYTES, `release page ${page}`);
    const batch = JSON.parse(source);
    if (!Array.isArray(batch)) throw new Error(`release page ${page}: response is not an array`);
    releases.push(...batch);
    if (batch.length < 100) return releases;
  }
  throw new Error(`GitHub release list exceeded ${MAX_RELEASE_PAGES * 100} entries`);
}

async function fetchChecksumSources(releases, token) {
  const sources = new Map();
  for (const release of releases) {
    if (release.draft) continue;
    const asset = release.assets?.find((candidate) => candidate.name === "SHA256SUMS");
    if (!asset?.url) continue;
    const response = await fetch(asset.url, {
      headers: githubHeaders(token, "application/octet-stream"),
      redirect: "follow",
    });
    sources.set(
      release.tag_name,
      await readBounded(response, MAX_CHECKSUM_BYTES, `${release.tag_name}/SHA256SUMS`),
    );
  }
  return sources;
}

function parseArgs(argv) {
  const options = {
    catalog: DEFAULT_CATALOG,
    output: DEFAULT_OUTPUT,
    repository: process.env.NEXTSQL_GITHUB_REPOSITORY || process.env.GITHUB_REPOSITORY || DEFAULT_REPOSITORY,
  };
  for (let i = 0; i < argv.length; i += 1) {
    const value = argv[i];
    if (value === "--catalog") options.catalog = path.resolve(argv[++i] ?? "");
    else if (value === "--output") options.output = path.resolve(argv[++i] ?? "");
    else if (value === "--repository") options.repository = argv[++i] ?? "";
    else throw new Error(`unknown argument: ${value}`);
  }
  return options;
}

export async function syncGitHubReleases(options) {
  const token = process.env.GH_TOKEN || process.env.GITHUB_TOKEN || "";
  const curated = JSON.parse(await readFile(options.catalog, "utf8"));
  const githubReleases = await fetchGitHubReleases(options.repository, token);
  const checksumSources = await fetchChecksumSources(githubReleases, token);
  const catalog = mergeCatalog(curated, githubReleases, checksumSources, options.repository);
  await mkdir(path.dirname(options.output), { recursive: true });
  await writeFile(options.output, `${JSON.stringify(catalog, null, 2)}\n`, "utf8");
  return catalog;
}

if (process.argv[1] && pathToFileURL(path.resolve(process.argv[1])).href === import.meta.url) {
  try {
    const options = parseArgs(process.argv.slice(2));
    const catalog = await syncGitHubReleases(options);
    const artifactCount = catalog.releases.reduce((total, release) => total + release.artifacts.length, 0);
    console.log(`synced ${catalog.releases.length} release(s), ${artifactCount} installer artifact(s)`);
  } catch (error) {
    console.error(error instanceof Error ? error.message : String(error));
    process.exitCode = 1;
  }
}
