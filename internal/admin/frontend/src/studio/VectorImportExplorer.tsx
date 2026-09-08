import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  Alert,
  Badge,
  Button,
  Checkbox,
  CodeBlock,
  CopyButton,
  Inline,
  Modal,
  ModalBody,
  ModalFooter,
  ModalHeader,
  ModalTitle,
  Select,
  Spinner,
  Stack,
  Text,
  Textarea,
} from "@bzync/rui";
import type { StudioTableDetail } from "../ops/api";
import {
  MAX_IMPORT_INPUT_BYTES,
  MAX_VECTOR_IMPORT_ROWS,
  autoImportMapping,
  autoVectorImportField,
  buildVectorImportSQL,
  dataGenColumns,
  parseImportText,
  vectorColumnsFromResult,
  type DataGenColumn,
  type ImportFormat,
  type VectorCatalogColumn,
} from "./resultTools";

const FORMAT_OPTIONS: { value: ImportFormat; label: string }[] = [
  { value: "ndjson", label: "NDJSON (one JSON object per line)" },
  { value: "json", label: "JSON array of objects" },
  { value: "csv", label: "CSV (comma-separated)" },
  { value: "csv-semicolon", label: "CSV (semicolon-separated)" },
  { value: "tsv", label: "TSV (tab-separated)" },
];

const PREVIEW_LINE_LIMIT = 160;

export function VectorImportExplorer({
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
  const [table, setTable] = useState(firstTable);
  const [detail, setDetail] = useState<StudioTableDetail | null>(
    initialDetail?.name === firstTable ? initialDetail : null,
  );
  const [loading, setLoading] = useState(Boolean(firstTable && initialDetail?.name !== firstTable));
  const [loadError, setLoadError] = useState<string | null>(null);
  const [format, setFormat] = useState<ImportFormat>("ndjson");
  const [rawText, setRawText] = useState("");
  const [fileError, setFileError] = useState<string | null>(null);
  const [emptyAsNull, setEmptyAsNull] = useState(true);
  const [vectorColumnName, setVectorColumnName] = useState<string>("");
  const [vectorFieldOverride, setVectorFieldOverride] = useState<string | null>(null);
  const [scalarOverrides, setScalarOverrides] = useState<Record<string, string>>({});
  const request = useRef(0);
  const fileInput = useRef<HTMLInputElement>(null);

  const requestTable = useCallback(async (name: string, known?: StudioTableDetail | null) => {
    const id = ++request.current;
    setTable(name);
    setDetail(null);
    setVectorColumnName("");
    setVectorFieldOverride(null);
    setScalarOverrides({});
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
    if (firstTable) void requestTable(firstTable, initialDetail);
    return () => { request.current += 1; };
    // Mounted fresh for every open; initialize once from the workspace snapshot.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const allColumns = useMemo<DataGenColumn[]>(() => dataGenColumns(detail), [detail]);
  const vectorColumns = useMemo<VectorCatalogColumn[]>(
    () => (detail ? vectorColumnsFromResult(detail.columns) : []),
    [detail],
  );
  const vectorColumn = useMemo<VectorCatalogColumn | null>(() => {
    if (vectorColumns.length === 0) return null;
    return vectorColumns.find((c) => c.name === vectorColumnName) ?? vectorColumns[0];
  }, [vectorColumns, vectorColumnName]);

  const parsed = useMemo(() => parseImportText(rawText, format), [rawText, format]);

  // Scalar auto-mapping excludes the vector column (it is not `supported` in
  // dataGenColumns, so autoImportMapping never targets it anyway) and the
  // chosen vector field.
  const scalarAutoMap = useMemo(
    () => autoImportMapping(parsed.fields, allColumns),
    [parsed.fields, allColumns],
  );
  const vectorField = useMemo(() => {
    if (vectorFieldOverride && parsed.fields.includes(vectorFieldOverride)) return vectorFieldOverride;
    if (!vectorColumn) return "";
    return autoVectorImportField(parsed.fields, vectorColumn.name, scalarAutoMap);
  }, [vectorFieldOverride, parsed.fields, vectorColumn, scalarAutoMap]);

  const scalarMapping = useMemo(() => {
    const merged: Record<string, string> = {};
    for (const field of parsed.fields) {
      if (field === vectorField) continue;
      merged[field] = scalarOverrides[field] ?? scalarAutoMap[field] ?? "";
    }
    return merged;
  }, [parsed.fields, vectorField, scalarOverrides, scalarAutoMap]);

  const built = useMemo(
    () => buildVectorImportSQL({ table, columns: allColumns, vectorColumn, vectorField, scalarMapping, parsed, emptyAsNull }),
    [table, allColumns, vectorColumn, vectorField, scalarMapping, parsed, emptyAsNull],
  );

  const preview = useMemo(() => {
    if (!built.sql) return null;
    const lines = built.sql.split("\n");
    if (lines.length <= PREVIEW_LINE_LIMIT) return { text: built.sql, hidden: 0 };
    return { text: lines.slice(0, PREVIEW_LINE_LIMIT).join("\n"), hidden: lines.length - PREVIEW_LINE_LIMIT };
  }, [built]);

  const scalarOptions = useMemo(
    () => [
      { value: "", label: "— skip —" },
      ...allColumns.filter((c) => c.supported).map((c) => ({ value: c.name, label: `${c.name} · ${c.type}` })),
    ],
    [allColumns],
  );

  const onFile = useCallback((file: File) => {
    setFileError(null);
    if (file.size > MAX_IMPORT_INPUT_BYTES) {
      setFileError(`That file is larger than ${(MAX_IMPORT_INPUT_BYTES / 1024 / 1024).toFixed(0)} MiB.`);
      return;
    }
    file
      .text()
      .then((text) => setRawText(text))
      .catch(() => setFileError("Could not read that file."));
  }, []);

  const insert = () => {
    if (!built.sql) return;
    onInsert(built.sql);
    onClose();
  };

  return (
    <Modal open onClose={onClose} size="lg" ariaLabel="Import a vector dataset" scrollable>
      <ModalHeader>
        <ModalTitle>Import vector dataset</ModalTitle>
      </ModalHeader>
      <ModalBody scrollable>
        <Stack gap="md">
          <Text size="sm" variant="muted">
            Maps one document field of embeddings onto the table's VECTOR / BITVECTOR / SPARSEVECTOR column and
            any other fields onto its scalar columns, then builds INSERT statements that replace the active editor
            tab — it never runs anything here. Each embedding cell is validated against the column's declared
            dimensions; a bad or wrong-length vector is reported, not guessed.
          </Text>

          {tables.length === 0 ? (
            <Alert variant="warning" title="No visible tables">
              The authenticated catalog returned no tables you can write to.
            </Alert>
          ) : (
            <Inline gap="md" wrap>
              <Select
                id="vecimport-table"
                label="Target table"
                options={tables.map((name) => ({ value: name, label: name }))}
                value={table}
                onChange={(value) => void requestTable(value)}
              />
              <Select
                id="vecimport-format"
                label="Document format"
                options={FORMAT_OPTIONS}
                value={format}
                onChange={(value) => setFormat(value as ImportFormat)}
              />
            </Inline>
          )}

          {detail && !loading && vectorColumns.length === 0 ? (
            <Alert variant="warning" title="No vector column">
              This table has no visible VECTOR, BITVECTOR, or SPARSEVECTOR column to import into.
            </Alert>
          ) : null}

          {vectorColumns.length > 0 ? (
            <Inline gap="md" align="center" wrap>
              <Select
                id="vecimport-column"
                label="Vector column"
                options={vectorColumns.map((c) => ({ value: c.name, label: `${c.name} · ${c.type}` }))}
                value={vectorColumn?.name ?? ""}
                onChange={(value) => setVectorColumnName(value)}
              />
              {vectorColumn?.dimensions != null ? (
                <Badge variant="muted">{vectorColumn.dimensions} dimensions</Badge>
              ) : (
                <Badge variant="warning">dimensions not declared</Badge>
              )}
            </Inline>
          ) : null}

          <Textarea
            id="vecimport-source"
            label="Document"
            hint="Paste the dataset, or load a file. An embedding cell may be a JSON array, a bracketed [1, 2, 3], a parenthesized (1, 2, 3), or a bare comma list. In CSV/TSV the vector cell must be quoted."
            rows={7}
            value={rawText}
            onChange={(event) => setRawText(event.currentTarget.value)}
          />
          <Inline gap="sm" align="center" wrap>
            <Button variant="outline" size="sm" onClick={() => fileInput.current?.click()}>
              Load file…
            </Button>
            {rawText ? (
              <Button variant="ghost" size="sm" onClick={() => setRawText("")}>
                Clear
              </Button>
            ) : null}
            <input
              ref={fileInput}
              type="file"
              accept=".csv,.tsv,.txt,.json,.ndjson,.jsonl,text/csv,application/json"
              hidden
              onChange={(event) => {
                const file = event.currentTarget.files?.[0];
                event.currentTarget.value = "";
                if (file) onFile(file);
              }}
            />
          </Inline>
          {fileError ? <Alert variant="error" title="Could not load that file">{fileError}</Alert> : null}

          <Checkbox
            id="vecimport-empty-null"
            label="Treat an empty or missing value as NULL"
            checked={emptyAsNull}
            onChange={(event) => setEmptyAsNull(event.currentTarget.checked)}
          />

          {loading ? (
            <Inline gap="sm" align="center" role="status">
              <Spinner size="sm" />
              <Text size="sm" variant="muted">Loading authorized columns…</Text>
            </Inline>
          ) : loadError ? (
            <Alert variant="error" title="Could not load table metadata">{loadError}</Alert>
          ) : parsed.error ? (
            <Alert variant="warning" title="Could not read the document">{parsed.error}</Alert>
          ) : parsed.fields.length > 0 && detail && vectorColumn ? (
            <Stack gap="sm" aria-label="Field mapping">
              <Text size="sm" variant="muted">
                {parsed.rows.length.toLocaleString()} data row{parsed.rows.length === 1 ? "" : "s"} detected
                {parsed.truncated || parsed.rows.length > MAX_VECTOR_IMPORT_ROWS
                  ? ` (only the first ${Math.min(parsed.rows.length, MAX_VECTOR_IMPORT_ROWS).toLocaleString()} will be imported)`
                  : ""}.
              </Text>
              <Select
                id="vecimport-vector-field"
                label={`Embedding field → ${vectorColumn.name}`}
                options={parsed.fields.map((field) => ({ value: field, label: field }))}
                value={vectorField}
                onChange={(value) => setVectorFieldOverride(value)}
              />
              {parsed.fields.filter((field) => field !== vectorField).length > 0 ? (
                <>
                  <Text size="sm" variant="muted">Map the remaining fields to scalar columns:</Text>
                  {parsed.fields
                    .filter((field) => field !== vectorField)
                    .map((field) => (
                      <Inline key={field} gap="sm" align="center" wrap>
                        <Select
                          id={`vecimport-field-${field}`}
                          label={field}
                          options={scalarOptions}
                          value={scalarMapping[field] ?? ""}
                          onChange={(value) => setScalarOverrides((current) => ({ ...current, [field]: value }))}
                        />
                      </Inline>
                    ))}
                </>
              ) : null}
              {allColumns.some((c) => !c.supported && c.name !== vectorColumn.name) ? (
                <Text size="xs" variant="muted">
                  {allColumns.filter((c) => !c.supported && c.name !== vectorColumn.name).map((c) => c.name).join(", ")}
                  {" "}— the importer cannot write these column types from a text value, so they are not offered as targets.
                </Text>
              ) : null}
            </Stack>
          ) : null}

          {built.sql ? (
            <>
              <div className="nss-datagen-sql-preview" tabIndex={0} aria-label="Generated INSERT SQL">
                <CodeBlock code={preview?.text ?? built.sql} language="sql" />
              </div>
              <Text size="xs" variant="muted">
                {built.rows.toLocaleString()} row{built.rows === 1 ? "" : "s"} across {built.statements.toLocaleString()}{" "}
                statement{built.statements === 1 ? "" : "s"}
                {built.truncated ? "; the dataset was truncated to the import ceiling" : ""}
                {preview && preview.hidden > 0 ? `; preview shows the first ${PREVIEW_LINE_LIMIT} lines` : ""}.
              </Text>
              <Inline gap="sm" align="center" wrap>
                <CopyButton value={built.sql} label="Copy SQL" size="sm" />
              </Inline>
            </>
          ) : detail && !loading && rawText.trim() && !parsed.error && vectorColumn ? (
            <Alert variant="warning" title="Not ready">{built.error}</Alert>
          ) : null}
        </Stack>
      </ModalBody>
      <ModalFooter>
        <Button variant="ghost" onClick={onClose}>Cancel</Button>
        <Button variant="primary" onClick={insert} disabled={!built.sql}>Insert into editor</Button>
      </ModalFooter>
    </Modal>
  );
}
