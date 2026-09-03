import type { Metadata } from "next";
import Link from "next/link";
import { notFound } from "next/navigation";
import { Badge } from "@bzync/rui";
import { CHANGE_KINDS, changeKindLabel, formatDate, getRelease, groupChanges, listReleases } from "@/lib/releases";
import { PlatformDownloads } from "@/components/download/PlatformDownloads";

export async function generateStaticParams() {
  const releases = await listReleases();
  return releases.map((release) => ({ version: release.version }));
}

export async function generateMetadata({ params }: { params: Promise<{ version: string }> }): Promise<Metadata> {
  const { version } = await params;
  const release = await getRelease(decodeURIComponent(version));
  if (!release) return { title: "Release" };
  return {
    title: `NextSQL ${release.version}`,
    description: release.summary || release.title,
  };
}

export default async function ReleasePage({ params }: { params: Promise<{ version: string }> }) {
  const { version } = await params;
  const decoded = decodeURIComponent(version);
  const [release, all] = await Promise.all([getRelease(decoded), listReleases()]);
  if (!release) notFound();
  const grouped = groupChanges(release.changes);
  const index = all.findIndex((item) => item.version === release.version);
  const newer = index > 0 ? all[index - 1] : undefined;
  const older = index >= 0 && index < all.length - 1 ? all[index + 1] : undefined;

  return (
    <main id="content" className="mx-auto max-w-6xl px-4 py-12 sm:px-5 sm:py-16">
      <p className="text-[11px] font-medium text-slate-400 dark:text-slate-500">
        <Link href="/download" className="hover:text-foreground">
          downloads
        </Link>{" "}
        / {release.version}
      </p>
      <div className="mt-3 flex flex-wrap items-center gap-2">
        <h1 className="text-[2rem] font-bold tracking-[-0.035em]">{release.version}</h1>
        {release.latest ? <Badge>latest</Badge> : null}
        <Badge variant={release.channel === "stable" ? "info" : "warning"}>{release.channel}</Badge>
      </div>
      <p className="mt-1 font-mono text-[12px] text-faint">{formatDate(release.releasedAt)}</p>
      <h2 className="mt-6 text-xl font-semibold tracking-tight">{release.title}</h2>
      <p className="mt-3 max-w-2xl text-[16px] leading-relaxed text-muted">{release.summary}</p>

      {release.highlights.length > 0 ? (
        <ul className="mt-6 max-w-2xl space-y-2">
          {release.highlights.map((item) => (
            <li key={item} className="flex gap-2 text-sm leading-6">
              <span className="text-blue-500 dark:text-blue-400">•</span>
              <span>{item}</span>
            </li>
          ))}
        </ul>
      ) : null}

      <section className="mt-10">
        <h3 className="text-sm font-semibold">Downloads</h3>
        <div className="mt-4">
          <PlatformDownloads release={release} />
        </div>
      </section>

      <section className="mt-12">
        <h3 className="text-sm font-semibold">What’s new</h3>
        <div className="mt-4 space-y-6">
          {CHANGE_KINDS.map((kind) =>
            grouped[kind].length === 0 ? null : (
              <div key={kind}>
                <p className="text-[11px] font-semibold uppercase tracking-[0.16em] text-faint">{changeKindLabel(kind)}</p>
                <ul className="mt-2 space-y-2">
                  {grouped[kind].map((change) => (
                    <li key={change.id} className="flex gap-3 text-sm leading-6">
                      <span className="w-16 shrink-0 font-mono text-[11px] uppercase tracking-wide text-faint">
                        {change.area}
                      </span>
                      <span>{change.text}</span>
                    </li>
                  ))}
                </ul>
              </div>
            ),
          )}
        </div>
      </section>

      <nav className="mt-14 grid gap-3 sm:grid-cols-2">
        {older ? (
          <Link
            href={`/download/${older.version}`}
            className="rounded-lg border border-black/[0.10] bg-white/70 px-4 py-4 dark:border-white/[0.09] dark:bg-transparent"
          >
            <div className="text-[11px] font-semibold uppercase tracking-[0.16em] text-slate-400">Previous</div>
            <div className="mt-1 text-sm font-medium">{older.version}</div>
          </Link>
        ) : (
          <span className="hidden sm:block" />
        )}
        {newer ? (
          <Link
            href={`/download/${newer.version}`}
            className="rounded-lg border border-black/[0.10] bg-white/70 px-4 py-4 text-left sm:text-right dark:border-white/[0.09] dark:bg-transparent"
          >
            <div className="text-[11px] font-semibold uppercase tracking-[0.16em] text-slate-400">Next</div>
            <div className="mt-1 text-sm font-medium">{newer.version}</div>
          </Link>
        ) : null}
      </nav>
    </main>
  );
}
