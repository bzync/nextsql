import { useEffect, useState } from "react";
import {
  Alert,
  Badge,
  Button,
  ConfirmDialog,
  Inline,
  Stat,
  Tabs,
  TabsContent,
  TabsList,
  TabsTrigger,
  Text,
} from "@bzync/rui";
import { api, type ActivityActionRequest } from "../api";
import { useReadModel } from "../useReadModel";
import { ResultTable } from "../ResultTable";
import { ViewFrame } from "./ViewFrame";
import { StatGrid } from "./Section";
import { Icon } from "../../shared/icons";
import { SegmentedControl } from "../../shared/SegmentedControl";

export function Activity({ onUnauthorized }: { onUnauthorized: () => void }) {
  const { data, error, loading, reload } = useReadModel(api.activity, onUnauthorized);
  const [pending, setPending] = useState<ActivityActionRequest | null>(null);
  const [busy, setBusy] = useState(false);
  const [actionError, setActionError] = useState<string | null>(null);
  const [autoRefresh, setAutoRefresh] = useState<number>(0);

  useEffect(() => {
    if (autoRefresh <= 0 || pending !== null) return;
    const timer = setInterval(() => {
      reload();
    }, autoRefresh * 1000);
    return () => clearInterval(timer);
  }, [autoRefresh, pending, reload]);

  const runAction = async () => {
    if (!pending) return;
    setBusy(true);
    setActionError(null);
    try {
      await api.activityAction(pending);
      setPending(null);
      reload();
    } catch (err: unknown) {
      setActionError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <ViewFrame loading={loading} error={error} warnings={data?.warnings}>
      {actionError ? (
        <Alert variant="error" title="Action failed" role="alert" className="mb-4">
          <Inline gap="sm" align="center" justify="between" wrap>
            <span>{actionError}</span>
            <Button size="sm" variant="ghost" onClick={() => setActionError(null)}>Dismiss</Button>
          </Inline>
        </Alert>
      ) : null}
      {data ? (
        <>
          <StatGrid>
            <Stat icon={<Icon name="users" />} label="Sessions" value={String(data.sessions.rows.length)} />
            <Stat icon={<Icon name="activity" />} label="Active queries" value={String(data.active_queries.rows.length)} />
            <Stat icon={<Icon name="clock" />} label="Transactions" value={String(data.transactions.rows.length)} />
            <Stat icon={<Icon name="lock" />} label="Locks" value={String(data.locks.rows.length)} />
          </StatGrid>

          <Inline gap="sm" align="center" justify="between" wrap className="mt-4 mb-2">
            <Inline gap="xs" align="center">
              <Text as="span" size="sm" variant="muted">Auto-refresh:</Text>
              <SegmentedControl
                label="Activity auto-refresh interval"
                value={String(autoRefresh)}
                onChange={(val) => setAutoRefresh(Number(val))}
                options={[
                  { label: "Off", value: "0" },
                  { label: "2s", value: "2" },
                  { label: "5s", value: "5" },
                  { label: "10s", value: "10" },
                ]}
              />
            </Inline>
            <Button
              size="sm"
              variant="outline"
              icon={<Icon name="refresh" size={13} />}
              onClick={reload}
              disabled={loading}
            >
              Refresh now
            </Button>
          </Inline>

          <Tabs defaultValue="sessions" className="mt-2">
            <TabsList className="mb-4">
              <TabsTrigger value="sessions">
                <Inline gap="xs" align="center" wrap={false}>
                  <Icon name="users" size={14} />
                  <span>Sessions</span>
                  <Badge variant="muted" size="sm">{data.sessions.rows.length}</Badge>
                </Inline>
              </TabsTrigger>
              <TabsTrigger value="queries">
                <Inline gap="xs" align="center" wrap={false}>
                  <Icon name="activity" size={14} />
                  <span>Active queries</span>
                  <Badge variant={data.active_queries.rows.length ? "warning" : "muted"} size="sm">
                    {data.active_queries.rows.length}
                  </Badge>
                </Inline>
              </TabsTrigger>
              <TabsTrigger value="transactions">
                <Inline gap="xs" align="center" wrap={false}>
                  <Icon name="clock" size={14} />
                  <span>Transactions</span>
                  <Badge variant="muted" size="sm">{data.transactions.rows.length}</Badge>
                </Inline>
              </TabsTrigger>
              <TabsTrigger value="locks">
                <Inline gap="xs" align="center" wrap={false}>
                  <Icon name="lock" size={14} />
                  <span>Locks</span>
                  <Badge variant={data.locks.rows.length ? "warning" : "muted"} size="sm">
                    {data.locks.rows.length}
                  </Badge>
                </Inline>
              </TabsTrigger>
            </TabsList>

            <TabsContent value="sessions">
              <ResultTable
                result={data.sessions}
                label="Sessions"
                actions={{
                  header: "Action",
                  render: (row) => (
                    <Button
                      size="sm"
                      variant="destructive"
                      onClick={() => row[0] && setPending({ action: "kill_session", id: String(row[0]) })}
                      title={`Terminate session ${row[0]}`}
                    >
                      Terminate
                    </Button>
                  ),
                }}
              />
            </TabsContent>
            <TabsContent value="queries">
              <ResultTable
                result={data.active_queries}
                empty="No queries running"
                label="Active queries"
                actions={{
                  header: "Action",
                  render: (row) => (
                    <Button
                      size="sm"
                      variant="outline"
                      onClick={() => row[0] && setPending({ action: "cancel_query", id: String(row[0]) })}
                      title={`Cancel query ${row[0]}`}
                    >
                      Cancel
                    </Button>
                  ),
                }}
              />
            </TabsContent>
            <TabsContent value="transactions">
              <ResultTable result={data.transactions} empty="No open transactions" label="Open transactions" />
            </TabsContent>
            <TabsContent value="locks">
              <ResultTable result={data.locks} empty="No locks held" label="Held locks" />
            </TabsContent>
          </Tabs>
        </>
      ) : null}
      {pending ? (
        <ConfirmDialog
          open
          onClose={() => setPending(null)}
          onConfirm={runAction}
          title={pending.action === "kill_session" ? `Terminate session ${pending.id}?` : `Cancel query ${pending.id}?`}
          description={
            pending.action === "kill_session"
              ? `Terminates connection ${pending.id}, rolling back any uncommitted transactions and releasing held locks.`
              : `Cancels running query ${pending.id} without terminating the client connection.`
          }
          confirmLabel={pending.action === "kill_session" ? "Terminate session" : "Cancel query"}
          destructive={pending.action === "kill_session"}
          loading={busy}
        />
      ) : null}
    </ViewFrame>
  );
}
