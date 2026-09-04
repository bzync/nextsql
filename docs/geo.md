# Geospatial and location

First-class WGS84 locations in the same ACID, encrypted, WAL-backed engine as the rest of NextSQL.

These four fixed shapes are the fast path for a plain WGS84 point/box/line/polygon column and are unaffected by the general `GEOMETRY` / `GEOGRAPHY` family (`docs/design-spatial.md`) — that family sits *alongside* these types, not as a generalization of them; the two coerce into one another (e.g. a `POINT` value flows into a `GEOGRAPHY(Point, 4326)` column, and back). Reach for `GEOMETRY`/`GEOGRAPHY` for `Multi*`/`GeometryCollection` geometry, an explicit SRID, or planar (as opposed to geodetic) math; reach for the types below when a WGS84 point/box/line/polygon is all you need.

## Types

| Type | Storage | Meaning |
|---|---|---|
| `POINT` | 16 bytes, two `float64` LE | longitude, latitude in degrees |
| `LOCATION` | same as `POINT` | type alias |
| `BOX` | 32 bytes, four `float64` LE | west, south, east, north |
| `LINESTRING` | `u16` vertex count + lon/lat pairs | at least two vertices |
| `POLYGON` | `u16` ring count + closed rings | exterior ring, then optional holes |

Longitude must be in `[-180, 180]`, latitude in `[-90, 90]`. Invalid coordinates fail closed. A `BOX` may wrap the antimeridian when west > east. `LINESTRING` and `POLYGON` reject an antimeridian-crossing span (`max lon − min lon > 180`) rather than guessing a wrap. Each ring is closed (first vertex equals last). Polygon construction rejects zero-area or self-intersecting rings, holes outside/touching the exterior, and overlapping or nested holes. Validation is bounded O(n²). Total vertices are capped at 256 so a geometry still fits in a 16 KiB page.

WKT text coerces into these types: `POINT(lon lat)`, `BOX(w s, e n)`, `LINESTRING(lon lat, …)`, `POLYGON((lon lat, …)[, (hole…)])`.

## Functions

Coordinates are **(longitude, latitude)**. `DISTANCE` is meters on the mean Earth sphere (IUGG radius 6 371 008.8 m, haversine). `DISTANCE_SPHEROID` is the Vincenty inverse on the WGS84 ellipsoid (a = 6 378 137 m, f = 1/298.257223563). Near-antipodal pairs that do not converge fall back to haversine; that fallback is not a geodesic.

`DISTANCE` and `DWITHIN` accept every pair of `POINT`, `BOX`, `LINESTRING`, and `POLYGON`. Segment topology (crossing, touching, and containment) is evaluated first and returns zero. Non-intersecting segment distance uses a local equirectangular closest point followed by haversine; this is the authoritative native predicate but is not a full ellipsoidal geodesic-to-polyline. Wrapping boxes are split at the antimeridian for bounded evaluation.

`WITHIN` / `COVERS` on a polygon use exterior-minus-holes ray casting.
`WITHIN(point, region)` requires strict interior and excludes every boundary;
`COVERS(region, point)` includes exterior and hole-ring boundaries but excludes
hole interiors. Boxes use the same strict-versus-inclusive distinction.

`AREA` uses a spherical ring integral in square meters and subtracts holes. `PERIMETER` sums the exterior and hole rings in meters. Polygon `CENTROID` uses the planar lon/lat ring centroid with holes subtracted; line centroids are segment-length weighted. These centroid semantics are deterministic native semantics, not an ellipsoidal center-of-mass calculation.

| Call | Result |
|---|---|
| `POINT(lon, lat)` | `POINT` |
| `BOX(west, south, east, north)` | `BOX` |
| `LINESTRING(wkt)` | `LINESTRING` |
| `POLYGON(wkt)` | `POLYGON` |
| `LON(p)` / `LAT(p)` | `DECIMAL` |
| `DISTANCE(a, b)` | shortest native distance in meters (all geometry pairs) |
| `DISTANCE_SPHEROID(a, b)` | meters (WGS84) |
| `LINELENGTH(line)` / `ST_Length` | meters along a `LINESTRING` |
| `DWITHIN(a, b, meters)` | boolean (all geometry pairs) |
| `WITHIN(p, box\|polygon)` | boolean |
| `COVERS(box\|polygon, p)` | boolean |
| `INTERSECTS(a, b)` / `DISJOINT(a, b)` | pairwise topology boolean |
| `AREA(polygon)` | spherical square meters, holes subtracted |
| `PERIMETER(polygon)` | meters across every ring |
| `CENTROID(geometry)` | `POINT` |
| `ENVELOPE(geometry)` / `BBOX` | axis-aligned `BOX` |
| `GEOMETRYTYPE(geometry)` | native type name |
| `NPOINTS(geometry)` | stored vertex count (`BOX` = 4) |
| `NRINGS(polygon)` | exterior + hole ring count |

Aliases: `ST_MakePoint`, `ST_MakeLine`, `ST_MakePolygon`, `ST_Distance`, `ST_DistanceSpheroid`, `ST_DWithin`, `ST_Within`, `ST_Covers`, `ST_Length`, `ST_Intersects`, `ST_Disjoint`, `ST_Area`, `ST_Perimeter`, `ST_Centroid`, `ST_Envelope`, `ST_GeometryType`, `ST_NPoints`, `ST_NRings`, `ST_X`, `ST_Y`, `LONGITUDE`, `LATITUDE`.

## Spatial index

```sql
CREATE SPATIAL INDEX ix_loc ON places (loc);
```

Requires a single `POINT` column. Not `UNIQUE` (finite hash cells can collide). Keys are a 64-bit Morton (Z-order) geohash plus the primary key. The value is the primary key so the executor can fetch the heap row.

`DWITHIN`, `DISTANCE(col, const) < r`, `WITHIN(col, box|polygon)`, and `COVERS(box|polygon, col)` are sargable. A `DWITHIN` against a constant `BOX`, `LINESTRING`, or `POLYGON` expands that geometry's bounding box by the radius. The optimizer covers the query with a geohash prefix range and keeps the original predicate as a residual (the prefix can over-select; the residual exactly re-evaluates the defined native predicate). A box that wraps the antimeridian falls back to a full index scan plus residual.

`EXPLAIN` shows `IndexScan … spatial`.

## Examples

```sql
CREATE TABLE places (
    id   UUID PRIMARY KEY DEFAULT UUID(),
    name STRING NOT NULL,
    loc  POINT NOT NULL
);

INSERT INTO places (name, loc) VALUES
    ('empire', POINT(-73.9857, 40.7484)),
    ('jfk',    'POINT(-73.7781 40.6413)');

CREATE SPATIAL INDEX ix_loc ON places (loc);

SELECT name FROM places
WHERE DWITHIN(loc, POINT(-73.9857, 40.7484), 5000);

SELECT name, DISTANCE(loc, POINT(-73.9857, 40.7484))
FROM places
WHERE WITHIN(loc, BOX(-74.1, 40.6, -73.8, 40.9));

SELECT name FROM places
WHERE WITHIN(loc, POLYGON('((-74.1 40.6, -73.8 40.6, -73.8 40.9, -74.1 40.9, -74.1 40.6))'));

SELECT DISTANCE_SPHEROID(POINT(-74.0060, 40.7128), POINT(-118.2437, 34.0522));

SELECT ST_Area(zone), ST_Perimeter(zone), ST_Centroid(zone)
FROM service_areas;

SELECT * FROM routes
WHERE ST_Intersects(path, POLYGON('((-74 40, -73 40, -73 41, -74 41, -74 40))'));
```

3D, geography-vs-geometry dual types, and spheroidal distance-to-polyline are not implemented.
