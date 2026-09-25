import { useCallback, useMemo, useRef, useState } from "react";
import {
  Alert,
  Badge,
  Button,
  Card,
  CardBody,
  Inline,
  Modal,
  ModalBody,
  ModalFooter,
  ModalHeader,
  ModalTitle,
  Radio,
  RadioGroup,
  Stack,
  Text,
} from "@bzync/rui";
import { Icon } from "../shared/icons";
import {
  compareBenchmarkRuns,
  formatDeltaPct,
  formatMicroseconds,
  generateBenchmarkComparisonMarkdown,
  parseBenchmarkReport,
  SAMPLE_BENCH_BASELINE,
  SAMPLE_BENCH_CANDIDATE,
  type BenchmarkComparisonResult,
  type BenchmarkHardware,
  type BenchmarkRunReport,
} from "./resultTools";

type ViewMode = "single" | "comparison";
type Slot = "baseline" | "candidate";
type StatusFilter = "all" | "regressed" | "improved";

export function BenchmarkViewer({
  onClose,
}: {
  onClose: () => void;
}) {
  const [viewMode, setViewMode] = useState<ViewMode>("comparison");
  const [activeSlot, setActiveSlot] = useState<Slot>("candidate");
  const [baselineRun, setBaselineRun] = useState<BenchmarkRunReport>(SAMPLE_BENCH_BASELINE);
  const [candidateRun, setCandidateRun] = useState<BenchmarkRunReport>(SAMPLE_BENCH_CANDIDATE);
  const [filterText, setFilterText] = useState<string>("");
  const [statusFilter, setStatusFilter] = useState<StatusFilter>("all");
  const [expandedWorkload, setExpandedWorkload] = useState<string | null>(null);
  const [copied, setCopied] = useState<boolean>(false);

  // Import JSON Modal State
  const [showImportModal, setShowImportModal] = useState<boolean>(false);
  const [importTargetSlot, setImportTargetSlot] = useState<Slot>("candidate");
  const [importJsonText, setImportJsonText] = useState<string>("");
  const [importError, setImportError] = useState<string | null>(null);
  const fileInputRef = useRef<HTMLInputElement | null>(null);

  // Load sample fixtures
  const handleLoadSamples = useCallback(() => {
    setBaselineRun(SAMPLE_BENCH_BASELINE);
    setCandidateRun(SAMPLE_BENCH_CANDIDATE);
  }, []);

  // Swap baseline and candidate
  const handleSwap = useCallback(() => {
    const prevBase = baselineRun;
    const prevCand = candidateRun;
    setBaselineRun(prevCand);
    setCandidateRun(prevBase);
  }, [baselineRun, candidateRun]);

  // Comparison result memo
  const comparison = useMemo<BenchmarkComparisonResult>(() => {
    return compareBenchmarkRuns(baselineRun, candidateRun);
  }, [baselineRun, candidateRun]);

  // Filtered comparison items
  const filteredComparisonItems = useMemo(() => {
    return comparison.items.filter((item) => {
      if (filterText.trim()) {
        const query = filterText.toLowerCase();
        if (!item.name.toLowerCase().includes(query)) {
          return false;
        }
      }
      if (statusFilter === "regressed" && item.status !== "regressed") {
        return false;
      }
      if (statusFilter === "improved" && item.status !== "improved") {
        return false;
      }
      return true;
    });
  }, [comparison.items, filterText, statusFilter]);

  // Single run active report
  const activeSingleRun = activeSlot === "baseline" ? baselineRun : candidateRun;

  // Filtered single run items
  const filteredSingleItems = useMemo(() => {
    if (!filterText.trim()) return activeSingleRun.items;
    const query = filterText.toLowerCase();
    return activeSingleRun.items.filter(
      (item) => item.name.toLowerCase().includes(query) || (item.workload?.toLowerCase().includes(query) ?? false),
    );
  }, [activeSingleRun.items, filterText]);

  // Single run summary metrics
  const singleRunMetrics = useMemo(() => {
    const items = activeSingleRun.items;
    if (items.length === 0) return { totalOps: 0, avgQps: 0, avgP50: 0, avgP99: 0 };
    let totalOps = 0;
    let sumQps = 0;
    let sumP50 = 0;
    let sumP99 = 0;
    for (const it of items) {
      totalOps += it.ops || 0;
      sumQps += it.qps || 0;
      sumP50 += it.p50_us || 0;
      sumP99 += it.p99_us || 0;
    }
    return {
      totalOps,
      avgQps: Math.round(sumQps / items.length),
      avgP50: Math.round(sumP50 / items.length),
      avgP99: Math.round(sumP99 / items.length),
    };
  }, [activeSingleRun.items]);

  // Copy Markdown Report
  const handleCopyMarkdown = useCallback(() => {
    const md = generateBenchmarkComparisonMarkdown(baselineRun, candidateRun, comparison);
    navigator.clipboard.writeText(md).then(() => {
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    });
  }, [baselineRun, candidateRun, comparison]);

  // Process JSON import
  const handleApplyImport = useCallback(() => {
    if (!importJsonText.trim()) {
      setImportError("Please provide benchmark JSON content to import.");
      return;
    }
    const { report, error } = parseBenchmarkReport(importJsonText);
    if (error || !report) {
      setImportError(error || "Invalid benchmark JSON format.");
      return;
    }
    if (importTargetSlot === "baseline") {
      setBaselineRun(report);
    } else {
      setCandidateRun(report);
    }
    setShowImportModal(false);
    setImportJsonText("");
    setImportError(null);
  }, [importJsonText, importTargetSlot]);

  // File upload change
  const handleFileUpload = useCallback((e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    if (!file) return;
    const reader = new FileReader();
    reader.onload = (event) => {
      const content = event.target?.result as string;
      if (content) {
        setImportJsonText(content);
        setImportError(null);
      }
    };
    reader.onerror = () => {
      setImportError("Failed to read file.");
    };
    reader.readAsText(file);
  }, []);

  return (
    <>
      <Modal open onClose={onClose} size="lg" ariaLabel="Benchmark result viewer and run comparison" scrollable>
        <ModalHeader>
          <ModalTitle>Benchmark Viewer & Run Comparison</ModalTitle>
        </ModalHeader>
        <ModalBody scrollable>
          <Stack gap="md">
            <Alert variant="info" title="Official NextSQL Benchmark Rigor">
              NextSQL official benchmarks (<code>nextsql-bench</code>) always run with write-ahead logging (WAL),
              fsync durability, production encryption, MVCC, and checksums enabled. Compare latency percentiles (P50, P99),
              throughput (QPS/TPS), and vector recall across runs to detect regressions before deploying.
            </Alert>

            {/* Top Controls Toolbar */}
            <div className="nss-bench-controls-card">
              <Stack gap="sm">
                <Inline gap="md" align="center" justify="between" wrap>
                  <div className="nss-bench-tablist" role="tablist" aria-label="View mode">
                    <Button
                      size="sm"
                      variant={viewMode === "comparison" ? "primary" : "outline"}
                      role="tab"
                      aria-selected={viewMode === "comparison"}
                      onClick={() => setViewMode("comparison")}
                    >
                      Run Comparison
                    </Button>
                    <Button
                      size="sm"
                      variant={viewMode === "single" ? "primary" : "outline"}
                      role="tab"
                      aria-selected={viewMode === "single"}
                      onClick={() => setViewMode("single")}
                    >
                      Single Run View
                    </Button>
                  </div>

                  <Inline gap="xs" align="center" wrap>
                    <Button
                      size="sm"
                      variant="outline"
                      icon={<Icon name="refresh" size={14} />}
                      onClick={handleLoadSamples}
                      title="Load sample baseline and candidate fixtures"
                    >
                      Load Samples
                    </Button>
                    <Button
                      size="sm"
                      variant="outline"
                      icon={<Icon name="download" size={14} />}
                      onClick={() => {
                        setImportError(null);
                        setShowImportModal(true);
                      }}
                      title="Import nextsql-bench JSON report"
                    >
                      Import JSON…
                    </Button>
                  </Inline>
                </Inline>

                {/* Secondary bar: Mode-specific selectors & filter */}
                <Inline gap="md" align="center" justify="between" wrap>
                  {viewMode === "comparison" ? (
                    <Inline gap="sm" align="center" wrap>
                      <Button
                        size="sm"
                        variant="outline"
                        icon={<Icon name="refresh" size={14} />}
                        onClick={handleSwap}
                        title="Swap baseline and candidate runs"
                        aria-label="Swap baseline and candidate runs"
                      >
                        Swap (⇄)
                      </Button>
                      <RadioGroup
                        label="Status Filter"
                        orientation="horizontal"
                        value={statusFilter}
                        onChange={(val) => setStatusFilter(val as StatusFilter)}
                      >
                        <Radio value="all" label="All" />
                        <Radio value="regressed" label="Regressions" />
                        <Radio value="improved" label="Improvements" />
                      </RadioGroup>
                    </Inline>
                  ) : (
                    <RadioGroup
                      label="Active Run"
                      orientation="horizontal"
                      value={activeSlot}
                      onChange={(val) => setActiveSlot(val as Slot)}
                    >
                      <Radio value="candidate" label={`Candidate: ${candidateRun.title || candidateRun.suite}`} />
                      <Radio value="baseline" label={`Baseline: ${baselineRun.title || baselineRun.suite}`} />
                    </RadioGroup>
                  )}

                  <div className="nss-bench-filter-input">
                    <input
                      type="text"
                      className="nss-bench-search-box"
                      placeholder="Filter workloads by name…"
                      aria-label="Filter workloads by name"
                      value={filterText}
                      onChange={(e) => setFilterText(e.target.value)}
                    />
                  </div>
                </Inline>
              </Stack>
            </div>

            {/* TAB: Run Comparison */}
            {viewMode === "comparison" ? (
              <Stack gap="md">
                {/* Summary Metrics Bar */}
                <div className="nss-bench-summary-bar">
                  <Stack gap="xs">
                    <Inline gap="md" align="center" justify="between" wrap>
                      <Inline gap="sm" align="center" wrap>
                        <Text size="sm" weight="semibold">
                          Comparison Summary:
                        </Text>
                        <Badge variant="success" size="sm">
                          {comparison.summary.improvements} improved
                        </Badge>
                        <Badge
                          variant={comparison.summary.regressions > 0 ? "error" : "muted"}
                          size="sm"
                        >
                          {comparison.summary.regressions} regressed
                        </Badge>
                        <Badge variant="muted" size="sm">
                          {comparison.summary.neutral} neutral
                        </Badge>
                        <Badge
                          variant={comparison.summary.avgQpsDeltaPct >= 0 ? "success" : "error"}
                          size="sm"
                        >
                          Avg QPS Δ: {formatDeltaPct(comparison.summary.avgQpsDeltaPct)}
                        </Badge>
                        <Badge
                          variant={comparison.summary.avgP99DeltaPct <= 0 ? "success" : "error"}
                          size="sm"
                        >
                          Avg P99 Δ: {formatDeltaPct(comparison.summary.avgP99DeltaPct)}
                        </Badge>
                      </Inline>

                      <Button
                        size="sm"
                        variant="outline"
                        icon={<Icon name={copied ? "check" : "copy"} size={14} />}
                        onClick={handleCopyMarkdown}
                        title="Copy GitHub markdown comparison table to clipboard"
                        aria-label="Copy markdown comparison table"
                      >
                        {copied ? "Copied Markdown!" : "Copy Markdown Report"}
                      </Button>
                    </Inline>

                    <Text size="xs" variant="muted">
                      <strong>Baseline:</strong> {baselineRun.title || baselineRun.suite} ({baselineRun.hardware?.version || "unknown"}) vs{" "}
                      <strong>Candidate:</strong> {candidateRun.title || candidateRun.suite} ({candidateRun.hardware?.version || "unknown"})
                    </Text>
                  </Stack>
                </div>

                {/* Comparison Table */}
                <div
                  className="nss-bench-table-container"
                  tabIndex={0}
                  role="region"
                  aria-label="Benchmark Comparison Table"
                >
                  <table className="nss-bench-table">
                    <thead>
                      <tr>
                        <th scope="col">Workload</th>
                        <th scope="col">Baseline QPS</th>
                        <th scope="col">Candidate QPS</th>
                        <th scope="col">Δ QPS</th>
                        <th scope="col">Baseline P50</th>
                        <th scope="col">Candidate P50</th>
                        <th scope="col">Δ P50</th>
                        <th scope="col">Baseline P99</th>
                        <th scope="col">Candidate P99</th>
                        <th scope="col">Δ P99</th>
                        <th scope="col">Recall (10/100)</th>
                        <th scope="col">Status</th>
                      </tr>
                    </thead>
                    <tbody>
                      {filteredComparisonItems.length === 0 ? (
                        <tr>
                          <td colSpan={12} className="nss-bench-empty">
                            No benchmark workloads match the selected filter.
                          </td>
                        </tr>
                      ) : (
                        filteredComparisonItems.map((item) => {
                          const isRegressed = item.status === "regressed";
                          const isImproved = item.status === "improved";

                          return (
                            <tr
                              key={item.name}
                              className={
                                isRegressed
                                  ? "nss-bench-row--regressed"
                                  : isImproved
                                    ? "nss-bench-row--improved"
                                    : ""
                              }
                            >
                              <td>
                                <Stack gap="xs">
                                  <span className="font-medium">{item.name}</span>
                                  {item.baseline?.workload ? (
                                    <Text size="xs" variant="muted">
                                      {item.baseline.workload}
                                    </Text>
                                  ) : null}
                                </Stack>
                              </td>
                              <td>{item.qpsBaseline > 0 ? item.qpsBaseline.toLocaleString() : "-"}</td>
                              <td>{item.qpsCandidate > 0 ? item.qpsCandidate.toLocaleString() : "-"}</td>
                              <td>
                                <span
                                  className={
                                    item.qpsDeltaPct > 5
                                      ? "text-success font-medium"
                                      : item.qpsDeltaPct < -5
                                        ? "text-error font-medium"
                                        : "text-muted"
                                  }
                                >
                                  {formatDeltaPct(item.qpsDeltaPct)}
                                </span>
                              </td>
                              <td>{formatMicroseconds(item.p50BaselineUs)}</td>
                              <td>{formatMicroseconds(item.p50CandidateUs)}</td>
                              <td>
                                <span
                                  className={
                                    item.p50DeltaPct < -5
                                      ? "text-success font-medium"
                                      : item.p50DeltaPct > 5
                                        ? "text-error font-medium"
                                        : "text-muted"
                                  }
                                >
                                  {formatDeltaPct(item.p50DeltaPct)}
                                </span>
                              </td>
                              <td>{formatMicroseconds(item.p99BaselineUs)}</td>
                              <td>{formatMicroseconds(item.p99CandidateUs)}</td>
                              <td>
                                <span
                                  className={
                                    item.p99DeltaPct < -5
                                      ? "text-success font-medium"
                                      : item.p99DeltaPct > 5
                                        ? "text-error font-medium"
                                        : "text-muted"
                                  }
                                >
                                  {formatDeltaPct(item.p99DeltaPct)}
                                </span>
                              </td>
                              <td>
                                {item.recallBaseline !== undefined || item.recallCandidate !== undefined ? (
                                  <Stack gap="xs">
                                    <Text size="xs">
                                      {item.recallBaseline !== undefined ? item.recallBaseline.toFixed(3) : "-"} →{" "}
                                      {item.recallCandidate !== undefined ? item.recallCandidate.toFixed(3) : "-"}
                                    </Text>
                                    {item.recallDelta !== undefined ? (
                                      <span
                                        className={
                                          item.recallDelta < -0.01
                                            ? "text-error font-medium text-xs"
                                            : item.recallDelta > 0.01
                                              ? "text-success font-medium text-xs"
                                              : "text-muted text-xs"
                                        }
                                      >
                                        Δ {(item.recallDelta * 100).toFixed(1)}%
                                      </span>
                                    ) : null}
                                  </Stack>
                                ) : (
                                  "-"
                                )}
                              </td>
                              <td>
                                <Badge
                                  size="sm"
                                  variant={
                                    item.status === "improved"
                                      ? "success"
                                      : item.status === "regressed"
                                        ? "error"
                                        : item.status === "added"
                                          ? "info"
                                          : item.status === "removed"
                                            ? "warning"
                                            : "muted"
                                  }
                                >
                                  {item.status.toUpperCase()}
                                </Badge>
                              </td>
                            </tr>
                          );
                        })
                      )}
                    </tbody>
                  </table>
                </div>

                {/* Environment Comparison Card */}
                <Card variant="bordered" className="nss-bench-env-card">
                  <CardBody>
                    <Stack gap="sm">
                      <Text size="sm" weight="semibold">
                        Hardware & Environment Context
                      </Text>
                      <div className="nss-bench-env-grid">
                        <div className="nss-bench-env-col">
                          <Text size="xs" weight="semibold" variant="muted">
                            BASELINE ENVIRONMENT
                          </Text>
                          <HardwareDetails hw={baselineRun.hardware} />
                        </div>
                        <div className="nss-bench-env-col">
                          <Text size="xs" weight="semibold" variant="muted">
                            CANDIDATE ENVIRONMENT
                          </Text>
                          <HardwareDetails hw={candidateRun.hardware} />
                        </div>
                      </div>
                    </Stack>
                  </CardBody>
                </Card>
              </Stack>
            ) : (
              /* TAB: Single Run View */
              <Stack gap="md">
                {/* Hardware Environment Specs */}
                <Card variant="bordered" className="nss-bench-hw-card">
                  <CardBody>
                    <Stack gap="sm">
                      <Inline gap="sm" align="center" justify="between">
                        <Text size="sm" weight="semibold">
                          Run Details: {activeSingleRun.title || activeSingleRun.suite}
                        </Text>
                        <Badge size="sm" variant="muted">
                          {activeSingleRun.generated_at}
                        </Badge>
                      </Inline>
                      <HardwareDetails hw={activeSingleRun.hardware} />
                    </Stack>
                  </CardBody>
                </Card>

                {/* Summary Metrics */}
                <div className="nss-bench-metrics-row">
                  <div className="nss-bench-metric-card">
                    <Text size="xs" variant="muted">
                      TOTAL WORKLOADS
                    </Text>
                    <Text size="lg" weight="bold">
                      {activeSingleRun.items.length}
                    </Text>
                  </div>
                  <div className="nss-bench-metric-card">
                    <Text size="xs" variant="muted">
                      AVERAGE QPS
                    </Text>
                    <Text size="lg" weight="bold">
                      {singleRunMetrics.avgQps.toLocaleString()}
                    </Text>
                  </div>
                  <div className="nss-bench-metric-card">
                    <Text size="xs" variant="muted">
                      AVG P50 LATENCY
                    </Text>
                    <Text size="lg" weight="bold">
                      {formatMicroseconds(singleRunMetrics.avgP50)}
                    </Text>
                  </div>
                  <div className="nss-bench-metric-card">
                    <Text size="xs" variant="muted">
                      AVG P99 LATENCY
                    </Text>
                    <Text size="lg" weight="bold">
                      {formatMicroseconds(singleRunMetrics.avgP99)}
                    </Text>
                  </div>
                  <div className="nss-bench-metric-card">
                    <Text size="xs" variant="muted">
                      TOTAL OPS
                    </Text>
                    <Text size="lg" weight="bold">
                      {singleRunMetrics.totalOps.toLocaleString()}
                    </Text>
                  </div>
                </div>

                {/* Workload Table */}
                <div
                  className="nss-bench-table-container"
                  tabIndex={0}
                  role="region"
                  aria-label="Benchmark Workload Table"
                >
                  <table className="nss-bench-table">
                    <thead>
                      <tr>
                        <th scope="col">Workload</th>
                        <th scope="col">Ops</th>
                        <th scope="col">QPS</th>
                        <th scope="col">TPS</th>
                        <th scope="col">P50</th>
                        <th scope="col">P95</th>
                        <th scope="col">P99</th>
                        <th scope="col">P99.9</th>
                        <th scope="col">Allocs</th>
                        <th scope="col">Recall (10/100)</th>
                        <th scope="col">SLO Target</th>
                      </tr>
                    </thead>
                    <tbody>
                      {filteredSingleItems.length === 0 ? (
                        <tr>
                          <td colSpan={11} className="nss-bench-empty">
                            No benchmark workloads match the selected filter.
                          </td>
                        </tr>
                      ) : (
                        filteredSingleItems.map((item) => {
                          const isExpanded = expandedWorkload === item.name;
                          return (
                            <tr key={item.name}>
                              <td>
                                <Stack gap="xs">
                                  <Inline gap="xs" align="center">
                                    <button
                                      type="button"
                                      className="nss-bench-toggle-btn"
                                      onClick={() => setExpandedWorkload(isExpanded ? null : item.name)}
                                      title={isExpanded ? "Collapse details" : "Expand details"}
                                      aria-expanded={isExpanded}
                                    >
                                      <Icon name={isExpanded ? "chevron-down" : "chevron-right"} size={12} />
                                    </button>
                                    <span className="font-medium">{item.name}</span>
                                  </Inline>
                                  {isExpanded ? (
                                    <div className="nss-bench-expanded-details">
                                      <Stack gap="xs">
                                        {item.query ? (
                                          <div>
                                            <span className="nss-bench-label">Query:</span>{" "}
                                            <code>{item.query}</code>
                                          </div>
                                        ) : null}
                                        {item.indexes ? (
                                          <div>
                                            <span className="nss-bench-label">Indexes:</span> {item.indexes}
                                          </div>
                                        ) : null}
                                        {item.cache ? (
                                          <div>
                                            <span className="nss-bench-label">Cache:</span> {item.cache}
                                          </div>
                                        ) : null}
                                        {item.row_width ? (
                                          <div>
                                            <span className="nss-bench-label">Row width:</span> {item.row_width}
                                          </div>
                                        ) : null}
                                        {item.wal_bytes !== undefined ? (
                                          <div>
                                            <span className="nss-bench-label">WAL bytes:</span>{" "}
                                            {item.wal_bytes.toLocaleString()} B
                                          </div>
                                        ) : null}
                                        {item.encrypt_pct !== undefined ? (
                                          <div>
                                            <span className="nss-bench-label">Encryption overhead:</span>{" "}
                                            {item.encrypt_pct.toFixed(2)}%
                                          </div>
                                        ) : null}
                                      </Stack>
                                    </div>
                                  ) : null}
                                </Stack>
                              </td>
                              <td>{(item.ops ?? 0) > 0 ? (item.ops ?? 0).toLocaleString() : "-"}</td>
                              <td>{(item.qps ?? 0) > 0 ? (item.qps ?? 0).toLocaleString() : "-"}</td>
                              <td>{(item.tps ?? 0) > 0 ? (item.tps ?? 0).toLocaleString() : "-"}</td>
                              <td>{formatMicroseconds(item.p50_us ?? 0)}</td>
                              <td>{formatMicroseconds(item.p95_us ?? 0)}</td>
                              <td>{formatMicroseconds(item.p99_us ?? 0)}</td>
                              <td>{formatMicroseconds(item.p999_us ?? 0)}</td>
                              <td>{item.allocs !== undefined ? item.allocs.toLocaleString() : "-"}</td>
                              <td>
                                {item.has_recall ? (
                                  <span>
                                    {item.recall_at_10 !== undefined ? item.recall_at_10.toFixed(3) : "-"} /{" "}
                                    {item.recall_at_100 !== undefined ? item.recall_at_100.toFixed(3) : "-"}
                                  </span>
                                ) : (
                                  "-"
                                )}
                              </td>
                              <td>
                                {item.target ? (
                                  <Inline gap="xs" align="center">
                                    <Badge size="sm" variant={item.met ? "success" : "error"}>
                                      {item.met ? "MET" : "NOT MET"}
                                    </Badge>
                                    <Text size="xs" variant="muted">
                                      {item.target}
                                    </Text>
                                  </Inline>
                                ) : (
                                  "-"
                                )}
                              </td>
                            </tr>
                          );
                        })
                      )}
                    </tbody>
                  </table>
                </div>
              </Stack>
            )}
          </Stack>
        </ModalBody>
        <ModalFooter>
          <Button variant="outline" onClick={onClose}>
            Close
          </Button>
        </ModalFooter>
      </Modal>

      {/* Import JSON Modal */}
      {showImportModal ? (
        <Modal
          open
          onClose={() => setShowImportModal(false)}
          size="md"
          ariaLabel="Import nextsql-bench JSON report"
          scrollable
        >
          <ModalHeader>
            <ModalTitle>Import Benchmark JSON Report</ModalTitle>
          </ModalHeader>
          <ModalBody scrollable>
            <Stack gap="md">
              <Text size="sm" variant="muted">
                Import benchmark results generated by <code>nextsql-bench -json</code>. You can paste the JSON content
                or upload a <code>.json</code> file.
              </Text>

              {importError ? (
                <Alert variant="error" title="Import Error" role="alert">
                  {importError}
                </Alert>
              ) : null}

              <RadioGroup
                label="Import Destination"
                orientation="horizontal"
                value={importTargetSlot}
                onChange={(val) => setImportTargetSlot(val as Slot)}
              >
                <Radio value="candidate" label="Candidate Run" />
                <Radio value="baseline" label="Baseline Run" />
              </RadioGroup>

              <Inline gap="sm" align="center">
                <input
                  type="file"
                  ref={fileInputRef}
                  style={{ display: "none" }}
                  accept=".json,application/json"
                  onChange={handleFileUpload}
                />
                <Button
                  size="sm"
                  variant="outline"
                  icon={<Icon name="download" size={14} />}
                  onClick={() => fileInputRef.current?.click()}
                >
                  Choose JSON File…
                </Button>
              </Inline>

              <Stack gap="xs">
                <label htmlFor="nss-bench-json-paste" className="nss-bench-label">
                  Or Paste Benchmark JSON:
                </label>
                <textarea
                  id="nss-bench-json-paste"
                  className="nss-bench-textarea"
                  rows={10}
                  placeholder={`{\n  "version": "nextsql-bench-report-v1",\n  "suite": "slo",\n  "hardware": { ... },\n  "reports": [ ... ]\n}`}
                  value={importJsonText}
                  onChange={(e) => {
                    setImportJsonText(e.target.value);
                    setImportError(null);
                  }}
                  spellCheck={false}
                />
              </Stack>
            </Stack>
          </ModalBody>
          <ModalFooter>
            <Button variant="outline" onClick={() => setShowImportModal(false)}>
              Cancel
            </Button>
            <Button variant="primary" onClick={handleApplyImport}>
              Load Report
            </Button>
          </ModalFooter>
        </Modal>
      ) : null}
    </>
  );
}

function HardwareDetails({ hw }: { hw?: BenchmarkHardware }) {
  if (!hw) {
    return <Text size="xs" variant="muted">No hardware metadata reported.</Text>;
  }

  return (
    <div className="nss-bench-hw-grid">
      <div className="nss-bench-hw-item">
        <span className="nss-bench-hw-label">CPU:</span>
        <span className="nss-bench-hw-value">{hw.cpu || `${hw.num_cpu || "?"} cores`}</span>
      </div>
      <div className="nss-bench-hw-item">
        <span className="nss-bench-hw-label">RAM:</span>
        <span className="nss-bench-hw-value">{hw.ram || "-"}</span>
      </div>
      <div className="nss-bench-hw-item">
        <span className="nss-bench-hw-label">Storage:</span>
        <span className="nss-bench-hw-value">{hw.storage || "-"}</span>
      </div>
      <div className="nss-bench-hw-item">
        <span className="nss-bench-hw-label">Filesystem:</span>
        <span className="nss-bench-hw-value">{hw.filesystem || "-"}</span>
      </div>
      <div className="nss-bench-hw-item">
        <span className="nss-bench-hw-label">OS / Arch:</span>
        <span className="nss-bench-hw-value">{hw.goos && hw.goarch ? `${hw.goos}/${hw.goarch}` : "-"}</span>
      </div>
      <div className="nss-bench-hw-item">
        <span className="nss-bench-hw-label">Encryption:</span>
        <span className="nss-bench-hw-value">{hw.encryption || "-"}</span>
      </div>
      <div className="nss-bench-hw-item">
        <span className="nss-bench-hw-label">Durability:</span>
        <span className="nss-bench-hw-value">{hw.durability || "-"}</span>
      </div>
      <div className="nss-bench-hw-item">
        <span className="nss-bench-hw-label">NextSQL Version:</span>
        <span className="nss-bench-hw-value">{hw.version || "-"}</span>
      </div>
      {hw.concurrency !== undefined ? (
        <div className="nss-bench-hw-item">
          <span className="nss-bench-hw-label">Concurrency:</span>
          <span className="nss-bench-hw-value">{hw.concurrency}</span>
        </div>
      ) : null}
      {hw.row_count !== undefined ? (
        <div className="nss-bench-hw-item">
          <span className="nss-bench-hw-label">Rows Seeded:</span>
          <span className="nss-bench-hw-value">{hw.row_count.toLocaleString()}</span>
        </div>
      ) : null}
      {hw.buffer_pages !== undefined ? (
        <div className="nss-bench-hw-item">
          <span className="nss-bench-hw-label">Buffer Pages:</span>
          <span className="nss-bench-hw-value">{hw.buffer_pages.toLocaleString()}</span>
        </div>
      ) : null}
    </div>
  );
}
