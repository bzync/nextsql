import { useState } from "react";
import {
  Alert,
  Badge,
  Button,
  ConfirmDialog,
  Heading,
  Inline,
  Input,
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

// Configuration is the M8 view: the running config.Config (system.config)
// with, per key, its value in the running process, its value in the node's
// on-disk nextsql.conf, and whether the two differ (restart_required).
//
// The editor issues SET CONFIG <key> = <value|DEFAULT>, which requires ADMIN
// ON CLUSTER server-side and persists to the node's nextsql.conf — the
// Manager never touches the file. Every write is persist-only: it takes
// effect on the next server restart, which is why every changed row shows a
// "restart required" badge until then. A server started without a config
// file has nothing to persist to and SET CONFIG fails there.
type PendingEdit = { key: string; value: string; reset: boolean };

export function Configuration({ onUnauthorized }: { onUnauthorized: () => void }) {
  const { data, error, loading, reload } = useReadModel(api.config, onUnauthorized);

  const [editKey, setEditKey] = useState<string | null>(null);
  const [draft, setDraft] = useState("");
  const [pending, setPending] = useState<PendingEdit | null>(null);
  const [busy, setBusy] = useState(false);
  const [actionError, setActionError] = useState<string | null>(null);
  const [actionResult, setActionResult] = useState<string | null>(null);

  async function run() {
    if (!pending) return;
    setBusy(true);
    setActionError(null);
    try {
      await api.configAction({
        key: pending.key,
        value: pending.reset ? undefined : pending.value,
        reset: pending.reset,
      });
      setActionResult(
        pending.reset
          ? `"${pending.key}" reset to its built-in default in nextsql.conf. Restart nextsqld to apply.`
          : `"${pending.key}" set to "${pending.value}" in nextsql.conf. Restart nextsqld to apply.`,
      );
      setPending(null);
      setEditKey(null);
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

  const rows = configRows(data?.config);

  return (
    <ViewFrame loading={loading} error={error} warnings={data?.warnings}>
      {data ? (
        <>
          <Alert variant="info" title="How edits apply">
            Edits are written to the node's <code>nextsql.conf</code> through the
            server (the Manager never touches the file) and take effect on the
            next <code>nextsqld</code> restart — every changed row stays marked
            "restart required" until then. A server started without a config
            file cannot persist edits.
          </Alert>
          {actionResult ? (
            <Alert variant="success" title="Saved" dismissable onDismiss={() => setActionResult(null)}>
              {actionResult}
            </Alert>
          ) : null}
          {actionError ? (
            <Alert variant="error" title="Save failed" dismissable onDismiss={() => setActionError(null)}>
              {actionError}
            </Alert>
          ) : null}

          <Stack gap="xs">
            <Heading as="h3" size="sm">Running configuration</Heading>
            {rows.length === 0 ? (
              <Text variant="muted" size="sm">
                No process-level configuration attached (embedded/CLI use).
              </Text>
            ) : (
              <Table density="compact">
                <TableHeader>
                  <TableRow>
                    <TableHead>Setting</TableHead>
                    <TableHead>Running value</TableHead>
                    <TableHead>File value</TableHead>
                    <TableHead>Status</TableHead>
                    <TableHead />
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {rows.map((row) => {
                    const editing = editKey === row.name;
                    const redacted = row.value === "[redacted]";
                    return (
                      <TableRow key={row.name}>
                        <TableCell>{row.name}</TableCell>
                        <TableCell>{row.value || <Text as="span" variant="muted">(default)</Text>}</TableCell>
                        <TableCell>
                          {row.fileValue || <Text as="span" variant="muted">(default)</Text>}
                        </TableCell>
                        <TableCell>
                          {row.restartRequired ? (
                            <Badge variant="warning">restart required</Badge>
                          ) : (
                            <Badge variant="muted">applied</Badge>
                          )}
                        </TableCell>
                        <TableCell>
                          {editing ? (
                            <Inline gap="xs" align="center" wrap>
                              <Input
                                value={draft}
                                onChange={(e) => setDraft(e.target.value)}
                                placeholder={redacted ? "new value" : row.value}
                                style={{ width: 180 }}
                              />
                              <Button
                                size="sm"
                                variant="primary"
                                disabled={draft.trim() === "" || draft === row.value}
                                onClick={() => setPending({ key: row.name, value: draft.trim(), reset: false })}
                              >
                                Save…
                              </Button>
                              <Button
                                size="sm"
                                variant="ghost"
                                onClick={() => setPending({ key: row.name, value: "", reset: true })}
                              >
                                Reset to default…
                              </Button>
                              <Button size="sm" variant="ghost" onClick={() => setEditKey(null)}>
                                Cancel
                              </Button>
                            </Inline>
                          ) : (
                            <Button
                              size="sm"
                              variant="ghost"
                              onClick={() => {
                                setEditKey(row.name);
                                setDraft(redacted ? "" : row.value);
                              }}
                            >
                              Edit
                            </Button>
                          )}
                        </TableCell>
                      </TableRow>
                    );
                  })}
                </TableBody>
              </Table>
            )}
          </Stack>
        </>
      ) : null}
      {pending ? (
        <ConfirmDialog
          open
          onClose={() => setPending(null)}
          onConfirm={run}
          title={
            pending.reset
              ? `Reset "${pending.key}" to its default?`
              : `Set "${pending.key}" to "${pending.value}"?`
          }
          description="This writes the node's nextsql.conf and takes effect only after nextsqld restarts. The Manager cannot restart the server."
          confirmLabel={pending.reset ? "Reset in nextsql.conf" : "Write to nextsql.conf"}
          loading={busy}
        />
      ) : null}
    </ViewFrame>
  );
}

type Row = { name: string; value: string; fileValue: string; restartRequired: boolean };

function configRows(rs: ResultSet | undefined): Row[] {
  if (!rs || !rs.columns || rs.columns.length === 0) return [];
  const ci = (n: string) => rs.columns.indexOf(n);
  const [cName, cVal, cFile, cRestart] = [ci("name"), ci("value"), ci("file_value"), ci("restart_required")];
  return (rs.rows ?? []).map((r) => ({
    name: String(r[cName] ?? ""),
    value: String(r[cVal] ?? ""),
    fileValue: String(r[cFile] ?? ""),
    restartRequired: String(r[cRestart] ?? "") === "yes",
  }));
}
