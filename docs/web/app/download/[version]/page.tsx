import type { Metadata } from "next";
import Link from "next/link";
import { notFound } from "next/navigation";
import { AdjacentNav } from "@/components/AdjacentNav";
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
      <p className="kicker">
        <Link href="/download" className="hover:text-foreground">
          Downloads
        </Link>
        <span className="mx-2 text-faint">/</span>
        {release.version}
      </p>
      <div className="mt-3 flex flex-wrap items-center gap-2">
        <h1 className="text-[2rem] font-semibold tracking-[-0.028em]">{release.version}</h1>
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
              <span className="text-faint">•</span>
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
                <p className="kicker">{changeKindLabel(kind)}</p>
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

      <AdjacentNav
        prev={older ? { href: `/download/${older.version}`, label: older.version } : undefined}
        next={newer ? { href: `/download/${newer.version}`, label: newer.version } : undefined}
      />
    </main>
  );
}
