import { useEffect, useMemo, useRef, useState, type UIEvent } from "react";
import { Badge, Button, EmptyState, Inline, Text } from "@bzync/rui";
import type { StudioResultSet } from "../ops/api";
import { CellInspector, type InspectedCell, type JSONExplorerContext } from "./CellInspector";
import { ExplainProfiler } from "./ExplainProfiler";
import { ExplainTree } from "./ExplainTree";
import { PlanComparison } from "./PlanComparison";
import {
  buildExplainProfile,
  captureExplainPlan,
  compactCellValue,
  downloadResult,
  explainAnalyzed,
  isInspectableStudioType,
  parseExplainPlan,
  tabularText,
  writeClipboard,
  type ExplainNode,
  type ExplainPlanSnapshot,
  type ExplainProfile,
  type ExportFormat,
  type RankResultContext,
} from "./resultTools";

const ROW_HEIGHT = 34;
const OVERSCAN_ROWS = 8;

type ResultGridProps = {
  result: StudioResultSet;
  label: string;
  streaming?: boolean;
  tools?: boolean;
  actionsDisabled?: boolean;
  exportPrefix?: string;
  jsonContext?: JSONExplorerContext;
  rankContext?: RankResultContext | null;
  planBaseline?: ExplainPlanSnapshot | null;
  onPinPlan?: (snapshot: ExplainPlanSnapshot) => void;
  onClearPlanBaseline?: () => void;
};

function message(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

export function ResultGrid({
  result,
  label,
  streaming = false,
  tools = false,
  actionsDisabled = false,
  exportPrefix = "nextsql-result",
  jsonContext,
  rankContext,
  planBaseline,
  onPinPlan,
  onClearPlanBaseline,
}: ResultGridProps) {
  const scroll = useRef<HTMLDivElement>(null);
  const [scrollTop, setScrollTop] = useState(0);
  const [viewportHeight, setViewportHeight] = useState(520);
  const [selectedRows, setSelectedRows] = useState<Set<number>>(() => new Set());
  const [activeCell, setActiveCell] = useState<InspectedCell | null>(null);
  const [inspectorOpen, setInspectorOpen] = useState(false);
  const [actionStatus, setActionStatus] = useState<string | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);
  const [view, setView] = useState<"table" | "plan" | "profile" | "compare">("plan");
  const [profile, setProfile] = useState<ExplainProfile | null>(null);
  const [comparisonPlan, setComparisonPlan] = useState<ExplainPlanSnapshot | null>(null);

  const explainNodes: ExplainNode[] | null = useMemo(() => {
    try {
      return parseExplainPlan(result);
    } catch {
      return null;
    }
  }, [result]);

  useEffect(() => {
    const element = scroll.current;
    if (!element) return;
    const measure = () => setViewportHeight(element.clientHeight || 520);
    measure();
    const observer = new ResizeObserver(measure);
    observer.observe(element);
    return () => observer.disconnect();
  }, []);

  useEffect(() => {
    setSelectedRows(new Set());
    setActiveCell(null);
    setInspectorOpen(false);
    setView("plan");
    setProfile(null);
    setComparisonPlan(null);
    setActionStatus(null);
    setActionError(null);
  }, [result.rows]);

  const window = useMemo(() => {
    const visible = Math.ceil(viewportHeight / ROW_HEIGHT);
    const start = Math.max(0, Math.floor(scrollTop / ROW_HEIGHT) - OVERSCAN_ROWS);
    const end = Math.min(result.rows.length, start + visible + OVERSCAN_ROWS * 2);
    return { start, end, rows: result.rows.slice(start, end) };
  }, [result.rows, scrollTop, viewportHeight]);

  const onScroll = (event: UIEvent<HTMLDivElement>) => setScrollTop(event.currentTarget.scrollTop);
  const disabled = streaming || actionsDisabled;
  const actionIndexes = selectedRows.size ? selectedRows : undefined;

  const toggleRow = (index: number) => {
    setSelectedRows((current) => {
      const next = new Set(current);
      if (next.has(index)) next.delete(index);
      else next.add(index);
      return next;
    });
    setActionStatus(null);
    setActionError(null);
  };

  const selectAll = () => {
    setSelectedRows(new Set(result.rows.map((_, index) => index)));
    setActionStatus(null);
    setActionError(null);
  };

  const copyCell = async () => {
    if (!activeCell || disabled) return;
    setActionError(null);
    try {
      await writeClipboard(activeCell.value ?? "NULL");
      setActionStatus(`Copied row ${activeCell.rowIndex + 1}, ${activeCell.column}.`);
    } catch (error) {
      setActionError(message(error));
    }
  };

  const copyRows = async () => {
    if (disabled) return;
    setActionError(null);
    try {
      const count = selectedRows.size || result.rows.length;
      await writeClipboard(tabularText(result, actionIndexes));
      setActionStatus(`Copied ${count.toLocaleString()} ${count === 1 ? "row" : "rows"} as safe TSV.`);
    } catch (error) {
      setActionError(message(error));
    }
  };

  const exportRows = (format: ExportFormat) => {
    if (disabled) return;
    setActionError(null);
    try {
      const count = downloadResult(result, format, actionIndexes, exportPrefix);
      setActionStatus(`Exported ${count.toLocaleString()} ${count === 1 ? "row" : "rows"} as ${format.toUpperCase()}.`);
    } catch (error) {
      setActionError(message(error));
    }
  };

  const pinCurrentPlan = () => {
    if (!explainNodes || !onPinPlan || disabled) return;
    setActionError(null);
    try {
      const snapshot = captureExplainPlan(explainNodes);
      onPinPlan(snapshot);
      setActionStatus(`Pinned ${snapshot.nodeCount.toLocaleString()}-operator plan as this tab's baseline.`);
    } catch (error) {
      setActionError(message(error));
    }
  };

  const compareCurrentPlan = () => {
    if (!explainNodes || !planBaseline || disabled) return;
    setActionError(null);
    try {
      setComparisonPlan(captureExplainPlan(explainNodes));
      setView("compare");
      setActionStatus(null);
    } catch (error) {
      setActionError(message(error));
    }
  };

  const profileCurrentPlan = () => {
    if (!explainNodes || disabled) return;
    setActionError(null);
    try {
      setProfile(buildExplainProfile(explainNodes));
      setView("profile");
      setActionStatus(null);
    } catch (error) {
      setActionError(message(error));
    }
  };

  if (result.columns.length === 0) {
    return (
      <EmptyState
        size="sm"
        density="compact"
        title={streaming ? "Statement running" : "Statement completed"}
        description={streaming ? "Waiting for the server…" : result.affected ? `${result.affected} affected` : "No result columns"}
      />
    );
  }
  if (result.rows.length === 0) {
    return <EmptyState size="sm" density="compact" title={streaming ? "Waiting for rows…" : "No rows"} />;
  }

  const topSpacer = window.start * ROW_HEIGHT;
  const bottomSpacer = (result.rows.length - window.end) * ROW_HEIGHT;
  const showRank = rankContext != null;
  const rankLabel = rankContext?.kind === "hybrid" ? "Hybrid rank" : "BM25 rank";
  const columnSpan = result.columns.length + (tools ? 1 : 0) + (showRank ? 1 : 0);
  const activeInspectable = activeCell ? isInspectableStudioType(activeCell.type) : false;

  return (
    <div className="nss-result-wrap">
      {tools ? (
        <div className="nss-result-tools" aria-label="Result actions">
          <Inline gap="xs" align="center" wrap>
            <Button variant="outline" size="sm" disabled={disabled || !activeCell} onClick={() => void copyCell()}>Copy cell</Button>
            <Button variant="outline" size="sm" disabled={disabled} onClick={() => void copyRows()}>
              {selectedRows.size ? "Copy selected" : "Copy all rows"}
            </Button>
            <Button variant="outline" size="sm" disabled={disabled || !activeInspectable} onClick={() => setInspectorOpen(true)}>Inspect native value</Button>
            <Button variant="outline" size="sm" disabled={disabled} onClick={() => exportRows("csv")}>Export CSV</Button>
            <Button variant="outline" size="sm" disabled={disabled} onClick={() => exportRows("json")}>Export JSON</Button>
          </Inline>
          <Inline gap="xs" align="center" wrap>
            <Text size="sm" variant="muted">
              {selectedRows.size
                ? `${selectedRows.size.toLocaleString()} selected`
                : `Actions use all ${result.rows.length.toLocaleString()} loaded rows`}
            </Text>
            {selectedRows.size === result.rows.length ? (
              <Button variant="ghost" size="sm" onClick={() => setSelectedRows(new Set())}>Clear selection</Button>
            ) : (
              <Button variant="ghost" size="sm" onClick={selectAll}>Select all loaded</Button>
            )}
          </Inline>
        </div>
      ) : null}
      {tools && (actionStatus || actionError) ? (
        <Text size="sm" variant={actionError ? "danger" : "success"} role={actionError ? "alert" : "status"}>
          {actionError ?? actionStatus}
        </Text>
      ) : null}
      {showRank && rankContext ? (
        <Inline gap="sm" align="center" wrap className="nss-fulltext-rank-note">
          <Badge variant="info">{rankContext.kind === "hybrid" ? "Hybrid ranked" : "BM25 ranked"}</Badge>
          <Text size="sm" variant="muted">
            {rankContext.kind === "hybrid"
              ? "Rank is the server result order (#1 is highest). NextSQL fuses BM25 and vector ranks with reciprocal rank fusion; no single fused score is exposed by the current result schema."
              : "Rank is the server result order (#1 is highest). Numeric BM25 scores are not exposed by the current result schema."}
            {rankContext.indexName ? ` Candidate index: ${rankContext.indexName}; use EXPLAIN to verify the chosen access path.` : " The optimizer may use a matching index or sequential fallback."}
          </Text>
        </Inline>
      ) : null}
      {explainNodes && explainNodes.length ? (
        <>
          <Inline gap="xs" align="center" role="group" aria-label="EXPLAIN view">
            <Button
              variant={view === "plan" ? "primary" : "outline"}
              size="sm"
              aria-pressed={view === "plan"}
              onClick={() => setView("plan")}
            >
              Plan
            </Button>
            <Button
              variant={view === "table" ? "primary" : "outline"}
              size="sm"
              aria-pressed={view === "table"}
              onClick={() => setView("table")}
            >
              Table
            </Button>
            {explainAnalyzed(explainNodes) ? (
              <Button
                variant={view === "profile" ? "primary" : "outline"}
                size="sm"
                aria-pressed={view === "profile"}
                onClick={profileCurrentPlan}
                disabled={disabled}
              >
                Profile
              </Button>
            ) : null}
            {planBaseline ? (
              <Button
                variant={view === "compare" ? "primary" : "outline"}
                size="sm"
                aria-pressed={view === "compare"}
                onClick={compareCurrentPlan}
                disabled={disabled}
              >
                Compare
              </Button>
            ) : null}
          </Inline>
          {onPinPlan ? (
            <Inline gap="xs" align="center" role="group" aria-label="Plan comparison actions">
              <Button variant="outline" size="sm" onClick={pinCurrentPlan} disabled={disabled}>
                {planBaseline ? "Replace baseline" : "Pin as baseline"}
              </Button>
              {planBaseline && onClearPlanBaseline ? (
                <Button
                  variant="ghost"
                  size="sm"
                  onClick={() => {
                    onClearPlanBaseline();
                    setComparisonPlan(null);
                    setView("plan");
                    setActionStatus("Cleared this tab's plan baseline.");
                    setActionError(null);
                  }}
                >
                  Clear baseline
                </Button>
              ) : null}
            </Inline>
          ) : null}
        </>
      ) : null}
      {explainNodes && explainNodes.length && view === "plan" ? (
        <ExplainTree nodes={explainNodes} analyzed={explainAnalyzed(explainNodes)} />
      ) : view === "profile" && profile ? (
        <ExplainProfiler profile={profile} />
      ) : view === "compare" && planBaseline && comparisonPlan ? (
        <PlanComparison baseline={planBaseline} current={comparisonPlan} />
      ) : (
      <>
      <div
        ref={scroll}
        className="nss-virtual-scroll"
        tabIndex={0}
        aria-label={`${label} scroll area`}
        aria-busy={streaming || undefined}
        onScroll={onScroll}
      >
        <table
          className="nss-virtual-table"
          aria-label={label}
          aria-rowcount={result.rows.length + 1}
          aria-colcount={columnSpan}
        >
          <thead>
          <tr>
            {tools ? <th scope="col" className="nss-select-column"><span className="nss-sr-only">Select row</span></th> : null}
            {showRank ? (
              <th scope="col" className="nss-rank-column">
                <span className="nss-column-heading">{rankLabel}</span>
                <span className="nss-column-type">ORDER</span>
              </th>
            ) : null}
            {result.columns.map((column, index) => (
              <th key={`${column}-${index}`} scope="col">
                <span className="nss-column-heading">{column}</span>
                {result.column_types[index] ? (
                  <span className="nss-column-type">{result.column_types[index]}</span>
                ) : null}
              </th>
            ))}
          </tr>
          </thead>
          <tbody>
          {topSpacer > 0 ? (
            <tr aria-hidden="true" className="nss-virtual-spacer">
              <td colSpan={columnSpan} style={{ height: topSpacer }} />
            </tr>
          ) : null}
          {window.rows.map((row, rowIndex) => {
            const absoluteIndex = window.start + rowIndex;
            return (
            <tr key={absoluteIndex} aria-rowindex={absoluteIndex + 2} aria-selected={tools ? selectedRows.has(absoluteIndex) : undefined}>
              {tools ? (
                <td className="nss-select-column">
                  <input
                    type="checkbox"
                    checked={selectedRows.has(absoluteIndex)}
                    disabled={disabled}
                    onChange={() => toggleRow(absoluteIndex)}
                    aria-label={`Select result row ${absoluteIndex + 1}`}
                  />
                </td>
              ) : null}
              {showRank ? <td className="nss-rank-column">#{absoluteIndex + 1}</td> : null}
              {result.columns.map((column, columnIndex) => {
                const cell = row[columnIndex] ?? null;
                const type = result.column_types[columnIndex] ?? "UNKNOWN";
                const active = activeCell?.rowIndex === absoluteIndex && activeCell.columnIndex === columnIndex;
                return (
                  <td key={columnIndex} className="nss-result-cell">
                    {tools ? (
                      <button
                        type="button"
                        className="nss-cell-button"
                        aria-pressed={active}
                        title={cell ?? "NULL"}
                        onClick={() => {
                          setActiveCell({ rowIndex: absoluteIndex, columnIndex, column, type, value: cell });
                          setActionStatus(null);
                          setActionError(null);
                        }}
                      >
                        {cell === null ? (
                          <Text as="span" variant="muted" className="nss-null">NULL</Text>
                        ) : (
                          compactCellValue(type, cell)
                        )}
                      </button>
                    ) : cell === null ? (
                      <Text as="span" variant="muted" className="nss-null">NULL</Text>
                    ) : (
                      <span title={cell}>{compactCellValue(type, cell)}</span>
                    )}
                  </td>
                );
              })}
            </tr>
            );
          })}
          {bottomSpacer > 0 ? (
            <tr aria-hidden="true" className="nss-virtual-spacer">
              <td colSpan={columnSpan} style={{ height: bottomSpacer }} />
            </tr>
          ) : null}
          </tbody>
        </table>
      </div>
      <Text variant="muted" size="sm" role="status">
        {streaming
          ? `Streaming ${result.rows.length.toLocaleString()} rows in bounded batches; only visible rows are mounted.`
          : `${result.rows.length.toLocaleString()} rows; only visible rows are mounted.`}
      </Text>
      </>
      )}
      {tools ? (
        <Text variant="muted" size="sm">
          CSV/TSV neutralizes spreadsheet formulas and writes SQL NULL as \\N. JSON preserves raw strings, types, duplicate column names, and NULL exactly.
        </Text>
      ) : null}
      <CellInspector
        key={activeCell ? `${activeCell.rowIndex}-${activeCell.columnIndex}` : "none"}
        cell={activeCell}
        open={inspectorOpen}
        onClose={() => setInspectorOpen(false)}
        jsonContext={jsonContext}
      />
    </div>
  );
}
