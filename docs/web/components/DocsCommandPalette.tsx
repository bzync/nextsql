"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useRouter } from "next/navigation";
import { CommandPalette, useCommand, type CommandItem } from "@bzync/rui";
import type { DocsSearchEntry } from "@/lib/search";

function escapeRegex(str: string): string {
  return str.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

function countMatches(entry: DocsSearchEntry, query: string): number {
  const q = query.trim();
  if (!q) return 0;
  const parts = new Set<string>();
  if (entry.label) parts.add(entry.label);
  if (entry.description) parts.add(entry.description);
  if (entry.content) parts.add(entry.content);
  if (entry.keywords) {
    for (const kw of entry.keywords) {
      if (kw) parts.add(kw);
    }
  }
  const text = Array.from(parts).join(" ");
  const terms = [q, ...q.split(/\s+/).filter((t) => t.length >= 2)];
  const uniqueTerms = Array.from(new Set(terms)).sort((a, b) => b.length - a.length);
  const pattern = uniqueTerms.map(escapeRegex).join("|");
  const regex = new RegExp(pattern, "gi");
  const matches = text.match(regex);
  return matches ? matches.length : 0;
}

function PageIcon() {
  return (
    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z" />
      <polyline points="14 2 14 8 20 8" />
    </svg>
  );
}

function SectionIcon() {
  return (
    <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <line x1="4" y1="9" x2="20" y2="9" />
      <line x1="4" y1="15" x2="20" y2="15" />
      <line x1="10" y1="3" x2="8" y2="21" />
      <line x1="16" y1="3" x2="14" y2="21" />
    </svg>
  );
}

/**
 * Global documentation search, backed by rui's <CommandPalette>.
 * Cmd/Ctrl+K is handled by <CommandProvider>; "/" is wired up here.
 */
export function DocsCommandPalette({ entries }: { entries: DocsSearchEntry[] }) {
  const router = useRouter();
  const { open, setOpen } = useCommand();
  const [searchQuery, setSearchQuery] = useState("");
  const queryRef = useRef("");

  useEffect(() => {
    if (!open) {
      const t = setTimeout(() => {
        setSearchQuery("");
        queryRef.current = "";
      }, 0);
      return () => clearTimeout(t);
    }
  }, [open]);

  useEffect(() => {
    let timer: ReturnType<typeof setTimeout> | undefined;
    const handleInput = (e: Event) => {
      const target = e.target as HTMLElement | null;
      if (
        target instanceof HTMLInputElement &&
        target.classList.contains("docs-search-input")
      ) {
        const val = target.value || "";
        queryRef.current = val;
        clearTimeout(timer);
        timer = setTimeout(() => {
          setSearchQuery(val);
        }, 100);
      }
    };
    document.addEventListener("input", handleInput, false);
    return () => {
      clearTimeout(timer);
      document.removeEventListener("input", handleInput, false);
    };
  }, []);

  const handleSelect = useCallback(
    (href: string) => {
      const input = document.querySelector(".docs-search-input") as HTMLInputElement | null;
      const query = (input?.value || queryRef.current || "").trim();
      let target = href;
      if (query) {
        const [base, hash] = target.split("#");
        const sep = base.includes("?") ? "&" : "?";
        target = `${base}${sep}q=${encodeURIComponent(query)}${hash ? `#${hash}` : ""}`;
        try {
          sessionStorage.setItem("docs_search_query", query);
        } catch {}
        window.dispatchEvent(
          new CustomEvent("docs-search-navigate", {
            detail: { query, href: target },
          }),
        );
      }
      router.push(target);
    },
    [router],
  );

  const items = useMemo<CommandItem[]>(
    () =>
      entries.map((entry) => {
        const count = searchQuery ? countMatches(entry, searchQuery) : 0;
        const countBadge =
          count > 0 ? `${count} ${count === 1 ? "match" : "matches"}` : undefined;

        return {
          id: entry.id,
          label: entry.label,
          description: entry.description,
          group: entry.group,
          keywords: entry.keywords,
          icon: entry.id.startsWith("page:") ? <PageIcon /> : <SectionIcon />,
          shortcut: countBadge ? [countBadge] : undefined,
          onSelect: () => handleSelect(entry.href),
        };
      }),
    [entries, searchQuery, handleSelect],
  );

  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (
        event.key === "/" &&
        !(event.target instanceof HTMLInputElement) &&
        !(event.target instanceof HTMLTextAreaElement) &&
        !(event.target instanceof HTMLElement && event.target.isContentEditable)
      ) {
        event.preventDefault();
        setOpen(true);
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [setOpen]);

  return (
    <CommandPalette
      className="docs-command-palette"
      items={items}
      placeholder="Search documentation…"
      emptyText="No matching pages."
      ariaLabel="Search documentation"
      inputClassName="docs-search-input border-0"
    />
  );
}
