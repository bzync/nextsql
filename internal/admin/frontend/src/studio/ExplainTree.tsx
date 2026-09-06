import { Badge, Inline, Stack, Text } from "@bzync/rui";
import { explainEstimateSeverity, type ExplainNode } from "./resultTools";

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

export function ExplainTree({ nodes, analyzed }: { nodes: ExplainNode[]; analyzed: boolean }) {
  return (
    <Stack gap="sm" className="nss-explain-tree">
      {!analyzed ? (
        <Text size="sm" variant="muted">
          Estimates only — run EXPLAIN ANALYZE to see measured rows, time, and resource use. Nothing below is a measurement.
        </Text>
      ) : null}
      <ul className="nss-explain-roots">
        {nodes.map((node, index) => (
          <ExplainNodeView key={index} node={node} analyzed={analyzed} />
        ))}
      </ul>
    </Stack>
  );
}
