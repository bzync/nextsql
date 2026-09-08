import type { StudioResultSet, StudioTableDetail } from "../ops/api";

export const MAX_RESULT_EXPORT_BYTES = 64 << 20;
export const MAX_JSON_TREE_NODES = 1_024;
export const MAX_JSON_TREE_DEPTH = 24;
export const MAX_INSPECTED_VECTOR_VALUES = 65_536;
export const MAX_GEO_PREVIEW_POINTS = 2_048;
export const MAX_GEO_PREVIEW_PARTS = 512;
export const MAX_GEO_COLLECTION_DEPTH = 8;
export const MAX_EXPLAIN_DEPTH = 64;
export const MAX_PLAN_COMPARISON_NODES = 512;
export const MAX_EXPLAIN_PROFILE_NODES = 512;
export const MAX_FULLTEXT_EXPLORER_QUERY_CHARS = 4_096;
export const MAX_FULLTEXT_EXPLORER_ROWS = 100;
export const MAX_VECTOR_EXPLORER_TOPK = 100;
export const MAX_GEO_EXPLORER_LIMIT = 100;
export const MAX_TABLE_CONSTRAINT_ROWS = 2_048;
// Well under `docs/geo.md`'s 256-total-vertex storage cap, since the drawn
// ring is auto-closed with one extra repeated vertex before it is sent.
export const MAX_GEO_EXPLORER_POLYGON_VERTICES = 64;

const spreadsheetFormula = /^[\t\r ]*[=+\-@]/;

export type ExportFormat = "csv" | "json";
export type NativeCellKind = "json" | "vector" | "geo" | "timestamptz" | "text";

export type ResultExport = {
  content: string;
  extension: ExportFormat;
  mime: string;
  rowCount: number;
};

export type JSONTreeNode = {
  label: string;
  kind: "object" | "array" | "string" | "number" | "boolean" | "null" | "limit";
  path: JSONPathPart[];
  value?: string;
  children?: JSONTreeNode[];
};

export type JSONPathPart = {
  value: string;
  arrayIndex: boolean;
};

export type JSONInspection = {
  root: JSONTreeNode;
  truncated: boolean;
  nodeCount: number;
};

export type JSONPathIndex = {
  name: string;
  status: string;
};

export type FullTextOutput = "rows" | "highlight" | "snippet";

export type FullTextCatalogIndex = {
  name: string;
  status: string;
  columns: string[];
  usable: boolean;
  reason?: string;
};

export type FullTextCatalog = {
  columns: string[];
  primaryColumns: string[];
  indexes: FullTextCatalogIndex[];
};

export type FullTextQueryState = {
  table: string;
  columns: string[];
  query: string;
  output: FullTextOutput;
  limit: number;
  index?: FullTextCatalogIndex;
  primaryColumns?: string[];
};

export type FullTextBuildResult = { sql: string; error: null } | { sql: null; error: string };

export type FullTextResultContext = {
  kind: "fulltext";
  indexName: string | null;
  output: FullTextOutput;
};

export type HybridResultContext = {
  kind: "hybrid";
  indexName: string | null;
};

export type RankResultContext = FullTextResultContext | HybridResultContext;

export type FilterOperator = "=" | "<>" | "<" | "<=" | ">" | ">=" | "is_null" | "is_not_null";

export type HybridFilterColumnKind = "numeric" | "quoted";

export type HybridFilterableColumn = {
  name: string;
  type: string;
  kind: HybridFilterColumnKind;
};

export type HybridCatalog = {
  filterColumns: HybridFilterableColumn[];
  fullText: FullTextCatalog;
  vector: VectorCatalog;
};

export type HybridFilterState = {
  column?: HybridFilterableColumn;
  operator: FilterOperator;
  value: string;
};

export type HybridQueryState = {
  table: string;
  filter: HybridFilterState;
  fullTextColumns: string[];
  fullTextIndex?: FullTextCatalogIndex;
  query: string;
  vectorColumn?: VectorCatalogColumn;
  vectorValues: number[] | null;
  metric: VectorMetric;
  limit: number;
};

export type HybridBuildResult = { sql: string; error: null } | { sql: null; error: string };

export type VectorColumnKind = "dense" | "bitvector" | "sparse";
export type VectorMetric = "cosine" | "l2" | "inner_product" | "hamming";

export type VectorCatalogColumn = {
  name: string;
  type: string;
  kind: VectorColumnKind;
  dimensions: number | null;
  metrics: VectorMetric[];
};

export type VectorCatalogIndex = {
  name: string;
  status: string;
  column: string;
  usable: boolean;
  reason?: string;
};

export type VectorCatalog = {
  columns: VectorCatalogColumn[];
  indexes: VectorCatalogIndex[];
};

export type VectorLiteralResult = { values: number[]; error: null } | { values: null; error: string };

export type VectorQueryState = {
  table: string;
  column?: VectorCatalogColumn;
  values: number[] | null;
  metric: VectorMetric;
  topK: number;
};

export type VectorBuildResult = { sql: string; error: null } | { sql: null; error: string };

export type ParsedVector = {
  mode: "dense" | "sparse";
  declaredDimensions: number | null;
  values: number[];
  indices: number[];
  nonZero: number;
  l2Norm: number;
};

export type GeoPoint = { x: number; y: number };
export type GeoShape =
  | "POINT"
  | "BOX"
  | "LINESTRING"
  | "POLYGON"
  | "MULTIPOINT"
  | "MULTILINESTRING"
  | "MULTIPOLYGON"
  | "GEOMETRYCOLLECTION";
// One flattened leaf element of a MULTIPOINT/MULTILINESTRING/MULTIPOLYGON/GEOMETRYCOLLECTION preview —
// a GEOMETRYCOLLECTION's own members (including nested collections) are recursively flattened into
// these simple shapes so the renderer never needs to special-case collection nesting.
export type GeoPart = { shape: "POINT" | "LINESTRING" | "POLYGON"; rings: GeoPoint[][] };
export type ParsedGeo = {
  shape: GeoShape;
  // Populated for the four fixed native shapes (POINT/BOX/LINESTRING/POLYGON); empty otherwise.
  rings: GeoPoint[][];
  // Populated for the general MULTIPOINT/MULTILINESTRING/MULTIPOLYGON/GEOMETRYCOLLECTION shapes; empty otherwise.
  parts: GeoPart[];
  pointCount: number;
  bounds: { minX: number; minY: number; maxX: number; maxY: number };
};

// The Geo Explorer only ever offers a POINT (or its LOCATION alias, which the
// catalog always reports back as "POINT") column as the searched column —
// `docs/geo.md`'s spatial index "requires a single POINT column", and every
// worked example in that doc searches a POINT column, so restricting to POINT
// keeps every generated query index-eligible and keeps WITHIN's fixed
// point-vs-region argument order (`WITHIN(point, box|polygon)`, verified
// against `internal/sql/types/geo.go`'s EvalGeo) from ever being backwards.
export type GeoCatalogColumn = { name: string; type: string };
export type GeoCatalogIndex = { name: string; status: string; column: string; usable: boolean; reason?: string };
export type GeoCatalog = { columns: GeoCatalogColumn[]; indexes: GeoCatalogIndex[] };

export type GeoDrawPoint = { lon: number; lat: number };
export type GeoQueryMode = "point_radius" | "polygon";

export type GeoQueryState = {
  table: string;
  column?: GeoCatalogColumn;
  mode: GeoQueryMode;
  point: GeoDrawPoint | null;
  radiusMeters: number | null;
  polygon: GeoDrawPoint[];
  limit: number;
};

export type GeoBuildResult = { sql: string; error: null } | { sql: null; error: string };

export type TimestampDetails = {
  utc: string;
  local: string;
  epochMilliseconds: number;
};

export function normalizeRowIndexes(total: number, indexes?: Iterable<number>): number[] {
  if (!indexes) return Array.from({ length: total }, (_, index) => index);
  const unique = new Set<number>();
  for (const index of indexes) {
    if (Number.isSafeInteger(index) && index >= 0 && index < total) unique.add(index);
  }
  return [...unique].sort((a, b) => a - b);
}

function neutralizeSpreadsheetFormula(value: string): string {
  return spreadsheetFormula.test(value) ? `'${value}` : value;
}

function delimitedCell(value: string | null, delimiter: string): string {
  let out = value === null ? "\\N" : neutralizeSpreadsheetFormula(value);
  if (out.includes('"') || out.includes("\r") || out.includes("\n") || out.includes(delimiter)) {
    out = `"${out.replace(/"/g, '""')}"`;
  }
  return out;
}

export function utf8ByteLength(value: string): number {
  let bytes = 0;
  for (let index = 0; index < value.length; index++) {
    const code = value.charCodeAt(index);
    if (code <= 0x7f) bytes++;
    else if (code <= 0x7ff) bytes += 2;
    else if (code >= 0xd800 && code <= 0xdbff && index + 1 < value.length) {
      const next = value.charCodeAt(index + 1);
      if (next >= 0xdc00 && next <= 0xdfff) {
        bytes += 4;
        index++;
      } else bytes += 3;
    } else bytes += 3;
  }
  return bytes;
}

function assertExportBound(content: string): void {
  if (utf8ByteLength(content) > MAX_RESULT_EXPORT_BYTES) {
    throw new Error("Result export exceeds the 64 MiB browser limit");
  }
}

export function tabularText(result: StudioResultSet, indexes?: Iterable<number>, delimiter = "\t"): string {
  if (delimiter !== "\t" && delimiter !== ",") throw new Error("unsupported result delimiter");
  const rows = normalizeRowIndexes(result.rows.length, indexes);
  const lines = [result.columns.map((value) => delimitedCell(value, delimiter)).join(delimiter)];
  for (const index of rows) {
    const row = result.rows[index];
    if (row.length !== result.columns.length) throw new Error("result row width does not match columns");
    lines.push(row.map((value) => delimitedCell(value, delimiter)).join(delimiter));
  }
  const content = lines.join("\r\n");
  assertExportBound(content);
  return content;
}

export function serializeResult(result: StudioResultSet, format: ExportFormat, indexes?: Iterable<number>): ResultExport {
  if (result.column_types.length !== result.columns.length) throw new Error("result column types do not match columns");
  const selected = normalizeRowIndexes(result.rows.length, indexes);
  for (const index of selected) {
    if (result.rows[index].length !== result.columns.length) throw new Error("result row width does not match columns");
  }
  if (format === "csv") {
    const content = `\ufeff${tabularText(result, selected, ",")}`;
    assertExportBound(content);
    return { content, extension: "csv", mime: "text/csv;charset=utf-8", rowCount: selected.length };
  }
  const content = JSON.stringify({
    format: "nextsql-studio-result-v1",
    columns: result.columns,
    column_types: result.column_types,
    rows: selected.map((index) => result.rows[index]),
    truncated: result.truncated,
  });
  assertExportBound(content);
  return { content, extension: "json", mime: "application/json;charset=utf-8", rowCount: selected.length };
}

export function resultFilename(prefix: string, format: ExportFormat, now = new Date()): string {
  const base = prefix
    .toLowerCase()
    .replace(/[^a-z0-9._-]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, 64) || "nextsql-result";
  const stamp = now.toISOString().replace(/[:.]/g, "-");
  return `${base}-${stamp}.${format}`;
}

export function downloadResult(result: StudioResultSet, format: ExportFormat, indexes?: Iterable<number>, prefix = "nextsql-result"): number {
  const exported = serializeResult(result, format, indexes);
  const url = URL.createObjectURL(new Blob([exported.content], { type: exported.mime }));
  const anchor = document.createElement("a");
  anchor.href = url;
  anchor.download = resultFilename(prefix, format);
  anchor.hidden = true;
  document.body.append(anchor);
  anchor.click();
  anchor.remove();
  setTimeout(() => URL.revokeObjectURL(url), 0);
  return exported.rowCount;
}

export async function writeClipboard(value: string): Promise<void> {
  if (!navigator.clipboard?.writeText) throw new Error("Clipboard access is unavailable in this browser context");
  await navigator.clipboard.writeText(value);
}

export function classifyStudioType(type: string): NativeCellKind {
  const normalized = type.trim().toUpperCase();
  if (normalized === "JSON") return "json";
  if (/^(?:VECTOR|SPARSEVECTOR|BITVECTOR)</.test(normalized)) return "vector";
  if (/^(?:POINT|BOX|LINESTRING|POLYGON|GEOMETRY|GEOGRAPHY)(?:\b|\()/.test(normalized)) return "geo";
  if (normalized === "TIMESTAMPTZ") return "timestamptz";
  return "text";
}

export function isInspectableStudioType(type: string): boolean {
  return classifyStudioType(type) !== "text";
}

export function declaredVectorDimensions(type: string): number | null {
  const match = type.trim().toUpperCase().match(/<(?:[A-Z0-9]+,)?(\d+)>$/);
  if (!match) return null;
  const dimensions = Number(match[1]);
  return Number.isSafeInteger(dimensions) && dimensions > 0 ? dimensions : null;
}

function boundedCommaCount(value: string, max: number): void {
  let entries = value.trim() ? 1 : 0;
  for (let index = 0; index < value.length; index++) {
    if (value[index] === "," && ++entries > max) throw new Error("vector exceeds the inspector value limit");
  }
}

export function parseVector(value: string, type: string): ParsedVector {
  const trimmed = value.trim();
  const declaredDimensions = declaredVectorDimensions(type);
  if (trimmed.startsWith("{") && trimmed.endsWith("}")) {
    const body = trimmed.slice(1, -1).trim();
    boundedCommaCount(body, MAX_INSPECTED_VECTOR_VALUES);
    const indices: number[] = [];
    const values: number[] = [];
    if (body) {
      for (const entry of body.split(",")) {
        const separator = entry.indexOf(":");
        if (separator <= 0) throw new Error("invalid sparse vector value");
        const index = Number(entry.slice(0, separator).trim());
        const number = Number(entry.slice(separator + 1).trim());
        if (!Number.isSafeInteger(index) || index < 0 || !Number.isFinite(number)) {
          throw new Error("invalid sparse vector value");
        }
        if (number === 0) throw new Error("sparse vector contains an explicit zero");
        if (declaredDimensions !== null && index >= declaredDimensions) {
          throw new Error("sparse vector index exceeds its declared dimensions");
        }
        if (indices.length && index <= indices[indices.length - 1]) throw new Error("sparse vector indices are not ordered");
        indices.push(index);
        values.push(number);
      }
    }
    const l2Norm = Math.sqrt(values.reduce((sum, number) => sum + number * number, 0));
    return { mode: "sparse", declaredDimensions, values, indices, nonZero: values.length, l2Norm };
  }
  if (!trimmed.startsWith("[") || !trimmed.endsWith("]")) throw new Error("invalid dense vector value");
  const body = trimmed.slice(1, -1).trim();
  boundedCommaCount(body, MAX_INSPECTED_VECTOR_VALUES);
  let parsed: unknown;
  try {
    parsed = JSON.parse(trimmed);
  } catch {
    throw new Error("invalid dense vector value");
  }
  if (!Array.isArray(parsed) || parsed.some((number) => typeof number !== "number" || !Number.isFinite(number))) {
    throw new Error("invalid dense vector value");
  }
  const values = parsed as number[];
  if (declaredDimensions !== null && values.length !== declaredDimensions) {
    throw new Error("vector length does not match its declared dimensions");
  }
  if (type.trim().toUpperCase().startsWith("BITVECTOR<") && values.some((number) => number !== 0 && number !== 1)) {
    throw new Error("bit vector values must be zero or one");
  }
  const l2Norm = Math.sqrt(values.reduce((sum, number) => sum + number * number, 0));
  return {
    mode: "dense",
    declaredDimensions,
    values,
    indices: values.map((_, index) => index),
    nonZero: values.reduce((count, number) => count + (number === 0 ? 0 : 1), 0),
    l2Norm,
  };
}

function jsonKind(value: unknown): JSONTreeNode["kind"] {
  if (value === null) return "null";
  if (Array.isArray(value)) return "array";
  if (typeof value === "object") return "object";
  return typeof value as "string" | "number" | "boolean";
}

function jsonScalar(value: unknown): string {
  return typeof value === "string" ? JSON.stringify(value) : String(value);
}

export function inspectJSON(raw: string): JSONInspection {
  const value: unknown = JSON.parse(raw);
  let remaining = MAX_JSON_TREE_NODES;
  let nodeCount = 0;
  let truncated = false;

  const build = (label: string, candidate: unknown, depth: number, path: JSONPathPart[]): JSONTreeNode => {
    if (remaining <= 0) {
      truncated = true;
      return { label, kind: "limit", path, value: "… node limit reached" };
    }
    remaining--;
    nodeCount++;
    const kind = jsonKind(candidate);
    if (kind !== "object" && kind !== "array") return { label, kind, path, value: jsonScalar(candidate) };
    const size = kind === "array" ? (candidate as unknown[]).length : Object.keys(candidate as object).length;
    if (depth >= MAX_JSON_TREE_DEPTH) {
      truncated = true;
      return { label, kind: "limit", path, value: `${kind}(${size}) · depth limit reached` };
    }
    const children: JSONTreeNode[] = [];
    if (kind === "array") {
      const values = candidate as unknown[];
      for (let index = 0; index < values.length; index++) {
        if (remaining <= 0) {
          truncated = true;
          break;
        }
        const part = { value: String(index), arrayIndex: true };
        children.push(build(part.value, values[index], depth + 1, [...path, part]));
      }
    } else {
      const values = candidate as Record<string, unknown>;
      for (const key in values) {
        if (!Object.prototype.hasOwnProperty.call(values, key)) continue;
        if (remaining <= 0) {
          truncated = true;
          break;
        }
        const part = { value: key, arrayIndex: false };
        children.push(build(key, values[key], depth + 1, [...path, part]));
      }
    }
    return { label, kind, path, value: `${kind}(${size})`, children };
  };

  return { root: build("$", value, 0, []), truncated, nodeCount };
}

// NextSQL JSON paths are native identifier paths, not JSONPath strings:
// `column.key.0`. Quoting every identifier makes generated SQL safe for
// whitespace, punctuation, reserved words, and case-sensitive names; array
// indexes deliberately stay numeric so the parser/executor treats them as
// array positions rather than object keys.
export function nativeJSONPath(column: string, path: JSONPathPart[]): string {
  let out = quoteIdentifier(column);
  for (const part of path) {
    if (part.arrayIndex) {
      if (!/^(?:0|[1-9][0-9]*)$/.test(part.value)) throw new Error("invalid JSON array index path part");
      out += `.${part.value}`;
    } else {
      out += `.${quoteIdentifier(part.value)}`;
    }
  }
  return out;
}

export function buildJSONPathQuery(table: string, column: string, path: JSONPathPart[]): string {
  return `SELECT ${nativeJSONPath(column, path)} FROM ${quoteIdentifier(table)} LIMIT 100`;
}

export function resultColumn(result: StudioResultSet, name: string): number {
  return result.columns.findIndex((column) => column === name);
}

// tableConstraintsResult builds the table inspector's unified constraint
// view only from catalog rows the current Studio session was already allowed
// to read. NextSQL does not have a separate system.constraints relation:
// primary-key and NOT NULL declarations live in system.columns, UNIQUE is an
// index property in system.indexes, and referential constraints live in
// system.foreign_keys. Keeping the source distinctions in `kind` avoids
// presenting a partial UNIQUE index as a SQL-standard table constraint.
export function tableConstraintsResult(detail: StudioTableDetail | null): StudioResultSet {
  const rows: (string | null)[][] = [];
  let totalRows = 0;
  const add = (kind: string, name: string, columns: string, details: string) => {
    totalRows++;
    if (rows.length < MAX_TABLE_CONSTRAINT_ROWS) rows.push([kind, name, columns, details]);
  };
  if (!detail) {
    return {
      columns: ["kind", "name", "columns", "details"],
      column_types: ["STRING", "STRING", "STRING", "STRING"],
      rows,
      truncated: false,
      elapsed_ms: 0,
    };
  }

  const columnName = resultColumn(detail.columns, "column_name");
  const columnOrdinal = resultColumn(detail.columns, "ordinal");
  const columnNotNull = resultColumn(detail.columns, "not_null");
  const columnPrimary = resultColumn(detail.columns, "is_primary");
  const orderedColumns = [...detail.columns.rows].sort((left, right) => {
    const leftOrdinal = columnOrdinal >= 0 ? Number(left[columnOrdinal]) : 0;
    const rightOrdinal = columnOrdinal >= 0 ? Number(right[columnOrdinal]) : 0;
    return (Number.isFinite(leftOrdinal) ? leftOrdinal : 0) - (Number.isFinite(rightOrdinal) ? rightOrdinal : 0);
  });
  const isTrue = (value: string | null | undefined) => value?.trim().toLowerCase() === "true";
  const primaryColumns = columnName >= 0 && columnPrimary >= 0
    ? orderedColumns
      .filter((row) => isTrue(row[columnPrimary]))
      .map((row) => row[columnName])
      .filter((name): name is string => typeof name === "string" && name.length > 0)
    : [];
  if (primaryColumns.length > 0) {
    add("PRIMARY KEY", "PRIMARY", primaryColumns.join(", "), "Declared by system.columns");
  }

  const indexName = resultColumn(detail.indexes, "index_name");
  const indexUnique = resultColumn(detail.indexes, "is_unique");
  const indexColumns = resultColumn(detail.indexes, "columns");
  const indexInclude = resultColumn(detail.indexes, "include_columns");
  const indexPredicate = resultColumn(detail.indexes, "predicate");
  const indexStatus = resultColumn(detail.indexes, "status");
  if (indexName >= 0 && indexUnique >= 0 && indexColumns >= 0) {
    for (const row of detail.indexes.rows) {
      const name = row[indexName];
      const columns = row[indexColumns];
      if (!isTrue(row[indexUnique]) || typeof name !== "string" || typeof columns !== "string") continue;
      // Some older Studio fixtures/servers surfaced the primary backing index
      // here while current system.indexes does not. PRIMARY is represented by
      // the system.columns declaration above, so never duplicate it.
      if (name.trim().toUpperCase() === "PRIMARY") continue;
      const detailParts: string[] = [];
      const include = indexInclude >= 0 ? row[indexInclude] : null;
      const predicate = indexPredicate >= 0 ? row[indexPredicate] : null;
      const status = indexStatus >= 0 ? row[indexStatus] : null;
      if (include) detailParts.push(`INCLUDE ${include}`);
      if (predicate) detailParts.push(`WHERE ${predicate}`);
      if (status) detailParts.push(`status ${status}`);
      add("UNIQUE INDEX", name, columns, detailParts.join(" ") || "Declared by system.indexes");
    }
  }

  if (columnName >= 0 && columnNotNull >= 0) {
    for (const row of orderedColumns) {
      const name = row[columnName];
      if (typeof name === "string" && name.length > 0 && isTrue(row[columnNotNull])) {
        add("NOT NULL", name, name, "Declared by system.columns");
      }
    }
  }

  const foreignKeys = detail.foreign_keys;
  const fkName = foreignKeys ? resultColumn(foreignKeys, "constraint_name") : -1;
  const fkOrdinal = foreignKeys ? resultColumn(foreignKeys, "ordinal") : -1;
  const fkColumn = foreignKeys ? resultColumn(foreignKeys, "column_name") : -1;
  const fkRefTable = foreignKeys ? resultColumn(foreignKeys, "ref_table") : -1;
  const fkRefColumn = foreignKeys ? resultColumn(foreignKeys, "ref_column") : -1;
  const fkOnDelete = foreignKeys ? resultColumn(foreignKeys, "on_delete") : -1;
  const fkOnUpdate = foreignKeys ? resultColumn(foreignKeys, "on_update") : -1;
  if (foreignKeys && fkName >= 0 && fkColumn >= 0 && fkRefTable >= 0 && fkRefColumn >= 0) {
    type ForeignKeyGroup = {
      name: string;
      first: number;
      columns: { ordinal: number; name: string; refName: string }[];
      refTable: string;
      onDelete: string;
      onUpdate: string;
    };
    const groups = new Map<string, ForeignKeyGroup>();
    foreignKeys.rows.forEach((row, position) => {
      const name = row[fkName];
      const column = row[fkColumn];
      const refTable = row[fkRefTable];
      const refColumn = row[fkRefColumn];
      if (typeof name !== "string" || typeof column !== "string" || typeof refTable !== "string" || typeof refColumn !== "string") return;
      let group = groups.get(name);
      if (!group) {
        group = {
          name,
          first: position,
          columns: [],
          refTable,
          onDelete: fkOnDelete >= 0 ? row[fkOnDelete] ?? "" : "",
          onUpdate: fkOnUpdate >= 0 ? row[fkOnUpdate] ?? "" : "",
        };
        groups.set(name, group);
      }
      const ordinal = fkOrdinal >= 0 ? Number(row[fkOrdinal]) : position;
      group.columns.push({
        ordinal: Number.isFinite(ordinal) ? ordinal : position,
        name: column,
        refName: refColumn,
      });
    });
    for (const group of [...groups.values()].sort((left, right) => left.first - right.first)) {
      group.columns.sort((left, right) => left.ordinal - right.ordinal);
      const actions = [
        group.onDelete ? `ON DELETE ${group.onDelete}` : "",
        group.onUpdate ? `ON UPDATE ${group.onUpdate}` : "",
      ].filter(Boolean);
      const reference = `REFERENCES ${group.refTable} (${group.columns.map((column) => column.refName).join(", ")})`;
      add(
        "FOREIGN KEY",
        group.name,
        group.columns.map((column) => column.name).join(", "),
        [reference, ...actions].join(" "),
      );
    }
  }

  return {
    columns: ["kind", "name", "columns", "details"],
    column_types: ["STRING", "STRING", "STRING", "STRING"],
    rows,
    truncated: totalRows > rows.length,
    elapsed_ms: 0,
  };
}

// The Full-text Explorer is catalog-driven. system.columns is the authority
// for eligible STRING/TEXT fields and primary-key display columns;
// system.indexes is the authority for full-text index name, ordered fields,
// and status. The catalog currently serializes an index's field list as a
// comma-delimited string without identifier escaping, so a field name that
// itself contains a comma cannot be reconstructed safely. Such an index is
// surfaced but marked unusable instead of guessed.
export function fullTextCatalog(detail: StudioTableDetail): FullTextCatalog {
  const columnNameIndex = resultColumn(detail.columns, "column_name");
  const columnTypeIndex = resultColumn(detail.columns, "type");
  const primaryIndex = resultColumn(detail.columns, "is_primary");
  const columns: string[] = [];
  const primaryColumns: string[] = [];
  if (columnNameIndex >= 0 && columnTypeIndex >= 0) {
    for (const row of detail.columns.rows) {
      const name = row[columnNameIndex];
      const type = row[columnTypeIndex]?.trim().toUpperCase();
      if (typeof name !== "string") continue;
      if (type === "STRING" || type === "TEXT") columns.push(name);
      if (primaryIndex >= 0 && row[primaryIndex]?.toLowerCase() === "true") primaryColumns.push(name);
    }
  }

  const kindIndex = resultColumn(detail.indexes, "kind");
  const indexNameIndex = resultColumn(detail.indexes, "index_name");
  const indexColumnsIndex = resultColumn(detail.indexes, "columns");
  const statusIndex = resultColumn(detail.indexes, "status");
  const indexes: FullTextCatalogIndex[] = [];
  if (kindIndex >= 0 && indexNameIndex >= 0 && indexColumnsIndex >= 0) {
    for (const row of detail.indexes.rows) {
      if (row[kindIndex]?.toLowerCase() !== "fulltext") continue;
      const name = row[indexNameIndex];
      const encoded = row[indexColumnsIndex];
      if (typeof name !== "string" || typeof encoded !== "string") continue;
      const fields = encoded.split(",");
      let reason: string | undefined;
      if (!encoded || fields.length === 0 || fields.length > 8) {
        reason = "catalog field list is empty or exceeds the eight-field SEARCH limit";
      } else if (new Set(fields).size !== fields.length) {
        reason = "catalog field list contains a duplicate";
      } else if (fields.some((field) => !columns.includes(field))) {
        reason = "catalog field boundaries cannot be matched safely to visible STRING/TEXT columns";
      }
      indexes.push({
        name,
        status: statusIndex >= 0 ? row[statusIndex] ?? "unknown" : "unknown",
        columns: reason ? [] : fields,
        usable: reason === undefined,
        ...(reason ? { reason } : {}),
      });
    }
  }
  return { columns, primaryColumns, indexes };
}

const VECTOR_METRICS_BY_KIND: Record<VectorColumnKind, VectorMetric[]> = {
  dense: ["cosine", "l2", "inner_product"],
  bitvector: ["hamming"],
  sparse: ["cosine", "inner_product"],
};

export const VECTOR_METRIC_SQL: Record<VectorMetric, string> = {
  cosine: "COSINE",
  l2: "L2",
  inner_product: "INNER_PRODUCT",
  hamming: "HAMMING",
};

function classifyVectorColumnType(type: string): VectorColumnKind | null {
  const upper = type.trim().toUpperCase();
  if (upper.startsWith("VECTOR<")) return "dense";
  if (upper.startsWith("BITVECTOR<")) return "bitvector";
  if (upper.startsWith("SPARSEVECTOR<")) return "sparse";
  return null;
}

// vectorColumnsFromResult lists the VECTOR/BITVECTOR/SPARSEVECTOR columns
// in an authorized system.columns result. IntelliSense uses this as the
// only vector metadata NextSQL exposes for completion (column kind + the
// metrics that kind accepts). There is no per-element / per-dimension
// catalog to complete inside a TO (...) literal.
export function vectorColumnsFromResult(columns: StudioResultSet): VectorCatalogColumn[] {
  const columnNameIndex = resultColumn(columns, "column_name");
  const columnTypeIndex = resultColumn(columns, "type");
  const out: VectorCatalogColumn[] = [];
  if (columnNameIndex < 0 || columnTypeIndex < 0) return out;
  for (const row of columns.rows ?? []) {
    const name = row[columnNameIndex];
    const type = row[columnTypeIndex];
    if (typeof name !== "string" || typeof type !== "string") continue;
    const kind = classifyVectorColumnType(type);
    if (!kind) continue;
    out.push({ name, type, kind, dimensions: declaredVectorDimensions(type), metrics: VECTOR_METRICS_BY_KIND[kind] });
  }
  return out;
}

// The Vector Explorer is catalog-driven the same way the Full-text Explorer
// is: system.columns is the authority for eligible VECTOR/BITVECTOR/
// SPARSEVECTOR columns and their declared dimensions; system.indexes is the
// authority for a candidate vector index's name and status. A vector index
// always covers exactly one column (`docs/vector.md`'s `CREATE VECTOR INDEX`
// requires exactly one), so — unlike full-text's up-to-eight-field
// comma-delimited list — an empty or comma-containing catalog column field
// is unconditionally unrepresentable and marked unusable rather than guessed.
// system.indexes reports every vector index (HNSW/IVF/IVFPQ/SPARSE) with the
// same "vector" kind and exposes no algorithm/quantization detail, so the
// catalog cannot distinguish them; the explorer only ever surfaces "a vector
// index exists and its status", identical to what the catalog can prove.
export function vectorCatalog(detail: StudioTableDetail): VectorCatalog {
  const columns = vectorColumnsFromResult(detail.columns);

  const kindIndex = resultColumn(detail.indexes, "kind");
  const indexNameIndex = resultColumn(detail.indexes, "index_name");
  const indexColumnsIndex = resultColumn(detail.indexes, "columns");
  const statusIndex = resultColumn(detail.indexes, "status");
  const indexes: VectorCatalogIndex[] = [];
  if (kindIndex >= 0 && indexNameIndex >= 0 && indexColumnsIndex >= 0) {
    for (const row of detail.indexes.rows) {
      if (row[kindIndex]?.toLowerCase() !== "vector") continue;
      const name = row[indexNameIndex];
      const encoded = row[indexColumnsIndex];
      if (typeof name !== "string" || typeof encoded !== "string") continue;
      let reason: string | undefined;
      if (!encoded || encoded.includes(",")) {
        reason = "catalog column field is empty or ambiguous for a single-column vector index";
      } else if (!columns.some((column) => column.name === encoded)) {
        reason = "catalog column does not match a visible vector column";
      }
      indexes.push({
        name,
        status: statusIndex >= 0 ? row[statusIndex] ?? "unknown" : "unknown",
        column: reason ? "" : encoded,
        usable: reason === undefined,
        ...(reason ? { reason } : {}),
      });
    }
  }
  return { columns, indexes };
}

// Accepts the same value shapes a user is likely to paste — a bracketed
// `[1, 2, 3]` (a copied embedding, or a copy of a result cell's own display
// format), a parenthesized `(1, 2, 3)` (the exact native literal), or a bare
// comma list — and always validates against the exact NEAREST literal
// grammar (`docs/vector.md`: a dense parenthesized tuple; a SPARSEVECTOR
// column accepts the same dense form, coercing zeros away server-side) and
// the column's own declared dimensions/value domain, never against a
// friendlier but unenforced shape.
export function parseVectorLiteralInput(input: string, column?: VectorCatalogColumn): VectorLiteralResult {
  const trimmed = input.trim();
  if (!trimmed) return { values: null, error: "Enter a vector." };
  const body = (trimmed.startsWith("(") && trimmed.endsWith(")")) || (trimmed.startsWith("[") && trimmed.endsWith("]"))
    ? trimmed.slice(1, -1)
    : trimmed;
  try {
    boundedCommaCount(body, MAX_INSPECTED_VECTOR_VALUES);
  } catch (error) {
    return { values: null, error: error instanceof Error ? error.message : String(error) };
  }
  const parts = body.split(",").map((part) => part.trim()).filter((part) => part.length > 0);
  if (parts.length === 0) return { values: null, error: "Enter at least one value." };
  const values: number[] = [];
  for (const part of parts) {
    const number = Number(part);
    if (!Number.isFinite(number)) return { values: null, error: `"${part}" is not a finite number.` };
    values.push(number);
  }
  if (column?.dimensions != null && values.length !== column.dimensions) {
    return {
      values: null,
      error: `${column.name} is declared with ${column.dimensions} dimension${column.dimensions === 1 ? "" : "s"}; this vector has ${values.length}.`,
    };
  }
  if (column?.kind === "bitvector" && values.some((number) => number !== 0 && number !== 1)) {
    return { values: null, error: "BITVECTOR values must be exactly 0 or 1." };
  }
  return { values, error: null };
}

export function buildVectorLiteral(values: number[]): string {
  return `(${values.join(", ")})`;
}

// Generates the exact native `NEAREST col TO (...) USING metric LIMIT n`
// grammar (`docs/vector.md`). There is no per-query HNSW/IVF/IVFPQ
// search-time tuning knob in NSQL at all — `WITH (QUANTIZATION = ...)` and
// `WITH (LISTS = ..., PROBES = ...)` are `CREATE VECTOR INDEX` options, not
// `NEAREST` options (checked against `internal/sql/parser`'s `nearestClause`,
// which parses only `col TO <expr> [USING metric]`) — so the explorer never
// offers settings that do not exist rather than rendering inert controls.
export function buildVectorSQL(state: VectorQueryState): VectorBuildResult {
  if (!state.table.trim()) return { sql: null, error: "Table is required." };
  if (!state.column) return { sql: null, error: "Select a VECTOR, BITVECTOR, or SPARSEVECTOR column." };
  if (!state.column.metrics.includes(state.metric)) {
    return { sql: null, error: `${state.column.name} does not support the ${state.metric.toUpperCase()} metric.` };
  }
  if (!state.values || state.values.length === 0) {
    return { sql: null, error: "Enter a valid vector to search for." };
  }
  if (!Number.isSafeInteger(state.topK) || state.topK < 1 || state.topK > MAX_VECTOR_EXPLORER_TOPK) {
    return { sql: null, error: `Top-K must be an integer from 1 to ${MAX_VECTOR_EXPLORER_TOPK}.` };
  }
  const sql = `SELECT * FROM ${quoteIdentifier(state.table)} NEAREST ${quoteIdentifier(state.column.name)} TO ${buildVectorLiteral(state.values)} USING ${state.metric.toUpperCase()} LIMIT ${state.topK}`;
  return { sql, error: null };
}

function quoteSQLString(value: string): string {
  return `'${value.replace(/'/g, "''")}'`;
}

// Generates the exact native SEARCH grammar documented in docs/fulltext.md.
// Selecting an index does not emit a hint — NextSQL has no full-text index
// hint syntax and the optimizer remains authoritative. It only locks the
// ordered SEARCH field list to that valid catalog index. HIGHLIGHT/SNIPPET
// output selects primary-key context plus one marked value per search field;
// SELECT * cannot be mixed with extra select-list expressions in the current
// grammar.
export function buildFullTextSQL(state: FullTextQueryState): FullTextBuildResult {
  if (!state.table.trim()) return { sql: null, error: "Table is required." };
  if (state.columns.length === 0) return { sql: null, error: "Select at least one STRING or TEXT column." };
  if (state.columns.length > 8) return { sql: null, error: "SEARCH supports at most eight columns." };
  if (state.columns.some((column) => !column.trim())) return { sql: null, error: "Search columns cannot be empty." };
  if (new Set(state.columns).size !== state.columns.length) return { sql: null, error: "Search columns cannot be repeated." };
  if (!state.query.trim()) return { sql: null, error: "Search phrase is required." };
  if (state.query.includes("\0")) return { sql: null, error: "Search phrase cannot contain NUL." };
  if (state.query.length > MAX_FULLTEXT_EXPLORER_QUERY_CHARS) {
    return { sql: null, error: `Search phrase exceeds the ${MAX_FULLTEXT_EXPLORER_QUERY_CHARS.toLocaleString()}-character Explorer limit.` };
  }
  if (!Number.isSafeInteger(state.limit) || state.limit < 1 || state.limit > MAX_FULLTEXT_EXPLORER_ROWS) {
    return { sql: null, error: `Result limit must be an integer from 1 to ${MAX_FULLTEXT_EXPLORER_ROWS}.` };
  }
  if (state.index) {
    if (!state.index.usable) return { sql: null, error: `Index ${state.index.name} cannot be used safely: ${state.index.reason ?? "invalid catalog metadata"}.` };
    if (state.index.status.toLowerCase() !== "valid") return { sql: null, error: `Index ${state.index.name} is ${state.index.status}, not valid.` };
    if (state.index.columns.length !== state.columns.length || state.index.columns.some((column, index) => column !== state.columns[index])) {
      return { sql: null, error: `Search columns must match index ${state.index.name} in catalog order.` };
    }
  }

  let projection = "*";
  if (state.output !== "rows") {
    const fn = state.output === "highlight" ? "HIGHLIGHT" : "SNIPPET";
    const context = [...new Set(state.primaryColumns ?? [])].map(quoteIdentifier);
    const marked = state.columns.map((column) => (
      `${fn}(${quoteIdentifier(column)}) AS ${quoteIdentifier(`${column}_${state.output}`)}`
    ));
    projection = [...context, ...marked].join(", ");
  }
  const fields = state.columns.map(quoteIdentifier).join(", ");
  return {
    sql: `SELECT ${projection} FROM ${quoteIdentifier(state.table)} SEARCH ${fields} FOR ${quoteSQLString(state.query)} LIMIT ${state.limit}`,
    error: null,
  };
}

// The structured-filter side of the Hybrid Explorer intentionally offers
// only scalar comparison operators (=, <>, <, <=, >, >=, IS [NOT] NULL) —
// NextSQL has no LIKE/regex operator at all (checked against
// `internal/sql/lexer`'s keyword table), and pattern matching over text is
// SEARCH's job, not this filter's. Only columns whose type coerces cleanly
// from a bare literal are offered: exact/unsigned integers and DECIMAL take
// a raw numeric literal; STRING/TEXT/UUID/TIMESTAMPTZ take a quoted string
// literal (verified against a real running nextsqld that a quoted TIMESTAMPTZ
// and UUID literal both coerce correctly). BLOB (hex-literal syntax), JSON,
// every VECTOR/BITVECTOR/SPARSEVECTOR column, every geo type, and every
// nested STRUCT/ARRAY/MAP column are excluded rather than guessed at.
const HYBRID_NUMERIC_FILTER_TYPES = /^(INT8|INT16|INT32|INT64|UINT8|UINT16|UINT32|UINT64|DECIMAL)/;
const HYBRID_QUOTED_FILTER_TYPES = /^(STRING|TEXT|UUID|TIMESTAMPTZ)/;

function hybridFilterColumnKind(type: string): HybridFilterColumnKind | null {
  const upper = type.trim().toUpperCase();
  if (HYBRID_NUMERIC_FILTER_TYPES.test(upper)) return "numeric";
  if (HYBRID_QUOTED_FILTER_TYPES.test(upper)) return "quoted";
  return null;
}

// The Hybrid Explorer composes the same catalog reads the Full-text and
// Vector Explorers already use, plus the filterable-column list above, so a
// table eligible for hybrid search is exactly a table eligible for both of
// the other two explorers.
export function hybridCatalog(detail: StudioTableDetail): HybridCatalog {
  const columnNameIndex = resultColumn(detail.columns, "column_name");
  const columnTypeIndex = resultColumn(detail.columns, "type");
  const filterColumns: HybridFilterableColumn[] = [];
  if (columnNameIndex >= 0 && columnTypeIndex >= 0) {
    for (const row of detail.columns.rows) {
      const name = row[columnNameIndex];
      const type = row[columnTypeIndex];
      if (typeof name !== "string" || typeof type !== "string") continue;
      const kind = hybridFilterColumnKind(type);
      if (!kind) continue;
      filterColumns.push({ name, type, kind });
    }
  }
  return { filterColumns, fullText: fullTextCatalog(detail), vector: vectorCatalog(detail) };
}

// No column selected means no filter at all — an empty WHERE is not an
// error, it just means the hybrid query is SEARCH+NEAREST only.
export function buildHybridFilterClause(filter: HybridFilterState): { clause: string | null; error: string | null } {
  if (!filter.column) return { clause: null, error: null };
  const columnSQL = quoteIdentifier(filter.column.name);
  if (filter.operator === "is_null") return { clause: `${columnSQL} IS NULL`, error: null };
  if (filter.operator === "is_not_null") return { clause: `${columnSQL} IS NOT NULL`, error: null };
  const trimmed = filter.value.trim();
  if (!trimmed) return { clause: null, error: "Enter a filter value." };
  if (filter.column.kind === "numeric") {
    if (!Number.isFinite(Number(trimmed))) return { clause: null, error: `"${trimmed}" is not a finite number.` };
    return { clause: `${columnSQL} ${filter.operator} ${trimmed}`, error: null };
  }
  return { clause: `${columnSQL} ${filter.operator} ${quoteSQLString(trimmed)}`, error: null };
}

// Generates the exact native `[WHERE ...] SEARCH ... FOR '...' NEAREST ...
// USING metric LIMIT n` hybrid grammar (`docs/vector.md`/`docs/optimizer.md`:
// "SEARCH + NEAREST (with or without WHERE) is planned as one hybrid
// problem") — verified clause order against a real running nextsqld before
// this function was written. Reuses the same field/index/phrase/metric/
// vector validation the standalone Full-text and Vector Explorers already
// apply; a hybrid query with no full-text or no vector input is rejected
// rather than silently degrading to one of the standalone explorers'
// simpler queries, since that degraded form is already better served by
// opening the dedicated Full-text or Vector Explorer instead.
export function buildHybridSQL(state: HybridQueryState): HybridBuildResult {
  if (!state.table.trim()) return { sql: null, error: "Table is required." };

  const filter = buildHybridFilterClause(state.filter);
  if (filter.error) return { sql: null, error: filter.error };

  if (state.fullTextColumns.length === 0) return { sql: null, error: "Select at least one STRING or TEXT column." };
  if (state.fullTextColumns.length > 8) return { sql: null, error: "SEARCH supports at most eight columns." };
  if (new Set(state.fullTextColumns).size !== state.fullTextColumns.length) return { sql: null, error: "Search columns cannot be repeated." };
  if (!state.query.trim()) return { sql: null, error: "Search phrase is required." };
  if (state.query.length > MAX_FULLTEXT_EXPLORER_QUERY_CHARS) {
    return { sql: null, error: `Search phrase exceeds the ${MAX_FULLTEXT_EXPLORER_QUERY_CHARS.toLocaleString()}-character Explorer limit.` };
  }
  if (state.fullTextIndex) {
    if (!state.fullTextIndex.usable) return { sql: null, error: `Index ${state.fullTextIndex.name} cannot be used safely: ${state.fullTextIndex.reason ?? "invalid catalog metadata"}.` };
    if (state.fullTextIndex.status.toLowerCase() !== "valid") return { sql: null, error: `Index ${state.fullTextIndex.name} is ${state.fullTextIndex.status}, not valid.` };
    if (state.fullTextIndex.columns.length !== state.fullTextColumns.length || state.fullTextIndex.columns.some((column, index) => column !== state.fullTextColumns[index])) {
      return { sql: null, error: `Search columns must match index ${state.fullTextIndex.name} in catalog order.` };
    }
  }

  if (!state.vectorColumn) return { sql: null, error: "Select a VECTOR, BITVECTOR, or SPARSEVECTOR column." };
  if (!state.vectorColumn.metrics.includes(state.metric)) {
    return { sql: null, error: `${state.vectorColumn.name} does not support the ${state.metric.toUpperCase()} metric.` };
  }
  if (!state.vectorValues || state.vectorValues.length === 0) {
    return { sql: null, error: "Enter a valid vector to search for." };
  }

  if (!Number.isSafeInteger(state.limit) || state.limit < 1 || state.limit > MAX_VECTOR_EXPLORER_TOPK) {
    return { sql: null, error: `Result limit must be an integer from 1 to ${MAX_VECTOR_EXPLORER_TOPK}.` };
  }

  const where = filter.clause ? ` WHERE ${filter.clause}` : "";
  const fields = state.fullTextColumns.map(quoteIdentifier).join(", ");
  const nearest = `NEAREST ${quoteIdentifier(state.vectorColumn.name)} TO ${buildVectorLiteral(state.vectorValues)} USING ${state.metric.toUpperCase()}`;
  const sql = `SELECT * FROM ${quoteIdentifier(state.table)}${where} SEARCH ${fields} FOR ${quoteSQLString(state.query)} ${nearest} LIMIT ${state.limit}`;
  return { sql, error: null };
}

// The Geo Explorer is catalog-driven the same way the Vector Explorer is:
// system.columns is the authority for eligible POINT columns (LOCATION is a
// storage-level alias that the catalog always reports back as "POINT", per
// `internal/sql/types/types.go`'s Type.String); system.indexes is the
// authority for a candidate spatial index's name and status. A spatial index
// always covers exactly one POINT column (`docs/geo.md`'s CREATE SPATIAL
// INDEX requires exactly one), so — like the Vector Explorer's index
// matching — an empty or comma-containing catalog column field is
// unconditionally unrepresentable and marked unusable rather than guessed.
export function geoCatalog(detail: StudioTableDetail): GeoCatalog {
  const columnNameIndex = resultColumn(detail.columns, "column_name");
  const columnTypeIndex = resultColumn(detail.columns, "type");
  const columns: GeoCatalogColumn[] = [];
  if (columnNameIndex >= 0 && columnTypeIndex >= 0) {
    for (const row of detail.columns.rows) {
      const name = row[columnNameIndex];
      const type = row[columnTypeIndex];
      if (typeof name !== "string" || typeof type !== "string") continue;
      if (type.trim().toUpperCase() !== "POINT") continue;
      columns.push({ name, type });
    }
  }

  const kindIndex = resultColumn(detail.indexes, "kind");
  const indexNameIndex = resultColumn(detail.indexes, "index_name");
  const indexColumnsIndex = resultColumn(detail.indexes, "columns");
  const statusIndex = resultColumn(detail.indexes, "status");
  const indexes: GeoCatalogIndex[] = [];
  if (kindIndex >= 0 && indexNameIndex >= 0 && indexColumnsIndex >= 0) {
    for (const row of detail.indexes.rows) {
      if (row[kindIndex]?.toLowerCase() !== "spatial") continue;
      const name = row[indexNameIndex];
      const encoded = row[indexColumnsIndex];
      if (typeof name !== "string" || typeof encoded !== "string") continue;
      let reason: string | undefined;
      if (!encoded || encoded.includes(",")) {
        reason = "catalog column field is empty or ambiguous for a single-column spatial index";
      } else if (!columns.some((column) => column.name === encoded)) {
        reason = "catalog column does not match a visible POINT column";
      }
      indexes.push({
        name,
        status: statusIndex >= 0 ? row[statusIndex] ?? "unknown" : "unknown",
        column: reason ? "" : encoded,
        usable: reason === undefined,
        ...(reason ? { reason } : {}),
      });
    }
  }
  return { columns, indexes };
}

// Coordinates are rounded to 7 decimal places (~1 cm at the equator, the
// common GPS/WKT convention) so a drawn or typed value never carries
// unreviewable binary floating-point noise into the generated SQL text.
function formatGeoNumber(value: number): string {
  if (!Number.isFinite(value)) throw new Error("coordinate must be a finite number");
  return Number(value.toFixed(7)).toString();
}

export function validateGeoPoint(point: GeoDrawPoint): string | null {
  if (!Number.isFinite(point.lon) || point.lon < -180 || point.lon > 180) {
    return "Longitude must be a number from -180 to 180.";
  }
  if (!Number.isFinite(point.lat) || point.lat < -90 || point.lat > 90) {
    return "Latitude must be a number from -90 to 90.";
  }
  return null;
}

// `POINT(lon, lat)` function-call form (`docs/geo.md`), used inside generated
// SQL. Argument order is longitude first, matching every fixed native type
// and every worked example in that doc.
export function buildPointCallLiteral(point: GeoDrawPoint): string {
  return `POINT(${formatGeoNumber(point.lon)}, ${formatGeoNumber(point.lat)})`;
}

// `POLYGON(wkt)` takes a single quoted WKT ring-list argument (`docs/geo.md`:
// `POLYGON('((-74.1 40.6, ..., -74.1 40.6))')`) — a bare comma-separated
// vertex list is not valid here, only a full ring-list string. The ring is
// closed by repeating the first vertex, exactly as `docs/geo.md` requires
// ("each ring is closed: first vertex equals last").
export function buildPolygonCallLiteral(vertices: GeoDrawPoint[]): string {
  const closed = [...vertices, vertices[0]];
  const ring = `(${closed.map((point) => `${formatGeoNumber(point.lon)} ${formatGeoNumber(point.lat)}`).join(", ")})`;
  return `POLYGON(${quoteSQLString(`(${ring})`)})`;
}

// `POINT(lon lat)` WKT text — the exact literal shape `parseGeo` accepts —
// used only to drive the reused CellInspector `GeoView` live preview, never
// sent to the server. Distinct from `buildPointCallLiteral`'s comma-separated
// SQL function-call form.
export function pointPreviewWKT(point: GeoDrawPoint): string {
  return `POINT(${formatGeoNumber(point.lon)} ${formatGeoNumber(point.lat)})`;
}

export function polygonPreviewWKT(vertices: GeoDrawPoint[]): string {
  const closed = [...vertices, vertices[0]];
  return `POLYGON((${closed.map((point) => `${formatGeoNumber(point.lon)} ${formatGeoNumber(point.lat)}`).join(", ")}))`;
}

// Generates either `WHERE DWITHIN(col, POINT(lon, lat), meters)` (DWITHIN
// accepts every geometry pair, so this form works even though the Explorer
// only ever offers a POINT column) or `WHERE WITHIN(col, POLYGON(...))`
// (WITHIN's first argument must be a POINT — verified against `EvalGeo` in
// `internal/sql/types/geo.go` — which is exactly this Explorer's only offered
// column kind). `SELECT *` cannot be mixed with extra select-list expressions
// in this grammar (checked against `internal/sql/parser`'s `sel()`), so no
// computed distance column is added alongside it.
export function buildGeoSQL(state: GeoQueryState): GeoBuildResult {
  if (!state.table.trim()) return { sql: null, error: "Table is required." };
  if (!state.column) return { sql: null, error: "Select a POINT column." };
  if (!Number.isSafeInteger(state.limit) || state.limit < 1 || state.limit > MAX_GEO_EXPLORER_LIMIT) {
    return { sql: null, error: `Result limit must be an integer from 1 to ${MAX_GEO_EXPLORER_LIMIT}.` };
  }
  const table = quoteIdentifier(state.table);
  const column = quoteIdentifier(state.column.name);

  if (state.mode === "point_radius") {
    if (!state.point) return { sql: null, error: "Click the map or enter longitude/latitude to place a point." };
    const pointError = validateGeoPoint(state.point);
    if (pointError) return { sql: null, error: pointError };
    if (state.radiusMeters == null || !Number.isFinite(state.radiusMeters) || state.radiusMeters < 0) {
      return { sql: null, error: "Radius must be a number of meters that is zero or greater." };
    }
    const sql = `SELECT * FROM ${table} WHERE DWITHIN(${column}, ${buildPointCallLiteral(state.point)}, ${formatGeoNumber(state.radiusMeters)}) LIMIT ${state.limit}`;
    return { sql, error: null };
  }

  if (state.polygon.length < 3) return { sql: null, error: "A polygon needs at least three vertices." };
  if (state.polygon.length > MAX_GEO_EXPLORER_POLYGON_VERTICES) {
    return { sql: null, error: `Polygon supports at most ${MAX_GEO_EXPLORER_POLYGON_VERTICES} vertices.` };
  }
  for (const vertex of state.polygon) {
    const vertexError = validateGeoPoint(vertex);
    if (vertexError) return { sql: null, error: vertexError };
  }
  const sql = `SELECT * FROM ${table} WHERE WITHIN(${column}, ${buildPolygonCallLiteral(state.polygon)}) LIMIT ${state.limit}`;
  return { sql, error: null };
}

// system.indexes currently reports JSON index columns as a dotted string
// without quoting/segment boundaries. That is exact only while every segment
// is a simple unquoted identifier (array indexes are unambiguous numeric
// segments). Return null for any special object key instead of guessing and
// falsely labelling a path indexed.
export function reportedJSONPathIndexes(
  indexes: StudioResultSet,
  column: string,
  path: JSONPathPart[],
): JSONPathIndex[] | null {
  if (path.length === 0) return [];
  const bare = /^[a-z_][a-z0-9_]*$/;
  if (!bare.test(column) || path.some((part) => !part.arrayIndex && !bare.test(part.value))) return null;

  const columnsIndex = resultColumn(indexes, "columns");
  const nameIndex = resultColumn(indexes, "index_name");
  const statusIndex = resultColumn(indexes, "status");
  if (columnsIndex < 0 || nameIndex < 0) return null;
  const expected = [column, ...path.map((part) => part.value)].join(".");
  return indexes.rows
    .filter((row) => row[columnsIndex] === expected && typeof row[nameIndex] === "string")
    .map((row) => ({ name: row[nameIndex] as string, status: row[statusIndex] ?? "unknown" }));
}

function parseCoordinatePair(value: string): GeoPoint {
  const parts = value.trim().split(/\s+/);
  if (parts.length !== 2) throw new Error("invalid coordinate pair");
  const x = Number(parts[0]);
  const y = Number(parts[1]);
  if (!Number.isFinite(x) || !Number.isFinite(y)) throw new Error("invalid coordinate pair");
  return { x, y };
}

function parseCoordinateList(value: string, maxPoints = MAX_GEO_PREVIEW_POINTS): GeoPoint[] {
  const parts = value.split(",");
  if (parts.length > maxPoints) throw new Error("geometry exceeds the preview point limit");
  return parts.map(parseCoordinatePair);
}

function geoBounds(rings: GeoPoint[][]): ParsedGeo["bounds"] {
  const points = rings.flat();
  if (!points.length) throw new Error("geometry has no coordinates");
  return points.reduce((bounds, point) => ({
    minX: Math.min(bounds.minX, point.x),
    minY: Math.min(bounds.minY, point.y),
    maxX: Math.max(bounds.maxX, point.x),
    maxY: Math.max(bounds.maxY, point.y),
  }), { minX: points[0].x, minY: points[0].y, maxX: points[0].x, maxY: points[0].y });
}

// --- general GEOMETRY/GEOGRAPHY (MULTIPOINT/MULTILINESTRING/MULTIPOLYGON/GEOMETRYCOLLECTION) ---
//
// The server's own WKT writer (internal/sql/types/spatial.go writeWKT) is the grammar these mirror:
// "KEYWORD(...)" with ", "-joined groups, GEOMETRYCOLLECTION nesting arbitrary sub-geometries (including
// further collections) up to MaxSpatialDepth (8) there. Unlike the four fixed native shapes, GEOMETRY
// coordinates are planar in an arbitrary column SRID and GEOGRAPHY coordinates are geodetic in an
// arbitrary column SRID — neither is necessarily WGS84 degrees, so (unlike the fixed-shape path above)
// this parser never applies a longitude/latitude range check.

type GeoBudget = { points: number; parts: number };

function takeParenBody(text: string): { body: string; rest: string } {
  const trimmed = text.replace(/^\s+/, "");
  if (trimmed[0] !== "(") throw new Error("expected (");
  let depth = 0;
  for (let index = 0; index < trimmed.length; index++) {
    if (trimmed[index] === "(") depth++;
    else if (trimmed[index] === ")") {
      depth--;
      if (depth === 0) return { body: trimmed.slice(1, index), rest: trimmed.slice(index + 1) };
    }
  }
  throw new Error("unbalanced parentheses");
}

// Splits "(a, b), (c, d)" into ["a, b", "c, d"] — one level of top-level (...) groups.
function splitTopLevelGroups(text: string): string[] {
  const out: string[] = [];
  let depth = 0;
  let start = -1;
  for (let index = 0; index < text.length; index++) {
    if (text[index] === "(") {
      if (depth === 0) start = index + 1;
      depth++;
    } else if (text[index] === ")") {
      depth--;
      if (depth === 0 && start >= 0) {
        out.push(text.slice(start, index));
        start = -1;
      }
    }
  }
  return out;
}

// Splits "POINT(1 2), LINESTRING(0 0, 1 1)" into its top-level comma-separated members, respecting
// nested parentheses (unlike splitTopLevelGroups, which strips one level of grouping parens itself).
function splitTopLevelSequence(text: string): string[] {
  const out: string[] = [];
  let depth = 0;
  let start = 0;
  for (let index = 0; index < text.length; index++) {
    if (text[index] === "(") depth++;
    else if (text[index] === ")") depth--;
    else if (text[index] === "," && depth === 0) {
      out.push(text.slice(start, index));
      start = index + 1;
    }
  }
  out.push(text.slice(start));
  return out.map((part) => part.trim()).filter((part) => part.length > 0);
}

// MULTIPOINT accepts both "(1 2), (3 4)" and "1 2, 3 4" — mirrors splitWKTPointList server-side.
function splitWKTPointList(text: string): string[] {
  return text.includes("(") ? splitTopLevelGroups(text) : splitTopLevelSequence(text);
}

function takeBoundedPoints(text: string, budget: GeoBudget): GeoPoint[] {
  const points = parseCoordinateList(text, budget.points);
  budget.points -= points.length;
  return points;
}

function takeBoundedRing(text: string, budget: GeoBudget): GeoPoint[] {
  const ring = takeBoundedPoints(text, budget);
  const first = ring[0];
  const last = ring[ring.length - 1];
  if (ring.length < 4 || first.x !== last.x || first.y !== last.y) {
    throw new Error("polygon rings must be closed and contain at least four points");
  }
  return ring;
}

function consumePart(budget: GeoBudget): void {
  budget.parts--;
  if (budget.parts < 0) throw new Error("geometry exceeds the preview part limit");
}

function parseSimplePart(shape: GeoPart["shape"], body: string, budget: GeoBudget): GeoPart {
  consumePart(budget);
  if (shape === "POINT") {
    const points = takeBoundedPoints(body, budget);
    if (points.length !== 1) throw new Error("POINT requires exactly one coordinate pair");
    return { shape, rings: [points] };
  }
  if (shape === "LINESTRING") {
    const points = takeBoundedPoints(body, budget);
    if (points.length < 2) throw new Error("linestring must contain at least two points");
    return { shape, rings: [points] };
  }
  const ringTexts = splitTopLevelGroups(body);
  if (!ringTexts.length) throw new Error("polygon requires an exterior ring");
  return { shape, rings: ringTexts.map((ringText) => takeBoundedRing(ringText, budget)) };
}

// Parses one WKT geometry expression into its flattened leaf parts — a GEOMETRYCOLLECTION's members
// (recursively, including nested collections) are expanded in place rather than kept as a tree, since
// the preview only ever needs to render simple POINT/LINESTRING/POLYGON primitives.
function parseGeneralParts(text: string, depth: number, budget: GeoBudget): GeoPart[] {
  if (depth > MAX_GEO_COLLECTION_DEPTH) throw new Error("geometry nesting exceeds the preview depth limit");
  const trimmed = text.trim();
  const keywordMatch = trimmed.match(/^([A-Za-z]+)\s*\(/);
  if (!keywordMatch) throw new Error("unrecognized geometry keyword");
  const keyword = keywordMatch[1].toUpperCase();
  const { body, rest } = takeParenBody(trimmed.slice(keywordMatch[1].length));
  if (rest.trim() !== "") throw new Error("trailing text after geometry");
  switch (keyword) {
    case "POINT":
    case "LINESTRING":
    case "POLYGON":
      return [parseSimplePart(keyword, body, budget)];
    case "MULTIPOINT":
      return splitWKTPointList(body).map((pointText) => parseSimplePart("POINT", pointText, budget));
    case "MULTILINESTRING":
      return splitTopLevelGroups(body).map((lineText) => parseSimplePart("LINESTRING", lineText, budget));
    case "MULTIPOLYGON":
      return splitTopLevelGroups(body).map((polygonText) => parseSimplePart("POLYGON", polygonText, budget));
    case "GEOMETRYCOLLECTION": {
      const inner = body.trim();
      if (inner === "" || /^EMPTY$/i.test(inner)) return [];
      const parts: GeoPart[] = [];
      for (const memberText of splitTopLevelSequence(inner)) {
        parts.push(...parseGeneralParts(memberText, depth + 1, budget));
      }
      return parts;
    }
    default:
      throw new Error("unrecognized geometry keyword");
  }
}

function parseFixedGeo(shape: "POINT" | "BOX" | "LINESTRING" | "POLYGON", body: string): ParsedGeo {
  let rings: GeoPoint[][];
  let remaining = MAX_GEO_PREVIEW_POINTS;
  const boundedList = (text: string) => {
    const points = parseCoordinateList(text, remaining);
    remaining -= points.length;
    return points;
  };
  if (shape === "POINT") {
    rings = [[parseCoordinatePair(body)]];
  } else if (shape === "BOX" || shape === "LINESTRING") {
    rings = [boundedList(body)];
    if (shape === "BOX" && rings[0].length !== 2) throw new Error("box must contain two corners");
    if (shape === "LINESTRING" && rings[0].length < 2) throw new Error("linestring must contain at least two points");
  } else {
    if (!body.startsWith("(") || !body.endsWith(")")) throw new Error("invalid polygon value");
    const ringText = body.slice(1, -1).split(/\)\s*,\s*\(/);
    rings = [];
    for (const ring of ringText) rings.push(boundedList(ring));
    for (const ring of rings) {
      const first = ring[0];
      const last = ring[ring.length - 1];
      if (ring.length < 4 || first.x !== last.x || first.y !== last.y) {
        throw new Error("polygon rings must be closed and contain at least four points");
      }
    }
  }
  const pointCount = rings.reduce((count, ring) => count + ring.length, 0);
  for (const point of rings.flat()) {
    if (point.x < -180 || point.x > 180 || point.y < -90 || point.y > 90) {
      throw new Error("fixed geography coordinate is out of longitude/latitude range");
    }
  }
  return { shape, rings, parts: [], pointCount, bounds: geoBounds(rings) };
}

export function parseGeo(value: string): ParsedGeo {
  const trimmed = value.trim();
  const fixedMatch = trimmed.match(/^(POINT|BOX|LINESTRING|POLYGON)\s*\((.*)\)$/i);
  if (fixedMatch) {
    return parseFixedGeo(fixedMatch[1].toUpperCase() as "POINT" | "BOX" | "LINESTRING" | "POLYGON", fixedMatch[2].trim());
  }
  const generalMatch = trimmed.match(/^(MULTIPOINT|MULTILINESTRING|MULTIPOLYGON|GEOMETRYCOLLECTION)\b/i);
  if (!generalMatch) throw new Error("this geometry shape has no compact preview");
  const shape = generalMatch[1].toUpperCase() as ParsedGeo["shape"];
  const budget: GeoBudget = { points: MAX_GEO_PREVIEW_POINTS, parts: MAX_GEO_PREVIEW_PARTS };
  const parts = parseGeneralParts(trimmed, 0, budget);
  const rings = parts.flatMap((part) => part.rings);
  if (!rings.length) throw new Error("geometry has no coordinates");
  return { shape, rings: [], parts, pointCount: rings.reduce((count, ring) => count + ring.length, 0), bounds: geoBounds(rings) };
}

export function timestampDetails(value: string): TimestampDetails {
  if (!/(?:Z|[+-]\d{2}:\d{2})$/i.test(value.trim())) throw new Error("TIMESTAMPTZ value has no explicit offset");
  const date = new Date(value);
  if (!Number.isFinite(date.getTime())) throw new Error("invalid TIMESTAMPTZ value");
  return {
    utc: date.toISOString(),
    local: new Intl.DateTimeFormat(undefined, {
      dateStyle: "full",
      timeStyle: "long",
    }).format(date),
    epochMilliseconds: date.getTime(),
  };
}

export function compactCellValue(type: string, value: string | null): string {
  if (value === null) return "NULL";
  const kind = classifyStudioType(type);
  if (kind === "json") {
    try {
      const parsed: unknown = JSON.parse(value);
      if (Array.isArray(parsed)) return `JSON array · ${parsed.length.toLocaleString()} items`;
      if (parsed && typeof parsed === "object") return `JSON object · ${Object.keys(parsed).length.toLocaleString()} keys`;
    } catch {
      return "Invalid JSON";
    }
  }
  if (kind === "vector") {
    try {
      const parsed = parseVector(value, type);
      const dimensions = parsed.declaredDimensions ?? parsed.values.length;
      return parsed.mode === "sparse"
        ? `${dimensions.toLocaleString()}-d sparse vector · ${parsed.nonZero.toLocaleString()} non-zero`
        : `${dimensions.toLocaleString()}-d vector`;
    } catch {
      return "Invalid vector";
    }
  }
  if (kind === "geo") {
    try {
      const parsed = parseGeo(value);
      if (parsed.shape === "POINT") return value;
      const parts = parsed.parts.length ? `${parsed.parts.length.toLocaleString()} parts · ` : "";
      return `${parsed.shape} · ${parts}${parsed.pointCount.toLocaleString()} points`;
    } catch {
      return value;
    }
  }
  return value;
}

// EXPLAIN / EXPLAIN ANALYZE. This is the exact schema
// internal/sql/optimizer.ExplainColumns emits (server-side, unchanged by
// this parser); it never guesses column meaning from position, only from
// this exact name match, so an unrelated result set with the same column
// count is never misread as a plan.
const EXPLAIN_COLUMNS = ["operator", "estimates", "actuals", "time", "cpu", "memory", "disk", "cache", "spill", "workers", "index"];

export type ExplainNode = {
  label: string;
  depth: number;
  estRows: number | null;
  estCost: number | null;
  actRows: number | null;
  time: string;
  cpu: string;
  memory: number;
  disk: number;
  cache: number;
  spill: number;
  workers: number;
  index: string;
  children: ExplainNode[];
};

export function isExplainResult(result: StudioResultSet): boolean {
  return result.columns.length === EXPLAIN_COLUMNS.length
    && result.columns.every((column, index) => column === EXPLAIN_COLUMNS[index]);
}

function parseExplainInt(value: string | null): number | null {
  if (!value) return null;
  const match = /-?\d+/.exec(value);
  if (!match) return null;
  const parsed = Number.parseInt(match[0], 10);
  return Number.isSafeInteger(parsed) ? parsed : null;
}

// parseExplainRow reads one server-emitted row into a flat node, ignorant of
// tree position — the operator cell's own leading-space indentation (the
// server's preorder encoding, two spaces per depth) is all that positions it
// in parseExplainPlan below.
function parseExplainRow(row: (string | null)[]): { depth: number; node: ExplainNode } {
  const raw = row[0] ?? "";
  const trimmed = raw.replace(/^ +/, "");
  const depth = Math.min(MAX_EXPLAIN_DEPTH, (raw.length - trimmed.length) >> 1);
  const estimates = row[1] ?? "";
  const estMatch = /rows=(-?\d+)\s+cost=(-?\d+)/.exec(estimates);
  return {
    depth,
    node: {
      label: trimmed.trim() || "(unknown operator)",
      depth,
      estRows: estMatch ? parseExplainInt(estMatch[1]) : null,
      estCost: estMatch ? parseExplainInt(estMatch[2]) : null,
      actRows: parseExplainInt(row[2] ?? null),
      time: row[3] ?? "",
      cpu: row[4] ?? "",
      memory: parseExplainInt(row[5] ?? null) ?? 0,
      disk: parseExplainInt(row[6] ?? null) ?? 0,
      cache: parseExplainInt(row[7] ?? null) ?? 0,
      spill: parseExplainInt(row[8] ?? null) ?? 0,
      workers: parseExplainInt(row[9] ?? null) ?? 0,
      index: row[10] ?? "",
      children: [],
    },
  };
}

// parseExplainPlan reconstructs the operator tree from the server's
// preorder/indented row encoding: each row's depth (from its operator
// cell's leading-space count) determines whose children list it joins, by
// popping a stack of "last node seen at each depth" back to its parent.
// Throws (caller falls back to the plain table) only when there are no rows
// to build a tree from at all; a malformed individual row still yields a
// node, just with null/default fields, so one bad row can't hide the rest
// of a plan.
export function parseExplainPlan(result: StudioResultSet): ExplainNode[] {
  if (!isExplainResult(result) || result.rows.length === 0) {
    throw new Error("not an EXPLAIN result");
  }
  const roots: ExplainNode[] = [];
  const stack: ExplainNode[] = [];
  for (const row of result.rows) {
    const { depth, node } = parseExplainRow(row);
    while (stack.length > depth) stack.pop();
    const parent = stack[depth - 1];
    if (parent) parent.children.push(node);
    else roots.push(node);
    stack[depth] = node;
  }
  return roots;
}

// explainAnalyzed reports whether any node in the parsed plan carries a
// measured row count — i.e. the statement was EXPLAIN ANALYZE, not plain
// EXPLAIN. Callers use this to decide whether to render any measured field
// at all, never inferring it from estimates alone.
export function explainAnalyzed(nodes: ExplainNode[]): boolean {
  return nodes.some((node) => node.actRows !== null || explainAnalyzed(node.children));
}

export type ExplainEstimateSeverity = "none" | "warning" | "error";

// explainEstimateSeverity flags an operator whose measured row count is an
// order of magnitude off from its estimate — a real, common signal of stale
// statistics or a cost-model miss — as "error" (>=10x either direction) or a
// milder "warning" (>=3x). Returns "none" whenever either side is unmeasured
// (no EXPLAIN ANALYZE was run), never guessing a comparison from one side
// alone.
export function explainEstimateSeverity(estRows: number | null, actRows: number | null): ExplainEstimateSeverity {
  if (estRows === null || actRows === null) return "none";
  if (estRows === 0) return actRows === 0 ? "none" : "error";
  const ratio = actRows / estRows;
  if (ratio >= 10 || ratio <= 0.1) return "error";
  if (ratio >= 3 || ratio <= 1 / 3) return "warning";
  return "none";
}

export type ExplainPlanSnapshot = {
  capturedAt: number;
  analyzed: boolean;
  nodeCount: number;
  nodes: ExplainNode[];
};

export type ExplainPlanChange = "same" | "metrics changed" | "operator changed" | "added" | "removed";

export type ExplainPlanComparisonRow = {
  path: string;
  change: ExplainPlanChange;
  baseline: ExplainNode | null;
  current: ExplainNode | null;
};

// captureExplainPlan keeps one compact, immutable-by-convention tree per tab
// for client-side comparison. The 512-operator ceiling is deliberately much
// lower than the general 5,000-row result preview: eight tabs can retain at
// most eight small plan baselines, never duplicate eight full result buffers.
export function captureExplainPlan(nodes: ExplainNode[], capturedAt = Date.now()): ExplainPlanSnapshot {
  if (!Number.isFinite(capturedAt)) throw new Error("invalid plan capture time");
  let nodeCount = 0;
  const clone = (node: ExplainNode): ExplainNode => {
    nodeCount++;
    if (nodeCount > MAX_PLAN_COMPARISON_NODES) {
      throw new Error(`Plan comparison supports at most ${MAX_PLAN_COMPARISON_NODES} operators.`);
    }
    return { ...node, children: node.children.map(clone) };
  };
  const copy = nodes.map(clone);
  if (copy.length === 0) throw new Error("cannot capture an empty plan");
  return { capturedAt, analyzed: explainAnalyzed(copy), nodeCount, nodes: copy };
}

function flattenExplainPlan(nodes: ExplainNode[]): Map<string, ExplainNode> {
  const out = new Map<string, ExplainNode>();
  const visit = (node: ExplainNode, path: string) => {
    out.set(path, node);
    node.children.forEach((child, index) => visit(child, `${path}.${index + 1}`));
  };
  nodes.forEach((node, index) => visit(node, String(index + 1)));
  return out;
}

function comparePlanPath(a: string, b: string): number {
  const left = a.split(".").map(Number);
  const right = b.split(".").map(Number);
  for (let i = 0; i < Math.max(left.length, right.length); i++) {
    if (left[i] === undefined) return -1;
    if (right[i] === undefined) return 1;
    if (left[i] !== right[i]) return left[i] - right[i];
  }
  return 0;
}

function explainMetricsEqual(a: ExplainNode, b: ExplainNode): boolean {
  return a.estRows === b.estRows
    && a.estCost === b.estCost
    && a.actRows === b.actRows
    && a.time === b.time
    && a.cpu === b.cpu
    && a.memory === b.memory
    && a.disk === b.disk
    && a.cache === b.cache
    && a.spill === b.spill
    && a.workers === b.workers;
}

// compareExplainPlans aligns nodes only by their 1-based structural path
// (for example 1.2.1), never by a guessed semantic identity. A label or index
// change is therefore an operator/topology change; unchanged identity with
// different server values is a metric change. Added/removed nodes are kept so
// a changed tree cannot silently disappear from the comparison.
export function compareExplainPlans(
  baseline: ExplainPlanSnapshot,
  current: ExplainPlanSnapshot,
): ExplainPlanComparisonRow[] {
  const before = flattenExplainPlan(baseline.nodes);
  const after = flattenExplainPlan(current.nodes);
  const paths = [...new Set([...before.keys(), ...after.keys()])].sort(comparePlanPath);
  return paths.map((path) => {
    const left = before.get(path) ?? null;
    const right = after.get(path) ?? null;
    let change: ExplainPlanChange;
    if (!left) change = "added";
    else if (!right) change = "removed";
    else if (left.label !== right.label || left.index !== right.index) change = "operator changed";
    else if (!explainMetricsEqual(left, right)) change = "metrics changed";
    else change = "same";
    return { path, change, baseline: left, current: right };
  });
}

export type ExplainProfileRow = {
  path: string;
  node: ExplainNode;
  timeNS: number | null;
  cpuNS: number | null;
  estimateFactor: number | null;
  estimateDirection: "match" | "higher" | "lower" | "unknown";
  estimateSeverity: ExplainEstimateSeverity;
};

export type ExplainProfile = {
  nodeCount: number;
  rows: ExplainProfileRow[];
  root: ExplainProfileRow;
  slowestNonRoot: ExplainProfileRow | null;
  worstEstimate: ExplainProfileRow | null;
  peakMemory: ExplainProfileRow;
  peakSpill: ExplainProfileRow;
  maxWorkers: ExplainProfileRow;
};

// parseExplainDurationNS accepts only the exact unit vocabulary emitted by
// optimizer.formatNS. The profiler keeps the original string for display and
// uses this conversion solely to compare reported values; malformed or
// unsafe-sized values fail closed instead of being treated as measurements.
export function parseExplainDurationNS(value: string): number | null {
  const match = /^(\d+)(ns|µs|ms|s)$/.exec(value);
  if (!match) return null;
  const amount = Number(match[1]);
  const scale = match[2] === "s" ? 1_000_000_000
    : match[2] === "ms" ? 1_000_000
      : match[2] === "µs" ? 1_000
        : 1;
  const ns = amount * scale;
  return Number.isSafeInteger(ns) ? ns : null;
}

function explainEstimateDifference(
  estimated: number | null,
  actual: number | null,
): Pick<ExplainProfileRow, "estimateFactor" | "estimateDirection"> {
  if (estimated === null || actual === null) {
    return { estimateFactor: null, estimateDirection: "unknown" };
  }
  if (estimated === actual) return { estimateFactor: 1, estimateDirection: "match" };
  if (estimated === 0) return { estimateFactor: Number.POSITIVE_INFINITY, estimateDirection: "higher" };
  if (actual === 0) return { estimateFactor: Number.POSITIVE_INFINITY, estimateDirection: "lower" };
  return {
    estimateFactor: Math.max(actual / estimated, estimated / actual),
    estimateDirection: actual > estimated ? "higher" : "lower",
  };
}

// buildExplainProfile is intentionally a breakdown of server-reported values,
// not an aggregation. Executor timings can include child work, and resource
// counters may be query-level on the root, so summing them would manufacture a
// false total. Reported maxima and estimate-error factors remain truthful.
export function buildExplainProfile(nodes: ExplainNode[]): ExplainProfile {
  if (!explainAnalyzed(nodes)) {
    throw new Error("Query profiling requires EXPLAIN ANALYZE.");
  }
  const rows: ExplainProfileRow[] = [];
  const visit = (node: ExplainNode, path: string) => {
    if (rows.length >= MAX_EXPLAIN_PROFILE_NODES) {
      throw new Error(`Query profiling supports at most ${MAX_EXPLAIN_PROFILE_NODES} operators.`);
    }
    const difference = explainEstimateDifference(node.estRows, node.actRows);
    rows.push({
      path,
      node,
      timeNS: parseExplainDurationNS(node.time),
      cpuNS: parseExplainDurationNS(node.cpu),
      estimateFactor: difference.estimateFactor,
      estimateDirection: difference.estimateDirection,
      estimateSeverity: explainEstimateSeverity(node.estRows, node.actRows),
    });
    node.children.forEach((child, index) => visit(child, `${path}.${index + 1}`));
  };
  nodes.forEach((node, index) => visit(node, String(index + 1)));
  if (rows.length === 0) throw new Error("cannot profile an empty plan");

  const byReportedDuration = (left: ExplainProfileRow, right: ExplainProfileRow) =>
    (right.timeNS ?? -1) - (left.timeNS ?? -1);
  const slowestNonRoot = rows
    .filter((row) => row.node.depth > 0 && (row.timeNS ?? 0) > 0)
    .sort(byReportedDuration)[0] ?? null;
  const worstEstimate = rows
    .filter((row) => row.estimateSeverity !== "none" && row.estimateFactor !== null)
    .sort((left, right) => (right.estimateFactor ?? 0) - (left.estimateFactor ?? 0))[0] ?? null;
  const peakMemory = [...rows].sort((left, right) => right.node.memory - left.node.memory)[0];
  const peakSpill = [...rows].sort((left, right) => right.node.spill - left.node.spill)[0];
  const maxWorkers = [...rows].sort((left, right) => right.node.workers - left.node.workers)[0];

  return {
    nodeCount: rows.length,
    rows,
    root: rows[0],
    slowestNonRoot,
    worstEstimate,
    peakMemory,
    peakSpill,
    maxWorkers,
  };
}

// --- GRANT/REVOKE builder --------------------------------------------------
//
// quoteIdentifier/buildGrantSQL generate text for the editor only; they never
// execute anything themselves. Every identifier is unconditionally rendered
// as a double-quoted identifier (internal/sql/lexer's quotedIdent, `""` doubles
// an embedded quote) rather than validated against a "looks like a bare
// identifier" pattern — that makes a name containing whitespace, punctuation,
// or a reserved word exactly as safe to interpolate as a plain one, matching
// how internal/sql/parser's own p.ident() accepts either token shape
// identically. An empty (post-trim) name is always rejected instead of
// quoted, since `""` is a lexer error ("empty quoted identifier"), not a
// silently-wrong empty name.
export function quoteIdentifier(name: string): string {
  return `"${name.replace(/"/g, '""')}"`;
}

export type GrantAction = "grant" | "revoke";
export type GrantMode = "role" | "privilege";
export type GrantScope =
  | "cluster" | "database" | "schema" | "table" | "column"
  | "function" | "resourcegroup" | "backup" | "replication" | "administration";

// GRANT_PRIVILEGES intentionally omits "alter": internal/sql/parser's
// privOrIdent() (the GRANT/REVOKE privilege-list parser) only accepts a
// closed set of privilege keywords plus a bare-identifier fallback, and
// "alter" lexes as its own reserved keyword (used by ALTER TABLE) that is
// not in that accepted set — so `GRANT ALTER ON ... TO ...` fails to parse
// today even though security.PrivAlter/ParsePrivilege("alter") both exist.
// Confirmed against the real parser (internal/sql/parser), not assumed from
// reading the grammar table alone. Every other security.md-documented
// privilege spelling — including "usage"/"restore"/"cdc", none of which are
// reserved keywords and so fall through privOrIdent's bare-identifier case —
// parses correctly and is included below.
export const GRANT_PRIVILEGES: { value: string; label: string }[] = [
  { value: "connect", label: "CONNECT" },
  { value: "select", label: "SELECT" },
  { value: "insert", label: "INSERT" },
  { value: "update", label: "UPDATE" },
  { value: "delete", label: "DELETE" },
  { value: "create", label: "CREATE" },
  { value: "drop", label: "DROP" },
  { value: "index", label: "INDEX" },
  { value: "execute", label: "EXECUTE" },
  { value: "usage", label: "USAGE" },
  { value: "grant", label: "GRANT" },
  { value: "backup", label: "BACKUP" },
  { value: "restore", label: "RESTORE" },
  { value: "replication", label: "REPLICATION" },
  { value: "cdc", label: "CDC" },
  { value: "admin", label: "ADMIN" },
];

export const GRANT_SCOPES: { value: GrantScope; label: string }[] = [
  { value: "cluster", label: "CLUSTER" },
  { value: "database", label: "DATABASE" },
  { value: "schema", label: "SCHEMA" },
  { value: "table", label: "TABLE" },
  { value: "column", label: "COLUMN" },
  { value: "function", label: "FUNCTION" },
  { value: "resourcegroup", label: "RESOURCE GROUP" },
  { value: "backup", label: "BACKUP" },
  { value: "replication", label: "REPLICATION" },
  { value: "administration", label: "ADMINISTRATION" },
];

export type GrantBuilderState = {
  action: GrantAction;
  mode: GrantMode;
  roleName: string;
  grantee: string;
  allPrivileges: boolean;
  privileges: string[];
  scope: GrantScope;
  objectName: string;
  columnTable: string;
  columnName: string;
};

export type GrantBuildResult = { sql: string; error: null } | { sql: null; error: string };

// buildGrantSQL renders exactly the grammar internal/sql/parser.grantRevoke
// accepts (verified directly against that parser, not inferred): a role-
// membership statement is `GRANT|REVOKE <role> TO|FROM <grantee>` with no ON
// clause at all, while a privilege statement is
// `GRANT|REVOKE <priv[, priv...]|ALL PRIVILEGES> ON <scope>[ <object>] TO|FROM <grantee>`.
// This never executes or analyzes the statement — the caller is expected to
// insert the result into the editor, where the existing confirm-before-run
// check and RBAC still apply exactly as they would to hand-typed SQL.
export function buildGrantSQL(state: GrantBuilderState): GrantBuildResult {
  const grantee = state.grantee.trim();
  if (!grantee) return { sql: null, error: "Grantee is required." };
  const verb = state.action === "grant" ? "GRANT" : "REVOKE";
  const prep = state.action === "grant" ? "TO" : "FROM";

  if (state.mode === "role") {
    const role = state.roleName.trim();
    if (!role) return { sql: null, error: "Role name is required." };
    return { sql: `${verb} ${quoteIdentifier(role)} ${prep} ${quoteIdentifier(grantee)}`, error: null };
  }

  let privClause: string;
  if (state.allPrivileges) {
    privClause = "ALL PRIVILEGES";
  } else if (state.privileges.length > 0) {
    privClause = state.privileges.map((p) => p.toUpperCase()).join(", ");
  } else {
    return { sql: null, error: "Select at least one privilege, or ALL PRIVILEGES." };
  }

  let scopeClause: string;
  switch (state.scope) {
    case "cluster":
      scopeClause = "CLUSTER";
      break;
    case "backup":
      scopeClause = "BACKUP";
      break;
    case "replication":
      scopeClause = "REPLICATION";
      break;
    case "administration":
      scopeClause = "ADMINISTRATION";
      break;
    case "database": {
      const name = state.objectName.trim();
      scopeClause = name ? `DATABASE ${quoteIdentifier(name)}` : "DATABASE";
      break;
    }
    case "schema": {
      const name = state.objectName.trim();
      if (!name) return { sql: null, error: "Schema name is required." };
      scopeClause = `SCHEMA ${quoteIdentifier(name)}`;
      break;
    }
    case "table": {
      const name = state.objectName.trim();
      if (!name) return { sql: null, error: "Table name is required." };
      scopeClause = `TABLE ${quoteIdentifier(name)}`;
      break;
    }
    case "function": {
      const name = state.objectName.trim();
      if (!name) return { sql: null, error: "Function name is required." };
      scopeClause = `FUNCTION ${quoteIdentifier(name)}`;
      break;
    }
    case "resourcegroup": {
      const name = state.objectName.trim();
      if (!name) return { sql: null, error: "Resource group name is required." };
      scopeClause = `RESOURCE GROUP ${quoteIdentifier(name)}`;
      break;
    }
    case "column": {
      const column = state.columnName.trim();
      if (!column) return { sql: null, error: "Column name is required." };
      const table = state.columnTable.trim();
      scopeClause = table
        ? `COLUMN ${quoteIdentifier(table)}.${quoteIdentifier(column)}`
        : `COLUMN ${quoteIdentifier(column)}`;
      break;
    }
    default:
      return { sql: null, error: "Select a scope." };
  }

  return { sql: `${verb} ${privClause} ON ${scopeClause} ${prep} ${quoteIdentifier(grantee)}`, error: null };
}

// namesFromResult reads one text column out of a plain columns/rows result
// (the shape api.security() returns) for autocomplete suggestions. Missing
// column, null cells, and blank strings are all dropped silently — this
// feeds a free-text Autocomplete, so a degraded or RBAC-empty catalog read
// just means fewer suggestions, never a blocking error.
export function namesFromResult(result: { columns: string[]; rows: (string | null)[][] } | null, column: string): string[] {
  if (!result) return [];
  const index = result.columns.indexOf(column);
  if (index < 0) return [];
  const names = new Set<string>();
  for (const row of result.rows) {
    const value = row[index];
    if (value) names.add(value);
  }
  return [...names].sort();
}

// grantStateFromRow turns one system.grants row into a REVOKE-prefilled
// GrantBuilderState for the Users & roles explorer's per-grant "Revoke"
// action. system.grants never records role-membership grants — those live
// in system.roles' `members` column instead (internal/security.ACL.Grant
// dispatches role membership to GrantRole, a separate code path from the
// privilege grants SnapshotInRealm returns) — so every row is always mode
// "privilege" with exactly its one persisted privilege, never a synthetic
// "ALL PRIVILEGES": that shorthand persists as the single privilege "admin"
// (internal/executor/security.go's applyGrant), which GRANT_PRIVILEGES
// already offers, so it round-trips as `REVOKE ADMIN ON ...` rather than
// reconstructing the flag. A COLUMN-scope object is `table.column` when
// granted with a table, or a bare column name when granted scope-wide
// (the exact shape ACL.allowedForLocked splits on "."), reversed the same
// way here. privilege/scope spellings match GRANT_PRIVILEGES/GRANT_SCOPES
// exactly (both come from security.Privilege.String()/ScopeKind.String()),
// but an unrecognized scope still falls back to "table" rather than
// producing a builder state with no scope selected at all.
export function grantStateFromRow(grantee: string, privilege: string, scope: string, object: string): GrantBuilderState {
  const validScope: GrantScope = GRANT_SCOPES.some((s) => s.value === scope) ? (scope as GrantScope) : "table";
  const state: GrantBuilderState = {
    action: "revoke",
    mode: "privilege",
    roleName: "",
    grantee,
    allPrivileges: false,
    privileges: [privilege],
    scope: validScope,
    objectName: "",
    columnTable: "",
    columnName: "",
  };
  if (validScope === "column") {
    const dot = object.indexOf(".");
    return dot < 0
      ? { ...state, columnName: object }
      : { ...state, columnTable: object.slice(0, dot), columnName: object.slice(dot + 1) };
  }
  return { ...state, objectName: object };
}

// --- Find/replace -----------------------------------------------------
//
// A literal-substring search only — never a regex, so a user's own find
// text (which may contain regex metacharacters with no such intent) can
// never be misinterpreted as a pattern, and never a catastrophic-backtracking
// risk. This edits only the local editor buffer text; it never touches the
// database and needs no confirm-before-run check.

export const MAX_FIND_MATCHES = 5_000;

export type FindMatch = { start: number; end: number };

// findAllMatches returns every non-overlapping occurrence of query in text,
// bounded by MAX_FIND_MATCHES so a pathological "find everything" query
// against a large buffer can't build an unbounded array. An empty query
// matches nothing (never "every position"), matching how an empty find box
// should behave.
export function findAllMatches(text: string, query: string, matchCase: boolean): FindMatch[] {
  if (!query) return [];
  const haystack = matchCase ? text : text.toLowerCase();
  const needle = matchCase ? query : query.toLowerCase();
  const matches: FindMatch[] = [];
  let from = 0;
  while (matches.length < MAX_FIND_MATCHES) {
    const index = haystack.indexOf(needle, from);
    if (index < 0) break;
    matches.push({ start: index, end: index + needle.length });
    from = index + needle.length;
  }
  return matches;
}

// nextMatchIndex picks which match Find Next should jump to: the first
// match starting at or after `after`, wrapping to the first match
// otherwise. Returns null only when there are no matches at all.
export function nextMatchIndex(matches: FindMatch[], after: number): number | null {
  if (matches.length === 0) return null;
  const index = matches.findIndex((m) => m.start >= after);
  return index >= 0 ? index : 0;
}

// previousMatchIndex mirrors nextMatchIndex for Find Previous: the last
// match strictly before `before`, wrapping to the last match otherwise.
export function previousMatchIndex(matches: FindMatch[], before: number): number | null {
  if (matches.length === 0) return null;
  for (let i = matches.length - 1; i >= 0; i--) {
    if (matches[i].start < before) return i;
  }
  return matches.length - 1;
}

// replaceAllMatches substitutes every match with replacement in one pass
// over the precomputed match list rather than re-searching the mutated
// string, so a replacement that itself contains the search text can never
// cause runaway re-matching.
export function replaceAllMatches(text: string, matches: FindMatch[], replacement: string): string {
  if (matches.length === 0) return text;
  let result = "";
  let cursor = 0;
  for (const match of matches) {
    result += text.slice(cursor, match.start) + replacement;
    cursor = match.end;
  }
  return result + text.slice(cursor);
}

// --- Catalog-aware IntelliSense -----------------------------------------
//
// Deliberately catalog-identifiers-only: table names (always available from
// the bootstrap's already-loaded system.tables) plus column names of tables
// the buffer's own FROM/JOIN clauses reference (lazily fetched through the
// same api.studioTable() every explorer already uses — no new server
// route). NextSQL keyword completion is intentionally NOT included: the
// only ground truth for the keyword set is internal/sql/lexer's unexported
// `keywords` map, and hand-copying it into the frontend would silently
// drift the moment a keyword is added there, exactly the kind of
// unverifiable duplication this codebase's own conventions reject. A
// quoted ("...") FROM/JOIN target is also not recognized — only a bare
// (optionally schema-qualified) identifier — so a table whose name needs
// quoting simply won't get column suggestions; it can still be typed by
// hand and queried normally.
//
// Vector-aware completion is the same catalog-only rule applied to the
// NEAREST clause: after NEAREST it offers VECTOR/BITVECTOR/SPARSEVECTOR
// columns from a referenced table; after USING it offers the metrics that
// column kind actually accepts. There is no per-element vector catalog, so
// the TO (...) literal is not a completion slot.

export const MAX_SQL_SUGGESTIONS = 50;
export const MAX_REFERENCED_TABLES = 8;
export const MAX_INTELLISENSE_TABLE_CACHE = 16;

export const REFERENCED_TABLE_RE = /\b(?:FROM|JOIN)\s+([A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)?)/gi;

// extractReferencedTables scans for bare-identifier FROM/JOIN targets in
// source order, deduplicated case-sensitively (NextSQL identifiers are
// case-sensitive unless quoted-lowercased by convention) and bounded at
// MAX_REFERENCED_TABLES so a pathologically long script can't trigger an
// unbounded number of catalog fetches.
export function extractReferencedTables(sql: string): string[] {
  const seen = new Set<string>();
  const out: string[] = [];
  for (const match of sql.matchAll(REFERENCED_TABLE_RE)) {
    const name = match[1];
    if (!name || seen.has(name)) continue;
    seen.add(name);
    out.push(name);
    if (out.length >= MAX_REFERENCED_TABLES) break;
  }
  return out;
}

// MAX_QUERY_PARAMS mirrors the server's studio.MaxQueryParams: one statement
// may reference at most this many positional placeholders.
export const MAX_QUERY_PARAMS = 32;

const QUERY_PARAM_RE = /(^|[^\w$])\$(\d{1,3})(?![\w$])/g;

// extractQueryParams returns the distinct positional placeholder numbers a
// statement references ($1, $2, …), ascending. Only NextSQL's positional form
// is recognized — a $name placeholder needs named binding the Studio wire
// does not carry. Numbers are clamped to [1, MAX_QUERY_PARAMS]; a match
// inside a string literal is not filtered out (a harmless spare input row is
// better than the regex guessing at quoting), matching the FROM/JOIN scan
// above. The caller binds positionally, so a gap (referencing $1 and $3 but
// not $2) still sends a dense array up to the highest number.
export function extractQueryParams(sql: string): number[] {
  const seen = new Set<number>();
  for (const match of sql.matchAll(QUERY_PARAM_RE)) {
    const n = Number(match[2]);
    if (!Number.isInteger(n) || n < 1 || n > MAX_QUERY_PARAMS) continue;
    seen.add(n);
  }
  return [...seen].sort((a, b) => a - b);
}

// currentWordRange finds the bare-identifier word touching `cursor`,
// extending in both directions over [A-Za-z0-9_] so accepting a suggestion
// replaces the whole token being edited, not just the part left of the
// caret. Returns an empty (start === end === cursor) range when the caret
// sits between two non-word characters, meaning "insert here" rather than
// "replace something".
export function currentWordRange(text: string, cursor: number): { start: number; end: number } {
  const isWordChar = (ch: string | undefined) => ch !== undefined && /[A-Za-z0-9_]/.test(ch);
  let start = Math.max(0, Math.min(cursor, text.length));
  let end = start;
  while (start > 0 && isWordChar(text[start - 1])) start--;
  while (end < text.length && isWordChar(text[end])) end++;
  return { start, end };
}

export type TableColumnsCache = Record<string, string[] | "loading" | "error">;

// Dotted native JSON-path targets of the single-column JSON-path indexes on
// a table, keyed by table name. Populated from the same api.studioTable()
// fetch that fills TableColumnsCache; absence just means "no JSON-path
// completions offered for that table", never a guess.
export type TableJSONPathCache = Record<string, string[]>;

export type SQLSuggestion = {
  kind: "table" | "column" | "json-path" | "vector-column" | "vector-metric";
  label: string;
  insertText: string;
  table?: string;
};

export type TableVectorCache = Record<string, VectorCatalogColumn[]>;

export type NearestSuggestContext =
  | { slot: "column"; start: number; end: number; typed: string }
  | { slot: "metric"; start: number; end: number; typed: string; column: string };

// currentNearestContext detects the two NEAREST-clause slots IntelliSense
// can complete from catalog metadata: the vector column after NEAREST, and
// the metric after USING. Inside TO (...) there is no per-element catalog,
// so that slot returns null and the ordinary table/column list is not
// swapped in either — the caller only switches when this is non-null.
// Bare identifiers only, matching extractReferencedTables / currentWordRange;
// a quoted identifier is left to the operator. The scan is confined to the
// current statement (text after the last semicolon) so a later USING cannot
// attach to an earlier NEAREST.
export function currentNearestContext(text: string, cursor: number): NearestSuggestContext | null {
  const word = currentWordRange(text, cursor);
  const before = text.slice(0, word.start);
  const stmtBefore = before.slice(before.lastIndexOf(";") + 1);
  const typed = text.slice(word.start, Math.max(word.start, Math.min(cursor, word.end)));
  if (/\bNEAREST\s+$/i.test(stmtBefore)) {
    return { slot: "column", start: word.start, end: word.end, typed };
  }
  const metric = stmtBefore.match(/\bNEAREST\s+([A-Za-z_][A-Za-z0-9_]*)\s+TO\b[\s\S]*?\bUSING\s+$/i);
  if (metric) {
    return { slot: "metric", start: word.start, end: word.end, typed, column: metric[1] };
  }
  return null;
}

export function rankNearestColumnSuggestions(
  typed: string,
  referencedTables: string[],
  cache: TableVectorCache,
  limit = MAX_SQL_SUGGESTIONS,
): SQLSuggestion[] {
  const lower = typed.toLowerCase();
  const seen = new Set<string>();
  const out: SQLSuggestion[] = [];
  for (const table of referencedTables) {
    const columns = cache[table];
    if (!Array.isArray(columns)) continue;
    for (const column of [...columns].sort((a, b) => a.name.localeCompare(b.name))) {
      if (seen.has(column.name) || column.name.toLowerCase() === lower) continue;
      if (lower.length > 0 && !column.name.toLowerCase().startsWith(lower)) continue;
      seen.add(column.name);
      out.push({
        kind: "vector-column",
        label: `${column.name} — ${table} · ${column.type}`,
        insertText: column.name,
        table,
      });
      if (out.length >= limit) return out;
    }
  }
  return out;
}

export function rankNearestMetricSuggestions(
  typed: string,
  columnName: string,
  referencedTables: string[],
  cache: TableVectorCache,
  limit = MAX_SQL_SUGGESTIONS,
): SQLSuggestion[] {
  let column: VectorCatalogColumn | undefined;
  for (const table of referencedTables) {
    const columns = cache[table];
    if (!Array.isArray(columns)) continue;
    column = columns.find((candidate) => candidate.name === columnName);
    if (column) break;
  }
  if (!column) return [];
  const resolved = column;
  const lower = typed.toLowerCase();
  const out: SQLSuggestion[] = [];
  for (const metric of resolved.metrics) {
    const sqlName = VECTOR_METRIC_SQL[metric];
    if (sqlName.toLowerCase() === lower) continue;
    if (lower.length > 0 && !sqlName.toLowerCase().startsWith(lower)) continue;
    out.push({
      kind: "vector-metric",
      label: `${sqlName} — ${resolved.name} · ${resolved.type}`,
      insertText: sqlName,
      table: referencedTables.find((table) => (cache[table] ?? []).some((candidate) => candidate.name === columnName)),
    });
    if (out.length >= limit) return out;
  }
  return out;
}

// jsonPathIndexPaths extracts the dotted native JSON-path target of each
// single-column JSON-path index on a table, from its authorized
// system.indexes rows (a `columns` value that contains a "." and no ","; a
// comma means a composite index, which is not a single JSON path). These
// indexed paths are the only JSON structure NextSQL exposes any metadata
// for — "where metadata is known" in the SQL-editor checklist's own words —
// so they are the only paths completion offers. Deduped, source order.
export function jsonPathIndexPaths(indexes: StudioResultSet): string[] {
  const columnsIndex = resultColumn(indexes, "columns");
  if (columnsIndex < 0) return [];
  const seen = new Set<string>();
  const out: string[] = [];
  for (const row of indexes.rows ?? []) {
    const value = row[columnsIndex];
    if (typeof value !== "string" || !value.includes(".") || value.includes(",")) continue;
    if (value.startsWith(".") || value.endsWith(".") || value.includes("..")) continue;
    if (seen.has(value)) continue;
    seen.add(value);
    out.push(value);
  }
  return out;
}

// currentJSONPathRange finds a dotted identifier path touching `cursor`
// (`metadata`, `metadata.`, `metadata.tags`), extending left over
// [A-Za-z0-9_.] and right only over the current [A-Za-z0-9_] segment (never
// past a further ".", since the text right of the caret is not what is being
// typed). Returns null when the touched token has no "." — a plain
// identifier the existing table/column completion already handles — or is a
// malformed path shape (leading "." or empty inner segment).
export function currentJSONPathRange(
  text: string,
  cursor: number,
): { start: number; end: number; typed: string } | null {
  const c = Math.max(0, Math.min(cursor, text.length));
  let start = c;
  while (start > 0 && /[A-Za-z0-9_.]/.test(text[start - 1])) start--;
  let end = c;
  while (end < text.length && /[A-Za-z0-9_]/.test(text[end])) end++;
  const token = text.slice(start, end);
  if (!token.includes(".") || token.startsWith(".") || token.includes("..")) return null;
  return { start, end, typed: text.slice(start, c) };
}

// rankJSONPathSuggestions offers the known indexed JSON paths — across every
// FROM/JOIN-referenced table whose index metadata has already resolved in
// `cache` — that begin with what the user has typed. Case-insensitive
// prefix, deduped, sorted, bounded. Never a guessed path: only one that is
// actually indexed on a referenced table.
export function rankJSONPathSuggestions(
  typed: string,
  referencedTables: string[],
  cache: TableJSONPathCache,
  limit = MAX_SQL_SUGGESTIONS,
): SQLSuggestion[] {
  const lower = typed.toLowerCase();
  const seen = new Set<string>();
  const out: SQLSuggestion[] = [];
  for (const table of referencedTables) {
    const paths = cache[table];
    if (!Array.isArray(paths)) continue;
    for (const path of [...paths].sort((a, b) => a.localeCompare(b))) {
      if (seen.has(path) || path.toLowerCase() === lower) continue;
      if (lower.length > 0 && !path.toLowerCase().startsWith(lower)) continue;
      seen.add(path);
      out.push({ kind: "json-path", label: `${path} — ${table}`, insertText: path, table });
      if (out.length >= limit) return out;
    }
  }
  return out;
}

// rankSQLSuggestions is pure and synchronous: it never fetches anything,
// only ranks whatever the caller already has. Table names always come from
// the already-loaded catalog; column names are offered only for tables
// whose columns have already resolved in `cache` (a "loading"/"error"/
// absent entry contributes no column suggestions, never a guess). An empty
// prefix matches everything (a manually triggered "show me what's here"),
// matching the bounded MAX_SQL_SUGGESTIONS ceiling either way.
export function rankSQLSuggestions(
  prefix: string,
  tableNames: string[],
  referencedTables: string[],
  cache: TableColumnsCache,
  limit = MAX_SQL_SUGGESTIONS,
): SQLSuggestion[] {
  const lowerPrefix = prefix.toLowerCase();
  const matches = (name: string) => lowerPrefix.length === 0 || name.toLowerCase().startsWith(lowerPrefix);
  const out: SQLSuggestion[] = [];

  for (const name of [...tableNames].filter(matches).sort((a, b) => a.localeCompare(b))) {
    if (out.length >= limit) return out;
    out.push({ kind: "table", label: name, insertText: name });
  }
  for (const table of referencedTables) {
    const columns = cache[table];
    if (!Array.isArray(columns)) continue;
    for (const column of columns.filter(matches).sort((a, b) => a.localeCompare(b))) {
      if (out.length >= limit) return out;
      out.push({ kind: "column", label: `${column} — ${table}`, insertText: column, table });
    }
  }
  return out;
}

// withCachedTableLoading folds one "start fetching this table's columns"
// transition into the cache + fetch-order pair in one pure step, so the
// MAX_INTELLISENSE_TABLE_CACHE eviction bound is unit-testable independent
// of React state timing. A table already present (loaded, loading, or
// errored) is a no-op — it is never re-fetched or reordered by this
// function. Eviction always removes the least-recently-*added* table (not
// least-recently-used), the same simple bound MAX_STUDIO_TABS uses for
// EXPLAIN plan-comparison baselines.
export function withCachedTableLoading(
  cache: TableColumnsCache,
  order: string[],
  table: string,
  max = MAX_INTELLISENSE_TABLE_CACHE,
): { cache: TableColumnsCache; order: string[] } {
  if (table in cache) return { cache, order };
  const nextOrder = [...order, table];
  let nextCache: TableColumnsCache = { ...cache, [table]: "loading" };
  while (Object.keys(nextCache).length > max && nextOrder.length > 0) {
    const evict = nextOrder.shift();
    if (evict !== undefined && evict !== table) {
      const { [evict]: _dropped, ...rest } = nextCache;
      nextCache = rest;
    }
  }
  return { cache: nextCache, order: nextOrder };
}

// --- Deterministic misspelled table-name suggestions ---------------------
//
// Bounded and honest, building directly on the IntelliSense extraction
// above: flags a *bare* (unqualified) FROM/JOIN target that names no real
// table, and offers a fix only when exactly one already-known table name is
// a small edit distance away. Never flags — and never suggests a fix for —
// a schema-qualified target (e.g. "system.capabilities"): the Studio
// bootstrap's table list is internal/executor's persisted user tables
// (system.tables' own rows) only, never the separate virtual system.*
// catalog views, so there is no ground-truth list to compare a qualified
// name against without guessing. Never flags anything while the bootstrap's
// own table list is truncated (studio.MaxTables): a partial catalog read
// cannot prove a name doesn't exist beyond the cutoff. A tie between two
// or more equally-close real table names is never resolved by guessing —
// no fix is offered for it, matching this codebase's existing
// ambiguous-rather-than-guessed convention (see the JSON Explorer's
// indexed-path indicator).

export type TableNameFix = {
  badName: string;
  suggestion: string;
  occurrences: { start: number; end: number }[];
};

export const MAX_TABLE_NAME_FIXES = 5;

// levenshteinDistance is the classic single-row edit-distance DP. Callers
// bound it to short identifier-length strings; NextSQL identifiers are
// already lexer-bounded in length, and the FROM/JOIN extraction above caps
// how many distinct targets a script can contribute.
export function levenshteinDistance(a: string, b: string): number {
  if (a === b) return 0;
  if (a.length === 0) return b.length;
  if (b.length === 0) return a.length;
  const prev = new Array<number>(b.length + 1);
  for (let j = 0; j <= b.length; j++) prev[j] = j;
  for (let i = 1; i <= a.length; i++) {
    let diag = prev[0];
    prev[0] = i;
    for (let j = 1; j <= b.length; j++) {
      const temp = prev[j];
      prev[j] = a[i - 1] === b[j - 1] ? diag : 1 + Math.min(diag, prev[j - 1], prev[j]);
      diag = temp;
    }
  }
  return prev[b.length];
}

// maxAllowedEditDistance keeps a short misread name (e.g. "ordrs") from
// matching too loosely — a 1-character tolerance below 5 characters, 2
// above, and no scaling further: a larger distance stops being a
// deterministic "misspelling" and starts being a guess.
function maxAllowedEditDistance(len: number): number {
  return len <= 4 ? 1 : 2;
}

export function suggestTableNameFixes(sql: string, tableNames: string[], tablesTruncated: boolean): TableNameFix[] {
  if (tablesTruncated) return [];
  const known = new Set(tableNames);
  const byName = new Map<string, { start: number; end: number }[]>();
  for (const match of sql.matchAll(REFERENCED_TABLE_RE)) {
    const name = match[1];
    if (!name || name.includes(".") || known.has(name)) continue;
    const groupStart = (match.index ?? 0) + match[0].length - name.length;
    const span = { start: groupStart, end: groupStart + name.length };
    const existing = byName.get(name);
    if (existing) existing.push(span);
    else byName.set(name, [span]);
  }

  const out: TableNameFix[] = [];
  for (const [badName, occurrences] of byName) {
    if (out.length >= MAX_TABLE_NAME_FIXES) break;
    const limit = maxAllowedEditDistance(badName.length);
    let best: string | null = null;
    let bestDist = Infinity;
    let tie = false;
    for (const candidate of tableNames) {
      if (Math.abs(candidate.length - badName.length) > limit) continue;
      const dist = levenshteinDistance(badName, candidate);
      if (dist > limit) continue;
      if (dist < bestDist) {
        bestDist = dist;
        best = candidate;
        tie = false;
      } else if (dist === bestDist) {
        tie = true;
      }
    }
    if (best && !tie) out.push({ badName, suggestion: best, occurrences });
  }
  return out;
}

// applyTableNameFix rewrites every occurrence of the one bad identifier
// this fix was computed for, working from the last span backward so
// earlier offsets stay valid; it never touches any other text.
export function applyTableNameFix(sql: string, fix: TableNameFix): string {
  let result = sql;
  for (const { start, end } of [...fix.occurrences].sort((a, b) => b.start - a.start)) {
    result = result.slice(0, start) + fix.suggestion + result.slice(end);
  }
  return result;
}

// --- Connection environment tagging (production safety) ------------------
//
// Studio has no multi-target connection model yet — it always runs on the
// single nextsqld the Operations-mode login reached. What it can do is let
// the operator label *that* connection's environment so a production
// connection shows a standing banner and defaults to a read-only safety
// mode. The label is a per-viewer browser preference only: it is stored in
// localStorage keyed by realm/database/user, never sent anywhere, and is
// not a security boundary (server-side RBAC is). No credential is stored.

export const STUDIO_ENVIRONMENTS = ["development", "test", "staging", "production"] as const;
export type StudioEnvironment = (typeof STUDIO_ENVIRONMENTS)[number];

export function isStudioEnvironment(value: unknown): value is StudioEnvironment {
  return typeof value === "string" && (STUDIO_ENVIRONMENTS as readonly string[]).includes(value);
}

// environmentStorageKey identifies which localStorage slot a connection's
// environment label lives in. Segments are sanitized so an exotic
// realm/database/user name can't break the key shape; this is a storage
// key, not an identity check.
export function environmentStorageKey(realm: string, database: string, user: string): string {
  const seg = (s: string) => (s || "default").replace(/[^A-Za-z0-9_.-]/g, "_");
  return `nextsql-studio-env:${seg(realm)}:${seg(database)}:${seg(user || "unknown")}`;
}

// editorDraftStorageKey is the localStorage slot for this connection's unsaved
// editor buffers (crash recovery). Same per-connection scoping and segment
// sanitization as environmentStorageKey.
export function editorDraftStorageKey(realm: string, database: string, user: string): string {
  const seg = (s: string) => (s || "default").replace(/[^A-Za-z0-9_.-]/g, "_");
  return `nextsql-studio-drafts:${seg(realm)}:${seg(database)}:${seg(user || "unknown")}`;
}

// layoutStorageKey is the localStorage slot for this connection's IDE
// layout (pane visibility + widths + last selected table name). Same
// per-connection scoping as the draft buffers. Never a credential.
export function layoutStorageKey(realm: string, database: string, user: string): string {
  const seg = (s: string) => (s || "default").replace(/[^A-Za-z0-9_.-]/g, "_");
  return `nextsql-studio-layout:${seg(realm)}:${seg(database)}:${seg(user || "unknown")}`;
}

// --- Recent connections (realm/database quick-switch) -----------------
//
// A convenience list of realm/database pairs recently switched to on the
// current nextsqld, scoped per host + user (those are what stay fixed; the
// realm/database are what vary). No credential is ever stored — a recent
// entry only prefills the Switch-connection form, which still requires the
// password. Pure/bounded so the browser storage read/write in the component
// stays trivial and try/caught.

export const MAX_RECENT_CONNECTIONS = 10;
const MAX_RECENT_NAME_LEN = 128;

export type RecentConnection = { realm: string; database: string; at: number };

// recentConnectionStorageKey scopes the list to one nextsqld + NSQL user.
export function recentConnectionStorageKey(serverAddr: string, user: string): string {
  const seg = (s: string) => (s || "default").replace(/[^A-Za-z0-9_.-]/g, "_");
  return `nextsql-studio-recents:${seg(serverAddr)}:${seg(user || "unknown")}`;
}

// parseRecentConnections decodes the stored JSON array. Anything malformed —
// not an array, wrong field types, over-long names — yields an empty list
// rather than a throw, matching every other Studio browser-state codec.
export function parseRecentConnections(raw: string | null): RecentConnection[] {
  if (!raw) return [];
  let data: unknown;
  try {
    data = JSON.parse(raw);
  } catch {
    return [];
  }
  if (!Array.isArray(data)) return [];
  const out: RecentConnection[] = [];
  for (const item of data) {
    if (!item || typeof item !== "object") continue;
    const rec = item as Record<string, unknown>;
    const realm = typeof rec.realm === "string" ? rec.realm : "";
    const database = typeof rec.database === "string" ? rec.database : "";
    const at = typeof rec.at === "number" && Number.isFinite(rec.at) ? rec.at : 0;
    if (realm.length > MAX_RECENT_NAME_LEN || database.length > MAX_RECENT_NAME_LEN) continue;
    if (realm === "" && database === "") continue;
    if (out.some((e) => e.realm === realm && e.database === database)) continue;
    out.push({ realm, database, at });
    if (out.length >= MAX_RECENT_CONNECTIONS) break;
  }
  return out;
}

export function serializeRecentConnections(list: RecentConnection[]): string {
  return JSON.stringify(list.slice(0, MAX_RECENT_CONNECTIONS));
}

// recordRecentConnection returns a new list with (realm, database) moved to
// the front (most recent first), de-duplicated, and capped. An all-empty
// pair (the deployment default) is not worth recording on its own — the
// toolbar already shows the current connection — so it is dropped.
export function recordRecentConnection(
  list: RecentConnection[],
  realm: string,
  database: string,
  now: number,
): RecentConnection[] {
  if (realm === "" && database === "") return list.slice(0, MAX_RECENT_CONNECTIONS);
  const rest = list.filter((e) => !(e.realm === realm && e.database === database));
  return [{ realm, database, at: now }, ...rest].slice(0, MAX_RECENT_CONNECTIONS);
}

// recentConnectionLabel renders one entry for a button/menu: "(default)" for
// an empty segment, "realm / database" otherwise.
export function recentConnectionLabel(entry: RecentConnection): string {
  return `${entry.realm || "(default realm)"} / ${entry.database || "(default database)"}`;
}

// --- Workflow trigger/schedule relationship diagram --------------------
//
// The catalog keeps trigger and schedule definitions as separate row sets.
// Build a deterministic graph from named columns only; malformed/older
// shapes are ignored rather than guessed. The API independently caps each
// source at 500 rows, and this helper applies a second combined relationship
// cap before anything is mounted in the always-visible text alternative.

export const MAX_WORKFLOW_RELATIONSHIPS = 500;
export const MAX_WORKFLOW_DIAGRAM_NODES = 60;
export const MAX_WORKFLOW_DIAGRAM_EDGES = 80;

export type WorkflowRelationship = {
  key: string;
  kind: "trigger" | "schedule";
  name: string;
  source: string;
  workflow: string;
  detail: string;
  enabled: boolean | null;
};

export type WorkflowRelationshipModel = {
  workflows: string[];
  relationships: WorkflowRelationship[];
  truncated: boolean;
};

function catalogString(row: (string | null)[], index: number): string | null {
  if (index < 0) return null;
  const value = row[index];
  return typeof value === "string" && value !== "" ? value : null;
}

export function buildWorkflowRelationships(
  workflows: StudioResultSet | null,
  triggers: StudioResultSet | null,
  schedules: StudioResultSet | null,
): WorkflowRelationshipModel {
  const workflowNames = new Set<string>();
  const workflowNameIndex = workflows?.columns.indexOf("name") ?? -1;
  for (const row of workflows?.rows ?? []) {
    const name = catalogString(row, workflowNameIndex);
    if (name) workflowNames.add(name);
  }

  const relationships = new Map<string, WorkflowRelationship>();
  if (triggers) {
    const ci = {
      name: triggers.columns.indexOf("name"),
      timing: triggers.columns.indexOf("timing"),
      event: triggers.columns.indexOf("event"),
      table: triggers.columns.indexOf("table_name"),
      workflow: triggers.columns.indexOf("workflow"),
    };
    if (ci.name >= 0 && ci.table >= 0 && ci.workflow >= 0) {
      for (const row of triggers.rows) {
        const name = catalogString(row, ci.name);
        const source = catalogString(row, ci.table);
        const workflow = catalogString(row, ci.workflow);
        if (!name || !source || !workflow) continue;
        const timing = catalogString(row, ci.timing);
        const event = catalogString(row, ci.event);
        const key = `trigger\u0000${source}\u0000${name}\u0000${workflow}`;
        if (!relationships.has(key)) {
          relationships.set(key, {
            key,
            kind: "trigger",
            name,
            source,
            workflow,
            detail: [timing, event].filter(Boolean).join(" "),
            enabled: null,
          });
        }
        workflowNames.add(workflow);
      }
    }
  }

  if (schedules) {
    const ci = {
      name: schedules.columns.indexOf("name"),
      kind: schedules.columns.indexOf("kind"),
      spec: schedules.columns.indexOf("spec"),
      workflow: schedules.columns.indexOf("workflow"),
      enabled: schedules.columns.indexOf("enabled"),
    };
    if (ci.name >= 0 && ci.workflow >= 0) {
      for (const row of schedules.rows) {
        const name = catalogString(row, ci.name);
        const workflow = catalogString(row, ci.workflow);
        if (!name || !workflow) continue;
        const kind = catalogString(row, ci.kind);
        const spec = catalogString(row, ci.spec);
        const rawEnabled = catalogString(row, ci.enabled)?.toLowerCase() ?? null;
        const enabled = rawEnabled === "true" ? true : rawEnabled === "false" ? false : null;
        const key = `schedule\u0000${name}\u0000${workflow}`;
        if (!relationships.has(key)) {
          relationships.set(key, {
            key,
            kind: "schedule",
            name,
            source: name,
            workflow,
            detail: [kind, spec].filter(Boolean).join(" "),
            enabled,
          });
        }
        workflowNames.add(workflow);
      }
    }
  }

  const all = [...relationships.values()].sort((a, b) =>
    a.workflow.localeCompare(b.workflow) ||
    a.kind.localeCompare(b.kind) ||
    a.source.localeCompare(b.source) ||
    a.name.localeCompare(b.name),
  );
  const truncated = Boolean(workflows?.truncated || triggers?.truncated || schedules?.truncated || all.length > MAX_WORKFLOW_RELATIONSHIPS);
  return {
    workflows: [...workflowNames].sort(),
    relationships: all.slice(0, MAX_WORKFLOW_RELATIONSHIPS),
    truncated,
  };
}

export type WorkflowDiagramNode = {
  id: string;
  kind: "table" | "trigger" | "schedule" | "workflow";
  name: string;
  x: number;
  y: number;
  w: number;
  h: number;
};
export type WorkflowDiagramLink = { key: string; d: string; title: string };
export type WorkflowDiagramLayout = {
  width: number;
  height: number;
  nodes: WorkflowDiagramNode[];
  links: WorkflowDiagramLink[];
};

export function layoutWorkflowRelationships(model: WorkflowRelationshipModel): WorkflowDiagramLayout | null {
  const nodeByID = new Map<string, { kind: WorkflowDiagramNode["kind"]; name: string; layer: number }>();
  const edges: { key: string; from: string; to: string; title: string }[] = [];
  const nodeID = (kind: WorkflowDiagramNode["kind"], name: string) => `${kind}\u0000${name}`;
  const addNode = (kind: WorkflowDiagramNode["kind"], name: string, layer: number) => {
    const id = nodeID(kind, name);
    if (!nodeByID.has(id)) nodeByID.set(id, { kind, name, layer });
    return id;
  };

  for (const workflow of model.workflows) addNode("workflow", workflow, 2);
  for (const relation of model.relationships) {
    const workflow = addNode("workflow", relation.workflow, 2);
    if (relation.kind === "trigger") {
      const table = addNode("table", relation.source, 0);
      const trigger = addNode("trigger", relation.name, 1);
      edges.push({
        key: `${relation.key}\u0000table`,
        from: table,
        to: trigger,
        title: `${relation.source} fires ${relation.name}`,
      });
      edges.push({
        key: `${relation.key}\u0000workflow`,
        from: trigger,
        to: workflow,
        title: `${relation.name} runs ${relation.workflow}`,
      });
    } else {
      const schedule = addNode("schedule", relation.name, 0);
      edges.push({
        key: relation.key,
        from: schedule,
        to: workflow,
        title: `${relation.name} runs ${relation.workflow}`,
      });
    }
  }

  if (
    model.truncated ||
    nodeByID.size === 0 ||
    nodeByID.size > MAX_WORKFLOW_DIAGRAM_NODES ||
    edges.length > MAX_WORKFLOW_DIAGRAM_EDGES
  ) {
    return null;
  }

  const nodeW = 190;
  const nodeH = 42;
  const gapX = 64;
  const gapY = 18;
  const pad = 10;
  const byLayer: [string[], string[], string[]] = [[], [], []];
  for (const [id, node] of nodeByID) byLayer[node.layer].push(id);
  for (const layer of byLayer) {
    layer.sort((a, b) => {
      const an = nodeByID.get(a)!;
      const bn = nodeByID.get(b)!;
      return an.kind.localeCompare(bn.kind) || an.name.localeCompare(bn.name);
    });
  }

  const positioned = new Map<string, WorkflowDiagramNode>();
  byLayer.forEach((layer, layerIndex) => {
    layer.forEach((id, rowIndex) => {
      const node = nodeByID.get(id)!;
      positioned.set(id, {
        id,
        kind: node.kind,
        name: node.name,
        x: pad + layerIndex * (nodeW + gapX),
        y: pad + rowIndex * (nodeH + gapY),
        w: nodeW,
        h: nodeH,
      });
    });
  });

  const links = edges.map((edge) => {
    const from = positioned.get(edge.from)!;
    const to = positioned.get(edge.to)!;
    const x1 = from.x + from.w;
    const y1 = from.y + from.h / 2;
    const x2 = to.x;
    const y2 = to.y + to.h / 2;
    const dx = Math.max(40, (x2 - x1) / 2);
    return {
      key: edge.key,
      d: `M ${x1} ${y1} C ${x1 + dx} ${y1}, ${x2 - dx} ${y2}, ${x2} ${y2}`,
      title: edge.title,
    };
  });

  return {
    width: pad * 2 + 3 * nodeW + 2 * gapX,
    height: pad * 2 + Math.max(...byLayer.map((layer) => layer.length), 1) * nodeH +
      Math.max(Math.max(...byLayer.map((layer) => layer.length), 1) - 1, 0) * gapY,
    nodes: [...positioned.values()],
    links,
  };
}

export function workflowRelationshipLabel(relation: WorkflowRelationship): string {
  const detail = relation.detail ? ` (${relation.detail})` : "";
  if (relation.kind === "trigger") {
    return `${relation.source} → ${relation.name}${detail} → ${relation.workflow}`;
  }
  const state = relation.enabled === null ? "" : relation.enabled ? "enabled" : "disabled";
  const scheduleDetail = [relation.detail, state].filter(Boolean).join(", ");
  return `${relation.name}${scheduleDetail ? ` (${scheduleDetail})` : ""} → ${relation.workflow}`;
}

export function workflowRelationshipSummary(model: WorkflowRelationshipModel): string {
  const triggerCount = model.relationships.filter((relation) => relation.kind === "trigger").length;
  const scheduleCount = model.relationships.length - triggerCount;
  const workflowCount = model.workflows.length;
  return `Workflow relationship diagram: ${workflowCount} ${workflowCount === 1 ? "workflow" : "workflows"}, ${triggerCount} ${triggerCount === 1 ? "trigger" : "triggers"}, ${scheduleCount} ${scheduleCount === 1 ? "schedule" : "schedules"}.`;
}

// --- Schema-relationship diagram (ER from actual foreign keys) -----------
//
// buildSchemaGraph collapses the flat one-row-per-referencing-column
// system.foreign_keys result into one edge per constraint, and groups the
// edges by referenced (parent) table for a readable relationships list.
// layoutSchemaGraph places the tables in dependency layers (a table that
// references nothing is leftmost; a table is one layer right of the
// furthest table it references, with cycles broken) and returns SVG
// geometry — or null when the graph is too large to draw legibly, in which
// case the caller shows the grouped list alone. Everything here is pure.

export const MAX_SCHEMA_DIAGRAM_TABLES = 40;
export const MAX_SCHEMA_DIAGRAM_EDGES = 80;

export type SchemaGraphEdge = {
  key: string;
  constraint: string;
  child: string;
  parent: string;
  columns: string[];
  refColumns: string[];
  onDelete: string | null;
};

export type SchemaGraphModel = {
  tables: string[];
  edges: SchemaGraphEdge[];
  byParent: { parent: string; edges: SchemaGraphEdge[] }[];
};

export function buildSchemaGraph(
  result: { columns: string[]; rows: (string | null)[][] } | null,
): SchemaGraphModel {
  const empty: SchemaGraphModel = { tables: [], edges: [], byParent: [] };
  if (!result) return empty;
  const col = (name: string) => result.columns.indexOf(name);
  const ci = {
    table: col("table_name"),
    constraint: col("constraint_name"),
    column: col("column_name"),
    refTable: col("ref_table"),
    refColumn: col("ref_column"),
    onDelete: col("on_delete"),
  };
  if (ci.table < 0 || ci.constraint < 0 || ci.refTable < 0) return empty;

  const grouped = new Map<string, SchemaGraphEdge>();
  const order: string[] = [];
  for (const row of result.rows) {
    const child = row[ci.table];
    const parent = row[ci.refTable];
    const constraint = row[ci.constraint];
    if (!child || !parent || !constraint) continue;
    const key = `${child}::${constraint}`;
    let edge = grouped.get(key);
    if (!edge) {
      edge = {
        key,
        constraint,
        child,
        parent,
        columns: [],
        refColumns: [],
        onDelete: ci.onDelete >= 0 ? row[ci.onDelete] : null,
      };
      grouped.set(key, edge);
      order.push(key);
    }
    const c = ci.column >= 0 ? row[ci.column] : null;
    const rc = ci.refColumn >= 0 ? row[ci.refColumn] : null;
    if (c) edge.columns.push(c);
    if (rc) edge.refColumns.push(rc);
  }

  const edges = order.map((key) => grouped.get(key)!);
  const tableSet = new Set<string>();
  for (const edge of edges) {
    tableSet.add(edge.child);
    tableSet.add(edge.parent);
  }
  const tables = [...tableSet].sort();

  const parents = new Map<string, SchemaGraphEdge[]>();
  for (const edge of edges) {
    const list = parents.get(edge.parent) ?? [];
    list.push(edge);
    parents.set(edge.parent, list);
  }
  const byParent = [...parents.keys()].sort().map((parent) => ({
    parent,
    edges: parents.get(parent)!.slice().sort((a, b) => a.child.localeCompare(b.child) || a.constraint.localeCompare(b.constraint)),
  }));

  return { tables, edges, byParent };
}

export type SchemaGraphNode = { name: string; x: number; y: number; w: number; h: number };
export type SchemaGraphLink = { key: string; child: string; parent: string; d: string; title: string };
export type SchemaGraphLayout = {
  width: number;
  height: number;
  nodes: SchemaGraphNode[];
  links: SchemaGraphLink[];
};

export function layoutSchemaGraph(model: SchemaGraphModel): SchemaGraphLayout | null {
  if (
    model.tables.length === 0 ||
    model.tables.length > MAX_SCHEMA_DIAGRAM_TABLES ||
    model.edges.length > MAX_SCHEMA_DIAGRAM_EDGES
  ) {
    return null;
  }

  const nodeW = 168;
  const nodeH = 30;
  const gapX = 96;
  const gapY = 18;
  const pad = 8;

  // outbound[t] = tables t references (its parents).
  const outbound = new Map<string, Set<string>>();
  for (const t of model.tables) outbound.set(t, new Set());
  for (const edge of model.edges) {
    if (edge.child !== edge.parent) outbound.get(edge.child)!.add(edge.parent);
  }

  // level(t): 0 if t references nothing; else 1 + max(level(parent)). Cycles
  // are broken by treating a back-edge to an in-progress node as level 0.
  const level = new Map<string, number>();
  const inProgress = new Set<string>();
  const visit = (t: string): number => {
    const cached = level.get(t);
    if (cached !== undefined) return cached;
    if (inProgress.has(t)) return 0;
    inProgress.add(t);
    let max = -1;
    for (const parent of outbound.get(t) ?? []) {
      max = Math.max(max, visit(parent));
    }
    inProgress.delete(t);
    const value = max + 1;
    level.set(t, value);
    return value;
  };
  for (const t of model.tables) visit(t);

  const byLevel = new Map<number, string[]>();
  for (const t of model.tables) {
    const l = level.get(t) ?? 0;
    const list = byLevel.get(l) ?? [];
    list.push(t);
    byLevel.set(l, list);
  }
  const levels = [...byLevel.keys()].sort((a, b) => a - b);
  for (const l of levels) byLevel.get(l)!.sort();

  const pos = new Map<string, SchemaGraphNode>();
  let maxRows = 0;
  levels.forEach((l, columnIndex) => {
    const column = byLevel.get(l)!;
    maxRows = Math.max(maxRows, column.length);
    column.forEach((name, rowIndex) => {
      pos.set(name, {
        name,
        x: pad + columnIndex * (nodeW + gapX),
        y: pad + rowIndex * (nodeH + gapY),
        w: nodeW,
        h: nodeH,
      });
    });
  });

  const width = pad * 2 + levels.length * (nodeW + gapX) - gapX;
  const height = pad * 2 + Math.max(maxRows, 1) * (nodeH + gapY) - gapY;

  const links: SchemaGraphLink[] = model.edges
    .filter((edge) => edge.child !== edge.parent && pos.has(edge.child) && pos.has(edge.parent))
    .map((edge) => {
      const child = pos.get(edge.child)!;
      const parent = pos.get(edge.parent)!;
      // Edge runs from the child's left edge to the parent's right edge
      // (child sits to the right of the tables it references).
      const x1 = child.x;
      const y1 = child.y + child.h / 2;
      const x2 = parent.x + parent.w;
      const y2 = parent.y + parent.h / 2;
      const dx = Math.max(40, Math.abs(x1 - x2) / 2);
      const cols = edge.columns.join(", ");
      const refs = edge.refColumns.join(", ");
      const action = edge.onDelete && edge.onDelete !== "RESTRICT" ? ` ON DELETE ${edge.onDelete}` : "";
      return {
        key: edge.key,
        child: edge.child,
        parent: edge.parent,
        d: `M ${x1} ${y1} C ${x1 - dx} ${y1}, ${x2 + dx} ${y2}, ${x2} ${y2}`,
        title: `${edge.child} (${cols}) → ${edge.parent} (${refs})${action}`,
      };
    });

  return { width, height, nodes: [...pos.values()], links };
}

export function schemaGraphSummary(model: SchemaGraphModel): string {
  const t = model.tables.length;
  const e = model.edges.length;
  return `Foreign-key relationship diagram: ${t} ${t === 1 ? "table" : "tables"}, ${e} ${e === 1 ? "reference" : "references"}.`;
}

// --- Global object search ----------------------------------------------
//
// Studio can enumerate two object namespaces completely and cheaply: the
// table list (from the bootstrap read) and the workflow list (one authorized
// system.workflows read). rankObjectMatches ranks those by how well a query
// matches the name: exact, then prefix, then substring, then in-order
// subsequence ("fuzzy"). It never fetches anything and never guesses a match
// that isn't a real subsequence of the name. Columns/indexes are not
// searched here — that would need a fetch per table.

export const MAX_OBJECT_SEARCH_RESULTS = 50;

export type StudioObjectKind = "table" | "workflow";
export type StudioObjectMatch = { name: string; kind: StudioObjectKind };

function isSubsequence(needle: string, haystack: string): boolean {
  let i = 0;
  for (let j = 0; j < haystack.length && i < needle.length; j++) {
    if (haystack[j] === needle[i]) i++;
  }
  return i === needle.length;
}

// --- Crash recovery for unsaved editors -------------------------------
//
// The Studio editor's tab buffers (title + SQL text only) are mirrored to
// localStorage so a browser crash / accidental close / reload does not lose
// unsaved work. Results, errors, plan baselines and query history are NOT
// mirrored — only the text you typed. serializeEditorDrafts/parseEditorDrafts
// are the pure, bounded codec; the caller wraps every storage access in
// try/catch and treats any failure as "no draft".

export const MAX_EDITOR_DRAFT_TABS = 8;
export const MAX_EDITOR_DRAFT_SQL_CHARS = 200_000;

export type EditorDraft = { title: string; sql: string };
export type EditorDrafts = { tabs: EditorDraft[]; activeIndex: number };

export function serializeEditorDrafts(tabs: EditorDraft[], activeIndex: number): string {
  const bounded = tabs
    .slice(0, MAX_EDITOR_DRAFT_TABS)
    .map((tab) => ({
      title: String(tab.title ?? "").slice(0, 200),
      sql: String(tab.sql ?? "").slice(0, MAX_EDITOR_DRAFT_SQL_CHARS),
    }));
  const clampedIndex = Number.isInteger(activeIndex)
    ? Math.min(Math.max(activeIndex, 0), Math.max(bounded.length - 1, 0))
    : 0;
  return JSON.stringify({ v: 1, tabs: bounded, activeIndex: clampedIndex });
}

export function parseEditorDrafts(raw: string | null | undefined): EditorDrafts | null {
  if (!raw) return null;
  let parsed: unknown;
  try {
    parsed = JSON.parse(raw);
  } catch {
    return null;
  }
  if (!parsed || typeof parsed !== "object") return null;
  const record = parsed as { tabs?: unknown; activeIndex?: unknown };
  if (!Array.isArray(record.tabs)) return null;
  const tabs: EditorDraft[] = [];
  for (const entry of record.tabs) {
    if (tabs.length >= MAX_EDITOR_DRAFT_TABS) break;
    if (!entry || typeof entry !== "object") continue;
    const e = entry as { title?: unknown; sql?: unknown };
    if (typeof e.sql !== "string") continue;
    tabs.push({
      title: typeof e.title === "string" && e.title ? e.title.slice(0, 200) : "Query",
      sql: e.sql.slice(0, MAX_EDITOR_DRAFT_SQL_CHARS),
    });
  }
  if (tabs.length === 0) return null;
  const activeIndex =
    Number.isInteger(record.activeIndex) &&
    (record.activeIndex as number) >= 0 &&
    (record.activeIndex as number) < tabs.length
      ? (record.activeIndex as number)
      : 0;
  return { tabs, activeIndex };
}

// editorDraftsWorthRestoring reports whether a parsed draft set is more than
// the pristine single default buffer — so a fresh session never shows a
// "restored" notice for content the user never actually wrote.
export function editorDraftsWorthRestoring(drafts: EditorDrafts | null, defaultSQL: string): boolean {
  if (!drafts) return false;
  if (drafts.tabs.length > 1) return true;
  const only = drafts.tabs[0];
  return only.sql.trim() !== "" && only.sql.trim() !== defaultSQL.trim();
}

// --- Layout persistence without credentials ----------------------------
//
// Explorer/inspector visibility and pixel widths, plus the last selected
// table *name*, are mirrored to localStorage per connection. SQL, results,
// query history, parameters, and any credential are out of scope — those
// either already have their own codec or must never persist. Malformed
// storage yields the default layout rather than a throw.

export const DEFAULT_EXPLORER_WIDTH = 260;
export const DEFAULT_INSPECTOR_WIDTH = 360;
export const MIN_EXPLORER_WIDTH = 190;
export const MAX_EXPLORER_WIDTH = 480;
export const MIN_INSPECTOR_WIDTH = 280;
export const MAX_INSPECTOR_WIDTH = 560;
export const LAYOUT_WIDTH_STEP = 16;
export const MAX_LAYOUT_TABLE_NAME = 128;

export type StudioLayout = {
  explorerVisible: boolean;
  inspectorVisible: boolean;
  explorerWidth: number;
  inspectorWidth: number;
  selectedTable: string | null;
};

export const DEFAULT_STUDIO_LAYOUT: StudioLayout = {
  explorerVisible: true,
  inspectorVisible: true,
  explorerWidth: DEFAULT_EXPLORER_WIDTH,
  inspectorWidth: DEFAULT_INSPECTOR_WIDTH,
  selectedTable: null,
};

export function clampLayoutWidth(value: unknown, min: number, max: number, fallback: number): number {
  const n = typeof value === "number" ? value : typeof value === "string" ? Number(value) : Number.NaN;
  if (!Number.isFinite(n)) return fallback;
  return Math.min(max, Math.max(min, Math.round(n)));
}

export function stepLayoutWidth(value: number, delta: number, min: number, max: number): number {
  return clampLayoutWidth(value + delta, min, max, value);
}

export function serializeStudioLayout(layout: StudioLayout): string {
  const selected = typeof layout.selectedTable === "string" ? layout.selectedTable.trim() : "";
  return JSON.stringify({
    v: 1,
    explorerVisible: layout.explorerVisible !== false,
    inspectorVisible: layout.inspectorVisible !== false,
    explorerWidth: clampLayoutWidth(layout.explorerWidth, MIN_EXPLORER_WIDTH, MAX_EXPLORER_WIDTH, DEFAULT_EXPLORER_WIDTH),
    inspectorWidth: clampLayoutWidth(layout.inspectorWidth, MIN_INSPECTOR_WIDTH, MAX_INSPECTOR_WIDTH, DEFAULT_INSPECTOR_WIDTH),
    selectedTable: selected ? selected.slice(0, MAX_LAYOUT_TABLE_NAME) : null,
  });
}

export function parseStudioLayout(raw: string | null | undefined): StudioLayout {
  if (!raw) return { ...DEFAULT_STUDIO_LAYOUT };
  let parsed: unknown;
  try {
    parsed = JSON.parse(raw);
  } catch {
    return { ...DEFAULT_STUDIO_LAYOUT };
  }
  if (!parsed || typeof parsed !== "object") return { ...DEFAULT_STUDIO_LAYOUT };
  const rec = parsed as Record<string, unknown>;
  const selected = typeof rec.selectedTable === "string" ? rec.selectedTable.trim() : "";
  return {
    explorerVisible: rec.explorerVisible !== false,
    inspectorVisible: rec.inspectorVisible !== false,
    explorerWidth: clampLayoutWidth(rec.explorerWidth, MIN_EXPLORER_WIDTH, MAX_EXPLORER_WIDTH, DEFAULT_EXPLORER_WIDTH),
    inspectorWidth: clampLayoutWidth(rec.inspectorWidth, MIN_INSPECTOR_WIDTH, MAX_INSPECTOR_WIDTH, DEFAULT_INSPECTOR_WIDTH),
    selectedTable: selected ? selected.slice(0, MAX_LAYOUT_TABLE_NAME) : null,
  };
}

export function resetStudioLayout(selectedTable: string | null): StudioLayout {
  const name = typeof selectedTable === "string" ? selectedTable.trim() : "";
  return {
    ...DEFAULT_STUDIO_LAYOUT,
    selectedTable: name ? name.slice(0, MAX_LAYOUT_TABLE_NAME) : null,
  };
}

export function rankObjectMatches(
  query: string,
  objects: StudioObjectMatch[],
  limit: number = MAX_OBJECT_SEARCH_RESULTS,
): StudioObjectMatch[] {
  const q = query.trim().toLocaleLowerCase();
  if (!q) {
    return objects
      .slice()
      .sort((a, b) => a.name.localeCompare(b.name) || a.kind.localeCompare(b.kind))
      .slice(0, limit);
  }
  const scored: { obj: StudioObjectMatch; score: number }[] = [];
  for (const obj of objects) {
    const name = obj.name.toLocaleLowerCase();
    let score: number;
    if (name === q) score = 0;
    else if (name.startsWith(q)) score = 1;
    else if (name.includes(q)) score = 2;
    else if (isSubsequence(q, name)) score = 3;
    else continue;
    scored.push({ obj, score });
  }
  scored.sort(
    (a, b) =>
      a.score - b.score ||
      a.obj.name.length - b.obj.name.length ||
      a.obj.name.localeCompare(b.obj.name) ||
      a.obj.kind.localeCompare(b.obj.kind),
  );
  return scored.slice(0, limit).map((s) => s.obj);
}

// --- Query result summary line --------------------------------------
//
// The one-line "N rows · M ms" status under the editor. A statement with no
// result columns is a write or a DDL: reporting "0 rows" for it is
// misleading, so surface the affected count (or a plain completion) instead.
// Pure so it is unit-tested directly against every result shape.

export function queryResultSummary(
  result: { columns: string[]; rows: unknown[]; affected?: number; elapsed_ms: number; truncated?: boolean },
  ranSelection: boolean,
): string {
  const ms = `${(result.elapsed_ms ?? 0).toLocaleString()} ms`;
  const prefix = ranSelection ? "Selection · " : "";
  if (result.columns.length === 0) {
    const affected = typeof result.affected === "number" && result.affected > 0 ? result.affected : 0;
    const body = affected > 0 ? `${affected.toLocaleString()} ${affected === 1 ? "row" : "rows"} affected` : "Statement completed";
    return `${prefix}${body} · ${ms}`;
  }
  const rows = result.rows.length.toLocaleString();
  const cols = result.columns.length.toLocaleString();
  const limit = result.truncated ? " · server preview limit reached" : "";
  return `${prefix}${rows} ${result.rows.length === 1 ? "row" : "rows"} · ${cols} ${result.columns.length === 1 ? "column" : "columns"} · ${ms}${limit}`;
}

// --- Realm-scoped administration warning -----------------------------
//
// CREATE/DROP USER and CREATE/DROP ROLE change the realm-wide principal
// namespace. NextSQL identities are defined once per realm ("realm
// identities and roles with database-scoped grants",
// docs/design-multidatabase-dbaas.md §19), so such a statement reaches
// every database in the connected realm — not just the one this Studio
// session is pointed at. The confirm-before-run dialog shows this line so a
// cross-database change is never made silently. Pure; unit-tested.

export function realmScopeWarning(kind: string, realm: string, database: string): string {
  const realmLabel = realm.trim() ? `realm "${realm.trim()}"` : "the default realm";
  const dbLabel = database.trim() ? `the "${database.trim()}" database` : "the current database";
  return `${kind || "This statement"} changes a realm-wide user or role: it affects every database in ${realmLabel}, not just ${dbLabel} you are connected to.`;
}

// --- Global command palette ------------------------------------------
//
// A keyboard-driven launcher (Ctrl/Cmd+K) over Studio's own actions —
// running a query, opening an explorer, switching connection, and so on.
// rankCommandMatches is the pure matcher; the component owns the action
// callbacks and the modal. Unlike rankObjectMatches an empty query keeps
// the caller's curated order rather than sorting alphabetically, and a
// disabled command is filtered out entirely (there is nothing to run).

export const MAX_COMMAND_PALETTE_RESULTS = 40;

export type CommandMatchable = { id: string; label: string; keywords?: string; disabled?: boolean };

export function rankCommandMatches<T extends CommandMatchable>(query: string, commands: T[], limit: number = MAX_COMMAND_PALETTE_RESULTS): T[] {
  const enabled = commands.filter((c) => !c.disabled);
  const q = query.trim().toLocaleLowerCase();
  if (!q) return enabled.slice(0, limit);
  const scored: { cmd: T; score: number; index: number }[] = [];
  enabled.forEach((cmd, index) => {
    const haystacks = [cmd.label.toLocaleLowerCase(), (cmd.keywords ?? "").toLocaleLowerCase()].filter(Boolean);
    let best = Infinity;
    for (const h of haystacks) {
      let score: number;
      if (h === q) score = 0;
      else if (h.startsWith(q)) score = 1;
      else if (h.includes(q)) score = 2;
      else if (isSubsequence(q, h)) score = 3;
      else continue;
      if (score < best) best = score;
    }
    if (best !== Infinity) scored.push({ cmd, score: best, index });
  });
  scored.sort((a, b) => a.score - b.score || a.cmd.label.length - b.cmd.label.length || a.index - b.index);
  return scored.slice(0, limit).map((s) => s.cmd);
}

// --- Saved queries (folders via tags) ---------------------------------
//
// Named, tagged SQL snippets the operator explicitly chooses to keep,
// mirrored to localStorage per connection. Like crash recovery, this is a
// deliberate persistence of editor *text only* — it is never sent anywhere
// and carries the same "your browser only" scoping as the environment tag.
// A "folder" is just a tag; the UI groups by it. All helpers here are pure
// and bounded; the caller wraps storage access in try/catch.

export const MAX_SAVED_QUERIES = 200;
export const MAX_SAVED_QUERY_SQL_CHARS = 200_000;
export const MAX_SAVED_QUERY_NAME_CHARS = 200;
export const MAX_SAVED_QUERY_TAGS = 10;

export type SavedQuery = {
  id: string;
  name: string;
  sql: string;
  tags: string[];
  updatedAt: number;
};

function normalizeTags(input: unknown): string[] {
  const raw = Array.isArray(input)
    ? input
    : typeof input === "string"
      ? input.split(",")
      : [];
  const seen = new Set<string>();
  const out: string[] = [];
  for (const value of raw) {
    if (typeof value !== "string") continue;
    const tag = value.trim().slice(0, 60);
    if (!tag || seen.has(tag.toLocaleLowerCase())) continue;
    seen.add(tag.toLocaleLowerCase());
    out.push(tag);
    if (out.length >= MAX_SAVED_QUERY_TAGS) break;
  }
  return out.sort((a, b) => a.localeCompare(b));
}

export function serializeSavedQueries(list: SavedQuery[]): string {
  const bounded = list.slice(0, MAX_SAVED_QUERIES).map((entry) => ({
    id: String(entry.id),
    name: String(entry.name ?? "").slice(0, MAX_SAVED_QUERY_NAME_CHARS),
    sql: String(entry.sql ?? "").slice(0, MAX_SAVED_QUERY_SQL_CHARS),
    tags: normalizeTags(entry.tags),
    updatedAt: Number.isFinite(entry.updatedAt) ? entry.updatedAt : 0,
  }));
  return JSON.stringify({ v: 1, queries: bounded });
}

export function parseSavedQueries(raw: string | null | undefined): SavedQuery[] {
  if (!raw) return [];
  let parsed: unknown;
  try {
    parsed = JSON.parse(raw);
  } catch {
    return [];
  }
  const record = parsed && typeof parsed === "object" ? (parsed as { queries?: unknown }) : null;
  if (!record || !Array.isArray(record.queries)) return [];
  const out: SavedQuery[] = [];
  for (const entry of record.queries) {
    if (out.length >= MAX_SAVED_QUERIES) break;
    if (!entry || typeof entry !== "object") continue;
    const e = entry as { id?: unknown; name?: unknown; sql?: unknown; tags?: unknown; updatedAt?: unknown };
    if (typeof e.id !== "string" || typeof e.sql !== "string") continue;
    const name = typeof e.name === "string" && e.name.trim() ? e.name.trim() : "Untitled query";
    out.push({
      id: e.id,
      name: name.slice(0, MAX_SAVED_QUERY_NAME_CHARS),
      sql: e.sql.slice(0, MAX_SAVED_QUERY_SQL_CHARS),
      tags: normalizeTags(e.tags),
      updatedAt: Number.isFinite(e.updatedAt) ? (e.updatedAt as number) : 0,
    });
  }
  return out;
}

// upsertSavedQuery returns a new list with `entry` inserted or replaced (by
// id), most-recently-updated first, capped at MAX_SAVED_QUERIES.
export function upsertSavedQuery(list: SavedQuery[], entry: SavedQuery): SavedQuery[] {
  const clean: SavedQuery = {
    id: entry.id,
    name: (entry.name.trim() || "Untitled query").slice(0, MAX_SAVED_QUERY_NAME_CHARS),
    sql: entry.sql.slice(0, MAX_SAVED_QUERY_SQL_CHARS),
    tags: normalizeTags(entry.tags),
    updatedAt: entry.updatedAt,
  };
  const rest = list.filter((q) => q.id !== entry.id);
  return [clean, ...rest]
    .sort((a, b) => b.updatedAt - a.updatedAt)
    .slice(0, MAX_SAVED_QUERIES);
}

export function removeSavedQuery(list: SavedQuery[], id: string): SavedQuery[] {
  return list.filter((q) => q.id !== id);
}

export function savedQueryTags(list: SavedQuery[]): string[] {
  const seen = new Set<string>();
  for (const q of list) for (const t of q.tags) seen.add(t);
  return [...seen].sort((a, b) => a.localeCompare(b));
}

export function filterSavedQueries(
  list: SavedQuery[],
  opts: { text?: string; tag?: string } = {},
): SavedQuery[] {
  const text = (opts.text ?? "").trim().toLocaleLowerCase();
  const tag = opts.tag ?? "";
  return list.filter((q) => {
    if (tag && !q.tags.includes(tag)) return false;
    if (!text) return true;
    return (
      q.name.toLocaleLowerCase().includes(text) ||
      q.sql.toLocaleLowerCase().includes(text) ||
      q.tags.some((t) => t.toLocaleLowerCase().includes(text))
    );
  });
}

export function savedQueryStorageKey(realm: string, database: string, user: string): string {
  const seg = (s: string) => (s || "default").replace(/[^A-Za-z0-9_.-]/g, "_");
  return `nextsql-studio-saved:${seg(realm)}:${seg(database)}:${seg(user || "unknown")}`;
}

// --- Git-friendly export / import of the saved-query set ---------------
//
// The localStorage codec above optimizes for compactness and orders by
// recency, so it rewrites on every save. Export instead writes a stable
// document — entries ordered by name then id, two-space indent, trailing
// newline — so a set checked into a repo produces a readable diff. Import
// merges by id (a re-exported file keeps ids stable): a matching id is
// overwritten only when the incoming entry is at least as new, so importing
// an older copy never clobbers a local edit. All helpers here stay pure and
// bounded by the same MAX_SAVED_QUERY_* limits.

export const SAVED_QUERY_EXPORT_FORMAT = "nextsql-studio-saved-queries-v1";
export const MAX_SAVED_QUERY_IMPORT_BYTES = 48 << 20;

function boundedSavedQuery(entry: {
  id?: unknown;
  name?: unknown;
  sql?: unknown;
  tags?: unknown;
  updatedAt?: unknown;
}): SavedQuery | null {
  if (typeof entry.id !== "string" || !entry.id) return null;
  if (typeof entry.sql !== "string") return null;
  const name =
    typeof entry.name === "string" && entry.name.trim() ? entry.name.trim() : "Untitled query";
  return {
    id: entry.id.slice(0, MAX_SAVED_QUERY_NAME_CHARS),
    name: name.slice(0, MAX_SAVED_QUERY_NAME_CHARS),
    sql: entry.sql.slice(0, MAX_SAVED_QUERY_SQL_CHARS),
    tags: normalizeTags(entry.tags),
    updatedAt: Number.isFinite(entry.updatedAt) ? (entry.updatedAt as number) : 0,
  };
}

export function exportSavedQueries(list: SavedQuery[]): string {
  const bounded: SavedQuery[] = [];
  for (const entry of list.slice(0, MAX_SAVED_QUERIES)) {
    const clean = boundedSavedQuery(entry);
    if (clean) bounded.push(clean);
  }
  bounded.sort((a, b) => a.name.localeCompare(b.name) || a.id.localeCompare(b.id));
  return `${JSON.stringify({ format: SAVED_QUERY_EXPORT_FORMAT, queries: bounded }, null, 2)}\n`;
}

export function parseSavedQueriesExport(raw: string | null | undefined): SavedQuery[] {
  if (!raw) return [];
  let parsed: unknown;
  try {
    parsed = JSON.parse(raw);
  } catch {
    return [];
  }
  const entries = Array.isArray(parsed)
    ? parsed
    : parsed &&
        typeof parsed === "object" &&
        Array.isArray((parsed as { queries?: unknown }).queries)
      ? ((parsed as { queries: unknown[] }).queries)
      : null;
  if (!entries) return [];
  const out: SavedQuery[] = [];
  const seen = new Set<string>();
  for (const entry of entries) {
    if (out.length >= MAX_SAVED_QUERIES) break;
    if (!entry || typeof entry !== "object") continue;
    const clean = boundedSavedQuery(entry as Record<string, unknown>);
    if (!clean || seen.has(clean.id)) continue;
    seen.add(clean.id);
    out.push(clean);
  }
  return out;
}

export type SavedQueryMergeResult = {
  list: SavedQuery[];
  added: number;
  updated: number;
  unchanged: number;
};

// mergeSavedQueries upserts `incoming` into `current` by id. A matching id is
// replaced only when the incoming entry is at least as new AND its content
// actually differs; an equal-or-older duplicate counts as unchanged. The
// result is recency-ordered and capped like the live list.
export function mergeSavedQueries(
  current: SavedQuery[],
  incoming: SavedQuery[],
): SavedQueryMergeResult {
  const byId = new Map(current.map((q) => [q.id, q]));
  let added = 0;
  let updated = 0;
  let unchanged = 0;
  for (const entry of incoming) {
    const existing = byId.get(entry.id);
    if (!existing) {
      byId.set(entry.id, entry);
      added += 1;
      continue;
    }
    const differs =
      entry.sql !== existing.sql ||
      entry.name !== existing.name ||
      entry.tags.join(" ") !== existing.tags.join(" ");
    if (differs && entry.updatedAt >= existing.updatedAt) {
      byId.set(entry.id, entry);
      updated += 1;
    } else {
      unchanged += 1;
    }
  }
  const list = [...byId.values()]
    .sort((a, b) => b.updatedAt - a.updatedAt)
    .slice(0, MAX_SAVED_QUERIES);
  return { list, added, updated, unchanged };
}

export function savedQueriesExportFilename(now = new Date()): string {
  const stamp = Number.isNaN(now.getTime()) ? "export" : now.toISOString().slice(0, 10);
  return `nextsql-studio-saved-queries-${stamp}.json`;
}

export function downloadSavedQueries(list: SavedQuery[], now = new Date()): void {
  const url = URL.createObjectURL(
    new Blob([exportSavedQueries(list)], { type: "application/json;charset=utf-8" }),
  );
  const anchor = document.createElement("a");
  anchor.href = url;
  anchor.download = savedQueriesExportFilename(now);
  anchor.hidden = true;
  document.body.append(anchor);
  anchor.click();
  anchor.remove();
  setTimeout(() => URL.revokeObjectURL(url), 0);
}

// --- Data generator for development -------------------------------------------
//
// Builds one or more `INSERT INTO "t" (...) VALUES (...), ...` statements of
// synthetic rows from the authorized `system.columns` metadata the Studio
// session already read, then loads them into the editor for review. It never
// executes anything — the caller inserts the text into the active tab, where
// confirm-before-run and server-side RBAC apply exactly as to hand-typed SQL.
//
// Value generation is fully deterministic given (state, columns): a seeded
// mulberry32 PRNG is advanced once per generated cell in column order, so the
// same seed always produces byte-identical SQL (unit-tested). `NOW()` cells are
// emitted as the literal function call, so the generated *text* stays
// deterministic even though the value it produces at run time does not.

export const MAX_DATAGEN_ROWS = 1_000;
export const MAX_DATAGEN_SQL_BYTES = 512 * 1024;
export const DATAGEN_ROWS_PER_STATEMENT = 100;

// The lorem pool is fixed so generated text is reproducible.
const DATAGEN_LOREM = [
  "lorem", "ipsum", "dolor", "sit", "amet", "consectetur", "adipiscing", "elit",
  "sed", "eiusmod", "tempor", "incididunt", "labore", "magna", "aliqua", "enim",
];

// A 2025 UTC window used by the "random timestamp" strategy so the emitted
// literal is deterministic rather than relative to the wall clock.
const DATAGEN_TS_ANCHOR = Date.UTC(2025, 0, 1);
const DATAGEN_TS_SPAN_MS = 90 * 24 * 60 * 60 * 1000;

export type DataGenFieldKind =
  | "int" | "decimal" | "text" | "uuid" | "bool" | "timestamptz" | "json" | "unsupported";

export type DataGenStrategy =
  | "skip"
  | "sequence"
  | "random-int"
  | "random-decimal"
  | "lorem"
  | "label"
  | "uuid"
  | "bool-random"
  | "bool-alternate"
  | "now"
  | "random-timestamp"
  | "json-empty";

export type DataGenColumn = {
  name: string;
  type: string;
  kind: DataGenFieldKind;
  ordinal: number;
  notNull: boolean;
  isPrimary: boolean;
  hasDefault: boolean;
  supported: boolean;
  // Always ends with "skip". First entry is the default choice.
  strategies: DataGenStrategy[];
};

export type DataGenState = {
  table: string;
  rowCount: number;
  seed: number;
  // Chosen strategy per column name; a missing entry uses the column's first
  // offered strategy.
  strategies: Record<string, DataGenStrategy>;
};

export type DataGenBuildResult =
  | { sql: string; error: null; rows: number; statements: number }
  | { sql: null; error: string };

function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

export function dataGenFieldKind(type: string): DataGenFieldKind {
  const normalized = type.trim().toUpperCase();
  if (/^U?INT(?:8|16|32|64)$/.test(normalized)) return "int";
  if (/^(?:DECIMAL|NUMERIC)\b/.test(normalized)) return "decimal";
  if (normalized === "STRING" || normalized === "TEXT") return "text";
  if (normalized === "UUID") return "uuid";
  if (normalized === "BOOL" || normalized === "BOOLEAN") return "bool";
  if (normalized === "TIMESTAMPTZ") return "timestamptz";
  if (normalized === "JSON") return "json";
  return "unsupported";
}

function dataGenStrategiesFor(kind: DataGenFieldKind, isPrimary: boolean): DataGenStrategy[] {
  switch (kind) {
    case "int":
      return isPrimary
        ? ["sequence", "random-int", "skip"]
        : ["random-int", "sequence", "skip"];
    case "decimal":
      return ["random-decimal", "skip"];
    case "text":
      return isPrimary ? ["label", "lorem", "uuid", "skip"] : ["lorem", "label", "uuid", "skip"];
    case "uuid":
      return ["uuid", "skip"];
    case "bool":
      return ["bool-random", "bool-alternate", "skip"];
    case "timestamptz":
      return ["now", "random-timestamp", "skip"];
    case "json":
      return ["json-empty", "skip"];
    default:
      return ["skip"];
  }
}

// dataGenColumns projects the authorized system.columns rows in the table-detail
// bundle into the generator's column model, ordered by catalog ordinal.
export function dataGenColumns(detail: StudioTableDetail | null): DataGenColumn[] {
  if (!detail) return [];
  const nameIdx = resultColumn(detail.columns, "column_name");
  const ordinalIdx = resultColumn(detail.columns, "ordinal");
  const typeIdx = resultColumn(detail.columns, "type");
  const notNullIdx = resultColumn(detail.columns, "not_null");
  const primaryIdx = resultColumn(detail.columns, "is_primary");
  const defaultIdx = resultColumn(detail.columns, "default_value");
  if (nameIdx < 0 || typeIdx < 0) return [];
  const isTrue = (value: string | null | undefined) => value?.trim().toLowerCase() === "true";
  const columns: DataGenColumn[] = [];
  detail.columns.rows.forEach((row, index) => {
    const name = row[nameIdx];
    if (typeof name !== "string" || name.length === 0) return;
    const type = typeof row[typeIdx] === "string" ? (row[typeIdx] as string) : "";
    const kind = dataGenFieldKind(type);
    const isPrimary = primaryIdx >= 0 && isTrue(row[primaryIdx]);
    const rawDefault = defaultIdx >= 0 ? row[defaultIdx] : null;
    const parsedOrdinal = ordinalIdx >= 0 ? Number(row[ordinalIdx]) : NaN;
    columns.push({
      name,
      type: type || "(unknown)",
      kind,
      ordinal: Number.isFinite(parsedOrdinal) ? parsedOrdinal : index + 1,
      notNull: notNullIdx >= 0 && isTrue(row[notNullIdx]),
      isPrimary,
      hasDefault: typeof rawDefault === "string" && rawDefault.trim().length > 0,
      supported: kind !== "unsupported",
      strategies: dataGenStrategiesFor(kind, isPrimary),
    });
  });
  return columns.sort((a, b) => a.ordinal - b.ordinal);
}

function dataGenDefaultStrategy(column: DataGenColumn): DataGenStrategy {
  // Respect an existing column DEFAULT by leaving the column out unless the
  // operator opts in.
  if (column.hasDefault || !column.supported) return "skip";
  return column.strategies[0] ?? "skip";
}

export function dataGenEffectiveStrategy(state: DataGenState, column: DataGenColumn): DataGenStrategy {
  const chosen = state.strategies[column.name];
  if (chosen && column.strategies.includes(chosen)) return chosen;
  return dataGenDefaultStrategy(column);
}

function dataGenUUID(rnd: () => number): string {
  const hex = "0123456789abcdef";
  let out = "";
  for (let i = 0; i < 32; i++) {
    if (i === 8 || i === 12 || i === 16 || i === 20) out += "-";
    if (i === 12) { out += "4"; continue; }
    if (i === 16) { out += hex[(Math.floor(rnd() * 4) & 0x3) | 0x8]; continue; }
    out += hex[Math.floor(rnd() * 16)];
  }
  return out;
}

function dataGenDecimal(type: string, rnd: () => number): string {
  const match = /\(\s*(\d+)\s*(?:,\s*(\d+)\s*)?\)/.exec(type);
  const precision = match ? Math.max(1, Math.min(38, Number(match[1]))) : 10;
  const scale = match && match[2] != null ? Math.max(0, Math.min(precision, Number(match[2]))) : 0;
  const intDigits = Math.max(1, Math.min(9, precision - scale));
  const intPart = Math.floor(rnd() * Math.pow(10, intDigits));
  if (scale === 0) return String(intPart);
  let frac = "";
  for (let i = 0; i < scale; i++) frac += String(Math.floor(rnd() * 10));
  return `${intPart}.${frac}`;
}

function dataGenValue(
  column: DataGenColumn,
  strategy: DataGenStrategy,
  rowIndex: number,
  rnd: () => number,
): string {
  switch (strategy) {
    case "sequence":
      // Draw once even though the value is positional, so toggling a column
      // between "sequence" and "random-int" does not shift the PRNG stream.
      rnd();
      return String(rowIndex + 1);
    case "random-int":
      return String(Math.floor(rnd() * 1_000_000));
    case "random-decimal":
      return dataGenDecimal(column.type, rnd);
    case "lorem": {
      const count = 2 + Math.floor(rnd() * 4);
      const words: string[] = [];
      for (let i = 0; i < count; i++) words.push(DATAGEN_LOREM[Math.floor(rnd() * DATAGEN_LOREM.length)]);
      return quoteSQLString(words.join(" "));
    }
    case "label":
      rnd();
      return quoteSQLString(`${column.name}-${rowIndex + 1}`);
    case "uuid":
      return quoteSQLString(dataGenUUID(rnd));
    case "bool-random":
      return rnd() < 0.5 ? "TRUE" : "FALSE";
    case "bool-alternate":
      rnd();
      return rowIndex % 2 === 0 ? "TRUE" : "FALSE";
    case "now":
      rnd();
      return "NOW()";
    case "random-timestamp": {
      const offset = Math.floor(rnd() * DATAGEN_TS_SPAN_MS);
      return quoteSQLString(new Date(DATAGEN_TS_ANCHOR + offset).toISOString());
    }
    case "json-empty":
      rnd();
      return quoteSQLString("{}");
    default:
      rnd();
      return "NULL";
  }
}

export function buildDataGeneratorSQL(state: DataGenState, columns: DataGenColumn[]): DataGenBuildResult {
  const table = state.table.trim();
  if (!table) return { sql: null, error: "Select a table." };
  if (columns.length === 0) return { sql: null, error: "This table exposes no columns to populate." };
  if (!Number.isSafeInteger(state.rowCount) || state.rowCount < 1 || state.rowCount > MAX_DATAGEN_ROWS) {
    return { sql: null, error: `Row count must be a whole number from 1 to ${MAX_DATAGEN_ROWS.toLocaleString()}.` };
  }
  if (!Number.isSafeInteger(state.seed) || state.seed < 0) {
    return { sql: null, error: "Seed must be a whole number that is zero or greater." };
  }

  const included: DataGenColumn[] = [];
  for (const column of columns) {
    const strategy = dataGenEffectiveStrategy(state, column);
    if (strategy === "skip") {
      if (column.notNull && !column.hasDefault) {
        const why = column.supported
          ? `column "${column.name}" is NOT NULL and has no default — choose a fill strategy for it`
          : `column "${column.name}" has type ${column.type}, which the generator cannot produce, and it is NOT NULL with no default — add a DEFAULT or insert its rows by hand`;
        return { sql: null, error: `Cannot generate rows: ${why}.` };
      }
      continue;
    }
    included.push(column);
  }
  if (included.length === 0) {
    return { sql: null, error: "Choose a fill strategy for at least one column." };
  }

  const rnd = mulberry32(state.seed);
  const tuples: string[] = [];
  for (let i = 0; i < state.rowCount; i++) {
    const values = included.map((column) => dataGenValue(column, dataGenEffectiveStrategy(state, column), i, rnd));
    tuples.push(`(${values.join(", ")})`);
  }

  const columnList = included.map((column) => quoteIdentifier(column.name)).join(", ");
  const header = `INSERT INTO ${quoteIdentifier(table)} (${columnList}) VALUES`;
  const statements: string[] = [];
  for (let start = 0; start < tuples.length; start += DATAGEN_ROWS_PER_STATEMENT) {
    const chunk = tuples.slice(start, start + DATAGEN_ROWS_PER_STATEMENT);
    statements.push(`${header}\n  ${chunk.join(",\n  ")};`);
  }
  const sql = statements.join("\n\n");
  if (utf8ByteLength(sql) > MAX_DATAGEN_SQL_BYTES) {
    return {
      sql: null,
      error: `Generated SQL would exceed ${(MAX_DATAGEN_SQL_BYTES / 1024).toFixed(0)} KiB — reduce the row count or the number of populated columns.`,
    };
  }
  return { sql, error: null, rows: state.rowCount, statements: statements.length };
}

// ---------------------------------------------------------------------------
// CSV / JSON / NDJSON import — a bounded INSERT-script builder.
//
// Follows the data-generator precedent exactly: it produces reviewable SQL
// text loaded into the editor and never executes anything, and it reuses the
// authorized `system.columns` metadata already in the table-detail bundle
// (via `dataGenColumns` / `dataGenFieldKind`). It maps the fields of a pasted
// or loaded delimited/JSON document onto a target table's columns and emits
// batched `INSERT` statements. Every identifier and string value is quoted;
// integer and boolean cells are validated against the target column's kind
// rather than blindly quoted, so a malformed cell is a named error rather
// than a broken statement. Empty/missing values become `NULL` when the
// "treat empty as NULL" option is on (this cannot distinguish a JSON `null`
// from a JSON `""` — an accepted limitation for a development import tool).
// ---------------------------------------------------------------------------

export const MAX_IMPORT_INPUT_BYTES = 8 * 1024 * 1024;
export const MAX_IMPORT_ROWS = 2_000;
export const MAX_IMPORT_SQL_BYTES = 1024 * 1024;
export const IMPORT_ROWS_PER_STATEMENT = 100;

export type ImportFormat = "csv" | "csv-semicolon" | "tsv" | "json" | "ndjson";

export type ParsedImport = {
  fields: string[];
  rows: string[][];
  parsedRows: number; // data rows recognized before the MAX_IMPORT_ROWS cap
  truncated: boolean;
  error: string | null;
};

export type ImportBuildResult =
  | { sql: string; error: null; rows: number; statements: number; truncated: boolean }
  | { sql: null; error: string };

const IMPORT_DELIMITER: Record<"csv" | "csv-semicolon" | "tsv", string> = {
  csv: ",",
  "csv-semicolon": ";",
  tsv: "\t",
};

const IMPORT_BOOL_TRUE = new Set(["true", "t", "1", "yes", "y"]);
const IMPORT_BOOL_FALSE = new Set(["false", "f", "0", "no", "n"]);

function parseDelimited(text: string, delimiter: string): { records: string[][]; error: string | null } {
  const records: string[][] = [];
  let field = "";
  let record: string[] = [];
  let inQuotes = false;
  let fieldStart = true;
  const pushField = () => { record.push(field); field = ""; fieldStart = true; };
  const pushRecord = () => { pushField(); records.push(record); record = []; };
  for (let i = 0; i < text.length; i++) {
    const ch = text[i];
    if (inQuotes) {
      if (ch === '"') {
        if (text[i + 1] === '"') { field += '"'; i++; } else inQuotes = false;
      } else {
        field += ch;
      }
      continue;
    }
    // A double-quote is only an opening quote at the very start of a field
    // (RFC 4180); elsewhere it is a literal character.
    if (ch === '"' && fieldStart) { inQuotes = true; fieldStart = false; continue; }
    if (ch === delimiter) { pushField(); continue; }
    if (ch === "\r") { if (text[i + 1] === "\n") i++; pushRecord(); continue; }
    if (ch === "\n") { pushRecord(); continue; }
    field += ch;
    fieldStart = false;
  }
  if (inQuotes) return { records: [], error: "The document has an unterminated quoted field." };
  if (field.length > 0 || record.length > 0) pushRecord();
  return { records, error: null };
}

function importScalar(value: unknown): string {
  if (value === null || value === undefined) return "";
  if (typeof value === "string") return value;
  if (typeof value === "number" || typeof value === "boolean" || typeof value === "bigint") return String(value);
  return JSON.stringify(value);
}

export function parseImportText(text: string, format: ImportFormat): ParsedImport {
  const empty: ParsedImport = { fields: [], rows: [], parsedRows: 0, truncated: false, error: null };
  if (utf8ByteLength(text) > MAX_IMPORT_INPUT_BYTES) {
    return { ...empty, error: `The document is larger than ${(MAX_IMPORT_INPUT_BYTES / 1024 / 1024).toFixed(0)} MiB.` };
  }
  if (!text.trim()) return empty;

  if (format === "json" || format === "ndjson") {
    const objects: unknown[] = [];
    if (format === "json") {
      let parsed: unknown;
      try {
        parsed = JSON.parse(text);
      } catch (error) {
        return { ...empty, error: `The document is not valid JSON: ${(error as Error).message}` };
      }
      if (!Array.isArray(parsed)) return { ...empty, error: "The JSON document must be an array of row objects." };
      objects.push(...parsed);
    } else {
      const lines = text.split(/\r?\n/);
      for (let i = 0; i < lines.length; i++) {
        const line = lines[i].trim();
        if (!line) continue;
        try {
          objects.push(JSON.parse(line));
        } catch (error) {
          return { ...empty, error: `Line ${i + 1} is not valid JSON: ${(error as Error).message}` };
        }
      }
    }
    const fields: string[] = [];
    const seen = new Set<string>();
    for (const obj of objects) {
      if (!obj || typeof obj !== "object" || Array.isArray(obj)) {
        return { ...empty, error: "Every row must be a JSON object." };
      }
      for (const key of Object.keys(obj)) {
        if (!seen.has(key)) { seen.add(key); fields.push(key); }
      }
    }
    if (fields.length === 0) return { ...empty, error: "No fields were found in the document." };
    const all = objects.map((obj) => fields.map((f) => importScalar((obj as Record<string, unknown>)[f])));
    return {
      fields,
      rows: all.slice(0, MAX_IMPORT_ROWS),
      parsedRows: all.length,
      truncated: all.length > MAX_IMPORT_ROWS,
      error: null,
    };
  }

  const { records, error } = parseDelimited(text.replace(/^\uFEFF/, ""), IMPORT_DELIMITER[format]);
  if (error) return { ...empty, error };
  const nonEmpty = records.filter((r) => r.some((c) => c.trim() !== ""));
  if (nonEmpty.length < 2) {
    return { ...empty, error: "The document needs a header row and at least one data row." };
  }
  const fields = nonEmpty[0].map((f) => f.trim());
  if (fields.some((f) => f === "")) return { ...empty, error: "The header row has an empty column name." };
  if (new Set(fields).size !== fields.length) return { ...empty, error: "The header row has a duplicate column name." };
  const data = nonEmpty.slice(1).map((r) => fields.map((_, i) => r[i] ?? ""));
  return {
    fields,
    rows: data.slice(0, MAX_IMPORT_ROWS),
    parsedRows: data.length,
    truncated: data.length > MAX_IMPORT_ROWS,
    error: null,
  };
}

function importErrorSnippet(value: string): string {
  return value.length > 40 ? `${value.slice(0, 40)}…` : value;
}

function importValue(
  column: DataGenColumn,
  raw: string,
  emptyAsNull: boolean,
  rowNumber: number,
): { sql: string; error: null } | { sql: null; error: string } {
  if (raw === "" && emptyAsNull) {
    if (column.notNull && !column.hasDefault) {
      return { sql: null, error: `Row ${rowNumber}: column "${column.name}" is NOT NULL but the value is empty.` };
    }
    return { sql: "NULL", error: null };
  }
  const trimmed = raw.trim();
  switch (column.kind) {
    case "int":
      if (!/^[+-]?\d+$/.test(trimmed)) {
        return { sql: null, error: `Row ${rowNumber}: "${importErrorSnippet(raw)}" is not an integer for column "${column.name}".` };
      }
      return { sql: String(BigInt(trimmed)), error: null };
    case "decimal":
      if (!/^[+-]?(?:\d+(?:\.\d*)?|\.\d+)(?:[eE][+-]?\d+)?$/.test(trimmed)) {
        return { sql: null, error: `Row ${rowNumber}: "${importErrorSnippet(raw)}" is not a number for column "${column.name}".` };
      }
      return { sql: trimmed, error: null };
    case "bool": {
      const t = trimmed.toLowerCase();
      if (IMPORT_BOOL_TRUE.has(t)) return { sql: "TRUE", error: null };
      if (IMPORT_BOOL_FALSE.has(t)) return { sql: "FALSE", error: null };
      return { sql: null, error: `Row ${rowNumber}: "${importErrorSnippet(raw)}" is not a boolean for column "${column.name}".` };
    }
    case "json":
      try {
        JSON.parse(raw);
      } catch {
        return { sql: null, error: `Row ${rowNumber}: column "${column.name}" expects JSON but "${importErrorSnippet(raw)}" does not parse.` };
      }
      return { sql: quoteSQLString(raw), error: null };
    default:
      return { sql: quoteSQLString(raw), error: null };
  }
}

export function buildImportInsertSQL(opts: {
  table: string;
  columns: DataGenColumn[];
  mapping: Record<string, string>;
  parsed: ParsedImport;
  emptyAsNull: boolean;
}): ImportBuildResult {
  const table = opts.table.trim();
  if (!table) return { sql: null, error: "Select a target table." };
  if (opts.parsed.error) return { sql: null, error: opts.parsed.error };
  if (opts.parsed.rows.length === 0) return { sql: null, error: "Load a document with a header row and at least one data row." };

  const byName = new Map(opts.columns.map((c) => [c.name, c]));
  const targets: { column: DataGenColumn; field: string }[] = [];
  const claimed = new Set<string>();
  for (const field of opts.parsed.fields) {
    const targetName = opts.mapping[field];
    if (!targetName) continue;
    const column = byName.get(targetName);
    if (!column) return { sql: null, error: `Field "${field}" is mapped to "${targetName}", which is not a column of ${table}.` };
    if (claimed.has(targetName)) return { sql: null, error: `Two fields are mapped to the column "${targetName}".` };
    if (!column.supported) {
      return { sql: null, error: `Column "${targetName}" has type ${column.type}, which cannot be imported from a text value.` };
    }
    claimed.add(targetName);
    targets.push({ column, field });
  }
  if (targets.length === 0) return { sql: null, error: "Map at least one field to a target column." };
  targets.sort((a, b) => a.column.ordinal - b.column.ordinal);

  for (const column of opts.columns) {
    if (column.notNull && !column.hasDefault && !claimed.has(column.name)) {
      return { sql: null, error: `Column "${column.name}" is NOT NULL and has no default — map a field to it.` };
    }
  }

  const fieldIndex = new Map(opts.parsed.fields.map((f, i) => [f, i]));
  const tuples: string[] = [];
  for (let r = 0; r < opts.parsed.rows.length; r++) {
    const row = opts.parsed.rows[r];
    const values: string[] = [];
    for (const { column, field } of targets) {
      const raw = row[fieldIndex.get(field) ?? -1] ?? "";
      const converted = importValue(column, raw, opts.emptyAsNull, r + 1);
      if (converted.sql === null) return { sql: null, error: converted.error };
      values.push(converted.sql);
    }
    tuples.push(`(${values.join(", ")})`);
  }

  const columnList = targets.map((t) => quoteIdentifier(t.column.name)).join(", ");
  const header = `INSERT INTO ${quoteIdentifier(table)} (${columnList}) VALUES`;
  const statements: string[] = [];
  for (let start = 0; start < tuples.length; start += IMPORT_ROWS_PER_STATEMENT) {
    statements.push(`${header}\n  ${tuples.slice(start, start + IMPORT_ROWS_PER_STATEMENT).join(",\n  ")};`);
  }
  const sql = statements.join("\n\n");
  if (utf8ByteLength(sql) > MAX_IMPORT_SQL_BYTES) {
    return {
      sql: null,
      error: `The generated SQL would exceed ${(MAX_IMPORT_SQL_BYTES / 1024).toFixed(0)} KiB — import fewer rows or columns at a time.`,
    };
  }
  return { sql, error: null, rows: opts.parsed.rows.length, statements: statements.length, truncated: opts.parsed.truncated };
}

// autoImportMapping pairs each document field with a target column by
// exact case-insensitive name match, but only to columns the generator can
// represent (`supported`). A field with no match maps to "" (skip).
export function autoImportMapping(fields: string[], columns: DataGenColumn[]): Record<string, string> {
  const supported = new Map(columns.filter((c) => c.supported).map((c) => [c.name.toLowerCase(), c.name]));
  const mapping: Record<string, string> = {};
  for (const field of fields) mapping[field] = supported.get(field.toLowerCase()) ?? "";
  return mapping;
}

// ---------------------------------------------------------------------------
// Vector dataset import — a bounded INSERT-script builder for embedding data.
//
// The CSV / JSON / NDJSON importer above deliberately refuses VECTOR /
// BITVECTOR / SPARSEVECTOR columns ("cannot be imported from a text value"):
// a generic text cell is not a vector. This dedicated path fills that gap for
// a development embedding dataset. It maps exactly one document field onto a
// table's vector column — that cell parsed as a bracketed `[…]`, parenthesized
// `(…)`, or bare comma list and validated against the column's declared
// dimensions (and, for a BITVECTOR, its 0/1 domain) by the same
// `parseVectorLiteralInput` grammar the Vector Explorer uses — and optionally
// maps other fields onto the table's scalar columns, reusing the generic
// importer's per-kind cell validation (`importValue`). It emits batched
// INSERT statements with the exact native parenthesized vector literal
// (`docs/vector.md`: `INSERT INTO docs (sig) VALUES ((1, 0, …));`). Same
// never-execute boundary and no server route — it reuses
// `GET /api/v1/studio/table`. A SPARSEVECTOR column takes the same dense
// parenthesized form; the server coerces the zeros away.
// ---------------------------------------------------------------------------

export const MAX_VECTOR_IMPORT_ROWS = 1_000;
// Rows × declared-dimensions ceiling. An embedding row carries far more values
// than a scalar CSV row, so this caps the emitted literal payload with an
// actionable message before the generic 1 MiB SQL ceiling
// (`MAX_IMPORT_SQL_BYTES`) would cut it off less helpfully.
export const MAX_VECTOR_IMPORT_VALUES = 262_144;

export type VectorImportBuildResult =
  | { sql: string; error: null; rows: number; statements: number; truncated: boolean }
  | { sql: null; error: string };

// autoVectorImportField picks the document field that most likely holds the
// embedding: an exact case-insensitive match to the vector column name, then
// the first field a scalar auto-mapping did not already claim, then the first
// field. Never a guess that silently overrides an operator's later choice —
// the modal always shows the resolved field in an editable Select.
export function autoVectorImportField(
  fields: string[],
  vectorColumnName: string,
  scalarMapping: Record<string, string>,
): string {
  if (fields.length === 0) return "";
  const exact = fields.find((f) => f.toLowerCase() === vectorColumnName.toLowerCase());
  if (exact) return exact;
  const unclaimed = fields.find((f) => !scalarMapping[f]);
  return unclaimed ?? fields[0];
}

export function buildVectorImportSQL(opts: {
  table: string;
  columns: DataGenColumn[]; // full dataGenColumns(detail): vector column carries its ordinal
  vectorColumn: VectorCatalogColumn | null;
  vectorField: string; // document field mapped to the vector column
  scalarMapping: Record<string, string>; // other field -> scalar column name ("" = skip)
  parsed: ParsedImport;
  emptyAsNull: boolean;
}): VectorImportBuildResult {
  const table = opts.table.trim();
  if (!table) return { sql: null, error: "Select a target table." };
  if (!opts.vectorColumn) {
    return { sql: null, error: "Select the table's VECTOR, BITVECTOR, or SPARSEVECTOR column." };
  }
  const vectorColumn = opts.vectorColumn;
  if (opts.parsed.error) return { sql: null, error: opts.parsed.error };
  if (opts.parsed.rows.length === 0) {
    return { sql: null, error: "Load a document with a header row and at least one data row." };
  }

  const fieldIndex = new Map(opts.parsed.fields.map((f, i) => [f, i]));
  if (!opts.vectorField || !fieldIndex.has(opts.vectorField)) {
    return { sql: null, error: "Map a document field to the vector column." };
  }

  const vectorMeta = opts.columns.find((c) => c.name === vectorColumn.name);
  const vectorOrdinal = vectorMeta ? vectorMeta.ordinal : Number.MAX_SAFE_INTEGER;
  const vectorNotNull = vectorMeta ? vectorMeta.notNull : false;
  const vectorHasDefault = vectorMeta ? vectorMeta.hasDefault : false;

  // Resolve the scalar targets with the generic importer's own rules.
  const byName = new Map(opts.columns.map((c) => [c.name, c]));
  const scalarTargets: { column: DataGenColumn; field: string }[] = [];
  const claimed = new Set<string>([vectorColumn.name]);
  for (const field of opts.parsed.fields) {
    if (field === opts.vectorField) continue;
    const targetName = opts.scalarMapping[field];
    if (!targetName) continue;
    if (targetName === vectorColumn.name) {
      return { sql: null, error: `Column "${targetName}" is the vector column — only the vector field maps to it.` };
    }
    const column = byName.get(targetName);
    if (!column) return { sql: null, error: `Field "${field}" is mapped to "${targetName}", which is not a column of ${table}.` };
    if (claimed.has(targetName)) return { sql: null, error: `Two fields are mapped to the column "${targetName}".` };
    if (!column.supported) {
      return { sql: null, error: `Column "${targetName}" has type ${column.type}, which cannot be imported from a text value.` };
    }
    claimed.add(targetName);
    scalarTargets.push({ column, field });
  }

  for (const column of opts.columns) {
    if (column === vectorMeta) continue;
    if (column.notNull && !column.hasDefault && !claimed.has(column.name)) {
      return { sql: null, error: `Column "${column.name}" is NOT NULL and has no default — map a field to it.` };
    }
  }

  const rows = opts.parsed.rows.slice(0, MAX_VECTOR_IMPORT_ROWS);
  const truncated = opts.parsed.truncated || opts.parsed.rows.length > MAX_VECTOR_IMPORT_ROWS;

  const dims = vectorColumn.dimensions;
  if (dims != null && dims * rows.length > MAX_VECTOR_IMPORT_VALUES) {
    return {
      sql: null,
      error: `${rows.length.toLocaleString()} rows × ${dims} dimensions exceeds the ${MAX_VECTOR_IMPORT_VALUES.toLocaleString()}-value import ceiling — import fewer rows at a time.`,
    };
  }

  const columnOrder = [
    { name: vectorColumn.name, ordinal: vectorOrdinal, vector: true as const },
    ...scalarTargets.map((t) => ({ name: t.column.name, ordinal: t.column.ordinal, vector: false as const })),
  ].sort((a, b) => a.ordinal - b.ordinal);

  const scalarByName = new Map(scalarTargets.map((t) => [t.column.name, t]));
  const vectorRawIndex = fieldIndex.get(opts.vectorField)!;

  const tuples: string[] = [];
  for (let r = 0; r < rows.length; r++) {
    const row = rows[r];
    const values: string[] = [];
    for (const entry of columnOrder) {
      if (entry.vector) {
        const raw = row[vectorRawIndex] ?? "";
        if (raw.trim() === "" && opts.emptyAsNull) {
          if (vectorNotNull && !vectorHasDefault) {
            return { sql: null, error: `Row ${r + 1}: column "${vectorColumn.name}" is NOT NULL but the value is empty.` };
          }
          values.push("NULL");
          continue;
        }
        const parsedVector = parseVectorLiteralInput(raw, vectorColumn);
        if (parsedVector.values === null) {
          return { sql: null, error: `Row ${r + 1}, column "${vectorColumn.name}": ${parsedVector.error}` };
        }
        values.push(buildVectorLiteral(parsedVector.values));
        continue;
      }
      const target = scalarByName.get(entry.name)!;
      const raw = row[fieldIndex.get(target.field) ?? -1] ?? "";
      const converted = importValue(target.column, raw, opts.emptyAsNull, r + 1);
      if (converted.sql === null) return { sql: null, error: converted.error };
      values.push(converted.sql);
    }
    tuples.push(`(${values.join(", ")})`);
  }

  const columnList = columnOrder.map((c) => quoteIdentifier(c.name)).join(", ");
  const header = `INSERT INTO ${quoteIdentifier(table)} (${columnList}) VALUES`;
  const statements: string[] = [];
  for (let start = 0; start < tuples.length; start += IMPORT_ROWS_PER_STATEMENT) {
    statements.push(`${header}\n  ${tuples.slice(start, start + IMPORT_ROWS_PER_STATEMENT).join(",\n  ")};`);
  }
  const sql = statements.join("\n\n");
  if (utf8ByteLength(sql) > MAX_IMPORT_SQL_BYTES) {
    return {
      sql: null,
      error: `The generated SQL would exceed ${(MAX_IMPORT_SQL_BYTES / 1024).toFixed(0)} KiB — import fewer rows at a time.`,
    };
  }
  return { sql, error: null, rows: rows.length, statements: statements.length, truncated };
}

// ---------------------------------------------------------------------------
// Parameterized INSERT / UPDATE / DELETE generation.
//
// Builds a positional-parameter ($1..$N) DML *template* for a target table
// from the authorized system.columns metadata already in the table-detail
// bundle (reusing `dataGenColumns`), loaded into the editor for review — the
// same never-execute boundary as the data generator and CSV/JSON import: no
// server route (it reuses `GET /api/v1/studio/table`), and the emitted text
// faces confirm-before-run and server-side RBAC like any hand-typed
// statement. The $1..$N placeholders it emits are bound by the editor's
// existing positional Parameters panel (`extractQueryParams`, log #186)
// before the operator runs the statement — unlike the data generator and
// import, no literal row values are ever produced here.
//
// Every column type is a valid target: this emits placeholders, not values,
// so `VECTOR` / geo / collection columns that the value builders reject are
// fine here.
// ---------------------------------------------------------------------------

export type DMLKind = "insert" | "update" | "delete";

export type DMLBuildState = {
  table: string;
  kind: DMLKind;
  // INSERT column list / UPDATE SET list. Order is normalized to catalog
  // ordinal; a missing/empty selection is an error.
  setColumns: string[];
  // UPDATE / DELETE WHERE key predicate (`col = $n AND …`). Ignored for
  // INSERT; required for UPDATE/DELETE so the statement targets specific rows.
  whereColumns: string[];
};

export type DMLBuildResult =
  | { sql: string; error: null; params: number }
  | { sql: null; error: string };

function resolveDMLColumns(
  names: string[],
  byName: Map<string, DataGenColumn>,
  table: string,
  role: string,
): DataGenColumn[] | string {
  const seen = new Set<string>();
  const out: DataGenColumn[] = [];
  for (const name of names) {
    const column = byName.get(name);
    if (!column) return `${role} column "${name}" is not a column of ${table}.`;
    if (seen.has(name)) return `${role} column "${name}" is listed twice.`;
    seen.add(name);
    out.push(column);
  }
  return out.sort((a, b) => a.ordinal - b.ordinal);
}

export function buildParameterizedDML(state: DMLBuildState, columns: DataGenColumn[]): DMLBuildResult {
  const table = state.table.trim();
  if (!table) return { sql: null, error: "Select a table." };
  if (columns.length === 0) return { sql: null, error: "This table exposes no columns." };

  const byName = new Map(columns.map((column) => [column.name, column]));
  let param = 0;
  const next = () => `$${(param += 1)}`;
  const done = (sql: string): DMLBuildResult => {
    if (param > MAX_QUERY_PARAMS) {
      return {
        sql: null,
        error: `That template needs ${param} parameters — the editor binds at most ${MAX_QUERY_PARAMS}. Use fewer columns.`,
      };
    }
    return { sql, error: null, params: param };
  };

  if (state.kind === "insert") {
    const set = resolveDMLColumns(state.setColumns, byName, table, "Insert");
    if (typeof set === "string") return { sql: null, error: set };
    if (set.length === 0) return { sql: null, error: "Choose at least one column to insert." };
    for (const column of columns) {
      if (column.notNull && !column.hasDefault && !set.includes(column)) {
        return { sql: null, error: `Column "${column.name}" is NOT NULL and has no default — include it.` };
      }
    }
    const names = set.map((column) => quoteIdentifier(column.name)).join(", ");
    const holders = set.map(() => next()).join(", ");
    return done(`INSERT INTO ${quoteIdentifier(table)} (${names})\n  VALUES (${holders});`);
  }

  const where = resolveDMLColumns(state.whereColumns, byName, table, "Filter");
  if (typeof where === "string") return { sql: null, error: where };
  if (where.length === 0) {
    return {
      sql: null,
      error: "Choose at least one column for the WHERE clause so the statement targets specific rows.",
    };
  }

  if (state.kind === "update") {
    const set = resolveDMLColumns(state.setColumns, byName, table, "Set");
    if (typeof set === "string") return { sql: null, error: set };
    if (set.length === 0) return { sql: null, error: "Choose at least one column to update." };
    const overlap = set.find((column) => where.includes(column));
    if (overlap) {
      return { sql: null, error: `Column "${overlap.name}" is in both SET and WHERE — remove it from one.` };
    }
    const assignments = set.map((column) => `${quoteIdentifier(column.name)} = ${next()}`);
    const predicate = where.map((column) => `${quoteIdentifier(column.name)} = ${next()}`);
    return done(
      `UPDATE ${quoteIdentifier(table)}\n` +
        `  SET ${assignments.join(",\n      ")}\n` +
        `  WHERE ${predicate.join("\n    AND ")};`,
    );
  }

  const predicate = where.map((column) => `${quoteIdentifier(column.name)} = ${next()}`);
  return done(`DELETE FROM ${quoteIdentifier(table)}\n  WHERE ${predicate.join("\n    AND ")};`);
}

// dmlDefaultColumns picks a sensible starting selection for a DML kind:
// INSERT includes every column, UPDATE sets the non-primary-key columns and
// keys on the primary key, DELETE keys on the primary key. Returns catalog
// order. When a table has no primary key the WHERE selection is empty and
// the operator must choose the key columns explicitly.
export function dmlDefaultColumns(
  kind: DMLKind,
  columns: DataGenColumn[],
): { setColumns: string[]; whereColumns: string[] } {
  const ordered = [...columns].sort((a, b) => a.ordinal - b.ordinal);
  const primary = ordered.filter((column) => column.isPrimary).map((column) => column.name);
  if (kind === "insert") {
    return { setColumns: ordered.map((column) => column.name), whereColumns: [] };
  }
  if (kind === "update") {
    return {
      setColumns: ordered.filter((column) => !column.isPrimary).map((column) => column.name),
      whereColumns: primary,
    };
  }
  return { setColumns: [], whereColumns: primary };
}

// ---------------------------------------------------------------------------
// Table / index designer.
//
// Builds a CREATE TABLE or CREATE INDEX statement *template* from a form
// state into the editor for review — the same never-execute boundary as the
// DML builder / data generator / import: no server route, and the emitted
// text faces confirm-before-run and server-side RBAC like any hand-typed
// statement. Types come from a closed NextSQL kind list assembled here
// (never interpolating free-text type SQL). Identifiers are always quoted.
// PRIMARY KEY is required (catalog.TableFromAST). nsql_ table names are
// rejected (catalog.ReservedName). Collections / ENUM / GEOMETRY subtypes
// stay out of this form — they need nested-type UI the designer does not
// pretend to have.
// ---------------------------------------------------------------------------

export const MAX_DESIGNER_COLUMNS = 64;
export const MAX_DESIGNER_INDEX_COLUMNS = 16;
export const MAX_FULLTEXT_INDEX_COLUMNS = 8;
export const MAX_DESIGNER_IDENT_CHARS = 128;
export const MAX_DESIGNER_CHAR_LEN = 65535;
export const MAX_DESIGNER_VECTOR_DIM = 8192;
export const MAX_DESIGNER_DECIMAL_PRECISION = 38;
export const MAX_DESIGNER_DEFAULT_CHARS = 256;
export const MAX_DESIGNER_JSON_PATH_SEGMENTS = 8;

export type DesignerTypeKind =
  | "INT8" | "INT16" | "INT32" | "INT64"
  | "UINT8" | "UINT16" | "UINT32" | "UINT64"
  | "DECIMAL" | "STRING" | "TEXT" | "CHAR" | "VARCHAR" | "BOOL" | "UUID" | "BLOB"
  | "DATE" | "TIME" | "TIMESTAMP" | "TIMESTAMPTZ" | "INTERVAL"
  | "FLOAT32" | "FLOAT64" | "JSON"
  | "VECTOR_F32" | "VECTOR_F16" | "VECTOR_I8" | "BITVECTOR" | "SPARSEVECTOR"
  | "POINT" | "BOX" | "LINESTRING" | "POLYGON"
  | "GEOMETRY" | "GEOGRAPHY";

export const DESIGNER_TYPE_KINDS: DesignerTypeKind[] = [
  "INT64", "INT32", "INT16", "INT8",
  "UINT64", "UINT32", "UINT16", "UINT8",
  "DECIMAL", "STRING", "TEXT", "CHAR", "VARCHAR", "BOOL", "UUID", "BLOB",
  "DATE", "TIME", "TIMESTAMP", "TIMESTAMPTZ", "INTERVAL",
  "FLOAT32", "FLOAT64", "JSON",
  "VECTOR_F32", "VECTOR_F16", "VECTOR_I8", "BITVECTOR", "SPARSEVECTOR",
  "POINT", "BOX", "LINESTRING", "POLYGON",
  "GEOMETRY", "GEOGRAPHY",
];

export const DESIGNER_TYPE_LABELS: Record<DesignerTypeKind, string> = {
  INT8: "INT8",
  INT16: "INT16",
  INT32: "INT32",
  INT64: "INT64",
  UINT8: "UINT8",
  UINT16: "UINT16",
  UINT32: "UINT32",
  UINT64: "UINT64",
  DECIMAL: "DECIMAL(p,s)",
  STRING: "STRING",
  TEXT: "TEXT",
  CHAR: "CHAR(n)",
  VARCHAR: "VARCHAR(n)",
  BOOL: "BOOL",
  UUID: "UUID",
  BLOB: "BLOB",
  DATE: "DATE",
  TIME: "TIME",
  TIMESTAMP: "TIMESTAMP",
  TIMESTAMPTZ: "TIMESTAMPTZ",
  INTERVAL: "INTERVAL",
  FLOAT32: "FLOAT32",
  FLOAT64: "FLOAT64",
  JSON: "JSON",
  VECTOR_F32: "VECTOR<F32,N>",
  VECTOR_F16: "VECTOR<F16,N>",
  VECTOR_I8: "VECTOR<I8,N>",
  BITVECTOR: "BITVECTOR<N>",
  SPARSEVECTOR: "SPARSEVECTOR<N>",
  POINT: "POINT",
  BOX: "BOX",
  LINESTRING: "LINESTRING",
  POLYGON: "POLYGON",
  GEOMETRY: "GEOMETRY",
  GEOGRAPHY: "GEOGRAPHY",
};

export type DesignerDefaultKind = "none" | "uuid" | "now" | "ai" | "literal";
export type DesignerFKAction = "RESTRICT" | "CASCADE" | "SET NULL" | "SET DEFAULT";
export type DesignerIndexKind = "btree" | "unique" | "fulltext" | "vector" | "spatial";
export type DesignerVectorMethod = "HNSW" | "IVF" | "IVFPQ" | "SPARSE";
export type DesignerVectorQuant = "NONE" | "F16" | "I8";
export type DesignerFTAnalyzer = "simple" | "english" | "french" | "german" | "spanish";

export type DesignerColumn = {
  id: string;
  name: string;
  typeKind: DesignerTypeKind;
  // CHAR/VARCHAR rune length; DECIMAL precision; VECTOR/BITVECTOR/SPARSEVECTOR dimension.
  typeParam: number;
  // DECIMAL scale only.
  typeScale: number;
  notNull: boolean;
  primaryKey: boolean;
  defaultKind: DesignerDefaultKind;
  defaultLiteral: string;
};

export type CreateTableState = {
  table: string;
  columns: DesignerColumn[];
  fkEnabled: boolean;
  fkColumns: string[];
  fkRefTable: string;
  fkRefColumns: string[];
  fkOnDelete: DesignerFKAction;
  fkOnUpdate: DesignerFKAction;
};

export type CreateIndexState = {
  name: string;
  table: string;
  kind: DesignerIndexKind;
  columns: string[];
  include: string[];
  jsonPath: string;
  analyzer: DesignerFTAnalyzer;
  vectorMethod: DesignerVectorMethod;
  vectorQuant: DesignerVectorQuant;
  ivfLists: number;
  ivfProbes: number;
  ivfSubspaces: number;
};

export type DesignerBuildResult =
  | { sql: string; error: null }
  | { sql: null; error: string };

export function newDesignerColumn(id: string, overrides?: Partial<DesignerColumn>): DesignerColumn {
  const typeKind = overrides?.typeKind ?? "STRING";
  return {
    id,
    name: "",
    notNull: false,
    primaryKey: false,
    defaultKind: "none",
    defaultLiteral: "",
    ...overrides,
    typeKind,
    typeParam: overrides?.typeParam ?? designerTypeParamDefault(typeKind),
    typeScale: overrides?.typeScale ?? 0,
  };
}

export function defaultCreateTableState(): CreateTableState {
  return {
    table: "new_table",
    columns: [
      newDesignerColumn("c1", {
        name: "id",
        typeKind: "UUID",
        notNull: true,
        primaryKey: true,
        defaultKind: "uuid",
      }),
      newDesignerColumn("c2", {
        name: "name",
        typeKind: "STRING",
        notNull: true,
      }),
    ],
    fkEnabled: false,
    fkColumns: [],
    fkRefTable: "",
    fkRefColumns: [],
    fkOnDelete: "RESTRICT",
    fkOnUpdate: "RESTRICT",
  };
}

export function defaultCreateIndexState(table = ""): CreateIndexState {
  return {
    name: "",
    table,
    kind: "btree",
    columns: [],
    include: [],
    jsonPath: "",
    analyzer: "simple",
    vectorMethod: "HNSW",
    vectorQuant: "NONE",
    ivfLists: 16,
    ivfProbes: 0,
    ivfSubspaces: 8,
  };
}

export function designerTypeParamDefault(kind: DesignerTypeKind): number {
  switch (kind) {
    case "CHAR":
      return 1;
    case "VARCHAR":
      return 255;
    case "DECIMAL":
      return 18;
    case "VECTOR_F32":
    case "VECTOR_F16":
    case "VECTOR_I8":
    case "BITVECTOR":
    case "SPARSEVECTOR":
      return 8;
    default:
      return 0;
  }
}

export function designerTypeNeedsLength(kind: DesignerTypeKind): boolean {
  return kind === "CHAR" || kind === "VARCHAR";
}

export function designerTypeNeedsDecimal(kind: DesignerTypeKind): boolean {
  return kind === "DECIMAL";
}

export function designerTypeNeedsDimension(kind: DesignerTypeKind): boolean {
  return (
    kind === "VECTOR_F32" ||
    kind === "VECTOR_F16" ||
    kind === "VECTOR_I8" ||
    kind === "BITVECTOR" ||
    kind === "SPARSEVECTOR"
  );
}

function designerIdentError(name: string, role: string, opts?: { reservedTable?: boolean }): string | null {
  const trimmed = name.trim();
  if (!trimmed) return `${role} is required.`;
  if (trimmed.length > MAX_DESIGNER_IDENT_CHARS) {
    return `${role} is longer than ${MAX_DESIGNER_IDENT_CHARS} characters.`;
  }
  if (/[\u0000-\u001f]/.test(trimmed)) return `${role} contains a control character.`;
  if (opts?.reservedTable && trimmed.toLowerCase().startsWith("nsql_")) {
    return `${role} cannot use the reserved nsql_ prefix.`;
  }
  return null;
}

export function designerColumnTypeSQL(column: DesignerColumn): DesignerBuildResult {
  const kind = column.typeKind;
  const param = Number.isFinite(column.typeParam) ? Math.trunc(column.typeParam) : NaN;
  const scale = Number.isFinite(column.typeScale) ? Math.trunc(column.typeScale) : NaN;
  switch (kind) {
    case "CHAR":
    case "VARCHAR":
      if (!Number.isInteger(param) || param < 1 || param > MAX_DESIGNER_CHAR_LEN) {
        return { sql: null, error: `${kind} length must be an integer from 1 to ${MAX_DESIGNER_CHAR_LEN}.` };
      }
      return { sql: `${kind}(${param})`, error: null };
    case "DECIMAL":
      if (!Number.isInteger(param) || param < 1 || param > MAX_DESIGNER_DECIMAL_PRECISION) {
        return { sql: null, error: `DECIMAL precision must be an integer from 1 to ${MAX_DESIGNER_DECIMAL_PRECISION}.` };
      }
      if (!Number.isInteger(scale) || scale < 0 || scale > param) {
        return { sql: null, error: "DECIMAL scale must be an integer from 0 to the precision." };
      }
      return { sql: `DECIMAL(${param},${scale})`, error: null };
    case "VECTOR_F32":
    case "VECTOR_F16":
    case "VECTOR_I8":
    case "BITVECTOR": {
      if (!Number.isInteger(param) || param < 1 || param > MAX_DESIGNER_VECTOR_DIM) {
        return { sql: null, error: `Vector dimension must be an integer from 1 to ${MAX_DESIGNER_VECTOR_DIM}.` };
      }
      const elem = kind === "VECTOR_F32" ? "F32" : kind === "VECTOR_F16" ? "F16" : kind === "VECTOR_I8" ? "I8" : null;
      return { sql: elem ? `VECTOR<${elem},${param}>` : `BITVECTOR<${param}>`, error: null };
    }
    case "SPARSEVECTOR":
      if (!Number.isInteger(param) || param < 1 || param > 65535) {
        return { sql: null, error: "SPARSEVECTOR dimension must be an integer from 1 to 65535." };
      }
      return { sql: `SPARSEVECTOR<${param}>`, error: null };
    default:
      return { sql: kind, error: null };
  }
}

function designerDefaultSQL(column: DesignerColumn, typeSQL: string): DesignerBuildResult {
  switch (column.defaultKind) {
    case "none":
      return { sql: "", error: null };
    case "uuid":
      return { sql: " DEFAULT UUID()", error: null };
    case "now":
      return { sql: " DEFAULT NOW()", error: null };
    case "ai":
      if (!typeSQL.startsWith("DECIMAL")) {
        return { sql: null, error: `DEFAULT AI() is only valid on a DECIMAL column, not ${typeSQL}.` };
      }
      return { sql: " DEFAULT AI()", error: null };
    case "literal": {
      const raw = column.defaultLiteral.trim();
      if (!raw) return { sql: null, error: `Column "${column.name.trim() || column.id}" needs a DEFAULT literal.` };
      if (raw.length > MAX_DESIGNER_DEFAULT_CHARS) {
        return { sql: null, error: `DEFAULT literal is longer than ${MAX_DESIGNER_DEFAULT_CHARS} characters.` };
      }
      if (/^-?\d+(?:\.\d+)?$/.test(raw) || raw === "TRUE" || raw === "FALSE" || raw === "NULL") {
        return { sql: ` DEFAULT ${raw}`, error: null };
      }
      return { sql: ` DEFAULT ${quoteSQLString(raw)}`, error: null };
    }
    default:
      return { sql: null, error: "Unknown DEFAULT kind." };
  }
}

export function buildCreateTableSQL(state: CreateTableState): DesignerBuildResult {
  const tableErr = designerIdentError(state.table, "Table name", { reservedTable: true });
  if (tableErr) return { sql: null, error: tableErr };
  const table = state.table.trim();
  if (!state.columns.length) return { sql: null, error: "Add at least one column." };
  if (state.columns.length > MAX_DESIGNER_COLUMNS) {
    return { sql: null, error: `A table can declare at most ${MAX_DESIGNER_COLUMNS} columns in this designer.` };
  }

  type PreparedColumn = {
    name: string;
    typeSQL: string;
    defSQL: string;
    primaryKey: boolean;
    notNull: boolean;
  };
  const seen = new Set<string>();
  const prepared: PreparedColumn[] = [];
  for (const column of state.columns) {
    const nameErr = designerIdentError(column.name, "Column name");
    if (nameErr) return { sql: null, error: nameErr };
    const name = column.name.trim();
    if (seen.has(name.toLowerCase())) return { sql: null, error: `Column "${name}" is listed twice.` };
    seen.add(name.toLowerCase());
    const type = designerColumnTypeSQL(column);
    if (type.error || !type.sql) return { sql: null, error: type.error ?? "Invalid column type." };
    if (column.primaryKey && (type.sql.startsWith("VECTOR<") || type.sql.startsWith("BITVECTOR<") || type.sql.startsWith("SPARSEVECTOR<"))) {
      return { sql: null, error: `Column "${name}" cannot be a PRIMARY KEY — vector types are not valid keys.` };
    }
    const def = designerDefaultSQL(column, type.sql);
    if (def.error || def.sql === null) return { sql: null, error: def.error ?? "Invalid DEFAULT." };
    prepared.push({
      name,
      typeSQL: type.sql,
      defSQL: def.sql,
      primaryKey: column.primaryKey,
      notNull: column.notNull || column.primaryKey,
    });
  }

  const pk = prepared.filter((column) => column.primaryKey);
  if (pk.length === 0) return { sql: null, error: "PRIMARY KEY is required." };
  const inlinePK = pk.length === 1;
  const body: string[] = [];
  for (const column of prepared) {
    let line = `  ${quoteIdentifier(column.name)} ${column.typeSQL}`;
    // Inline PRIMARY KEY only when this is the sole key column, matching
    // internal/catalog/ddl.CreateTableSQL.
    if (inlinePK && column.primaryKey) line += " PRIMARY KEY";
    else if (column.notNull) line += " NOT NULL";
    line += column.defSQL;
    body.push(line);
  }
  if (!inlinePK) {
    body.push(`  PRIMARY KEY (${pk.map((column) => quoteIdentifier(column.name)).join(", ")})`);
  }

  if (state.fkEnabled) {
    if (state.fkColumns.length === 0) return { sql: null, error: "Choose at least one foreign-key column." };
    const refErr = designerIdentError(state.fkRefTable, "Referenced table", { reservedTable: true });
    if (refErr) return { sql: null, error: refErr };
    if (state.fkRefColumns.length === 0) return { sql: null, error: "Choose at least one referenced column." };
    if (state.fkColumns.length !== state.fkRefColumns.length) {
      return { sql: null, error: "Foreign-key and referenced column lists must be the same length." };
    }
    const localSeen = new Set<string>();
    for (const name of state.fkColumns) {
      if (!seen.has(name.trim().toLowerCase())) {
        return { sql: null, error: `Foreign-key column "${name}" is not a column of this table.` };
      }
      if (localSeen.has(name.trim().toLowerCase())) {
        return { sql: null, error: `Foreign-key column "${name}" is listed twice.` };
      }
      localSeen.add(name.trim().toLowerCase());
    }
    const refSeen = new Set<string>();
    for (const name of state.fkRefColumns) {
      const err = designerIdentError(name, "Referenced column");
      if (err) return { sql: null, error: err };
      if (refSeen.has(name.trim().toLowerCase())) {
        return { sql: null, error: `Referenced column "${name.trim()}" is listed twice.` };
      }
      refSeen.add(name.trim().toLowerCase());
    }
    const del = state.fkOnDelete;
    const up = state.fkOnUpdate;
    if (!isDesignerFKAction(del) || !isDesignerFKAction(up)) {
      return { sql: null, error: "Unknown foreign-key action." };
    }
    body.push(
      `  FOREIGN KEY (${state.fkColumns.map((name) => quoteIdentifier(name.trim())).join(", ")}) ` +
        `REFERENCES ${quoteIdentifier(state.fkRefTable.trim())} ` +
        `(${state.fkRefColumns.map((name) => quoteIdentifier(name.trim())).join(", ")}) ` +
        `ON DELETE ${del} ON UPDATE ${up}`,
    );
  }

  return {
    sql: `CREATE TABLE ${quoteIdentifier(table)} (\n${body.join(",\n")}\n);`,
    error: null,
  };
}

function isDesignerFKAction(value: string): value is DesignerFKAction {
  return value === "RESTRICT" || value === "CASCADE" || value === "SET NULL" || value === "SET DEFAULT";
}

export type DesignerIndexColumnClass =
  | "text"
  | "json"
  | "vector-dense"
  | "vector-bit"
  | "vector-sparse"
  | "geo"
  | "other";

export function designerIndexColumnClass(type: string): DesignerIndexColumnClass {
  const upper = type.trim().toUpperCase();
  if (upper === "STRING" || upper === "TEXT" || upper.startsWith("CHAR(") || upper.startsWith("VARCHAR(")) {
    return "text";
  }
  if (upper === "JSON") return "json";
  if (upper.startsWith("VECTOR<")) return "vector-dense";
  if (upper.startsWith("BITVECTOR<")) return "vector-bit";
  if (upper.startsWith("SPARSEVECTOR<")) return "vector-sparse";
  if (
    upper === "POINT" ||
    upper === "BOX" ||
    upper === "LINESTRING" ||
    upper === "POLYGON" ||
    upper.startsWith("GEOMETRY") ||
    upper.startsWith("GEOGRAPHY")
  ) {
    return "geo";
  }
  return "other";
}

function parseDesignerJSONPath(raw: string): DesignerBuildResult & { segments?: string[] } {
  const trimmed = raw.trim();
  if (!trimmed) return { sql: "", error: null, segments: [] };
  const parts = trimmed.split(".").map((part) => part.trim()).filter((part) => part.length > 0);
  if (parts.length === 0) return { sql: null, error: "JSON path has no segments." };
  if (parts.length > MAX_DESIGNER_JSON_PATH_SEGMENTS) {
    return { sql: null, error: `JSON path can have at most ${MAX_DESIGNER_JSON_PATH_SEGMENTS} segments.` };
  }
  for (const part of parts) {
    const err = designerIdentError(part, "JSON path segment");
    if (err) return { sql: null, error: err };
  }
  return {
    sql: parts.map((part) => `.${quoteIdentifier(part)}`).join(""),
    error: null,
    segments: parts,
  };
}

export function buildCreateIndexSQL(
  state: CreateIndexState,
  columns: { name: string; type: string }[],
): DesignerBuildResult {
  const nameErr = designerIdentError(state.name, "Index name");
  if (nameErr) return { sql: null, error: nameErr };
  const tableErr = designerIdentError(state.table, "Table name");
  if (tableErr) return { sql: null, error: tableErr };
  if (columns.length === 0) return { sql: null, error: "This table exposes no columns to index." };
  if (state.columns.length === 0) return { sql: null, error: "Choose at least one index column." };

  const byName = new Map(columns.map((column) => [column.name, column]));
  const seen = new Set<string>();
  const resolved: { name: string; type: string }[] = [];
  for (const name of state.columns) {
    const column = byName.get(name);
    if (!column) return { sql: null, error: `Index column "${name}" is not a column of ${state.table.trim()}.` };
    if (seen.has(name)) return { sql: null, error: `Index column "${name}" is listed twice.` };
    seen.add(name);
    resolved.push(column);
  }

  const includeSeen = new Set<string>();
  const include: { name: string; type: string }[] = [];
  if (state.kind === "btree" || state.kind === "unique") {
    for (const name of state.include) {
      const column = byName.get(name);
      if (!column) return { sql: null, error: `INCLUDE column "${name}" is not a column of ${state.table.trim()}.` };
      if (seen.has(name)) return { sql: null, error: `Column "${name}" cannot be both a key and an INCLUDE column.` };
      if (includeSeen.has(name)) return { sql: null, error: `INCLUDE column "${name}" is listed twice.` };
      includeSeen.add(name);
      include.push(column);
    }
  } else if (state.include.length > 0) {
    return { sql: null, error: "INCLUDE is only valid on a B+Tree index." };
  }

  const jsonPath = parseDesignerJSONPath(state.jsonPath);
  if (jsonPath.error) return { sql: null, error: jsonPath.error };
  if ((jsonPath.segments?.length ?? 0) > 0) {
    if (state.kind !== "btree" && state.kind !== "unique") {
      return { sql: null, error: "A JSON path is only valid on a B+Tree index." };
    }
    if (resolved.length !== 1 || designerIndexColumnClass(resolved[0].type) !== "json") {
      return { sql: null, error: "A JSON path requires exactly one JSON key column." };
    }
  }

  switch (state.kind) {
    case "btree":
    case "unique": {
      if (resolved.length > MAX_DESIGNER_INDEX_COLUMNS) {
        return { sql: null, error: `A B+Tree index can list at most ${MAX_DESIGNER_INDEX_COLUMNS} key columns in this designer.` };
      }
      const verb = state.kind === "unique" ? "CREATE UNIQUE INDEX" : "CREATE INDEX";
      const keys = resolved.map((column, index) => {
        const ident = quoteIdentifier(column.name);
        if (index === 0 && (jsonPath.segments?.length ?? 0) > 0) return ident + (jsonPath.sql ?? "");
        return ident;
      });
      let sql = `${verb} ${quoteIdentifier(state.name.trim())} ON ${quoteIdentifier(state.table.trim())} (${keys.join(", ")})`;
      if (include.length > 0) {
        sql += ` INCLUDE (${include.map((column) => quoteIdentifier(column.name)).join(", ")})`;
      }
      return { sql: `${sql};`, error: null };
    }
    case "fulltext": {
      if (resolved.length > MAX_FULLTEXT_INDEX_COLUMNS) {
        return { sql: null, error: `A FULLTEXT index can list at most ${MAX_FULLTEXT_INDEX_COLUMNS} columns.` };
      }
      for (const column of resolved) {
        if (designerIndexColumnClass(column.type) !== "text") {
          return { sql: null, error: `Column "${column.name}" is ${column.type}, not STRING/TEXT/CHAR/VARCHAR — FULLTEXT cannot index it.` };
        }
      }
      const analyzer = state.analyzer;
      if (analyzer !== "simple" && analyzer !== "english" && analyzer !== "french" && analyzer !== "german" && analyzer !== "spanish") {
        return { sql: null, error: "Unknown full-text analyzer." };
      }
      let sql =
        `CREATE FULLTEXT INDEX ${quoteIdentifier(state.name.trim())} ON ${quoteIdentifier(state.table.trim())} ` +
        `(${resolved.map((column) => quoteIdentifier(column.name)).join(", ")})`;
      if (analyzer !== "simple") sql += ` WITH (ANALYZER = '${analyzer}')`;
      return { sql: `${sql};`, error: null };
    }
    case "vector": {
      if (resolved.length !== 1) return { sql: null, error: "A VECTOR index covers exactly one column." };
      const column = resolved[0];
      const klass = designerIndexColumnClass(column.type);
      if (klass !== "vector-dense" && klass !== "vector-bit" && klass !== "vector-sparse") {
        return { sql: null, error: `Column "${column.name}" is ${column.type}, not a vector type.` };
      }
      const method = state.vectorMethod;
      if (method === "SPARSE" && klass !== "vector-sparse") {
        return { sql: null, error: "USING SPARSE is only valid on a SPARSEVECTOR column." };
      }
      if (method !== "SPARSE" && klass === "vector-sparse") {
        return { sql: null, error: "A SPARSEVECTOR column can only use USING SPARSE." };
      }
      if ((method === "IVF" || method === "IVFPQ") && klass !== "vector-dense") {
        return { sql: null, error: `USING ${method} is only valid on a real-valued VECTOR column.` };
      }
      if (klass === "vector-bit" && method !== "HNSW") {
        return { sql: null, error: "A BITVECTOR column uses USING HNSW (Hamming graph) or no vector index." };
      }
      let using: string;
      if (method === "HNSW") {
        using = " USING HNSW";
        if (klass === "vector-dense" && (state.vectorQuant === "F16" || state.vectorQuant === "I8")) {
          using += ` WITH (QUANTIZATION = '${state.vectorQuant}')`;
        } else if (state.vectorQuant !== "NONE" && klass !== "vector-dense") {
          return { sql: null, error: "Quantization is only valid on a real-valued HNSW index." };
        }
      } else if (method === "SPARSE") {
        using = " USING SPARSE";
      } else if (method === "IVF" || method === "IVFPQ") {
        const lists = Math.trunc(state.ivfLists);
        if (!Number.isInteger(lists) || lists < 1) {
          return { sql: null, error: `${method} LISTS must be an integer ≥ 1.` };
        }
        const probes = Math.trunc(state.ivfProbes);
        if (state.ivfProbes && (!Number.isInteger(probes) || probes < 1)) {
          return { sql: null, error: `${method} PROBES must be an integer ≥ 1 when set.` };
        }
        if (method === "IVF") {
          using = ` USING IVF WITH (LISTS = ${lists}`;
          if (probes >= 1) using += `, PROBES = ${probes}`;
          using += ")";
        } else {
          const subspaces = Math.trunc(state.ivfSubspaces);
          if (!Number.isInteger(subspaces) || subspaces < 1) {
            return { sql: null, error: "IVFPQ SUBSPACES must be an integer ≥ 1." };
          }
          using = ` USING IVFPQ WITH (LISTS = ${lists}`;
          if (probes >= 1) using += `, PROBES = ${probes}`;
          using += `, SUBSPACES = ${subspaces})`;
        }
      } else {
        return { sql: null, error: "Unknown vector index method." };
      }
      return {
        sql:
          `CREATE VECTOR INDEX ${quoteIdentifier(state.name.trim())} ON ${quoteIdentifier(state.table.trim())} ` +
          `(${quoteIdentifier(column.name)})${using};`,
        error: null,
      };
    }
    case "spatial": {
      if (resolved.length !== 1) return { sql: null, error: "A SPATIAL index covers exactly one column." };
      const column = resolved[0];
      if (designerIndexColumnClass(column.type) !== "geo") {
        return { sql: null, error: `Column "${column.name}" is ${column.type}, not a geo type.` };
      }
      return {
        sql:
          `CREATE SPATIAL INDEX ${quoteIdentifier(state.name.trim())} ON ${quoteIdentifier(state.table.trim())} ` +
          `(${quoteIdentifier(column.name)});`,
        error: null,
      };
    }
    default:
      return { sql: null, error: "Unknown index kind." };
  }
}

export function designerIndexKindOptions(columns: { name: string; type: string }[]): DesignerIndexKind[] {
  const classes = new Set(columns.map((column) => designerIndexColumnClass(column.type)));
  const kinds: DesignerIndexKind[] = ["btree", "unique"];
  if (classes.has("text")) kinds.push("fulltext");
  if (classes.has("vector-dense") || classes.has("vector-bit") || classes.has("vector-sparse")) kinds.push("vector");
  if (classes.has("geo")) kinds.push("spatial");
  return kinds;
}

// --- SQL formatter --------------------------------------------------------
//
// formatSQL reflows a NextSQL statement (or a `;`-separated script) for
// readability: it normalizes whitespace, uppercases recognized keywords, and
// breaks a line before each major clause keyword. It is deliberately NOT
// built on `internal/sql/lexer` — that lexer discards comments and folds
// identifier case, so it cannot be reversed into source text. This is a
// self-contained, comment-preserving tokenizer with a strict safety net:
// the formatted text is re-tokenized and its significant-token stream
// (keyword tokens compared case-insensitively, every other token byte for
// byte, comments included and in order) must be identical to the input's.
// Any mismatch, any unterminated string / quoted identifier / block comment,
// or an over-size buffer returns the input UNCHANGED — the formatter never
// risks altering what a statement means. Known limitations, by design:
// parenthesized groups (column definition lists, VALUES tuples, subqueries)
// are kept on one line, and an `AND` / `OR` inside `CASE` or `BETWEEN` at
// statement level is not treated specially beyond those two guards.
export const MAX_FORMAT_SQL_BYTES = 1 << 20; // matches the editor's 1 MiB SQL bound

type FmtTokKind =
  | "word" | "number" | "string" | "qident" | "blob"
  | "param" | "punct" | "line-comment" | "block-comment" | "ws";

type FmtTok = { kind: FmtTokKind; text: string };

// The lexer keyword table (internal/sql/lexer/lexer.go). Casing any of these
// is safe: a keyword token outside a string / quoted identifier / comment
// carries no case significance in NSQL.
const FORMAT_KEYWORDS = new Set<string>(
  (
    "create table index unique on insert into values select distinct from where update set delete begin " +
    "commit rollback primary key not null default and or between in is limit offset as true false " +
    "transaction read committed snapshot serializable uuid string text blob int8 int16 int32 int64 " +
    "uint8 uint16 uint32 uint64 char varchar enum float32 float64 decimal timestamptz timestamp date time " +
    "interval json struct array map vector bitvector sparsevector f32 f16 i8 explain analyze maintain point " +
    "box location linestring polygon spatial geometry geography fulltext search for nearest to using hnsw " +
    "cosine l2 inner_product hamming join inner left right full cross outer group having by drop user role " +
    "grant revoke identified cluster database schema column function backup replication administration " +
    "connect execute all privileges admin reset foreign references constraint cascade restrict action match " +
    "alter add rename rebuild order asc desc if exists case when then else end union intersect except with " +
    "over schedule every at cron upsert returning workflow run trigger before after each show task tasks " +
    "cancel subscribe encrypted client transfer leader resource drain maintenance enable disable reconcile confirm"
  ).split(" "),
);

// Clause keywords that start a fresh line at statement level (depth 0). Join
// keywords are handled separately so `LEFT OUTER JOIN` stays on one line.
const FORMAT_NEWLINE_KEYWORDS = new Set<string>([
  "select", "from", "where", "having", "limit", "offset", "values", "set",
  "returning", "search", "nearest", "for", "group", "order", "union",
  "intersect", "except",
]);

const FORMAT_JOIN_WORDS = new Set<string>([
  "join", "inner", "left", "right", "full", "cross", "outer",
]);

const FORMAT_VECTOR_TYPE_WORDS = new Set<string>(["vector", "bitvector", "sparsevector"]);

// Keywords that take a parenthesized argument list directly (a parametric
// type or a value constructor), so `VARCHAR(255)` / `POINT(x, y)` keep the
// `(` attached. Every other keyword before a `(` keeps a space: `IN (…)`,
// `VALUES (…)`, `t (a, b)`.
const FORMAT_CALL_KEYWORDS = new Set<string>([
  "char", "varchar", "decimal", "enum", "uuid", "point", "box", "linestring",
  "polygon", "geometry", "geography", "struct", "array", "map", "location",
]);

class FmtLexError extends Error {}

function isFmtDigit(c: string): boolean {
  return c >= "0" && c <= "9";
}

function isFmtIdentStart(c: string): boolean {
  return (
    c === "_" ||
    (c >= "A" && c <= "Z") ||
    (c >= "a" && c <= "z") ||
    c.charCodeAt(0) > 127
  );
}

function isFmtIdentPart(c: string): boolean {
  return isFmtIdentStart(c) || isFmtDigit(c);
}

// tokenizeSQLForFormat mirrors internal/sql/lexer's token boundaries but
// keeps comments and never folds case. It throws FmtLexError on an
// unterminated construct so the caller can bail out and leave the SQL alone.
function tokenizeSQLForFormat(src: string): FmtTok[] {
  const toks: FmtTok[] = [];
  const n = src.length;
  let i = 0;
  while (i < n) {
    const c = src[i];
    if (c === " " || c === "\t" || c === "\n" || c === "\r") {
      let j = i + 1;
      while (j < n && (src[j] === " " || src[j] === "\t" || src[j] === "\n" || src[j] === "\r")) j++;
      toks.push({ kind: "ws", text: src.slice(i, j) });
      i = j;
      continue;
    }
    if (c === "-" && src[i + 1] === "-") {
      let j = i + 2;
      while (j < n && src[j] !== "\n") j++;
      toks.push({ kind: "line-comment", text: src.slice(i, j) });
      i = j;
      continue;
    }
    if (c === "/" && src[i + 1] === "*") {
      let j = i + 2;
      while (j + 1 < n && !(src[j] === "*" && src[j + 1] === "/")) j++;
      if (!(src[j] === "*" && src[j + 1] === "/")) throw new FmtLexError("unterminated block comment");
      j += 2;
      toks.push({ kind: "block-comment", text: src.slice(i, j) });
      i = j;
      continue;
    }
    if (c === "'") {
      let j = i + 1;
      for (;;) {
        if (j >= n) throw new FmtLexError("unterminated string");
        if (src[j] === "'") {
          if (src[j + 1] === "'") { j += 2; continue; }
          j++;
          break;
        }
        j++;
      }
      toks.push({ kind: "string", text: src.slice(i, j) });
      i = j;
      continue;
    }
    if (c === '"') {
      let j = i + 1;
      for (;;) {
        if (j >= n) throw new FmtLexError("unterminated quoted identifier");
        if (src[j] === '"') {
          if (src[j + 1] === '"') { j += 2; continue; }
          j++;
          break;
        }
        j++;
      }
      toks.push({ kind: "qident", text: src.slice(i, j) });
      i = j;
      continue;
    }
    if ((c === "x" || c === "X") && src[i + 1] === "'") {
      let j = i + 2;
      while (j < n && src[j] !== "'") j++;
      if (j >= n) throw new FmtLexError("unterminated blob literal");
      j++;
      toks.push({ kind: "blob", text: src.slice(i, j) });
      i = j;
      continue;
    }
    if (c === "$" && isFmtDigit(src[i + 1] ?? "")) {
      let j = i + 1;
      while (j < n && isFmtDigit(src[j])) j++;
      toks.push({ kind: "param", text: src.slice(i, j) });
      i = j;
      continue;
    }
    if (isFmtDigit(c) || (c === "." && isFmtDigit(src[i + 1] ?? ""))) {
      let j = i;
      while (j < n && isFmtDigit(src[j])) j++;
      if (src[j] === ".") {
        j++;
        while (j < n && isFmtDigit(src[j])) j++;
      }
      toks.push({ kind: "number", text: src.slice(i, j) });
      i = j;
      continue;
    }
    if (isFmtIdentStart(c)) {
      let j = i + 1;
      while (j < n && isFmtIdentPart(src[j])) j++;
      toks.push({ kind: "word", text: src.slice(i, j) });
      i = j;
      continue;
    }
    if (c === "<" && (src[i + 1] === ">" || src[i + 1] === "=")) {
      toks.push({ kind: "punct", text: src.slice(i, i + 2) });
      i += 2;
      continue;
    }
    if ((c === ">" || c === "!") && src[i + 1] === "=") {
      toks.push({ kind: "punct", text: src.slice(i, i + 2) });
      i += 2;
      continue;
    }
    toks.push({ kind: "punct", text: c });
    i++;
  }
  return toks;
}

function fmtKeywordCased(text: string): string {
  return FORMAT_KEYWORDS.has(text.toLowerCase()) ? text.toUpperCase() : text;
}

function sameFormatTokenStream(a: FmtTok[], b: FmtTok[]): boolean {
  if (a.length !== b.length) return false;
  for (let i = 0; i < a.length; i++) {
    const x = a[i];
    const y = b[i];
    if (x.kind !== y.kind) return false;
    if (x.kind === "word") {
      if (x.text.toLowerCase() !== y.text.toLowerCase()) return false;
      // A non-keyword identifier must keep its exact original case.
      if (!FORMAT_KEYWORDS.has(x.text.toLowerCase()) && x.text !== y.text) return false;
    } else if (x.text !== y.text) {
      return false;
    }
  }
  return true;
}

const FORMAT_UNARY_PREV_PUNCT = new Set<string>([
  "(", ",", "=", "<", ">", "<=", ">=", "<>", "!=", "+", "-", "*", "/",
]);

type FmtSigTok = FmtTok & { wsBefore: boolean };

function renderFormattedSQL(sig: FmtSigTok[]): string {
  const INDENT = "  ";
  let out = "";
  let atLineStart = true;
  let curIndent = 0;
  let depth = 0;
  let caseDepth = 0;
  let typeArgDepth = 0;
  let betweenPending = false;
  let attachNext = false;
  let pendingBreak: { indent: number; blank: boolean } | null = null;
  let prev: FmtTok | null = null;
  // The current top-level clause. A comma only starts a new line inside a
  // clause whose operands are conventionally one-per-line (a SELECT / SET /
  // VALUES list); in FROM / GROUP BY / ORDER BY / SEARCH a comma stays inline.
  let clauseCtx = "";
  const COMMA_BREAK_CLAUSES = new Set<string>(["select", "set", "values", "returning"]);

  const breakLine = (indent: number, blank: boolean) => {
    out = out.replace(/[ \t]+$/, "");
    out += blank ? "\n\n" : "\n";
    out += INDENT.repeat(indent);
    curIndent = indent;
    atLineStart = true;
  };

  for (let k = 0; k < sig.length; k++) {
    const t = sig[k];
    const lc = t.kind === "word" ? t.text.toLowerCase() : "";
    const prevLc = prev && prev.kind === "word" ? prev.text.toLowerCase() : "";
    const topLevel = depth === 0 && caseDepth === 0 && typeArgDepth === 0;

    // 1. Decide whether this token starts a new line.
    if (k === 0) {
      if (topLevel && FORMAT_NEWLINE_KEYWORDS.has(lc)) clauseCtx = lc;
    } else if (pendingBreak) {
      breakLine(pendingBreak.indent, pendingBreak.blank);
    } else if (topLevel && t.kind === "word") {
      if (lc === "and" || lc === "or") {
        if (!(lc === "and" && betweenPending)) breakLine(1, false);
      } else if (FORMAT_JOIN_WORDS.has(lc)) {
        // Break only on the first word of the join phrase.
        if (!FORMAT_JOIN_WORDS.has(prevLc)) { breakLine(0, false); clauseCtx = "join"; }
      } else if (lc === "on") {
        breakLine(1, false);
      } else if (FORMAT_NEWLINE_KEYWORDS.has(lc)) {
        breakLine(0, false);
        clauseCtx = lc;
      }
    }
    pendingBreak = null;

    // 2. Decide the separator before the token text.
    const vectorTypeOpener =
      t.kind === "punct" && t.text === "<" && FORMAT_VECTOR_TYPE_WORDS.has(prevLc);
    const unarySign =
      t.kind === "punct" &&
      (t.text === "+" || t.text === "-") &&
      (prev === null ||
        (prev.kind === "punct" && FORMAT_UNARY_PREV_PUNCT.has(prev.text)) ||
        (prev.kind === "word" && FORMAT_KEYWORDS.has(prevLc)));

    let space = true;
    if (atLineStart || prev === null) {
      space = false;
    } else if (attachNext) {
      space = false;
    } else if (t.kind === "punct" && (t.text === "," || t.text === ";" || t.text === ")")) {
      space = false;
    } else if (t.kind === "punct" && t.text === ".") {
      space = false;
    } else if (prev.kind === "punct" && (prev.text === "." || prev.text === "(")) {
      space = false;
    } else if (
      t.kind === "punct" && t.text === "(" &&
      (prev.kind === "word" && FORMAT_CALL_KEYWORDS.has(prevLc))
    ) {
      // A parametric type / constructor keyword: `VARCHAR(255)`.
      space = false;
    } else if (
      t.kind === "punct" && t.text === "(" && !t.wsBefore &&
      (prev.kind === "qident" ||
        (prev.kind === "punct" && prev.text === ")") ||
        (prev.kind === "word" && !FORMAT_KEYWORDS.has(prevLc)))
    ) {
      // Ambiguous `name(` — could be a function call or a bare table before a
      // column list. Honor whatever spacing the author used in the source.
      space = false;
    } else if (typeArgDepth > 0 && t.kind === "punct" && (t.text === ">" || t.text === ",")) {
      space = false;
    } else if (vectorTypeOpener) {
      space = false;
    }

    if (space) out += " ";
    out += t.kind === "word" ? fmtKeywordCased(t.text) : t.text;
    atLineStart = false;
    attachNext = false;

    // 3. Update structural state after the token.
    if (t.kind === "punct") {
      if (t.text === "(") depth++;
      else if (t.text === ")") depth = Math.max(0, depth - 1);
      else if (vectorTypeOpener) typeArgDepth++;
      else if (t.text === ">" && typeArgDepth > 0) typeArgDepth--;
    } else if (t.kind === "word") {
      if (lc === "case") caseDepth++;
      else if (lc === "end") caseDepth = Math.max(0, caseDepth - 1);
      else if (lc === "between") betweenPending = true;
      else if (lc === "and") betweenPending = false;
    }
    if (t.kind === "word" && (FORMAT_NEWLINE_KEYWORDS.has(lc) || lc === "or")) betweenPending = false;

    attachNext =
      (t.kind === "punct" && (t.text === "(" || t.text === ".")) ||
      unarySign ||
      vectorTypeOpener ||
      (typeArgDepth > 0 && t.kind === "punct" && t.text === ",");

    // 4. Queue a forced break for the next token where required.
    if (t.kind === "line-comment") {
      pendingBreak = { indent: curIndent, blank: false };
    } else if (t.kind === "punct" && t.text === ";") {
      pendingBreak = { indent: 0, blank: true };
      depth = 0;
      caseDepth = 0;
      typeArgDepth = 0;
      betweenPending = false;
      clauseCtx = "";
    } else if (
      t.kind === "punct" && t.text === "," &&
      depth === 0 && caseDepth === 0 && typeArgDepth === 0 &&
      COMMA_BREAK_CLAUSES.has(clauseCtx)
    ) {
      pendingBreak = { indent: 1, blank: false };
      betweenPending = false;
    }

    prev = t;
  }

  return out;
}

// formatSQL returns a reflowed copy of the SQL, or the input unchanged when
// it cannot do so without risking a semantic change (see the section
// comment above). The caller compares the return value to its input to
// decide whether anything changed.
export function formatSQL(input: string): string {
  if (!input || !input.trim()) return input;
  if (utf8ByteLength(input) > MAX_FORMAT_SQL_BYTES) return input;

  let inTokens: FmtTok[];
  try {
    inTokens = tokenizeSQLForFormat(input);
  } catch {
    return input;
  }
  const inSig: FmtSigTok[] = [];
  let sawWs = false;
  for (const tok of inTokens) {
    if (tok.kind === "ws") { sawWs = true; continue; }
    inSig.push({ ...tok, wsBefore: sawWs });
    sawWs = false;
  }
  if (inSig.length === 0) return input;

  const rendered = renderFormattedSQL(inSig)
    .replace(/[ \t]+\n/g, "\n")
    .replace(/\n{3,}/g, "\n\n")
    .replace(/^\s+/, "")
    .replace(/\s+$/, "");

  let outTokens: FmtTok[];
  try {
    outTokens = tokenizeSQLForFormat(rendered);
  } catch {
    return input;
  }
  const outSig = outTokens.filter((t) => t.kind !== "ws");
  if (!sameFormatTokenStream(inSig, outSig)) return input;

  return rendered === input.replace(/^\s+/, "").replace(/\s+$/, "") ? input : rendered;
}

// ---------------------------------------------------------------------------
// Editable data grid & staged-change review.
//
// In-memory staged modifications (cell updates, row deletions, row insertions)
// over an authorized table whose primary-key columns are present in the
// query result. Changes are reviewed before execution, previewed as a native
// transactional SQL script (BEGIN; ... COMMIT;), and executed transactionally
// through the session's official-driver connection or loaded into the editor.
// ---------------------------------------------------------------------------

export const MAX_STAGED_CHANGES = 500;
export const MAX_STAGED_SQL_BYTES = 512 * 1024;

export type StagedUpdate = {
  kind: "update";
  table: string;
  rowKey: string;
  pkValues: Record<string, string | null>;
  column: string;
  columnType: string;
  oldValue: string | null;
  newValue: string | null;
};

export type StagedDelete = {
  kind: "delete";
  table: string;
  rowKey: string;
  pkValues: Record<string, string | null>;
  rowValues: (string | null)[];
};

export type StagedInsert = {
  kind: "insert";
  table: string;
  tempId: string;
  values: Record<string, string | null>;
};

export type StagedChange = StagedUpdate | StagedDelete | StagedInsert;

export type StagedChanges = {
  table: string;
  pkColumns: string[];
  columnTypes: Record<string, string>;
  updates: Record<string, Record<string, StagedUpdate>>;
  deletes: Record<string, StagedDelete>;
  inserts: StagedInsert[];
};

export type StagedBuildResult = {
  sql: string | null;
  statements: string[];
  error: string | null;
};

export function createEmptyStagedChanges(
  table: string,
  pkColumns: string[],
  columnTypes: Record<string, string> = {},
): StagedChanges {
  return {
    table,
    pkColumns,
    columnTypes,
    updates: {},
    deletes: {},
    inserts: [],
  };
}

export function stagedChangesCount(changes: StagedChanges | null | undefined): number {
  if (!changes) return 0;
  let count = 0;
  for (const rowKey of Object.keys(changes.updates)) {
    count += Object.keys(changes.updates[rowKey] || {}).length;
  }
  count += Object.keys(changes.deletes || {}).length;
  count += (changes.inserts || []).length;
  return count;
}

export function stagedChangesSummary(changes: StagedChanges | null | undefined): string {
  if (!changes) return "No staged changes";
  let updateCount = 0;
  for (const rowKey of Object.keys(changes.updates)) {
    updateCount += Object.keys(changes.updates[rowKey] || {}).length;
  }
  const deleteCount = Object.keys(changes.deletes || {}).length;
  const insertCount = (changes.inserts || []).length;
  const parts: string[] = [];
  if (updateCount > 0) parts.push(`${updateCount} ${updateCount === 1 ? "update" : "updates"}`);
  if (deleteCount > 0) parts.push(`${deleteCount} ${deleteCount === 1 ? "deletion" : "deletions"}`);
  if (insertCount > 0) parts.push(`${insertCount} ${insertCount === 1 ? "insertion" : "insertions"}`);
  return parts.length > 0 ? parts.join(", ") : "No staged changes";
}

export function tablePKColumns(detail: StudioTableDetail | null | undefined): string[] {
  if (!detail || !detail.columns || !detail.columns.rows) return [];
  const nameIdx = resultColumn(detail.columns, "column_name");
  const primaryIdx = resultColumn(detail.columns, "is_primary");
  if (nameIdx < 0 || primaryIdx < 0) return [];
  const pks: string[] = [];
  for (const row of detail.columns.rows) {
    const isPrim = (row[primaryIdx] ?? "").trim().toLowerCase() === "true";
    const name = row[nameIdx];
    if (isPrim && name) {
      pks.push(name);
    }
  }
  return pks;
}

export function tableColumnTypesRecord(detail: StudioTableDetail | null | undefined): Record<string, string> {
  if (!detail || !detail.columns || !detail.columns.rows) return {};
  const nameIdx = resultColumn(detail.columns, "column_name");
  const typeIdx = resultColumn(detail.columns, "type");
  if (nameIdx < 0 || typeIdx < 0) return {};
  const map: Record<string, string> = {};
  for (const row of detail.columns.rows) {
    const name = row[nameIdx];
    const type = row[typeIdx];
    if (name && type) {
      map[name] = type;
    }
  }
  return map;
}

export function detectEditableTable(sql: string, catalogTables: string[]): string | null {
  if (!sql) return null;
  const trimmed = sql.trim();
  if (!/^SELECT\b/i.test(trimmed)) return null;
  if (/\b(?:JOIN|UNION)\b/i.test(trimmed)) return null;

  const match = /\bFROM\s+(?:"([^"]+)"|([A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)?))/i.exec(trimmed);
  if (!match) return null;
  const raw = match[1] ?? match[2];
  if (!raw) return null;
  const clean = raw.trim();
  if (clean.toLowerCase().startsWith("system.") || clean.toLowerCase().startsWith("nsql_")) {
    return null;
  }
  const found = catalogTables.find((t) => t.toLowerCase() === clean.toLowerCase());
  return found ?? null;
}

export function isResultEditable(
  result: StudioResultSet | null | undefined,
  pkColumns: string[],
): { editable: boolean; reason?: string } {
  if (!result || !result.columns || result.columns.length === 0) {
    return { editable: false, reason: "No result columns available." };
  }
  if (!pkColumns || pkColumns.length === 0) {
    return { editable: false, reason: "Table has no primary key defined." };
  }
  const resultCols = new Set(result.columns.map((c) => c.toLowerCase()));
  const missing = pkColumns.filter((pk) => !resultCols.has(pk.toLowerCase()));
  if (missing.length > 0) {
    return {
      editable: false,
      reason: `Primary key column(s) missing from result: ${missing.join(", ")}.`,
    };
  }
  return { editable: true };
}

export function extractRowPK(
  row: (string | null)[],
  columns: string[],
  pkColumns: string[],
): Record<string, string | null> {
  const pkMap: Record<string, string | null> = {};
  for (const pk of pkColumns) {
    const idx = columns.findIndex((c) => c.toLowerCase() === pk.toLowerCase());
    pkMap[pk] = idx >= 0 ? (row[idx] ?? null) : null;
  }
  return pkMap;
}

export function makeRowKey(pkValues: Record<string, string | null>): string {
  const keys = Object.keys(pkValues).sort();
  return keys.map((k) => `${k}=${pkValues[k] ?? "NULL"}`).join("|");
}

export function formatCellSQLLiteral(value: string | null, type: string): string {
  if (value === null) return "NULL";
  const kind = dataGenFieldKind(type);
  switch (kind) {
    case "int":
    case "decimal": {
      const trimmed = value.trim();
      if (/^-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?$/.test(trimmed)) {
        return trimmed;
      }
      return quoteSQLString(value);
    }
    case "bool": {
      const lower = value.trim().toLowerCase();
      if (lower === "true" || lower === "1") return "TRUE";
      if (lower === "false" || lower === "0") return "FALSE";
      return quoteSQLString(value);
    }
    default:
      return quoteSQLString(value);
  }
}

export function stageCellUpdate(
  current: StagedChanges,
  rowKey: string,
  pkValues: Record<string, string | null>,
  column: string,
  columnType: string,
  oldValue: string | null,
  newValue: string | null,
): { changes: StagedChanges; error: string | null } {
  if (current.deletes[rowKey]) {
    return { changes: current, error: "Cannot update a row marked for deletion." };
  }

  if (newValue === oldValue) {
    if (!current.updates[rowKey] || !current.updates[rowKey][column]) {
      return { changes: current, error: null };
    }
    const nextUpdates = { ...current.updates };
    const rowUpdates = { ...nextUpdates[rowKey] };
    delete rowUpdates[column];
    if (Object.keys(rowUpdates).length === 0) {
      delete nextUpdates[rowKey];
    } else {
      nextUpdates[rowKey] = rowUpdates;
    }
    return { changes: { ...current, updates: nextUpdates }, error: null };
  }

  const curCount = stagedChangesCount(current);
  const isExisting = Boolean(current.updates[rowKey]?.[column]);
  if (!isExisting && curCount >= MAX_STAGED_CHANGES) {
    return { changes: current, error: `Maximum ${MAX_STAGED_CHANGES} staged changes reached.` };
  }

  const nextUpdates = { ...current.updates };
  const rowUpdates = { ...(nextUpdates[rowKey] || {}) };
  rowUpdates[column] = {
    kind: "update",
    table: current.table,
    rowKey,
    pkValues,
    column,
    columnType,
    oldValue,
    newValue,
  };
  nextUpdates[rowKey] = rowUpdates;
  return { changes: { ...current, updates: nextUpdates }, error: null };
}

export function stageRowDelete(
  current: StagedChanges,
  rowKey: string,
  pkValues: Record<string, string | null>,
  rowValues: (string | null)[],
): { changes: StagedChanges; error: string | null } {
  const nextDeletes = { ...current.deletes };
  const nextUpdates = { ...current.updates };

  if (nextDeletes[rowKey]) {
    delete nextDeletes[rowKey];
    return { changes: { ...current, deletes: nextDeletes }, error: null };
  }

  const curCount = stagedChangesCount(current);
  if (curCount >= MAX_STAGED_CHANGES) {
    return { changes: current, error: `Maximum ${MAX_STAGED_CHANGES} staged changes reached.` };
  }

  delete nextUpdates[rowKey];
  nextDeletes[rowKey] = {
    kind: "delete",
    table: current.table,
    rowKey,
    pkValues,
    rowValues,
  };
  return { changes: { ...current, updates: nextUpdates, deletes: nextDeletes }, error: null };
}

export function stageRowInsert(
  current: StagedChanges,
  values: Record<string, string | null>,
): { changes: StagedChanges; error: string | null } {
  const curCount = stagedChangesCount(current);
  if (curCount >= MAX_STAGED_CHANGES) {
    return { changes: current, error: `Maximum ${MAX_STAGED_CHANGES} staged changes reached.` };
  }

  const tempId = `new-row-${Date.now()}-${Math.random().toString(36).slice(2, 7)}`;
  const item: StagedInsert = {
    kind: "insert",
    table: current.table,
    tempId,
    values,
  };
  return {
    changes: { ...current, inserts: [...current.inserts, item] },
    error: null,
  };
}

export function removeStagedInsert(current: StagedChanges, tempId: string): StagedChanges {
  return {
    ...current,
    inserts: current.inserts.filter((ins) => ins.tempId !== tempId),
  };
}

export function buildStagedChangeSQL(changes: StagedChanges): StagedBuildResult {
  const total = stagedChangesCount(changes);
  if (total === 0) {
    return { sql: null, statements: [], error: "No staged changes to commit." };
  }
  if (total > MAX_STAGED_CHANGES) {
    return { sql: null, statements: [], error: `Cannot commit more than ${MAX_STAGED_CHANGES} changes at once.` };
  }

  const tableIdent = quoteIdentifier(changes.table);
  const stmts: string[] = [];

  const buildPKPredicate = (pkValues: Record<string, string | null>): string => {
    const parts: string[] = [];
    for (const pk of changes.pkColumns) {
      const val = pkValues[pk] ?? null;
      const type = changes.columnTypes[pk] || "STRING";
      if (val === null) {
        parts.push(`${quoteIdentifier(pk)} IS NULL`);
      } else {
        parts.push(`${quoteIdentifier(pk)} = ${formatCellSQLLiteral(val, type)}`);
      }
    }
    return parts.join(" AND ");
  };

  // 1. UPDATE statements (grouped by row)
  for (const rowKey of Object.keys(changes.updates)) {
    const colUpdates = changes.updates[rowKey];
    const cols = Object.keys(colUpdates);
    if (cols.length === 0) continue;

    const first = colUpdates[cols[0]];
    const setClauses = cols.map((col) => {
      const upd = colUpdates[col];
      const type = upd.columnType || changes.columnTypes[col] || "STRING";
      return `${quoteIdentifier(col)} = ${formatCellSQLLiteral(upd.newValue, type)}`;
    });

    const whereClause = buildPKPredicate(first.pkValues);
    stmts.push(`UPDATE ${tableIdent} SET ${setClauses.join(", ")} WHERE ${whereClause};`);
  }

  // 2. DELETE statements
  for (const rowKey of Object.keys(changes.deletes)) {
    const del = changes.deletes[rowKey];
    const whereClause = buildPKPredicate(del.pkValues);
    stmts.push(`DELETE FROM ${tableIdent} WHERE ${whereClause};`);
  }

  // 3. INSERT statements
  for (const ins of changes.inserts) {
    const cols = Object.keys(ins.values).filter((k) => ins.values[k] !== undefined);
    if (cols.length === 0) continue;

    const colIdents = cols.map(quoteIdentifier).join(", ");
    const valLiterals = cols
      .map((col) => {
        const val = ins.values[col] ?? null;
        const type = changes.columnTypes[col] || "STRING";
        return formatCellSQLLiteral(val, type);
      })
      .join(", ");

    stmts.push(`INSERT INTO ${tableIdent} (${colIdents}) VALUES (${valLiterals});`);
  }

  if (stmts.length === 0) {
    return { sql: null, statements: [], error: "No statements generated from staged changes." };
  }

  const allStatements = ["BEGIN;", ...stmts, "COMMIT;"];
  const fullSQL = allStatements.join("\n");

  if (utf8ByteLength(fullSQL) > MAX_STAGED_SQL_BYTES) {
    return {
      sql: null,
      statements: [],
      error: `Generated SQL exceeds size limit of ${MAX_STAGED_SQL_BYTES / 1024} KiB.`,
    };
  }

  return {
    sql: fullSQL,
    statements: allStatements,
    error: null,
  };
}
