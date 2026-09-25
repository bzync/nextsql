import type { ResultSet } from "./api";

export type StaleTable = { name: string; live: number; analyzed: number | null };

// staleTables lists the tables whose live row count has drifted from the
// planner's last ANALYZE snapshot, plus any non-empty table ANALYZE has never
// seen (analyzed_rows NULL). The planner costs every plan from that snapshot,
// so this drift — not the live count — is what makes it pick a bad plan. An
// empty table ANALYZE has never seen is not worth an operator's attention, and
// a server too old to report analyzed_rows yields nothing rather than a
// fabricated warning.
export function staleTables(stats: ResultSet | undefined | null): StaleTable[] {
  if (!stats || !Array.isArray(stats.columns) || !Array.isArray(stats.rows)) return [];
  const nameAt = stats.columns.indexOf("table_name");
  const liveAt = stats.columns.indexOf("row_count");
  const analyzedAt = stats.columns.indexOf("analyzed_rows");
  if (nameAt < 0 || liveAt < 0 || analyzedAt < 0) return [];
  const out: StaleTable[] = [];
  for (const row of stats.rows) {
    if (!Array.isArray(row)) continue;
    const live = Number(row[liveAt] ?? 0);
    const raw = row[analyzedAt];
    const analyzed = raw === null || raw === undefined ? null : Number(raw);
    if (!Number.isFinite(live) || (analyzed !== null && !Number.isFinite(analyzed))) continue;
    if (analyzed === null ? live > 0 : analyzed !== live) {
      out.push({ name: String(row[nameAt]), live, analyzed });
    }
  }
  return out;
}

// describeStaleTables renders the first few drifted tables for an operator,
// naming the live and analyzed counts so the size of the drift is visible
// rather than just its existence.
export function describeStaleTables(stale: StaleTable[], limit = 6): string {
  const shown = stale
    .slice(0, limit)
    .map(
      (t) =>
        `${t.name} (${t.live.toLocaleString()} live vs ${
          t.analyzed === null ? "never analyzed" : t.analyzed.toLocaleString()
        })`,
    )
    .join(" · ");
  return stale.length > limit ? `${shown} · …` : shown;
}

// humanBytes renders a byte count in binary units. Storage figures arrive as
// raw byte strings, which are unreadable at the sizes a real deployment
// reaches.
export function humanBytes(n: number): string {
  if (!Number.isFinite(n) || n < 0) return String(n);
  const units = ["B", "KiB", "MiB", "GiB", "TiB", "PiB"];
  let v = n;
  let u = 0;
  while (v >= 1024 && u < units.length - 1) {
    v /= 1024;
    u++;
  }
  return `${u === 0 ? v : v.toFixed(2)} ${units[u]}`;
}

export type StorageSummary = {
  fileSize: number | null;
  pageSize: number;
  pageCount: number;
  freePages: number;
  livePages: number;
};

// summarizeStorage pulls the figures an operator reads system.storage for. It
// returns null when the row is absent or does not carry the columns, so a
// server older than these columns renders the plain table and no summary
// rather than a line of zeroes.
export function summarizeStorage(storage: ResultSet | undefined | null): StorageSummary | null {
  if (!storage || !Array.isArray(storage.columns) || !Array.isArray(storage.rows)) return null;
  const row = storage.rows[0];
  if (!Array.isArray(row)) return null;
  const at = (name: string) => storage.columns.indexOf(name);
  const cell = (name: string): number | null => {
    const i = at(name);
    if (i < 0) return null;
    const raw = row[i];
    if (raw === null || raw === undefined) return null;
    const n = Number(raw);
    return Number.isFinite(n) ? n : null;
  };
  const pageCount = cell("page_count");
  const pageSize = cell("page_size");
  if (pageCount === null || pageSize === null) return null;
  const freePages = cell("free_pages") ?? 0;
  return {
    fileSize: cell("file_size"),
    pageSize,
    pageCount,
    freePages,
    livePages: Math.max(0, pageCount - freePages),
  };
}

// describeStorage is the one-line form shown above the storage table. It
// never presents the live extent as the disk footprint: a preallocating
// deployment holds far more file than the high-water mark accounts for, and
// conflating them is what made the old numbers misleading.
export function describeStorage(s: StorageSummary | null): string {
  if (!s) return "";
  const live = `${s.livePages.toLocaleString()} live page${s.livePages === 1 ? "" : "s"} (${humanBytes(
    s.livePages * s.pageSize,
  )})`;
  const free = s.freePages > 0 ? `, ${s.freePages.toLocaleString()} reclaimable` : "";
  const disk = s.fileSize === null ? "file size unavailable" : `${humanBytes(s.fileSize)} on disk`;
  return `${disk} · ${live}${free}`;
}
