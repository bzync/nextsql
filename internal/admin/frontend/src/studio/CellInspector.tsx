import { useMemo, useState } from "react";
import {
  Alert,
  Badge,
  Button,
  CodeBlock,
  CopyButton,
  Heading,
  Inline,
  Modal,
  ModalBody,
  ModalFooter,
  Stack,
  Tabs,
  TabsContent,
  TabsList,
  TabsTrigger,
  Text,
} from "@bzync/rui";
import {
  classifyStudioType,
  buildJSONPathQuery,
  inspectJSON,
  nativeJSONPath,
  parseGeo,
  parseVector,
  reportedJSONPathIndexes,
  timestampDetails,
  type GeoPoint,
  type JSONTreeNode,
  type ParsedGeo,
} from "./resultTools";
import type { StudioResultSet } from "../ops/api";

export type InspectedCell = {
  rowIndex: number;
  columnIndex: number;
  column: string;
  type: string;
  value: string | null;
};

export type JSONExplorerContext = {
  table: string;
  columns: string[];
  indexes: StudioResultSet;
  onInsertQuery: (sql: string) => void;
};

const MAX_VECTOR_ROWS = 256;

function JSONNode({
  node,
  selected,
  onSelect,
  column,
}: {
  node: JSONTreeNode;
  selected: JSONTreeNode;
  onSelect: (node: JSONTreeNode) => void;
  column: string;
}) {
  const path = nativeJSONPath(column, node.path);
  return (
    <li className={`nss-json-node nss-json-${node.kind}`}>
      <button
        type="button"
        className="nss-json-node-button"
        aria-pressed={selected === node}
        aria-label={`Select JSON path ${path}`}
        onClick={() => onSelect(node)}
      >
        <span className="nss-json-key">{node.label}</span>
        <span aria-hidden="true">: </span>
        <span className="nss-json-value">{node.value}</span>
      </button>
      {node.children?.length ? (
        <ul>
          {node.children.map((child, index) => (
            <JSONNode
              key={`${child.label}-${index}`}
              node={child}
              selected={selected}
              onSelect={onSelect}
              column={column}
            />
          ))}
        </ul>
      ) : null}
    </li>
  );
}

function JSONView({
  value,
  column,
  context,
  onClose,
}: {
  value: string;
  column: string;
  context?: JSONExplorerContext;
  onClose: () => void;
}) {
  const [selectedNode, setSelectedNode] = useState<JSONTreeNode | null>(null);
  const inspection = useMemo(() => {
    try {
      return { data: inspectJSON(value), error: null };
    } catch (error) {
      return { data: null, error: error instanceof Error ? error.message : String(error) };
    }
  }, [value]);
  if (!inspection.data) return <Alert variant="error" title="Invalid JSON">{inspection.error}</Alert>;
  const selected = selectedNode ?? inspection.data.root;
  const path = nativeJSONPath(column, selected.path);
  const sourceContext = context?.columns.includes(column) ? context : undefined;
  const indexes = sourceContext ? reportedJSONPathIndexes(sourceContext.indexes, column, selected.path) : undefined;
  const validIndexes = indexes?.filter((index) => index.status.toLowerCase() === "valid") ?? [];
  const otherIndexes = indexes?.filter((index) => index.status.toLowerCase() !== "valid") ?? [];

  const insertQuery = () => {
    if (!sourceContext) return;
    sourceContext.onInsertQuery(buildJSONPathQuery(sourceContext.table, column, selected.path));
    onClose();
  };

  return (
    <Stack gap="md">
      <Tabs defaultValue="tree">
        <TabsList aria-label="JSON representation">
          <TabsTrigger value="tree">Tree</TabsTrigger>
          <TabsTrigger value="raw">Raw</TabsTrigger>
        </TabsList>
        <TabsContent value="tree">
          <Stack gap="sm">
            {inspection.data.truncated ? (
              <Alert variant="warning" title="Tree preview bounded">
                Showing at most 1,024 nodes and 24 levels. Raw JSON remains available unchanged.
              </Alert>
            ) : null}
            <Text size="sm" variant="muted">
              {inspection.data.nodeCount.toLocaleString()} nodes. Select any node to inspect its native path.
            </Text>
            <ul className="nss-json-tree" aria-label="JSON path tree">
              <JSONNode
                node={inspection.data.root}
                selected={selected}
                onSelect={setSelectedNode}
                column={column}
              />
            </ul>
          </Stack>
        </TabsContent>
        <TabsContent value="raw">
          <div className="nss-inspector-code" tabIndex={0} aria-label="Raw JSON value">
            <CodeBlock code={value} language="json" />
          </div>
        </TabsContent>
      </Tabs>

      <section className="nss-json-path" aria-labelledby="nss-json-path-title">
        <Stack gap="sm">
          <Inline gap="sm" align="center" justify="between" wrap>
            <Heading id="nss-json-path-title" as="h3" size="xs">Selected native path</Heading>
            {selected.path.length === 0 ? (
              <Badge variant="muted">Root value</Badge>
            ) : sourceContext === undefined ? (
              <Badge variant="muted">Index status unavailable</Badge>
            ) : indexes === null ? (
              <Badge variant="muted">Index status ambiguous</Badge>
            ) : validIndexes.length > 0 ? (
              <Badge variant="success">Indexed</Badge>
            ) : otherIndexes.length > 0 ? (
              <Badge variant="warning">Index not valid</Badge>
            ) : (
              <Badge variant="muted">Not indexed</Badge>
            )}
          </Inline>
          <CodeBlock code={path} language="sql" className="nss-json-path-code" />
          {validIndexes.length > 0 ? (
            <Text size="sm" variant="muted">
              Reported by system.indexes: {validIndexes.map((index) => index.name).join(", ")}.
            </Text>
          ) : otherIndexes.length > 0 ? (
            <Text size="sm" variant="warning">
              system.indexes reports {otherIndexes.map((index) => `${index.name} (${index.status})`).join(", ")}.
            </Text>
          ) : indexes === null && sourceContext ? (
            <Text size="sm" variant="muted">
              This path contains a quoted or special object key. The current system.indexes text field does not preserve segment boundaries, so Studio will not guess.
            </Text>
          ) : !sourceContext ? (
            <Text size="sm" variant="muted">
              {context
                ? `The selected table does not expose ${column} as a queryable JSON column. Select its source table to enable index lookup and query generation.`
                : "Select the source table in Database explorer to compare this column with its authorized index metadata and generate a query."}
            </Text>
          ) : null}
          <Inline gap="sm" align="center" wrap>
            <CopyButton value={path} label="Copy JSON path" size="sm" />
            <Button variant="outline" size="sm" onClick={insertQuery} disabled={!sourceContext}>
              Insert path query
            </Button>
          </Inline>
          <Text size="xs" variant="muted">
            Insert only updates the active editor tab. Running the query still uses the authenticated NSQL session and server-side RBAC.
          </Text>
        </Stack>
      </section>
    </Stack>
  );
}

function VectorView({ value, type }: { value: string; type: string }) {
  const parsed = useMemo(() => {
    try {
      return { data: parseVector(value, type), error: null };
    } catch (error) {
      return { data: null, error: error instanceof Error ? error.message : String(error) };
    }
  }, [type, value]);
  if (!parsed.data) return <Alert variant="error" title="Invalid vector">{parsed.error}</Alert>;
  const rows = parsed.data.values.slice(0, MAX_VECTOR_ROWS);
  return (
    <Stack gap="md">
      <dl className="nss-cell-facts">
        <div><dt>Storage type</dt><dd>{type}</dd></div>
        <div><dt>Mode</dt><dd>{parsed.data.mode}</dd></div>
        <div><dt>Declared dimensions</dt><dd>{parsed.data.declaredDimensions?.toLocaleString() ?? "unknown"}</dd></div>
        <div><dt>Returned values</dt><dd>{parsed.data.values.length.toLocaleString()}</dd></div>
        <div><dt>Non-zero values</dt><dd>{parsed.data.nonZero.toLocaleString()}</dd></div>
        <div><dt>L2 norm</dt><dd>{parsed.data.l2Norm.toLocaleString(undefined, { maximumSignificantDigits: 8 })}</dd></div>
      </dl>
      <div className="nss-vector-values" tabIndex={0} aria-label="Vector values">
        <table>
          <thead><tr><th scope="col">Index</th><th scope="col">Value</th></tr></thead>
          <tbody>
            {rows.map((number, index) => (
              <tr key={`${parsed.data?.indices[index]}-${index}`}>
                <td>{parsed.data?.indices[index]}</td>
                <td>{number}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {parsed.data.values.length > MAX_VECTOR_ROWS ? (
        <Text variant="warning" size="sm">Showing the first {MAX_VECTOR_ROWS} values; copy the raw value for the complete vector.</Text>
      ) : null}
    </Stack>
  );
}

function project(point: GeoPoint, geo: ParsedGeo): GeoPoint {
  const width = Math.max(geo.bounds.maxX - geo.bounds.minX, 1e-12);
  const height = Math.max(geo.bounds.maxY - geo.bounds.minY, 1e-12);
  return {
    x: 12 + ((point.x - geo.bounds.minX) / width) * 296,
    y: 168 - ((point.y - geo.bounds.minY) / height) * 156,
  };
}

// The four fixed native types are always hard-coded WGS84 lon/lat degrees; the general GEOMETRY family
// is planar in an arbitrary column SRID and GEOGRAPHY is geodetic in an arbitrary column SRID — neither
// is necessarily WGS84 degrees, so the label must come from the declared column type, not the shape.
function coordinateOrderLabel(type: string): string {
  const normalized = type.trim().toUpperCase();
  if (normalized.startsWith("GEOMETRY")) return "x, y (planar, per column SRID)";
  if (normalized.startsWith("GEOGRAPHY")) return "longitude, latitude (geodetic, per column SRID)";
  return "longitude, latitude";
}

function GeoPartShape({ part, geo }: { part: { shape: "POINT" | "LINESTRING" | "POLYGON"; rings: GeoPoint[][] }; geo: ParsedGeo }) {
  return (
    <>
      {part.rings.map((ring, index) => {
        const projected = ring.map((point) => project(point, geo));
        return part.shape === "POINT" ? (
          <circle key={index} cx={projected[0].x} cy={projected[0].y} r="4" className="nss-geo-point" />
        ) : (
          <polyline
            key={index}
            points={projected.map((point) => `${point.x},${point.y}`).join(" ")}
            className={part.shape === "POLYGON" ? "nss-geo-polygon" : "nss-geo-line"}
          />
        );
      })}
    </>
  );
}

// Exported so the Geo Explorer (StudioWorkspace) can reuse the exact same
// bounded SVG coordinate-preview renderer for a not-yet-executed literal it
// is building, instead of duplicating the projection/rendering logic.
export function GeoView({ value, type }: { value: string; type: string }) {
  const parsed = useMemo(() => {
    try {
      return { data: parseGeo(value), error: null };
    } catch (error) {
      return { data: null, error: error instanceof Error ? error.message : String(error) };
    }
  }, [value]);
  if (!parsed.data) {
    return (
      <Stack gap="sm">
        <Alert variant="info" title="Preview unavailable">{parsed.error}</Alert>
        <div className="nss-inspector-code" tabIndex={0} aria-label="Raw geometry value">
          <CodeBlock code={value} language="text" />
        </div>
      </Stack>
    );
  }
  const geo = parsed.data;
  const hasParts = geo.parts.length > 0;
  let projected: GeoPoint[][] = [];
  if (!hasParts) {
    projected = geo.rings.map((ring) => ring.map((point) => project(point, geo)));
    if (geo.shape === "BOX" && projected[0]?.length === 2) {
      const [first, second] = projected[0];
      projected = [[first, { x: second.x, y: first.y }, second, { x: first.x, y: second.y }, first]];
    }
  }
  return (
    <Stack gap="md">
      <dl className="nss-cell-facts">
        <div><dt>Type</dt><dd>{type}</dd></div>
        <div><dt>Shape</dt><dd>{geo.shape}</dd></div>
        <div><dt>Coordinate order</dt><dd>{coordinateOrderLabel(type)}</dd></div>
        {hasParts ? <div><dt>Parts</dt><dd>{geo.parts.length.toLocaleString()}</dd></div> : null}
        <div><dt>Points</dt><dd>{geo.pointCount.toLocaleString()}</dd></div>
        <div><dt>Bounds</dt><dd>{geo.bounds.minX}, {geo.bounds.minY} → {geo.bounds.maxX}, {geo.bounds.maxY}</dd></div>
      </dl>
      <svg className="nss-geo-preview" viewBox="0 0 320 180" role="img" aria-label={`${geo.shape} coordinate preview`}>
        <rect x="1" y="1" width="318" height="178" rx="6" className="nss-geo-frame" />
        {hasParts ? (
          geo.parts.map((part, index) => <GeoPartShape key={index} part={part} geo={geo} />)
        ) : geo.shape === "POINT" ? (
          <circle cx={projected[0][0].x} cy={projected[0][0].y} r="5" className="nss-geo-point" />
        ) : projected.map((ring, index) => (
          <polyline
            key={index}
            points={ring.map((point) => `${point.x},${point.y}`).join(" ")}
            className={geo.shape === "POLYGON" || geo.shape === "BOX" ? "nss-geo-polygon" : "nss-geo-line"}
          />
        ))}
      </svg>
      <Text size="sm" variant="muted">Coordinate preview is normalized to the cell bounds; it is not a distance-preserving map projection.</Text>
      <div className="nss-inspector-code" tabIndex={0} aria-label="Raw geometry value">
        <CodeBlock code={value} language="text" />
      </div>
    </Stack>
  );
}

function TimestampView({ value }: { value: string }) {
  const parsed = useMemo(() => {
    try {
      return { data: timestampDetails(value), error: null };
    } catch (error) {
      return { data: null, error: error instanceof Error ? error.message : String(error) };
    }
  }, [value]);
  if (!parsed.data) return <Alert variant="error" title="Invalid TIMESTAMPTZ">{parsed.error}</Alert>;
  return (
    <dl className="nss-cell-facts">
      <div><dt>Server value</dt><dd>{value}</dd></div>
      <div><dt>UTC</dt><dd>{parsed.data.utc}</dd></div>
      <div><dt>Browser timezone</dt><dd>{parsed.data.local}</dd></div>
      <div><dt>Unix epoch</dt><dd>{parsed.data.epochMilliseconds.toLocaleString()} ms</dd></div>
    </dl>
  );
}

export function CellInspector({
  cell,
  open,
  onClose,
  jsonContext,
}: {
  cell: InspectedCell | null;
  open: boolean;
  onClose: () => void;
  jsonContext?: JSONExplorerContext;
}) {
  if (!cell) return null;
  const kind = classifyStudioType(cell.type);
  return (
    <Modal
      open={open}
      onClose={onClose}
      size="xl"
      title={kind === "json" ? `JSON Explorer · ${cell.column}` : `${cell.column} · ${cell.type}`}
      description={`Row ${cell.rowIndex + 1}, column ${cell.columnIndex + 1}. Raw values are preserved for copy and export.`}
      scrollable
      closeAriaLabel="Close cell inspector"
    >
      <ModalBody scrollable>
        {cell.value === null ? (
          <Alert variant="info" title="SQL NULL">This cell has no value.</Alert>
        ) : kind === "json" ? (
          <JSONView value={cell.value} column={cell.column} context={jsonContext} onClose={onClose} />
        ) : kind === "vector" ? (
          <VectorView value={cell.value} type={cell.type} />
        ) : kind === "geo" ? (
          <GeoView value={cell.value} type={cell.type} />
        ) : kind === "timestamptz" ? (
          <TimestampView value={cell.value} />
        ) : (
          <div className="nss-inspector-code" tabIndex={0} aria-label="Raw cell value">
            <CodeBlock code={cell.value} language="text" />
          </div>
        )}
      </ModalBody>
      <ModalFooter>
        <CopyButton value={cell.value ?? "NULL"} label="Copy raw value" size="sm" />
        <Button variant="outline" size="sm" onClick={onClose}>Close</Button>
      </ModalFooter>
    </Modal>
  );
}
