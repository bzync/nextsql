import { useCallback, useRef, useState, type ReactNode } from "react";
import { EmptyState, Inline, Spinner, Text } from "@bzync/rui";
import { Icon, type IconName } from "../shared/icons";
import { ApiError, type StudioResultSet, type StudioTableDetail, type StudioWorkflowOverview } from "../ops/api";

// SchemaTree is the Database explorer's lazy-loaded object tree. The table
// list itself comes from the bootstrap read the workspace already holds; a
// table's columns and indexes are fetched only the first time its node is
// expanded, and the workflow branch is fetched only the first time it is
// opened. Every fetch goes through the same authorized api.studioTable /
// api.studioWorkflows the rest of Studio uses, so the server's RBAC stays
// the sole authority over what appears here.

type LoadState<T> =
  | { status: "idle" }
  | { status: "loading" }
  | { status: "error"; message: string }
  | { status: "ready"; data: T };

function cell(result: StudioResultSet, row: (string | null)[], column: string): string | null {
  const index = result.columns.indexOf(column);
  return index < 0 ? null : row[index];
}

function rows(result: StudioResultSet | null | undefined): (string | null)[][] {
  return result?.rows ?? [];
}

function columnLeaves(detail: StudioTableDetail): { key: string; label: string; primary: boolean }[] {
  return rows(detail.columns).map((row, index) => {
    const name = cell(detail.columns, row, "column_name") ?? `column ${index + 1}`;
    const type = cell(detail.columns, row, "type");
    return {
      key: `${name}:${index}`,
      label: type ? `${name} · ${type}` : name,
      primary: (cell(detail.columns, row, "is_primary") ?? "").toLowerCase() === "true",
    };
  });
}

function indexLeaves(detail: StudioTableDetail): { key: string; label: string; muted: string | null }[] {
  return rows(detail.indexes).map((row, index) => {
    const name = cell(detail.indexes, row, "index_name") ?? `index ${index + 1}`;
    const kind = cell(detail.indexes, row, "kind");
    const unique = (cell(detail.indexes, row, "is_unique") ?? "").toLowerCase() === "true";
    const status = cell(detail.indexes, row, "status");
    const tags = [kind, unique ? "unique" : null, status && status.toLowerCase() !== "valid" ? status : null]
      .filter((tag): tag is string => Boolean(tag));
    return { key: `${name}:${index}`, label: name, muted: tags.length ? tags.join(" · ") : null };
  });
}

function foreignKeyLeaves(detail: StudioTableDetail): { key: string; label: string; muted: string | null }[] {
  const groups = new Map<string, { cols: string[]; refTable: string; refCols: string[]; onDelete: string | null }>();
  const order: string[] = [];
  rows(detail.foreign_keys).forEach((row, index) => {
    const name = cell(detail.foreign_keys, row, "constraint_name") ?? `fk ${index + 1}`;
    let group = groups.get(name);
    if (!group) {
      group = { cols: [], refTable: cell(detail.foreign_keys, row, "ref_table") ?? "?", refCols: [], onDelete: cell(detail.foreign_keys, row, "on_delete") };
      groups.set(name, group);
      order.push(name);
    }
    const col = cell(detail.foreign_keys, row, "column_name");
    const refCol = cell(detail.foreign_keys, row, "ref_column");
    if (col) group.cols.push(col);
    if (refCol) group.refCols.push(refCol);
  });
  return order.map((name) => {
    const group = groups.get(name)!;
    const target = group.refCols.length ? `${group.refTable} (${group.refCols.join(", ")})` : group.refTable;
    const action = group.onDelete && group.onDelete !== "RESTRICT" ? ` · ON DELETE ${group.onDelete}` : "";
    return { key: name, label: name, muted: `(${group.cols.join(", ")}) → ${target}${action}` };
  });
}

function triggerLeaves(detail: StudioTableDetail): { key: string; label: string; muted: string | null }[] {
  return rows(detail.triggers).map((row, index) => {
    const name = cell(detail.triggers, row, "name") ?? `trigger ${index + 1}`;
    const timing = cell(detail.triggers, row, "timing");
    const event = cell(detail.triggers, row, "event");
    const workflow = cell(detail.triggers, row, "workflow");
    const parts = [
      [timing, event].filter(Boolean).join(" ") || null,
      workflow ? `→ ${workflow}` : null,
    ].filter((part): part is string => Boolean(part));
    return { key: `${name}:${index}`, label: name, muted: parts.length ? parts.join(" ") : null };
  });
}

function workflowLeaves(overview: StudioWorkflowOverview): { key: string; label: string; muted: string | null }[] {
  return rows(overview.workflows).map((row, index) => {
    const name = cell(overview.workflows, row, "name") ?? `workflow ${index + 1}`;
    const owner = cell(overview.workflows, row, "owner");
    return { key: `${name}:${index}`, label: name, muted: owner ? `owner ${owner}` : null };
  });
}

function messageOf(error: unknown): string {
  if (error instanceof ApiError) return error.message;
  return error instanceof Error ? error.message : "Request failed.";
}

function Disclosure({
  id,
  label,
  icon,
  count,
  open,
  onToggle,
  children,
}: {
  id: string;
  label: string;
  icon?: IconName;
  count?: number;
  open: boolean;
  onToggle: () => void;
  children: ReactNode;
}) {
  return (
    <li className="nss-tree-item">
      <button
        type="button"
        className="nss-tree-branch"
        aria-expanded={open}
        aria-controls={`${id}-group`}
        onClick={onToggle}
      >
        <Icon name={open ? "chevron-down" : "chevron-right"} size={14} className="nss-tree-caret" />
        {icon ? <Icon name={icon} size={14} className="nss-tree-icon" /> : null}
        <span className="nss-tree-branch-label">{label}</span>
        {typeof count === "number" ? <span className="nss-tree-count">{count}</span> : null}
      </button>
      <ul id={`${id}-group`} className="nss-tree-group" hidden={!open}>
        {open ? children : null}
      </ul>
    </li>
  );
}

export function SchemaTree({
  tables,
  totalTables,
  filterActive,
  tablesTruncated,
  loadingTables,
  selectedTable,
  onSelectTable,
  loadTable,
  loadWorkflows,
}: {
  tables: string[];
  totalTables: number;
  filterActive: boolean;
  tablesTruncated: boolean;
  loadingTables: boolean;
  selectedTable: string | null;
  onSelectTable: (name: string) => void;
  loadTable: (name: string) => Promise<StudioTableDetail>;
  loadWorkflows: () => Promise<StudioWorkflowOverview>;
}) {
  const [expanded, setExpanded] = useState<Set<string>>(() => new Set(["tables"]));
  const [tableDetail, setTableDetail] = useState<Record<string, LoadState<StudioTableDetail>>>({});
  const [workflows, setWorkflows] = useState<LoadState<StudioWorkflowOverview>>({ status: "idle" });
  const inFlight = useRef<Set<string>>(new Set());

  const isOpen = useCallback((id: string) => expanded.has(id), [expanded]);
  const toggle = useCallback((id: string) => {
    setExpanded((current) => {
      const next = new Set(current);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  }, []);

  const fetchTable = useCallback((name: string) => {
    if (inFlight.current.has(`t:${name}`)) return;
    setTableDetail((current) => {
      const existing = current[name];
      if (existing && (existing.status === "ready" || existing.status === "loading")) return current;
      return { ...current, [name]: { status: "loading" } };
    });
    inFlight.current.add(`t:${name}`);
    loadTable(name)
      .then((data) => setTableDetail((current) => ({ ...current, [name]: { status: "ready", data } })))
      .catch((error: unknown) =>
        setTableDetail((current) => ({ ...current, [name]: { status: "error", message: messageOf(error) } })),
      )
      .finally(() => inFlight.current.delete(`t:${name}`));
  }, [loadTable]);

  const fetchWorkflows = useCallback(() => {
    if (inFlight.current.has("wf")) return;
    setWorkflows((current) => (current.status === "ready" || current.status === "loading" ? current : { status: "loading" }));
    inFlight.current.add("wf");
    loadWorkflows()
      .then((data) => setWorkflows({ status: "ready", data }))
      .catch((error: unknown) => setWorkflows({ status: "error", message: messageOf(error) }))
      .finally(() => inFlight.current.delete("wf"));
  }, [loadWorkflows]);

  const toggleTable = useCallback((name: string) => {
    const id = `table:${name}`;
    if (!isOpen(id)) fetchTable(name);
    toggle(id);
  }, [fetchTable, isOpen, toggle]);

  const toggleWorkflows = useCallback(() => {
    if (!isOpen("workflows")) fetchWorkflows();
    toggle("workflows");
  }, [fetchWorkflows, isOpen, toggle]);

  const renderTableChildren = (name: string) => {
    const state = tableDetail[name] ?? { status: "idle" as const };
    if (state.status === "loading" || state.status === "idle") {
      return (
        <li className="nss-tree-leaf" role="status">
          <Inline gap="sm" align="center">
            <Spinner size="sm" />
            <Text size="sm" variant="muted">Loading metadata…</Text>
          </Inline>
        </li>
      );
    }
    if (state.status === "error") {
      return (
        <li className="nss-tree-leaf">
          <Text size="sm" variant="danger">{state.message}</Text>
        </li>
      );
    }
    const columns = columnLeaves(state.data);
    const indexes = indexLeaves(state.data);
    const foreignKeys = foreignKeyLeaves(state.data);
    const triggers = triggerLeaves(state.data);
    return (
      <>
        <Disclosure
          id={`table-${name}-columns`}
          label="Columns"
          icon="list"
          count={columns.length}
          open={isOpen(`table:${name}:columns`)}
          onToggle={() => toggle(`table:${name}:columns`)}
        >
          {columns.length ? (
            columns.map((column) => (
              <li key={column.key} className="nss-tree-leaf">
                <span className="nss-tree-leaf-label">{column.label}</span>
                {column.primary ? <span className="nss-tree-count">PK</span> : null}
              </li>
            ))
          ) : (
            <li className="nss-tree-leaf"><Text size="sm" variant="muted">No columns visible.</Text></li>
          )}
        </Disclosure>
        <Disclosure
          id={`table-${name}-indexes`}
          label="Indexes"
          icon="layers"
          count={indexes.length}
          open={isOpen(`table:${name}:indexes`)}
          onToggle={() => toggle(`table:${name}:indexes`)}
        >
          {indexes.length ? (
            indexes.map((index) => (
              <li key={index.key} className="nss-tree-leaf">
                <span className="nss-tree-leaf-label">{index.label}</span>
                {index.muted ? <span className="nss-tree-leaf-muted">{index.muted}</span> : null}
              </li>
            ))
          ) : (
            <li className="nss-tree-leaf"><Text size="sm" variant="muted">No indexes.</Text></li>
          )}
        </Disclosure>
        <Disclosure
          id={`table-${name}-foreign-keys`}
          label="Foreign keys"
          icon="network"
          count={foreignKeys.length}
          open={isOpen(`table:${name}:foreign-keys`)}
          onToggle={() => toggle(`table:${name}:foreign-keys`)}
        >
          {foreignKeys.length ? (
            foreignKeys.map((fk) => (
              <li key={fk.key} className="nss-tree-leaf">
                <span className="nss-tree-leaf-label">{fk.label}</span>
                {fk.muted ? <span className="nss-tree-leaf-muted">{fk.muted}</span> : null}
              </li>
            ))
          ) : (
            <li className="nss-tree-leaf"><Text size="sm" variant="muted">No foreign keys.</Text></li>
          )}
        </Disclosure>
        {triggers.length ? (
          <Disclosure
            id={`table-${name}-triggers`}
            label="Triggers"
            icon="play"
            count={triggers.length}
            open={isOpen(`table:${name}:triggers`)}
            onToggle={() => toggle(`table:${name}:triggers`)}
          >
            {triggers.map((trigger) => (
              <li key={trigger.key} className="nss-tree-leaf">
                <span className="nss-tree-leaf-label">{trigger.label}</span>
                {trigger.muted ? <span className="nss-tree-leaf-muted">{trigger.muted}</span> : null}
              </li>
            ))}
          </Disclosure>
        ) : null}
      </>
    );
  };

  const renderWorkflowChildren = () => {
    if (workflows.status === "loading" || workflows.status === "idle") {
      return (
        <li className="nss-tree-leaf" role="status">
          <Inline gap="sm" align="center">
            <Spinner size="sm" />
            <Text size="sm" variant="muted">Loading workflows…</Text>
          </Inline>
        </li>
      );
    }
    if (workflows.status === "error") {
      return (
        <li className="nss-tree-leaf">
          <Text size="sm" variant="danger">{workflows.message}</Text>
        </li>
      );
    }
    const leaves = workflowLeaves(workflows.data);
    if (!leaves.length) {
      return <li className="nss-tree-leaf"><Text size="sm" variant="muted">No workflows visible.</Text></li>;
    }
    return leaves.map((leaf) => (
      <li key={leaf.key} className="nss-tree-leaf">
        <span className="nss-tree-leaf-label">{leaf.label}</span>
        {leaf.muted ? <span className="nss-tree-leaf-muted">{leaf.muted}</span> : null}
      </li>
    ));
  };

  return (
    <ul className="nss-tree" aria-label="Database objects">
      <Disclosure
        id="schema-tables"
        label="Tables"
        icon="table"
        count={tables.length}
        open={isOpen("tables")}
        onToggle={() => toggle("tables")}
      >
        {loadingTables ? (
          <li className="nss-tree-leaf" role="status">
            <Inline gap="sm" align="center">
              <Spinner size="sm" />
              <Text size="sm" variant="muted">Loading authorized tables…</Text>
            </Inline>
          </li>
        ) : tables.length ? (
          tables.map((name) => (
            <li key={name} className="nss-tree-item">
              <div className="nss-tree-row">
                <button
                  type="button"
                  className="nss-tree-caret-button"
                  aria-expanded={isOpen(`table:${name}`)}
                  aria-controls={`table-${name}-group`}
                  aria-label={`${isOpen(`table:${name}`) ? "Collapse" : "Expand"} ${name}`}
                  onClick={() => toggleTable(name)}
                >
                  <Icon name={isOpen(`table:${name}`) ? "chevron-down" : "chevron-right"} size={14} />
                </button>
                <button
                  type="button"
                  className="nss-object-button nss-tree-object"
                  aria-pressed={selectedTable === name}
                  onClick={() => {
                    onSelectTable(name);
                    if (!isOpen(`table:${name}`)) toggleTable(name);
                  }}
                >
                  <Icon name="table" size={14} className="nss-tree-icon" />
                  <span>{name}</span>
                </button>
              </div>
              <ul id={`table-${name}-group`} className="nss-tree-group" hidden={!isOpen(`table:${name}`)}>
                {isOpen(`table:${name}`) ? renderTableChildren(name) : null}
              </ul>
            </li>
          ))
        ) : (
          <li className="nss-tree-leaf">
            <EmptyState
              size="sm"
              density="compact"
              title={totalTables ? "No matching tables" : "No visible tables"}
              description={
                totalTables
                  ? "Try a different filter."
                  : "Your server permissions determine this list."
              }
            />
          </li>
        )}
        {tablesTruncated && !filterActive ? (
          <li className="nss-tree-leaf">
            <Text size="sm" variant="warning">Showing the first 1,000 authorized tables.</Text>
          </li>
        ) : null}
      </Disclosure>

      <Disclosure
        id="schema-workflows"
        label="Workflows"
        icon="activity"
        count={workflows.status === "ready" ? workflowLeaves(workflows.data).length : undefined}
        open={isOpen("workflows")}
        onToggle={toggleWorkflows}
      >
        {renderWorkflowChildren()}
      </Disclosure>
    </ul>
  );
}
