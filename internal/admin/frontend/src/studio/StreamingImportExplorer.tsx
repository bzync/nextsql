import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  Alert,
  Badge,
  Button,
  Checkbox,
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
import { api, type StudioTableDetail } from "../ops/api";
import {
  BULK_IMPORT_BATCH_SIZES,
  DEFAULT_BULK_IMPORT_BATCH_SIZE,
  MAX_BULK_IMPORT_INPUT_BYTES,
  autoImportMapping,
  buildBulkImportBatches,
  dataGenColumns,
  formatThroughput,
  parseBulkImportText,
  type BulkImportPlan,
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

const BATCH_SIZE_OPTIONS = BULK_IMPORT_BATCH_SIZES.map((size) => ({
  value: String(size),
  label: `${size} rows per batch`,
}));

function queryID(): string {
  if (typeof crypto.randomUUID === "function") return crypto.randomUUID();
  const random = crypto.getRandomValues(new Uint32Array(2));
  return `studio-bulk-${Date.now().toString(36)}-${random[0].toString(36)}${random[1].toString(36)}`;
}

type ExecutionLog = {
  time: string;
  text: string;
  variant?: "info" | "success" | "warning" | "error";
};

export function StreamingImportExplorer({
  onClose,
  onSuccess,
  tables,
  initialTable,
  initialDetail,
  loadTable,
  readOnly = false,
  isProduction = false,
}: {
  onClose: () => void;
  onSuccess?: (table: string, rowCount: number) => void;
  tables: string[];
  initialTable: string | null;
  initialDetail: StudioTableDetail | null;
  loadTable: (name: string) => Promise<StudioTableDetail>;
  readOnly?: boolean;
  isProduction?: boolean;
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
  const [fileName, setFileName] = useState<string | null>(null);
  const [fileError, setFileError] = useState<string | null>(null);
  const [emptyAsNull, setEmptyAsNull] = useState(true);
  const [transactional, setTransactional] = useState(true);
  const [batchSize, setBatchSize] = useState<number>(DEFAULT_BULK_IMPORT_BATCH_SIZE);
  const [overrides, setOverrides] = useState<Record<string, string>>({});
  const [prodConfirmed, setProdConfirmed] = useState(false);

  // Execution state
  const [phase, setPhase] = useState<"idle" | "running" | "completed" | "error" | "cancelled">("idle");
  const [currentBatchIndex, setCurrentBatchIndex] = useState(0);
  const [importedRows, setImportedRows] = useState(0);
  const [elapsedMs, setElapsedMs] = useState(0);
  const [throughput, setThroughput] = useState("0 rows/sec");
  const [estimatedRemaining, setEstimatedRemaining] = useState("");
  const [logs, setLogs] = useState<ExecutionLog[]>([]);
  const [errorMessage, setErrorMessage] = useState<string | null>(null);

  const request = useRef(0);
  const fileInput = useRef<HTMLInputElement>(null);
  const abortRef = useRef(false);

  const requestTable = useCallback(
    async (name: string, known?: StudioTableDetail | null) => {
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
    },
    [loadTable],
  );

  useEffect(() => {
    if (firstTable) void requestTable(firstTable, initialDetail);
    return () => {
      request.current += 1;
    };
  }, []);

  const columns = useMemo<DataGenColumn[]>(() => dataGenColumns(detail), [detail]);
  const parsed = useMemo(() => parseBulkImportText(rawText, format), [rawText, format]);
  const autoMap = useMemo(() => autoImportMapping(parsed.fields, columns), [parsed.fields, columns]);
  const mapping = useMemo(() => {
    const merged: Record<string, string> = {};
    for (const field of parsed.fields) {
      merged[field] = overrides[field] ?? autoMap[field] ?? "";
    }
    return merged;
  }, [parsed.fields, overrides, autoMap]);

  const plan = useMemo<BulkImportPlan>(() => {
    return buildBulkImportBatches({
      table,
      columns,
      mapping,
      parsed,
      emptyAsNull,
      batchSize,
    });
  }, [table, columns, mapping, parsed, emptyAsNull, batchSize]);

  const targetOptions = useMemo(
    () => [
      { value: "", label: "— skip —" },
      ...columns
        .filter((c) => c.supported)
        .map((c) => ({ value: c.name, label: `${c.name} (${c.type})` })),
    ],
    [columns],
  );

  const onFile = useCallback((file: File) => {
    setFileError(null);
    setFileName(file.name);
    if (file.size > MAX_BULK_IMPORT_INPUT_BYTES) {
      setFileError(`File is ${(file.size / 1024 / 1024).toFixed(1)} MiB, which exceeds the ${(MAX_BULK_IMPORT_INPUT_BYTES / 1024 / 1024).toFixed(0)} MiB bound.`);
      return;
    }
    // Auto-detect format based on file extension
    const ext = file.name.split(".").pop()?.toLowerCase();
    if (ext === "tsv") setFormat("tsv");
    else if (ext === "json") setFormat("json");
    else if (ext === "ndjson" || ext === "jsonl") setFormat("ndjson");
    else if (ext === "csv") setFormat("csv");

    file
      .text()
      .then((text) => setRawText(text))
      .catch(() => setFileError("Could not read file from disk."));
  }, []);

  const addLog = useCallback((text: string, variant: ExecutionLog["variant"] = "info") => {
    const time = new Date().toLocaleTimeString();
    setLogs((prev) => [...prev, { time, text, variant }].slice(-500));
  }, []);

  const startImport = async () => {
    if (readOnly || !plan || plan.error || plan.batches.length === 0) return;
    if (isProduction && !prodConfirmed) return;

    abortRef.current = false;
    setPhase("running");
    setCurrentBatchIndex(0);
    setImportedRows(0);
    setErrorMessage(null);
    setLogs([]);

    const startTs = Date.now();
    setElapsedMs(0);
    addLog(`Starting streaming import into "${table}": ${plan.totalRows.toLocaleString()} rows in ${plan.totalBatches.toLocaleString()} batches (${batchSize} rows/batch).`);

    let inTxn = false;
    try {
      if (transactional) {
        addLog("Beginning transaction (BEGIN)…");
        await api.studioQuery({ query_id: queryID(), sql: "BEGIN;" });
        inTxn = true;
      }

      let rowsDone = 0;
      for (let i = 0; i < plan.batches.length; i++) {
        if (abortRef.current) {
          addLog(`Import cancelled by operator at batch ${i + 1} of ${plan.totalBatches}.`, "warning");
          if (inTxn) {
            addLog("Rolling back transaction (ROLLBACK)…", "warning");
            try {
              await api.studioQuery({ query_id: queryID(), sql: "ROLLBACK;" });
            } catch {
              // ignore
            }
            inTxn = false;
          }
          setPhase("cancelled");
          return;
        }

        const batch = plan.batches[i];
        setCurrentBatchIndex(i);
        const batchStart = Date.now();
        await api.studioQuery({ query_id: queryID(), sql: batch.sql });
        const batchElapsed = Date.now() - batchStart;

        rowsDone += batch.rowCount;
        const totalElapsed = Date.now() - startTs;
        setImportedRows(rowsDone);
        setElapsedMs(totalElapsed);
        setThroughput(formatThroughput(rowsDone, totalElapsed));

        const rate = rowsDone / (totalElapsed / 1000);
        if (rate > 0 && rowsDone < plan.totalRows) {
          const sec = Math.ceil((plan.totalRows - rowsDone) / rate);
          setEstimatedRemaining(sec > 60 ? `~${Math.ceil(sec / 60)}m left` : `~${sec}s left`);
        } else {
          setEstimatedRemaining("");
        }

        addLog(`Batch ${i + 1}/${plan.totalBatches} (${batch.startRow}–${batch.endRow}): inserted ${batch.rowCount.toLocaleString()} rows (${batchElapsed}ms)`, "info");
      }

      if (inTxn) {
        addLog("Committing transaction (COMMIT)…");
        await api.studioQuery({ query_id: queryID(), sql: "COMMIT;" });
        inTxn = false;
      }

      const totalDuration = Date.now() - startTs;
      setElapsedMs(totalDuration);
      addLog(`Streaming import finished successfully: ${rowsDone.toLocaleString()} rows committed to "${table}".`, "success");
      setPhase("completed");
      onSuccess?.(table, rowsDone);
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err);
      addLog(`Execution error at batch ${currentBatchIndex + 1}: ${msg}`, "error");
      if (inTxn) {
        addLog("Rolling back transaction (ROLLBACK)…", "warning");
        try {
          await api.studioQuery({ query_id: queryID(), sql: "ROLLBACK;" });
        } catch {
          // ignore
        }
        inTxn = false;
      }
      setErrorMessage(msg);
      setPhase("error");
    }
  };

  const cancelImport = () => {
    abortRef.current = true;
  };

  const resetToSetup = () => {
    setPhase("idle");
    setErrorMessage(null);
    setLogs([]);
    setCurrentBatchIndex(0);
    setImportedRows(0);
  };

  const percent = plan && plan.totalRows > 0 ? Math.min(100, Math.round((importedRows / plan.totalRows) * 100)) : 0;
  const previewRows = useMemo(() => parsed.rows.slice(0, 5), [parsed.rows]);

  return (
    <Modal
      open
      onClose={() => {
        if (phase !== "running") onClose();
      }}
      size="lg"
      ariaLabel="Streaming bulk import"
      scrollable
    >
      <ModalHeader>
        <ModalTitle>Streaming bulk import</ModalTitle>
      </ModalHeader>
      <ModalBody scrollable>
        <Stack gap="md">
          {phase === "idle" ? (
            <>
              <Text size="sm" variant="muted">
                Stream data directly into a table in chunked batches over your authenticated session connection.
                Validates types before execution and supports transactional rollback.
              </Text>

              {readOnly ? (
                <Alert variant="warning" title="Read-only mode is active">
                  Read-only mode is enabled in the top bar. You must disable read-only mode to insert rows into tables.
                </Alert>
              ) : null}

              {tables.length === 0 ? (
                <Alert variant="warning" title="No visible tables">
                  The authenticated catalog returned no tables you can write to.
                </Alert>
              ) : (
                <Inline gap="md" wrap>
                  <Select
                    id="bulk-import-table"
                    label="Target table"
                    options={tables.map((name) => ({ value: name, label: name }))}
                    value={table}
                    onChange={(value) => void requestTable(value)}
                  />
                  <Select
                    id="bulk-import-format"
                    label="Document format"
                    options={FORMAT_OPTIONS}
                    value={format}
                    onChange={(value) => setFormat(value as ImportFormat)}
                  />
                  <Select
                    id="bulk-import-batch-size"
                    label="Batch size"
                    options={BATCH_SIZE_OPTIONS}
                    value={String(batchSize)}
                    onChange={(val) => setBatchSize(Number(val))}
                  />
                </Inline>
              )}

              <Textarea
                id="bulk-import-source"
                label="Document"
                hint={fileName ? `Loaded file: ${fileName}` : "Paste document or load a file (CSV/TSV header row required)"}
                rows={5}
                value={rawText}
                onChange={(event) => {
                  setFileName(null);
                  setRawText(event.currentTarget.value);
                }}
              />
              <Inline gap="sm" align="center" wrap>
                <Button variant="outline" size="sm" onClick={() => fileInput.current?.click()}>
                  Load file…
                </Button>
                {rawText ? (
                  <Button
                    variant="ghost"
                    size="sm"
                    onClick={() => {
                      setRawText("");
                      setFileName(null);
                    }}
                  >
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
              {fileError ? <Alert variant="error" title="Could not load file">{fileError}</Alert> : null}

              <Inline gap="lg" wrap>
                <Checkbox
                  id="bulk-import-empty-null"
                  label="Treat empty or missing values as NULL"
                  checked={emptyAsNull}
                  onChange={(event) => setEmptyAsNull(event.currentTarget.checked)}
                />
                <Checkbox
                  id="bulk-import-transactional"
                  label="Wrap in a single transaction (roll back on error)"
                  checked={transactional}
                  onChange={(event) => setTransactional(event.currentTarget.checked)}
                />
              </Inline>

              {loading ? (
                <Inline gap="sm" align="center" role="status">
                  <Spinner size="sm" />
                  <Text size="sm" variant="muted">Loading table metadata…</Text>
                </Inline>
              ) : loadError ? (
                <Alert variant="error" title="Could not load table metadata">{loadError}</Alert>
              ) : parsed.error ? (
                <Alert variant="warning" title="Could not read document">{parsed.error}</Alert>
              ) : parsed.fields.length > 0 && detail ? (
                <>
                  <Stack gap="sm">
                    <Text size="sm" variant="muted">
                      {parsed.totalRows.toLocaleString()} row{parsed.totalRows === 1 ? "" : "s"} detected.
                      Map document fields to table columns:
                    </Text>
                    {parsed.fields.map((field) => (
                      <Inline key={field} gap="sm" align="center" wrap>
                        <Select
                          id={`bulk-import-field-${field}`}
                          label={field}
                          options={targetOptions}
                          value={mapping[field] ?? ""}
                          onChange={(value) => setOverrides((cur) => ({ ...cur, [field]: value }))}
                        />
                      </Inline>
                    ))}
                    {columns.some((c) => !c.supported) ? (
                      <Text size="xs" variant="muted">
                        {columns.filter((c) => !c.supported).map((c) => c.name).join(", ")} — cannot be imported from text cells and are omitted.
                      </Text>
                    ) : null}
                  </Stack>

                  {previewRows.length > 0 ? (
                    <Stack gap="xs">
                      <Text size="xs" variant="muted">Sample data preview (first {previewRows.length} rows):</Text>
                      <div
                        className="nss-bulk-import-preview-table-container"
                        tabIndex={0}
                        role="region"
                        aria-label="Import data preview"
                      >
                        <table className="nss-bulk-import-preview-table">
                          <thead>
                            <tr>
                              {parsed.fields.map((f) => (
                                <th key={f}>
                                  {f}
                                  {mapping[f] ? <span className="text-muted font-normal"> → {mapping[f]}</span> : null}
                                </th>
                              ))}
                            </tr>
                          </thead>
                          <tbody>
                            {previewRows.map((r, ri) => (
                              <tr key={ri}>
                                {parsed.fields.map((_, fi) => (
                                  <td key={fi}>{r[fi] || <span className="text-muted">NULL</span>}</td>
                                ))}
                              </tr>
                            ))}
                          </tbody>
                        </table>
                      </div>
                    </Stack>
                  ) : null}
                </>
              ) : null}

              {plan.error && rawText.trim() && !parsed.error ? (
                <Alert variant="warning" title="Import not ready">{plan.error}</Alert>
              ) : null}

              {isProduction && !plan.error && plan.totalRows > 0 ? (
                <Alert variant="error" title="Production database warning">
                  <Stack gap="xs">
                    <Text size="sm">
                      You are connected to a <strong>production</strong> server profile. Streaming import will insert data directly into table <code>{table}</code>.
                    </Text>
                    <Checkbox
                      id="bulk-import-confirm-prod"
                      label={`I confirm inserting ${plan.totalRows.toLocaleString()} rows into production table "${table}"`}
                      checked={prodConfirmed}
                      onChange={(e) => setProdConfirmed(e.currentTarget.checked)}
                    />
                  </Stack>
                </Alert>
              ) : null}

              {!plan.error && plan.totalRows > 0 ? (
                <Text size="xs" variant="muted">
                  Ready to stream {plan.totalRows.toLocaleString()} rows across {plan.totalBatches.toLocaleString()} batches into <code>{table}</code>.
                </Text>
              ) : null}
            </>
          ) : (
            <Stack gap="md">
              {phase === "running" ? (
                <Inline gap="sm" align="center">
                  <Spinner size="sm" />
                  <Text size="sm" weight="semibold">Streaming import in progress…</Text>
                </Inline>
              ) : phase === "completed" ? (
                <Alert variant="success" title="Import finished successfully">
                  Committed {importedRows.toLocaleString()} rows into <code>{table}</code> across {plan.totalBatches} batches in {(elapsedMs / 1000).toFixed(1)}s ({throughput}).
                </Alert>
              ) : phase === "cancelled" ? (
                <Alert variant="warning" title="Import cancelled">
                  Import cancelled after {importedRows.toLocaleString()} rows. {transactional ? "Transaction was rolled back; no rows were written." : "Previous batches remain committed."}
                </Alert>
              ) : (
                <Alert variant="error" title="Import failed">
                  {errorMessage}. {transactional ? "Transaction was rolled back; 0 rows committed." : "Prior batches were committed."}
                </Alert>
              )}

              {/* Accessible progress bar */}
              <div className="nss-bulk-import-progress-container">
                <div
                  className="nss-bulk-import-progress-bar"
                  role="progressbar"
                  aria-valuenow={percent}
                  aria-valuemin={0}
                  aria-valuemax={100}
                  aria-label="Import progress"
                  style={{ width: `${percent}%` }}
                />
              </div>

              {/* KPI stats */}
              <div className="nss-bulk-import-stats-grid">
                <div className="nss-bulk-import-stat-card">
                  <span className="nss-bulk-import-stat-label">Progress</span>
                  <span className="nss-bulk-import-stat-value">{percent}%</span>
                </div>
                <div className="nss-bulk-import-stat-card">
                  <span className="nss-bulk-import-stat-label">Rows</span>
                  <span className="nss-bulk-import-stat-value">{importedRows.toLocaleString()} / {plan.totalRows.toLocaleString()}</span>
                </div>
                <div className="nss-bulk-import-stat-card">
                  <span className="nss-bulk-import-stat-label">Batches</span>
                  <span className="nss-bulk-import-stat-value">{currentBatchIndex + (phase === "running" ? 1 : 0)} / {plan.totalBatches}</span>
                </div>
                <div className="nss-bulk-import-stat-card">
                  <span className="nss-bulk-import-stat-label">Throughput</span>
                  <span className="nss-bulk-import-stat-value">{throughput}</span>
                </div>
                <div className="nss-bulk-import-stat-card">
                  <span className="nss-bulk-import-stat-label">Elapsed</span>
                  <span className="nss-bulk-import-stat-value">{(elapsedMs / 1000).toFixed(1)}s</span>
                </div>
              </div>

              {/* Activity log */}
              <Stack gap="xs">
                <Inline justify="between" align="center">
                  <Text size="xs" variant="muted">Activity log</Text>
                  {estimatedRemaining ? <Badge variant="muted">{estimatedRemaining}</Badge> : null}
                </Inline>
                <div
                  className="nss-bulk-import-log"
                  tabIndex={0}
                  role="region"
                  aria-label="Streaming import activity log"
                >
                  {logs.length === 0 ? (
                    <span className="text-muted">Awaiting start…</span>
                  ) : (
                    logs.map((log, i) => (
                      <div key={i} className={`nss-bulk-import-log-line nss-bulk-import-log-${log.variant ?? "info"}`}>
                        <span className="nss-bulk-import-log-time">[{log.time}]</span> {log.text}
                      </div>
                    ))
                  )}
                </div>
              </Stack>
            </Stack>
          )}
        </Stack>
      </ModalBody>
      <ModalFooter>
        {phase === "idle" ? (
          <>
            <Button variant="ghost" onClick={onClose}>Cancel</Button>
            <Button
              variant="primary"
              onClick={() => void startImport()}
              disabled={
                readOnly ||
                Boolean(plan.error) ||
                plan.totalRows === 0 ||
                (isProduction && !prodConfirmed)
              }
            >
              Start streaming import
            </Button>
          </>
        ) : phase === "running" ? (
          <Button variant="destructive" onClick={cancelImport}>Cancel import</Button>
        ) : (
          <>
            <Button variant="ghost" onClick={resetToSetup}>Import another file</Button>
            <Button variant="primary" onClick={onClose}>Done</Button>
          </>
        )}
      </ModalFooter>
    </Modal>
  );
}
