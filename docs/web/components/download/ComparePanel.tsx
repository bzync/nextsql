"use client";

import { useMemo, useState } from "react";
import {
  type ChangeKind,
  type Release,
  CHANGE_KINDS,
  changeKindLabel,
  changesBetween,
  groupChanges,
} from "@/lib/release-model";
import { Badge } from "@bzync/rui";

const kindBadge: Record<ChangeKind, "success" | "info" | "warning" | "muted" | "error"> = {
  added: "success",
  changed: "info",
  fixed: "warning",
  removed: "muted",
  breaking: "error",
};

export function ComparePanel({ releases }: { releases: Release[] }) {
  const versions = releases.map((release) => release.version);
  const [from, setFrom] = useState(versions[1] ?? versions[0] ?? "");
  const [to, setTo] = useState(versions[0] ?? "");

  const grouped = useMemo(() => {
    if (!from || !to || from === to) {
      const current = releases.find((release) => release.version === to);
      return groupChanges(current?.changes ?? []);
    }
    return groupChanges(changesBetween(releases, from, to));
  }, [from, to, releases]);

  const total = CHANGE_KINDS.reduce((sum, kind) => sum + grouped[kind].length, 0);

  if (releases.length === 0) return null;

  return (
    <section className="rounded-xl border border-black/[0.08] p-5 sm:p-6 dark:border-white/[0.08]">
      <p className="text-[11px] font-semibold uppercase tracking-[0.18em] text-blue-500 dark:text-blue-400">
        Compare
      </p>
      <h2 className="mt-2 text-xl font-bold tracking-[-0.03em] sm:text-2xl">What changed between versions</h2>
      <p className="mt-2 max-w-2xl text-sm leading-6 text-muted">
        Pick two published releases. The list includes every added, changed, fixed, removed, and breaking note in
        that range.
      </p>
      <div className="mt-5 grid gap-3 sm:grid-cols-2">
        <label className="block text-sm">
          <span className="mb-1.5 block text-[11px] font-semibold uppercase tracking-[0.16em] text-faint">From</span>
          <select
            value={from}
            onChange={(event) => setFrom(event.target.value)}
            className="h-10 w-full rounded-lg border border-black/10 bg-white px-3 text-sm dark:border-white/10 dark:bg-white/[0.04]"
          >
            {versions.map((version) => (
              <option key={version} value={version}>
                {version}
              </option>
            ))}
          </select>
        </label>
        <label className="block text-sm">
          <span className="mb-1.5 block text-[11px] font-semibold uppercase tracking-[0.16em] text-faint">To</span>
          <select
            value={to}
            onChange={(event) => setTo(event.target.value)}
            className="h-10 w-full rounded-lg border border-black/10 bg-white px-3 text-sm dark:border-white/10 dark:bg-white/[0.04]"
          >
            {versions.map((version) => (
              <option key={version} value={version}>
                {version}
              </option>
            ))}
          </select>
        </label>
      </div>

      <p className="mt-4 text-sm text-muted">
        {from === to
          ? `Showing notes in ${to}.`
          : `${total} update${total === 1 ? "" : "s"} from ${from} to ${to}.`}
      </p>

      <div className="mt-5 space-y-5">
        {CHANGE_KINDS.map((kind) =>
          grouped[kind].length === 0 ? null : (
            <div key={kind}>
              <div className="mb-2 flex items-center gap-2">
                <Badge variant={kindBadge[kind]}>{changeKindLabel(kind)}</Badge>
                <span className="text-[12px] text-faint">{grouped[kind].length}</span>
              </div>
              <ul className="space-y-2">
                {grouped[kind].map((change) => (
                  <li key={change.id} className="flex gap-3 text-sm leading-6">
                    <span className="w-16 shrink-0 font-mono text-[11px] uppercase tracking-wide text-faint">
                      {change.area}
                    </span>
                    <span>{change.text}</span>
                  </li>
                ))}
              </ul>
            </div>
          ),
        )}
        {total === 0 ? <p className="text-sm text-muted">No comparison notes in this range yet.</p> : null}
      </div>
    </section>
  );
}
