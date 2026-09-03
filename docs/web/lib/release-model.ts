import { assetPath } from "@/lib/asset-path";

export const PLATFORMS = [
  "linux-amd64",
  "linux-arm64",
  "darwin-amd64",
  "darwin-arm64",
  "windows-amd64",
] as const;

export const ARTIFACT_KINDS = [
  "nextsql",
  "nextsqld",
  "nextsql-bench",
  "deb",
  "run",
  "setup",
  "archive",
] as const;

export const CHANGE_KINDS = ["added", "changed", "fixed", "removed", "breaking"] as const;

export const CHANGE_AREAS = [
  "Engine",
  "SQL",
  "JSON",
  "Search",
  "Vectors",
  "Geo",
  "Security",
  "HA",
  "CLI",
  "Drivers",
  "Ops",
] as const;

export type Platform = (typeof PLATFORMS)[number];
export type ArtifactKind = (typeof ARTIFACT_KINDS)[number];
export type ChangeKind = (typeof CHANGE_KINDS)[number];
export type ChangeArea = (typeof CHANGE_AREAS)[number];
export type ReleaseStatus = "draft" | "published";
export type ReleaseChannel = "stable" | "preview";

export type Artifact = {
  id: string;
  kind: ArtifactKind;
  platform: Platform;
  filename: string;
  size: number;
  sha256: string;
};

export type Change = {
  id: string;
  kind: ChangeKind;
  area: ChangeArea;
  text: string;
};

export type Release = {
  version: string;
  title: string;
  status: ReleaseStatus;
  channel: ReleaseChannel;
  latest: boolean;
  releasedAt: string;
  summary: string;
  highlights: string[];
  changes: Change[];
  artifacts: Artifact[];
};

export type Catalog = {
  releases: Release[];
};

const VERSION_RE = /^\d+\.\d+\.\d+(?:-[A-Za-z0-9.-]+)?$/;

export function isVersion(value: string): boolean {
  return VERSION_RE.test(value);
}

export function compareVersions(a: string, b: string): number {
  const pa = parseVersion(a);
  const pb = parseVersion(b);
  for (let i = 0; i < 3; i += 1) {
    if (pa.nums[i] !== pb.nums[i]) return pa.nums[i] - pb.nums[i];
  }
  if (pa.pre && !pb.pre) return -1;
  if (!pa.pre && pb.pre) return 1;
  return pa.pre.localeCompare(pb.pre);
}

function parseVersion(version: string): { nums: number[]; pre: string } {
  const [core, pre = ""] = version.split("-", 2);
  const nums = core.split(".").map((part) => Number.parseInt(part, 10) || 0);
  while (nums.length < 3) nums.push(0);
  return { nums, pre };
}

export function artifactUrl(version: string, filename: string): string {
  return assetPath(`/downloads/${encodeURIComponent(version)}/${encodeURIComponent(filename)}`);
}

export function formatBytes(size: number): string {
  if (size < 1024) return `${size} B`;
  if (size < 1024 * 1024) return `${(size / 1024).toFixed(1)} KiB`;
  return `${(size / (1024 * 1024)).toFixed(1)} MiB`;
}

export function formatDate(iso: string): string {
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return iso;
  return new Intl.DateTimeFormat("en-GB", {
    day: "numeric",
    month: "short",
    year: "numeric",
  }).format(date);
}

export function changesBetween(releases: Release[], fromVersion: string, toVersion: string): Change[] {
  const ordered = [...releases].sort((a, b) => compareVersions(a.version, b.version));
  const fromIndex = ordered.findIndex((release) => release.version === fromVersion);
  const toIndex = ordered.findIndex((release) => release.version === toVersion);
  if (fromIndex < 0 || toIndex < 0) return [];
  const [start, end] = fromIndex <= toIndex ? [fromIndex + 1, toIndex] : [toIndex + 1, fromIndex];
  return ordered.slice(start, end + 1).flatMap((release) => release.changes);
}

export function groupChanges(changes: Change[]): Record<ChangeKind, Change[]> {
  const groups = Object.fromEntries(CHANGE_KINDS.map((kind) => [kind, [] as Change[]])) as Record<ChangeKind, Change[]>;
  for (const change of changes) groups[change.kind].push(change);
  return groups;
}

export function platformLabel(platform: Platform): string {
  const labels: Record<Platform, string> = {
    "linux-amd64": "Linux x64",
    "linux-arm64": "Linux ARM64",
    "darwin-amd64": "macOS Intel",
    "darwin-arm64": "macOS Apple Silicon",
    "windows-amd64": "Windows x64",
  };
  return labels[platform];
}

export function kindLabel(kind: ArtifactKind): string {
  const labels: Record<ArtifactKind, string> = {
    nextsql: "CLI (nextsql)",
    nextsqld: "Server (nextsqld)",
    "nextsql-bench": "Bench (nextsql-bench)",
    deb: "Debian package (.deb)",
    run: "Linux installer (.run)",
    setup: "Windows setup (.exe)",
    archive: "Archive (.tar.gz / .zip)",
  };
  return labels[kind];
}

export function changeKindLabel(kind: ChangeKind): string {
  const labels: Record<ChangeKind, string> = {
    added: "Added",
    changed: "Changed",
    fixed: "Fixed",
    removed: "Removed",
    breaking: "Breaking",
  };
  return labels[kind];
}
