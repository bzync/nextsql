import { Alert, Badge, Inline, Stack, Stat, Text } from "@bzync/rui";
import { ResultTable } from "../ops/ResultTable";
import type { ResultSet } from "../ops/api";
import type { ExplainProfile, ExplainProfileRow } from "./resultTools";

function estimateSignal(row: ExplainProfileRow): string {
  if (row.estimateFactor === null || row.estimateDirection === "unknown") return "unavailable";
  if (row.estimateDirection === "match") return "matches";
  const factor = Number.isFinite(row.estimateFactor) ? `${row.estimateFactor.toFixed(1)}×` : "∞";
  return `${factor} ${row.estimateDirection}`;
}

function operatorSignal(row: ExplainProfileRow | null): string {
  if (!row) return "No non-zero child timing";
  return `${row.path} · ${row.node.label} · ${row.node.time}`;
}

function estimateHotspot(row: ExplainProfileRow | null): string {
  if (!row) return "No ≥3× miss";
  return `${row.path} · ${row.node.label} · ${estimateSignal(row)}`;
}

// ExplainProfiler presents a bounded interpretation of server-authored
// EXPLAIN ANALYZE fields. It does not sum timings/resources or derive
// percentages because operator measurements may include child work and the
// root can carry query-level budget counters.
export function ExplainProfiler({ profile }: { profile: ExplainProfile }) {
  const result: ResultSet = {
    columns: [
      "path", "signal", "operator", "estimated rows", "actual rows",
      "estimate error", "time", "cpu", "memory", "disk", "cache",
      "spill", "workers", "index",
    ],
    rows: profile.rows.map((row) => [
      row.path,
      row.estimateSeverity,
      row.node.label,
      row.node.estRows === null ? null : String(row.node.estRows),
      row.node.actRows === null ? null : String(row.node.actRows),
      estimateSignal(row),
      row.node.time,
      row.node.cpu,
      String(row.node.memory),
      String(row.node.disk),
      String(row.node.cache),
      String(row.node.spill),
      String(row.node.workers),
      row.node.index || "none",
    ]),
  };

  return (
    <Stack gap="md" className="nss-explain-profiler">
      <Alert variant="info" title="Server-reported EXPLAIN ANALYZE fields">
        Timings can include child work, CPU can mirror elapsed time, root
        resources can describe the query budget, and a zero disk/cache counter
        can mean no operator attribution. Studio therefore shows reported
        maxima and never sums operators or invents percentages.
      </Alert>
      <Inline gap="md" wrap>
        <Stat label="Operators" value={String(profile.nodeCount)} />
        <Stat label="Root reported time" value={profile.root.node.time || "0ns"} />
        <Stat label="Longest reported child" value={operatorSignal(profile.slowestNonRoot)} />
        <Stat label="Largest estimate miss" value={estimateHotspot(profile.worstEstimate)} />
        <Stat label="Peak reported memory" value={`${profile.peakMemory.node.memory.toLocaleString()} bytes`} />
        <Stat label="Peak reported spill" value={`${profile.peakSpill.node.spill.toLocaleString()} bytes`} />
        <Stat label="Maximum workers" value={String(profile.maxWorkers.node.workers)} />
      </Inline>
      <Inline gap="xs" align="center" wrap>
        <Badge variant="success">ANALYZE result</Badge>
        <Text size="sm" variant="muted">
          Structural paths preserve the server plan order; warning/error signals
          mean actual rows differ from estimates by at least 3×/10×.
        </Text>
      </Inline>
      <ResultTable result={result} label="EXPLAIN ANALYZE operator profile" />
    </Stack>
  );
}
