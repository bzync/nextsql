import { AdjacentNav } from "@/components/AdjacentNav";
import { TableOfContents } from "@/components/docs/TableOfContents";
import { Markdown } from "@/lib/markdown";
import { adjacentDocs, findDocGroup } from "@/lib/nav";
import type { DocPage as DocPageData } from "@/lib/content";

export function DocPage({ doc }: { doc: DocPageData }) {
  const { prev, next } = adjacentDocs(doc.slug);
  const group = findDocGroup(doc.slug);
  const toc = doc.headings.filter((heading) => heading.level === 2);

  return (
    <div className="grid grid-cols-1 xl:grid-cols-[minmax(0,1fr)_13.5rem]">
      <article className="min-w-0 px-4 py-8 sm:px-8 sm:py-10 lg:py-12">
        {group ? <p className="kicker">{group.title}</p> : null}
        {toc.length > 0 ? (
          <details className="mt-5 rounded-md border border-line xl:hidden">
            <summary className="flex cursor-pointer list-none items-center justify-between px-3 py-2.5 text-[12px] font-medium text-muted marker:content-none [&::-webkit-details-marker]:hidden">
              On this page
              <span className="text-faint" aria-hidden="true">
                +
              </span>
            </summary>
            <ul className="space-y-0.5 border-t border-line px-2 py-2">
              {toc.map((heading) => (
                <li key={heading.id}>
                  <a
                    href={`#${heading.id}`}
                    className="block rounded-md px-2 py-2 text-[13px] text-muted hover:bg-bg-hover hover:text-foreground"
                  >
                    {heading.text}
                  </a>
                </li>
              ))}
            </ul>
          </details>
        ) : null}
        <div className="doc-prose mt-4">
          <Markdown source={doc.body} />
        </div>
        <AdjacentNav
          prev={prev ? { href: `/docs/${prev.slug}`, label: prev.title } : undefined}
          next={next ? { href: `/docs/${next.slug}`, label: next.title } : undefined}
        />
      </article>
      {toc.length > 0 ? (
        <aside className="hidden xl:block">
          <div className="sticky top-[4.5rem] border-l border-line py-12 pl-5">
            <TableOfContents items={toc} />
          </div>
        </aside>
      ) : null}
    </div>
  );
}
