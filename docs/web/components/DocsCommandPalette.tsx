"use client";

import { useEffect, useMemo } from "react";
import { useRouter } from "next/navigation";
import { CommandPalette, useCommand, type CommandItem } from "@bzync/rui";
import type { DocsSearchEntry } from "@/lib/search";

/**
 * Global documentation search, backed by rui's <CommandPalette>.
 * Cmd/Ctrl+K is handled by <CommandProvider>; "/" is wired up here.
 */
export function DocsCommandPalette({ entries }: { entries: DocsSearchEntry[] }) {
  const router = useRouter();
  const { setOpen } = useCommand();

  const items = useMemo<CommandItem[]>(
    () =>
      entries.map((entry) => ({
        id: entry.id,
        label: entry.label,
        description: entry.description,
        group: entry.group,
        keywords: entry.keywords,
        onSelect: () => router.push(entry.href),
      })),
    [entries, router],
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
      items={items}
      placeholder="Search documentation…"
      emptyText="No matching pages."
      ariaLabel="Search documentation"
      inputClassName="docs-search-input border-0"
    />
  );
}
