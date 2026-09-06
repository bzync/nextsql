import { useMemo } from "react";
import {
  Alert,
  Button,
  Inline,
  List,
  ListItem,
  Modal,
  ModalBody,
  ModalFooter,
  ModalHeader,
  ModalTitle,
  Spinner,
  Stack,
  Stat,
  Text,
} from "@bzync/rui";
import type { StudioMigrationHistory } from "../ops/api";
import { ResultTable } from "../ops/ResultTable";

// Schema-migration history explorer (Developer operations): a read-only view of
// the database's reserved nsql_schema_migrations table, fetched through the
// logged-in operator's own NSQL connection (GET /api/v1/studio/migrations).
// The same SELECT any editor statement could run — the table's own SELECT
// privilege is the sole authority — so a non-privileged or migration-less
// database yields present=false, not an error.
//
// Read-only by design: authoring, validating, dry-running and applying
// migrations stays with the `nextsql migrate` CLI, which needs the local
// migration files that this protocol-only Admin client never holds. This panel
// only reports what the official migration system has already recorded.
export function MigrationExplorer({
  onClose,
  data,
  loading,
  error,
  onRefresh,
}: {
  onClose: () => void;
  data: StudioMigrationHistory | null;
  loading: boolean;
  error: string | null;
  onRefresh: () => void;
}) {
  const cols = data?.history.columns ?? [];
  const rows = data?.history.rows ?? [];
  const versionIdx = cols.indexOf("version");
  const dirtyIdx = cols.indexOf("dirty");
  const directionIdx = cols.indexOf("direction");

  const summary = useMemo(() => {
    if (!data || !data.present) return null;
    const isDirty = (cell: string | null) =>
      typeof cell === "string" && cell.trim() !== "" && cell.trim() !== "0";
    const dirtyRows =
      dirtyIdx < 0 ? [] : rows.filter((row) => isDirty(row[dirtyIdx]));
    const last = rows.length > 0 ? rows[rows.length - 1] : null;
    return {
      count: rows.length,
      current:
        last && versionIdx >= 0 && typeof last[versionIdx] === "string"
          ? (last[versionIdx] as string)
          : null,
      dirtyVersions: dirtyRows
        .map((row) => (versionIdx >= 0 ? row[versionIdx] : null))
        .filter((v): v is string => typeof v === "string"),
      lastDirection:
        last && directionIdx >= 0 && typeof last[directionIdx] === "string"
          ? (last[directionIdx] as string)
          : null,
    };
  }, [data, rows, versionIdx, dirtyIdx, directionIdx]);

  return (
    <Modal open onClose={onClose} size="lg" ariaLabel="Schema migration history" scrollable>
      <ModalHeader>
        <ModalTitle>Schema migration history</ModalTitle>
      </ModalHeader>
      <ModalBody scrollable>
        <Stack gap="md">
          <Alert variant="info" title="Scope">
            A read-only snapshot of this database's{" "}
            <code>nsql_schema_migrations</code> table — the record the official
            migration system writes as it applies and reverts migrations. Creating,
            validating, dry-running and applying migrations stays with the{" "}
            <code>nextsql migrate</code> CLI, which needs the local migration
            files this client does not hold.
          </Alert>
          {data?.warnings?.length ? (
            <Alert variant="warning" title="Some data was unavailable">
              <List>
                {data.warnings.map((warning, index) => (
                  <ListItem key={index}>{warning}</ListItem>
                ))}
              </List>
            </Alert>
          ) : null}
          {data?.truncated ? (
            <Alert variant="warning" title="History truncated">
              The history is capped at 2,000 rows for display; older migration
              records are omitted.
            </Alert>
          ) : null}
          {error ? (
            <Alert variant="error" title="Could not load" role="alert">
              <Inline gap="sm" align="center" wrap>
                <span>{error}</span>
                <Button variant="outline" size="sm" onClick={onRefresh}>Retry</Button>
              </Inline>
            </Alert>
          ) : loading && !data ? (
            <Inline gap="sm" align="center" role="status">
              <Spinner size="sm" />
              <Text size="sm" variant="muted">Loading migration history…</Text>
            </Inline>
          ) : data && !data.present ? (
            <Alert variant="info" title="No migration history">
              The official migration system has not run on this database yet, or
              your role cannot read <code>nsql_schema_migrations</code>. Run{" "}
              <code>nextsql migrate up</code> against this database to initialize it.
            </Alert>
          ) : data ? (
            <>
              {summary?.dirtyVersions.length ? (
                <Alert variant="error" title="Dirty migration state" role="alert">
                  {summary.dirtyVersions.length === 1
                    ? `Version ${summary.dirtyVersions[0]} is marked dirty — a migration failed part-way. Resolve it with `
                    : `Versions ${summary.dirtyVersions.join(", ")} are marked dirty — a migration failed part-way. Resolve it with `}
                  <code>nextsql migrate repair</code> or{" "}
                  <code>nextsql migrate force</code> before applying more.
                </Alert>
              ) : null}
              <Inline gap="md" wrap>
                <Stat label="Applied migrations" value={String(summary?.count ?? 0)} />
                <Stat label="Current version" value={summary?.current ?? "—"} />
                <Stat label="Last direction" value={summary?.lastDirection ?? "—"} />
                <Stat label="Dirty" value={summary?.dirtyVersions.length ? "Yes" : "No"} />
              </Inline>
              <ResultTable
                result={data.history}
                empty="No migrations have been applied to this database"
                label="Schema migrations"
              />
            </>
          ) : null}
        </Stack>
      </ModalBody>
      <ModalFooter>
        <Button variant="outline" onClick={onClose}>Close</Button>
        <Button variant="primary" onClick={onRefresh} disabled={loading}>
          {loading && data ? "Refreshing…" : "Refresh"}
        </Button>
      </ModalFooter>
    </Modal>
  );
}
