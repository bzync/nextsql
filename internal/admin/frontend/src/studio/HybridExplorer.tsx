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
  MAX_FULLTEXT_EXPLORER_QUERY_CHARS,
  MAX_VECTOR_EXPLORER_TOPK,
  buildHybridSQL,
  hybridCatalog,
  parseVectorLiteralInput,
  type FilterOperator,
  type HybridResultContext,
  type VectorMetric,
} from "./resultTools";

const NO_FILTER = "__none__";
const AUTOMATIC_INDEX = "__automatic__";

const METRIC_LABELS: Record<VectorMetric, string> = {
  cosine: "COSINE (similarity)",
  l2: "L2 (Euclidean distance)",
  inner_product: "INNER_PRODUCT (dot product)",
  hamming: "HAMMING (differing bits)",
};

const OPERATOR_LABELS: Record<FilterOperator, string> = {
  "=": "= (equals)",
  "<>": "<> (not equals)",
  "<": "< (less than)",
  "<=": "<= (less than or equal)",
  ">": "> (greater than)",
  ">=": ">= (greater than or equal)",
  is_null: "IS NULL",
  is_not_null: "IS NOT NULL",
};

export function HybridExplorer({
  onClose,
  onInsert,
  onRun,
  onExplain,
  tables,
  initialTable,
  initialDetail,
  loadTable,
  busy,
}: {
  onClose: () => void;
  onInsert: (sql: string) => void;
  onRun: (sql: string, context: HybridResultContext) => void;
  onExplain: (sql: string) => void;
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

  const [filterColumnName, setFilterColumnName] = useState(NO_FILTER);
  const [filterOperator, setFilterOperator] = useState<FilterOperator>("=");
  const [filterValue, setFilterValue] = useState("");

  const [indexName, setIndexName] = useState(AUTOMATIC_INDEX);
  const [fullTextColumns, setFullTextColumns] = useState<string[]>([]);
  const [query, setQuery] = useState("");

  const [vectorColumnName, setVectorColumnName] = useState("");
  const [metric, setMetric] = useState<VectorMetric>("cosine");
  const [vectorText, setVectorText] = useState("");

  const [limit, setLimit] = useState("10");
  const request = useRef(0);

  const applyDetail = useCallback((next: StudioTableDetail) => {
    const catalog = hybridCatalog(next);
    setDetail(next);
    setFilterColumnName(NO_FILTER);
    setFilterOperator("=");
    setFilterValue("");
    const preferredIndex = catalog.fullText.indexes.find((index) => index.usable && index.status.toLowerCase() === "valid");
    setIndexName(preferredIndex?.name ?? AUTOMATIC_INDEX);
    setFullTextColumns(preferredIndex?.columns ?? catalog.fullText.columns.slice(0, 1));
    const preferredVector = catalog.vector.columns[0];
    setVectorColumnName(preferredVector?.name ?? "");
    setMetric(preferredVector?.metrics[0] ?? "cosine");
  }, []);

  const requestTable = useCallback(async (name: string, known?: StudioTableDetail | null) => {
    const id = ++request.current;
    setTable(name);
    setDetail(null);
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

  const catalog = useMemo(
    () => detail ? hybridCatalog(detail) : { filterColumns: [], fullText: { columns: [], primaryColumns: [], indexes: [] }, vector: { columns: [], indexes: [] } },
    [detail],
  );
  const filterColumn = catalog.filterColumns.find((candidate) => candidate.name === filterColumnName);
  const selectedIndex = catalog.fullText.indexes.find((index) => index.name === indexName);
  const vectorColumn = catalog.vector.columns.find((candidate) => candidate.name === vectorColumnName);
  const matchedVectorIndex = vectorColumn ? catalog.vector.indexes.find((index) => index.column === vectorColumn.name) : undefined;
  const parsedVector = useMemo(() => parseVectorLiteralInput(vectorText, vectorColumn), [vectorColumn, vectorText]);

  const built = useMemo(() => buildHybridSQL({
    table,
    filter: { column: filterColumn, operator: filterOperator, value: filterValue },
    fullTextColumns,
    fullTextIndex: selectedIndex,
    query,
    vectorColumn,
    vectorValues: parsedVector.values,
    metric,
    limit: Number(limit),
  }), [filterColumn, filterOperator, filterValue, fullTextColumns, limit, metric, parsedVector.values, query, selectedIndex, table, vectorColumn]);

  const chooseFullTextIndex = (name: string) => {
    setIndexName(name);
    if (name === AUTOMATIC_INDEX) {
      if (fullTextColumns.length === 0) setFullTextColumns(catalog.fullText.columns.slice(0, 1));
      return;
    }
    const selected = catalog.fullText.indexes.find((index) => index.name === name);
    setFullTextColumns(selected?.columns ?? []);
  };

  const chooseVectorColumn = (name: string) => {
    setVectorColumnName(name);
    const next = catalog.vector.columns.find((candidate) => candidate.name === name);
    if (next && !next.metrics.includes(metric)) setMetric(next.metrics[0]);
  };

  const insert = () => {
    if (!built.sql) return;
    onInsert(built.sql);
    onClose();
  };

  const explain = () => {
    if (!built.sql || busy) return;
    onExplain(`EXPLAIN ANALYZE ${built.sql}`);
    onClose();
  };

  const run = () => {
    if (!built.sql || busy) return;
    onRun(built.sql, { kind: "hybrid", indexName: selectedIndex?.name ?? null });
    onClose();
  };

  return (
    <Modal open onClose={onClose} size="lg" ariaLabel="Hybrid Explorer" scrollable>
      <ModalHeader>
        <ModalTitle>Hybrid Explorer</ModalTitle>
      </ModalHeader>
      <ModalBody scrollable>
        <Stack gap="md">
          <Text size="sm" variant="muted">
            Combines an optional structured filter with full-text SEARCH and vector NEAREST into one native hybrid
            statement (`docs/vector.md`: WHERE + SEARCH + NEAREST is planned as one physical plan). Insert only edits
            the active tab; Run search and Explain use the same bounded NSQL stream as the main Run action.
          </Text>

          {tables.length === 0 ? (
            <Alert variant="warning" title="No visible tables">
              The authenticated catalog returned no tables available for hybrid search.
            </Alert>
          ) : (
            <Select
              id="hybrid-table"
              label="Table"
              options={tables.map((name) => ({ value: name, label: name }))}
              value={table}
              onChange={(value) => void requestTable(value)}
            />
          )}

          {loading ? (
            <Inline gap="sm" align="center" role="status">
              <Spinner size="sm" />
              <Text size="sm" variant="muted">Loading authorized columns and indexes…</Text>
            </Inline>
          ) : loadError ? (
            <Alert variant="error" title="Could not load table metadata">{loadError}</Alert>
          ) : detail ? (
            <>
              <Text size="sm" weight="medium">Structured filter (optional)</Text>
              <Select
                id="hybrid-filter-column"
                label="Filter column"
                options={[
                  { value: NO_FILTER, label: "No filter" },
                  ...catalog.filterColumns.map((column) => ({ value: column.name, label: `${column.name} · ${column.type}` })),
                ]}
                value={filterColumnName}
                onChange={setFilterColumnName}
              />
              {filterColumn ? (
                <>
                  <Select
                    id="hybrid-filter-operator"
                    label="Operator"
                    options={(Object.keys(OPERATOR_LABELS) as FilterOperator[]).map((value) => ({ value, label: OPERATOR_LABELS[value] }))}
                    value={filterOperator}
                    onChange={(value) => setFilterOperator(value as FilterOperator)}
                  />
                  {filterOperator !== "is_null" && filterOperator !== "is_not_null" ? (
                    <Input
                      id="hybrid-filter-value"
                      label="Value"
                      value={filterValue}
                      onChange={(event) => setFilterValue(event.currentTarget.value)}
                      placeholder={filterColumn.kind === "numeric" ? "42" : "text value"}
                    />
                  ) : null}
                </>
              ) : null}

              <Text size="sm" weight="medium">Full-text SEARCH</Text>
              {catalog.fullText.columns.length === 0 ? (
                <Alert variant="warning" title="No searchable columns">
                  This table has no visible STRING or TEXT columns.
                </Alert>
              ) : (
                <>
                  <Select
                    id="hybrid-fulltext-index"
                    label="Full-text index"
                    options={[
                      { value: AUTOMATIC_INDEX, label: "Automatic / sequential fallback" },
                      ...catalog.fullText.indexes.map((index) => ({
                        value: index.name,
                        label: `${index.name} · ${index.status}${index.columns.length ? ` · ${index.columns.join(", ")}` : ""}`,
                      })),
                    ]}
                    value={indexName}
                    onChange={chooseFullTextIndex}
                  />
                  <Select
                    id="hybrid-fulltext-columns"
                    multiple
                    label="Search columns (catalog order)"
                    placeholder="Choose one to eight STRING/TEXT columns"
                    options={catalog.fullText.columns.map((name) => ({ value: name, label: name }))}
                    value={fullTextColumns}
                    disabled={indexName !== AUTOMATIC_INDEX}
                    onChange={setFullTextColumns}
                  />
                </>
              )}
              <Input
                id="hybrid-query"
                label="Search phrase"
                value={query}
                maxLength={MAX_FULLTEXT_EXPLORER_QUERY_CHARS}
                onChange={(event) => setQuery(event.currentTarget.value)}
                placeholder="database performance"
              />

              <Text size="sm" weight="medium">Vector NEAREST</Text>
              {catalog.vector.columns.length === 0 ? (
                <Alert variant="warning" title="No vector columns">
                  This table has no visible VECTOR, BITVECTOR, or SPARSEVECTOR column.
                </Alert>
              ) : (
                <>
                  <Select
                    id="hybrid-vector-column"
                    label="Vector column"
                    options={catalog.vector.columns.map((candidate) => ({ value: candidate.name, label: `${candidate.name} · ${candidate.type}` }))}
                    value={vectorColumnName}
                    onChange={chooseVectorColumn}
                  />
                  {matchedVectorIndex ? (
                    <Inline gap="sm" align="center" wrap>
                      <Badge variant={matchedVectorIndex.usable && matchedVectorIndex.status.toLowerCase() === "valid" ? "success" : "warning"}>
                        {matchedVectorIndex.status}
                      </Badge>
                      <Text size="xs" variant="muted">Candidate index: {matchedVectorIndex.name}.</Text>
                    </Inline>
                  ) : null}
                  <Select
                    id="hybrid-metric"
                    label="Ranking metric"
                    options={(vectorColumn?.metrics ?? ["cosine", "l2", "inner_product", "hamming"] as VectorMetric[]).map((value) => ({
                      value,
                      label: METRIC_LABELS[value],
                    }))}
                    value={metric}
                    onChange={(value) => setMetric(value as VectorMetric)}
                  />
                  <Textarea
                    id="hybrid-vector-input"
                    label="Query vector"
                    value={vectorText}
                    onChange={(event) => setVectorText(event.currentTarget.value)}
                    placeholder="1, 0, 0.5, 0 or [1, 0, 0.5, 0]"
                    rows={2}
                  />
                  {vectorText.trim() ? (
                    parsedVector.values ? (
                      <Alert variant="success" title="Vector inspector">
                        {parsedVector.values.length} value{parsedVector.values.length === 1 ? "" : "s"}
                        {vectorColumn?.dimensions != null ? ` of ${vectorColumn.dimensions} declared` : ""}
                      </Alert>
                    ) : (
                      <Alert variant="error" title="Vector inspector">{parsedVector.error}</Alert>
                    )
                  ) : null}
                </>
              )}
            </>
          ) : null}

          <Input
            label="Result limit"
            type="number"
            min="1"
            max={String(MAX_VECTOR_EXPLORER_TOPK)}
            value={limit}
            onChange={(event) => setLimit(event.currentTarget.value)}
          />

          {built.sql ? (
            <>
              <div className="nss-vector-sql-preview" tabIndex={0} aria-label="Generated hybrid SQL">
                <CodeBlock code={built.sql} language="sql" />
              </div>
              <Inline gap="sm" align="center" wrap>
                <CopyButton value={built.sql} label="Copy SQL" size="sm" />
                <Text size="xs" variant="muted">
                  Results retain the server's fused rank order. No single fused BM25+vector score is exposed.
                </Text>
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
        <Button variant="outline" onClick={explain} disabled={!built.sql || busy}>Explain</Button>
        <Button variant="primary" onClick={run} disabled={!built.sql || busy}>
          {busy ? "Another query is running" : "Run search"}
        </Button>
      </ModalFooter>
    </Modal>
  );
}
