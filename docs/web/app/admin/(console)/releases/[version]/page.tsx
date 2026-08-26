import { notFound } from "next/navigation";
import { ArtifactManager } from "@/components/admin/ArtifactManager";
import { ReleaseEditor } from "@/components/admin/ReleaseEditor";
import { getRelease } from "@/lib/releases";

export const dynamic = "force-dynamic";

export default async function EditReleasePage({ params }: { params: Promise<{ version: string }> }) {
  const { version } = await params;
  const release = await getRelease(decodeURIComponent(version), { includeDrafts: true });
  if (!release) notFound();

  return (
    <div className="max-w-3xl space-y-10">
      <div>
        <p className="text-[11px] font-semibold uppercase tracking-[0.18em] text-blue-500 dark:text-blue-400">
          Edit release
        </p>
        <h1 className="mt-2 text-2xl font-bold tracking-[-0.03em]">{release.version}</h1>
      </div>
      <ReleaseEditor mode="edit" release={release} />
      <ArtifactManager release={release} />
    </div>
  );
}
