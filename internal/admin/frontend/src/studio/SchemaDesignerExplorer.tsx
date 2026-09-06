import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  Alert,
  Badge,
  Button,
  Checkbox,
  CodeBlock,
  CopyButton,
  Inline,
  Input,
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
import {
  DESIGNER_TYPE_KINDS,
  DESIGNER_TYPE_LABELS,
  MAX_DESIGNER_CHAR_LEN,
  MAX_DESIGNER_COLUMNS,
  MAX_DESIGNER_DECIMAL_PRECISION,
  MAX_DESIGNER_VECTOR_DIM,
  buildCreateIndexSQL,
  buildCreateTableSQL,
  dataGenColumns,
  defaultCreateIndexState,
  defaultCreateTableState,
  designerIndexColumnClass,
  designerIndexKindOptions,
  designerTypeNeedsDecimal,
  designerTypeNeedsDimension,
  designerTypeNeedsLength,
  designerTypeParamDefault,
  newDesignerColumn,
  type CreateIndexState,
  type CreateTableState,
  type DesignerColumn,
  type DesignerDefaultKind,
  type DesignerFKAction,
  type DesignerFTAnalyzer,
  type DesignerIndexKind,
  type DesignerTypeKind,
  type DesignerVectorMethod,
  type DesignerVectorQuant,
} from "./resultTools";

export type SchemaDesignerMode = "table" | "index";

const FK_ACTIONS: DesignerFKAction[] = ["RESTRICT", "CASCADE", "SET NULL", "SET DEFAULT"];
const DEFAULT_KINDS: { value: DesignerDefaultKind; label: string }[] = [
  { value: "none", label: "None" },
  { value: "uuid", label: "UUID()" },
  { value: "now", label: "NOW()" },
  { value: "ai", label: "AI()" },
  { value: "literal", label: "Literal" },
];
const INDEX_KIND_LABEL: Record<DesignerIndexKind, string> = {
  btree: "B+Tree",
  unique: "UNIQUE B+Tree",
  fulltext: "FULLTEXT",
  vector: "VECTOR",
  spatial: "SPATIAL",
};
const VECTOR_METHODS: DesignerVectorMethod[] = ["HNSW", "IVF", "IVFPQ", "SPARSE"];
const ANALYZERS: DesignerFTAnalyzer[] = ["simple", "english", "french", "german", "spanish"];

function toggleName(list: string[], name: string): string[] {
  return list.includes(name) ? list.filter((item) => item !== name) : [...list, name];
}

export function SchemaDesignerExplorer({
  onClose,
  onInsert,
  tables,
  initialTable,
  initialDetail,
  loadTable,
  initialMode = "table",
}: {
  onClose: () => void;
  onInsert: (sql: string) => void;
  tables: string[];
  initialTable: string | null;
  initialDetail: StudioTableDetail | null;
  loadTable: (name: string) => Promise<StudioTableDetail>;
  initialMode?: SchemaDesignerMode;
}) {
  const firstTable = initialTable && tables.includes(initialTable) ? initialTable : tables[0] ?? "";
  const [mode, setMode] = useState<SchemaDesignerMode>(initialMode);
  const [tableState, setTableState] = useState<CreateTableState>(defaultCreateTableState);
  const [indexState, setIndexState] = useState<CreateIndexState>(() => defaultCreateIndexState(firstTable));
  const [detail, setDetail] = useState<StudioTableDetail | null>(
    initialDetail?.name === firstTable ? initialDetail : null,
  );
  const [loading, setLoading] = useState(Boolean(firstTable && initialDetail?.name !== firstTable && initialMode === "index"));
  const [loadError, setLoadError] = useState<string | null>(null);
  const request = useRef(0);
  const nextColumn = useRef(tableState.columns.length + 1);

  const requestTable = useCallback(async (name: string, known?: StudioTableDetail | null) => {
    const id = ++request.current;
    setIndexState((current) => ({ ...defaultCreateIndexState(name), name: current.name, kind: current.kind }));
    setDetail(null);
    setLoadError(null);
    if (!name) {
      setLoading(false);
      return;
    }
    if (known?.name === name) {
      setLoading(false);
      setDetail(known);
      return;
    }
    setLoading(true);
    try {
      const loaded = await loadTable(name);
      if (request.current !== id) return;
      setDetail(loaded);
    } catch (error) {
      if (request.current !== id) return;
      setLoadError(error instanceof Error ? error.message : String(error));
    } finally {
      if (request.current === id) setLoading(false);
    }
  }, [loadTable]);

  useEffect(() => {
    if (initialMode === "index" && firstTable) void requestTable(firstTable, initialDetail);
    return () => { request.current += 1; };
    // Mounted fresh for every open; initialize once from the workspace snapshot.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const catalogColumns = useMemo(
    () => dataGenColumns(detail).map((column) => ({ name: column.name, type: column.type })),
    [detail],
  );
  const indexKinds = useMemo(() => designerIndexKindOptions(catalogColumns), [catalogColumns]);
  const tableBuilt = useMemo(() => buildCreateTableSQL(tableState), [tableState]);
  const indexBuilt = useMemo(
    () => buildCreateIndexSQL(indexState, catalogColumns),
    [indexState, catalogColumns],
  );
  const built = mode === "table" ? tableBuilt : indexBuilt;

  const changeMode = (next: SchemaDesignerMode) => {
    setMode(next);
    if (next === "index" && indexState.table && !detail && !loading) {
      void requestTable(indexState.table, initialDetail?.name === indexState.table ? initialDetail : null);
    }
  };

  const patchTable = (patch: Partial<CreateTableState>) => setTableState((current) => ({ ...current, ...patch }));
  const patchIndex = (patch: Partial<CreateIndexState>) => setIndexState((current) => ({ ...current, ...patch }));

  const patchColumn = (id: string, patch: Partial<DesignerColumn>) => {
    setTableState((current) => ({
      ...current,
      columns: current.columns.map((column) => {
        if (column.id !== id) return column;
        const typeKind = patch.typeKind ?? column.typeKind;
        const next: DesignerColumn = { ...column, ...patch, typeKind };
        if (patch.typeKind && patch.typeKind !== column.typeKind) {
          next.typeParam = designerTypeParamDefault(patch.typeKind);
          next.typeScale = patch.typeKind === "DECIMAL" ? 0 : 0;
        }
        return next;
      }),
    }));
  };

  const addColumn = () => {
    if (tableState.columns.length >= MAX_DESIGNER_COLUMNS) return;
    const id = `c${nextColumn.current++}`;
    setTableState((current) => ({ ...current, columns: [...current.columns, newDesignerColumn(id)] }));
  };

  const removeColumn = (id: string) => {
    setTableState((current) => {
      const columns = current.columns.filter((column) => column.id !== id);
      const names = new Set(columns.map((column) => column.name.trim()));
      return {
        ...current,
        columns,
        fkColumns: current.fkColumns.filter((name) => names.has(name)),
      };
    });
  };

  const insert = () => {
    if (!built.sql) return;
    onInsert(built.sql);
    onClose();
  };

  const title = mode === "table" ? "Design a table" : "Design an index";

  return (
    <Modal open onClose={onClose} size="lg" ariaLabel={title} scrollable>
      <ModalHeader>
        <ModalTitle>{title}</ModalTitle>
      </ModalHeader>
      <ModalBody scrollable>
        <Stack gap="md">
          <Text size="sm" variant="muted">
            Builds a native <code>CREATE TABLE</code> or <code>CREATE INDEX</code> statement from this form and
            loads it into the active editor tab (replacing its contents) — it never runs anything here. Review the
            SQL, then run it so server-side RBAC and validation stay authoritative.
          </Text>

          <RadioGroup
            label="Object"
            orientation="horizontal"
            value={mode}
            onChange={(value) => changeMode(value as SchemaDesignerMode)}
          >
            <Radio value="table" label="Table" />
            <Radio value="index" label="Index" />
          </RadioGroup>

          {mode === "table" ? (
            <TableForm
              state={tableState}
              tables={tables}
              onPatch={patchTable}
              onPatchColumn={patchColumn}
              onAddColumn={addColumn}
              onRemoveColumn={removeColumn}
            />
          ) : (
            <IndexForm
              state={indexState}
              tables={tables}
              catalogColumns={catalogColumns}
              indexKinds={indexKinds}
              loading={loading}
              loadError={loadError}
              onPatch={patchIndex}
              onChangeTable={(name) => void requestTable(name)}
            />
          )}

          {built.sql ? (
            <>
              <div className="nss-datagen-sql-preview" tabIndex={0} aria-label="Generated DDL">
                <CodeBlock code={built.sql} language="sql" />
              </div>
              <Inline gap="sm" align="center" wrap>
                <CopyButton value={built.sql} label="Copy SQL" size="sm" />
              </Inline>
            </>
          ) : (
            <Alert variant="warning" title="Not ready">{built.error}</Alert>
          )}
        </Stack>
      </ModalBody>
      <ModalFooter>
        <Button variant="ghost" onClick={onClose}>Cancel</Button>
        <Button variant="primary" onClick={insert} disabled={!built.sql}>Insert into editor</Button>
      </ModalFooter>
    </Modal>
  );
}

function TableForm({
  state,
  tables,
  onPatch,
  onPatchColumn,
  onAddColumn,
  onRemoveColumn,
}: {
  state: CreateTableState;
  tables: string[];
  onPatch: (patch: Partial<CreateTableState>) => void;
  onPatchColumn: (id: string, patch: Partial<DesignerColumn>) => void;
  onAddColumn: () => void;
  onRemoveColumn: (id: string) => void;
}) {
  const namedColumns = state.columns.filter((column) => column.name.trim());
  return (
    <Stack gap="md">
      <Input
        id="designer-table-name"
        label="Table name"
        value={state.table}
        onChange={(event) => onPatch({ table: event.currentTarget.value })}
      />

      <Stack gap="sm" aria-label="Columns">
        {state.columns.map((column, index) => (
          <fieldset key={column.id} className="nss-dml-fieldset">
            <legend>Column {index + 1}</legend>
            <Stack gap="sm">
              <Inline gap="sm" wrap>
                <Input
                  id={`designer-col-name-${column.id}`}
                  label="Name"
                  value={column.name}
                  onChange={(event) => onPatchColumn(column.id, { name: event.currentTarget.value })}
                />
                <Select
                  id={`designer-col-type-${column.id}`}
                  label="Type"
                  options={DESIGNER_TYPE_KINDS.map((kind) => ({ value: kind, label: DESIGNER_TYPE_LABELS[kind] }))}
                  value={column.typeKind}
                  onChange={(value) => onPatchColumn(column.id, { typeKind: value as DesignerTypeKind })}
                />
                {designerTypeNeedsLength(column.typeKind) ? (
                  <Input
                    id={`designer-col-len-${column.id}`}
                    label="Length"
                    type="number"
                    min="1"
                    max={String(MAX_DESIGNER_CHAR_LEN)}
                    value={String(column.typeParam)}
                    onChange={(event) => onPatchColumn(column.id, { typeParam: Number(event.currentTarget.value) })}
                  />
                ) : null}
                {designerTypeNeedsDecimal(column.typeKind) ? (
                  <>
                    <Input
                      id={`designer-col-prec-${column.id}`}
                      label="Precision"
                      type="number"
                      min="1"
                      max={String(MAX_DESIGNER_DECIMAL_PRECISION)}
                      value={String(column.typeParam)}
                      onChange={(event) => onPatchColumn(column.id, { typeParam: Number(event.currentTarget.value) })}
                    />
                    <Input
                      id={`designer-col-scale-${column.id}`}
                      label="Scale"
                      type="number"
                      min="0"
                      max={String(column.typeParam)}
                      value={String(column.typeScale)}
                      onChange={(event) => onPatchColumn(column.id, { typeScale: Number(event.currentTarget.value) })}
                    />
                  </>
                ) : null}
                {designerTypeNeedsDimension(column.typeKind) ? (
                  <Input
                    id={`designer-col-dim-${column.id}`}
                    label="Dimensions"
                    type="number"
                    min="1"
                    max={String(column.typeKind === "SPARSEVECTOR" ? 65535 : MAX_DESIGNER_VECTOR_DIM)}
                    value={String(column.typeParam)}
                    onChange={(event) => onPatchColumn(column.id, { typeParam: Number(event.currentTarget.value) })}
                  />
                ) : null}
              </Inline>
              <Inline gap="sm" wrap align="center">
                <Checkbox
                  label="PRIMARY KEY"
                  checked={column.primaryKey}
                  onChange={() => onPatchColumn(column.id, { primaryKey: !column.primaryKey, notNull: true })}
                />
                <Checkbox
                  label="NOT NULL"
                  checked={column.notNull || column.primaryKey}
                  disabled={column.primaryKey}
                  onChange={() => onPatchColumn(column.id, { notNull: !column.notNull })}
                />
                <Select
                  id={`designer-col-def-${column.id}`}
                  label="DEFAULT"
                  options={DEFAULT_KINDS}
                  value={column.defaultKind}
                  onChange={(value) => onPatchColumn(column.id, { defaultKind: value as DesignerDefaultKind })}
                />
                {column.defaultKind === "literal" ? (
                  <Input
                    id={`designer-col-lit-${column.id}`}
                    label="Literal"
                    value={column.defaultLiteral}
                    onChange={(event) => onPatchColumn(column.id, { defaultLiteral: event.currentTarget.value })}
                  />
                ) : null}
                {column.primaryKey ? <Badge variant="muted">PK</Badge> : null}
                <Button
                  variant="ghost"
                  size="sm"
                  onClick={() => onRemoveColumn(column.id)}
                  disabled={state.columns.length <= 1}
                >
                  Remove
                </Button>
              </Inline>
            </Stack>
          </fieldset>
        ))}
        <Button
          variant="outline"
          size="sm"
          onClick={onAddColumn}
          disabled={state.columns.length >= MAX_DESIGNER_COLUMNS}
        >
          Add column
        </Button>
      </Stack>

      <fieldset className="nss-dml-fieldset">
        <legend>Foreign key (optional)</legend>
        <Stack gap="sm">
          <Checkbox
            label="Add a FOREIGN KEY"
            checked={state.fkEnabled}
            onChange={() => onPatch({ fkEnabled: !state.fkEnabled })}
          />
          {state.fkEnabled ? (
            <>
              <Text size="xs" variant="muted">
                One constraint, loaded into the <code>CREATE TABLE</code> for review. Referenced columns are names
                you type (or pick from an existing table); the server validates they exist when you run the statement.
              </Text>
              <fieldset className="nss-dml-fieldset">
                <legend>Local columns</legend>
                <Stack gap="xs">
                  {namedColumns.length === 0 ? (
                    <Text size="sm" variant="muted">Name a column first.</Text>
                  ) : namedColumns.map((column) => (
                    <Checkbox
                      key={`fk-local-${column.id}`}
                      label={column.name.trim()}
                      checked={state.fkColumns.includes(column.name.trim())}
                      onChange={() => onPatch({ fkColumns: toggleName(state.fkColumns, column.name.trim()) })}
                    />
                  ))}
                </Stack>
              </fieldset>
              {tables.length > 0 ? (
                <Select
                  id="designer-fk-ref-table"
                  label="Referenced table"
                  options={[
                    { value: "", label: "Choose a table" },
                    ...[state.table.trim(), ...tables]
                      .filter((name, index, all) => name && all.indexOf(name) === index)
                      .map((name) => ({ value: name, label: name === state.table.trim() ? `${name} (this table)` : name })),
                  ]}
                  value={state.fkRefTable}
                  onChange={(value) => onPatch({ fkRefTable: value, fkRefColumns: [] })}
                />
              ) : (
                <Input
                  id="designer-fk-ref-table"
                  label="Referenced table"
                  value={state.fkRefTable}
                  onChange={(event) => onPatch({ fkRefTable: event.currentTarget.value })}
                />
              )}
              <Input
                id="designer-fk-ref-cols"
                label="Referenced columns"
                placeholder="id"
                value={state.fkRefColumns.join(", ")}
                onChange={(event) => onPatch({
                  fkRefColumns: event.currentTarget.value.split(",").map((name) => name.trim()).filter(Boolean),
                })}
              />
              <Inline gap="sm" wrap>
                <Select
                  id="designer-fk-on-delete"
                  label="ON DELETE"
                  options={FK_ACTIONS.map((value) => ({ value, label: value }))}
                  value={state.fkOnDelete}
                  onChange={(value) => onPatch({ fkOnDelete: value as DesignerFKAction })}
                />
                <Select
                  id="designer-fk-on-update"
                  label="ON UPDATE"
                  options={FK_ACTIONS.map((value) => ({ value, label: value }))}
                  value={state.fkOnUpdate}
                  onChange={(value) => onPatch({ fkOnUpdate: value as DesignerFKAction })}
                />
              </Inline>
            </>
          ) : null}
        </Stack>
      </fieldset>
    </Stack>
  );
}

function IndexForm({
  state,
  tables,
  catalogColumns,
  indexKinds,
  loading,
  loadError,
  onPatch,
  onChangeTable,
}: {
  state: CreateIndexState;
  tables: string[];
  catalogColumns: { name: string; type: string }[];
  indexKinds: DesignerIndexKind[];
  loading: boolean;
  loadError: string | null;
  onPatch: (patch: Partial<CreateIndexState>) => void;
  onChangeTable: (name: string) => void;
}) {
  const eligible = catalogColumns.filter((column) => {
    const klass = designerIndexColumnClass(column.type);
    if (state.kind === "fulltext") return klass === "text";
    if (state.kind === "vector") return klass === "vector-dense" || klass === "vector-bit" || klass === "vector-sparse";
    if (state.kind === "spatial") return klass === "geo";
    return true;
  });
  const firstKey = catalogColumns.find((column) => column.name === state.columns[0]);
  const showJSONPath = (state.kind === "btree" || state.kind === "unique") && firstKey && designerIndexColumnClass(firstKey.type) === "json";
  const showInclude = state.kind === "btree" || state.kind === "unique";
  const vectorColumn = catalogColumns.find((column) => column.name === state.columns[0]);
  const vectorClass = vectorColumn ? designerIndexColumnClass(vectorColumn.type) : null;

  return (
    <Stack gap="md">
      <Input
        id="designer-index-name"
        label="Index name"
        value={state.name}
        onChange={(event) => onPatch({ name: event.currentTarget.value })}
      />

      {tables.length === 0 ? (
        <Alert variant="warning" title="No visible tables">
          The authenticated catalog returned no tables you can index.
        </Alert>
      ) : (
        <Select
          id="designer-index-table"
          label="Table"
          options={tables.map((name) => ({ value: name, label: name }))}
          value={state.table}
          onChange={onChangeTable}
        />
      )}

      {loading ? (
        <Inline gap="sm" align="center" role="status">
          <Spinner size="sm" />
          <Text size="sm" variant="muted">Loading authorized columns…</Text>
        </Inline>
      ) : loadError ? (
        <Alert variant="error" title="Could not load table metadata">{loadError}</Alert>
      ) : catalogColumns.length === 0 && state.table ? (
        <Alert variant="warning" title="No columns">This table exposes no columns to index.</Alert>
      ) : catalogColumns.length > 0 ? (
        <>
          <Select
            id="designer-index-kind"
            label="Index kind"
            options={indexKinds.map((kind) => ({ value: kind, label: INDEX_KIND_LABEL[kind] }))}
            value={indexKinds.includes(state.kind) ? state.kind : "btree"}
            onChange={(value) => onPatch({ kind: value as DesignerIndexKind, columns: [], include: [], jsonPath: "" })}
          />

          <fieldset className="nss-dml-fieldset">
            <legend>Key columns</legend>
            <Stack gap="xs">
              {eligible.length === 0 ? (
                <Text size="sm" variant="muted">No columns of a type this index kind accepts.</Text>
              ) : eligible.map((column) => (
                <Inline key={`ix-${column.name}`} gap="sm" align="center" wrap>
                  <Checkbox
                    label={`${column.name} · ${column.type}`}
                    checked={state.columns.includes(column.name)}
                    onChange={() => onPatch({ columns: toggleName(state.columns, column.name) })}
                  />
                </Inline>
              ))}
            </Stack>
          </fieldset>

          {showJSONPath ? (
            <Input
              id="designer-index-json-path"
              label="JSON path"
              placeholder="category"
              value={state.jsonPath}
              onChange={(event) => onPatch({ jsonPath: event.currentTarget.value })}
            />
          ) : null}

          {showInclude ? (
            <fieldset className="nss-dml-fieldset">
              <legend>INCLUDE columns</legend>
              <Text size="xs" variant="muted">Covering payload only — not part of the key. B+Tree indexes only.</Text>
              <Stack gap="xs">
                {catalogColumns.filter((column) => !state.columns.includes(column.name)).map((column) => (
                  <Checkbox
                    key={`inc-${column.name}`}
                    label={`${column.name} · ${column.type}`}
                    checked={state.include.includes(column.name)}
                    onChange={() => onPatch({ include: toggleName(state.include, column.name) })}
                  />
                ))}
              </Stack>
            </fieldset>
          ) : null}

          {state.kind === "fulltext" ? (
            <Select
              id="designer-index-analyzer"
              label="Analyzer"
              options={ANALYZERS.map((value) => ({ value, label: value }))}
              value={state.analyzer}
              onChange={(value) => onPatch({ analyzer: value as DesignerFTAnalyzer })}
            />
          ) : null}

          {state.kind === "vector" ? (
            <Stack gap="sm">
              <Select
                id="designer-index-vec-method"
                label="USING"
                options={VECTOR_METHODS
                  .filter((method) => {
                    if (vectorClass === "vector-sparse") return method === "SPARSE";
                    if (vectorClass === "vector-bit") return method === "HNSW";
                    if (vectorClass === "vector-dense") return method !== "SPARSE";
                    return true;
                  })
                  .map((value) => ({ value, label: value }))}
                value={state.vectorMethod}
                onChange={(value) => onPatch({ vectorMethod: value as DesignerVectorMethod })}
              />
              {state.vectorMethod === "HNSW" && vectorClass === "vector-dense" ? (
                <Select
                  id="designer-index-vec-quant"
                  label="Quantization"
                  options={[
                    { value: "NONE", label: "None" },
                    { value: "F16", label: "F16" },
                    { value: "I8", label: "I8" },
                  ]}
                  value={state.vectorQuant}
                  onChange={(value) => onPatch({ vectorQuant: value as DesignerVectorQuant })}
                />
              ) : null}
              {state.vectorMethod === "IVF" || state.vectorMethod === "IVFPQ" ? (
                <Inline gap="sm" wrap>
                  <Input
                    id="designer-index-ivf-lists"
                    label="LISTS"
                    type="number"
                    min="1"
                    value={String(state.ivfLists)}
                    onChange={(event) => onPatch({ ivfLists: Number(event.currentTarget.value) })}
                  />
                  <Input
                    id="designer-index-ivf-probes"
                    label="PROBES (optional)"
                    type="number"
                    min="0"
                    value={String(state.ivfProbes)}
                    onChange={(event) => onPatch({ ivfProbes: Number(event.currentTarget.value) })}
                  />
                  {state.vectorMethod === "IVFPQ" ? (
                    <Input
                      id="designer-index-ivf-subspaces"
                      label="SUBSPACES"
                      type="number"
                      min="1"
                      value={String(state.ivfSubspaces)}
                      onChange={(event) => onPatch({ ivfSubspaces: Number(event.currentTarget.value) })}
                    />
                  ) : null}
                </Inline>
              ) : null}
            </Stack>
          ) : null}
        </>
      ) : null}
    </Stack>
  );
}
