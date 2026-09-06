import { useState } from "react";
import {
  Alert,
  Badge,
  Button,
  Card,
  CardBody,
  CardHeader,
  CardTitle,
  ConfirmDialog,
  Heading,
  Inline,
  NumberInput,
  Stack,
  Text,
} from "@bzync/rui";
import { api, ApiError, type ClusterAction } from "../api";
import { useReadModel } from "../useReadModel";
import { ResultTable } from "../ResultTable";
import { ViewFrame } from "./ViewFrame";

type ActionCopy = {
  title: string;
  description: string;
  confirmLabel: string;
  destructive: boolean;
};

const ACTION_COPY: Record<ClusterAction, ActionCopy> = {
  transfer_leader: {
    title: "Transfer leadership?",
    description:
      "Asks this Raft cluster's current leader to hand off to another voter (CLUSTER TRANSFER LEADER). Requires a healthy quorum; fails if this node has no cluster attached.",
    confirmLabel: "Transfer leadership",
    destructive: true,
  },
  drain: {
    title: "Drain and stop this node?",
    description:
      "Stops accepting new connections immediately, closes idle ones, waits for busy ones (0 = the node's configured default), force-closes what remains, then the nextsqld process itself exits — the same as a planned restart, minus the restart. It will not come back on its own; someone must start it again outside the Manager. Safe on a follower in a cluster (the rest keep serving); on a standalone node this takes the database offline.",
    confirmLabel: "Drain and stop nextsqld",
    destructive: true,
  },
  maintenance_enable: {
    title: "Enable maintenance mode?",
    description:
      "Every subsequent mutating statement on this node fails Unavailable until disabled. Node-local — a leader failover does not carry it to the new leader.",
    confirmLabel: "Enable maintenance",
    destructive: false,
  },
  maintenance_disable: {
    title: "Disable maintenance mode?",
    description: "Resumes accepting mutating statements on this node.",
    confirmLabel: "Disable maintenance",
    destructive: false,
  },
  reconcile_confirm: {
    title: "Confirm replication reconciled?",
    description:
      "Clears this node's own replication-suspect flag after you've manually verified its data. Only do this once you've confirmed the node is caught up — it unblocks STRONG reads on this node.",
    confirmLabel: "Confirm reconciled",
    destructive: false,
  },
};

// Cluster is the M6 Cluster view: system.replication + system.replica_health
// (both live, always-visible tables), plus the CLUSTER admin actions —
// TRANSFER LEADER / DRAIN / MAINTENANCE ENABLE|DISABLE / RECONCILE CONFIRM.
// Every action already requires ADMIN ON CLUSTER server-side; the Manager
// adds only a confirmation step before issuing the exact documented SQL.
export function Cluster({ onUnauthorized }: { onUnauthorized: () => void }) {
  const { data, error, loading, reload } = useReadModel(api.cluster, onUnauthorized);
  const [pending, setPending] = useState<ClusterAction | null>(null);
  const [drainTimeoutMs, setDrainTimeoutMs] = useState(0);
  const [busy, setBusy] = useState(false);
  const [actionError, setActionError] = useState<string | null>(null);
  const [actionResult, setActionResult] = useState<string | null>(null);

  async function runAction(action: ClusterAction) {
    setBusy(true);
    setActionError(null);
    try {
      const res = await api.clusterAction(action, action === "drain" ? drainTimeoutMs : undefined);
      const result = res.rows[0]?.[0];
      setActionResult(result ?? "done");
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

  const copy = pending ? ACTION_COPY[pending] : null;

  return (
    <ViewFrame loading={loading} error={error} warnings={data?.warnings}>
      {data ? (
        <>
          <Inline gap="sm" align="center">
            <Badge variant={data.clustered ? "info" : "muted"}>
              {data.clustered ? "Clustered" : "Standalone"}
            </Badge>
          </Inline>
          {actionResult ? (
            <Alert variant="success" title="Action acknowledged">{actionResult}</Alert>
          ) : null}
          {actionError ? (
            <Alert variant="error" title="Action failed">{actionError}</Alert>
          ) : null}
          <Card variant="bordered">
            <CardHeader>
              <CardTitle as="h3">Cluster actions</CardTitle>
            </CardHeader>
            <CardBody>
              <Stack gap="sm">
                <Text variant="muted" size="sm">
                  Each action requires cluster ADMIN and runs on the node this session is
                  connected to. Confirm before running.
                </Text>
                <Inline gap="sm" wrap align="center">
                  <Inline gap="xs" align="center">
                    <NumberInput
                      aria-label="Drain timeout (ms), 0 = node default"
                      min={0}
                      max={86400000}
                      value={drainTimeoutMs}
                      onChange={setDrainTimeoutMs}
                      style={{ width: 140 }}
                    />
                    <Button variant="outline" onClick={() => setPending("drain")}>Drain node…</Button>
                  </Inline>
                  <Button variant="outline" onClick={() => setPending("transfer_leader")}>
                    Transfer leadership…
                  </Button>
                  <Button variant="outline" onClick={() => setPending("maintenance_enable")}>
                    Enable maintenance…
                  </Button>
                  <Button variant="outline" onClick={() => setPending("maintenance_disable")}>
                    Disable maintenance…
                  </Button>
                  <Button variant="outline" onClick={() => setPending("reconcile_confirm")}>
                    Confirm reconciled…
                  </Button>
                </Inline>
              </Stack>
            </CardBody>
          </Card>
          <Stack gap="xs">
            <Heading as="h3" size="sm">Replication</Heading>
            <ResultTable result={data.replication} />
          </Stack>
          <Stack gap="xs">
            <Heading as="h3" size="sm">Replica health</Heading>
            <ResultTable result={data.replica_health} empty="No replica health rows (standalone node)" />
          </Stack>
        </>
      ) : null}
      {copy ? (
        <ConfirmDialog
          open
          onClose={() => setPending(null)}
          onConfirm={() => pending && runAction(pending)}
          title={copy.title}
          description={copy.description}
          confirmLabel={copy.confirmLabel}
          destructive={copy.destructive}
          loading={busy}
        />
      ) : null}
    </ViewFrame>
  );
}
