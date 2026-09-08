import { useMemo, useState } from "react";
import {
  Alert,
  Badge,
  Button,
  ConfirmDialog,
  Inline,
  InlineCode,
  Input,
  Pagination,
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
import { Section } from "./Section";
import { Icon } from "../../shared/icons";

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

  const [search, setSearch] = useState("");
  const [page, setPage] = useState(1);
  const [pageSize] = useState(12);

  const rows = configRows(data?.config);

  const filteredRows = useMemo(() => {
    let list = rows;
    if (search.trim()) {
      const q = search.trim().toLowerCase();
      list = list.filter(
        (r) =>
          r.name.toLowerCase().includes(q) ||
          r.value.toLowerCase().includes(q) ||
          r.fileValue.toLowerCase().includes(q)
      );
    }
    return list;
  }, [rows, search]);

  const totalPages = Math.max(1, Math.ceil(filteredRows.length / pageSize));
  const safePage = Math.min(page, totalPages);
  const startIndex = (safePage - 1) * pageSize;
  const endIndex = Math.min(startIndex + pageSize, filteredRows.length);
  const paginatedRows = filteredRows.slice(startIndex, endIndex);

  return (
    <ViewFrame loading={loading} error={error} warnings={data?.warnings}>
      {data ? (
        <>
          <Alert variant="info" title="How edits apply">
            Edits are written to the node's <InlineCode>nextsql.conf</InlineCode> through the
            server (the Manager never touches the file) and take effect on the
            next <InlineCode>nextsqld</InlineCode> restart — every changed row stays marked
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

          <Section title="Running configuration" icon="sliders">
            {rows.length === 0 ? (
              <Text variant="muted" size="sm">
                No process-level configuration attached (embedded/CLI use).
              </Text>
            ) : (
              <div className="nsm-table-container">
                <div className="nsm-table-toolbar">
                  <Inline gap="sm" align="center" justify="between" wrap>
                    <div className="nsm-table-search">
                      <Icon name="search" size={13} className="nsm-table-search-icon" />
                      <input
                        type="search"
                        value={search}
                        onChange={(e) => {
                          setSearch(e.target.value);
                          setPage(1);
                        }}
                        placeholder="Search settings…"
                        className="nsm-table-search-input"
                        aria-label="Filter configuration settings"
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
                    {search ? (
                      <Badge variant="muted" size="sm">
                        {filteredRows.length} of {rows.length} matched
                      </Badge>
                    ) : (
                      <span className="text-xs text-muted-foreground">{rows.length} total settings</span>
                    )}
                  </Inline>
                </div>

                <div className="nsm-result-table-scroll" role="region" aria-label="Running configuration table" tabIndex={0}>
                  <Table density="compact" scrollAreaClassName="nsm-result-table-inner">
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
                      {paginatedRows.length === 0 ? (
                        <TableRow>
                          <TableCell colSpan={5} className="text-center py-6 text-muted-foreground">
                            No settings matching &quot;{search}&quot;
                          </TableCell>
                        </TableRow>
                      ) : (
                        paginatedRows.map((row) => {
                          const editing = editKey === row.name;
                          const redacted = row.value === "[redacted]";
                          return (
                            <TableRow key={row.name}>
                              <TableCell className="font-mono text-xs">{row.name}</TableCell>
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
                                    icon={<Icon name="sliders" size={14} />}
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
                        })
                      )}
                    </TableBody>
                  </Table>
                </div>

                <div className="nsm-table-footer">
                  <Inline gap="md" align="center" justify="between" wrap>
                    <span className="text-xs text-muted-foreground">
                      {filteredRows.length === 0 ? (
                        "0 settings"
                      ) : (
                        <>
                          Showing <strong className="text-foreground font-semibold">{startIndex + 1}</strong> to{" "}
                          <strong className="text-foreground font-semibold">{endIndex}</strong> of{" "}
                          <strong className="text-foreground font-semibold">{filteredRows.length}</strong>
                          {rows.length !== filteredRows.length ? ` (filtered from ${rows.length})` : " settings"}
                        </>
                      )}
                    </span>

                    {filteredRows.length > 12 ? (
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
            )}
          </Section>
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
