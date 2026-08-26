"use client";

import { useEffect, useMemo, useState } from "react";
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
import { buttonClassName } from "@/components/ui/button";

export function PlatformDownloads({ release }: { release: Release }) {
  const [platform, setPlatform] = useState<Platform>("linux-amd64");

  useEffect(() => {
    setPlatform(detectPlatform());
  }, []);

  const matching = useMemo(
    () => release.artifacts.filter((artifact) => artifact.platform === platform),
    [release.artifacts, platform],
  );

  if (release.artifacts.length === 0) {
    return (
      <div className="rounded-lg border border-black/[0.08] px-4 py-4 text-sm text-muted dark:border-white/[0.08]">
        No prebuilt binaries for this version yet. Install from source with{" "}
        <code className="rounded bg-bg-hover px-1 font-mono text-[12px]">go install github.com/bzync/nextsql/cmd/nextsql@{release.version === "0.1.0-dev" ? "latest" : `v${release.version}`}</code>
        .
      </div>
    );
  }

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap gap-2">
        {PLATFORMS.filter((item) => release.artifacts.some((artifact) => artifact.platform === item)).map((item) => (
          <button
            key={item}
            type="button"
            onClick={() => setPlatform(item)}
            className={
              item === platform
                ? "rounded-lg bg-black/[0.06] px-3 py-1.5 text-sm font-medium dark:bg-white/[0.08]"
                : "rounded-lg px-3 py-1.5 text-sm text-muted hover:bg-black/[0.04] dark:hover:bg-white/[0.05]"
            }
          >
            {platformLabel(item)}
          </button>
        ))}
      </div>
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
    <li className="flex flex-col gap-2 rounded-lg border border-black/[0.08] px-4 py-3 sm:flex-row sm:items-center sm:justify-between dark:border-white/[0.08]">
      <div className="min-w-0">
        <p className="text-sm font-medium">
          {kindLabel(artifact.kind)}
          <span className="ml-2 text-faint">{platformLabel(artifact.platform)}</span>
        </p>
        <p className="truncate font-mono text-[11px] text-faint">
          {artifact.filename} · {formatBytes(artifact.size)} · sha256:{artifact.sha256.slice(0, 16)}
        </p>
      </div>
      <a href={artifactUrl(version, artifact.filename)} className={buttonClassName({ size: "sm" })}>
        Download
      </a>
    </li>
  );
}

function detectPlatform(): Platform {
  const ua = navigator.userAgent.toLowerCase();
  const platform = navigator.platform?.toLowerCase() ?? "";
  const isMac = platform.includes("mac") || ua.includes("mac os");
  const isWin = platform.includes("win") || ua.includes("windows");
  const isArm = ua.includes("arm64") || ua.includes("aarch64") || platform.includes("arm");
  if (isMac) return isArm ? "darwin-arm64" : ua.includes("intel") ? "darwin-amd64" : "darwin-arm64";
  if (isWin) return "windows-amd64";
  return isArm ? "linux-arm64" : "linux-amd64";
}
