import { useCallback, useEffect, useMemo, useRef, useState, type KeyboardEvent } from "react";
import {
  Alert,
  Badge,
  Button,
  Card,
  CardBody,
  CardHeader,
  CardTitle,
  Checkbox,
  CodeEditor,
  ConfirmDialog,
  CopyButton,
  EmptyState,
  Heading,
  Inline,
  Input,
  Popover,
  PopoverContent,
  Select,
  Spinner,
  Stack,
  Text,
} from "@bzync/rui";
import { Icon, type IconName } from "../shared/icons";
import {
  ApiError,
  api,
  type Activity,
  type Security,
  type ServerConnection,
  type StudioAnalysis,
  type StudioDiagnostic,
  type StudioBootstrap,
  type StudioReadConsistency,
  type StudioQueryParam,
  type StudioMigrationHistory,
  type StudioResultSet,
  type StudioSchemaGraph,
  type StudioTableDetail,
  type StudioWorkflowOverview,
  type Whoami,
} from "../ops/api";
import { GrantBuilder } from "./GrantBuilder";
import { FullTextExplorer } from "./FullTextExplorer";
import { VectorExplorer } from "./VectorExplorer";
import { HybridExplorer } from "./HybridExplorer";
import { GeoExplorer } from "./GeoExplorer";
import { SecurityExplorer } from "./SecurityExplorer";
import { ActivityExplorer } from "./ActivityExplorer";
import { AuditExplorer } from "./AuditExplorer";
import { WorkflowExplorer } from "./WorkflowExplorer";
import { MigrationExplorer } from "./MigrationExplorer";
import { SchemaDiagramExplorer } from "./SchemaDiagramExplorer";
import { DataGeneratorExplorer } from "./DataGeneratorExplorer";
import { ImportExplorer } from "./ImportExplorer";
import { VectorImportExplorer } from "./VectorImportExplorer";
import { DMLBuilderExplorer } from "./DMLBuilderExplorer";
import { SchemaDesignerExplorer, type SchemaDesignerMode } from "./SchemaDesignerExplorer";
import { ObjectSearch } from "./ObjectSearch";
import { CommandPalette, type StudioCommand } from "./CommandPalette";
import { SwitchConnection } from "./SwitchConnection";
import { SavedQueries } from "./SavedQueries";
import { SchemaTree } from "./SchemaTree";
import { ResultGrid } from "./ResultGrid";
import {
  STUDIO_ENVIRONMENTS,
  DEFAULT_STUDIO_LAYOUT,
  LAYOUT_WIDTH_STEP,
  MAX_EXPLORER_WIDTH,
  MAX_INSPECTOR_WIDTH,
  MIN_EXPLORER_WIDTH,
  MIN_INSPECTOR_WIDTH,
  applyTableNameFix,
  currentJSONPathRange,
  currentNearestContext,
  currentWordRange,
  editorDraftStorageKey,
  detectEditableTable,
  editorDraftsWorthRestoring,
  environmentStorageKey,
  extractQueryParams,
  extractReferencedTables,
  findAllMatches,
  formatSQL,
  grantStateFromRow,
  isStudioEnvironment,
  jsonPathIndexPaths,
  layoutStorageKey,
  namesFromResult,
  parseStudioLayout,
  queryResultSummary,
  realmScopeWarning,
  resetStudioLayout,
  resultColumn,
  nextMatchIndex,
  previousMatchIndex,
  parseEditorDrafts,
  parseRecentConnections,
  recentConnectionStorageKey,
  recordRecentConnection,
  serializeRecentConnections,
  parseSavedQueries,
  parseSavedQueriesExport,
  downloadSavedQueries,
  mergeSavedQueries,
  MAX_SAVED_QUERY_IMPORT_BYTES,
  MAX_TABLE_CONSTRAINT_ROWS,
  rankJSONPathSuggestions,
  rankNearestColumnSuggestions,
  rankNearestMetricSuggestions,
  rankSQLSuggestions,
  removeSavedQuery,
  replaceAllMatches,
  savedQueryStorageKey,
  serializeEditorDrafts,
  serializeSavedQueries,
  serializeStudioLayout,
  stepLayoutWidth,
  upsertSavedQuery,
  suggestTableNameFixes,
  tableColumnTypesRecord,
  tableConstraintsResult,
  tablePKColumns,
  vectorColumnsFromResult,
  withCachedTableLoading,
  type FindMatch,
  type ExplainPlanSnapshot,
  type FullTextResultContext,
  type GrantBuilderState,
  type HybridResultContext,
  type RankResultContext,
  type SQLSuggestion,
  type SavedQuery,
  type StudioEnvironment,
  type RecentConnection,
  type TableColumnsCache,
  type TableJSONPathCache,
  type TableNameFix,
  type TableVectorCache,
} from "./resultTools";

const DEFAULT_SQL = "SELECT * FROM system.capabilities ORDER BY name";

function columnIndex(result: StudioResultSet, name: string): number {
  return result.columns.findIndex((column) => column === name);
}

function tableNames(bootstrap: StudioBootstrap | null): string[] {
  if (!bootstrap) return [];
  const nameIndex = columnIndex(bootstrap.tables, "name");
  if (nameIndex < 0) return [];
  return bootstrap.tables.rows
    .map((row) => row[nameIndex])
    .filter((name): name is string => typeof name === "string");
}

function JSONColumnNames(detail: StudioTableDetail | null): string[] {
  if (!detail) return [];
  const nameIndex = columnIndex(detail.columns, "column_name");
  const typeIndex = columnIndex(detail.columns, "type");
  if (nameIndex < 0 || typeIndex < 0) return [];
  return detail.columns.rows
    .filter((row) => row[typeIndex]?.trim().toUpperCase() === "JSON")
    .map((row) => row[nameIndex])
    .filter((name): name is string => typeof name === "string");
}

// tableDDLScript joins the system.table_ddl rows for one table into a single
// runnable script: the CREATE TABLE first (rows arrive object_type DESC), then
// each CREATE INDEX, every statement terminated with a semicolon.
function tableDDLScript(detail: StudioTableDetail | null): string {
  if (!detail?.ddl) return "";
  const ddlIndex = columnIndex(detail.ddl, "ddl");
  if (ddlIndex < 0) return "";
  return detail.ddl.rows
    .map((row) => row[ddlIndex])
    .filter((value): value is string => typeof value === "string" && value.trim().length > 0)
    .map((value) => (value.trimEnd().endsWith(";") ? value.trimEnd() : `${value.trimEnd()};`))
    .join("\n\n");
}

function capabilityAvailable(bootstrap: StudioBootstrap | null, name: string): boolean {
  if (!bootstrap) return false;
  const nameIndex = columnIndex(bootstrap.capabilities, "name");
  const statusIndex = columnIndex(bootstrap.capabilities, "status");
  if (nameIndex < 0 || statusIndex < 0) return false;
  const row = bootstrap.capabilities.rows.find((candidate) => candidate[nameIndex] === name);
  const status = row?.[statusIndex]?.toLowerCase();
  return status === "supported" || status === "production" || status === "experimental";
}

function queryID(): string {
  if (typeof crypto.randomUUID === "function") return crypto.randomUUID();
  const random = crypto.getRandomValues(new Uint32Array(2));
  return `studio-${Date.now().toString(36)}-${random[0].toString(36)}${random[1].toString(36)}`;
}

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

// Query history is an in-memory, per-session record only: it is never
// written to localStorage/sessionStorage or sent anywhere, and disappears on
// reload or navigation away from Studio — the privacy control is "does not
// persist," backed by an explicit Clear action and a bounded size so a
// session of large statements can't grow this without limit.
type StudioHistoryEntry = {
  id: string;
  sql: string;
  ranAt: number;
  isSelection: boolean;
  outcome: "success" | "canceled" | "error";
  rowCount?: number;
  elapsedMs?: number;
  error?: string;
};

const MAX_HISTORY_ENTRIES = 50;
const MAX_HISTORY_BYTES = 2 << 20;

function historyEntryBytes(entry: StudioHistoryEntry): number {
  return entry.sql.length + (entry.error?.length ?? 0);
}

function pushHistory(current: StudioHistoryEntry[], entry: StudioHistoryEntry): StudioHistoryEntry[] {
  const next = [entry, ...current];
  let bytes = next.reduce((sum, e) => sum + historyEntryBytes(e), 0);
  while (next.length > 1 && (next.length > MAX_HISTORY_ENTRIES || bytes > MAX_HISTORY_BYTES)) {
    bytes -= historyEntryBytes(next.pop() as StudioHistoryEntry);
  }
  return next;
}

function historyPreview(sql: string): string {
  const flattened = sql.replace(/\s+/g, " ").trim();
  return flattened.length > 80 ? `${flattened.slice(0, 80)}…` : flattened;
}

// Each tab is an independent SQL buffer plus its own last result/error —
// but not its own "running" state: the server allows at most one active
// Studio query per session (a second attempt fails fast with 409), so
// running/cancellation stay single, session-wide state tagged with which
// tab owns the in-flight query (runningTabId below), rather than letting
// every tab believe it can run concurrently.
type StudioTab = {
  id: string;
  title: string;
  sql: string;
  result: StudioResultSet | null;
  queryError: string | null;
  ranSelection: boolean;
  script: ScriptStatementResult[] | null;
  scriptSelected: number | null;
  resultContext: RankResultContext | null;
  planBaseline: ExplainPlanSnapshot | null;
  // Per-placeholder bind values for a parameterized Run, keyed by the 1-based
  // positional number ($1 → 1). Ephemeral tab state — never persisted (the
  // editor drafts are SQL text only).
  paramValues: Record<number, { text: string; isNull: boolean }>;
};

const MAX_STUDIO_TABS = 8;

function newTab(id: string, title: string, sql: string): StudioTab {
  return {
    id, title, sql, result: null, queryError: null, ranSelection: false,
    script: null, scriptSelected: null, resultContext: null, planBaseline: null,
    paramValues: {},
  };
}

// Execute Script runs every statement in the buffer sequentially — NextSQL
// has no server batch mode, and a tab can only ever have one active query —
// stopping at the first failure so a script never silently continues past a
// statement that did not do what it was supposed to.
type ScriptStatementResult = {
  sql: string;
  status: "pending" | "running" | "success" | "error" | "canceled" | "skipped";
  result?: StudioResultSet;
  error?: string;
};

const MAX_SCRIPT_CONFIRM_REASONS = 10;

function LayoutSplitter({
  label,
  value,
  min,
  max,
  invert,
  className,
  onChange,
}: {
  label: string;
  value: number;
  min: number;
  max: number;
  invert?: boolean;
  className?: string;
  onChange: (next: number) => void;
}) {
  const drag = useRef<{ pointerId: number; startX: number; startW: number } | null>(null);

  const applyDelta = useCallback(
    (dx: number) => {
      onChange(stepLayoutWidth(drag.current?.startW ?? value, invert ? -dx : dx, min, max));
    },
    [invert, max, min, onChange, value],
  );

  return (
    <div
      role="separator"
      aria-orientation="vertical"
      aria-label={label}
      aria-valuenow={value}
      aria-valuemin={min}
      aria-valuemax={max}
      tabIndex={0}
      className={className ? `nss-splitter ${className}` : "nss-splitter"}
      onPointerDown={(event) => {
        event.currentTarget.setPointerCapture(event.pointerId);
        drag.current = { pointerId: event.pointerId, startX: event.clientX, startW: value };
      }}
      onPointerMove={(event) => {
        if (!drag.current || drag.current.pointerId !== event.pointerId) return;
        applyDelta(event.clientX - drag.current.startX);
      }}
      onPointerUp={(event) => {
        if (drag.current?.pointerId === event.pointerId) drag.current = null;
      }}
      onLostPointerCapture={() => {
        drag.current = null;
      }}
      onKeyDown={(event) => {
        if (event.key === "ArrowLeft") {
          event.preventDefault();
          onChange(stepLayoutWidth(value, invert ? LAYOUT_WIDTH_STEP : -LAYOUT_WIDTH_STEP, min, max));
        } else if (event.key === "ArrowRight") {
          event.preventDefault();
          onChange(stepLayoutWidth(value, invert ? -LAYOUT_WIDTH_STEP : LAYOUT_WIDTH_STEP, min, max));
        } else if (event.key === "Home") {
          event.preventDefault();
          onChange(min);
        } else if (event.key === "End") {
          event.preventDefault();
          onChange(max);
        }
      }}
    />
  );
}

export function StudioWorkspace({
  who,
  onUnauthorized,
  onConnectionChanged,
  serverConnection,
  connectionChecking,
  onRetryConnection,
  routeTable,
  onSelectRouteTable,
}: {
  who: Whoami;
  onUnauthorized: () => void;
  onConnectionChanged: (next: { realm: string; database: string }) => void;
  serverConnection: ServerConnection | null;
  connectionChecking: boolean;
  onRetryConnection: () => void;
  routeTable?: string;
  onSelectRouteTable?: (table: string) => void;
}) {
  const [bootstrap, setBootstrap] = useState<StudioBootstrap | null>(null);
  const [switchOpen, setSwitchOpen] = useState(false);
  const [recentConnections, setRecentConnections] = useState<RecentConnection[]>([]);
  const [paletteOpen, setPaletteOpen] = useState(false);
  const [readMode, setReadMode] = useState<StudioReadConsistency>("strong");
  const [stalenessSec, setStalenessSec] = useState(0);
  const [readConsistencyError, setReadConsistencyError] = useState<string | null>(null);
  const [applyingReadMode, setApplyingReadMode] = useState(false);
  const [bootstrapError, setBootstrapError] = useState<string | null>(null);
  const [loadingBootstrap, setLoadingBootstrap] = useState(true);
  const [filter, setFilter] = useState("");
  // Layout persistence: pane visibility/widths and the last selected table
  // name (never SQL, results, or a credential). Hydrated once from this
  // connection's localStorage slot; a missing/malformed value is the default.
  const layoutKey = layoutStorageKey(who.realm ?? "", who.database ?? "", who.user ?? "");
  const [initialLayout] = useState(() => {
    try {
      return parseStudioLayout(window.localStorage.getItem(layoutKey));
    } catch {
      return { ...DEFAULT_STUDIO_LAYOUT };
    }
  });
  const [explorerVisible, setExplorerVisible] = useState(initialLayout.explorerVisible);
  const [inspectorVisible, setInspectorVisible] = useState(initialLayout.inspectorVisible);
  const [explorerWidth, setExplorerWidth] = useState(initialLayout.explorerWidth);
  const [inspectorWidth, setInspectorWidth] = useState(initialLayout.inspectorWidth);
  const restoredTable = useRef(initialLayout.selectedTable);
  const restoredTableApplied = useRef(false);
  const [selectedTable, setSelectedTable] = useState<string | null>(initialLayout.selectedTable);
  const [detail, setDetail] = useState<StudioTableDetail | null>(null);
  const [detailError, setDetailError] = useState<string | null>(null);
  const [loadingDetail, setLoadingDetail] = useState(() => Boolean(initialLayout.selectedTable));
  // Crash recovery: mirror the editor tab buffers (title + SQL only) to
  // localStorage keyed by this connection, and rehydrate them on load so a
  // crash / accidental close / reload does not lose unsaved work.
  const draftKey = editorDraftStorageKey(who.realm ?? "", who.database ?? "", who.user ?? "");
  const [restoredDrafts] = useState(() => {
    try {
      const parsed = parseEditorDrafts(window.localStorage.getItem(draftKey));
      return editorDraftsWorthRestoring(parsed, DEFAULT_SQL) ? parsed : null;
    } catch {
      return null;
    }
  });
  const [tabs, setTabs] = useState<StudioTab[]>(() =>
    restoredDrafts
      ? restoredDrafts.tabs.map((draft) => newTab(queryID(), draft.title, draft.sql))
      : [newTab(queryID(), "Query 1", DEFAULT_SQL)],
  );
  const [activeTabId, setActiveTabId] = useState(() =>
    restoredDrafts ? tabs[restoredDrafts.activeIndex]?.id ?? tabs[0].id : tabs[0].id,
  );
  const [draftNoticeVisible, setDraftNoticeVisible] = useState(() => Boolean(restoredDrafts));

  // Saved queries: named, tag-grouped SQL the operator explicitly keeps.
  // Same per-connection localStorage scoping as the draft buffers; text only,
  // never sent anywhere.
  const savedKey = savedQueryStorageKey(who.realm ?? "", who.database ?? "", who.user ?? "");
  const [savedQueries, setSavedQueries] = useState<SavedQuery[]>(() => {
    try {
      return parseSavedQueries(window.localStorage.getItem(savedKey));
    } catch {
      return [];
    }
  });
  const [savedOpen, setSavedOpen] = useState(false);
  const [savedImportNotice, setSavedImportNotice] = useState<string | null>(null);
  const savedFileInput = useRef<HTMLInputElement>(null);
  const [running, setRunning] = useState(false);
  const [runningTabId, setRunningTabId] = useState<string | null>(null);
  const [cancelRequested, setCancelRequested] = useState(false);
  const [checkingSQL, setCheckingSQL] = useState(false);
  const [environment, setEnvironmentState] = useState<StudioEnvironment | null>(null);
  const [readOnlyMode, setReadOnlyMode] = useState(false);
  const [pendingRun, setPendingRun] = useState<{ analysis: StudioAnalysis; sql: string; isSelection: boolean; tabId: string; blockedReadOnly: boolean; realmScoped: boolean; params?: StudioQueryParam[] } | null>(null);
  const [pendingScript, setPendingScript] = useState<{ tabId: string; statements: string[]; flagged: { index: number; reasons: string[] }[] } | null>(null);
  const [splittingScript, setSplittingScript] = useState(false);
  const [hasSelection, setHasSelection] = useState(false);
  const [history, setHistory] = useState<StudioHistoryEntry[]>([]);
  const [historyOpen, setHistoryOpen] = useState(false);
  const [moreOpen, setMoreOpen] = useState(false);
  const [grantBuilderOpen, setGrantBuilderOpen] = useState(false);
  const [grantPrefill, setGrantPrefill] = useState<GrantBuilderState | null>(null);
  const [fullTextExplorerOpen, setFullTextExplorerOpen] = useState(false);
  const [vectorExplorerOpen, setVectorExplorerOpen] = useState(false);
  const [hybridExplorerOpen, setHybridExplorerOpen] = useState(false);
  const [geoExplorerOpen, setGeoExplorerOpen] = useState(false);
  const [securityExplorerOpen, setSecurityExplorerOpen] = useState(false);
  const [security, setSecurity] = useState<Security | null>(null);
  const [securityLoading, setSecurityLoading] = useState(false);
  const [securityError, setSecurityError] = useState<string | null>(null);
  const securityLoaded = useRef(false);
  const [activityExplorerOpen, setActivityExplorerOpen] = useState(false);
  const [activity, setActivity] = useState<Activity | null>(null);
  const [activityLoading, setActivityLoading] = useState(false);
  const [activityError, setActivityError] = useState<string | null>(null);
  const [auditExplorerOpen, setAuditExplorerOpen] = useState(false);
  const [workflowExplorerOpen, setWorkflowExplorerOpen] = useState(false);
  const [workflows, setWorkflows] = useState<StudioWorkflowOverview | null>(null);
  const [workflowsLoading, setWorkflowsLoading] = useState(false);
  const [workflowsError, setWorkflowsError] = useState<string | null>(null);
  const [migrationExplorerOpen, setMigrationExplorerOpen] = useState(false);
  const [migrations, setMigrations] = useState<StudioMigrationHistory | null>(null);
  const [migrationsLoading, setMigrationsLoading] = useState(false);
  const [migrationsError, setMigrationsError] = useState<string | null>(null);
  const [objectSearchOpen, setObjectSearchOpen] = useState(false);
  const [dataGeneratorOpen, setDataGeneratorOpen] = useState(false);
  const [importOpen, setImportOpen] = useState(false);
  const [vectorImportOpen, setVectorImportOpen] = useState(false);
  const [dmlBuilderOpen, setDmlBuilderOpen] = useState(false);
  const [schemaDesignerOpen, setSchemaDesignerOpen] = useState(false);
  const [schemaDesignerMode, setSchemaDesignerMode] = useState<SchemaDesignerMode>("table");
  const [schemaDiagramOpen, setSchemaDiagramOpen] = useState(false);
  const [schemaGraph, setSchemaGraph] = useState<StudioSchemaGraph | null>(null);
  const [schemaGraphLoading, setSchemaGraphLoading] = useState(false);
  const [schemaGraphError, setSchemaGraphError] = useState<string | null>(null);
  const [findOpen, setFindOpen] = useState(false);
  const [findQuery, setFindQuery] = useState("");
  const [replaceQuery, setReplaceQuery] = useState("");
  const [findMatchCase, setFindMatchCase] = useState(false);
  const [currentMatchIndex, setCurrentMatchIndex] = useState<number | null>(null);
  const [replaceStatus, setReplaceStatus] = useState<string | null>(null);
  const [formatNotice, setFormatNotice] = useState<string | null>(null);
  const [parseDiags, setParseDiags] = useState<StudioDiagnostic[]>([]);
  const diagSeq = useRef(0);
  const [suggestOpen, setSuggestOpen] = useState(false);
  const [cursorPos, setCursorPos] = useState(0);
  const [activeSuggestionIndex, setActiveSuggestionIndex] = useState(0);
  const [tableColumnsCache, setTableColumnsCache] = useState<TableColumnsCache>({});
  const [tableJSONPathCache, setTableJSONPathCache] = useState<TableJSONPathCache>({});
  const [tableVectorCache, setTableVectorCache] = useState<TableVectorCache>({});
  const tableFetchOrder = useRef<string[]>([]);
  const findInputRef = useRef<HTMLInputElement>(null);
  const activeQuery = useRef<string | null>(null);
  const cancelRequestedRef = useRef(false);
  const scriptAbortRef = useRef(false);
  const detailRequest = useRef<string | null>(null);
  const securityLoadingRef = useRef(false);
  const activityLoadingRef = useRef(false);
  const workflowsLoadingRef = useRef(false);
  const migrationsLoadingRef = useRef(false);
  const schemaGraphLoadingRef = useRef(false);
  const mounted = useRef(true);
  const editorHost = useRef<HTMLDivElement>(null);
  const tabCounter = useRef(restoredDrafts ? restoredDrafts.tabs.length : 1);

  const activeTab = tabs.find((t) => t.id === activeTabId) ?? tabs[0];
  const sql = activeTab.sql;
  const { result, queryError, ranSelection, script, scriptSelected, resultContext } = activeTab;
  const selectedScriptRow = script && scriptSelected !== null ? script[scriptSelected] : null;
  const otherTabRunning = running && runningTabId !== activeTabId;
  const runningTab = tabs.find((t) => t.id === runningTabId);

  const updateTab = useCallback((id: string, patch: Partial<StudioTab> | ((tab: StudioTab) => Partial<StudioTab>)) => {
    setTabs((current) => current.map((t) => (t.id === id ? { ...t, ...(typeof patch === "function" ? patch(t) : patch) } : t)));
  }, []);

  const setSQL = useCallback((value: string) => updateTab(activeTabId, { sql: value }), [activeTabId, updateTab]);

  const addTab = useCallback(() => {
    setTabs((current) => {
      if (current.length >= MAX_STUDIO_TABS) return current;
      tabCounter.current += 1;
      const tab = newTab(queryID(), `Query ${tabCounter.current}`, DEFAULT_SQL);
      setActiveTabId(tab.id);
      return [...current, tab];
    });
  }, []);

  const closeTab = useCallback((id: string) => {
    if (id === runningTabId) return;
    setTabs((current) => {
      if (current.length <= 1) return current;
      const index = current.findIndex((t) => t.id === id);
      if (index < 0) return current;
      const next = current.filter((t) => t.id !== id);
      setActiveTabId((activeId) => {
        if (activeId !== id) return activeId;
        const fallback = next[index] ?? next[index - 1] ?? next[0];
        return fallback.id;
      });
      return next;
    });
  }, [runningTabId]);

  // Debounced mirror of the tab buffers to localStorage. Best-effort: a
  // private window, blocked site data, or a quota error just means no crash
  // recovery, never a broken editor.
  useEffect(() => {
    const handle = window.setTimeout(() => {
      try {
        const activeIndex = Math.max(0, tabs.findIndex((t) => t.id === activeTabId));
        window.localStorage.setItem(
          draftKey,
          serializeEditorDrafts(tabs.map((t) => ({ title: t.title, sql: t.sql })), activeIndex),
        );
      } catch {
        /* best effort */
      }
    }, 500);
    return () => window.clearTimeout(handle);
  }, [tabs, activeTabId, draftKey]);

  useEffect(() => {
    const handle = window.setTimeout(() => {
      try {
        window.localStorage.setItem(
          layoutKey,
          serializeStudioLayout({
            explorerVisible,
            inspectorVisible,
            explorerWidth,
            inspectorWidth,
            selectedTable,
          }),
        );
      } catch {
        /* best effort — a private/blocked window just does not persist layout */
      }
    }, 200);
    return () => window.clearTimeout(handle);
  }, [layoutKey, explorerVisible, inspectorVisible, explorerWidth, inspectorWidth, selectedTable]);

  const resetLayout = useCallback(() => {
    const next = resetStudioLayout(selectedTable);
    setExplorerVisible(next.explorerVisible);
    setInspectorVisible(next.inspectorVisible);
    setExplorerWidth(next.explorerWidth);
    setInspectorWidth(next.inspectorWidth);
  }, [selectedTable]);

  const discardRestoredDrafts = useCallback(() => {
    try {
      window.localStorage.removeItem(draftKey);
    } catch {
      /* best effort */
    }
    const fresh = newTab(queryID(), "Query 1", DEFAULT_SQL);
    tabCounter.current = 1;
    setTabs([fresh]);
    setActiveTabId(fresh.id);
    setDraftNoticeVisible(false);
  }, [draftKey]);

  useEffect(() => {
    try {
      window.localStorage.setItem(savedKey, serializeSavedQueries(savedQueries));
    } catch {
      /* best effort — no saved-query persistence in a private/blocked window */
    }
  }, [savedQueries, savedKey]);

  const saveCurrentQuery = useCallback((name: string, tags: string) => {
    setSavedQueries((current) =>
      upsertSavedQuery(current, {
        id: queryID(),
        name,
        sql,
        tags: tags.split(","),
        updatedAt: Date.now(),
      }),
    );
  }, [sql]);

  const updateSavedQuerySQL = useCallback((id: string) => {
    setSavedQueries((current) => {
      const existing = current.find((q) => q.id === id);
      if (!existing) return current;
      return upsertSavedQuery(current, { ...existing, sql, updatedAt: Date.now() });
    });
  }, [sql]);

  const renameSavedQuery = useCallback((id: string, name: string) => {
    setSavedQueries((current) => {
      const existing = current.find((q) => q.id === id);
      if (!existing) return current;
      return upsertSavedQuery(current, { ...existing, name, updatedAt: Date.now() });
    });
  }, []);

  const deleteSavedQuery = useCallback((id: string) => {
    setSavedQueries((current) => removeSavedQuery(current, id));
  }, []);

  const loadSavedQuery = useCallback((sql: string) => {
    setSQL(sql);
    setSavedOpen(false);
  }, [setSQL]);

  const exportSavedQueriesToFile = useCallback(() => {
    try {
      downloadSavedQueries(savedQueries);
    } catch {
      setSavedImportNotice("Could not export the saved-query file in this browser.");
    }
  }, [savedQueries]);

  const requestSavedQueryImport = useCallback(() => {
    setSavedImportNotice(null);
    savedFileInput.current?.click();
  }, []);

  const importSavedQueriesFromFile = useCallback((file: File) => {
    if (file.size > MAX_SAVED_QUERY_IMPORT_BYTES) {
      setSavedImportNotice("That file is too large to be a saved-query set.");
      return;
    }
    const reader = new FileReader();
    reader.onerror = () => setSavedImportNotice("Could not read that file.");
    reader.onload = () => {
      const text = typeof reader.result === "string" ? reader.result : "";
      const incoming = parseSavedQueriesExport(text);
      if (incoming.length === 0) {
        setSavedImportNotice("No importable saved queries were found in that file.");
        return;
      }
      const { list, added, updated, unchanged } = mergeSavedQueries(savedQueries, incoming);
      setSavedQueries(list);
      const parts = [`${added} added`, `${updated} updated`];
      if (unchanged) parts.push(`${unchanged} unchanged`);
      setSavedImportNotice(`Imported saved queries — ${parts.join(", ")}.`);
    };
    reader.readAsText(file);
  }, [savedQueries]);

  const handleFailure = useCallback((error: unknown, setError: (message: string) => void) => {
    if (error instanceof ApiError && error.status === 401) {
      onUnauthorized();
      return;
    }
    setError(errorMessage(error));
  }, [onUnauthorized]);

  const loadBootstrap = useCallback(() => {
    setLoadingBootstrap(true);
    api.studioBootstrap()
      .then((data) => {
        if (!mounted.current) return;
        setBootstrap(data);
        setBootstrapError(null);
        setReadMode(data.read_consistency ?? "strong");
        setStalenessSec(Math.round((data.max_staleness_ms ?? 0) / 1000));
      })
      .catch((error: unknown) => {
        if (mounted.current) handleFailure(error, setBootstrapError);
      })
      .finally(() => {
        if (mounted.current) setLoadingBootstrap(false);
      });
  }, [handleFailure]);

  // Read consistency is a live session-control change on the current
  // connection — no reconnect. STRONG/STALE apply immediately on selection;
  // BOUNDED also re-applies when its staleness bound is edited.
  const applyReadConsistency = useCallback((mode: StudioReadConsistency, sec: number) => {
    setApplyingReadMode(true);
    setReadConsistencyError(null);
    api.studioSetReadConsistency({
      mode,
      max_staleness_ms: mode === "bounded" ? Math.max(0, Math.round(sec)) * 1000 : 0,
    })
      .then((state) => {
        if (!mounted.current) return;
        setReadMode(state.mode);
        setStalenessSec(Math.round(state.max_staleness_ms / 1000));
      })
      .catch((error: unknown) => {
        if (mounted.current) handleFailure(error, setReadConsistencyError);
      })
      .finally(() => {
        if (mounted.current) setApplyingReadMode(false);
      });
  }, [handleFailure]);

  useEffect(() => {
    mounted.current = true;
    loadBootstrap();
    return () => {
      mounted.current = false;
      const id = activeQuery.current;
      if (id) void api.studioCancel(id).catch(() => undefined);
    };
  }, [loadBootstrap]);

  // RUI's editor intentionally has a small prop surface. Give its internal
  // textarea a stable accessible name after mount without replacing the
  // design-system primitive.
  useEffect(() => {
    editorHost.current?.querySelector("textarea")?.setAttribute("aria-label", "SQL editor");
  });

  // Tracks whether the editor currently has a non-empty text selection, so
  // the Run action can offer "Execute selection" instead of always running
  // the whole editor. selectionStart/selectionEnd survive losing focus (e.g.
  // clicking the Run button itself), so the actual run reads them directly
  // from the textarea rather than from this reactive mirror.
  useEffect(() => {
    const update = () => {
      const textarea = editorHost.current?.querySelector("textarea");
      if (!textarea) {
        setHasSelection(false);
        return;
      }
      const selected = textarea.value.slice(textarea.selectionStart, textarea.selectionEnd).trim();
      setHasSelection(selected.length > 0);
      setCursorPos(textarea.selectionEnd);
    };
    document.addEventListener("selectionchange", update);
    update();
    return () => document.removeEventListener("selectionchange", update);
  }, []);

  // Switching tabs swaps the single mounted editor's bound text without a
  // native selection event; any stale selection from the previous tab no
  // longer means anything, so treat every tab switch as starting unselected,
  // and any open suggestion list belonged to the previous tab's buffer.
  useEffect(() => {
    setHasSelection(false);
    setSuggestOpen(false);
  }, [activeTabId]);

  // Catalog-aware IntelliSense (see resultTools.ts's "Catalog-aware
  // IntelliSense" section for the full boundary/rationale). Table names are
  // free (already loaded in bootstrap); column names for a FROM/JOIN target
  // are fetched lazily and only while the suggestion panel is actually
  // open, through the same api.studioTable() every explorer already uses —
  // no eager/speculative catalog reads.
  const catalogTableNames = useMemo(() => tableNames(bootstrap), [bootstrap]);
  const referencedTables = useMemo(() => extractReferencedTables(sql), [sql]);
  // Positional placeholders ($1..$N) referenced by the active buffer. When
  // non-empty the editor shows a bind panel; the values feed the Run request.
  const queryParamNumbers = useMemo(() => extractQueryParams(sql), [sql]);

  const setParamValue = useCallback(
    (n: number, patch: Partial<{ text: string; isNull: boolean }>) => {
      updateTab(activeTabId, (tab) => {
        const current = tab.paramValues[n] ?? { text: "", isNull: false };
        return { paramValues: { ...tab.paramValues, [n]: { ...current, ...patch } } };
      });
    },
    [activeTabId, updateTab],
  );

  // buildQueryParams turns the active tab's bind slots into the positional
  // wire array for `target`. NextSQL binds by position, so the array is dense
  // up to the highest referenced number; an untouched slot sends "" (coerced
  // server-side), an explicit NULL sends null. Returns undefined when the
  // statement has no placeholders.
  const buildQueryParams = useCallback(
    (target: string): StudioQueryParam[] | undefined => {
      const numbers = extractQueryParams(target);
      if (numbers.length === 0) return undefined;
      const count = numbers[numbers.length - 1];
      const slots = tabs.find((t) => t.id === activeTabId)?.paramValues ?? {};
      return Array.from({ length: count }, (_, i) => {
        const slot = slots[i + 1];
        return slot?.isNull ? { value: null } : { value: slot?.text ?? "" };
      });
    },
    [tabs, activeTabId],
  );
  const suggestWordRange = useMemo(() => currentWordRange(sql, cursorPos), [sql, cursorPos]);
  const suggestPrefix = sql.slice(suggestWordRange.start, cursorPos);
  // When the caret sits inside a dotted path (`metadata.tags`), completion
  // switches to the known indexed JSON paths on the referenced tables — the
  // only JSON structure NextSQL exposes metadata for — instead of the plain
  // table/column list.
  const jsonPathContext = useMemo(() => currentJSONPathRange(sql, cursorPos), [sql, cursorPos]);
  const nearestContext = useMemo(() => currentNearestContext(sql, cursorPos), [sql, cursorPos]);
  const suggestions = useMemo(() => {
    if (!suggestOpen) return [];
    if (jsonPathContext) {
      return rankJSONPathSuggestions(jsonPathContext.typed, referencedTables, tableJSONPathCache);
    }
    if (nearestContext?.slot === "column") {
      return rankNearestColumnSuggestions(nearestContext.typed, referencedTables, tableVectorCache);
    }
    if (nearestContext?.slot === "metric") {
      return rankNearestMetricSuggestions(nearestContext.typed, nearestContext.column, referencedTables, tableVectorCache);
    }
    return rankSQLSuggestions(suggestPrefix, catalogTableNames, referencedTables, tableColumnsCache);
  }, [suggestOpen, jsonPathContext, nearestContext, suggestPrefix, catalogTableNames, referencedTables, tableColumnsCache, tableJSONPathCache, tableVectorCache]);

  useEffect(() => {
    setActiveSuggestionIndex(0);
  }, [suggestOpen, suggestPrefix, jsonPathContext?.typed, nearestContext?.slot, nearestContext?.typed]);

  // RUI's CodeEditor has no completion/overlay primitive and no way to pass
  // through arbitrary aria-* props, so the ARIA 1.2 combobox-with-listbox-
  // popup role is applied imperatively to its underlying textarea, the same
  // technique the mount effect above already uses for aria-label. Focus
  // never leaves the textarea while suggesting — aria-activedescendant
  // names the highlighted option instead of moving real DOM focus into the
  // popover, so typing keeps filtering the list without interruption.
  useEffect(() => {
    const textarea = editorHost.current?.querySelector("textarea");
    if (!textarea) return;
    textarea.setAttribute("role", "combobox");
    textarea.setAttribute("aria-autocomplete", "list");
    textarea.setAttribute("aria-expanded", suggestOpen ? "true" : "false");
    textarea.setAttribute("aria-controls", "studio-suggest-listbox");
    if (suggestOpen && suggestions[activeSuggestionIndex]) {
      textarea.setAttribute("aria-activedescendant", `studio-suggest-option-${activeSuggestionIndex}`);
    } else {
      textarea.removeAttribute("aria-activedescendant");
    }
  }, [suggestOpen, suggestions, activeSuggestionIndex]);

  useEffect(() => {
    if (!suggestOpen) return;
    let cache = tableColumnsCache;
    let order = tableFetchOrder.current;
    const toFetch: string[] = [];
    for (const table of referencedTables) {
      if (table in cache) continue;
      const next = withCachedTableLoading(cache, order, table);
      cache = next.cache;
      order = next.order;
      toFetch.push(table);
    }
    tableFetchOrder.current = order;
    if (cache !== tableColumnsCache) setTableColumnsCache(cache);
    for (const table of toFetch) {
      api.studioTable(table)
        .then((data) => {
          if (!mounted.current) return;
          setTableColumnsCache((prev) => (table in prev ? { ...prev, [table]: namesFromResult(data.columns, "column_name") } : prev));
          setTableJSONPathCache((prev) => ({ ...prev, [table]: jsonPathIndexPaths(data.indexes) }));
          setTableVectorCache((prev) => ({ ...prev, [table]: vectorColumnsFromResult(data.columns) }));
        })
        .catch(() => {
          if (!mounted.current) return;
          setTableColumnsCache((prev) => (table in prev ? { ...prev, [table]: "error" } : prev));
        });
    }
  }, [suggestOpen, referencedTables, tableColumnsCache]);

  // Deterministic misspelled-table-name suggestions (see resultTools.ts's
  // "Deterministic misspelled table-name suggestions" section for the full
  // boundary/rationale). Pure and synchronous — no fetch, reuses the
  // already-loaded catalogTableNames, and is disabled outright while the
  // bootstrap's own table list is truncated.
  const tableNameFixes = useMemo(
    () => suggestTableNameFixes(sql, catalogTableNames, Boolean(bootstrap?.tables_truncated)),
    [sql, catalogTableNames, bootstrap?.tables_truncated],
  );

  const applyFix = useCallback((fix: TableNameFix) => {
    setSQL(applyTableNameFix(sql, fix));
  }, [sql, setSQL]);

  // Live parse diagnostics: a debounced round trip to the server's own
  // grammar (POST /studio/query/diagnostics — no connection, no query slot)
  // reports where each statement in the buffer fails to parse. RUI's
  // CodeEditor has no text-overlay primitive, so this renders as a strip
  // under the editor with a jump-to-error control rather than an inline
  // squiggle. Advisory only — the server still parses/binds/authorizes on
  // Run — and grammar-only: an unknown table or column is reported by
  // nextsqld when the statement actually executes, not here. A network or
  // auth failure clears the strip; it must never block a run.
  useEffect(() => {
    const buffer = sql;
    if (!buffer.trim()) {
      setParseDiags([]);
      return;
    }
    const seq = ++diagSeq.current;
    const handle = window.setTimeout(() => {
      api
        .studioDiagnostics(buffer)
        .then((report) => {
          if (!mounted.current || diagSeq.current !== seq) return;
          setParseDiags(report.diagnostics.slice(0, 20));
        })
        .catch((error: unknown) => {
          if (!mounted.current || diagSeq.current !== seq) return;
          if (error instanceof ApiError && error.status === 401) {
            onUnauthorized();
            return;
          }
          setParseDiags([]);
        });
    }, 400);
    return () => window.clearTimeout(handle);
  }, [sql, onUnauthorized]);

  // jumpToDiagnostic moves the real textarea caret to a diagnostic's
  // position (offset is a UTF-16 code-unit index, matching JS string
  // indexing) and selects the token there so the operator sees where the
  // parser stopped.
  const jumpToDiagnostic = useCallback((diag: StudioDiagnostic) => {
    const textarea = editorHost.current?.querySelector("textarea");
    if (!textarea) return;
    const start = Math.max(0, Math.min(diag.offset, textarea.value.length));
    const end = Math.min(textarea.value.length, start + 1);
    requestAnimationFrame(() => {
      textarea.focus();
      textarea.setSelectionRange(start, end);
      setCursorPos(start);
    });
  }, []);

  // Editable data grid & transactional changes
  const editableTable = useMemo(() => {
    if (!result || !sql) return null;
    return detectEditableTable(sql, catalogTableNames);
  }, [result, sql, catalogTableNames]);

  const [editableDetail, setEditableDetail] = useState<StudioTableDetail | null>(null);

  useEffect(() => {
    if (!editableTable) {
      setEditableDetail(null);
      return;
    }
    if (selectedTable === editableTable && detail) {
      setEditableDetail(detail);
      return;
    }
    let active = true;
    api.studioTable(editableTable)
      .then((data) => {
        if (active) setEditableDetail(data);
      })
      .catch(() => {
        if (active) setEditableDetail(null);
      });
    return () => {
      active = false;
    };
  }, [editableTable, selectedTable, detail]);

  const editablePKs = useMemo(() => {
    if (!editableTable) return [];
    if (editableDetail) return tablePKColumns(editableDetail);
    if (bootstrap?.tables) {
      const nameIdx = resultColumn(bootstrap.tables, "name");
      const pkIdx = resultColumn(bootstrap.tables, "pk");
      if (nameIdx >= 0 && pkIdx >= 0) {
        const row = bootstrap.tables.rows.find((r) => (r[nameIdx] ?? "").toLowerCase() === editableTable.toLowerCase());
        const pk = row ? row[pkIdx] : null;
        if (pk) return [pk];
      }
    }
    return [];
  }, [editableTable, editableDetail, bootstrap?.tables]);

  const editableColTypes = useMemo(() => {
    if (editableDetail) return tableColumnTypesRecord(editableDetail);
    return {};
  }, [editableDetail]);

  const openSuggest = useCallback(() => {
    setSuggestOpen(true);
    requestAnimationFrame(() => editorHost.current?.querySelector("textarea")?.focus());
  }, []);

  const acceptSuggestion = useCallback((suggestion: SQLSuggestion) => {
    const textarea = editorHost.current?.querySelector("textarea");
    const range =
      suggestion.kind === "json-path"
        ? currentJSONPathRange(sql, cursorPos) ?? currentWordRange(sql, cursorPos)
        : currentWordRange(sql, cursorPos);
    const nextValue = sql.slice(0, range.start) + suggestion.insertText + sql.slice(range.end);
    setSQL(nextValue);
    setSuggestOpen(false);
    const cursor = range.start + suggestion.insertText.length;
    if (textarea) {
      requestAnimationFrame(() => {
        textarea.focus();
        textarea.setSelectionRange(cursor, cursor);
        setCursorPos(cursor);
      });
    }
  }, [sql, cursorPos]);

  // Both the GRANT/REVOKE builder's grantee/role suggestions and the Users &
  // roles explorer read the same admin-only system.users/system.roles/
  // system.grants views Operations mode's Security view already exposes
  // (api.security()) — reused as-is rather than adding a dedicated Studio
  // endpoint. Those views return zero rows (not an error) for a non-admin
  // caller, so a degraded fetch here just means empty sections/no
  // suggestions; every field stays usable either way. Loads once per mount,
  // on first open of either surface, not on every open.
  const loadSecurity = useCallback(() => {
    if (securityLoaded.current || securityLoadingRef.current) return;
    securityLoaded.current = true;
    securityLoadingRef.current = true;
    setSecurityLoading(true);
    setSecurityError(null);
    api.security()
      .then((data) => {
        if (!mounted.current) return;
        setSecurity(data);
        setSecurityError(null);
      })
      .catch((error: unknown) => {
        if (!mounted.current) return;
        if (error instanceof ApiError && error.status === 401) {
          onUnauthorized();
          return;
        }
        securityLoaded.current = false;
        setSecurityError(errorMessage(error));
      })
      .finally(() => {
        securityLoadingRef.current = false;
        if (mounted.current) setSecurityLoading(false);
      });
  }, [onUnauthorized]);

  const reloadSecurity = useCallback(() => {
    if (securityLoadingRef.current) return;
    securityLoaded.current = false;
    loadSecurity();
  }, [loadSecurity]);

  const openGrantBuilder = useCallback(() => {
    setGrantPrefill(null);
    setGrantBuilderOpen(true);
    loadSecurity();
  }, [loadSecurity]);

  const openSecurityExplorer = useCallback(() => {
    setSecurityExplorerOpen(true);
    loadSecurity();
  }, [loadSecurity]);

  const openAuditExplorer = useCallback(() => {
    setAuditExplorerOpen(true);
    reloadSecurity();
  }, [reloadSecurity]);

  const revokeGrant = useCallback((row: { grantee: string; privilege: string; scope: string; object: string }) => {
    setGrantPrefill(grantStateFromRow(row.grantee, row.privilege, row.scope, row.object));
    setSecurityExplorerOpen(false);
    setGrantBuilderOpen(true);
  }, []);

  // Unlike loadSecurity, this refetches on every open and every explicit
  // Refresh click — sessions/transactions/locks are live state, not the
  // rarely-changing users/roles/grants catalog, so a load-once cache would
  // show a stale snapshot the whole time the modal stays open.
  const loadActivity = useCallback(() => {
    if (activityLoadingRef.current) return;
    activityLoadingRef.current = true;
    setActivityLoading(true);
    setActivityError(null);
    api.activity()
      .then((data) => {
        if (!mounted.current) return;
        setActivity(data);
        setActivityError(null);
      })
      .catch((error: unknown) => {
        if (!mounted.current) return;
        if (error instanceof ApiError && error.status === 401) {
          onUnauthorized();
          return;
        }
        setActivityError(errorMessage(error));
      })
      .finally(() => {
        activityLoadingRef.current = false;
        if (mounted.current) setActivityLoading(false);
      });
  }, [onUnauthorized]);

  const openActivityExplorer = useCallback(() => {
    setActivityExplorerOpen(true);
    loadActivity();
  }, [loadActivity]);

  // Tasks are live scheduler state, so — like loadActivity — this refetches
  // on every open and every explicit Refresh rather than caching.
  const loadWorkflows = useCallback(() => {
    if (workflowsLoadingRef.current) return;
    workflowsLoadingRef.current = true;
    setWorkflowsLoading(true);
    setWorkflowsError(null);
    api.studioWorkflows()
      .then((data) => {
        if (!mounted.current) return;
        setWorkflows(data);
        setWorkflowsError(null);
      })
      .catch((error: unknown) => {
        if (!mounted.current) return;
        if (error instanceof ApiError && error.status === 401) {
          onUnauthorized();
          return;
        }
        setWorkflowsError(errorMessage(error));
      })
      .finally(() => {
        workflowsLoadingRef.current = false;
        if (mounted.current) setWorkflowsLoading(false);
      });
  }, [onUnauthorized]);

  const openWorkflowExplorer = useCallback(() => {
    setWorkflowExplorerOpen(true);
    loadWorkflows();
  }, [loadWorkflows]);

  // Migration history is live server state (a migration can be applied from
  // the CLI at any time), so — like loadWorkflows — this refetches on every
  // open and every explicit Refresh rather than caching.
  const loadMigrations = useCallback(() => {
    if (migrationsLoadingRef.current) return;
    migrationsLoadingRef.current = true;
    setMigrationsLoading(true);
    setMigrationsError(null);
    api.studioMigrations()
      .then((data) => {
        if (!mounted.current) return;
        setMigrations(data);
        setMigrationsError(null);
      })
      .catch((error: unknown) => {
        if (!mounted.current) return;
        if (error instanceof ApiError && error.status === 401) {
          onUnauthorized();
          return;
        }
        setMigrationsError(errorMessage(error));
      })
      .finally(() => {
        migrationsLoadingRef.current = false;
        if (mounted.current) setMigrationsLoading(false);
      });
  }, [onUnauthorized]);

  const openMigrationExplorer = useCallback(() => {
    setMigrationExplorerOpen(true);
    loadMigrations();
  }, [loadMigrations]);

  const openObjectSearch = useCallback(() => {
    setObjectSearchOpen(true);
    if (!workflows) loadWorkflows();
  }, [workflows, loadWorkflows]);

  const loadSchemaGraph = useCallback(() => {
    if (schemaGraphLoadingRef.current) return;
    schemaGraphLoadingRef.current = true;
    setSchemaGraphLoading(true);
    setSchemaGraphError(null);
    api.studioSchemaGraph()
      .then((data) => {
        if (!mounted.current) return;
        setSchemaGraph(data);
        setSchemaGraphError(null);
      })
      .catch((error: unknown) => {
        if (!mounted.current) return;
        if (error instanceof ApiError && error.status === 401) {
          onUnauthorized();
          return;
        }
        setSchemaGraphError(errorMessage(error));
      })
      .finally(() => {
        schemaGraphLoadingRef.current = false;
        if (mounted.current) setSchemaGraphLoading(false);
      });
  }, [onUnauthorized]);

  const openSchemaDiagram = useCallback(() => {
    setSchemaDiagramOpen(true);
    loadSchemaGraph();
  }, [loadSchemaGraph]);

  const principalUsers = useMemo(() => namesFromResult(security?.users ?? null, "name"), [security]);
  const principalRoles = useMemo(() => namesFromResult(security?.roles ?? null, "role"), [security]);

  const allTables = useMemo(() => tableNames(bootstrap), [bootstrap]);
  const fullTextSupported = useMemo(() => capabilityAvailable(bootstrap, "fulltext"), [bootstrap]);
  const vectorSupported = useMemo(() => capabilityAvailable(bootstrap, "vector"), [bootstrap]);
  const hybridSupported = fullTextSupported && vectorSupported;
  const geoSupported = useMemo(() => capabilityAvailable(bootstrap, "geo"), [bootstrap]);

  const moreGroups = useMemo(() => {
    const run = (action: () => void) => {
      setMoreOpen(false);
      action();
    };
    const groups: { label: string; items: { label: string; icon: IconName; disabled?: boolean; title?: string; run: () => void }[] }[] = [
      {
        label: "Search",
        items: [
          { label: "Full-text…", icon: "search", disabled: !fullTextSupported, title: fullTextSupported ? "Build a native SEARCH query" : "This server does not report fulltext support", run: () => run(() => setFullTextExplorerOpen(true)) },
          { label: "Vector…", icon: "layers", disabled: !vectorSupported, title: vectorSupported ? "Build a native NEAREST query" : "This server does not report vector support", run: () => run(() => setVectorExplorerOpen(true)) },
          { label: "Hybrid…", icon: "network", disabled: !hybridSupported, title: hybridSupported ? "Build a native structured filter + SEARCH + NEAREST query" : "This server does not report both fulltext and vector support", run: () => run(() => setHybridExplorerOpen(true)) },
          { label: "Geo…", icon: "map-pin", disabled: !geoSupported, title: geoSupported ? "Build a native DWITHIN/WITHIN query over a POINT column" : "This server does not report geo support", run: () => run(() => setGeoExplorerOpen(true)) },
        ],
      },
      {
        label: "Security",
        items: [
          { label: "Grant / Revoke…", icon: "key", run: () => run(openGrantBuilder) },
          { label: "Users & roles…", icon: "users", run: () => run(openSecurityExplorer) },
          { label: "Audit…", icon: "shield", run: () => run(openAuditExplorer) },
        ],
      },
      {
        label: "Operations",
        items: [
          { label: "Transactions & locks…", icon: "lock", run: () => run(openActivityExplorer) },
          { label: "Workflows & CDC…", icon: "activity", run: () => run(openWorkflowExplorer) },
          { label: "Migrations…", icon: "clock", run: () => run(openMigrationExplorer) },
        ],
      },
      {
        label: "Schema",
        items: [
          { label: "Schema diagram…", icon: "network", run: () => run(openSchemaDiagram) },
          { label: "Design schema…", icon: "table", run: () => run(() => { setSchemaDesignerMode("table"); setSchemaDesignerOpen(true); }) },
        ],
      },
      {
        label: "Data",
        items: [
          { label: "Generate data…", icon: "plus", run: () => run(() => setDataGeneratorOpen(true)) },
          { label: "Import data…", icon: "download", run: () => run(() => setImportOpen(true)) },
          { label: "Import vector dataset…", icon: "download", run: () => run(() => setVectorImportOpen(true)) },
          { label: "Parameterized DML…", icon: "file", run: () => run(() => setDmlBuilderOpen(true)) },
        ],
      },
    ];
    return groups;
  }, [
    fullTextSupported, vectorSupported, hybridSupported, geoSupported,
    openGrantBuilder, openSecurityExplorer, openAuditExplorer,
    openActivityExplorer, openWorkflowExplorer, openMigrationExplorer, openSchemaDiagram,
  ]);
  const jsonExplorerContext = useMemo(() => {
    if (!selectedTable || !detail) return undefined;
    return {
      table: selectedTable,
      columns: JSONColumnNames(detail),
      indexes: detail.indexes,
      onInsertQuery: setSQL,
    };
  }, [detail, selectedTable, setSQL]);
  const visibleTables = useMemo(() => {
    const needle = filter.trim().toLocaleLowerCase();
    if (!needle) return allTables;
    return allTables.filter((name) => name.toLocaleLowerCase().includes(needle));
  }, [allTables, filter]);

  const selectTable = useCallback((name: string) => {
    detailRequest.current = name;
    setSelectedTable(name);
    onSelectRouteTable?.(name);
    setDetail(null);
    setDetailError(null);
    setLoadingDetail(true);
    api.studioTable(name)
      .then((data) => {
        if (mounted.current && detailRequest.current === name) setDetail(data);
      })
      .catch((error: unknown) => {
        if (mounted.current && detailRequest.current === name) handleFailure(error, setDetailError);
      })
      .finally(() => {
        if (mounted.current && detailRequest.current === name) setLoadingDetail(false);
      });
  }, [handleFailure, onSelectRouteTable]);

  useEffect(() => {
    if (restoredTableApplied.current) return;
    const name = routeTable && allTables.includes(routeTable) ? routeTable : restoredTable.current;
    if (!bootstrap) {
      if (bootstrapError) {
        restoredTableApplied.current = true;
        setLoadingDetail(false);
      }
      return;
    }
    restoredTableApplied.current = true;
    if (!name) {
      setLoadingDetail(false);
      return;
    }
    if (allTables.includes(name)) {
      selectTable(name);
      return;
    }
    setSelectedTable(null);
    setLoadingDetail(false);
  }, [bootstrap, bootstrapError, allTables, selectTable, routeTable]);

  useEffect(() => {
    if (routeTable && allTables.includes(routeTable) && selectedTable !== routeTable) {
      selectTable(routeTable);
    }
  }, [routeTable, allTables, selectedTable, selectTable]);

  const loadFullTextTable = useCallback(async (name: string): Promise<StudioTableDetail> => {
    try {
      return await api.studioTable(name);
    } catch (error) {
      if (error instanceof ApiError && error.status === 401) onUnauthorized();
      throw error;
    }
  }, [onUnauthorized]);

  const loadWorkflowOverview = useCallback(async (): Promise<StudioWorkflowOverview> => {
    try {
      return await api.studioWorkflows();
    } catch (error) {
      if (error instanceof ApiError && error.status === 401) onUnauthorized();
      throw error;
    }
  }, [onUnauthorized]);

  const executeQuery = useCallback(async (
    tabId: string,
    target: string,
    isSelection: boolean,
    nextResultContext: RankResultContext | null = null,
    params?: StudioQueryParam[],
  ) => {
    if (running) return;
    const id = queryID();
    activeQuery.current = id;
    cancelRequestedRef.current = false;
    setRunning(true);
    setRunningTabId(tabId);
    setCancelRequested(false);
    updateTab(tabId, {
      queryError: null,
      result: null,
      ranSelection: isSelection,
      script: null,
      scriptSelected: null,
      resultContext: nextResultContext,
    });
    let finalRowCount = 0;
    let finalElapsedMs = 0;
    try {
      await api.studioQueryStream(
        { query_id: id, sql: target, params },
        {
          onMeta: (meta) => {
            if (!mounted.current) return;
            updateTab(tabId, {
              result: { columns: meta.columns, column_types: meta.column_types, rows: [], truncated: false, elapsed_ms: 0 },
            });
          },
          onRows: (rows) => {
            if (!mounted.current) return;
            updateTab(tabId, (tab) => ({ result: tab.result ? { ...tab.result, rows: [...tab.result.rows, ...rows] } : tab.result }));
          },
          onComplete: (complete) => {
            finalRowCount = complete.row_count;
            finalElapsedMs = complete.elapsed_ms;
            if (!mounted.current) return;
            updateTab(tabId, (tab) => ({
              result: tab.result ? {
                ...tab.result,
                affected: complete.affected,
                truncated: complete.truncated,
                elapsed_ms: complete.elapsed_ms,
              } : tab.result,
            }));
          },
        },
      );
      if (mounted.current) {
        setHistory((current) => pushHistory(current, {
          id: queryID(), sql: target, ranAt: Date.now(), isSelection,
          outcome: "success", rowCount: finalRowCount, elapsedMs: finalElapsedMs,
        }));
      }
    } catch (error: unknown) {
      if (!mounted.current) return;
      if (error instanceof ApiError && error.status === 401) {
        onUnauthorized();
      } else if (error instanceof ApiError && error.status === 408 && cancelRequestedRef.current) {
        updateTab(tabId, { queryError: "Query canceled." });
        setHistory((current) => pushHistory(current, {
          id: queryID(), sql: target, ranAt: Date.now(), isSelection, outcome: "canceled",
        }));
      } else {
        updateTab(tabId, { queryError: errorMessage(error) });
        setHistory((current) => pushHistory(current, {
          id: queryID(), sql: target, ranAt: Date.now(), isSelection,
          outcome: "error", error: errorMessage(error),
        }));
      }
    } finally {
      if (activeQuery.current === id) activeQuery.current = null;
      cancelRequestedRef.current = false;
      if (mounted.current) {
        setRunning(false);
        setRunningTabId(null);
        setCancelRequested(false);
      }
    }
  }, [onUnauthorized, running, updateTab]);

  const runFullTextSearch = useCallback((target: string, context: FullTextResultContext) => {
    if (running) return;
    const tabId = activeTabId;
    updateTab(tabId, { sql: target });
    void executeQuery(tabId, target, false, context);
  }, [activeTabId, executeQuery, running, updateTab]);

  const runVectorSearch = useCallback((target: string) => {
    if (running) return;
    const tabId = activeTabId;
    updateTab(tabId, { sql: target });
    void executeQuery(tabId, target, false, null);
  }, [activeTabId, executeQuery, running, updateTab]);

  const runHybridSearch = useCallback((target: string, context: HybridResultContext) => {
    if (running) return;
    const tabId = activeTabId;
    updateTab(tabId, { sql: target });
    void executeQuery(tabId, target, false, context);
  }, [activeTabId, executeQuery, running, updateTab]);

  const runGeoSearch = useCallback((target: string) => {
    if (running) return;
    const tabId = activeTabId;
    updateTab(tabId, { sql: target });
    void executeQuery(tabId, target, false, null);
  }, [activeTabId, executeQuery, running, updateTab]);

  const runHybridExplain = useCallback((target: string) => {
    if (running) return;
    const tabId = activeTabId;
    updateTab(tabId, { sql: target });
    void executeQuery(tabId, target, false, null);
  }, [activeTabId, executeQuery, running, updateTab]);

  const commitStagedChanges = useCallback(async (_fullSQL: string, statements: string[]) => {
    if (running || !statements || statements.length === 0) return;
    const tabId = activeTabId;
    const currentSQL = sql;
    const id = queryID();
    setRunning(true);
    setRunningTabId(tabId);
    try {
      for (const stmt of statements) {
        await api.studioQuery({ query_id: id, sql: stmt });
      }
    } catch (err) {
      try {
        await api.studioQuery({ query_id: queryID(), sql: "ROLLBACK;" });
      } catch {
        // ignore rollback error
      }
      throw err;
    } finally {
      setRunning(false);
      setRunningTabId(null);
    }
    void executeQuery(tabId, currentSQL, false);
  }, [activeTabId, executeQuery, running, sql]);

  const loadFromHistory = useCallback((entry: StudioHistoryEntry) => {
    setSQL(entry.sql);
    setHistoryOpen(false);
  }, []);

  const clearHistory = useCallback(() => setHistory([]), []);

  // selectedSQL reads the live textarea selection directly (it survives
  // losing focus, e.g. to the Run button itself) rather than trusting
  // hasSelection's async selectionchange mirror, so the text actually run
  // always matches what was highlighted at the moment Run was invoked.
  const selectedSQL = useCallback((): string | null => {
    const textarea = editorHost.current?.querySelector("textarea");
    if (!textarea || textarea.selectionStart === textarea.selectionEnd) return null;
    const selected = sql.slice(textarea.selectionStart, textarea.selectionEnd).trim();
    return selected || null;
  }, [sql]);

  // requestRun asks the server to classify the statement (same parser the
  // executor itself uses) before running it. A statement flagged destructive
  // — UPDATE/DELETE with no WHERE, or dropping a table/column/index/etc. —
  // shows a confirmation instead of running immediately. Analysis failure
  // (a syntax error the real executor will report anyway, or a transient
  // network problem) never blocks the run; it only means no warning shows.
  // A non-empty editor selection runs only the selected text.
  const requestRun = useCallback(async () => {
    if (running || checkingSQL) return;
    const tabId = activeTabId;
    const selection = selectedSQL();
    const target = selection ?? sql;
    if (!target.trim()) return;
    const params = buildQueryParams(target);
    setCheckingSQL(true);
    try {
      const analysis = await api.studioAnalyze(target);
      if (!mounted.current) return;
      const blockedReadOnly = readOnlyMode && analysis.write;
      const realmScoped = Boolean(analysis.realm_scoped);
      if (analysis.destructive || blockedReadOnly || realmScoped) {
        setPendingRun({ analysis, sql: target, isSelection: Boolean(selection), tabId, blockedReadOnly, realmScoped, params });
        return;
      }
    } catch (error: unknown) {
      if (error instanceof ApiError && error.status === 401) {
        onUnauthorized();
        return;
      }
      // Any other analyze failure falls through to running the statement
      // unconfirmed, matching pre-existing behavior.
    } finally {
      if (mounted.current) setCheckingSQL(false);
    }
    void executeQuery(tabId, target, Boolean(selection), null, params);
  }, [activeTabId, buildQueryParams, checkingSQL, executeQuery, onUnauthorized, readOnlyMode, running, selectedSQL, sql]);

  const confirmPendingRun = useCallback(() => {
    if (!pendingRun) return;
    const { sql: target, isSelection, tabId, params } = pendingRun;
    setPendingRun(null);
    void executeQuery(tabId, target, isSelection, null, params);
  }, [executeQuery, pendingRun]);

  // executeScript runs statements one at a time through the same bounded,
  // non-streaming M1 query path each has always used (a script's per-
  // statement results are small enough to keep whole; the streaming grid is
  // for one large result, not many small ones). It stops at the first error
  // or cancellation rather than continuing past a statement that didn't do
  // what the script expected.
  const executeScript = useCallback(async (tabId: string, statements: string[]) => {
    scriptAbortRef.current = false;
    setRunning(true);
    setRunningTabId(tabId);
    setCancelRequested(false);
    updateTab(tabId, {
      queryError: null,
      result: null,
      script: statements.map((s) => ({ sql: s, status: "pending" as const })),
      scriptSelected: null,
      resultContext: null,
    });
    for (let i = 0; i < statements.length; i++) {
      if (scriptAbortRef.current) {
        updateTab(tabId, (tab) => ({
          script: tab.script?.map((row, idx) => (idx >= i ? { ...row, status: "skipped" as const } : row)) ?? null,
        }));
        break;
      }
      const stmtSql = statements[i];
      const id = queryID();
      activeQuery.current = id;
      cancelRequestedRef.current = false;
      updateTab(tabId, (tab) => ({
        script: tab.script?.map((row, idx) => (idx === i ? { ...row, status: "running" as const } : row)) ?? null,
      }));
      try {
        const result = await api.studioQuery({ query_id: id, sql: stmtSql });
        if (!mounted.current) return;
        updateTab(tabId, (tab) => ({
          script: tab.script?.map((row, idx) => (idx === i ? { ...row, status: "success" as const, result } : row)) ?? null,
          scriptSelected: i,
        }));
        setHistory((current) => pushHistory(current, {
          id: queryID(), sql: stmtSql, ranAt: Date.now(), isSelection: false,
          outcome: "success", rowCount: result.rows.length, elapsedMs: result.elapsed_ms,
        }));
      } catch (error: unknown) {
        if (!mounted.current) return;
        if (error instanceof ApiError && error.status === 401) {
          onUnauthorized();
          scriptAbortRef.current = true;
        }
        const canceled = error instanceof ApiError && error.status === 408 && cancelRequestedRef.current;
        const message = canceled ? "Canceled." : errorMessage(error);
        updateTab(tabId, (tab) => ({
          script: tab.script?.map((row, idx) => (idx === i ? { ...row, status: canceled ? "canceled" as const : "error" as const, error: message } : row)) ?? null,
          scriptSelected: i,
        }));
        setHistory((current) => pushHistory(current, {
          id: queryID(), sql: stmtSql, ranAt: Date.now(), isSelection: false,
          outcome: canceled ? "canceled" : "error", error: canceled ? undefined : message,
        }));
        scriptAbortRef.current = true;
      } finally {
        if (activeQuery.current === id) activeQuery.current = null;
        cancelRequestedRef.current = false;
      }
    }
    if (mounted.current) {
      setRunning(false);
      setRunningTabId(null);
      setCancelRequested(false);
    }
  }, [onUnauthorized, updateTab]);

  // requestRunScript splits the whole buffer with the server's real-lexer
  // tokenizer (never a naive client-side ';' split), then analyzes every
  // resulting statement so a script containing a destructive statement gets
  // one consolidated warning before anything runs — not a confirmation
  // dialog interrupting the script partway through.
  const requestRunScript = useCallback(async () => {
    if (running || checkingSQL || splittingScript) return;
    const tabId = activeTabId;
    const buffer = sql;
    if (!buffer.trim()) return;
    setSplittingScript(true);
    try {
      const { statements } = await api.studioSplit(buffer);
      if (!mounted.current) return;
      const analyses = await Promise.all(statements.map((s) => api.studioAnalyze(s).catch(() => null)));
      if (!mounted.current) return;
      const flagged = analyses
        .map((a, index) => ({ a, index }))
        .filter((entry) => entry.a?.destructive || entry.a?.realm_scoped || (readOnlyMode && entry.a?.write))
        .map((entry) => {
          const reasons = entry.a?.destructive ? [...(entry.a.reasons ?? [])] : [];
          if (entry.a?.realm_scoped) {
            reasons.push(realmScopeWarning(entry.a.kind, who.realm ?? "", who.database ?? ""));
          }
          if (readOnlyMode && entry.a?.write) {
            reasons.push(`Read-only mode is on${environment ? ` for this ${environment} connection` : ""}; this ${entry.a.kind} writes data.`);
          }
          return { index: entry.index, reasons };
        });
      if (flagged.length > 0) {
        setPendingScript({ tabId, statements, flagged });
        return;
      }
      void executeScript(tabId, statements);
    } catch (error: unknown) {
      if (!mounted.current) return;
      if (error instanceof ApiError && error.status === 401) {
        onUnauthorized();
        return;
      }
      updateTab(tabId, { queryError: errorMessage(error) });
    } finally {
      if (mounted.current) setSplittingScript(false);
    }
  }, [activeTabId, checkingSQL, environment, executeScript, onUnauthorized, readOnlyMode, running, splittingScript, sql, updateTab, who.realm, who.database]);

  const confirmPendingScript = useCallback(() => {
    if (!pendingScript) return;
    const { tabId, statements } = pendingScript;
    setPendingScript(null);
    void executeScript(tabId, statements);
  }, [executeScript, pendingScript]);

  const cancelQuery = useCallback(async () => {
    const id = activeQuery.current;
    const tabId = runningTabId;
    if (!id || cancelRequested) return;
    cancelRequestedRef.current = true;
    scriptAbortRef.current = true;
    setCancelRequested(true);
    try {
      await api.studioCancel(id);
    } catch (error: unknown) {
      if (!mounted.current) return;
      if (error instanceof ApiError && error.status === 404) return;
      handleFailure(error, (message) => { if (tabId) updateTab(tabId, { queryError: message }); });
    }
  }, [cancelRequested, handleFailure, runningTabId, updateTab]);

  // Find/replace edits only the local editor buffer text — never the
  // database — so it needs no confirm-before-run check of its own. Matching
  // is a literal substring search (never a regex), matching how a user's
  // own find text should behave with no surprise pattern interpretation.
  const findMatches = useMemo(() => findAllMatches(sql, findQuery, findMatchCase), [sql, findQuery, findMatchCase]);

  // A fresh query, a toggled match-case setting, or switching tabs all
  // invalidate any remembered match position — it may no longer point at
  // the same text, or even the same buffer.
  useEffect(() => {
    setCurrentMatchIndex(null);
    setReplaceStatus(null);
  }, [findQuery, findMatchCase, activeTabId]);

  const openFind = useCallback(() => {
    const selection = selectedSQL();
    if (selection) setFindQuery(selection);
    setFindOpen(true);
    requestAnimationFrame(() => findInputRef.current?.focus());
  }, [selectedSQL]);

  // Reflow the active tab's SQL for readability. formatSQL never runs
  // anything and returns the buffer untouched when it cannot reformat
  // safely (an unterminated string/comment, or a reflow that would not
  // re-tokenize identically) — so the worst case is "nothing changed".
  const formatBuffer = useCallback(() => {
    const current = tabs.find((t) => t.id === activeTabId)?.sql ?? "";
    if (!current.trim()) {
      setFormatNotice("Nothing to format.");
      return;
    }
    const next = formatSQL(current);
    if (next === current) {
      setFormatNotice("Already formatted, or it could not be reformatted safely.");
      return;
    }
    updateTab(activeTabId, { sql: next });
    setFormatNotice("Formatted.");
  }, [tabs, activeTabId, updateTab]);

  useEffect(() => {
    if (!formatNotice) return;
    const timer = window.setTimeout(() => setFormatNotice(null), 4000);
    return () => window.clearTimeout(timer);
  }, [formatNotice]);

  const focusMatch = useCallback((index: number, matches: FindMatch[]) => {
    const textarea = editorHost.current?.querySelector("textarea");
    const match = matches[index];
    if (!textarea || !match) return;
    textarea.focus();
    textarea.setSelectionRange(match.start, match.end);
  }, []);

  const findNext = useCallback(() => {
    if (findMatches.length === 0) return;
    const textarea = editorHost.current?.querySelector("textarea");
    const after = textarea ? textarea.selectionEnd : 0;
    const index = nextMatchIndex(findMatches, after);
    if (index === null) return;
    setCurrentMatchIndex(index);
    focusMatch(index, findMatches);
  }, [findMatches, focusMatch]);

  const findPrevious = useCallback(() => {
    if (findMatches.length === 0) return;
    const textarea = editorHost.current?.querySelector("textarea");
    const before = textarea ? textarea.selectionStart : sql.length;
    const index = previousMatchIndex(findMatches, before);
    if (index === null) return;
    setCurrentMatchIndex(index);
    focusMatch(index, findMatches);
  }, [findMatches, focusMatch, sql.length]);

  // Replace acts on the current selection only when it exactly matches one
  // of the live matches (mirrors common editor "Replace" behavior); otherwise
  // it just finds the next occurrence first, requiring a second click to
  // actually replace, rather than guessing which match the user meant.
  // Editing happens through the same native-setter-plus-"input"-event path
  // the CodeEditor's own onChange contract expects, so StudioTab.sql stays
  // in sync exactly as if the user had typed the replacement.
  const replaceCurrent = useCallback(() => {
    const textarea = editorHost.current?.querySelector("textarea");
    if (!textarea) return;
    const onMatch = findMatches.some((m) => m.start === textarea.selectionStart && m.end === textarea.selectionEnd);
    if (!onMatch) {
      findNext();
      return;
    }
    const start = textarea.selectionStart;
    const end = textarea.selectionEnd;
    const nextValue = sql.slice(0, start) + replaceQuery + sql.slice(end);
    const setter = Object.getOwnPropertyDescriptor(window.HTMLTextAreaElement.prototype, "value")?.set;
    setter?.call(textarea, nextValue);
    textarea.dispatchEvent(new Event("input", { bubbles: true }));
    const cursor = start + replaceQuery.length;
    const remaining = findAllMatches(nextValue, findQuery, findMatchCase);
    const index = nextMatchIndex(remaining, cursor);
    if (index !== null) {
      setCurrentMatchIndex(index);
      textarea.setSelectionRange(remaining[index].start, remaining[index].end);
    } else {
      textarea.setSelectionRange(cursor, cursor);
      setCurrentMatchIndex(null);
    }
  }, [findMatches, findMatchCase, findNext, findQuery, replaceQuery, sql]);

  const replaceAll = useCallback(() => {
    if (findMatches.length === 0) return;
    const count = findMatches.length;
    setSQL(replaceAllMatches(sql, findMatches, replaceQuery));
    setReplaceStatus(`Replaced ${count} occurrence${count === 1 ? "" : "s"}.`);
    setCurrentMatchIndex(null);
  }, [findMatches, replaceQuery, setSQL, sql]);

  const onEditorKeyDown = useCallback((event: KeyboardEvent<HTMLDivElement>) => {
    if (event.key === "Enter" && (event.ctrlKey || event.metaKey)) {
      event.preventDefault();
      event.stopPropagation();
      void requestRun();
    } else if (event.code === "KeyF" && event.shiftKey && event.altKey && !event.ctrlKey && !event.metaKey) {
      event.preventDefault();
      event.stopPropagation();
      formatBuffer();
    } else if ((event.key === "f" || event.key === "F") && (event.ctrlKey || event.metaKey)) {
      event.preventDefault();
      event.stopPropagation();
      openFind();
    } else if (event.key === " " && event.ctrlKey && !suggestOpen) {
      event.preventDefault();
      event.stopPropagation();
      openSuggest();
    } else if (suggestOpen) {
      if (event.key === "Escape") {
        event.preventDefault();
        event.stopPropagation();
        setSuggestOpen(false);
      } else if (event.key === "ArrowDown") {
        event.preventDefault();
        event.stopPropagation();
        setActiveSuggestionIndex((i) => (suggestions.length === 0 ? 0 : (i + 1) % suggestions.length));
      } else if (event.key === "ArrowUp") {
        event.preventDefault();
        event.stopPropagation();
        setActiveSuggestionIndex((i) => (suggestions.length === 0 ? 0 : (i - 1 + suggestions.length) % suggestions.length));
      } else if (event.key === "Enter" || event.key === "Tab") {
        const chosen = suggestions[activeSuggestionIndex];
        if (chosen) {
          event.preventDefault();
          event.stopPropagation();
          acceptSuggestion(chosen);
        }
      }
    }
  }, [acceptSuggestion, activeSuggestionIndex, formatBuffer, openFind, openSuggest, requestRun, suggestOpen, suggestions]);

  const insertTableQuery = useCallback(() => {
    if (selectedTable) setSQL(`SELECT * FROM ${selectedTable} LIMIT 100`);
  }, [selectedTable]);

  // Connection environment tagging (production safety). The label is a
  // per-viewer browser preference keyed by this connection's realm/database/
  // user — never sent anywhere, no credential stored. A production tag turns
  // on the read-only safety mode by default for the session (the user can
  // still toggle it off, and each write is individually overridable via the
  // confirm dialog). Every storage access is guarded: a private window or
  // blocked site data just means "no tag", which is the safe default.
  const envStorageKey = useMemo(
    () => environmentStorageKey(who.realm ?? "", who.database ?? "", who.user ?? ""),
    [who.realm, who.database, who.user],
  );
  useEffect(() => {
    let stored: string | null = null;
    try {
      stored = window.localStorage.getItem(envStorageKey);
    } catch {
      stored = null;
    }
    const env = isStudioEnvironment(stored) ? stored : null;
    setEnvironmentState(env);
    setReadOnlyMode(env === "production");
  }, [envStorageKey]);

  const setEnvironment = useCallback(
    (next: StudioEnvironment | null) => {
      setEnvironmentState(next);
      setReadOnlyMode(next === "production");
      try {
        if (next) window.localStorage.setItem(envStorageKey, next);
        else window.localStorage.removeItem(envStorageKey);
      } catch {
        // A read-only storage context still gets the in-memory tag for this
        // session; it just won't persist across reloads.
      }
    },
    [envStorageKey],
  );

  // Recent connections: realm/database pairs recently switched to on this
  // nextsqld, keyed per host + user (never a credential — a recent entry only
  // prefills the Switch-connection form, which still asks for the password).
  const recentKey = useMemo(
    () => recentConnectionStorageKey(bootstrap?.server_addr ?? "", who.user ?? ""),
    [bootstrap?.server_addr, who.user],
  );
  useEffect(() => {
    try {
      setRecentConnections(parseRecentConnections(window.localStorage.getItem(recentKey)));
    } catch {
      setRecentConnections([]);
    }
  }, [recentKey]);
  const rememberConnection = useCallback(
    (realm: string, database: string) => {
      // Write synchronously here rather than inside a setState updater: a
      // successful switch immediately remounts this component (the Studio key
      // includes the realm/database), and a pending updater on the outgoing
      // fiber would be discarded. localStorage is the source of truth the new
      // mount reads.
      try {
        const current = parseRecentConnections(window.localStorage.getItem(recentKey));
        const next = recordRecentConnection(current, realm, database, Date.now());
        window.localStorage.setItem(recentKey, serializeRecentConnections(next));
        setRecentConnections(next);
      } catch {
        setRecentConnections((c) => recordRecentConnection(c, realm, database, Date.now()));
      }
    },
    [recentKey],
  );

  const warnings = [...(bootstrap?.warnings ?? []), ...(detail?.warnings ?? [])];
  const contextLabel = [who.user, who.database && `db:${who.database}`, who.realm && `realm:${who.realm}`]
    .filter(Boolean)
    .join(" · ");

  // Global command palette (Ctrl/Cmd+K): a keyboard launcher over Studio's
  // own actions. Every command maps to an existing handler — the palette adds
  // no behavior of its own, only a way to reach it without the mouse.
  const commands = useMemo<StudioCommand[]>(() => {
    const busy = running || checkingSQL || splittingScript;
    return [
      { id: "new-tab", label: "New query tab", keywords: "editor add", run: addTab },
      { id: "run", label: "Run query", hint: "Ctrl+Enter", keywords: "execute sql", disabled: busy || (!hasSelection && !sql.trim()), run: () => void requestRun() },
      { id: "run-script", label: "Run script", keywords: "execute all statements", disabled: busy || hasSelection || !sql.trim(), run: () => void requestRunScript() },
      { id: "cancel", label: "Cancel running query", keywords: "stop abort", disabled: !running || cancelRequested, run: () => void cancelQuery() },
      { id: "suggest", label: "Suggest table / column names", hint: "Ctrl+Space", keywords: "autocomplete intellisense", run: openSuggest },
      { id: "format-sql", label: "Format SQL", hint: "Shift+Alt+F", keywords: "reflow pretty print indent beautify tidy", disabled: !sql.trim(), run: formatBuffer },
      { id: "saved", label: "Saved queries…", keywords: "snippets folders tags", run: () => setSavedOpen(true) },
      { id: "search-objects", label: "Search objects…", keywords: "find table workflow", run: openObjectSearch },
      { id: "switch-connection", label: "Switch connection…", keywords: "realm database reconnect", run: () => setSwitchOpen(true) },
      { id: "schema-diagram", label: "Schema diagram…", keywords: "er foreign keys relationships", run: () => setSchemaDiagramOpen(true) },
      { id: "data-generator", label: "Generate development data…", keywords: "seed rows insert synthetic fixture mock sample", run: () => setDataGeneratorOpen(true) },
      { id: "import-data", label: "Import CSV / JSON data…", keywords: "load file tsv ndjson insert upload", run: () => setImportOpen(true) },
      { id: "import-vector-dataset", label: "Import vector dataset…", keywords: "embedding vector bitvector sparsevector ndjson load insert ann", run: () => setVectorImportOpen(true) },
      { id: "dml-builder", label: "Parameterized INSERT / UPDATE / DELETE…", keywords: "dml template placeholder $1 bind statement write", run: () => setDmlBuilderOpen(true) },
      { id: "design-table", label: "Design table…", keywords: "create table schema ddl columns primary key foreign key", run: () => { setSchemaDesignerMode("table"); setSchemaDesignerOpen(true); } },
      { id: "design-index", label: "Design index…", keywords: "create index unique fulltext vector spatial btree", run: () => { setSchemaDesignerMode("index"); setSchemaDesignerOpen(true); } },
      { id: "grant-builder", label: "GRANT / REVOKE builder…", keywords: "privileges rbac permissions", run: () => setGrantBuilderOpen(true) },
      { id: "explorer-fulltext", label: "Open Full-text explorer", keywords: "search bm25", run: () => setFullTextExplorerOpen(true) },
      { id: "explorer-vector", label: "Open Vector explorer", keywords: "ann nearest embedding", run: () => setVectorExplorerOpen(true) },
      { id: "explorer-hybrid", label: "Open Hybrid explorer", keywords: "search vector filter", run: () => setHybridExplorerOpen(true) },
      { id: "explorer-geo", label: "Open Geo explorer", keywords: "spatial point polygon", run: () => setGeoExplorerOpen(true) },
      { id: "explorer-users", label: "Open Users & roles", keywords: "security grants privileges", run: openSecurityExplorer },
      { id: "explorer-txn", label: "Open Transactions & locks", keywords: "activity sessions", run: openActivityExplorer },
      { id: "explorer-audit", label: "Open Audit log", keywords: "verify trail", run: openAuditExplorer },
      { id: "explorer-workflows", label: "Open Workflows, tasks & change streams", keywords: "trigger schedule cdc", run: openWorkflowExplorer },
      { id: "explorer-migrations", label: "Open Schema migration history", keywords: "migrate version dirty schema lifecycle", run: openMigrationExplorer },
      { id: "toggle-explorer", label: explorerVisible ? "Hide database explorer" : "Show database explorer", keywords: "layout pane sidebar", run: () => setExplorerVisible((v) => !v) },
      { id: "toggle-inspector", label: inspectorVisible ? "Hide inspector" : "Show inspector", keywords: "layout pane details", run: () => setInspectorVisible((v) => !v) },
      { id: "reset-layout", label: "Reset layout", keywords: "panes widths default", run: resetLayout },
    ];
  }, [
    running, checkingSQL, splittingScript, hasSelection, sql, cancelRequested,
    addTab, requestRun, requestRunScript, cancelQuery, openSuggest, formatBuffer, openObjectSearch,
    openSecurityExplorer, openActivityExplorer, openAuditExplorer, openWorkflowExplorer,
    openMigrationExplorer, explorerVisible, inspectorVisible, resetLayout,
  ]);

  useEffect(() => {
    const onKey = (event: globalThis.KeyboardEvent) => {
      if ((event.ctrlKey || event.metaKey) && !event.altKey && !event.shiftKey && event.key.toLowerCase() === "k") {
        event.preventDefault();
        setPaletteOpen((open) => !open);
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);

  return (
    <Stack gap="md" className="nss-workspace">
      {bootstrapError ? (
        <Alert variant="error" title="Could not load Studio" role="alert">
          <Inline gap="sm" align="center" wrap>
            <span>{bootstrapError}</span>
            <Button variant="outline" size="sm" onClick={loadBootstrap}>Retry</Button>
          </Inline>
        </Alert>
      ) : null}
      {warnings.map((warning, index) => (
        <Alert key={`${warning}-${index}`} variant="warning" title="Catalog warning">{warning}</Alert>
      ))}

      {draftNoticeVisible ? (
        <Alert variant="info" title="Unsaved editor tabs restored" role="status">
          <Inline gap="sm" align="center" wrap>
            <Text size="sm">
              Your editor tabs from the last session on this connection were restored from this browser.
            </Text>
            <Button variant="outline" size="sm" onClick={() => setDraftNoticeVisible(false)}>Keep them</Button>
            <Button variant="ghost" size="sm" onClick={discardRestoredDrafts}>Start fresh</Button>
          </Inline>
        </Alert>
      ) : null}

      {savedImportNotice ? (
        <Alert variant="info" title="Saved queries" role="status">
          <Inline gap="sm" align="center" wrap>
            <Text size="sm">{savedImportNotice}</Text>
            <Button variant="ghost" size="sm" onClick={() => setSavedImportNotice(null)}>Dismiss</Button>
          </Inline>
        </Alert>
      ) : null}

      {serverConnection && !serverConnection.connected ? (
        <Alert variant="error" title="nextsqld is not connected" role="alert">
          <Inline gap="sm" align="center" wrap>
            <Text size="sm">
              {serverConnection.error || "The Admin session is still signed in, but this workspace cannot reach nextsqld."}
              {" "}Queries will fail until the server is reachable again. Sign in again if the process restarted.
            </Text>
            <Button variant="outline" size="sm" icon={<Icon name="refresh" size={14} />} onClick={onRetryConnection}>Retry</Button>
          </Inline>
        </Alert>
      ) : null}

      {environment === "production" ? (
        <Alert variant="warning" title="Production environment" role="alert">
          <Inline gap="sm" align="center" wrap>
            <Text size="sm">
              This connection is tagged <strong>production</strong>.
              {readOnlyMode
                ? " Read-only mode is on — a write statement asks for confirmation before it runs."
                : " Read-only mode is off — writes run without an extra prompt."}
            </Text>
            <Button
              variant="outline"
              size="sm"
              onClick={() => setReadOnlyMode((v) => !v)}
            >
              {readOnlyMode ? "Turn off read-only mode" : "Turn on read-only mode"}
            </Button>
          </Inline>
        </Alert>
      ) : null}

      <div className="nss-toolbar" aria-label="Studio connection context">
        <Inline gap="sm" align="center" wrap>
          {serverConnection && !serverConnection.connected ? (
            <Badge variant="error" dot>Disconnected</Badge>
          ) : connectionChecking && !serverConnection ? (
            <Badge variant="muted" dot>Checking…</Badge>
          ) : (
            <Badge variant="success" dot>Connected</Badge>
          )}
          {(serverConnection?.server_addr || bootstrap?.server_addr) ? (
            <Text size="sm" variant="muted" title={`nextsqld ${serverConnection?.server_addr || bootstrap?.server_addr}`}>
              {serverConnection?.server_addr || bootstrap?.server_addr}
            </Text>
          ) : null}
          <Text size="sm" variant="muted" title={contextLabel}>{contextLabel}</Text>
          {bootstrap ? (
            <Badge variant="muted">{bootstrap.capabilities.rows.length} capabilities</Badge>
          ) : null}
          {environment && environment !== "production" ? (
            <Badge variant="muted">{environment}</Badge>
          ) : null}
          {environment === "production" && readOnlyMode ? (
            <Badge variant="warning">read-only</Badge>
          ) : null}
          {readMode !== "strong" ? (
            <Badge variant="warning" title="Reads on this session may return stale data">{readMode} reads</Badge>
          ) : null}
        </Inline>
        <Inline gap="sm" align="center" wrap>
          <Button variant="outline" size="sm" icon={<Icon name="plug" size={14} />} onClick={() => setSwitchOpen(true)}>Switch connection…</Button>
          <Select
            id="studio-read-consistency"
            label="Read consistency"
            labelClassName="sr-only"
            wrapperClassName="nss-toolbar-select"
            triggerClassName="nss-toolbar-select-trigger"
            options={[
              { value: "strong", label: "Strong" },
              { value: "bounded", label: "Bounded" },
              { value: "stale", label: "Stale" },
            ]}
            value={readMode}
            disabled={applyingReadMode}
            onChange={(value) => {
              const next: StudioReadConsistency = value === "bounded" || value === "stale" ? value : "strong";
              setReadMode(next);
              applyReadConsistency(next, stalenessSec);
            }}
          />
          {readMode === "bounded" ? (
            <Input
              id="studio-read-staleness"
              label="Max staleness (s)"
              labelClassName="sr-only"
              wrapperClassName="nss-toolbar-staleness"
              placeholder="Staleness (s)"
              type="number"
              min={0}
              value={String(stalenessSec)}
              disabled={applyingReadMode}
              onChange={(event) => setStalenessSec(Number(event.currentTarget.value) || 0)}
              onBlur={() => applyReadConsistency("bounded", stalenessSec)}
            />
          ) : null}
          <Select
            id="studio-environment"
            label="Environment"
            labelClassName="sr-only"
            wrapperClassName="nss-toolbar-select"
            triggerClassName="nss-toolbar-select-trigger"
            options={[
              { value: "", label: "Not set" },
              ...STUDIO_ENVIRONMENTS.map((env) => ({ value: env, label: env })),
            ]}
            value={environment ?? ""}
            onChange={(value) => setEnvironment(isStudioEnvironment(value) ? value : null)}
          />
          {!explorerVisible ? (
            <Button variant="outline" size="sm" icon={<Icon name="folder" size={14} />} onClick={() => setExplorerVisible(true)}>Show explorer</Button>
          ) : null}
          {!inspectorVisible ? (
            <Button variant="outline" size="sm" icon={<Icon name="search" size={14} />} onClick={() => setInspectorVisible(true)}>Show inspector</Button>
          ) : null}
          {!explorerVisible ? (
            <Button variant="ghost" size="sm" icon={<Icon name="terminal" size={14} />} onClick={() => setPaletteOpen(true)} title="Command palette (Ctrl/Cmd+K)">Commands</Button>
          ) : null}
        </Inline>
      </div>

      {readConsistencyError ? (
        <Alert variant="error" title="Could not change read consistency" role="alert">
          <Inline gap="sm" align="center" wrap>
            <span>{readConsistencyError}</span>
            <Button variant="ghost" size="sm" onClick={() => setReadConsistencyError(null)}>Dismiss</Button>
          </Inline>
        </Alert>
      ) : null}

      {switchOpen ? (
        <SwitchConnection
          serverAddr={bootstrap?.server_addr ?? "nextsqld"}
          user={who.user}
          currentRealm={who.realm ?? ""}
          currentDatabase={who.database ?? ""}
          recent={recentConnections}
          onClose={() => setSwitchOpen(false)}
          onSwitched={(next) => {
            setSwitchOpen(false);
            rememberConnection(next.realm, next.database);
            onConnectionChanged(next);
          }}
        />
      ) : null}

      <div
        className={[
          "nss-layout",
          explorerVisible ? "" : "nss-layout--no-explorer",
          inspectorVisible ? "" : "nss-layout--no-inspector",
        ].filter(Boolean).join(" ")}
        style={{
          ["--nss-explorer-w" as string]: `${explorerWidth}px`,
          ["--nss-inspector-w" as string]: `${inspectorWidth}px`,
        }}
      >
        {explorerVisible ? (
        <Card variant="bordered" className="nss-explorer">
          <CardHeader className="nss-explorer-header">
            <CardTitle as="h2">
              <Inline gap="xs" align="center" wrap={false}>
                <Icon name="database" size={16} />
                Database explorer
              </Inline>
            </CardTitle>
            <Inline gap="xs" align="center" wrap>
              <Button variant="ghost" size="sm" icon={<Icon name="terminal" size={14} />} onClick={() => setPaletteOpen(true)} title="Command palette (Ctrl/Cmd+K)">Commands</Button>
              <Button variant="outline" size="sm" icon={<Icon name="search" size={14} />} onClick={openObjectSearch}>Search objects…</Button>
              <Button variant="ghost" size="sm" onClick={() => setExplorerVisible(false)}>Hide explorer</Button>
            </Inline>
          </CardHeader>
          <CardBody className="nss-explorer-body">
            <Input
              id="studio-table-filter"
              label="Filter tables"
              size="sm"
              value={filter}
              onChange={(event) => setFilter(event.currentTarget.value)}
              placeholder="Table name"
            />
            <SchemaTree
              tables={visibleTables}
              totalTables={allTables.length}
              filterActive={filter.trim().length > 0}
              tablesTruncated={Boolean(bootstrap?.tables_truncated)}
              loadingTables={loadingBootstrap}
              selectedTable={selectedTable}
              onSelectTable={selectTable}
              loadTable={loadFullTextTable}
              loadWorkflows={loadWorkflowOverview}
            />
          </CardBody>
        </Card>
        ) : null}
        {explorerVisible ? (
          <LayoutSplitter
            label="Resize database explorer"
            value={explorerWidth}
            min={MIN_EXPLORER_WIDTH}
            max={MAX_EXPLORER_WIDTH}
            onChange={setExplorerWidth}
          />
        ) : null}

        <Card variant="bordered" className="nss-editor-card">
          <CardHeader className="nss-editor-header">
            <Stack gap="xs">
              <CardTitle as="h2">
                <Inline gap="xs" align="center" wrap={false}>
                  <Icon name="studio" size={16} />
                  Query editor
                </Inline>
              </CardTitle>
              <Text size="sm" variant="muted">
                One statement · 1 MiB SQL · 25 second timeout
                {hasSelection ? " · a selection runs only the highlighted text" : ""}
              </Text>
            </Stack>
            <div className="nss-editor-actions">
              <div className="nss-action-group">
                <Button variant="primary" size="sm" icon={<Icon name="play" size={14} />} onClick={() => void requestRun()} disabled={running || checkingSQL || splittingScript || (serverConnection !== null && !serverConnection.connected) || (!hasSelection && !sql.trim())} title="Run query (Ctrl+Enter)">
                  {otherTabRunning ? "Busy…" : running ? "Running…" : checkingSQL ? "Checking…" : hasSelection ? "Run selection" : "Run query"}
                </Button>
                <Button variant="outline" size="sm" icon={<Icon name="list" size={14} />} onClick={() => void requestRunScript()} disabled={running || checkingSQL || splittingScript || hasSelection || (serverConnection !== null && !serverConnection.connected) || !sql.trim()} title="Run script">
                  {splittingScript ? "Splitting…" : "Run script"}
                </Button>
                <Button variant="outline" size="sm" icon={<Icon name="stop" size={14} />} onClick={() => void cancelQuery()} disabled={!running || cancelRequested} title="Cancel query">
                  {cancelRequested ? "Canceling…" : "Cancel"}
                </Button>
              </div>
              <div className="nss-toolbar-sep" aria-hidden="true" />
              <div className="nss-action-group">
              <Popover
                open={suggestOpen}
                onOpenChange={(open) => (open ? openSuggest() : setSuggestOpen(false))}
                ariaLabel="SQL suggestions"
                side="bottom"
                align="start"
                trigger={<Button variant="outline" size="sm" icon={<Icon name="search" size={14} />} onClick={openSuggest}>Suggest</Button>}
              >
                <PopoverContent className="nss-suggest-panel">
                  <Stack gap="sm">
                    <Text size="sm" weight="medium">
                      {jsonPathContext
                        ? "Indexed JSON paths"
                        : nearestContext?.slot === "column"
                          ? "Vector columns"
                          : nearestContext?.slot === "metric"
                            ? "Vector metrics"
                            : "SQL suggestions"}
                    </Text>
                    <Text size="xs" variant="muted">
                      {jsonPathContext
                        ? "Native JSON paths that a referenced table has an index on — the only JSON structure the server exposes metadata for."
                        : nearestContext?.slot === "column"
                          ? "VECTOR / BITVECTOR / SPARSEVECTOR columns on a referenced table — the only vector metadata the catalog exposes for completion."
                          : nearestContext?.slot === "metric"
                            ? "Metrics the NEAREST column's declared type actually accepts. There is no per-element vector metadata to complete inside TO (…)."
                            : "Catalog table and column names only — no keywords. Ctrl+Space reopens; type to filter, Up/Down/Enter/Esc work without leaving the editor."}
                    </Text>
                    <ul id="studio-suggest-listbox" role="listbox" aria-label="SQL suggestions" className="nss-suggest-list">
                      {suggestions.length === 0 ? (
                        <li className="nss-suggest-empty">No matches</li>
                      ) : (
                        suggestions.map((suggestion, index) => (
                          <li
                            key={`${suggestion.kind}-${suggestion.table ?? ""}-${suggestion.insertText}-${index}`}
                            id={`studio-suggest-option-${index}`}
                            role="option"
                            aria-selected={index === activeSuggestionIndex}
                            className={`nss-suggest-option${index === activeSuggestionIndex ? " nss-suggest-option-active" : ""}`}
                            onClick={() => acceptSuggestion(suggestion)}
                          >
                            <Badge variant={suggestion.kind === "table" ? "info" : "muted"} size="sm">
                              {suggestion.kind === "table"
                                ? "Table"
                                : suggestion.kind === "json-path"
                                  ? "JSON path"
                                  : suggestion.kind === "vector-column"
                                    ? "Vector"
                                    : suggestion.kind === "vector-metric"
                                      ? "Metric"
                                      : "Column"}
                            </Badge>
                            <span>{suggestion.label}</span>
                          </li>
                        ))
                      )}
                    </ul>
                    {referencedTables.some((table) => tableColumnsCache[table] === "loading") ? (
                      <Text size="xs" variant="muted">
                        {jsonPathContext
                          ? "Loading index metadata…"
                          : nearestContext
                            ? "Loading vector columns…"
                            : "Loading columns…"}
                      </Text>
                    ) : null}
                  </Stack>
                </PopoverContent>
              </Popover>
              <Button
                variant="outline"
                size="sm"
                onClick={formatBuffer}
                disabled={!sql.trim()}
                title="Reflow this tab's SQL (Shift+Alt+F) — never runs anything"
              >
                Format
              </Button>
              <Popover
                open={findOpen}
                onOpenChange={setFindOpen}
                ariaLabel="Find and replace"
                side="bottom"
                align="start"
                trigger={<Button variant="outline" size="sm" onClick={openFind}>Find</Button>}
              >
                <PopoverContent className="nss-find-panel">
                  <Stack gap="sm">
                    <Text size="sm" weight="medium">Find and replace</Text>
                    <Text size="xs" variant="muted">Edits only this tab's SQL text — never runs anything.</Text>
                    <Input
                      ref={findInputRef}
                      label="Find"
                      size="sm"
                      value={findQuery}
                      onChange={(event) => setFindQuery(event.currentTarget.value)}
                      onKeyDown={(event) => {
                        if (event.key === "Enter") {
                          event.preventDefault();
                          if (event.shiftKey) findPrevious(); else findNext();
                        }
                      }}
                      placeholder="Text to find"
                    />
                    <Checkbox
                      label="Match case"
                      size="sm"
                      checked={findMatchCase}
                      onChange={(event) => setFindMatchCase(event.currentTarget.checked)}
                    />
                    <Inline gap="sm" align="center" justify="between">
                      <Text size="xs" variant="muted">
                        {!findQuery
                          ? ""
                          : findMatches.length === 0
                            ? "No matches"
                            : currentMatchIndex === null
                              ? `${findMatches.length} match${findMatches.length === 1 ? "" : "es"}`
                              : `Match ${currentMatchIndex + 1} of ${findMatches.length}`}
                      </Text>
                      <Inline gap="xs">
                        <Button variant="outline" size="sm" onClick={findPrevious} disabled={findMatches.length === 0}>Previous</Button>
                        <Button variant="outline" size="sm" onClick={findNext} disabled={findMatches.length === 0}>Next</Button>
                      </Inline>
                    </Inline>
                    <Input
                      label="Replace with"
                      size="sm"
                      value={replaceQuery}
                      onChange={(event) => setReplaceQuery(event.currentTarget.value)}
                      placeholder="Replacement text"
                    />
                    <Inline gap="xs" justify="end">
                      <Button variant="outline" size="sm" onClick={replaceCurrent} disabled={findMatches.length === 0}>Replace</Button>
                      <Button variant="outline" size="sm" onClick={replaceAll} disabled={findMatches.length === 0}>Replace all</Button>
                    </Inline>
                    {replaceStatus ? <Text size="xs" variant="muted">{replaceStatus}</Text> : null}
                  </Stack>
                </PopoverContent>
              </Popover>
              </div>
              <div className="nss-toolbar-sep" aria-hidden="true" />
              <div className="nss-action-group">
                <Popover
                  open={moreOpen}
                  onOpenChange={setMoreOpen}
                  ariaLabel="More Studio tools"
                  side="bottom"
                  align="end"
                  wrapperClassName="nss-more"
                  className="nss-more-panel"
                  trigger={
                    <Button variant="outline" size="sm" icon={<Icon name="more" size={14} />} aria-label="More Studio tools" title="More tools">
                      More
                    </Button>
                  }
                >
                  <PopoverContent className="nss-more-menu">
                    {moreGroups.map((group) => (
                      <div key={group.label} className="nss-more-group">
                        <p className="nss-more-group-label">{group.label}</p>
                        {group.items.map((item) => (
                          <button
                            key={item.label}
                            type="button"
                            className="nss-more-item"
                            disabled={item.disabled}
                            title={item.title}
                            onClick={item.run}
                          >
                            <Icon name={item.icon} size={14} />
                            <span>{item.label}</span>
                          </button>
                        ))}
                      </div>
                    ))}
                  </PopoverContent>
                </Popover>
              </div>
              <div className="nss-toolbar-sep" aria-hidden="true" />
              <div className="nss-action-group">
                <input
                  ref={savedFileInput}
                  type="file"
                  accept="application/json,.json"
                  hidden
                  onChange={(event) => {
                    const file = event.currentTarget.files?.[0];
                    event.currentTarget.value = "";
                    if (file) importSavedQueriesFromFile(file);
                  }}
                />
                <SavedQueries
                  open={savedOpen}
                  onOpenChange={setSavedOpen}
                  queries={savedQueries}
                  currentSQL={sql}
                  onSave={saveCurrentQuery}
                  onUpdateSQL={updateSavedQuerySQL}
                  onRename={renameSavedQuery}
                  onDelete={deleteSavedQuery}
                  onLoad={loadSavedQuery}
                  onExport={exportSavedQueriesToFile}
                  onImport={requestSavedQueryImport}
                />
                <Popover
                  open={historyOpen}
                  onOpenChange={setHistoryOpen}
                  ariaLabel="Query history"
                  side="bottom"
                  align="end"
                  trigger={
                    <Button variant="outline" size="sm">
                      History{history.length ? ` (${history.length})` : ""}
                    </Button>
                  }
                >
                  <PopoverContent className="nss-history-panel">
                    <Stack gap="sm">
                      <Inline gap="sm" align="center" justify="between">
                        <Text size="sm" weight="medium">Query history</Text>
                        <Button variant="ghost" size="sm" onClick={clearHistory} disabled={!history.length}>Clear</Button>
                      </Inline>
                      <Text size="xs" variant="muted">This session only — not saved between reloads.</Text>
                      {history.length ? (
                        <ul className="nss-history-list" aria-label="Recent statements">
                          {history.map((entry) => (
                            <li key={entry.id}>
                              <button
                                type="button"
                                className="nss-history-item"
                                onClick={() => loadFromHistory(entry)}
                                title={entry.sql}
                              >
                                <span className="nss-history-sql">{historyPreview(entry.sql)}</span>
                                <Inline gap="xs" align="center">
                                  <Badge
                                    size="sm"
                                    variant={entry.outcome === "success" ? "success" : entry.outcome === "error" ? "error" : "muted"}
                                  >
                                    {entry.outcome}
                                  </Badge>
                                  {entry.outcome === "success" ? (
                                    <Text as="span" size="xs" variant="muted">
                                      {(entry.rowCount ?? 0).toLocaleString()} rows · {(entry.elapsedMs ?? 0).toLocaleString()} ms
                                    </Text>
                                  ) : null}
                                </Inline>
                              </button>
                            </li>
                          ))}
                        </ul>
                      ) : (
                        <Text size="sm" variant="muted">No queries run yet this session.</Text>
                      )}
                    </Stack>
                  </PopoverContent>
                </Popover>
              </div>
            </div>
          </CardHeader>
          <CardBody className="nss-editor-body">
            <div className="nss-tab-strip" aria-label="Query tabs">
              {tabs.map((tab) => (
                <div key={tab.id} className="nss-tab">
                  <button
                    type="button"
                    className="nss-tab-button"
                    aria-pressed={tab.id === activeTabId}
                    onClick={() => setActiveTabId(tab.id)}
                  >
                    {tab.id === runningTabId ? <Spinner size="xs" /> : null}
                    <span>{tab.title}</span>
                  </button>
                  {tabs.length > 1 ? (
                    <button
                      type="button"
                      className="nss-tab-close"
                      aria-label={`Close ${tab.title}`}
                      disabled={tab.id === runningTabId}
                      onClick={() => closeTab(tab.id)}
                    >
                      ×
                    </button>
                  ) : null}
                </div>
              ))}
              <button
                type="button"
                className="nss-tab-add"
                aria-label="New query tab"
                disabled={tabs.length >= MAX_STUDIO_TABS}
                onClick={addTab}
              >
                +
              </button>
            </div>
            {otherTabRunning ? (
              <Alert variant="info" title="Another tab is running">
                {`"${runningTab?.title ?? "A tab"}" is running a query — only one query runs at a time.`}
              </Alert>
            ) : null}
            <div ref={editorHost} onKeyDownCapture={onEditorKeyDown} className="nss-editor-host">
              <CodeEditor
                value={sql}
                onChange={setSQL}
                language="sql"
                placeholder="Enter NextSQL SQL"
                minRows={12}
                maxRows={28}
              />
            </div>
            {formatNotice ? (
              <Text size="xs" variant="muted" role="status" className="nss-format-notice">
                {formatNotice}
              </Text>
            ) : null}
            {/* aria-live="polite" (not role="alert") deliberately: this recomputes
                on every keystroke, unlike the explicit-action confirm/error alerts
                above, so it must never interrupt like an assertive alert would. */}
            {tableNameFixes.length > 0 ? (
              <div className="nss-table-fixes" aria-live="polite">
                {tableNameFixes.map((fix) => (
                  <Alert key={fix.badName} variant="warning" title="Table not found">
                    <Inline gap="sm" align="center" wrap>
                      <Text as="span" size="sm">
                        {`"${fix.badName}" doesn't match any table you can see.`}
                      </Text>
                      <Button variant="outline" size="sm" onClick={() => applyFix(fix)}>
                        {`Use "${fix.suggestion}" instead`}
                      </Button>
                    </Inline>
                  </Alert>
                ))}
              </div>
            ) : null}
            {/* Live parse diagnostics. aria-live="polite" for the same reason
                as the table-fix strip above — it recomputes as you type and
                must not interrupt. Advisory: the server re-parses on Run. */}
            {parseDiags.length > 0 ? (
              <div className="nss-parse-diags" aria-live="polite">
                {parseDiags.map((diag, index) => (
                  <Alert
                    key={`${diag.offset}-${index}`}
                    variant="warning"
                    title={parseDiags.length > 1 ? `Syntax error (${index + 1} of ${parseDiags.length})` : "Syntax error"}
                  >
                    <Inline gap="sm" align="center" wrap>
                      <Text as="span" size="sm">
                        {`Line ${diag.line}, column ${diag.column}: ${diag.message}.`}
                      </Text>
                      <Button variant="outline" size="sm" onClick={() => jumpToDiagnostic(diag)}>
                        Go to error
                      </Button>
                    </Inline>
                  </Alert>
                ))}
              </div>
            ) : null}
            {queryParamNumbers.length > 0 ? (
              <section className="nss-params" aria-labelledby="studio-params-title">
                <Heading id="studio-params-title" as="h3" size="sm">Parameters</Heading>
                <Text size="xs" variant="muted">
                  Bind values for this statement's placeholders. Values are sent as text and
                  coerced to each placeholder's type by the server. Not saved with the query.
                </Text>
                <ul className="nss-params-list" aria-label="Query parameters">
                  {queryParamNumbers.map((n) => {
                    const slot = activeTab.paramValues[n] ?? { text: "", isNull: false };
                    return (
                      <li key={n} className="nss-params-item">
                        <Input
                          id={`studio-param-${n}`}
                          label={`$${n}`}
                          size="sm"
                          value={slot.text}
                          disabled={slot.isNull}
                          placeholder={slot.isNull ? "NULL" : "value"}
                          onChange={(event) => setParamValue(n, { text: event.currentTarget.value })}
                        />
                        <Checkbox
                          label="NULL"
                          size="sm"
                          checked={slot.isNull}
                          onChange={(event) => setParamValue(n, { isNull: event.currentTarget.checked })}
                        />
                      </li>
                    );
                  })}
                </ul>
              </section>
            ) : null}
            {script ? (
              <>
                <div className="nss-query-status" aria-live="polite">
                  <Text size="sm" variant="muted">
                    {running && runningTabId === activeTabId
                      ? `Running statement ${script.filter((row) => row.status !== "pending").length} of ${script.length}…`
                      : `${script.filter((row) => row.status === "success").length} of ${script.length} statements ran`}
                  </Text>
                </div>
                <section aria-labelledby="studio-script-title" className="nss-results">
                  <Heading id="studio-script-title" as="h3" size="sm">Script results</Heading>
                  <ul className="nss-script-list" aria-label="Script statements">
                    {script.map((row, index) => (
                      <li key={index}>
                        <button
                          type="button"
                          className="nss-script-item"
                          aria-pressed={scriptSelected === index}
                          onClick={() => updateTab(activeTabId, { scriptSelected: index })}
                        >
                          <span className="nss-script-index">{index + 1}</span>
                          <span className="nss-history-sql">{historyPreview(row.sql)}</span>
                          <Inline gap="xs" align="center">
                            <Badge
                              size="sm"
                              variant={
                                row.status === "success" ? "success"
                                  : row.status === "error" ? "error"
                                    : row.status === "running" ? "info"
                                      : "muted"
                              }
                            >
                              {row.status === "running" ? <Spinner size="xs" /> : null}
                              {row.status}
                            </Badge>
                            {row.status === "success" && row.result ? (
                              <Text as="span" size="xs" variant="muted">
                                {row.result.rows.length.toLocaleString()} rows · {row.result.elapsed_ms.toLocaleString()} ms
                              </Text>
                            ) : null}
                          </Inline>
                        </button>
                      </li>
                    ))}
                  </ul>
                  {selectedScriptRow ? (
                    selectedScriptRow.status === "error" || selectedScriptRow.status === "canceled" ? (
                      <Alert
                        variant={selectedScriptRow.status === "canceled" ? "info" : "error"}
                        title={selectedScriptRow.status === "canceled" ? "Statement canceled" : "Statement failed"}
                        role="alert"
                      >
                        {selectedScriptRow.error}
                      </Alert>
                    ) : selectedScriptRow.result ? (
                      <ResultGrid
                        result={selectedScriptRow.result}
                        label={`Statement ${(scriptSelected ?? 0) + 1} results`}
                        tools
                        exportPrefix={`nextsql-${who.database || "result"}-stmt-${(scriptSelected ?? 0) + 1}`}
                        jsonContext={jsonExplorerContext}
                      />
                    ) : null
                  ) : (
                    <Text size="sm" variant="muted">Select a statement above to view its result.</Text>
                  )}
                </section>
              </>
            ) : (
              <>
                <div className="nss-query-status" aria-live="polite">
                  {running && runningTabId === activeTabId ? (
                    <Inline gap="sm" align="center">
                      <Spinner size="xs" />
                      <Text size="sm" variant="muted">
                        {cancelRequested
                          ? "Cancellation requested…"
                          : result
                            ? `${result.rows.length.toLocaleString()} rows received…`
                            : "Query running…"}
                      </Text>
                    </Inline>
                  ) : result ? (
                    <Text size="sm" variant="muted">
                      {queryResultSummary(result, ranSelection)}
                    </Text>
                  ) : (
                    <Text size="sm" variant="muted">Ready</Text>
                  )}
                </div>
                {queryError ? (
                  <Alert variant={queryError === "Query canceled." ? "info" : "error"} title={queryError === "Query canceled." ? "Query canceled" : "Query failed"} role="alert">
                    {queryError}
                  </Alert>
                ) : null}
                {result?.truncated ? (
                  <Alert variant="warning" title="Result preview truncated">
                    Studio canceled and closed the result stream after its 5,000-row or 8 MiB preview limit.
                  </Alert>
                ) : null}
                {result ? (
                  <section aria-labelledby="studio-results-title" className="nss-results">
                    <Heading id="studio-results-title" as="h3" size="sm">Results</Heading>
                    <ResultGrid
                      result={result}
                      label="Query results"
                      streaming={running && runningTabId === activeTabId}
                      tools
                      actionsDisabled={Boolean(queryError)}
                      exportPrefix={`nextsql-${who.database || "result"}`}
                      jsonContext={jsonExplorerContext}
                      rankContext={resultContext}
                      planBaseline={activeTab.planBaseline}
                      onPinPlan={(planBaseline) => updateTab(activeTabId, { planBaseline })}
                      onClearPlanBaseline={() => updateTab(activeTabId, { planBaseline: null })}
                      editableTable={editableTable}
                      pkColumns={editablePKs}
                      columnTypes={editableColTypes}
                      onCommitChanges={commitStagedChanges}
                      onOpenInEditor={(newSql) => setSQL(newSql)}
                    />
                  </section>
                ) : (
                  <EmptyState
                    size="sm"
                    density="compact"
                    title="No query results yet"
                    description="Run a statement to see typed, bounded results."
                  />
                )}
              </>
            )}
          </CardBody>
        </Card>

        {inspectorVisible ? (
          <LayoutSplitter
            className="nss-splitter-inspector"
            label="Resize inspector"
            value={inspectorWidth}
            min={MIN_INSPECTOR_WIDTH}
            max={MAX_INSPECTOR_WIDTH}
            invert
            onChange={setInspectorWidth}
          />
        ) : null}
        {inspectorVisible ? (
        <Card variant="bordered" className="nss-inspector">
          <CardHeader className="nss-inspector-header">
            <Stack gap="xs">
              <CardTitle as="h2">
                <Inline gap="xs" align="center" wrap={false}>
                  <Icon name={selectedTable ? "table" : "layers"} size={16} />
                  {selectedTable ?? "Server capabilities"}
                </Inline>
              </CardTitle>
              <Text size="sm" variant="muted">
                {selectedTable ? "Authorized catalog metadata" : "Features reported by this server"}
              </Text>
            </Stack>
            <Inline gap="xs" align="center" wrap>
              {selectedTable ? (
                <Button variant="outline" size="sm" icon={<Icon name="play" size={14} />} onClick={insertTableQuery}>Insert SELECT</Button>
              ) : null}
              <Button variant="ghost" size="sm" onClick={() => setInspectorVisible(false)}>Hide inspector</Button>
            </Inline>
          </CardHeader>
          <CardBody className="nss-inspector-body">
            {loadingDetail ? (
              <Inline gap="sm" align="center" className="nss-loading" role="status">
                <Spinner size="sm" />
                <Text size="sm" variant="muted">Loading table metadata…</Text>
              </Inline>
            ) : detailError ? (
              <Alert variant="error" title="Could not load table" role="alert">{detailError}</Alert>
            ) : detail ? (
              <Stack gap="md">
                <section aria-labelledby="studio-table-overview-title">
                  <Heading id="studio-table-overview-title" as="h3" size="xs">Overview</Heading>
                  <ResultGrid result={detail.table} label={`${detail.name} overview`} />
                </section>
                <section aria-labelledby="studio-columns-title">
                  <Heading id="studio-columns-title" as="h3" size="xs">Columns</Heading>
                  <ResultGrid result={detail.columns} label={`${detail.name} columns`} />
                </section>
                <section aria-labelledby="studio-indexes-title">
                  <Heading id="studio-indexes-title" as="h3" size="xs">Indexes</Heading>
                  <ResultGrid result={detail.indexes} label={`${detail.name} indexes`} />
                </section>
                {(() => {
                  const constraints = tableConstraintsResult(detail);
                  return (
                    <section aria-labelledby="studio-constraints-title">
                      <Heading id="studio-constraints-title" as="h3" size="xs">Constraints</Heading>
                      {constraints.rows.length > 0 ? (
                        <Stack gap="xs">
                          <Text size="xs" variant="muted">
                            Unified from authorized <code>system.columns</code>, <code>system.indexes</code>, and <code>system.foreign_keys</code> metadata.
                          </Text>
                          <ResultGrid result={constraints} label={`${detail.name} constraints`} />
                          {constraints.truncated ? (
                            <Text size="xs" variant="warning" role="status">
                              Constraint preview stopped at {MAX_TABLE_CONSTRAINT_ROWS.toLocaleString()} rows.
                            </Text>
                          ) : null}
                        </Stack>
                      ) : (
                        <Text size="sm" variant="muted">This table has no declared constraints.</Text>
                      )}
                    </section>
                  );
                })()}
                <section aria-labelledby="studio-foreign-keys-title">
                  <Heading id="studio-foreign-keys-title" as="h3" size="xs">Foreign keys</Heading>
                  {(detail.foreign_keys?.rows?.length ?? 0) > 0 ? (
                    <ResultGrid result={detail.foreign_keys} label={`${detail.name} foreign keys`} />
                  ) : (
                    <Text size="sm" variant="muted">This table has no foreign keys.</Text>
                  )}
                </section>
                {((detail.referencing_keys?.rows?.length ?? 0) > 0 || (detail.triggers?.rows?.length ?? 0) > 0) ? (
                  <section aria-labelledby="studio-dependencies-title">
                    <Heading id="studio-dependencies-title" as="h3" size="xs">Dependencies</Heading>
                    <Stack gap="sm">
                      {(detail.referencing_keys?.rows?.length ?? 0) > 0 ? (
                        <Stack gap="xs">
                          <Heading as="h4" size="xs">Referenced by</Heading>
                          <Text size="xs" variant="muted">
                            Foreign keys on other visible tables that point at <code>{detail.name}</code>.
                          </Text>
                          <ResultGrid result={detail.referencing_keys} label={`tables referencing ${detail.name}`} />
                        </Stack>
                      ) : null}
                      {(detail.triggers?.rows?.length ?? 0) > 0 ? (
                        <Stack gap="xs">
                          <Heading as="h4" size="xs">Triggers</Heading>
                          <Text size="xs" variant="muted">
                            Row triggers defined on this table, from authorized <code>system.triggers</code>.
                          </Text>
                          <ResultGrid result={detail.triggers} label={`triggers on ${detail.name}`} />
                        </Stack>
                      ) : null}
                    </Stack>
                  </section>
                ) : null}
                <section aria-labelledby="studio-statistics-title">
                  <Heading id="studio-statistics-title" as="h3" size="xs">Statistics</Heading>
                  {(detail.table_stats?.rows?.length ?? 0) > 0 || (detail.index_stats?.rows?.length ?? 0) > 0 ? (
                    <Stack gap="sm">
                      {(detail.table_stats?.rows?.length ?? 0) > 0 ? (
                        <ResultGrid result={detail.table_stats} label={`${detail.name} table statistics`} />
                      ) : null}
                      {(detail.index_stats?.rows?.length ?? 0) > 0 ? (
                        <ResultGrid result={detail.index_stats} label={`${detail.name} index statistics`} />
                      ) : null}
                    </Stack>
                  ) : (
                    <Text size="sm" variant="muted">
                      No statistics recorded yet. Run ANALYZE on this table to populate row-count estimates.
                    </Text>
                  )}
                </section>
                {(() => {
                  const ddlScript = tableDDLScript(detail);
                  if (!ddlScript) return null;
                  return (
                    <section aria-labelledby="studio-ddl-title">
                      <Heading id="studio-ddl-title" as="h3" size="xs">DDL</Heading>
                      <Stack gap="xs">
                        <Text size="xs" variant="muted">
                          Canonical <code>CREATE</code> statements from <code>system.table_ddl</code>, rendered by the server.
                        </Text>
                        <pre className="nss-ddl-block" tabIndex={0} aria-label={`${detail.name} DDL`}>{ddlScript}</pre>
                        <Inline gap="xs" wrap>
                          <CopyButton value={ddlScript} label="Copy DDL" size="sm" />
                          <Button variant="outline" size="sm" onClick={() => { setSQL(ddlScript); }}>
                            Open in editor
                          </Button>
                        </Inline>
                      </Stack>
                    </section>
                  );
                })()}
              </Stack>
            ) : bootstrap ? (
              <ResultGrid result={bootstrap.capabilities} label="Server capabilities" />
            ) : loadingBootstrap ? (
              <Inline gap="sm" align="center" className="nss-loading" role="status">
                <Spinner size="sm" />
                <Text size="sm" variant="muted">Loading server capabilities…</Text>
              </Inline>
            ) : (
              <EmptyState size="sm" density="compact" title="Capabilities unavailable" />
            )}
          </CardBody>
        </Card>
        ) : null}
      </div>

      {pendingRun ? (
        <ConfirmDialog
          open
          onClose={() => setPendingRun(null)}
          onConfirm={confirmPendingRun}
          title={pendingRun.blockedReadOnly && !pendingRun.analysis.destructive
            ? `Read-only mode — run this ${pendingRun.analysis.kind}?`
            : pendingRun.realmScoped && !pendingRun.analysis.destructive
              ? `Realm-wide change — run this ${pendingRun.analysis.kind}?`
              : `Confirm ${pendingRun.analysis.kind}`}
          description={
            <Stack gap="xs">
              {pendingRun.isSelection ? <Text variant="muted">This runs only the selected text.</Text> : null}
              {pendingRun.blockedReadOnly ? (
                <Text>
                  Read-only mode is on{environment ? ` for this ${environment} connection` : ""}. This{" "}
                  {pendingRun.analysis.kind} statement writes data.
                </Text>
              ) : null}
              {pendingRun.realmScoped ? (
                <Text>{realmScopeWarning(pendingRun.analysis.kind, who.realm ?? "", who.database ?? "")}</Text>
              ) : null}
              {pendingRun.analysis.reasons?.map((reason, index) => <Text key={index}>{reason}</Text>)}
            </Stack>
          }
          confirmLabel="Run anyway"
          destructive
        />
      ) : null}

      {pendingScript ? (
        <ConfirmDialog
          open
          onClose={() => setPendingScript(null)}
          onConfirm={confirmPendingScript}
          title={`Confirm script (${pendingScript.statements.length} statements)`}
          description={
            <Stack gap="xs">
              <Text variant="muted">
                {pendingScript.flagged.length} of {pendingScript.statements.length} statements are flagged
                (destructive or realm-wide{readOnlyMode ? ", or a write while read-only mode is on" : ""}).
                The script stops at the first failed or canceled statement.
              </Text>
              {pendingScript.flagged.slice(0, MAX_SCRIPT_CONFIRM_REASONS).map(({ index, reasons }) => (
                <Text key={index}>
                  #{index + 1}: {reasons.length > 0 ? reasons.join(" ") : "Flagged statement."}
                </Text>
              ))}
              {pendingScript.flagged.length > MAX_SCRIPT_CONFIRM_REASONS ? (
                <Text variant="muted">…and {pendingScript.flagged.length - MAX_SCRIPT_CONFIRM_REASONS} more.</Text>
              ) : null}
            </Stack>
          }
          confirmLabel="Run anyway"
          destructive
        />
      ) : null}

      <GrantBuilder
        open={grantBuilderOpen}
        onClose={() => setGrantBuilderOpen(false)}
        onInsert={setSQL}
        tables={allTables}
        users={principalUsers}
        roles={principalRoles}
        principalsError={securityError}
        initial={grantPrefill}
      />
      {securityExplorerOpen ? (
        <SecurityExplorer
          onClose={() => setSecurityExplorerOpen(false)}
          data={security}
          loading={securityLoading}
          error={securityError}
          onRetry={reloadSecurity}
          onRevoke={revokeGrant}
          onNewGrant={() => { setSecurityExplorerOpen(false); openGrantBuilder(); }}
        />
      ) : null}
      {activityExplorerOpen ? (
        <ActivityExplorer
          onClose={() => setActivityExplorerOpen(false)}
          data={activity}
          loading={activityLoading}
          error={activityError}
          onRefresh={loadActivity}
        />
      ) : null}
      {auditExplorerOpen ? (
        <AuditExplorer
          onClose={() => setAuditExplorerOpen(false)}
          data={security}
          loading={securityLoading}
          error={securityError}
          onRefresh={reloadSecurity}
        />
      ) : null}
      {workflowExplorerOpen ? (
        <WorkflowExplorer
          onClose={() => setWorkflowExplorerOpen(false)}
          data={workflows}
          loading={workflowsLoading}
          error={workflowsError}
          onRefresh={loadWorkflows}
        />
      ) : null}
      {migrationExplorerOpen ? (
        <MigrationExplorer
          onClose={() => setMigrationExplorerOpen(false)}
          data={migrations}
          loading={migrationsLoading}
          error={migrationsError}
          onRefresh={loadMigrations}
        />
      ) : null}
      {schemaDiagramOpen ? (
        <SchemaDiagramExplorer
          onClose={() => setSchemaDiagramOpen(false)}
          data={schemaGraph}
          loading={schemaGraphLoading}
          error={schemaGraphError}
          onRefresh={loadSchemaGraph}
        />
      ) : null}
      {paletteOpen ? (
        <CommandPalette commands={commands} onClose={() => setPaletteOpen(false)} />
      ) : null}
      {objectSearchOpen ? (
        <ObjectSearch
          onClose={() => setObjectSearchOpen(false)}
          tables={allTables}
          workflows={namesFromResult(workflows?.workflows ?? null, "name")}
          workflowsLoading={workflowsLoading && !workflows}
          onOpenTable={selectTable}
          onOpenWorkflow={openWorkflowExplorer}
        />
      ) : null}
      {fullTextExplorerOpen ? (
        <FullTextExplorer
          onClose={() => setFullTextExplorerOpen(false)}
          onInsert={setSQL}
          onRun={runFullTextSearch}
          tables={allTables}
          initialTable={selectedTable}
          initialDetail={detail}
          loadTable={loadFullTextTable}
          busy={running}
        />
      ) : null}
      {vectorExplorerOpen ? (
        <VectorExplorer
          onClose={() => setVectorExplorerOpen(false)}
          onInsert={setSQL}
          onRun={runVectorSearch}
          tables={allTables}
          initialTable={selectedTable}
          initialDetail={detail}
          loadTable={loadFullTextTable}
          busy={running}
        />
      ) : null}
      {hybridExplorerOpen ? (
        <HybridExplorer
          onClose={() => setHybridExplorerOpen(false)}
          onInsert={setSQL}
          onRun={runHybridSearch}
          onExplain={runHybridExplain}
          tables={allTables}
          initialTable={selectedTable}
          initialDetail={detail}
          loadTable={loadFullTextTable}
          busy={running}
        />
      ) : null}
      {geoExplorerOpen ? (
        <GeoExplorer
          onClose={() => setGeoExplorerOpen(false)}
          onInsert={setSQL}
          onRun={runGeoSearch}
          tables={allTables}
          initialTable={selectedTable}
          initialDetail={detail}
          loadTable={loadFullTextTable}
          busy={running}
        />
      ) : null}
      {dataGeneratorOpen ? (
        <DataGeneratorExplorer
          onClose={() => setDataGeneratorOpen(false)}
          onInsert={setSQL}
          tables={allTables}
          initialTable={selectedTable}
          initialDetail={detail}
          loadTable={loadFullTextTable}
        />
      ) : null}
      {importOpen ? (
        <ImportExplorer
          onClose={() => setImportOpen(false)}
          onInsert={setSQL}
          tables={allTables}
          initialTable={selectedTable}
          initialDetail={detail}
          loadTable={loadFullTextTable}
        />
      ) : null}
      {vectorImportOpen ? (
        <VectorImportExplorer
          onClose={() => setVectorImportOpen(false)}
          onInsert={setSQL}
          tables={allTables}
          initialTable={selectedTable}
          initialDetail={detail}
          loadTable={loadFullTextTable}
        />
      ) : null}
      {dmlBuilderOpen ? (
        <DMLBuilderExplorer
          onClose={() => setDmlBuilderOpen(false)}
          onInsert={setSQL}
          tables={allTables}
          initialTable={selectedTable}
          initialDetail={detail}
          loadTable={loadFullTextTable}
        />
      ) : null}
      {schemaDesignerOpen ? (
        <SchemaDesignerExplorer
          onClose={() => setSchemaDesignerOpen(false)}
          onInsert={setSQL}
          tables={allTables}
          initialTable={selectedTable}
          initialDetail={detail}
          loadTable={loadFullTextTable}
          initialMode={schemaDesignerMode}
        />
      ) : null}
    </Stack>
  );
}
