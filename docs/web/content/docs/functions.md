# Functions

NextSQL function names are case-insensitive. Arguments are type checked and are
not silently converted between unrelated types. Unless a function says
otherwise, a SQL `NULL` argument produces `NULL`.

```sql
SELECT CONCAT('Next', 'SQL');
SELECT CONCAT(first_name, ' ', last_name) FROM people;
```

## String functions

String functions accept `STRING`, `TEXT`, `CHAR`, and `VARCHAR` values. A
`CHAR` argument is treated as its content without storage padding; `VARCHAR`
is treated as `STRING`. Results preserve `TEXT` where applicable.

| Function | Behavior |
|---|---|
| `LOWER(value)` | Unicode lowercase; preserves `STRING` versus `TEXT` |
| `UPPER(value)` | Unicode uppercase; preserves `STRING` versus `TEXT` |
| `LENGTH(value)` | Number of Unicode code points, not UTF-8 bytes |
| `SUBSTRING(value, start [, length])` | 1-based code-point slice; `length` must be non-negative |
| `TRIM(value)` | Remove leading and trailing Unicode whitespace |
| `LTRIM(value)` | Remove leading Unicode whitespace |
| `RTRIM(value)` | Remove trailing Unicode whitespace |
| `REPLACE(value, old, new)` | Replace every literal occurrence of `old` |
| `CONCAT(value [, value ...])` | Concatenate one or more values; result is `TEXT` when any argument is `TEXT`, otherwise `STRING` |
| `STARTS_WITH(value, prefix)` | Case-sensitive literal prefix test |
| `ENDS_WITH(value, suffix)` | Case-sensitive literal suffix test |
| `CONTAINS(value, substring)` | Case-sensitive literal substring test |

`CONCAT` is variadic but requires at least one argument. Every argument must be
a string type. It follows NextSQL's normal NULL propagation, so
`CONCAT('a', NULL)` returns `NULL` rather than treating NULL as an empty string.

```sql
SELECT CONCAT('customer-', id) FROM customers; -- rejected when id is not a string
SELECT CONCAT('prefix-', body) FROM articles;  -- TEXT when body is TEXT
```

## Numeric functions

| Function | Behavior |
|---|---|
| `ABS(value)` | Exact DECIMAL absolute value |
| `ROUND(value [, scale])` | Exact rounding; half-away-from-zero ties and non-negative result scale |
| `CEIL(value)` | Exact DECIMAL ceiling |
| `FLOOR(value)` | Exact DECIMAL floor |
| `MOD(value, divisor)` | Exact aligned-scale remainder; a zero divisor is rejected |
| `POWER(base, exponent)` | Finite DECIMAL approximation rounded to eight fractional digits |
| `SQRT(value)` | Non-negative DECIMAL square root rounded to eight fractional digits |

The exact functions do not pass through binary floating point. `POWER` rejects
non-finite results and `SQRT` rejects negative inputs.

## NULL and value functions

| Function | Behavior |
|---|---|
| `COALESCE(value [, value ...])` | Evaluate left to right and return the first non-NULL value |
| `NULLIF(left, right)` | Return a typed NULL when the two coercible values compare equal; otherwise return `left` |
| `GREATEST(value [, value ...])` | Greatest coercible value; NULL if any input is NULL |
| `LEAST(value [, value ...])` | Least coercible value; NULL if any input is NULL |

`COALESCE` is lazy: arguments after the first non-NULL value are not evaluated.
All four functions require at least one argument except `NULLIF`, which requires
exactly two.

## Date and time functions

Date/time functions use UTC and accept `year`, `month`, `day`, `hour`,
`minute`, and `second` units.

| Function | Behavior |
|---|---|
| `EXTRACT(unit, timestamptz)` | Return the selected UTC field |
| `DATE_TRUNC(unit, timestamptz)` | Return the containing UTC boundary |
| `DATE_ADD(timestamptz, integer, unit)` | Calendar addition for year/month/day; elapsed duration for smaller units |
| `DATE_DIFF(start, end, unit)` | Calendar boundary difference for year/month; truncated elapsed difference for smaller units |

Unknown units and non-integral `DATE_ADD` amounts are rejected.

## JSON functions

These functions operate directly on validated binary NSJB documents. Paths may
use `a.b.0` or `$.a.b.0` notation. See [JSON](/docs/json) for storage, limits,
and index behavior.

| Function | Behavior |
|---|---|
| `JSON_GET(doc, path)` | Return a typed SQL scalar or JSON container; a missing path is SQL NULL |
| `JSON_ARRAY_LENGTH(doc [, path])` | Return the direct array element count |
| `JSON_TYPE(doc [, path])` | Return `object`, `array`, `string`, `number`, `boolean`, or `null` |
| `JSON_SET(doc, path, value)` | Replace a value or create missing object keys; array indexes must exist |
| `JSON_REMOVE(doc, path)` | Remove an object key or array element; a missing path is a no-op |
| `JSON_CONTAINS(doc, target)` | Recursive object-subset and array-element containment |

A constant-path `JSON_GET(column, path)` predicate is matched to the same native
path expression used by JSON-path indexes. A dynamic path remains a runtime
call and is not considered sargable.

## Aggregate functions

| Function | Behavior |
|---|---|
| `COUNT(*)` | Count input rows |
| `COUNT(value)` | Count non-NULL values |
| `SUM(value)` | Exact numeric sum |
| `AVG(value)` | Exact numeric average |
| `MIN(value)` | Least non-NULL value |
| `MAX(value)` | Greatest non-NULL value |
| `ARRAY_AGG(value)` | Collect non-NULL values into `ARRAY<T>` |
| `MAP_AGG(key, value)` | Collect canonical map entries; NULL or duplicate keys are rejected |

Aggregate arguments may be computed expressions. Aggregates can also appear in
larger select expressions, such as `COUNT(*) + 1`. See
[Relational](/docs/relational) for grouping and HAVING behavior and
[Collections](/docs/collections) for collection aggregates.

## Window functions

Supported window calls are `ROW_NUMBER()`, `RANK()`, `DENSE_RANK()`,
`LAG(value [, offset [, default]])`, `LEAD(value [, offset [, default]])`,
`FIRST_VALUE(value)`, `LAST_VALUE(value)`, and `COUNT` / `SUM` / `AVG` / `MIN`
/ `MAX` with `OVER (...)`.

Window expressions support `PARTITION BY`, window `ORDER BY`, and bounded
`ROWS` or `RANGE` frames. They run after filtering and grouping but before
query-level DISTINCT, ordering, and limits. See [Relational](/docs/relational)
for the full frame and NULL-ordering rules.

## Collection functions

| Function | Behavior |
|---|---|
| `ELEMENT_AT(array, index)` | Return a 1-based array element |
| `ELEMENT_AT(map, key)` | Return the value for a map key |
| `CARDINALITY(value)` / `ARRAY_LENGTH(array)` | Return an array length |
| `ARRAY_CONTAINS(array, value)` | Test array membership |
| `MAP_CONTAINS_KEY(map, key)` | Test map-key membership |
| `MAP_KEYS(map)` | Return canonical map keys as an array |
| `MAP_VALUES(map)` | Return values in canonical key order |
| `MAP_SIZE(map)` | Return the number of entries |

`STRUCT(...)`, `ARRAY(...)`, and `MAP(...)` construct collection values;
subscript syntax is shorthand for `ELEMENT_AT`. See
[Collections](/docs/collections) for constructors, nesting limits, aggregates,
and `UNNEST`.

## Vector functions

| Function | Behavior |
|---|---|
| `COSINE(a, b)` | Cosine similarity |
| `COSINE_DISTANCE(a, b)` | `1 - COSINE(a, b)` |
| `L2(a, b)` | Euclidean distance |
| `L1(a, b)` / `MANHATTAN(a, b)` | Manhattan distance |
| `INNER_PRODUCT(a, b)` / `DOT(a, b)` | Dot product |
| `VECTOR_DIM(value)` | Vector dimension |
| `VECTOR_NORM(value)` | Euclidean norm |
| `VECTOR_NORMALIZE(value)` | Unit vector; a zero vector is rejected |
| `VECTOR_ADD(a, b)` | Element-wise addition |
| `VECTOR_SUBTRACT(a, b)` | Element-wise subtraction |
| `VECTOR_SCALE(value, scale)` | Multiply by a DECIMAL scale |

Arguments must have equal dimensions and finite values. Some algebra operations
are intentionally unavailable for `SPARSEVECTOR` or `BITVECTOR`; see
[Vectors](/docs/vectors) for the exact type/metric matrix and ANN behavior.

## Geospatial functions

The fixed WGS84 types provide `POINT`, `BOX`, `LINESTRING`, `POLYGON`, `LON`,
`LAT`, `DISTANCE`, `DISTANCE_SPHEROID`, `DWITHIN`, `WITHIN`, `COVERS`,
`INTERSECTS`, `DISJOINT`, `LINELENGTH`, `AREA`, `PERIMETER`, `CENTROID`,
`ENVELOPE`, `GEOMETRYTYPE`, `NPOINTS`, and `NRINGS`, including documented
`ST_*` aliases.

General `GEOMETRY` / `GEOGRAPHY` adds constructors and serializers such as
`ST_GEOMFROMTEXT`, `ST_GEOGFROMTEXT`, `ST_POINT`, `ST_ASTEXT`, `ST_ASBINARY`,
`ST_ASGEOJSON`, and `ST_GEOMFROMGEOJSON`; accessors and measurements such as
`ST_SRID`, `ST_DIMENSION`, `ST_LENGTH`, and `ST_AREA`; topological predicates;
overlay operations; `ST_BUFFER`, `ST_SIMPLIFY`, `ST_SEGMENTIZE`, and the bounded
`ST_TRANSFORM` subset. See [Geospatial](/docs/geo) for native semantics,
supported aliases, indexability, SRID rules, and limits.

## Search result functions

`HIGHLIGHT(value [, pre, post])` and
`SNIPPET(value [, width [, pre, post]])` are valid only in a SELECT list with a
`SEARCH` clause. Snippet width is 16–4096 Unicode code points (default 160).
Both fail closed in predicates, grouping, joins, and DML. See
[Full-text search](/docs/fulltext) for analyzer and marker behavior.

## Execution-time defaults

`UUID()`, `NOW()`, and `AI()` are evaluated during execution and are not folded
by the optimizer. `AI()` is only valid as a `DECIMAL(p,0)` column default and
allocates in the statement transaction, so a rollback reuses the number.
