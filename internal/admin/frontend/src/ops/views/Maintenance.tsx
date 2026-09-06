import { useState } from "react";
import {
  Alert,
  Button,
  Card,
  CardBody,
  CardHeader,
  CardTitle,
  Checkbox,
  ConfirmDialog,
  Heading,
  Input,
  Select,
  Stack,
  Text,
} from "@bzync/rui";
import { api, ApiError, type MaintainScope, type ResultSet } from "../api";
import { useReadModel } from "../useReadModel";
import { ResultTable } from "../ResultTable";
import { ViewFrame } from "./ViewFrame";

type PendingAction =
  | { kind: "analyze"; target: string }
  | { kind: "rebuild_index"; target: string; online: boolean }
  | { kind: "maintain"; scope: MaintainScope; target: string };

function describeResult(action: PendingAction, res: ResultSet): string {
  const n = res.affected ?? 0;
  switch (action.kind) {
    case "analyze":
      return action.target
        ? `Analyzed "${action.target}".`
        : `Analyzed ${n} table${n === 1 ? "" : "s"}.`;
    case "rebuild_index":
      return `Rebuilt index "${action.target}"${action.online ? " (online)" : ""}.`;
    case "maintain":
      return `Reclaimed ${n} dead version${n === 1 ? "" : "s"}.`;
  }
}

function confirmCopy(action: PendingAction): { title: string; description: string; confirmLabel: string } {
  switch (action.kind) {
    case "analyze":
      return {
        title: action.target ? `Analyze "${action.target}"?` : "Analyze every table?",
        description: "Recomputes and persists cost-estimator statistics. Read-only otherwise; safe to run anytime.",
        confirmLabel: "Analyze",
      };
    case "rebuild_index":
      return {
        title: `Rebuild index "${action.target}"?`,
        description: action.online
          ? "Builds a fresh copy of this index without blocking concurrent writes, then swaps it in atomically. Not available for partitioned tables, vector, or full-text indexes."
          : "Performs a blocking rebuild from the current transaction snapshot, then swaps it in atomically. Concurrent statements on this table wait until it completes.",
        confirmLabel: "Rebuild index",
      };
    case "maintain":
      return {
        title:
          action.scope === "database"
            ? "Run maintenance on the whole database?"
            : `Run maintenance on ${action.scope} "${action.target}"?`,
        description:
          "A leader-only, blocking pass that reclaims physical tombstones (capped at 10,000 per statement). Cannot run inside a transaction.",
        confirmLabel: "Run maintenance",
      };
  }
}

// Maintenance is the M7 view: system.tables/indexes/table_stats/index_stats
// plus the three maintenance statements (ANALYZE, REBUILD INDEX, MAINTAIN)
// — all already gated server-side (SELECT/INDEX/ADMIN ON CLUSTER
// respectively), so this view adds only a confirmation step.
export function Maintenance({ onUnauthorized }: { onUnauthorized: () => void }) {
  const { data, error, loading, reload } = useReadModel(api.maintenance, onUnauthorized);

  const [analyzeTarget, setAnalyzeTarget] = useState("");
  const [rebuildTarget, setRebuildTarget] = useState("");
  const [rebuildOnline, setRebuildOnline] = useState(false);
  const [maintainScope, setMaintainScope] = useState<MaintainScope>("database");
  const [maintainTarget, setMaintainTarget] = useState("");

  const [pending, setPending] = useState<PendingAction | null>(null);
  const [busy, setBusy] = useState(false);
  const [actionError, setActionError] = useState<string | null>(null);
  const [actionResult, setActionResult] = useState<string | null>(null);

  async function run() {
    if (!pending) return;
    setBusy(true);
    setActionError(null);
    try {
      const res = await api.maintenanceAction(
        pending.kind === "analyze"
          ? { op: "analyze", target: pending.target || undefined }
          : pending.kind === "rebuild_index"
            ? { op: "rebuild_index", target: pending.target, online: pending.online }
            : { op: "maintain", scope: pending.scope, target: pending.scope === "database" ? undefined : pending.target },
      );
      setActionResult(describeResult(pending, res));
      setPending(null);
      reload();
    } catch (err) {
      if (err instanceof ApiError && err.status === 401) {
        onUnauthorized();
        return;
      }
      setActionError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  }

  const copy = pending ? confirmCopy(pending) : null;

  return (
    <ViewFrame loading={loading} error={error} warnings={data?.warnings}>
      {data ? (
        <>
          {actionResult ? <Alert variant="success" title="Action acknowledged">{actionResult}</Alert> : null}
          {actionError ? <Alert variant="error" title="Action failed">{actionError}</Alert> : null}

          <Card variant="bordered">
            <CardHeader>
              <CardTitle as="h3">Statistics — ANALYZE</CardTitle>
            </CardHeader>
            <CardBody>
              <Stack gap="sm">
                <Text variant="muted" size="sm">
                  Leave the table name blank to analyze every table.
                </Text>
                <Stack gap="xs" style={{ maxWidth: 320 }}>
                  <Input
                    placeholder="table name (optional)"
                    value={analyzeTarget}
                    onChange={(e) => setAnalyzeTarget(e.target.value)}
                  />
                  <Button
                    variant="outline"
                    onClick={() => setPending({ kind: "analyze", target: analyzeTarget.trim() })}
                  >
                    Analyze…
                  </Button>
                </Stack>
              </Stack>
            </CardBody>
          </Card>

          <Card variant="bordered">
            <CardHeader>
              <CardTitle as="h3">Rebuild index</CardTitle>
            </CardHeader>
            <CardBody>
              <Stack gap="sm">
                <Stack gap="xs" style={{ maxWidth: 320 }}>
                  <Input
                    placeholder="index name"
                    value={rebuildTarget}
                    onChange={(e) => setRebuildTarget(e.target.value)}
                  />
                  <Checkbox
                    label="ONLINE (not available for partitioned/vector/full-text indexes)"
                    checked={rebuildOnline}
                    onChange={(e) => setRebuildOnline(e.target.checked)}
                  />
                  <Button
                    variant="outline"
                    disabled={!rebuildTarget.trim()}
                    onClick={() => setPending({ kind: "rebuild_index", target: rebuildTarget.trim(), online: rebuildOnline })}
                  >
                    Rebuild…
                  </Button>
                </Stack>
              </Stack>
            </CardBody>
          </Card>

          <Card variant="bordered">
            <CardHeader>
              <CardTitle as="h3">Storage reclamation — MAINTAIN</CardTitle>
            </CardHeader>
            <CardBody>
              <Stack gap="sm">
                <Text variant="muted" size="sm">
                  Reclaims physical tombstones left behind by deletes/updates, capped at 10,000 per
                  statement. Requires cluster ADMIN and runs on the leader.
                </Text>
                <Stack gap="xs" style={{ maxWidth: 320 }}>
                  <Select
                    value={maintainScope}
                    onChange={(v) => setMaintainScope(v as MaintainScope)}
                    options={[
                      { value: "database", label: "Whole database" },
                      { value: "table", label: "One table" },
                      { value: "index", label: "One index" },
                    ]}
                  />
                  {maintainScope !== "database" ? (
                    <Input
                      placeholder={maintainScope === "table" ? "table name" : "index name"}
                      value={maintainTarget}
                      onChange={(e) => setMaintainTarget(e.target.value)}
                    />
                  ) : null}
                  <Button
                    variant="outline"
                    disabled={maintainScope !== "database" && !maintainTarget.trim()}
                    onClick={() =>
                      setPending({ kind: "maintain", scope: maintainScope, target: maintainTarget.trim() })
                    }
                  >
                    Run maintenance…
                  </Button>
                </Stack>
              </Stack>
            </CardBody>
          </Card>

          <Stack gap="xs">
            <Heading as="h3" size="sm">Tables</Heading>
            <ResultTable result={data.tables} empty="No user tables" />
          </Stack>
          <Stack gap="xs">
            <Heading as="h3" size="sm">Table statistics</Heading>
            <ResultTable result={data.table_stats} empty="No statistics collected yet" />
          </Stack>
          <Stack gap="xs">
            <Heading as="h3" size="sm">Indexes</Heading>
            <ResultTable result={data.indexes} empty="No indexes" />
          </Stack>
          <Stack gap="xs">
            <Heading as="h3" size="sm">Index statistics</Heading>
            <ResultTable result={data.index_stats} empty="No statistics collected yet" />
          </Stack>
        </>
      ) : null}
      {copy ? (
        <ConfirmDialog
          open
          onClose={() => setPending(null)}
          onConfirm={run}
          title={copy.title}
          description={copy.description}
          confirmLabel={copy.confirmLabel}
          loading={busy}
        />
      ) : null}
    </ViewFrame>
  );
}
