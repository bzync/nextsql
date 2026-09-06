import { useEffect, useMemo, useState } from "react";
import {
  Alert,
  Button,
  Checkbox,
  CodeBlock,
  Input,
  Modal,
  ModalBody,
  ModalFooter,
  ModalHeader,
  ModalTitle,
  RadioGroup,
  Radio,
  Select,
  Stack,
  Text,
} from "@bzync/rui";
import {
  buildGrantSQL,
  GRANT_PRIVILEGES,
  GRANT_SCOPES,
  type GrantAction,
  type GrantBuilderState,
  type GrantMode,
  type GrantScope,
} from "./resultTools";

const INITIAL_STATE: GrantBuilderState = {
  action: "grant",
  mode: "privilege",
  roleName: "",
  grantee: "",
  allPrivileges: false,
  privileges: [],
  scope: "table",
  objectName: "",
  columnTable: "",
  columnName: "",
};

// Scopes that never take an object name at all — the parser's scope()
// consumes only the keyword itself for these (internal/sql/parser.scope).
const NAMELESS_SCOPES = new Set<GrantScope>(["cluster", "backup", "replication", "administration"]);

const OBJECT_LABEL: Partial<Record<GrantScope, string>> = {
  database: "Database (optional — defaults to the current database)",
  schema: "Schema name",
  table: "Table name",
  function: "Function name",
  resourcegroup: "Resource group name",
};

export function GrantBuilder({
  open,
  onClose,
  onInsert,
  tables,
  users,
  roles,
  principalsError,
  initial,
}: {
  open: boolean;
  onClose: () => void;
  onInsert: (sql: string) => void;
  tables: string[];
  users: string[];
  roles: string[];
  principalsError: string | null;
  // Seeds the form from a real system.grants row (the Users & roles
  // explorer's "Revoke" action) instead of always starting blank. Re-applied
  // every time the modal transitions to open, so a stale prior fill never
  // leaks into a differently-prefilled (or unprefilled) reopen.
  initial?: GrantBuilderState | null;
}) {
  const [state, setState] = useState<GrantBuilderState>(initial ?? INITIAL_STATE);

  useEffect(() => {
    if (open) setState(initial ?? INITIAL_STATE);
  }, [open, initial]);

  const update = (patch: Partial<GrantBuilderState>) => setState((current) => ({ ...current, ...patch }));

  const grantees = useMemo(() => [...new Set([...users, ...roles])].sort(), [users, roles]);
  const built = useMemo(() => buildGrantSQL(state), [state]);

  const handleClose = () => {
    setState(INITIAL_STATE);
    onClose();
  };

  const handleInsert = () => {
    if (!built.sql) return;
    onInsert(built.sql);
    setState(INITIAL_STATE);
    onClose();
  };

  return (
    <Modal open={open} onClose={handleClose} size="lg" ariaLabel="GRANT/REVOKE builder" scrollable>
      <ModalHeader>
        <ModalTitle>GRANT / REVOKE builder</ModalTitle>
      </ModalHeader>
      <ModalBody scrollable>
        <Stack gap="md">
          <Text size="sm" variant="muted">
            Builds one GRANT or REVOKE statement and inserts it into the active editor tab — it never runs
            anything here. Review it like any other statement before running it.
          </Text>
          {principalsError ? (
            <Alert variant="warning" title="Suggestions unavailable">
              {principalsError} You can still type a name directly.
            </Alert>
          ) : null}

          <RadioGroup
            label="Action"
            orientation="horizontal"
            value={state.action}
            onChange={(value) => update({ action: value as GrantAction })}
          >
            <Radio value="grant" label="GRANT" />
            <Radio value="revoke" label="REVOKE" />
          </RadioGroup>

          <RadioGroup
            label="What to grant"
            orientation="horizontal"
            value={state.mode}
            onChange={(value) => update({ mode: value as GrantMode })}
          >
            <Radio value="privilege" label="Privilege on a scope" />
            <Radio value="role" label="Role membership" />
          </RadioGroup>

          {state.mode === "role" ? (
            <>
              <Input
                label="Role"
                list="grant-builder-roles"
                value={state.roleName}
                onChange={(event) => update({ roleName: event.currentTarget.value })}
                placeholder="analyst"
              />
              <datalist id="grant-builder-roles">
                {roles.map((role) => <option key={role} value={role} />)}
              </datalist>
            </>
          ) : (
            <>
              <Checkbox
                label="ALL PRIVILEGES"
                description="Grants admin-equivalent access within the chosen scope."
                checked={state.allPrivileges}
                onChange={(event) => update({ allPrivileges: event.currentTarget.checked })}
              />
              <Select
                multiple
                label="Privileges"
                placeholder="Choose one or more privileges"
                disabled={state.allPrivileges}
                options={GRANT_PRIVILEGES}
                value={state.privileges}
                onChange={(value) => update({ privileges: value })}
              />
              <Select
                label="Scope"
                options={GRANT_SCOPES}
                value={state.scope}
                onChange={(value) => update({ scope: value as GrantScope })}
              />
              {state.scope === "column" ? (
                <>
                  <Input
                    label="Table (optional — a bare column name applies scope-wide)"
                    list="grant-builder-tables"
                    value={state.columnTable}
                    onChange={(event) => update({ columnTable: event.currentTarget.value })}
                    placeholder="orders"
                  />
                  <Input
                    label="Column"
                    value={state.columnName}
                    onChange={(event) => update({ columnName: event.currentTarget.value })}
                    placeholder="email"
                  />
                </>
              ) : !NAMELESS_SCOPES.has(state.scope) ? (
                <Input
                  label={OBJECT_LABEL[state.scope]}
                  list={state.scope === "table" ? "grant-builder-tables" : undefined}
                  value={state.objectName}
                  onChange={(event) => update({ objectName: event.currentTarget.value })}
                  placeholder={state.scope === "table" ? "orders" : ""}
                />
              ) : null}
              <datalist id="grant-builder-tables">
                {tables.map((name) => <option key={name} value={name} />)}
              </datalist>
            </>
          )}

          <Input
            label={state.action === "grant" ? "Grant to" : "Revoke from"}
            list="grant-builder-grantees"
            value={state.grantee}
            onChange={(event) => update({ grantee: event.currentTarget.value })}
            placeholder="app"
          />
          <datalist id="grant-builder-grantees">
            {grantees.map((name) => <option key={name} value={name} />)}
          </datalist>

          <Stack gap="xs">
            <Text size="sm" weight="medium">Preview</Text>
            {built.sql ? (
              <CodeBlock code={built.sql} language="sql" />
            ) : (
              <Text size="sm" variant="muted">{built.error}</Text>
            )}
          </Stack>
        </Stack>
      </ModalBody>
      <ModalFooter>
        <Button variant="outline" onClick={handleClose}>Cancel</Button>
        <Button variant="primary" onClick={handleInsert} disabled={!built.sql}>
          Insert into editor
        </Button>
      </ModalFooter>
    </Modal>
  );
}
