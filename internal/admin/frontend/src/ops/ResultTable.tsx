import { useMemo, useState } from "react";
import {
  Badge,
  Button,
  EmptyState,
  Inline,
  Pagination,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
  Text,
} from "@bzync/rui";
import type { ResultSet } from "./api";
import { Icon } from "../shared/icons";
import { useUserPreferences } from "./userPreferences";

export interface ResultTableProps {
  result: ResultSet;
  empty?: string;
  label?: string;
  defaultPageSize?: number;
  pageSizeOptions?: number[];
  showSearch?: boolean;
  showExport?: boolean;
}

export function ResultTable({
  result,
  empty,
  label = "Result table",
  defaultPageSize,
  pageSizeOptions = [10, 25, 50, 100],
  showSearch = true,
  showExport = true,
}: ResultTableProps) {
  const { preferences } = useUserPreferences();
  const initialPageSize = defaultPageSize ?? preferences.defaultPageSize ?? 10;
  const [pageSize, setPageSize] = useState<number>(initialPageSize);
  const [page, setPage] = useState<number>(1);
  const [searchQuery, setSearchQuery] = useState<string>("");
  const [sortCol, setSortCol] = useState<number | null>(null);
  const [sortAsc, setSortAsc] = useState<boolean>(true);
  const [copied, setCopied] = useState<boolean>(false);

  const totalRawRows = result?.rows?.length ?? 0;

  const filteredRows = useMemo(() => {
    let rows = result?.rows || [];
    if (searchQuery.trim()) {
      const q = searchQuery.trim().toLowerCase();
      rows = rows.filter((row) =>
        row.some((cell) => cell !== null && String(cell).toLowerCase().includes(q))
      );
    }
    if (sortCol !== null && result?.columns && sortCol < result.columns.length) {
      const idx = sortCol;
      rows = [...rows].sort((a, b) => {
        const va = a[idx];
        const vb = b[idx];
        if (va === null && vb === null) return 0;
        if (va === null) return 1;
        if (vb === null) return -1;
        const na = Number(va);
        const nb = Number(vb);
        if (!isNaN(na) && !isNaN(nb) && typeof va !== "boolean" && typeof vb !== "boolean") {
          return sortAsc ? na - nb : nb - na;
        }
        const sa = String(va);
        const sb = String(vb);
        return sortAsc ? sa.localeCompare(sb) : sb.localeCompare(sa);
      });
    }
    return rows;
  }, [result?.rows, result?.columns, searchQuery, sortCol, sortAsc]);

  const totalPages = Math.max(1, Math.ceil(filteredRows.length / pageSize));
  const safePage = Math.min(page, totalPages);
  const startIndex = (safePage - 1) * pageSize;
  const endIndex = Math.min(startIndex + pageSize, filteredRows.length);
  const paginatedRows = filteredRows.slice(startIndex, endIndex);

  const handleSort = (index: number) => {
    if (sortCol === index) {
      if (sortAsc) {
        setSortAsc(false);
      } else {
        setSortCol(null);
        setSortAsc(true);
      }
    } else {
      setSortCol(index);
      setSortAsc(true);
    }
  };

  const copyAsCSV = async () => {
    if (!result?.columns || !filteredRows.length) return;
    const header = result.columns.map((c) => `"${c.replace(/"/g, '""')}"`).join(",");
    const body = filteredRows
      .map((row) =>
        row
          .map((cell) => (cell === null ? "" : `"${String(cell).replace(/"/g, '""')}"`))
          .join(",")
      )
      .join("\n");
    const csv = `${header}\n${body}`;
    try {
      await navigator.clipboard.writeText(csv);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    } catch {
      // clipboard write denied
    }
  };

  if (!result || !result.columns || result.columns.length === 0) {
    return <EmptyState size="sm" density="compact" icon={<Icon name="table" />} title={empty ?? "No data"} />;
  }
  if (!result.rows || result.rows.length === 0) {
    return <EmptyState size="sm" density="compact" icon={<Icon name="table" />} title={empty ?? "No rows"} />;
  }

  const shouldShowToolbar = (showSearch && totalRawRows > 5) || showExport;

  return (
    <div className="nsm-table-container">
      {shouldShowToolbar ? (
        <div className="nsm-table-toolbar">
          <Inline gap="sm" align="center" justify="between" wrap>
            {showSearch && totalRawRows > 5 ? (
              <div className="nsm-table-search">
                <Icon name="search" size={13} className="nsm-table-search-icon" />
                <input
                  type="search"
                  value={searchQuery}
                  onChange={(e) => {
                    setSearchQuery(e.target.value);
                    setPage(1);
                  }}
                  placeholder="Filter rows…"
                  className="nsm-table-search-input"
                  aria-label="Filter rows in table"
                />
                {searchQuery ? (
                  <button
                    type="button"
                    onClick={() => {
                      setSearchQuery("");
                      setPage(1);
                    }}
                    className="nsm-table-search-clear"
                    aria-label="Clear filter"
                  >
                    ×
                  </button>
                ) : null}
              </div>
            ) : <div />}

            <Inline gap="xs" align="center">
              {searchQuery ? (
                <Badge variant="muted" size="sm">
                  {filteredRows.length} of {totalRawRows} matched
                </Badge>
              ) : null}
              {showExport && totalRawRows > 0 ? (
                <Button
                  variant="ghost"
                  size="sm"
                  icon={<Icon name={copied ? "check" : "copy"} size={13} />}
                  onClick={copyAsCSV}
                  title="Copy rows as CSV"
                >
                  {copied ? "Copied CSV" : "Export CSV"}
                </Button>
              ) : null}
            </Inline>
          </Inline>
        </div>
      ) : null}

      <div className="nsm-result-table-scroll" role="region" aria-label={label} tabIndex={0}>
        <Table density={preferences.density} scrollAreaClassName="nsm-result-table-inner">
          <TableHeader>
            <TableRow>
              {result.columns.map((colName, idx) => {
                const isSorted = sortCol === idx;
                return (
                  <TableHead
                    key={colName}
                    onClick={() => handleSort(idx)}
                    className="nsm-th-sortable"
                    title={`Click to sort by ${colName}`}
                  >
                    <span className="inline-flex items-center gap-1.5 cursor-pointer select-none">
                      <span>{colName}</span>
                      {isSorted ? (
                        <Icon
                          name={sortAsc ? "chevron-up" : "chevron-down"}
                          size={12}
                          className="text-accent-500 font-bold shrink-0"
                        />
                      ) : (
                        <span className="nsm-sort-placeholder opacity-0 group-hover:opacity-40">↕</span>
                      )}
                    </span>
                  </TableHead>
                );
              })}
            </TableRow>
          </TableHeader>
          <TableBody>
            {paginatedRows.length === 0 ? (
              <TableRow>
                <TableCell colSpan={result.columns.length} className="text-center py-6 text-muted-foreground">
                  No matching rows found
                </TableCell>
              </TableRow>
            ) : (
              paginatedRows.map((row, i) => (
                <TableRow key={startIndex + i}>
                  {row.map((cell, j) => (
                    <TableCell key={j}>
                      {cell === null ? (
                        <Text as="span" variant="muted" className="font-mono text-xs opacity-60">
                          NULL
                        </Text>
                      ) : (
                        cell
                      )}
                    </TableCell>
                  ))}
                </TableRow>
              ))
            )}
          </TableBody>
        </Table>
      </div>

      <div className="nsm-table-footer">
        <Inline gap="md" align="center" justify="between" wrap>
          <Inline gap="sm" align="center">
            <span className="text-xs text-muted-foreground">
              {filteredRows.length === 0 ? (
                "0 rows"
              ) : (
                <>
                  Showing <strong className="text-foreground font-semibold">{startIndex + 1}</strong> to{" "}
                  <strong className="text-foreground font-semibold">{endIndex}</strong> of{" "}
                  <strong className="text-foreground font-semibold">{filteredRows.length}</strong>
                  {totalRawRows !== filteredRows.length ? ` (filtered from ${totalRawRows})` : " rows"}
                </>
              )}
            </span>

            {filteredRows.length > 10 ? (
              <div className="nsm-page-size-selector">
                <span className="text-xs text-muted-foreground mr-1.5">Per page:</span>
                <Inline gap="xs" align="center">
                  {pageSizeOptions.map((opt) => (
                    <button
                      key={opt}
                      type="button"
                      className={`nsm-page-size-btn${pageSize === opt ? " nsm-page-size-btn--active" : ""}`}
                      onClick={() => {
                        setPageSize(opt);
                        setPage(1);
                      }}
                      aria-label={`${opt} rows per page`}
                    >
                      {opt}
                    </button>
                  ))}
                </Inline>
              </div>
            ) : null}
          </Inline>

          <Pagination
            page={safePage}
            totalPages={totalPages}
            onPageChange={(nextPage) => setPage(nextPage)}
            className="nsm-pagination"
          />
        </Inline>
      </div>
    </div>
  );
}
