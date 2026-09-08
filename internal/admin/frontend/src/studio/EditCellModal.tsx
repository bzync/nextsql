import { useEffect, useState, type FormEvent } from "react";
import {
  Badge,
  Button,
  Inline,
  Input,
  Modal,
  ModalBody,
  ModalFooter,
  ModalHeader,
  ModalTitle,
  Stack,
  Text,
} from "@bzync/rui";

export function EditCellModal({
  open,
  table,
  column,
  columnType,
  rowKey,
  currentValue,
  originalValue,
  onSave,
  onClose,
}: {
  open: boolean;
  table: string;
  column: string;
  columnType: string;
  rowKey: string;
  currentValue: string | null;
  originalValue: string | null;
  onSave: (newValue: string | null) => void;
  onClose: () => void;
}) {
  const [isNull, setIsNull] = useState<boolean>(currentValue === null);
  const [value, setValue] = useState<string>(currentValue ?? "");

  useEffect(() => {
    setIsNull(currentValue === null);
    setValue(currentValue ?? "");
  }, [currentValue, open]);

  if (!open) return null;

  const handleSubmit = (event: FormEvent) => {
    event.preventDefault();
    onSave(isNull ? null : value);
  };

  const isJSON = columnType.toUpperCase() === "JSON";
  const isModified = (isNull ? null : value) !== originalValue;

  return (
    <Modal open onClose={onClose} size="sm" ariaLabel={`Edit cell ${column}`}>
      <ModalHeader>
        <ModalTitle>Edit cell</ModalTitle>
      </ModalHeader>
      <form onSubmit={handleSubmit}>
        <ModalBody>
          <Stack gap="sm">
            <Inline gap="xs" align="center" wrap>
              <Text size="sm">
                Table: <strong>{table}</strong>
              </Text>
              <Text size="sm">·</Text>
              <Text size="sm">
                Column: <strong>{column}</strong>
              </Text>
              <Badge variant="info">{columnType}</Badge>
            </Inline>
            <Text size="xs" variant="muted">
              Row PK: <code>{rowKey}</code>
            </Text>
            {originalValue !== null ? (
              <Text size="xs" variant="muted">
                Original value: <code>{originalValue}</code>
              </Text>
            ) : (
              <Text size="xs" variant="muted">
                Original value: <span className="nss-null">NULL</span>
              </Text>
            )}

            <Stack gap="xs">
              <label>
                <Inline gap="xs" align="center">
                  <input
                    type="checkbox"
                    checked={isNull}
                    onChange={(e) => {
                      setIsNull(e.target.checked);
                      if (e.target.checked) setValue("");
                    }}
                  />
                  <Text size="sm">Set value to NULL</Text>
                </Inline>
              </label>

              {!isNull ? (
                isJSON ? (
                  <textarea
                    className="nss-cell-textarea"
                    rows={4}
                    value={value}
                    onChange={(e) => setValue(e.target.value)}
                    autoFocus
                    placeholder="Enter JSON value…"
                  />
                ) : (
                  <Input
                    value={value}
                    onChange={(e) => setValue(e.target.value)}
                    autoFocus
                    placeholder="Enter value…"
                  />
                )
              ) : null}
            </Stack>

            {isModified ? (
              <Badge variant="warning">Modified from original</Badge>
            ) : null}
          </Stack>
        </ModalBody>
        <ModalFooter>
          <Button variant="ghost" size="sm" type="button" onClick={onClose}>
            Cancel
          </Button>
          <Button variant="primary" size="sm" type="submit">
            Stage change
          </Button>
        </ModalFooter>
      </form>
    </Modal>
  );
}
