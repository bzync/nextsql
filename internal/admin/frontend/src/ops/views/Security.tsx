import { useState } from "react";
import { Alert, Badge, Button, Card, CardBody, CardHeader, CardTitle, Inline, Stack, Tabs, TabsContent, TabsList, TabsTrigger, Text } from "@bzync/rui";
import { api } from "../api";
import { useReadModel } from "../useReadModel";
import { ResultTable } from "../ResultTable";
import { ViewFrame } from "./ViewFrame";
import type { ResultSet } from "../api";
import { AuditVerifyCard } from "../AuditVerifyCard";
import { Icon } from "../../shared/icons";
import { SecurityAdmin, namesFrom, columnsByTable } from "../SecurityAdmin";

// Security shows users/roles/grants from the durable auth.Store/security.ACL
// state (system.users/roles/grants), the live listener's redacted TLS status
// (system.tls), the attached envelope's redacted key rotation state
// (system.key_versions — current version and retained/revoked/retired
// counts per key, never key material), and a bounded, chain-verified tail
// of the audit log (system.audit_verify/system.audit_log). All seven tables
// are admin-only server-side — a non-admin operator sees empty tables here,
// not an error, matching the rest of system.*'s row-filter-on-RBAC
// convention. This closes M4's originally scoped surface.
export function Security({ onUnauthorized }: { onUnauthorized: () => void }) {
  const { data, error, loading, reload } = useReadModel(api.security, onUnauthorized);
  const [status, setStatus] = useState<string | null>(null);
  // Column names come from system.users/system.roles/system.grants as the
  // engine renders them. Object suggestions are only what already appears in
  // a grant — a datalist hint, never a constraint on what can be typed.
  const users = namesFrom(data?.users, "name");
  const roles = namesFrom(data?.roles, "role");
  const tables = namesFrom(data?.tables, "name");
  const columns = columnsByTable(data?.columns);
  const resourceGroups = namesFrom(data?.resource_groups, "name");
  return (
    <ViewFrame loading={loading} error={error} warnings={data?.warnings}>
      {data ? (
        <Stack gap="md">
          <Alert variant="info" title="Scope">
            Users, roles, grants, TLS status, key rotation status, and a
            recent audit-log tail with chain-verification status. Creating
            principals and changing grants runs as your own database user, so
            the server's RBAC decides what you may do here.
          </Alert>

          {status ? (
            <Alert variant="success" title="Applied">
              <Inline gap="sm" align="center" wrap>
                <Text size="sm">{status}</Text>
                <Button size="sm" variant="ghost" onClick={() => setStatus(null)}>Dismiss</Button>
              </Inline>
            </Alert>
          ) : null}

          <SecurityAdmin
            users={users}
            roles={roles}
            tables={tables}
            columns={columns}
            resourceGroups={resourceGroups}
            onDone={(message) => {
              setStatus(message);
              reload();
            }}
          />

          <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
            <TLSStatusCard tls={data.tls} />
            <AuditVerifyCard auditVerify={data.audit_verify} />
          </div>

          <Tabs defaultValue="users" className="mt-2">
            <TabsList className="mb-4">
              <TabsTrigger value="users">
                <Inline gap="xs" align="center" wrap={false}>
                  <Icon name="users" size={14} />
                  <span>Users</span>
                  <Badge variant="muted" size="sm">{data.users.rows.length}</Badge>
                </Inline>
              </TabsTrigger>
              <TabsTrigger value="roles">
                <Inline gap="xs" align="center" wrap={false}>
                  <Icon name="shield" size={14} />
                  <span>Roles</span>
                  <Badge variant="muted" size="sm">{data.roles.rows.length}</Badge>
                </Inline>
              </TabsTrigger>
              <TabsTrigger value="grants">
                <Inline gap="xs" align="center" wrap={false}>
                  <Icon name="lock" size={14} />
                  <span>Grants</span>
                  <Badge variant="muted" size="sm">{data.grants.rows.length}</Badge>
                </Inline>
              </TabsTrigger>
              <TabsTrigger value="audit">
                <Inline gap="xs" align="center" wrap={false}>
                  <Icon name="file" size={14} />
                  <span>Audit log</span>
                  <Badge variant="muted" size="sm">{data.audit_log.rows.length}</Badge>
                </Inline>
              </TabsTrigger>
              <TabsTrigger value="keys">
                <Inline gap="xs" align="center" wrap={false}>
                  <Icon name="key" size={14} />
                  <span>Key rotation</span>
                  <Badge variant="muted" size="sm">{data.key_versions.rows.length}</Badge>
                </Inline>
              </TabsTrigger>
            </TabsList>

            <TabsContent value="users">
              <ResultTable result={data.users} empty="No users visible (requires cluster ADMIN, or none exist)" label="Users" />
            </TabsContent>
            <TabsContent value="roles">
              <ResultTable result={data.roles} empty="No roles created" label="Roles" />
            </TabsContent>
            <TabsContent value="grants">
              <ResultTable result={data.grants} empty="No grants issued" label="Grants" />
            </TabsContent>
            <TabsContent value="audit">
              <ResultTable
                result={data.audit_log}
                empty="No audit log attached (embedded/CLI use), or nothing recorded yet"
                label="Audit log"
              />
            </TabsContent>
            <TabsContent value="keys">
              <ResultTable
                result={data.key_versions}
                empty="No envelope attached (embedded/CLI use, or a deployment with no persistent keystore file)"
                label="Key versions"
              />
            </TabsContent>
          </Tabs>
        </Stack>
      ) : null}
    </ViewFrame>
  );
}

// TLSStatusCard turns system.tls's single generic result row into a labeled
// fact sheet instead of a raw table — friendlier for a one-row status than
// ResultTable's column-header layout, same reasoning as Cluster's Badge.
function TLSStatusCard({ tls }: { tls: ResultSet }) {
  const cols = tls?.columns ?? [];
  const row = tls?.rows?.[0] ?? [];
  const get = (name: string) => {
    const i = cols.indexOf(name);
    return i < 0 ? null : row[i];
  };
  const enabled = get("enabled") === "TRUE";
  const daysStr = get("days_until_expiry");
  const days = daysStr === null ? null : Number(daysStr);
  const expiryVariant = days !== null && days < 14 ? "error" : days !== null && days < 30 ? "warning" : "success";

  return (
    <Card variant="bordered">
      <CardHeader>
        <CardTitle as="h3">
          <Inline gap="xs" align="center" wrap={false}>
            <Icon name="lock" size={16} />
            TLS
          </Inline>
        </CardTitle>
      </CardHeader>
      <CardBody>
        <Stack gap="sm">
          <Inline gap="sm" align="center">
            <Badge variant={enabled ? "success" : "muted"}>
              {enabled ? "TLS enabled" : "No TLS listener attached"}
            </Badge>
            {enabled && get("mtls_required") === "TRUE" ? <Badge variant="info">mTLS required</Badge> : null}
            {enabled && get("client_crl_configured") === "TRUE" ? <Badge variant="info">CRL configured</Badge> : null}
          </Inline>
          {enabled ? (
            <Stack gap="xs">
              <Text size="sm">
                <Text as="span" variant="muted">Subject: </Text>
                {get("subject") || "—"}
              </Text>
              <Text size="sm">
                <Text as="span" variant="muted">Issuer: </Text>
                {get("issuer") || "—"}
              </Text>
              <Text size="sm">
                <Text as="span" variant="muted">Valid: </Text>
                {get("not_before") || "—"} to {get("not_after") || "—"}
              </Text>
              <Inline gap="xs" align="center">
                <Text as="span" variant="muted" size="sm">Expires in: </Text>
                <Badge variant={expiryVariant}>{days !== null ? `${days} days` : "unknown"}</Badge>
              </Inline>
              <Text size="sm">
                <Text as="span" variant="muted">DNS names: </Text>
                {get("dns_names") || "—"}
              </Text>
            </Stack>
          ) : (
            <Text variant="muted" size="sm">
              Either this is a loopback plaintext deployment, or the connected node runs
              embedded/CLI without a listening server.
            </Text>
          )}
        </Stack>
      </CardBody>
    </Card>
  );
}
