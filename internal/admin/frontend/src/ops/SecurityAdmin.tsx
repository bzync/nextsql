import { useMemo, useState, type FormEvent, type ReactNode } from "react";
import {
  Alert,
  Badge,
  Button,
  Checkbox,
  Inline,
  Input,
  Modal,
  ModalBody,
  ModalFooter,
  ModalHeader,
  ModalTitle,
  Select,
  Stack,
  Text,
} from "@bzync/rui";
import { api, ApiError, type ResultSet, type SecurityActionRequest } from "./api";
import { Icon } from "../shared/icons";

// RBAC administration for the Security view.
//
// Every control here posts a structured request to /api/v1/security/action,
// which renders the documented SQL statement server-side and runs it on the
// operator's own connection. Authorization is therefore entirely the server's:
// an operator without the privilege gets the engine's own refusal (403), and
// this UI can do nothing that the same person could not do by typing the
// statement. Reading who exists stays the existing system.users/roles/grants
// read model, so the forms only need name suggestions from it.

// Privilege and scope spellings match security.Privilege.String() /
// ScopeKind.String() and the server's own closed lists in
// internal/admin/ops/security_action.go. "alter" is deliberately absent: it
// lexes as a reserved keyword outside the GRANT privilege-list grammar, so
// `GRANT ALTER ON ...` does not parse.
const PRIVILEGES = [
  { value: "connect", label: "CONNECT" },
  { value: "select", label: "SELECT" },
  { value: "insert", label: "INSERT" },
  { value: "update", label: "UPDATE" },
  { value: "delete", label: "DELETE" },
  { value: "create", label: "CREATE" },
  { value: "drop", label: "DROP" },
  { value: "index", label: "INDEX" },
  { value: "execute", label: "EXECUTE" },
  { value: "usage", label: "USAGE" },
  { value: "grant", label: "GRANT" },
  { value: "backup", label: "BACKUP" },
  { value: "restore", label: "RESTORE" },
  { value: "replication", label: "REPLICATION" },
  { value: "cdc", label: "CDC" },
  { value: "admin", label: "ADMIN" },
];

const SCOPES = [
  { value: "cluster", label: "CLUSTER" },
  { value: "database", label: "DATABASE" },
  { value: "schema", label: "SCHEMA" },
  { value: "table", label: "TABLE" },
  { value: "column", label: "COLUMN" },
  { value: "function", label: "FUNCTION" },
  { value: "resourcegroup", label: "RESOURCE GROUP" },
  { value: "backup", label: "BACKUP" },
  { value: "replication", label: "REPLICATION" },
  { value: "administration", label: "ADMINISTRATION" },
];

// Scopes that name no object at all.
const NAMELESS_SCOPES = new Set(["cluster", "backup", "replication", "administration"]);

const OBJECT_LABEL: Record<string, string> = {
  database: "Database (optional — blank means the connected database)",
  schema: "Schema",
  table: "Table",
  function: "Function",
  resourcegroup: "Resource group",
};

// A plain identifier is the only thing the server will interpolate, so the
// form refuses anything else up front rather than round-tripping a rejection.
const IDENT = /^[A-Za-z_][A-Za-z0-9_]{0,127}$/;

export function namesFrom(result: ResultSet | undefined, column: string): string[] {
  if (!result) return [];
  const index = result.columns.indexOf(column);
  if (index < 0) return [];
  const names = new Set<string>();
  for (const row of result.rows) {
    const value = row[index];
    if (value) names.add(value);
  }
  return [...names].sort();
}

// columnsByTable groups system.columns into table -> column names, so the
// COLUMN-scope picker can offer only the columns that belong to the table
// the operator actually chose.
export function columnsByTable(result: ResultSet | undefined): Record<string, string[]> {
  const out: Record<string, string[]> = {};
  if (!result) return out;
  const t = result.columns.indexOf("table_name");
  const c = result.columns.indexOf("column_name");
  if (t < 0 || c < 0) return out;
  for (const row of result.rows) {
    const table = row[t];
    const column = row[c];
    if (!table || !column) continue;
    (out[table] ??= []).push(column);
  }
  return out;
}

const opt = (names: string[]) => names.map((n) => ({ value: n, label: n }));

// PickerField is a dropdown that explains itself when there is nothing to
// pick. An empty <Select> gives an operator no idea whether the catalog is
// genuinely empty or their own RBAC is hiding it, so the empty case renders
// a note in place of the control rather than a dead dropdown.
function PickerField({
  label,
  names,
  value,
  onChange,
  emptyNote,
  placeholder,
}: {
  label: string;
  names: string[];
  value: string;
  onChange: (value: string) => void;
  emptyNote: string;
  placeholder?: string;
}) {
  if (names.length === 0) {
    return (
      <Stack gap="xs">
        <Text size="sm" weight="semibold">{label}</Text>
        <Alert variant="info" title="Nothing to choose">{emptyNote}</Alert>
      </Stack>
    );
  }
  return (
    <Select
      label={label}
      placeholder={placeholder ?? `Choose a ${label.toLowerCase()}`}
      options={opt(names)}
      value={value}
      onChange={(v) => onChange(v as string)}
    />
  );
}

type Dialog =
  | { kind: "create_user" }
  | { kind: "drop_user" }
  | { kind: "create_role" }
  | { kind: "drop_role" }
  | { kind: "role_membership" }
  | { kind: "grant" };

// onDone reports the confirmation text to the parent rather than rendering it
// here: applying a change reloads the read model, and ViewFrame swaps its
// children for a spinner while that runs, which would unmount this component
// and destroy the message before the operator could read it. The parent owns
// the read model and survives the reload, so the confirmation lives there.
export function SecurityAdmin({
  users,
  roles,
  tables,
  columns,
  resourceGroups,
  onDone,
}: {
  users: string[];
  roles: string[];
  tables: string[];
  columns: Record<string, string[]>;
  resourceGroups: string[];
  onDone: (message: string) => void;
}) {
  const [dialog, setDialog] = useState<Dialog | null>(null);
  const grantees = useMemo(() => [...new Set([...users, ...roles])].sort(), [users, roles]);

  const finish = (message: string) => {
    setDialog(null);
    onDone(message);
  };

  return (
    <Stack gap="sm">
      <Inline gap="xs" align="center" wrap>
        <Button size="sm" variant="primary" icon={<Icon name="users" size={14} />} onClick={() => setDialog({ kind: "create_user" })}>
          New user…
        </Button>
        <Button size="sm" variant="outline" onClick={() => setDialog({ kind: "create_role" })}>
          New role…
        </Button>
        <Button size="sm" variant="outline" onClick={() => setDialog({ kind: "role_membership" })}>
          Role membership…
        </Button>
        <Button size="sm" variant="outline" icon={<Icon name="lock" size={14} />} onClick={() => setDialog({ kind: "grant" })}>
          Grant / revoke privilege…
        </Button>
        <Button size="sm" variant="ghost" disabled={users.length === 0} onClick={() => setDialog({ kind: "drop_user" })}>
          Drop user…
        </Button>
        <Button size="sm" variant="ghost" disabled={roles.length === 0} onClick={() => setDialog({ kind: "drop_role" })}>
          Drop role…
        </Button>
      </Inline>
      {dialog?.kind === "create_user" ? (
        <CreateUserDialog onClose={() => setDialog(null)} onDone={finish} />
      ) : null}
      {dialog?.kind === "create_role" ? (
        <SimpleNameDialog
          title="Create role"
          label="Role name"
          verb="Create role"
          build={(name) => ({ op: "create_role", name })}
          done={(name) => `Role ${name} created.`}
          onClose={() => setDialog(null)}
          onDone={finish}
        />
      ) : null}
      {dialog?.kind === "drop_user" ? (
        <DropDialog
          title="Drop user"
          label="User"
          names={users}
          warning="Dropping a user terminates that user's sessions. This cannot be undone."
          build={(name) => ({ op: "drop_user", name })}
          done={(name) => `User ${name} dropped.`}
          onClose={() => setDialog(null)}
          onDone={finish}
        />
      ) : null}
      {dialog?.kind === "drop_role" ? (
        <DropDialog
          title="Drop role"
          label="Role"
          names={roles}
          warning="Every principal that held this role loses the privileges it carried. This cannot be undone."
          build={(name) => ({ op: "drop_role", name })}
          done={(name) => `Role ${name} dropped.`}
          onClose={() => setDialog(null)}
          onDone={finish}
        />
      ) : null}
      {dialog?.kind === "role_membership" ? (
        <RoleMembershipDialog roles={roles} grantees={grantees} onClose={() => setDialog(null)} onDone={finish} />
      ) : null}
      {dialog?.kind === "grant" ? (
        <GrantDialog
          grantees={grantees}
          tables={tables}
          columns={columns}
          resourceGroups={resourceGroups}
          onClose={() => setDialog(null)}
          onDone={finish}
        />
      ) : null}
    </Stack>
  );
}

// runAction centralizes the one thing every dialog does: post, surface the
// server's own refusal verbatim on failure, and never leave the form busy.
function useAction(onDone: (message: string) => void) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const run = async (body: SecurityActionRequest, message: string) => {
    setError(null);
    setBusy(true);
    try {
      await api.securityAction(body);
      onDone(message);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };
  return { busy, error, run };
}

function DialogFrame({
  title,
  onClose,
  onSubmit,
  submitLabel,
  submitDisabled,
  busy,
  error,
  children,
}: {
  title: string;
  onClose: () => void;
  onSubmit: (e: FormEvent) => void;
  submitLabel: string;
  submitDisabled?: boolean;
  busy: boolean;
  error: string | null;
  children: ReactNode;
}) {
  return (
    <Modal open onClose={onClose} size="sm" ariaLabel={title}>
      <ModalHeader>
        <ModalTitle>{title}</ModalTitle>
      </ModalHeader>
      <form onSubmit={onSubmit} autoComplete="off">
        <ModalBody>
          <Stack gap="sm">
            {error ? <Alert variant="error" title="Refused">{error}</Alert> : null}
            {children}
          </Stack>
        </ModalBody>
        <ModalFooter>
          <Button variant="ghost" size="sm" type="button" onClick={onClose} disabled={busy}>Cancel</Button>
          <Button variant="primary" size="sm" type="submit" disabled={busy || submitDisabled}>
            {busy ? "Working…" : submitLabel}
          </Button>
        </ModalFooter>
      </form>
    </Modal>
  );
}

function CreateUserDialog({ onClose, onDone }: { onClose: () => void; onDone: (m: string) => void }) {
  const [name, setName] = useState("");
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const { busy, error, run } = useAction(onDone);

  const nameOK = IDENT.test(name.trim());
  const match = password.length > 0 && password === confirm;

  return (
    <DialogFrame
      title="Create user"
      onClose={onClose}
      busy={busy}
      error={error}
      submitLabel="Create user"
      submitDisabled={!nameOK || !match}
      onSubmit={(e) => {
        e.preventDefault();
        void run({ op: "create_user", name: name.trim(), password }, `User ${name.trim()} created.`);
      }}
    >
      <Text size="sm" variant="muted">
        A new user starts with no privileges at all. Grant it what it needs afterwards.
      </Text>
      <Input
        label="User name"
        value={name}
        onChange={(e) => setName(e.currentTarget.value)}
        autoComplete="off"
        hint={name && !nameOK ? "Letters, digits and underscore only, starting with a letter or underscore." : undefined}
      />
      <Input
        label="Password"
        type="password"
        value={password}
        onChange={(e) => setPassword(e.currentTarget.value)}
        autoComplete="new-password"
      />
      <Input
        label="Confirm password"
        type="password"
        value={confirm}
        onChange={(e) => setConfirm(e.currentTarget.value)}
        autoComplete="new-password"
        hint={confirm && !match ? "Passwords do not match." : undefined}
      />
    </DialogFrame>
  );
}

function SimpleNameDialog({
  title,
  label,
  verb,
  build,
  done,
  onClose,
  onDone,
}: {
  title: string;
  label: string;
  verb: string;
  build: (name: string) => SecurityActionRequest;
  done: (name: string) => string;
  onClose: () => void;
  onDone: (m: string) => void;
}) {
  const [name, setName] = useState("");
  const { busy, error, run } = useAction(onDone);
  const ok = IDENT.test(name.trim());
  return (
    <DialogFrame
      title={title}
      onClose={onClose}
      busy={busy}
      error={error}
      submitLabel={verb}
      submitDisabled={!ok}
      onSubmit={(e) => {
        e.preventDefault();
        void run(build(name.trim()), done(name.trim()));
      }}
    >
      <Input
        label={label}
        value={name}
        onChange={(e) => setName(e.currentTarget.value)}
        hint={name && !ok ? "Letters, digits and underscore only, starting with a letter or underscore." : undefined}
      />
    </DialogFrame>
  );
}

function DropDialog({
  title,
  label,
  names,
  warning,
  build,
  done,
  onClose,
  onDone,
}: {
  title: string;
  label: string;
  names: string[];
  warning: string;
  build: (name: string) => SecurityActionRequest;
  done: (name: string) => string;
  onClose: () => void;
  onDone: (m: string) => void;
}) {
  const [name, setName] = useState("");
  const [confirmed, setConfirmed] = useState(false);
  const { busy, error, run } = useAction(onDone);
  return (
    <DialogFrame
      title={title}
      onClose={onClose}
      busy={busy}
      error={error}
      submitLabel={title}
      submitDisabled={!name || !confirmed}
      onSubmit={(e) => {
        e.preventDefault();
        void run(build(name), done(name));
      }}
    >
      <Alert variant="warning" title="This cannot be undone">{warning}</Alert>
      <Select
        label={label}
        placeholder={`Choose a ${label.toLowerCase()}`}
        options={names.map((n) => ({ value: n, label: n }))}
        value={name}
        onChange={(value) => setName(value as string)}
      />
      <Checkbox
        label={`Yes, drop ${name || "this principal"}`}
        checked={confirmed}
        onChange={(e) => setConfirmed(e.currentTarget.checked)}
      />
    </DialogFrame>
  );
}

function RoleMembershipDialog({
  roles,
  grantees,
  onClose,
  onDone,
}: {
  roles: string[];
  grantees: string[];
  onClose: () => void;
  onDone: (m: string) => void;
}) {
  const [action, setAction] = useState<"grant_role" | "revoke_role">("grant_role");
  const [role, setRole] = useState("");
  const [grantee, setGrantee] = useState("");
  const { busy, error, run } = useAction(onDone);
  // Self-membership is meaningless, so the chosen role is never its own
  // recipient. The server refuses it too, in case the request is hand-made.
  const recipients = grantees.filter((g) => g !== role.trim());
  const ok =
    IDENT.test(role.trim()) && IDENT.test(grantee.trim()) && role.trim() !== grantee.trim();
  const granting = action === "grant_role";
  return (
    <DialogFrame
      title="Role membership"
      onClose={onClose}
      busy={busy}
      error={error}
      submitLabel={granting ? "Grant role" : "Revoke role"}
      submitDisabled={!ok}
      onSubmit={(e) => {
        e.preventDefault();
        void run(
          { op: action, role: role.trim(), grantee: grantee.trim() },
          granting
            ? `Role ${role.trim()} granted to ${grantee.trim()}.`
            : `Role ${role.trim()} revoked from ${grantee.trim()}.`,
        );
      }}
    >
      <Select
        label="Action"
        options={[
          { value: "grant_role", label: "Grant role to a principal" },
          { value: "revoke_role", label: "Revoke role from a principal" },
        ]}
        value={action}
        onChange={(value) => setAction(value as "grant_role" | "revoke_role")}
      />
      <PickerField
        label="Role"
        names={roles}
        value={role}
        onChange={(v) => {
          setRole(v);
          if (v === grantee) setGrantee("");
        }}
        emptyNote="No roles exist yet. Create one with New role… before granting membership."
      />
      {/* A role may be granted to a user or to another role — nested roles are
          resolved transitively — but never to itself, which would record a
          meaningless self-membership. The chosen role is therefore not offered
          as its own recipient. */}
      <PickerField
        label={granting ? "Grant to" : "Revoke from"}
        names={recipients}
        value={grantee}
        onChange={setGrantee}
        emptyNote={
          role
            ? `No other user or role is available to receive ${role}. A role cannot be granted to itself.`
            : "No users or roles are visible to you. A role has to be granted to someone."
        }
      />
    </DialogFrame>
  );
}

function GrantDialog({
  grantees,
  tables,
  columns,
  resourceGroups,
  onClose,
  onDone,
}: {
  grantees: string[];
  tables: string[];
  columns: Record<string, string[]>;
  resourceGroups: string[];
  onClose: () => void;
  onDone: (m: string) => void;
}) {
  const [action, setAction] = useState<"grant" | "revoke">("grant");
  const [allPrivileges, setAllPrivileges] = useState(false);
  const [privileges, setPrivileges] = useState<string[]>([]);
  const [scope, setScope] = useState("table");
  const [object, setObject] = useState("");
  const [columnTable, setColumnTable] = useState("");
  const [columnName, setColumnName] = useState("");
  const [grantee, setGrantee] = useState("");
  const { busy, error, run } = useAction(onDone);

  const granting = action === "grant";
  // Only SCHEMA and FUNCTION have no catalog view to enumerate, so they stay
  // typed; every other object is chosen from what the server actually
  // reports the operator may see.
  const typedScope = scope === "schema" || scope === "function";
  const columnChoices = columnTable ? (columns[columnTable] ?? []) : [];

  const ok =
    IDENT.test(grantee.trim()) &&
    (allPrivileges || privileges.length > 0) &&
    (scope === "column"
      ? IDENT.test(columnName.trim())
      : NAMELESS_SCOPES.has(scope) || scope === "database"
        ? object.trim() === "" || IDENT.test(object.trim())
        : IDENT.test(object.trim()));

  return (
    <DialogFrame
      title={granting ? "Grant privilege" : "Revoke privilege"}
      onClose={onClose}
      busy={busy}
      error={error}
      submitLabel={granting ? "Grant" : "Revoke"}
      submitDisabled={!ok}
      onSubmit={(e) => {
        e.preventDefault();
        void run(
          {
            op: action,
            grantee: grantee.trim(),
            all_privileges: allPrivileges,
            privileges: allPrivileges ? [] : privileges,
            scope,
            object: object.trim(),
            column_table: columnTable.trim(),
            column_name: columnName.trim(),
          },
          granting ? `Privileges granted to ${grantee.trim()}.` : `Privileges revoked from ${grantee.trim()}.`,
        );
      }}
    >
      <Select
        label="Action"
        options={[
          { value: "grant", label: "GRANT" },
          { value: "revoke", label: "REVOKE" },
        ]}
        value={action}
        onChange={(value) => setAction(value as "grant" | "revoke")}
      />
      <Checkbox
        label="ALL PRIVILEGES"
        description="Admin-equivalent access within the chosen scope."
        checked={allPrivileges}
        onChange={(e) => setAllPrivileges(e.currentTarget.checked)}
      />
      <Select
        multiple
        label="Privileges"
        placeholder="Choose one or more privileges"
        disabled={allPrivileges}
        options={PRIVILEGES}
        value={privileges}
        onChange={(value) => setPrivileges(value as string[])}
      />
      <Select
        label="Scope"
        options={SCOPES}
        value={scope}
        onChange={(value) => {
          setScope(value as string);
          setObject("");
          setColumnTable("");
          setColumnName("");
        }}
      />

      {scope === "column" ? (
        <>
          <PickerField
            label="Table"
            names={tables}
            value={columnTable}
            onChange={(v) => {
              setColumnTable(v);
              setColumnName("");
            }}
            emptyNote="No tables are visible to you, so a column cannot be picked. A COLUMN grant needs a table you can see."
          />
          {columnTable ? (
            <PickerField
              label="Column"
              names={columnChoices}
              value={columnName}
              onChange={setColumnName}
              emptyNote={`No columns are visible for ${columnTable}.`}
            />
          ) : (
            <Text size="sm" variant="muted">Choose a table first to list its columns.</Text>
          )}
        </>
      ) : scope === "table" ? (
        <PickerField
          label="Table"
          names={tables}
          value={object}
          onChange={setObject}
          emptyNote="No tables are visible to you. Create a table, or ask for SELECT on one, before granting on it."
        />
      ) : scope === "resourcegroup" ? (
        <PickerField
          label="Resource group"
          names={resourceGroups}
          value={object}
          onChange={setObject}
          emptyNote="No resource groups exist yet. Create one with CREATE RESOURCE GROUP before granting USAGE on it."
        />
      ) : scope === "database" ? (
        <Text size="sm" variant="muted">
          <Badge variant="info">DATABASE</Badge> applies to the database you are connected to.
        </Text>
      ) : typedScope ? (
        <Input
          label={OBJECT_LABEL[scope] ?? "Object"}
          value={object}
          onChange={(e) => setObject(e.currentTarget.value)}
          hint={`There is no catalog view listing ${scope}s, so this one is typed.`}
        />
      ) : (
        <Text size="sm" variant="muted">
          <Badge variant="info">{scope.toUpperCase()}</Badge> scope names no object.
        </Text>
      )}

      <PickerField
        label={granting ? "Grant to" : "Revoke from"}
        names={grantees}
        value={grantee}
        onChange={setGrantee}
        emptyNote="No users or roles are visible to you. Create a user or role first — a grant needs someone to receive it."
      />
    </DialogFrame>
  );
}
