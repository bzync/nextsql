# Geospatial

Coordinates are **(longitude, latitude)** on WGS84. This is not PostGIS.

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

SELECT DISTANCE_SPHEROID(POINT(-74.0060, 40.7128), POINT(-118.2437, 34.0522));
```

## Types and literals

WKT also coerces: `POINT(lon lat)`, `BOX(w s, e n)`, `LINESTRING(...)`, `POLYGON((...))`.

| Type | Meaning |
|---|---|
| `POINT` / `LOCATION` | longitude, latitude |
| `BOX` | west, south, east, north |
| `LINESTRING` | at least two vertices |
| `POLYGON` | validated simple exterior ring, optional non-overlapping holes; 256-vertex cap |

`CREATE SPATIAL INDEX` requires a single `POINT` column (not `UNIQUE`). The optimizer uses a Morton geohash prefix for `DWITHIN`, `DISTANCE(col, const) < r`, `WITHIN`, and `COVERS`. The residual predicate is exact. `EXPLAIN` shows `IndexScan … spatial`.

## Distances

`DISTANCE` is haversine meters. `DISTANCE_SPHEROID` is Vincenty on the WGS84 ellipsoid; near-antipodal pairs fall back to haversine.

`DISTANCE`, `DWITHIN`, `INTERSECTS`, and `DISJOINT` accept every pair of
`POINT`, `BOX`, `LINESTRING`, and `POLYGON`. Also available: `LON` / `LAT`,
`COVERS`, `LINELENGTH`, polygon `AREA` / `PERIMETER`, `CENTROID`, `ENVELOPE`,
`GEOMETRYTYPE`, `NPOINTS`, `NRINGS`, and `ST_*` aliases. Polygon construction
rejects self-intersections and invalid hole topology.

3D, geography-vs-geometry dual types, and spheroidal distance-to-polyline are not implemented. Engine note: [`docs/geo.md`](https://github.com/bzync/nextsql/blob/main/docs/geo.md).
