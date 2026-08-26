import type { Metadata } from "next";
import Link from "next/link";
import { Badge } from "@/components/ui/badge";
import { buttonClassName } from "@/components/ui/button";
import { ComparePanel } from "@/components/download/ComparePanel";
import { PlatformDownloads } from "@/components/download/PlatformDownloads";
import { formatDate, latestRelease, listReleases } from "@/lib/releases";
import { site } from "@/lib/site";

export const dynamic = "force-dynamic";

export const metadata: Metadata = {
  title: "Download",
  description: "Download installable NextSQL binaries and compare what changed between versions.",
};

export default async function DownloadPage() {
  const releases = await listReleases();
  const latest = (await latestRelease()) ?? releases[0];

  return (
    <main id="content">
      <section className="border-b border-line">
        <div className="mx-auto max-w-6xl px-4 py-12 sm:px-5 sm:py-16">
          <p className="text-[11px] font-semibold uppercase tracking-[0.18em] text-blue-500 dark:text-blue-400">
            Downloads
          </p>
          <h1 className="mt-3 max-w-2xl text-[2.15rem] font-bold tracking-[-0.035em] sm:text-[2.5rem]">
            Installable NextSQL, versioned.
          </h1>
          <p className="mt-4 max-w-xl text-[16px] leading-relaxed text-muted">
            Prebuilt CLI and server binaries for each release, plus a changelog you can compare. Source installs
            with Go still work.
          </p>
          {latest ? (
            <div className="mt-8 rounded-xl border border-black/[0.08] p-5 sm:p-6 dark:border-white/[0.08]">
              <div className="flex flex-wrap items-center gap-2">
                <h2 className="text-lg font-semibold">{latest.version}</h2>
                {latest.latest ? <Badge>latest</Badge> : null}
                <Badge variant={latest.channel === "stable" ? "info" : "warning"}>{latest.channel}</Badge>
                <span className="font-mono text-[11px] text-faint">{formatDate(latest.releasedAt)}</span>
              </div>
              <p className="mt-2 text-sm leading-6 text-muted">{latest.summary}</p>
              {latest.highlights.length > 0 ? (
                <ul className="mt-4 space-y-1.5 text-sm">
                  {latest.highlights.map((item) => (
                    <li key={item} className="flex gap-2">
                      <span className="text-blue-500 dark:text-blue-400">•</span>
                      <span>{item}</span>
                    </li>
                  ))}
                </ul>
              ) : null}
              <div className="mt-6">
                <PlatformDownloads release={latest} />
              </div>
              <div className="mt-5 flex flex-wrap gap-3 text-sm">
                <Link href={`/download/${latest.version}`} className="text-link underline underline-offset-3">
                  Release notes
                </Link>
                <Link href="/docs/install" className="text-muted hover:text-foreground">
                  Install from source
                </Link>
              </div>
            </div>
          ) : (
            <p className="mt-8 text-sm text-muted">
              No published binaries yet. Use{" "}
              <Link href="/docs/install" className="text-link underline underline-offset-3">
                go install
              </Link>{" "}
              ({site.version}).
            </p>
          )}
        </div>
      </section>

      {releases.length > 0 ? (
        <section className="border-b border-line">
          <div className="mx-auto max-w-6xl px-4 py-12 sm:px-5 sm:py-16">
            <ComparePanel releases={releases} />
          </div>
        </section>
      ) : null}

      <section>
        <div className="mx-auto max-w-6xl px-4 py-12 sm:px-5 sm:py-16">
          <h2 className="text-xl font-bold tracking-[-0.03em] sm:text-2xl">All versions</h2>
          {releases.length === 0 ? (
            <p className="mt-3 text-sm text-muted">Nothing published.</p>
          ) : (
            <ul className="mt-6 divide-y divide-black/[0.06] overflow-hidden rounded-xl border border-black/[0.08] dark:divide-white/[0.06] dark:border-white/[0.08]">
              {releases.map((release) => (
                <li key={release.version} className="flex flex-col gap-3 px-4 py-4 sm:flex-row sm:items-center sm:justify-between">
                  <div className="min-w-0">
                    <div className="flex flex-wrap items-center gap-2">
                      <Link href={`/download/${release.version}`} className="font-semibold hover:underline">
                        {release.version}
                      </Link>
                      {release.latest ? <Badge>latest</Badge> : null}
                      <Badge variant={release.channel === "stable" ? "info" : "warning"}>{release.channel}</Badge>
                    </div>
                    <p className="mt-1 text-sm text-muted">{release.title}</p>
                    <p className="mt-1 font-mono text-[11px] text-faint">
                      {formatDate(release.releasedAt)} · {release.artifacts.length} file{release.artifacts.length === 1 ? "" : "s"}
                    </p>
                  </div>
                  <Link href={`/download/${release.version}`} className={buttonClassName({ variant: "outline", size: "sm" })}>
                    Notes
                  </Link>
                </li>
              ))}
            </ul>
          )}
        </div>
      </section>
    </main>
  );
}
