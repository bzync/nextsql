import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  Alert,
  Badge,
  Button,
  Checkbox,
  CodeBlock,
  Inline,
  Modal,
  ModalBody,
  ModalFooter,
  ModalHeader,
  ModalTitle,
  Radio,
  RadioGroup,
  Select,
  Spinner,
  Stack,
  Text,
} from "@bzync/rui";
import type { StudioTableDetail } from "../ops/api";
import { Icon } from "../shared/icons";
import {
  buildSchemaDiffMigrationSQL,
  computeTableSchemaDiff,
  extractTableSchema,
  parseCreateTableDDL,
  type ColumnDiffItem,
  type ForeignKeyDiffItem,
  type IndexDiffItem,
  type SchemaDiffStatus,
  type TableSchema,
  type TableSchemaDiffResult,
} from "./resultTools";

type TargetMode = "table" | "ddl";
type DiffCategory = "overview" | "columns" | "indexes" | "foreignKeys" | "migration";

const STATUS_BADGE: Record<SchemaDiffStatus, { variant: "success" | "error" | "warning" | "muted"; label: string }> = {
  added: { variant: "success", label: "Added" },
  dropped: { variant: "error", label: "Dropped" },
  altered: { variant: "warning", label: "Altered" },
  identical: { variant: "muted", label: "Identical" },
};

export function SchemaDiffExplorer({
  onClose,
  onInsert,
  tables,
  initialTable,
  initialDetail,
  loadTable,
}: {
  onClose: () => void;
  onInsert: (sql: string) => void;
  tables: string[];
  initialTable: string | null;
  initialDetail: StudioTableDetail | null;
  loadTable: (name: string) => Promise<StudioTableDetail>;
}) {
  const firstTable = initialTable && tables.includes(initialTable) ? initialTable : tables[0] ?? "";
  const secondTable = tables.find((t) => t !== firstTable) ?? firstTable;

  const [baseTableName, setBaseTableName] = useState<string>(firstTable);
  const [targetMode, setTargetMode] = useState<TargetMode>("table");
  const [targetTableName, setTargetTableName] = useState<string>(secondTable);
  const [targetDDL, setTargetDDL] = useState<string>("");
  const [activeTab, setActiveTab] = useState<DiffCategory>("overview");
  const [includeDrops, setIncludeDrops] = useState<boolean>(false);
  const [showIdentical, setShowIdentical] = useState<boolean>(false);
  const [copied, setCopied] = useState<boolean>(false);

  // Cache of loaded table details to prevent redundant network fetches
  const cache = useRef<Map<string, StudioTableDetail>>(new Map());
  if (initialDetail?.name && !cache.current.has(initialDetail.name)) {
    cache.current.set(initialDetail.name, initialDetail);
  }

  const [baseDetail, setBaseDetail] = useState<StudioTableDetail | null>(
    initialDetail?.name === firstTable ? initialDetail : null,
  );
  const [targetDetail, setTargetDetail] = useState<StudioTableDetail | null>(
    initialDetail?.name === secondTable ? initialDetail : null,
  );
  const [loadingBase, setLoadingBase] = useState<boolean>(false);
  const [loadingTarget, setLoadingTarget] = useState<boolean>(false);
  const [loadError, setLoadError] = useState<string | null>(null);

  // Load Base Table Detail
  useEffect(() => {
    if (!baseTableName) {
      setBaseDetail(null);
      return;
    }
    const cached = cache.current.get(baseTableName);
    if (cached) {
      setBaseDetail(cached);
      return;
    }
    let active = true;
    setLoadingBase(true);
    setLoadError(null);
    loadTable(baseTableName)
      .then((detail) => {
        if (!active) return;
        cache.current.set(baseTableName, detail);
        setBaseDetail(detail);
      })
      .catch((err) => {
        if (!active) return;
        setLoadError(err instanceof Error ? err.message : String(err));
      })
      .finally(() => {
        if (active) setLoadingBase(false);
      });
    return () => {
      active = false;
    };
  }, [baseTableName, loadTable]);

  // Load Target Table Detail
  useEffect(() => {
    if (targetMode !== "table" || !targetTableName) {
      setTargetDetail(null);
      return;
    }
    const cached = cache.current.get(targetTableName);
    if (cached) {
      setTargetDetail(cached);
      return;
    }
    let active = true;
    setLoadingTarget(true);
    setLoadError(null);
    loadTable(targetTableName)
      .then((detail) => {
        if (!active) return;
        cache.current.set(targetTableName, detail);
        setTargetDetail(detail);
      })
      .catch((err) => {
        if (!active) return;
        setLoadError(err instanceof Error ? err.message : String(err));
      })
      .finally(() => {
        if (active) setLoadingTarget(false);
      });
    return () => {
      active = false;
    };
  }, [targetMode, targetTableName, loadTable]);

  // Extract schemas
  const baseSchema = useMemo<TableSchema | null>(() => {
    return extractTableSchema(baseDetail);
  }, [baseDetail]);

  const targetSchema = useMemo<TableSchema | null>(() => {
    if (targetMode === "table") {
      return extractTableSchema(targetDetail);
    }
    if (targetMode === "ddl" && targetDDL.trim().length > 0) {
      return parseCreateTableDDL(targetDDL);
    }
    return null;
  }, [targetMode, targetDetail, targetDDL]);

  // Compute diff
  const diffResult = useMemo<TableSchemaDiffResult>(() => {
    return computeTableSchemaDiff(baseSchema, targetSchema);
  }, [baseSchema, targetSchema]);

  // Generate migration SQL
  const migration = useMemo(() => {
    return buildSchemaDiffMigrationSQL(diffResult, {
      includeDrops,
      targetTableName: baseTableName,
    });
  }, [diffResult, includeDrops, baseTableName]);

  // Swap base and target
  const handleSwap = useCallback(() => {
    if (targetMode !== "table") return;
    const prevBase = baseTableName;
    const prevTarget = targetTableName;
    setBaseTableName(prevTarget);
    setTargetTableName(prevBase);
  }, [targetMode, baseTableName, targetTableName]);

  // Copy Migration SQL
  const handleCopySQL = useCallback(() => {
    if (!migration.sql) return;
    navigator.clipboard.writeText(migration.sql).then(() => {
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    });
  }, [migration.sql]);

  // Insert into SQL editor
  const handleOpenInEditor = useCallback(() => {
    if (!migration.sql) return;
    onInsert(migration.sql);
    onClose();
  }, [migration.sql, onInsert, onClose]);

  const tableSelectOptions = useMemo(() => {
    return tables.map((t) => ({ value: t, label: t }));
  }, [tables]);

  const filteredColumns = useMemo(() => {
    if (showIdentical) return diffResult.columns;
    return diffResult.columns.filter((c) => c.status !== "identical");
  }, [diffResult.columns, showIdentical]);

  const filteredIndexes = useMemo(() => {
    if (showIdentical) return diffResult.indexes;
    return diffResult.indexes.filter((i) => i.status !== "identical");
  }, [diffResult.indexes, showIdentical]);

  const filteredForeignKeys = useMemo(() => {
    if (showIdentical) return diffResult.foreignKeys;
    return diffResult.foreignKeys.filter((f) => f.status !== "identical");
  }, [diffResult.foreignKeys, showIdentical]);

  const loading = loadingBase || loadingTarget;

  return (
    <Modal open onClose={onClose} size="lg" ariaLabel="Schema diff and migration generator" scrollable>
      <ModalHeader>
        <ModalTitle>Schema Diff & Migration Generator</ModalTitle>
      </ModalHeader>
      <ModalBody scrollable>
        <Stack gap="md">
          <Alert variant="info" title="Review-First Architecture">
            NextSQL Studio never executes DDL silently. Compare schema definitions, inspect drift, and
            generate native migration SQL to review and run in the SQL editor under operator authorization.
          </Alert>

          {loadError ? (
            <Alert variant="error" title="Failed to load table schema" role="alert">
              {loadError}
            </Alert>
          ) : null}

          {/* Controls Card */}
          <div className="nss-diff-controls-card">
            <Stack gap="sm">
              <Inline gap="md" align="end" wrap>
                <div className="nss-diff-select-group">
                  <Select
                    label="Source (Base) Table"
                    value={baseTableName}
                    onChange={(val) => setBaseTableName(val)}
                    options={tableSelectOptions}
                    disabled={tables.length === 0}
                  />
                </div>

                {targetMode === "table" ? (
                  <Button
                    size="sm"
                    variant="outline"
                    icon={<Icon name="refresh" size={14} />}
                    onClick={handleSwap}
                    title="Swap base and target tables"
                    aria-label="Swap base and target tables"
                  >
                    Swap (⇄)
                  </Button>
                ) : null}

                <div className="nss-diff-target-toggle">
                  <RadioGroup
                    label="Compare Against"
                    orientation="horizontal"
                    value={targetMode}
                    onChange={(val) => setTargetMode(val as TargetMode)}
                  >
                    <Radio value="table" label="Another Table" />
                    <Radio value="ddl" label="Custom DDL (SQL)" />
                  </RadioGroup>
                </div>

                {targetMode === "table" ? (
                  <div className="nss-diff-select-group">
                    <Select
                      label="Target Table"
                      value={targetTableName}
                      onChange={(val) => setTargetTableName(val)}
                      options={tableSelectOptions}
                      disabled={tables.length === 0}
                    />
                  </div>
                ) : null}
              </Inline>

              {targetMode === "ddl" ? (
                <div className="nss-diff-ddl-input">
                  <label htmlFor="nss-diff-target-ddl" className="nss-diff-label">
                    Target Table <code>CREATE TABLE</code> DDL:
                  </label>
                  <textarea
                    id="nss-diff-target-ddl"
                    className="nss-diff-textarea"
                    rows={6}
                    placeholder="CREATE TABLE target_table ( id INT64 PRIMARY KEY, name STRING NOT NULL );"
                    value={targetDDL}
                    onChange={(e) => setTargetDDL(e.target.value)}
                    spellCheck={false}
                  />
                  {targetDDL.trim().length > 0 && !targetSchema ? (
                    <Text size="xs" variant="muted">
                      Unable to parse DDL syntax. Ensure it follows <code>CREATE TABLE name ( ... );</code> format.
                    </Text>
                  ) : null}
                </div>
              ) : null}

              <Inline gap="lg" align="center" wrap>
                <Checkbox
                  label="Include destructive DROP statements"
                  checked={includeDrops}
                  onChange={() => setIncludeDrops((v) => !v)}
                />
                <Checkbox
                  label="Show identical objects"
                  checked={showIdentical}
                  onChange={() => setShowIdentical((v) => !v)}
                />
              </Inline>
            </Stack>
          </div>

          {/* Loading Indicator */}
          {loading ? (
            <Inline gap="sm" align="center" justify="center" className="nss-diff-loading">
              <Spinner size="sm" />
              <Text size="sm" variant="muted">
                Inspecting table catalog metadata…
              </Text>
            </Inline>
          ) : null}

          {/* Diff Summary Header */}
          {!loading ? (
            <div className="nss-diff-summary-bar">
              {diffResult.identical ? (
                <Alert variant="success" title="Schemas Are Identical">
                  The base table <code>{baseTableName}</code> and target match across all columns, types, nullability,
                  defaults, primary keys, indexes, and constraints.
                </Alert>
              ) : (
                <Stack gap="xs">
                  <Inline gap="sm" align="center" wrap>
                    <Text size="sm" weight="semibold">
                      Diff Summary:
                    </Text>
                    <Badge variant="warning" size="sm">
                      {diffResult.counts.totalChanges} difference{diffResult.counts.totalChanges === 1 ? "" : "s"}
                    </Badge>
                    {diffResult.counts.columnsAdded > 0 ? (
                      <Badge variant="success" size="sm">
                        +{diffResult.counts.columnsAdded} col added
                      </Badge>
                    ) : null}
                    {diffResult.counts.columnsAltered > 0 ? (
                      <Badge variant="warning" size="sm">
                        ~{diffResult.counts.columnsAltered} col modified
                      </Badge>
                    ) : null}
                    {diffResult.counts.columnsDropped > 0 ? (
                      <Badge variant="error" size="sm">
                        -{diffResult.counts.columnsDropped} col dropped
                      </Badge>
                    ) : null}
                    {diffResult.counts.indexesAdded > 0 ? (
                      <Badge variant="success" size="sm">
                        +{diffResult.counts.indexesAdded} idx added
                      </Badge>
                    ) : null}
                    {diffResult.counts.indexesDropped > 0 ? (
                      <Badge variant="error" size="sm">
                        -{diffResult.counts.indexesDropped} idx dropped
                      </Badge>
                    ) : null}
                    {diffResult.counts.foreignKeysAdded > 0 ? (
                      <Badge variant="success" size="sm">
                        +{diffResult.counts.foreignKeysAdded} FK added
                      </Badge>
                    ) : null}
                  </Inline>
                </Stack>
              )}
            </div>
          ) : null}

          {/* Category Tabs */}
          <div className="nss-diff-tablist" role="tablist" aria-label="Diff categories">
            <Button
              size="sm"
              variant={activeTab === "overview" ? "primary" : "outline"}
              role="tab"
              aria-selected={activeTab === "overview"}
              onClick={() => setActiveTab("overview")}
            >
              Overview
            </Button>
            <Button
              size="sm"
              variant={activeTab === "columns" ? "primary" : "outline"}
              role="tab"
              aria-selected={activeTab === "columns"}
              onClick={() => setActiveTab("columns")}
            >
              Columns ({filteredColumns.length})
            </Button>
            <Button
              size="sm"
              variant={activeTab === "indexes" ? "primary" : "outline"}
              role="tab"
              aria-selected={activeTab === "indexes"}
              onClick={() => setActiveTab("indexes")}
            >
              Indexes ({filteredIndexes.length})
            </Button>
            <Button
              size="sm"
              variant={activeTab === "foreignKeys" ? "primary" : "outline"}
              role="tab"
              aria-selected={activeTab === "foreignKeys"}
              onClick={() => setActiveTab("foreignKeys")}
            >
              Foreign Keys ({filteredForeignKeys.length})
            </Button>
            <Button
              size="sm"
              variant={activeTab === "migration" ? "primary" : "outline"}
              role="tab"
              aria-selected={activeTab === "migration"}
              onClick={() => setActiveTab("migration")}
            >
              Migration SQL ({migration.statementsCount})
            </Button>
          </div>

          {/* Tab Panes */}
          <div className="nss-diff-tab-content">
            {activeTab === "overview" ? (
              <Stack gap="md">
                <div className="nss-diff-overview-grid">
                  <div className="nss-diff-overview-card">
                    <Text size="sm" weight="semibold">
                      Base (Source) Table: <code>{baseTableName || "(none)"}</code>
                    </Text>
                    <Text size="xs" variant="muted">
                      {baseSchema?.columns.length ?? 0} columns · {baseSchema?.indexes.length ?? 0} indexes ·{" "}
                      {baseSchema?.foreignKeys.length ?? 0} foreign keys
                    </Text>
                  </div>
                  <div className="nss-diff-overview-card">
                    <Text size="sm" weight="semibold">
                      Target: <code>{targetMode === "table" ? targetTableName || "(none)" : "Custom DDL"}</code>
                    </Text>
                    <Text size="xs" variant="muted">
                      {targetSchema?.columns.length ?? 0} columns · {targetSchema?.indexes.length ?? 0} indexes ·{" "}
                      {targetSchema?.foreignKeys.length ?? 0} foreign keys
                    </Text>
                  </div>
                </div>

                {filteredColumns.length > 0 ? (
                  <Stack gap="xs">
                    <Text size="sm" weight="semibold">
                      Column Changes ({filteredColumns.length}):
                    </Text>
                    <ColumnsDiffTable items={filteredColumns} />
                  </Stack>
                ) : null}

                {filteredIndexes.length > 0 ? (
                  <Stack gap="xs">
                    <Text size="sm" weight="semibold">
                      Index Changes ({filteredIndexes.length}):
                    </Text>
                    <IndexesDiffTable items={filteredIndexes} />
                  </Stack>
                ) : null}

                {filteredForeignKeys.length > 0 ? (
                  <Stack gap="xs">
                    <Text size="sm" weight="semibold">
                      Foreign Key Changes ({filteredForeignKeys.length}):
                    </Text>
                    <ForeignKeysDiffTable items={filteredForeignKeys} />
                  </Stack>
                ) : null}
              </Stack>
            ) : null}

            {activeTab === "columns" ? (
              <Stack gap="sm">
                {filteredColumns.length === 0 ? (
                  <Text size="sm" variant="muted">
                    No column differences found. Toggle &quot;Show identical objects&quot; to inspect matching columns.
                  </Text>
                ) : (
                  <ColumnsDiffTable items={filteredColumns} />
                )}
              </Stack>
            ) : null}

            {activeTab === "indexes" ? (
              <Stack gap="sm">
                {filteredIndexes.length === 0 ? (
                  <Text size="sm" variant="muted">
                    No index differences found. Toggle &quot;Show identical objects&quot; to inspect matching indexes.
                  </Text>
                ) : (
                  <IndexesDiffTable items={filteredIndexes} />
                )}
              </Stack>
            ) : null}

            {activeTab === "foreignKeys" ? (
              <Stack gap="sm">
                {filteredForeignKeys.length === 0 ? (
                  <Text size="sm" variant="muted">
                    No foreign key differences found. Toggle &quot;Show identical objects&quot; to inspect matching foreign keys.
                  </Text>
                ) : (
                  <ForeignKeysDiffTable items={filteredForeignKeys} />
                )}
              </Stack>
            ) : null}

            {activeTab === "migration" ? (
              <Stack gap="sm">
                {migration.warnings.length > 0 ? (
                  <Alert variant="warning" title="Migration Safety Warnings">
                    <ul className="nss-diff-warnings-list">
                      {migration.warnings.map((w, idx) => (
                        <li key={idx}>{w}</li>
                      ))}
                    </ul>
                  </Alert>
                ) : null}

                <div className="nss-diff-code-wrapper">
                  <CodeBlock code={migration.sql} language="sql" />
                </div>
              </Stack>
            ) : null}
          </div>
        </Stack>
      </ModalBody>
      <ModalFooter>
        <Inline gap="sm" justify="between" align="center" className="w-full">
          <div role="status" aria-live="polite">
            {copied ? (
              <Text size="xs" variant="success">
                Migration SQL copied to clipboard!
              </Text>
            ) : null}
          </div>
          <Inline gap="sm">
            <Button size="sm" variant="outline" onClick={onClose}>
              Close
            </Button>
            <Button
              size="sm"
              variant="outline"
              onClick={handleCopySQL}
              disabled={!migration.sql || diffResult.identical}
              icon={<Icon name="copy" size={14} />}
            >
              {copied ? "Copied!" : "Copy SQL"}
            </Button>
            <Button
              size="sm"
              variant="primary"
              onClick={handleOpenInEditor}
              disabled={!migration.sql || diffResult.identical}
              icon={<Icon name="terminal" size={14} />}
            >
              Open in SQL Editor
            </Button>
          </Inline>
        </Inline>
      </ModalFooter>
    </Modal>
  );
}

function ColumnsDiffTable({ items }: { items: ColumnDiffItem[] }) {
  return (
    <div className="nss-diff-table-container">
      <table className="nss-diff-table" aria-label="Column differences">
        <thead>
          <tr>
            <th scope="col" style={{ width: "90px" }}>
              Status
            </th>
            <th scope="col">Column</th>
            <th scope="col">Source (Base)</th>
            <th scope="col">Target</th>
            <th scope="col">Changes</th>
          </tr>
        </thead>
        <tbody>
          {items.map((item) => {
            const badge = STATUS_BADGE[item.status];
            return (
              <tr key={item.name} className={`nss-diff-row--${item.status}`}>
                <td>
                  <Badge variant={badge.variant} size="sm">
                    {badge.label}
                  </Badge>
                </td>
                <td>
                  <code>{item.name}</code>
                </td>
                <td>
                  {item.base ? (
                    <span className="nss-diff-spec">
                      <code>{item.base.type}</code>
                      {item.base.isPrimary ? " PK" : ""}
                      {item.base.notNull ? " NOT NULL" : ""}
                      {item.base.defaultValue ? ` DEFAULT ${item.base.defaultValue}` : ""}
                    </span>
                  ) : (
                    <span className="nss-diff-empty">&mdash;</span>
                  )}
                </td>
                <td>
                  {item.target ? (
                    <span className="nss-diff-spec">
                      <code>{item.target.type}</code>
                      {item.target.isPrimary ? " PK" : ""}
                      {item.target.notNull ? " NOT NULL" : ""}
                      {item.target.defaultValue ? ` DEFAULT ${item.target.defaultValue}` : ""}
                    </span>
                  ) : (
                    <span className="nss-diff-empty">&mdash;</span>
                  )}
                </td>
                <td>
                  {item.changes.length > 0 ? (
                    <ul className="nss-diff-change-list">
                      {item.changes.map((c, i) => (
                        <li key={i}>{c}</li>
                      ))}
                    </ul>
                  ) : (
                    <span className="nss-diff-empty">Matching</span>
                  )}
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}

function IndexesDiffTable({ items }: { items: IndexDiffItem[] }) {
  return (
    <div className="nss-diff-table-container">
      <table className="nss-diff-table" aria-label="Index differences">
        <thead>
          <tr>
            <th scope="col" style={{ width: "90px" }}>
              Status
            </th>
            <th scope="col">Index Name</th>
            <th scope="col">Source (Base)</th>
            <th scope="col">Target</th>
            <th scope="col">Changes</th>
          </tr>
        </thead>
        <tbody>
          {items.map((item) => {
            const badge = STATUS_BADGE[item.status];
            return (
              <tr key={item.name} className={`nss-diff-row--${item.status}`}>
                <td>
                  <Badge variant={badge.variant} size="sm">
                    {badge.label}
                  </Badge>
                </td>
                <td>
                  <code>{item.name}</code>
                </td>
                <td>
                  {item.base ? (
                    <span className="nss-diff-spec">
                      {item.base.isUnique ? "UNIQUE " : ""}
                      <code>({item.base.columns.join(", ")})</code>
                      {item.base.predicate ? ` WHERE ${item.base.predicate}` : ""}
                    </span>
                  ) : (
                    <span className="nss-diff-empty">&mdash;</span>
                  )}
                </td>
                <td>
                  {item.target ? (
                    <span className="nss-diff-spec">
                      {item.target.isUnique ? "UNIQUE " : ""}
                      <code>({item.target.columns.join(", ")})</code>
                      {item.target.predicate ? ` WHERE ${item.target.predicate}` : ""}
                    </span>
                  ) : (
                    <span className="nss-diff-empty">&mdash;</span>
                  )}
                </td>
                <td>
                  {item.changes.length > 0 ? (
                    <ul className="nss-diff-change-list">
                      {item.changes.map((c, i) => (
                        <li key={i}>{c}</li>
                      ))}
                    </ul>
                  ) : (
                    <span className="nss-diff-empty">Matching</span>
                  )}
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}

function ForeignKeysDiffTable({ items }: { items: ForeignKeyDiffItem[] }) {
  return (
    <div className="nss-diff-table-container">
      <table className="nss-diff-table" aria-label="Foreign key differences">
        <thead>
          <tr>
            <th scope="col" style={{ width: "90px" }}>
              Status
            </th>
            <th scope="col">Constraint Name</th>
            <th scope="col">Source (Base)</th>
            <th scope="col">Target</th>
            <th scope="col">Changes</th>
          </tr>
        </thead>
        <tbody>
          {items.map((item) => {
            const badge = STATUS_BADGE[item.status];
            return (
              <tr key={item.name} className={`nss-diff-row--${item.status}`}>
                <td>
                  <Badge variant={badge.variant} size="sm">
                    {badge.label}
                  </Badge>
                </td>
                <td>
                  <code>{item.name}</code>
                </td>
                <td>
                  {item.base ? (
                    <span className="nss-diff-spec">
                      <code>{item.base.columnName}</code> &rarr; <code>{item.base.refTable}({item.base.refColumn})</code>
                      {item.base.onDelete !== "RESTRICT" ? ` ON DELETE ${item.base.onDelete}` : ""}
                    </span>
                  ) : (
                    <span className="nss-diff-empty">&mdash;</span>
                  )}
                </td>
                <td>
                  {item.target ? (
                    <span className="nss-diff-spec">
                      <code>{item.target.columnName}</code> &rarr; <code>{item.target.refTable}({item.target.refColumn})</code>
                      {item.target.onDelete !== "RESTRICT" ? ` ON DELETE ${item.target.onDelete}` : ""}
                    </span>
                  ) : (
                    <span className="nss-diff-empty">&mdash;</span>
                  )}
                </td>
                <td>
                  {item.changes.length > 0 ? (
                    <ul className="nss-diff-change-list">
                      {item.changes.map((c, i) => (
                        <li key={i}>{c}</li>
                      ))}
                    </ul>
                  ) : (
                    <span className="nss-diff-empty">Matching</span>
                  )}
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}
