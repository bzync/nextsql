import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  Alert,
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
  autoImportMapping,
  buildImportInsertSQL,
  dataGenColumns,
  parseImportText,
  type DataGenColumn,
  type ImportFormat,
} from "./resultTools";

const FORMAT_OPTIONS: { value: ImportFormat; label: string }[] = [
  { value: "csv", label: "CSV (comma-separated)" },
  { value: "csv-semicolon", label: "CSV (semicolon-separated)" },
  { value: "tsv", label: "TSV (tab-separated)" },
  { value: "json", label: "JSON array of objects" },
  { value: "ndjson", label: "NDJSON (one object per line)" },
];

const PREVIEW_LINE_LIMIT = 160;

export function ImportExplorer({
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
  const [format, setFormat] = useState<ImportFormat>("csv");
  const [rawText, setRawText] = useState("");
  const [fileError, setFileError] = useState<string | null>(null);
  const [emptyAsNull, setEmptyAsNull] = useState(true);
  const [overrides, setOverrides] = useState<Record<string, string>>({});
  const request = useRef(0);
  const fileInput = useRef<HTMLInputElement>(null);

  const requestTable = useCallback(async (name: string, known?: StudioTableDetail | null) => {
    const id = ++request.current;
    setTable(name);
    setDetail(null);
    setOverrides({});
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

  const columns = useMemo<DataGenColumn[]>(() => dataGenColumns(detail), [detail]);
  const parsed = useMemo(() => parseImportText(rawText, format), [rawText, format]);
  const autoMap = useMemo(
    () => autoImportMapping(parsed.fields, columns),
    [parsed.fields, columns],
  );
  const mapping = useMemo(() => {
    const merged: Record<string, string> = {};
    for (const field of parsed.fields) {
      merged[field] = overrides[field] ?? autoMap[field] ?? "";
    }
    return merged;
  }, [parsed.fields, overrides, autoMap]);

  const built = useMemo(
    () => buildImportInsertSQL({ table, columns, mapping, parsed, emptyAsNull }),
    [table, columns, mapping, parsed, emptyAsNull],
  );

  const preview = useMemo(() => {
    if (!built.sql) return null;
    const lines = built.sql.split("\n");
    if (lines.length <= PREVIEW_LINE_LIMIT) return { text: built.sql, hidden: 0 };
    return { text: lines.slice(0, PREVIEW_LINE_LIMIT).join("\n"), hidden: lines.length - PREVIEW_LINE_LIMIT };
  }, [built]);

  const targetOptions = useMemo(
    () => [
      { value: "", label: "— skip —" },
      ...columns.filter((c) => c.supported).map((c) => ({ value: c.name, label: `${c.name} · ${c.type}` })),
    ],
    [columns],
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
    <Modal open onClose={onClose} size="lg" ariaLabel="Import CSV or JSON data" scrollable>
      <ModalHeader>
        <ModalTitle>Import CSV / JSON data</ModalTitle>
      </ModalHeader>
      <ModalBody scrollable>
        <Stack gap="md">
          <Text size="sm" variant="muted">
            Parses a pasted or loaded document, maps its fields to the target table's authorized columns, and
            builds INSERT statements that replace the active editor tab — it never runs anything here. Integer
            and boolean cells are checked against the column type; a bad value is reported, not guessed.
          </Text>

          {tables.length === 0 ? (
            <Alert variant="warning" title="No visible tables">
              The authenticated catalog returned no tables you can write to.
            </Alert>
          ) : (
            <Inline gap="md" wrap>
              <Select
                id="import-table"
                label="Target table"
                options={tables.map((name) => ({ value: name, label: name }))}
                value={table}
                onChange={(value) => void requestTable(value)}
              />
              <Select
                id="import-format"
                label="Document format"
                options={FORMAT_OPTIONS}
                value={format}
                onChange={(value) => setFormat(value as ImportFormat)}
              />
            </Inline>
          )}

          <Textarea
            id="import-source"
            label="Document"
            hint="Paste the document, or load a file. The first CSV/TSV row is the header."
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
            id="import-empty-null"
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
          ) : parsed.fields.length > 0 && detail ? (
            <Stack gap="sm" aria-label="Field mapping">
              <Text size="sm" variant="muted">
                {parsed.rows.length.toLocaleString()} data row{parsed.rows.length === 1 ? "" : "s"} detected
                {parsed.truncated ? ` (only the first ${parsed.rows.length.toLocaleString()} will be imported)` : ""}.
                Map each document field to a column:
              </Text>
              {parsed.fields.map((field) => (
                <Inline key={field} gap="sm" align="center" wrap>
                  <Select
                    id={`import-field-${field}`}
                    label={field}
                    options={targetOptions}
                    value={mapping[field] ?? ""}
                    onChange={(value) => setOverrides((current) => ({ ...current, [field]: value }))}
                  />
                </Inline>
              ))}
              {columns.some((c) => !c.supported) ? (
                <Text size="xs" variant="muted">
                  {columns.filter((c) => !c.supported).map((c) => c.name).join(", ")} — the importer cannot write
                  {" "}these column types from a text value, so they are not offered as targets.
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
                {preview && preview.hidden > 0 ? `; preview shows the first ${PREVIEW_LINE_LIMIT} lines` : ""}.
              </Text>
              <Inline gap="sm" align="center" wrap>
                <CopyButton value={built.sql} label="Copy SQL" size="sm" />
              </Inline>
            </>
          ) : detail && !loading && rawText.trim() && !parsed.error ? (
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
