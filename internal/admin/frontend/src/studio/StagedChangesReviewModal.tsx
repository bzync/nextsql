import { useMemo, useState } from "react";
import {
  Alert,
  Badge,
  Button,
  Heading,
  Inline,
  Modal,
  ModalBody,
  ModalFooter,
  ModalHeader,
  ModalTitle,
  Spinner,
  Stack,
  Text,
} from "@bzync/rui";
import {
  buildStagedChangeSQL,
  stagedChangesCount,
  stagedChangesSummary,
  writeClipboard,
  type StagedChanges,
} from "./resultTools";

export function StagedChangesReviewModal({
  open,
  changes,
  onClose,
  onDiscardAll,
  onRemoveUpdate,
  onRemoveDelete,
  onRemoveInsert,
  onCommit,
  onOpenInEditor,
}: {
  open: boolean;
  changes: StagedChanges;
  onClose: () => void;
  onDiscardAll: () => void;
  onRemoveUpdate: (rowKey: string, col: string) => void;
  onRemoveDelete: (rowKey: string) => void;
  onRemoveInsert: (tempId: string) => void;
  onCommit: (sql: string, statements: string[]) => Promise<void>;
  onOpenInEditor?: (sql: string) => void;
}) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [copyStatus, setCopyStatus] = useState<string | null>(null);

  const built = useMemo(() => buildStagedChangeSQL(changes), [changes]);
  const count = useMemo(() => stagedChangesCount(changes), [changes]);

  if (!open) return null;

  const handleCopy = async () => {
    if (!built.sql) return;
    try {
      await writeClipboard(built.sql);
      setCopyStatus("Copied transaction SQL.");
      setTimeout(() => setCopyStatus(null), 2500);
    } catch {
      setCopyStatus("Could not copy SQL.");
    }
  };

  const handleCommit = async () => {
    if (!built.sql || built.statements.length === 0 || busy) return;
    setBusy(true);
    setError(null);
    try {
      await onCommit(built.sql, built.statements);
      onClose();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };

  const handleOpenEditor = () => {
    if (built.sql && onOpenInEditor) {
      onOpenInEditor(built.sql);
      onClose();
    }
  };

  // Flatten updates list for display
  const updateList: { rowKey: string; col: string; oldVal: string | null; newVal: string | null; type: string }[] = [];
  for (const rowKey of Object.keys(changes.updates)) {
    for (const col of Object.keys(changes.updates[rowKey] || {})) {
      const u = changes.updates[rowKey][col];
      updateList.push({
        rowKey,
        col,
        oldVal: u.oldValue,
        newVal: u.newValue,
        type: u.columnType,
      });
    }
  }

  const deleteList = Object.values(changes.deletes);
  const insertList = changes.inserts;

  return (
    <Modal open onClose={onClose} size="lg" ariaLabel="Review staged data changes">
      <ModalHeader>
        <ModalTitle>Review staged changes</ModalTitle>
      </ModalHeader>
      <ModalBody>
        <Stack gap="md">
          <Inline gap="xs" align="center" justify="between">
            <Inline gap="xs" align="center">
              <Text size="sm">
                Target table: <strong>{changes.table}</strong>
              </Text>
              <Badge variant="warning">{stagedChangesSummary(changes)}</Badge>
            </Inline>
            <Button variant="ghost" size="sm" onClick={onDiscardAll} disabled={busy}>
              Discard all changes
            </Button>
          </Inline>

          {error ? (
            <Alert variant="error" title="Commit failed" role="alert">
              {error}
            </Alert>
          ) : null}

          {built.error ? (
            <Alert variant="warning" title="Cannot build SQL" role="alert">
              {built.error}
            </Alert>
          ) : null}

          <Stack gap="sm">
            <Heading as="h4" size="xs">
              Staged operations ({count})
            </Heading>

            {/* Updates list */}
            {updateList.map((u) => (
              <div key={`${u.rowKey}-${u.col}`} className="nss-staged-item">
                <Inline gap="xs" align="center" justify="between">
                  <Inline gap="xs" align="center" wrap>
                    <Badge variant="info">UPDATE</Badge>
                    <Text size="xs" variant="muted">
                      Row <code>{u.rowKey}</code>
                    </Text>
                    <Text size="xs">·</Text>
                    <Text size="xs" weight="semibold">
                      {u.col}
                    </Text>
                    <Text size="xs" variant="muted">
                      {u.oldVal === null ? "<NULL>" : `"${u.oldVal}"`} →{" "}
                      {u.newVal === null ? "<NULL>" : `"${u.newVal}"`}
                    </Text>
                  </Inline>
                  <Button
                    variant="ghost"
                    size="sm"
                    onClick={() => onRemoveUpdate(u.rowKey, u.col)}
                    disabled={busy}
                  >
                    Remove
                  </Button>
                </Inline>
              </div>
            ))}

            {/* Deletes list */}
            {deleteList.map((d) => (
              <div key={d.rowKey} className="nss-staged-item">
                <Inline gap="xs" align="center" justify="between">
                  <Inline gap="xs" align="center">
                    <Badge variant="error">DELETE</Badge>
                    <Text size="xs" variant="muted">
                      Row <code>{d.rowKey}</code>
                    </Text>
                  </Inline>
                  <Button
                    variant="ghost"
                    size="sm"
                    onClick={() => onRemoveDelete(d.rowKey)}
                    disabled={busy}
                  >
                    Remove
                  </Button>
                </Inline>
              </div>
            ))}

            {/* Inserts list */}
            {insertList.map((ins, idx) => (
              <div key={ins.tempId} className="nss-staged-item">
                <Inline gap="xs" align="center" justify="between">
                  <Inline gap="xs" align="center" wrap>
                    <Badge variant="success">INSERT</Badge>
                    <Text size="xs" variant="muted">
                      Row #{idx + 1}
                    </Text>
                    <Text size="xs">
                      (
                      {Object.keys(ins.values)
                        .filter((k) => ins.values[k] !== null)
                        .map((k) => `${k}=${ins.values[k]}`)
                        .join(", ")}
                      )
                    </Text>
                  </Inline>
                  <Button
                    variant="ghost"
                    size="sm"
                    onClick={() => onRemoveInsert(ins.tempId)}
                    disabled={busy}
                  >
                    Remove
                  </Button>
                </Inline>
              </div>
            ))}
          </Stack>

          {built.sql ? (
            <Stack gap="xs">
              <Inline gap="xs" align="center" justify="between">
                <Heading as="h4" size="xs">
                  Transactional SQL script preview
                </Heading>
                <Inline gap="xs">
                  <Button variant="outline" size="sm" onClick={handleCopy}>
                    Copy SQL
                  </Button>
                  {onOpenInEditor ? (
                    <Button variant="outline" size="sm" onClick={handleOpenEditor}>
                      Open in editor
                    </Button>
                  ) : null}
                </Inline>
              </Inline>
              <pre
                className="nss-fulltext-sql-preview"
                tabIndex={0}
                aria-label="Generated transactional SQL"
              >
                {built.sql}
              </pre>
              {copyStatus ? (
                <Text size="xs" variant="muted" role="status">
                  {copyStatus}
                </Text>
              ) : null}
            </Stack>
          ) : null}
        </Stack>
      </ModalBody>
      <ModalFooter>
        <Button variant="ghost" size="sm" type="button" onClick={onClose} disabled={busy}>
          Close
        </Button>
        <Button
          variant="primary"
          size="sm"
          type="button"
          onClick={handleCommit}
          disabled={busy || !built.sql || count === 0}
        >
          {busy ? (
            <Inline gap="xs" align="center">
              <Spinner size="xs" />
              <span>Committing…</span>
            </Inline>
          ) : (
            `Commit ${count} change${count === 1 ? "" : "s"}`
          )}
        </Button>
      </ModalFooter>
    </Modal>
  );
}
