import { useMemo, useState } from "react";
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
  Select,
  Spinner,
  Stack,
  Stat,
  Text,
} from "@bzync/rui";
import type { ResultSet, StudioWorkflowOverview } from "../ops/api";
import { ResultTable } from "../ops/ResultTable";
import { WorkflowRelationships } from "./WorkflowRelationships";

// Workflows, triggers, schedules, tasks & change streams explorer
// (Workflow/CDC scope): a read-only view over the authorized system catalog,
// fetched through the logged-in operator's own NSQL connection
// (GET /api/v1/studio/workflows). Every view carries the catalog's own RBAC
// filtering — a non-admin sees only what it owns or may see, zero rows rather
// than an error — so a degraded optional read means an empty section.
//
// Read-only by design, matching the Transaction console / Audit viewer: the
// Definitions are authored through the editor's own CREATE/ALTER
// WORKFLOW|TRIGGER|SCHEDULE statements (which pass the same RBAC the server
// enforces), CANCEL TASK is a deliberate follow-on rather than a Studio-local
// button, and a CDC subscription can only be paused/resumed by its own
// consuming client — Studio is not that client.
export function WorkflowExplorer({
  onClose,
  data,
  loading,
  error,
  onRefresh,
}: {
  onClose: () => void;
  data: StudioWorkflowOverview | null;
  loading: boolean;
  error: string | null;
  onRefresh: () => void;
}) {
  const [workflowFilter, setWorkflowFilter] = useState("");

  const taskWorkflowIndex = data ? (data.tasks.columns ?? []).indexOf("workflow") : -1;

  const workflowOptions = useMemo(() => {
    if (!data || taskWorkflowIndex < 0) return [];
    const names = new Set<string>();
    for (const row of data.tasks.rows ?? []) {
      const name = row[taskWorkflowIndex];
      if (typeof name === "string" && name !== "") names.add(name);
    }
    return [...names].sort();
  }, [data, taskWorkflowIndex]);

  const filteredTasks: ResultSet | null = useMemo(() => {
    if (!data) return null;
    if (workflowFilter === "" || taskWorkflowIndex < 0) return data.tasks;
    return {
      ...data.tasks,
      rows: (data.tasks.rows ?? []).filter((row) => row[taskWorkflowIndex] === workflowFilter),
    };
  }, [data, workflowFilter, taskWorkflowIndex]);

  return (
    <Modal open onClose={onClose} size="lg" ariaLabel="Workflows, triggers, schedules, tasks & change streams" scrollable>
      <ModalHeader>
        <ModalTitle>Workflows, relationships &amp; activity</ModalTitle>
      </ModalHeader>
      <ModalBody scrollable>
        <Stack gap="md">
          <Alert variant="info" title="Scope">
            A read-only snapshot of visible workflows and the triggers,
            schedules, durable tasks, and node-local CDC subscriptions related
            to them — directly from the server's authorized <code>system.*</code>{" "}
            catalog. Definitions are authored through the editor's own{" "}
            <code>CREATE</code>/<code>ALTER</code> statements; there is no
            cancel/retry or pause/resume action here.
          </Alert>
          {data?.warnings?.length ? (
            <Alert variant="warning" title="Some data was unavailable">
              <List>
                {data.warnings.map((warning, index) => (
                  <ListItem key={index}>{warning}</ListItem>
                ))}
              </List>
            </Alert>
          ) : null}
          {data && [data.workflows, data.triggers, data.schedules, data.tasks, data.change_streams].some((result) => result.truncated) ? (
            <Alert variant="warning" title="Large catalog snapshot">
              Each section is capped at 500 rows. A section marked by this
              snapshot may omit later definitions or activity rows.
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
              <Text size="sm" variant="muted">Loading workflows and tasks…</Text>
            </Inline>
          ) : data ? (
            <>
              <Inline gap="md" wrap>
                <Stat label="Workflows" value={String(data.workflows.rows?.length ?? 0)} />
                <Stat label="Triggers" value={String(data.triggers.rows?.length ?? 0)} />
                <Stat label="Schedules" value={String(data.schedules.rows?.length ?? 0)} />
                <Stat label="Tasks" value={String(data.tasks.rows?.length ?? 0)} />
                <Stat label="Change streams" value={String(data.change_streams.rows?.length ?? 0)} />
              </Inline>
              <Stack gap="xs">
                <Heading as="h3" size="sm">Workflows</Heading>
                <ResultTable
                  result={data.workflows}
                  empty="No workflows visible"
                  label="Workflows"
                />
              </Stack>
              <WorkflowRelationships
                workflows={data.workflows}
                triggers={data.triggers}
                schedules={data.schedules}
              />
              <Stack gap="xs">
                <Heading as="h3" size="sm">Triggers</Heading>
                <ResultTable
                  result={data.triggers}
                  empty="No triggers visible"
                  label="Triggers"
                />
              </Stack>
              <Stack gap="xs">
                <Heading as="h3" size="sm">Schedules</Heading>
                <ResultTable
                  result={data.schedules}
                  empty="No schedules visible"
                  label="Schedules"
                />
              </Stack>
              <Stack gap="xs">
                <Inline gap="sm" align="end" wrap>
                  <Heading as="h3" size="sm">Scheduled tasks</Heading>
                  {workflowOptions.length > 0 ? (
                    <Select
                      id="workflow-task-filter"
                      label="Filter by workflow"
                      options={[
                        { value: "", label: "All workflows" },
                        ...workflowOptions.map((name) => ({ value: name, label: name })),
                      ]}
                      value={workflowFilter}
                      onChange={setWorkflowFilter}
                    />
                  ) : null}
                </Inline>
                <ResultTable
                  result={filteredTasks ?? data.tasks}
                  empty="No scheduled tasks"
                  label="Scheduled tasks"
                />
              </Stack>
              <Stack gap="xs">
                <Heading as="h3" size="sm">Change streams (CDC)</Heading>
                <Text size="sm" variant="muted">
                  Open <code>SUBSCRIBE</code> consumers on this node. <code>lsn</code>{" "}
                  is each subscription's last-observed commit position (its resume cursor).
                </Text>
                <ResultTable
                  result={data.change_streams}
                  empty="No change-stream subscriptions open"
                  label="Change streams"
                />
              </Stack>
            </>
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
