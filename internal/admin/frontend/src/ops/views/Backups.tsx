import { useMemo, useState } from "react";
import {
  Alert,
  Badge,
  Button,
  Card,
  CardBody,
  CardHeader,
  CardTitle,
  ConfirmDialog,
  CodeBlock,
  Inline,
  InlineCode,
  Input,
  Pagination,
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
import { Section } from "./Section";
import { Icon } from "../../shared/icons";

// Backups is the M5 view: system.backups (the verified backups in the node's
// configured backup_dir) plus BACKUP DATABASE and VERIFY BACKUP, both gated
// on the BACKUP privilege server-side. Restore/PITR is offline-only — a
// running server cannot restore into itself — so the view shows the exact
// CLI command instead of a button.
type Pending = { kind: "create" } | { kind: "verify"; name: string };

export function Backups({ onUnauthorized }: { onUnauthorized: () => void }) {
  const { data, error, loading, reload } = useReadModel(api.backups, onUnauthorized);

  const [pending, setPending] = useState<Pending | null>(null);
  // The confirmed action currently in flight. BACKUP DATABASE and VERIFY BACKUP
  // both run a full restore test server-side and can take a long time, so the
  // confirm dialog closes on confirm and the originating button — "Back up
  // now…", or the Verify button of that one row — carries the spinner and stays
  // disabled until the call settles. Keeping it on the row also identifies
  // *which* backup is being verified, which a modal spinner cannot.
  const [running, setRunning] = useState<Pending | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);
  const [actionResult, setActionResult] = useState<string | null>(null);

  async function run() {
    const job = pending;
    if (!job || running) return;
    setRunning(job);
    setPending(null);
    setActionError(null);
    setActionResult(null);
    try {
      const res = await api.backupAction(
        job.kind === "create" ? { op: "create" } : { op: "verify", name: job.name },
      );
      const row = res.rows?.[0] ?? [];
      const col = (n: string) => res.columns?.indexOf(n) ?? -1;
      if (job.kind === "create") {
        setActionResult(`Backup "${row[col("name")]}" created and restore-tested.`);
      } else {
        const ok = row[col("verified")] === "yes";
        setActionResult(
          ok
            ? `Backup "${job.name}" verified: hash chain intact and restore test passed.`
            : `Backup "${job.name}" FAILED verification: ${row[col("problem")] || "unknown"}`,
        );
      }
      reload();
    } catch (err) {
      if (err instanceof ApiError && err.status === 401) {
        onUnauthorized();
        return;
      }
      const raw = err instanceof Error ? err.message : String(err);
      setActionError(isBackupDirUnset(raw) ? BACKUP_DIR_UNSET_MESSAGE : raw);
    } finally {
      setRunning(null);
    }
  }

  const rows = backupRows(data?.backups);
  const backupDirUnset = data?.backup_state === "unset";
  const [search, setSearch] = useState("");
  const [page, setPage] = useState(1);
  const [pageSize] = useState(10);

  const filteredRows = useMemo(() => {
    let list = rows;
    if (search.trim()) {
      const q = search.trim().toLowerCase();
      list = list.filter(
        (b) =>
          b.name.toLowerCase().includes(q) ||
          b.databaseId.toLowerCase().includes(q) ||
          b.checkpointLsn.toLowerCase().includes(q)
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
          {backupDirUnset ? (
            <BackupDirSetup
              onApplied={(message) => {
                setActionResult(message);
                reload();
              }}
            />
          ) : null}

          {actionResult ? (
            <Alert variant="success" title="Success" dismissable onDismiss={() => setActionResult(null)}>
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
              <CardTitle as="h3">
                <Inline gap="xs" align="center" wrap={false}>
                  <Icon name="archive" size={16} />
                  Physical backups
                </Inline>
              </CardTitle>
            </CardHeader>
            <CardBody>
              <Stack gap="sm">
                <Text size="sm" variant="muted">
                  Physical backups write an immutable snapshot of all table files,
                  WAL segments, and the manifest to <InlineCode>backup_dir</InlineCode>.
                  Every backup is verified with block checksums before it is
                  recorded in <InlineCode>system.backups</InlineCode>.
                </Text>
                <Button
                  variant="primary"
                  size="sm"
                  icon={<Icon name="archive" size={14} />}
                  disabled={backupDirUnset || running !== null}
                  loading={running?.kind === "create"}
                  // RUI's Button hides its children (visibility:hidden) behind
                  // the spinner while loading, which would leave the control
                  // with no accessible name — name it explicitly for as long
                  // as that is the case.
                  aria-label={running?.kind === "create" ? "Backing up…" : undefined}
                  onClick={() => setPending({ kind: "create" })}
                >
                  {running?.kind === "create" ? "Backing up…" : "Back up now…"}
                </Button>
              </Stack>
            </CardBody>
          </Card>

          <Section title="Backups" icon="archive">
            {rows.length === 0 ? (
              <Text variant="muted" size="sm">
                {backupDirUnset
                  ? "No backups — this server has no backup_dir configured (see above)."
                  : "No backups yet on this server."}
              </Text>
            ) : (
              <div className="nsm-table-container">
                {rows.length > 5 ? (
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
                          placeholder="Search backups…"
                          className="nsm-table-search-input"
                          aria-label="Filter backups"
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
                      ) : null}
                    </Inline>
                  </div>
                ) : null}

                <div className="nsm-result-table-scroll" role="region" aria-label="Backups table" tabIndex={0}>
                  <Table density="compact" scrollAreaClassName="nsm-result-table-inner">
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
                      {paginatedRows.length === 0 ? (
                        <TableRow>
                          <TableCell colSpan={5} className="text-center py-6 text-muted-foreground">
                            No backups matching &quot;{search}&quot;
                          </TableCell>
                        </TableRow>
                      ) : (
                        paginatedRows.map((b) => (
                          <TableRow key={b.name}>
                            <TableCell className="font-mono text-xs">{b.name}</TableCell>
                            <TableCell><Text as="span" variant="muted">{b.createdAt}</Text></TableCell>
                            <TableCell><Text as="span" variant="muted">{b.databaseId}</Text></TableCell>
                            <TableCell className="font-mono text-xs">{b.checkpointLsn}</TableCell>
                            <TableCell>
                              <Button
                                size="sm"
                                variant="ghost"
                                icon={<Icon name="check" size={14} />}
                                disabled={running !== null}
                                loading={running?.kind === "verify" && running.name === b.name}
                                // Named explicitly while the spinner hides the
                                // label (see the create button above); the
                                // backup name also tells a screen-reader user
                                // which row is busy.
                                aria-label={
                                  running?.kind === "verify" && running.name === b.name
                                    ? `Verifying… ${b.name}`
                                    : undefined
                                }
                                onClick={() => setPending({ kind: "verify", name: b.name })}
                              >
                                {running?.kind === "verify" && running.name === b.name ? "Verifying…" : "Verify"}
                              </Button>
                            </TableCell>
                          </TableRow>
                        ))
                      )}
                    </TableBody>
                  </Table>
                </div>

                <div className="nsm-table-footer">
                  <Inline gap="md" align="center" justify="between" wrap>
                    <span className="text-xs text-muted-foreground">
                      {filteredRows.length === 0 ? (
                        "0 backups"
                      ) : (
                        <>
                          Showing <strong className="text-foreground font-semibold">{startIndex + 1}</strong> to{" "}
                          <strong className="text-foreground font-semibold">{endIndex}</strong> of{" "}
                          <strong className="text-foreground font-semibold">{filteredRows.length}</strong>
                          {rows.length !== filteredRows.length ? ` (filtered from ${rows.length})` : " backups"}
                        </>
                      )}
                    </span>

                    {filteredRows.length > 10 ? (
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

          <Card variant="bordered">
            <CardHeader>
              <CardTitle as="h3">
                <Inline gap="xs" align="center" wrap={false}>
                  <Icon name="download" size={16} />
                  Restore &amp; point-in-time recovery
                </Inline>
              </CardTitle>
            </CardHeader>
            <CardBody>
              <Stack gap="sm">
                <Inline gap="xs" align="center">
                  <Badge variant="warning">offline only</Badge>
                  <Text size="sm" variant="muted">
                    A running server cannot restore into itself. Stop
                    <InlineCode>nextsqld</InlineCode>, then run:
                  </Text>
                </Inline>
                <CodeBlock code={data.restore_hint} language="bash" />
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
        />
      ) : null}
    </ViewFrame>
  );
}

// The server returns nerr.Unavailable ("no backup directory configured on
// this server (set backup_dir)") when BACKUP DATABASE / VERIFY BACKUP run on
// a node with no backup_dir. Recognize it so the view can show remediation
// instead of the raw wire string — matched loosely because the driver wraps
// it ("nextsql unavailable: nextsql: <msg>").
function isBackupDirUnset(msg: string): boolean {
  return /no backup directory configured/i.test(msg);
}

const BACKUP_DIR_UNSET_MESSAGE =
  "This server has no backup_dir configured, so it cannot take or verify " +
  "backups. Set backup_dir to an absolute path in the Configuration view " +
  "(or in nextsql.conf) and restart nextsqld, then reload this view.";

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

// BackupDirSetup turns the "backup_dir is not set" dead end into something an
// operator can act on. The old copy told them to edit nextsql.conf and restart
// but not how, and hid the real precondition: SET CONFIG only persists when
// nextsqld was started from a configuration file (--config). A flag-only
// server cannot be configured from here at all, and saying so plainly beats
// letting the operator type a path and receive the engine's raw
// "nothing to persist" error.
//
// The write itself is the existing M8 config route — one SET CONFIG statement
// on the operator's own connection, requiring ADMIN ON CLUSTER server-side.
// Admin never touches nextsql.conf itself. The setting is persist-only: it
// takes effect when the node restarts, which the UI states before and after.
function BackupDirSetup({ onApplied }: { onApplied: (message: string) => void }) {
  const [open, setOpen] = useState(false);
  const [path, setPath] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [noConfigFile, setNoConfigFile] = useState(false);

  const apply = async () => {
    setError(null);
    setBusy(true);
    try {
      await api.configAction({ key: "backup_dir", value: path.trim(), reset: false });
      setOpen(false);
      setPath("");
      onApplied(
        `backup_dir written to nextsql.conf as ${path.trim()}. Restart nextsqld for it to take effect — backups stay unavailable until then.`,
      );
    } catch (err) {
      const message = err instanceof ApiError ? err.message : err instanceof Error ? err.message : String(err);
      // The engine's own precondition, surfaced as guidance rather than a raw
      // error string: a server started from flags has no file to write to.
      if (/not started from a configuration file|nothing to persist/i.test(message)) {
        setNoConfigFile(true);
      }
      setError(message);
    } finally {
      setBusy(false);
    }
  };

  return (
    <Alert variant="warning" title="backup_dir is not set">
      <Stack gap="sm">
        <Text size="sm">
          This node was started without a configured <InlineCode>backup_dir</InlineCode>,
          so physical backups cannot be created or listed. Set a directory the
          server process can write to; it is saved to the node's{" "}
          <InlineCode>nextsql.conf</InlineCode> and applies after a restart.
        </Text>

        {noConfigFile ? (
          <Alert variant="error" title="This server cannot be configured from here">
            <Stack gap="xs">
              <Text size="sm">
                <InlineCode>nextsqld</InlineCode> was started from command-line
                flags, not a configuration file, so there is nothing for{" "}
                <InlineCode>SET CONFIG</InlineCode> to persist. Restart it with{" "}
                <InlineCode>--config /path/to/nextsql.conf</InlineCode> (the file
                may be empty to start with), then set{" "}
                <InlineCode>backup_dir</InlineCode> here or in Configuration.
              </Text>
            </Stack>
          </Alert>
        ) : error ? (
          <Alert variant="error" title="Could not set backup_dir">{error}</Alert>
        ) : null}

        {open && !noConfigFile ? (
          <Stack gap="xs">
            <Input
              label="Backup directory"
              value={path}
              onChange={(e) => setPath(e.currentTarget.value)}
              placeholder="/var/lib/nextsql/backups"
              hint="An absolute path on the server host, writable by the nextsqld process."
            />
            <Inline gap="xs" align="center" wrap>
              <Button size="sm" variant="primary" disabled={busy || path.trim() === ""} onClick={() => void apply()}>
                {busy ? "Saving…" : "Save to nextsql.conf"}
              </Button>
              <Button size="sm" variant="ghost" disabled={busy} onClick={() => { setOpen(false); setError(null); }}>
                Cancel
              </Button>
            </Inline>
          </Stack>
        ) : !noConfigFile ? (
          <Inline gap="xs" align="center">
            <Button size="sm" variant="outline" onClick={() => setOpen(true)}>
              Set backup directory…
            </Button>
          </Inline>
        ) : null}
      </Stack>
    </Alert>
  );
}
