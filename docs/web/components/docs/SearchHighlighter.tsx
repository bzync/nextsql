"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { usePathname } from "next/navigation";

function escapeRegex(str: string): string {
  return str.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

function buildSearchRegex(query: string): RegExp | null {
  const q = query.trim();
  if (!q) return null;
  // Match full phrase first, then individual terms (>= 2 chars)
  const terms = [q, ...q.split(/\s+/).filter((t) => t.length >= 2)];
  const uniqueTerms = Array.from(new Set(terms)).sort((a, b) => b.length - a.length);
  const pattern = uniqueTerms.map(escapeRegex).join("|");
  return new RegExp(`(${pattern})`, "gi");
}

function clearMarks(container: HTMLElement) {
  const marks = container.querySelectorAll<HTMLElement>('mark[data-search-highlight="true"]');
  marks.forEach((mark) => {
    mark.replaceWith(document.createTextNode(mark.textContent || ""));
  });
  container.normalize();
}

export function SearchHighlighter() {
  const pathname = usePathname();
  const [activeQuery, setActiveQuery] = useState("");
  const [totalMatches, setTotalMatches] = useState(0);
  const [activeIndex, setActiveIndex] = useState(0);
  const marksRef = useRef<HTMLElement[]>([]);

  const scrollToMatch = useCallback((index: number, marks: HTMLElement[]) => {
    if (marks.length === 0) return;
    const clamped = (index + marks.length) % marks.length;
    marks.forEach((m, idx) => {
      if (idx === clamped) {
        m.classList.add("search-highlight-active");
        m.scrollIntoView({ behavior: "smooth", block: "center" });
      } else {
        m.classList.remove("search-highlight-active");
      }
    });
    setActiveIndex(clamped);
  }, []);

  const handleClear = useCallback(() => {
    const container =
      (document.querySelector(".doc-prose") as HTMLElement | null) ||
      (document.querySelector("#content article") as HTMLElement | null) ||
      (document.getElementById("content") as HTMLElement | null);

    if (container) {
      clearMarks(container);
    }
    marksRef.current = [];
    setActiveQuery("");
    setTotalMatches(0);
    setActiveIndex(0);

    try {
      sessionStorage.removeItem("docs_search_query");
      const url = new URL(window.location.href);
      let changed = false;
      for (const param of ["q", "highlight", "hl", "search"]) {
        if (url.searchParams.has(param)) {
          url.searchParams.delete(param);
          changed = true;
        }
      }
      if (changed) {
        window.history.replaceState({}, "", url.toString());
      }
    } catch {}
  }, []);

  const applyHighlights = useCallback(
    (queryStr: string) => {
      const q = queryStr.trim();
      const container =
        (document.querySelector(".doc-prose") as HTMLElement | null) ||
        (document.querySelector("#content article") as HTMLElement | null) ||
        (document.getElementById("content") as HTMLElement | null);

      if (!container) return;

      // Always clear previous marks first
      clearMarks(container);
      marksRef.current = [];

      if (!q) {
        setActiveQuery("");
        setTotalMatches(0);
        setActiveIndex(0);
        return;
      }

      const regex = buildSearchRegex(q);
      if (!regex) {
        setActiveQuery("");
        setTotalMatches(0);
        return;
      }

      // Collect eligible text nodes without mutating DOM while walking
      const textNodes: Text[] = [];
      const walker = document.createTreeWalker(container, NodeFilter.SHOW_TEXT, {
        acceptNode(node) {
          if (!node.textContent || !node.textContent.trim()) return NodeFilter.FILTER_REJECT;
          const parent = node.parentElement;
          if (!parent) return NodeFilter.FILTER_REJECT;
          const tag = parent.tagName.toLowerCase();
          if (
            tag === "script" ||
            tag === "style" ||
            tag === "textarea" ||
            tag === "input" ||
            tag === "mark" ||
            parent.hasAttribute("data-search-highlight") ||
            parent.closest(".search-highlight-bar") ||
            parent.closest(".table-of-contents") ||
            parent.closest(".adjacent-nav")
          ) {
            return NodeFilter.FILTER_REJECT;
          }
          return NodeFilter.FILTER_ACCEPT;
        },
      });

      let curr = walker.nextNode();
      while (curr) {
        textNodes.push(curr as Text);
        curr = walker.nextNode();
      }

      const createdMarks: HTMLElement[] = [];
      for (const node of textNodes) {
        const text = node.textContent;
        if (!text || !regex.test(text)) continue;

        regex.lastIndex = 0;
        const frag = document.createDocumentFragment();
        let lastIndex = 0;
        let match: RegExpExecArray | null;

        while ((match = regex.exec(text)) !== null) {
          if (match.index > lastIndex) {
            frag.appendChild(document.createTextNode(text.slice(lastIndex, match.index)));
          }
          const mark = document.createElement("mark");
          mark.className = "search-highlight";
          mark.setAttribute("data-search-highlight", "true");
          mark.textContent = match[0];
          createdMarks.push(mark);
          frag.appendChild(mark);
          lastIndex = match.index + match[0].length;
        }

        if (lastIndex < text.length) {
          frag.appendChild(document.createTextNode(text.slice(lastIndex)));
        }

        node.replaceWith(frag);
      }

      marksRef.current = createdMarks;
      setActiveQuery(q);
      setTotalMatches(createdMarks.length);
      setActiveIndex(0);

      if (createdMarks.length > 0) {
        // If there is an anchor hash, let hash take precedence, otherwise scroll to first match
        if (window.location.hash) {
          const hashId = window.location.hash.slice(1);
          const targetEl = document.getElementById(hashId);
          if (targetEl) {
            targetEl.scrollIntoView({ behavior: "smooth", block: "start" });
            const targetIdx = createdMarks.findIndex((m) => {
              return (
                Boolean(targetEl.compareDocumentPosition(m) & Node.DOCUMENT_POSITION_FOLLOWING) ||
                targetEl.contains(m)
              );
            });
            const idx = targetIdx >= 0 ? targetIdx : 0;
            createdMarks[idx].classList.add("search-highlight-active");
            setActiveIndex(idx);
            return;
          }
        }
        scrollToMatch(0, createdMarks);
      }
    },
    [scrollToMatch],
  );

  // Check URL query and session storage on navigation
  useEffect(() => {
    const readQuery = (): string => {
      try {
        const params = new URLSearchParams(window.location.search);
        return (
          params.get("q") ||
          params.get("highlight") ||
          params.get("hl") ||
          params.get("search") ||
          sessionStorage.getItem("docs_search_query") ||
          ""
        );
      } catch {
        return "";
      }
    };

    const timer = window.setTimeout(() => {
      applyHighlights(readQuery());
    }, 0);

    const onSearchNav = (e: Event) => {
      const customEvent = e as CustomEvent<{ query?: string }>;
      const query = customEvent.detail?.query ?? readQuery();
      applyHighlights(query);
    };

    const onPopState = () => {
      applyHighlights(readQuery());
    };

    window.addEventListener("docs-search-navigate", onSearchNav);
    window.addEventListener("popstate", onPopState);

    return () => {
      window.clearTimeout(timer);
      window.removeEventListener("docs-search-navigate", onSearchNav);
      window.removeEventListener("popstate", onPopState);
    };
  }, [pathname, applyHighlights]);

  // Keyboard navigation: Escape to clear, Enter/Shift+Enter to navigate matches
  useEffect(() => {
    if (totalMatches === 0) return;

    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        e.preventDefault();
        handleClear();
        return;
      }

      // If user is typing in an input or textarea, don't intercept Enter
      if (
        e.target instanceof HTMLInputElement ||
        e.target instanceof HTMLTextAreaElement ||
        (e.target instanceof HTMLElement && e.target.isContentEditable)
      ) {
        return;
      }

      if (e.key === "Enter" || e.key === "F3") {
        e.preventDefault();
        if (e.shiftKey) {
          scrollToMatch(activeIndex - 1, marksRef.current);
        } else {
          scrollToMatch(activeIndex + 1, marksRef.current);
        }
      }
    };

    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [totalMatches, activeIndex, handleClear, scrollToMatch]);

  if (!activeQuery || totalMatches === 0) {
    return null;
  }

  return (
    <div
      className="search-highlight-bar fixed bottom-6 left-1/2 -translate-x-1/2 z-50 flex items-center gap-2.5 rounded-full border border-line-strong bg-bg-card/95 px-3.5 py-1.5 text-xs font-medium text-ink shadow-2xl backdrop-blur-md transition-all duration-200"
      role="status"
      aria-live="polite"
    >
      <span className="flex items-center gap-1.5 font-semibold text-amber-500 dark:text-amber-400 select-none">
        <svg
          width="13"
          height="13"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          strokeWidth="2.5"
          strokeLinecap="round"
          strokeLinejoin="round"
          aria-hidden="true"
        >
          <circle cx="11" cy="11" r="8" />
          <path d="m21 21-4.35-4.35" />
        </svg>
        <span>Search:</span>
      </span>
      <span
        className="max-w-[130px] sm:max-w-[200px] truncate rounded bg-amber-500/15 px-1.5 py-0.5 font-mono text-[11px] text-amber-700 dark:text-amber-300 font-semibold select-none"
        title={activeQuery}
      >
        &quot;{activeQuery}&quot;
      </span>
      <span className="text-faint font-mono text-[11px] select-none whitespace-nowrap">
        {activeIndex + 1} of {totalMatches}
      </span>
      <div className="flex items-center gap-0.5 border-l border-line pl-1.5">
        <button
          type="button"
          onClick={() => scrollToMatch(activeIndex - 1, marksRef.current)}
          disabled={totalMatches <= 1}
          aria-label="Previous match (Shift+Enter)"
          title="Previous match (Shift+Enter)"
          className="flex h-5 w-5 items-center justify-center rounded hover:bg-bg-hover text-muted hover:text-ink disabled:opacity-30 transition-colors"
        >
          <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5">
            <path d="m18 15-6-6-6 6" />
          </svg>
        </button>
        <button
          type="button"
          onClick={() => scrollToMatch(activeIndex + 1, marksRef.current)}
          disabled={totalMatches <= 1}
          aria-label="Next match (Enter)"
          title="Next match (Enter)"
          className="flex h-5 w-5 items-center justify-center rounded hover:bg-bg-hover text-muted hover:text-ink disabled:opacity-30 transition-colors"
        >
          <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5">
            <path d="m6 9 6 6 6-6" />
          </svg>
        </button>
        <button
          type="button"
          onClick={handleClear}
          aria-label="Clear highlights (Esc)"
          title="Clear highlights (Esc)"
          className="ml-1 flex h-5 w-5 items-center justify-center rounded hover:bg-bg-hover text-muted hover:text-ink transition-colors"
        >
          <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5">
            <path d="M18 6 6 18M6 6l12 12" />
          </svg>
        </button>
      </div>
    </div>
  );
}
