"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import { useEffect, useState } from "react";
import { SiteHeader } from "@/components/SiteHeader";
import { SearchDialog } from "@/components/Search";
import { docsNav, docHref } from "@/lib/nav";
import { site } from "@/lib/site";
import { cn } from "@/lib/cn";

export function DocsChrome({ children }: { children: React.ReactNode }) {
  const pathname = usePathname();
  const [search, setSearch] = useState(false);
  const [menu, setMenu] = useState(false);

  useEffect(() => {
    if (!menu) return;
    const previous = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    return () => {
      document.body.style.overflow = previous;
    };
  }, [menu]);

  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      const meta = event.metaKey || event.ctrlKey;
      if (event.key === "k" && meta) {
        event.preventDefault();
        setSearch(true);
        return;
      }
      if (event.key === "Escape") {
        setMenu(false);
        return;
      }
      if (
        event.key === "/" &&
        !(event.target instanceof HTMLInputElement) &&
        !(event.target instanceof HTMLTextAreaElement)
      ) {
        event.preventDefault();
        setSearch(true);
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);

  return (
    <div className="min-h-full">
      <SiteHeader
        onSearch={() => setSearch(true)}
        onMenu={() => setMenu(true)}
        menuOpen={menu}
      />
      <div className="mx-auto grid max-w-6xl grid-cols-1 lg:grid-cols-[224px_minmax(0,1fr)]">
        <aside className="hidden border-r border-black/[0.07] dark:border-white/[0.07] lg:block">
          <div className="sticky top-14 max-h-[calc(100vh-3.5rem)] overflow-auto px-2 py-6">
            <Sidebar pathname={pathname} />
          </div>
        </aside>
        <div className="min-w-0" id="content">
          {children}
          <div className="border-t border-black/[0.07] px-4 py-5 font-mono text-[11px] text-slate-500 dark:border-white/[0.07] sm:px-8">
            {site.version} ·{" "}
            <a href={site.github} className="hover:text-slate-900 dark:hover:text-white" target="_blank" rel="noreferrer">
              GitHub
            </a>
          </div>
        </div>
      </div>
      {menu ? (
        <div className="fixed inset-0 z-[60] lg:hidden">
          <button
            className="absolute inset-0 bg-slate-900/40 dark:bg-navy-950/60"
            onClick={() => setMenu(false)}
            aria-label="Close menu"
          />
          <div className="absolute inset-y-0 left-0 flex w-[min(88vw,20rem)] flex-col border-r border-line bg-bg pt-[env(safe-area-inset-top)] pb-[env(safe-area-inset-bottom)] shadow-xl">
            <div className="flex h-14 items-center justify-between px-4">
              <span className="text-sm font-semibold">Docs</span>
              <button
                type="button"
                onClick={() => setMenu(false)}
                className="inline-flex h-10 w-10 items-center justify-center rounded-lg text-slate-500 hover:bg-black/5 hover:text-slate-900 dark:hover:bg-white/6 dark:hover:text-white"
                aria-label="Close menu"
              >
                <CloseIcon />
              </button>
            </div>
            <div className="flex-1 overflow-auto overscroll-contain px-2 pb-6">
              <Sidebar pathname={pathname} onNavigate={() => setMenu(false)} />
            </div>
          </div>
        </div>
      ) : null}
      <SearchDialog open={search} onClose={() => setSearch(false)} />
    </div>
  );
}

function CloseIcon() {
  return (
    <svg width="18" height="18" viewBox="0 0 24 24" fill="none" aria-hidden="true">
      <path d="M6 6l12 12M18 6 6 18" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" />
    </svg>
  );
}

function Sidebar({
  pathname,
  onNavigate,
}: {
  pathname: string;
  onNavigate?: () => void;
}) {
  return (
    <nav aria-label="Documentation">
      {docsNav.map((group) => (
        <div key={group.title} className="mb-5">
          <p className="mb-1.5 px-3 text-[10px] font-bold uppercase tracking-widest text-slate-400 dark:text-slate-500">
            {group.title}
          </p>
          <ul className="space-y-0.5">
            {group.items.map((item) => {
              const href = docHref(item.slug);
              const active =
                pathname === href ||
                (item.slug === "introduction" && pathname === "/docs");
              return (
                <li key={item.slug}>
                  <Link
                    href={href}
                    onClick={onNavigate}
                    aria-current={active ? "page" : undefined}
                    className={cn(
                      "group relative flex min-h-11 items-center rounded-lg px-3 text-[14px] leading-none transition-all duration-150 lg:h-9 lg:min-h-0 lg:text-[13px]",
                      active
                        ? "border border-blue-200/80 bg-blue-50 font-semibold text-blue-700 dark:border-blue-400/[0.16] dark:bg-blue-500/[0.13] dark:text-blue-300"
                        : "text-slate-600 hover:bg-black/[0.04] hover:text-slate-900 dark:text-slate-400 dark:hover:bg-white/[0.055] dark:hover:text-slate-100",
                    )}
                  >
                    {active ? (
                      <span className="absolute top-1/2 left-0 h-4 w-[3px] -translate-y-1/2 rounded-r-full bg-blue-500 dark:bg-blue-400" />
                    ) : null}
                    {item.title}
                  </Link>
                </li>
              );
            })}
          </ul>
        </div>
      ))}
    </nav>
  );
}
