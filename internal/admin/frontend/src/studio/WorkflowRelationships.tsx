import { useMemo } from "react";
import { Alert, Heading, List, ListItem, Stack, Text } from "@bzync/rui";
import type { StudioResultSet } from "../ops/api";
import {
  buildWorkflowRelationships,
  layoutWorkflowRelationships,
  MAX_WORKFLOW_DIAGRAM_NODES,
  workflowRelationshipLabel,
  workflowRelationshipSummary,
} from "./resultTools";

function compactName(name: string): string {
  return name.length > 26 ? `${name.slice(0, 23)}…` : name;
}

// The SVG is an enhancement over the always-visible relationship list. It is
// omitted when either catalog read was capped or the graph exceeds its strict
// legibility bound, so Studio never draws a misleading partial diagram.
export function WorkflowRelationships({
  workflows,
  triggers,
  schedules,
}: {
  workflows: StudioResultSet;
  triggers: StudioResultSet;
  schedules: StudioResultSet;
}) {
  const model = useMemo(
    () => buildWorkflowRelationships(workflows, triggers, schedules),
    [workflows, triggers, schedules],
  );
  const layout = useMemo(() => layoutWorkflowRelationships(model), [model]);

  if (model.relationships.length === 0) {
    return (
      <Stack gap="xs">
        <Heading as="h3" size="sm">Trigger &amp; schedule relationships</Heading>
        <Text size="sm" variant="muted">
          No trigger or schedule relationships are visible.
        </Text>
      </Stack>
    );
  }

  return (
    <Stack gap="sm">
      <Heading as="h3" size="sm">Trigger &amp; schedule relationships</Heading>
      {model.truncated ? (
        <Alert variant="warning" title="Relationship view capped">
          A workflow, trigger, or schedule catalog reached its bound. The
          bounded list below remains available, but the diagram is hidden
          because it would be incomplete.
        </Alert>
      ) : null}
      {layout ? (
        <div
          className="nss-schema-diagram-scroll"
          tabIndex={0}
          role="group"
          aria-label="Workflow relationship diagram, scrollable"
        >
          <svg
            className="nss-schema-diagram"
            viewBox={`0 0 ${layout.width} ${layout.height}`}
            width={layout.width}
            height={layout.height}
            role="img"
            aria-label={workflowRelationshipSummary(model)}
          >
            <defs>
              <marker
                id="nss-workflow-arrow"
                viewBox="0 0 10 10"
                refX="9"
                refY="5"
                markerWidth="7"
                markerHeight="7"
                orient="auto-start-reverse"
              >
                <path d="M 0 0 L 10 5 L 0 10 z" className="nss-schema-arrowhead" />
              </marker>
            </defs>
            {layout.links.map((link) => (
              <path
                key={link.key}
                d={link.d}
                className="nss-schema-link"
                markerEnd="url(#nss-workflow-arrow)"
              >
                <title>{link.title}</title>
              </path>
            ))}
            {layout.nodes.map((node) => (
              <g key={node.id} className={`nss-schema-node nss-workflow-node nss-workflow-node-${node.kind}`}>
                <title>{`${node.kind}: ${node.name}`}</title>
                <rect x={node.x} y={node.y} width={node.w} height={node.h} rx="4" />
                <text x={node.x + 10} y={node.y + 14}>
                  <tspan className="nss-workflow-node-kind">{node.kind.toUpperCase()}</tspan>
                  <tspan x={node.x + 10} dy="16">{compactName(node.name)}</tspan>
                </text>
              </g>
            ))}
          </svg>
        </div>
      ) : !model.truncated ? (
        <Text size="sm" variant="muted">
          This relationship graph has more than {MAX_WORKFLOW_DIAGRAM_NODES} nodes
          or too many links to draw legibly; the complete bounded relationship
          list remains below.
        </Text>
      ) : null}
      <Stack gap="xs">
        <Heading as="h4" size="xs">Relationship list</Heading>
        <List>
          {model.relationships.map((relationship) => (
            <ListItem key={relationship.key}>
              <Text as="span" size="sm">{workflowRelationshipLabel(relationship)}</Text>
            </ListItem>
          ))}
        </List>
      </Stack>
    </Stack>
  );
}
