import assert from "node:assert/strict";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { pathToFileURL } from "node:url";
import * as esbuild from "esbuild";

const scratch = mkdtempSync(join(tmpdir(), "nextsql-studio-results-"));
const outfile = join(scratch, "result-tools.mjs");

try {
  await esbuild.build({
    entryPoints: [new URL("./src/studio/resultTools.ts", import.meta.url).pathname],
    bundle: true,
    platform: "node",
    format: "esm",
    target: ["node20"],
    outfile,
  });
  const tools = await import(`${pathToFileURL(outfile).href}?v=${Date.now()}`);

  const result = {
    columns: ["value", "value", "formula"],
    column_types: ["STRING", "STRING", "STRING"],
    rows: [
      ["hello,\nworld", null, "=2+2"],
      ["second", "", " -17"],
    ],
    truncated: false,
    elapsed_ms: 1,
  };

  assert.deepEqual(tools.normalizeRowIndexes(2, [1, 1, -1, 99, 0]), [0, 1]);
  assert.equal(tools.utf8ByteLength("ASCII · café · 🐘"), new TextEncoder().encode("ASCII · café · 🐘").byteLength);
  const tsv = tools.tabularText(result);
  assert.match(tsv, /\\N/);
  assert.match(tsv, /'=2\+2/);
  assert.match(tsv, /' -17/);
  assert.match(tsv, /"hello,\nworld"/);

  const csv = tools.serializeResult(result, "csv", [1]);
  assert.equal(csv.rowCount, 1);
  assert.equal(csv.content.startsWith("\ufeff"), true);
  assert.match(csv.content, /second,,\s*' -17/);

  const json = tools.serializeResult(result, "json", [1, 0]);
  const decoded = JSON.parse(json.content);
  assert.equal(decoded.format, "nextsql-studio-result-v1");
  assert.deepEqual(decoded.columns, result.columns, "duplicate column names must be preserved");
  assert.deepEqual(decoded.rows, result.rows, "row order must be canonical, not caller iteration order");
  assert.equal(decoded.rows[0][1], null, "JSON must preserve SQL NULL");

  assert.equal(tools.classifyStudioType("JSON"), "json");
  assert.equal(tools.classifyStudioType("VECTOR<F32,3>"), "vector");
  assert.equal(tools.classifyStudioType("GEOGRAPHY(POLYGON,4326)"), "geo");
  assert.equal(tools.classifyStudioType("TIMESTAMPTZ"), "timestamptz");
  assert.equal(tools.classifyStudioType("STRING"), "text");

  const dense = tools.parseVector("[3,4,0]", "VECTOR<F32,3>");
  assert.equal(dense.mode, "dense");
  assert.equal(dense.l2Norm, 5);
  assert.equal(dense.nonZero, 2);
  const sparse = tools.parseVector("{1:3,4:4}", "SPARSEVECTOR<8>");
  assert.equal(sparse.mode, "sparse");
  assert.deepEqual(sparse.indices, [1, 4]);
  assert.equal(sparse.l2Norm, 5);
  const bits = tools.parseVector("[1,0,1,1]", "BITVECTOR<4>");
  assert.equal(bits.declaredDimensions, 4);
  assert.equal(bits.nonZero, 3);
  assert.throws(() => tools.parseVector("[1,2]", "VECTOR<F32,3>"), /declared dimensions/);
  assert.throws(() => tools.parseVector("{8:1}", "SPARSEVECTOR<8>"), /declared dimensions/);
  assert.throws(() => tools.parseVector("{1:0}", "SPARSEVECTOR<8>"), /explicit zero/);
  assert.throws(() => tools.parseVector("[1,2,0,1]", "BITVECTOR<4>"), /zero or one/);
  assert.equal(tools.compactCellValue("VECTOR<F32,3>", "[3,4,0]"), "3-d vector");
  assert.equal(tools.compactCellValue("JSON", "{\"a\":1}"), "JSON object · 1 keys");

  const tree = tools.inspectJSON(JSON.stringify(Array.from({ length: 1_100 }, (_, index) => ({ index }))));
  assert.equal(tree.truncated, true);
  assert.ok(tree.nodeCount <= tools.MAX_JSON_TREE_NODES, `JSON tree mounted ${tree.nodeCount} nodes`);
  assert.equal(tree.root.kind, "array");
  let deep = { leaf: true };
  for (let index = 0; index < 30; index++) deep = { child: deep };
  const deepTree = tools.inspectJSON(JSON.stringify(deep));
  assert.equal(deepTree.truncated, true, "JSON tree depth must be bounded");

  const pathTree = tools.inspectJSON('{"tags":[{"sku":"A-1"}],"a.b":true}');
  const tagsNode = pathTree.root.children.find((node) => node.label === "tags");
  const firstTag = tagsNode.children[0];
  const skuNode = firstTag.children[0];
  assert.deepEqual(skuNode.path, [
    { value: "tags", arrayIndex: false },
    { value: "0", arrayIndex: true },
    { value: "sku", arrayIndex: false },
  ]);
  assert.equal(tools.nativeJSONPath("metadata", skuNode.path), '"metadata"."tags".0."sku"');
  assert.equal(
    tools.buildJSONPathQuery("articles", "metadata", skuNode.path),
    'SELECT "metadata"."tags".0."sku" FROM "articles" LIMIT 100',
  );
  assert.throws(
    () => tools.nativeJSONPath("metadata", [{ value: "-1", arrayIndex: true }]),
    /invalid JSON array index/,
  );

  const indexMetadata = {
    columns: ["table_name", "index_name", "kind", "columns", "status"],
    column_types: ["STRING", "STRING", "STRING", "STRING", "STRING"],
    rows: [
      ["articles", "ix_tag_sku", "btree", "metadata.tags.0.sku", "valid"],
      ["articles", "ix_other", "btree", "metadata.other", "valid"],
    ],
    truncated: false,
    elapsed_ms: 1,
  };
  assert.deepEqual(tools.reportedJSONPathIndexes(indexMetadata, "metadata", skuNode.path), [
    { name: "ix_tag_sku", status: "valid" },
  ]);
  assert.deepEqual(tools.reportedJSONPathIndexes(indexMetadata, "metadata", tagsNode.path), []);
  const specialNode = pathTree.root.children.find((node) => node.label === "a.b");
  assert.equal(
    tools.reportedJSONPathIndexes(indexMetadata, "metadata", specialNode.path),
    null,
    "system.indexes does not preserve quoted JSON-path segment boundaries, so special keys stay unknown",
  );

  const fullTextDetail = {
    generated_at: new Date(0).toISOString(),
    name: "articles",
    table: { columns: ["name"], column_types: ["STRING"], rows: [["articles"]], truncated: false, elapsed_ms: 1 },
    columns: {
      columns: ["table_name", "column_name", "ordinal", "type", "not_null", "is_primary", "default_value"],
      column_types: ["STRING", "STRING", "INT64", "STRING", "BOOL", "BOOL", "STRING"],
      rows: [
        ["articles", "id", "0", "INT64", "true", "true", ""],
        ["articles", "title", "1", "STRING", "true", "false", ""],
        ["articles", "body", "2", "TEXT", "false", "false", ""],
        ["articles", "embedding", "3", "VECTOR<F32,3>", "false", "false", ""],
      ],
      truncated: false,
      elapsed_ms: 1,
    },
    indexes: {
      columns: ["table_name", "index_name", "kind", "is_unique", "columns", "include_columns", "predicate", "status"],
      column_types: ["STRING", "STRING", "STRING", "BOOL", "STRING", "STRING", "STRING", "STRING"],
      rows: [
        ["articles", "ix_articles_text", "fulltext", "false", "title,body", "", "", "valid"],
        ["articles", "ix_body_building", "fulltext", "false", "body", "", "", "building"],
        ["articles", "ix_ambiguous", "fulltext", "false", "title,body,with,comma", "", "", "valid"],
      ],
      truncated: false,
      elapsed_ms: 1,
    },
  };
  const fullTextCatalog = tools.fullTextCatalog(fullTextDetail);
  assert.deepEqual(fullTextCatalog.columns, ["title", "body"]);
  assert.deepEqual(fullTextCatalog.primaryColumns, ["id"]);
  assert.deepEqual(fullTextCatalog.indexes[0], {
    name: "ix_articles_text",
    status: "valid",
    columns: ["title", "body"],
    usable: true,
  });
  assert.equal(fullTextCatalog.indexes[2].usable, false, "ambiguous catalog field boundaries must fail closed");

  const fullTextBase = {
    table: "articles",
    columns: ["title", "body"],
    query: "customer's database",
    output: "snippet",
    limit: 20,
    index: fullTextCatalog.indexes[0],
    primaryColumns: fullTextCatalog.primaryColumns,
  };
  assert.deepEqual(tools.buildFullTextSQL(fullTextBase), {
    sql: `SELECT "id", SNIPPET("title") AS "title_snippet", SNIPPET("body") AS "body_snippet" FROM "articles" SEARCH "title", "body" FOR 'customer''s database' LIMIT 20`,
    error: null,
  });
  assert.equal(
    tools.buildFullTextSQL({ ...fullTextBase, output: "highlight" }).sql,
    `SELECT "id", HIGHLIGHT("title") AS "title_highlight", HIGHLIGHT("body") AS "body_highlight" FROM "articles" SEARCH "title", "body" FOR 'customer''s database' LIMIT 20`,
  );
  assert.equal(
    tools.buildFullTextSQL({ ...fullTextBase, output: "rows", index: undefined }).sql,
    `SELECT * FROM "articles" SEARCH "title", "body" FOR 'customer''s database' LIMIT 20`,
  );
  assert.match(tools.buildFullTextSQL({ ...fullTextBase, columns: ["body", "title"] }).error, /catalog order/);
  assert.match(tools.buildFullTextSQL({ ...fullTextBase, index: fullTextCatalog.indexes[1], columns: ["body"] }).error, /not valid/);
  assert.match(tools.buildFullTextSQL({ ...fullTextBase, index: fullTextCatalog.indexes[2] }).error, /cannot be used safely/);
  assert.match(tools.buildFullTextSQL({ ...fullTextBase, query: "" }).error, /required/);
  assert.match(tools.buildFullTextSQL({ ...fullTextBase, limit: 101 }).error, /integer from 1 to 100/);
  assert.match(
    tools.buildFullTextSQL({ ...fullTextBase, query: "x".repeat(tools.MAX_FULLTEXT_EXPLORER_QUERY_CHARS + 1) }).error,
    /Explorer limit/,
  );

  const vectorDetail = {
    ...fullTextDetail,
    columns: {
      ...fullTextDetail.columns,
      rows: [
        ...fullTextDetail.columns.rows,
        ["articles", "sig", "4", "BITVECTOR<4>", "false", "false", ""],
        ["articles", "sparse", "5", "SPARSEVECTOR<8>", "false", "false", ""],
      ],
    },
    indexes: {
      ...fullTextDetail.indexes,
      rows: [
        ...fullTextDetail.indexes.rows,
        ["articles", "ix_embedding_hnsw", "vector", "false", "embedding", "", "", "valid"],
        ["articles", "ix_ambiguous_vector", "vector", "false", "embedding,sig", "", "", "valid"],
      ],
    },
  };
  const vectorCatalog = tools.vectorCatalog(vectorDetail);
  assert.deepEqual(vectorCatalog.columns.map((column) => column.name), ["embedding", "sig", "sparse"]);
  const embeddingColumn = vectorCatalog.columns[0];
  assert.deepEqual(embeddingColumn, { name: "embedding", type: "VECTOR<F32,3>", kind: "dense", dimensions: 3, metrics: ["cosine", "l2", "inner_product"] });
  assert.deepEqual(vectorCatalog.columns[1].metrics, ["hamming"], "BITVECTOR only supports HAMMING");
  assert.deepEqual(vectorCatalog.columns[2].metrics, ["cosine", "inner_product"], "SPARSEVECTOR rejects L2/HAMMING");
  assert.deepEqual(vectorCatalog.indexes[0], { name: "ix_embedding_hnsw", status: "valid", column: "embedding", usable: true });
  assert.equal(vectorCatalog.indexes[1].usable, false, "a multi-column catalog field is unrepresentable for a single-column vector index");

  assert.deepEqual(tools.parseVectorLiteralInput("1, 0, 0.5", embeddingColumn), { values: [1, 0, 0.5], error: null });
  assert.deepEqual(tools.parseVectorLiteralInput("[1, 0, 0.5]", embeddingColumn), { values: [1, 0, 0.5], error: null });
  assert.deepEqual(tools.parseVectorLiteralInput("(1, 0, 0.5)", embeddingColumn), { values: [1, 0, 0.5], error: null });
  assert.match(tools.parseVectorLiteralInput("1, 0", embeddingColumn).error, /declared with 3 dimensions/);
  assert.match(tools.parseVectorLiteralInput("1, x, 0", embeddingColumn).error, /not a finite number/);
  assert.match(tools.parseVectorLiteralInput("", embeddingColumn).error, /Enter a vector/);
  assert.match(tools.parseVectorLiteralInput("1, 2, 0, 3", vectorCatalog.columns[1]).error, /must be exactly 0 or 1/);
  assert.deepEqual(tools.parseVectorLiteralInput("1, 0, 0, 1", vectorCatalog.columns[1]), { values: [1, 0, 0, 1], error: null });

  assert.equal(tools.buildVectorLiteral([1, 0, 0.5]), "(1, 0, 0.5)");
  const vectorBase = { table: "articles", column: embeddingColumn, values: [1, 0, 0.5], metric: "cosine", topK: 10 };
  assert.deepEqual(tools.buildVectorSQL(vectorBase), {
    sql: `SELECT * FROM "articles" NEAREST "embedding" TO (1, 0, 0.5) USING COSINE LIMIT 10`,
    error: null,
  });
  assert.equal(
    tools.buildVectorSQL({ ...vectorBase, metric: "l2" }).sql,
    `SELECT * FROM "articles" NEAREST "embedding" TO (1, 0, 0.5) USING L2 LIMIT 10`,
  );
  assert.match(tools.buildVectorSQL({ ...vectorBase, metric: "hamming" }).error, /does not support the HAMMING metric/);
  assert.match(tools.buildVectorSQL({ ...vectorBase, column: undefined }).error, /Select a VECTOR/);
  assert.match(tools.buildVectorSQL({ ...vectorBase, values: null }).error, /valid vector/);
  assert.match(tools.buildVectorSQL({ ...vectorBase, topK: 0 }).error, /Top-K must be an integer/);
  assert.match(tools.buildVectorSQL({ ...vectorBase, topK: tools.MAX_VECTOR_EXPLORER_TOPK + 1 }).error, /Top-K must be an integer/);

  const hybridCatalog = tools.hybridCatalog(vectorDetail);
  assert.deepEqual(hybridCatalog.filterColumns.map((column) => column.name), ["id", "title", "body"], "JSON/vector/bitvector/sparse columns are excluded from the structured filter");
  assert.deepEqual(hybridCatalog.filterColumns[0], { name: "id", type: "INT64", kind: "numeric" });
  assert.deepEqual(hybridCatalog.filterColumns[1], { name: "title", type: "STRING", kind: "quoted" });
  assert.deepEqual(hybridCatalog.fullText.columns, ["title", "body"], "hybridCatalog reuses fullTextCatalog exactly");
  assert.deepEqual(hybridCatalog.vector.columns.map((column) => column.name), ["embedding", "sig", "sparse"], "hybridCatalog reuses vectorCatalog exactly");

  const idColumn = hybridCatalog.filterColumns[0];
  const titleColumn = hybridCatalog.filterColumns[1];
  assert.deepEqual(tools.buildHybridFilterClause({ column: undefined, operator: "=", value: "" }), { clause: null, error: null }, "no column selected means no filter, not an error");
  assert.deepEqual(tools.buildHybridFilterClause({ column: idColumn, operator: "=", value: "5" }), { clause: `"id" = 5`, error: null });
  assert.deepEqual(tools.buildHybridFilterClause({ column: titleColumn, operator: "<>", value: "x's" }), { clause: `"title" <> 'x''s'`, error: null });
  assert.deepEqual(tools.buildHybridFilterClause({ column: idColumn, operator: "is_null", value: "" }), { clause: `"id" IS NULL`, error: null });
  assert.deepEqual(tools.buildHybridFilterClause({ column: idColumn, operator: "is_not_null", value: "" }), { clause: `"id" IS NOT NULL`, error: null });
  assert.match(tools.buildHybridFilterClause({ column: idColumn, operator: "=", value: "" }).error, /Enter a filter value/);
  assert.match(tools.buildHybridFilterClause({ column: idColumn, operator: "=", value: "x" }).error, /not a finite number/);

  const hybridBase = {
    table: "articles",
    filter: { column: undefined, operator: "=", value: "" },
    fullTextColumns: ["title", "body"],
    fullTextIndex: undefined,
    query: "database performance",
    vectorColumn: embeddingColumn,
    vectorValues: [1, 0, 0.5],
    metric: "cosine",
    limit: 10,
  };
  assert.deepEqual(tools.buildHybridSQL(hybridBase), {
    sql: `SELECT * FROM "articles" SEARCH "title", "body" FOR 'database performance' NEAREST "embedding" TO (1, 0, 0.5) USING COSINE LIMIT 10`,
    error: null,
  });
  assert.equal(
    tools.buildHybridSQL({ ...hybridBase, filter: { column: idColumn, operator: ">", value: "10" } }).sql,
    `SELECT * FROM "articles" WHERE "id" > 10 SEARCH "title", "body" FOR 'database performance' NEAREST "embedding" TO (1, 0, 0.5) USING COSINE LIMIT 10`,
  );
  assert.match(tools.buildHybridSQL({ ...hybridBase, filter: { column: idColumn, operator: "=", value: "x" } }).error, /not a finite number/, "an invalid filter value fails before any SQL is built");
  assert.match(tools.buildHybridSQL({ ...hybridBase, fullTextColumns: [] }).error, /Select at least one STRING or TEXT column/);
  assert.match(tools.buildHybridSQL({ ...hybridBase, query: "" }).error, /Search phrase is required/);
  assert.match(tools.buildHybridSQL({ ...hybridBase, vectorColumn: undefined }).error, /Select a VECTOR/);
  assert.match(tools.buildHybridSQL({ ...hybridBase, vectorValues: null }).error, /valid vector/);
  assert.match(tools.buildHybridSQL({ ...hybridBase, metric: "hamming" }).error, /does not support the HAMMING metric/);
  assert.match(tools.buildHybridSQL({ ...hybridBase, limit: 0 }).error, /Result limit must be an integer/);

  const point = tools.parseGeo("POINT(-73.98 40.75)");
  assert.deepEqual(point.rings[0][0], { x: -73.98, y: 40.75 }, "fixed geo order is longitude, latitude");
  const box = tools.parseGeo("BOX(-1 -2, 3 4)");
  assert.equal(box.pointCount, 2);
  const line = tools.parseGeo("LINESTRING(0 0, 1 1, 2 1)");
  assert.equal(line.pointCount, 3);
  const polygon = tools.parseGeo("POLYGON((0 0, 1 0, 1 1, 0 0))");
  assert.equal(polygon.shape, "POLYGON");
  assert.equal(polygon.pointCount, 4);
  assert.deepEqual(polygon.bounds, { minX: 0, minY: 0, maxX: 1, maxY: 1 });
  assert.throws(() => tools.parseGeo("POLYGON((0 0, 1 0, 1 1, 0 1))"), /must be closed/);
  assert.throws(() => tools.parseGeo("POINT(181 0)"), /longitude\/latitude range/);

  const multiPoint = tools.parseGeo("MULTIPOINT((0 0), (1 1))");
  assert.equal(multiPoint.shape, "MULTIPOINT");
  assert.equal(multiPoint.parts.length, 2);
  assert.equal(multiPoint.pointCount, 2);
  const multiPointBare = tools.parseGeo("MULTIPOINT(0 0, 1 1)");
  assert.deepEqual(multiPointBare.bounds, multiPoint.bounds, "both MULTIPOINT spellings parse the same points");
  assert.equal(
    tools.parseGeo("MULTIPOINT(500 500)").bounds.maxX,
    500,
    "GEOMETRY/GEOGRAPHY coordinates are not range-checked against WGS84 degrees like the fixed shapes are",
  );

  const multiLine = tools.parseGeo("MULTILINESTRING((0 0, 1 1), (2 2, 3 3, 4 4))");
  assert.equal(multiLine.parts.length, 2);
  assert.equal(multiLine.parts[0].shape, "LINESTRING");
  assert.equal(multiLine.pointCount, 5);

  const multiPolygon = tools.parseGeo("MULTIPOLYGON(((0 0, 1 0, 1 1, 0 0)), ((5 5, 6 5, 6 6, 5 5)))");
  assert.equal(multiPolygon.parts.length, 2);
  assert.equal(multiPolygon.parts[0].shape, "POLYGON");

  const collection = tools.parseGeo("GEOMETRYCOLLECTION(POINT(1 2), LINESTRING(0 0, 1 1))");
  assert.equal(collection.shape, "GEOMETRYCOLLECTION");
  assert.equal(collection.parts.length, 2);
  assert.deepEqual(collection.parts.map((part) => part.shape), ["POINT", "LINESTRING"]);

  const nestedCollection = tools.parseGeo("GEOMETRYCOLLECTION(POINT(1 2), MULTIPOLYGON(((0 0, 1 0, 1 1, 0 0))))");
  assert.equal(nestedCollection.parts.length, 2, "a MULTIPOLYGON member flattens to its own leaf parts");
  assert.equal(nestedCollection.parts[1].shape, "POLYGON");

  assert.throws(() => tools.parseGeo("GEOMETRYCOLLECTION(EMPTY)"), /no coordinates/);
  assert.throws(() => tools.parseGeo("GEOMETRYCOLLECTION()"), /no coordinates/);
  assert.throws(() => tools.parseGeo("GEOMETRYCOLLECTION(NOTAGEOMETRY(1 2))"), /unrecognized geometry keyword/);
  assert.throws(() => tools.parseGeo("MULTIPOLYGON(((0 0, 1 0, 1 1, 0 1)))"), /must be closed/);
  assert.throws(() => tools.parseGeo("NOTAGEOMETRY(1 2)"), /no compact preview/);

  const geoDetail = {
    ...vectorDetail,
    columns: {
      ...vectorDetail.columns,
      rows: [
        ...vectorDetail.columns.rows,
        ["articles", "loc", "6", "POINT", "false", "false", ""],
      ],
    },
    indexes: {
      ...vectorDetail.indexes,
      rows: [
        ...vectorDetail.indexes.rows,
        ["articles", "ix_loc_spatial", "spatial", "false", "loc", "", "", "valid"],
        ["articles", "ix_ambiguous_spatial", "spatial", "false", "loc,embedding", "", "", "valid"],
      ],
    },
  };
  const geoCatalog = tools.geoCatalog(geoDetail);
  assert.deepEqual(geoCatalog.columns, [{ name: "loc", type: "POINT" }], "only POINT columns are offered");
  assert.deepEqual(geoCatalog.indexes[0], { name: "ix_loc_spatial", status: "valid", column: "loc", usable: true });
  assert.equal(geoCatalog.indexes[1].usable, false, "a multi-column catalog field is unrepresentable for a single-column spatial index");
  const locColumn = geoCatalog.columns[0];

  assert.equal(tools.validateGeoPoint({ lon: 200, lat: 0 }), "Longitude must be a number from -180 to 180.");
  assert.equal(tools.validateGeoPoint({ lon: 0, lat: -100 }), "Latitude must be a number from -90 to 90.");
  assert.equal(tools.validateGeoPoint({ lon: -73.9857, lat: 40.7484 }), null);

  assert.equal(tools.buildPointCallLiteral({ lon: -73.9857, lat: 40.7484 }), "POINT(-73.9857, 40.7484)");
  assert.equal(tools.pointPreviewWKT({ lon: -73.9857, lat: 40.7484 }), "POINT(-73.9857 40.7484)");
  assert.equal(
    tools.buildPolygonCallLiteral([{ lon: -74.1, lat: 40.6 }, { lon: -73.8, lat: 40.6 }, { lon: -73.8, lat: 40.9 }]),
    "POLYGON('((-74.1 40.6, -73.8 40.6, -73.8 40.9, -74.1 40.6))')",
    "the ring is auto-closed by repeating the first vertex",
  );
  assert.equal(
    tools.polygonPreviewWKT([{ lon: -74.1, lat: 40.6 }, { lon: -73.8, lat: 40.6 }, { lon: -73.8, lat: 40.9 }]),
    "POLYGON((-74.1 40.6, -73.8 40.6, -73.8 40.9, -74.1 40.6))",
  );
  // Round-trip: the SQL-generation literal must itself be a valid preview
  // once reformatted as WKT, and the exact ParseWKT/EvalGeo shape confirmed
  // against internal/sql/types/geo.go.
  assert.doesNotThrow(() => tools.parseGeo(tools.polygonPreviewWKT([{ lon: -74.1, lat: 40.6 }, { lon: -73.8, lat: 40.6 }, { lon: -73.8, lat: 40.9 }])));

  const geoPointBase = { table: "articles", column: locColumn, mode: "point_radius", point: { lon: -73.9857, lat: 40.7484 }, radiusMeters: 5000, polygon: [], limit: 10 };
  assert.deepEqual(tools.buildGeoSQL(geoPointBase), {
    sql: `SELECT * FROM "articles" WHERE DWITHIN("loc", POINT(-73.9857, 40.7484), 5000) LIMIT 10`,
    error: null,
  });
  assert.match(tools.buildGeoSQL({ ...geoPointBase, column: undefined }).error, /Select a POINT column/);
  assert.match(tools.buildGeoSQL({ ...geoPointBase, point: null }).error, /Click the map/);
  assert.match(tools.buildGeoSQL({ ...geoPointBase, point: { lon: 200, lat: 0 } }).error, /Longitude must be a number/);
  assert.match(tools.buildGeoSQL({ ...geoPointBase, radiusMeters: -1 }).error, /Radius must be a number/);
  assert.match(tools.buildGeoSQL({ ...geoPointBase, limit: 0 }).error, /Result limit must be an integer/);

  const geoPolygonBase = {
    table: "articles",
    column: locColumn,
    mode: "polygon",
    point: null,
    radiusMeters: null,
    polygon: [{ lon: -74.1, lat: 40.6 }, { lon: -73.8, lat: 40.6 }, { lon: -73.8, lat: 40.9 }, { lon: -74.1, lat: 40.9 }],
    limit: 10,
  };
  assert.deepEqual(tools.buildGeoSQL(geoPolygonBase), {
    sql: `SELECT * FROM "articles" WHERE WITHIN("loc", POLYGON('((-74.1 40.6, -73.8 40.6, -73.8 40.9, -74.1 40.9, -74.1 40.6))')) LIMIT 10`,
    error: null,
  });
  assert.match(tools.buildGeoSQL({ ...geoPolygonBase, polygon: geoPolygonBase.polygon.slice(0, 2) }).error, /at least three vertices/);
  assert.match(
    tools.buildGeoSQL({ ...geoPolygonBase, polygon: Array.from({ length: tools.MAX_GEO_EXPLORER_POLYGON_VERTICES + 1 }, (_, index) => ({ lon: index * 0.001, lat: 0 })) }).error,
    /supports at most/,
  );

  const timestamp = tools.timestampDetails("2024-01-01T01:30:00+01:30");
  assert.equal(timestamp.utc, "2024-01-01T00:00:00.000Z");
  assert.equal(timestamp.epochMilliseconds, 1704067200000);
  assert.throws(() => tools.timestampDetails("2024-01-01T00:00:00"), /explicit offset/);

  const filename = tools.resultFilename("Default / Query", "json", new Date("2024-01-02T03:04:05.006Z"));
  assert.equal(filename, "default-query-2024-01-02T03-04-05-006Z.json");

  const explainColumns = ["operator", "estimates", "actuals", "time", "cpu", "memory", "disk", "cache", "spill", "workers", "index"];
  const explainColumnTypes = explainColumns.map(() => "STRING");
  const notExplain = { columns: ["a", "b"], column_types: ["STRING", "STRING"], rows: [["1", "2"]], truncated: false, elapsed_ms: 1 };
  assert.equal(tools.isExplainResult(notExplain), false);
  assert.throws(() => tools.parseExplainPlan(notExplain), /not an EXPLAIN result/);

  // Mirrors real `EXPLAIN SELECT * FROM t WHERE n > 5 ORDER BY n LIMIT 2`
  // output captured against a live nextsqld: 2-space-per-depth indentation
  // baked into the operator cell, no actuals/time/cpu populated.
  const explainOnly = {
    columns: explainColumns,
    column_types: explainColumnTypes,
    rows: [
      ["Limit 2", "rows=2 cost=25720", "", "", "", "0", "0", "0", "0", "0", ""],
      ["  TopNSort fetch=2 1", "rows=2 cost=25720", "", "", "", "0", "0", "0", "0", "0", ""],
      ["    Project id, n", "rows=330 cost=15160", "", "", "", "0", "0", "0", "0", "0", ""],
      ["      Filter (n > 5)", "rows=330 cost=14500", "", "", "", "0", "0", "0", "0", "0", ""],
      ["        SeqScan t", "rows=330 cost=14500", "", "", "", "0", "0", "0", "0", "0", ""],
    ],
    truncated: false,
    elapsed_ms: 1,
  };
  assert.equal(tools.isExplainResult(explainOnly), true);
  const plan = tools.parseExplainPlan(explainOnly);
  assert.equal(plan.length, 1, "one root operator");
  assert.equal(plan[0].label, "Limit 2");
  assert.equal(plan[0].estRows, 2);
  assert.equal(plan[0].estCost, 25720);
  assert.equal(plan[0].actRows, null, "plain EXPLAIN has no actuals");
  assert.equal(tools.explainAnalyzed(plan), false);
  let node = plan[0];
  const chain = [node.label];
  while (node.children.length) { node = node.children[0]; chain.push(node.label); }
  assert.deepEqual(chain, ["Limit 2", "TopNSort fetch=2 1", "Project id, n", "Filter (n > 5)", "SeqScan t"], "linear plan chain, deepest last");

  // Mirrors real `EXPLAIN ANALYZE` output against the same query/live server:
  // actuals/time/cpu/workers populated, and the TopNSort row's actual (0)
  // vs. estimated (2) rows is a real order-of-magnitude-adjacent case (not
  // flagged) while a synthetic 50x-off row below is.
  const explainAnalyze = {
    columns: explainColumns,
    column_types: explainColumnTypes,
    rows: [
      ["Limit 2", "rows=2 cost=25720", "rows=2", "145µs", "145µs", "64", "0", "0", "0", "1", ""],
      ["  TopNSort fetch=2 1", "rows=2 cost=25720", "rows=0", "0ns", "0ns", "0", "0", "0", "0", "1", ""],
      ["    SeqScan t", "rows=330 cost=14500", "rows=3", "0ns", "0ns", "0", "0", "0", "0", "1", "idx_t"],
    ],
    truncated: false,
    elapsed_ms: 1,
  };
  const analyzedPlan = tools.parseExplainPlan(explainAnalyze);
  assert.equal(tools.explainAnalyzed(analyzedPlan), true);
  assert.equal(analyzedPlan[0].actRows, 2);
  assert.equal(analyzedPlan[0].time, "145µs");
  assert.equal(analyzedPlan[0].workers, 1);
  assert.equal(analyzedPlan[0].children[0].index, "");
  assert.equal(analyzedPlan[0].children[0].children[0].index, "idx_t");

  assert.equal(tools.explainEstimateSeverity(null, 5), "none", "unmeasured estimate never compared");
  assert.equal(tools.explainEstimateSeverity(5, null), "none", "unmeasured actual never compared");
  assert.equal(tools.explainEstimateSeverity(100, 100), "none");
  assert.equal(tools.explainEstimateSeverity(100, 350), "warning", "3.5x is a mild miss");
  assert.equal(tools.explainEstimateSeverity(100, 5000), "error", "50x is a real estimation error");
  assert.equal(tools.explainEstimateSeverity(0, 0), "none", "zero estimate matching zero actual is not an error");
  assert.equal(tools.explainEstimateSeverity(0, 10), "error", "zero estimate but rows actually produced is an error");

  const baselineSnapshot = tools.captureExplainPlan(analyzedPlan, 1_725_000_000_000);
  assert.equal(baselineSnapshot.nodeCount, 3);
  assert.equal(baselineSnapshot.analyzed, true);
  assert.equal(baselineSnapshot.capturedAt, 1_725_000_000_000);
  const priorRootLabel = analyzedPlan[0].label;
  analyzedPlan[0].label = "mutated after capture";
  assert.equal(baselineSnapshot.nodes[0].label, "Limit 2", "a pinned baseline must not alias the live parsed tree");
  analyzedPlan[0].label = priorRootLabel;

  const alternativePlan = tools.parseExplainPlan({
    columns: explainColumns,
    column_types: explainColumnTypes,
    rows: [
      ["Limit 2", "rows=2 cost=800", "rows=2", "80µs", "75µs", "32", "0", "4", "0", "1", ""],
      ["  IndexScan t", "rows=2 cost=700", "rows=2", "70µs", "65µs", "16", "0", "4", "0", "1", "idx_t_fast"],
      ["    Filter (n > 5)", "rows=2 cost=100", "rows=2", "5µs", "5µs", "0", "0", "0", "0", "1", ""],
      ["      BitmapIndex idx_t_fast", "rows=2 cost=50", "rows=2", "3µs", "3µs", "0", "0", "0", "0", "1", "idx_t_fast"],
    ],
    truncated: false,
    elapsed_ms: 1,
  });
  const currentSnapshot = tools.captureExplainPlan(alternativePlan, 1_725_000_001_000);
  const comparison = tools.compareExplainPlans(baselineSnapshot, currentSnapshot);
  assert.deepEqual(
    comparison.map((row) => [row.path, row.change]),
    [["1", "metrics changed"], ["1.1", "operator changed"], ["1.1.1", "operator changed"], ["1.1.1.1", "added"]],
    "plan comparison must align only by deterministic structural path and retain added operators",
  );
  assert.equal(comparison[1].baseline.label, "TopNSort fetch=2 1");
  assert.equal(comparison[1].current.label, "IndexScan t");
  assert.equal(comparison[3].baseline, null);
  assert.equal(comparison[3].current.index, "idx_t_fast");
  assert.throws(() => tools.captureExplainPlan([], 0), /empty plan/);
  const tooManyPlanNodes = Array.from({ length: tools.MAX_PLAN_COMPARISON_NODES + 1 }, (_, index) => ({
    ...alternativePlan[0], label: `root ${index}`, children: [],
  }));
  assert.throws(() => tools.captureExplainPlan(tooManyPlanNodes, 0), /at most 512 operators/);

  assert.equal(tools.parseExplainDurationNS("145µs"), 145_000);
  assert.equal(tools.parseExplainDurationNS("2ms"), 2_000_000);
  assert.equal(tools.parseExplainDurationNS("1s"), 1_000_000_000);
  assert.equal(tools.parseExplainDurationNS("1.5ms"), null, "only the server's integer duration format is accepted");
  assert.equal(tools.parseExplainDurationNS("unknown"), null);
  assert.throws(() => tools.buildExplainProfile(plan), /requires EXPLAIN ANALYZE/);
  const profile = tools.buildExplainProfile(alternativePlan);
  assert.equal(profile.nodeCount, 4);
  assert.deepEqual(profile.rows.map((row) => row.path), ["1", "1.1", "1.1.1", "1.1.1.1"]);
  assert.equal(profile.root.timeNS, 80_000);
  assert.equal(profile.slowestNonRoot.path, "1.1");
  assert.equal(profile.slowestNonRoot.timeNS, 70_000);
  assert.equal(profile.worstEstimate, null, "matching estimates should not manufacture a hotspot");
  assert.equal(profile.peakMemory.path, "1");
  assert.equal(profile.maxWorkers.node.workers, 1);
  const estimateMissProfile = tools.buildExplainProfile(analyzedPlan);
  assert.equal(estimateMissProfile.worstEstimate.path, "1.1");
  assert.equal(estimateMissProfile.worstEstimate.estimateFactor, Number.POSITIVE_INFINITY);
  assert.equal(estimateMissProfile.worstEstimate.estimateDirection, "lower");
  assert.throws(() => tools.buildExplainProfile(tooManyPlanNodes), /at most 512 operators/);

  // GRANT/REVOKE builder: quoting, per-scope shape, and required-field
  // validation. Every generated shape below was also confirmed against the
  // real internal/sql/parser (not just this frontend's own assumptions).
  assert.equal(tools.quoteIdentifier("orders"), '"orders"');
  assert.equal(tools.quoteIdentifier('a"b'), '"a""b"');

  const base = {
    action: "grant", mode: "privilege", roleName: "", grantee: "app",
    allPrivileges: false, privileges: ["select"], scope: "table",
    objectName: "orders", columnTable: "", columnName: "",
  };
  assert.deepEqual(tools.buildGrantSQL(base), { sql: 'GRANT SELECT ON TABLE "orders" TO "app"', error: null });
  assert.deepEqual(
    tools.buildGrantSQL({ ...base, action: "revoke" }),
    { sql: 'REVOKE SELECT ON TABLE "orders" FROM "app"', error: null },
  );
  assert.deepEqual(
    tools.buildGrantSQL({ ...base, privileges: ["select", "insert"] }),
    { sql: 'GRANT SELECT, INSERT ON TABLE "orders" TO "app"', error: null },
  );
  assert.deepEqual(
    tools.buildGrantSQL({ ...base, allPrivileges: true, privileges: [] }),
    { sql: 'GRANT ALL PRIVILEGES ON TABLE "orders" TO "app"', error: null },
  );
  assert.deepEqual(
    tools.buildGrantSQL({ ...base, scope: "cluster", objectName: "" }),
    { sql: 'GRANT SELECT ON CLUSTER TO "app"', error: null },
  );
  assert.deepEqual(
    tools.buildGrantSQL({ ...base, scope: "database", objectName: "" }),
    { sql: 'GRANT SELECT ON DATABASE TO "app"', error: null },
  );
  assert.deepEqual(
    tools.buildGrantSQL({ ...base, scope: "database", objectName: "sales" }),
    { sql: 'GRANT SELECT ON DATABASE "sales" TO "app"', error: null },
  );
  assert.deepEqual(
    tools.buildGrantSQL({ ...base, scope: "resourcegroup", objectName: "reporting" }),
    { sql: 'GRANT SELECT ON RESOURCE GROUP "reporting" TO "app"', error: null },
  );
  assert.deepEqual(
    tools.buildGrantSQL({ ...base, scope: "column", objectName: "", columnTable: "orders", columnName: "total" }),
    { sql: 'GRANT SELECT ON COLUMN "orders"."total" TO "app"', error: null },
  );
  assert.deepEqual(
    tools.buildGrantSQL({ ...base, scope: "column", objectName: "", columnTable: "", columnName: "total" }),
    { sql: 'GRANT SELECT ON COLUMN "total" TO "app"', error: null },
  );
  assert.deepEqual(
    tools.buildGrantSQL({ ...base, mode: "role", roleName: "analyst" }),
    { sql: 'GRANT "analyst" TO "app"', error: null },
  );
  assert.deepEqual(
    tools.buildGrantSQL({ ...base, mode: "role", roleName: "analyst", action: "revoke" }),
    { sql: 'REVOKE "analyst" FROM "app"', error: null },
  );
  // Every rejection is a validation error, not a malformed statement.
  assert.equal(tools.buildGrantSQL({ ...base, grantee: "  " }).error, "Grantee is required.");
  assert.equal(tools.buildGrantSQL({ ...base, mode: "role", roleName: "" }).error, "Role name is required.");
  assert.equal(tools.buildGrantSQL({ ...base, privileges: [] }).error, "Select at least one privilege, or ALL PRIVILEGES.");
  assert.equal(tools.buildGrantSQL({ ...base, scope: "schema", objectName: "" }).error, "Schema name is required.");
  assert.equal(tools.buildGrantSQL({ ...base, scope: "table", objectName: "" }).error, "Table name is required.");
  assert.equal(
    tools.buildGrantSQL({ ...base, scope: "column", objectName: "", columnName: "" }).error,
    "Column name is required.",
  );
  // "alter" is deliberately absent from the offered privilege list — see the
  // comment on GRANT_PRIVILEGES — so pin that the list never regresses to
  // include it (its keyword is reserved by ALTER TABLE and unsupported by
  // internal/sql/parser's GRANT/REVOKE grammar today).
  assert.ok(!tools.GRANT_PRIVILEGES.some((p) => p.value === "alter"), "alter must stay excluded until the parser accepts it");

  assert.deepEqual(
    tools.namesFromResult({ columns: ["name", "password_algo"], rows: [["app", "argon2id"], [null, "pbkdf2"], ["app", "argon2id"]] }, "name"),
    ["app"],
    "namesFromResult drops nulls/blanks and de-duplicates",
  );
  assert.deepEqual(tools.namesFromResult(null, "name"), []);
  assert.deepEqual(tools.namesFromResult({ columns: ["role"], rows: [] }, "missing-column"), []);

  // Users & roles explorer: grantStateFromRow reverses one real system.grants
  // row into a REVOKE-prefilled builder state — the exact inverse of
  // buildGrantSQL's own privilege/table/column shapes above.
  assert.deepEqual(
    tools.grantStateFromRow("app", "select", "table", "orders"),
    {
      action: "revoke", mode: "privilege", roleName: "", grantee: "app",
      allPrivileges: false, privileges: ["select"], scope: "table",
      objectName: "orders", columnTable: "", columnName: "",
    },
  );
  assert.equal(
    tools.buildGrantSQL(tools.grantStateFromRow("app", "select", "table", "orders")).sql,
    'REVOKE SELECT ON TABLE "orders" FROM "app"',
    "the reversed state must round-trip back through buildGrantSQL",
  );
  assert.deepEqual(
    tools.grantStateFromRow("app", "admin", "cluster", ""),
    {
      action: "revoke", mode: "privilege", roleName: "", grantee: "app",
      allPrivileges: false, privileges: ["admin"], scope: "cluster",
      objectName: "", columnTable: "", columnName: "",
    },
    "ALL PRIVILEGES persists as a single 'admin' privilege, not a synthetic ALL flag",
  );
  assert.deepEqual(
    tools.grantStateFromRow("app", "select", "column", "orders.total"),
    {
      action: "revoke", mode: "privilege", roleName: "", grantee: "app",
      allPrivileges: false, privileges: ["select"], scope: "column",
      objectName: "", columnTable: "orders", columnName: "total",
    },
    "a table-qualified column object must split back into columnTable/columnName",
  );
  assert.deepEqual(
    tools.grantStateFromRow("app", "select", "column", "total"),
    {
      action: "revoke", mode: "privilege", roleName: "", grantee: "app",
      allPrivileges: false, privileges: ["select"], scope: "column",
      objectName: "", columnTable: "", columnName: "total",
    },
    "a bare (scope-wide) column object must not be misread as a table name",
  );
  assert.equal(
    tools.grantStateFromRow("app", "select", "not-a-real-scope", "x").scope,
    "table",
    "an unrecognized scope spelling must fall back to a selectable scope, not an inert one",
  );

  // Find/replace: literal-substring matching (never regex), wrap-around
  // navigation, and a single-pass replace-all that never re-scans its own
  // output.
  const findText = "SELECT id FROM t WHERE id = 1 OR id = 2";
  assert.deepEqual(tools.findAllMatches(findText, "id", false), [
    { start: 7, end: 9 }, { start: 23, end: 25 }, { start: 33, end: 35 },
  ]);
  assert.deepEqual(tools.findAllMatches(findText, "ID", false), tools.findAllMatches(findText, "id", false), "case-insensitive by default");
  assert.deepEqual(tools.findAllMatches(findText, "ID", true), [], "match-case must not find a different-case query");
  assert.deepEqual(tools.findAllMatches(findText, "", false), [], "an empty query matches nothing");
  assert.deepEqual(tools.findAllMatches("aaaa", "aa", false), [{ start: 0, end: 2 }, { start: 2, end: 4 }], "matches must not overlap");
  assert.ok(
    tools.findAllMatches("a".repeat(20_000), "a", false).length <= tools.MAX_FIND_MATCHES,
    "match count must stay bounded on a pathological query",
  );

  const idMatches = tools.findAllMatches(findText, "id", false);
  assert.equal(tools.nextMatchIndex(idMatches, 0), 0);
  assert.equal(tools.nextMatchIndex(idMatches, 8), 1, "searching from inside a match finds the following one");
  assert.equal(tools.nextMatchIndex(idMatches, 33), 2, "searching from exactly a match's start finds that match");
  assert.equal(tools.nextMatchIndex(idMatches, 36), 0, "Find Next wraps around past the last match");
  assert.equal(tools.nextMatchIndex([], 0), null, "no matches at all");
  assert.equal(tools.previousMatchIndex(idMatches, 40), 2);
  assert.equal(tools.previousMatchIndex(idMatches, 24), 1, "searching backward from exactly a match's start finds that match");
  assert.equal(tools.previousMatchIndex(idMatches, 20), 0, "searching backward from between two matches finds the preceding one");
  assert.equal(tools.previousMatchIndex(idMatches, 0), 2, "Find Previous wraps around past the first match");
  assert.equal(tools.previousMatchIndex([], 0), null);

  assert.equal(tools.replaceAllMatches(findText, idMatches, "pk"), "SELECT pk FROM t WHERE pk = 1 OR pk = 2");
  assert.equal(
    tools.replaceAllMatches("id id id", tools.findAllMatches("id id id", "id", false), "idid"),
    "idid idid idid",
    "a replacement containing the search text must not be re-matched",
  );
  assert.equal(tools.replaceAllMatches(findText, [], "pk"), findText, "no matches means no change");

  // Catalog-aware IntelliSense: FROM/JOIN table extraction, word-range
  // detection, ranking, and the bounded fetch-cache reducer.
  assert.deepEqual(
    tools.extractReferencedTables("SELECT * FROM orders o JOIN customers c ON o.customer_id = c.id"),
    ["orders", "customers"],
  );
  assert.deepEqual(
    tools.extractReferencedTables("select * from system.capabilities"),
    ["system.capabilities"],
    "a schema-qualified bare identifier is recognized",
  );
  assert.deepEqual(
    tools.extractReferencedTables('SELECT * FROM "weird name"'),
    [],
    "a quoted FROM/JOIN target is not recognized — only a bare identifier",
  );
  assert.deepEqual(
    tools.extractReferencedTables("SELECT * FROM t JOIN t ON 1=1"),
    ["t"],
    "the same table referenced twice is deduplicated",
  );
  {
    const many = "SELECT * FROM t0 " + Array.from({ length: 20 }, (_, i) => `JOIN t${i + 1} ON 1=1`).join(" ");
    assert.equal(
      tools.extractReferencedTables(many).length,
      tools.MAX_REFERENCED_TABLES,
      "referenced-table extraction must stay bounded",
    );
  }

  assert.deepEqual(tools.currentWordRange("SELECT ord FROM t", 10), { start: 7, end: 10 }, "cursor at the end of a word");
  assert.deepEqual(tools.currentWordRange("SELECT ord FROM t", 8), { start: 7, end: 10 }, "cursor inside a word extends both directions");
  assert.deepEqual(tools.currentWordRange("SELECT  FROM t", 7), { start: 7, end: 7 }, "cursor between spaces has an empty word range");
  assert.deepEqual(tools.currentWordRange("orders", 0), { start: 0, end: 6 }, "cursor at position zero");

  const tableCatalog = ["orders", "order_items", "customers"];
  const orderSuggestions = tools.rankSQLSuggestions("ord", tableCatalog, [], {});
  assert.deepEqual(orderSuggestions.map((s) => s.insertText), ["order_items", "orders"], "table matches are prefix-filtered and sorted");
  assert.ok(orderSuggestions.every((s) => s.kind === "table"));

  const withColumns = tools.rankSQLSuggestions(
    "cu",
    tableCatalog,
    ["orders", "customers"],
    { orders: ["customer_id", "id"], customers: "loading" },
  );
  assert.deepEqual(
    withColumns.map((s) => `${s.kind}:${s.insertText}`),
    ["table:customers", "column:customer_id"],
    "columns are only offered for tables whose cache entry has already resolved to an array",
  );

  assert.deepEqual(tools.rankSQLSuggestions("nope", tableCatalog, [], {}), [], "no match yields no suggestions");
  {
    const manyTables = Array.from({ length: 100 }, (_, i) => `zzz${i}`);
    assert.equal(
      tools.rankSQLSuggestions("zzz", manyTables, [], {}).length,
      tools.MAX_SQL_SUGGESTIONS,
      "the ranked suggestion list must stay bounded",
    );
  }

  {
    let state = { cache: {}, order: [] };
    state = tools.withCachedTableLoading(state.cache, state.order, "a");
    assert.deepEqual(state.cache, { a: "loading" });
    state = tools.withCachedTableLoading(state.cache, state.order, "a");
    assert.deepEqual(state.cache, { a: "loading" }, "an already-present table is a no-op, not re-queued");
    assert.deepEqual(state.order, ["a"]);
  }
  {
    // Fill exactly to the cap, then confirm the next distinct table evicts
    // the oldest rather than growing unbounded.
    let state = { cache: {}, order: [] };
    for (let i = 0; i < tools.MAX_INTELLISENSE_TABLE_CACHE; i++) {
      state = tools.withCachedTableLoading(state.cache, state.order, `t${i}`);
    }
    assert.equal(Object.keys(state.cache).length, tools.MAX_INTELLISENSE_TABLE_CACHE);
    state = tools.withCachedTableLoading(state.cache, state.order, "overflow");
    assert.equal(Object.keys(state.cache).length, tools.MAX_INTELLISENSE_TABLE_CACHE, "cache must stay bounded");
    assert.equal("t0" in state.cache, false, "the oldest-inserted table is evicted first");
    assert.equal("overflow" in state.cache, true);
  }

  // Deterministic misspelled table-name suggestions.
  assert.equal(tools.levenshteinDistance("orders", "orders"), 0);
  assert.equal(tools.levenshteinDistance("orders", "order"), 1, "one deletion");
  assert.equal(tools.levenshteinDistance("orders", "ordrs"), 1, "one deletion mid-word");
  assert.equal(tools.levenshteinDistance("orders", "custmers"), 5);
  assert.equal(tools.levenshteinDistance("", "abc"), 3);
  assert.equal(tools.levenshteinDistance("abc", ""), 3);

  {
    const fixes = tools.suggestTableNameFixes("SELECT * FROM ordrs WHERE id = 1", tableCatalog, false);
    assert.equal(fixes.length, 1);
    assert.equal(fixes[0].badName, "ordrs");
    assert.equal(fixes[0].suggestion, "orders");
    assert.deepEqual(fixes[0].occurrences, [{ start: 14, end: 19 }], "occurrence span covers exactly the bad identifier");
    assert.equal(
      tools.applyTableNameFix("SELECT * FROM ordrs WHERE id = 1", fixes[0]),
      "SELECT * FROM orders WHERE id = 1",
    );
  }
  assert.deepEqual(
    tools.suggestTableNameFixes("SELECT * FROM orders", tableCatalog, false),
    [],
    "a real table name is never flagged",
  );
  assert.deepEqual(
    tools.suggestTableNameFixes("SELECT * FROM system.capabilities", tableCatalog, false),
    [],
    "a schema-qualified target is never flagged — there is no ground-truth list to check it against",
  );
  assert.deepEqual(
    tools.suggestTableNameFixes("SELECT * FROM zzzzzzzzzz", tableCatalog, false),
    [],
    "a name too far from every real table gets no suggestion, not a wild guess",
  );
  assert.deepEqual(
    tools.suggestTableNameFixes("SELECT * FROM ordrs", tableCatalog, true),
    [],
    "flagging is disabled outright while the bootstrap's own table list is truncated",
  );
  {
    // "orderz" is edit-distance 1 from both "orders" and... construct two
    // equally-close real names on purpose to prove a tie is never guessed.
    const tied = tools.suggestTableNameFixes("SELECT * FROM orderz", ["orders", "orderx"], false);
    assert.deepEqual(tied, [], "a tie between two equally-close real names is never resolved by guessing");
  }
  {
    // Seven distinct real tables, each with its own one-edit-away typo and
    // no cross-matches between them, so the cap below is exercised by real
    // findings rather than by mutual ties.
    const manyReal = ["apple", "brick", "candy", "dodge", "eagle", "frost", "grape"];
    const manyBadQuery = "SELECT * FROM aple JOIN brik JOIN candi JOIN dodg JOIN eagl JOIN frst JOIN grap ON 1=1";
    assert.equal(
      tools.suggestTableNameFixes(manyBadQuery, manyReal, false).length,
      tools.MAX_TABLE_NAME_FIXES,
      "the fix list must stay bounded",
    );
  }
  {
    const twice = tools.suggestTableNameFixes("SELECT * FROM ordrs JOIN ordrs ON 1=1", tableCatalog, false);
    assert.equal(twice.length, 1, "the same bad name is reported once");
    assert.equal(twice[0].occurrences.length, 2, "but every occurrence is recorded for the fix to rewrite");
    assert.equal(
      tools.applyTableNameFix("SELECT * FROM ordrs JOIN ordrs ON 1=1", twice[0]),
      "SELECT * FROM orders JOIN orders ON 1=1",
    );
  }

  // JSON-path completion where metadata is known.
  {
    const indexes = {
      columns: ["table_name", "index_name", "kind", "is_unique", "columns", "include_columns", "predicate", "status"],
      rows: [
        ["articles", "PRIMARY", "btree", "true", "id", "", "", "valid"],
        ["articles", "ix_first_tag", "btree", "false", "metadata.tags.0", "", "", "valid"],
        ["articles", "ix_category", "btree", "false", "metadata.category", "", "", "valid"],
        ["articles", "ix_composite", "btree", "false", "a.b,c.d", "", "", "valid"],
      ],
    };
    assert.deepEqual(
      tools.jsonPathIndexPaths(indexes),
      ["metadata.tags.0", "metadata.category"],
      "only single-column dotted-path index targets are JSON paths (composite and plain-column indexes are skipped)",
    );
    assert.deepEqual(tools.jsonPathIndexPaths({ columns: ["x"], rows: [] }), [], "no columns field / no rows yields nothing");
  }
  {
    assert.equal(tools.currentJSONPathRange("SELECT id FROM articles", 8), null, "a plain identifier is not a JSON-path context");
    const afterDot = tools.currentJSONPathRange("SELECT metadata. FROM articles", 16);
    assert.deepEqual(afterDot, { start: 7, end: 16, typed: "metadata." }, "the caret right after a dot is a JSON-path context");
    const partial = tools.currentJSONPathRange("SELECT metadata.ca FROM articles", 18);
    assert.deepEqual(partial, { start: 7, end: 18, typed: "metadata.ca" }, "a partial segment is captured, caret does not extend past a further dot");
    const midPath = tools.currentJSONPathRange("SELECT metadata.tags.0 FROM t", 21);
    assert.deepEqual(midPath, { start: 7, end: 22, typed: "metadata.tags." }, "typed text is only up to the caret; the range still covers the whole path");
    assert.equal(tools.currentJSONPathRange("SELECT .bad FROM t", 8), null, "a leading dot is not a native path shape");
  }
  {
    const cache = { articles: ["metadata.tags.0", "metadata.category"], other: "loading" };
    assert.deepEqual(
      tools.rankJSONPathSuggestions("metadata.", ["articles"], cache).map((s) => s.insertText),
      ["metadata.category", "metadata.tags.0"],
      "every indexed path under the typed prefix is offered, sorted",
    );
    assert.deepEqual(
      tools.rankJSONPathSuggestions("metadata.ca", ["articles"], cache).map((s) => s.insertText),
      ["metadata.category"],
      "prefix narrows the offered paths",
    );
    assert.deepEqual(
      tools.rankJSONPathSuggestions("metadata.category", ["articles"], cache).map((s) => s.insertText),
      [],
      "an exact match offers nothing more to complete",
    );
    assert.deepEqual(
      tools.rankJSONPathSuggestions("metadata.", ["missing"], cache),
      [],
      "a table whose index metadata has not resolved contributes no paths, never a guess",
    );
    assert.ok(
      tools.rankJSONPathSuggestions("metadata.", ["articles"], cache).every((s) => s.kind === "json-path"),
      "JSON-path suggestions carry their own kind",
    );
  }

  {
    // Vector-aware completion: NEAREST column + USING metric from catalog
    // column kind. Never completes inside TO (...).
    const columns = {
      columns: ["column_name", "type"],
      rows: [
        ["id", "INT64"],
        ["embedding", "VECTOR<F32,3>"],
        ["bits", "BITVECTOR<8>"],
        ["sparse", "SPARSEVECTOR<16>"],
        ["title", "STRING"],
      ],
    };
    const vec = tools.vectorColumnsFromResult(columns);
    assert.deepEqual(vec.map((c) => c.name), ["embedding", "bits", "sparse"], "only vector-typed columns are offered");
    assert.deepEqual(vec[0].metrics, ["cosine", "l2", "inner_product"]);
    assert.deepEqual(vec[1].metrics, ["hamming"]);
    assert.deepEqual(vec[2].metrics, ["cosine", "inner_product"]);
    assert.deepEqual(tools.vectorColumnsFromResult({ columns: ["x"], rows: [] }), [], "a result without column_name/type yields nothing");

    const afterNearest = "SELECT * FROM articles NEAREST ";
    const colCtx = tools.currentNearestContext(afterNearest, afterNearest.length);
    assert.deepEqual(colCtx, { slot: "column", start: afterNearest.length, end: afterNearest.length, typed: "" }, "caret after NEAREST is a vector-column slot");
    const partial = "SELECT * FROM articles NEAREST emb";
    const partialCtx = tools.currentNearestContext(partial, partial.length);
    assert.equal(partialCtx?.slot, "column");
    assert.equal(partialCtx?.typed, "emb");
    assert.equal(tools.currentNearestContext("SELECT * FROM articles WHERE id = 1", 20), null, "a non-NEAREST caret is not a vector slot");
    const createUsing = "CREATE VECTOR INDEX x ON t (embedding) USING ";
    assert.equal(tools.currentNearestContext(createUsing, createUsing.length), null, "CREATE INDEX USING is not a NEAREST metric slot");

    const afterUsing = "SELECT * FROM articles NEAREST embedding TO (1, 0, 0.5) USING ";
    const metricCtx = tools.currentNearestContext(afterUsing, afterUsing.length);
    assert.equal(metricCtx?.slot, "metric");
    assert.equal(metricCtx?.column, "embedding");
    assert.equal(metricCtx?.typed, "");
    const prior = "SELECT * FROM t NEAREST embedding TO (1) USING COSINE; SELECT 1 USING ";
    assert.equal(tools.currentNearestContext(prior, prior.length), null, "USING in a later statement does not attach to an earlier NEAREST");

    const cache = { articles: vec };
    assert.deepEqual(
      tools.rankNearestColumnSuggestions("", ["articles"], cache).map((s) => s.insertText),
      ["bits", "embedding", "sparse"],
      "an empty prefix lists every vector column, sorted",
    );
    assert.deepEqual(
      tools.rankNearestColumnSuggestions("emb", ["articles"], cache).map((s) => s.insertText),
      ["embedding"],
      "prefix narrows to the matching vector column",
    );
    assert.deepEqual(
      tools.rankNearestColumnSuggestions("embedding", ["articles"], cache),
      [],
      "an exact match offers nothing more to complete",
    );
    assert.deepEqual(
      tools.rankNearestColumnSuggestions("", ["missing"], cache),
      [],
      "a table whose vector metadata has not resolved contributes nothing, never a guess",
    );
    assert.ok(
      tools.rankNearestColumnSuggestions("", ["articles"], cache).every((s) => s.kind === "vector-column"),
      "vector-column suggestions carry their own kind",
    );

    assert.deepEqual(
      tools.rankNearestMetricSuggestions("", "embedding", ["articles"], cache).map((s) => s.insertText),
      ["COSINE", "L2", "INNER_PRODUCT"],
      "a dense VECTOR column offers exactly the three real-valued metrics",
    );
    assert.deepEqual(
      tools.rankNearestMetricSuggestions("", "bits", ["articles"], cache).map((s) => s.insertText),
      ["HAMMING"],
      "a BITVECTOR column offers only HAMMING",
    );
    assert.deepEqual(
      tools.rankNearestMetricSuggestions("C", "embedding", ["articles"], cache).map((s) => s.insertText),
      ["COSINE"],
      "metric prefix is case-insensitive against the SQL spelling",
    );
    assert.deepEqual(
      tools.rankNearestMetricSuggestions("", "title", ["articles"], cache),
      [],
      "a non-vector column name yields no metric list, never every metric",
    );
    assert.deepEqual(
      tools.rankNearestMetricSuggestions("", "embedding", ["missing"], cache),
      [],
      "an unresolved table contributes no metrics",
    );
  }

  {
    // Unified table-constraint view: the four constraint shapes come from
    // their existing authorized catalog sources, not a guessed new schema.
    const constraintDetail = {
      columns: {
        columns: ["table_name", "column_name", "ordinal", "type", "not_null", "is_primary", "default_value"],
        rows: [
          ["orders", "id", "2", "INT64", "true", "true", ""],
          ["orders", "region", "1", "STRING", "true", "true", ""],
          ["orders", "external_id", "3", "STRING", "false", "false", ""],
        ],
      },
      indexes: {
        columns: ["table_name", "index_name", "kind", "is_unique", "columns", "include_columns", "predicate", "status"],
        rows: [
          ["orders", "PRIMARY", "btree", "true", "region,id", "", "", "valid"],
          ["orders", "uq_orders_external", "btree", "true", "external_id", "id", "external_id IS NOT NULL", "building"],
          ["orders", "ix_orders_region", "btree", "false", "region", "", "", "valid"],
        ],
      },
      foreign_keys: {
        columns: ["table_name", "constraint_name", "ordinal", "column_name", "ref_table", "ref_column", "on_delete", "on_update"],
        rows: [
          ["orders", "fk_orders_tenant", "2", "id", "tenants", "order_id", "CASCADE", "RESTRICT"],
          ["orders", "fk_orders_tenant", "1", "region", "tenants", "region", "CASCADE", "RESTRICT"],
        ],
      },
    };
    const constraints = tools.tableConstraintsResult(constraintDetail);
    assert.deepEqual(constraints.rows, [
      ["PRIMARY KEY", "PRIMARY", "region, id", "Declared by system.columns"],
      ["UNIQUE INDEX", "uq_orders_external", "external_id", "INCLUDE id WHERE external_id IS NOT NULL status building"],
      ["NOT NULL", "region", "region", "Declared by system.columns"],
      ["NOT NULL", "id", "id", "Declared by system.columns"],
      ["FOREIGN KEY", "fk_orders_tenant", "region, id", "REFERENCES tenants (region, order_id) ON DELETE CASCADE ON UPDATE RESTRICT"],
    ], "primary/unique/not-null/FK metadata is unified in stable source order");
    assert.equal(constraints.truncated, false);
    assert.equal(constraints.rows.filter((row) => row[1] === "PRIMARY").length, 1, "a surfaced PRIMARY backing index is not duplicated");

    const emptyConstraints = tools.tableConstraintsResult({
      columns: { columns: ["column_name", "not_null", "is_primary"], rows: [["note", "false", "false"]] },
      indexes: { columns: ["index_name", "is_unique", "columns"], rows: [] },
      foreign_keys: { columns: ["constraint_name", "column_name", "ref_table", "ref_column"], rows: [] },
    });
    assert.deepEqual(emptyConstraints.rows, [], "a table with no declarations yields an empty panel, not invented metadata");

    const manyNotNull = Array.from({ length: tools.MAX_TABLE_CONSTRAINT_ROWS + 3 }, (_, i) => [`c${i}`, String(i), "true", "false"]);
    const bounded = tools.tableConstraintsResult({
      columns: { columns: ["column_name", "ordinal", "not_null", "is_primary"], rows: manyNotNull },
      indexes: { columns: ["index_name", "is_unique", "columns"], rows: [] },
      foreign_keys: { columns: ["constraint_name", "column_name", "ref_table", "ref_column"], rows: [] },
    });
    assert.equal(bounded.rows.length, tools.MAX_TABLE_CONSTRAINT_ROWS, "derived constraint rows are bounded");
    assert.equal(bounded.truncated, true, "the caller can disclose a bounded constraint preview");
  }

  {
    // Workflow relationships: named catalog columns -> bounded deterministic
    // TABLE -> TRIGGER -> WORKFLOW / SCHEDULE -> WORKFLOW graph.
    const rs = (columns, rows, truncated = false) => ({
      columns,
      column_types: columns.map(() => "STRING"),
      rows,
      truncated,
      elapsed_ms: 1,
    });
    const workflows = rs(["name", "owner"], [
      ["touch_articles", "operator"],
      ["rollup_daily", "operator"],
      ["isolated", "operator"],
    ]);
    const triggers = rs(
      ["name", "timing", "event", "table_name", "workflow"],
      [
        ["articles_after_insert", "AFTER", "INSERT", "articles", "touch_articles"],
        ["malformed", "AFTER", "UPDATE", null, "touch_articles"],
      ],
    );
    const schedules = rs(
      ["name", "kind", "spec", "workflow", "enabled"],
      [["nightly", "EVERY", "24h0m0s", "rollup_daily", "true"]],
    );
    const model = tools.buildWorkflowRelationships(workflows, triggers, schedules);
    assert.deepEqual(model.workflows, ["isolated", "rollup_daily", "touch_articles"], "listed and referenced workflows are deduplicated and sorted");
    assert.equal(model.relationships.length, 2, "malformed catalog rows are omitted, never guessed");
    assert.equal(
      tools.workflowRelationshipLabel(model.relationships[0]),
      "nightly (EVERY 24h0m0s, enabled) → rollup_daily",
      "schedule labels preserve the native kind/spec and enabled state",
    );
    assert.equal(
      tools.workflowRelationshipLabel(model.relationships[1]),
      "articles → articles_after_insert (AFTER INSERT) → touch_articles",
      "trigger labels preserve the table/timing/event path",
    );
    const layout = tools.layoutWorkflowRelationships(model);
    assert.ok(layout, "a small complete relationship graph lays out");
    assert.equal(layout.nodes.length, 6, "workflow, table, trigger, and schedule nodes are all represented");
    assert.equal(layout.links.length, 3, "a trigger contributes two links and a schedule one");
    const layer = Object.fromEntries(layout.nodes.map((node) => [`${node.kind}:${node.name}`, node.x]));
    assert.ok(layer["table:articles"] < layer["trigger:articles_after_insert"], "the trigger follows its table");
    assert.ok(layer["trigger:articles_after_insert"] < layer["workflow:touch_articles"], "the workflow follows its trigger");
    assert.match(tools.workflowRelationshipSummary(model), /3 workflows, 1 trigger, 1 schedule/, "the summary is the SVG text label");

    const tooManyWorkflows = rs(
      ["name"],
      Array.from({ length: tools.MAX_WORKFLOW_DIAGRAM_NODES + 1 }, (_, i) => [`workflow_${i}`]),
    );
    assert.equal(
      tools.layoutWorkflowRelationships(tools.buildWorkflowRelationships(tooManyWorkflows, null, null)),
      null,
      "an oversized graph is not drawn",
    );
    const manyTriggers = rs(
      ["name", "table_name", "workflow"],
      Array.from({ length: tools.MAX_WORKFLOW_RELATIONSHIPS + 3 }, (_, i) => [`trigger_${i}`, `table_${i}`, `workflow_${i}`]),
    );
    const bounded = tools.buildWorkflowRelationships(null, manyTriggers, null);
    assert.equal(bounded.relationships.length, tools.MAX_WORKFLOW_RELATIONSHIPS, "the text alternative is bounded too");
    assert.equal(bounded.truncated, true, "the caller can disclose a capped relationship list");
    assert.equal(tools.layoutWorkflowRelationships(bounded), null, "a partial graph is never drawn as though complete");
  }

  {
    // Schema-relationship graph: flat system.foreign_keys rows -> edges + layers.
    const fkColumns = ["table_name", "constraint_name", "ordinal", "column_name", "ref_table", "ref_column", "on_delete", "on_update"];
    const schemaFKs = {
      columns: fkColumns,
      column_types: fkColumns.map(() => "STRING"),
      rows: [
        ["orders", "fk_orders_customer", "1", "customer_id", "customers", "id", "CASCADE", "RESTRICT"],
        ["order_lines", "fk_lines_order", "1", "order_id", "orders", "id", "CASCADE", "RESTRICT"],
        ["order_lines", "fk_lines_product", "1", "product_id", "products", "id", "RESTRICT", "RESTRICT"],
        // composite FK: two rows, one edge
        ["shipments", "fk_ship_line", "1", "order_id", "order_lines", "order_id", "CASCADE", "RESTRICT"],
        ["shipments", "fk_ship_line", "2", "line_no", "order_lines", "line_no", "CASCADE", "RESTRICT"],
      ],
      truncated: false,
      elapsed_ms: 1,
    };
    const model = tools.buildSchemaGraph(schemaFKs);
    assert.deepEqual(model.tables, ["customers", "order_lines", "orders", "products", "shipments"], "every table on either side of an edge appears once, sorted");
    assert.equal(model.edges.length, 4, "a composite FK collapses to one edge");
    const shipEdge = model.edges.find((e) => e.constraint === "fk_ship_line");
    assert.deepEqual(shipEdge.columns, ["order_id", "line_no"], "composite child columns are kept in ordinal order");
    assert.deepEqual(shipEdge.refColumns, ["order_id", "line_no"], "composite referenced columns are kept in ordinal order");
    assert.deepEqual(model.byParent.map((g) => g.parent), ["customers", "order_lines", "orders", "products"], "relationships are grouped by referenced table, sorted");

    const layout = tools.layoutSchemaGraph(model);
    assert.ok(layout, "a small graph lays out");
    assert.equal(layout.nodes.length, 5, "every table gets a node");
    assert.equal(layout.links.length, 4, "every edge gets a link");
    const nodeX = Object.fromEntries(layout.nodes.map((n) => [n.name, n.x]));
    assert.ok(nodeX.customers < nodeX.orders, "a referenced table sits left of the table referencing it");
    assert.ok(nodeX.orders < nodeX.order_lines, "dependency depth increases left to right");
    assert.ok(nodeX.order_lines < nodeX.shipments, "a two-hop dependency is two layers right");
    assert.ok(layout.links.every((l) => l.d.startsWith("M ")), "each link is an SVG path");

    // Cycle: a <-> b must not hang or throw.
    const cyclic = tools.buildSchemaGraph({
      columns: fkColumns,
      column_types: fkColumns.map(() => "STRING"),
      rows: [
        ["a", "fk_a_b", "1", "b_id", "b", "id", "RESTRICT", "RESTRICT"],
        ["b", "fk_b_a", "1", "a_id", "a", "id", "RESTRICT", "RESTRICT"],
      ],
    });
    const cyclicLayout = tools.layoutSchemaGraph(cyclic);
    assert.ok(cyclicLayout && cyclicLayout.nodes.length === 2, "a foreign-key cycle still lays out");

    assert.equal(tools.layoutSchemaGraph(tools.buildSchemaGraph(null)), null, "an empty graph has no layout");
    const many = { columns: fkColumns, column_types: fkColumns.map(() => "STRING"), rows: [] };
    for (let i = 0; i < tools.MAX_SCHEMA_DIAGRAM_TABLES + 2; i++) {
      many.rows.push([`t${i}`, `fk_${i}`, "1", "p", "hub", "id", "RESTRICT", "RESTRICT"]);
    }
    assert.equal(tools.layoutSchemaGraph(tools.buildSchemaGraph(many)), null, "an oversized schema returns no layout (caller shows the list only)");
    assert.match(tools.schemaGraphSummary(model), /5 tables, 4 references/, "the summary is the SVG's text label");
  }

  {
    // Global object search ranking.
    const objects = [
      { name: "orders", kind: "table" },
      { name: "order_lines", kind: "table" },
      { name: "customers", kind: "table" },
      { name: "rollup_orders", kind: "workflow" },
      { name: "audit_log", kind: "table" },
    ];
    assert.deepEqual(
      tools.rankObjectMatches("order", objects).map((m) => `${m.kind}:${m.name}`),
      ["table:orders", "table:order_lines", "workflow:rollup_orders"],
      "exact/prefix beat substring; shorter name wins a tie",
    );
    assert.deepEqual(
      tools.rankObjectMatches("alog", objects).map((m) => m.name),
      ["audit_log"],
      "an in-order subsequence still matches",
    );
    assert.deepEqual(tools.rankObjectMatches("zzz", objects), [], "no match, no guess");
    assert.equal(tools.rankObjectMatches("", objects).length, objects.length, "an empty query lists everything, sorted");
    assert.deepEqual(
      tools.rankObjectMatches("", objects).map((m) => m.name),
      ["audit_log", "customers", "order_lines", "orders", "rollup_orders"],
      "the empty-query list is name-sorted",
    );
    const many = Array.from({ length: tools.MAX_OBJECT_SEARCH_RESULTS + 10 }, (_, i) => ({ name: `t_${i}`, kind: "table" }));
    assert.equal(tools.rankObjectMatches("t_", many).length, tools.MAX_OBJECT_SEARCH_RESULTS, "results are capped");
  }

  {
    // Crash-recovery draft codec.
    const drafts = { tabs: [{ title: "Query 1", sql: "SELECT 1" }, { title: "Query 2", sql: "SELECT 2" }], activeIndex: 1 };
    const round = tools.parseEditorDrafts(tools.serializeEditorDrafts(drafts.tabs, drafts.activeIndex));
    assert.deepEqual(round, drafts, "drafts round-trip through serialize/parse");
    assert.equal(tools.parseEditorDrafts("not json"), null, "garbage parses to null, not a throw");
    assert.equal(tools.parseEditorDrafts('{"tabs":[]}'), null, "an empty tab list is treated as no draft");
    assert.equal(tools.parseEditorDrafts('{"tabs":[{"title":"x"}]}'), null, "a tab with no sql string is dropped");
    const clamped = tools.parseEditorDrafts(tools.serializeEditorDrafts(drafts.tabs, 99));
    assert.equal(clamped.activeIndex, drafts.tabs.length - 1, "an out-of-range active index clamps to the last tab");
    const negative = tools.parseEditorDrafts(tools.serializeEditorDrafts(drafts.tabs, -5));
    assert.equal(negative.activeIndex, 0, "a negative active index clamps to 0");
    const big = tools.parseEditorDrafts(tools.serializeEditorDrafts([{ title: "t", sql: "x".repeat(tools.MAX_EDITOR_DRAFT_SQL_CHARS + 50) }], 0));
    assert.equal(big.tabs[0].sql.length, tools.MAX_EDITOR_DRAFT_SQL_CHARS, "an oversized buffer is truncated, not rejected");
    const manyTabs = Array.from({ length: tools.MAX_EDITOR_DRAFT_TABS + 4 }, (_, i) => ({ title: `t${i}`, sql: `SELECT ${i}` }));
    assert.equal(tools.parseEditorDrafts(tools.serializeEditorDrafts(manyTabs, 0)).tabs.length, tools.MAX_EDITOR_DRAFT_TABS, "tab count is capped");

    const DEFAULT = "SELECT * FROM system.capabilities ORDER BY name";
    assert.equal(tools.editorDraftsWorthRestoring(null, DEFAULT), false, "no draft: nothing to restore");
    assert.equal(tools.editorDraftsWorthRestoring({ tabs: [{ title: "Query 1", sql: DEFAULT }], activeIndex: 0 }, DEFAULT), false, "the pristine default buffer is not worth a restore notice");
    assert.equal(tools.editorDraftsWorthRestoring({ tabs: [{ title: "Query 1", sql: "" }], activeIndex: 0 }, DEFAULT), false, "an empty buffer is not worth restoring");
    assert.equal(tools.editorDraftsWorthRestoring({ tabs: [{ title: "Query 1", sql: "SELECT 42" }], activeIndex: 0 }, DEFAULT), true, "an edited buffer is worth restoring");
    assert.equal(tools.editorDraftsWorthRestoring({ tabs: [{ title: "a", sql: DEFAULT }, { title: "b", sql: DEFAULT }], activeIndex: 0 }, DEFAULT), true, "more than one tab is always worth restoring");
  }

  {
    // Layout persistence codec — pane visibility/widths + last table name,
    // never a credential or SQL buffer.
    const layout = {
      explorerVisible: false,
      inspectorVisible: true,
      explorerWidth: 300,
      inspectorWidth: 400,
      selectedTable: "articles",
    };
    assert.deepEqual(tools.parseStudioLayout(tools.serializeStudioLayout(layout)), layout, "layout round-trips");
    assert.deepEqual(tools.parseStudioLayout(null), tools.DEFAULT_STUDIO_LAYOUT, "missing storage is the default layout");
    assert.deepEqual(tools.parseStudioLayout("not json"), tools.DEFAULT_STUDIO_LAYOUT, "garbage parses to the default, not a throw");
    assert.deepEqual(tools.parseStudioLayout("{}").explorerVisible, true, "a missing visibility flag defaults to shown");
    assert.equal(tools.parseStudioLayout('{"explorerVisible":false}').explorerVisible, false, "an explicit hide is kept");
    assert.equal(tools.parseStudioLayout('{"selectedTable":"  orders  "}').selectedTable, "orders", "a selected table name is trimmed");
    assert.equal(tools.parseStudioLayout('{"selectedTable":""}').selectedTable, null, "an empty selected table is dropped");
    const longName = "t".repeat(tools.MAX_LAYOUT_TABLE_NAME + 20);
    assert.equal(tools.parseStudioLayout(tools.serializeStudioLayout({ ...tools.DEFAULT_STUDIO_LAYOUT, selectedTable: longName })).selectedTable.length, tools.MAX_LAYOUT_TABLE_NAME, "an oversized table name is truncated");
    assert.equal(tools.parseStudioLayout('{"explorerWidth":12}').explorerWidth, tools.MIN_EXPLORER_WIDTH, "an undersized explorer width clamps to min");
    assert.equal(tools.parseStudioLayout('{"explorerWidth":9000}').explorerWidth, tools.MAX_EXPLORER_WIDTH, "an oversized explorer width clamps to max");
    assert.equal(tools.parseStudioLayout('{"inspectorWidth":"nope"}').inspectorWidth, tools.DEFAULT_INSPECTOR_WIDTH, "a non-numeric width falls back");
    assert.equal(tools.clampLayoutWidth(12.4, 190, 480, 260), 190);
    assert.equal(tools.stepLayoutWidth(260, tools.LAYOUT_WIDTH_STEP, tools.MIN_EXPLORER_WIDTH, tools.MAX_EXPLORER_WIDTH), 276);
    assert.equal(tools.stepLayoutWidth(tools.MAX_EXPLORER_WIDTH, tools.LAYOUT_WIDTH_STEP, tools.MIN_EXPLORER_WIDTH, tools.MAX_EXPLORER_WIDTH), tools.MAX_EXPLORER_WIDTH, "a step past max clamps");
    const reset = tools.resetStudioLayout("orders");
    assert.equal(reset.explorerVisible, true);
    assert.equal(reset.explorerWidth, tools.DEFAULT_EXPLORER_WIDTH);
    assert.equal(reset.selectedTable, "orders", "reset keeps the selected table and restores pane defaults");
    assert.match(tools.layoutStorageKey("r a", "db", "user"), /^nextsql-studio-layout:/);
    assert.equal(JSON.parse(tools.serializeStudioLayout(layout)).password, undefined, "the layout document has no credential field");
  }

  {
    // Saved queries.
    const now = 1_000;
    let list = tools.upsertSavedQuery([], { id: "a", name: "  Top orders ", sql: "SELECT * FROM orders", tags: "reporting, ops", updatedAt: now });
    assert.deepEqual(list[0].name, "Top orders", "name is trimmed");
    assert.deepEqual(list[0].tags, ["ops", "reporting"], "comma tags are split, deduped, sorted");
    list = tools.upsertSavedQuery(list, { id: "b", name: "Recent signups", sql: "SELECT * FROM users", tags: ["ops"], updatedAt: now + 10 });
    assert.deepEqual(list.map((q) => q.id), ["b", "a"], "most recently updated is first");
    list = tools.upsertSavedQuery(list, { id: "a", name: "Top orders v2", sql: "SELECT id FROM orders", tags: ["reporting"], updatedAt: now + 20 });
    assert.equal(list.length, 2, "upsert by id replaces, does not duplicate");
    assert.deepEqual(list[0].id, "a", "the updated entry moves to the front");
    assert.deepEqual(tools.savedQueryTags(list), ["ops", "reporting"], "tag set is the union, sorted");
    assert.deepEqual(tools.filterSavedQueries(list, { tag: "ops" }).map((q) => q.id), ["b"], "tag filter narrows to entries carrying that tag");
    assert.deepEqual(tools.filterSavedQueries(list, { text: "signup" }).map((q) => q.id), ["b"], "text filter matches the name");
    assert.deepEqual(tools.filterSavedQueries(list, { text: "from orders" }).map((q) => q.id), ["a"], "text filter also matches the SQL body");
    list = tools.removeSavedQuery(list, "b");
    assert.deepEqual(list.map((q) => q.id), ["a"], "remove drops the entry");

    const round = tools.parseSavedQueries(tools.serializeSavedQueries(list));
    assert.deepEqual(round, list, "saved queries round-trip");
    assert.deepEqual(tools.parseSavedQueries("nope"), [], "garbage parses to an empty list");
    assert.deepEqual(tools.parseSavedQueries('{"queries":[{"name":"x"}]}'), [], "an entry without id+sql is dropped");
    const big = tools.parseSavedQueries(tools.serializeSavedQueries([{ id: "x", name: "n", sql: "y".repeat(tools.MAX_SAVED_QUERY_SQL_CHARS + 5), tags: [], updatedAt: 1 }]));
    assert.equal(big[0].sql.length, tools.MAX_SAVED_QUERY_SQL_CHARS, "an oversized saved SQL body is truncated");
    const manyTags = tools.upsertSavedQuery([], { id: "t", name: "n", sql: "s", tags: Array.from({ length: 30 }, (_, i) => `tag${i}`), updatedAt: 1 });
    assert.equal(manyTags[0].tags.length, tools.MAX_SAVED_QUERY_TAGS, "tag count is capped");
  }

  {
    // Git-friendly export / import of the saved-query set.
    const set = [
      { id: "z1", name: "Zebra report", sql: "SELECT 2", tags: ["ops"], updatedAt: 200 },
      { id: "a1", name: "Alpha report", sql: "SELECT 1", tags: ["reporting", "ops"], updatedAt: 100 },
    ];
    const doc = tools.exportSavedQueries(set);
    assert.ok(doc.endsWith("\n"), "export ends with a trailing newline");
    assert.match(doc, /\n {2}"format"/, "export is two-space indented");
    const parsedDoc = JSON.parse(doc);
    assert.equal(parsedDoc.format, tools.SAVED_QUERY_EXPORT_FORMAT, "export carries the format tag");
    assert.deepEqual(parsedDoc.queries.map((q) => q.id), ["a1", "z1"], "export orders by name, not recency");
    assert.equal(tools.exportSavedQueries(set), tools.exportSavedQueries([...set].reverse()), "export is order-independent (git-friendly)");

    assert.deepEqual(tools.parseSavedQueriesExport(doc).map((q) => q.id).sort(), ["a1", "z1"], "the export round-trips through the importer");
    assert.deepEqual(tools.parseSavedQueriesExport(JSON.stringify(set)).map((q) => q.id).sort(), ["a1", "z1"], "a bare array is also accepted on import");
    assert.deepEqual(tools.parseSavedQueriesExport("not json"), [], "unparseable import yields nothing");
    assert.deepEqual(tools.parseSavedQueriesExport('{"queries":[{"name":"x"}]}'), [], "an entry missing id+sql is dropped on import");

    const current = [
      { id: "a1", name: "Alpha report", sql: "SELECT 1", tags: ["ops"], updatedAt: 100 },
      { id: "keep", name: "Local only", sql: "SELECT 9", tags: [], updatedAt: 50 },
    ];
    const incoming = [
      { id: "a1", name: "Alpha report v2", sql: "SELECT 1, 2", tags: ["ops"], updatedAt: 300 },
      { id: "a1-stale", name: "should not clobber", sql: "x", tags: [], updatedAt: 1 },
      { id: "new", name: "Fresh", sql: "SELECT 3", tags: [], updatedAt: 400 },
    ];
    const merged = tools.mergeSavedQueries(current, incoming.slice(0, 1).concat([
      { id: "keep", name: "Local only", sql: "SELECT 9", tags: [], updatedAt: 10 },
      incoming[2],
    ]));
    assert.equal(merged.added, 1, "a new id counts as added");
    assert.equal(merged.updated, 1, "a newer matching id counts as updated");
    assert.equal(merged.unchanged, 1, "an older/identical matching id counts as unchanged");
    assert.equal(merged.list.find((q) => q.id === "a1").sql, "SELECT 1, 2", "the newer copy wins");
    assert.equal(merged.list.find((q) => q.id === "keep").sql, "SELECT 9", "an older incoming copy never clobbers a local edit");
    assert.deepEqual(merged.list.map((q) => q.id), ["new", "a1", "keep"], "merge result is recency-ordered");

    assert.match(tools.savedQueriesExportFilename(new Date("2026-09-06T12:00:00Z")), /saved-queries-2026-09-06\.json$/, "export filename is date-stamped");
  }

  {
    // Positional query-parameter extraction.
    assert.deepEqual(tools.extractQueryParams("SELECT 1"), [], "no placeholders");
    assert.deepEqual(
      tools.extractQueryParams("SELECT * FROM t WHERE a = $1 AND b = $2 OR c = $1"),
      [1, 2],
      "distinct, ascending",
    );
    assert.deepEqual(tools.extractQueryParams("WHERE id = $3"), [3], "a gap is preserved (no $1/$2 invented)");
    assert.deepEqual(tools.extractQueryParams("SELECT $2, $10, $1"), [1, 2, 10], "multi-digit and out-of-order");
    assert.deepEqual(tools.extractQueryParams("SELECT price$1, a$2b"), [], "an identifier-adjacent $n is not a placeholder");
    assert.deepEqual(tools.extractQueryParams("SELECT $0, $999"), [], "0 and >MAX_QUERY_PARAMS are ignored");
    assert.equal(tools.extractQueryParams(`SELECT ${"$1,".repeat(50)} 0`).length <= tools.MAX_QUERY_PARAMS, true, "result never exceeds the cap");
  }

  {
    // Recent-connection quick-switch list.
    assert.deepEqual(tools.parseRecentConnections(null), [], "missing storage → empty");
    assert.deepEqual(tools.parseRecentConnections("not json"), [], "garbage → empty");
    assert.deepEqual(tools.parseRecentConnections('{"realm":"x"}'), [], "non-array → empty");
    assert.deepEqual(
      tools.parseRecentConnections(JSON.stringify([
        { realm: "acme", database: "prod", at: 3 },
        { realm: "", database: "", at: 2 },
        { realm: "acme", database: "prod", at: 1 },
        { realm: "x".repeat(200), database: "y", at: 1 },
        { realm: "beta", database: "stage", at: 1 },
      ])),
      [
        { realm: "acme", database: "prod", at: 3 },
        { realm: "beta", database: "stage", at: 1 },
      ],
      "drops all-empty, dedupes, drops over-long names",
    );

    const start = [{ realm: "acme", database: "prod", at: 1 }];
    assert.deepEqual(
      tools.recordRecentConnection(start, "beta", "stage", 5),
      [
        { realm: "beta", database: "stage", at: 5 },
        { realm: "acme", database: "prod", at: 1 },
      ],
      "new entry goes to the front",
    );
    assert.deepEqual(
      tools.recordRecentConnection(start, "acme", "prod", 9),
      [{ realm: "acme", database: "prod", at: 9 }],
      "an existing pair is moved to the front, not duplicated",
    );
    assert.deepEqual(tools.recordRecentConnection(start, "", "", 9), start, "the all-default pair is not recorded");
    const many = Array.from({ length: tools.MAX_RECENT_CONNECTIONS }, (_, i) => ({ realm: `r${i}`, database: "d", at: i }));
    assert.equal(tools.recordRecentConnection(many, "new", "d", 99).length, tools.MAX_RECENT_CONNECTIONS, "the list stays capped");
    assert.equal(tools.recentConnectionLabel({ realm: "", database: "d", at: 0 }), "(default realm) / d");

    const round = tools.parseRecentConnections(tools.serializeRecentConnections(start));
    assert.deepEqual(round, start, "serialize → parse round-trips");
  }

  {
    // Command-palette matcher.
    const cmds = [
      { id: "run", label: "Run query", keywords: "execute sql" },
      { id: "run-script", label: "Run script" },
      { id: "cancel", label: "Cancel running query", disabled: true },
      { id: "saved", label: "Saved queries" },
      { id: "geo", label: "Open Geo explorer", keywords: "spatial" },
    ];
    assert.deepEqual(
      tools.rankCommandMatches("", cmds).map((c) => c.id),
      ["run", "run-script", "saved", "geo"],
      "empty query keeps curated order and drops disabled commands",
    );
    assert.equal(tools.rankCommandMatches("", cmds, 2).length, 2, "the limit is honored");
    assert.deepEqual(
      tools.rankCommandMatches("run q", cmds).map((c) => c.id),
      ["run"],
      "a label prefix beats an unrelated match",
    );
    assert.deepEqual(tools.rankCommandMatches("spatial", cmds).map((c) => c.id), ["geo"], "keywords are searched");
    assert.deepEqual(tools.rankCommandMatches("cancel", cmds), [], "a disabled command never matches");
    assert.deepEqual(tools.rankCommandMatches("zzzz", cmds), [], "no fuzzy match → empty");
    assert.deepEqual(
      tools.rankCommandMatches("runscr", cmds).map((c) => c.id),
      ["run-script"],
      "an in-order subsequence still matches when nothing better does",
    );
  }

  {
    // Query result summary line.
    assert.equal(
      tools.queryResultSummary({ columns: ["id", "name"], rows: [1, 2, 3], elapsed_ms: 12 }, false),
      "3 rows · 2 columns · 12 ms",
      "a read reports rows and columns",
    );
    assert.equal(
      tools.queryResultSummary({ columns: ["id"], rows: [1], elapsed_ms: 1 }, false),
      "1 row · 1 column · 1 ms",
      "singular row/column",
    );
    assert.equal(
      tools.queryResultSummary({ columns: ["id"], rows: [1, 2], elapsed_ms: 1, truncated: true }, false),
      "2 rows · 1 column · 1 ms · server preview limit reached",
      "a truncated read says so",
    );
    assert.equal(
      tools.queryResultSummary({ columns: [], rows: [], affected: 5, elapsed_ms: 3 }, false),
      "5 rows affected · 3 ms",
      "a write reports the affected count, not '0 rows'",
    );
    assert.equal(
      tools.queryResultSummary({ columns: [], rows: [], affected: 1, elapsed_ms: 3 }, false),
      "1 row affected · 3 ms",
      "singular affected row",
    );
    assert.equal(
      tools.queryResultSummary({ columns: [], rows: [], affected: 0, elapsed_ms: 4 }, false),
      "Statement completed · 4 ms",
      "a DDL with no affected count reads as a plain completion",
    );
    assert.equal(
      tools.queryResultSummary({ columns: ["id"], rows: [1], elapsed_ms: 1 }, true),
      "Selection · 1 row · 1 column · 1 ms",
      "a selection run is labelled",
    );
  }

  {
    // Realm-scoped administration warning.
    const named = tools.realmScopeWarning("DropUser", "acme", "analytics");
    assert.match(named, /realm "acme"/, "names the connected realm");
    assert.match(named, /"analytics" database/, "names the connected database");
    assert.match(named, /every database in/, "explains the cross-database reach");
    assert.match(named, /^DropUser /, "leads with the statement kind");

    const defaulted = tools.realmScopeWarning("", "", "");
    assert.match(defaulted, /the default realm/, "empty realm falls back to 'the default realm'");
    assert.match(defaulted, /the current database/, "empty database falls back to 'the current database'");
    assert.match(defaulted, /^This statement /, "empty kind falls back to 'This statement'");
  }

  {
    // Data generator for development.
    const columnsResult = (rows) => ({
      columns: ["table_name", "column_name", "ordinal", "type", "not_null", "is_primary", "default_value"],
      column_types: ["STRING", "STRING", "DECIMAL", "STRING", "BOOL", "BOOL", "STRING"],
      rows,
      truncated: false,
      elapsed_ms: 0,
    });
    const detail = {
      name: "orders",
      columns: columnsResult([
        ["orders", "id", "1", "INT64", "true", "true", null],
        ["orders", "label", "2", "STRING", "true", "false", null],
        ["orders", "amount", "3", "DECIMAL(10,2)", "false", "false", null],
        ["orders", "created", "4", "TIMESTAMPTZ", "true", "false", null],
        ["orders", "note", "5", "TEXT", "false", "false", "'n/a'"],
        ["orders", "embedding", "6", "VECTOR<F32,4>", "false", "false", null],
      ]),
    };
    const cols = tools.dataGenColumns(detail);
    assert.equal(cols.length, 6);
    assert.deepEqual(cols.map((c) => c.name), ["id", "label", "amount", "created", "note", "embedding"], "ordered by ordinal");
    assert.equal(cols.find((c) => c.name === "embedding").supported, false, "VECTOR is not generatable");
    assert.equal(cols.find((c) => c.name === "id").strategies[0], "sequence", "a PK integer defaults to a sequence");

    const base = {
      table: "orders",
      rowCount: 3,
      seed: 1,
      strategies: { id: "sequence", label: "label", amount: "random-decimal", created: "now" },
    };
    const built = tools.buildDataGeneratorSQL(base, cols);
    assert.equal(built.error, null);
    // note has a DEFAULT and embedding is nullable+unsupported → both omitted.
    assert.match(built.sql, /^INSERT INTO "orders" \("id", "label", "amount", "created"\) VALUES\n/);
    assert.match(built.sql, /\(1, 'label-1', \d+\.\d{2}, NOW\(\)\),\n/);
    assert.match(built.sql, /\(3, 'label-3', \d+\.\d{2}, NOW\(\)\);$/);
    assert.equal(built.rows, 3);
    assert.equal(built.statements, 1);

    // Deterministic given (state, columns).
    assert.equal(tools.buildDataGeneratorSQL(base, cols).sql, built.sql, "same seed → identical SQL");
    assert.notEqual(
      tools.buildDataGeneratorSQL({ ...base, seed: 2 }, cols).sql,
      built.sql,
      "a different seed changes the random cells",
    );

    // A NOT NULL column with no default cannot be skipped.
    const skipped = tools.buildDataGeneratorSQL({ ...base, strategies: { ...base.strategies, label: "skip" } }, cols);
    assert.equal(skipped.sql, null);
    assert.match(skipped.error, /"label" is NOT NULL and has no default/);

    // A NOT NULL unsupported column with no default is a blocking error.
    const badVector = tools.buildDataGeneratorSQL(base, cols.map((c) => (c.name === "embedding" ? { ...c, notNull: true } : c)));
    assert.equal(badVector.sql, null);
    assert.match(badVector.error, /VECTOR<F32,4>/);

    // Bounds.
    assert.match(tools.buildDataGeneratorSQL({ ...base, rowCount: 0 }, cols).error, /1 to 1,000/);
    assert.match(tools.buildDataGeneratorSQL({ ...base, rowCount: 99999 }, cols).error, /1 to 1,000/);
    assert.match(tools.buildDataGeneratorSQL({ ...base, seed: -1 }, cols).error, /zero or greater/);

    // Batches into ≤100-row statements.
    const many = tools.buildDataGeneratorSQL({ ...base, rowCount: 250 }, cols);
    assert.equal(many.statements, 3);
    assert.equal(many.sql.match(/INSERT INTO "orders"/g).length, 3);

    // Identifier and string quoting are always applied.
    const evilDetail = {
      name: 'a"b',
      columns: columnsResult([['a"b', 'c"l', "1", "STRING", "false", "false", null]]),
    };
    const evilCols = tools.dataGenColumns(evilDetail);
    const evil = tools.buildDataGeneratorSQL({ table: 'a"b', rowCount: 1, seed: 5, strategies: { 'c"l': "label" } }, evilCols);
    assert.match(evil.sql, /^INSERT INTO "a""b" \("c""l"\) VALUES/);
    assert.match(evil.sql, /'c"l-1'/, "embedded double-quote is safe inside a SQL string literal");

    assert.equal(tools.dataGenFieldKind("UINT16"), "int");
    assert.equal(tools.dataGenFieldKind("DECIMAL(5)"), "decimal");
    assert.equal(tools.dataGenFieldKind("BITVECTOR<8>"), "unsupported");
    assert.equal(tools.dataGenFieldKind("uuid"), "uuid");
  }

  {
    // CSV / JSON / NDJSON import → bounded INSERT-script builder.
    const columnsResult = (rows) => ({
      columns: ["table_name", "column_name", "ordinal", "type", "not_null", "is_primary", "default_value"],
      column_types: ["STRING", "STRING", "DECIMAL", "STRING", "BOOL", "BOOL", "STRING"],
      rows,
      truncated: false,
      elapsed_ms: 0,
    });
    const detail = {
      name: "orders",
      columns: columnsResult([
        ["orders", "id", "1", "INT64", "true", "true", null],
        ["orders", "label", "2", "STRING", "false", "false", null],
        ["orders", "paid", "3", "BOOL", "false", "false", null],
        ["orders", "amount", "4", "DECIMAL(10,2)", "false", "false", null],
        ["orders", "meta", "5", "JSON", "false", "false", null],
        ["orders", "embedding", "6", "VECTOR<F32,4>", "true", "false", null],
      ]),
    };
    const cols = tools.dataGenColumns(detail);

    // CSV parsing: header + quoted fields with embedded delimiter/newline/quote.
    const csv = 'id,label,paid\n1,"Hello, world",true\n2,"a ""quoted"" bit",0\n';
    const parsed = tools.parseImportText(csv, "csv");
    assert.equal(parsed.error, null);
    assert.deepEqual(parsed.fields, ["id", "label", "paid"]);
    assert.equal(parsed.rows.length, 2);
    assert.deepEqual(parsed.rows[0], ["1", "Hello, world", "true"]);
    assert.deepEqual(parsed.rows[1], ["2", 'a "quoted" bit', "0"]);

    // Auto-mapping is exact case-insensitive, never to an unsupported column.
    const auto = tools.autoImportMapping(["ID", "label", "nope"], cols);
    assert.equal(auto.ID, "id");
    assert.equal(auto.label, "label");
    assert.equal(auto.nope, "");

    const build = (opts) => tools.buildImportInsertSQL({
      table: "orders", columns: cols, parsed, emptyAsNull: true,
      mapping: { id: "id", label: "label", paid: "paid" }, ...opts,
    });
    // embedding is NOT NULL with no default and unmapped → blocking error.
    assert.match(build().error, /"embedding" is NOT NULL and has no default/);

    // Drop the blocker for the rest of the cases.
    const ok = cols.filter((c) => c.name !== "embedding");
    const okBuild = (opts) => tools.buildImportInsertSQL({
      table: "orders", columns: ok, parsed, emptyAsNull: true,
      mapping: { id: "id", label: "label", paid: "paid" }, ...opts,
    });
    const good = okBuild();
    assert.equal(good.error, null);
    assert.match(good.sql, /^INSERT INTO "orders" \("id", "label", "paid"\) VALUES\n/);
    assert.match(good.sql, /\(1, 'Hello, world', TRUE\),/);
    assert.match(good.sql, /\(2, 'a "quoted" bit', FALSE\);$/);
    assert.equal(good.rows, 2);
    assert.equal(good.statements, 1);

    // Column order follows catalog ordinal, not document field order.
    const reordered = tools.buildImportInsertSQL({
      table: "orders", columns: ok, parsed, emptyAsNull: true,
      mapping: { paid: "paid", label: "label", id: "id" },
    });
    assert.match(reordered.sql, /\("id", "label", "paid"\)/);

    // A non-integer cell for an INT column is a named row error.
    const badInt = tools.parseImportText("id,label\nx,hi\n", "csv");
    assert.match(
      tools.buildImportInsertSQL({ table: "orders", columns: ok, parsed: badInt, emptyAsNull: true, mapping: { id: "id", label: "label" } }).error,
      /Row 1: "x" is not an integer for column "id"/,
    );

    // Two fields mapped to one column is rejected.
    assert.match(
      tools.buildImportInsertSQL({ table: "orders", columns: ok, parsed, emptyAsNull: true, mapping: { id: "id", label: "id", paid: "paid" } }).error,
      /Two fields are mapped to the column "id"/,
    );

    // Empty value → NULL when the option is on; → error for a NOT NULL column.
    const withEmpty = tools.parseImportText("id,label\n7,\n", "csv");
    assert.match(
      tools.buildImportInsertSQL({ table: "orders", columns: ok, parsed: withEmpty, emptyAsNull: true, mapping: { id: "id", label: "label" } }).sql,
      /\(7, NULL\);/,
    );
    assert.match(
      tools.buildImportInsertSQL({ table: "orders", columns: ok, parsed: tools.parseImportText("id,label\n,x\n", "csv"), emptyAsNull: true, mapping: { id: "id", label: "label" } }).error,
      /Row 1: column "id" is NOT NULL but the value is empty/,
    );

    // JSON array: fields are the union of object keys, first-seen order.
    const json = tools.parseImportText('[{"id":1,"label":"a"},{"id":2,"paid":true}]', "json");
    assert.deepEqual(json.fields, ["id", "label", "paid"]);
    assert.deepEqual(json.rows[1], ["2", "", "true"]);
    assert.match(
      tools.buildImportInsertSQL({ table: "orders", columns: ok, parsed: json, emptyAsNull: true, mapping: { id: "id", label: "label", paid: "paid" } }).sql,
      /\(2, NULL, TRUE\);/,
    );

    // NDJSON, and JSON validation for a JSON column.
    const ndjson = tools.parseImportText('{"id":1,"meta":"{\\"k\\":1}"}\n{"id":2,"meta":"nope"}\n', "ndjson");
    assert.equal(ndjson.rows.length, 2);
    assert.match(
      tools.buildImportInsertSQL({ table: "orders", columns: ok, parsed: ndjson, emptyAsNull: true, mapping: { id: "id", meta: "meta" } }).error,
      /Row 2: column "meta" expects JSON/,
    );

    // Bounds: parse truncates at MAX_IMPORT_ROWS; SQL batches at 100/statement.
    const manyLines = ["id,label"];
    for (let i = 1; i <= 250; i++) manyLines.push(`${i},row${i}`);
    const many = tools.parseImportText(manyLines.join("\n"), "csv");
    const manyBuild = tools.buildImportInsertSQL({ table: "orders", columns: ok, parsed: many, emptyAsNull: true, mapping: { id: "id", label: "label" } });
    assert.equal(manyBuild.statements, 3);
    assert.equal(manyBuild.sql.match(/INSERT INTO "orders"/g).length, 3);

    // Identifier + string quoting always applied.
    const evilDetail = { name: 'a"b', columns: columnsResult([['a"b', 'c"l', "1", "STRING", "false", "false", null]]) };
    const evilCols = tools.dataGenColumns(evilDetail);
    const evil = tools.buildImportInsertSQL({
      table: 'a"b', columns: evilCols, emptyAsNull: true,
      parsed: tools.parseImportText("x\nit's \"fine\"\n", "csv"), mapping: { x: 'c"l' },
    });
    assert.match(evil.sql, /^INSERT INTO "a""b" \("c""l"\) VALUES/);
    assert.match(evil.sql, /'it''s "fine"'/);

    // Malformed documents.
    assert.match(tools.parseImportText('id,label\n"unterminated', "csv").error, /unterminated quoted field/);
    assert.match(tools.parseImportText("id,id\n1,2\n", "csv").error, /duplicate column name/);
    assert.match(tools.parseImportText("id,label\n", "csv").error, /header row and at least one data row/);
    assert.match(tools.parseImportText('{"id":1}', "json").error, /must be an array/);
  }

  {
    // Vector dataset import → bounded INSERT-script builder for embeddings.
    const columnsResult = (rows) => ({
      columns: ["table_name", "column_name", "ordinal", "type", "not_null", "is_primary", "default_value"],
      column_types: ["STRING", "STRING", "DECIMAL", "STRING", "BOOL", "BOOL", "STRING"],
      rows,
      truncated: false,
      elapsed_ms: 0,
    });
    const detail = {
      name: "docs",
      columns: columnsResult([
        ["docs", "id", "1", "INT64", "true", "true", null],
        ["docs", "title", "2", "STRING", "false", "false", null],
        ["docs", "embedding", "3", "VECTOR<F32,3>", "true", "false", null],
      ]),
    };
    const cols = tools.dataGenColumns(detail);
    const vectorCols = tools.vectorColumnsFromResult(detail.columns);
    const embedding = vectorCols.find((c) => c.name === "embedding");
    assert.equal(embedding.kind, "dense");
    assert.equal(embedding.dimensions, 3);

    const build = (opts) => tools.buildVectorImportSQL({
      table: "docs", columns: cols, vectorColumn: embedding, emptyAsNull: true,
      scalarMapping: {}, ...opts,
    });

    // NDJSON with an array-valued embedding field + a scalar column.
    const ndjson = tools.parseImportText(
      '{"id":1,"title":"a","embedding":[0.1,0.2,0.3]}\n{"id":2,"title":"b","embedding":[1,0,-1]}\n',
      "ndjson",
    );
    const good = build({ parsed: ndjson, vectorField: "embedding", scalarMapping: { id: "id", title: "title" } });
    assert.equal(good.error, null);
    assert.match(good.sql, /^INSERT INTO "docs" \("id", "title", "embedding"\) VALUES\n/);
    assert.match(good.sql, /\(1, 'a', \(0\.1, 0\.2, 0\.3\)\),/);
    assert.match(good.sql, /\(2, 'b', \(1, 0, -1\)\);$/);
    assert.equal(good.rows, 2);
    assert.equal(good.statements, 1);
    assert.equal(good.truncated, false);

    // autoVectorImportField prefers an exact case-insensitive name match.
    assert.equal(tools.autoVectorImportField(["id", "Embedding", "x"], "embedding", {}), "Embedding");
    assert.equal(tools.autoVectorImportField(["id", "vec"], "embedding", { id: "id" }), "vec");

    // A wrong-length vector is a named per-row error, never silently padded.
    const badDim = tools.parseImportText('{"id":1,"embedding":[0.1,0.2]}\n', "ndjson");
    assert.match(
      build({ parsed: badDim, vectorField: "embedding", scalarMapping: { id: "id" } }).error,
      /Row 1, column "embedding": .*3 dimensions; this vector has 2/,
    );

    // The embedding field is required (the column is NOT NULL, no default).
    assert.match(
      build({ parsed: ndjson, vectorField: "", scalarMapping: { id: "id", title: "title" } }).error,
      /Map a document field to the vector column/,
    );

    // A NOT NULL scalar column with no mapped field blocks the build.
    assert.match(
      build({ parsed: ndjson, vectorField: "embedding", scalarMapping: { title: "title" } }).error,
      /Column "id" is NOT NULL and has no default/,
    );

    // CSV with a quoted vector cell; column list follows catalog ordinal.
    const csv = tools.parseImportText('embedding,id\n"[1, 2, 3]",7\n', "csv");
    const csvBuild = build({ parsed: csv, vectorField: "embedding", scalarMapping: { id: "id" } });
    assert.equal(csvBuild.error, null);
    assert.match(csvBuild.sql, /\("id", "embedding"\) VALUES\n  \(7, \(1, 2, 3\)\);/);

    // BITVECTOR column rejects non 0/1 values.
    const bitDetail = {
      name: "sigs",
      columns: columnsResult([
        ["sigs", "sig", "1", "BITVECTOR<4>", "true", "false", null],
      ]),
    };
    const bitCol = tools.vectorColumnsFromResult(bitDetail.columns).find((c) => c.name === "sig");
    assert.match(
      tools.buildVectorImportSQL({
        table: "sigs", columns: tools.dataGenColumns(bitDetail), vectorColumn: bitCol, emptyAsNull: true,
        scalarMapping: {}, vectorField: "sig",
        parsed: tools.parseImportText('{"sig":[1,0,2,1]}\n', "ndjson"),
      }).error,
      /BITVECTOR values must be exactly 0 or 1/,
    );

    // Rows × dimensions ceiling → an actionable error, not a giant statement.
    const wideDetail = {
      name: "w",
      columns: columnsResult([["w", "e", "1", "VECTOR<F32,1000>", "true", "false", null]]),
    };
    const wideCol = tools.vectorColumnsFromResult(wideDetail.columns).find((c) => c.name === "e");
    const wideLines = [];
    for (let i = 0; i < 400; i++) {
      wideLines.push(JSON.stringify({ e: new Array(1000).fill(0) }));
    }
    assert.match(
      tools.buildVectorImportSQL({
        table: "w", columns: tools.dataGenColumns(wideDetail), vectorColumn: wideCol, emptyAsNull: true,
        scalarMapping: {}, vectorField: "e",
        parsed: tools.parseImportText(wideLines.join("\n"), "ndjson"),
      }).error,
      /exceeds the .* import ceiling/,
    );

    // Identifier quoting is always applied.
    const evilDetail = {
      name: 'a"b',
      columns: columnsResult([['a"b', 'e"c', "1", "VECTOR<F32,2>", "true", "false", null]]),
    };
    const evilCol = tools.vectorColumnsFromResult(evilDetail.columns).find((c) => c.name === 'e"c');
    const evil = tools.buildVectorImportSQL({
      table: 'a"b', columns: tools.dataGenColumns(evilDetail), vectorColumn: evilCol, emptyAsNull: true,
      scalarMapping: {}, vectorField: "e",
      parsed: tools.parseImportText('{"e":[1,2]}\n', "ndjson"),
    });
    assert.match(evil.sql, /^INSERT INTO "a""b" \("e""c"\) VALUES\n  \(\(1, 2\)\);/);

    // No vector column selected → a clear message.
    assert.match(
      tools.buildVectorImportSQL({
        table: "docs", columns: cols, vectorColumn: null, emptyAsNull: true,
        scalarMapping: {}, vectorField: "embedding", parsed: ndjson,
      }).error,
      /VECTOR, BITVECTOR, or SPARSEVECTOR column/,
    );
  }

  {
    // Parameterized INSERT / UPDATE / DELETE generation.
    const columnsResult = (rows) => ({
      columns: ["table_name", "column_name", "ordinal", "type", "not_null", "is_primary", "default_value"],
      column_types: ["STRING", "STRING", "DECIMAL", "STRING", "BOOL", "BOOL", "STRING"],
      rows,
      truncated: false,
      elapsed_ms: 0,
    });
    const detail = {
      name: "orders",
      columns: columnsResult([
        ["orders", "id", "1", "INT64", "true", "true", null],
        ["orders", "label", "2", "STRING", "true", "false", null],
        ["orders", "amount", "3", "DECIMAL(10,2)", "false", "false", null],
        ["orders", "created", "4", "TIMESTAMPTZ", "false", "false", "NOW()"],
        ["orders", "embedding", "5", "VECTOR<F32,4>", "false", "false", null],
      ]),
    };
    const cols = tools.dataGenColumns(detail);

    // Defaults: INSERT all columns, UPDATE sets non-PK / keys on PK, DELETE keys on PK.
    assert.deepEqual(tools.dmlDefaultColumns("insert", cols).setColumns, ["id", "label", "amount", "created", "embedding"]);
    assert.deepEqual(tools.dmlDefaultColumns("update", cols), { setColumns: ["label", "amount", "created", "embedding"], whereColumns: ["id"] });
    assert.deepEqual(tools.dmlDefaultColumns("delete", cols), { setColumns: [], whereColumns: ["id"] });

    // INSERT: placeholder per column, catalog order, a VECTOR column is a valid target.
    const ins = tools.buildParameterizedDML(
      { table: "orders", kind: "insert", setColumns: ["embedding", "id", "label"], whereColumns: [] },
      cols,
    );
    assert.equal(ins.error, null);
    assert.equal(ins.sql, 'INSERT INTO "orders" ("id", "label", "embedding")\n  VALUES ($1, $2, $3);');
    assert.equal(ins.params, 3);

    // INSERT: a NOT NULL column with no default cannot be left out.
    assert.match(
      tools.buildParameterizedDML({ table: "orders", kind: "insert", setColumns: ["id"], whereColumns: [] }, cols).error,
      /"label" is NOT NULL and has no default/,
    );
    // A NOT NULL column that has a default (created) may be left out.
    assert.equal(
      tools.buildParameterizedDML({ table: "orders", kind: "insert", setColumns: ["id", "label"], whereColumns: [] }, cols).error,
      null,
    );

    // UPDATE: SET params first, then WHERE params; SET/WHERE overlap is rejected.
    const upd = tools.buildParameterizedDML(
      { table: "orders", kind: "update", setColumns: ["amount", "label"], whereColumns: ["id"] },
      cols,
    );
    assert.equal(upd.sql, 'UPDATE "orders"\n  SET "label" = $1,\n      "amount" = $2\n  WHERE "id" = $3;');
    assert.equal(upd.params, 3);
    assert.match(
      tools.buildParameterizedDML({ table: "orders", kind: "update", setColumns: ["id"], whereColumns: ["id"] }, cols).error,
      /"id" is in both SET and WHERE/,
    );
    assert.match(
      tools.buildParameterizedDML({ table: "orders", kind: "update", setColumns: ["label"], whereColumns: [] }, cols).error,
      /at least one column for the WHERE clause/,
    );

    // DELETE: composite key predicate.
    const del = tools.buildParameterizedDML(
      { table: "orders", kind: "delete", setColumns: [], whereColumns: ["id", "label"] },
      cols,
    );
    assert.equal(del.sql, 'DELETE FROM "orders"\n  WHERE "id" = $1\n    AND "label" = $2;');

    // Unknown / duplicated column names.
    assert.match(
      tools.buildParameterizedDML({ table: "orders", kind: "insert", setColumns: ["nope"], whereColumns: [] }, cols).error,
      /"nope" is not a column of orders/,
    );
    assert.match(
      tools.buildParameterizedDML({ table: "orders", kind: "delete", setColumns: [], whereColumns: ["id", "id"] }, cols).error,
      /"id" is listed twice/,
    );

    // Parameter ceiling mirrors the editor's Parameters panel (MAX_QUERY_PARAMS).
    const wideRows = [];
    for (let i = 1; i <= 40; i++) wideRows.push(["wide", `c${i}`, String(i), "STRING", "false", "false", null]);
    const wideCols = tools.dataGenColumns({ name: "wide", columns: columnsResult(wideRows) });
    assert.match(
      tools.buildParameterizedDML({ table: "wide", kind: "insert", setColumns: wideCols.map((c) => c.name), whereColumns: [] }, wideCols).error,
      /needs 40 parameters/,
    );

    // Identifier quoting is always applied.
    const evilCols = tools.dataGenColumns({ name: 'a"b', columns: columnsResult([['a"b', 'c"l', "1", "STRING", "false", "false", null]]) });
    assert.equal(
      tools.buildParameterizedDML({ table: 'a"b', kind: "insert", setColumns: ['c"l'], whereColumns: [] }, evilCols).sql,
      'INSERT INTO "a""b" ("c""l")\n  VALUES ($1);',
    );
  }

  {
    // Table / index designer: CREATE TABLE / CREATE INDEX templates, never execute.

    const def = tools.defaultCreateTableState();
    const created = tools.buildCreateTableSQL(def);
    assert.equal(created.error, null);
    assert.equal(
      created.sql,
      'CREATE TABLE "new_table" (\n  "id" UUID PRIMARY KEY DEFAULT UUID(),\n  "name" STRING NOT NULL\n);',
    );

    assert.match(tools.buildCreateTableSQL({ ...def, table: "" }).error, /Table name is required/);
    assert.match(tools.buildCreateTableSQL({ ...def, table: "nsql_secret" }).error, /reserved nsql_ prefix/);
    assert.match(
      tools.buildCreateTableSQL({
        ...def,
        columns: def.columns.map((c) => ({ ...c, primaryKey: false })),
      }).error,
      /PRIMARY KEY is required/,
    );
    assert.match(
      tools.buildCreateTableSQL({
        ...def,
        columns: [def.columns[0], { ...def.columns[1], name: "id" }],
      }).error,
      /listed twice/,
    );

    const composite = tools.buildCreateTableSQL({
      ...def,
      table: "orders",
      columns: [
        tools.newDesignerColumn("a", { name: "tenant", typeKind: "STRING", primaryKey: true, notNull: true }),
        tools.newDesignerColumn("b", { name: "id", typeKind: "UUID", primaryKey: true, defaultKind: "uuid" }),
        tools.newDesignerColumn("c", { name: "qty", typeKind: "INT64" }),
      ],
    });
    assert.equal(
      composite.sql,
      'CREATE TABLE "orders" (\n  "tenant" STRING NOT NULL,\n  "id" UUID NOT NULL DEFAULT UUID(),\n  "qty" INT64,\n  PRIMARY KEY ("tenant", "id")\n);',
    );

    const decimalAI = tools.buildCreateTableSQL({
      ...def,
      columns: [
        tools.newDesignerColumn("pk", {
          name: "id",
          typeKind: "DECIMAL",
          typeParam: 18,
          typeScale: 0,
          primaryKey: true,
          defaultKind: "ai",
        }),
      ],
    });
    assert.equal(decimalAI.sql, 'CREATE TABLE "new_table" (\n  "id" DECIMAL(18,0) PRIMARY KEY DEFAULT AI()\n);');
    assert.match(
      tools.buildCreateTableSQL({
        ...def,
        columns: [tools.newDesignerColumn("pk", { name: "id", typeKind: "UUID", primaryKey: true, defaultKind: "ai" })],
      }).error,
      /DEFAULT AI\(\) is only valid on a DECIMAL column/,
    );

    const vectorPK = tools.buildCreateTableSQL({
      ...def,
      columns: [tools.newDesignerColumn("pk", { name: "id", typeKind: "VECTOR_F32", typeParam: 8, primaryKey: true })],
    });
    assert.match(vectorPK.error, /cannot be a PRIMARY KEY/);

    const charCol = tools.designerColumnTypeSQL(tools.newDesignerColumn("x", { typeKind: "CHAR", typeParam: 4 }));
    assert.equal(charCol.sql, "CHAR(4)");
    assert.match(tools.designerColumnTypeSQL(tools.newDesignerColumn("x", { typeKind: "CHAR", typeParam: 0 })).error, /CHAR length/);
    assert.equal(
      tools.designerColumnTypeSQL(tools.newDesignerColumn("x", { typeKind: "VECTOR_F16", typeParam: 3 })).sql,
      "VECTOR<F16,3>",
    );
    assert.equal(
      tools.designerColumnTypeSQL(tools.newDesignerColumn("x", { typeKind: "DECIMAL", typeParam: 10, typeScale: 2 })).sql,
      "DECIMAL(10,2)",
    );

    const quoted = tools.buildCreateTableSQL({
      ...def,
      table: 'a"b',
      columns: [
        tools.newDesignerColumn("pk", {
          name: 'c"l',
          typeKind: "STRING",
          primaryKey: true,
          defaultKind: "literal",
          defaultLiteral: "it's",
        }),
      ],
    });
    assert.equal(quoted.sql, `CREATE TABLE "a""b" (\n  "c""l" STRING PRIMARY KEY DEFAULT 'it''s'\n);`);

    const withFK = tools.buildCreateTableSQL({
      ...def,
      table: "items",
      columns: [
        tools.newDesignerColumn("pk", { name: "id", typeKind: "UUID", primaryKey: true, defaultKind: "uuid" }),
        tools.newDesignerColumn("fk", { name: "owner_id", typeKind: "UUID" }),
      ],
      fkEnabled: true,
      fkColumns: ["owner_id"],
      fkRefTable: "users",
      fkRefColumns: ["id"],
      fkOnDelete: "CASCADE",
      fkOnUpdate: "RESTRICT",
    });
    assert.match(withFK.sql, /FOREIGN KEY \("owner_id"\) REFERENCES "users" \("id"\) ON DELETE CASCADE ON UPDATE RESTRICT/);
    assert.match(
      tools.buildCreateTableSQL({
        ...def,
        fkEnabled: true,
        fkColumns: ["nope"],
        fkRefTable: "users",
        fkRefColumns: ["id"],
        fkOnDelete: "RESTRICT",
        fkOnUpdate: "RESTRICT",
      }).error,
      /not a column of this table/,
    );

    const ixCols = [
      { name: "id", type: "INT64" },
      { name: "label", type: "STRING" },
      { name: "body", type: "TEXT" },
      { name: "meta", type: "JSON" },
      { name: "embedding", type: "VECTOR<F32,8>" },
      { name: "bits", type: "BITVECTOR<8>" },
      { name: "sparse", type: "SPARSEVECTOR<128>" },
      { name: "loc", type: "POINT" },
      { name: "note", type: "STRING" },
    ];
    assert.deepEqual(tools.designerIndexKindOptions(ixCols), ["btree", "unique", "fulltext", "vector", "spatial"]);

    const btree = tools.buildCreateIndexSQL(
      { ...tools.defaultCreateIndexState("orders"), name: "ix_label", columns: ["label"], include: ["note"] },
      ixCols,
    );
    assert.equal(btree.sql, 'CREATE INDEX "ix_label" ON "orders" ("label") INCLUDE ("note");');

    const unique = tools.buildCreateIndexSQL(
      { ...tools.defaultCreateIndexState("orders"), name: "ux_label", kind: "unique", columns: ["label"] },
      ixCols,
    );
    assert.equal(unique.sql, 'CREATE UNIQUE INDEX "ux_label" ON "orders" ("label");');

    const jsonPath = tools.buildCreateIndexSQL(
      { ...tools.defaultCreateIndexState("orders"), name: "ix_cat", columns: ["meta"], jsonPath: "category.kind" },
      ixCols,
    );
    assert.equal(jsonPath.sql, 'CREATE INDEX "ix_cat" ON "orders" ("meta"."category"."kind");');
    assert.match(
      tools.buildCreateIndexSQL(
        { ...tools.defaultCreateIndexState("orders"), name: "ix_bad", columns: ["label"], jsonPath: "category" },
        ixCols,
      ).error,
      /exactly one JSON key column/,
    );

    const ft = tools.buildCreateIndexSQL(
      { ...tools.defaultCreateIndexState("orders"), name: "ft_body", kind: "fulltext", columns: ["label", "body"], analyzer: "english" },
      ixCols,
    );
    assert.equal(ft.sql, `CREATE FULLTEXT INDEX "ft_body" ON "orders" ("label", "body") WITH (ANALYZER = 'english');`);
    assert.match(
      tools.buildCreateIndexSQL(
        { ...tools.defaultCreateIndexState("orders"), name: "ft_bad", kind: "fulltext", columns: ["embedding"] },
        ixCols,
      ).error,
      /FULLTEXT cannot index it/,
    );

    const hnsw = tools.buildCreateIndexSQL(
      {
        ...tools.defaultCreateIndexState("orders"),
        name: "vn_emb",
        kind: "vector",
        columns: ["embedding"],
        vectorMethod: "HNSW",
        vectorQuant: "F16",
      },
      ixCols,
    );
    assert.equal(hnsw.sql, `CREATE VECTOR INDEX "vn_emb" ON "orders" ("embedding") USING HNSW WITH (QUANTIZATION = 'F16');`);

    const ivfpq = tools.buildCreateIndexSQL(
      {
        ...tools.defaultCreateIndexState("orders"),
        name: "vn_pq",
        kind: "vector",
        columns: ["embedding"],
        vectorMethod: "IVFPQ",
        ivfLists: 16,
        ivfProbes: 4,
        ivfSubspaces: 8,
      },
      ixCols,
    );
    assert.equal(
      ivfpq.sql,
      'CREATE VECTOR INDEX "vn_pq" ON "orders" ("embedding") USING IVFPQ WITH (LISTS = 16, PROBES = 4, SUBSPACES = 8);',
    );

    assert.match(
      tools.buildCreateIndexSQL(
        { ...tools.defaultCreateIndexState("orders"), name: "vn_bad", kind: "vector", columns: ["embedding"], vectorMethod: "SPARSE" },
        ixCols,
      ).error,
      /USING SPARSE is only valid on a SPARSEVECTOR column/,
    );
    const sparse = tools.buildCreateIndexSQL(
      { ...tools.defaultCreateIndexState("orders"), name: "vn_sp", kind: "vector", columns: ["sparse"], vectorMethod: "SPARSE" },
      ixCols,
    );
    assert.equal(sparse.sql, 'CREATE VECTOR INDEX "vn_sp" ON "orders" ("sparse") USING SPARSE;');

    const spatial = tools.buildCreateIndexSQL(
      { ...tools.defaultCreateIndexState("orders"), name: "sp_loc", kind: "spatial", columns: ["loc"] },
      ixCols,
    );
    assert.equal(spatial.sql, 'CREATE SPATIAL INDEX "sp_loc" ON "orders" ("loc");');

    assert.match(
      tools.buildCreateIndexSQL(
        { ...tools.defaultCreateIndexState("orders"), name: "ix_missing", columns: ["nope"] },
        ixCols,
      ).error,
      /is not a column of orders/,
    );
    assert.match(
      tools.buildCreateIndexSQL(
        { ...tools.defaultCreateIndexState("orders"), name: "ix_inc", columns: ["label"], include: ["label"] },
        ixCols,
      ).error,
      /cannot be both a key and an INCLUDE column/,
    );
    const quotedIx = tools.buildCreateIndexSQL(
      { ...tools.defaultCreateIndexState('a"b'), name: 'ix"1', columns: ['c"l'] },
      [{ name: 'c"l', type: "STRING" }],
    );
    assert.equal(quotedIx.sql, 'CREATE INDEX "ix""1" ON "a""b" ("c""l");');
  }

  // SQL formatter — reflow, keyword casing, comment preservation, and the
  // fail-closed safety net (formatSQL never alters what a statement means).
  {
    assert.equal(
      tools.formatSQL("select a,b from t where x=1 and y=2 order by a"),
      "SELECT a,\n  b\nFROM t\nWHERE x = 1\n  AND y = 2\nORDER BY a",
    );
    assert.equal(
      tools.formatSQL("SELECT * FROM t   WHERE  a  BETWEEN 1 AND 10"),
      "SELECT *\nFROM t\nWHERE a BETWEEN 1 AND 10",
      "BETWEEN ... AND is not broken across lines",
    );
    assert.equal(
      tools.formatSQL("select count(*) from orders o join items i on o.id=i.order_id"),
      "SELECT count(*)\nFROM orders o\nJOIN items i\n  ON o.id = i.order_id",
      "only lexer keywords are cased — a function name like count() is left as written",
    );
    assert.equal(
      tools.formatSQL("select uuid() as id -- comment\nfrom t"),
      "SELECT UUID() AS id -- comment\nFROM t",
      "a trailing line comment keeps its own line and is preserved verbatim",
    );
    assert.equal(
      tools.formatSQL("insert into t (a,b) values (1,2),(3,4)"),
      "INSERT INTO t (a, b)\nVALUES (1, 2),\n  (3, 4)",
    );
    assert.equal(
      tools.formatSQL("select 'a, b' as s, x from t where c <> -1"),
      "SELECT 'a, b' AS s,\n  x\nFROM t\nWHERE c <> -1",
      "commas and keywords inside a string literal are untouched; unary minus attaches",
    );
    assert.equal(
      tools.formatSQL("create table m (v vector<f32,3>)"),
      "CREATE TABLE m (v VECTOR<F32,3>)",
      "vector type parameters are not spread out",
    );
    assert.equal(
      tools.formatSQL("select a from t;select b from u"),
      "SELECT a\nFROM t;\n\nSELECT b\nFROM u",
    );
    // Fail-closed: an unterminated string is returned untouched.
    assert.equal(tools.formatSQL("select 'oops from t"), "select 'oops from t");
    // Fail-closed: an unterminated block comment is returned untouched.
    assert.equal(tools.formatSQL("select 1 /* nope"), "select 1 /* nope");
    // A non-keyword identifier keeps its exact case; only keywords are cased.
    assert.equal(tools.formatSQL("Select MyCol From MyTable"), "SELECT MyCol\nFROM MyTable");
    // Idempotent: formatting already-formatted SQL is a no-op.
    const once = tools.formatSQL("select a,b from t where x=1");
    assert.equal(tools.formatSQL(once), once);
    // Every formatter output re-tokenizes to the same significant stream as
    // its input (this is the guarantee the fail-closed net enforces).
    for (const q of [
      "select a,b,c from t",
      "update t set a=1,b=2 where id=$1",
      "delete from t where id in (1,2,3)",
      "select x from a left outer join b on a.k=b.k where a.v>=10 or a.v<0",
      "SELECT c FROM t /* mid */ WHERE q = x'deadbeef'",
      "select * from docs search body for 'hot chocolate' nearest embedding to (0.1,0.2) using cosine limit 5",
    ]) {
      const formatted = tools.formatSQL(q);
      const nospace = (s) => s.replace(/\s+/g, "").toLowerCase();
      assert.equal(nospace(formatted), nospace(q), `formatter changed tokens for: ${q}`);
    }
  }

  // Editable data grid & staged-change review.
  {
    const tables = ["articles", "users", "order_items"];

    // detectEditableTable
    assert.equal(tools.detectEditableTable("SELECT * FROM articles", tables), "articles");
    assert.equal(tools.detectEditableTable("select id, title from \"articles\" where id = 1", tables), "articles");
    assert.equal(tools.detectEditableTable("SELECT * FROM Articles", tables), "articles");
    assert.equal(tools.detectEditableTable("SELECT * FROM missing_table", tables), null);
    assert.equal(tools.detectEditableTable("SELECT * FROM system.tables", tables), null);
    assert.equal(tools.detectEditableTable("SELECT * FROM nsql_schema_migrations", tables), null);
    assert.equal(tools.detectEditableTable("SELECT a.id, u.name FROM articles a JOIN users u ON a.author_id = u.id", tables), null);
    assert.equal(tools.detectEditableTable("SELECT * FROM articles UNION SELECT * FROM users", tables), null);
    assert.equal(tools.detectEditableTable("UPDATE articles SET title = 'x'", tables), null);

    // isResultEditable
    const sampleResult = {
      columns: ["id", "title", "author_id"],
      column_types: ["INT64", "STRING", "INT64"],
      rows: [["1", "First post", "10"]],
      truncated: false,
      elapsed_ms: 1,
    };
    assert.equal(tools.isResultEditable(sampleResult, ["id"]).editable, true);
    assert.equal(tools.isResultEditable(sampleResult, ["missing_pk"]).editable, false);
    assert.equal(tools.isResultEditable(sampleResult, []).editable, false);
    assert.equal(tools.isResultEditable({ columns: [], rows: [] }, ["id"]).editable, false);

    // makeRowKey & extractRowPK
    const pkVals = tools.extractRowPK(["1", "First post", "10"], sampleResult.columns, ["id"]);
    assert.deepEqual(pkVals, { id: "1" });
    assert.equal(tools.makeRowKey(pkVals), "id=1");

    const compositePKVals = tools.extractRowPK(["100", "200", "5"], ["user_id", "item_id", "qty"], ["item_id", "user_id"]);
    assert.deepEqual(compositePKVals, { item_id: "200", user_id: "100" });
    assert.equal(tools.makeRowKey(compositePKVals), "item_id=200|user_id=100");

    // formatCellSQLLiteral
    assert.equal(tools.formatCellSQLLiteral(null, "STRING"), "NULL");
    assert.equal(tools.formatCellSQLLiteral("42", "INT64"), "42");
    assert.equal(tools.formatCellSQLLiteral("3.1415", "DECIMAL(10,4)"), "3.1415");
    assert.equal(tools.formatCellSQLLiteral("true", "BOOL"), "TRUE");
    assert.equal(tools.formatCellSQLLiteral("false", "BOOL"), "FALSE");
    assert.equal(tools.formatCellSQLLiteral("Hello 'world'", "STRING"), "'Hello ''world'''");

    // Staging changes: updates, deletes, inserts
    let changes = tools.createEmptyStagedChanges("articles", ["id"], {
      id: "INT64",
      title: "STRING",
      author_id: "INT64",
    });
    assert.equal(tools.stagedChangesCount(changes), 0);
    assert.equal(tools.stagedChangesSummary(changes), "No staged changes");

    // 1. Stage update
    const upd1 = tools.stageCellUpdate(changes, "id=1", { id: "1" }, "title", "STRING", "First post", "Updated Title");
    assert.equal(upd1.error, null);
    changes = upd1.changes;
    assert.equal(tools.stagedChangesCount(changes), 1);
    assert.equal(tools.stagedChangesSummary(changes), "1 update");

    // Stage another column on the same row -> coalesced under rowKey "id=1"
    const upd2 = tools.stageCellUpdate(changes, "id=1", { id: "1" }, "author_id", "INT64", "10", "25");
    assert.equal(upd2.error, null);
    changes = upd2.changes;
    assert.equal(tools.stagedChangesCount(changes), 2);
    assert.equal(tools.stagedChangesSummary(changes), "2 updates");

    // Reverting an update back to original value removes the staged update
    const revert = tools.stageCellUpdate(changes, "id=1", { id: "1" }, "author_id", "INT64", "10", "10");
    assert.equal(revert.error, null);
    changes = revert.changes;
    assert.equal(tools.stagedChangesCount(changes), 1);

    // 2. Stage delete on row id=2
    const del1 = tools.stageRowDelete(changes, "id=2", { id: "2" }, ["2", "Second post", "11"]);
    assert.equal(del1.error, null);
    changes = del1.changes;
    assert.equal(tools.stagedChangesCount(changes), 2);
    assert.equal(tools.stagedChangesSummary(changes), "1 update, 1 deletion");

    // Updating a deleted row fails
    const badUpd = tools.stageCellUpdate(changes, "id=2", { id: "2" }, "title", "STRING", "Second post", "Cannot edit");
    assert.match(badUpd.error, /marked for deletion/);

    // Unmarking delete
    const undel = tools.stageRowDelete(changes, "id=2", { id: "2" }, ["2", "Second post", "11"]);
    assert.equal(undel.error, null);
    changes = undel.changes;
    assert.equal(tools.stagedChangesCount(changes), 1);
    assert.equal(tools.stagedChangesSummary(changes), "1 update");

    // Re-delete for SQL build test
    changes = tools.stageRowDelete(changes, "id=2", { id: "2" }, ["2", "Second post", "11"]).changes;

    // 3. Stage insert
    const ins1 = tools.stageRowInsert(changes, { id: "3", title: "New Article", author_id: "99" });
    assert.equal(ins1.error, null);
    changes = ins1.changes;
    assert.equal(tools.stagedChangesCount(changes), 3);
    assert.equal(tools.stagedChangesSummary(changes), "1 update, 1 deletion, 1 insertion");

    // 4. Build transactional SQL
    const built = tools.buildStagedChangeSQL(changes);
    assert.equal(built.error, null);
    assert.equal(built.statements[0], "BEGIN;");
    assert.equal(built.statements[built.statements.length - 1], "COMMIT;");

    // Check statements contents
    assert.ok(built.statements.some((s) => s.includes("UPDATE \"articles\" SET \"title\" = 'Updated Title' WHERE \"id\" = 1;")));
    assert.ok(built.statements.some((s) => s.includes("DELETE FROM \"articles\" WHERE \"id\" = 2;")));
    assert.ok(built.statements.some((s) => s.includes("INSERT INTO \"articles\"")));

    // Composite primary key handling
    let compositeChanges = tools.createEmptyStagedChanges("order_items", ["order_id", "line_no"], {
      order_id: "INT64",
      line_no: "INT64",
      qty: "INT64",
    });
    compositeChanges = tools.stageCellUpdate(
      compositeChanges,
      "line_no=1|order_id=500",
      { order_id: "500", line_no: "1" },
      "qty",
      "INT64",
      "2",
      "5",
    ).changes;
    const compositeBuilt = tools.buildStagedChangeSQL(compositeChanges);
    assert.equal(compositeBuilt.error, null);
    assert.ok(compositeBuilt.statements.some((s) => s.includes("WHERE \"order_id\" = 500 AND \"line_no\" = 1;")));

    // Empty changes
    const emptyBuilt = tools.buildStagedChangeSQL(tools.createEmptyStagedChanges("articles", ["id"]));
    assert.match(emptyBuilt.error, /No staged changes/);
  }

  console.log("Studio result helper tests passed");
} finally {
  rmSync(scratch, { recursive: true, force: true });
}
