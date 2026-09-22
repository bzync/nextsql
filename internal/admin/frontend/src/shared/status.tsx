import { Badge, type BadgeSize } from "@bzync/rui";

export type ConnectionState = "connected" | "disconnected" | "checking";

// Server reachability uses one badge everywhere it is labeled.
export function ConnectionBadge({
  state,
  title,
}: {
  state: ConnectionState;
  title?: string;
}) {
  if (state === "disconnected") {
    return <Badge variant="error" dot title={title}>Disconnected</Badge>;
  }
  if (state === "checking") {
    return <Badge variant="muted" dot title={title}>Checking…</Badge>;
  }
  return <Badge variant="success" dot title={title}>Connected</Badge>;
}

// production is the only environment that changes the operator's caution level.
export function EnvironmentBadge({
  environment,
  size = "sm",
}: {
  environment: string;
  size?: BadgeSize;
}) {
  return (
    <Badge variant={environment === "production" ? "warning" : "muted"} size={size}>
      {environment}
    </Badge>
  );
}

// Index rows from system.indexes: usable + status "valid" is healthy.
export function IndexHealthBadge({
  status,
  usable,
}: {
  status: string;
  usable: boolean;
}) {
  const healthy = usable && status.toLowerCase() === "valid";
  return <Badge variant={healthy ? "success" : "warning"}>{status}</Badge>;
}
