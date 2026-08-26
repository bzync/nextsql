import Link from "next/link";
import { Markdown } from "@/lib/markdown";
import { adjacentDocs } from "@/lib/nav";
import type { DocPage as DocPageData } from "@/lib/content";

export function DocPage({ doc }: { doc: DocPageData }) {
  const { prev, next } = adjacentDocs(doc.slug);
  const toc = doc.headings.filter((h) => h.level === 2);

  return (
    <div className="grid grid-cols-1 xl:grid-cols-[minmax(0,1fr)_180px]">
      <article className="min-w-0 px-4 py-8 sm:px-8 sm:py-10 lg:py-12">
        <p className="text-[11px] font-medium text-slate-400 dark:text-slate-500">docs / {doc.slug}</p>
        {toc.length > 0 ? (
          <details className="mt-4 rounded-lg border border-black/[0.10] bg-white/70 xl:hidden dark:border-white/[0.09] dark:bg-transparent">
            <summary className="flex cursor-pointer list-none items-center justify-between px-4 py-3 text-[11px] font-bold uppercase tracking-widest text-slate-500 marker:content-none [&::-webkit-details-marker]:hidden">
              On this page
              <span className="text-slate-400" aria-hidden="true">
                +
              </span>
            </summary>
            <ul className="space-y-1 border-t border-black/[0.07] px-2 py-2 dark:border-white/[0.07]">
              {toc.map((heading) => (
                <li key={heading.id}>
                  <a
                    href={`#${heading.id}`}
                    className="block rounded-md px-2 py-2 text-[13px] text-slate-600 hover:bg-black/[0.04] hover:text-slate-900 dark:text-slate-400 dark:hover:bg-white/[0.05] dark:hover:text-white"
                  >
                    {heading.text}
                  </a>
                </li>
              ))}
            </ul>
          </details>
        ) : null}
        <div className="doc-prose mt-3">
          <Markdown source={doc.body} />
        </div>
        <nav className="mt-14 grid max-w-[44rem] grid-cols-1 gap-3 sm:grid-cols-2">
          {prev ? (
            <Link
              href={`/docs/${prev.slug}`}
              className="rounded-lg border border-black/[0.10] bg-white/70 px-4 py-4 transition-colors hover:border-blue-500/40 dark:border-white/[0.09] dark:bg-transparent"
            >
              <div className="text-[11px] font-semibold uppercase tracking-[0.16em] text-slate-400">Previous</div>
              <div className="mt-1 text-sm font-medium">{prev.title}</div>
            </Link>
          ) : (
            <span className="hidden sm:block" />
          )}
          {next ? (
            <Link
              href={`/docs/${next.slug}`}
              className="rounded-lg border border-black/[0.10] bg-white/70 px-4 py-4 text-left transition-colors hover:border-blue-500/40 dark:border-white/[0.09] dark:bg-transparent sm:text-right"
            >
              <div className="text-[11px] font-semibold uppercase tracking-[0.16em] text-slate-400">Next</div>
              <div className="mt-1 text-sm font-medium">{next.title}</div>
            </Link>
          ) : null}
        </nav>
      </article>
      {toc.length > 0 ? (
        <aside className="hidden xl:block">
          <div className="sticky top-[4.5rem] border-l border-black/[0.07] py-12 pl-5 dark:border-white/[0.07]">
            <p className="text-[10px] font-bold uppercase tracking-widest text-slate-400 dark:text-slate-500">
              On this page
            </p>
            <ul className="mt-3 space-y-2">
              {toc.map((heading) => (
                <li key={heading.id}>
                  <a
                    href={`#${heading.id}`}
                    className="text-[12.5px] leading-5 text-slate-500 transition-colors hover:text-slate-900 dark:text-slate-400 dark:hover:text-white"
                  >
                    {heading.text}
                  </a>
                </li>
              ))}
            </ul>
          </div>
        </aside>
      ) : null}
    </div>
  );
}
