import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  Alert,
  Badge,
  Button,
  CodeBlock,
  CopyButton,
  Inline,
  Input,
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
  MAX_VECTOR_EXPLORER_TOPK,
  buildVectorSQL,
  parseVectorLiteralInput,
  vectorCatalog,
  type VectorMetric,
} from "./resultTools";

const METRIC_LABELS: Record<VectorMetric, string> = {
  cosine: "COSINE (similarity)",
  l2: "L2 (Euclidean distance)",
  inner_product: "INNER_PRODUCT (dot product)",
  hamming: "HAMMING (differing bits)",
};

export function VectorExplorer({
  onClose,
  onInsert,
  onRun,
  tables,
  initialTable,
  initialDetail,
  loadTable,
  busy,
}: {
  onClose: () => void;
  onInsert: (sql: string) => void;
  onRun: (sql: string) => void;
  tables: string[];
  initialTable: string | null;
  initialDetail: StudioTableDetail | null;
  loadTable: (name: string) => Promise<StudioTableDetail>;
  busy: boolean;
}) {
  const firstTable = initialTable && tables.includes(initialTable) ? initialTable : tables[0] ?? "";
  const [table, setTable] = useState(firstTable);
  const [detail, setDetail] = useState<StudioTableDetail | null>(
    initialDetail?.name === firstTable ? initialDetail : null,
  );
  const [loading, setLoading] = useState(Boolean(firstTable && initialDetail?.name !== firstTable));
  const [loadError, setLoadError] = useState<string | null>(null);
  const [columnName, setColumnName] = useState("");
  const [metric, setMetric] = useState<VectorMetric>("cosine");
  const [vectorText, setVectorText] = useState("");
  const [topK, setTopK] = useState("10");
  const request = useRef(0);

  const applyDetail = useCallback((next: StudioTableDetail) => {
    const catalog = vectorCatalog(next);
    setDetail(next);
    const preferred = catalog.columns[0];
    setColumnName(preferred?.name ?? "");
    setMetric(preferred?.metrics[0] ?? "cosine");
  }, []);

  const requestTable = useCallback(async (name: string, known?: StudioTableDetail | null) => {
    const id = ++request.current;
    setTable(name);
    setDetail(null);
    setColumnName("");
    setLoadError(null);
    if (!name) {
      setLoading(false);
      return;
    }
    if (known?.name === name) {
      setLoading(false);
      applyDetail(known);
      return;
    }
    setLoading(true);
    try {
      const loaded = await loadTable(name);
      if (request.current !== id) return;
      applyDetail(loaded);
    } catch (error) {
      if (request.current !== id) return;
      setLoadError(error instanceof Error ? error.message : String(error));
    } finally {
      if (request.current === id) setLoading(false);
    }
  }, [applyDetail, loadTable]);

  useEffect(() => {
    if (firstTable) void requestTable(firstTable, initialDetail);
    return () => { request.current += 1; };
    // This component is mounted fresh for every open, so initialization must
    // run once from the snapshot supplied by StudioWorkspace.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const catalog = useMemo(() => detail ? vectorCatalog(detail) : { columns: [], indexes: [] }, [detail]);
  const column = catalog.columns.find((candidate) => candidate.name === columnName);
  const matchedIndex = column ? catalog.indexes.find((index) => index.column === column.name) : undefined;
  const parsedVector = useMemo(() => parseVectorLiteralInput(vectorText, column), [column, vectorText]);
  const built = useMemo(() => buildVectorSQL({
    table,
    column,
    values: parsedVector.values,
    metric,
    topK: Number(topK),
  }), [column, metric, parsedVector.values, table, topK]);

  const chooseColumn = (name: string) => {
    setColumnName(name);
    const next = catalog.columns.find((candidate) => candidate.name === name);
    if (next && !next.metrics.includes(metric)) setMetric(next.metrics[0]);
  };

  const insert = () => {
    if (!built.sql) return;
    onInsert(built.sql);
    onClose();
  };

  const run = () => {
    if (!built.sql || busy) return;
    onRun(built.sql);
    onClose();
  };

  return (
    <Modal open onClose={onClose} size="lg" ariaLabel="Vector Explorer" scrollable>
      <ModalHeader>
        <ModalTitle>Vector Explorer</ModalTitle>
      </ModalHeader>
      <ModalBody scrollable>
        <Stack gap="md">
          <Text size="sm" variant="muted">
            Builds one native NEAREST statement from authorized catalog metadata. Insert only edits the active tab;
            Run search uses the same bounded NSQL stream, cancellation, history, and server-side RBAC as the main Run action.
          </Text>

          {tables.length === 0 ? (
            <Alert variant="warning" title="No visible tables">
              The authenticated catalog returned no tables available for vector search.
            </Alert>
          ) : (
            <Select
              id="vector-table"
              label="Table"
              options={tables.map((name) => ({ value: name, label: name }))}
              value={table}
              onChange={(value) => void requestTable(value)}
            />
          )}

          {loading ? (
            <Inline gap="sm" align="center" role="status">
              <Spinner size="sm" />
              <Text size="sm" variant="muted">Loading authorized vector columns…</Text>
            </Inline>
          ) : loadError ? (
            <Alert variant="error" title="Could not load table metadata">{loadError}</Alert>
          ) : detail ? (
            catalog.columns.length === 0 ? (
              <Alert variant="warning" title="No vector columns">
                This table has no visible VECTOR, BITVECTOR, or SPARSEVECTOR column.
              </Alert>
            ) : (
              <>
                <Select
                  id="vector-column"
                  label="Vector column"
                  options={catalog.columns.map((candidate) => ({ value: candidate.name, label: `${candidate.name} · ${candidate.type}` }))}
                  value={columnName}
                  onChange={chooseColumn}
                />
                {matchedIndex ? (
                  <Inline gap="sm" align="center" wrap>
                    <Badge variant={matchedIndex.usable && matchedIndex.status.toLowerCase() === "valid" ? "success" : "warning"}>
                      {matchedIndex.status}
                    </Badge>
                    <Text size="xs" variant="muted">
                      Candidate index: {matchedIndex.name}. NextSQL reports every vector index (HNSW/IVF/IVFPQ/SPARSE) the same
                      way; use EXPLAIN to verify the chosen access path and algorithm.
                    </Text>
                  </Inline>
                ) : (
                  <Text size="xs" variant="muted">
                    No vector index found for this column. NEAREST remains valid over exact flat search.
                  </Text>
                )}
              </>
            )
          ) : null}

          <Select
            id="vector-metric"
            label="Ranking metric"
            options={(column?.metrics ?? ["cosine", "l2", "inner_product", "hamming"] as VectorMetric[]).map((value) => ({
              value,
              label: METRIC_LABELS[value],
            }))}
            value={metric}
            onChange={(value) => setMetric(value as VectorMetric)}
          />
          <Text size="xs" variant="muted">
            NSQL has no per-query HNSW/IVF search-time tuning setting — only CREATE VECTOR INDEX build-time options
            (QUANTIZATION, LISTS, PROBES) exist, so none are offered here.
          </Text>

          <Textarea
            id="vector-input"
            label="Query vector"
            value={vectorText}
            onChange={(event) => setVectorText(event.currentTarget.value)}
            placeholder="1, 0, 0.5, 0 or [1, 0, 0.5, 0]"
            rows={3}
          />
          {vectorText.trim() ? (
            parsedVector.values ? (
              <Alert variant="success" title="Vector inspector">
                {parsedVector.values.length} value{parsedVector.values.length === 1 ? "" : "s"}
                {column?.dimensions != null ? ` of ${column.dimensions} declared` : ""} · L2 norm{" "}
                {Math.sqrt(parsedVector.values.reduce((sum, value) => sum + value * value, 0)).toFixed(4)} · non-zero{" "}
                {parsedVector.values.filter((value) => value !== 0).length}
              </Alert>
            ) : (
              <Alert variant="error" title="Vector inspector">{parsedVector.error}</Alert>
            )
          ) : null}

          <Input
            label="Top-K"
            type="number"
            min="1"
            max={String(MAX_VECTOR_EXPLORER_TOPK)}
            value={topK}
            onChange={(event) => setTopK(event.currentTarget.value)}
          />

          {built.sql ? (
            <>
              <div className="nss-vector-sql-preview" tabIndex={0} aria-label="Generated vector SQL">
                <CodeBlock code={built.sql} language="sql" />
              </div>
              <Inline gap="sm" align="center" wrap>
                <CopyButton value={built.sql} label="Copy SQL" size="sm" />
              </Inline>
            </>
          ) : (
            <Alert variant="warning" title="Query is not ready">{built.error}</Alert>
          )}
        </Stack>
      </ModalBody>
      <ModalFooter>
        <Button variant="ghost" onClick={onClose}>Cancel</Button>
        <Button variant="outline" onClick={insert} disabled={!built.sql}>Insert into editor</Button>
        <Button variant="primary" onClick={run} disabled={!built.sql || busy}>
          {busy ? "Another query is running" : "Run search"}
        </Button>
      </ModalFooter>
    </Modal>
  );
}
