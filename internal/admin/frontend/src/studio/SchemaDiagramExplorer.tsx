import { useMemo } from "react";
import {
  Alert,
  Button,
  Heading,
  Inline,
  List,
  ListItem,
  Modal,
  ModalBody,
  ModalFooter,
  ModalHeader,
  ModalTitle,
  Spinner,
  Stack,
  Stat,
  Text,
} from "@bzync/rui";
import type { StudioSchemaGraph } from "../ops/api";
import {
  buildSchemaGraph,
  layoutSchemaGraph,
  schemaGraphSummary,
  MAX_SCHEMA_DIAGRAM_TABLES,
} from "./resultTools";

// Schema-relationship diagram (Database explorer scope): a read-only view of
// the foreign keys across every table the operator can see, from a single
// authorized SELECT against system.foreign_keys (GET /api/v1/studio/schema-
// graph). The SVG lays tables out in dependency layers; the grouped
// "Relationships" list below it is always shown and is the diagram's text
// alternative. A schema too large to draw legibly shows the list alone.
export function SchemaDiagramExplorer({
  onClose,
  data,
  loading,
  error,
  onRefresh,
}: {
  onClose: () => void;
  data: StudioSchemaGraph | null;
  loading: boolean;
  error: string | null;
  onRefresh: () => void;
}) {
  const model = useMemo(() => buildSchemaGraph(data?.foreign_keys ?? null), [data]);
  const layout = useMemo(() => layoutSchemaGraph(model), [model]);

  return (
    <Modal open onClose={onClose} size="lg" ariaLabel="Schema relationship diagram" scrollable>
      <ModalHeader>
        <ModalTitle>Schema relationships</ModalTitle>
      </ModalHeader>
      <ModalBody scrollable>
        <Stack gap="md">
          <Alert variant="info" title="Scope">
            Every foreign key between tables you can see, from the authorized{" "}
            <code>system.foreign_keys</code> catalog. The arrow points from the
            referencing (child) table to the referenced (parent) table.
          </Alert>
          {data?.truncated ? (
            <Alert variant="warning" title="Large schema">
              The foreign-key read was capped; some relationships are not shown.
            </Alert>
          ) : null}
          {data?.warnings?.length ? (
            <Alert variant="warning" title="Some data was unavailable">
              <List>
                {data.warnings.map((warning, index) => (
                  <ListItem key={index}>{warning}</ListItem>
                ))}
              </List>
            </Alert>
          ) : null}
          {error ? (
            <Alert variant="error" title="Could not load" role="alert">
              <Inline gap="sm" align="center" wrap>
                <span>{error}</span>
                <Button variant="outline" size="sm" onClick={onRefresh}>Retry</Button>
              </Inline>
            </Alert>
          ) : loading && !data ? (
            <Inline gap="sm" align="center" role="status">
              <Spinner size="sm" />
              <Text size="sm" variant="muted">Loading schema relationships…</Text>
            </Inline>
          ) : data ? (
            model.edges.length === 0 ? (
              <Text size="sm" variant="muted">
                No foreign-key relationships between the tables you can see.
              </Text>
            ) : (
              <>
                <Inline gap="md" wrap>
                  <Stat label="Tables" value={String(model.tables.length)} />
                  <Stat label="Relationships" value={String(model.edges.length)} />
                </Inline>
                {layout ? (
                  <div
                    className="nss-schema-diagram-scroll"
                    tabIndex={0}
                    role="group"
                    aria-label="Foreign-key diagram, scrollable"
                  >
                    <svg
                      className="nss-schema-diagram"
                      viewBox={`0 0 ${layout.width} ${layout.height}`}
                      width={layout.width}
                      height={layout.height}
                      role="img"
                      aria-label={schemaGraphSummary(model)}
                    >
                      <defs>
                        <marker
                          id="nss-schema-arrow"
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
                        <path key={link.key} d={link.d} className="nss-schema-link" markerEnd="url(#nss-schema-arrow)">
                          <title>{link.title}</title>
                        </path>
                      ))}
                      {layout.nodes.map((node) => (
                        <g key={node.name} className="nss-schema-node">
                          <rect x={node.x} y={node.y} width={node.w} height={node.h} rx="4" />
                          <text x={node.x + node.w / 2} y={node.y + node.h / 2} dominantBaseline="central" textAnchor="middle">
                            {node.name}
                          </text>
                        </g>
                      ))}
                    </svg>
                  </div>
                ) : (
                  <Text size="sm" variant="muted">
                    This schema has more than {MAX_SCHEMA_DIAGRAM_TABLES} related tables — the
                    relationships are listed below instead of drawn.
                  </Text>
                )}
                <Stack gap="xs">
                  <Heading as="h3" size="sm">Relationships</Heading>
                  <List>
                    {model.byParent.map((group) => (
                      <ListItem key={group.parent}>
                        <Text as="span" weight="medium">{group.parent}</Text>
                        <List>
                          {group.edges.map((edge) => (
                            <ListItem key={edge.key}>
                              <Text as="span" size="sm">
                                {edge.child} ({edge.columns.join(", ")}) &rarr;{" "}
                                {edge.parent} ({edge.refColumns.join(", ")})
                                {edge.onDelete && edge.onDelete !== "RESTRICT"
                                  ? ` · ON DELETE ${edge.onDelete}`
                                  : ""}
                              </Text>
                            </ListItem>
                          ))}
                        </List>
                      </ListItem>
                    ))}
                  </List>
                </Stack>
              </>
            )
          ) : null}
        </Stack>
      </ModalBody>
      <ModalFooter>
        <Button variant="outline" onClick={onClose}>Close</Button>
        <Button variant="primary" onClick={onRefresh} disabled={loading}>
          {loading && data ? "Refreshing…" : "Refresh"}
        </Button>
      </ModalFooter>
    </Modal>
  );
}
