import { useState, type FormEvent } from "react";
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

export function AddRowModal({
  open,
  table,
  columns,
  columnTypes,
  onAddRow,
  onClose,
}: {
  open: boolean;
  table: string;
  columns: string[];
  columnTypes: string[];
  onAddRow: (values: Record<string, string | null>) => void;
  onClose: () => void;
}) {
  const [values, setValues] = useState<Record<string, string>>({});
  const [nullFlags, setNullFlags] = useState<Record<string, boolean>>({});

  if (!open) return null;

  const handleSubmit = (event: FormEvent) => {
    event.preventDefault();
    const finalValues: Record<string, string | null> = {};
    for (const col of columns) {
      if (nullFlags[col]) {
        finalValues[col] = null;
      } else if (values[col] !== undefined && values[col].trim().length > 0) {
        finalValues[col] = values[col];
      } else {
        finalValues[col] = null;
      }
    }
    onAddRow(finalValues);
  };

  return (
    <Modal open onClose={onClose} size="md" ariaLabel={`Insert row into ${table}`}>
      <ModalHeader>
        <ModalTitle>Insert row into {table}</ModalTitle>
      </ModalHeader>
      <form onSubmit={handleSubmit}>
        <ModalBody>
          <Stack gap="md">
            <Text size="sm" variant="muted">
              Specify values for the new row. Empty fields or checked NULL fields will be inserted as SQL NULL.
            </Text>
            <Stack gap="sm">
              {columns.map((col, idx) => {
                const type = columnTypes[idx] || "STRING";
                const isNull = Boolean(nullFlags[col]);
                const val = values[col] || "";
                return (
                  <div key={col} className="nss-add-row-field">
                    <Inline gap="xs" align="center" justify="between">
                      <Inline gap="xs" align="center">
                        <Text size="sm" weight="semibold">
                          {col}
                        </Text>
                        <Badge variant="info">{type}</Badge>
                      </Inline>
                      <label>
                        <Inline gap="xs" align="center">
                          <input
                            type="checkbox"
                            checked={isNull}
                            onChange={(e) =>
                              setNullFlags((prev) => ({
                                ...prev,
                                [col]: e.target.checked,
                              }))
                            }
                          />
                          <Text size="xs" variant="muted">
                            NULL
                          </Text>
                        </Inline>
                      </label>
                    </Inline>
                    {!isNull ? (
                      <Input
                        size="sm"
                        value={val}
                        placeholder={`Enter ${col} (${type})…`}
                        onChange={(e) =>
                          setValues((prev) => ({
                            ...prev,
                            [col]: e.target.value,
                          }))
                        }
                      />
                    ) : null}
                  </div>
                );
              })}
            </Stack>
          </Stack>
        </ModalBody>
        <ModalFooter>
          <Button variant="ghost" size="sm" type="button" onClick={onClose}>
            Cancel
          </Button>
          <Button variant="primary" size="sm" type="submit">
            Stage insert
          </Button>
        </ModalFooter>
      </form>
    </Modal>
  );
}
