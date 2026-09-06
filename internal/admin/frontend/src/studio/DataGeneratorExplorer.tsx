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
} from "@bzync/rui";
import type { StudioTableDetail } from "../ops/api";
import {
  MAX_DATAGEN_ROWS,
  buildDataGeneratorSQL,
  dataGenColumns,
  dataGenEffectiveStrategy,
  type DataGenColumn,
  type DataGenStrategy,
} from "./resultTools";

const STRATEGY_LABELS: Record<DataGenStrategy, string> = {
  skip: "Skip (use DEFAULT / NULL)",
  sequence: "Sequence 1, 2, 3…",
  "random-int": "Random integer",
  "random-decimal": "Random decimal",
  lorem: "Lorem words",
  label: "Name + row number",
  uuid: "Random UUID",
  "bool-random": "Random true/false",
  "bool-alternate": "Alternating true/false",
  now: "NOW()",
  "random-timestamp": "Random timestamp (2025 window)",
  "json-empty": "Empty object {}",
};

const PREVIEW_LINE_LIMIT = 160;

export function DataGeneratorExplorer({
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
  const [rowCount, setRowCount] = useState("25");
  const [seed, setSeed] = useState("1");
  const [strategies, setStrategies] = useState<Record<string, DataGenStrategy>>({});
  const request = useRef(0);

  const requestTable = useCallback(async (name: string, known?: StudioTableDetail | null) => {
    const id = ++request.current;
    setTable(name);
    setDetail(null);
    setStrategies({});
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
  const state = useMemo(() => ({
    table,
    rowCount: Number(rowCount),
    seed: Number(seed),
    strategies,
  }), [table, rowCount, seed, strategies]);
  const built = useMemo(() => buildDataGeneratorSQL(state, columns), [state, columns]);

  const preview = useMemo(() => {
    if (!built.sql) return null;
    const lines = built.sql.split("\n");
    if (lines.length <= PREVIEW_LINE_LIMIT) return { text: built.sql, hidden: 0 };
    return { text: lines.slice(0, PREVIEW_LINE_LIMIT).join("\n"), hidden: lines.length - PREVIEW_LINE_LIMIT };
  }, [built]);

  const setStrategy = (name: string, value: DataGenStrategy) =>
    setStrategies((current) => ({ ...current, [name]: value }));

  const insert = () => {
    if (!built.sql) return;
    onInsert(built.sql);
    onClose();
  };

  return (
    <Modal open onClose={onClose} size="lg" ariaLabel="Generate development data" scrollable>
      <ModalHeader>
        <ModalTitle>Generate development data</ModalTitle>
      </ModalHeader>
      <ModalBody scrollable>
        <Stack gap="md">
          <Text size="sm" variant="muted">
            Builds INSERT statements of synthetic rows from authorized catalog metadata and loads them into the
            active editor tab (replacing its contents) — it never runs anything here. The same seed always
            produces the same SQL.
          </Text>

          {tables.length === 0 ? (
            <Alert variant="warning" title="No visible tables">
              The authenticated catalog returned no tables you can populate.
            </Alert>
          ) : (
            <Select
              id="datagen-table"
              label="Table"
              options={tables.map((name) => ({ value: name, label: name }))}
              value={table}
              onChange={(value) => void requestTable(value)}
            />
          )}

          <Inline gap="md" wrap>
            <Input
              id="datagen-rows"
              label="Rows"
              type="number"
              min="1"
              max={String(MAX_DATAGEN_ROWS)}
              value={rowCount}
              onChange={(event) => setRowCount(event.currentTarget.value)}
            />
            <Input
              id="datagen-seed"
              label="Seed"
              type="number"
              min="0"
              value={seed}
              onChange={(event) => setSeed(event.currentTarget.value)}
            />
          </Inline>

          {loading ? (
            <Inline gap="sm" align="center" role="status">
              <Spinner size="sm" />
              <Text size="sm" variant="muted">Loading authorized columns…</Text>
            </Inline>
          ) : loadError ? (
            <Alert variant="error" title="Could not load table metadata">{loadError}</Alert>
          ) : detail ? (
            columns.length === 0 ? (
              <Alert variant="warning" title="No columns">This table exposes no columns to populate.</Alert>
            ) : (
              <Stack gap="sm" aria-label="Column fill strategies">
                {columns.map((column) => {
                  const effective = dataGenEffectiveStrategy(state, column);
                  return (
                    <Inline key={column.name} gap="sm" align="center" wrap>
                      <Select
                        id={`datagen-col-${column.name}`}
                        label={`${column.name} · ${column.type}`}
                        options={column.strategies.map((value) => ({ value, label: STRATEGY_LABELS[value] }))}
                        value={effective}
                        onChange={(value) => setStrategy(column.name, value as DataGenStrategy)}
                      />
                      {column.isPrimary ? <Badge variant="muted">PK</Badge> : null}
                      {column.notNull ? <Badge variant="muted">NOT NULL</Badge> : null}
                      {!column.supported ? (
                        <Badge variant="warning">type not generatable</Badge>
                      ) : null}
                    </Inline>
                  );
                })}
              </Stack>
            )
          ) : null}

          {built.sql ? (
            <>
              <div className="nss-datagen-sql-preview" tabIndex={0} aria-label="Generated INSERT SQL">
                <CodeBlock code={preview?.text ?? built.sql} language="sql" />
              </div>
              {preview && preview.hidden > 0 ? (
                <Text size="xs" variant="muted">
                  Preview shows the first {PREVIEW_LINE_LIMIT} lines; {preview.hidden.toLocaleString()} more will be
                  inserted ({built.rows.toLocaleString()} rows across {built.statements.toLocaleString()} statement
                  {built.statements === 1 ? "" : "s"}).
                </Text>
              ) : (
                <Text size="xs" variant="muted">
                  {built.rows.toLocaleString()} row{built.rows === 1 ? "" : "s"} across {built.statements.toLocaleString()}{" "}
                  statement{built.statements === 1 ? "" : "s"}.
                </Text>
              )}
              <Inline gap="sm" align="center" wrap>
                <CopyButton value={built.sql} label="Copy SQL" size="sm" />
              </Inline>
            </>
          ) : detail && !loading ? (
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
