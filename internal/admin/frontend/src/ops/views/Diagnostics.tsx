import { useMemo, useState } from "react";
import {
  Alert,
  Badge,
  Button,
  Heading,
  Inline,
  Link,
  Pagination,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
  Tabs,
  TabsContent,
  TabsList,
  TabsTrigger,
  Text,
} from "@bzync/rui";
import { api } from "../api";
import { useReadModel } from "../useReadModel";
import { ViewFrame } from "./ViewFrame";
import { Section } from "./Section";
import type { ResultSet } from "../api";
import { Icon, type IconName } from "../../shared/icons";

const CATEGORY_META: Record<string, { label: string; icon: IconName; desc: string }> = {
  throughput: { label: "Throughput", icon: "activity", desc: "Query, transaction, and statement rates" },
  latency: { label: "Latency", icon: "clock", desc: "Execution and commit timings" },
  encryption: { label: "Encryption", icon: "lock", desc: "Storage envelope and field-level crypto" },
  storage: { label: "Storage", icon: "hard-drive", desc: "Buffer pool, page cache, and disk usage" },
  replication: { label: "Replication", icon: "network", desc: "Raft consensus, replica health, and lag" },
  maintenance: { label: "Maintenance", icon: "wrench", desc: "Storage reclamation and statistics rebuilds" },
  cdc: { label: "CDC", icon: "layers", desc: "Change data capture pipeline & stream status" },
  runtime: { label: "Runtime", icon: "cpu", desc: "Go runtime memory, GC pauses, and goroutines" },
};

// Diagnostics is the M9 Logs & Diagnostics view: live process metrics
// (system.metrics) and a bounded tail of the connected node's own structured
// log (system.server_log), both admin-only server-side and empty (not an
// error) for embedded/CLI use with nothing attached. The redacted
// diagnostic-bundle download is a later M9 increment. Metric values are
// rendered with their unit hint humanized (bytes -> KiB/MiB, nanoseconds ->
// ms, per_second/ratio_pct as-is); the raw string is always kept alongside.
export function Diagnostics({ onUnauthorized }: { onUnauthorized: () => void }) {
  const { data, error, loading } = useReadModel(api.diagnostics, onUnauthorized);
  return (
    <ViewFrame loading={loading} error={error} warnings={data?.warnings}>
      {data ? (
        <>
          <Alert variant="info" title="Scope">
            Live process metrics and a recent server-log tail from the connected
            node. Read-only. The log tail is an in-memory diagnostic buffer (the
            newest ~500 lines) — the durable log is still stderr / the service
            journal.
          </Alert>
          <Inline gap="sm" align="center" wrap>
            {/* A direct authenticated GET — the session cookie rides along
                same-origin; the server sends it back as a JSON attachment. */}
            <Button asChild variant="outline" size="sm">
              <Link href="/api/v1/diagnostics/bundle" download>Download diagnostic bundle</Link>
            </Button>
            <Text as="span" variant="muted" size="sm">
              A single redacted JSON document (metrics, config, cluster, TLS/key
              status, capabilities, audit-chain status, server-log tail). No key
              material, no tenant data.
            </Text>
          </Inline>
          <Section title="Server log" icon="file">
            <ServerLogPanel log={data.server_log} />
          </Section>
          <Section title="Metrics" icon="diagnostics">
            <MetricsPanel metrics={data.metrics} />
          </Section>
        </>
      ) : null}
    </ViewFrame>
  );
}

function levelVariant(level: string): "error" | "warning" | "info" | "muted" {
  const l = level.toUpperCase();
  if (l.startsWith("ERROR") || l.startsWith("FATAL")) return "error";
  if (l.startsWith("WARN")) return "warning";
  if (l.startsWith("INFO")) return "info";
  return "muted";
}

function ServerLogPanel({ log }: { log: ResultSet }) {
  const cols = log?.columns ?? [];
  const rows = log?.rows ?? [];
  const [search, setSearch] = useState("");
  const [levelFilter, setLevelFilter] = useState<string>("ALL");
  const [page, setPage] = useState(1);
  const [pageSize] = useState(15);

  const ci = (name: string) => cols.indexOf(name);
  const [cTime, cLevel, cMsg, cAttrs] = [ci("event_time"), ci("level"), ci("message"), ci("attributes")];

  const filteredRows = useMemo(() => {
    let list = rows;
    if (levelFilter !== "ALL") {
      list = list.filter((r) => String(r[cLevel] ?? "").toUpperCase().includes(levelFilter));
    }
    if (search.trim()) {
      const q = search.trim().toLowerCase();
      list = list.filter((r) =>
        r.some((cell) => cell !== null && String(cell).toLowerCase().includes(q))
      );
    }
    return list;
  }, [rows, search, levelFilter, cLevel]);

  const totalPages = Math.max(1, Math.ceil(filteredRows.length / pageSize));
  const safePage = Math.min(page, totalPages);
  const startIndex = (safePage - 1) * pageSize;
  const endIndex = Math.min(startIndex + pageSize, filteredRows.length);
  const paginatedRows = filteredRows.slice(startIndex, endIndex);

  if (cols.length === 0 || rows.length === 0) {
    return (
      <Alert variant="info" title="No server log available">
        The connected node has no in-memory log ring attached (embedded/CLI
        use), or nothing has been logged yet.
      </Alert>
    );
  }

  return (
    <div className="nsm-table-container">
      <div className="nsm-table-toolbar">
        <Inline gap="sm" align="center" justify="between" wrap>
          <Inline gap="xs" align="center">
            <div className="nsm-table-search">
              <Icon name="search" size={13} className="nsm-table-search-icon" />
              <input
                type="search"
                value={search}
                onChange={(e) => {
                  setSearch(e.target.value);
                  setPage(1);
                }}
                placeholder="Search server log…"
                className="nsm-table-search-input"
                aria-label="Filter server log"
              />
              {search ? (
                <button
                  type="button"
                  onClick={() => {
                    setSearch("");
                    setPage(1);
                  }}
                  className="nsm-table-search-clear"
                  aria-label="Clear filter"
                >
                  ×
                </button>
              ) : null}
            </div>
            <Inline gap="xs" align="center">
              {["ALL", "ERROR", "WARN", "INFO"].map((lvl) => (
                <button
                  key={lvl}
                  type="button"
                  onClick={() => {
                    setLevelFilter(lvl);
                    setPage(1);
                  }}
                  className={`nsm-page-size-btn${levelFilter === lvl ? " nsm-page-size-btn--active" : ""}`}
                >
                  {lvl}
                </button>
              ))}
            </Inline>
          </Inline>

          <span className="text-xs text-muted-foreground">
            {filteredRows.length} of {rows.length} logs
          </span>
        </Inline>
      </div>

      <div className="nsm-result-table-scroll" role="region" aria-label="Server log table" tabIndex={0}>
        <Table density="compact" scrollAreaClassName="nsm-result-table-inner">
          <TableHeader>
            <TableRow>
              <TableHead>Time</TableHead>
              <TableHead>Level</TableHead>
              <TableHead>Message</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {paginatedRows.length === 0 ? (
              <TableRow>
                <TableCell colSpan={3} className="text-center py-6 text-muted-foreground">
                  No log records match the current filter
                </TableCell>
              </TableRow>
            ) : (
              paginatedRows.map((r, i) => {
                const level = String(r[cLevel] ?? "");
                const attrs = String(r[cAttrs] ?? "");
                return (
                  <TableRow key={startIndex + i}>
                    <TableCell className="font-mono text-xs whitespace-nowrap">
                      <Text as="span" variant="muted">{String(r[cTime] ?? "")}</Text>
                    </TableCell>
                    <TableCell>
                      <Badge variant={levelVariant(level)}>{level || "?"}</Badge>
                    </TableCell>
                    <TableCell>
                      <Inline gap="xs" wrap>
                        <Text as="span">{String(r[cMsg] ?? "")}</Text>
                        {attrs ? <Text as="span" variant="muted" className="font-mono text-xs">{attrs}</Text> : null}
                      </Inline>
                    </TableCell>
                  </TableRow>
                );
              })
            )}
          </TableBody>
        </Table>
      </div>

      <div className="nsm-table-footer">
        <Inline gap="md" align="center" justify="between" wrap>
          <span className="text-xs text-muted-foreground">
            {filteredRows.length === 0 ? (
              "0 lines"
            ) : (
              <>
                Showing <strong className="text-foreground font-semibold">{startIndex + 1}</strong> to{" "}
                <strong className="text-foreground font-semibold">{endIndex}</strong> of{" "}
                <strong className="text-foreground font-semibold">{filteredRows.length}</strong>
                {rows.length !== filteredRows.length ? ` (filtered from ${rows.length})` : " log lines"}
              </>
            )}
          </span>

          {filteredRows.length > 15 ? (
            <Pagination
              page={safePage}
              totalPages={totalPages}
              onPageChange={(nextPage) => setPage(nextPage)}
              className="nsm-pagination"
            />
          ) : null}
        </Inline>
      </div>
    </div>
  );
}

function humanBytes(n: number): string {
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

function humanDuration(ns: number): string {
  if (!Number.isFinite(ns) || ns < 0) return String(ns);
  if (ns === 0) return "0";
  if (ns < 1_000) return `${ns} ns`;
  if (ns < 1_000_000) return `${(ns / 1_000).toFixed(1)} µs`;
  if (ns < 1_000_000_000) return `${(ns / 1_000_000).toFixed(2)} ms`;
  const s = ns / 1_000_000_000;
  if (s < 90) return `${s.toFixed(2)} s`;
  if (s < 5400) return `${(s / 60).toFixed(1)} min`;
  if (s < 129_600) return `${(s / 3600).toFixed(1)} h`;
  return `${(s / 86_400).toFixed(1)} d`;
}

function pretty(value: string, unit: string): string {
  const n = Number(value);
  switch (unit) {
    case "bytes":
      return humanBytes(n);
    case "nanoseconds":
      return humanDuration(n);
    case "ratio_pct":
      return Number.isFinite(n) ? `${n.toFixed(2)} %` : value;
    case "per_second":
      return Number.isFinite(n) ? n.toFixed(2) : value;
    default:
      return Number.isFinite(n) ? n.toLocaleString() : value;
  }
}

function MetricsPanel({ metrics }: { metrics: ResultSet }) {
  const cols = metrics?.columns ?? [];
  const rows = metrics?.rows ?? [];
  if (cols.length === 0 || rows.length === 0) {
    return (
      <Alert variant="info" title="No metrics available">
        The connected node has no process-level metrics registry attached
        (embedded/CLI use).
      </Alert>
    );
  }
  const ci = (name: string) => cols.indexOf(name);
  const [cCat, cName, cVal, cUnit] = [ci("category"), ci("name"), ci("value"), ci("unit")];
  const groups = new Map<string, (string | null)[][]>();
  for (const r of rows) {
    const cat = String(r[cCat] ?? "other").toLowerCase();
    if (!groups.has(cat)) groups.set(cat, []);
    groups.get(cat)!.push(r);
  }

  const knownOrder = ["throughput", "latency", "encryption", "storage", "replication", "maintenance", "cdc", "runtime"];
  const categories = [
    ...knownOrder.filter((cat) => groups.has(cat)),
    ...Array.from(groups.keys()).filter((cat) => !knownOrder.includes(cat)),
  ];

  const defaultCategory = categories[0] ?? "throughput";

  return (
    <Tabs defaultValue={defaultCategory}>
      <TabsList className="mb-4">
        {categories.map((cat) => {
          const meta = CATEGORY_META[cat] ?? {
            label: cat === "cdc" ? "CDC" : cat.charAt(0).toUpperCase() + cat.slice(1),
            icon: "diagnostics" as IconName,
            desc: "",
          };
          const count = groups.get(cat)?.length ?? 0;
          return (
            <TabsTrigger key={cat} value={cat}>
              <Inline gap="xs" align="center" wrap={false}>
                <Icon name={meta.icon} size={14} />
                <span>{meta.label}</span>
                <Badge variant="muted" size="sm">{count}</Badge>
              </Inline>
            </TabsTrigger>
          );
        })}
      </TabsList>

      {categories.map((cat) => {
        const meta = CATEGORY_META[cat] ?? {
          label: cat === "cdc" ? "CDC" : cat.charAt(0).toUpperCase() + cat.slice(1),
          icon: "diagnostics" as IconName,
          desc: "",
        };
        const grows = groups.get(cat) ?? [];
        return (
          <TabsContent key={cat} value={cat}>
            <div className="nsm-table-container">
              <div className="nsm-table-toolbar">
                <Inline gap="sm" align="center" justify="between" className="w-full">
                  <Inline gap="xs" align="center">
                    <Icon name={meta.icon} size={16} />
                    <Heading as="h4" size="sm" className="font-semibold text-foreground">
                      {meta.label}
                    </Heading>
                    {meta.desc ? (
                      <Text as="span" variant="muted" size="xs">
                        — {meta.desc}
                      </Text>
                    ) : null}
                  </Inline>
                  <Badge variant="muted" size="sm">{grows.length} metrics</Badge>
                </Inline>
              </div>
              <div className="nsm-result-table-scroll">
                <Table density="compact">
                  <TableHeader>
                    <TableRow>
                      <TableHead>Metric</TableHead>
                      <TableHead>Value</TableHead>
                      <TableHead>Raw</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {grows.map((r, i) => {
                      const name = String(r[cName] ?? "");
                      const raw = String(r[cVal] ?? "");
                      const unit = String(r[cUnit] ?? "");
                      return (
                        <TableRow key={i}>
                          <TableCell className="font-mono text-xs font-medium">{name}</TableCell>
                          <TableCell className="font-mono text-xs font-semibold">{pretty(raw, unit)}</TableCell>
                          <TableCell>
                            <Text as="span" variant="muted" size="xs" className="font-mono">
                              {raw}
                              {unit && unit !== "count" ? ` ${unit}` : ""}
                            </Text>
                          </TableCell>
                        </TableRow>
                      );
                    })}
                  </TableBody>
                </Table>
              </div>
            </div>
          </TabsContent>
        );
      })}
    </Tabs>
  );
}
