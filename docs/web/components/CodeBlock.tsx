"use client";

import { HighlightCode } from "@/lib/highlight";
import { CopyButton } from "@bzync/rui";

export function isBashLang(lang?: string, title?: string): boolean {
  const l = (lang || "").toLowerCase().trim();
  const t = (title || "").toLowerCase().trim();
  const bashKeywords = /^(bash|sh|shell|terminal|zsh|console)$/i;
  return bashKeywords.test(l) || (!l && bashKeywords.test(t));
}

export function isSqlLang(lang?: string, title?: string): boolean {
  const l = (lang || "").toLowerCase().trim();
  const t = (title || "").toLowerCase().trim();
  const sqlKeywords = /^(sql|nsql|nextsql)$/i;
  return sqlKeywords.test(l) || (!l && sqlKeywords.test(t)) || t.endsWith(".sql");
}

export function isDriverLang(lang?: string, title?: string): boolean {
  const l = (lang || "").toLowerCase().trim();
  const t = (title || "").toLowerCase().trim();
  const driverPattern = /^(go|golang|js|javascript|ts|typescript|php|python|py|ruby|rb)$/i;
  return driverPattern.test(l) || (!l && driverPattern.test(t));
}

export function isProtoLang(lang?: string, title?: string): boolean {
  const l = (lang || "").toLowerCase().trim();
  const t = (title || "").toLowerCase().trim();
  const protoKeywords = /^(wire|protocol|proto|nsql-proto|frame|packet|binary)$/i;
  return (
    protoKeywords.test(l) ||
    (!l && protoKeywords.test(t)) ||
    t.includes("wire") ||
    t.includes("protocol") ||
    t.includes("frame")
  );
}

function getDriverMeta(lang?: string, title?: string): { label: string; badgeColor: string } {
  const l = (lang || title || "").toLowerCase().trim();
  if (l === "go" || l === "golang" || l.endsWith(".go")) {
    return { label: "go", badgeColor: "#38bdf8" };
  }
  if (l === "js" || l === "javascript" || l === "ts" || l === "typescript" || l.endsWith(".js") || l.endsWith(".ts")) {
    return { label: l.includes("ts") ? "ts" : "js", badgeColor: "#fcd34d" };
  }
  if (l === "php" || l.endsWith(".php")) {
    return { label: "php", badgeColor: "#a78bfa" };
  }
  if (l === "python" || l === "py" || l.endsWith(".py")) {
    return { label: "python", badgeColor: "#38bdf8" };
  }
  if (l === "ruby" || l === "rb" || l.endsWith(".rb")) {
    return { label: "ruby", badgeColor: "#fb7185" };
  }
  return { label: lang || title || "code", badgeColor: "#a5b4fc" };
}

export function CodeBlock({
  code,
  lang,
  title,
}: {
  code: string;
  lang?: string;
  title?: string;
}) {
  const isBash = isBashLang(lang, title);
  const isSql = !isBash && isSqlLang(lang, title);
  const isDriver = !isBash && !isSql && isDriverLang(lang, title);
  const isProto = !isBash && !isSql && !isDriver && isProtoLang(lang, title);

  if (isBash) {
    return (
      <div className="code-block-bash group relative overflow-hidden rounded-md border border-[var(--bash-border)] bg-[var(--bash-bg)] text-[var(--bash-fg)]">
        <div className="code-block-header flex items-center justify-between border-b border-[var(--bash-header-border)] bg-[var(--bash-header-bg)] px-4 py-2">
          <span className="inline-flex items-center gap-1.5 font-mono text-[11px] text-[var(--bash-header-fg)]">
            <span className="select-none font-bold text-[var(--bash-prompt)]" aria-hidden="true">
              $
            </span>
            {title || lang || "bash"}
          </span>
          <CopyButton
            value={code}
            label="copy"
            className="border-transparent bg-transparent lowercase text-[var(--bash-header-fg)] opacity-75 hover:bg-white/10 hover:opacity-100 hover:text-white"
          />
        </div>
        <pre className="overflow-x-auto overscroll-x-contain bg-transparent p-4 text-[12.5px] leading-6 text-[var(--bash-fg)] [-webkit-overflow-scrolling:touch] sm:px-5 sm:text-[13px]">
          <code>
            <HighlightCode code={code} lang={lang || "bash"} />
          </code>
        </pre>
      </div>
    );
  }

  if (isSql) {
    return (
      <div className="code-block-sql group relative overflow-hidden rounded-md border border-[var(--sql-border)] bg-[var(--sql-bg)] text-[var(--sql-fg)]">
        <div className="code-block-header flex items-center justify-between border-b border-[var(--sql-header-border)] bg-[var(--sql-header-bg)] px-4 py-2">
          <span className="inline-flex items-center gap-1.5 font-mono text-[11px] text-[var(--sql-header-fg)]">
            <DatabaseIcon className="h-3 w-3 text-[var(--sql-type)]" />
            {title || lang || "sql"}
          </span>
          <CopyButton
            value={code}
            label="copy"
            className="border-transparent bg-transparent lowercase text-[var(--sql-header-fg)] opacity-75 hover:bg-white/10 hover:opacity-100 hover:text-white"
          />
        </div>
        <pre className="overflow-x-auto overscroll-x-contain bg-transparent p-4 text-[12.5px] leading-6 text-[var(--sql-fg)] [-webkit-overflow-scrolling:touch] sm:px-5 sm:text-[13px]">
          <code>
            <HighlightCode code={code} lang={lang || "sql"} />
          </code>
        </pre>
      </div>
    );
  }

  if (isDriver) {
    const meta = getDriverMeta(lang, title);
    return (
      <div className={`code-block-driver code-block-${meta.label} group relative overflow-hidden rounded-md border border-[var(--driver-border)] bg-[var(--driver-bg)] text-[var(--driver-fg)]`}>
        <div className="code-block-header flex items-center justify-between border-b border-[var(--driver-header-border)] bg-[var(--driver-header-bg)] px-4 py-2">
          <span className="inline-flex items-center gap-1.5 font-mono text-[11px] text-[var(--driver-header-fg)]">
            <span
              className="inline-flex h-4 items-center justify-center rounded px-1 font-mono text-[10px] font-bold uppercase leading-none select-none"
              style={{
                color: meta.badgeColor,
                backgroundColor: `color-mix(in oklab, ${meta.badgeColor} 18%, transparent)`,
              }}
              aria-hidden="true"
            >
              {meta.label}
            </span>
            {title || lang}
          </span>
          <CopyButton
            value={code}
            label="copy"
            className="border-transparent bg-transparent lowercase text-[var(--driver-header-fg)] opacity-75 hover:bg-white/10 hover:opacity-100 hover:text-white"
          />
        </div>
        <pre className="overflow-x-auto overscroll-x-contain bg-transparent p-4 text-[12.5px] leading-6 text-[var(--driver-fg)] [-webkit-overflow-scrolling:touch] sm:px-5 sm:text-[13px]">
          <code>
            <HighlightCode code={code} lang={lang} />
          </code>
        </pre>
      </div>
    );
  }

  if (isProto) {
    return (
      <div className="code-block-proto group relative overflow-hidden rounded-md border border-[var(--proto-border)] bg-[var(--proto-bg)] text-[var(--proto-fg)]">
        <div className="code-block-header flex items-center justify-between border-b border-[var(--proto-header-border)] bg-[var(--proto-header-bg)] px-4 py-2">
          <span className="inline-flex items-center gap-1.5 font-mono text-[11px] text-[var(--proto-header-fg)]">
            <span
              className="inline-flex h-4 items-center justify-center rounded px-1.5 font-mono text-[10px] font-bold uppercase tracking-wider leading-none select-none text-[#2dd4bf] bg-[#2dd4bf]/15"
              aria-hidden="true"
            >
              wire
            </span>
            <span className="text-[var(--proto-header-fg)] opacity-90">
              {title || lang || "protocol"}
            </span>
          </span>
          <CopyButton
            value={code}
            label="copy"
            className="border-transparent bg-transparent lowercase text-[var(--proto-header-fg)] opacity-75 hover:bg-white/10 hover:opacity-100 hover:text-white"
          />
        </div>
        <pre className="overflow-x-auto overscroll-x-contain bg-transparent p-4 text-[12.5px] leading-6 text-[var(--proto-fg)] [-webkit-overflow-scrolling:touch] sm:px-5 sm:text-[13px]">
          <code>
            <HighlightCode code={code} lang={lang || "wire"} />
          </code>
        </pre>
      </div>
    );
  }

  return (
    <div className="group relative overflow-hidden rounded-md border border-white/10 bg-code-bg text-code-fg">
      <div className="flex items-center justify-between border-b border-white/10 px-4 py-2">
        <span className="font-mono text-[11px] text-slate-400">
          {title || lang || "code"}
        </span>
        <CopyButton
          value={code}
          label="copy"
          className="border-transparent bg-transparent lowercase text-slate-400 hover:bg-white/10 hover:text-white"
        />
      </div>
      <pre className="overflow-x-auto overscroll-x-contain bg-transparent p-4 text-[12.5px] leading-6 text-code-fg [-webkit-overflow-scrolling:touch] sm:px-5 sm:text-[13px]">
        <code>
          <HighlightCode code={code} lang={lang} />
        </code>
      </pre>
    </div>
  );
}

function DatabaseIcon({ className }: { className?: string }) {
  return (
    <svg
      width="12"
      height="12"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
      strokeLinecap="round"
      strokeLinejoin="round"
      className={className}
      aria-hidden="true"
    >
      <ellipse cx="12" cy="5" rx="9" ry="3" />
      <path d="M21 12c0 1.66-4 3-9 3s-9-1.34-9-3" />
      <path d="M3 5v14c0 1.66 4 3 9 3s9-1.34 9-3V5" />
    </svg>
  );
}
