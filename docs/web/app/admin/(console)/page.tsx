import Link from "next/link";
import { Badge } from "@/components/ui/badge";
import { buttonClassName } from "@/components/ui/button";
import { formatDate, listReleases } from "@/lib/releases";

export const dynamic = "force-dynamic";

export default async function AdminHomePage() {
  const releases = await listReleases({ includeDrafts: true });

  return (
    <div>
      <div className="flex flex-col gap-4 sm:flex-row sm:items-start sm:justify-between">
        <div className="min-w-0">
          <p className="text-[11px] font-semibold uppercase tracking-[0.18em] text-blue-500 dark:text-blue-400">
            Releases
          </p>
          <h1 className="mt-2 text-2xl font-bold tracking-[-0.03em] sm:text-3xl">Installable NextSQL</h1>
          <p className="mt-2 max-w-xl text-sm leading-6 text-muted">
            Create a version, describe what changed, then upload binaries. Published releases appear on{" "}
            <Link href="/download" className="text-link underline underline-offset-3">
              /download
            </Link>{" "}
            with a feature comparison against earlier versions.
          </p>
        </div>
        <Link href="/admin/releases/new" className={buttonClassName({ className: "shrink-0 self-start" })}>
          New release
        </Link>
      </div>

      {releases.length === 0 ? (
        <p className="mt-10 text-sm text-muted">No releases yet.</p>
      ) : (
        <ul className="mt-8 divide-y divide-black/[0.06] overflow-hidden rounded-xl border border-black/[0.08] dark:divide-white/[0.06] dark:border-white/[0.08]">
          {releases.map((release) => (
            <li key={release.version} className="flex flex-col gap-3 px-4 py-4 sm:flex-row sm:items-center sm:justify-between">
              <div className="min-w-0">
                <div className="flex flex-wrap items-center gap-2">
                  <Link href={`/admin/releases/${release.version}`} className="font-semibold hover:underline">
                    {release.version}
                  </Link>
                  <Badge variant={release.status === "published" ? "success" : "muted"}>{release.status}</Badge>
                  <Badge variant={release.channel === "stable" ? "info" : "warning"}>{release.channel}</Badge>
                  {release.latest ? <Badge>latest</Badge> : null}
                </div>
                <p className="mt-1 truncate text-sm text-muted">{release.title}</p>
                <p className="mt-1 font-mono text-[11px] text-faint">
                  {formatDate(release.releasedAt)} · {release.artifacts.length} file{release.artifacts.length === 1 ? "" : "s"} ·{" "}
                  {release.changes.length} change{release.changes.length === 1 ? "" : "s"}
                </p>
              </div>
              <Link href={`/admin/releases/${release.version}`} className={buttonClassName({ variant: "outline", size: "sm" })}>
                Edit
              </Link>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
