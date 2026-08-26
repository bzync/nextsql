import "server-only";

import { createHash } from "node:crypto";
import { mkdir, readFile, rm, writeFile } from "node:fs/promises";
import path from "node:path";
import {
  type Artifact,
  type ArtifactKind,
  type Catalog,
  type Platform,
  type Release,
  compareVersions,
  isVersion,
} from "@/lib/release-model";

export * from "@/lib/release-model";

const DATA_DIR = path.join(process.cwd(), "data");
const CATALOG_PATH = path.join(DATA_DIR, "releases.json");
export const DOWNLOADS_DIR = path.join(process.cwd(), "public", "downloads");

let writeChain: Promise<unknown> = Promise.resolve();

function withLock<T>(fn: () => Promise<T>): Promise<T> {
  const run = writeChain.then(fn, fn);
  writeChain = run.then(
    () => undefined,
    () => undefined,
  );
  return run;
}

async function emptyCatalog(): Promise<Catalog> {
  return { releases: [] };
}

export async function loadCatalog(): Promise<Catalog> {
  try {
    const raw = await readFile(CATALOG_PATH, "utf8");
    const parsed = JSON.parse(raw) as Catalog;
    if (!parsed || !Array.isArray(parsed.releases)) return emptyCatalog();
    return parsed;
  } catch {
    return emptyCatalog();
  }
}

export async function saveCatalog(catalog: Catalog): Promise<void> {
  await mkdir(DATA_DIR, { recursive: true });
  await writeFile(CATALOG_PATH, `${JSON.stringify(catalog, null, 2)}\n`, "utf8");
}

export async function listReleases(opts?: { includeDrafts?: boolean }): Promise<Release[]> {
  const catalog = await loadCatalog();
  const releases = opts?.includeDrafts
    ? catalog.releases
    : catalog.releases.filter((release) => release.status === "published");
  return [...releases].sort((a, b) => compareVersions(b.version, a.version));
}

export async function getRelease(version: string, opts?: { includeDrafts?: boolean }): Promise<Release | undefined> {
  const releases = await listReleases(opts);
  return releases.find((release) => release.version === version);
}

export async function latestRelease(): Promise<Release | undefined> {
  const releases = await listReleases();
  return releases.find((release) => release.latest) ?? releases[0];
}

export async function upsertRelease(input: Omit<Release, "artifacts"> & { artifacts?: Artifact[] }): Promise<Release> {
  if (!isVersion(input.version)) {
    throw new Error("Version must look like 1.2.3 or 1.2.3-beta.1");
  }
  return withLock(async () => {
    const catalog = await loadCatalog();
    const existing = catalog.releases.find((release) => release.version === input.version);
    const release: Release = {
      ...input,
      highlights: input.highlights.map((item) => item.trim()).filter(Boolean),
      changes: input.changes.filter((change) => change.text.trim()),
      artifacts: existing?.artifacts ?? input.artifacts ?? [],
      latest: input.latest,
    };
    if (release.latest) {
      catalog.releases = catalog.releases.map((item) => ({ ...item, latest: item.version === release.version }));
    }
    const index = catalog.releases.findIndex((item) => item.version === release.version);
    if (index >= 0) {
      catalog.releases[index] = { ...catalog.releases[index], ...release, artifacts: catalog.releases[index].artifacts };
    } else {
      catalog.releases.push(release);
    }
    await saveCatalog(catalog);
    return catalog.releases.find((item) => item.version === release.version)!;
  });
}

export async function deleteRelease(version: string): Promise<void> {
  await withLock(async () => {
    const catalog = await loadCatalog();
    catalog.releases = catalog.releases.filter((release) => release.version !== version);
    await saveCatalog(catalog);
    await rm(path.join(DOWNLOADS_DIR, version), { recursive: true, force: true });
  });
}

export async function setLatest(version: string): Promise<void> {
  await withLock(async () => {
    const catalog = await loadCatalog();
    if (!catalog.releases.some((release) => release.version === version)) {
      throw new Error("Release not found");
    }
    catalog.releases = catalog.releases.map((release) => ({
      ...release,
      latest: release.version === version,
    }));
    await saveCatalog(catalog);
  });
}

export async function addArtifact(version: string, file: File, kind: ArtifactKind, platform: Platform): Promise<Artifact> {
  if (!isVersion(version)) throw new Error("Invalid version");
  const filename = sanitizeFilename(file.name);
  if (!filename) throw new Error("Invalid filename");
  if (file.size > 400 * 1024 * 1024) throw new Error("File exceeds 400 MiB");

  const bytes = Buffer.from(await file.arrayBuffer());
  const sha256 = createHash("sha256").update(bytes).digest("hex");
  const destDir = path.join(DOWNLOADS_DIR, version);
  await mkdir(destDir, { recursive: true });
  await writeFile(path.join(destDir, filename), bytes);

  const artifact: Artifact = {
    id: `${kind}-${platform}-${sha256.slice(0, 8)}`,
    kind,
    platform,
    filename,
    size: bytes.length,
    sha256,
  };

  await withLock(async () => {
    const catalog = await loadCatalog();
    const release = catalog.releases.find((item) => item.version === version);
    if (!release) throw new Error("Save the release before uploading binaries");
    release.artifacts = [
      ...release.artifacts.filter((item) => !(item.kind === kind && item.platform === platform)),
      artifact,
    ];
    await saveCatalog(catalog);
  });

  return artifact;
}

export async function removeArtifact(version: string, artifactId: string): Promise<void> {
  await withLock(async () => {
    const catalog = await loadCatalog();
    const release = catalog.releases.find((item) => item.version === version);
    if (!release) throw new Error("Release not found");
    const artifact = release.artifacts.find((item) => item.id === artifactId);
    release.artifacts = release.artifacts.filter((item) => item.id !== artifactId);
    await saveCatalog(catalog);
    if (artifact) {
      await rm(path.join(DOWNLOADS_DIR, version, artifact.filename), { force: true });
    }
  });
}

function sanitizeFilename(name: string): string {
  const base = path.basename(name).replace(/[^A-Za-z0-9._+-]/g, "-");
  return base.slice(0, 180);
}
