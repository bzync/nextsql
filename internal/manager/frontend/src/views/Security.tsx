import { Alert, Badge, Card, CardBody, CardHeader, CardTitle, Heading, Inline, Stack, Text } from "@bzync/rui";
import { api } from "../api";
import { useReadModel } from "../useReadModel";
import { ResultTable } from "../ResultTable";
import { ViewFrame } from "./ViewFrame";
import type { ResultSet } from "../api";

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
  const { data, error, loading } = useReadModel(api.security, onUnauthorized);
  return (
    <ViewFrame loading={loading} error={error} warnings={data?.warnings}>
      {data ? (
        <>
          <Alert variant="info" title="Scope">
            Users, roles, grants, TLS status, key rotation status, and a
            recent audit-log tail with chain-verification status.
          </Alert>
          <TLSStatusCard tls={data.tls} />
          <AuditVerifyCard auditVerify={data.audit_verify} />
          <Stack gap="xs">
            <Heading as="h3" size="sm">Audit log</Heading>
            <ResultTable
              result={data.audit_log}
              empty="No audit log attached (embedded/CLI use), or nothing recorded yet"
            />
          </Stack>
          <Stack gap="xs">
            <Heading as="h3" size="sm">Key rotation</Heading>
            <ResultTable
              result={data.key_versions}
              empty="No envelope attached (embedded/CLI use, or a deployment with no persistent keystore file)"
            />
          </Stack>
          <Stack gap="xs">
            <Heading as="h3" size="sm">Users</Heading>
            <ResultTable result={data.users} empty="No users visible (requires cluster ADMIN, or none exist)" />
          </Stack>
          <Stack gap="xs">
            <Heading as="h3" size="sm">Roles</Heading>
            <ResultTable result={data.roles} empty="No roles created" />
          </Stack>
          <Stack gap="xs">
            <Heading as="h3" size="sm">Grants</Heading>
            <ResultTable result={data.grants} empty="No grants issued" />
          </Stack>
        </>
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
        <CardTitle as="h3">TLS</CardTitle>
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

// AuditVerifyCard turns system.audit_verify's single generic result row into
// a labeled fact sheet, same reasoning as TLSStatusCard: one status row of
// several columns reads better as labeled fields than a table.
function AuditVerifyCard({ auditVerify }: { auditVerify: ResultSet }) {
  const cols = auditVerify?.columns ?? [];
  const row = auditVerify?.rows?.[0] ?? [];
  const get = (name: string) => {
    const i = cols.indexOf(name);
    return i < 0 ? null : row[i];
  };
  const lines = Number(get("lines") ?? "0");
  const verified = get("verified") === "TRUE";
  const signaturesChecked = get("signatures_checked") === "TRUE";
  const problem = get("problem");

  return (
    <Card variant="bordered">
      <CardHeader>
        <CardTitle as="h3">Audit chain</CardTitle>
      </CardHeader>
      <CardBody>
        <Stack gap="sm">
          <Inline gap="sm" align="center" wrap>
            <Badge variant={problem ? "error" : lines > 0 ? (verified ? "success" : "error") : "muted"}>
              {problem && lines === 0
                ? "Audit log unreadable"
                : lines === 0
                  ? "No audit log attached"
                  : verified
                    ? "Chain verified"
                    : "Chain verification FAILED"}
            </Badge>
            {lines > 0 && get("signing_started") === "TRUE" ? (
              <Badge variant={signaturesChecked ? "info" : "warning"}>
                {signaturesChecked ? "Signatures checked" : "Signing enabled, not checked"}
              </Badge>
            ) : null}
          </Inline>
          {lines > 0 ? (
            <Text size="sm">
              <Text as="span" variant="muted">Lines: </Text>
              {get("lines")} (legacy {get("legacy_count")}, chained {get("chained_count")}, signed {get("signed_count")})
            </Text>
          ) : (
            <Text variant="muted" size="sm">
              Embedded/CLI use, or the connected node has no audit log configured.
            </Text>
          )}
          {problem ? (
            <Text size="sm" variant="danger">
              {lines > 0 ? `Line ${get("first_bad_line")}: ` : ""}{problem}
            </Text>
          ) : null}
        </Stack>
      </CardBody>
    </Card>
  );
}
