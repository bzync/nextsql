"use client";

import { useActionState, useEffect, useRef } from "react";
import { useRouter } from "next/navigation";
import {
  ARTIFACT_KINDS,
  PLATFORMS,
  type Release,
  artifactUrl,
  formatBytes,
  kindLabel,
  platformLabel,
} from "@/lib/release-model";
import { Button } from "@/components/ui/button";
import { deleteArtifactAction, uploadArtifactAction, type ActionState } from "@/app/admin/actions";

export function ArtifactManager({ release }: { release: Release }) {
  const router = useRouter();
  const formRef = useRef<HTMLFormElement>(null);
  const [state, action, pending] = useActionState(uploadArtifactAction, undefined as ActionState | undefined);

  useEffect(() => {
    if (state?.ok) {
      formRef.current?.reset();
      router.refresh();
    }
  }, [state, router]);

  return (
    <section className="space-y-5">
      <div>
        <h2 className="text-sm font-semibold">Installable binaries</h2>
        <p className="mt-1 text-sm text-muted">
          Upload nextsql, nextsqld, nextsql-bench, or a combined archive for each platform. Files land in{" "}
          <code className="rounded bg-bg-hover px-1 font-mono text-[12px]">/downloads/{release.version}/</code>.
        </p>
      </div>

      <form ref={formRef} action={action} className="grid gap-3 rounded-lg border border-black/[0.08] p-4 sm:grid-cols-[1fr_1fr_1fr_auto] sm:items-end dark:border-white/[0.08]">
        <input type="hidden" name="version" value={release.version} />
        <label className="block text-sm">
          <span className="mb-1.5 block text-[11px] font-semibold uppercase tracking-[0.16em] text-faint">Kind</span>
          <select name="kind" className="h-10 w-full rounded-lg border border-black/10 bg-white px-2 text-sm dark:border-white/10 dark:bg-white/[0.04]">
            {ARTIFACT_KINDS.map((kind) => (
              <option key={kind} value={kind}>
                {kindLabel(kind)}
              </option>
            ))}
          </select>
        </label>
        <label className="block text-sm">
          <span className="mb-1.5 block text-[11px] font-semibold uppercase tracking-[0.16em] text-faint">Platform</span>
          <select name="platform" className="h-10 w-full rounded-lg border border-black/10 bg-white px-2 text-sm dark:border-white/10 dark:bg-white/[0.04]">
            {PLATFORMS.map((platform) => (
              <option key={platform} value={platform}>
                {platformLabel(platform)}
              </option>
            ))}
          </select>
        </label>
        <label className="block text-sm">
          <span className="mb-1.5 block text-[11px] font-semibold uppercase tracking-[0.16em] text-faint">File</span>
          <input name="file" type="file" required className="block w-full text-sm file:mr-3 file:rounded-md file:border-0 file:bg-black/5 file:px-3 file:py-2 dark:file:bg-white/10" />
        </label>
        <Button type="submit" disabled={pending}>
          {pending ? "Uploading…" : "Upload"}
        </Button>
      </form>
      {state && !state.ok ? <p className="text-sm text-red-600 dark:text-red-400">{state.error}</p> : null}

      {release.artifacts.length === 0 ? (
        <p className="text-sm text-muted">No binaries yet. The public download page will keep pointing at go install until you upload files.</p>
      ) : (
        <ul className="divide-y divide-black/[0.06] rounded-lg border border-black/[0.08] dark:divide-white/[0.06] dark:border-white/[0.08]">
          {release.artifacts.map((artifact) => (
            <li key={artifact.id} className="flex flex-col gap-2 px-4 py-3 sm:flex-row sm:items-center sm:justify-between">
              <div className="min-w-0">
                <p className="truncate text-sm font-medium">
                  {kindLabel(artifact.kind)} · {platformLabel(artifact.platform)}
                </p>
                <p className="truncate font-mono text-[11px] text-faint">
                  {artifact.filename} · {formatBytes(artifact.size)} · sha256 {artifact.sha256.slice(0, 12)}…
                </p>
              </div>
              <div className="flex gap-2">
                <a href={artifactUrl(release.version, artifact.filename)} className="text-sm text-link underline underline-offset-3">
                  Download
                </a>
                <button
                  type="button"
                  className="text-sm text-red-600 dark:text-red-400"
                  onClick={async () => {
                    const result = await deleteArtifactAction(release.version, artifact.id);
                    if (result.ok) router.refresh();
                    else alert(result.error);
                  }}
                >
                  Remove
                </button>
              </div>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}
