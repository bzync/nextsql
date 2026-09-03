import "server-only";

import { readFile } from "node:fs/promises";
import path from "node:path";
import { type Catalog, type Release, compareVersions } from "@/lib/release-model";

export * from "@/lib/release-model";

const DATA_DIR = path.join(process.cwd(), "data");
const CATALOG_PATH = path.join(DATA_DIR, "releases.json");
export const DOWNLOADS_DIR = path.join(process.cwd(), "public", "downloads");

async function emptyCatalog(): Promise<Catalog> {
  return { releases: [] };
}

async function loadCatalog(): Promise<Catalog> {
  try {
    const raw = await readFile(CATALOG_PATH, "utf8");
    const parsed = JSON.parse(raw) as Catalog;
    if (!parsed || !Array.isArray(parsed.releases)) return emptyCatalog();
    return parsed;
  } catch {
    return emptyCatalog();
  }
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
