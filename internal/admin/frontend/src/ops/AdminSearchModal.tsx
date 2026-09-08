import { useEffect, useMemo, useRef, useState } from "react";
import { Badge, Input, Kbd, Modal, ModalBody, ModalHeader, ModalTitle, Stack, Text, useTheme } from "@bzync/rui";
import { Icon, type IconName } from "../shared/icons";
import { api, type Whoami } from "./api";

export type SearchItem = {
  id: string;
  category: "Workspaces" | "Tables" | "Studio Tools" | "Actions";
  label: string;
  description: string;
  icon: IconName;
  keywords?: string;
  badge?: string;
  run: () => void;
};

const WORKSPACES: { id: string; label: string; description: string; icon: IconName; keywords: string }[] = [
  { id: "overview", label: "Overview", description: "System health, node status, and cluster metrics", icon: "overview", keywords: "home dashboard metrics nodes status health" },
  { id: "studio", label: "Studio", description: "Interactive SQL query editor, table inspector, and results grid", icon: "studio", keywords: "sql query editor console run explain select tables ide" },
  { id: "databases", label: "Databases", description: "Storage breakdown, databases, realms, and table catalogs", icon: "database", keywords: "tables schemas storage size realms metadata" },
  { id: "activity", label: "Activity", description: "Active queries, live transactions, lock waits, and sessions", icon: "activity", keywords: "queries locks transactions sessions blocking deadlocks" },
  { id: "security", label: "Security", description: "Users, roles, privileges, RBAC grants, and audit log", icon: "shield", keywords: "users roles rbac grants privileges passwords audit" },
  { id: "cluster", label: "Cluster", description: "Raft consensus, replica nodes, partitions, and membership", icon: "cluster", keywords: "raft nodes consensus replicas leadership quorum" },
  { id: "backups", label: "Backups", description: "Physical backups, point-in-time recovery, and restore drills", icon: "archive", keywords: "backup pitr restore snapshots wal archiving" },
  { id: "maintenance", label: "Maintenance", description: "Compaction, index rebuilds, checkpoints, and storage vacuum", icon: "wrench", keywords: "vacuum compaction checkpoint reindex btree optimize" },
  { id: "configuration", label: "Configuration", description: "Engine parameters, memory buffers, WAL, and tuning", icon: "sliders", keywords: "settings config parameters buffers memory wal limits" },
  { id: "diagnostics", label: "Diagnostics", description: "Telemetry, crash dumps, health inspection, and logs", icon: "diagnostics", keywords: "logs health diagnostics dump errors crash profiling" },
];

const STUDIO_TOOLS: { id: string; label: string; description: string; icon: IconName; keywords: string }[] = [
  { id: "fulltext", label: "Full-text Search Explorer", description: "Build native SEARCH queries with BM25, snippets, and highlights", icon: "search", keywords: "bm25 text snippets search analyzer" },
  { id: "vector", label: "Vector Search Explorer", description: "Build native NEAREST queries over dense and sparse embeddings", icon: "layers", keywords: "nearest vector ann cosine euclidean hamming embedding" },
  { id: "hybrid", label: "Hybrid Search Explorer", description: "Combine structured filters, full-text search, and vector ranking", icon: "network", keywords: "hybrid search vector structured filter rank" },
  { id: "geo", label: "Geo & Spatial Explorer", description: "Visual map query builder for POINT coordinates and polygons", icon: "map-pin", keywords: "geo spatial point polygon dwithin within coordinates" },
  { id: "diagram", label: "Schema Diagram (ER)", description: "Visual foreign-key relationships and entity-relationship graph", icon: "network", keywords: "er diagram foreign keys schema relationships graph" },
  { id: "design", label: "Design Table or Index", description: "Visual schema designer for CREATE TABLE and CREATE INDEX DDL", icon: "table", keywords: "create table ddl columns indexes designer primary key" },
  { id: "datagen", label: "Generate Development Data", description: "Synthetic fixture and mock data generator with typed generators", icon: "plus", keywords: "generate seed mock fixtures synthetic rows sample" },
  { id: "import", label: "Import CSV / JSON Data", description: "Batch data importer from CSV, TSV, JSON, and NDJSON files", icon: "download", keywords: "import csv json tsv ndjson load upload file" },
  { id: "dml", label: "Parameterized DML Builder", description: "Interactive parameterized INSERT, UPDATE, and DELETE builder", icon: "file", keywords: "dml insert update delete bind parameters $1 template" },
  { id: "grant", label: "GRANT / REVOKE Builder", description: "Interactive permission builder for users, roles, and tables", icon: "key", keywords: "grant revoke permissions privileges rbac security" },
  { id: "users", label: "Users & Roles Explorer", description: "Inspect authorized users, assigned roles, and granted rights", icon: "users", keywords: "users roles privileges accounts logins" },
  { id: "locks", label: "Transactions & Locks Explorer", description: "Inspect active transactions, lock holders, and waiting sessions", icon: "lock", keywords: "transactions locks sessions blocking activity" },
  { id: "audit", label: "Audit Log Viewer", description: "Inspect append-only audit trail and cryptographic hash-chain signatures", icon: "shield", keywords: "audit trail hash chain signatures verify compliance" },
  { id: "workflows", label: "Workflows & CDC Explorer", description: "Inspect triggers, scheduled workflows, and change streams", icon: "activity", keywords: "workflows cdc change streams triggers schedules tasks" },
  { id: "migrations", label: "Migrations History Explorer", description: "Inspect schema migration versions, execution timestamps, and status", icon: "clock", keywords: "migrations history schema version applied dirty" },
];

export function AdminSearchModal({
  open,
  onClose,
  onNavigate,
  who: _who,
  onSwitchConnection,
  onSignOut,
  onOpenSettings,
}: {
  open: boolean;
  onClose: () => void;
  onNavigate: (route: string) => void;
  who: Whoami;
  onSwitchConnection?: () => void;
  onSignOut?: () => void;
  onOpenSettings?: () => void;
}) {
  const [query, setQuery] = useState("");
  const [activeIndex, setActiveIndex] = useState(0);
  const inputRef = useRef<HTMLInputElement>(null);
  const { setTheme } = useTheme();
  const [tables, setTables] = useState<string[]>([]);

  useEffect(() => {
    if (!open) return;
    api.databases()
      .then((data) => {
        if (!data?.tables?.rows) return;
        const nameIdx = data.tables.columns.findIndex((c) => c === "table_name" || c === "name");
        if (nameIdx >= 0) {
          const names = data.tables.rows
            .map((r) => r[nameIdx])
            .filter((n): n is string => typeof n === "string" && n.length > 0);
          setTables(names);
        }
      })
      .catch(() => {
        /* non-fatal; search continues without table list */
      });
  }, [open]);

  useEffect(() => {
    if (open) {
      setQuery("");
      setActiveIndex(0);
      window.setTimeout(() => inputRef.current?.focus(), 30);
    }
  }, [open]);

  const allItems = useMemo<SearchItem[]>(() => {
    const items: SearchItem[] = [];

    for (const ws of WORKSPACES) {
      items.push({
        id: `ws-${ws.id}`,
        category: "Workspaces",
        label: ws.label,
        description: ws.description,
        icon: ws.icon,
        keywords: ws.keywords,
        badge: "Workspace",
        run: () => {
          onClose();
          onNavigate(ws.id);
        },
      });
    }

    for (const tbl of tables) {
      items.push({
        id: `tbl-${tbl}`,
        category: "Tables",
        label: tbl,
        description: `Inspect and query "${tbl}" in Studio`,
        icon: "table",
        keywords: `table ${tbl} data columns`,
        badge: "Table",
        run: () => {
          onClose();
          onNavigate(`studio?table=${encodeURIComponent(tbl)}`);
        },
      });
    }

    for (const tool of STUDIO_TOOLS) {
      items.push({
        id: `tool-${tool.id}`,
        category: "Studio Tools",
        label: tool.label,
        description: tool.description,
        icon: tool.icon,
        keywords: tool.keywords,
        badge: "Tool",
        run: () => {
          onClose();
          onNavigate("studio");
        },
      });
    }

    items.push({
      id: "act-switch-connection",
      category: "Actions",
      label: "Switch Connection…",
      description: "Switch to another database or realm on this server",
      icon: "plug",
      keywords: "switch connection database realm reconnect server",
      badge: "Action",
      run: () => {
        onClose();
        onSwitchConnection?.();
      },
    });

    items.push({
      id: "act-theme-dark",
      category: "Actions",
      label: "Set Theme: Dark",
      description: "Switch NextSQL Admin to dark mode",
      icon: "sliders",
      keywords: "theme dark appearance mode color",
      badge: "Theme",
      run: () => {
        onClose();
        setTheme("dark");
      },
    });

    items.push({
      id: "act-theme-light",
      category: "Actions",
      label: "Set Theme: Light",
      description: "Switch NextSQL Admin to light mode",
      icon: "sliders",
      keywords: "theme light appearance mode color",
      badge: "Theme",
      run: () => {
        onClose();
        setTheme("light");
      },
    });

    items.push({
      id: "act-theme-system",
      category: "Actions",
      label: "Set Theme: Follow System",
      description: "Follow your operating system's theme preference",
      icon: "refresh",
      keywords: "theme system auto appearance",
      badge: "Theme",
      run: () => {
        onClose();
        setTheme("system");
      },
    });

    if (onOpenSettings) {
      items.push({
        id: "act-user-settings",
        category: "Actions",
        label: "User & Workspace Settings",
        description: "Configure operator preferences, table density, and session parameters",
        icon: "settings",
        keywords: "user settings preferences profile identity density page size theme auto refresh",
        badge: "Settings",
        run: () => {
          onClose();
          onOpenSettings();
        },
      });
    }

    if (onSignOut) {
      items.push({
        id: "act-sign-out",
        category: "Actions",
        label: "Sign Out",
        description: "End this authenticated Admin session",
        icon: "logout",
        keywords: "sign out log out exit disconnect",
        badge: "Auth",
        run: () => {
          onClose();
          onSignOut();
        },
      });
    }

    return items;
  }, [tables, onNavigate, onClose, onSwitchConnection, onSignOut, onOpenSettings, setTheme]);

  const filteredItems = useMemo(() => {
    const q = query.trim().toLowerCase();
    if (!q) return allItems.slice(0, 18);

    const tokens = q.split(/\s+/).filter(Boolean);
    return allItems
      .filter((item) => {
        const text = `${item.label} ${item.description} ${item.keywords ?? ""} ${item.category}`.toLowerCase();
        return tokens.every((token) => text.includes(token));
      })
      .slice(0, 24);
  }, [allItems, query]);

  useEffect(() => {
    setActiveIndex(0);
  }, [filteredItems]);

  const activate = (item: SearchItem | undefined) => {
    if (!item) return;
    item.run();
  };

  if (!open) return null;

  return (
    <Modal open onClose={onClose} size="md" ariaLabel="Search NextSQL Admin" scrollable>
      <ModalHeader className="nsa-search-modal-header">
        <ModalTitle>Search NextSQL Admin</ModalTitle>
      </ModalHeader>
      <ModalBody scrollable className="nsa-search-modal-body">
        <Stack gap="sm">
          <Input
            ref={inputRef}
            id="admin-search-input"
            label="Search NextSQL Admin navigation and tools"
            labelClassName="sr-only"
            size="sm"
            value={query}
            placeholder="Type to search workspaces, tables, tools, or actions…"
            role="combobox"
            aria-expanded
            aria-controls="admin-search-results"
            aria-activedescendant={filteredItems[activeIndex] ? `admin-search-opt-${activeIndex}` : undefined}
            onChange={(event) => setQuery(event.currentTarget.value)}
            onKeyDown={(event) => {
              if (event.key === "ArrowDown") {
                event.preventDefault();
                setActiveIndex((index) => Math.min(index + 1, Math.max(filteredItems.length - 1, 0)));
              } else if (event.key === "ArrowUp") {
                event.preventDefault();
                setActiveIndex((index) => Math.max(index - 1, 0));
              } else if (event.key === "Enter") {
                event.preventDefault();
                activate(filteredItems[activeIndex]);
              }
            }}
          />
          <div className="nsa-search-hints">
            <Text size="xs" variant="muted">
              Use <Kbd keys={["↑"]} size="sm" /> <Kbd keys={["↓"]} size="sm" /> to navigate, <Kbd keys={["Enter"]} size="sm" /> to select, <Kbd keys={["Esc"]} size="sm" /> to close.
            </Text>
          </div>
          {filteredItems.length === 0 ? (
            <div className="nsa-search-empty">
              <Text size="sm" variant="muted">No matching workspace, table, or action found for &quot;{query}&quot;.</Text>
            </div>
          ) : (
            <ul id="admin-search-results" className="nsa-search-results-list" role="listbox" aria-label="Search results">
              {filteredItems.map((item, index) => {
                const isActive = index === activeIndex;
                return (
                  <li key={item.id} role="presentation">
                    <button
                      type="button"
                      id={`admin-search-opt-${index}`}
                      role="option"
                      aria-selected={isActive}
                      className={`nsa-search-option${isActive ? " nsa-search-option-active" : ""}`}
                      onMouseEnter={() => setActiveIndex(index)}
                      onClick={() => activate(item)}
                    >
                      <span className="nsa-search-opt-icon" aria-hidden="true">
                        <Icon name={item.icon} size={16} />
                      </span>
                      <div className="nsa-search-opt-content">
                        <div className="nsa-search-opt-top">
                          <span className="nsa-search-opt-title">{item.label}</span>
                          {item.badge ? (
                            <Badge
                              size="sm"
                              variant={
                                item.category === "Workspaces" ? "info"
                                  : item.category === "Tables" ? "success"
                                    : item.category === "Studio Tools" ? "warning"
                                      : "muted"
                              }
                            >
                              {item.badge}
                            </Badge>
                          ) : null}
                        </div>
                        <span className="nsa-search-opt-desc">{item.description}</span>
                      </div>
                    </button>
                  </li>
                );
              })}
            </ul>
          )}
        </Stack>
      </ModalBody>
    </Modal>
  );
}
