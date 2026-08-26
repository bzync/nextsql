"use client";

import { useEffect, useMemo, useState } from "react";
import { useRouter } from "next/navigation";
import { allDocs } from "@/lib/nav";
import { Kbd } from "@/components/ui/kbd";

export function SearchDialog({
  open,
  onClose,
}: {
  open: boolean;
  onClose: () => void;
}) {
  const router = useRouter();
  const [query, setQuery] = useState("");
  const [active, setActive] = useState(0);

  const close = () => {
    setQuery("");
    setActive(0);
    onClose();
  };

  const results = useMemo(() => {
    const q = query.trim().toLowerCase();
    if (!q) return allDocs().slice(0, 8);
    return allDocs()
      .filter(
        (doc) =>
          doc.title.toLowerCase().includes(q) ||
          doc.description.toLowerCase().includes(q) ||
          doc.slug.includes(q),
      )
      .slice(0, 12);
  }, [query]);

  useEffect(() => {
    setActive(0);
  }, [query]);

  useEffect(() => {
    if (!open) return;
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        event.preventDefault();
        close();
      }
      if (event.key === "ArrowDown") {
        event.preventDefault();
        setActive((i) => Math.min(i + 1, Math.max(results.length - 1, 0)));
      }
      if (event.key === "ArrowUp") {
        event.preventDefault();
        setActive((i) => Math.max(i - 1, 0));
      }
      if (event.key === "Enter" && results[active]) {
        event.preventDefault();
        router.push(`/docs/${results[active].slug}`);
        close();
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [open, results, active, router]);

  useEffect(() => {
    if (!open) return;
    const previous = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    return () => {
      document.body.style.overflow = previous;
    };
  }, [open]);

  if (!open) return null;

  return (
    <div className="fixed inset-0 z-[60] flex items-stretch justify-center sm:items-start sm:px-4 sm:pt-[14vh]">
      <button
        className="absolute inset-0 bg-slate-900/40 backdrop-blur-[6px] dark:bg-navy-950/60"
        onClick={close}
        aria-label="Close search"
      />
      <div className="relative flex w-full max-w-lg flex-col overflow-hidden border-black/[0.10] bg-white shadow-[0_18px_48px_-22px_rgba(15,23,42,0.45)] max-sm:h-full max-sm:pt-[env(safe-area-inset-top)] sm:rounded-xl sm:border dark:border-white/[0.08] dark:bg-navy-800/95 dark:shadow-[0_18px_48px_-26px_rgba(0,0,0,0.90),inset_0_1px_0_rgba(255,255,255,0.055)]">
        <div className="flex items-center gap-2 border-b border-black/[0.07] px-3 dark:border-white/[0.07]">
          <SearchIcon />
          <input
            autoFocus
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            placeholder="Search documentation…"
            className="h-12 w-full bg-transparent text-base text-slate-900 outline-none placeholder:text-slate-400 shadow-none focus-visible:!shadow-none sm:text-sm dark:text-white dark:placeholder:text-slate-500"
          />
          <button
            type="button"
            onClick={close}
            className="inline-flex h-10 items-center rounded-lg px-2 text-sm text-slate-500 sm:hidden"
          >
            Close
          </button>
          <span className="hidden sm:inline-flex">
            <Kbd>ESC</Kbd>
          </span>
        </div>
        <ul className="max-h-none flex-1 overflow-auto p-1.5 sm:max-h-80">
          {results.length === 0 ? (
            <li className="px-4 py-8 text-center text-sm text-slate-500">No matching pages.</li>
          ) : (
            results.map((doc, index) => (
              <li key={doc.slug}>
                <button
                  type="button"
                  className={`flex min-h-11 w-full flex-col rounded-lg px-3 py-2.5 text-left transition-colors ${
                    index === active
                      ? "bg-blue-500/[0.13] text-blue-700 dark:text-blue-300"
                      : "hover:bg-black/[0.04] dark:hover:bg-white/[0.05]"
                  }`}
                  onMouseEnter={() => setActive(index)}
                  onClick={() => {
                    router.push(`/docs/${doc.slug}`);
                    close();
                  }}
                >
                  <span className="text-sm font-medium text-slate-900 dark:text-white">{doc.title}</span>
                  <span className="text-xs text-slate-500 dark:text-slate-400">{doc.description}</span>
                </button>
              </li>
            ))
          )}
        </ul>
      </div>
    </div>
  );
}

function SearchIcon() {
  return (
    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" className="shrink-0 text-slate-400" aria-hidden="true">
      <circle cx="11" cy="11" r="8" />
      <path d="m21 21-4.3-4.3" />
    </svg>
  );
}
