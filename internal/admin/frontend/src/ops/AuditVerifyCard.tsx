import { Badge, Card, CardBody, CardHeader, CardTitle, Inline, Stack, Text } from "@bzync/rui";
import type { ResultSet } from "./api";
import { Icon } from "../shared/icons";

// AuditVerifyCard turns system.audit_verify's single generic result row into
// a labeled fact sheet instead of a raw table. It is shared by Operations
// Security and Studio's read-only Audit viewer so both surfaces interpret the
// authoritative catalog row identically.
export function AuditVerifyCard({ auditVerify }: { auditVerify: ResultSet }) {
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
        <CardTitle as="h3">
          <Inline gap="xs" align="center" wrap={false}>
            <Icon name="shield" size={16} />
            Audit chain
          </Inline>
        </CardTitle>
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
            <Text size="sm" className="nsa-audit-verify-problem">
              {lines > 0 ? `Line ${get("first_bad_line")}: ` : ""}{problem}
            </Text>
          ) : null}
        </Stack>
      </CardBody>
    </Card>
  );
}
