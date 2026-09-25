import { Badge, Button, Card, CardBody, CardHeader, CardTitle, Inline, Stack, Text } from "@bzync/rui";
import { type SessionProfile, type StudioReadConsistency } from "../ops/api";
import { EnvironmentBadge } from "../shared/status";
import { Icon } from "../shared/icons";
import { formatRelativeTime, type RecentConnection } from "./resultTools";

// ConnectionExplorer renders the active session's connection details,
// operator-declared target server profiles, and recent connections history in
// the Studio sidebar. All state is strictly client-side / authorized
// session-bound; credentials are never handled here.
export function ConnectionExplorer({
  currentProfile,
  allProfiles,
  user,
  database,
  readConsistency,
  onRequestSwitchServer,
  recentConnections,
  onSelectRecent,
  onClearRecents,
  onOpenHome,
}: {
  currentProfile: SessionProfile;
  allProfiles: SessionProfile[];
  user: string;
  database: string;
  readConsistency: StudioReadConsistency;
  onRequestSwitchServer?: (profileId?: string) => void;
  recentConnections: RecentConnection[];
  onSelectRecent: (recent: RecentConnection) => void;
  onClearRecents: () => void;
  onOpenHome?: () => void;
}) {
  const otherProfiles = allProfiles.filter((p) => p.id !== currentProfile.id);

  return (
    <Stack gap="md" className="nss-conn-explorer">
      {/* Active connection card */}
      <Card variant="bordered" className="nss-conn-card">
        <CardHeader className="nss-conn-card-header">
          <CardTitle as="h3">
            <Inline gap="xs" align="center" wrap={false}>
              <span className="nss-conn-status-dot nss-conn-status-dot--connected" aria-hidden="true" />
              <span className="sr-only">Connected:</span>
              <span>{currentProfile.name}</span>
              {currentProfile.environment ? <EnvironmentBadge environment={currentProfile.environment} /> : null}
            </Inline>
          </CardTitle>
        </CardHeader>
        <CardBody className="nss-conn-card-body">
          <dl className="nss-conn-details">
            <div className="nss-conn-detail-row">
              <dt>Host</dt>
              <dd className="font-mono text-xs">{currentProfile.address || "127.0.0.1:7210"}</dd>
            </div>
            <div className="nss-conn-detail-row">
              <dt>Database</dt>
              <dd className="font-mono text-xs">{database || "default"}</dd>
            </div>
            <div className="nss-conn-detail-row">
              <dt>User</dt>
              <dd className="font-mono text-xs">{user}</dd>
            </div>
            <div className="nss-conn-detail-row">
              <dt>Security</dt>
              <dd>
                {currentProfile.mtls ? (
                  <Badge variant="success">mTLS</Badge>
                ) : currentProfile.tls ? (
                  <Badge variant="success">TLS</Badge>
                ) : (
                  <Badge variant="muted">Plaintext (loopback)</Badge>
                )}
              </dd>
            </div>
            <div className="nss-conn-detail-row">
              <dt>Consistency</dt>
              <dd>
                {readConsistency === "strong" ? (
                  <Badge variant="success">Strong reads</Badge>
                ) : (
                  <Badge variant="warning">{readConsistency} reads</Badge>
                )}
              </dd>
            </div>
          </dl>

          <Inline gap="xs" wrap className="nss-conn-card-actions">
            {onRequestSwitchServer ? (
              <Button
                variant="outline"
                size="sm"
                icon={<Icon name="plug" size={14} />}
                onClick={() => onRequestSwitchServer()}
              >
                Switch server…
              </Button>
            ) : null}
            {onOpenHome ? (
              <Button
                variant="ghost"
                size="sm"
                icon={<Icon name="overview" size={14} />}
                onClick={onOpenHome}
              >
                Home / Overview…
              </Button>
            ) : null}
          </Inline>
        </CardBody>
      </Card>

      {/* Other declared server profiles */}
      <Stack gap="xs" className="nss-conn-section">
        <Inline gap="xs" align="center" justify="between">
          <Text size="xs" weight="medium" variant="muted" className="uppercase tracking-wider">
            Available Profiles ({allProfiles.length})
          </Text>
        </Inline>

        {otherProfiles.length === 0 ? (
          <Text size="xs" variant="muted" className="nss-conn-empty">
            No other server profiles configured.
          </Text>
        ) : (
          <ul className="nss-conn-list" role="list" aria-label="Available server profiles">
            {otherProfiles.map((p) => (
              <li key={p.id} className="nss-conn-item">
                <div className="nss-conn-item-main">
                  <Inline gap="xs" align="center" wrap={false}>
                    <Icon name="server" size={14} />
                    <span className="nss-conn-item-name font-medium">{p.name}</span>
                    {p.environment ? <EnvironmentBadge environment={p.environment} /> : null}
                  </Inline>
                  <Text size="xs" variant="muted" className="font-mono">
                    {p.address} {p.database ? `· ${p.database}` : ""}
                  </Text>
                </div>
                {onRequestSwitchServer ? (
                  <Button
                    variant="outline"
                    size="sm"
                    onClick={() => onRequestSwitchServer(p.id)}
                    aria-label={`Switch to ${p.name}`}
                  >
                    Connect
                  </Button>
                ) : null}
              </li>
            ))}
          </ul>
        )}
      </Stack>

      {/* Recent connections */}
      <Stack gap="xs" className="nss-conn-section">
        <Inline gap="xs" align="center" justify="between">
          <Text size="xs" weight="medium" variant="muted" className="uppercase tracking-wider">
            Recent Connections ({recentConnections.length})
          </Text>
          {recentConnections.length > 0 ? (
            <Button variant="ghost" size="sm" onClick={onClearRecents}>
              Clear
            </Button>
          ) : null}
        </Inline>

        {recentConnections.length === 0 ? (
          <Text size="xs" variant="muted" className="nss-conn-empty">
            No recent connections recorded.
          </Text>
        ) : (
          <ul className="nss-conn-list" role="list" aria-label="Recent connections">
            {recentConnections.map((r) => (
              <li key={`${r.profileId}:${r.user}:${r.database}`} className="nss-conn-item">
                <div className="nss-conn-item-main">
                  <Inline gap="xs" align="center" wrap={false}>
                    <Icon name="clock" size={14} />
                    <span className="nss-conn-item-name font-medium">{r.name}</span>
                    {r.environment ? <EnvironmentBadge environment={r.environment} /> : null}
                  </Inline>
                  <Text size="xs" variant="muted">
                    <span className="font-mono">{r.user}@{r.database}</span>
                    {r.connectedAt ? ` · ${formatRelativeTime(r.connectedAt)}` : ""}
                  </Text>
                </div>
                <Button
                  variant="outline"
                  size="sm"
                  onClick={() => onSelectRecent(r)}
                  aria-label={`Connect to ${r.name} as ${r.user}`}
                >
                  Connect
                </Button>
              </li>
            ))}
          </ul>
        )}
      </Stack>
    </Stack>
  );
}
