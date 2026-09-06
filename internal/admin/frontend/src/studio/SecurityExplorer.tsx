import {
  Alert,
  Button,
  EmptyState,
  Heading,
  Inline,
  Modal,
  ModalBody,
  ModalFooter,
  ModalHeader,
  ModalTitle,
  Spinner,
  Stack,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
  Text,
} from "@bzync/rui";
import type { Security } from "../ops/api";
import { ResultTable } from "../ops/ResultTable";

// Users & roles privilege explorer (Developer operations scope): a read view
// over the same admin-only system.users/system.roles/system.grants catalog
// Operations mode's Security view already exposes (api.security(), also
// already reused for the GRANT/REVOKE builder's grantee suggestions) — no
// new server route. All three tables return zero rows, never an error, for
// a non-admin caller (docs/system-catalog.md), so a degraded read here just
// means empty sections, matching Operations mode's own empty-state wording.
// Each grant row's "Revoke" action hands its exact grantee/privilege/scope/
// object back to the caller, which pre-fills and opens the existing
// GrantBuilder — this view never executes anything itself.
export function SecurityExplorer({
  onClose,
  data,
  loading,
  error,
  onRetry,
  onRevoke,
  onNewGrant,
}: {
  onClose: () => void;
  data: Security | null;
  loading: boolean;
  error: string | null;
  onRetry: () => void;
  onRevoke: (row: { grantee: string; privilege: string; scope: string; object: string }) => void;
  onNewGrant: () => void;
}) {
  const grants = data?.grants;
  const grantColumns = grants?.columns ?? [];
  const granteeIndex = grantColumns.indexOf("grantee");
  const privilegeIndex = grantColumns.indexOf("privilege");
  const scopeIndex = grantColumns.indexOf("scope");
  const objectIndex = grantColumns.indexOf("object");
  const grantRowsUsable = granteeIndex >= 0 && privilegeIndex >= 0 && scopeIndex >= 0 && objectIndex >= 0;

  return (
    <Modal open onClose={onClose} size="lg" ariaLabel="Users & roles" scrollable>
      <ModalHeader>
        <ModalTitle>Users & roles</ModalTitle>
      </ModalHeader>
      <ModalBody scrollable>
        <Stack gap="md">
          <Alert variant="info" title="Scope">
            Users, roles, and persisted grants — the same admin-only catalog Operations
            mode's Security view reads. A non-admin connection sees empty sections here,
            not an error.
          </Alert>
          {error ? (
            <Alert variant="error" title="Could not load" role="alert">
              <Inline gap="sm" align="center" wrap>
                <span>{error}</span>
                <Button variant="outline" size="sm" onClick={onRetry}>Retry</Button>
              </Inline>
            </Alert>
          ) : loading && !data ? (
            <Inline gap="sm" align="center" role="status">
              <Spinner size="sm" />
              <Text size="sm" variant="muted">Loading users, roles, and grants…</Text>
            </Inline>
          ) : data ? (
            <>
              <Stack gap="xs">
                <Heading as="h3" size="sm">Users</Heading>
                <ResultTable result={data.users} empty="No users visible (requires cluster ADMIN, or none exist)" />
              </Stack>
              <Stack gap="xs">
                <Heading as="h3" size="sm">Roles</Heading>
                <ResultTable result={data.roles} empty="No roles created" />
              </Stack>
              <Stack gap="xs">
                <Heading as="h3" size="sm">Grants</Heading>
                {!grants || grants.rows.length === 0 ? (
                  <EmptyState size="sm" density="compact" title="No grants issued" />
                ) : !grantRowsUsable ? (
                  <ResultTable result={grants} empty="No grants issued" />
                ) : (
                  <Table density="compact">
                    <TableHeader>
                      <TableRow>
                        {grantColumns.map((c) => <TableHead key={c}>{c}</TableHead>)}
                        <TableHead>Actions</TableHead>
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {grants.rows.map((row, i) => {
                        const grantee = row[granteeIndex] ?? "";
                        const privilege = row[privilegeIndex] ?? "";
                        const scope = row[scopeIndex] ?? "";
                        const object = row[objectIndex] ?? "";
                        return (
                          <TableRow key={i}>
                            {row.map((cell, j) => (
                              <TableCell key={j}>
                                {cell === null ? <Text as="span" variant="muted">NULL</Text> : cell}
                              </TableCell>
                            ))}
                            <TableCell>
                              <Button
                                variant="outline"
                                size="sm"
                                onClick={() => onRevoke({ grantee, privilege, scope, object })}
                              >
                                Revoke…
                              </Button>
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
        </Stack>
      </ModalBody>
      <ModalFooter>
        <Button variant="outline" onClick={onClose}>Close</Button>
        <Button variant="primary" onClick={onNewGrant}>Grant / Revoke…</Button>
      </ModalFooter>
    </Modal>
  );
}
