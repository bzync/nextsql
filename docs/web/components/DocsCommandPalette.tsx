"use client";

import { useEffect, useMemo } from "react";
import { useRouter } from "next/navigation";
import { CommandPalette, useCommand, type CommandItem } from "@bzync/rui";
import { docsNav, docHref } from "@/lib/nav";

/**
 * Global documentation search, backed by rui's <CommandPalette>.
 * Cmd/Ctrl+K is handled by <CommandProvider>; "/" is wired up here.
 */
export function DocsCommandPalette() {
  const router = useRouter();
  const { setOpen } = useCommand();

  const items = useMemo<CommandItem[]>(
    () =>
      docsNav.flatMap((group) =>
        group.items.map((item) => ({
          id: item.slug,
          label: item.title,
          description: item.description,
          group: group.title,
          keywords: [item.slug],
          onSelect: () => router.push(docHref(item.slug)),
        })),
      ),
    [router],
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
    />
  );
}
