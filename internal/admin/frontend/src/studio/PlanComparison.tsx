import { Alert, Inline, Stack, Stat, Text } from "@bzync/rui";
import { ResultTable } from "../ops/ResultTable";
import type { ResultSet } from "../ops/api";
import {
  compareExplainPlans,
  type ExplainNode,
  type ExplainPlanSnapshot,
} from "./resultTools";

function pair(before: string, after: string): string {
  return before === after ? before : `${before} → ${after}`;
}

function estimate(node: ExplainNode | null): string {
  if (!node) return "—";
  return `${node.estRows ?? "?"} rows / cost ${node.estCost ?? "?"}`;
}

function measured(node: ExplainNode | null, analyzed: boolean): string {
  if (!node || !analyzed) return "unmeasured";
  return `${node.actRows ?? "?"} rows / ${node.time || "0ns"} / cpu ${node.cpu || "0ns"}`;
}

function resources(node: ExplainNode | null, analyzed: boolean): string {
  if (!node || !analyzed) return "unmeasured";
  return `mem ${node.memory} / disk ${node.disk} / cache ${node.cache} / spill ${node.spill} / workers ${node.workers}`;
}

function indexName(node: ExplainNode | null): string {
  return node?.index || "none";
}

// PlanComparison uses structural paths only. It never claims two differently
// positioned operators are semantically equivalent, and it preserves the
// server's raw time/resource units instead of inventing normalized metrics.
export function PlanComparison({
  baseline,
  current,
}: {
  baseline: ExplainPlanSnapshot;
  current: ExplainPlanSnapshot;
}) {
  const comparison = compareExplainPlans(baseline, current);
  const structuralChanges = comparison.filter((row) => row.change === "operator changed" || row.change === "added" || row.change === "removed").length;
  const metricChanges = comparison.filter((row) => row.change === "metrics changed").length;
  const result: ResultSet = {
    columns: ["path", "change", "operator", "estimate", "measured", "resources", "index"],
    rows: comparison.map((row) => [
      row.path,
      row.change,
      pair(row.baseline?.label ?? "—", row.current?.label ?? "—"),
      pair(estimate(row.baseline), estimate(row.current)),
      pair(measured(row.baseline, baseline.analyzed), measured(row.current, current.analyzed)),
      pair(resources(row.baseline, baseline.analyzed), resources(row.current, current.analyzed)),
      pair(indexName(row.baseline), indexName(row.current)),
    ]),
  };

  return (
    <Stack gap="md" className="nss-plan-comparison">
      <Alert variant="info" title="Deterministic structural comparison">
        Nodes are aligned by their 1-based tree path, not guessed semantic identity.
        Values show baseline → current only when they differ; measured fields remain
        “unmeasured” unless that snapshot came from EXPLAIN ANALYZE.
      </Alert>
      <Inline gap="md" wrap>
        <Stat label="Operators" value={`${baseline.nodeCount} → ${current.nodeCount}`} />
        <Stat label="Structural changes" value={String(structuralChanges)} />
        <Stat label="Metric-only changes" value={String(metricChanges)} />
        <Stat
          label="Measurement scope"
          value={baseline.analyzed && current.analyzed ? "Both analyzed" : baseline.analyzed || current.analyzed ? "Mixed" : "Estimates only"}
        />
      </Inline>
      <Text size="sm" variant="muted">
        Baseline captured {new Date(baseline.capturedAt).toLocaleString()}.
      </Text>
      <ResultTable result={result} label="Plan comparison by structural path" />
    </Stack>
  );
}
