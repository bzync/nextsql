import { Badge, Button, Card, CardBody, CardHeader, CardTitle, Inline, Modal, ModalBody, ModalFooter, ModalHeader, ModalTitle, Stack, Text } from "@bzync/rui";
import { type SessionProfile } from "../ops/api";
import { EnvironmentBadge } from "../shared/status";
import { Icon } from "../shared/icons";
import { formatRelativeTime, type RecentConnection } from "./resultTools";

export function RecentConnectionsModal({
  open,
  onClose,
  currentProfile,
  allProfiles,
  user,
  database,
  tableCount,
  savedQueriesCount,
  hasDrafts,
  recentConnections,
  onClearRecents,
  onRequestSwitchServer,
  onNewTab,
  onOpenSavedQueries,
  onOpenSearchObjects,
  onOpenSchemaDesigner,
  onOpenImport,
  onOpenSchemaDiagram,
  onOpenDataGenerator,
  onOpenSchemaDiff,
  onOpenBenchmarkViewer,
  onOpenStreamingImport,
}: {
  open: boolean;
  onClose: () => void;
  currentProfile: SessionProfile;
  allProfiles: SessionProfile[];
  user: string;
  database: string;
  tableCount: number;
  savedQueriesCount: number;
  hasDrafts: boolean;
  recentConnections: RecentConnection[];
  onClearRecents: () => void;
  onRequestSwitchServer?: (profileId?: string) => void;
  onNewTab: () => void;
  onOpenSavedQueries: () => void;
  onOpenSearchObjects: () => void;
  onOpenSchemaDesigner: () => void;
  onOpenImport: () => void;
  onOpenStreamingImport?: () => void;
  onOpenSchemaDiagram: () => void;
  onOpenDataGenerator: () => void;
  onOpenSchemaDiff?: () => void;
  onOpenBenchmarkViewer?: () => void;
}) {
  if (!open) return null;

  return (
    <Modal open onClose={onClose} size="lg" ariaLabel="Connections and recent projects" scrollable>
      <ModalHeader>
        <ModalTitle>
          <Inline gap="xs" align="center">
            <Icon name="network" size={20} />
            <span>Connections & Recent Projects</span>
          </Inline>
        </ModalTitle>
      </ModalHeader>
      <ModalBody scrollable>
        <Stack gap="lg" className="nss-home-modal-body">
          {/* Active Session Overview */}
          <Card variant="bordered" className="nss-home-active-card">
            <CardHeader>
              <CardTitle as="h2">
                <Inline gap="sm" align="center" justify="between">
                  <Inline gap="xs" align="center">
                    <span className="nss-conn-status-dot nss-conn-status-dot--connected" aria-hidden="true" />
                    <span className="sr-only">Active session:</span>
                    <span className="font-semibold text-base">{currentProfile.name}</span>
                    {currentProfile.environment ? <EnvironmentBadge environment={currentProfile.environment} /> : null}
                  </Inline>
                  <Text size="xs" variant="muted" className="font-mono">
                    {currentProfile.address || "127.0.0.1:7210"}
                  </Text>
                </Inline>
              </CardTitle>
            </CardHeader>
            <CardBody>
              <Inline gap="md" align="center" justify="between" wrap className="nss-home-active-meta">
                <Inline gap="lg" align="center" wrap>
                  <div>
                    <Text size="xs" variant="muted">Database</Text>
                    <Text size="sm" weight="medium" className="font-mono">{database || "default"}</Text>
                  </div>
                  <div>
                    <Text size="xs" variant="muted">User</Text>
                    <Text size="sm" weight="medium" className="font-mono">{user}</Text>
                  </div>
                  <div>
                    <Text size="xs" variant="muted">Catalog</Text>
                    <Text size="sm" weight="medium">{tableCount} tables</Text>
                  </div>
                  <div>
                    <Text size="xs" variant="muted">Security</Text>
                    <div className="pt-0.5">
                      {currentProfile.mtls ? (
                        <Badge variant="success">mTLS</Badge>
                      ) : currentProfile.tls ? (
                        <Badge variant="success">TLS</Badge>
                      ) : (
                        <Badge variant="muted">Plaintext</Badge>
                      )}
                    </div>
                  </div>
                  {hasDrafts ? (
                    <div>
                      <Text size="xs" variant="muted">Workspace</Text>
                      <div className="pt-0.5">
                        <Badge variant="warning">Unsaved tabs</Badge>
                      </div>
                    </div>
                  ) : null}
                </Inline>

                <Inline gap="xs" wrap>
                  {onRequestSwitchServer ? (
                    <Button
                      variant="outline"
                      size="sm"
                      icon={<Icon name="plug" size={14} />}
                      onClick={() => {
                        onClose();
                        onRequestSwitchServer();
                      }}
                    >
                      Switch server…
                    </Button>
                  ) : null}
                  <Button
                    variant="primary"
                    size="sm"
                    icon={<Icon name="plus" size={14} />}
                    onClick={() => {
                      onClose();
                      onNewTab();
                    }}
                  >
                    New query tab
                  </Button>
                </Inline>
              </Inline>
            </CardBody>
          </Card>

          {/* Recent Connections */}
          <Stack gap="sm">
            <Inline gap="xs" align="center" justify="between">
              <div>
                <Text size="sm" weight="semibold">Recent Connections</Text>
                <Text size="xs" variant="muted">Previous connections recorded in this browser</Text>
              </div>
              {recentConnections.length > 0 ? (
                <Button variant="ghost" size="sm" onClick={onClearRecents}>
                  Clear history
                </Button>
              ) : null}
            </Inline>

            {recentConnections.length === 0 ? (
              <Card variant="bordered" className="p-4 text-center">
                <Text size="sm" variant="muted">No recent connections recorded yet.</Text>
              </Card>
            ) : (
              <div className="nss-home-table-wrap">
                <table className="nss-home-table" aria-label="Recent connections list">
                  <thead>
                    <tr>
                      <th scope="col">Server</th>
                      <th scope="col">Environment</th>
                      <th scope="col">User & Database</th>
                      <th scope="col">Last Active</th>
                      <th scope="col" className="text-right">Action</th>
                    </tr>
                  </thead>
                  <tbody>
                    {recentConnections.map((r) => (
                      <tr key={`${r.profileId}:${r.user}:${r.database}`}>
                        <td className="font-medium">
                          <Inline gap="xs" align="center">
                            <Icon name="server" size={14} />
                            <span>{r.name}</span>
                          </Inline>
                        </td>
                        <td>
                          {r.environment ? <EnvironmentBadge environment={r.environment} /> : <Text size="xs" variant="muted">-</Text>}
                        </td>
                        <td className="font-mono text-xs">
                          {r.user}@{r.database}
                        </td>
                        <td className="text-xs text-muted-foreground">
                          {r.connectedAt ? formatRelativeTime(r.connectedAt) : "-"}
                        </td>
                        <td className="text-right">
                          {onRequestSwitchServer ? (
                            <Button
                              variant="outline"
                              size="sm"
                              onClick={() => {
                                onClose();
                                onRequestSwitchServer(r.profileId);
                              }}
                              aria-label={`Switch to ${r.name}`}
                            >
                              Connect
                            </Button>
                          ) : null}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </Stack>

          {/* Configured Server Profiles */}
          {allProfiles.length > 1 ? (
            <Stack gap="sm">
              <div>
                <Text size="sm" weight="semibold">Available Server Profiles</Text>
                <Text size="xs" variant="muted">Declared targets from nextsql-admin --profiles</Text>
              </div>

              <div className="nss-home-profile-grid">
                {allProfiles.map((p) => {
                  const isCurrent = p.id === currentProfile.id;
                  return (
                    <Card key={p.id} variant="bordered" className={`nss-home-profile-card${isCurrent ? " nss-home-profile-card--current" : ""}`}>
                      <CardBody className="p-3">
                        <Stack gap="xs">
                          <Inline gap="xs" align="center" justify="between">
                            <span className="font-medium text-sm">{p.name}</span>
                            {p.environment ? <EnvironmentBadge environment={p.environment} /> : null}
                          </Inline>
                          <Text size="xs" variant="muted" className="font-mono">
                            {p.address} {p.database ? `· ${p.database}` : ""}
                          </Text>
                          <Inline gap="xs" align="center" justify="between" className="pt-1">
                            <Text size="xs" variant="muted">
                              {p.mtls ? "mTLS" : p.tls ? "TLS" : "Plaintext"}
                            </Text>
                            {isCurrent ? (
                              <Badge variant="success">Active</Badge>
                            ) : onRequestSwitchServer ? (
                              <Button
                                variant="outline"
                                size="sm"
                                onClick={() => {
                                  onClose();
                                  onRequestSwitchServer(p.id);
                                }}
                              >
                                Switch
                              </Button>
                            ) : null}
                          </Inline>
                        </Stack>
                      </CardBody>
                    </Card>
                  );
                })}
              </div>
            </Stack>
          ) : null}

          {/* Workspace Projects & Quick Actions */}
          <Stack gap="sm">
            <div>
              <Text size="sm" weight="semibold">Workspace Tools & Artifacts</Text>
              <Text size="xs" variant="muted">Explore database objects, snippets, and tools</Text>
            </div>

            <div className="nss-home-tools-grid">
              <Card variant="bordered" className="nss-home-tool-card">
                <CardBody className="p-3">
                  <Stack gap="xs">
                    <Inline gap="xs" align="center">
                      <Icon name="folder" size={16} />
                      <span className="font-medium text-sm">Saved Queries</span>
                    </Inline>
                    <Text size="xs" variant="muted">
                      {savedQueriesCount} saved snippets in local browser storage.
                    </Text>
                    <Button
                      variant="outline"
                      size="sm"
                      onClick={() => {
                        onClose();
                        onOpenSavedQueries();
                      }}
                    >
                      Open saved queries…
                    </Button>
                  </Stack>
                </CardBody>
              </Card>

              <Card variant="bordered" className="nss-home-tool-card">
                <CardBody className="p-3">
                  <Stack gap="xs">
                    <Inline gap="xs" align="center">
                      <Icon name="search" size={16} />
                      <span className="font-medium text-sm">Object Finder</span>
                    </Inline>
                    <Text size="xs" variant="muted">
                      Fast keyboard search across {tableCount} tables and workflows.
                    </Text>
                    <Button
                      variant="outline"
                      size="sm"
                      onClick={() => {
                        onClose();
                        onOpenSearchObjects();
                      }}
                    >
                      Search objects…
                    </Button>
                  </Stack>
                </CardBody>
              </Card>

              <Card variant="bordered" className="nss-home-tool-card">
                <CardBody className="p-3">
                  <Stack gap="xs">
                    <Inline gap="xs" align="center">
                      <Icon name="table" size={16} />
                      <span className="font-medium text-sm">Schema Designer</span>
                    </Inline>
                    <Text size="xs" variant="muted">
                      Visual native DDL table & index builder with live SQL preview.
                    </Text>
                    <Button
                      variant="outline"
                      size="sm"
                      onClick={() => {
                        onClose();
                        onOpenSchemaDesigner();
                      }}
                    >
                      Design schema…
                    </Button>
                  </Stack>
                </CardBody>
              </Card>

              <Card variant="bordered" className="nss-home-tool-card">
                <CardBody className="p-3">
                  <Stack gap="xs">
                    <Inline gap="xs" align="center">
                      <Icon name="download" size={16} />
                      <span className="font-medium text-sm">Data Import</span>
                    </Inline>
                    <Text size="xs" variant="muted">
                      Bounded CSV, TSV, JSON, and NDJSON batch insert generator.
                    </Text>
                    <Button
                      variant="outline"
                      size="sm"
                      onClick={() => {
                        onClose();
                        onOpenImport();
                      }}
                    >
                      Import data…
                    </Button>
                  </Stack>
                </CardBody>
              </Card>

              {onOpenStreamingImport ? (
                <Card variant="bordered" className="nss-home-tool-card">
                  <CardBody className="p-3">
                    <Stack gap="xs">
                      <Inline gap="xs" align="center">
                        <Icon name="download" size={16} />
                        <span className="font-medium text-sm">Bulk Import</span>
                      </Inline>
                      <Text size="xs" variant="muted">
                        Stream CSV, TSV, or NDJSON datasets directly into tables.
                      </Text>
                      <Button
                        variant="outline"
                        size="sm"
                        onClick={() => {
                          onClose();
                          onOpenStreamingImport();
                        }}
                      >
                        Bulk import…
                      </Button>
                    </Stack>
                  </CardBody>
                </Card>
              ) : null}

              <Card variant="bordered" className="nss-home-tool-card">
                <CardBody className="p-3">
                  <Stack gap="xs">
                    <Inline gap="xs" align="center">
                      <Icon name="layers" size={16} />
                      <span className="font-medium text-sm">ER Diagram</span>
                    </Inline>
                    <Text size="xs" variant="muted">
                      Dependency layer graph of foreign key relationships.
                    </Text>
                    <Button
                      variant="outline"
                      size="sm"
                      onClick={() => {
                        onClose();
                        onOpenSchemaDiagram();
                      }}
                    >
                      View diagram…
                    </Button>
                  </Stack>
                </CardBody>
              </Card>

              <Card variant="bordered" className="nss-home-tool-card">
                <CardBody className="p-3">
                  <Stack gap="xs">
                    <Inline gap="xs" align="center">
                      <Icon name="sparkles" size={16} />
                      <span className="font-medium text-sm">Data Generator</span>
                    </Inline>
                    <Text size="xs" variant="muted">
                      Deterministic seed-based synthetic sample row generator.
                    </Text>
                    <Button
                      variant="outline"
                      size="sm"
                      onClick={() => {
                        onClose();
                        onOpenDataGenerator();
                      }}
                    >
                      Generate data…
                    </Button>
                  </Stack>
                </CardBody>
              </Card>

              {onOpenSchemaDiff ? (
                <Card variant="bordered" className="nss-home-tool-card">
                  <CardBody className="p-3">
                    <Stack gap="xs">
                      <Inline gap="xs" align="center">
                        <Icon name="layers" size={16} />
                        <span className="font-medium text-sm">Schema Diff</span>
                      </Inline>
                      <Text size="xs" variant="muted">
                        Compare table schemas, inspect drift, and generate migration DDL.
                      </Text>
                      <Button
                        variant="outline"
                        size="sm"
                        onClick={() => {
                          onClose();
                          onOpenSchemaDiff();
                        }}
                      >
                        Compare schema…
                      </Button>
                    </Stack>
                  </CardBody>
                </Card>
              ) : null}

              {onOpenBenchmarkViewer ? (
                <Card variant="bordered" className="nss-home-tool-card">
                  <CardBody className="p-3">
                    <Stack gap="xs">
                      <Inline gap="xs" align="center">
                        <Icon name="activity" size={16} />
                        <span className="font-medium text-sm">Benchmarks</span>
                      </Inline>
                      <Text size="xs" variant="muted">
                        View nextsql-bench reports and compare latency, QPS, and recall across runs.
                      </Text>
                      <Button
                        variant="outline"
                        size="sm"
                        onClick={() => {
                          onClose();
                          onOpenBenchmarkViewer();
                        }}
                      >
                        View benchmarks…
                      </Button>
                    </Stack>
                  </CardBody>
                </Card>
              ) : null}
            </div>
          </Stack>
        </Stack>
      </ModalBody>
      <ModalFooter>
        <Button variant="outline" onClick={onClose}>Close</Button>
      </ModalFooter>
    </Modal>
  );
}
