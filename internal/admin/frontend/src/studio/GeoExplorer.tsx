import { useCallback, useEffect, useMemo, useRef, useState, type MouseEvent } from "react";
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
  MAX_GEO_EXPLORER_LIMIT,
  MAX_GEO_EXPLORER_POLYGON_VERTICES,
  buildGeoSQL,
  geoCatalog,
  pointPreviewWKT,
  polygonPreviewWKT,
  validateGeoPoint,
  type GeoCatalogColumn,
  type GeoDrawPoint,
  type GeoQueryMode,
} from "./resultTools";
import { GeoView } from "./CellInspector";

const MODE_LABELS: Record<GeoQueryMode, string> = {
  point_radius: "Point + radius (DWITHIN)",
  polygon: "Polygon (WITHIN)",
};

// A fixed equirectangular world extent (lon -180..180 across the width, lat
// 90..-90 down the height), not a zoomable/pannable map — clicking gives a
// coarse starting coordinate; the numeric Longitude/Latitude fields are the
// precise, keyboard-accessible path and are always kept in sync with clicks.
const CANVAS_WIDTH = 320;
const CANVAS_HEIGHT = 180;
const GRATICULE_STEP_LON = 30;
const GRATICULE_STEP_LAT = 30;

function xFromLon(lon: number): number {
  return ((lon + 180) / 360) * CANVAS_WIDTH;
}
function yFromLat(lat: number): number {
  return ((90 - lat) / 180) * CANVAS_HEIGHT;
}

// Meters-to-degrees is a local equirectangular approximation (matching the
// disclaimer already used by the reused `GeoView` preview below) purely to
// give a visual sense of scale for the radius handle — never sent to the
// server, which evaluates DWITHIN's true haversine/geodesic distance itself.
function radiusDegrees(point: GeoDrawPoint, meters: number): { dLon: number; dLat: number } {
  const metersPerDegreeLat = 111_320;
  const cos = Math.max(Math.cos((point.lat * Math.PI) / 180), 0.01);
  return { dLon: meters / (metersPerDegreeLat * cos), dLat: meters / metersPerDegreeLat };
}

function pointFromClick(event: MouseEvent<SVGSVGElement>): GeoDrawPoint {
  const rect = event.currentTarget.getBoundingClientRect();
  const px = ((event.clientX - rect.left) / rect.width) * CANVAS_WIDTH;
  const py = ((event.clientY - rect.top) / rect.height) * CANVAS_HEIGHT;
  const lon = Math.round(((px / CANVAS_WIDTH) * 360 - 180) * 1e4) / 1e4;
  const lat = Math.round((90 - (py / CANVAS_HEIGHT) * 180) * 1e4) / 1e4;
  return { lon, lat };
}

function DrawCanvas({
  mode,
  point,
  radiusMeters,
  polygon,
  onClickPoint,
}: {
  mode: GeoQueryMode;
  point: GeoDrawPoint | null;
  radiusMeters: number | null;
  polygon: GeoDrawPoint[];
  onClickPoint: (point: GeoDrawPoint) => void;
}) {
  const graticule: { x1: number; y1: number; x2: number; y2: number }[] = [];
  for (let lon = -180; lon <= 180; lon += GRATICULE_STEP_LON) {
    graticule.push({ x1: xFromLon(lon), y1: 0, x2: xFromLon(lon), y2: CANVAS_HEIGHT });
  }
  for (let lat = -90; lat <= 90; lat += GRATICULE_STEP_LAT) {
    graticule.push({ x1: 0, y1: yFromLat(lat), x2: CANVAS_WIDTH, y2: yFromLat(lat) });
  }

  let radius: { cx: number; cy: number; rx: number; ry: number } | null = null;
  if (mode === "point_radius" && point && radiusMeters != null && radiusMeters > 0) {
    const { dLon, dLat } = radiusDegrees(point, radiusMeters);
    radius = { cx: xFromLon(point.lon), cy: yFromLat(point.lat), rx: (dLon / 360) * CANVAS_WIDTH, ry: (dLat / 180) * CANVAS_HEIGHT };
  }

  const polygonProjected = polygon.map((vertex) => ({ x: xFromLon(vertex.lon), y: yFromLat(vertex.lat) }));

  return (
    <svg
      className="nss-geo-draw"
      viewBox={`0 0 ${CANVAS_WIDTH} ${CANVAS_HEIGHT}`}
      // Decorative/convenience only: every coordinate it can set is already
      // reachable through the numeric fields and Add/Remove vertex buttons
      // rendered alongside it, so it is excluded from the accessibility tree
      // rather than given an incomplete keyboard interaction of its own.
      aria-hidden="true"
      onClick={(event) => onClickPoint(pointFromClick(event))}
    >
      <rect x="1" y="1" width={CANVAS_WIDTH - 2} height={CANVAS_HEIGHT - 2} rx="6" className="nss-geo-frame" />
      {graticule.map((line, index) => (
        <line key={index} x1={line.x1} y1={line.y1} x2={line.x2} y2={line.y2} className="nss-geo-draw-grid" />
      ))}
      {mode === "point_radius" && point ? (
        <>
          {radius ? <ellipse cx={radius.cx} cy={radius.cy} rx={radius.rx} ry={radius.ry} className="nss-geo-draw-radius" /> : null}
          <circle cx={xFromLon(point.lon)} cy={yFromLat(point.lat)} r="5" className="nss-geo-point" />
        </>
      ) : null}
      {mode === "polygon" && polygonProjected.length > 0 ? (
        <>
          {polygonProjected.length > 1 ? (
            <polygon
              points={[...polygonProjected, polygonProjected[0]].map((vertex) => `${vertex.x},${vertex.y}`).join(" ")}
              className="nss-geo-polygon"
            />
          ) : null}
          {polygonProjected.map((vertex, index) => (
            <circle key={index} cx={vertex.x} cy={vertex.y} r="4" className="nss-geo-draw-vertex" />
          ))}
        </>
      ) : null}
    </svg>
  );
}

export function GeoExplorer({
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

  const [mode, setMode] = useState<GeoQueryMode>("point_radius");
  const [lonText, setLonText] = useState("");
  const [latText, setLatText] = useState("");
  const [radiusText, setRadiusText] = useState("1000");
  const [vertexLonText, setVertexLonText] = useState("");
  const [vertexLatText, setVertexLatText] = useState("");
  const [vertexError, setVertexError] = useState<string | null>(null);
  const [polygon, setPolygon] = useState<GeoDrawPoint[]>([]);
  const [limit, setLimit] = useState("10");
  const request = useRef(0);

  const applyDetail = useCallback((next: StudioTableDetail) => {
    const catalog = geoCatalog(next);
    setDetail(next);
    setColumnName(catalog.columns[0]?.name ?? "");
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

  const catalog = useMemo(() => detail ? geoCatalog(detail) : { columns: [], indexes: [] }, [detail]);
  const column = catalog.columns.find((candidate: GeoCatalogColumn) => candidate.name === columnName);
  const matchedIndex = column ? catalog.indexes.find((index) => index.column === column.name) : undefined;

  const point: GeoDrawPoint | null = useMemo(() => {
    if (!lonText.trim() || !latText.trim()) return null;
    const lon = Number(lonText);
    const lat = Number(latText);
    if (!Number.isFinite(lon) || !Number.isFinite(lat)) return null;
    return { lon, lat };
  }, [lonText, latText]);
  const pointError = point ? validateGeoPoint(point) : null;

  const radiusMeters = radiusText.trim() ? Number(radiusText) : null;

  const built = useMemo(() => buildGeoSQL({
    table,
    column,
    mode,
    point,
    radiusMeters,
    polygon,
    limit: Number(limit),
  }), [column, limit, mode, point, polygon, radiusMeters, table]);

  const setPointFromCanvas = (next: GeoDrawPoint) => {
    if (mode === "point_radius") {
      setLonText(String(next.lon));
      setLatText(String(next.lat));
    } else {
      addVertex(next);
    }
  };

  const addVertex = (vertex: GeoDrawPoint) => {
    if (polygon.length >= MAX_GEO_EXPLORER_POLYGON_VERTICES) {
      setVertexError(`Polygon supports at most ${MAX_GEO_EXPLORER_POLYGON_VERTICES} vertices.`);
      return;
    }
    const error = validateGeoPoint(vertex);
    if (error) {
      setVertexError(error);
      return;
    }
    setVertexError(null);
    setPolygon((current) => [...current, vertex]);
  };

  const addVertexFromInputs = () => {
    if (!vertexLonText.trim() || !vertexLatText.trim()) {
      setVertexError("Enter both longitude and latitude.");
      return;
    }
    const lon = Number(vertexLonText);
    const lat = Number(vertexLatText);
    if (!Number.isFinite(lon) || !Number.isFinite(lat)) {
      setVertexError("Longitude and latitude must be finite numbers.");
      return;
    }
    addVertex({ lon, lat });
    setVertexLonText("");
    setVertexLatText("");
  };

  const removeVertex = (index: number) => {
    setPolygon((current) => current.filter((_, candidate) => candidate !== index));
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

  let previewValue: string | null = null;
  let previewType: "POINT" | "POLYGON" | null = null;
  if (mode === "point_radius" && point && !pointError) {
    previewValue = pointPreviewWKT(point);
    previewType = "POINT";
  } else if (mode === "polygon" && polygon.length >= 3) {
    previewValue = polygonPreviewWKT(polygon);
    previewType = "POLYGON";
  }

  return (
    <Modal open onClose={onClose} size="lg" ariaLabel="Geo Explorer" scrollable>
      <ModalHeader>
        <ModalTitle>Geo Explorer</ModalTitle>
      </ModalHeader>
      <ModalBody scrollable>
        <Stack gap="md">
          <Text size="sm" variant="muted">
            Builds one native DWITHIN or WITHIN statement against a POINT column from authorized catalog metadata
            (`docs/geo.md`). Coordinates are always longitude, then latitude. Insert only edits the active tab; Run
            search uses the same bounded NSQL stream, cancellation, history, and server-side RBAC as the main Run
            action.
          </Text>

          {tables.length === 0 ? (
            <Alert variant="warning" title="No visible tables">
              The authenticated catalog returned no tables available for geo search.
            </Alert>
          ) : (
            <Select
              id="geo-table"
              label="Table"
              options={tables.map((name) => ({ value: name, label: name }))}
              value={table}
              onChange={(value) => void requestTable(value)}
            />
          )}

          {loading ? (
            <Inline gap="sm" align="center" role="status">
              <Spinner size="sm" />
              <Text size="sm" variant="muted">Loading authorized POINT columns…</Text>
            </Inline>
          ) : loadError ? (
            <Alert variant="error" title="Could not load table metadata">{loadError}</Alert>
          ) : detail ? (
            catalog.columns.length === 0 ? (
              <Alert variant="warning" title="No POINT columns">
                This table has no visible POINT (or LOCATION) column.
              </Alert>
            ) : (
              <>
                <Select
                  id="geo-column"
                  label="POINT column"
                  options={catalog.columns.map((candidate) => ({ value: candidate.name, label: `${candidate.name} · ${candidate.type}` }))}
                  value={columnName}
                  onChange={setColumnName}
                />
                {matchedIndex ? (
                  <Inline gap="sm" align="center" wrap>
                    <Badge variant={matchedIndex.usable && matchedIndex.status.toLowerCase() === "valid" ? "success" : "warning"}>
                      {matchedIndex.status}
                    </Badge>
                    <Text size="xs" variant="muted">Candidate spatial index: {matchedIndex.name}.</Text>
                  </Inline>
                ) : (
                  <Text size="xs" variant="muted">
                    No spatial index found for this column. DWITHIN/WITHIN remain valid over a full scan.
                  </Text>
                )}
              </>
            )
          ) : null}

          <Select
            id="geo-mode"
            label="Query shape"
            options={(Object.keys(MODE_LABELS) as GeoQueryMode[]).map((value) => ({ value, label: MODE_LABELS[value] }))}
            value={mode}
            onChange={(value) => setMode(value as GeoQueryMode)}
          />

          <DrawCanvas mode={mode} point={point} radiusMeters={radiusMeters} polygon={polygon} onClickPoint={setPointFromCanvas} />
          <Text size="xs" variant="muted">
            Click the map above for an approximate coordinate, then refine it with the fields below — the map is a
            convenience and is not itself part of the keyboard/screen-reader path.
          </Text>

          {mode === "point_radius" ? (
            <>
              <Inline gap="sm" wrap>
                <Input
                  id="geo-lon"
                  label="Longitude"
                  type="number"
                  min="-180"
                  max="180"
                  step="any"
                  value={lonText}
                  onChange={(event) => setLonText(event.currentTarget.value)}
                  placeholder="-73.9857"
                />
                <Input
                  id="geo-lat"
                  label="Latitude"
                  type="number"
                  min="-90"
                  max="90"
                  step="any"
                  value={latText}
                  onChange={(event) => setLatText(event.currentTarget.value)}
                  placeholder="40.7484"
                />
                <Input
                  id="geo-radius"
                  label="Radius (meters)"
                  type="number"
                  min="0"
                  step="any"
                  value={radiusText}
                  onChange={(event) => setRadiusText(event.currentTarget.value)}
                />
              </Inline>
              {point && pointError ? <Alert variant="error" title="Invalid coordinate">{pointError}</Alert> : null}
            </>
          ) : (
            <Stack gap="sm">
              <Inline gap="sm" wrap align="end">
                <Input
                  id="geo-vertex-lon"
                  label="Longitude"
                  type="number"
                  min="-180"
                  max="180"
                  step="any"
                  value={vertexLonText}
                  onChange={(event) => setVertexLonText(event.currentTarget.value)}
                  placeholder="-74.1"
                />
                <Input
                  id="geo-vertex-lat"
                  label="Latitude"
                  type="number"
                  min="-90"
                  max="90"
                  step="any"
                  value={vertexLatText}
                  onChange={(event) => setVertexLatText(event.currentTarget.value)}
                  placeholder="40.6"
                />
                <Button variant="outline" size="sm" onClick={addVertexFromInputs}>Add vertex</Button>
                <Button variant="ghost" size="sm" onClick={() => { setPolygon([]); setVertexError(null); }} disabled={polygon.length === 0}>
                  Clear vertices
                </Button>
              </Inline>
              {vertexError ? <Alert variant="error" title="Invalid vertex">{vertexError}</Alert> : null}
              {polygon.length > 0 ? (
                <ul className="nss-geo-vertex-list">
                  {polygon.map((vertex, index) => (
                    <li key={index}>
                      <Text size="xs">{index + 1}. {vertex.lon}, {vertex.lat}</Text>
                      <Button variant="ghost" size="sm" onClick={() => removeVertex(index)}>Remove</Button>
                    </li>
                  ))}
                </ul>
              ) : (
                <Text size="xs" variant="muted">A polygon needs at least three vertices; the ring closes automatically.</Text>
              )}
            </Stack>
          )}

          <Input
            label="Result limit"
            type="number"
            min="1"
            max={String(MAX_GEO_EXPLORER_LIMIT)}
            value={limit}
            onChange={(event) => setLimit(event.currentTarget.value)}
          />

          {previewValue && previewType ? <GeoView value={previewValue} type={previewType} /> : null}

          {built.sql ? (
            <>
              <div className="nss-vector-sql-preview" tabIndex={0} aria-label="Generated geo SQL">
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
