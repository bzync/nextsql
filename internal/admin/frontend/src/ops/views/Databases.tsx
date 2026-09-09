import { useMemo, useState } from "react";
import { Badge, Inline, Tabs, TabsContent, TabsList, TabsTrigger, Text } from "@bzync/rui";
import { api, type ResultSet, type Whoami } from "../api";
import { useReadModel } from "../useReadModel";
import { ResultTable } from "../ResultTable";
import { ViewFrame } from "./ViewFrame";
import { Icon } from "../../shared/icons";

export function Databases({ who, onUnauthorized }: { who: Whoami; onUnauthorized: () => void }) {
  const { data, error, loading } = useReadModel(api.databases, onUnauthorized);
  return (
    <ViewFrame loading={loading} error={error} warnings={data?.warnings}>
      {data ? (
        <>
          <Inline gap="sm" align="center">
            <Badge variant="muted">Single-database deployment</Badge>
            <Text variant="muted" size="sm">
              A NextSQL deployment serves exactly one database.
            </Text>
          </Inline>

          <Tabs defaultValue="catalog" className="mt-4">
            <TabsList className="mb-4">
              <TabsTrigger value="catalog">
                <Inline gap="xs" align="center" wrap={false}>
                  <Icon name="database" size={14} />
                  <span>Databases</span>
                  <Badge variant="muted" size="sm">{data.databases.rows.length || 1}</Badge>
                </Inline>
              </TabsTrigger>
              <TabsTrigger value="stats">
                <Inline gap="xs" align="center" wrap={false}>
                  <Icon name="diagnostics" size={14} />
                  <span>Table statistics</span>
                  <Badge variant="muted" size="sm">{data.table_stats.rows.length}</Badge>
                </Inline>
              </TabsTrigger>
              <TabsTrigger value="storage">
                <Inline gap="xs" align="center" wrap={false}>
                  <Icon name="hard-drive" size={14} />
                  <span>Storage</span>
                  <Badge variant="muted" size="sm">{data.storage.rows.length}</Badge>
                </Inline>
              </TabsTrigger>
            </TabsList>

            <TabsContent value="catalog">
              <CatalogTree
                databases={data.databases}
                tables={data.tables}
                connectedDatabase={who.database}
              />
            </TabsContent>
            <TabsContent value="stats">
              <ResultTable result={data.table_stats} empty="No statistics collected yet" label="Table statistics" />
            </TabsContent>
            <TabsContent value="storage">
              <ResultTable result={data.storage} label="Storage" />
            </TabsContent>
          </Tabs>
        </>
      ) : null}
    </ViewFrame>
  );
}

// CatalogTree draws the containment the catalog actually has now that
// multi-realm hosting is gone: one database, holding tables. Each level is a
// disclosure row rather than an ARIA tree — nested <ul>/<li> plus
// aria-expanded buttons is the accessible shape here, and it does not owe the
// caller roving-tabindex keyboard semantics.
//
// One engine truth this view must not paper over: system.tables is the
// *connected* database's catalog, and system.databases is empty on a
// deployment with no registry, so the connected database is synthesized as
// the root rather than leaving the tables without a home.
function CatalogTree({
  databases,
  tables,
  connectedDatabase,
}: {
  databases: ResultSet;
  tables: ResultSet;
  connectedDatabase: string;
}) {
  const model = useMemo(() => buildTree(databases, connectedDatabase), [databases, connectedDatabase]);
  const tableCount = tables.rows?.length ?? 0;
  return (
    <div className="nsm-tree">
      <ul className="nsm-tree-list">
        {model.map((db) => (
          <DatabaseRow key={db.id || db.name} db={db} tables={tables} tableCount={tableCount} />
        ))}
      </ul>
    </div>
  );
}

function DatabaseRow({
  db,
  tables,
  tableCount,
}: {
  db: DbNode;
  tables: ResultSet;
  tableCount: number;
}) {
  const [open, setOpen] = useState(false);
  const bodyId = `database-${slug(db.id || db.name)}`;
  return (
    <li className="nsm-tree-node">
      <button
        type="button"
        className="nsm-tree-row"
        aria-expanded={open}
        aria-controls={bodyId}
        onClick={() => setOpen((v) => !v)}
      >
        <Icon name={open ? "chevron-down" : "chevron-right"} size={14} className="nsm-tree-chevron" />
        <Icon name="database" size={14} className="nsm-tree-icon" />
        <span className="nsm-tree-name">{db.name || db.id || "(unnamed database)"}</span>
        <span className="nsm-tree-badges">
          {db.connected ? <Badge variant="info" size="sm">connected</Badge> : null}
          {db.state ? <Badge variant="muted" size="sm">{db.state}</Badge> : null}
          {db.capBytes > 0 ? <Badge variant="muted" size="sm">cap {humanBytes(db.capBytes)}</Badge> : null}
          {db.connected ? (
            <Badge variant="muted" size="sm">
              {tableCount} {tableCount === 1 ? "table" : "tables"}
            </Badge>
          ) : null}
        </span>
      </button>
      <div id={bodyId} hidden={!open} className="nsm-tree-body">
        {db.connected ? (
          <ResultTable
            result={tables}
            empty="No user tables"
            label={`Tables in ${db.name || db.id}`}
            defaultPageSize={10}
          />
        ) : (
          <Text variant="muted" size="sm">
            Tables are read from the database this session is connected to. Sign
            in with {db.name || db.id} selected to browse its tables.
          </Text>
        )}
      </div>
    </li>
  );
}

type DbNode = {
  id: string;
  name: string;
  state: string;
  capBytes: number;
  connected: boolean;
};

// buildTree reads system.databases, which holds exactly one row — the
// database this deployment serves — and is empty when no deployment registry
// is attached. The empty case still yields the connected database, so the
// tables never lose their home in the tree.
export function buildTree(databases: ResultSet, connectedDatabase: string): DbNode[] {
  const dbs: DbNode[] = rowsOf(databases).map((get) => {
    const name = get("name");
    return {
      id: get("database_id"),
      name,
      state: get("state"),
      capBytes: num(get("storage_cap_bytes")),
      connected: name === connectedDatabase,
    };
  });
  if (dbs.length === 0) {
    dbs.push({
      id: "",
      name: connectedDatabase || "default",
      state: "",
      capBytes: 0,
      connected: true,
    });
  }
  return dbs;
}

// rowsOf yields a column-name accessor per row so a catalog column that is
// absent (an older server, a narrower RBAC projection) reads as "" instead of
// shifting every other value.
function rowsOf(rs: ResultSet | undefined): Array<(col: string) => string> {
  if (!rs || !rs.columns || rs.columns.length === 0) return [];
  const index = new Map(rs.columns.map((c, i) => [c, i]));
  return (rs.rows ?? []).map((row) => (col: string) => {
    const i = index.get(col);
    return i === undefined ? "" : String(row[i] ?? "");
  });
}

function num(v: string): number {
  const n = Number(v);
  return Number.isFinite(n) ? n : 0;
}

function slug(s: string): string {
  return s.replace(/[^A-Za-z0-9_-]+/g, "-") || "x";
}

function humanBytes(n: number): string {
  if (!Number.isFinite(n) || n < 0) return String(n);
  const units = ["B", "KiB", "MiB", "GiB", "TiB", "PiB"];
  let v = n;
  let u = 0;
  while (v >= 1024 && u < units.length - 1) {
    v /= 1024;
    u++;
  }
  return `${u === 0 ? v : v.toFixed(2)} ${units[u]}`;
}
