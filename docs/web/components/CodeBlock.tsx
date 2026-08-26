"use client";

import { HighlightCode } from "@/lib/highlight";
import { CopyButton } from "@/components/ui/copy-button";

export function CodeBlock({
  code,
  lang,
  title,
}: {
  code: string;
  lang?: string;
  title?: string;
}) {
  return (
    <div className="group relative overflow-hidden rounded-xl border border-black/10 bg-code-bg text-code-fg shadow-[0_18px_48px_-28px_rgba(15,23,42,0.45)] dark:border-white/10 dark:shadow-[0_18px_48px_-26px_rgba(0,0,0,0.90)]">
      <div className="flex items-center justify-between border-b border-white/10 bg-white/[0.03] px-4 py-2.5">
        <span className="font-mono text-xs text-slate-400">
          {title || lang || "code"}
        </span>
        <CopyButton value={code} tone="onDark" />
      </div>
      <pre className="overflow-x-auto overscroll-x-contain p-4 text-[12.5px] leading-6 text-code-fg [-webkit-overflow-scrolling:touch] sm:px-5 sm:text-[13px]">
        <code>
          <HighlightCode code={code} lang={lang} />
        </code>
      </pre>
    </div>
  );
}
