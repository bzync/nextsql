import { useMemo, useState, type ReactNode } from "react";
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
            <Badge variant={data.hosted ? "info" : "muted"}>
              {data.hosted ? "Hosted · multi-database" : "Single-database"}
            </Badge>
            <Text variant="muted" size="sm">
              {data.hosted
                ? "This node is serving a hosted deployment."
                : "This node is serving one database."}
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
                realms={data.realms}
                databases={data.databases}
                tables={data.tables}
                connectedRealm={who.realm}
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

// CatalogTree replaces the old flat Realms/Databases/Tables tabs with the
// containment the catalog actually has: a realm holds databases, a database
// holds tables. Each level is a disclosure row rather than an ARIA tree —
// nested <ul>/<li> plus aria-expanded buttons is the accessible shape here,
// and it does not owe the caller roving-tabindex keyboard semantics.
//
// One engine truth this view must not paper over: system.tables is the
// *connected* database's catalog. One Ops connection binds one realm and one
// database at handshake, so no other database's tables are reachable from
// this session. Expanding a database that is not the connected one says so
// instead of showing an empty or borrowed table list.
function CatalogTree({
  realms,
  databases,
  tables,
  connectedRealm,
  connectedDatabase,
}: {
  realms: ResultSet;
  databases: ResultSet;
  tables: ResultSet;
  connectedRealm: string;
  connectedDatabase: string;
}) {
  const model = useMemo(
    () => buildTree(realms, databases, connectedRealm, connectedDatabase),
    [realms, databases, connectedRealm, connectedDatabase],
  );

  const tableCount = tables.rows?.length ?? 0;
  const renderDatabases = (dbs: DbNode[]) => (
    <ul className="nsm-tree-list">
      {dbs.map((db) => (
        <DatabaseRow
          key={`${db.realmId}/${db.id || db.name}`}
          db={db}
          tables={tables}
          tableCount={tableCount}
        />
      ))}
    </ul>
  );

  if (model.realms.length === 0) {
    // Non-multi-realm deployment: system.realms is empty, so there is no realm
    // level to draw and the databases are the roots.
    return <div className="nsm-tree">{renderDatabases(model.databases)}</div>;
  }

  return (
    <div className="nsm-tree">
      <ul className="nsm-tree-list">
        {model.realms.map((realm) => (
          <RealmRow key={realm.id || realm.name} realm={realm}>
            {renderDatabases(realm.databases)}
          </RealmRow>
        ))}
      </ul>
    </div>
  );
}

function RealmRow({ realm, children }: { realm: RealmNode; children: ReactNode }) {
  const [open, setOpen] = useState(realm.connected);
  const bodyId = `realm-${slug(realm.id || realm.name)}`;
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
        <Icon name="layers" size={14} className="nsm-tree-icon" />
        <span className="nsm-tree-name">{realm.name || realm.id || "(unnamed realm)"}</span>
        <span className="nsm-tree-badges">
          {realm.connected ? <Badge variant="info" size="sm">connected</Badge> : null}
          {realm.state ? <Badge variant="muted" size="sm">{realm.state}</Badge> : null}
          <Badge variant="muted" size="sm">
            {realm.databases.length} {realm.databases.length === 1 ? "database" : "databases"}
          </Badge>
          {realm.capBytes > 0 ? <Badge variant="muted" size="sm">cap {humanBytes(realm.capBytes)}</Badge> : null}
          {realm.rootDelegated ? <Badge variant="warning" size="sm">realm root delegated</Badge> : null}
        </span>
      </button>
      <div id={bodyId} hidden={!open} className="nsm-tree-body">
        {realm.databases.length === 0 ? (
          <Text variant="muted" size="sm">No databases registered in this realm.</Text>
        ) : (
          children
        )}
      </div>
    </li>
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
  const bodyId = `database-${slug(`${db.realmId}-${db.id || db.name}`)}`;
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
          {db.layout ? <Badge variant="muted" size="sm">{db.layout}</Badge> : null}
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
  realmId: string;
  realmName: string;
  id: string;
  name: string;
  state: string;
  layout: string;
  capBytes: number;
  connected: boolean;
};

type RealmNode = {
  id: string;
  name: string;
  state: string;
  capBytes: number;
  rootDelegated: boolean;
  connected: boolean;
  databases: DbNode[];
};

type Tree = { realms: RealmNode[]; databases: DbNode[] };

// buildTree joins system.databases onto system.realms by realm_id. Both are
// empty on a non-hosted deployment, which is reported by the catalog rather
// than an error — that case still gets one database row (the connected one)
// so the tables never lose their home in the tree.
export function buildTree(
  realms: ResultSet,
  databases: ResultSet,
  connectedRealm: string,
  connectedDatabase: string,
): Tree {
  const dbs: DbNode[] = rowsOf(databases).map((get) => {
    const realmName = get("realm_name");
    const name = get("name");
    return {
      realmId: get("realm_id"),
      realmName,
      id: get("database_id"),
      name,
      state: get("state"),
      layout: get("layout"),
      capBytes: num(get("storage_cap_bytes")),
      connected: sameTarget(realmName, get("realm_id"), connectedRealm) && name === connectedDatabase,
    };
  });

  const realmNodes: RealmNode[] = rowsOf(realms).map((get) => {
    const id = get("realm_id");
    const name = get("name");
    return {
      id,
      name,
      state: get("state"),
      capBytes: num(get("storage_cap_bytes")),
      rootDelegated: truthy(get("realm_root_delegated")),
      connected: sameTarget(name, id, connectedRealm),
      databases: [],
    };
  });

  const byId = new Map<string, RealmNode>();
  for (const r of realmNodes) if (r.id) byId.set(r.id, r);
  for (const db of dbs) {
    const parent = byId.get(db.realmId);
    if (parent) {
      parent.databases.push(db);
      continue;
    }
    // A database whose realm is not in system.realms (a realm the caller
    // cannot see, or a registry read that raced a realm drop) still belongs
    // somewhere visible rather than being dropped from the view.
    const orphan: RealmNode = {
      id: db.realmId,
      name: db.realmName || db.realmId || "(unknown realm)",
      state: "",
      capBytes: 0,
      rootDelegated: false,
      connected: sameTarget(db.realmName, db.realmId, connectedRealm),
      databases: [db],
    };
    byId.set(db.realmId, orphan);
    realmNodes.push(orphan);
  }

  if (realmNodes.length === 0 && dbs.length === 0) {
    dbs.push({
      realmId: "",
      realmName: connectedRealm,
      id: "",
      name: connectedDatabase || "default",
      state: "",
      layout: "",
      capBytes: 0,
      connected: true,
    });
  }
  return { realms: realmNodes, databases: dbs };
}

// The session reports one target realm; system.realms/system.databases name it
// by realm name, and the registry by realm id. Either identifies the session's
// own realm. An empty session realm means the non-realm default.
function sameTarget(realmName: string, realmID: string, connectedRealm: string): boolean {
  if (connectedRealm === "") return realmName === "" || realmName === "default" || realmID === "";
  return realmName === connectedRealm || realmID === connectedRealm;
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

function truthy(v: string): boolean {
  return v === "true" || v === "yes" || v === "1";
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
