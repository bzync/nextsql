import { Alert, Badge, Button, Heading, Inline, Stack, Table, TableBody, TableCell, TableHead, TableHeader, TableRow, Text } from "@bzync/rui";
import { api } from "../api";
import { useReadModel } from "../useReadModel";
import { ViewFrame } from "./ViewFrame";
import type { ResultSet } from "../api";

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
              <a href="/api/v1/diagnostics/bundle" download>Download diagnostic bundle</a>
            </Button>
            <Text as="span" variant="muted" size="sm">
              A single redacted JSON document (metrics, config, cluster, TLS/key
              status, capabilities, audit-chain status, server-log tail). No key
              material, no tenant data.
            </Text>
          </Inline>
          <Stack gap="xs">
            <Heading as="h3" size="sm">Server log</Heading>
            <ServerLogPanel log={data.server_log} />
          </Stack>
          <Heading as="h3" size="sm">Metrics</Heading>
          <MetricsPanel metrics={data.metrics} />
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
  if (cols.length === 0 || rows.length === 0) {
    return (
      <Alert variant="info" title="No server log available">
        The connected node has no in-memory log ring attached (embedded/CLI
        use), or nothing has been logged yet.
      </Alert>
    );
  }
  const ci = (name: string) => cols.indexOf(name);
  const [cTime, cLevel, cMsg, cAttrs] = [ci("event_time"), ci("level"), ci("message"), ci("attributes")];
  return (
    <Table density="compact">
      <TableHeader>
        <TableRow>
          <TableHead>Time</TableHead>
          <TableHead>Level</TableHead>
          <TableHead>Message</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {rows.map((r, i) => {
          const level = String(r[cLevel] ?? "");
          const attrs = String(r[cAttrs] ?? "");
          return (
            <TableRow key={i}>
              <TableCell>
                <Text as="span" variant="muted">{String(r[cTime] ?? "")}</Text>
              </TableCell>
              <TableCell>
                <Badge variant={levelVariant(level)}>{level || "?"}</Badge>
              </TableCell>
              <TableCell>
                <Inline gap="xs" wrap>
                  <span>{String(r[cMsg] ?? "")}</span>
                  {attrs ? <Text as="span" variant="muted">{attrs}</Text> : null}
                </Inline>
              </TableCell>
            </TableRow>
          );
        })}
      </TableBody>
    </Table>
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
    const cat = String(r[cCat] ?? "other");
    if (!groups.has(cat)) groups.set(cat, []);
    groups.get(cat)!.push(r);
  }
  return (
    <Stack gap="lg">
      {[...groups.entries()].map(([cat, grows]) => (
        <Stack gap="xs" key={cat}>
          <Heading as="h3" size="sm" style={{ textTransform: "capitalize" }}>
            {cat}
          </Heading>
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
                    <TableCell>{name}</TableCell>
                    <TableCell>{pretty(raw, unit)}</TableCell>
                    <TableCell>
                      <Text as="span" variant="muted">
                        {raw}
                        {unit && unit !== "count" ? ` ${unit}` : ""}
                      </Text>
                    </TableCell>
                  </TableRow>
                );
              })}
            </TableBody>
          </Table>
        </Stack>
      ))}
    </Stack>
  );
}
