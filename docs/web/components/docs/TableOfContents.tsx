"use client";

import { useEffect, useState } from "react";
import { cn } from "@/lib/cn";

type TocItem = { id: string; text: string };

export function TableOfContents({ items }: { items: TocItem[] }) {
  const [active, setActive] = useState(items[0]?.id ?? "");

  useEffect(() => {
    if (items.length === 0) return;
    const headings = items
      .map((item) => document.getElementById(item.id))
      .filter((node): node is HTMLElement => node !== null);
    if (headings.length === 0) return;

    const observer = new IntersectionObserver(
      (entries) => {
        const visible = entries
          .filter((entry) => entry.isIntersecting)
          .sort((a, b) => a.boundingClientRect.top - b.boundingClientRect.top);
        if (visible[0]?.target.id) {
          setActive(visible[0].target.id);
          return;
        }
        const above = headings.filter((node) => node.getBoundingClientRect().top < 96);
        const last = above[above.length - 1];
        if (last) setActive(last.id);
      },
      { rootMargin: "-80px 0px -70% 0px", threshold: [0, 1] },
    );

    headings.forEach((node) => observer.observe(node));
    return () => observer.disconnect();
  }, [items]);

  if (items.length === 0) return null;

  return (
    <nav aria-label="On this page">
      <p className="kicker">On this page</p>
      <ul className="mt-3 space-y-1.5">
        {items.map((item) => (
          <li key={item.id}>
            <a
              href={`#${item.id}`}
              data-active={item.id === active ? "true" : undefined}
              className={cn(
                "toc-link block py-0.5 text-[13px] leading-5 transition-colors",
                item.id === active ? "text-foreground" : "text-muted hover:text-foreground",
              )}
            >
              {item.text}
            </a>
          </li>
        ))}
      </ul>
    </nav>
  );
}
