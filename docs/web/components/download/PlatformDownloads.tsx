"use client";

import Link from "next/link";
import { useMemo, useState, useSyncExternalStore } from "react";
import {
  type Artifact,
  type Platform,
  type Release,
  PLATFORMS,
  artifactUrl,
  formatBytes,
  kindLabel,
  platformLabel,
} from "@/lib/release-model";
import { Alert, Button, ToggleGroup, ToggleGroupItem } from "@bzync/rui";

const subscribeBrowser = () => () => {};

export function PlatformDownloads({ release }: { release: Release }) {
  const fallback = release.artifacts[0]?.platform ?? "linux-amd64";
  const detected = useSyncExternalStore(
    subscribeBrowser,
    () => {
      const candidate = detectPlatform();
      return release.artifacts.some((artifact) => artifact.platform === candidate) ? candidate : fallback;
    },
    () => fallback,
  );
  const onWindows = useSyncExternalStore(subscribeBrowser, detectWindows, () => false);
  const [selectedPlatform, setSelectedPlatform] = useState<Platform | null>(null);
  const platform = selectedPlatform ?? detected;

  const matching = useMemo(
    () => release.artifacts.filter((artifact) => artifact.platform === platform),
    [release.artifacts, platform],
  );

  if (release.artifacts.length === 0) {
    return (
      <div className="rounded-md border border-line px-4 py-4 text-sm text-muted">
        No prebuilt binaries for this version yet. Install from source with{" "}
        <code className="rounded bg-bg-hover px-1 font-mono text-[12px]">go install github.com/bzync/nextsql/cmd/nextsql@v{release.version}</code>
        .
      </div>
    );
  }

  return (
    <div className="space-y-4">
      {onWindows && (
        <Alert variant="info" title="Windows">
          NextSQL does not run natively on Windows. Install a WSL 2 distribution and use the Linux x64 packages inside it —
          see{" "}
          <Link href="/docs/install#windows-wsl-2" className="underline">
            Windows (WSL 2)
          </Link>
          .
        </Alert>
      )}
      <ToggleGroup
        type="single"
        size="sm"
        variant="outline"
        value={platform}
        aria-label="Package platform"
        onValueChange={(next) => {
          if (next) setSelectedPlatform(next as Platform);
        }}
      >
        {PLATFORMS.filter((item) => release.artifacts.some((artifact) => artifact.platform === item)).map((item) => (
          <ToggleGroupItem key={item} value={item}>
            {platformLabel(item)}
          </ToggleGroupItem>
        ))}
      </ToggleGroup>
      <ul className="space-y-2">
        {(matching.length ? matching : release.artifacts).map((artifact) => (
          <ArtifactRow key={artifact.id} version={release.version} artifact={artifact} />
        ))}
      </ul>
    </div>
  );
}

function ArtifactRow({ version, artifact }: { version: string; artifact: Artifact }) {
  return (
    <li className="flex flex-col gap-2 rounded-md border border-line px-4 py-3 sm:flex-row sm:items-center sm:justify-between">
      <div className="min-w-0">
        <p className="text-sm font-medium">
          {kindLabel(artifact.kind)}
          <span className="ml-2 text-faint">{platformLabel(artifact.platform)}</span>
        </p>
        <p className="truncate font-mono text-[11px] text-faint">
          {artifact.filename} · {formatBytes(artifact.size)} · sha256:{artifact.sha256.slice(0, 16)}
        </p>
      </div>
      <Button asChild size="sm">
        <a href={artifactUrl(version, artifact)}>Download</a>
      </Button>
    </li>
  );
}

// A Windows visitor is offered the Linux packages, which run inside WSL 2.
function detectWindows(): boolean {
  const platform = navigator.platform?.toLowerCase() ?? "";
  return platform.startsWith("win") || navigator.userAgent.toLowerCase().includes("windows");
}

function detectPlatform(): Platform {
  const ua = navigator.userAgent.toLowerCase();
  const platform = navigator.platform?.toLowerCase() ?? "";
  const isMac = platform.includes("mac") || ua.includes("mac os");
  const isArm = ua.includes("arm64") || ua.includes("aarch64") || platform.includes("arm");
  if (isMac) return isArm ? "darwin-arm64" : ua.includes("intel") ? "darwin-amd64" : "darwin-arm64";
  return isArm ? "linux-arm64" : "linux-amd64";
}
