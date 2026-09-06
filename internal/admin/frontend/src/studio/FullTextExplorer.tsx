import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  Alert,
  Badge,
  Button,
  CodeBlock,
  CopyButton,
  Input,
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
} from "@bzync/rui";
import type { StudioTableDetail } from "../ops/api";
import {
  MAX_FULLTEXT_EXPLORER_QUERY_CHARS,
  MAX_FULLTEXT_EXPLORER_ROWS,
  buildFullTextSQL,
  fullTextCatalog,
  type FullTextOutput,
  type FullTextResultContext,
} from "./resultTools";

const AUTOMATIC_INDEX = "__automatic__";

export function FullTextExplorer({
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
  onRun: (sql: string, context: FullTextResultContext) => void;
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
  const [indexName, setIndexName] = useState(AUTOMATIC_INDEX);
  const [columns, setColumns] = useState<string[]>([]);
  const [query, setQuery] = useState("");
  const [output, setOutput] = useState<FullTextOutput>("snippet");
  const [limit, setLimit] = useState("20");
  const request = useRef(0);

  const applyDetail = useCallback((next: StudioTableDetail) => {
    const catalog = fullTextCatalog(next);
    const preferred = catalog.indexes.find((index) => index.usable && index.status.toLowerCase() === "valid");
    setDetail(next);
    setIndexName(preferred?.name ?? AUTOMATIC_INDEX);
    setColumns(preferred?.columns ?? catalog.columns.slice(0, 1));
  }, []);

  const requestTable = useCallback(async (name: string, known?: StudioTableDetail | null) => {
    const id = ++request.current;
    setTable(name);
    setDetail(null);
    setIndexName(AUTOMATIC_INDEX);
    setColumns([]);
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

  const catalog = useMemo(() => detail ? fullTextCatalog(detail) : { columns: [], primaryColumns: [], indexes: [] }, [detail]);
  const selectedIndex = catalog.indexes.find((index) => index.name === indexName);
  const built = useMemo(() => buildFullTextSQL({
    table,
    columns,
    query,
    output,
    limit: Number(limit),
    index: selectedIndex,
    primaryColumns: catalog.primaryColumns,
  }), [catalog.primaryColumns, columns, indexName, limit, output, query, selectedIndex, table]);

  const chooseIndex = (name: string) => {
    setIndexName(name);
    if (name === AUTOMATIC_INDEX) {
      if (columns.length === 0) setColumns(catalog.columns.slice(0, 1));
      return;
    }
    const selected = catalog.indexes.find((index) => index.name === name);
    setColumns(selected?.columns ?? []);
  };

  const insert = () => {
    if (!built.sql) return;
    onInsert(built.sql);
    onClose();
  };

  const run = () => {
    if (!built.sql || busy) return;
    onRun(built.sql, {
      kind: "fulltext",
      indexName: selectedIndex?.name ?? null,
      output,
    });
    onClose();
  };

  return (
    <Modal open onClose={onClose} size="lg" ariaLabel="Full-text Explorer" scrollable>
      <ModalHeader>
        <ModalTitle>Full-text Explorer</ModalTitle>
      </ModalHeader>
      <ModalBody scrollable>
        <Stack gap="md">
          <Text size="sm" variant="muted">
            Builds one native SEARCH statement from authorized catalog metadata. Insert only edits the active tab;
            Run search uses the same bounded NSQL stream, cancellation, history, and server-side RBAC as the main Run action.
          </Text>

          {tables.length === 0 ? (
            <Alert variant="warning" title="No visible tables">
              The authenticated catalog returned no tables available for full-text search.
            </Alert>
          ) : (
            <Select
              id="fulltext-table"
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
              {catalog.columns.length === 0 ? (
                <Alert variant="warning" title="No searchable columns">
                  This table has no visible STRING or TEXT columns. NextSQL FULLTEXT indexes and SEARCH require those types.
                </Alert>
              ) : null}
              <Select
                id="fulltext-index"
                label="Full-text index"
                options={[
                  { value: AUTOMATIC_INDEX, label: "Automatic / sequential fallback" },
                  ...catalog.indexes.map((index) => ({
                    value: index.name,
                    label: `${index.name} · ${index.status}${index.columns.length ? ` · ${index.columns.join(", ")}` : ""}`,
                  })),
                ]}
                value={indexName}
                onChange={chooseIndex}
              />
              {selectedIndex ? (
                <Inline gap="sm" align="center" wrap>
                  <Badge variant={selectedIndex.usable && selectedIndex.status.toLowerCase() === "valid" ? "success" : "warning"}>
                    {selectedIndex.status}
                  </Badge>
                  <Text size="xs" variant="muted">
                    Candidate index: {selectedIndex.name}. The optimizer remains authoritative; use EXPLAIN to verify the chosen access path.
                  </Text>
                </Inline>
              ) : (
                <Text size="xs" variant="muted">
                  No index hint is generated. SEARCH remains valid and may use an exact matching index or its bounded sequential fallback.
                </Text>
              )}
              {selectedIndex && !selectedIndex.usable ? (
                <Alert variant="warning" title="Index metadata is ambiguous">
                  {selectedIndex.reason} Studio will not guess field boundaries from the catalog's comma-delimited text.
                </Alert>
              ) : null}
              <Select
                id="fulltext-columns"
                multiple
                label="Search columns (catalog order)"
                placeholder="Choose one to eight STRING/TEXT columns"
                options={catalog.columns.map((name) => ({ value: name, label: name }))}
                value={columns}
                disabled={indexName !== AUTOMATIC_INDEX}
                onChange={(value) => setColumns(value)}
              />
            </>
          ) : null}

          <Input
            id="fulltext-query"
            label="Search phrase"
            value={query}
            maxLength={MAX_FULLTEXT_EXPLORER_QUERY_CHARS}
            onChange={(event) => setQuery(event.currentTarget.value)}
            placeholder="database performance or &quot;exact phrase&quot;"
          />
          <Text size="xs" variant="muted">
            Native phrase quotes, trailing * prefix search, trailing ~ fuzzy search, and typo tolerance pass through unchanged.
          </Text>

          <Select
            id="fulltext-output"
            label="Matched-text output"
            options={[
              { value: "snippet", label: "SNIPPET for each search column" },
              { value: "highlight", label: "HIGHLIGHT for each search column" },
              { value: "rows", label: "Rows only" },
            ]}
            value={output}
            onChange={(value) => setOutput(value as FullTextOutput)}
          />
          <Input
            label="Result limit"
            type="number"
            min="1"
            max={String(MAX_FULLTEXT_EXPLORER_ROWS)}
            value={limit}
            onChange={(event) => setLimit(event.currentTarget.value)}
          />

          {built.sql ? (
            <>
              <div className="nss-fulltext-sql-preview" tabIndex={0} aria-label="Generated full-text SQL">
                <CodeBlock code={built.sql} language="sql" />
              </div>
              <Inline gap="sm" align="center" wrap>
                <CopyButton value={built.sql} label="Copy SQL" size="sm" />
                <Text size="xs" variant="muted">
                  Results retain the server's BM25 order. Numeric BM25 scores are not exposed by the current result schema.
                </Text>
              </Inline>
            </>
          ) : (
            <Alert variant="warning" title="Query is not ready">{built.error}</Alert>
          )}
          {output !== "rows" ? (
            <Text size="xs" variant="muted">
              HIGHLIGHT/SNIPPET markers are shown as literal result text, never interpreted as HTML.
            </Text>
          ) : null}
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
