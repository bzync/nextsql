import {
  Alert,
  Button,
  Heading,
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
import type { Activity } from "../ops/api";
import { ResultTable } from "../ops/ResultTable";

// Transaction console / Lock explorer (Developer operations scope): a
// read-only view over the same system.sessions/system.active_queries/
// system.transactions/system.locks bundle Operations mode's Activity view
// already exposes (api.activity()) — no new server route, same reused-
// catalog pattern as the Users & roles explorer. Unlike users/roles/grants,
// this data is live and changes on the timescale of a running query, so —
// unlike loadSecurity's load-once-per-mount cache — the caller refetches on
// every open and this view offers its own manual Refresh, rather than
// silently showing a stale transaction/lock snapshot.
//
// This is read-only by design: NextSQL has no server-side surface to cancel
// or roll back another session's transaction (only that session's own
// connection, or credential revocation via `nextsql token revoke`, can end
// it — see TODO.md's Developer operations scope note), so there is no
// "kill" action to wire up here, unlike the Users & roles explorer's
// Revoke-into-GrantBuilder action.
export function ActivityExplorer({
  onClose,
  data,
  loading,
  error,
  onRefresh,
}: {
  onClose: () => void;
  data: Activity | null;
  loading: boolean;
  error: string | null;
  onRefresh: () => void;
}) {
  return (
    <Modal open onClose={onClose} size="lg" ariaLabel="Transactions & locks" scrollable>
      <ModalHeader>
        <ModalTitle>Transactions & locks</ModalTitle>
      </ModalHeader>
      <ModalBody scrollable>
        <Stack gap="md">
          <Alert variant="info" title="Scope">
            A read-only snapshot of open sessions, running queries, active transactions,
            and held locks — the same catalog Operations mode's Activity view reads.
            NextSQL has no way to cancel another session's transaction from here; use
            that session's own connection, or revoke its credential.
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
              <Text size="sm" variant="muted">Loading sessions, transactions, and locks…</Text>
            </Inline>
          ) : data ? (
            <>
              <Inline gap="md" wrap>
                <Stat label="Sessions" value={String(data.sessions.rows.length)} />
                <Stat label="Active queries" value={String(data.active_queries.rows.length)} />
                <Stat label="Transactions" value={String(data.transactions.rows.length)} />
                <Stat label="Locks" value={String(data.locks.rows.length)} />
              </Inline>
              <Stack gap="xs">
                <Heading as="h3" size="sm">Transactions</Heading>
                <ResultTable result={data.transactions} empty="No open transactions" label="Open transactions" />
              </Stack>
              <Stack gap="xs">
                <Heading as="h3" size="sm">Locks</Heading>
                <ResultTable result={data.locks} empty="No locks held" label="Held locks" />
              </Stack>
              <Stack gap="xs">
                <Heading as="h3" size="sm">Active queries</Heading>
                <ResultTable result={data.active_queries} empty="No queries running" label="Active queries" />
              </Stack>
              <Stack gap="xs">
                <Heading as="h3" size="sm">Sessions</Heading>
                <ResultTable result={data.sessions} label="Sessions" />
              </Stack>
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
