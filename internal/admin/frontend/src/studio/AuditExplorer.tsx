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
  Text,
} from "@bzync/rui";
import type { Security } from "../ops/api";
import { AuditVerifyCard } from "../ops/AuditVerifyCard";
import { ResultTable } from "../ops/ResultTable";

// Studio's Audit viewer is a read-only projection of the exact live,
// admin-only audit_verify/audit_log data Operations Security already reads.
// The caller deliberately refreshes api.security() on every open and manual
// Refresh because the durable audit file can change independently of Studio.
export function AuditExplorer({
  onClose,
  data,
  loading,
  error,
  onRefresh,
}: {
  onClose: () => void;
  data: Security | null;
  loading: boolean;
  error: string | null;
  onRefresh: () => void;
}) {
  return (
    <Modal open onClose={onClose} size="lg" ariaLabel="Audit viewer" scrollable>
      <ModalHeader>
        <ModalTitle>Audit viewer</ModalTitle>
      </ModalHeader>
      <ModalBody scrollable>
        <Stack gap="md">
          <Alert variant="info" title="Read-only verified tail">
            The server verifies the durable audit chain on every read and returns at
            most its 200 most recent redacted records. Only server-side catalog
            authorization decides what is visible; this view cannot modify the log.
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
              <Text size="sm" variant="muted">Verifying the audit chain and loading its recent tail…</Text>
            </Inline>
          ) : data ? (
            <>
              <AuditVerifyCard auditVerify={data.audit_verify} />
              <Stack gap="xs">
                <Heading as="h3" size="sm">Recent audit records</Heading>
                <ResultTable
                  result={data.audit_log}
                  empty="No audit log attached (embedded/CLI use), or nothing recorded yet"
                  label="Recent audit records"
                />
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
