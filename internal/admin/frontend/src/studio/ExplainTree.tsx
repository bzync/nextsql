import { useState, useMemo } from "react";
import { Badge, Button, Inline, Stack, Text } from "@bzync/rui";
import { SegmentedControl } from "../shared/SegmentedControl";
import {
  explainEstimateSeverity,
  buildVisualPlanLayout,
  explainNodesToJSON,
  type ExplainNode,
} from "./resultTools";

function ExplainNodeView({ node, analyzed }: { node: ExplainNode; analyzed: boolean }) {
  const severity = analyzed ? explainEstimateSeverity(node.estRows, node.actRows) : "none";
  return (
    <li className="nss-explain-node">
      <div className="nss-explain-row">
        <Text as="span" weight="medium" className="nss-explain-label">{node.label}</Text>
        <Inline gap="xs" align="center" wrap>
          <Badge size="sm" variant="muted">est {node.estRows ?? "?"} rows</Badge>
          {analyzed && node.actRows !== null ? (
            <Badge size="sm" variant={severity === "error" ? "error" : severity === "warning" ? "warning" : "success"}>
              actual {node.actRows} rows
              {severity !== "none" ? ` · ${(node.actRows / Math.max(node.estRows ?? 1, 1)).toFixed(1)}x est.` : ""}
            </Badge>
          ) : null}
          {analyzed && node.actRows !== null ? (
            <Text as="span" size="xs" variant="muted">
              {node.time || "0ns"} · cpu {node.cpu || "0ns"}
              {node.workers > 1 ? ` · ${node.workers} workers` : ""}
              {node.memory ? ` · ${node.memory.toLocaleString()} mem` : ""}
              {node.disk ? ` · ${node.disk.toLocaleString()} disk` : ""}
              {node.cache ? ` · ${node.cache.toLocaleString()} cache` : ""}
              {node.spill ? ` · ${node.spill.toLocaleString()} spill` : ""}
            </Text>
          ) : null}
          {node.index ? <Badge size="sm" variant="info">{node.index}</Badge> : null}
        </Inline>
      </div>
      {node.children.length ? (
        <ul className="nss-explain-children">
          {node.children.map((child, index) => (
            <ExplainNodeView key={index} node={child} analyzed={analyzed} />
          ))}
        </ul>
      ) : null}
    </li>
  );
}

export function ExplainTree({
  nodes,
  analyzed,
  rawPlan,
}: {
  nodes: ExplainNode[];
  analyzed: boolean;
  rawPlan?: string;
}) {
  const [viewMode, setViewMode] = useState<"visual" | "tree" | "json">("visual");
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [zoom, setZoom] = useState<number>(100);
  const [copied, setCopied] = useState<boolean>(false);

  const layout = useMemo(() => buildVisualPlanLayout(nodes), [nodes]);

  // Select root node by default if none selected or if selected node not in layout
  const activeNode = useMemo(() => {
    if (!layout.nodes.length) return null;
    if (selectedId) {
      const found = layout.nodes.find((n) => n.id === selectedId);
      if (found) return found;
    }
    return layout.nodes[0];
  }, [layout, selectedId]);

  const jsonText = useMemo(() => {
    if (rawPlan) {
      try {
        const parsed = JSON.parse(rawPlan);
        return JSON.stringify(parsed, null, 2);
      } catch {
        return rawPlan;
      }
    }
    return explainNodesToJSON(nodes);
  }, [rawPlan, nodes]);

  const copyJSON = () => {
    navigator.clipboard.writeText(jsonText).then(() => {
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    });
  };

  const downloadJSON = () => {
    try {
      const blob = new Blob([jsonText], { type: "application/json" });
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = `nextsql-plan-${Date.now()}.json`;
      a.click();
      URL.revokeObjectURL(url);
    } catch {
      /* clipboard fallback */
      copyJSON();
    }
  };

  return (
    <Stack gap="sm" className="nss-explain-tree">
      <div className="nss-plan-toolbar">
        <Inline gap="sm" align="center" wrap>
          <SegmentedControl
            size="sm"
            value={viewMode}
            onChange={(val) => setViewMode(val as "visual" | "tree" | "json")}
            options={[
              { value: "visual", label: "Visual Graph" },
              { value: "tree", label: "Tree View" },
              { value: "json", label: "Raw JSON" },
            ]}
            label="Plan view mode"
          />
          {viewMode === "visual" ? (
            <Inline gap="xs" align="center">
              <Button
                variant="outline"
                size="sm"
                onClick={() => setZoom((z) => Math.max(50, z - 10))}
                disabled={zoom <= 50}
                aria-label="Zoom out query plan"
              >
                −
              </Button>
              <Text size="xs" variant="muted" style={{ minWidth: "36px", textAlign: "center" }}>
                {zoom}%
              </Text>
              <Button
                variant="outline"
                size="sm"
                onClick={() => setZoom((z) => Math.min(150, z + 10))}
                disabled={zoom >= 150}
                aria-label="Zoom in query plan"
              >
                +
              </Button>
              {zoom !== 100 ? (
                <Button variant="ghost" size="sm" onClick={() => setZoom(100)}>
                  Reset
                </Button>
              ) : null}
            </Inline>
          ) : null}
        </Inline>

        <Inline gap="xs" align="center" wrap>
          <Badge size="sm" variant="muted">{layout.operatorCount} {layout.operatorCount === 1 ? "operator" : "operators"}</Badge>
          {analyzed ? (
            <Badge size="sm" variant="success">ANALYZE measured</Badge>
          ) : (
            <Badge size="sm" variant="muted">Estimates only</Badge>
          )}
          {viewMode === "json" ? (
            <Button variant="outline" size="sm" onClick={copyJSON}>
              {copied ? "Copied!" : "Copy JSON"}
            </Button>
          ) : null}
          <Button variant="outline" size="sm" onClick={downloadJSON} aria-label="Export plan JSON">
            Export JSON
          </Button>
        </Inline>
      </div>

      {!analyzed && viewMode !== "json" ? (
        <Text size="xs" variant="muted">
          Estimates only — run EXPLAIN ANALYZE to observe measured rows, time, and worker allocation.
        </Text>
      ) : null}

      {viewMode === "visual" ? (
        <div className="nss-plan-visual-section">
          <div
            className="nss-plan-canvas-wrap"
            tabIndex={0}
            role="region"
            aria-label="Visual execution plan canvas"
          >
            <div
              className="nss-plan-viewport"
              style={{
                width: layout.width,
                height: layout.height,
                transform: zoom !== 100 ? `scale(${zoom / 100})` : undefined,
                transformOrigin: "top left",
              }}
            >
              <svg
                className="nss-plan-edges-svg"
                width={layout.width}
                height={layout.height}
                aria-hidden="true"
              >
                <defs>
                  <marker
                    id="nss-plan-arrow"
                    viewBox="0 0 10 10"
                    refX="6"
                    refY="5"
                    markerWidth="6"
                    markerHeight="6"
                    orient="auto-start-reverse"
                  >
                    <path d="M 0 1 L 10 5 L 0 9 z" fill="var(--color-border-strong, #64748b)" />
                  </marker>
                </defs>
                {layout.edges.map((edge) => (
                  <path
                    key={edge.id}
                    d={edge.d}
                    className="nss-plan-edge"
                    markerEnd="url(#nss-plan-arrow)"
                  />
                ))}
              </svg>

              {layout.nodes.map((vnode) => {
                const isSelected = activeNode?.id === vnode.id;
                const severity = analyzed ? vnode.severity : "none";
                return (
                  <div
                    key={vnode.id}
                    role="button"
                    tabIndex={0}
                    aria-label={`Operator ${vnode.label}, cost ${vnode.node.estCost ?? 0}`}
                    className={`nss-plan-node-card ${isSelected ? "selected" : ""}`}
                    style={{
                      left: vnode.x,
                      top: vnode.y,
                      width: vnode.w,
                      height: vnode.h,
                    }}
                    onClick={() => setSelectedId(vnode.id)}
                    onKeyDown={(e) => {
                      if (e.key === "Enter" || e.key === " ") {
                        e.preventDefault();
                        setSelectedId(vnode.id);
                      }
                    }}
                  >
                    <div className="nss-plan-card-header">
                      <span className={`nss-plan-cat-badge nss-cat-${vnode.category}`}>
                        {vnode.category.toUpperCase()}
                      </span>
                      {vnode.node.index ? (
                        <Badge size="sm" variant="info">{vnode.node.index}</Badge>
                      ) : null}
                    </div>

                    <div className="nss-plan-card-title" title={vnode.label}>
                      {vnode.opName}
                      {vnode.detail ? <span className="nss-plan-card-detail"> {vnode.detail}</span> : null}
                    </div>

                    <div className="nss-plan-card-metrics">
                      {analyzed && vnode.node.actRows !== null ? (
                        <Badge
                          size="sm"
                          variant={severity === "error" ? "error" : severity === "warning" ? "warning" : "success"}
                        >
                          actual {vnode.node.actRows} rows
                          {severity !== "none"
                            ? ` · ${(vnode.node.actRows / Math.max(vnode.node.estRows ?? 1, 1)).toFixed(1)}x est.`
                            : ""}
                        </Badge>
                      ) : (
                        <Badge size="sm" variant="muted">
                          est {vnode.node.estRows ?? "?"} rows
                        </Badge>
                      )}
                      {analyzed && vnode.node.time ? (
                        <span className="nss-plan-time-chip">{vnode.node.time}</span>
                      ) : (
                        <span className="nss-plan-cost-chip">cost {vnode.node.estCost ?? 0}</span>
                      )}
                    </div>
                  </div>
                );
              })}
            </div>
          </div>

          {activeNode ? (
            <div className="nss-plan-inspector" role="region" aria-label="Operator details">
              <div className="nss-plan-inspector-header">
                <Inline gap="xs" align="center">
                  <span className={`nss-plan-cat-badge nss-cat-${activeNode.category}`}>
                    {activeNode.category.toUpperCase()}
                  </span>
                  <Text weight="semibold" size="sm">{activeNode.label}</Text>
                </Inline>
                {activeNode.node.index ? (
                  <Badge size="sm" variant="info">Index: {activeNode.node.index}</Badge>
                ) : null}
              </div>

              <div className="nss-plan-inspector-grid">
                <div className="nss-plan-stat-block">
                  <span className="nss-stat-label">Estimated Rows</span>
                  <span className="nss-stat-value">{activeNode.node.estRows?.toLocaleString() ?? "—"}</span>
                </div>
                <div className="nss-plan-stat-block">
                  <span className="nss-stat-label">Estimated Cost</span>
                  <span className="nss-stat-value">{activeNode.node.estCost?.toLocaleString() ?? "—"}</span>
                </div>
                {analyzed ? (
                  <>
                    <div className="nss-plan-stat-block">
                      <span className="nss-stat-label">Actual Rows</span>
                      <span className="nss-stat-value">
                        {activeNode.node.actRows?.toLocaleString() ?? "0"}
                        {activeNode.node.estRows !== null && activeNode.node.actRows !== null ? (
                          <span className={`nss-factor-${activeNode.severity}`}>
                            {" "}({(activeNode.node.actRows / Math.max(activeNode.node.estRows, 1)).toFixed(1)}x est.)
                          </span>
                        ) : null}
                      </span>
                    </div>
                    <div className="nss-plan-stat-block">
                      <span className="nss-stat-label">Execution Time</span>
                      <span className="nss-stat-value">{activeNode.node.time || "0ns"}</span>
                    </div>
                    <div className="nss-plan-stat-block">
                      <span className="nss-stat-label">CPU Work Time</span>
                      <span className="nss-stat-value">{activeNode.node.cpu || "0ns"}</span>
                    </div>
                    <div className="nss-plan-stat-block">
                      <span className="nss-stat-label">Workers</span>
                      <span className="nss-stat-value">{activeNode.node.workers || 1}</span>
                    </div>
                    <div className="nss-plan-stat-block">
                      <span className="nss-stat-label">Memory</span>
                      <span className="nss-stat-value">{activeNode.node.memory ? `${activeNode.node.memory.toLocaleString()} B` : "0"}</span>
                    </div>
                    <div className="nss-plan-stat-block">
                      <span className="nss-stat-label">Disk / Spill</span>
                      <span className="nss-stat-value">
                        {activeNode.node.disk ? `${activeNode.node.disk.toLocaleString()} B` : "0"}
                        {activeNode.node.spill ? ` (${activeNode.node.spill.toLocaleString()} spill)` : ""}
                      </span>
                    </div>
                  </>
                ) : null}
              </div>
            </div>
          ) : null}
        </div>
      ) : viewMode === "tree" ? (
        <ul className="nss-explain-roots">
          {nodes.map((node, index) => (
            <ExplainNodeView key={index} node={node} analyzed={analyzed} />
          ))}
        </ul>
      ) : (
        <pre className="nss-plan-json" tabIndex={0} aria-label="JSON execution plan">
          {jsonText}
        </pre>
      )}
    </Stack>
  );
}
