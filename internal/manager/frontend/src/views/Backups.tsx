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
  Stack,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
  Text,
} from "@bzync/rui";
import { api, ApiError, type ResultSet } from "../api";
import { useReadModel } from "../useReadModel";
import { ViewFrame } from "./ViewFrame";

// Backups is the M5 view: system.backups (the verified backups in the node's
// configured backup_dir) plus BACKUP DATABASE and VERIFY BACKUP, both gated
// on the BACKUP privilege server-side. Restore/PITR is offline-only — a
// running server cannot restore into itself — so the view shows the exact
// CLI command instead of a button.
type Pending = { kind: "create" } | { kind: "verify"; name: string };

export function Backups({ onUnauthorized }: { onUnauthorized: () => void }) {
  const { data, error, loading, reload } = useReadModel(api.backups, onUnauthorized);

  const [pending, setPending] = useState<Pending | null>(null);
  const [busy, setBusy] = useState(false);
  const [actionError, setActionError] = useState<string | null>(null);
  const [actionResult, setActionResult] = useState<string | null>(null);

  async function run() {
    if (!pending) return;
    setBusy(true);
    setActionError(null);
    try {
      const res = await api.backupAction(
        pending.kind === "create" ? { op: "create" } : { op: "verify", name: pending.name },
      );
      const row = res.rows?.[0] ?? [];
      const col = (n: string) => res.columns?.indexOf(n) ?? -1;
      if (pending.kind === "create") {
        setActionResult(`Backup "${row[col("name")]}" created and restore-tested.`);
      } else {
        const ok = row[col("verified")] === "yes";
        setActionResult(
          ok
            ? `Backup "${pending.name}" verified: hash chain intact and restore test passed.`
            : `Backup "${pending.name}" FAILED verification: ${row[col("problem")] || "unknown"}`,
        );
      }
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

  const rows = backupRows(data?.backups);

  return (
    <ViewFrame loading={loading} error={error} warnings={data?.warnings}>
      {data ? (
        <>
          <Alert variant="info" title="Scope">
            Backups are written to the server's configured <code>backup_dir</code>
            {" "}and are encrypted, integrity-checked, and restore-tested before
            they are published. Creating a backup does not stop the server.
          </Alert>
          {actionResult ? (
            <Alert variant="success" title="Done" dismissable onDismiss={() => setActionResult(null)}>
              {actionResult}
            </Alert>
          ) : null}
          {actionError ? (
            <Alert variant="error" title="Action failed" dismissable onDismiss={() => setActionError(null)}>
              {actionError}
            </Alert>
          ) : null}

          <Card variant="bordered">
            <CardHeader>
              <CardTitle as="h3">Create a backup</CardTitle>
            </CardHeader>
            <CardBody>
              <Stack gap="sm">
                <Text variant="muted" size="sm">
                  Writes a new verified backup into a timestamped subdirectory of
                  the server's <code>backup_dir</code>. May take a while for a
                  large database.
                </Text>
                <Button variant="primary" onClick={() => setPending({ kind: "create" })}>
                  Back up now…
                </Button>
              </Stack>
            </CardBody>
          </Card>

          <Stack gap="xs">
            <Heading as="h3" size="sm">Backups</Heading>
            {rows.length === 0 ? (
              <Text variant="muted" size="sm">
                No backups (or no <code>backup_dir</code> configured on the
                server).
              </Text>
            ) : (
              <Table density="compact">
                <TableHeader>
                  <TableRow>
                    <TableHead>Name</TableHead>
                    <TableHead>Created</TableHead>
                    <TableHead>Database</TableHead>
                    <TableHead>Checkpoint LSN</TableHead>
                    <TableHead />
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {rows.map((b) => (
                    <TableRow key={b.name}>
                      <TableCell>{b.name}</TableCell>
                      <TableCell><Text as="span" variant="muted">{b.createdAt}</Text></TableCell>
                      <TableCell><Text as="span" variant="muted">{b.databaseId}</Text></TableCell>
                      <TableCell>{b.checkpointLsn}</TableCell>
                      <TableCell>
                        <Button size="sm" variant="ghost" onClick={() => setPending({ kind: "verify", name: b.name })}>
                          Verify
                        </Button>
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            )}
          </Stack>

          <Card variant="bordered">
            <CardHeader>
              <CardTitle as="h3">Restore &amp; point-in-time recovery</CardTitle>
            </CardHeader>
            <CardBody>
              <Stack gap="sm">
                <Inline gap="xs" align="center">
                  <Badge variant="warning">offline only</Badge>
                  <Text size="sm" variant="muted">
                    A running server cannot restore into itself. Stop
                    <code> nextsqld</code>, then run:
                  </Text>
                </Inline>
                <pre style={{ whiteSpace: "pre-wrap", fontSize: 12, margin: 0 }}>
                  {data.restore_hint}
                </pre>
              </Stack>
            </CardBody>
          </Card>
        </>
      ) : null}
      {pending ? (
        <ConfirmDialog
          open
          onClose={() => setPending(null)}
          onConfirm={run}
          title={pending.kind === "create" ? "Create a backup now?" : `Verify backup "${pending.name}"?`}
          description={
            pending.kind === "create"
              ? "Writes an encrypted, restore-tested backup into the server's backup_dir. The server keeps serving during the backup."
              : "Runs hash verification plus a restore test into a temporary directory. Does not touch the live database."
          }
          confirmLabel={pending.kind === "create" ? "Back up" : "Verify"}
          loading={busy}
        />
      ) : null}
    </ViewFrame>
  );
}

type Row = { name: string; createdAt: string; databaseId: string; checkpointLsn: string };

function backupRows(rs: ResultSet | undefined): Row[] {
  if (!rs || !rs.columns || rs.columns.length === 0) return [];
  const ci = (n: string) => rs.columns.indexOf(n);
  const [cName, cCreated, cDb, cCp] = [ci("name"), ci("created_at"), ci("database_id"), ci("checkpoint_lsn")];
  return (rs.rows ?? []).map((r) => ({
    name: String(r[cName] ?? ""),
    createdAt: String(r[cCreated] ?? ""),
    databaseId: String(r[cDb] ?? ""),
    checkpointLsn: String(r[cCp] ?? ""),
  }));
}
